package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/config"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// internal/auth/shared_state_test.go already proves the multi-replica OAuth
// state and admin-lockout claims at the auth package level, by constructing
// two independent auth.OAuthStateStore / auth.AdminAuthenticator instances
// sharing one storage.ObjectStore. The tests in this file prove the same
// claims one layer up, through the real HTTP router and handlers, on two
// independently-built *Server values -- not the same *Server (or the same
// underlying *auth.GitHubOAuth / *auth.AdminAuthenticator) invoked twice,
// which would trivially "share state" because it's literally the same Go
// object regardless of whether the shared-store design works at all.
// newReplicaServer below deliberately re-derives every auth dependency from
// scratch per call, the way two separate replica processes each doing their
// own config.Load()-driven wiring against the same backing store would.

// fakeGitHubAPI stands in for network access to the real GitHub API,
// keyed by request path, so the OAuth callback test below can drive a
// *complete* successful login (token exchange + org membership check) with
// no real network call -- deterministic and fast, and critically able to
// produce an unambiguous *success* signal (302 + a session cookie) rather
// than a generic failure. A generic "the downstream call failed" stub
// would produce the same 401 "GitHub authentication failed" response
// whether or not the cross-replica state hand-off itself worked, which
// very nearly shipped as this test's assertion -- verified by hand
// (temporarily reverted OAuthStateStore.Issue's store write, not
// committed): with only a network-failure stub, that mutation left this
// test green, because "GitHub authentication failed" was the response
// either way (ConsumeState rejecting the state, or the stubbed token
// exchange failing after ConsumeState succeeded, are indistinguishable
// through that one generic message).
type fakeGitHubAPI struct{}

func (fakeGitHubAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	var payload string
	switch req.URL.Path {
	case "/login/oauth/access_token":
		payload = `{"access_token":"test-access-token"}`
	case "/user/memberships/orgs/reportportal":
		payload = `{"state":"active"}`
	case "/user":
		payload = `{"login":"octocat"}`
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(payload))),
		Header:     make(http.Header),
	}, nil
}

// newReplicaServer builds a fully-wired *Server backed by store, with its
// own freshly-constructed SessionManager/AdminAuthenticator/GitHubOAuth --
// the same dependency graph newTestEnv builds, minus the storage-dependent
// services (Catalogue/Publish/Lifecycle/License) this file's tests don't
// exercise. AllowedTeam is deliberately left unset so VerifyMembership
// stops after the org-membership check, keeping fakeGitHubAPI's canned
// responses minimal.
func newReplicaServer(t *testing.T, store storage.ObjectStore, adminPasswordHash string) *Server {
	t.Helper()
	sessions := auth.NewSessionManager(testSessionSecret, testIssuer, 3600, auth.NewDenylist(store))
	admin := auth.NewAdminAuthenticator(true, "admin", adminPasswordHash, store)
	gh := &auth.GitHubOAuth{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Org:          "reportportal",
		RedirectURL:  "https://marketplace.test/api/v1/auth/github/callback",
		Sessions:     sessions,
		States:       auth.NewOAuthStateStore(store),
		HTTPClient:   &http.Client{Transport: fakeGitHubAPI{}},
	}
	cfg := &config.Config{TrustedProxyHops: 0}
	return NewServer(Deps{Config: cfg, Sessions: sessions, AdminAuth: admin, GitHub: gh})
}

// TestOAuthLoginStateSurvivesAcrossTwoServerInstances is the HTTP-level
// regression test for finding F4-inmemory-state-not-shared-across-replicas'
// OAuth-state half: a state issued by GET /auth/github/login on one
// replica must be accepted by GET /auth/github/callback landing on a
// *different* replica, since a load balancer with no sticky sessions gives
// no guarantee the two requests hit the same process.
func TestOAuthLoginStateSurvivesAcrossTwoServerInstances(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "test-signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("s3cret-operator-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	replicaA := newReplicaServer(t, store, string(passwordHash))
	replicaB := newReplicaServer(t, store, string(passwordHash))

	loginRec := httptest.NewRecorder()
	replicaA.Handler().ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/login", nil))
	if loginRec.Code != http.StatusFound {
		t.Fatalf("expected 302 from GET .../auth/github/login on replica A, got %d body=%s", loginRec.Code, loginRec.Body.String())
	}
	var stateCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == auth.OAuthStateCookie {
			stateCookie = c
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatalf("expected replica A to set a non-empty %s cookie; got cookies=%v", auth.OAuthStateCookie, loginRec.Result().Cookies())
	}

	// The callback lands on replica B -- a different process in production,
	// a different *Server (and a different *auth.GitHubOAuth /
	// *auth.OAuthStateStore) here -- carrying the state issued by replica A.
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?code=dummy-code&state="+stateCookie.Value, nil)
	callbackReq.AddCookie(stateCookie)
	callbackRec := httptest.NewRecorder()
	replicaB.Handler().ServeHTTP(callbackRec, callbackReq)

	// A full, unambiguous success (302 redirect + a new session cookie) can
	// only happen if replica B's ConsumeState accepted the state replica A
	// issued -- there is no other way through GitHubOAuth.Callback to reach
	// the redirect. Any failure along the way (state rejected, exchange
	// rejected, membership rejected) instead produces a 401 with the same
	// generic message, so this is deliberately checked as one clear
	// pass/fail signal rather than by inspecting the failure message.
	if callbackRec.Code != http.StatusFound {
		t.Fatalf("expected replica B to accept a state issued by replica A and redirect (302) -- the two replicas are not actually sharing OAuth state through the store; got %d body=%s", callbackRec.Code, callbackRec.Body.String())
	}
	sessionSet := false
	for _, c := range callbackRec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			sessionSet = true
		}
	}
	if !sessionSet {
		t.Fatalf("expected replica B to set the %s cookie after a successful cross-replica callback; got cookies=%v", auth.SessionCookieName, callbackRec.Result().Cookies())
	}
}

// TestAdminLoginLockoutSharedAcrossTwoServerInstances is the HTTP-level
// regression test for finding F4-inmemory-state-not-shared-across-replicas'
// admin-lockout half: an attacker round-robined by a load balancer across
// two replicas must still be limited to loginLockoutMaxFailures(5) attempts
// total for a given client+username, not 5 per replica.
func TestAdminLoginLockoutSharedAcrossTwoServerInstances(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "test-signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("s3cret-operator-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	replicaA := newReplicaServer(t, store, string(passwordHash))
	replicaB := newReplicaServer(t, store, string(passwordHash))
	replicas := []*Server{replicaA, replicaB}

	doLogin := func(srv *Server, password string) *httptest.ResponseRecorder {
		t.Helper()
		reqBody := `{"username":"admin","password":"` + password + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		// Fixed RemoteAddr so both replicas compute the same lockout key
		// (clientIP(r, hops) + "|" + username) for the same simulated caller.
		req.RemoteAddr = "203.0.113.222:54321"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// 5 failed attempts, alternating replica, must exhaust the shared
	// allowance regardless of how many distinct replica processes see them.
	for i := 0; i < 5; i++ {
		rec := doLogin(replicas[i%2], "wrong-password")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d (replica %d): expected 401 (ordinary bad credentials), got %d body=%s", i+1, i%2, rec.Code, rec.Body.String())
		}
	}

	// The 6th attempt, on the replica that *didn't* see the 5th failure,
	// must already be locked out -- a per-process counter would allow this
	// (5 more fresh attempts on that replica) instead.
	if rec := doLogin(replicaA, "wrong-password"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected replica A locked out after 5 failures split across two replicas, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doLogin(replicaB, "wrong-password"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected replica B locked out too (shared lockout), got %d body=%s", rec.Code, rec.Body.String())
	}
}
