package license

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	// ErrEntitlementExpired is AMD-10: the matching entitlement's ExpiresAt is
	// non-nil and earlier than the verification clock. Distinct from ErrNotFound
	// (no entitlement for that customer at all) and from the auth package's
	// ErrLicenseTokenExpired (the JWT's own short-lived "exp" claim) — AMD-09 maps
	// this one to 403 LICENSE_EXPIRED, not 401.
	ErrEntitlementExpired = errors.New("entitlement expired")
	// ErrKeyNotFound is AMD-11's "keyId absent" case for key-level revocation: no
	// key in the entitlement resolves (via domain.LicensePublicKey.ResolvedKeyID)
	// to the requested keyId.
	ErrKeyNotFound = errors.New("key not found")
	// ErrLastActiveKey is AMD-11's 422 case: the requested key is the
	// entitlement's last remaining non-revoked key. Revoking it would leave the
	// customer holding an entitlement no live key can ever verify against again;
	// whole-entitlement revocation (Service.Revoke) is the correct operation for
	// that, not key rotation.
	ErrLastActiveKey = errors.New("cannot revoke the entitlement's last active key")
	// ErrEntitlementRevoked is AMD-09 row 3's "JWT valid but entitlement revoked"
	// case: the token's signature verifies against a real, known entitlement, but
	// that entitlement has been revoked via Service.Revoke. Service.Revoke tombstones
	// the entitlement (sets RevokedAt) instead of deleting it -- see Revoke's doc
	// comment for why -- specifically so this is a distinct Go error from ErrNotFound
	// (no entitlement ever existed for that customer): AMD-09 puts unknown-customerId
	// at 401 LICENSE_JWT_INVALID but revoked-entitlement at 403
	// LICENSE_ENTITLEMENT_DENIED, and a client/operator being unable to tell "your
	// licence was revoked" apart from "your licence never existed" is precisely the
	// FR-A-05 message-quality gap AMD-09 exists to close.
	ErrEntitlementRevoked = errors.New("entitlement revoked")
	// ErrEntitlementTierDenied is AMD-12 condition (2)'s tier half: the matching
	// entitlement's Tier field is not "premium". Service.Create hardcodes
	// Tier: "premium" for every entitlement it issues today, but Tier is an ordinary
	// persisted document field -- an operator editing authorized_keys.json directly,
	// or a future migration/import path, can set it to anything, and a stored
	// document with a non-premium tier must not authorize a premium download just
	// because an entitlement happens to exist and its signature verifies.
	ErrEntitlementTierDenied = errors.New("entitlement tier does not grant premium access")
)

// tierPremium is the only Tier value Service.VerifyToken authorizes for a premium
// artifact download (AMD-12 condition (2)). Shared with Service.Create so the value
// an entitlement is stamped with at issuance and the value VerifyToken requires can
// never drift apart into two different literals.
const tierPremium = "premium"

type Service struct {
	Store storage.ObjectStore
	// Now returns the current time. nil means time.Now().UTC(). Every expiry this
	// package checks (AMD-10 entitlement expiry, and license JWT verification's own
	// exp handling via internal/auth) is driven through this instead of a hidden
	// time.Now() call, so it is provable against an injected clock in tests instead
	// of requiring real sleeps — matching this repo's existing convention (see
	// lifecycle.OrphanCleanup.Now).
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) load(ctx context.Context) (*domain.AuthorizedKeys, int64, error) {
	obj, err := s.Store.Read(ctx, storage.PathAuthorizedKeys)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return &domain.AuthorizedKeys{Entitlements: []domain.LicenseEntitlement{}}, 0, nil
		}
		return nil, 0, err
	}
	var ak domain.AuthorizedKeys
	if err := json.Unmarshal(obj.Data, &ak); err != nil {
		return nil, obj.Generation, err
	}
	return &ak, obj.Generation, nil
}

// List returns every LIVE entitlement (GET /api/v1/licenses). Revoked entitlements
// are deliberately excluded: Service.Revoke tombstones rather than deletes them (see
// its doc comment), and the wire response built from this (internal/httpapi's
// LicenseEntitlementResponse) has no field that would let a caller distinguish a
// listed revoked entitlement from a live one, so surfacing one here would silently
// resurrect it in the operator-facing listing. VerifyToken deliberately does NOT go
// through List/this filtering -- it reads the raw document via s.load so it can still
// see (and reject) a revoked entitlement's tombstone.
func (s *Service) List(ctx context.Context) ([]domain.LicenseEntitlement, error) {
	ak, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.LicenseEntitlement, 0, len(ak.Entitlements))
	for _, e := range ak.Entitlements {
		if e.RevokedAt != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

type CreateResult struct {
	Entitlement domain.LicenseEntitlement
	PrivateKey  string
}

func (s *Service) Create(ctx context.Context, customerID string, expiresAt *time.Time) (*CreateResult, error) {
	if ve := domain.ValidateCustomerID(customerID); ve != nil {
		return nil, ve
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	now := s.now()
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	keyID, err := domain.DeriveLicenseKeyID(pubB64)
	if err != nil {
		return nil, err
	}
	ent := domain.LicenseEntitlement{
		CustomerID: customerID,
		Tier:       tierPremium,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		PublicKeys: []domain.LicensePublicKey{{
			KeyID:     keyID,
			PublicKey: pubB64,
			IssuedAt:  now,
		}},
	}

	err = storage.WriteWithRetry(ctx, s.Store, storage.PathAuthorizedKeys, func(data []byte, gen int64) ([]byte, error) {
		var ak domain.AuthorizedKeys
		if len(data) > 0 {
			if err := json.Unmarshal(data, &ak); err != nil {
				return nil, err
			}
		}
		for _, e := range ak.Entitlements {
			if e.CustomerID == customerID {
				return nil, ErrConflict
			}
		}
		ak.Entitlements = append(ak.Entitlements, ent)
		return json.MarshalIndent(ak, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}
	return &CreateResult{Entitlement: ent, PrivateKey: base64.StdEncoding.EncodeToString(priv)}, nil
}

// Revoke implements whole-entitlement revocation (DELETE
// /api/v1/licenses/{customerId}) as a tombstone: it sets RevokedAt on customerID's
// entitlement rather than removing the entitlement from the document. Before this,
// Revoke deleted the record outright, which made a revoked customer indistinguishable
// from one that never had an entitlement at all -- both produced ErrNotFound from
// VerifyToken, which AMD-09 maps to 401 LICENSE_JWT_INVALID, contradicting AMD-09 row
// 3's requirement that a revoked (but real) entitlement return 403
// LICENSE_ENTITLEMENT_DENIED. See domain.LicenseEntitlement.RevokedAt's doc comment
// for the retention-cost tradeoff this accepts.
//
// Returns ErrNotFound if there is no entitlement for customerID at all (unchanged
// from before this rewrite). Revoking an already-revoked entitlement is idempotent:
// it succeeds without changing RevokedAt again, matching RevokeKey's existing
// idempotent-revoke convention below.
func (s *Service) Revoke(ctx context.Context, customerID string) error {
	now := s.now()
	return storage.WriteWithRetry(ctx, s.Store, storage.PathAuthorizedKeys, func(data []byte, gen int64) ([]byte, error) {
		var ak domain.AuthorizedKeys
		if len(data) > 0 {
			if err := json.Unmarshal(data, &ak); err != nil {
				return nil, err
			}
		}
		for i, e := range ak.Entitlements {
			if e.CustomerID != customerID {
				continue
			}
			if e.RevokedAt != nil {
				// Already revoked: idempotent no-op success.
				return json.MarshalIndent(ak, "", "  ")
			}
			ak.Entitlements[i].RevokedAt = &now
			return json.MarshalIndent(ak, "", "  ")
		}
		return nil, ErrNotFound
	}, 5)
}

type RotateResult struct {
	CustomerID string `json:"customerId"`
	// KeyID is AMD-11's keyId for the newly-rotated key: the same value
	// domain.DeriveLicenseKeyID derives from PublicKey (first 8 hex chars of
	// SHA-256(publicKey)), returned so the operator/client can address this exact key
	// with DELETE /api/v1/licenses/{customerId}/keys/{keyId} without recomputing it.
	KeyID      string `json:"keyId"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

func (s *Service) RotateKey(ctx context.Context, customerID string) (*RotateResult, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	now := s.now()
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	keyID, err := domain.DeriveLicenseKeyID(pubB64)
	if err != nil {
		return nil, err
	}
	err = storage.WriteWithRetry(ctx, s.Store, storage.PathAuthorizedKeys, func(data []byte, gen int64) ([]byte, error) {
		var ak domain.AuthorizedKeys
		if err := json.Unmarshal(data, &ak); err != nil {
			return nil, err
		}
		found := false
		for i, e := range ak.Entitlements {
			if e.CustomerID == customerID {
				found = true
				ak.Entitlements[i].PublicKeys = append(ak.Entitlements[i].PublicKeys, domain.LicensePublicKey{
					KeyID:     keyID,
					PublicKey: pubB64,
					IssuedAt:  now,
				})
				break
			}
		}
		if !found {
			return nil, ErrNotFound
		}
		return json.MarshalIndent(ak, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}
	return &RotateResult{
		CustomerID: customerID,
		KeyID:      keyID,
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		PublicKey:  pubB64,
	}, nil
}

// VerifyToken is the sanctioned, single entry point for verifying a premium-artifact
// license JWT (AMD-09/AMD-10/AMD-11/AMD-12's JWT-and-entitlement-expiry steps). It
// replaces the previous shape — httpapi parsing the token unverified to pick a
// customer, fetching that customer's keys, and only THEN verifying — with one function
// that carries the "nothing is authorized before the signature verifies" guarantee by
// construction, instead of leaving it up to every call site to get the ordering right.
//
// # Ordering guarantee
//
//  1. token's customerId claim is read WITHOUT verifying the signature
//     (auth.PeekUnverifiedCustomerID), purely to select which entitlement's keys are
//     candidates for step 2. This is not a privileged lookup: entitlement public keys
//     are not secret, so fetching the wrong customer's keys (because the claim was
//     forged) leaks nothing — it only ever produces a candidate set that a forged
//     signature will fail to verify against.
//  2. auth.VerifyLicenseJWT verifies the signature (AMD-11 kid-aware key selection)
//     against ONLY that candidate set. Nothing from step 1 is trusted as fact yet —
//     it was only ever a set of keys to try.
//  3. Only once step 2 succeeds are the VERIFIED claims used: AMD-10's entitlement
//     ExpiresAt is checked against s.now(), and the verified claims are returned.
//
// See internal/auth's license.go package doc comment for the lower-level half of this
// guarantee (why consulting the JWS `kid` header, unlike a payload claim, is sound
// before verification).
func (s *Service) VerifyToken(ctx context.Context, token string) (*auth.LicenseClaims, error) {
	if token == "" {
		return nil, auth.ErrLicenseTokenMissing
	}
	customerID, err := auth.PeekUnverifiedCustomerID(token)
	if err != nil {
		return nil, err
	}
	ak, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	var ent *domain.LicenseEntitlement
	for i := range ak.Entitlements {
		if ak.Entitlements[i].CustomerID == customerID {
			ent = &ak.Entitlements[i]
			break
		}
	}
	if ent == nil {
		// Unknown customerId: AMD-09 maps this to 401 LICENSE_JWT_INVALID, the
		// same bucket as an unparseable/bad-signature token — but this package
		// still distinguishes it from that (a different Go error value) rather
		// than pre-collapsing the two, since httpapi (a different chunk) owns
		// deciding the HTTP mapping, not this package.
		return nil, ErrNotFound
	}

	claims, err := auth.VerifyLicenseJWT(token, ent.PublicKeys, s.now)
	if err != nil {
		return nil, err
	}

	// AMD-12 condition (2) and AMD-10, flow step 7a: revocation, tier and expiry are
	// all entitlement STATE (never client-controlled JWT claims), but every one of
	// them is withheld until AFTER signature verification succeeds — nothing about a
	// real entitlement's state is exposed to a caller who has not proven possession
	// of a live private key for it, matching this function's overall "nothing is
	// authorized before the signature verifies" guarantee (see the package doc
	// comment above). An unverified token gets ErrLicenseTokenInvalid/
	// ErrLicenseKeyInvalid above and never reaches any of these checks.
	if ent.RevokedAt != nil {
		return nil, ErrEntitlementRevoked
	}
	if ent.Tier != tierPremium {
		return nil, ErrEntitlementTierDenied
	}
	if ent.ExpiresAt != nil && ent.ExpiresAt.Before(s.now()) {
		return nil, ErrEntitlementExpired
	}

	return claims, nil
}

// RevokeKey implements AMD-11 key-level revocation: it sets RevokedAt on exactly the
// key in customerID's entitlement whose domain.LicensePublicKey.ResolvedKeyID equals
// keyID (the derived, authoritative id — never a possibly-empty/stale stored KeyID
// field, see LicensePublicKey.ResolvedKeyID's doc comment). This is distinct from
// whole-entitlement revocation (Service.Revoke / DELETE /api/v1/licenses/{customerId}),
// which is unchanged and removes the entitlement outright.
//
// Returns ErrNotFound if there is no entitlement for customerID, ErrKeyNotFound if no
// key resolves to keyID, and ErrLastActiveKey if keyID is the entitlement's last
// remaining non-revoked key (AMD-11: whole-entitlement revocation must be used
// instead, so a customer is never left holding an entitlement with zero live keys).
// Revoking an already-revoked key is idempotent: it succeeds without changing
// RevokedAt again.
func (s *Service) RevokeKey(ctx context.Context, customerID, keyID string) error {
	now := s.now()
	return storage.WriteWithRetry(ctx, s.Store, storage.PathAuthorizedKeys, func(data []byte, gen int64) ([]byte, error) {
		var ak domain.AuthorizedKeys
		if len(data) > 0 {
			if err := json.Unmarshal(data, &ak); err != nil {
				return nil, err
			}
		}
		for i, e := range ak.Entitlements {
			if e.CustomerID != customerID {
				continue
			}
			targetIdx := -1
			nonRevoked := 0
			for j, k := range e.PublicKeys {
				id, derr := k.ResolvedKeyID()
				if derr != nil {
					continue
				}
				if k.RevokedAt == nil {
					nonRevoked++
				}
				if id == keyID {
					targetIdx = j
				}
			}
			if targetIdx == -1 {
				return nil, ErrKeyNotFound
			}
			if ak.Entitlements[i].PublicKeys[targetIdx].RevokedAt != nil {
				// Already revoked: idempotent no-op success.
				return json.MarshalIndent(ak, "", "  ")
			}
			if nonRevoked <= 1 {
				return nil, ErrLastActiveKey
			}
			ak.Entitlements[i].PublicKeys[targetIdx].RevokedAt = &now
			return json.MarshalIndent(ak, "", "  ")
		}
		return nil, ErrNotFound
	}, 5)
}
