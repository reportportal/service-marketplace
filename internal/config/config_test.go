package config

import (
	"strings"
	"testing"
	"time"
)

// validEnv is the minimum set that gets Load() past validateSecrets, so a test
// can isolate the one variable it is actually about.
func validEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"STORAGE_SIGNING_SECRET": "storage-signing-secret-long-enough-for-validation",
		"JWT_SECRET":             "jwt-secret-long-enough-for-validation-0000000000",
		"ADMIN_PASSWORD_HASH":    "admin-password-hash-long-enough-for-validation00",
	} {
		t.Setenv(k, v)
	}
}

// TestLoad_MalformedSecurityConfigFailsStartup pins that a security-relevant
// setting with an unparseable value stops the process instead of silently
// standing in the default.
//
// Both cases below were reproducible before this change:
//   - ADMIN_LOGIN_ENABLED="flase" parsed as false-with-error, and getEnvBool
//     returned its default of TRUE -- an operator disabling password login got
//     it left enabled, with no error and nothing logged. That is fail-open on
//     an authentication control.
//   - TRUSTED_PROXY_HOPS="one" fell back to 0. Behind a TLS-terminating
//     ingress, isHTTPS (internal/httpapi/errors.go) then stops trusting
//     X-Forwarded-Proto, so BuildSessionCookie omits Secure and the session
//     cookie is transmitted over plaintext.
//
// The default is the right value when a variable is UNSET. It is the wrong
// value when the operator set it and got the spelling wrong, because then it
// silently contradicts stated intent.
//
// Mutation this kills: reverting any of the getEnv*E helpers to swallow the
// parse error and return the default.
func TestLoad_MalformedSecurityConfigFailsStartup(t *testing.T) {
	for _, tc := range []struct {
		key, bad string
	}{
		{"ADMIN_LOGIN_ENABLED", "flase"},
		{"TRUSTED_PROXY_HOPS", "one"},
		{"ALLOW_INSECURE_DEFAULTS", "yes-please"},
		{"JWT_TTL_SECONDS", "3600s"},
		{"ORPHAN_CLEANUP_INTERVAL", "5 minutes"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			validEnv(t)
			t.Setenv(tc.key, tc.bad)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() with %s=%q returned no error -- a malformed security-relevant "+
					"value must fail startup, not silently substitute the default", tc.key, tc.bad)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("Load() error = %q, want it to name %s so the operator can find the typo",
					err, tc.key)
			}
		})
	}
}

// TestLoad_UnsetKeysStillTakeDefaults is the other side: failing closed on a
// malformed value must not turn an UNSET variable into a startup failure.
// Defaults are the documented behaviour for absent configuration.
func TestLoad_UnsetKeysStillTakeDefaults(t *testing.T) {
	validEnv(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() with no optional keys set: %v", err)
	}
	if !c.AdminLoginEnabled {
		t.Errorf("AdminLoginEnabled = false, want true (documented default)")
	}
	if c.TrustedProxyHops != 0 {
		t.Errorf("TrustedProxyHops = %d, want 0 (documented default)", c.TrustedProxyHops)
	}
	if c.AllowInsecureDefaults {
		t.Errorf("AllowInsecureDefaults = true, want false (documented default)")
	}
	if c.JWTTTLSeconds != 3600 {
		t.Errorf("JWTTTLSeconds = %d, want 3600 (documented default)", c.JWTTTLSeconds)
	}
	if c.OrphanCleanupInterval != 5*time.Minute {
		t.Errorf("OrphanCleanupInterval = %v, want 5m (documented default)", c.OrphanCleanupInterval)
	}
	if c.StorageType != StorageLocal {
		t.Errorf("StorageType = %q, want %q (documented default)", c.StorageType, StorageLocal)
	}
}

// TestLoad_WellFormedValuesAreHonoured guards against the lazy fix of making
// every parse failure fatal by never parsing anything: the values operators do
// set correctly must still take effect.
func TestLoad_WellFormedValuesAreHonoured(t *testing.T) {
	validEnv(t)
	t.Setenv("ADMIN_LOGIN_ENABLED", "false")
	t.Setenv("TRUSTED_PROXY_HOPS", "1")
	t.Setenv("JWT_TTL_SECONDS", "600")
	t.Setenv("ORPHAN_CLEANUP_INTERVAL", "90s")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if c.AdminLoginEnabled {
		t.Errorf("AdminLoginEnabled = true, want false")
	}
	if c.TrustedProxyHops != 1 {
		t.Errorf("TrustedProxyHops = %d, want 1", c.TrustedProxyHops)
	}
	if c.JWTTTLSeconds != 600 {
		t.Errorf("JWTTTLSeconds = %d, want 600", c.JWTTTLSeconds)
	}
	if c.OrphanCleanupInterval != 90*time.Second {
		t.Errorf("OrphanCleanupInterval = %v, want 90s", c.OrphanCleanupInterval)
	}
}

// TestLoad_NegativeTrustedProxyHopsIsRejected: the old code clamped a negative
// value to 0 silently. Clamping is the same silent-contradiction problem --
// -1 is not something an operator means.
func TestLoad_NegativeTrustedProxyHopsIsRejected(t *testing.T) {
	validEnv(t)
	t.Setenv("TRUSTED_PROXY_HOPS", "-1")

	if _, err := Load(); err == nil {
		t.Fatalf("Load() with TRUSTED_PROXY_HOPS=-1 returned no error, want a rejection")
	}
}

// TestLoad_CDNBaseURLSuffixWarning: a CDN_BASE_URL that does not end in /cdn
// is how dev ended up handing out bucket URLs (403 for every download) while
// /ready stayed green. It is a warning, not a failure, because a CDN host that
// forwards to /cdn is a legitimate topology; that one opts out explicitly.
func TestLoad_CDNBaseURLSuffixWarning(t *testing.T) {
	for _, tc := range []struct {
		name, base, optOut string
		wantWarning        bool
	}{
		{"default base", "", "", false},
		{"cdn suffix", "https://marketplace.example/cdn", "", false},
		{"cdn suffix trailing slash", "https://marketplace.example/cdn/", "", false},
		{"bare host", "https://dev.marketplace.example", "", true},
		{"other path", "https://marketplace.example/cdn-assets", "", true},
		{"bare host opted out", "https://cdn.marketplace.example", "false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validEnv(t)
			if tc.base != "" {
				t.Setenv("CDN_BASE_URL", tc.base)
			}
			if tc.optOut != "" {
				t.Setenv("CDN_BASE_URL_SUFFIX_CHECK", tc.optOut)
			}
			c, err := Load()
			if err != nil {
				t.Fatalf("Load() must not fail over CDN_BASE_URL: %v", err)
			}
			warnings := strings.Join(c.Warnings(), "\n")
			if got := warnings != ""; got != tc.wantWarning {
				t.Fatalf("warning present = %v, want %v (%q)", got, tc.wantWarning, warnings)
			}
			if tc.wantWarning && !strings.Contains(warnings, "signed") {
				t.Fatalf("warning should explain the signed-URL requirement: %q", warnings)
			}
		})
	}
}

func TestLoad_CDNSelfCheckFlag(t *testing.T) {
	validEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !c.CDNSelfCheck {
		t.Errorf("CDNSelfCheck = false, want true (documented default)")
	}

	t.Setenv("CDN_SELF_CHECK", "false")
	if c, err = Load(); err != nil || c.CDNSelfCheck {
		t.Fatalf("CDN_SELF_CHECK=false: CDNSelfCheck=%v err=%v", c != nil && c.CDNSelfCheck, err)
	}

	t.Setenv("CDN_SELF_CHECK", "nope")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "CDN_SELF_CHECK") {
		t.Fatalf("malformed CDN_SELF_CHECK must fail naming the key, got %v", err)
	}
}

func TestLoad_GitLabOIDC(t *testing.T) {
	validEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if c.PublishOIDCGitLabIssuer != "" || c.PublishOIDCGitLabJWKSURL != "" {
		t.Fatalf("gitlab OIDC must stay disabled by default, got issuer %q jwks %q", c.PublishOIDCGitLabIssuer, c.PublishOIDCGitLabJWKSURL)
	}

	t.Setenv("PUBLISH_OIDC_AUDIENCE", "marketplace.reportportal.io")
	t.Setenv("PUBLISH_OIDC_GITLAB_ISSUER", "https://git.epam.com/")
	t.Setenv("PUBLISH_OIDC_GITLAB_JWKS_URL", "https://git.epam.com/oauth/discovery/keys")
	c, err = Load()
	if err != nil {
		t.Fatalf("Load() with gitlab issuer: %v", err)
	}
	if c.PublishOIDCGitLabIssuer != "https://git.epam.com" {
		t.Fatalf("issuer = %q, want trailing slash trimmed", c.PublishOIDCGitLabIssuer)
	}
	if c.PublishOIDCGitLabJWKSURL != "https://git.epam.com/oauth/discovery/keys" {
		t.Fatalf("jwks = %q", c.PublishOIDCGitLabJWKSURL)
	}

	t.Setenv("PUBLISH_OIDC_GITLAB_ISSUER", "https://token.actions.githubusercontent.com")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "PUBLISH_OIDC_GITLAB_ISSUER") {
		t.Fatalf("github issuer reused as gitlab must fail, got %v", err)
	}

	t.Setenv("PUBLISH_OIDC_GITLAB_ISSUER", "http://git.epam.com")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "PUBLISH_OIDC_GITLAB_ISSUER") {
		t.Fatalf("non-https issuer must fail, got %v", err)
	}
}
