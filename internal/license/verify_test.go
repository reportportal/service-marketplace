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
	"errors"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

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
