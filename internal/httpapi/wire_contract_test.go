package httpapi

// Every type that reaches the HTTP wire is bound here to the property names its
// docs/openapi/service-marketplace-v1.yaml schema declares. A struct that forgets a
// json tag, or a handler that spells a map key differently than the schema, fails this
// test — that is the whole point: Go's zero-config struct marshalling makes wire drift
// silent everywhere except here.
//
// Each case constructs a fully populated value of the wire type (every optional/
// omitempty field set to a non-zero value, so it appears in the marshalled output) and
// asserts its top-level JSON keys are exactly the schema's declared properties — no
// more (an undocumented field leaking onto the wire), no fewer (a documented field the
// type can never produce).

import (
	"encoding/json"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/license"
	"github.com/reportportal/service-marketplace/internal/openapispec"
	"github.com/reportportal/service-marketplace/internal/publish"
)

const openAPISpecPath = "../../docs/openapi/service-marketplace-v1.yaml"

func TestWireTypesMatchOpenAPISchema(t *testing.T) {
	schemas, err := openapispec.Load(openAPISpecPath)
	if err != nil {
		t.Fatalf("loading OpenAPI spec: %v", err)
	}

	now := time.Now().UTC()
	blockReason := "CVE-2026-1234 in jackson-databind"
	changelogURL := "https://cdn.example/CHANGELOG.md"
	expires := domain.Date{Time: now}

	cases := []struct {
		schema string
		value  any
	}{
		{"PublishResponse", publish.Result{PluginID: "plugin-jira-cloud", Version: "1.4.2", SHA256: "abc"}},
		{"RotateLicenseKeyResponse", license.RotateResult{CustomerID: "acme-corp", PrivateKey: "priv", PublicKey: "pub"}},

		{"Author", domain.Author{Name: "ReportPortal Team", Email: "a@b.com", URL: "https://reportportal.io"}},
		{"Compatibility", domain.Compatibility{ReportPortal: ">=25.1"}},
		{"BlockedVersion", domain.BlockedVersion{Version: "1.0.0", BlockedAt: now, Reason: "reason"}},
		{"SecurityAdvisory", domain.SecurityAdvisory{Severity: domain.SeverityHigh, Text: "text", AttachedAt: now}},
		{"PluginTombstone", domain.PluginTombstone{Removed: now, RemovalReason: "reason", RemovedBy: "operator"}},
		{"PluginListItem", domain.IndexPlugin{
			ID: "plugin-jira-cloud", Name: "Jira Cloud", LatestVersion: "1.4.2", Description: "d",
			Category: domain.CategoryBugTracking, Access: domain.AccessPublic, Tier: domain.TierOfficial,
		}},
		{"PluginManifestFields", domain.Manifest{
			ID: "plugin-jira-cloud", Name: "Jira Cloud", Version: "1.4.2", Description: "d",
			Author: domain.Author{Name: "A"}, License: "Apache-2.0", Category: domain.CategoryBugTracking,
			Compatibility: domain.Compatibility{ReportPortal: ">=25.1"}, Homepage: "https://reportportal.io",
			Access: domain.AccessPublic, ContactURL: "https://reportportal.io/pricing",
		}},
		{"LicensePublicKey", LicensePublicKeyResponse{PublicKey: "pub", IssuedAt: domain.Date{Time: now}}},
		{"LicenseEntitlement", LicenseEntitlementResponse{
			CustomerID: "acme-corp", Tier: "premium", IssuedAt: domain.Date{Time: now}, ExpiresAt: &expires,
			PublicKeys: []LicensePublicKeyResponse{{PublicKey: "pub", IssuedAt: domain.Date{Time: now}}},
		}},

		{"PluginListResponse", PluginListResponse{Plugins: []domain.IndexPlugin{{
			ID: "p", Name: "n", LatestVersion: "1.0.0", Description: "d",
			Category: domain.CategoryImport, Access: domain.AccessPublic, Tier: domain.TierOfficial,
		}}}},
		{"PluginDetail", PluginDetailResponse{
			ID: "p", Name: "n", Version: "1.0.0", Description: "d", Author: domain.Author{Name: "A"},
			License: "Apache-2.0", Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
			Homepage: "https://x", Access: domain.AccessPublic, ContactURL: "https://x/pricing",
			Tier: domain.TierOfficial, LatestVersion: "1.0.0",
		}},
		{"PluginVersionListResponse", PluginVersionListResponse{
			PluginID: "p",
			Versions: []PluginVersionSummary{{
				Version: "1.0.0", PublishedAt: &now, Blocked: true, BlockedAt: &now, BlockReason: blockReason,
			}},
		}},
		{"PluginVersionDetail", PluginVersionDetailResponse{
			ID: "p", Name: "n", Version: "1.0.0", Description: "d", Author: domain.Author{Name: "A"},
			License: "Apache-2.0", Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
			Homepage: "https://x", Access: domain.AccessPublic, ContactURL: "https://x/pricing",
			Tier: domain.TierOfficial, Blocked: true, BlockedAt: &now, BlockReason: blockReason,
			Advisory:       &domain.SecurityAdvisory{Severity: domain.SeverityHigh, Text: "t", AttachedAt: now},
			SHA256:         "abc",
			ChangelogURL:   &changelogURL,
			ScreenshotURLs: []string{"https://cdn/1.png"},
		}},
		{"PremiumArtifactResponse", PremiumArtifactResponse{DownloadURL: "https://s3/x", ExpiresAt: now}},
		{"BlockedArtifactError", BlockedArtifactErrorResponse{Blocked: true, BlockedAt: now, Reason: "reason"}},
		{"PluginOperatorState", PluginOperatorStateResponse{
			ID: "p", Tier: domain.TierOfficial, LatestVersion: "1.0.0",
			BlockedVersions: []domain.BlockedVersion{{Version: "1.0.0", BlockedAt: now, Reason: "r"}},
		}},
		{"AuthConfigResponse", AuthConfigResponse{GithubEnabled: true, AdminLoginEnabled: true}},
		{"AuthTokenResponse", AuthTokenResponse{AccessToken: "tok", TokenType: "Bearer", ExpiresIn: 3600}},
		{"LicenseEntitlementListResponse", LicenseEntitlementListResponse{Entitlements: []LicenseEntitlementResponse{
			{CustomerID: "acme-corp", Tier: "premium", IssuedAt: domain.Date{Time: now}, ExpiresAt: &expires,
				PublicKeys: []LicensePublicKeyResponse{{PublicKey: "pub", IssuedAt: domain.Date{Time: now}}}},
		}}},
		{"CreateLicenseResponse", CreateLicenseResponse{
			CustomerID: "acme-corp", Tier: "premium", IssuedAt: domain.Date{Time: now}, ExpiresAt: &expires,
			PublicKeys: []LicensePublicKeyResponse{{PublicKey: "pub", IssuedAt: domain.Date{Time: now}}},
			PrivateKey: "priv",
		}},

		{"ErrorResponse", ErrorResponse{Code: CodeNotFound, Message: "not found"}},
		{"ValidationErrorResponse", ValidationErrorResponse{Code: CodeValidation, Message: "bad", Errors: []FieldError{{Field: "f", Message: "m"}}}},
	}

	for _, c := range cases {
		t.Run(c.schema, func(t *testing.T) {
			want, err := openapispec.Properties(schemas, c.schema)
			if err != nil {
				t.Fatalf("resolving schema %q: %v", c.schema, err)
			}
			got := marshalledTopLevelKeys(t, c.value)
			assertKeySetsEqual(t, c.schema, want, got)
		})
	}
}

func marshalledTopLevelKeys(t *testing.T, v any) map[string]bool {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %T into map: %v (raw: %s)", v, err, data)
	}
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func assertKeySetsEqual(t *testing.T, schema string, want, got map[string]bool) {
	t.Helper()
	var missing, extra []string
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return
	}
	sort.Strings(missing)
	sort.Strings(extra)
	t.Errorf("%s: wire keys do not match OpenAPI schema properties\n  missing (declared in schema, never emitted): %v\n  extra   (emitted, not declared in schema):    %v", schema, missing, extra)
}

// dateOnlyPattern is what docs/openapi/service-marketplace-v1.yaml's `format: date`
// means on the wire: "YYYY-MM-DD", never a full RFC3339 timestamp. TestWireTypesMatchOpenAPISchema
// only pins property *names* against the schema — it would pass unchanged if issuedAt/
// expiresAt regressed to marshalling a full timestamp, since the key would still be
// there. This test pins the *value* format instead, so that regression fails here.
var dateOnlyPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func TestLicenseWireDatesAreDateOnlyNotTimestamps(t *testing.T) {
	now := time.Now().UTC()
	expires := domain.Date{Time: now}

	t.Run("LicenseEntitlementResponse", func(t *testing.T) {
		entitlement := LicenseEntitlementResponse{
			CustomerID: "acme-corp", Tier: "premium", IssuedAt: domain.Date{Time: now}, ExpiresAt: &expires,
			PublicKeys: []LicensePublicKeyResponse{{PublicKey: "pub", IssuedAt: domain.Date{Time: now}}},
		}
		data := marshalOrFatal(t, entitlement)
		assertDateOnlyField(t, data, "issuedAt")
		assertDateOnlyField(t, data, "expiresAt")

		var withKeys struct {
			PublicKeys []json.RawMessage `json:"publicKeys"`
		}
		if err := json.Unmarshal(data, &withKeys); err != nil {
			t.Fatalf("unmarshal publicKeys: %v", err)
		}
		if len(withKeys.PublicKeys) != 1 {
			t.Fatalf("want 1 public key, got %d", len(withKeys.PublicKeys))
		}
		assertDateOnlyField(t, withKeys.PublicKeys[0], "issuedAt")
	})

	t.Run("CreateLicenseResponse", func(t *testing.T) {
		resp := CreateLicenseResponse{
			CustomerID: "acme-corp", Tier: "premium", IssuedAt: domain.Date{Time: now}, ExpiresAt: &expires,
			PublicKeys: []LicensePublicKeyResponse{{PublicKey: "pub", IssuedAt: domain.Date{Time: now}}},
			PrivateKey: "priv",
		}
		data := marshalOrFatal(t, resp)
		assertDateOnlyField(t, data, "issuedAt")
		assertDateOnlyField(t, data, "expiresAt")
	})
}

func marshalOrFatal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return data
}

func assertDateOnlyField(t *testing.T, data []byte, field string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal into map: %v (raw: %s)", err, data)
	}
	raw, ok := m[field]
	if !ok {
		t.Fatalf("field %q not present in %s", field, data)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("field %q is not a JSON string: %v (raw: %s)", field, err, raw)
	}
	if !dateOnlyPattern.MatchString(s) {
		t.Errorf("%s = %q: OpenAPI declares `format: date` (YYYY-MM-DD) but the wire value is not date-only (looks like a timestamp)", field, s)
	}
}
