package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// routePolicy names the credential-gate a route is wired with in router.go.
type routePolicy int

const (
	// policyPublic: no operator credential is required; every credential
	// kind (including none) must reach the handler.
	policyPublic routePolicy = iota
	// policySessionOnly: s.requireSession — only a valid, non-expired,
	// non-revoked operator session reaches the handler. Used for the small
	// set of operator routes AMD-02 does not name (currently only
	// POST /api/v1/auth/logout): an OIDC token is just an unrecognized
	// bearer value here and gets the generic 401, not the token-type-aware
	// 403 — see policySessionOnlyRejectOIDC for the routes AMD-02 does
	// govern.
	policySessionOnly
	// policyOIDCVersionScoped: s.requireSessionOrPublishOIDC on
	// POST /api/v1/plugins/{pluginId}/versions — the one route the adopted
	// decision (AMD-02/AMD-15) designates for OIDC publish tokens, scoped to
	// the token's allow-listed plugin id matching the URL pluginId. A valid
	// OIDC token whose repo claim has no allow-list entry at all is refused
	// with 403 regardless of whether the target plugin exists (AMD-15).
	policyOIDCVersionScoped
	// policySessionOnlyRejectOIDC: s.requireSessionRejectOIDC — every
	// operator route AMD-02-oidc-token-scope names as scoped OUT of GitHub
	// OIDC tokens: POST /api/v1/plugins, PATCH /api/v1/plugins/{pluginId},
	// DELETE /api/v1/plugins/{pluginId},
	// POST .../versions/{version}/block,
	// POST .../versions/{version}/advisory, and every /api/v1/licenses/*
	// operation. These routes accept operator session JWTs ONLY. A GitHub
	// Actions OIDC bearer token is a recognized credential type, just not
	// one these routes accept, so it is refused with 403
	// TOKEN_TYPE_NOT_PERMITTED (not the generic 401 an absent or garbage
	// credential gets) — regardless of whether the token's repo claim is
	// allow-listed for some plugin (AMD-02's "regardless of allow-list
	// membership").
	policySessionOnlyRejectOIDC
)

// routeCase is one row of the authorization matrix: a route the router
// actually serves (verified against the live router by
// TestRouteInventoryIsFullyClassified) plus a concrete URL to exercise it
// and the credential policy it is expected to enforce.
type routeCase struct {
	method  string
	pattern string // as chi.Walk reports it, e.g. "/api/v1/plugins/{pluginId}"
	target  string // concrete request path
	policy  routePolicy
}

// authzMatrix is the hand-authored source of truth for what each route's
// credential gate is supposed to be. TestRouteInventoryIsFullyClassified
// diffs it against the live router so a route added without a row here (or
// a row left behind for a route that no longer exists) fails the suite.
var authzMatrix = []routeCase{
	{http.MethodGet, "/api/v1/auth/config", "/api/v1/auth/config", policyPublic},
	{http.MethodGet, "/api/v1/auth/github/callback", "/api/v1/auth/github/callback", policyPublic},
	{http.MethodGet, "/api/v1/auth/github/login", "/api/v1/auth/github/login", policyPublic},
	{http.MethodPost, "/api/v1/auth/login", "/api/v1/auth/login", policyPublic},
	{http.MethodPost, "/api/v1/auth/logout", "/api/v1/auth/logout", policySessionOnly},

	{http.MethodGet, "/api/v1/licenses", "/api/v1/licenses", policySessionOnlyRejectOIDC},
	{http.MethodPost, "/api/v1/licenses", "/api/v1/licenses", policySessionOnlyRejectOIDC},
	{http.MethodDelete, "/api/v1/licenses/{customerId}", "/api/v1/licenses/cust-1", policySessionOnlyRejectOIDC},
	{http.MethodPost, "/api/v1/licenses/{customerId}/keys", "/api/v1/licenses/cust-1/keys", policySessionOnlyRejectOIDC},
	{http.MethodDelete, "/api/v1/licenses/{customerId}/keys/{keyId}", "/api/v1/licenses/cust-1/keys/a1b2c3d4", policySessionOnlyRejectOIDC},

	{http.MethodGet, "/api/v1/plugins", "/api/v1/plugins", policyPublic},
	{http.MethodPost, "/api/v1/plugins", "/api/v1/plugins", policySessionOnlyRejectOIDC},
	{http.MethodGet, "/api/v1/plugins/{pluginId}", "/api/v1/plugins/" + testOIDCPluginID, policyPublic},
	{http.MethodPatch, "/api/v1/plugins/{pluginId}", "/api/v1/plugins/" + testOIDCPluginID, policySessionOnlyRejectOIDC},
	{http.MethodDelete, "/api/v1/plugins/{pluginId}", "/api/v1/plugins/" + testOIDCPluginID, policySessionOnlyRejectOIDC},
	{http.MethodGet, "/api/v1/plugins/{pluginId}/versions", "/api/v1/plugins/" + testOIDCPluginID + "/versions", policyPublic},
	{http.MethodPost, "/api/v1/plugins/{pluginId}/versions", "/api/v1/plugins/" + testOIDCPluginID + "/versions", policyOIDCVersionScoped},
	{http.MethodGet, "/api/v1/plugins/{pluginId}/versions/{version}", "/api/v1/plugins/" + testOIDCPluginID + "/versions/1.0.0", policyPublic},
	{http.MethodPost, "/api/v1/plugins/{pluginId}/versions/{version}/advisory", "/api/v1/plugins/" + testOIDCPluginID + "/versions/1.0.0/advisory", policySessionOnlyRejectOIDC},
	{http.MethodGet, "/api/v1/plugins/{pluginId}/versions/{version}/artifact", "/api/v1/plugins/" + testOIDCPluginID + "/versions/1.0.0/artifact", policyPublic},
	{http.MethodPost, "/api/v1/plugins/{pluginId}/versions/{version}/block", "/api/v1/plugins/" + testOIDCPluginID + "/versions/1.0.0/block", policySessionOnlyRejectOIDC},
}

// routePrefixesOutsideAPI lists every non-/api/v1 route surface the router
// serves today, and why it is deliberately excluded from the operator/OIDC
// credential matrix above:
//   - /health, /ready: unauthenticated liveness/readiness probes by design.
//   - /cdn/*: gated by the storage layer's own signed-URL scheme
//     (LocalStore.VerifySignedURL), not by operator session or OIDC — a
//     different credential family entirely.
//   - /operator, /operator/*: static asset serving for the operator SPA; the
//     SPA itself calls back into the /api/v1 routes above, which are the
//     actual enforcement point.
var routePrefixesOutsideAPI = map[string]bool{
	"/health": true, "/ready": true, "/cdn/*": true,
	"/operator": true, "/operator/*": true,
}

// TestRouteInventoryIsFullyClassified walks the REAL router (not a
// hand-maintained list of what we think it serves) and fails if any route it
// finds has no row in authzMatrix, or if authzMatrix has a row for a route
// the router no longer serves. This is what makes the matrix load-bearing:
// a new route added to router.go without a corresponding authzMatrix row
// fails this test, rather than shipping unclassified.
func TestRouteInventoryIsFullyClassified(t *testing.T) {
	env := newTestEnv(t)
	mux, ok := env.Server.Handler().(chi.Router)
	if !ok {
		t.Fatalf("Server.Handler() is not a chi.Router: %T", env.Server.Handler())
	}

	want := map[string]bool{}
	for _, rc := range authzMatrix {
		want[rc.method+" "+rc.pattern] = true
	}
	seen := map[string]bool{}

	err := chi.Walk(mux, func(method, route string, handler http.Handler, mws ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1") {
			if !routePrefixesOutsideAPI[route] {
				t.Errorf("router serves %s %s outside /api/v1 and outside the documented exclusion list; classify it in authzMatrix or add it to routePrefixesOutsideAPI with a reason", method, route)
			}
			return nil
		}
		key := method + " " + route
		seen[key] = true
		if !want[key] {
			t.Errorf("router serves %s with no row in authzMatrix — add one before merging", key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("authzMatrix has a row %q for a route the router no longer serves — remove it", k)
		}
	}
}

// TestRouteAuthorizationMatrix drives every row of authzMatrix against the
// real router with each relevant credential kind, asserting who is let
// through and who is refused.
func TestRouteAuthorizationMatrix(t *testing.T) {
	for _, rc := range authzMatrix {
		rc := rc
		t.Run(rc.method+" "+rc.pattern, func(t *testing.T) {
			env := newTestEnv(t)
			switch rc.policy {
			case policyPublic:
				assertPublicRoute(t, env, rc)
			case policySessionOnly:
				assertSessionOnlyRoute(t, env, rc)
			case policyOIDCVersionScoped:
				assertOIDCVersionScopedRoute(t, env, rc)
			case policySessionOnlyRejectOIDC:
				assertSessionOnlyRejectOIDCRoute(t, env, rc)
			default:
				t.Fatalf("unhandled routePolicy %v", rc.policy)
			}
		})
	}
}

// operatorAuthMessage is the literal message requireSession/
// requireSessionOrOIDC emit when a credential is refused at the door
// (router.go: CodeUnauthorized, "Invalid or missing bearer token"). Matching
// on it lets these assertions tell "blocked at the credential gate" apart
// from a handler-level 401 for an unrelated reason (e.g. the GitHub OAuth
// callback's own "Invalid OAuth state").
const operatorAuthMessage = "Invalid or missing bearer token"

func allCredentials() []credential {
	return []credential{credNone, credOperatorSession, credExpiredSession, credRevokedSession, credOIDCPublish, credOIDCOtherPlugin, credOIDCUnmappedRepo}
}

func assertPublicRoute(t *testing.T, env *testEnv, rc routeCase) {
	t.Helper()
	for _, cred := range allCredentials() {
		req := env.newRequest(rc.method, rc.target, cred, nil, "")
		rec := env.do(req)
		if rec.Code == http.StatusUnauthorized {
			body := decodeErrorEnvelope(t, rec)
			if body.Message == operatorAuthMessage {
				t.Fatalf("%s %s: public route was blocked at the operator-credential gate for %s; body=%s", rc.method, rc.target, cred, rec.Body.String())
			}
		}
	}
}

// assertSessionOnlyRoute covers the small set of operator routes AMD-02
// does not name (currently only POST /api/v1/auth/logout): an OIDC bearer
// token is simply not a valid session credential here and gets the same
// generic 401 as any other unrecognized bearer value. Contrast
// assertSessionOnlyRejectOIDCRoute below, which asserts the
// AMD-02-specific 403 TOKEN_TYPE_NOT_PERMITTED behavior for the routes the
// amendment actually governs.
func assertSessionOnlyRoute(t *testing.T, env *testEnv, rc routeCase) {
	t.Helper()
	for _, cred := range []credential{credNone, credExpiredSession, credRevokedSession, credOIDCPublish, credOIDCOtherPlugin} {
		req := env.newRequest(rc.method, rc.target, cred, nil, "")
		rec := env.do(req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s with %s: expected 401 (session-only route), got %d body=%s", rc.method, rc.target, cred, rec.Code, rec.Body.String())
		}
		body := decodeErrorEnvelope(t, rec)
		if body.Message != operatorAuthMessage {
			t.Fatalf("%s %s with %s: expected the operator-auth-required message, got %q", rc.method, rc.target, cred, body.Message)
		}
	}
	req := env.newRequest(rc.method, rc.target, credOperatorSession, nil, "")
	rec := env.do(req)
	if rec.Code == http.StatusUnauthorized {
		body := decodeErrorEnvelope(t, rec)
		if body.Message == operatorAuthMessage {
			t.Fatalf("%s %s: a valid operator session was refused at the credential gate; body=%s", rc.method, rc.target, rec.Body.String())
		}
	}
}

func assertOIDCVersionScopedRoute(t *testing.T, env *testEnv, rc routeCase) {
	t.Helper()
	for _, cred := range []credential{credNone, credExpiredSession, credRevokedSession} {
		req := env.newRequest(rc.method, rc.target, cred, nil, "")
		rec := env.do(req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s with %s: expected 401, got %d body=%s", rc.method, rc.target, cred, rec.Code, rec.Body.String())
		}
	}
	// A valid operator session may always publish a version.
	req := env.newRequest(rc.method, rc.target, credOperatorSession, nil, "")
	rec := env.do(req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("%s %s: a valid operator session was refused; body=%s", rc.method, rc.target, rec.Body.String())
	}
	// An OIDC token allow-listed for THIS URL's pluginId reaches the handler
	// (AMD-02/AMD-15's sanctioned path).
	req = env.newRequest(rc.method, rc.target, credOIDCPublish, nil, "")
	rec = env.do(req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("%s %s: an OIDC token allow-listed for this plugin was refused; body=%s", rc.method, rc.target, rec.Body.String())
	}
	// An OIDC token allow-listed for a DIFFERENT pluginId reaches the
	// middleware (it is a valid OIDC token) but is rejected by
	// handlePublishVersion's own plugin-id check — 403, not 401.
	req = env.newRequest(rc.method, rc.target, credOIDCOtherPlugin, nil, "")
	rec = env.do(req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("%s %s: OIDC token scoped to a different pluginId should get 403, got %d body=%s", rc.method, rc.target, rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Code != CodeForbidden {
		t.Fatalf("%s %s: expected error code %q, got %q", rc.method, rc.target, CodeForbidden, body.Code)
	}
	// An otherwise-valid OIDC token whose repo claim has no allow-list entry
	// at all is refused with 403 too — AMD-15's "non-allow-listed callers ...
	// receive 403 regardless of whether the plugin exists", not the generic
	// 401 a garbage or absent credential gets.
	req = env.newRequest(rc.method, rc.target, credOIDCUnmappedRepo, nil, "")
	rec = env.do(req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("%s %s: OIDC token with no allow-list entry should get 403, got %d body=%s", rc.method, rc.target, rec.Code, rec.Body.String())
	}
	body = decodeErrorEnvelope(t, rec)
	if body.Code != CodeForbidden {
		t.Fatalf("%s %s: expected error code %q, got %q", rc.method, rc.target, CodeForbidden, body.Code)
	}
}

// assertSessionOnlyRejectOIDCRoute covers every route policySessionOnlyRejectOIDC
// names: POST /api/v1/plugins, PATCH /api/v1/plugins/{pluginId},
// DELETE /api/v1/plugins/{pluginId}, POST .../versions/{version}/block,
// POST .../versions/{version}/advisory, and every /api/v1/licenses/* route.
// Per the adopted product decision, "OIDC tokens are accepted ONLY on the
// version-publish route" (AMD-02): a GitHub-issuer bearer token on any of
// these routes must get 403 TOKEN_TYPE_NOT_PERMITTED, regardless of
// allow-list membership — distinct from the generic 401 an absent, expired,
// or garbage credential gets, because the OIDC token itself is a
// recognized, valid credential; it is simply the wrong type for these
// routes.
//
// This closes finding F1 (go-assessment.json) for POST /api/v1/plugins:
// router.go used to wire api.With(s.requireSessionOrOIDC(true)).Post(
// "/plugins", ...) with a `publishOnly bool` parameter that was never read
// in the function body, so an allow-listed OIDC token reached
// handlePublishFirst here exactly as it did on the version-publish route,
// and could create a brand-new plugin at TierOfficial with no operator
// involved.
//
// It also closes the second half of AMD-02 for the remaining lifecycle and
// license routes: those previously used plain s.requireSession, under which
// a GitHub-issuer token was indistinguishable from a garbage credential
// (generic 401) instead of being refused as a recognized-but-wrong-type
// credential (403 TOKEN_TYPE_NOT_PERMITTED). Every route in this policy is
// now gated by s.requireSessionRejectOIDC, a single-purpose middleware with
// no boolean to silently ignore.
func assertSessionOnlyRejectOIDCRoute(t *testing.T, env *testEnv, rc routeCase) {
	t.Helper()
	for _, cred := range []credential{credNone, credExpiredSession, credRevokedSession} {
		req := env.newRequest(rc.method, rc.target, cred, nil, "")
		rec := env.do(req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s with %s: expected 401, got %d body=%s", rc.method, rc.target, cred, rec.Code, rec.Body.String())
		}
	}
	req := env.newRequest(rc.method, rc.target, credOperatorSession, nil, "")
	rec := env.do(req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("%s %s: a valid operator session was refused; body=%s", rc.method, rc.target, rec.Body.String())
	}

	t.Run("oidc_token_must_be_refused_per_AMD-02_AMD-15", func(t *testing.T) {
		for _, cred := range []credential{credOIDCPublish, credOIDCOtherPlugin, credOIDCUnmappedRepo} {
			req := env.newRequest(rc.method, rc.target, cred, nil, "")
			rec := env.do(req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with %s: AMD-02/AMD-15 requires OIDC tokens to be refused with 403 here, got %d body=%s", rc.method, rc.target, cred, rec.Code, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Code != CodeTokenTypeNotPermitted {
				t.Fatalf("%s %s with %s: expected error code %q, got %q", rc.method, rc.target, cred, CodeTokenTypeNotPermitted, body.Code)
			}
		}
	})
}
