package httpapi

// The catalogue listing is derived from plugin state, and until now it was only ever regenerated
// as a side effect of a mutation — publish, block, remove, set-tier. So an upgrade that adds a
// field to the listing (`author`, `contactUrl` and `compatibility` each did) left every existing
// entry without it, and an operator had no step to run: the gap stayed until somebody happened to
// publish or block something unrelated.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

func rebuildIndex(t *testing.T, env *testEnv, cred credential) int {
	t.Helper()
	return env.do(env.newRequest(http.MethodPost, "/api/v1/index/rebuild", cred, nil, "")).Code
}

// TestRebuildIndexRestoresAListingUpgradedPastItsShape simulates exactly the case the review
// describes: an index written by an older build, holding an entry that predates a field, with no
// mutation coming to fix it.
//
// Kills removing the route or the RebuildIndex call behind it.
func TestRebuildIndexRestoresAListingUpgradedPastItsShape(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	if rec := publishViaHTTP(t, env, listingManifest(id, "1.0.0", ">=25.1")); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	// what an older registry left behind: the plugin is listed, but without the fields this
	// build's listing carries
	stale, err := json.Marshal(domain.Index{Plugins: []domain.IndexPlugin{{
		ID: id, Name: "Stale", LatestVersion: "1.0.0", Category: domain.CategoryOther,
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// the generation the publish left, so this is an overwrite rather than a create
	current, err := env.Store.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if _, err := env.Store.Write(context.Background(), storage.PathIndex, stale, current.Generation); err != nil {
		t.Fatalf("seed stale index: %v", err)
	}
	if got := listedCompatibility(t, env, id); got != "" {
		t.Fatalf("premise wrong: the seeded index already carries compatibility %q", got)
	}

	if status := rebuildIndex(t, env, credOperatorSession); status != http.StatusOK {
		t.Fatalf("rebuild: status %d, want 200", status)
	}

	if got := listedCompatibility(t, env, id); got != ">=25.1" {
		t.Errorf("compatibility after rebuild = %q, want %q", got, ">=25.1")
	}
}

// Derived entirely from plugin state, so running it twice is running it once.
func TestRebuildIndexIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	if rec := publishViaHTTP(t, env, listingManifest(id, "1.0.0", ">=25.1")); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d", rec.Code)
	}

	first := readIndexBytes(t, env)
	if status := rebuildIndex(t, env, credOperatorSession); status != http.StatusOK {
		t.Fatalf("rebuild: status %d", status)
	}
	if second := readIndexBytes(t, env); second != first {
		t.Errorf("rebuild changed a listing nothing else touched:\n first=%s\nsecond=%s", first, second)
	}
}

// It rewrites the whole public catalogue, so it is an operator action and not a publisher one —
// the same guard every other lifecycle mutation carries.
func TestRebuildIndexIsOperatorOnly(t *testing.T) {
	env := newTestEnv(t)

	if status := rebuildIndex(t, env, credNone); status != http.StatusUnauthorized {
		t.Errorf("anonymous rebuild: status %d, want 401", status)
	}
	if status := rebuildIndex(t, env, credOIDCPublish); status != http.StatusForbidden {
		t.Errorf("publish-OIDC rebuild: status %d, want 403", status)
	}
}

func listedCompatibility(t *testing.T, env *testEnv, pluginID string) string {
	t.Helper()
	for _, p := range readIndex(t, env).Plugins {
		if p.ID == pluginID {
			return p.Compatibility
		}
	}
	t.Fatalf("plugin %q absent from the listing", pluginID)
	return ""
}

func readIndex(t *testing.T, env *testEnv) domain.Index {
	t.Helper()
	var idx domain.Index
	if err := json.Unmarshal([]byte(readIndexBytes(t, env)), &idx); err != nil {
		t.Fatalf("parse index: %v", err)
	}
	return idx
}

func readIndexBytes(t *testing.T, env *testEnv) string {
	t.Helper()
	obj, err := env.Store.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	return string(obj.Data)
}
