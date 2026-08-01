package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// buildPublishMultipart builds a minimal multipart/form-data body containing
// only the required "jar" part, matching what publish.ParseMultipart /
// Server.parsePublishBundle expect.
func buildPublishMultipart(t *testing.T, jar []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("jar", "plugin.jar")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(jar); err != nil {
		t.Fatalf("write jar part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// TestPublishVersionAllowListedOIDCAutoCreatesPlugin is the regression test
// for AMD-15-ci-first-publish / D-05 (adopted "auto-create"): an allow-listed
// GitHub Actions OIDC token publishing to POST .../versions for a pluginId
// that does not exist yet must create the plugin entry at tier: official as
// part of writing the version, not 404 — this is the sanctioned first-CI-publish
// path (US-MRKT-OPR-004 "first publish happens via operator ui or ci").
func TestPublishVersionAllowListedOIDCAutoCreatesPlugin(t *testing.T) {
	env := newTestEnv(t)

	if exists, _ := env.Store.Exists(context.Background(), storage.PluginStatePath(testOIDCPluginID)); exists {
		t.Fatalf("test precondition violated: %s already has plugin state", testOIDCPluginID)
	}

	m := &domain.Manifest{
		ID: testOIDCPluginID, Name: "Demo", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body, contentType := buildPublishMultipart(t, jar)

	req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+testOIDCPluginID+"/versions", credOIDCPublish, body, contentType)
	rec := env.do(req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 (auto-create on allow-listed OIDC first publish), got %d body=%s", rec.Code, rec.Body.String())
	}

	obj, err := env.Store.Read(context.Background(), storage.PluginStatePath(testOIDCPluginID))
	if err != nil {
		t.Fatalf("plugin state was not created: %v", err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatalf("decode plugin state: %v", err)
	}
	if st.Tier != domain.TierOfficial {
		t.Errorf("auto-created plugin tier = %q, want %q (D-05: \"creates the entry with tier: official\")", st.Tier, domain.TierOfficial)
	}
	if st.LatestVersion != "1.0.0" {
		t.Errorf("auto-created plugin latestVersion = %q, want %q", st.LatestVersion, "1.0.0")
	}
}

// TestPublishVersionOperatorSessionDoesNotAutoCreate guards the other half
// of D-05: auto-create is scoped to allow-listed OIDC callers only. An
// operator session publishing a version for a pluginId that doesn't exist
// yet still 404s — first publish via the Operator UI goes through
// POST /api/v1/plugins (handlePublishFirst), not this route.
func TestPublishVersionOperatorSessionDoesNotAutoCreate(t *testing.T) {
	env := newTestEnv(t)

	m := &domain.Manifest{
		ID: testOIDCPluginID, Name: "Demo", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body, contentType := buildPublishMultipart(t, jar)

	req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+testOIDCPluginID+"/versions", credOperatorSession, body, contentType)
	rec := env.do(req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (operator session must not auto-create), got %d body=%s", rec.Code, rec.Body.String())
	}
	body2 := decodeErrorEnvelope(t, rec)
	if body2.Code != CodeNotFound {
		t.Fatalf("expected error code %q, got %q", CodeNotFound, body2.Code)
	}
}

// TestOperatorIdentityUsesVerifiedOIDCSubject is the regression test for
// finding F2-oidc-identity-not-recorded (go-assessment.json, RECURS):
// operatorIdentity used to fall back to the fixed literal "github-actions"
// for every OIDC-authenticated caller because router.go discarded the
// verified `sub` claim with `_`. ADR-014 requires accountability — the
// actual repo/workflow, not an indistinguishable constant.
func TestOperatorIdentityUsesVerifiedOIDCSubject(t *testing.T) {
	const subject = "repo:reportportal/plugin-x-repo:ref:refs/heads/main"
	ctx := context.WithValue(context.Background(), ctxOIDCSubject, subject)

	got := operatorIdentity(ctx)
	if got != subject {
		t.Fatalf("operatorIdentity(oidc ctx) = %q, want the verified subject %q", got, subject)
	}
	if got == "github-actions" {
		t.Fatalf("operatorIdentity fell back to the fixed literal \"github-actions\" instead of the verified OIDC subject")
	}
}

// TestRequireSessionOrPublishOIDCCarriesSubjectIntoContext exercises the real
// middleware (not a hand-built context) end to end: a valid OIDC bearer
// token's verified subject must reach operatorIdentity via the request
// context requireSessionOrPublishOIDC constructs.
func TestRequireSessionOrPublishOIDCCarriesSubjectIntoContext(t *testing.T) {
	env := newTestEnv(t)

	var captured string
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = operatorIdentity(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := env.Server.requireSessionOrPublishOIDC(probe)

	req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+testOIDCPluginID+"/versions", credOIDCPublish, nil, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	want := "repo:" + testOIDCRepo + ":ref:refs/heads/main"
	if captured != want {
		t.Fatalf("operatorIdentity via requireSessionOrPublishOIDC = %q, want %q", captured, want)
	}
}
