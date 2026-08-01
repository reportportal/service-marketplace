package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/auth"
)

// TestAuthenticateSessionRejectedCredentialReturns401 is the dedicated
// regression test for finding F2-invalid-session-returns-500-not-401
// (assessment: RECURS): an expired, malformed, or revoked operator session
// credential (bearer or cookie) made every requireSession-protected route
// answer 500 INTERNAL_ERROR instead of 401.
//
// Root cause: auth.SessionManager.Verify (internal/auth/session.go) returns
// the plain package-level sentinel auth.ErrUnauthorized — built with
// errors.New, not *httpapi.APIError — for a parse failure, a bad signature,
// an expired exp, a wrong typ claim, or a denylisted jti alike.
// authenticateSession (internal/httpapi/router.go) used to return that
// sentinel straight through to requireSession's writeError(w, err) call.
// writeError's only special case is errors.As(err, &apiErr) against
// *APIError (internal/httpapi/errors.go); a bare sentinel never matches
// that, so it fell to the generic branch and emitted 500 INTERNAL_ERROR.
//
// This is a documented status-code contract, not an inferred one:
// docs/openapi/service-marketplace-v1.yaml declares 401 Unauthorized on
// every operator-session-protected operation, and
// AMD-13-session-jwt-lifetime (requirements/AMENDMENTS-v1.md) is the
// governing amendment for the operator-session case specifically: "any
// operator request with an expired token returns 401" (mirrored as an AC on
// US-MRKT-OPR-003: "A token presented 61 minutes after issuance is rejected
// with 401 on every operator POST/PATCH/DELETE route"). authenticateSession
// is the single chokepoint both requireSession and the cookie-fallback
// branch of requireSessionOrOIDC call through, so mapping every
// non-*APIError verification failure to 401 UNAUTHORIZED here satisfies
// AMD-13 for every session-gated route in one place.
//
// NOTE for WS-AUTHZ (owns internal/httpapi, internal/auth going forward):
// this mapping already ships — do not re-implement it.
func TestAuthenticateSessionRejectedCredentialReturns401(t *testing.T) {
	tests := []struct {
		name string
		cred credential
	}{
		{"expired session: valid signature, exp in the past", credExpiredSession},
		{"revoked session: valid signature, jti denylisted", credRevokedSession},
		{"malformed bearer token: not a JWT at all", credMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			// POST /api/v1/auth/logout is requireSession-gated and needs no
			// other fixture state, keeping this test isolated from the
			// route-authorization matrix in router_authz_test.go.
			req := env.newRequest(http.MethodPost, "/api/v1/auth/logout", tt.cred, nil, "")
			rec := env.do(req)

			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("a rejected-but-present session credential produced 500 INTERNAL_ERROR (finding F2-invalid-session-returns-500-not-401 regression); body=%s", rec.Body.String())
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 per AMD-13 / the OpenAPI Unauthorized response, got %d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Code != CodeUnauthorized {
				t.Fatalf("expected error code %q per AMD-13, got %q", CodeUnauthorized, body.Code)
			}
		})
	}
}

// TestAuthenticateSessionRejectedCookieCredentialReturns401 is the
// cookie-carried half of the F2/AMD-13 taxonomy that
// TestAuthenticateSessionRejectedCredentialReturns401 above does not
// exercise: testutil_test.go's newRequest/credential helpers only ever set
// the credential as a bearer Authorization header (documented reason: the
// cookie path additionally requires an XSRF double-submit token, a
// separate concern from authorization). authenticateSession
// (internal/httpapi/router.go) reads the bearer header first and falls
// back to the mp_operator_session cookie when it's absent, mapping the
// same three rejection modes through the same code path either way — but
// nothing asserted that end to end until this test. Confirms WS-AUTHZ's
// task instruction to "finish the taxonomy properly": both credential
// carriers, not just bearer.
//
// This test alone cannot prove the cookie is actually read, though: an
// absent Authorization header *and* an absent cookie ("no credential at
// all") fall through authenticateSession's separate bearer=="" branch to
// the exact same 401 UNAUTHORIZED / "Invalid or missing bearer token"
// response these three cases expect. A prior review caught this directly:
// deleting the cookie-fallback block outright (so authenticateSession only
// ever reads the bearer header) left this test green, because a
// cookie-carried rejected credential and no credential whatsoever are
// indistinguishable from the response alone. See
// TestAuthenticateSessionAcceptsValidSessionCookie below for the
// positive-path assertion that actually closes the gap: it fails outright
// under that exact mutation, where this test does not.
func TestAuthenticateSessionRejectedCookieCredentialReturns401(t *testing.T) {
	tests := []struct {
		name  string
		token func(e *testEnv) string
	}{
		{"expired session cookie: valid signature, exp in the past", func(e *testEnv) string { return e.expiredSessionToken() }},
		{"revoked session cookie: valid signature, jti denylisted", func(e *testEnv) string { return e.revokedSessionToken() }},
		{"malformed session cookie: not a JWT at all", func(e *testEnv) string { return "not-a-jwt-at-all" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tt.token(env)})
			rec := env.do(req)

			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("a rejected-but-present session cookie produced 500 INTERNAL_ERROR (finding F2-invalid-session-returns-500-not-401 regression); body=%s", rec.Body.String())
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 per AMD-13 / the OpenAPI Unauthorized response, got %d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Code != CodeUnauthorized {
				t.Fatalf("expected error code %q per AMD-13, got %q", CodeUnauthorized, body.Code)
			}
		})
	}
}

// TestAuthenticateSessionAcceptsValidSessionCookie is the positive-path
// proof that authenticateSession's cookie-fallback branch is actually
// reached, which TestAuthenticateSessionRejectedCookieCredentialReturns401
// above cannot provide on its own (see that test's doc comment): a valid,
// unexpired, non-revoked session token carried *only* as the
// mp_operator_session cookie -- no Authorization header at all -- must
// authenticate successfully on a requireSession-gated route.
//
// GET /api/v1/licenses is deliberately chosen over POST /auth/logout: it is
// requireSession-gated but a safe method, so requireSession's XSRF
// double-submit check (gated on isUnsafe(r.Method) && hasSessionCookie(r))
// never engages, keeping this a pure test of the cookie-fallback branch of
// authenticateSession, not of CSRF handling.
//
// If the cookie fallback is ever deleted (authenticateSession reads only
// the bearer header), this request has no Authorization header either, so
// it falls to the "missing credential" branch and gets 401 UNAUTHORIZED
// instead of 200 -- unlike the rejection test above, this fails outright
// under that mutation.
func TestAuthenticateSessionAcceptsValidSessionCookie(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/licenses", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: env.operatorSessionToken()})
	rec := env.do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a valid session carried only as the %s cookie (cookie-fallback branch of authenticateSession), got %d body=%s", auth.SessionCookieName, rec.Code, rec.Body.String())
	}
}
