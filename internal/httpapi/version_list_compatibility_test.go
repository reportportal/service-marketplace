package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/lifecycle"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// a second version of a plugin that already exists: PublishFirst refuses, by design
func publishTestVersion(t *testing.T, pub *publish.Service, m *domain.Manifest) {
	t.Helper()
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	bundle := &publish.Bundle{JAR: jar, JARFilename: "plugin.jar", Screenshots: map[string][]byte{}}
	if _, err := pub.PublishVersion(context.Background(), m.ID, bundle, "test-operator", false); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
}

// A consumer rendering a table of versions needs each row's compatibility and advisory at once.
// Before this, both lived only on the per-version detail route, so a ten-version table cost ten
// extra requests — and service-ui simply did without, which is why an incompatible version was
// offered with a live Install button.
func TestListVersionsCarriesCompatibilityAndAdvisoryPerRow(t *testing.T) {
	srv, _, pub := newTestServer(t)
	publishTestPlugin(t, pub, &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	})
	publishTestVersion(t, pub, &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "2.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=26.2"},
		Access: domain.AccessPublic,
	})

	life := &lifecycle.Service{Store: srv.deps.Store, Invalidator: cdn.NoopInvalidator{}}
	if _, err := life.AttachAdvisory(
		context.Background(), "plugin-demo", "1.0.0", domain.SeverityHigh, "CVE-2026-1234",
	); err != nil {
		t.Fatalf("AttachAdvisory: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/plugin-demo/versions", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got PluginVersionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	byVersion := map[string]PluginVersionSummary{}
	for _, v := range got.Versions {
		byVersion[v.Version] = v
	}

	// each version answers for its own declared range, not the plugin's newest
	for version, want := range map[string]string{"1.0.0": ">=25.1", "2.0.0": ">=26.2"} {
		row, ok := byVersion[version]
		if !ok {
			t.Fatalf("version %s missing from the listing", version)
		}
		if row.Compatibility == nil {
			t.Fatalf("version %s carries no compatibility; a consumer would have to fetch its detail", version)
		}
		if row.Compatibility.ReportPortal != want {
			t.Errorf("version %s compatibility = %q, want %q", version, row.Compatibility.ReportPortal, want)
		}
	}

	if byVersion["1.0.0"].Advisory == nil {
		t.Error("the advisory attached to 1.0.0 is not in its row")
	} else if byVersion["1.0.0"].Advisory.Severity != domain.SeverityHigh {
		t.Errorf("advisory severity = %q, want high", byVersion["1.0.0"].Advisory.Severity)
	}

	// and an advisory belongs to the version it was attached to, not to every row
	if byVersion["2.0.0"].Advisory != nil {
		t.Error("2.0.0 carries an advisory that was attached to 1.0.0")
	}
}

// Absent is unknown, never "compatible". An entry published before the range was recorded has
// none, and the field must then be missing from the JSON rather than present and empty — a
// consumer distinguishing "no range declared" from "range is the empty string" would otherwise
// have to guess, and guessing is what the refusal exists to prevent.
func TestListVersionsOmitsCompatibilityWhenTheEntryPredatesIt(t *testing.T) {
	srv, _, pub := newTestServer(t)
	publishTestPlugin(t, pub, &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	})

	// what an older state file looks like: a version recorded before the field existed
	path := storage.PluginStatePath("plugin-demo")
	obj, err := srv.deps.Store.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	for i := range st.Versions {
		st.Versions[i].Compatibility = ""
	}
	stripped, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if _, err := srv.deps.Store.Write(context.Background(), path, stripped, obj.Generation); err != nil {
		t.Fatalf("write state: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/plugin-demo/versions", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var raw struct {
		Versions []map[string]any `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(raw.Versions))
	}
	if _, present := raw.Versions[0]["compatibility"]; present {
		t.Errorf("compatibility is present for an entry that declares none: %v", raw.Versions[0])
	}
}
