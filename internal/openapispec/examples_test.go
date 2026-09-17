package openapispec_test

import (
	"path/filepath"
	"testing"

	"github.com/reportportal/service-marketplace/internal/openapispec"
)

// An example that contradicts the schema printed beside it is worse than no example: it is the
// thing an integrator copies, and it is published. Both of these drifted the moment `author` and
// `contactUrl` became required on the listing — the schema moved and the example did not, and
// nothing here noticed until a reviewer read the file.
//
// Kills removing a required property from an example, and kills making a property required
// without adding it to the example.
func TestPublishedExamplesSatisfyTheirOwnSchemas(t *testing.T) {
	specPath := filepath.Join("..", "..", "docs", "openapi", "service-marketplace-v1.yaml")
	schemas, err := openapispec.Load(specPath)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	for _, c := range []struct {
		example string
		schema  string
		// field the example nests its items under, empty when the example is the object itself
		items string
	}{
		{example: "PluginListExample", schema: "PluginListItem", items: "plugins"},
		{example: "PluginDetailExample", schema: "PluginDetail"},
		{example: "PluginVersionDetailExample", schema: "PluginVersionDetail"},
	} {
		t.Run(c.example, func(t *testing.T) {
			required, err := openapispec.RequiredProperties(schemas, c.schema)
			if err != nil {
				t.Fatalf("required properties of %s: %v", c.schema, err)
			}
			if len(required) == 0 {
				t.Fatalf("%s declares nothing required; this test would pass vacuously", c.schema)
			}

			value, err := openapispec.LoadExample(specPath, c.example)
			if err != nil {
				t.Fatalf("load example: %v", err)
			}
			for i, entry := range entries(t, value, c.items) {
				for field := range required {
					if _, ok := entry[field]; !ok {
						t.Errorf("%s[%d] omits %q, which %s requires", c.example, i, field, c.schema)
					}
				}
			}
		})
	}
}

// entries normalises an example to the list of objects to check: the items under `field`, or the
// example itself when it names none.
func entries(t *testing.T, value any, field string) []map[string]any {
	t.Helper()
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("example is %T, want an object", value)
	}
	if field == "" {
		return []map[string]any{obj}
	}
	raw, ok := obj[field]
	if !ok {
		t.Fatalf("example has no %q", field)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("example %q is %T, want a list", field, raw)
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%q entry is %T, want an object", field, item)
		}
		out = append(out, m)
	}
	return out
}
