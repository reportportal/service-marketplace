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
	// ErrEntitlementExpired: the entitlement exists and the token verified against
	// its key, but its own ExpiresAt has elapsed. Distinct from ErrNotFound (no
	// entitlement for that customerId at all) because AMD-09 puts them on opposite
	// sides of the 401/403 split — the whole point of the table is that an operator
	// can tell "renew your licence" from "wrong key".
	ErrEntitlementExpired = errors.New("entitlement expired")
)

type Service struct {
	Store storage.ObjectStore
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
	now := time.Now().UTC()
	ent := domain.LicenseEntitlement{
		CustomerID: customerID,
		Tier:       "premium",
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		PublicKeys: []domain.LicensePublicKey{{
			PublicKey: base64.StdEncoding.EncodeToString(pub),
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
	now := time.Now().UTC()
	var pubB64 string
	err = storage.WriteWithRetry(ctx, s.Store, storage.PathAuthorizedKeys, func(data []byte, gen int64) ([]byte, error) {
		var ak domain.AuthorizedKeys
		if err := json.Unmarshal(data, &ak); err != nil {
			return nil, err
		}
		found := false
		for i, e := range ak.Entitlements {
			if e.CustomerID == customerID {
				found = true
				pubB64 = base64.StdEncoding.EncodeToString(pub)
				ak.Entitlements[i].PublicKeys = append(ak.Entitlements[i].PublicKeys, domain.LicensePublicKey{
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

// entitlementFor returns customerID's entitlement, or ErrNotFound when the document
// holds none. It deliberately reports nothing about the entitlement's state (expiry):
// that is only decided in VerifyToken, after a signature has proven the caller holds
// the matching private key.
func (s *Service) entitlementFor(ctx context.Context, customerID string) (*domain.LicenseEntitlement, error) {
	ak, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ak.Entitlements {
		if ak.Entitlements[i].CustomerID == customerID {
			return &ak.Entitlements[i], nil
		}
	}
	return nil, ErrNotFound
}

// VerifyToken is the single entry point for verifying a premium-artifact licence JWT.
// It keeps AMD-09's conditions separable instead of collapsing them: ErrNotFound for
// an unknown customerId, auth.ErrUnauthorized for a token that is unparseable, signed
// by a foreign key or past its own exp, and ErrEntitlementExpired for a verified token
// whose entitlement window has closed. httpapi owns turning those into status/code
// pairs.
//
// Ordering: the customerId claim is read unverified only to pick candidate keys (see
// auth.PeekUnverifiedCustomerID); the signature is verified against exactly those
// keys; only then is entitlement state consulted, so a forged token can never learn
// anything about a real entitlement.
func (s *Service) VerifyToken(ctx context.Context, token string) (*auth.LicenseClaims, error) {
	customerID, err := auth.PeekUnverifiedCustomerID(token)
	if err != nil {
		return nil, err
	}
	ent, err := s.entitlementFor(ctx, customerID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(ent.PublicKeys))
	for _, k := range ent.PublicKeys {
		keys = append(keys, k.PublicKey)
	}
	claims, err := auth.VerifyLicenseJWT(token, keys)
	if err != nil {
		return nil, err
	}
	if ent.ExpiresAt != nil && ent.ExpiresAt.Before(time.Now()) {
		return nil, ErrEntitlementExpired
	}
	return claims, nil
}
