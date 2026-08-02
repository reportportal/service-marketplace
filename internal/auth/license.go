package auth

// Premium-artifact license JWT verification (AMD-09/AMD-10/AMD-11/AMD-12).
//
// # Trust ordering
//
// The signature must be verified before ANY claim — including the payload's
// customerId/pluginId/exp — is trusted for anything. VerifyLicenseJWT enforces this
// directly: it never reads a payload claim off a token it has not just successfully
// verified against a specific key.
//
// The one claim-shaped value this file reads before verification is the JWS
// PROTECTED HEADER's `kid` (see peekKeyID / the kid handling in VerifyLicenseJWT). That
// is sound, unlike reading an unverified PAYLOAD claim, because the protected header is
// itself covered by the signature: an attacker who edits `kid` without the matching
// private key produces a token whose signature check fails outright, not a token that
// verifies with attacker-chosen behaviour. `kid` therefore can only ever narrow which
// key gets *tried*; it can never cause a forged token to verify.
//
// # The customerId chicken-and-egg problem
//
// Verification needs candidate keys, and this package has none of its own storage — the
// caller (internal/license.Service.VerifyToken) has to fetch a customer's keys before
// calling VerifyLicenseJWT, and the only way to know WHICH customer's keys to fetch is
// to read the token's (unverified, at that point) customerId claim. PeekUnverifiedCustomerID
// exists for exactly that lookup, and ONLY that: it is a deliberately narrow escape
// hatch from the "verify before trusting a claim" rule above, sound if and only if
// nothing is authorized by the lookup itself — the fetched keys are just a candidate
// set that verification (this file) still has to accept or reject. A forged customerId
// claim used this way costs an attacker nothing but a set of keys that will fail to
// verify their forged signature; it is not, and must never become, a way to skip
// signature verification. See internal/license.Service.VerifyToken's doc comment for
// where that guarantee is actually assembled end to end.

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/reportportal/service-marketplace/internal/domain"
)

// Typed license-JWT verification errors (AMD-09's table). This package only needs to
// keep these distinctions separable — httpapi (a different chunk) owns mapping them
// onto AMD-09's status/code pairs.
var (
	// ErrLicenseTokenMissing is an empty/blank token string.
	ErrLicenseTokenMissing = errors.New("license: token missing")
	// ErrLicenseTokenInvalid covers every signature-verification failure: malformed
	// compact serialization, an unparseable header/payload, a signature that does not
	// verify against any candidate key, or a payload missing a required claim.
	ErrLicenseTokenInvalid = errors.New("license: token unparseable or signature invalid")
	// ErrLicenseTokenExpired is the JWT's own "exp" claim having elapsed. This is
	// distinct from AMD-10's entitlement-level ExpiresAt check, which
	// internal/license.Service.VerifyToken performs separately using the verified
	// claims this package returns — the two expirations have different lifecycles
	// (a short-lived token vs. a whole entitlement's validity window) and AMD-09 maps
	// them to different codes (LICENSE_JWT_INVALID vs LICENSE_EXPIRED).
	ErrLicenseTokenExpired = errors.New("license: token expired")
	// ErrLicenseKeyInvalid is AMD-11's "unknown or revoked kid": the token carries a
	// `kid` header that does not exactly match a non-revoked candidate key. Per AMD-11
	// this is a hard failure — VerifyLicenseJWT must never fall through to trying
	// other keys when a kid was present but didn't resolve to a usable one.
	ErrLicenseKeyInvalid = errors.New("license: unknown or revoked key id")
)

// LicenseClaims is the verified content of a premium-artifact license JWT. Every field
// is populated only after VerifyLicenseJWT has verified the token's signature — see
// this file's package doc comment.
type LicenseClaims struct {
	CustomerID string
	PluginID   string
	Exp        time.Time
}

// PeekUnverifiedCustomerID reads the `customerId` claim from token WITHOUT verifying
// its signature. See this file's package doc comment ("The customerId
// chicken-and-egg problem") for the single sound use of the result: selecting which
// entitlement's keys to pass to VerifyLicenseJWT as candidates. The returned value must
// never be used to authorize anything by itself.
func PeekUnverifiedCustomerID(token string) (string, error) {
	parsed, err := jwt.Parse([]byte(token), jwt.WithVerify(false), jwt.WithValidate(false))
	if err != nil {
		return "", ErrLicenseTokenInvalid
	}
	cid, _ := parsed.Get("customerId")
	customerID, _ := cid.(string)
	return customerID, nil
}

// peekKeyID reads the JWS protected header's `kid` value without verifying the
// signature. This is safe to consult before verification for key SELECTION only — see
// this file's package doc comment for why the protected header, unlike a payload
// claim, cannot be forged without also invalidating the signature.
func peekKeyID(token string) (string, error) {
	msg, err := jws.Parse([]byte(token))
	if err != nil {
		return "", err
	}
	sigs := msg.Signatures()
	if len(sigs) == 0 || sigs[0].ProtectedHeaders() == nil {
		return "", nil
	}
	return sigs[0].ProtectedHeaders().KeyID(), nil
}

// VerifyLicenseJWT verifies token against keys — a candidate set, normally one
// customer's entitlement.PublicKeys, see PeekUnverifiedCustomerID — and returns the
// verified claims.
//
// Key selection (AMD-11): if the token carries a `kid` header, EXACTLY the candidate
// key whose domain.LicensePublicKey.ResolvedKeyID equals it, and whose RevokedAt is
// nil, is attempted; an unknown or revoked kid is ErrLicenseKeyInvalid and NEVER falls
// through to trying other keys. A kid-less token (migration fallback) tries each
// non-revoked candidate key in order, accepting the first whose signature verifies.
//
// Trust ordering: no candidate key is chosen because of anything read from the token's
// PAYLOAD, and no payload claim (customerId, pluginId, exp) is read, returned, or
// trusted until the ed25519 signature has actually verified against the selected key.
// See this file's package doc comment for the full guarantee, including why consulting
// the (signature-covered) `kid` header before that point is sound.
//
// now, if non-nil, is used as the verification clock for the JWT's own "exp" claim, so
// expiry is testable without sleeping; nil means time.Now().UTC() — the same
// nil-means-wall-clock convention this repo already uses (see
// lifecycle.OrphanCleanup.Now).
func VerifyLicenseJWT(token string, keys []domain.LicensePublicKey, now func() time.Time) (*LicenseClaims, error) {
	if token == "" {
		return nil, ErrLicenseTokenMissing
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	kid, err := peekKeyID(token)
	if err != nil {
		return nil, ErrLicenseTokenInvalid
	}

	var candidates []domain.LicensePublicKey
	if kid != "" {
		var match *domain.LicensePublicKey
		for i := range keys {
			id, derr := keys[i].ResolvedKeyID()
			if derr != nil {
				continue
			}
			if id == kid {
				k := keys[i]
				match = &k
				break
			}
		}
		if match == nil || match.RevokedAt != nil {
			return nil, ErrLicenseKeyInvalid
		}
		candidates = []domain.LicensePublicKey{*match}
	} else {
		for _, k := range keys {
			if k.RevokedAt == nil {
				candidates = append(candidates, k)
			}
		}
	}

	var lastErr error
	for _, k := range candidates {
		raw, derr := base64.StdEncoding.DecodeString(k.PublicKey)
		if derr != nil {
			lastErr = derr
			continue
		}
		if len(raw) != ed25519.PublicKeySize {
			lastErr = ErrLicenseTokenInvalid
			continue
		}
		pub := ed25519.PublicKey(raw)
		parsed, perr := jwt.Parse([]byte(token),
			jwt.WithKey(jwa.EdDSA, pub),
			jwt.WithValidate(true),
			jwt.WithClock(jwt.ClockFunc(now)),
		)
		if perr != nil {
			lastErr = perr
			continue
		}
		// Signature verified against a key we know belongs to the candidate set.
		// Only now are payload claims trusted.
		cid, _ := parsed.Get("customerId")
		pid, _ := parsed.Get("pluginId")
		customerID, _ := cid.(string)
		pluginID, _ := pid.(string)
		if customerID == "" || pluginID == "" {
			return nil, ErrLicenseTokenInvalid
		}
		return &LicenseClaims{CustomerID: customerID, PluginID: pluginID, Exp: parsed.Expiration()}, nil
	}
	if lastErr != nil && errors.Is(lastErr, jwt.ErrTokenExpired()) {
		return nil, ErrLicenseTokenExpired
	}
	return nil, ErrLicenseTokenInvalid
}
