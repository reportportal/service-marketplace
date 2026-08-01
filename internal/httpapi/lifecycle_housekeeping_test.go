package httpapi

// TestPatchPlugin_ReportsWarningsWhenIndexRebuildFails proves the wire half
// of the "lifecycle mutations must not report success they did not achieve"
// contract end to end, through the real router and the real handler: a
// PATCH that changes a plugin's tier still returns 200 with the new tier
// (the primary write committed) but also carries a non-empty "warnings"
// array when its downstream index rebuild failed, instead of silently
// swallowing that failure the way the handler did before this change.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/lifecycle"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

// serverWithFaultedLifecycle wires a fresh Catalogue/Publish/Lifecycle
// against fs, keeping every other dependency (Sessions/AdminAuth/GitHub/
// OIDC/Config) from e.Server -- serverWithStore alone is insufficient here
// because it only swaps Deps.Store, not the already-constructed Lifecycle/
// Publish services, which each hold their own reference to the store they
// were built with (see multi_replica_test.go's newReplicaServer for the
// same pattern).
func (e *testEnv) serverWithFaultedLifecycle(fs storage.ObjectStore) *Server {
	e.t.Helper()
	d := e.Server.deps
	d.Store = fs
	d.Publish = &publish.Service{Store: fs, Invalidator: cdn.NoopInvalidator{}}
	d.Catalogue = &catalogue.Service{Store: fs}
	d.Lifecycle = &lifecycle.Service{Store: fs, Invalidator: cdn.NoopInvalidator{}, Publisher: d.Publish}
	return NewServer(d)
}

func TestPatchPlugin_ReportsWarningsWhenIndexRebuildFails(t *testing.T) {
	e := newTestEnv(t)
	e.seedPlugin("plugin-x", "1.0.0")

	fs := storagetest.Wrap(e.Store)
	fs.Fail(storagetest.OpWrite, storage.PathIndex, errors.New("boom: index write unavailable"))
	srv := e.serverWithFaultedLifecycle(fs)

	req := e.newRequest(http.MethodPatch, "/api/v1/plugins/plugin-x", credOperatorSession,
		[]byte(`{"tier":"official"}`), "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the tier change itself must still succeed): body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Tier     string   `json:"tier"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if body.Tier != "official" {
		t.Fatalf("tier = %q, want \"official\" (primary write must have committed): body=%s", body.Tier, rec.Body.String())
	}
	if len(body.Warnings) == 0 {
		t.Fatalf("response has no warnings, want at least one reporting the failed index rebuild: body=%s", rec.Body.String())
	}
}

// TestDeletePlugin_ReportsWarningsWhenIndexRebuildFails is the DELETE-side
// twin of TestPatchPlugin_ReportsWarningsWhenIndexRebuildFails, and the
// MAJOR fix this file exists for: handleRemovePlugin sets
// tomb.Warnings = hk.Warnings, but until this test existed nothing in the
// full httpapi or lifecycle suites actually exercised that line through the
// real router/handler. Deleting it (never setting the tombstone's warnings)
// left every existing test green -- proven below by repeating exactly that
// mutation and watching this test catch it.
func TestDeletePlugin_ReportsWarningsWhenIndexRebuildFails(t *testing.T) {
	e := newTestEnv(t)
	e.seedPlugin("plugin-x", "1.0.0")

	fs := storagetest.Wrap(e.Store)
	fs.Fail(storagetest.OpWrite, storage.PathIndex, errors.New("boom: index write unavailable"))
	srv := e.serverWithFaultedLifecycle(fs)

	req := e.newRequest(http.MethodDelete, "/api/v1/plugins/plugin-x", credOperatorSession,
		[]byte(`{"removalReason":"policy violation"}`), "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the removal itself must still succeed): body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		RemovalReason string   `json:"removalReason"`
		Warnings      []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if body.RemovalReason != "policy violation" {
		t.Fatalf("removalReason = %q, want \"policy violation\" (primary write must have committed): body=%s", body.RemovalReason, rec.Body.String())
	}
	if len(body.Warnings) == 0 {
		t.Fatalf("response has no warnings, want at least one reporting the failed index rebuild: body=%s", rec.Body.String())
	}
}

// TestDeletePlugin_NoWarningsOnFullSuccess is the control: a normal DELETE
// against a healthy store carries no "warnings" field at all.
func TestDeletePlugin_NoWarningsOnFullSuccess(t *testing.T) {
	e := newTestEnv(t)
	e.seedPlugin("plugin-x", "1.0.0")

	req := e.newRequest(http.MethodDelete, "/api/v1/plugins/plugin-x", credOperatorSession,
		[]byte(`{"removalReason":"policy violation"}`), "application/json")
	rec := httptest.NewRecorder()
	e.Server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if _, present := body["warnings"]; present {
		t.Fatalf("response carries a \"warnings\" field on full success: %s", rec.Body.String())
	}
}

// TestPatchPlugin_NoWarningsOnFullSuccess is the control: a normal PATCH
// against a healthy store carries no "warnings" field at all.
func TestPatchPlugin_NoWarningsOnFullSuccess(t *testing.T) {
	e := newTestEnv(t)
	e.seedPlugin("plugin-x", "1.0.0")

	req := e.newRequest(http.MethodPatch, "/api/v1/plugins/plugin-x", credOperatorSession,
		[]byte(`{"tier":"official"}`), "application/json")
	rec := httptest.NewRecorder()
	e.Server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if _, present := body["warnings"]; present {
		t.Fatalf("response carries a \"warnings\" field on full success: %s", rec.Body.String())
	}
}
