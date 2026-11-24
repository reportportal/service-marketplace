package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
)

func TestCompatibilityCeilingNarrowsTheEffectiveRangeAndLeavesTheJar(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	publishCeilingVersion(t, env, manifestAt(id, "1.0.0"))

	body, err := json.Marshal(map[string]string{
		"reportportal": "<27.0",
		"reason":       "RP 27.0 removes ExtensionPoint X",
	})
	if err != nil {
		t.Fatal(err)
	}
	target := "/api/v1/plugins/" + id + "/versions/1.0.0/compatibility-ceiling"
	rec := env.do(env.newRequest(http.MethodPut, target, credOperatorSession, body, "application/json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("set ceiling: got %d body=%s", rec.Code, rec.Body.String())
	}
	var stored domain.CompatibilityCeiling
	if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil {
		t.Fatalf("decode ceiling: %v", err)
	}
	if stored.ReportPortal != "<27.0" || stored.Reason != "RP 27.0 removes ExtensionPoint X" || stored.SetBy != "operator@example.com" {
		t.Fatalf("stored ceiling = %+v", stored)
	}

	st := readPluginState(t, env, id)
	if st.LatestVersion != "1.0.0" {
		t.Fatalf("latestVersion = %q, a ceiling must not withdraw the version", st.LatestVersion)
	}
	if len(st.CompatibilityCeilings) != 1 || st.CompatibilityCeilings[0].ReportPortal != "<27.0" {
		t.Fatalf("plugin state ceilings = %+v", st.CompatibilityCeilings)
	}

	row := versionSummary(t, env, id, "1.0.0")
	if row.Compatibility == nil || row.Compatibility.ReportPortal != ">=25.1" {
		t.Fatalf("author compatibility = %+v, want the manifest range", row.Compatibility)
	}
	if row.EffectiveCompatibility == nil || row.EffectiveCompatibility.ReportPortal != ">=25.1, <27.0" {
		t.Fatalf("effectiveCompatibility = %+v", row.EffectiveCompatibility)
	}
	if row.CompatibilityCeiling != "<27.0" || row.CompatibilityCeilingReason != "RP 27.0 removes ExtensionPoint X" {
		t.Fatalf("ceiling on the version list = %q %q", row.CompatibilityCeiling, row.CompatibilityCeilingReason)
	}

	detail := versionDetail(t, env, id, "1.0.0")
	if detail.Compatibility.ReportPortal != ">=25.1" {
		t.Fatalf("detail author range = %q", detail.Compatibility.ReportPortal)
	}
	if detail.EffectiveCompatibility == nil || detail.EffectiveCompatibility.ReportPortal != ">=25.1, <27.0" {
		t.Fatalf("detail effective = %+v", detail.EffectiveCompatibility)
	}

	if got, ok := listingCompatibility(t, env, id); !ok || got != ">=25.1, <27.0" {
		t.Fatalf("listing compatibility = %q, present %v, want the effective range of latest", got, ok)
	}

	art := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+id+"/versions/1.0.0/artifact", credNone, nil, ""))
	if art.Code != http.StatusFound {
		t.Fatalf("artifact status = %d, a ceiling must still serve the jar; body=%s", art.Code, art.Body.String())
	}

	// A second publish keeps the ceiling, and latest follows the new version rather than the ceiling.
	publishCeilingVersion(t, env, &domain.Manifest{
		ID: id, Name: "Demo", Version: "2.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=26.0"},
		Access: domain.AccessPublic,
	})
	st = readPluginState(t, env, id)
	if st.LatestVersion != "2.0.0" {
		t.Fatalf("latestVersion after 2.0.0 = %q", st.LatestVersion)
	}
	if c, ok := domain.FindCeiling(st.CompatibilityCeilings, "1.0.0"); !ok || c.ReportPortal != "<27.0" {
		t.Fatalf("ceiling on 1.0.0 after a later publish = %+v, present %v", c, ok)
	}
	if got, ok := listingCompatibility(t, env, id); !ok || got != ">=26.0" {
		t.Fatalf("listing compatibility = %q, present %v, want 2.0.0's author range", got, ok)
	}
}

func TestCompatibilityCeilingRejectsAnEmptyIntersectionAndABadBound(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	publishCeilingVersion(t, env, manifestAt(id, "1.0.0"))
	target := "/api/v1/plugins/" + id + "/versions/1.0.0/compatibility-ceiling"

	empty, err := json.Marshal(map[string]string{"reportportal": "<25.1", "reason": "too low"})
	if err != nil {
		t.Fatal(err)
	}
	rec := env.do(env.newRequest(http.MethodPut, target, credOperatorSession, empty, "application/json"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty intersection: got %d body=%s", rec.Code, rec.Body.String())
	}
	var verr ValidationErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &verr); err != nil {
		t.Fatal(err)
	}
	if len(verr.Errors) != 1 || verr.Errors[0].Field != "reportportal" {
		t.Fatalf("validation errors = %+v", verr.Errors)
	}

	bad, err := json.Marshal(map[string]string{"reportportal": ">=27.0", "reason": "not an upper bound"})
	if err != nil {
		t.Fatal(err)
	}
	rec = env.do(env.newRequest(http.MethodPut, target, credOperatorSession, bad, "application/json"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad bound: got %d body=%s", rec.Code, rec.Body.String())
	}
	if st := readPluginState(t, env, id); len(st.CompatibilityCeilings) != 0 {
		t.Fatalf("rejected ceilings were stored: %+v", st.CompatibilityCeilings)
	}
}

func TestClearCompatibilityCeilingRestoresTheAuthorRange(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	publishCeilingVersion(t, env, manifestAt(id, "1.0.0"))
	target := "/api/v1/plugins/" + id + "/versions/1.0.0/compatibility-ceiling"

	body, err := json.Marshal(map[string]string{"reportportal": "<27.0", "reason": "breaks on 27"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := env.do(env.newRequest(http.MethodPut, target, credOperatorSession, body, "application/json")); rec.Code != http.StatusOK {
		t.Fatalf("set: got %d body=%s", rec.Code, rec.Body.String())
	}

	replaced, err := json.Marshal(map[string]string{"reportportal": "<=26.2", "reason": "26.2 is the last release"})
	if err != nil {
		t.Fatal(err)
	}
	rec := env.do(env.newRequest(http.MethodPut, target, credOperatorSession, replaced, "application/json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("replace: got %d body=%s", rec.Code, rec.Body.String())
	}
	row := versionSummary(t, env, id, "1.0.0")
	if row.EffectiveCompatibility == nil || row.EffectiveCompatibility.ReportPortal != ">=25.1, <=26.2" {
		t.Fatalf("effective after replace = %+v", row.EffectiveCompatibility)
	}
	if len(readPluginState(t, env, id).CompatibilityCeilings) != 1 {
		t.Fatal("replace stored a second ceiling")
	}

	rec = env.do(env.newRequest(http.MethodDelete, target, credOperatorSession, nil, ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear: got %d body=%s", rec.Code, rec.Body.String())
	}
	raw := versionSummaryRaw(t, env, id, "1.0.0")
	if _, ok := raw["compatibilityCeilingReason"]; ok {
		t.Fatalf("reason still present after clear: %s", raw["compatibilityCeilingReason"])
	}
	var effective domain.Compatibility
	if err := json.Unmarshal(raw["effectiveCompatibility"], &effective); err != nil {
		t.Fatalf("effective after clear: %v raw=%s", err, raw["effectiveCompatibility"])
	}
	if effective.ReportPortal != ">=25.1" {
		t.Fatalf("effective after clear = %q, want the author range", effective.ReportPortal)
	}
	if got, ok := listingCompatibility(t, env, id); !ok || got != ">=25.1" {
		t.Fatalf("listing after clear = %q, present %v", got, ok)
	}

	rec = env.do(env.newRequest(http.MethodDelete, target, credOperatorSession, nil, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second clear: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func publishCeilingVersion(t *testing.T, env *testEnv, m *domain.Manifest) {
	t.Helper()
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	body, contentType := buildPublishMultipart(t, jar)
	rec := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins/"+m.ID+"/versions", credOIDCPublish, body, contentType))
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish %s: got %d body=%s", m.Version, rec.Code, rec.Body.String())
	}
}

func versionSummary(t *testing.T, env *testEnv, pluginID, version string) PluginVersionSummary {
	t.Helper()
	raw := versionSummaryRaw(t, env, pluginID, version)
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var row PluginVersionSummary
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func versionSummaryRaw(t *testing.T, env *testEnv, pluginID, version string) map[string]json.RawMessage {
	t.Helper()
	rec := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions", credNone, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list versions: got %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Versions []map[string]json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, row := range body.Versions {
		var got string
		if err := json.Unmarshal(row["version"], &got); err != nil {
			t.Fatal(err)
		}
		if got == version {
			return row
		}
	}
	t.Fatalf("version %s missing from the list", version)
	return nil
}

func versionDetail(t *testing.T, env *testEnv, pluginID, version string) PluginVersionDetailResponse {
	t.Helper()
	rec := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/"+version, credNone, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("version detail: got %d body=%s", rec.Code, rec.Body.String())
	}
	var detail PluginVersionDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}
