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
	CategoryBugTracking    Category = "bug-tracking"
	CategoryNotifications  Category = "notifications"
	CategoryAuthorization  Category = "authorization"
	CategoryImport         Category = "import"
	CategoryOther          Category = "other"
)

func ValidCategory(c Category) bool {
	switch c {
	case CategoryBugTracking, CategoryNotifications, CategoryAuthorization, CategoryImport, CategoryOther:
		return true
	default:
		return false
	}
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
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Description   string          `json:"description"`
	Author        Author          `json:"author"`
	License       string          `json:"license"`
	Category      Category        `json:"category"`
	Compatibility Compatibility   `json:"compatibility"`
	Homepage      string          `json:"homepage,omitempty"`
	Access        AccessTier      `json:"access,omitempty"`
	ContactURL    string          `json:"contactUrl,omitempty"`
}

type IndexPlugin struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	LatestVersion  string     `json:"latestVersion"`
	Description    string     `json:"description,omitempty"`
	Category       Category   `json:"category"`
	Access         AccessTier `json:"access"`
	Tier           TrustTier  `json:"tier"`
}

type Index struct {
	Plugins []IndexPlugin `json:"plugins"`
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
	ID              string           `json:"id"`
	Tier            TrustTier        `json:"tier"`
	LatestVersion   string           `json:"latestVersion"`
	Versions        []VersionMeta    `json:"versions"`
	BlockedVersions []BlockedVersion `json:"blockedVersions,omitempty"`
	Removed         *time.Time       `json:"removed,omitempty"`
	RemovalReason   string           `json:"removalReason,omitempty"`
	RemovedBy       string           `json:"removedBy,omitempty"`
	VersionStates   map[string]VersionState `json:"versionStates,omitempty"`
}

type PluginTombstone struct {
	Removed       time.Time `json:"removed"`
	RemovalReason string    `json:"removalReason"`
	RemovedBy     string    `json:"removedBy"`
}

type LicensePublicKey struct {
	PublicKey string `json:"publicKey"`
	IssuedAt  Date   `json:"issuedAt"`
}

type LicenseEntitlement struct {
	CustomerID string             `json:"customerId"`
	Tier       string             `json:"tier"`
	IssuedAt   Date               `json:"issuedAt"`
	ExpiresAt  *Date              `json:"expiresAt,omitempty"`
	PublicKeys []LicensePublicKey `json:"publicKeys"`
}

type AuthorizedKeys struct {
	Entitlements []LicenseEntitlement `json:"entitlements"`
}
