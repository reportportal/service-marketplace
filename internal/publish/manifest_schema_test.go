package publish

// docs/schemas/marketplace-manifest.schema.json is the schema publishers validate
// marketplace-manifest.json against, and it declares "additionalProperties": false — so
// a field the registry accepts but the schema does not declare makes every publisher's
// local validation fail on a manifest the registry itself would take. This binds
// domain.Manifest to that schema the same way wire_contract_test.go binds the response
// types to the OpenAPI document.

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/openapispec"
)

const manifestSchemaPath = "../../docs/schemas/marketplace-manifest.schema.json"

func TestManifestFieldsMatchPublishedJSONSchema(t *testing.T) {
	want, err := openapispec.JSONSchemaProperties(manifestSchemaPath)
	if err != nil {
		t.Fatalf("loading manifest schema: %v", err)
	}

	pf4jID := "Azure DevOps"
	// Every optional field set, so each one appears in the marshalled output.
	m := domain.Manifest{
		ID: "plugin-bts-azure", Name: "Azure DevOps", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0", Category: domain.CategoryBugTracking,
		Compatibility: domain.Compatibility{ReportPortal: ">=25.1"}, Homepage: "https://x",
		Access: domain.AccessPublic, ContactURL: "https://x/pricing", PF4JID: &pf4jID,
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	var missing, extra []string
	for k := range want {
		if _, ok := got[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("domain.Manifest does not match %s\n  missing (declared in schema, never emitted): %v\n  extra   (emitted, rejected by additionalProperties:false): %v",
			manifestSchemaPath, missing, extra)
	}
}
