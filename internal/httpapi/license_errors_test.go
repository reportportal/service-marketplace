package httpapi

// AMD-09: every licence failure on the premium-artifact route must be separately
// diagnosable, so ReportPortal's backend can tell an operator "your licence expired,
// renew it" apart from "you pasted the wrong key" apart from "this licence does not
// cover this plugin". Before this, everything except a missing token collapsed into
// one 403 "Invalid license" and support had to guess.
//
// The table (requirements/AMENDMENTS-v1.md, AMD-09):
//
//	Authorization absent or blank                        401 LICENSE_JWT_MISSING
//	unparseable / bad signature / elapsed exp /
//	  unknown customerId                                 401 LICENSE_JWT_INVALID
//	entitlement expired                                  403 LICENSE_EXPIRED
//	entitlement does not cover the plugin                403 LICENSE_ENTITLEMENT_DENIED
//	version blocked                                      403 BlockedArtifactError (unchanged)
//
// The likeliest way to get this wrong is putting a row on the wrong side of the
// 401/403 split — an unknown customer and an elapsed *token* exp are 401, while an
// expired *entitlement* is 403 — so every row is asserted here individually.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/reportportal/service-marketplace/internal/storage"
)

const (
	licPluginID  = "plugin-premium"
	licVersion   = "1.0.0"
	licCustomer  = "acme-corp"
	licArtifactP = "/api/v1/plugins/" + licPluginID + "/versions/" + licVersion + "/artifact"
)

// seedPremiumPlugin is seedPlugin's premium twin: same direct-to-storage seeding, but
// the manifest declares access "premium" so the artifact route takes the licence path.
func (e *testEnv) seedPremiumPlugin(pluginID, version string) {
	e.t.Helper()
	ctx := context.Background()
	m := map[string]any{
		"id": pluginID, "name": pluginID, "version": version, "description": "d",
		"author": map[string]string{"name": "A"}, "license": "Apache-2.0",
		"category": "other", "compatibility": map[string]string{"reportportal": ">=25.1"},
		"access": "premium", "contactUrl": "https://reportportal.io/pricing",
	}
	mb, _ := json.Marshal(m)
	if _, err := e.Store.Write(ctx, storage.VersionManifestPath(pluginID, version), mb, 0); err != nil {
		e.t.Fatalf("seed premium manifest: %v", err)
	}
	st := map[string]any{
		"id": pluginID, "tier": "official", "latestVersion": version,
		"versions": []map[string]any{{"version": version, "publishedAt": time.Now().UTC(), "sha256": "deadbeef"}},
	}
	sb, _ := json.Marshal(st)
	if _, err := e.Store.Write(ctx, storage.PluginStatePath(pluginID), sb, 0); err != nil {
		e.t.Fatalf("seed premium plugin state: %v", err)
	}
}

// createEntitlement issues a real entitlement through the licence service and returns
// the customer's private key, so tests sign tokens exactly as a customer would.
func (e *testEnv) createEntitlement(customerID string, expiresAt *time.Time) string {
	e.t.Helper()
	res, err := e.Server.deps.License.Create(context.Background(), customerID, expiresAt)
	if err != nil {
		e.t.Fatalf("create entitlement: %v", err)
	}
	return res.PrivateKey
}

func mintLicenseJWT(t *testing.T, privB64, customerID, pluginID string, exp time.Time) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	built, err := jwt.NewBuilder().
		Claim("customerId", customerID).
		Claim("pluginId", pluginID).
		IssuedAt(time.Now().Add(-time.Minute)).
		Expiration(exp).
		Build()
	if err != nil {
		t.Fatalf("build license token: %v", err)
	}
	signed, err := jwt.Sign(built, jwt.WithKey(jwa.EdDSA, ed25519.PrivateKey(raw)))
	if err != nil {
		t.Fatalf("sign license token: %v", err)
	}
	return string(signed)
}

func foreignPrivateKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return base64.StdEncoding.EncodeToString(priv)
}

// premiumEnv seeds one premium plugin plus one entitlement for licCustomer.
func premiumEnv(t *testing.T, entitlementExpiry *time.Time) (*testEnv, string) {
	t.Helper()
	env := newTestEnv(t)
	env.seedPremiumPlugin(licPluginID, licVersion)
	return env, env.createEntitlement(licCustomer, entitlementExpiry)
}

func TestArtifactLicenseErrorTable(t *testing.T) {
	past := time.Now().Add(-24 * time.Hour)

	cases := []struct {
		name string
		// authHeader returns the Authorization header value to send; "" means send
		// none at all.
		authHeader func(t *testing.T, env *testEnv, priv string) string
		// entitlementExpiry is the seeded entitlement's own ExpiresAt.
		entitlementExpiry *time.Time
		wantStatus        int
		wantCode          ErrorCode
	}{
		{
			name:       "no Authorization header",
			authHeader: func(*testing.T, *testEnv, string) string { return "" },
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeLicenseJWTMissing,
		},
		{
			name:       "blank bearer value",
			authHeader: func(*testing.T, *testEnv, string) string { return "Bearer    " },
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeLicenseJWTMissing,
		},
		{
			name:       "unparseable token",
			authHeader: func(*testing.T, *testEnv, string) string { return "Bearer not-a-jwt-at-all" },
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeLicenseJWTInvalid,
		},
		{
			name: "signature by a key the entitlement does not hold",
			authHeader: func(t *testing.T, _ *testEnv, _ string) string {
				return "Bearer " + mintLicenseJWT(t, foreignPrivateKey(t), licCustomer, licPluginID, time.Now().Add(time.Hour))
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeLicenseJWTInvalid,
		},
		{
			// A token's own exp is a token problem: 401, NOT the 403 an expired
			// entitlement gets.
			name: "elapsed token exp",
			authHeader: func(t *testing.T, _ *testEnv, priv string) string {
				return "Bearer " + mintLicenseJWT(t, priv, licCustomer, licPluginID, time.Now().Add(-time.Hour))
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeLicenseJWTInvalid,
		},
		{
			// An unknown customerId is indistinguishable from a forged token: 401.
			name: "unknown customerId",
			authHeader: func(t *testing.T, _ *testEnv, priv string) string {
				return "Bearer " + mintLicenseJWT(t, priv, "globex-corp", licPluginID, time.Now().Add(time.Hour))
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeLicenseJWTInvalid,
		},
		{
			// The entitlement's own window has closed: 403, and a distinct code, so
			// an operator is told to renew rather than to check their key.
			name: "expired entitlement, valid token",
			authHeader: func(t *testing.T, _ *testEnv, priv string) string {
				return "Bearer " + mintLicenseJWT(t, priv, licCustomer, licPluginID, time.Now().Add(time.Hour))
			},
			entitlementExpiry: &past,
			wantStatus:        http.StatusForbidden,
			wantCode:          CodeLicenseExpired,
		},
		{
			name: "pluginId claim does not cover the requested plugin",
			authHeader: func(t *testing.T, _ *testEnv, priv string) string {
				return "Bearer " + mintLicenseJWT(t, priv, licCustomer, "plugin-other", time.Now().Add(time.Hour))
			},
			wantStatus: http.StatusForbidden,
			wantCode:   CodeLicenseEntitlementDenied,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, priv := premiumEnv(t, tc.entitlementExpiry)
			req := httptest.NewRequest(http.MethodGet, licArtifactP, nil)
			if h := tc.authHeader(t, env, priv); h != "" {
				req.Header.Set("Authorization", h)
			}
			rec := env.do(req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q; body=%s", body.Code, tc.wantCode, rec.Body.String())
			}
			// AMD-09: licence failures are told apart by `code`. They must never
			// carry the blocked-version payload's `blocked` field — not even as
			// false — so no client is tempted to branch on field presence.
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decoding body: %v", err)
			}
			if _, ok := raw["blocked"]; ok {
				t.Fatalf("licence error body carries a \"blocked\" field: %s", rec.Body.String())
			}
		})
	}
}

// The four licence codes must be distinct from one another: a table whose rows share a
// code tells support nothing.
func TestLicenseErrorCodesAreDistinct(t *testing.T) {
	seen := map[ErrorCode]bool{}
	for _, c := range []ErrorCode{CodeLicenseJWTMissing, CodeLicenseJWTInvalid, CodeLicenseExpired, CodeLicenseEntitlementDenied} {
		if c == "" {
			t.Fatal("a licence error code is empty")
		}
		if seen[c] {
			t.Fatalf("duplicate licence error code %q", c)
		}
		seen[c] = true
	}
}

// Regression guard for the row the table leaves untouched: a valid licence still gets
// its short-lived signed URL.
func TestPremiumArtifactWithValidLicenseStillReturnsSignedURL(t *testing.T) {
	env, priv := premiumEnv(t, nil)
	req := httptest.NewRequest(http.MethodGet, licArtifactP, nil)
	req.Header.Set("Authorization", "Bearer "+mintLicenseJWT(t, priv, licCustomer, licPluginID, time.Now().Add(time.Hour)))
	rec := env.do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body PremiumArtifactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding premium artifact response: %v; body=%s", err, rec.Body.String())
	}
	if body.DownloadURL == "" || body.ExpiresAt.IsZero() {
		t.Fatalf("premium artifact response = %+v, want a signed URL and its expiry", body)
	}
}

// An entitlement that has not expired is honored right up to its ExpiresAt: only an
// elapsed one is denied.
func TestPremiumArtifactHonorsAnUnexpiredEntitlement(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	env, priv := premiumEnv(t, &future)
	req := httptest.NewRequest(http.MethodGet, licArtifactP, nil)
	req.Header.Set("Authorization", "Bearer "+mintLicenseJWT(t, priv, licCustomer, licPluginID, time.Now().Add(time.Hour)))
	rec := env.do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an entitlement expiring in the future; body=%s", rec.Code, rec.Body.String())
	}
}

// The blocked-version 403 keeps its own shape (blocked:true + reason) and is reached
// before any licence check, so blocking still works on a premium plugin.
func TestBlockedVersionArtifactKeepsItsBlockedPayload(t *testing.T) {
	env, priv := premiumEnv(t, nil)
	blockVersion(t, env, licPluginID, licVersion, "CVE-2026-1234")

	req := httptest.NewRequest(http.MethodGet, licArtifactP, nil)
	req.Header.Set("Authorization", "Bearer "+mintLicenseJWT(t, priv, licCustomer, licPluginID, time.Now().Add(time.Hour)))
	rec := env.do(req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Blocked bool   `json:"blocked"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding blocked payload: %v; body=%s", err, rec.Body.String())
	}
	if !body.Blocked || body.Reason != "CVE-2026-1234" {
		t.Fatalf("blocked payload = %+v, want blocked:true with its reason; body=%s", body, rec.Body.String())
	}
}

// A version with no manifest on disk is not installable and is not a licence problem:
// it stays a 404, licence or no licence.
func TestIncompleteVersionArtifactStays404(t *testing.T) {
	env, priv := premiumEnv(t, nil)
	ctx := context.Background()
	if err := env.Store.Delete(ctx, storage.VersionManifestPath(licPluginID, licVersion)); err != nil {
		t.Fatalf("removing manifest: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, licArtifactP, nil)
	req.Header.Set("Authorization", "Bearer "+mintLicenseJWT(t, priv, licCustomer, licPluginID, time.Now().Add(time.Hour)))
	rec := env.do(req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a version with no manifest; body=%s", rec.Code, rec.Body.String())
	}
}

// blockVersion blocks a version through the operator route, so the block payload the
// artifact route reads is the one the real lifecycle service writes.
func blockVersion(t *testing.T, env *testEnv, pluginID, version, reason string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"reason": reason})
	req := env.newRequest(http.MethodPost,
		"/api/v1/plugins/"+pluginID+"/versions/"+version+"/block",
		credOperatorSession, body, "application/json")
	rec := env.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("block version = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
