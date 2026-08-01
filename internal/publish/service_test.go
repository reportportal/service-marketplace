package publish

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

func TestExtractManifest(t *testing.T) {
	m := &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExtractManifest(jar)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Version != m.Version {
		t.Fatalf("unexpected manifest: %+v", got)
	}
}

// --- AMD-04-duplicate-publish-contract -------------------------------------

func newTestService(t *testing.T, store storage.ObjectStore) *Service {
	t.Helper()
	return &Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
}

func newLocalStore(t *testing.T) *storage.LocalStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func testManifest(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

// TestPublishVersionIdempotentRetryReturns200NoObjectsWritten is the
// regression test for AMD-04-duplicate-publish-contract branch 2: "Version
// committed AND the uploaded jar's SHA-256 equals the stored checksum -> 200
// with the existing PublishResponse; no objects are written." This is what
// makes a CI retry after a lost 201 response safe.
//
// Mutation that makes this fail: replace the SHA-256 comparison in
// PublishVersion with the old "artifact file exists" check (no idempotent
// branch at all) — see git history at internal/publish/service.go before
// this change; the retry then returns ErrConflict-shaped 409 instead of a
// no-op 200, and idempotent is never true.
func TestPublishVersionIdempotentRetryReturns200NoObjectsWritten(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m := testManifest("plugin-demo", "1.0.0")
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	before, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatalf("read plugin.json before retry: %v", err)
	}

	res, idempotent, err := svc.PublishVersion(ctx, m.ID, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator", false)
	if err != nil {
		t.Fatalf("PublishVersion (byte-identical retry) = %v, want nil error", err)
	}
	if !idempotent {
		t.Fatalf("idempotent = false, want true for a byte-identical republish of a committed version")
	}
	wantSHA := storage.HashSHA256(jar)
	if res.SHA256 != wantSHA {
		t.Fatalf("SHA256 = %q, want %q", res.SHA256, wantSHA)
	}

	after, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatalf("read plugin.json after retry: %v", err)
	}
	if after.Generation != before.Generation {
		t.Fatalf("plugin.json generation changed (%d -> %d): AMD-04 branch 2 requires no objects be written on an idempotent retry", before.Generation, after.Generation)
	}
}

// TestPublishVersionDifferentContentReturns409VersionAlreadyPublished is the
// regression test for AMD-04 branch 3: "Version committed AND the SHA-256
// differs -> 409 Conflict, ErrorResponse.code = VERSION_ALREADY_PUBLISHED".
//
// Mutation that makes this fail: dropping the SHA-256 comparison collapses
// branches 2 and 3 into "always ErrConflict", which happens to still return
// an error here — so the mutation that actually defeats this test is
// removing the version-conflict check entirely (falling through to
// s.publish, silently overwriting a committed version's checksum), which
// violates FR-R-05 immutability and makes this test's error assertion fail
// with err == nil.
func TestPublishVersionDifferentContentReturns409VersionAlreadyPublished(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m1 := testManifest("plugin-demo", "1.0.0")
	m1.Description = "first upload"
	jar1, err := BuildTestJAR(m1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	m2 := testManifest("plugin-demo", "1.0.0")
	m2.Description = "different bytes, same id/version"
	jar2, err := BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}
	if storage.HashSHA256(jar1) == storage.HashSHA256(jar2) {
		t.Fatalf("test fixture bug: jar1 and jar2 hash the same")
	}

	_, idempotent, err := svc.PublishVersion(ctx, "plugin-demo", &Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator", false)
	if idempotent {
		t.Fatalf("idempotent = true, want false for a conflicting republish")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("PublishVersion (different content, same version) = %v, want ErrVersionConflict", err)
	}
}

// TestPublishVersionNotCommittedRetryOverwritesOrphanedArtifact is the
// regression test for AMD-04 branch 1: a version whose publish was
// interrupted before plugin.json committed it leaves an orphaned artifact
// that a retry must overwrite, "regardless of byte-equality with the
// partial state" — even when the retry's bytes differ from the failed
// attempt's.
//
// Mutation that makes this fail: restoring the old create-only
// Write(path, data, 0) for the jar (which silently swallowed
// storage.ErrConflict and kept the orphaned bytes instead of overwriting)
// makes the final assertion fail: the stored artifact's SHA-256 stays
// jarA's instead of becoming jarB's.
func TestPublishVersionNotCommittedRetryOverwritesOrphanedArtifact(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)
	fs := storagetest.Wrap(backing)
	svc := newTestService(t, fs)

	base := testManifest("plugin-demo", "1.0.0")
	jar0, err := BuildTestJAR(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar0, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	mA := testManifest("plugin-demo", "2.0.0")
	mA.Description = "attempt A"
	jarA, err := BuildTestJAR(mA)
	if err != nil {
		t.Fatal(err)
	}

	boom := errors.New("simulated network blip")
	fs.FailN(storagetest.OpWrite, storage.PluginStatePath(base.ID), boom, 1)

	if _, _, err := svc.PublishVersion(ctx, base.ID, &Bundle{JAR: jarA, JARFilename: "p.jar"}, "operator", false); !errors.Is(err, boom) {
		t.Fatalf("expected the injected plugin.json write failure to surface, got %v", err)
	}

	artPath := storage.VersionArtifactPath(base.ID, "2.0.0", string(domain.AccessPublic))
	if ok, _ := backing.Exists(ctx, artPath); !ok {
		t.Fatalf("test precondition: the orphaned 2.0.0 artifact should exist after the interrupted attempt")
	}
	stObj, err := backing.Read(ctx, storage.PluginStatePath(base.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		t.Fatal(err)
	}
	for _, v := range st.Versions {
		if v.Version == "2.0.0" {
			t.Fatalf("test precondition: plugin.json must not reference 2.0.0 after the interrupted attempt")
		}
	}

	mB := testManifest("plugin-demo", "2.0.0")
	mB.Description = "attempt B (retry, different bytes)"
	jarB, err := BuildTestJAR(mB)
	if err != nil {
		t.Fatal(err)
	}
	if storage.HashSHA256(jarA) == storage.HashSHA256(jarB) {
		t.Fatalf("test fixture bug: jarA and jarB hash the same")
	}

	res, idempotent, err := svc.PublishVersion(ctx, base.ID, &Bundle{JAR: jarB, JARFilename: "p.jar"}, "operator", false)
	if err != nil {
		t.Fatalf("retry after interrupted publish: %v", err)
	}
	if idempotent {
		t.Fatalf("idempotent = true, want false: a not-committed retry is a fresh write, not AMD-04 branch 2")
	}
	wantSHA := storage.HashSHA256(jarB)
	if res.SHA256 != wantSHA {
		t.Fatalf("SHA256 = %q, want %q (retry content)", res.SHA256, wantSHA)
	}

	artObj, err := backing.Read(ctx, artPath)
	if err != nil {
		t.Fatalf("read overwritten artifact: %v", err)
	}
	if got := storage.HashSHA256(artObj.Data); got != wantSHA {
		t.Fatalf("stored artifact bytes were not overwritten by the retry: got sha %q, want %q (attempt A's orphaned bytes were kept)", got, wantSHA)
	}
}

// --- AMD-06-removal-lifecycle / D-06 ----------------------------------------

// TestPublishFirstExistingLivePluginReturns409PluginAlreadyExists guards the
// non-tombstoned half of AMD-04/AMD-06's PublishFirst rule: a live
// plugin.json (removed == nil) always 409s, directing the caller to the
// versions route — it is never treated as a resurrection.
//
// Mutation that makes this fail: dropping the `st.Removed == nil` guard
// (treating any existing plugin.json as a resurrection candidate) makes
// PublishFirst silently overwrite a live plugin's state instead of
// returning ErrPluginExists.
func TestPublishFirstExistingLivePluginReturns409PluginAlreadyExists(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m := testManifest("plugin-demo", "1.0.0")
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); !errors.Is(err, ErrPluginExists) {
		t.Fatalf("PublishFirst on an existing live plugin = %v, want ErrPluginExists", err)
	}
}

// TestPublishFirstResurrectsTombstonedPlugin is the regression test for
// AMD-06-removal-lifecycle / D-06 (adopted): "POST /api/v1/plugins ... is
// the explicit resurrection path: it resets removed/removalReason/removedBy
// to null, publishes the uploaded version ..., and regenerates index.json."
// It also proves the other half of D-06 — "explicitly NOT through the CI
// auto-create path" — by asserting PublishVersion(autoCreate=true) still
// 410s against the same tombstone.
//
// Mutation that makes this fail: PublishFirst's old unconditional
// `if _, err := s.Store.Read(...); err == nil { return nil, ErrConflict }`
// (no Removed inspection at all) makes the resurrection call return
// ErrPluginExists-shaped 409 instead of succeeding, and the tombstone is
// never cleared.
func TestPublishFirstResurrectsTombstonedPlugin(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m := testManifest("plugin-demo", "1.0.0")
	jar1, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	// Simulate lifecycle.Service.RemovePlugin's tombstone write directly
	// (importing internal/lifecycle here would be an import cycle: it
	// already imports internal/publish).
	tombstoneAt := time.Now().UTC()
	obj, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatal(err)
	}
	st.Removed = &tombstoneAt
	st.RemovalReason = "DMCA claim"
	st.RemovedBy = "operator@example.com"
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(ctx, storage.PluginStatePath(m.ID), body, obj.Generation); err != nil {
		t.Fatalf("seed tombstone: %v", err)
	}

	// D-06: the auto-create (CI/OIDC) path must never resurrect.
	_, _, err = svc.PublishVersion(ctx, m.ID, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator", true)
	var removedErr *RemovedError
	if !errors.As(err, &removedErr) {
		t.Fatalf("PublishVersion(autoCreate=true) on a tombstoned plugin = %v, want *RemovedError (410) — the auto-create path must never resurrect", err)
	}

	m2 := testManifest("plugin-demo", "1.0.1")
	jar2, err := BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.PublishFirst(ctx, &Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator")
	if err != nil {
		t.Fatalf("PublishFirst resurrection: %v", err)
	}
	if res.Version != "1.0.1" {
		t.Fatalf("resurrection published version %q, want 1.0.1", res.Version)
	}

	obj2, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st2 domain.PluginState
	if err := json.Unmarshal(obj2.Data, &st2); err != nil {
		t.Fatal(err)
	}
	if st2.Removed != nil {
		t.Fatalf("resurrection must clear Removed, still set to %v", st2.Removed)
	}
	if st2.RemovalReason != "" || st2.RemovedBy != "" {
		t.Fatalf("resurrection must clear RemovalReason/RemovedBy, got %q/%q", st2.RemovalReason, st2.RemovedBy)
	}
	if st2.LatestVersion != "1.0.1" {
		t.Fatalf("LatestVersion = %q, want 1.0.1", st2.LatestVersion)
	}
}

// midPublishRemover wraps a real store and, the moment the first jar object
// is written, injects a concurrent tombstone commit into plugin.json —
// modelling lifecycle.Service.RemovePlugin racing in between PublishVersion's
// own top-level Removed check and publish()'s plugin.json compare-and-swap.
type midPublishRemover struct {
	storage.ObjectStore
	t        *testing.T
	pluginID string
	fired    bool
}

func (m *midPublishRemover) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	n, err := m.ObjectStore.Write(ctx, objectPath, data, expectedGen)
	if err == nil && !m.fired && strings.HasSuffix(objectPath, ".jar") {
		m.fired = true
		obj, rerr := m.ObjectStore.Read(ctx, storage.PluginStatePath(m.pluginID))
		if rerr != nil {
			m.t.Fatalf("midPublishRemover: read plugin.json: %v", rerr)
		}
		var st domain.PluginState
		if uerr := json.Unmarshal(obj.Data, &st); uerr != nil {
			m.t.Fatalf("midPublishRemover: unmarshal plugin.json: %v", uerr)
		}
		now := time.Now().UTC()
		st.Removed = &now
		st.RemovalReason = "raced removal"
		st.RemovedBy = "operator@example.com"
		body, merr := json.MarshalIndent(st, "", "  ")
		if merr != nil {
			m.t.Fatalf("midPublishRemover: marshal: %v", merr)
		}
		if _, werr := m.ObjectStore.Write(ctx, storage.PluginStatePath(m.pluginID), body, obj.Generation); werr != nil {
			m.t.Fatalf("midPublishRemover: write tombstone: %v", werr)
		}
	}
	return n, err
}

// TestPublishVersionObservesRemovalThatCommitsMidPublish is the regression
// test for the go-assessment finding "PublishVersion checks the removed
// flag once at the top of the flow, but its own plugin.json
// compare-and-swap callback never re-checks it — so a removal that commits
// mid-publish is not observed." A DELETE that lands in the window between
// PublishVersion's initial read and publish()'s own plugin.json CAS must
// still be observed by that CAS, not silently overwritten by the in-flight
// publish.
//
// Mutation that makes this fail: removing the `if st.Removed != nil`
// re-check inside publish()'s WriteWithRetry callback (leaving only
// PublishVersion's earlier one-time check) makes this call succeed and
// append 2.0.0 to a tombstoned plugin.json instead of returning
// *RemovedError.
func TestPublishVersionObservesRemovalThatCommitsMidPublish(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)

	m := testManifest("plugin-demo", "1.0.0")
	jar1, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	seedSvc := newTestService(t, backing)
	if _, err := seedSvc.PublishFirst(ctx, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	remover := &midPublishRemover{ObjectStore: backing, t: t, pluginID: m.ID}
	svc := newTestService(t, remover)

	m2 := testManifest("plugin-demo", "2.0.0")
	jar2, err := BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = svc.PublishVersion(ctx, m.ID, &Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator", false)
	var removedErr *RemovedError
	if !errors.As(err, &removedErr) {
		t.Fatalf("PublishVersion racing a mid-flight removal = %v, want *RemovedError (410)", err)
	}
	if !remover.fired {
		t.Fatalf("test bug: the simulated concurrent removal never fired")
	}

	obj, err := backing.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatal(err)
	}
	for _, v := range st.Versions {
		if v.Version == "2.0.0" {
			t.Fatalf("plugin.json must not record 2.0.0 when removal committed mid-publish")
		}
	}
}
