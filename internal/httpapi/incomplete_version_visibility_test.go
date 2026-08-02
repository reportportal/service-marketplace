package httpapi

// This file closes the family of bugs described in the branch report: every
// CLIENT-FACING read path in internal/httpapi must agree that "a
// committed-but-incomplete version does not exist" (domain.IsVersionComplete
// false; see that function's doc comment). Earlier rounds each fixed one
// endpoint (first the catalogue listing, then the advertised version list)
// and left the next one -- version detail, artifact download, and the
// version-list-vs-plugin-detail 404 mismatch on a brand-new plugin -- still
// serving or otherwise disagreeing about an interrupted publish. Every test
// below drives the real router (e.newRequest/e.do, the production
// middleware chain) against a state reached through the real publish
// pipeline with injected storage faults (storagetest.FaultStore), never a
// hand-seeded plugin.json -- matching this package's existing precedent
// (lifecycle_housekeeping_test.go's serverWithFaultedLifecycle,
// internal/publish/catalogue_no_complete_version_test.go).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

// doOn sends req through srv's real handler chain, mirroring testEnv.do but
// usable against a Server built by serverWithFaultedLifecycle rather than
// e.Server itself.
func doOn(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// nthPluginStateWriteFailer fails exactly the Nth Write call observed for
// pluginStatePath, letting every other call (including earlier and later
// ones to that same path, and every call to any other path) pass through.
// It exists to model a crash during publish()'s follow-up "mark this
// version's artifacts complete" compare-and-swap (markVersionComplete):
// that write lands on the exact same plugin.json path as the earlier commit
// CAS, so it can't be targeted by storagetest.FaultStore's Fail/FailN alone
// (those arm by (op, path), not by which occurrence of a repeated path they
// should fire on) -- see internal/publish/service_test.go's
// secondWriteFailer, the precedent this mirrors.
type nthPluginStateWriteFailer struct {
	*storagetest.FaultStore
	pluginStatePath string
	n               int
	err             error
	calls           int
}

func (f *nthPluginStateWriteFailer) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	if objectPath == f.pluginStatePath {
		f.calls++
		if f.calls == f.n {
			return 0, f.err
		}
	}
	return f.FaultStore.Write(ctx, objectPath, data, expectedGen)
}

func incompleteVisTestManifest(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

func mustBuildJAR(t *testing.T, m *domain.Manifest) []byte {
	t.Helper()
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	return jar
}

// --- state (a): interrupted before any artifact was written ---------------

// TestReadPaths_FirstPublishInterruptedBeforeAnyArtifact_VersionInvisibleEverywhere
// is the brand-new-plugin repro from the branch report: PublishFirst's jar
// write fails, so plugin.json commits a VersionMeta with Complete: false and
// LatestVersion stays "", but NOT ONE artifact object (jar, manifest) is
// ever written. Before this fix: GET .../versions listed the version (200)
// while GET .../versions/{v} and the artifact both 404'd. After this fix,
// EVERY client read path must agree the version does not exist: the version
// list must omit it, detail and artifact must 404, and plugin detail (the
// index-side path that was already correct) must keep 404ing too.
//
// Mutation this kills: reverting catalogue.Service.loadPlugin's completeness
// filter (or GetVersion's committed-document check) makes the versions-list
// assertion below see the phantom "1.0.0" entry again, or makes GetVersion
// serve it via manifest-object existence.
func TestReadPaths_FirstPublishInterruptedBeforeAnyArtifact_VersionInvisibleEverywhere(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-fresh-interrupted"

	m := incompleteVisTestManifest(pluginID, "1.0.0")
	jar := mustBuildJAR(t, m)

	fs := storagetest.Wrap(e.Store)
	artPath := storage.VersionArtifactPath(pluginID, "1.0.0", string(domain.AccessPublic))
	boom := errors.New("simulated crash before any artifact write")
	fs.FailN(storagetest.OpWrite, artPath, boom, 1)
	faultySrv := e.serverWithFaultedLifecycle(fs)

	body, contentType := buildPublishMultipart(t, jar)
	req := e.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body, contentType)
	rec := doOn(faultySrv, req)
	if rec.Code == http.StatusCreated {
		t.Fatalf("expected the injected jar-write failure to surface (not 201), got 201: body=%s", rec.Body.String())
	}

	// Precondition: plugin.json really did commit an incomplete entry, and
	// no artifact object exists -- otherwise the assertions below would not
	// be observing the mixed state the bug report describes (see this
	// branch's own precedent for catching a test that passed by accident).
	stObj, err := e.Store.Read(context.Background(), storage.PluginStatePath(pluginID))
	if err != nil {
		t.Fatalf("test precondition: plugin.json must exist after the interrupted first publish: %v", err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Versions) != 1 || domain.IsVersionComplete(st.Versions[0]) {
		t.Fatalf("test precondition violated: want exactly one committed-but-incomplete version, got %+v", st.Versions)
	}
	if exists, _ := e.Store.Exists(context.Background(), storage.VersionManifestPath(pluginID, "1.0.0")); exists {
		t.Fatalf("test precondition violated: manifest must not exist after a crash before any artifact write")
	}

	// From here on, use a clean server (no faults armed) reading the SAME
	// backing store -- exactly what a fresh request after the crash sees.
	clean := e.serverWithFaultedLifecycle(e.Store)

	t.Run("version list omits it", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET .../versions: status = %d, want 200 (the plugin record itself exists, just with nothing to list): body=%s", rec.Code, rec.Body.String())
		}
		var out PluginVersionListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Versions) != 0 {
			t.Fatalf("GET .../versions: versions = %+v, want empty -- the interrupted 1.0.0 must be omitted, not listed", out.Versions)
		}
	})

	t.Run("version detail 404s", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET .../versions/1.0.0: status = %d, want 404: body=%s", rec.Code, rec.Body.String())
		}
		errBody := decodeErrorEnvelope(t, rec)
		if errBody.Message != "Version not found" {
			t.Fatalf("GET .../versions/1.0.0: message = %q, want %q (the plugin DOES exist -- only the version doesn't; see the genuine bug this branch also fixes)", errBody.Message, "Version not found")
		}
	})

	t.Run("artifact download 404s", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0/artifact", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET .../artifact: status = %d, want 404: body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("plugin detail 404s (already-correct index-side path)", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID, credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /plugins/%s: status = %d, want 404: body=%s", pluginID, rec.Code, rec.Body.String())
		}
	})
}

// --- state (b): manifest written, completion marker never flipped --------

// TestReadPaths_CompletionMarkerNeverFlipped_MixedPluginHidesOnlyIncompleteVersion
// is the second branch-report repro: an EXISTING plugin (one clean, complete
// 1.0.0) gets a second publish, 2.0.0, whose jar/manifest/changelog all land
// successfully but whose completion-marker CAS (markVersionComplete) crashes.
// Before this fix: the catalogue listing already hid 2.0.0 (an earlier
// round's fix), but the version list, version detail (200), and the
// artifact download (302 + the whole jar) all still served it. This test
// proves every one of those now agrees with the catalogue: 2.0.0 is
// invisible, 1.0.0 stays fully served.
//
// Mutation this kills: any read path that keys visibility off manifest-
// object existence (GetVersion's old behaviour) instead of the committed,
// completeness-filtered plugin.json would keep serving 2.0.0's detail and
// artifact here, since its manifest genuinely IS on disk.
func TestReadPaths_CompletionMarkerNeverFlipped_MixedPluginHidesOnlyIncompleteVersion(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-mixed-http"

	// v1.0.0: a clean, complete publish through the real router.
	m1 := incompleteVisTestManifest(pluginID, "1.0.0")
	jar1 := mustBuildJAR(t, m1)
	body1, ct1 := buildPublishMultipart(t, jar1)
	rec1 := e.do(e.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body1, ct1))
	if rec1.Code != http.StatusCreated {
		t.Fatalf("seed publish of 1.0.0: expected 201, got %d body=%s", rec1.Code, rec1.Body.String())
	}

	// v2.0.0: every artifact write succeeds, but the completion-marker CAS
	// (the SECOND write to plugin.json within this call: first the commit
	// CAS, then markVersionComplete) fails.
	m2 := incompleteVisTestManifest(pluginID, "2.0.0")
	jar2 := mustBuildJAR(t, m2)
	fs := storagetest.Wrap(e.Store)
	boom := errors.New("simulated crash during completion-marker write")
	cs := &nthPluginStateWriteFailer{FaultStore: fs, pluginStatePath: storage.PluginStatePath(pluginID), n: 2, err: boom}
	faultySrv := e.serverWithFaultedLifecycle(cs)

	body2, ct2 := buildPublishMultipart(t, jar2)
	req2 := e.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions", credOperatorSession, body2, ct2)
	rec2 := doOn(faultySrv, req2)
	if rec2.Code == http.StatusCreated || rec2.Code == http.StatusOK {
		t.Fatalf("expected the injected completion-marker write failure to surface as an error status, got %d body=%s", rec2.Code, rec2.Body.String())
	}

	// Precondition: 2.0.0's jar and manifest are really on disk, but
	// plugin.json still marks it incomplete -- otherwise this test isn't
	// observing the state the bug report describes.
	if _, err := e.Store.Read(context.Background(), storage.VersionArtifactPath(pluginID, "2.0.0", string(domain.AccessPublic))); err != nil {
		t.Fatalf("test precondition: 2.0.0 jar must be on disk after the completion-marker crash: %v", err)
	}
	if _, err := e.Store.Read(context.Background(), storage.VersionManifestPath(pluginID, "2.0.0")); err != nil {
		t.Fatalf("test precondition: 2.0.0 manifest must be on disk after the completion-marker crash: %v", err)
	}
	stObj, err := e.Store.Read(context.Background(), storage.PluginStatePath(pluginID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		t.Fatal(err)
	}
	for _, v := range st.Versions {
		if v.Version == "2.0.0" && domain.IsVersionComplete(v) {
			t.Fatalf("test precondition violated: 2.0.0 must not be Complete after the injected crash")
		}
	}

	clean := e.serverWithFaultedLifecycle(e.Store)

	t.Run("version list advertises only 1.0.0", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
		var out PluginVersionListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Versions) != 1 || out.Versions[0].Version != "1.0.0" {
			t.Fatalf("versions = %+v, want exactly [1.0.0] -- 2.0.0 must stay hidden", out.Versions)
		}
	})

	t.Run("2.0.0 detail 404s", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/2.0.0", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("2.0.0 artifact 404s", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/2.0.0/artifact", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("1.0.0 stays fully visible: detail", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("1.0.0 stays fully visible: artifact", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0/artifact", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 (public artifact redirect): body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("plugin detail still reports latestVersion 1.0.0", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID, credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
		var out PluginDetailResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.LatestVersion != "1.0.0" {
			t.Fatalf("latestVersion = %q, want %q", out.LatestVersion, "1.0.0")
		}
	})
}

// --- state (c): the same version after a successful retry ----------------

// TestReadPaths_SuccessfulRetryAfterInterruption_VersionFullyVisibleEverywhere
// closes the loop: a brand-new plugin's first publish is interrupted before
// any artifact write (state (a)), then retried against a healthy store with
// byte-identical content. AMD-04's healing branch must finish the artifact
// writes and flip Complete to true, and every client read path must then
// serve the version normally -- list, detail, and artifact download alike.
func TestReadPaths_SuccessfulRetryAfterInterruption_VersionFullyVisibleEverywhere(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-retry-http"

	m := incompleteVisTestManifest(pluginID, "1.0.0")
	jar := mustBuildJAR(t, m)

	fs := storagetest.Wrap(e.Store)
	artPath := storage.VersionArtifactPath(pluginID, "1.0.0", string(domain.AccessPublic))
	boom := errors.New("simulated crash before any artifact write")
	fs.FailN(storagetest.OpWrite, artPath, boom, 1)
	faultySrv := e.serverWithFaultedLifecycle(fs)

	body1, ct1 := buildPublishMultipart(t, jar)
	rec1 := doOn(faultySrv, e.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body1, ct1))
	if rec1.Code == http.StatusCreated {
		t.Fatalf("expected the injected jar-write failure to surface, got 201: body=%s", rec1.Body.String())
	}

	// Sanity: before the retry, the version must NOT be visible -- otherwise
	// the "now visible" assertions below would prove nothing new.
	clean := e.serverWithFaultedLifecycle(e.Store)
	preReq := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0", credNone, nil, "")
	preRec := doOn(clean, preReq)
	if preRec.Code != http.StatusNotFound {
		t.Fatalf("test precondition violated: version must 404 before the retry, got %d body=%s", preRec.Code, preRec.Body.String())
	}

	// Retry: same plugin, same version, same content, no faults armed --
	// takes AMD-04's healing branch via POST .../versions (autoCreate not
	// needed; the plugin record already exists, just incomplete).
	body2, ct2 := buildPublishMultipart(t, jar)
	req2 := e.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions", credOperatorSession, body2, ct2)
	rec2 := doOn(clean, req2)
	if rec2.Code != http.StatusOK && rec2.Code != http.StatusCreated {
		t.Fatalf("healing retry: status = %d, want 200 or 201: body=%s", rec2.Code, rec2.Body.String())
	}

	t.Run("version list now includes it", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
		var out PluginVersionListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Versions) != 1 || out.Versions[0].Version != "1.0.0" {
			t.Fatalf("versions = %+v, want exactly [1.0.0] now that the retry finished", out.Versions)
		}
	})

	t.Run("version detail now 200s", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("artifact download now succeeds", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0/artifact", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302: body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("plugin detail now 200s with latestVersion 1.0.0", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID, credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
		var out PluginDetailResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.LatestVersion != "1.0.0" {
			t.Fatalf("latestVersion = %q, want %q", out.LatestVersion, "1.0.0")
		}
	})
}

// --- the genuine bug: "Plugin not found" for a plugin that DOES exist -----

// TestGetVersion_UnknownVersionOnExistingPlugin_ReturnsVersionNotFoundNotPluginNotFound
// is the standalone regression test for the bug called out in the branch
// report: handleGetVersion answered "Plugin not found" whenever
// catalogue.GetVersion returned ErrNotFound, which it did for BOTH "the
// plugin doesn't exist" and "the plugin exists but this version doesn't" --
// collapsing two different truths into one message. This plugin exists,
// cleanly, with one complete version; asking for a version that was never
// published must say so, not claim the plugin itself is missing.
//
// Mutation this kills: reverting handleGetVersion to map every
// catalogue.GetVersion error to "Plugin not found" makes the message
// assertion below fail.
func TestGetVersion_UnknownVersionOnExistingPlugin_ReturnsVersionNotFoundNotPluginNotFound(t *testing.T) {
	e := newTestEnv(t)
	e.seedPlugin("plugin-known", "1.0.0")

	req := e.newRequest(http.MethodGet, "/api/v1/plugins/plugin-known/versions/9.9.9", credNone, nil, "")
	rec := e.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: body=%s", rec.Code, rec.Body.String())
	}
	errBody := decodeErrorEnvelope(t, rec)
	if errBody.Message != "Version not found" {
		t.Fatalf("message = %q, want %q -- plugin-known DOES exist, only version 9.9.9 doesn't", errBody.Message, "Version not found")
	}
}

// --- neighbouring-state guard: blocking is a different axis --------------

// TestGetArtifact_BlockedCompleteVersion_Stays403NotDemotedTo404 guards the
// axis this branch must not confuse: a blocked version is COMPLETE but
// un-installable, and must keep its existing 403-with-reason behaviour. This
// plugin's only version publishes cleanly (Complete implicitly true) and is
// then blocked -- the completeness filter this branch adds to
// catalogue.Service.loadPlugin must never treat "blocked" as "incomplete",
// or the artifact route would wrongly 404 instead of 403.
//
// Mutation this kills: folding BlockedVersions into the completeness filter
// (or checking blocked status via st.Versions membership instead of the
// separate BlockedVersions slice) would turn this 403 into a 404.
func TestGetArtifact_BlockedCompleteVersion_Stays403NotDemotedTo404(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-blocked-http"

	m := incompleteVisTestManifest(pluginID, "1.0.0")
	jar := mustBuildJAR(t, m)
	body, ct := buildPublishMultipart(t, jar)
	recPublish := e.do(e.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body, ct))
	if recPublish.Code != http.StatusCreated {
		t.Fatalf("seed publish: expected 201, got %d body=%s", recPublish.Code, recPublish.Body.String())
	}

	recBlock := e.do(e.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions/1.0.0/block",
		credOperatorSession, []byte(`{"reason":"cve-test"}`), "application/json"))
	if recBlock.Code != http.StatusOK {
		t.Fatalf("BlockVersion: expected 200, got %d body=%s", recBlock.Code, recBlock.Body.String())
	}

	t.Run("artifact download stays 403", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0/artifact", credNone, nil, "")
		rec := e.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (blocked, not incomplete): body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("version detail still 200s, reporting blocked", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/1.0.0", credNone, nil, "")
		rec := e.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
		var out PluginVersionDetailResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !out.Blocked {
			t.Fatalf("blocked = false, want true")
		}
	})

	t.Run("version list still includes it, marked blocked", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions", credNone, nil, "")
		rec := e.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
		}
		var out PluginVersionListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Versions) != 1 || !out.Versions[0].Blocked {
			t.Fatalf("versions = %+v, want exactly one entry, blocked", out.Versions)
		}
	})
}

// TestGetArtifact_BlockedAndIncompleteVersion_404sLikeEveryOtherPath closes the
// last residual disagreement in this family, found by review. handleGetArtifact
// consults st.BlockedVersions BEFORE asking the catalogue whether the version
// exists at all. BlockedVersions is deliberately never touched by loadPlugin's
// completeness filter (blocking is a separate axis -- see
// TestGetArtifact_BlockedCompleteVersion_Stays403NotDemotedTo404), so for a
// version that is BOTH incomplete AND blocked the 403 branch fired for a
// version every other read path reports as non-existent.
//
// The state is genuinely reachable, not theoretical: lifecycle.BlockVersion
// validates the version against its own UNFILTERED read of plugin.json, so an
// operator can successfully block a committed-but-incomplete version.
//
// The rule this branch enforces is that such a version does not exist to a
// client, and "does not exist" must win over "exists but is un-installable":
// 403-with-reason otherwise both contradicts the 404s and confirms the
// existence of a version the rest of the API denies.
//
// Mutation this kills: removing the st.Versions membership guard in front of
// handleGetArtifact's BlockedVersions loop restores the 403 and fails this test.
func TestGetArtifact_BlockedAndIncompleteVersion_404sLikeEveryOtherPath(t *testing.T) {
	e := newTestEnv(t)
	const pluginID = "plugin-blocked-incomplete-http"

	// v1.0.0: a clean publish, so the plugin itself exists and is listable.
	m1 := incompleteVisTestManifest(pluginID, "1.0.0")
	body1, ct1 := buildPublishMultipart(t, mustBuildJAR(t, m1))
	if rec := e.do(e.newRequest(http.MethodPost, "/api/v1/plugins", credOperatorSession, body1, ct1)); rec.Code != http.StatusCreated {
		t.Fatalf("seed publish of 1.0.0: expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	// v2.0.0: artifacts land, the completion-marker CAS crashes.
	m2 := incompleteVisTestManifest(pluginID, "2.0.0")
	fs := storagetest.Wrap(e.Store)
	cs := &nthPluginStateWriteFailer{
		FaultStore:      fs,
		pluginStatePath: storage.PluginStatePath(pluginID),
		n:               2,
		err:             errors.New("simulated crash during completion-marker write"),
	}
	body2, ct2 := buildPublishMultipart(t, mustBuildJAR(t, m2))
	req2 := e.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions", credOperatorSession, body2, ct2)
	if rec := doOn(e.serverWithFaultedLifecycle(cs), req2); rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		t.Fatalf("expected the injected completion-marker failure to surface as an error status, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Precondition: 2.0.0 is committed-but-incomplete, and its jar really is on
	// disk -- so a 403 here would be pointing at bytes that exist.
	stObj, err := e.Store.Read(context.Background(), storage.PluginStatePath(pluginID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range st.Versions {
		if v.Version == "2.0.0" {
			found = true
			if domain.IsVersionComplete(v) {
				t.Fatalf("test precondition violated: 2.0.0 must not be Complete after the injected crash")
			}
		}
	}
	if !found {
		t.Fatalf("test precondition violated: 2.0.0 must be committed to plugin.json, got %+v", st.Versions)
	}

	clean := e.serverWithFaultedLifecycle(e.Store)

	// An operator blocks the invisible version. This is expected to succeed --
	// BlockVersion reads the unfiltered document -- and is what creates the state.
	recBlock := doOn(clean, e.newRequest(http.MethodPost, "/api/v1/plugins/"+pluginID+"/versions/2.0.0/block",
		credOperatorSession, []byte(`{"reason":"cve-test"}`), "application/json"))
	if recBlock.Code != http.StatusOK {
		t.Fatalf("test precondition: blocking the incomplete version must succeed to reach this state, got %d body=%s",
			recBlock.Code, recBlock.Body.String())
	}

	t.Run("artifact 404s rather than 403-blocked", func(t *testing.T) {
		req := e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/2.0.0/artifact", credNone, nil, "")
		rec := doOn(clean, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 -- an incomplete version must not exist to a client, "+
				"and 403-blocked both contradicts the detail/list 404s and confirms it exists: body=%s",
				rec.Code, rec.Body.String())
		}
	})

	t.Run("detail and list agree it does not exist", func(t *testing.T) {
		rec := doOn(clean, e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions/2.0.0", credNone, nil, ""))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("detail status = %d, want 404: body=%s", rec.Code, rec.Body.String())
		}
		recList := doOn(clean, e.newRequest(http.MethodGet, "/api/v1/plugins/"+pluginID+"/versions", credNone, nil, ""))
		var out PluginVersionListResponse
		if err := json.Unmarshal(recList.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Versions) != 1 || out.Versions[0].Version != "1.0.0" {
			t.Fatalf("versions = %+v, want exactly [1.0.0]", out.Versions)
		}
	})
}
