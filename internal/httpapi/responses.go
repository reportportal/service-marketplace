package httpapi

// Wire response types for docs/openapi/service-marketplace-v1.yaml. Handlers used to
// build these ad hoc as map[string]any literals; a typo in a map key is exactly the
// same silent-drift risk as a missing struct tag, it just has no compiler or test to
// catch it. Naming these lets internal/httpapi/wire_contract_test.go bind every one of
// them to its schema.

import (
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
)

// AuthConfigResponse — GET /api/v1/auth/config
type AuthConfigResponse struct {
	GithubEnabled     bool `json:"githubEnabled"`
	AdminLoginEnabled bool `json:"adminLoginEnabled"`
}

// AuthTokenResponse — POST /api/v1/auth/login
type AuthTokenResponse struct {
	AccessToken string `json:"accessToken"`
	TokenType   string `json:"tokenType"`
	ExpiresIn   int    `json:"expiresIn"`
}

// LicensePublicKeyResponse and LicenseEntitlementResponse are the wire shapes for the
// OpenAPI LicensePublicKey/LicenseEntitlement schemas, both of which declare
// `issuedAt`/`expiresAt` as `format: date`. They are deliberately separate Go types
// from domain.LicensePublicKey/LicenseEntitlement — those are the persisted document
// at auth/authorized_keys.json (full RFC3339 timestamps, matching every existing
// deployment's on-disk bytes) — so satisfying the wire's date-only format can never
// silently change what gets read from or written to storage. Build these with
// newLicenseEntitlementResponse; do not marshal the domain types directly onto the
// wire.
type LicensePublicKeyResponse struct {
	PublicKey string      `json:"publicKey"`
	IssuedAt  domain.Date `json:"issuedAt"`
}

type LicenseEntitlementResponse struct {
	CustomerID string                     `json:"customerId"`
	Tier       string                     `json:"tier"`
	IssuedAt   domain.Date                `json:"issuedAt"`
	ExpiresAt  *domain.Date               `json:"expiresAt,omitempty"`
	PublicKeys []LicensePublicKeyResponse `json:"publicKeys"`
}

// newLicenseEntitlementResponse converts the persisted domain.LicenseEntitlement into
// its wire representation. domain.LicenseEntitlement.CreatedAt is what storage calls
// the entitlement's issue date; the OpenAPI schema calls it "issuedAt" — the rename
// happens here, on the wire boundary, not on the persisted field.
func newLicenseEntitlementResponse(e domain.LicenseEntitlement) LicenseEntitlementResponse {
	keys := make([]LicensePublicKeyResponse, len(e.PublicKeys))
	for i, k := range e.PublicKeys {
		keys[i] = LicensePublicKeyResponse{PublicKey: k.PublicKey, IssuedAt: domain.Date{Time: k.IssuedAt}}
	}
	var expires *domain.Date
	if e.ExpiresAt != nil {
		expires = &domain.Date{Time: *e.ExpiresAt}
	}
	return LicenseEntitlementResponse{
		CustomerID: e.CustomerID,
		Tier:       e.Tier,
		IssuedAt:   domain.Date{Time: e.CreatedAt},
		ExpiresAt:  expires,
		PublicKeys: keys,
	}
}

// LicenseEntitlementListResponse — GET /api/v1/licenses
type LicenseEntitlementListResponse struct {
	Entitlements []LicenseEntitlementResponse `json:"entitlements"`
}

// CreateLicenseResponse — POST /api/v1/licenses. allOf(LicenseEntitlement, {privateKey}).
type CreateLicenseResponse struct {
	CustomerID string                     `json:"customerId"`
	Tier       string                     `json:"tier"`
	IssuedAt   domain.Date                `json:"issuedAt"`
	ExpiresAt  *domain.Date               `json:"expiresAt,omitempty"`
	PublicKeys []LicensePublicKeyResponse `json:"publicKeys"`
	PrivateKey string                     `json:"privateKey"`
}

// PluginListResponse — GET /api/v1/plugins
type PluginListResponse struct {
	Plugins []domain.IndexPlugin `json:"plugins"`
}

// PluginDetailResponse — GET /api/v1/plugins/{pluginId}. allOf(PluginManifestFields,
// {tier, latestVersion}).
type PluginDetailResponse struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Version       string               `json:"version"`
	Description   string               `json:"description"`
	Author        domain.Author        `json:"author"`
	License       string               `json:"license"`
	Category      domain.Category      `json:"category"`
	Compatibility domain.Compatibility `json:"compatibility"`
	Homepage      string               `json:"homepage,omitempty"`
	Access        domain.AccessTier    `json:"access,omitempty"`
	ContactURL    string               `json:"contactUrl,omitempty"`
	Tier          domain.TrustTier     `json:"tier"`
	LatestVersion string               `json:"latestVersion"`
}

// PluginVersionSummary is one entry of PluginVersionListResponse.versions.
type PluginVersionSummary struct {
	Version     string     `json:"version"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	Blocked     bool       `json:"blocked"`
	BlockedAt   *time.Time `json:"blockedAt,omitempty"`
	BlockReason string     `json:"blockReason,omitempty"`
}

// PluginVersionListResponse — GET /api/v1/plugins/{pluginId}/versions
type PluginVersionListResponse struct {
	PluginID string                 `json:"pluginId"`
	Versions []PluginVersionSummary `json:"versions"`
}

// PluginVersionDetailResponse — GET /api/v1/plugins/{pluginId}/versions/{version}.
// allOf(PluginManifestFields, {tier, blocked, blockedAt, blockReason, advisory, sha256,
// changelogUrl, screenshotUrls}). Unlike PluginDetail this schema has no latestVersion.
type PluginVersionDetailResponse struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Version       string               `json:"version"`
	Description   string               `json:"description"`
	Author        domain.Author        `json:"author"`
	License       string               `json:"license"`
	Category      domain.Category      `json:"category"`
	Compatibility domain.Compatibility `json:"compatibility"`
	Homepage      string               `json:"homepage,omitempty"`
	Access        domain.AccessTier    `json:"access,omitempty"`
	ContactURL    string               `json:"contactUrl,omitempty"`

	Tier           domain.TrustTier         `json:"tier"`
	Blocked        bool                     `json:"blocked"`
	BlockedAt      *time.Time               `json:"blockedAt,omitempty"`
	BlockReason    string                   `json:"blockReason,omitempty"`
	Advisory       *domain.SecurityAdvisory `json:"advisory,omitempty"`
	SHA256         string                   `json:"sha256"`
	ChangelogURL   *string                  `json:"changelogUrl,omitempty"`
	ScreenshotURLs []string                 `json:"screenshotUrls"`
}

// PremiumArtifactResponse — GET .../artifact for a premium plugin.
type PremiumArtifactResponse struct {
	DownloadURL string    `json:"downloadUrl"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// BlockedArtifactErrorResponse — 403 body of GET .../artifact for a blocked version.
type BlockedArtifactErrorResponse struct {
	Blocked   bool      `json:"blocked"`
	BlockedAt time.Time `json:"blockedAt"`
	Reason    string    `json:"reason"`
}

// PluginOperatorStateResponse — PATCH /api/v1/plugins/{pluginId}
type PluginOperatorStateResponse struct {
	ID              string                  `json:"id"`
	Tier            domain.TrustTier        `json:"tier"`
	LatestVersion   string                  `json:"latestVersion"`
	BlockedVersions []domain.BlockedVersion `json:"blockedVersions,omitempty"`
}
