package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// publishAsOIDC publishes manifest m through the CI route (POST .../versions with the plugin's
// own allow-listed token), the path the real pipelines take.
func publishAsOIDC(t *testing.T, env *testEnv, m *domain.Manifest) *httptest.ResponseRecorder {
	t.Helper()
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	body, contentType := buildPublishMultipart(t, jar)
	return env.do(env.newRequest(http.MethodPost, "/api/v1/plugins/"+m.ID+"/versions", credOIDCPublish, body, contentType))
}

func removePluginViaHTTP(t *testing.T, env *testEnv, id, reason string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"removalReason": reason})
	rec := env.do(env.newRequest(http.MethodDelete, "/api/v1/plugins/"+id, credOperatorSession, body, "application/json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("remove %s: status %d body=%s", id, rec.Code, rec.Body.String())
	}
}

func restorePluginViaHTTP(env *testEnv, id string, cred credential) *httptest.ResponseRecorder {
	return env.do(env.newRequest(http.MethodPost, "/api/v1/plugins/"+id+"/restore", cred, nil, ""))
}

// seedRemoved writes a tombstoned plugin.json with a fixed removal time, the way RemovePlugin
// leaves it, so list tests control ordering instead of racing the clock.
func seedRemoved(t *testing.T, env *testEnv, id string, removed time.Time, reason, by string) {
	t.Helper()
	st := domain.PluginState{ID: id, Tier: domain.TierOfficial, Removed: &removed, RemovalReason: reason, RemovedBy: by}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := env.Store.Write(context.Background(), storage.PluginStatePath(id), data, 0); err != nil {
		t.Fatalf("seed removed state: %v", err)
	}
}

// TestPublishingToARemovedIdIsGoneNotNotFound pins FR-OP-04: a removed id still has its
// plugin.json, so it is not "not found" — and a CI log that says 404 sends the reader looking for
// a plugin that was never created. It must say the id is withdrawn, who did it and why, and that
// only an operator can reopen it; and it must say so on both publish routes.
//
// Kills mapping the publish refusal back to ErrNotFound / 404, and a PublishFirst that leaves the
// removed case as a bare 409.
func TestPublishingToARemovedIdIsGoneNotNotFound(t *testing.T) {
	env := newTestEnv(t)
	if rec := publishAsOIDC(t, env, manifestAt(testOIDCPluginID, "1.0.0")); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}
	const reason = "Superseded by plugin-auth-saml-v2"
	removePluginViaHTTP(t, env, testOIDCPluginID, reason)

	check := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusGone {
			t.Fatalf("status %d, want 410 body=%s", rec.Code, rec.Body.String())
		}
		var got PluginRemovedErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("body is not the removed-plugin error: %v body=%s", err, rec.Body.String())
		}
		// the envelope half goes through the same schema check every other error does
		assertOpenAPIErrorSchema(t, ErrorResponse{Code: got.Code, Message: got.Message})
		if got.Code != CodeGone {
			t.Errorf("code %q, want %q", got.Code, CodeGone)
		}
		if got.RemovalReason != reason {
			t.Errorf("removalReason %q, want %q", got.RemovalReason, reason)
		}
		if got.RemovedBy != "operator@example.com" {
			t.Errorf("removedBy %q, want the operator who removed it", got.RemovedBy)
		}
		if got.Removed.IsZero() {
			t.Errorf("removed timestamp missing from the tombstone body")
		}
		for _, want := range []string{testOIDCPluginID, reason, "restore"} {
			if !strings.Contains(got.Message, want) {
				t.Errorf("message %q does not mention %q", got.Message, want)
			}
		}
	}

	t.Run("new version by the plugin's own CI token", func(t *testing.T) {
		check(t, publishAsOIDC(t, env, manifestAt(testOIDCPluginID, "1.0.1")))
	})
	t.Run("new version by operator session", func(t *testing.T) {
		check(t, publishVersionViaHTTP(t, env, manifestAt(testOIDCPluginID, "1.0.1")))
	})
	t.Run("first publish by operator session", func(t *testing.T) {
		check(t, publishViaHTTP(t, env, manifestAt(testOIDCPluginID, "1.0.1")))
	})

	t.Run("nothing was written for the refused publish", func(t *testing.T) {
		files, err := env.Store.ListPrefix(context.Background(), "plugins/"+testOIDCPluginID+"/")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(files) != 1 || !strings.HasSuffix(files[0], "plugin.json") {
			t.Errorf("a refused publish left objects behind: %v", files)
		}
	})
}

// TestRestoreReopensARemovedIdForPublishing is the happy path of FR-OP-04a, and the half of the
// contract that is easy to get subtly wrong: restore reopens the id, it does not resurrect the
// plugin.
//
//   - the id stops answering 410 — and does not start answering 200 with versions whose files
//     removal deleted, so it is 404 until something is published;
//   - the operator's record survives: a version blocked before removal is still blocked, so a
//     restore cannot be used to put a withdrawn build back on offer;
//   - the next publish by the plugin's own CI token works and lands in the catalogue.
//
// Kills a restore that keeps the stale version list, one that clears BlockedVersions, and one that
// forgets to clear the tombstone fields.
func TestRestoreReopensARemovedIdForPublishing(t *testing.T) {
	env := newTestEnv(t)
	id := testOIDCPluginID
	for _, v := range []string{"1.0.0", "1.1.0"} {
		if rec := publishAsOIDC(t, env, manifestAt(id, v)); rec.Code != http.StatusCreated {
			t.Fatalf("publish %s: status %d body=%s", v, rec.Code, rec.Body.String())
		}
	}
	blockBody, _ := json.Marshal(map[string]string{"reason": "Ships a vulnerable dependency"})
	if rec := env.do(env.newRequest(http.MethodPost, "/api/v1/plugins/"+id+"/versions/1.0.0/block", credOperatorSession, blockBody, "application/json")); rec.Code != http.StatusOK {
		t.Fatalf("block: status %d body=%s", rec.Code, rec.Body.String())
	}
	removePluginViaHTTP(t, env, id, "Test removal")

	rec := restorePluginViaHTTP(env, id, credOperatorSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status %d body=%s", rec.Code, rec.Body.String())
	}
	var res PluginRestoredResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
	if res.ID != id || res.RestoredBy != "operator@example.com" || res.Restored.IsZero() {
		t.Errorf("restore response = %+v, want id %q restored by the operator with a timestamp", res, id)
	}

	st := readPluginState(t, env, id)
	if st.Removed != nil || st.RemovalReason != "" || st.RemovedBy != "" {
		t.Errorf("tombstone fields survived the restore: removed=%v reason=%q by=%q", st.Removed, st.RemovalReason, st.RemovedBy)
	}
	if st.Restored == nil || st.RestoredBy != "operator@example.com" {
		t.Errorf("restore left no audit trace: restored=%v by=%q", st.Restored, st.RestoredBy)
	}
	if len(st.Versions) != 0 || st.LatestVersion != "" {
		t.Errorf("restore kept versions whose files removal deleted: versions=%v latest=%q", st.Versions, st.LatestVersion)
	}
	if len(st.BlockedVersions) != 1 || st.BlockedVersions[0].Version != "1.0.0" {
		t.Errorf("restore dropped the operator's blocked versions: %v", st.BlockedVersions)
	}

	// reads: no longer a tombstone, and nothing to show yet
	if rec := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+id, credNone, nil, "")); rec.Code != http.StatusNotFound {
		t.Errorf("GET restored plugin with no versions: status %d, want 404 (not 410, not a stale 200)", rec.Code)
	}

	// and the id takes a publish again
	if rec := publishAsOIDC(t, env, manifestAt(id, "2.0.0")); rec.Code != http.StatusCreated {
		t.Fatalf("publish after restore: status %d body=%s", rec.Code, rec.Body.String())
	}
	if got := latestInIndex(t, env, id); got != "2.0.0" {
		t.Errorf("catalogue latestVersion after republish = %q, want 2.0.0", got)
	}
	if rec := env.do(env.newRequest(http.MethodGet, "/api/v1/plugins/"+id, credNone, nil, "")); rec.Code != http.StatusOK {
		t.Errorf("GET after republish: status %d, want 200", rec.Code)
	}
}

// TestRestoreRefusals pins the three ways restore says no, each of which an operator can hit by
// clicking a stale row: an id nobody ever published (404), an id that is not removed (409 — a
// quiet 200 would let two operators both believe they did it), and a plugin's own CI token (403,
// the route is operator-session only).
func TestRestoreRefusals(t *testing.T) {
	env := newTestEnv(t)

	t.Run("unknown id", func(t *testing.T) {
		rec := restorePluginViaHTTP(env, "plugin-never-published", credOperatorSession)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404 body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeErrorEnvelope(t, rec); got.Code != CodeNotFound {
			t.Errorf("code %q, want %q", got.Code, CodeNotFound)
		}
	})

	t.Run("plugin that is not removed", func(t *testing.T) {
		env.seedPlugin(testOtherPluginID, "1.0.0")
		rec := restorePluginViaHTTP(env, testOtherPluginID, credOperatorSession)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409 body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeErrorEnvelope(t, rec); got.Code != CodeConflict {
			t.Errorf("code %q, want %q", got.Code, CodeConflict)
		}
		if st := readPluginState(t, env, testOtherPluginID); st.LatestVersion != "1.0.0" || st.Restored != nil {
			t.Errorf("a refused restore modified the plugin: %+v", st)
		}
	})

	t.Run("already restored", func(t *testing.T) {
		env.seedPlugin("plugin-twice-restored", "1.0.0")
		removePluginViaHTTP(t, env, "plugin-twice-restored", "first")
		if rec := restorePluginViaHTTP(env, "plugin-twice-restored", credOperatorSession); rec.Code != http.StatusOK {
			t.Fatalf("first restore: status %d body=%s", rec.Code, rec.Body.String())
		}
		if rec := restorePluginViaHTTP(env, "plugin-twice-restored", credOperatorSession); rec.Code != http.StatusConflict {
			t.Fatalf("second restore: status %d, want 409 body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("the plugin's own CI token cannot lift its removal", func(t *testing.T) {
		env.seedPlugin("plugin-ci-held", "1.0.0")
		removePluginViaHTTP(t, env, "plugin-ci-held", "again")
		rec := restorePluginViaHTTP(env, "plugin-ci-held", credOIDCPublish)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status %d, want 403 body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeErrorEnvelope(t, rec); got.Code != CodeTokenTypeNotPermitted {
			t.Errorf("code %q, want %q", got.Code, CodeTokenTypeNotPermitted)
		}
		if st := readPluginState(t, env, "plugin-ci-held"); st.Removed == nil {
			t.Errorf("the refused request lifted the removal anyway")
		}
	})
}

// TestListRemovedPlugins pins what the operator UI's Removed tab shows: every tombstone, newest
// first, with the three fields FR-OP-04 names — and only tombstones. The public catalogue omits
// removed plugins by design, so this list is the only place they can be enumerated; it must not
// also fill up with live or restored ones.
func TestListRemovedPlugins(t *testing.T) {
	env := newTestEnv(t)
	base := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	seedRemoved(t, env, "plugin-auth-saml", base, "Registry cleanup", "drcrazy")
	seedRemoved(t, env, "plugin-bts-monday", base.Add(2*time.Hour), "Renamed", "drcrazy")
	seedRemoved(t, env, "billing", base.Add(time.Hour), "Moved to plugin-other-billing", "someone-else")
	env.seedPlugin("plugin-live", "1.0.0")

	list := func() []domain.RemovedPlugin {
		t.Helper()
		rec := env.do(env.newRequest(http.MethodGet, "/api/v1/removed-plugins", credOperatorSession, nil, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		var out RemovedPluginListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		return out.Plugins
	}

	got := list()
	wantOrder := []string{"plugin-bts-monday", "billing", "plugin-auth-saml"}
	if len(got) != len(wantOrder) {
		t.Fatalf("listed %d removed plugins, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("row %d is %q, want %q (newest removal first)", i, got[i].ID, id)
		}
	}
	if got[2].RemovalReason != "Registry cleanup" || got[2].RemovedBy != "drcrazy" || !got[2].Removed.Equal(base) {
		t.Errorf("row lost its tombstone fields: %+v", got[2])
	}

	// restoring takes the plugin off the list again
	if rec := restorePluginViaHTTP(env, "billing", credOperatorSession); rec.Code != http.StatusOK {
		t.Fatalf("restore: status %d body=%s", rec.Code, rec.Body.String())
	}
	if after := list(); len(after) != 2 {
		t.Errorf("restored plugin is still listed as removed: %+v", after)
	}
}

// TestListRemovedIsEmptyArrayNotNull: the UI iterates the array, and `null` would turn "nothing
// removed" into a script error rather than an empty tab.
func TestListRemovedIsEmptyArrayNotNull(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(env.newRequest(http.MethodGet, "/api/v1/removed-plugins", credOperatorSession, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["plugins"]) != "[]" {
		t.Errorf("plugins = %s, want []", raw["plugins"])
	}
}
