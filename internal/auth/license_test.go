package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/reportportal/service-marketplace/internal/domain"
)

// testKey is a generated ed25519 keypair plus its base64/derived-keyId form, used to
// build both candidate domain.LicensePublicKey entries and signed tokens.
type testKey struct {
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	pubB64 string
	keyID  string
}

func newTestKey(t *testing.T) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	keyID, err := domain.DeriveLicenseKeyID(pubB64)
	if err != nil {
		t.Fatalf("derive key id: %v", err)
	}
	return testKey{pub: pub, priv: priv, pubB64: pubB64, keyID: keyID}
}

// signLicenseToken builds a license JWT signed by k, with the given claims. If kid is
// non-empty it is set as the JWS protected header's `kid`; if kid == "__none__" no kid
// header is set at all (distinct from "" which is not a meaningful sentinel here since
// every test that cares passes a real kid or the no-kid sentinel explicitly).
func signLicenseToken(t *testing.T, k testKey, customerID, pluginID string, exp time.Time, kid string) string {
	t.Helper()
	builder := jwt.NewBuilder().
		Claim("customerId", customerID).
		Claim("pluginId", pluginID)
	if !exp.IsZero() {
		builder = builder.Expiration(exp)
	}
	tok, err := builder.Build()
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
	signed, err := jwt.Sign(tok, signOpt)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return string(signed)
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func keyEntry(k testKey, revoked bool) domain.LicensePublicKey {
	var revokedAt *time.Time
	if revoked {
		rt := time.Now().UTC()
		revokedAt = &rt
	}
	return domain.LicensePublicKey{KeyID: k.keyID, PublicKey: k.pubB64, RevokedAt: revokedAt}
}

func TestVerifyLicenseJWT_MissingToken(t *testing.T) {
	_, err := VerifyLicenseJWT("", nil, nil)
	if !errors.Is(err, ErrLicenseTokenMissing) {
		t.Fatalf("err = %v, want ErrLicenseTokenMissing", err)
	}
}

func TestVerifyLicenseJWT_Success_KidMatch(t *testing.T) {
	k := newTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token := signLicenseToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour), k.keyID)

	claims, err := VerifyLicenseJWT(token, []domain.LicensePublicKey{keyEntry(k, false)}, fixedClock(now))
	if err != nil {
		t.Fatalf("VerifyLicenseJWT: %v", err)
	}
	if claims.CustomerID != "acme-corp" || claims.PluginID != "plugin-jira-cloud" {
		t.Fatalf("claims = %+v, want customerId/pluginId acme-corp/plugin-jira-cloud", claims)
	}
}

// TestVerifyLicenseJWT_RevokedKid_NeverVerifies is the mutation most worth proving
// (AMD-11): a token whose kid resolves to a REVOKED key must be rejected even though
// the signature itself is perfectly valid for that key. A verifier that only checks
// "does the kid resolve to *a* key" and forgets to also check RevokedAt would let this
// through -- see this test's paired mutation proof.
func TestVerifyLicenseJWT_RevokedKid_NeverVerifies(t *testing.T) {
	k := newTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token := signLicenseToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour), k.keyID)

	claims, err := VerifyLicenseJWT(token, []domain.LicensePublicKey{keyEntry(k, true)}, fixedClock(now))
	if !errors.Is(err, ErrLicenseKeyInvalid) {
		t.Fatalf("err = %v, want ErrLicenseKeyInvalid", err)
	}
	if claims != nil {
		t.Fatalf("claims = %+v, want nil on a revoked key", claims)
	}
}

// TestVerifyLicenseJWT_UnknownKid_HardFailsWithoutFallback: the token's kid does not
// match ANY candidate key's derived id. Even though the token was legitimately signed
// by keyA (a live, non-revoked candidate), AMD-11 requires this to be a hard failure --
// never a fall-through to "well, some other non-revoked key might verify it anyway".
func TestVerifyLicenseJWT_UnknownKid_HardFailsWithoutFallback(t *testing.T) {
	k := newTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Signed BY k, but the kid header claims an id that resolves to nothing in the
	// candidate set.
	token := signLicenseToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour), "00000000")

	claims, err := VerifyLicenseJWT(token, []domain.LicensePublicKey{keyEntry(k, false)}, fixedClock(now))
	if !errors.Is(err, ErrLicenseKeyInvalid) {
		t.Fatalf("err = %v, want ErrLicenseKeyInvalid", err)
	}
	if claims != nil {
		t.Fatalf("claims = %+v, want nil on unknown kid", claims)
	}
}

// TestVerifyLicenseJWT_KidLess_FallbackSkipsRevokedKeys: the migration fallback (no kid
// header) must still exclude revoked keys from the set it tries -- a revoked key must
// never verify by EITHER route (kid-matched or kid-less fallback).
func TestVerifyLicenseJWT_KidLess_FallbackSkipsRevokedKeys(t *testing.T) {
	revokedButWouldVerify := newTestKey(t)
	liveButWrongKey := newTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Signed by the revoked key, no kid header at all.
	token := signLicenseToken(t, revokedButWouldVerify, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour), "")

	keys := []domain.LicensePublicKey{
		keyEntry(revokedButWouldVerify, true), // would verify the signature, but revoked
		keyEntry(liveButWrongKey, false),      // live, but wrong key: won't verify
	}
	claims, err := VerifyLicenseJWT(token, keys, fixedClock(now))
	if !errors.Is(err, ErrLicenseTokenInvalid) {
		t.Fatalf("err = %v, want ErrLicenseTokenInvalid (only candidate that could verify is revoked)", err)
	}
	if claims != nil {
		t.Fatalf("claims = %+v, want nil", claims)
	}
}

func TestVerifyLicenseJWT_KidLess_FallbackTriesNonRevokedKeys(t *testing.T) {
	decoyRevoked := newTestKey(t)
	signer := newTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token := signLicenseToken(t, signer, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour), "")

	keys := []domain.LicensePublicKey{
		keyEntry(decoyRevoked, true),
		keyEntry(signer, false),
	}
	claims, err := VerifyLicenseJWT(token, keys, fixedClock(now))
	if err != nil {
		t.Fatalf("VerifyLicenseJWT: %v", err)
	}
	if claims.CustomerID != "acme-corp" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestVerifyLicenseJWT_BadSignature(t *testing.T) {
	signer := newTestKey(t)
	other := newTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token := signLicenseToken(t, signer, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour), "")

	_, err := VerifyLicenseJWT(token, []domain.LicensePublicKey{keyEntry(other, false)}, fixedClock(now))
	if !errors.Is(err, ErrLicenseTokenInvalid) {
		t.Fatalf("err = %v, want ErrLicenseTokenInvalid", err)
	}
}

func TestVerifyLicenseJWT_Malformed(t *testing.T) {
	_, err := VerifyLicenseJWT("not-a-jwt", []domain.LicensePublicKey{keyEntry(newTestKey(t), false)}, nil)
	if !errors.Is(err, ErrLicenseTokenInvalid) {
		t.Fatalf("err = %v, want ErrLicenseTokenInvalid", err)
	}
}

// TestVerifyLicenseJWT_ExpiredToken_DistinctFromInvalid proves the JWT's own "exp"
// claim elapsing returns ErrLicenseTokenExpired specifically -- distinguishable from a
// bad signature -- and that this is driven by the injected clock, not a real sleep.
func TestVerifyLicenseJWT_ExpiredToken_DistinctFromInvalid(t *testing.T) {
	k := newTestKey(t)
	exp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token := signLicenseToken(t, k, "acme-corp", "plugin-jira-cloud", exp, k.keyID)

	// One second after exp: rejected as expired.
	after := exp.Add(time.Second)
	_, err := VerifyLicenseJWT(token, []domain.LicensePublicKey{keyEntry(k, false)}, fixedClock(after))
	if !errors.Is(err, ErrLicenseTokenExpired) {
		t.Fatalf("err = %v, want ErrLicenseTokenExpired", err)
	}

	// One second before exp: accepted.
	before := exp.Add(-time.Second)
	claims, err := VerifyLicenseJWT(token, []domain.LicensePublicKey{keyEntry(k, false)}, fixedClock(before))
	if err != nil {
		t.Fatalf("VerifyLicenseJWT (before exp): %v", err)
	}
	if claims.CustomerID != "acme-corp" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestPeekUnverifiedCustomerID(t *testing.T) {
	k := newTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token := signLicenseToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour), "")

	cid, err := PeekUnverifiedCustomerID(token)
	if err != nil {
		t.Fatalf("PeekUnverifiedCustomerID: %v", err)
	}
	if cid != "acme-corp" {
		t.Fatalf("cid = %q, want acme-corp", cid)
	}
}
