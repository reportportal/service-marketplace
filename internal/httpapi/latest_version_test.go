package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// TestPublishingAnOlderVersionDoesNotDemoteLatest pins what "latest" means: the highest
// version published, not the most recent upload. Backporting a fix to an older branch after
// a newer release is ordinary, and if the upload order won, every instance already on the
// newer version would stop being offered its update and a fresh install would fetch the
// older jar.
//
// Kills the mutation `st.LatestVersion = m.Version` in publish.Service.publish.
func TestPublishingAnOlderVersionDoesNotDemoteLatest(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID

	for _, version := range []string{"1.5.2", "1.5.0"} {
		jar, err := publish.BuildTestJAR(manifestAt(id, version))
		if err != nil {
			t.Fatalf("BuildTestJAR(%s): %v", version, err)
		}
		body, contentType := buildPublishMultipart(t, jar)
		req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+id+"/versions", credOIDCPublish, body, contentType)
		if rec := env.do(req); rec.Code != http.StatusCreated {
			t.Fatalf("publish %s: got %d body=%s", version, rec.Code, rec.Body.String())
		}
	}

	st := readPluginState(t, env, id)
	if st.LatestVersion != "1.5.2" {
		t.Errorf("latestVersion = %q after publishing 1.5.2 then 1.5.0, want %q", st.LatestVersion, "1.5.2")
	}
	if len(st.Versions) != 2 {
		t.Errorf("both versions must still be published, got %d", len(st.Versions))
	}
}

// TestPublishingANewerVersionAdvancesLatest is the other half: the ordinary case still works.
func TestPublishingANewerVersionAdvancesLatest(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID

	for _, version := range []string{"1.5.2", "1.5.10"} {
		jar, err := publish.BuildTestJAR(manifestAt(id, version))
		if err != nil {
			t.Fatalf("BuildTestJAR(%s): %v", version, err)
		}
		body, contentType := buildPublishMultipart(t, jar)
		req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+id+"/versions", credOIDCPublish, body, contentType)
		if rec := env.do(req); rec.Code != http.StatusCreated {
			t.Fatalf("publish %s: got %d body=%s", version, rec.Code, rec.Body.String())
		}
	}

	// 1.5.10 beats 1.5.2 numerically and loses to it as a string, so this also pins that
	// the comparison is by precedence rather than lexicographic.
	if st := readPluginState(t, env, id); st.LatestVersion != "1.5.10" {
		t.Errorf("latestVersion = %q after publishing 1.5.2 then 1.5.10, want %q", st.LatestVersion, "1.5.10")
	}
}

func manifestAt(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

func readPluginState(t *testing.T, env *testEnv, id string) domain.PluginState {
	t.Helper()
	obj, err := env.Store.Read(context.Background(), storage.PluginStatePath(id))
	if err != nil {
		t.Fatalf("read plugin state: %v", err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatalf("decode plugin state: %v", err)
	}
	return st
}
