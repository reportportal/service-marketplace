package domain

import "time"

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
