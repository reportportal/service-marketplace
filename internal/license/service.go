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
)

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

func (s *Service) save(ctx context.Context, ak *domain.AuthorizedKeys) error {
	data, err := json.MarshalIndent(ak, "", "  ")
	if err != nil {
		return err
	}
	return storage.WriteWithRetry(ctx, s.Store, storage.PathAuthorizedKeys, func(existing []byte, gen int64) ([]byte, error) {
		return data, nil
	}, 5)
}

func (s *Service) List(ctx context.Context) ([]domain.LicenseEntitlement, error) {
	ak, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	return ak.Entitlements, nil
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
		Tier:       "premium",
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

func (s *Service) Revoke(ctx context.Context, customerID string) error {
	return storage.WriteWithRetry(ctx, s.Store, storage.PathAuthorizedKeys, func(data []byte, gen int64) ([]byte, error) {
		var ak domain.AuthorizedKeys
		if err := json.Unmarshal(data, &ak); err != nil {
			return nil, err
		}
		out := make([]domain.LicenseEntitlement, 0, len(ak.Entitlements))
		found := false
		for _, e := range ak.Entitlements {
			if e.CustomerID == customerID {
				found = true
				continue
			}
			out = append(out, e)
		}
		if !found {
			return nil, ErrNotFound
		}
		ak.Entitlements = out
		return json.MarshalIndent(ak, "", "  ")
	}, 5)
}

type RotateResult struct {
	CustomerID string `json:"customerId"`
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
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		PublicKey:  pubB64,
	}, nil
}

// PublicKeysForCustomer returns customerID's raw public key strings (no key metadata:
// this is a display/listing helper, not part of the verification path — see
// VerifyToken for that). Returns ErrNotFound if there is no entitlement for
// customerID, or ErrEntitlementExpired (AMD-10) if there is one but its ExpiresAt has
// passed as of s.now().
func (s *Service) PublicKeysForCustomer(ctx context.Context, customerID string) ([]string, error) {
	ak, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range ak.Entitlements {
		if e.CustomerID != customerID {
			continue
		}
		if e.ExpiresAt != nil && e.ExpiresAt.Before(s.now()) {
			return nil, ErrEntitlementExpired
		}
		keys := make([]string, 0, len(e.PublicKeys))
		for _, k := range e.PublicKeys {
			keys = append(keys, k.PublicKey)
		}
		return keys, nil
	}
	return nil, ErrNotFound
}

func (s *Service) FindCustomerByPlugin(ctx context.Context, customerID, pluginID string) ([]string, error) {
	return s.PublicKeysForCustomer(ctx, customerID)
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

	// AMD-10, flow step 7a: entitlement expiry is checked only AFTER signature
	// verification succeeds, against the now-trusted token — an unverified token
	// gets ErrLicenseTokenInvalid/ErrLicenseKeyInvalid above, never a chance to
	// reach this "is the entitlement itself still current" check.
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
