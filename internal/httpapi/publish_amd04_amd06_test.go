package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
)

func amd04TestManifest(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

// TestHandlePublishVersionIdempotentRetryReturns200 is the HTTP-level
// regression test for AMD-04-duplicate-publish-contract branch 2: retrying
// an already-published version with byte-identical content answers 200 with
// the original PublishResponse, not 409 — this is what makes a CI retry
// after a lost 201 response safe.
//
// Mutation that makes this fail: handlePublishVersion always writing
// http.StatusCreated (ignoring the idempotent bool PublishVersion returns)
// makes the retry return 201 instead of 200.
func TestHandlePublishVersionIdempotentRetryReturns200(t *testing.T) {
	env := newTestEnv(t)
	const pluginID = "plugin-idempotent"

	m := amd04TestManifest(pluginID, "1.0.0")
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}

	body2, contentType2 := buildPublishMultipart(t, jar)
	reqFirst := env.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body2, contentType2)
	recFirst := env.do(reqFirst)
	if recFirst.Code != http.StatusCreated {
		t.Fatalf("PublishFirst seed: expected 201, got %d body=%s", recFirst.Code, recFirst.Body.String())
	}

	body3, contentType3 := buildPublishMultipart(t, jar)
	reqRetry := env.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions", credOperatorSession, body3, contentType3)
	recRetry := env.do(reqRetry)
	if recRetry.Code != http.StatusOK {
		t.Fatalf("byte-identical retry via POST .../versions: expected 200 (AMD-04 branch 2), got %d body=%s", recRetry.Code, recRetry.Body.String())
	}
	var res publish.Result
	if err := json.Unmarshal(recRetry.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode PublishResponse: %v", err)
	}
	if res.Version != "1.0.0" || res.PluginID != pluginID {
		t.Fatalf("unexpected PublishResponse: %+v", res)
	}
}

// TestHandlePublishVersionConflictingRetryReturns409WithCode is the
// HTTP-level regression test for AMD-04 branch 3: a committed version
// republished with different content gets 409 with
// ErrorResponse.code = VERSION_ALREADY_PUBLISHED.
func TestHandlePublishVersionConflictingRetryReturns409WithCode(t *testing.T) {
	env := newTestEnv(t)
	const pluginID = "plugin-conflict"

	m1 := amd04TestManifest(pluginID, "1.0.0")
	jar1, err := publish.BuildTestJAR(m1)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body1, contentType1 := buildPublishMultipart(t, jar1)
	reqFirst := env.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body1, contentType1)
	recFirst := env.do(reqFirst)
	if recFirst.Code != http.StatusCreated {
		t.Fatalf("PublishFirst seed: expected 201, got %d body=%s", recFirst.Code, recFirst.Body.String())
	}

	m2 := amd04TestManifest(pluginID, "1.0.0")
	m2.Description = "different content, same version"
	jar2, err := publish.BuildTestJAR(m2)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body2, contentType2 := buildPublishMultipart(t, jar2)
	req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions", credOperatorSession, body2, contentType2)
	rec := env.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	errBody := decodeErrorEnvelope(t, rec)
	if errBody.Code != CodeVersionAlreadyPublished {
		t.Fatalf("error code = %q, want %q", errBody.Code, CodeVersionAlreadyPublished)
	}
}

// TestHandlePublishFirstDuplicateLivePluginReturns409WithCode is the
// HTTP-level regression test for AMD-04's PublishFirst rule: an id whose
// plugin.json already exists with removed == nil always 409s with code
// PLUGIN_ALREADY_EXISTS, directing the caller to the versions route.
func TestHandlePublishFirstDuplicateLivePluginReturns409WithCode(t *testing.T) {
	env := newTestEnv(t)
	const pluginID = "plugin-exists"

	m := amd04TestManifest(pluginID, "1.0.0")
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body1, contentType1 := buildPublishMultipart(t, jar)
	rec1 := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body1, contentType1))
	if rec1.Code != http.StatusCreated {
		t.Fatalf("seed PublishFirst: expected 201, got %d body=%s", rec1.Code, rec1.Body.String())
	}

	body2, contentType2 := buildPublishMultipart(t, jar)
	rec2 := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body2, contentType2))
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	errBody := decodeErrorEnvelope(t, rec2)
	if errBody.Code != CodePluginAlreadyExists {
		t.Fatalf("error code = %q, want %q", errBody.Code, CodePluginAlreadyExists)
	}
}

// TestHandleResurrectionEndToEnd drives the full AMD-06-removal-lifecycle /
// D-06 (adopted) resurrection path through the real router: publish, remove
// (DELETE, operator session), then resurrect via POST /api/v1/plugins
// (operator session) — and confirms an allow-listed OIDC publish to the
// same, still-tombstoned id on POST .../versions is refused with 410
// instead of resurrecting, keeping the two paths distinct per D-06
// ("explicitly NOT through the CI auto-create path").
func TestHandleResurrectionEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	const pluginID = testOIDCPluginID // has an OIDC allow-list entry wired in newTestEnv

	m := amd04TestManifest(pluginID, "1.0.0")
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body1, contentType1 := buildPublishMultipart(t, jar)
	recFirst := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body1, contentType1))
	if recFirst.Code != http.StatusCreated {
		t.Fatalf("seed PublishFirst: expected 201, got %d body=%s", recFirst.Code, recFirst.Body.String())
	}

	removeBody := []byte(`{"removalReason":"test removal"}`)
	recDel := env.do(env.newRequest(http.MethodDelete, "/api/v1/plugins/"+pluginID, credOperatorSession, removeBody, "application/json"))
	if recDel.Code != http.StatusOK {
		t.Fatalf("DELETE: expected 200, got %d body=%s", recDel.Code, recDel.Body.String())
	}

	// D-06: the allow-listed OIDC auto-create path must NOT resurrect —
	// it still sees a tombstone and returns 410, never 201.
	bodyOIDC, contentTypeOIDC := buildPublishMultipart(t, jar)
	recOIDC := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions", credOIDCPublish, bodyOIDC, contentTypeOIDC))
	if recOIDC.Code != http.StatusGone {
		t.Fatalf("OIDC auto-create publish against a tombstoned plugin: expected 410, got %d body=%s", recOIDC.Code, recOIDC.Body.String())
	}
	var tomb domain.PluginTombstone
	if err := json.Unmarshal(recOIDC.Body.Bytes(), &tomb); err != nil {
		t.Fatalf("decode tombstone: %v", err)
	}
	if tomb.RemovalReason != "test removal" {
		t.Fatalf("tombstone RemovalReason = %q, want %q", tomb.RemovalReason, "test removal")
	}

	// The explicit resurrection path: operator session, POST /api/v1/plugins.
	m2 := amd04TestManifest(pluginID, "1.0.1")
	jar2, err := publish.BuildTestJAR(m2)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body2, contentType2 := buildPublishMultipart(t, jar2)
	recResurrect := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body2, contentType2))
	if recResurrect.Code != http.StatusCreated {
		t.Fatalf("resurrection via POST /api/v1/plugins: expected 201, got %d body=%s", recResurrect.Code, recResurrect.Body.String())
	}

	recGet := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID, credNone, nil, ""))
	if recGet.Code != http.StatusOK {
		t.Fatalf("GET after resurrection: expected 200, got %d body=%s", recGet.Code, recGet.Body.String())
	}
}
