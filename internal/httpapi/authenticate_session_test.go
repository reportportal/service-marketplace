package httpapi

import (
	"net/http"
	"testing"
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
