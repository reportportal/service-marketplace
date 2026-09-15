package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// listingManifest is a public plugin at a named version and range, so a test can say which
// version declared what without restating the whole manifest each time.
func listingManifest(pluginID, version, reportPortal string) *domain.Manifest {
	return &domain.Manifest{
		ID: pluginID, Name: "Quality Gate", Version: version, Description: "d",
		Author: domain.Author{Name: "ReportPortal"}, License: "Apache-2.0",
		Category:      domain.CategoryOther,
		Compatibility: domain.Compatibility{ReportPortal: reportPortal},
		Access:        domain.AccessPublic,
	}
}

// a second version of a plugin that already exists: the first-publish route refuses it by design
func publishVersionViaHTTP(t *testing.T, env *testEnv, m *domain.Manifest) *httptest.ResponseRecorder {
	t.Helper()
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body, contentType := buildPublishMultipart(t, jar)
	target := "/api/v1/plugins/" + m.ID + "/versions"

	return env.do(env.newRequest(http.MethodPost, target, credOperatorSession, body, contentType))
}

func listingCompatibility(t *testing.T, env *testEnv, pluginID string) (string, bool) {
	t.Helper()
	raw, ok := listItem(t, env, pluginID)["compatibility"]
	if !ok {
		return "", false
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("compatibility is not a JSON string: %v (raw %s)", err, raw)
	}
	return got, true
}

// TestListingCarriesCompatibility pins the range onto the catalogue listing. A row in a
// catalogue offers to install the latest build, and whether that build runs on the instance
// reading the row is the one thing the row cannot answer for itself. Without this field the
// consumer's only options are to fetch one version detail per row — which is what the
// denormalisation exists to avoid — or to offer an install that the server will refuse.
//
// Kills dropping Compatibility from domain.IndexPlugin or from the index rebuild.
func TestListingCarriesCompatibility(t *testing.T) {
	env := newTestEnv(t)

	if rec := publishViaHTTP(t, env, listingManifest(testOIDCPluginID, "1.0.0", ">=25.1")); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	got, present := listingCompatibility(t, env, testOIDCPluginID)
	if !present {
		t.Fatalf("GET /api/v1/plugins: compatibility missing from the listing; the manifest declared >=25.1")
	}
	if got != ">=25.1" {
		t.Errorf("compatibility = %q, want %q", got, ">=25.1")
	}
}

// TestListingCarriesTheLatestVersionsRange is the case that makes the field mean anything. A
// range belongs to a version, and the listing names exactly one — the latest. Publishing a
// newer build that widens or narrows its requirement must move the listing with it; reporting
// the first version's range forever would tell a 26.x instance it cannot run a plugin built
// for it, or worse, the reverse.
func TestListingCarriesTheLatestVersionsRange(t *testing.T) {
	env := newTestEnv(t)

	if rec := publishViaHTTP(t, env, listingManifest(testOIDCPluginID, "1.0.0", ">=25.1")); rec.Code != http.StatusCreated {
		t.Fatalf("publish 1.0.0: status %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := publishVersionViaHTTP(t, env, listingManifest(testOIDCPluginID, "2.0.0", ">=26.2")); rec.Code != http.StatusCreated {
		t.Fatalf("publish 2.0.0: status %d body=%s", rec.Code, rec.Body.String())
	}

	got, present := listingCompatibility(t, env, testOIDCPluginID)
	if !present {
		t.Fatalf("compatibility missing from the listing after a second publish")
	}
	if got != ">=26.2" {
		t.Errorf("compatibility = %q, want the latest version's %q", got, ">=26.2")
	}
}

// TestListingOmitsCompatibilityWhenThePluginPredatesIt is the other half, and it matters more
// here than for a cosmetic field: absent must stay distinguishable from "no requirement". A
// consumer that read a missing range as "runs anywhere" would offer installs the server then
// refuses, and one that read it as "runs nowhere" would ground every plugin published before
// the field existed. It has to be able to tell that nobody answered.
func TestListingOmitsCompatibilityWhenThePluginPredatesIt(t *testing.T) {
	env := newTestEnv(t)

	if rec := publishViaHTTP(t, env, listingManifest(testOIDCPluginID, "1.0.0", ">=25.1")); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	// what a manifest stored before the field existed looks like; the index is rebuilt from
	// the manifest, so blanking it there is what an older record actually presents
	ctx := context.Background()
	path := storage.VersionManifestPath(testOIDCPluginID, "1.0.0")
	obj, err := env.Store.Read(ctx, path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m domain.Manifest
	if err := json.Unmarshal(obj.Data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	m.Compatibility = domain.Compatibility{}
	stripped, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if _, err := env.Store.Write(ctx, path, stripped, obj.Generation); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// publishing another plugin rebuilds the whole index, which is what re-reads the manifest
	if rec := publishViaHTTP(t, env, listingManifest(testOtherPluginID, "1.0.0", ">=25.1")); rec.Code != http.StatusCreated {
		t.Fatalf("publish second plugin: status %d body=%s", rec.Code, rec.Body.String())
	}

	if got, present := listingCompatibility(t, env, testOIDCPluginID); present {
		t.Errorf("listing carries a compatibility key %q for a plugin that declares none", got)
	}
}
