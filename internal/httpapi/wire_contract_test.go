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
	"sort"
	"testing"

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

	cases := []struct {
		schema string
		value  any
	}{
		{"PublishResponse", publish.Result{PluginID: "plugin-jira-cloud", Version: "1.4.2", SHA256: "abc"}},
		{"RotateLicenseKeyResponse", license.RotateResult{CustomerID: "acme-corp", PrivateKey: "priv", PublicKey: "pub"}},
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
