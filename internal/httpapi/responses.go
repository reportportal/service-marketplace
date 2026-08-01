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

// PluginListItemResponse is the wire shape for the OpenAPI PluginListItem schema.
// Deliberately a separate Go type from domain.IndexPlugin: that type is the literal
// persisted document at index.json (see internal/catalogue.Service.loadIndex and
// internal/publish.Service.rebuildIndex, both of which marshal/unmarshal it directly),
// so a wire-only change made on it (a renamed field, a computed field the client
// wants) would silently rewrite index.json. Build these with newPluginListItemResponse.
type PluginListItemResponse struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	LatestVersion string            `json:"latestVersion"`
	Description   string            `json:"description,omitempty"`
	Category      domain.Category   `json:"category"`
	Access        domain.AccessTier `json:"access"`
	Tier          domain.TrustTier  `json:"tier"`
}

func newPluginListItemResponse(p domain.IndexPlugin) PluginListItemResponse {
	return PluginListItemResponse{
		ID: p.ID, Name: p.Name, LatestVersion: p.LatestVersion, Description: p.Description,
		Category: p.Category, Access: p.Access, Tier: p.Tier,
	}
}

// PluginListResponse — GET /api/v1/plugins
type PluginListResponse struct {
	Plugins []PluginListItemResponse `json:"plugins"`
}

// BlockedVersionResponse is the wire shape for the OpenAPI BlockedVersion schema.
// Deliberately a separate Go type from domain.BlockedVersion: that type is the literal
// persisted shape inside plugins/{id}/plugin.json (see domain.PluginState.
// BlockedVersions, written/read directly by internal/lifecycle.Service), so a wire-only
// change made on it would silently rewrite plugin.json. Build these with
// newBlockedVersionResponse.
type BlockedVersionResponse struct {
	Version   string    `json:"version"`
	BlockedAt time.Time `json:"blockedAt"`
	Reason    string    `json:"reason"`
}

func newBlockedVersionResponse(bv domain.BlockedVersion) BlockedVersionResponse {
	return BlockedVersionResponse{Version: bv.Version, BlockedAt: bv.BlockedAt, Reason: bv.Reason}
}

// SecurityAdvisoryResponse is the wire shape for the OpenAPI SecurityAdvisory schema.
// Deliberately a separate Go type from domain.SecurityAdvisory: that type is the
// literal persisted shape inside plugins/{id}/plugin.json (see domain.PluginState.
// VersionStates[version].Advisory, written/read directly by internal/lifecycle.
// Service), so a wire-only change made on it would silently rewrite plugin.json. Build
// these with newSecurityAdvisoryResponse.
type SecurityAdvisoryResponse struct {
	Severity   domain.AdvisorySeverity `json:"severity"`
	Text       string                  `json:"text"`
	AttachedAt time.Time               `json:"attachedAt"`
}

func newSecurityAdvisoryResponse(a domain.SecurityAdvisory) SecurityAdvisoryResponse {
	return SecurityAdvisoryResponse{Severity: a.Severity, Text: a.Text, AttachedAt: a.AttachedAt}
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

	Tier           domain.TrustTier          `json:"tier"`
	Blocked        bool                      `json:"blocked"`
	BlockedAt      *time.Time                `json:"blockedAt,omitempty"`
	BlockReason    string                    `json:"blockReason,omitempty"`
	Advisory       *SecurityAdvisoryResponse `json:"advisory,omitempty"`
	SHA256         string                    `json:"sha256"`
	ChangelogURL   *string                   `json:"changelogUrl,omitempty"`
	ScreenshotURLs []string                  `json:"screenshotUrls"`
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
	ID              string                   `json:"id"`
	Tier            domain.TrustTier         `json:"tier"`
	LatestVersion   string                   `json:"latestVersion"`
	BlockedVersions []BlockedVersionResponse `json:"blockedVersions,omitempty"`
}
