package httpapi

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
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

// A login through a proxy with TRUSTED_PROXY_HOPS=0 is logged once: every
// client then shares the proxy's address as its rate-limit key.
func TestLoginWarnsOnceAboutUntrustedProxy(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	env := newTestEnv(t)
	login := func(xff string) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"admin","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		env.do(req)
	}
	login("")
	if buf.Len() != 0 {
		t.Fatalf("warned without a proxy: %q", buf.String())
	}
	login("198.51.100.1")
	login("198.51.100.2")
	if n := strings.Count(buf.String(), "TRUSTED_PROXY_HOPS=0"); n != 1 {
		t.Fatalf("warnings %d, want 1: %q", n, buf.String())
	}
}
