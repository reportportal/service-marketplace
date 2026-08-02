package domain

import (
	"encoding/json"
	"time"
)

// dateLayout is the calendar-date-only format the license entitlement schema declares
// (OpenAPI `format: date`) for issuedAt/expiresAt — a customer-facing license validity
// window, not a timestamp.
const dateLayout = "2006-01-02"

// Date is a calendar date (no time-of-day, no timezone) that marshals as
// docs/openapi/service-marketplace-v1.yaml declares it: "2006-01-02". A bare time.Time
// field marshals with a full RFC3339 timestamp, which is a wire-contract mismatch for
// any field the spec declares `format: date`.
type Date struct {
	time.Time
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Time.Format(dateLayout))
}

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		d.Time = time.Time{}
		return nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return err
	}
	d.Time = t
	return nil
}

type Category string

const (
	CategoryBugTracking   Category = "bug-tracking"
	CategoryNotifications Category = "notifications"
	CategoryAuthorization Category = "authorization"
	CategoryImport        Category = "import"
)

// AllCategories is the RP-defined controlled vocabulary (§6.2 of the marketplace plan):
// closed, operator-extended only, not author-extensible. It is the single source of
// truth ValidCategory checks against, and what
// internal/domain/category_vocabulary_test.go binds to the OpenAPI PluginCategory enum
// and the marketplace-manifest JSON Schema's category enum so the three cannot drift
// apart again.
var AllCategories = []Category{CategoryBugTracking, CategoryNotifications, CategoryAuthorization, CategoryImport}

func ValidCategory(c Category) bool {
	for _, v := range AllCategories {
		if c == v {
			return true
		}
	}
	return false
}

type AccessTier string

const (
	AccessPublic  AccessTier = "public"
	AccessPremium AccessTier = "premium"
)

type TrustTier string

const (
	TierOfficial TrustTier = "official"
	TierPartner  TrustTier = "partner"
)

type AdvisorySeverity string

const (
	SeverityLow      AdvisorySeverity = "low"
	SeverityMedium   AdvisorySeverity = "medium"
	SeverityHigh     AdvisorySeverity = "high"
	SeverityCritical AdvisorySeverity = "critical"
)

type Author struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Compatibility struct {
	ReportPortal string `json:"reportportal"`
}

type Manifest struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Description   string        `json:"description"`
	Author        Author        `json:"author"`
	License       string        `json:"license"`
	Category      Category      `json:"category"`
	Compatibility Compatibility `json:"compatibility"`
	Homepage      string        `json:"homepage,omitempty"`
	Access        AccessTier    `json:"access,omitempty"`
	ContactURL    string        `json:"contactUrl,omitempty"`
}

type IndexPlugin struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	LatestVersion string     `json:"latestVersion"`
	Description   string     `json:"description,omitempty"`
	Category      Category   `json:"category"`
	Access        AccessTier `json:"access"`
	Tier          TrustTier  `json:"tier"`
	// Versions is the plugin's full committed version set (every published
	// version, including blocked-but-not-removed ones -- blocking makes a
	// version un-installable, not uncommitted). This is the AMD-27
	// orphan-cleanup reference set: internal/lifecycle.OrphanCleanup treats a
	// plugins/{id}/versions/{v}/ (or private/plugins/{id}/versions/{v}/)
	// directory as a deletion candidate only if v is absent from here.
	// index.json documents written before this field existed omit it
	// entirely, decoding to a nil slice -- OrphanCleanup treats a
	// non-empty index whose entries all decode to zero versions as
	// reference data that "looks wrong" and refuses to run, rather than
	// reading every version directory in the registry as unreferenced.
	Versions []string `json:"versions,omitempty"`
}

type Index struct {
	Plugins []IndexPlugin `json:"plugins"`
	// Complete attests that this document was produced by a rebuild that
	// successfully read and resolved every known plugin.json (excluding
	// legitimately-tombstoned plugins, whose artifacts are already deleted by
	// the time a rebuild runs) -- not that the catalogue happens to look
	// plausible. internal/publish.Service.rebuildIndex sets this to true only
	// on the success path; if any plugin.json failed to read, failed to
	// unmarshal, or its latest version's manifest could not be resolved,
	// rebuildIndex aborts the whole rebuild and writes nothing at all, so a
	// document that IS on disk with Complete==false can only be:
	//   - one written before this field existed (decodes to the zero value,
	//     false, by construction -- no legacy-format special-casing needed), or
	//   - one written by hand / by something other than rebuildIndex.
	// internal/lifecycle.OrphanCleanup treats Complete==false, combined with
	// storage holding candidate version directories, as reference data it
	// cannot prove is exhaustive -- and refuses to delete anything on that
	// basis. This is deliberately a positive attestation ("I verified
	// everything") rather than a derived plausibility heuristic ("this looks
	// empty" / "this looks legacy-shaped"): a heuristic only catches the
	// shapes its author thought of, and a partial index missing one plugin
	// among many looks exactly as "plausible" as a genuinely complete one.
	Complete bool `json:"complete"`
}

type BlockedVersion struct {
	Version   string    `json:"version"`
	BlockedAt time.Time `json:"blockedAt"`
	Reason    string    `json:"reason"`
}

type VersionMeta struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"publishedAt,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
}

type SecurityAdvisory struct {
	Severity   AdvisorySeverity `json:"severity"`
	Text       string           `json:"text"`
	AttachedAt time.Time        `json:"attachedAt"`
}

type VersionState struct {
	Advisory *SecurityAdvisory `json:"advisory,omitempty"`
}

type PluginState struct {
	ID              string                  `json:"id"`
	Tier            TrustTier               `json:"tier"`
	LatestVersion   string                  `json:"latestVersion"`
	Versions        []VersionMeta           `json:"versions"`
	BlockedVersions []BlockedVersion        `json:"blockedVersions,omitempty"`
	Removed         *time.Time              `json:"removed,omitempty"`
	RemovalReason   string                  `json:"removalReason,omitempty"`
	RemovedBy       string                  `json:"removedBy,omitempty"`
	VersionStates   map[string]VersionState `json:"versionStates,omitempty"`
}

type PluginTombstone struct {
	Removed       time.Time `json:"removed"`
	RemovalReason string    `json:"removalReason"`
	RemovedBy     string    `json:"removedBy"`
	// Warnings is populated only by handleRemovePlugin, only when this
	// removal's downstream housekeeping (index rebuild, CDN invalidation)
	// failed after the tombstone itself had already committed. Every other
	// caller that builds a PluginTombstone (GetPlugin, ListVersions,
	// GetArtifact -- see catalogue.TombstoneFromState) leaves it empty: this
	// field describes what happened during a removal, not the tombstone's
	// persisted shape, and PluginTombstone is not itself a persisted
	// document (see wire_storage_separation_test.go).
	Warnings []string `json:"warnings,omitempty"`
}

// LicensePublicKey and LicenseEntitlement are dual-purpose: they are marshalled both
// as HTTP response bodies (see httpapi.LicensePublicKeyResponse/
// LicenseEntitlementResponse, which convert from these) and, unmarshalled, ARE the
// persisted document at auth/authorized_keys.json (see internal/license.Service.load/
// save). They deliberately keep the storage-era shape — full time.Time/RFC3339
// timestamps, matching every byte already on disk in any existing deployment — so a
// wire-contract fix (e.g. satisfying the OpenAPI `format: date` declaration on the
// response) never has to touch what gets read from or written to storage. Wire-only
// concerns (the `Date` date-only formatting, the OpenAPI-declared "issuedAt" name for
// what storage still calls CreatedAt) belong on the httpapi response types, not here.
//
// KID is likewise storage-only, for a different reason than the dates: it was never
// part of the wire contract at all (LicensePublicKey's OpenAPI schema has no "kid"
// property, and it never carried one). But every entitlement created or rotated by the
// release before this branch wrote a "kid" value into authorized_keys.json, and
// Service.load/save round-trips this type directly, so dropping the Go field — correct
// as a wire decision, since nothing on the wire ever read it — silently erases every
// existing "kid" from disk the next time *any* entitlement in the document is created,
// rotated or revoked (the whole document is unmarshalled and re-marshalled on every
// write, not just the changed entitlement). Keeping the field here preserves whatever
// is already on disk without reviving it on the wire. New keys deliberately leave it
// empty (see license.Service.Create/RotateKey) rather than resurrect the old
// randomID()-based generator: AMD-11 defines a real keyId as a deterministic
// `first 8 hex chars of SHA-256(publicKey)`, a different value under a different wire
// name ("keyId", not "kid") than the old opaque random one this field preserves. Every
// key issued between this fix and AMD-11 landing keeps kid empty, so AMD-11's
// migration only has one case to handle (absent) instead of two (absent, or present but
// wrong-scheme).
type LicensePublicKey struct {
	KID       string    `json:"kid,omitempty"`
	PublicKey string    `json:"publicKey"`
	IssuedAt  time.Time `json:"issuedAt"`
}

type LicenseEntitlement struct {
	CustomerID string             `json:"customerId"`
	Tier       string             `json:"tier"`
	CreatedAt  time.Time          `json:"createdAt,omitempty"`
	ExpiresAt  *time.Time         `json:"expiresAt,omitempty"`
	PublicKeys []LicensePublicKey `json:"publicKeys"`
}

type AuthorizedKeys struct {
	Entitlements []LicenseEntitlement `json:"entitlements"`
}
