package httpapi

// pf4jId end to end through the real router: a publish whose marketplace-manifest.json
// declares the plugin's PF4J Plugin-Id must surface it on the catalogue listing, the
// plugin detail and the version detail, and a publish that declares nothing must leave
// all three untouched — the two states have to stay distinguishable for the
// installed-plugin merge (requirements/integration/STAGE0-id-mapping-decision.md).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
)

func pf4jTestManifest(id, name string, pf4jID *string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: name, Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryBugTracking, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic, PF4JID: pf4jID,
	}
}

// publishViaHTTP performs a first publish through POST /api/v1/plugins with an operator
// session, i.e. the same path a real publisher takes.
func publishViaHTTP(t *testing.T, env *testEnv, m *domain.Manifest) *httptest.ResponseRecorder {
	t.Helper()
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body, contentType := buildPublishMultipart(t, jar)
	return env.do(env.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body, contentType))
}

func getJSONObject(t *testing.T, env *testEnv, target string) map[string]json.RawMessage {
	t.Helper()
	rec := env.do(env.newRequest(http.MethodGet, target, credNone, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d body=%s", target, rec.Code, rec.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET %s: decode: %v body=%s", target, err, rec.Body.String())
	}
	return out
}

// listItem returns the raw JSON object for pluginID from GET /api/v1/plugins.
func listItem(t *testing.T, env *testEnv, pluginID string) map[string]json.RawMessage {
	t.Helper()
	body := getJSONObject(t, env, "/api/v1/plugins")
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(body["plugins"], &items); err != nil {
		t.Fatalf("decode plugins array: %v", err)
	}
	for _, item := range items {
		var id string
		if err := json.Unmarshal(item["id"], &id); err == nil && id == pluginID {
			return item
		}
	}
	t.Fatalf("plugin %q not present in listing: %v", pluginID, items)
	return nil
}

func assertPF4JID(t *testing.T, surface string, body map[string]json.RawMessage, want string) {
	t.Helper()
	raw, ok := body["pf4jId"]
	if !ok {
		t.Fatalf("%s: pf4jId missing; the published manifest declared %q", surface, want)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%s: pf4jId is not a JSON string: %v (raw %s)", surface, err, raw)
	}
	if got != want {
		t.Errorf("%s: pf4jId = %q, want %q", surface, got, want)
	}
}

// assertNoPF4JID pins the encoding decision for "not declared": the key is absent
// entirely — never null, never "".
func assertNoPF4JID(t *testing.T, surface string, body map[string]json.RawMessage) {
	t.Helper()
	if raw, ok := body["pf4jId"]; ok {
		t.Errorf("%s: pf4jId present as %s for a plugin that declared none; absent must mean an absent key", surface, raw)
	}
}

func TestPF4JIDSurfacesOnListDetailAndVersionDetail(t *testing.T) {
	env := newTestEnv(t)
	pf4jID := "Azure DevOps" // space + uppercase: rejected by the registry's own id rule
	if rec := publishViaHTTP(t, env, pf4jTestManifest("plugin-bts-azure", "Azure DevOps", &pf4jID)); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	assertPF4JID(t, "GET /api/v1/plugins", listItem(t, env, "plugin-bts-azure"), pf4jID)
	assertPF4JID(t, "GET /api/v1/plugins/{id}", getJSONObject(t, env, "/api/v1/plugins/plugin-bts-azure"), pf4jID)
	assertPF4JID(t, "GET /api/v1/plugins/{id}/versions/{version}",
		getJSONObject(t, env, "/api/v1/plugins/plugin-bts-azure/versions/1.0.0"), pf4jID)
}

func TestPluginPublishedWithoutPF4JIDIsUnaffected(t *testing.T) {
	env := newTestEnv(t)
	declared := "Azure DevOps"
	if rec := publishViaHTTP(t, env, pf4jTestManifest("plugin-bts-azure", "Azure DevOps", &declared)); rec.Code != http.StatusCreated {
		t.Fatalf("publish with pf4jId: status %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := publishViaHTTP(t, env, pf4jTestManifest("plugin-slack", "Slack", nil)); rec.Code != http.StatusCreated {
		t.Fatalf("publish without pf4jId: status %d body=%s", rec.Code, rec.Body.String())
	}

	assertNoPF4JID(t, "GET /api/v1/plugins", listItem(t, env, "plugin-slack"))
	assertNoPF4JID(t, "GET /api/v1/plugins/{id}", getJSONObject(t, env, "/api/v1/plugins/plugin-slack"))
	assertNoPF4JID(t, "GET /api/v1/plugins/{id}/versions/{version}",
		getJSONObject(t, env, "/api/v1/plugins/plugin-slack/versions/1.0.0"))

	// ...and the declaring plugin in the same registry still carries its value, so the
	// two are distinguishable rather than uniformly blank.
	assertPF4JID(t, "GET /api/v1/plugins", listItem(t, env, "plugin-bts-azure"), declared)
}

func TestPublishRejectsPF4JIDViolatingTheBound(t *testing.T) {
	cases := map[string]string{
		"newline":        "Azure DevOps\nBUILD SUCCESSFUL",
		"path traversal": "../../etc/passwd",
		"too long":       strings.Repeat("a", 65),
		"empty":          "",
	}
	for name, pf4jID := range cases {
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t)
			rec := publishViaHTTP(t, env, pf4jTestManifest("plugin-bts-azure", "Azure DevOps", &pf4jID))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
			var body ValidationErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error envelope: %v body=%s", err, rec.Body.String())
			}
			if body.Code != CodeValidation {
				t.Errorf("code = %q, want %q", body.Code, CodeValidation)
			}
			named := false
			for _, fe := range body.Errors {
				if strings.Contains(fe.Field, "pf4jId") {
					named = true
				}
			}
			if !named {
				t.Errorf("no field error names pf4jId: %+v", body.Errors)
			}
		})
	}
}
