package license

// Tests for VerifyToken, RevokeKey and the injectable clock (AMD-09/AMD-10/AMD-11).
// These exercise Service against a real storage.LocalStore, not a mock, so the
// WriteWithRetry/CAS plumbing and JSON round-trip are part of what's proven, not
// assumed.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// seedEntitlementDirect writes a single-entitlement authorized_keys.json document
// directly to store, bypassing Service.Create, so a test can construct an entitlement
// shape Create itself would never produce (e.g. a non-"premium" tier) -- exactly the
// AMD-12 gap this proves: Create hardcodes Tier: tierPremium today, but the field is
// a normal persisted string an operator or a future migration can set to anything,
// and VerifyToken must check its VALUE, not merely that an entitlement exists.
func seedEntitlementDirect(t *testing.T, store storage.ObjectStore, ent domain.LicenseEntitlement) {
	t.Helper()
	ak := domain.AuthorizedKeys{Entitlements: []domain.LicenseEntitlement{ent}}
	data, err := json.MarshalIndent(ak, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed entitlement: %v", err)
	}
	if _, err := store.Write(context.Background(), storage.PathAuthorizedKeys, data, 0); err != nil {
		t.Fatalf("write seed entitlement: %v", err)
	}
}

type verifyTestKey struct {
	priv   ed25519.PrivateKey
	pubB64 string
	keyID  string
}

func newVerifyTestKey(t *testing.T) verifyTestKey {
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
	return verifyTestKey{priv: priv, pubB64: pubB64, keyID: keyID}
}

func signToken(t *testing.T, k verifyTestKey, customerID, pluginID string, exp time.Time) string {
	t.Helper()
	tok, err := jwt.NewBuilder().
		Claim("customerId", customerID).
		Claim("pluginId", pluginID).
		Expiration(exp).
		Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.EdDSA, k.priv))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return string(signed)
}

func newVerifyTestStore(t *testing.T) storage.ObjectStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func TestVerifyToken_UnknownCustomer(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	k := newVerifyTestKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	token := signToken(t, k, "nobody-corp", "plugin-jira-cloud", now.Add(time.Hour))

	_, err := svc.VerifyToken(context.Background(), token)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestVerifyToken_Success(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	res, err := svc.Create(context.Background(), "acme-corp", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	priv, err := base64.StdEncoding.DecodeString(res.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	k := verifyTestKey{priv: ed25519.PrivateKey(priv)}
	token := signToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour))

	claims, err := svc.VerifyToken(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if claims.CustomerID != "acme-corp" || claims.PluginID != "plugin-jira-cloud" {
		t.Fatalf("claims = %+v", claims)
	}
}

// TestVerifyToken_EntitlementExpiry_Boundary is AMD-10's mandated boundary: a token
// presented one second after the entitlement's expiresAt is rejected, and one second
// before is accepted. Driven entirely by the injected clock -- no sleeping.
func TestVerifyToken_EntitlementExpiry_Boundary(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	createTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return createTime }

	expiresAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	res, err := svc.Create(context.Background(), "acme-corp", &expiresAt)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	priv, err := base64.StdEncoding.DecodeString(res.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	k := verifyTestKey{priv: ed25519.PrivateKey(priv)}
	token := signToken(t, k, "acme-corp", "plugin-jira-cloud", expiresAt.Add(24*time.Hour))

	// One second after expiresAt: rejected.
	svc.Now = func() time.Time { return expiresAt.Add(time.Second) }
	if _, err := svc.VerifyToken(context.Background(), token); !errors.Is(err, ErrEntitlementExpired) {
		t.Fatalf("one second after expiresAt: err = %v, want ErrEntitlementExpired", err)
	}

	// One second before expiresAt: accepted.
	svc.Now = func() time.Time { return expiresAt.Add(-time.Second) }
	if _, err := svc.VerifyToken(context.Background(), token); err != nil {
		t.Fatalf("one second before expiresAt: VerifyToken: %v", err)
	}

	// Exactly at expiresAt: "earlier than now" is false, so still accepted.
	svc.Now = func() time.Time { return expiresAt }
	if _, err := svc.VerifyToken(context.Background(), token); err != nil {
		t.Fatalf("exactly at expiresAt: VerifyToken: %v", err)
	}
}

func TestVerifyToken_RevokedKey_Rejected(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	res, err := svc.Create(context.Background(), "acme-corp", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// RevokeKey refuses to revoke an entitlement's LAST active key (AMD-11's 422
	// case, tested separately below) -- rotate first so the key under test is not
	// the only live one.
	if _, err := svc.RotateKey(context.Background(), "acme-corp"); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	ents, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keyID := ents[0].PublicKeys[0].KeyID
	if keyID == "" {
		t.Fatalf("Create did not populate KeyID: %+v", ents[0].PublicKeys[0])
	}

	if err := svc.RevokeKey(context.Background(), "acme-corp", keyID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	priv, err := base64.StdEncoding.DecodeString(res.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	k := verifyTestKey{priv: ed25519.PrivateKey(priv)}
	// Signed by the REVOKED key (customer's original private key); the surviving
	// live key from RotateKey is a different keypair and won't verify this
	// signature. Either way, the revoked key must never be the one that succeeds.
	token := signToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour))

	_, err = svc.VerifyToken(context.Background(), token)
	if !errors.Is(err, auth.ErrLicenseKeyInvalid) && !errors.Is(err, auth.ErrLicenseTokenInvalid) {
		t.Fatalf("err = %v, want a revoked/invalid key error", err)
	}
}

func TestRevokeKey_UnknownKeyID(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	if _, err := svc.Create(context.Background(), "acme-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.RevokeKey(context.Background(), "acme-corp", "00000000"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("err = %v, want ErrKeyNotFound", err)
	}
}

func TestRevokeKey_UnknownCustomer(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	if err := svc.RevokeKey(context.Background(), "nobody-corp", "00000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestRevokeKey_LastActiveKey_Rejected is AMD-11's 422 case: revoking an entitlement's
// only remaining non-revoked key must be refused so a customer is never left holding an
// entitlement with zero live keys -- whole-entitlement revocation (Service.Revoke) is
// the correct operation for that instead.
func TestRevokeKey_LastActiveKey_Rejected(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	ctx := context.Background()
	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ents, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keyID := ents[0].PublicKeys[0].KeyID

	if err := svc.RevokeKey(ctx, "acme-corp", keyID); !errors.Is(err, ErrLastActiveKey) {
		t.Fatalf("err = %v, want ErrLastActiveKey", err)
	}

	// And the key must still be live afterwards -- the rejected call must not have
	// applied a partial revocation.
	ents, err = svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if ents[0].PublicKeys[0].RevokedAt != nil {
		t.Fatalf("key was revoked despite ErrLastActiveKey: %+v", ents[0].PublicKeys[0])
	}
}

// TestRevokeKey_AllowedWhenAnotherKeyRemainsLive proves the 422 guard is scoped to
// "last NON-REVOKED key", not "only key ever issued": after RotateKey adds a second
// live key, revoking the first must succeed.
func TestRevokeKey_AllowedWhenAnotherKeyRemainsLive(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	ctx := context.Background()
	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.RotateKey(ctx, "acme-corp"); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	ents, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	firstKeyID := ents[0].PublicKeys[0].KeyID

	if err := svc.RevokeKey(ctx, "acme-corp", firstKeyID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	ents, err = svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if ents[0].PublicKeys[0].RevokedAt == nil {
		t.Fatalf("first key not revoked: %+v", ents[0].PublicKeys[0])
	}
	if ents[0].PublicKeys[1].RevokedAt != nil {
		t.Fatalf("second key unexpectedly revoked: %+v", ents[0].PublicKeys[1])
	}
}

func TestRevokeKey_Idempotent(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	ctx := context.Background()
	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.RotateKey(ctx, "acme-corp"); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	ents, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	firstKeyID := ents[0].PublicKeys[0].KeyID

	if err := svc.RevokeKey(ctx, "acme-corp", firstKeyID); err != nil {
		t.Fatalf("RevokeKey (1st): %v", err)
	}
	if err := svc.RevokeKey(ctx, "acme-corp", firstKeyID); err != nil {
		t.Fatalf("RevokeKey (2nd, idempotent): %v", err)
	}
}

// TestRotateKey_ResponseIncludesKeyID is AMD-11: RotateLicenseKeyResponse (built
// directly from license.RotateResult -- see internal/httpapi/handlers_auth.go's
// handleRotateLicenseKey, which writes it to the wire unmodified) must carry the new
// key's keyId so an operator/client can address it with
// DELETE /api/v1/licenses/{customerId}/keys/{keyId} without recomputing
// domain.DeriveLicenseKeyID(publicKey) themselves.
func TestRotateKey_ResponseIncludesKeyID(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	ctx := context.Background()
	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := svc.RotateKey(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	wantKeyID, err := domain.DeriveLicenseKeyID(res.PublicKey)
	if err != nil {
		t.Fatalf("DeriveLicenseKeyID: %v", err)
	}
	if res.KeyID != wantKeyID {
		t.Fatalf("RotateResult.KeyID = %q, want %q (derived from the returned PublicKey)", res.KeyID, wantKeyID)
	}

	// The stored entitlement's second key must resolve to the same id -- the
	// response's KeyID is not allowed to be a value invented independently of what
	// verification (which uses LicensePublicKey.ResolvedKeyID) will actually match.
	ents, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ents[0].PublicKeys) != 2 {
		t.Fatalf("want 2 public keys after rotate, got %d", len(ents[0].PublicKeys))
	}
	storedKeyID := ents[0].PublicKeys[1].KeyID
	if storedKeyID != wantKeyID {
		t.Fatalf("stored PublicKeys[1].KeyID = %q, want %q", storedKeyID, wantKeyID)
	}
}

// TestCreate_UsesInjectedClock proves Create's timestamps come from Service.Now, not a
// hidden time.Now() call -- provable without sleeping.
func TestCreate_UsesInjectedClock(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	fixed := time.Date(2030, 5, 4, 3, 2, 1, 0, time.UTC)
	svc.Now = func() time.Time { return fixed }

	res, err := svc.Create(context.Background(), "acme-corp", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.Entitlement.CreatedAt.Equal(fixed) {
		t.Fatalf("CreatedAt = %v, want %v", res.Entitlement.CreatedAt, fixed)
	}
	if !res.Entitlement.PublicKeys[0].IssuedAt.Equal(fixed) {
		t.Fatalf("IssuedAt = %v, want %v", res.Entitlement.PublicKeys[0].IssuedAt, fixed)
	}
}

// TestVerifyToken_NonPremiumTier_Denied is AMD-12 condition (2)'s tier half: an
// entitlement whose Tier is not "premium" must never authorize a download, even
// though its JWT signature verifies cleanly and it is neither revoked nor expired.
// Service.Create hardcodes Tier: tierPremium (so this shape can never come from
// Create itself today), but Tier is a normal persisted document field an operator or
// a future migration can set directly -- seedEntitlementDirect constructs exactly
// that document, bypassing Create, to prove VerifyToken checks the field's value
// instead of assuming presence of an entitlement means premium.
func TestVerifyToken_NonPremiumTier_Denied(t *testing.T) {
	store := newVerifyTestStore(t)
	svc := &Service{Store: store}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	k := newVerifyTestKey(t)
	seedEntitlementDirect(t, store, domain.LicenseEntitlement{
		CustomerID: "acme-corp",
		Tier:       "basic",
		CreatedAt:  now,
		PublicKeys: []domain.LicensePublicKey{{
			KeyID:     k.keyID,
			PublicKey: k.pubB64,
			IssuedAt:  now,
		}},
	})

	token := signToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour))

	_, err := svc.VerifyToken(context.Background(), token)
	if !errors.Is(err, ErrEntitlementTierDenied) {
		t.Fatalf("err = %v, want ErrEntitlementTierDenied", err)
	}
}

// TestVerifyToken_PremiumTier_Allowed is the control for the tier check above: an
// otherwise-identical entitlement whose Tier IS "premium" must still succeed, proving
// the tier check discriminates on the field's value rather than rejecting everything.
func TestVerifyToken_PremiumTier_Allowed(t *testing.T) {
	store := newVerifyTestStore(t)
	svc := &Service{Store: store}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	k := newVerifyTestKey(t)
	seedEntitlementDirect(t, store, domain.LicenseEntitlement{
		CustomerID: "acme-corp",
		Tier:       "premium",
		CreatedAt:  now,
		PublicKeys: []domain.LicensePublicKey{{
			KeyID:     k.keyID,
			PublicKey: k.pubB64,
			IssuedAt:  now,
		}},
	})

	token := signToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour))

	if _, err := svc.VerifyToken(context.Background(), token); err != nil {
		t.Fatalf("VerifyToken: %v, want success for a premium-tier entitlement", err)
	}
}

// TestVerifyToken_RevokedEntitlement_Distinguishable is design choice (b) for the
// revoked-vs-unknown-customer split: whole-entitlement revocation (Service.Revoke)
// must leave a customer distinguishable from one that never existed, so a token that
// verified successfully before revocation gets a different, more specific error
// afterward (ErrEntitlementRevoked) than the "unknown customerId" bucket
// (ErrNotFound) -- see the doc comment on ErrEntitlementRevoked and on
// domain.LicenseEntitlement.RevokedAt for why this project chose a tombstone over a
// hard delete.
func TestVerifyToken_RevokedEntitlement_Distinguishable(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	ctx := context.Background()

	res, err := svc.Create(ctx, "acme-corp", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	priv, err := base64.StdEncoding.DecodeString(res.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	k := verifyTestKey{priv: ed25519.PrivateKey(priv)}
	token := signToken(t, k, "acme-corp", "plugin-jira-cloud", now.Add(time.Hour))

	// Sanity: the token verifies before revocation.
	if _, err := svc.VerifyToken(ctx, token); err != nil {
		t.Fatalf("VerifyToken before revoke: %v", err)
	}

	if err := svc.Revoke(ctx, "acme-corp"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, err = svc.VerifyToken(ctx, token)
	if !errors.Is(err, ErrEntitlementRevoked) {
		t.Fatalf("err = %v, want ErrEntitlementRevoked", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("a revoked customer must not be indistinguishable from an unknown one: err = %v also satisfies ErrNotFound", err)
	}
}

// TestRevoke_Idempotent proves a second whole-entitlement revoke of an
// already-revoked customer succeeds without error -- matching RevokeKey's existing
// idempotent-revoke convention in this package (TestRevokeKey_Idempotent above)
// -- rather than returning ErrNotFound merely because the tombstoned record no
// longer matches "found and not yet revoked".
func TestRevoke_Idempotent(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	ctx := context.Background()
	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Revoke(ctx, "acme-corp"); err != nil {
		t.Fatalf("Revoke (1st): %v", err)
	}
	if err := svc.Revoke(ctx, "acme-corp"); err != nil {
		t.Fatalf("Revoke (2nd, idempotent): %v", err)
	}
}

// TestRevoke_UnknownCustomer_ErrNotFound proves Revoke still refuses a customerId
// that never had an entitlement at all, unchanged from before the tombstone rewrite --
// including on a store that has never held any entitlement document yet.
func TestRevoke_UnknownCustomer_ErrNotFound(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	if err := svc.Revoke(context.Background(), "nobody-corp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestList_ExcludesRevokedEntitlements proves the tombstone rewrite of Revoke did not
// regress GET /api/v1/licenses (backed by Service.List): before this change, a
// revoked entitlement was deleted outright and so necessarily absent from List's
// result; List must still omit it now that Revoke keeps the record on disk, since
// LicenseEntitlementResponse (internal/httpapi/responses.go) has no wire field that
// would let a caller tell a listed revoked entitlement apart from a live one.
func TestList_ExcludesRevokedEntitlements(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	ctx := context.Background()
	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create acme-corp: %v", err)
	}
	if _, err := svc.Create(ctx, "globex-corp", nil); err != nil {
		t.Fatalf("Create globex-corp: %v", err)
	}
	if err := svc.Revoke(ctx, "acme-corp"); err != nil {
		t.Fatalf("Revoke acme-corp: %v", err)
	}

	ents, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("List returned %d entitlements, want 1 (revoked acme-corp must be excluded): %+v", len(ents), ents)
	}
	if ents[0].CustomerID != "globex-corp" {
		t.Fatalf("List returned %q, want only globex-corp", ents[0].CustomerID)
	}
}

// TestRevoke_DoesNotDisturbOtherEntitlements proves Revoke's tombstone write only
// touches the target customer's RevokedAt: a second, unrelated entitlement in the same
// document must still verify successfully afterward.
func TestRevoke_DoesNotDisturbOtherEntitlements(t *testing.T) {
	svc := &Service{Store: newVerifyTestStore(t)}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := svc.Create(ctx, "acme-corp", nil); err != nil {
		t.Fatalf("Create acme-corp: %v", err)
	}
	globexRes, err := svc.Create(ctx, "globex-corp", nil)
	if err != nil {
		t.Fatalf("Create globex-corp: %v", err)
	}

	if err := svc.Revoke(ctx, "acme-corp"); err != nil {
		t.Fatalf("Revoke acme-corp: %v", err)
	}

	priv, err := base64.StdEncoding.DecodeString(globexRes.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	k := verifyTestKey{priv: ed25519.PrivateKey(priv)}
	token := signToken(t, k, "globex-corp", "plugin-jira-cloud", now.Add(time.Hour))

	if _, err := svc.VerifyToken(ctx, token); err != nil {
		t.Fatalf("VerifyToken for globex-corp after acme-corp was revoked: %v", err)
	}
}
