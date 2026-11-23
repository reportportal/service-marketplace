package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIsHTTPSGatesForwardedProtoBehindTrustedProxyHops is the regression
// test for assessment finding F5-isHTTPS-trusts-unvalidated-header:
// isHTTPS() honored X-Forwarded-Proto unconditionally, unlike clientIP()
// (internal/httpapi/errors.go), which only trusts X-Forwarded-For once
// TrustedProxyHops is configured. An unauthenticated caller could therefore
// assert "https" on a connection the deployment never saw as TLS, and
// isHTTPS's return value gates the Secure attribute on the session, XSRF,
// and OAuth-state cookies (internal/auth/session.go BuildSessionCookie /
// BuildXSRFCookie / BuildOAuthStateCookie) — so a forged header could cause
// the server to omit Secure from a cookie that must have it, or fake a
// value that has it, depending on direction of the spoof.
func TestIsHTTPSGatesForwardedProtoBehindTrustedProxyHops(t *testing.T) {
	tests := []struct {
		name             string
		tls              bool
		forwardedProto   string
		trustedProxyHops int
		want             bool
	}{
		{"direct TLS termination, no header", true, "", 0, true},
		{"direct TLS termination, hostile header ignored", true, "http", 0, true},
		{"plain HTTP, no trusted proxy, spoofed https header must not be honored", false, "https", 0, false},
		{"plain HTTP, no header at all", false, "", 1, false},
		{"plain HTTP, trusted proxy configured, header honored", false, "https", 1, true},
		{"plain HTTP, trusted proxy configured, header says http", false, "http", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.forwardedProto != "" {
				r.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			got := isHTTPS(r, tt.trustedProxyHops)
			if got != tt.want {
				t.Fatalf("isHTTPS() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestForgedForwardedProtoDoesNotMarkCookieSecureWithoutTrustedProxy drives
// the real router end to end: a plain-HTTP request with a spoofed
// X-Forwarded-Proto: https header must not receive a Secure-flagged cookie
// when TrustedProxyHops is 0 (the newTestEnv default), since the deployment
// never terminated TLS for this hop.
func TestForgedForwardedProtoDoesNotMarkCookieSecureWithoutTrustedProxy(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := env.do(req)

	found := false
	for _, sc := range rec.Result().Header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "XSRF-TOKEN=") {
			found = true
			if strings.Contains(sc, "Secure") {
				t.Fatalf("XSRF cookie was marked Secure from a forged X-Forwarded-Proto header with TrustedProxyHops=0: %s", sc)
			}
		}
	}
	if !found {
		t.Fatalf("expected ensureXSRF to set an XSRF-TOKEN cookie, got Set-Cookie headers: %v", rec.Result().Header.Values("Set-Cookie"))
	}
}

func TestClientIPBehindTrustedProxies(t *testing.T) {
	tests := []struct {
		name string
		hops int
		xff  []string
		want string
	}{
		{"no proxy trusted, header ignored", 0, []string{"198.51.100.1"}, "203.0.113.7"},
		{"one hop, client-supplied prefix ignored", 1, []string{"6.6.6.6, 198.51.100.1"}, "198.51.100.1"},
		{"one hop, proxy adds its own header line", 1, []string{"6.6.6.6", "198.51.100.1"}, "198.51.100.1"},
		{"two hops", 2, []string{"6.6.6.6, 198.51.100.1, 10.0.0.9"}, "198.51.100.1"},
		{"fewer entries than hops falls back to the peer", 2, []string{"6.6.6.6"}, "203.0.113.7"},
		{"entry that is not an IP falls back to the peer", 1, []string{"not-an-ip"}, "203.0.113.7"},
		{"entry with a port falls back to the peer", 1, []string{"198.51.100.1:55"}, "203.0.113.7"},
		{"no header falls back to the peer", 1, nil, "203.0.113.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.RemoteAddr = "203.0.113.7:4444"
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIP(r, tt.hops); got != tt.want {
				t.Fatalf("clientIP = %q, want %q", got, tt.want)
			}
		})
	}
}
