package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"golang.org/x/crypto/bcrypt"

	"github.com/reportportal/service-marketplace/internal/analytics"
	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/config"
	"github.com/reportportal/service-marketplace/internal/license"
	"github.com/reportportal/service-marketplace/internal/lifecycle"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// Fixed test-environment constants. The session secret must be >=32 bytes to
// match production validation (see internal/config.minSecretLen), though
// these tests build *config.Config by hand rather than via config.Load so
// that value is not itself enforced here.
const (
	testSessionSecret = "test-session-secret-32-bytes-ok"
	testIssuer        = "marketplace-test"
	testOIDCAudience  = "marketplace-test-audience"
	testOIDCRepo      = "reportportal/plugin-x-repo"
	testOIDCPluginID  = "plugin-x"
	testOtherOIDCRepo = "reportportal/plugin-other-repo"
	testOtherPluginID = "plugin-other"
)

// testEnv bundles a fully-wired Server (the real router, the real
// middleware chain) with the pieces a test needs to mint credentials and
// seed storage state directly.
type testEnv struct {
	t        *testing.T
	Server   *Server
	Store    *storage.LocalStore
	Sessions *auth.SessionManager
	oidcKey  jwk.Key
}

// newTestEnv wires the same dependency graph cmd/marketplace/main.go builds,
// backed by a temp-dir LocalStore, so tests exercise the production router
// and middleware, not a stand-in.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "test-signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	denylist := auth.NewDenylist(store)
	sessions := auth.NewSessionManager(testSessionSecret, testIssuer, 3600, denylist)

	oidcKey := generateRSAJWK(t)
	pub, err := oidcKey.PublicKey()
	if err != nil {
		t.Fatalf("oidc public key: %v", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		t.Fatalf("add oidc public key: %v", err)
	}
	oidc := &auth.PublishOIDCVerifier{
		Audience: testOIDCAudience,
		AllowedSources: map[string]string{
			testOIDCRepo:      testOIDCPluginID,
			testOtherOIDCRepo: testOtherPluginID,
		},
		KeySet: set,
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("s3cret-operator-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	admin := auth.NewAdminAuthenticator(true, "admin", string(passwordHash))
	gh := &auth.GitHubOAuth{Sessions: sessions, States: auth.NewOAuthStateStore()}
	ga := &analytics.GA4Client{}
	invalidator := cdn.NoopInvalidator{}

	pubSvc := &publish.Service{Store: store, Invalidator: invalidator}
	cat := &catalogue.Service{Store: store}
	lc := &lifecycle.Service{Store: store, Invalidator: invalidator, Publisher: pubSvc}
	lic := &license.Service{Store: store}
	cfg := &config.Config{TrustedProxyHops: 0}

	srv := NewServer(Deps{
		Config:     cfg,
		Store:      store,
		LocalStore: store,
		Catalogue:  cat,
		Publish:    pubSvc,
		Lifecycle:  lc,
		License:    lic,
		Analytics:  ga,
		Sessions:   sessions,
		AdminAuth:  admin,
		GitHub:     gh,
		OIDC:       oidc,
	})

	return &testEnv{t: t, Server: srv, Store: store, Sessions: sessions, oidcKey: oidcKey}
}

// serverWithStore returns a fresh Server sharing every dependency with
// e.Server except Deps.Store, which is replaced with store. Used to wire a
// storagetest.FaultStore in for a single test without re-deriving the rest
// of the dependency graph.
func (e *testEnv) serverWithStore(store storage.ObjectStore) *Server {
	d := e.Server.deps
	d.Store = store
	return NewServer(d)
}

func generateRSAJWK(t *testing.T) jwk.Key {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	key, err := jwk.FromRaw(priv)
	if err != nil {
		t.Fatalf("jwk.FromRaw: %v", err)
	}
	_ = key.Set(jwk.KeyIDKey, "test-key")
	_ = key.Set(jwk.AlgorithmKey, jwa.RS256)
	return key
}

// --- credential minting ------------------------------------------------

// seedPlugin writes a minimal plugin.json + manifest.json + jar directly
// into storage, bypassing the publish pipeline, so lifecycle/read tests have
// a plugin to act on without going through PublishFirst.
func (e *testEnv) seedPlugin(pluginID, version string) {
	e.t.Helper()
	ctx := context.Background()
	m := map[string]any{
		"id": pluginID, "name": pluginID, "version": version, "description": "d",
		"author": map[string]string{"name": "A"}, "license": "Apache-2.0",
		"category": "other", "compatibility": map[string]string{"reportportal": ">=25.1"},
		"access": "public",
	}
	mb, _ := json.Marshal(m)
	if _, err := e.Store.Write(ctx, storage.VersionManifestPath(pluginID, version), mb, 0); err != nil {
		e.t.Fatalf("seed manifest: %v", err)
	}
	st := map[string]any{
		"id": pluginID, "tier": "official", "latestVersion": version,
		"versions": []map[string]any{{"version": version, "publishedAt": time.Now().UTC(), "sha256": "deadbeef"}},
	}
	sb, _ := json.Marshal(st)
	if _, err := e.Store.Write(ctx, storage.PluginStatePath(pluginID), sb, 0); err != nil {
		e.t.Fatalf("seed plugin state: %v", err)
	}
}

func (e *testEnv) operatorSessionToken() string {
	e.t.Helper()
	tok, _, err := e.Sessions.Issue(context.Background(), "operator@example.com")
	if err != nil {
		e.t.Fatalf("issue session: %v", err)
	}
	return tok
}

func (e *testEnv) revokedSessionToken() string {
	e.t.Helper()
	tok, exp, err := e.Sessions.Issue(context.Background(), "operator@example.com")
	if err != nil {
		e.t.Fatalf("issue session: %v", err)
	}
	claims, err := e.Sessions.Verify(context.Background(), tok)
	if err != nil {
		e.t.Fatalf("verify freshly issued session: %v", err)
	}
	e.Sessions.Revoke(context.Background(), claims.JTI, exp)
	return tok
}

// expiredSessionToken hand-builds a token with the same signing key/issuer/typ
// claim SessionManager.Issue uses, but with an expiration in the past —
// SessionManager can't issue one directly since Issue always sets a future exp.
func (e *testEnv) expiredSessionToken() string {
	e.t.Helper()
	built, err := jwt.NewBuilder().
		Issuer(testIssuer).
		Subject("operator@example.com").
		JwtID("expired-test-jti").
		IssuedAt(time.Now().Add(-2 * time.Hour)).
		Expiration(time.Now().Add(-time.Hour)).
		Claim("typ", "session").
		Build()
	if err != nil {
		e.t.Fatalf("build expired token: %v", err)
	}
	signed, err := jwt.Sign(built, jwt.WithKey(jwa.HS256, []byte(testSessionSecret)))
	if err != nil {
		e.t.Fatalf("sign expired token: %v", err)
	}
	return string(signed)
}

// oidcToken mints a GitHub-Actions-shaped OIDC token for repo, matching the
// AllowedSources mapping wired into newTestEnv's PublishOIDCVerifier.
func (e *testEnv) oidcToken(repo string) string {
	e.t.Helper()
	built, err := jwt.NewBuilder().
		Issuer("https://token.actions.githubusercontent.com").
		Subject("repo:"+repo+":ref:refs/heads/main").
		Audience([]string{testOIDCAudience}).
		Claim("repository", repo).
		IssuedAt(time.Now()).
		Expiration(time.Now().Add(10*time.Minute)).
		Build()
	if err != nil {
		e.t.Fatalf("build oidc token: %v", err)
	}
	signed, err := jwt.Sign(built, jwt.WithKey(jwa.RS256, e.oidcKey))
	if err != nil {
		e.t.Fatalf("sign oidc token: %v", err)
	}
	return string(signed)
}

// --- credential kinds & request building --------------------------------

// credential names every distinct kind of caller the route-authorization
// matrix distinguishes.
type credential int

const (
	credNone credential = iota
	credOperatorSession
	credExpiredSession
	credRevokedSession
	credOIDCPublish     // OIDC token allow-listed for testOIDCPluginID
	credOIDCOtherPlugin // OIDC token allow-listed for a *different* plugin id
	credMalformed       // not a JWT at all — garbage bearer value
)

func (c credential) String() string {
	switch c {
	case credNone:
		return "no-credential"
	case credOperatorSession:
		return "operator-session"
	case credExpiredSession:
		return "expired-session"
	case credRevokedSession:
		return "revoked-session"
	case credOIDCPublish:
		return "oidc-publish-token"
	case credOIDCOtherPlugin:
		return "oidc-token-other-plugin"
	case credMalformed:
		return "malformed-bearer-token"
	default:
		return "unknown-credential"
	}
}

// newRequest builds a request carrying the given credential as a bearer
// token. Operator sessions are exercised as bearer tokens (not the session
// cookie) throughout the matrix, which keeps it a pure authorization check —
// the cookie path additionally requires XSRF double-submit, a separate
// concern already enforced by requireSession's CSRF check.
func (e *testEnv) newRequest(method, target string, cred credential, body []byte, contentType string) *http.Request {
	e.t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	switch cred {
	case credNone:
	case credOperatorSession:
		r.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	case credExpiredSession:
		r.Header.Set("Authorization", "Bearer "+e.expiredSessionToken())
	case credRevokedSession:
		r.Header.Set("Authorization", "Bearer "+e.revokedSessionToken())
	case credOIDCPublish:
		r.Header.Set("Authorization", "Bearer "+e.oidcToken(testOIDCRepo))
	case credOIDCOtherPlugin:
		r.Header.Set("Authorization", "Bearer "+e.oidcToken(testOtherOIDCRepo))
	case credMalformed:
		r.Header.Set("Authorization", "Bearer not-a-jwt-at-all")
	}
	return r
}

func (e *testEnv) do(req *http.Request) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.Server.Handler().ServeHTTP(rec, req)
	return rec
}

// --- response / schema assertions ---------------------------------------

// openAPIErrorCodes mirrors components.schemas.ErrorResponse.code.enum in
// docs/openapi/service-marketplace-v1.yaml. There is no YAML dependency in
// this module (see go.mod — deliberately kept minimal), so this enum is
// hand-kept in sync with the spec rather than loaded from it; a mismatch
// here is exactly the kind of drift a future contract test should close by
// parsing the spec directly once a YAML/JSON-schema dependency is justified.
var openAPIErrorCodes = map[ErrorCode]bool{
	"NOT_FOUND": true, "UNAUTHORIZED": true, "FORBIDDEN": true, "CONFLICT": true,
	"GONE": true, "VALIDATION_ERROR": true, "SERVICE_UNAVAILABLE": true,
	"TOO_MANY_REQUESTS": true, "PAYLOAD_TOO_LARGE": true, "BAD_REQUEST": true,
	"METHOD_NOT_ALLOWED": true, "UNSUPPORTED_MEDIA_TYPE": true, "NOT_ACCEPTABLE": true,
	"INTERNAL_ERROR": true, "STORED_DOCUMENT_UNREADABLE": true, "SIGNING_UNAVAILABLE": true,
	"STORAGE_CONFLICT": true, "STORAGE_UNAVAILABLE": true, "CSRF_TOKEN_INVALID": true,
}

// decodeErrorEnvelope decodes rec's body as an ErrorResponse and checks it
// against the OpenAPI ErrorResponse schema (required code+message, code
// drawn from the documented enum) before returning it.
func decodeErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error envelope is not valid JSON: %v\nbody=%s", err, rec.Body.String())
	}
	assertOpenAPIErrorSchema(t, body)
	return body
}

func assertOpenAPIErrorSchema(t *testing.T, body ErrorResponse) {
	t.Helper()
	if body.Code == "" {
		t.Fatalf("error envelope missing required field \"code\" per OpenAPI ErrorResponse schema")
	}
	if body.Message == "" {
		t.Fatalf("error envelope missing required field \"message\" per OpenAPI ErrorResponse schema")
	}
	if !openAPIErrorCodes[body.Code] {
		t.Fatalf("error code %q is not in the OpenAPI ErrorResponse.code enum (docs/openapi/service-marketplace-v1.yaml)", body.Code)
	}
}
