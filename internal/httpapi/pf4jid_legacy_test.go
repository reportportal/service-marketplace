package httpapi

// pf4jId is additive, and "additive" has to hold for documents that are ALREADY in the
// bucket — not merely for ones the new publish path writes. So this seeds the store with
// raw legacy bytes: an index.json, a plugin.json and a version manifest.json exactly as
// the pre-pf4jId code emitted them (compact json.Marshal output, no pf4jId key at all),
// then drives every public read route through the real router and afterwards compares
// the stored objects byte for byte.
//
// The byte comparison is the point. This project has already shipped a wire-format fix
// that silently rewrote authorized_keys.json on read; a read that re-marshals what it
// parsed is indistinguishable from a correct one until the day the round trip is lossy.
// The generation is compared alongside the bytes so that even a byte-identical rewrite —
// harmless-looking, but it invalidates every concurrent writer's CAS token — fails here.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Legacy documents, hand-written as the pre-change code emitted them.
const (
	legacyPluginID = "plugin-bts-jira"
	legacyVersion  = "1.4.2"

	legacyIndexJSON = `{"plugins":[{"id":"plugin-bts-jira","name":"JIRA Cloud","latestVersion":"1.4.2","description":"Bug tracking for Jira Cloud","category":"bug-tracking","access":"public","tier":"official"}]}`

	legacyPluginJSON = `{"id":"plugin-bts-jira","tier":"official","latestVersion":"1.4.2","versions":[{"version":"1.4.2","publishedAt":"2026-01-15T10:00:00Z","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}]}`

	legacyManifestJSON = `{"id":"plugin-bts-jira","name":"JIRA Cloud","version":"1.4.2","description":"Bug tracking for Jira Cloud","author":{"name":"ReportPortal Team"},"license":"Apache-2.0","category":"bug-tracking","compatibility":{"reportportal":">=25.1"},"access":"public"}`
)

// storedObject is a stored document's full observable state: its bytes and the
// generation a CAS writer would hold.
type storedObject struct {
	data []byte
	gen  int64
}

func snapshotObjects(t *testing.T, env *testEnv, paths []string) map[string]storedObject {
	t.Helper()
	out := make(map[string]storedObject, len(paths))
	for _, p := range paths {
		obj, err := env.Store.Read(context.Background(), p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		out[p] = storedObject{data: append([]byte(nil), obj.Data...), gen: obj.Generation}
	}
	return out
}

// seedLegacyStore writes the raw pre-pf4jId bytes straight into storage, bypassing the
// publish pipeline entirely — nothing in this test may re-serialize them on the way in.
func seedLegacyStore(t *testing.T, env *testEnv) []string {
	t.Helper()
	ctx := context.Background()
	docs := map[string]string{
		"index.json":                          legacyIndexJSON,
		"plugins/plugin-bts-jira/plugin.json": legacyPluginJSON,
		"plugins/plugin-bts-jira/versions/1.4.2/manifest.json":             legacyManifestJSON,
		"plugins/plugin-bts-jira/versions/1.4.2/plugin-bts-jira-1.4.2.jar": "not-a-real-jar",
	}
	paths := make([]string, 0, len(docs))
	for p, body := range docs {
		if _, err := env.Store.Write(ctx, p, []byte(body), 0); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
		paths = append(paths, p)
	}
	return paths
}

func TestLegacyDocumentsWithoutPF4JIDServeAndAreNeverRewritten(t *testing.T) {
	env := newTestEnv(t)
	paths := seedLegacyStore(t, env)
	before := snapshotObjects(t, env, paths)

	// (a) every public read route serves the legacy documents correctly, with pf4jId
	// simply absent rather than null, empty or an error.
	t.Run("listing", func(t *testing.T) {
		item := listItem(t, env, legacyPluginID)
		assertJSONField(t, "GET /api/v1/plugins", item, "name", "JIRA Cloud")
		assertNoPF4JID(t, "GET /api/v1/plugins", item)
	})

	t.Run("plugin detail", func(t *testing.T) {
		body := getJSONObject(t, env, "/api/v1/plugins/"+legacyPluginID)
		assertJSONField(t, "GET /api/v1/plugins/{id}", body, "version", legacyVersion)
		assertNoPF4JID(t, "GET /api/v1/plugins/{id}", body)
	})

	t.Run("version list", func(t *testing.T) {
		body := getJSONObject(t, env, "/api/v1/plugins/"+legacyPluginID+"/versions")
		assertJSONField(t, "GET /api/v1/plugins/{id}/versions", body, "pluginId", legacyPluginID)
		var versions []map[string]json.RawMessage
		if err := json.Unmarshal(body["versions"], &versions); err != nil {
			t.Fatalf("decode versions: %v", err)
		}
		if len(versions) != 1 {
			t.Fatalf("versions = %v, want exactly the one legacy version", versions)
		}
		assertJSONField(t, "GET /api/v1/plugins/{id}/versions", versions[0], "version", legacyVersion)
	})

	t.Run("version detail", func(t *testing.T) {
		body := getJSONObject(t, env, "/api/v1/plugins/"+legacyPluginID+"/versions/"+legacyVersion)
		assertJSONField(t, "GET .../versions/{version}", body, "license", "Apache-2.0")
		assertNoPF4JID(t, "GET .../versions/{version}", body)
	})

	t.Run("artifact", func(t *testing.T) {
		rec := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+legacyPluginID+"/versions/"+legacyVersion+"/artifact", credNone, nil, ""))
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
		}
	})

	// (b) and none of it touched the bucket.
	after := snapshotObjects(t, env, paths)
	for p, want := range before {
		got := after[p]
		if string(got.data) != string(want.data) {
			t.Errorf("%s was rewritten by a read:\n stored before: %s\n stored after:  %s", p, want.data, got.data)
		}
		if got.gen != want.gen {
			t.Errorf("%s generation %d -> %d: a read wrote to the object, invalidating every concurrent writer's CAS token", p, want.gen, got.gen)
		}
	}
}

func assertJSONField(t *testing.T, surface string, body map[string]json.RawMessage, field, want string) {
	t.Helper()
	raw, ok := body[field]
	if !ok {
		t.Fatalf("%s: %q missing from response", surface, field)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%s: %q is not a JSON string: %v", surface, field, err)
	}
	if got != want {
		t.Errorf("%s: %s = %q, want %q", surface, field, got, want)
	}
}
