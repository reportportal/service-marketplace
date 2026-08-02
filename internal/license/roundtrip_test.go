package license

// domain.LicenseEntitlement / domain.LicensePublicKey are dual-purpose: they are the
// wire response bodies for the /api/v1/licenses endpoints AND the literal persisted
// document at auth/authorized_keys.json (see Service.load/save). A change that is
// correct for the wire (e.g. satisfying the OpenAPI `format: date` declaration) can
// silently rewrite what is on disk if it is made on the same Go type that json.Marshal
// uses for storage. These tests pin both directions of that compatibility:
//
//   - a document written by the release before this fix must still load correctly
//     (upgrade safety)
//   - a document written by this code must still parse under the previous release's
//     struct shape (rollback safety)
//
// Bytes are seeded/verified as literal JSON, not by constructing and marshalling
// today's domain.LicenseEntitlement — round-tripping the current type through itself
// would prove nothing about compatibility with what is actually already on disk.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/storage"
)

// previousReleaseLicensePublicKey and previousReleaseLicenseEntitlement mirror the
// struct shape domain.LicensePublicKey/LicenseEntitlement had before this change:
// full RFC3339 timestamps (encoding/json's default time.Time marshalling) and a "kid"
// field on each public key. That is what every existing deployment's
// auth/authorized_keys.json contains today, and what the previous release's binary
// would deserialize a rolled-back-to document with.
type previousReleaseLicensePublicKey struct {
	KID       string    `json:"kid,omitempty"`
	PublicKey string    `json:"publicKey"`
	IssuedAt  time.Time `json:"issuedAt"`
}

type previousReleaseLicenseEntitlement struct {
	CustomerID string                            `json:"customerId"`
	Tier       string                            `json:"tier"`
	CreatedAt  time.Time                         `json:"createdAt,omitempty"`
	ExpiresAt  *time.Time                        `json:"expiresAt,omitempty"`
	PublicKeys []previousReleaseLicensePublicKey `json:"publicKeys"`
}

type previousReleaseAuthorizedKeys struct {
	Entitlements []previousReleaseLicenseEntitlement `json:"entitlements"`
}

func newLocalStore(t *testing.T) (*storage.LocalStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "", "")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store, root
}

func authorizedKeysPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(storage.PathAuthorizedKeys))
}

// TestLoad_ReadsDocumentWrittenByPreviousRelease seeds the exact bytes the previous
// release wrote — RFC3339 timestamps under "createdAt"/"issuedAt", plus a "kid" this
// code no longer has a field for — and asserts today's code still loads every value
// correctly.
func TestLoad_ReadsDocumentWrittenByPreviousRelease(t *testing.T) {
	store, root := newLocalStore(t)

	createdAt := time.Now().UTC().AddDate(-1, 0, 0).Truncate(time.Second)
	expiresAt := time.Now().UTC().AddDate(3, 0, 0).Truncate(time.Second)
	issuedAt := createdAt

	seed := fmt.Sprintf(`{
  "entitlements": [
    {
      "customerId": "acme-corp",
      "tier": "premium",
      "createdAt": %q,
      "expiresAt": %q,
      "publicKeys": [
        {
          "kid": "S3JhZ2Vy",
          "publicKey": "3q2+7w==",
          "issuedAt": %q
        }
      ]
    }
  ]
}`, createdAt.Format(time.RFC3339), expiresAt.Format(time.RFC3339), issuedAt.Format(time.RFC3339))

	if err := os.MkdirAll(filepath.Dir(authorizedKeysPath(root)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(authorizedKeysPath(root), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed authorized_keys.json: %v", err)
	}

	svc := &Service{Store: store}

	ents, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("want 1 entitlement, got %d: %+v", len(ents), ents)
	}
	e := ents[0]
	if e.CustomerID != "acme-corp" || e.Tier != "premium" {
		t.Fatalf("entitlement identity lost: %+v", e)
	}
	if !e.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %v, want %v (a document written by the previous release must still load with its issued date intact)", e.CreatedAt, createdAt)
	}
	if e.ExpiresAt == nil || !e.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", e.ExpiresAt, expiresAt)
	}
	if len(e.PublicKeys) != 1 || e.PublicKeys[0].PublicKey != "3q2+7w==" {
		t.Fatalf("PublicKeys lost: %+v", e.PublicKeys)
	}
	if !e.PublicKeys[0].IssuedAt.Equal(issuedAt) {
		t.Fatalf("PublicKeys[0].IssuedAt = %v, want %v", e.PublicKeys[0].IssuedAt, issuedAt)
	}
}

// TestCreate_PreservesExistingEntitlementKidOnRewrite seeds a document, in the
// previous release's shape, containing an entitlement whose public key carries a "kid"
// — exactly what every entitlement created or rotated before this branch wrote to
// auth/authorized_keys.json. It then calls Service.Create for a *different* customer:
// Create's WriteWithRetry callback unmarshals the whole document, appends the new
// entitlement, and marshals the whole document back — so every existing entitlement's
// bytes round-trip through whatever Go type Service uses, whether or not that
// entitlement is the one being changed. If domain.LicensePublicKey has no field for
// "kid", the unmarshal silently drops it and the marshal never writes it back: the
// pre-existing entitlement's kid is gone from disk after this call, with no error.
func TestCreate_PreservesExistingEntitlementKidOnRewrite(t *testing.T) {
	store, root := newLocalStore(t)

	issuedAt := time.Now().UTC().AddDate(-1, 0, 0).Truncate(time.Second)

	seed := fmt.Sprintf(`{
  "entitlements": [
    {
      "customerId": "acme-corp",
      "tier": "premium",
      "createdAt": %q,
      "publicKeys": [
        {
          "kid": "S3JhZ2Vy",
          "publicKey": "3q2+7w==",
          "issuedAt": %q
        }
      ]
    }
  ]
}`, issuedAt.Format(time.RFC3339), issuedAt.Format(time.RFC3339))

	if err := os.MkdirAll(filepath.Dir(authorizedKeysPath(root)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(authorizedKeysPath(root), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed authorized_keys.json: %v", err)
	}

	svc := &Service{Store: store}

	// Create a second, unrelated entitlement. This forces Service.Create to read,
	// unmarshal, re-marshal and write the *whole* document, including acme-corp's
	// untouched entitlement.
	if _, err := svc.Create(context.Background(), "globex-corp", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	raw, err := os.ReadFile(authorizedKeysPath(root))
	if err != nil {
		t.Fatalf("reading written document: %v", err)
	}

	var doc previousReleaseAuthorizedKeys
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing rewritten document: %v\nraw: %s", err, raw)
	}
	var acme *previousReleaseLicenseEntitlement
	for i := range doc.Entitlements {
		if doc.Entitlements[i].CustomerID == "acme-corp" {
			acme = &doc.Entitlements[i]
		}
	}
	if acme == nil {
		t.Fatalf("acme-corp entitlement lost entirely on rewrite: %s", raw)
	}
	if len(acme.PublicKeys) != 1 || acme.PublicKeys[0].KID != "S3JhZ2Vy" {
		t.Fatalf("acme-corp's public key kid was silently dropped on an unrelated Create's document rewrite: got %+v, want kid %q\nraw: %s", acme.PublicKeys, "S3JhZ2Vy", raw)
	}
}

// TestSave_WritesDocumentThePreviousReleaseCanStillRead exercises Service.Create
// against a real LocalStore, then parses the bytes it actually wrote using the
// previous release's struct shape — the rollback direction: if the deployment is
// rolled back after this code has written new entitlements, the old binary must still
// be able to read them.
func TestSave_WritesDocumentThePreviousReleaseCanStillRead(t *testing.T) {
	store, root := newLocalStore(t)
	svc := &Service{Store: store}

	expires := time.Now().UTC().AddDate(1, 0, 0)
	if _, err := svc.Create(context.Background(), "acme-corp", &expires); err != nil {
		t.Fatalf("Create: %v", err)
	}

	raw, err := os.ReadFile(authorizedKeysPath(root))
	if err != nil {
		t.Fatalf("reading written document: %v", err)
	}

	var old previousReleaseAuthorizedKeys
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("a document written by this code must still parse under the previous release's struct shape (rollback safety): %v\nraw: %s", err, raw)
	}
	if len(old.Entitlements) != 1 {
		t.Fatalf("want 1 entitlement, got %d: %s", len(old.Entitlements), raw)
	}
	e := old.Entitlements[0]
	if e.CustomerID != "acme-corp" || e.Tier != "premium" {
		t.Fatalf("entitlement identity lost on rollback read: %+v", e)
	}
	if e.CreatedAt.IsZero() {
		t.Fatalf("CreatedAt lost on the previous release's read path (rollback would silently erase every entitlement's issued date): %s", raw)
	}
	if e.ExpiresAt == nil || !e.ExpiresAt.Equal(expires) {
		t.Fatalf("ExpiresAt = %v, want %v: %s", e.ExpiresAt, expires, raw)
	}
	if len(e.PublicKeys) != 1 || e.PublicKeys[0].IssuedAt.IsZero() {
		t.Fatalf("PublicKeys[0].IssuedAt lost on rollback read: %+v", e.PublicKeys)
	}
}
