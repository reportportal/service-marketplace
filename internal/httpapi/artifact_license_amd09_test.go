package httpapi

// AMD-09's premium-artifact license error table (requirements/AMENDMENTS-v1.md), rendered
// end to end through the real router (e.do / doOn), the real middleware chain and a real
// license.Service backed by storage.LocalStore -- never a bare unit test of
// licenseErrorResponse. Every row of the table gets its own test, because the task's own
// warning is exactly right: getting the 401/403 split wrong (unknown customerId and an
// expired JWT `exp` land on 401 LICENSE_JWT_INVALID; expired ENTITLEMENT and a pluginId
// mismatch land on 403, split further into LICENSE_EXPIRED vs LICENSE_ENTITLEMENT_DENIED)
// is the single most likely defect here, and a table-collapsing bug would slip through a
// test that only checked one or two rows.

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
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// seedNonPremiumEntitlement writes a license entitlement directly to storage with an
// arbitrary Tier, bypassing license.Service.Create (which hardcodes a "premium"
// tier for every entitlement it issues) -- exactly the shape AMD-12 condition (2)
// must reject: a stored document whose tier is not "premium", reachable by an
// operator editing the document directly or by a future migration/import path even
// though no code path in this release ever produces it via Create.
func (e *testEnv) seedNonPremiumEntitlement(t *testing.T, customerID, tier string, priv ed25519.PrivateKey, pub ed25519.PublicKey) {
	t.Helper()
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	keyID, err := domain.DeriveLicenseKeyID(pubB64)
	if err != nil {
		t.Fatalf("DeriveLicenseKeyID: %v", err)
	}
	now := time.Now().UTC()
	ak := domain.AuthorizedKeys{Entitlements: []domain.LicenseEntitlement{{
		CustomerID: customerID,
		Tier:       tier,
		CreatedAt:  now,
		PublicKeys: []domain.LicensePublicKey{{KeyID: keyID, PublicKey: pubB64, IssuedAt: now}},
	}}}
	data, err := json.MarshalIndent(ak, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed entitlement: %v", err)
	}
	if _, err := e.Store.Write(context.Background(), storage.PathAuthorizedKeys, data, 0); err != nil {
		t.Fatalf("write seed entitlement: %v", err)
	}
}

// --- fixtures ----------------------------------------------------------------

func premiumTestManifest(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo Premium", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access:     domain.AccessPremium,
		ContactURL: "https://example.com/pricing",
	}
}

// publishPremiumPlugin publishes a single-version premium plugin through the real
// POST /api/v1/plugins router path, so every test in this file exercises the artifact
// endpoint against a plugin that reached storage the same way production traffic does.
func (e *testEnv) publishPremiumPlugin(t *testing.T, pluginID, version string) {
	t.Helper()
	m := premiumTestManifest(pluginID, version)
	jar := mustBuildJAR(t, m)
	body, ct := buildPublishMultipart(t, jar)
	rec := e.do(e.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body, ct))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed premium publish: expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// amd09Key is a raw Ed25519 keypair used to sign test tokens.
type amd09Key struct {
	priv ed25519.PrivateKey
}

func newAMD09Key(t *testing.T) amd09Key {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return amd09Key{priv: priv}
}

func decodeAMD09PrivateKey(t *testing.T, b64 string) amd09Key {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	return amd09Key{priv: ed25519.PrivateKey(raw)}
}

// amd09Token builds a compact-serialized license JWT signed by k. kid == "" omits the JWS
// protected header's `kid` entirely, exercising AMD-11's kid-less migration-fallback path;
// a non-empty kid sets it explicitly (including a deliberately wrong one, for the
// unknown/revoked-kid test). exp.IsZero() omits the `exp` claim.
func amd09Token(t *testing.T, k amd09Key, kid, customerID, pluginID string, exp time.Time) string {
	t.Helper()
	builder := jwt.NewBuilder().
		Claim("customerId", customerID).
		Claim("pluginId", pluginID)
	if !exp.IsZero() {
		builder = builder.Expiration(exp)
	}
	built, err := builder.Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}

	var signOpt jwt.SignEncryptParseOption
	if kid == "" {
		signOpt = jwt.WithKey(jwa.EdDSA, k.priv)
	} else {
		hdrs := jws.NewHeaders()
		if err := hdrs.Set(jws.KeyIDKey, kid); err != nil {
			t.Fatalf("set kid header: %v", err)
		}
		signOpt = jwt.WithKey(jwa.EdDSA, k.priv, jws.WithProtectedHeaders(hdrs))
	}
	signed, err := jwt.Sign(built, signOpt)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return string(signed)
}

func artifactURL(pluginID, version string) string {
	return "/api/v1/plugins/" + pluginID + "/versions/" + version + "/artifact"
}

func bearerArtifactRequest(pluginID, version, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, artifactURL(pluginID, version), nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// assertLicenseError decodes rec as an AMD-09 error envelope, asserts it matches the
// wanted status/code, and -- the AMD-09 requirement that clients must be able to branch
// on 'code' alone -- asserts the body carries NO 'blocked' field, unlike a blocked-version
// 403 (BlockedArtifactErrorResponse).
func assertLicenseError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode ErrorCode) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d: body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Code != wantCode {
		t.Fatalf("code = %q, want %q: body=%s", body.Code, wantCode, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body as object: %v", err)
	}
	if _, ok := raw["blocked"]; ok {
		t.Fatalf("license error body carries a 'blocked' field -- AMD-09 requires the entitlement-denial error bodies to have NO 'blocked' field so a client can tell them apart from a blocked-version 403 by 'code' alone: body=%s", rec.Body.String())
	}
}

// --- AMD-09 table, row by row --------------------------------------------------

// TestGetArtifact_MissingAuthHeader_401JWTMissing is AMD-09's first row: "Authorization
// header absent or blank" -> 401 LICENSE_JWT_MISSING.
func TestGetArtifact_MissingAuthHeader_401JWTMissing(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-missing"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", ""))
	assertLicenseError(t, rec, http.StatusUnauthorized, CodeLicenseJWTMissing)
}

// TestGetArtifact_MalformedToken_401JWTInvalid: "JWT unparseable" -> 401 LICENSE_JWT_INVALID.
func TestGetArtifact_MalformedToken_401JWTInvalid(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-malformed"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", "not-a-jwt-at-all"))
	assertLicenseError(t, rec, http.StatusUnauthorized, CodeLicenseJWTInvalid)
}

// TestGetArtifact_UnknownCustomerId_401JWTInvalid: "... or unknown customerId" -> 401
// LICENSE_JWT_INVALID, NOT 403 -- this is the row easiest to confuse with entitlement
// denial, since "no entitlement for this customer" sounds adjacent to "entitlement
// revoked". AMD-09 puts it firmly on the 401 side, in the same bucket as a bad signature.
func TestGetArtifact_UnknownCustomerId_401JWTInvalid(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-unknown-cust"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	k := newAMD09Key(t) // never registered with any entitlement
	token := amd09Token(t, k, "", "nobody-corp", pluginID, time.Now().Add(time.Hour))

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusUnauthorized, CodeLicenseJWTInvalid)
}

// TestGetArtifact_BadSignature_401JWTInvalid: a token whose customerId names a real
// entitlement, but which was signed by a key that entitlement never registered -> 401
// LICENSE_JWT_INVALID (the candidate key set is non-empty, but no candidate verifies it).
func TestGetArtifact_BadSignature_401JWTInvalid(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-badsig"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")
	e.createLicense("acme-corp")

	forged := newAMD09Key(t) // not acme-corp's registered key
	token := amd09Token(t, forged, "", "acme-corp", pluginID, time.Now().Add(time.Hour))

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusUnauthorized, CodeLicenseJWTInvalid)
}

// TestGetArtifact_ExpiredJWTExp_401JWTInvalid: "... or expired exp" -> 401
// LICENSE_JWT_INVALID. Distinct from AMD-10's entitlement-expiry row below: this is the
// JWT's own short-lived exp claim, checked before the entitlement is even consulted, and
// the entitlement itself is NOT expired here.
func TestGetArtifact_ExpiredJWTExp_401JWTInvalid(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-expjwt"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")
	created := e.createLicense("acme-corp")
	k := decodeAMD09PrivateKey(t, created.PrivateKey)

	token := amd09Token(t, k, "", "acme-corp", pluginID, time.Now().Add(-time.Hour))

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusUnauthorized, CodeLicenseJWTInvalid)
}

// TestGetArtifact_EntitlementExpired_403LicenseExpired is AMD-10's mandatory verification
// step and its explicit boundary: "a token presented one second after expiresAt is
// rejected" -- reject with 403 LICENSE_EXPIRED, NOT 401, when the JWT itself verifies fine
// but the matching entitlement's expiresAt has passed. Uses serverWithLicenseClock to pin
// "now" to exactly one second after expiresAt, rather than sleeping. Creates the
// entitlement directly via license.Service.Create (not the HTTP endpoint, whose
// expiresAt request field is date-only) so expiresAt carries second-level precision.
func TestGetArtifact_EntitlementExpired_403LicenseExpired(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-entexp"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	expiresAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	res, err := e.Server.deps.License.Create(context.Background(), "acme-corp", &expiresAt)
	if err != nil {
		t.Fatalf("License.Create: %v", err)
	}
	k := decodeAMD09PrivateKey(t, res.PrivateKey)

	// The JWT's own exp is comfortably in the future relative to the pinned clock below --
	// only the ENTITLEMENT is expired.
	token := amd09Token(t, k, "", "acme-corp", pluginID, expiresAt.Add(24*time.Hour))

	oneSecondAfter := expiresAt.Add(time.Second)
	srv := e.serverWithLicenseClock(func() time.Time { return oneSecondAfter })

	rec := doOn(srv, bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusForbidden, CodeLicenseExpired)
}

// TestGetArtifact_EntitlementNotYetExpired_Succeeds is the boundary's other side: a token
// presented BEFORE expiresAt (here, exactly at expiresAt minus one second) must still
// succeed -- proving TestGetArtifact_EntitlementExpired_403LicenseExpired isn't just always
// rejecting.
func TestGetArtifact_EntitlementNotYetExpired_Succeeds(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-entnotyet"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	expiresAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	res, err := e.Server.deps.License.Create(context.Background(), "acme-corp", &expiresAt)
	if err != nil {
		t.Fatalf("License.Create: %v", err)
	}
	k := decodeAMD09PrivateKey(t, res.PrivateKey)
	token := amd09Token(t, k, "", "acme-corp", pluginID, expiresAt.Add(24*time.Hour))

	oneSecondBefore := expiresAt.Add(-time.Second)
	srv := e.serverWithLicenseClock(func() time.Time { return oneSecondBefore })

	rec := doOn(srv, bearerArtifactRequest(pluginID, "1.0.0", token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (entitlement not yet expired): body=%s", rec.Code, rec.Body.String())
	}
}

// TestGetArtifact_PluginIDMismatch_403EntitlementDenied is AMD-12/AMD-09's pluginId-claim
// row: a token that verifies cleanly for a real, non-expired entitlement, but whose
// pluginId claim names a DIFFERENT plugin than the URL path -> 403
// LICENSE_ENTITLEMENT_DENIED, not 401.
func TestGetArtifact_PluginIDMismatch_403EntitlementDenied(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-mismatch"
	const otherPluginID = "plugin-premium-other"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")
	created := e.createLicense("acme-corp")
	k := decodeAMD09PrivateKey(t, created.PrivateKey)

	token := amd09Token(t, k, "", "acme-corp", otherPluginID, time.Now().Add(time.Hour))

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusForbidden, CodeLicenseEntitlementDenied)
}

// TestGetArtifact_NonPremiumTier_403EntitlementDenied is AMD-12 condition (2)'s tier
// half reaching the client-visible artifact endpoint: an entitlement whose Tier is
// not "premium" must be refused with 403 LICENSE_ENTITLEMENT_DENIED (AMD-09 row 3)
// even though the token's signature verifies cleanly against a real, non-revoked,
// non-expired entitlement. license.Service.Create cannot produce this shape itself
// (it hardcodes tierPremium), so this seeds the document directly -- see
// seedNonPremiumEntitlement's doc comment for why that is still a real, reachable
// document shape and not an untestable hypothetical.
func TestGetArtifact_NonPremiumTier_403EntitlementDenied(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-nonprem-tier"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	e.seedNonPremiumEntitlement(t, "acme-corp", "basic", priv, pub)
	k := amd09Key{priv: priv}
	token := amd09Token(t, k, "", "acme-corp", pluginID, time.Now().Add(time.Hour))

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusForbidden, CodeLicenseEntitlementDenied)
}

// TestGetArtifact_RevokedEntitlement_403EntitlementDenied is AMD-09 row 3's other
// half, and the specific gap this task exists to close: "JWT valid but entitlement
// revoked" -> 403 LICENSE_ENTITLEMENT_DENIED, not 401. Whole-entitlement revocation
// (DELETE /api/v1/licenses/{customerId}) tombstones rather than deletes the
// entitlement (see license.Service.Revoke's doc comment) precisely so this case is
// distinguishable, at the HTTP layer, from
// TestGetArtifact_UnknownCustomerId_401JWTInvalid above -- both requests use a
// customerId this endpoint currently refuses, but only one of them ever had a real,
// working entitlement.
func TestGetArtifact_RevokedEntitlement_403EntitlementDenied(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-revoked-whole"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")
	created := e.createLicense("acme-corp")
	k := decodeAMD09PrivateKey(t, created.PrivateKey)
	token := amd09Token(t, k, "", "acme-corp", pluginID, time.Now().Add(time.Hour))

	// Sanity: valid before revocation -- without this, the 403 below could just be
	// this token never having worked in the first place.
	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	if rec.Code != http.StatusOK {
		t.Fatalf("before revoke: status = %d, want 200: body=%s", rec.Code, rec.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/licenses/acme-corp", nil)
	delReq.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	delRec := e.do(delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("revoke entitlement: status = %d, body = %s", delRec.Code, delRec.Body.String())
	}

	rec = e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusForbidden, CodeLicenseEntitlementDenied)
}

// TestGetArtifact_RevokedKid_401JWTInvalid is AMD-11's kid-aware revocation reaching the
// client-visible artifact endpoint: a token signed with a kid that DOES belong to the
// customer's entitlement, but whose key has since been revoked via the real
// DELETE /api/v1/licenses/{customerId}/keys/{keyId} route, must be rejected -- and per
// AMD-11 text ("returns 401 LICENSE_JWT_INVALID for an unknown or revoked kid") that is
// 401, not 403. Revoking requires a second live key first (AMD-11's last-active-key
// guard), so this rotates one in before revoking the original.
func TestGetArtifact_RevokedKid_401JWTInvalid(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-revoked"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	created := e.createLicense("acme-corp")
	k := decodeAMD09PrivateKey(t, created.PrivateKey)
	keyID := created.PublicKeys[0].KeyID

	e.rotateLicenseKey("acme-corp") // second live key, so revoking the first is allowed

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/licenses/acme-corp/keys/"+keyID, nil)
	delReq.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	delRec := e.do(delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("revoke key: status = %d, body = %s", delRec.Code, delRec.Body.String())
	}

	token := amd09Token(t, k, keyID, "acme-corp", pluginID, time.Now().Add(time.Hour))

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	assertLicenseError(t, rec, http.StatusUnauthorized, CodeLicenseJWTInvalid)
}

// TestGetArtifact_ValidLicense_Succeeds is the control: a correctly signed, non-expired,
// matching-pluginId token against a live, non-revoked key must actually succeed with a
// pre-signed download URL. Without this, every 401/403 test above could be passing because
// the endpoint rejects everything, not because it discriminates correctly.
func TestGetArtifact_ValidLicense_Succeeds(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-valid"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")
	created := e.createLicense("acme-corp")
	k := decodeAMD09PrivateKey(t, created.PrivateKey)

	token := amd09Token(t, k, "", "acme-corp", pluginID, time.Now().Add(time.Hour))

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
	}
	var out PremiumArtifactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.DownloadURL == "" {
		t.Fatalf("downloadUrl is empty: body=%s", rec.Body.String())
	}
}

// TestGetArtifact_BlockedVersionResponse_HasBlockedFieldUnlikeLicenseErrors is the other
// half of assertLicenseError's guard: proves the blocked-version 403 body DOES carry
// blocked:true, so the "no blocked field" assertion on license-error bodies is actually
// discriminating something real, not vacuously true because nothing in this file ever sets
// it either way.
func TestGetArtifact_BlockedVersionResponse_HasBlockedFieldUnlikeLicenseErrors(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-premium-blocked-shape"
	e.publishPremiumPlugin(t, pluginID, "1.0.0")

	recBlock := e.do(e.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions/1.0.0/block",
		credOperatorSession, []byte(`{"reason":"cve-test"}`), "application/json"))
	if recBlock.Code != http.StatusOK {
		t.Fatalf("BlockVersion: expected 200, got %d body=%s", recBlock.Code, recBlock.Body.String())
	}

	rec := e.do(bearerArtifactRequest(pluginID, "1.0.0", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (blocked): body=%s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	blockedRaw, ok := raw["blocked"]
	if !ok {
		t.Fatalf("blocked-version 403 body is missing 'blocked': body=%s", rec.Body.String())
	}
	var blocked bool
	if err := json.Unmarshal(blockedRaw, &blocked); err != nil || !blocked {
		t.Fatalf("blocked-version 403 body's 'blocked' field = %s, want true: body=%s", blockedRaw, rec.Body.String())
	}
}
