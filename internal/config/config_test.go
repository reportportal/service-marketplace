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
