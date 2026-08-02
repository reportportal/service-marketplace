package openapispec

import (
	"sort"
	"testing"
)

const (
	specPath     = "../../docs/openapi/service-marketplace-v1.yaml"
	manifestPath = "../../docs/schemas/marketplace-manifest.schema.json"
)

func TestLoadAndProperties(t *testing.T) {
	schemas, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, err := Properties(schemas, "PublishResponse")
	if err != nil {
		t.Fatalf("Properties(PublishResponse): %v", err)
	}
	want := []string{"pluginId", "version", "sha256"}
	assertKeys(t, "PublishResponse", got, want)
}

func TestPropertiesFollowsAllOfAndRef(t *testing.T) {
	schemas, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// PluginDetail = allOf [ $ref PluginManifestFields, {tier, latestVersion} ]
	got, err := Properties(schemas, "PluginDetail")
	if err != nil {
		t.Fatalf("Properties(PluginDetail): %v", err)
	}
	want := []string{
		"id", "name", "version", "description", "author", "license", "category",
		"compatibility", "homepage", "access", "contactUrl", "tier", "latestVersion",
	}
	assertKeys(t, "PluginDetail", got, want)
}

func TestEnum(t *testing.T) {
	schemas, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := Enum(schemas, "PluginCategory")
	if err != nil {
		t.Fatalf("Enum(PluginCategory): %v", err)
	}
	want := []string{"bug-tracking", "notifications", "authorization", "import"}
	assertSlice(t, "PluginCategory enum", got, want)
}

func TestPropertyEnum(t *testing.T) {
	schemas, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := PropertyEnum(schemas, "ErrorResponse", "code")
	if err != nil {
		t.Fatalf("PropertyEnum(ErrorResponse, code): %v", err)
	}
	// AMD-09's premium-artifact license error codes (requirements/AMENDMENTS-v1.md)
	// must be part of the registry's error vocabulary, alongside a spot check of a
	// long-standing code, so this test fails if either the new codes or the
	// property-resolution machinery itself regresses.
	for _, want := range []string{"NOT_FOUND", "LICENSE_JWT_MISSING", "LICENSE_JWT_INVALID", "LICENSE_ENTITLEMENT_DENIED", "LICENSE_EXPIRED"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("PropertyEnum(ErrorResponse, code) = %v, missing %q", got, want)
		}
	}
}

// TestPropertyEnumFollowsAllOf pins that PropertyEnum resolves a property
// declared on the BASE schema even when queried through a schema that only
// reaches it via allOf composition (ValidationErrorResponse allOf's
// ErrorResponse) -- the same composition Properties already has to follow.
func TestPropertyEnumFollowsAllOf(t *testing.T) {
	schemas, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := PropertyEnum(schemas, "ValidationErrorResponse", "code")
	if err != nil {
		t.Fatalf("PropertyEnum(ValidationErrorResponse, code): %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("PropertyEnum(ValidationErrorResponse, code) = empty, want ErrorResponse's code enum via allOf")
	}
}

func TestJSONSchemaEnum(t *testing.T) {
	got, err := JSONSchemaEnum(manifestPath, "category")
	if err != nil {
		t.Fatalf("JSONSchemaEnum: %v", err)
	}
	want := []string{"bug-tracking", "notifications", "authorization", "import"}
	assertSlice(t, "manifest schema category enum", got, want)
}

func assertKeys(t *testing.T, label string, got map[string]bool, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d keys %v, want %d keys %v", label, len(got), keysOf(got), len(want), want)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("%s: missing expected key %q, got %v", label, w, keysOf(got))
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertSlice(t *testing.T, label string, got, want []string) {
	t.Helper()
	g := append([]string{}, got...)
	w := append([]string{}, want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) != len(w) {
		t.Fatalf("%s: got %v, want %v", label, g, w)
	}
	for i := range g {
		if g[i] != w[i] {
			t.Fatalf("%s: got %v, want %v", label, g, w)
		}
	}
}
