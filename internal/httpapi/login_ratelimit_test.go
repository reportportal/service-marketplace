package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Without a trusted proxy, client-supplied address headers must not choose
// the login rate-limit key: a fresh value per request would otherwise lift
// the limit.
func TestLoginRateLimitIgnoresClientAddressHeaders(t *testing.T) {
	for _, header := range []string{"True-Client-IP", "X-Real-IP", "X-Forwarded-For"} {
		t.Run(header, func(t *testing.T) {
			env := newTestEnv(t)
			var last int
			for i := range 6 {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
					strings.NewReader(`{"username":"admin","password":"wrong"}`))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = "203.0.113.7:4444"
				req.Header.Set(header, fmt.Sprintf("198.51.100.%d", i+1))
				last = env.do(req).Code
			}
			if last != http.StatusTooManyRequests {
				t.Fatalf("sixth wrong password: status %d, want 429", last)
			}
		})
	}
}
