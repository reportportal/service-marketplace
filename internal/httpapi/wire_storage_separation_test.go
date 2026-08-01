package httpapi

// This is the sweep the review asked for after e8501ac fixed
// domain.LicenseEntitlement/LicensePublicKey: find every OTHER domain type that is both
// (a) the literal shape json.Marshal/json.Unmarshal uses for a persisted document, and
// (b) reachable, directly or through embedding, from a type a handler writes onto the
// HTTP response. Any type meeting both conditions is the same landmine that bug was: a
// change made only to satisfy the OpenAPI contract (a date format, a renamed field, a
// dropped field) can silently rewrite what is already on disk, because there is only
// one Go type and one json struct tag set governing both directions.
//
// storageOnlyDomainTypes is that sweep's result: every domain type whose json shape IS
// a persisted document (see the doc comment beside each in internal/domain/types.go).
// This test asserts none of them are reachable from a type this package hands to
// writeJSON. It is deliberately structural (reflection over the Go type, not over a
// marshalled sample) so it fails the moment a field is *declared* with one of these
// types, before any handler even runs -- reintroducing the coupling is caught here, not
// three commits later when someone "fixes" a date format again.
//
// domain.Author, domain.Compatibility, domain.Category, domain.AccessTier,
// domain.TrustTier and domain.AdvisorySeverity are deliberately NOT in this set even
// though they too are persisted (inside domain.Manifest / domain.PluginState) and
// reused verbatim on the wire (PluginDetailResponse.Author, IndexPlugin.Category, ...).
// See the report accompanying this change for why: they are plain strings/string enums
// with no format-sensitive representation (no time.Time, no computed field), the
// OpenAPI schema and the manifest JSON Schema declare the identical shape on purpose
// (a plugin's author/compatibility block is displayed on the wire exactly as the
// manifest author declared it; the category vocabulary is required by
// internal/domain/category_vocabulary_test.go to be textually identical across Go, the
// OpenAPI enum and the manifest schema enum), so there is no wire-only formatting
// concern that could ever need to diverge from storage. Splitting them would be
// separation for symmetry, not for risk.

import (
	"reflect"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/license"
	"github.com/reportportal/service-marketplace/internal/publish"
)

var storageOnlyDomainTypes = map[reflect.Type]bool{
	reflect.TypeOf(domain.IndexPlugin{}):        true, // index.json (domain.Index.Plugins)
	reflect.TypeOf(domain.Index{}):              true, // index.json
	reflect.TypeOf(domain.BlockedVersion{}):     true, // plugins/{id}/plugin.json (PluginState.BlockedVersions)
	reflect.TypeOf(domain.SecurityAdvisory{}):   true, // plugins/{id}/plugin.json (PluginState.VersionStates[v].Advisory)
	reflect.TypeOf(domain.VersionState{}):       true, // plugins/{id}/plugin.json
	reflect.TypeOf(domain.VersionMeta{}):        true, // plugins/{id}/plugin.json (PluginState.Versions)
	reflect.TypeOf(domain.PluginState{}):        true, // plugins/{id}/plugin.json
	reflect.TypeOf(domain.Manifest{}):           true, // plugins/{id}/versions/{v}/manifest.json
	reflect.TypeOf(domain.AuthorizedKeys{}):     true, // auth/authorized_keys.json
	reflect.TypeOf(domain.LicenseEntitlement{}): true, // auth/authorized_keys.json (AuthorizedKeys.Entitlements)
	reflect.TypeOf(domain.LicensePublicKey{}):   true, // auth/authorized_keys.json
}

// wireResponseTypes are every type this package (or a service it calls directly, for
// publish/license results the handlers pass straight through) hands to writeJSON as a
// response body. domain.PluginTombstone is included even though it lives in package
// domain: handleGetPlugin/handleListVersions/handleGetArtifact/handleRemovePlugin all
// write it directly as the response body, so it needs the same guarantee a response
// type declared in this package would get. It passes today because, unlike
// LicenseEntitlement, it is never unmarshalled from or marshalled to storage under this
// name -- lifecycle.Service builds it fresh from domain.PluginState's Removed/
// RemovalReason/RemovedBy fields on every call, it is not itself a persisted shape.
var wireResponseTypes = []struct {
	label string
	value any
}{
	{"AuthConfigResponse", AuthConfigResponse{}},
	{"AuthTokenResponse", AuthTokenResponse{}},
	{"LicensePublicKeyResponse", LicensePublicKeyResponse{}},
	{"LicenseEntitlementResponse", LicenseEntitlementResponse{}},
	{"LicenseEntitlementListResponse", LicenseEntitlementListResponse{}},
	{"CreateLicenseResponse", CreateLicenseResponse{}},
	{"PluginListResponse", PluginListResponse{}},
	{"PluginDetailResponse", PluginDetailResponse{}},
	{"PluginVersionSummary", PluginVersionSummary{}},
	{"PluginVersionListResponse", PluginVersionListResponse{}},
	{"PluginVersionDetailResponse", PluginVersionDetailResponse{}},
	{"PremiumArtifactResponse", PremiumArtifactResponse{}},
	{"BlockedArtifactErrorResponse", BlockedArtifactErrorResponse{}},
	{"PluginOperatorStateResponse", PluginOperatorStateResponse{}},
	{"publish.Result (handlePublishFirst/handlePublishVersion write this directly)", publish.Result{}},
	{"license.RotateResult (handleRotateLicenseKey writes this directly)", license.RotateResult{}},
	{"domain.PluginTombstone (handleGetPlugin/handleListVersions/handleGetArtifact/handleRemovePlugin write this directly)", domain.PluginTombstone{}},
}

func TestWireResponseTypesDoNotEmbedStorageOnlyDomainTypes(t *testing.T) {
	for _, c := range wireResponseTypes {
		t.Run(c.label, func(t *testing.T) {
			seen := map[reflect.Type]bool{}
			walkForStorageOnlyTypes(t, c.label, reflect.TypeOf(c.value), seen)
		})
	}
}

func walkForStorageOnlyTypes(t *testing.T, label string, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	if typ == nil {
		return
	}
	switch typ.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Map:
		walkForStorageOnlyTypes(t, label, typ.Elem(), seen)
		return
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeOf(time.Time{}) {
		return
	}
	if seen[typ] {
		return
	}
	seen[typ] = true
	if storageOnlyDomainTypes[typ] {
		t.Errorf("%s reaches %s, which is the literal persisted-document shape (see internal/domain/types.go). "+
			"A wire-only change to satisfy the OpenAPI contract on this type would silently rewrite what storage "+
			"reads and writes. Add a dedicated httpapi response type plus a converter instead, the way "+
			"LicensePublicKeyResponse/LicenseEntitlementResponse and newLicenseEntitlementResponse do.",
			label, typ)
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		walkForStorageOnlyTypes(t, label, f.Type, seen)
	}
}
