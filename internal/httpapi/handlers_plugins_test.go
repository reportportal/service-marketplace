package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/config"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

func newTestServer(t *testing.T) (*Server, *catalogue.Service, *publish.Service) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	pub := &publish.Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
	cat := &catalogue.Service{Store: store}
	// Config must never be nil: every real Server is wired through
	// cmd/marketplace/main.go's config.Load(), which always returns a
	// non-nil *config.Config, and ensureXSRF/isHTTPS dereference
	// s.deps.Config.TrustedProxyHops on every request.
	srv := NewServer(Deps{Store: store, Catalogue: cat, Publish: pub, Config: &config.Config{}})
	return srv, cat, pub
}

func publishTestPlugin(t *testing.T, pub *publish.Service, m *domain.Manifest) {
	t.Helper()
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	bundle := &publish.Bundle{JAR: jar, JARFilename: "plugin.jar", Screenshots: map[string][]byte{}}
	if _, err := pub.PublishFirst(context.Background(), bundle, "test-operator"); err != nil {
		t.Fatalf("PublishFirst: %v", err)
	}
}

// GET /api/v1/plugins/{pluginId}/versions/{version} must return exactly the
// PluginVersionDetail schema's properties: no latestVersion (that belongs to
// PluginDetail, a different schema) and screenshotUrls present as an array even when
// there are no screenshots — not JSON null.
func TestGetVersionResponseMatchesPluginVersionDetailSchema(t *testing.T) {
	srv, _, pub := newTestServer(t)
	m := &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	publishTestPlugin(t, pub, m)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/plugin-demo/versions/1.0.0", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}

	if _, present := body["latestVersion"]; present {
		t.Errorf("response carries latestVersion, which PluginVersionDetail does not declare: %s", rec.Body.String())
	}

	raw, present := body["screenshotUrls"]
	if !present {
		t.Fatalf("response is missing required screenshotUrls: %s", rec.Body.String())
	}
	if string(raw) == "null" {
		t.Errorf("screenshotUrls marshalled as JSON null; schema declares type: array (not nullable): %s", rec.Body.String())
	}
	var urls []string
	if err := json.Unmarshal(raw, &urls); err != nil {
		t.Errorf("screenshotUrls is not a JSON array: %v (%s)", err, raw)
	}
}
