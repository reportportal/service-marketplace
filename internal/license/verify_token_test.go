package license

// AMD-09 requires the HTTP layer to tell "your licence expired" apart from "you pasted
// the wrong key" apart from "this licence does not cover this plugin". It can only do
// that if this package keeps those conditions separable in the first place: before
// AMD-09, an unknown customerId and an entitlement whose window had closed both came
// back as ErrNotFound, which is precisely why the two collapsed into one 403 on the
// wire. These tests pin the distinction at its source.

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
)

// signLicenseJWT mints a licence token the way a customer's tooling would: EdDSA over
// the entitlement's private key, carrying the customerId/pluginId claims.
func signLicenseJWT(t *testing.T, privB64, customerID, pluginID string, exp time.Time) string {
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

func newServiceWithEntitlement(t *testing.T, customerID string, expiresAt *time.Time) (*Service, string) {
	t.Helper()
	store, _ := newLocalStore(t)
	svc := &Service{Store: store}
	res, err := svc.Create(context.Background(), customerID, expiresAt)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return svc, res.PrivateKey
}

func TestVerifyTokenAcceptsALiveEntitlement(t *testing.T) {
	svc, priv := newServiceWithEntitlement(t, "acme-corp", nil)
	tok := signLicenseJWT(t, priv, "acme-corp", "plugin-x", time.Now().Add(time.Hour))

	claims, err := svc.VerifyToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("VerifyToken on a live entitlement: %v", err)
	}
	if claims.CustomerID != "acme-corp" || claims.PluginID != "plugin-x" {
		t.Fatalf("verified claims = %+v, want customerId acme-corp / pluginId plugin-x", claims)
	}
}

// The AMD-09 split lives or dies here: an unknown customer is a 401-bucket error while
// an entitlement whose window has closed is a 403-bucket one, so the two must never be
// the same Go error.
func TestVerifyTokenDistinguishesUnknownCustomerFromExpiredEntitlement(t *testing.T) {
	past := time.Now().Add(-24 * time.Hour)
	svc, priv := newServiceWithEntitlement(t, "acme-corp", &past)

	expiredEnt := signLicenseJWT(t, priv, "acme-corp", "plugin-x", time.Now().Add(time.Hour))
	if _, err := svc.VerifyToken(context.Background(), expiredEnt); !errors.Is(err, ErrEntitlementExpired) {
		t.Fatalf("expired entitlement: err = %v, want ErrEntitlementExpired", err)
	}

	unknown := signLicenseJWT(t, priv, "globex-corp", "plugin-x", time.Now().Add(time.Hour))
	if _, err := svc.VerifyToken(context.Background(), unknown); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown customerId: err = %v, want ErrNotFound", err)
	}
	if errors.Is(ErrNotFound, ErrEntitlementExpired) || errors.Is(ErrEntitlementExpired, ErrNotFound) {
		t.Fatal("ErrNotFound and ErrEntitlementExpired must stay distinct error values")
	}
}

// A token the entitlement's key did not sign is a token problem, not an entitlement
// problem: it must not surface as ErrEntitlementExpired/ErrNotFound.
func TestVerifyTokenRejectsForeignSignature(t *testing.T) {
	svc, _ := newServiceWithEntitlement(t, "acme-corp", nil)
	_, foreign, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tok := signLicenseJWT(t, base64.StdEncoding.EncodeToString(foreign), "acme-corp", "plugin-x", time.Now().Add(time.Hour))

	_, err = svc.VerifyToken(context.Background(), tok)
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("foreign signature: err = %v, want auth.ErrUnauthorized", err)
	}
}

// An elapsed JWT exp is a token problem too (401 bucket), and it must be reported as
// one even when the entitlement behind it is perfectly live.
func TestVerifyTokenRejectsElapsedTokenExpiry(t *testing.T) {
	svc, priv := newServiceWithEntitlement(t, "acme-corp", nil)
	tok := signLicenseJWT(t, priv, "acme-corp", "plugin-x", time.Now().Add(-time.Hour))

	_, err := svc.VerifyToken(context.Background(), tok)
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("elapsed token exp: err = %v, want auth.ErrUnauthorized", err)
	}
}

// Entitlement state must stay behind the signature: a forged token naming a real
// customer whose entitlement has expired gets the token error, never the entitlement's.
func TestVerifyTokenChecksSignatureBeforeEntitlementState(t *testing.T) {
	past := time.Now().Add(-24 * time.Hour)
	svc, _ := newServiceWithEntitlement(t, "acme-corp", &past)
	_, foreign, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tok := signLicenseJWT(t, base64.StdEncoding.EncodeToString(foreign), "acme-corp", "plugin-x", time.Now().Add(time.Hour))

	_, err = svc.VerifyToken(context.Background(), tok)
	if errors.Is(err, ErrEntitlementExpired) {
		t.Fatal("an unverified token was told the entitlement's expiry state")
	}
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("err = %v, want auth.ErrUnauthorized", err)
	}
}
