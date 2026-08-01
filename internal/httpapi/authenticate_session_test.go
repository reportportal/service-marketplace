package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/auth"
)

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
			req := env.newRequest(http.MethodPost, "/api/v1/auth/logout", tt.cred, nil, "")
			rec := env.do(req)

			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("rejected session credential returned 500; body=%s", rec.Body.String())
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Code != CodeUnauthorized {
				t.Fatalf("expected %q, got %q", CodeUnauthorized, body.Code)
			}
		})
	}
}

func TestAuthenticateSessionRejectedCookieCredentialReturns401(t *testing.T) {
	tests := []struct {
		name  string
		token func(e *testEnv) string
	}{
		{"expired session cookie", func(e *testEnv) string { return e.expiredSessionToken() }},
		{"revoked session cookie", func(e *testEnv) string { return e.revokedSessionToken() }},
		{"malformed session cookie", func(e *testEnv) string { return "not-a-jwt-at-all" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tt.token(env)})
			rec := env.do(req)

			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("rejected session cookie returned 500; body=%s", rec.Body.String())
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Code != CodeUnauthorized {
				t.Fatalf("expected %q, got %q", CodeUnauthorized, body.Code)
			}
		})
	}
}

// Cookie-only auth must work: rejection tests alone cannot prove the cookie fallback runs.
func TestAuthenticateSessionAcceptsValidSessionCookie(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/licenses", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: env.operatorSessionToken()})
	rec := env.do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for cookie-only session, got %d body=%s", rec.Code, rec.Body.String())
	}
}
