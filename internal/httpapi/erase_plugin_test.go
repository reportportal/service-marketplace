package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// deletePluginViaHTTP issues DELETE /plugins/{id} with the given JSON body.
func deletePluginViaHTTP(t *testing.T, env *testEnv, id string, body map[string]any, cred credential) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return env.do(env.newRequest(http.MethodDelete, "/api/v1/plugins/"+id, cred, raw, "application/json"))
}

// pluginObjects lists everything the registry holds for a plugin, public and private tree both —
// the same two places "remove" has to clean.
func pluginObjects(t *testing.T, env *testEnv, id string) []string {
	t.Helper()
	var all []string
	for _, prefix := range []string{storage.PluginPrefix(id), storage.PrivatePluginPrefix(id)} {
		files, err := env.Store.ListPrefix(context.Background(), prefix)
		if err != nil {
			t.Fatalf("list %s: %v", prefix, err)
		}
		all = append(all, files...)
	}
	return all
}

// seedPremiumJar puts a premium jar where VersionArtifactPath keeps it: outside plugins/, in the
// private namespace (its own bucket in production).
func seedPremiumJar(t *testing.T, env *testEnv, id string) string {
	t.Helper()
	p := storage.VersionArtifactPath(id, "1.0.0", "premium")
	if _, err := env.Store.Write(context.Background(), p, []byte("premium-jar"), 0); err != nil {
		t.Fatalf("seed premium jar: %v", err)
	}
	return p
}

// TestEraseLeavesNothingAndFreesTheId pins the "no tombstone" choice: every object goes —
// plugin.json and the private tree included — so the id is indistinguishable from one never used:
// reads are 404 (not 410), the listings forget it, and the plugin's own CI can publish to it
// again from scratch.
//
// Kills an erase that keeps plugin.json (the id would still answer 410), one that only walks
// plugins/ (premium jars would survive), and one that leaves the catalogue listing stale.
func TestEraseLeavesNothingAndFreesTheId(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	if rec := publishAsOIDC(t, env, manifestAt(id, "1.0.0")); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}
	premium := seedPremiumJar(t, env, id)

	rec := deletePluginViaHTTP(t, env, id, map[string]any{"leaveTombstone": false, "removalReason": "dev clean-up"}, credOperatorSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("erase: status %d body=%s", rec.Code, rec.Body.String())
	}
	var receipt domain.PluginErased
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode receipt: %v body=%s", err, rec.Body.String())
	}
	if receipt.ID != id || receipt.ErasedBy != "operator@example.com" || receipt.RemovalReason != "dev clean-up" || receipt.Erased.IsZero() {
		t.Errorf("receipt = %+v, want id, operator, reason and a timestamp", receipt)
	}

	if left := pluginObjects(t, env, id); len(left) != 0 {
		t.Errorf("erase left objects behind: %v", left)
	}
	if exists, _ := env.Store.Exists(context.Background(), premium); exists {
		t.Errorf("premium jar %s survived the erase", premium)
	}

	for _, target := range []string{"/api/v1/plugins/" + id, "/api/v1/plugins/" + id + "/versions"} {
		rec := env.do(env.newRequest(http.MethodGet, target, credNone, nil, ""))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s after erase: status %d, want 404 (not 410: no tombstone was left)", target, rec.Code)
		}
	}
	if got := latestInIndex(t, env, id); got != "" {
		t.Errorf("catalogue still lists the erased plugin at %q", got)
	}
	rec = env.do(env.newRequest(http.MethodGet, "/api/v1/removed-plugins", credOperatorSession, nil, ""))
	var removed RemovedPluginListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &removed); err != nil {
		t.Fatalf("decode removed list: %v", err)
	}
	if len(removed.Plugins) != 0 {
		t.Errorf("an erased plugin is listed as removed: %+v", removed.Plugins)
	}

	// the id is free: the plugin's own CI publishes, and starts a fresh history
	if rec := publishAsOIDC(t, env, manifestAt(id, "2.0.0")); rec.Code != http.StatusCreated {
		t.Fatalf("publish after erase: status %d body=%s", rec.Code, rec.Body.String())
	}
	st := readPluginState(t, env, id)
	if len(st.Versions) != 1 || st.LatestVersion != "2.0.0" || st.Removed != nil || st.Restored != nil {
		t.Errorf("state after erase+publish is not fresh: %+v", st)
	}
}

// TestEraseClearsAnAlreadyRemovedPlugin is the case this option exists for: an id someone already
// removed with a tombstone, which is now unusable. Erasing it must work (the plain remove answers
// 409 for it), take no reason, and leave the id publishable — and unlike Restore, leave no
// trace of the old removal on the next plugin.json.
func TestEraseClearsAnAlreadyRemovedPlugin(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	seedRemoved(t, env, id, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC), "Registry cleanup", "drcrazy")

	if rec := publishAsOIDC(t, env, manifestAt(id, "1.0.0")); rec.Code != http.StatusGone {
		t.Fatalf("precondition: publish to a tombstoned id: status %d, want 410", rec.Code)
	}
	if rec := deletePluginViaHTTP(t, env, id, map[string]any{"removalReason": "again"}, credOperatorSession); rec.Code != http.StatusConflict {
		t.Fatalf("precondition: a second tombstone removal: status %d, want 409", rec.Code)
	}

	rec := deletePluginViaHTTP(t, env, id, map[string]any{"leaveTombstone": false}, credOperatorSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("erase a tombstoned plugin without a reason: status %d body=%s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, has := raw["removalReason"]; has {
		t.Errorf("receipt carries a removalReason nobody gave: %s", rec.Body.String())
	}

	if rec := publishAsOIDC(t, env, manifestAt(id, "1.0.0")); rec.Code != http.StatusCreated {
		t.Fatalf("publish after erase: status %d body=%s", rec.Code, rec.Body.String())
	}
	if st := readPluginState(t, env, id); st.Removed != nil || st.RemovedBy != "" || st.Restored != nil || st.RestoredBy != "" {
		t.Errorf("the old removal left a trace on the fresh plugin: %+v", st)
	}
}

// TestRemoveKeepsATombstoneByDefault: a client written before the option existed sends no
// leaveTombstone and must get exactly what it always got.
func TestRemoveKeepsATombstoneByDefault(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"field absent":    {"removalReason": "Legal request"},
		"explicitly true": {"removalReason": "Legal request", "leaveTombstone": true},
	} {
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t)
			env.seedPlugin(testOIDCPluginID, "1.0.0")

			rec := deletePluginViaHTTP(t, env, testOIDCPluginID, body, credOperatorSession)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
			}
			var tomb domain.PluginTombstone
			if err := json.Unmarshal(rec.Body.Bytes(), &tomb); err != nil || tomb.RemovalReason != "Legal request" {
				t.Fatalf("body is not the tombstone: %v %s", err, rec.Body.String())
			}
			if rec := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+testOIDCPluginID, credNone, nil, "")); rec.Code != http.StatusGone {
				t.Errorf("GET after tombstone removal: status %d, want 410", rec.Code)
			}
			if st := readPluginState(t, env, testOIDCPluginID); st.Removed == nil {
				t.Errorf("no tombstone in plugin.json")
			}
		})
	}
}

// TestTombstoneRemovalStillRequiresAReason: the reason is what the 410 tells the world, so it is
// required exactly when a tombstone carries it — and the refusal must not touch the plugin.
func TestTombstoneRemovalStillRequiresAReason(t *testing.T) {
	env := newTestEnv(t)
	env.seedPlugin(testOIDCPluginID, "1.0.0")

	for name, body := range map[string]map[string]any{
		"no reason":     {},
		"blank reason":  {"removalReason": "   "},
		"explicit true": {"leaveTombstone": true},
	} {
		rec := deletePluginViaHTTP(t, env, testOIDCPluginID, body, credOperatorSession)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422 body=%s", name, rec.Code, rec.Body.String())
		}
	}
	if st := readPluginState(t, env, testOIDCPluginID); st.Removed != nil || len(pluginObjects(t, env, testOIDCPluginID)) == 0 {
		t.Errorf("a refused removal modified the plugin")
	}
}

// TestRemovingAnUnknownIdIs404InBothModes pins the bug fix: DELETE on an id that was never
// published used to decode the empty document and answer 500.
func TestRemovingAnUnknownIdIs404InBothModes(t *testing.T) {
	env := newTestEnv(t)
	for name, body := range map[string]map[string]any{
		"with tombstone": {"removalReason": "x"},
		"erase":          {"leaveTombstone": false},
	} {
		rec := deletePluginViaHTTP(t, env, "plugin-never-published", body, credOperatorSession)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 body=%s", name, rec.Code, rec.Body.String())
			continue
		}
		if got := decodeErrorEnvelope(t, rec); got.Code != CodeNotFound {
			t.Errorf("%s: code %q, want %q", name, got.Code, CodeNotFound)
		}
	}
}

// TestTombstoneRemovalDeletesPremiumJarsToo: removal promises that no jar of the plugin can be
// served again. Premium jars live under private/plugins/, which the old cleanup never listed.
func TestTombstoneRemovalDeletesPremiumJarsToo(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	env.seedPlugin(id, "1.0.0")
	premium := seedPremiumJar(t, env, id)

	if rec := deletePluginViaHTTP(t, env, id, map[string]any{"removalReason": "Legal request"}, credOperatorSession); rec.Code != http.StatusOK {
		t.Fatalf("remove: status %d body=%s", rec.Code, rec.Body.String())
	}
	if exists, _ := env.Store.Exists(context.Background(), premium); exists {
		t.Errorf("premium jar %s survived a removal", premium)
	}
	left := pluginObjects(t, env, id)
	if len(left) != 1 || left[0] != storage.PluginStatePath(id) {
		t.Errorf("after a tombstone removal only plugin.json should remain, got %v", left)
	}
}

// TestEraseWorksOnAnUnreadableState: a plugin.json that no longer decodes is a reason to erase
// the plugin, not to refuse to.
func TestEraseWorksOnAnUnreadableState(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	env.seedPlugin(id, "1.0.0")
	if err := env.Store.Delete(context.Background(), storage.PluginStatePath(id)); err != nil {
		t.Fatalf("delete state: %v", err)
	}
	if _, err := env.Store.Write(context.Background(), storage.PluginStatePath(id), []byte("{not json"), 0); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	rec := deletePluginViaHTTP(t, env, id, map[string]any{"leaveTombstone": false}, credOperatorSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if left := pluginObjects(t, env, id); len(left) != 0 {
		t.Errorf("left objects behind: %v", left)
	}
}

// TestEraseIsOperatorSessionOnly: the plugin's own CI token must not be able to wipe the plugin
// it publishes — nor to erase a tombstone and so free an id an operator closed.
func TestEraseIsOperatorSessionOnly(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	seedRemoved(t, env, id, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC), "Legal request", "operator@example.com")

	rec := deletePluginViaHTTP(t, env, id, map[string]any{"leaveTombstone": false}, credOIDCPublish)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeErrorEnvelope(t, rec); got.Code != CodeTokenTypeNotPermitted {
		t.Errorf("code %q, want %q", got.Code, CodeTokenTypeNotPermitted)
	}
	if st := readPluginState(t, env, id); st.Removed == nil {
		t.Errorf("the refused request erased the tombstone anyway")
	}
}
