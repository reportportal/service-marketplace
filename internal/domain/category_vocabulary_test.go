package domain_test

// The plugin category vocabulary is closed and RP-defined (reportportal-plugin-
// marketplace-plan.md §6.2: "Extending the vocabulary is an operator-side change ...
// and is intentionally not author-extensible"). Three artefacts each carry their own
// copy of it — the Go domain.Category type, the OpenAPI PluginCategory enum, and the
// marketplace-manifest JSON Schema's category enum — and nothing bound them together,
// so they were free to drift. This test is that binding: it fails the moment any one
// of the three lists a value the other two do not.

import (
	"sort"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/openapispec"
)

const (
	openAPISpecPath    = "../../docs/openapi/service-marketplace-v1.yaml"
	manifestSchemaPath = "../../docs/schemas/marketplace-manifest.schema.json"
)

func TestCategoryVocabularyIsClosedAndSyncedAcrossArtefacts(t *testing.T) {
	goCategories := make([]string, 0, len(domain.AllCategories))
	for _, c := range domain.AllCategories {
		goCategories = append(goCategories, string(c))
	}

	schemas, err := openapispec.Load(openAPISpecPath)
	if err != nil {
		t.Fatalf("loading OpenAPI spec: %v", err)
	}
	openAPICategories, err := openapispec.Enum(schemas, "PluginCategory")
	if err != nil {
		t.Fatalf("resolving PluginCategory enum: %v", err)
	}

	manifestCategories, err := openapispec.JSONSchemaEnum(manifestSchemaPath, "category")
	if err != nil {
		t.Fatalf("resolving manifest schema category enum: %v", err)
	}

	assertSameVocabulary(t, "domain.Category", goCategories, "OpenAPI PluginCategory", openAPICategories)
	assertSameVocabulary(t, "domain.Category", goCategories, "manifest JSON Schema category", manifestCategories)
}

func assertSameVocabulary(t *testing.T, aName string, a []string, bName string, b []string) {
	t.Helper()
	sa, sb := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(sa)
	sort.Strings(sb)
	if len(sa) != len(sb) {
		t.Fatalf("%s (%v) and %s (%v) disagree on the category vocabulary", aName, sa, bName, sb)
	}
	for i := range sa {
		if sa[i] != sb[i] {
			t.Fatalf("%s (%v) and %s (%v) disagree on the category vocabulary", aName, sa, bName, sb)
		}
	}
}
