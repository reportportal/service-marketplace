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
