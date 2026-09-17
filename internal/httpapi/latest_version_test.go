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

// Blocking the newest build is the one thing that makes "latest" go down, and the listing has to
// follow it there. `LatestVersion` was maintained incrementally at publish — kept only when an
// arriving version compared higher — which is monotonic and so could never fall. The catalogue
// went on naming a version the artifact route answers 403 for, and the row offered an install that
// could only fail.
//
// Kills reverting either half: the recompute in BlockVersion, and the index rebuild after it.
func TestBlockingTheNewestVersionMovesLatestDown(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID

	for _, version := range []string{"1.5.0", "1.5.2"} {
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

	blockBody, err := json.Marshal(map[string]string{"reason": "Ships a vulnerable dependency"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	target := "/api/v1/plugins/" + id + "/versions/1.5.2/block"
	if rec := env.do(env.newRequest(http.MethodPost, target, credOperatorSession, blockBody, "application/json")); rec.Code != http.StatusOK {
		t.Fatalf("block 1.5.2: got %d body=%s", rec.Code, rec.Body.String())
	}

	st := readPluginState(t, env, id)
	if st.LatestVersion != "1.5.0" {
		t.Errorf("latestVersion = %q after blocking 1.5.2, want %q", st.LatestVersion, "1.5.0")
	}
	// the blocked version is withheld, not deleted: the versions table still lists it, marked
	if len(st.Versions) != 2 {
		t.Errorf("blocking must not remove the version, got %d", len(st.Versions))
	}

	// and the listing, which carries latestVersion and the range declared by it, must have been
	// rebuilt rather than left until some unrelated publish happened to do it
	if got := latestInIndex(t, env, id); got != "1.5.0" {
		t.Errorf("index latestVersion = %q, want %q", got, "1.5.0")
	}
}

// A block that leaves nothing installable says so rather than naming a version that 403s.
func TestBlockingEveryVersionLeavesNoLatest(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID

	jar, err := publish.BuildTestJAR(manifestAt(id, "1.5.0"))
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body, contentType := buildPublishMultipart(t, jar)
	if rec := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins/"+id+"/versions", credOIDCPublish, body, contentType)); rec.Code != http.StatusCreated {
		t.Fatalf("publish: got %d", rec.Code)
	}

	blockBody, _ := json.Marshal(map[string]string{"reason": "Withdrawn"})
	target := "/api/v1/plugins/" + id + "/versions/1.5.0/block"
	if rec := env.do(env.newRequest(http.MethodPost, target, credOperatorSession, blockBody, "application/json")); rec.Code != http.StatusOK {
		t.Fatalf("block: got %d body=%s", rec.Code, rec.Body.String())
	}

	if st := readPluginState(t, env, id); st.LatestVersion != "" {
		t.Errorf("latestVersion = %q with every version blocked, want empty", st.LatestVersion)
	}
}

// latestInIndex reads the published catalogue and returns the plugin's latestVersion, or "" when
// the listing does not carry the plugin at all.
func latestInIndex(t *testing.T, env *testEnv, pluginID string) string {
	t.Helper()
	obj, err := env.Store.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var idx struct {
		Plugins []domain.IndexPlugin `json:"plugins"`
	}
	if err := json.Unmarshal(obj.Data, &idx); err != nil {
		t.Fatalf("parse index: %v", err)
	}
	for _, p := range idx.Plugins {
		if p.ID == pluginID {
			return p.LatestVersion
		}
	}
	return ""
}
