package lifecycle

// This file exercises internal/lifecycle.OrphanCleanup, the AMD-27
// orphan-cleanup sweep. Per the workstream brief the ordering is not
// negotiable -- "defuse first, then make it correct" -- so every test here
// proves a guard fails safe (deletes nothing) before any test proves the
// mechanism actually deletes anything. storagetest.FaultStore supplies the
// "reference document fails to load" and "age cannot be determined" fault
// injection; the age-guard tests use an injected clock (Config.Now is a real
// field on Now, not FaultStore) rather than wall-clock sleeps, per AMD-27's
// own acceptance criteria ("aged past 24h (injected clock)").

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

func newCleanupStore(t *testing.T) *storage.LocalStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func baseCleanupConfig() CleanupConfig {
	return CleanupConfig{
		Enabled:     true,
		DryRun:      false,
		MinAge:      24 * time.Hour,
		RunInterval: 24 * time.Hour,
		LeaseTTL:    15 * time.Minute,
	}
}

// seedIndex writes a real index.json with the given plugin -> committed
// versions mapping, exactly the shape a fully successful rebuildIndex
// produces -- including Complete: true, since every plugin passed in here is
// modelled as having resolved cleanly (see domain.Index.Complete).
func seedIndex(t *testing.T, store storage.ObjectStore, plugins map[string][]string) {
	t.Helper()
	idx := domain.Index{Complete: true}
	for id, versions := range plugins {
		idx.Plugins = append(idx.Plugins, domain.IndexPlugin{ID: id, Name: id, Versions: versions})
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if _, err := store.Write(context.Background(), storage.PathIndex, data, 0); err != nil {
		t.Fatalf("seed index.json: %v", err)
	}
}

// seedVersionObject writes a single artifact object under a plugin/version
// directory, the way a real publish would.
func seedVersionObject(t *testing.T, store storage.ObjectStore, pluginID, version string) string {
	t.Helper()
	p := storage.VersionArtifactPath(pluginID, version, "public")
	if _, err := store.Write(context.Background(), p, []byte("jar-bytes"), 0); err != nil {
		t.Fatalf("seed version object %s: %v", p, err)
	}
	return p
}

func mustExist(t *testing.T, store storage.ObjectStore, path string, want bool) {
	t.Helper()
	ok, err := store.Exists(context.Background(), path)
	if err != nil {
		t.Fatalf("Exists(%s): %v", path, err)
	}
	if ok != want {
		t.Fatalf("Exists(%s) = %v, want %v", path, ok, want)
	}
}

// TestOrphanCleanup_DisabledByDefault proves a zero-value CleanupConfig (the
// production default: Config{} embedded in an unconfigured Service) performs
// no storage I/O at all -- not even a lease read. Every operation is armed to
// fail via FaultStore; if the disabled check were removed (or moved after
// the first storage call), Run would either hit an armed fault (reported as
// Aborted, not the "disabled" skip this test asserts) or panic.
func TestOrphanCleanup_DisabledByDefault(t *testing.T) {
	fs := storagetest.Wrap(newCleanupStore(t))
	fs.Fail(storagetest.OpRead, storagetest.AnyKey, errors.New("must not be called while disabled"))
	fs.Fail(storagetest.OpWrite, storagetest.AnyKey, errors.New("must not be called while disabled"))
	fs.Fail(storagetest.OpDelete, storagetest.AnyKey, errors.New("must not be called while disabled"))
	fs.Fail(storagetest.OpListPrefix, storagetest.AnyKey, errors.New("must not be called while disabled"))

	job := &OrphanCleanup{Store: fs, Config: CleanupConfig{}, Owner: "replica-1"}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Ran {
		t.Fatalf("report.Ran = true, want false: %+v", report)
	}
	if report.Aborted {
		t.Fatalf("report.Aborted = true, want false (disabled means skipped, not aborted): %+v", report)
	}
	if report.SkipReason != "disabled" {
		t.Fatalf("SkipReason = %q, want %q", report.SkipReason, "disabled")
	}
}

// TestOrphanCleanup_AbortsWhenIndexFailsToLoad is the fault-injection guard
// the workstream brief calls out explicitly: "a run against a reference
// document that fails to load must delete nothing at all." A real orphan
// candidate (unreferenced by any index, aged well past MinAge) is seeded so
// there is something to wrongly delete if the guard is missing.
func TestOrphanCleanup_AbortsWhenIndexFailsToLoad(t *testing.T) {
	backing := newCleanupStore(t)
	seedIndex(t, backing, map[string][]string{}) // present but will fail to *read*
	orphan := seedVersionObject(t, backing, "plugin-x", "1.0.0")

	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpRead, storage.PathIndex, errors.New("boom: index unreadable"))

	job := &OrphanCleanup{
		Store:  fs,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().Add(48 * time.Hour) },
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Aborted {
		t.Fatalf("report.Aborted = false, want true: %+v", report)
	}
	if report.Deleted != 0 {
		t.Fatalf("report.Deleted = %d, want 0", report.Deleted)
	}
	mustExist(t, backing, orphan, true)
}

// TestOrphanCleanup_RefusesWhenIndexRecordsNoVersionsAtAll covers the other
// "reference data looks wrong" case: an index.json that loads fine but whose
// documented plugins collectively carry zero versions while storage plainly
// holds version directories. This is exactly the shape a pre-AMD-27
// index.json has (no `versions` field existed before this change) -- if
// OrphanCleanup trusted it, every version directory in the registry would
// read as unreferenced on the first run after upgrade.
func TestOrphanCleanup_RefusesWhenIndexRecordsNoVersionsAtAll(t *testing.T) {
	store := newCleanupStore(t)
	// A legacy-shaped index.json: plugin listed, but no "versions" key.
	if _, err := store.Write(context.Background(), storage.PathIndex,
		[]byte(`{"plugins":[{"id":"plugin-x","name":"X","latestVersion":"1.0.0"}]}`), 0); err != nil {
		t.Fatalf("seed legacy index.json: %v", err)
	}
	orphan := seedVersionObject(t, store, "plugin-x", "1.0.0")

	job := &OrphanCleanup{
		Store:  store,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().Add(48 * time.Hour) },
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Aborted {
		t.Fatalf("report.Aborted = false, want true: %+v", report)
	}
	if report.Deleted != 0 {
		t.Fatalf("report.Deleted = %d, want 0", report.Deleted)
	}
	mustExist(t, store, orphan, true)
}

// TestOrphanCleanup_RefusesOnEmptyIndexNotMarkedComplete is the MAJOR fix:
// before this change, the refuse-to-delete guard was conjoined with
// `len(idx.Plugins) > 0`, so a syntactically valid `{"plugins":[]}` document
// -- which references zero versions just as thoroughly as the "plugins
// listed but all empty" shape the guard already caught -- sailed straight
// through it. Zero plugins referenced means every version directory in
// storage reads as an orphan. This index.json was never written by
// rebuildIndex (which always sets "complete":true on success), so it must be
// refused on that basis regardless of how many plugins it lists.
func TestOrphanCleanup_RefusesOnEmptyIndexNotMarkedComplete(t *testing.T) {
	store := newCleanupStore(t)
	if _, err := store.Write(context.Background(), storage.PathIndex, []byte(`{"plugins":[]}`), 0); err != nil {
		t.Fatalf("seed empty index.json: %v", err)
	}
	orphan := seedVersionObject(t, store, "plugin-x", "1.0.0")

	job := &OrphanCleanup{
		Store:  store,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().Add(48 * time.Hour) },
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Aborted {
		t.Fatalf("report.Aborted = false, want true (an index not marked complete must never be trusted, empty or not): %+v", report)
	}
	if report.Deleted != 0 {
		t.Fatalf("report.Deleted = %d, want 0", report.Deleted)
	}
	mustExist(t, store, orphan, true)
}

// TestOrphanCleanup_DoesNotDeleteVersionsOfAPluginItCannotRead is this
// package's version of "defeat your own guard": it runs the real
// publish.Service (so index.json is exactly what production code writes),
// legitimately publishes two plugins, then makes one plugin's plugin.json
// unreadable -- reference data that is only PARTIALLY unavailable, the exact
// shape the pre-fix guard missed (every OTHER plugin still has versions, so
// the old "all plugins report zero versions" heuristic never fires). It
// proves the sweeper deletes nothing at all against that store: not
// plugin-bad's version (protected because rebuildIndex refused to drop it
// from index.json), and not plugin-good's version either (protected because
// it is still legitimately referenced).
func TestOrphanCleanup_DoesNotDeleteVersionsOfAPluginItCannotRead(t *testing.T) {
	root := t.TempDir()
	backing, err := storage.NewLocalStore(root, "http://cdn.test", "signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	pub := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}

	publishManifest(t, pub, "plugin-good", "1.0.0")
	publishManifest(t, pub, "plugin-bad", "1.0.0")

	goodPath := storage.VersionArtifactPath("plugin-good", "1.0.0", "public")
	badPath := storage.VersionArtifactPath("plugin-bad", "1.0.0", "public")
	mustExist(t, backing, goodPath, true)
	mustExist(t, backing, badPath, true)

	// Simulate plugin-bad's plugin.json becoming unreadable sometime after
	// it was legitimately published (bit rot, a bad manual edit, whatever).
	// A later, unrelated operator action (SetTier/RemovePlugin on any
	// plugin, or another publish) would trigger rebuildIndex against this
	// store; per the BLOCKING fix it refuses to overwrite index.json, so the
	// document on disk stays exactly what it was right after both
	// legitimate publishes -- still referencing both plugins.
	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpRead, storage.PluginStatePath("plugin-bad"), errors.New("boom: plugin-bad/plugin.json unreadable"))

	faultyPub := &publish.Service{Store: fs, Invalidator: cdn.NoopInvalidator{}}
	if err := faultyPub.RebuildIndex(context.Background()); err == nil {
		t.Fatalf("RebuildIndex succeeded despite plugin-bad being unreadable -- setup invalid for this test")
	}

	job := &OrphanCleanup{
		Store:  fs,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().Add(48 * time.Hour) },
	}
	report, runErr := job.Run(context.Background())
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if report.Deleted != 0 {
		t.Fatalf("report.Deleted = %d, want 0 (a partially-unreadable store must delete nothing at all): %+v", report.Deleted, report)
	}
	mustExist(t, backing, goodPath, true)
	mustExist(t, backing, badPath, true)
}

// publishManifest publishes a minimal valid version for pluginID/version via
// a real publish.Service, so index.json and plugin.json reflect exactly what
// production code writes.
func publishManifest(t *testing.T, pub *publish.Service, pluginID, version string) {
	t.Helper()
	m := &domain.Manifest{
		ID: pluginID, Name: pluginID, Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR(%s): %v", pluginID, err)
	}
	if _, err := pub.PublishFirst(context.Background(), &publish.Bundle{JAR: jar, JARFilename: "p.jar", Screenshots: map[string][]byte{}}, "op"); err != nil {
		t.Fatalf("PublishFirst(%s): %v", pluginID, err)
	}
}

// TestOrphanCleanup_AgeGuardHoldsRecentlyWrittenObject is the other
// fault-injection guard from the brief: "a run that starts while a publish
// is mid-flight must not delete that publish's objects." A publish writes
// its artifact object before it commits plugin.json/index.json, so from the
// sweep's point of view a fresh, unreferenced object looks identical to a
// genuine orphan -- the age guard is what tells them apart.
func TestOrphanCleanup_AgeGuardHoldsRecentlyWrittenObject(t *testing.T) {
	store := newCleanupStore(t)
	seedIndex(t, store, map[string][]string{"plugin-x": {"1.0.0"}})
	// Simulates a publish of 2.0.0 that has written its artifact but has not
	// yet committed plugin.json/index.json -- unreferenced, and brand new.
	inFlight := seedVersionObject(t, store, "plugin-x", "2.0.0")

	fixedNow := time.Now().UTC()
	job := &OrphanCleanup{
		Store:  store,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return fixedNow },
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Aborted {
		t.Fatalf("report.Aborted = true, want false: %+v", report)
	}
	if report.Deleted != 0 {
		t.Fatalf("report.Deleted = %d, want 0 (age guard must hold the in-flight publish's object)", report.Deleted)
	}
	if report.HeldByAge == 0 {
		t.Fatalf("report.HeldByAge = 0, want >0: %+v", report)
	}
	mustExist(t, store, inFlight, true)
}

// TestOrphanCleanup_DeletesUnreferencedObjectPastAgeGuard is the positive
// case: once MinAge has genuinely elapsed (by the injected clock) for an
// object unreferenced by index.json, and Enabled+non-dry-run, it is deleted.
func TestOrphanCleanup_DeletesUnreferencedObjectPastAgeGuard(t *testing.T) {
	store := newCleanupStore(t)
	seedIndex(t, store, map[string][]string{"plugin-x": {"1.0.0"}})
	orphan := seedVersionObject(t, store, "plugin-x", "2.0.0") // never referenced

	job := &OrphanCleanup{
		Store:  store,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().Add(25 * time.Hour) },
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Aborted {
		t.Fatalf("report.Aborted = true, want false: %+v", report)
	}
	if report.Deleted != 1 {
		t.Fatalf("report.Deleted = %d, want 1: %+v", report.Deleted, report)
	}
	mustExist(t, store, orphan, false)
}

// TestOrphanCleanup_ReferencedVersionNeverDeletedRegardlessOfAge proves the
// other half of AMD-27's operative criteria: "a directory referenced by
// index.json is never deleted, regardless of age."
func TestOrphanCleanup_ReferencedVersionNeverDeletedRegardlessOfAge(t *testing.T) {
	store := newCleanupStore(t)
	seedIndex(t, store, map[string][]string{"plugin-x": {"1.0.0"}})
	live := seedVersionObject(t, store, "plugin-x", "1.0.0")

	job := &OrphanCleanup{
		Store:  store,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().AddDate(1, 0, 0) }, // a year old
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Deleted != 0 {
		t.Fatalf("report.Deleted = %d, want 0", report.Deleted)
	}
	mustExist(t, store, live, true)
}

// TestOrphanCleanup_DryRunReportsWithoutDeleting proves DryRun computes the
// same candidate set and reports it as Deleted (what-would-happen), but
// issues no Delete calls -- proven both by the object surviving and by
// arming a Delete fault that would fail the test if hit.
func TestOrphanCleanup_DryRunReportsWithoutDeleting(t *testing.T) {
	backing := newCleanupStore(t)
	seedIndex(t, backing, map[string][]string{"plugin-x": {"1.0.0"}})
	orphan := seedVersionObject(t, backing, "plugin-x", "2.0.0")

	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpDelete, storagetest.AnyKey, errors.New("dry-run must never call Delete"))

	cfg := baseCleanupConfig()
	cfg.DryRun = true
	job := &OrphanCleanup{
		Store:  fs,
		Config: cfg,
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().Add(25 * time.Hour) },
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.DryRun {
		t.Fatalf("report.DryRun = false, want true")
	}
	if report.Deleted != 1 {
		t.Fatalf("report.Deleted = %d, want 1 (dry-run still reports what would be deleted)", report.Deleted)
	}
	mustExist(t, backing, orphan, true)
}

// TestOrphanCleanup_UnknownAgeHeldBack covers the case RW-INDEX.stage2.md
// calls "the unknown-createdAt case": if Stat cannot determine an object's
// age, the candidate is held back rather than either deleted (unsafe) or
// used to abort the entire run (too broad -- one bad object should not stop
// every other legitimate deletion in the same sweep).
func TestOrphanCleanup_UnknownAgeHeldBack(t *testing.T) {
	backing := newCleanupStore(t)
	seedIndex(t, backing, map[string][]string{})
	orphan := seedVersionObject(t, backing, "plugin-x", "2.0.0")

	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpStat, orphan, errors.New("boom: stat unavailable"))

	job := &OrphanCleanup{
		Store:  fs,
		Config: baseCleanupConfig(),
		Owner:  "replica-1",
		Now:    func() time.Time { return time.Now().UTC().Add(48 * time.Hour) },
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Aborted {
		t.Fatalf("report.Aborted = true, want false (one candidate's stat failure must not abort the whole run): %+v", report)
	}
	if report.Deleted != 0 {
		t.Fatalf("report.Deleted = %d, want 0", report.Deleted)
	}
	if report.HeldUnknownAge == 0 {
		t.Fatalf("report.HeldUnknownAge = 0, want >0: %+v", report)
	}
	mustExist(t, backing, orphan, true)
}

// TestOrphanCleanup_NotDueYetSkipsSecondRun proves AMD-27's "runs once per
// 24h" schedule: calling Run twice back-to-back (same clock) only sweeps
// once: the second call finds LastRunAt inside RunInterval and skips.
func TestOrphanCleanup_NotDueYetSkipsSecondRun(t *testing.T) {
	store := newCleanupStore(t)
	seedIndex(t, store, map[string][]string{})
	fixedNow := time.Now().UTC()

	job := &OrphanCleanup{Store: store, Config: baseCleanupConfig(), Owner: "replica-1", Now: func() time.Time { return fixedNow }}
	first, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !first.Ran {
		t.Fatalf("first run: Ran = false, want true: %+v", first)
	}

	second, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Ran {
		t.Fatalf("second run: Ran = true, want false (not due for another 24h): %+v", second)
	}
	if second.SkipReason != "not due yet" {
		t.Fatalf("second run SkipReason = %q, want %q", second.SkipReason, "not due yet")
	}
}

// TestOrphanCleanup_SingleRunnerAcrossReplicas is this package's version of
// the "two independently-constructed instances share one store" pattern
// already used by internal/auth/shared_state_test.go and
// internal/httpapi/multi_replica_test.go: two OrphanCleanup instances with
// different Owner values, sharing one backing store, calling Run
// concurrently. The storage layer's CAS primitive (Write with an expected
// generation) is the coordination mechanism -- exactly what the workstream
// brief says to use instead of inventing new coordination -- so exactly one
// of the two must actually run the sweep.
func TestOrphanCleanupSingleRunnerAcrossReplicas(t *testing.T) {
	store := newCleanupStore(t)
	seedIndex(t, store, map[string][]string{"plugin-x": {"1.0.0"}})
	orphan := seedVersionObject(t, store, "plugin-x", "2.0.0")
	fixedNow := time.Now().UTC().Add(25 * time.Hour)

	cfg := baseCleanupConfig()
	replicaA := &OrphanCleanup{Store: store, Config: cfg, Owner: "replica-a", Now: func() time.Time { return fixedNow }}
	replicaB := &OrphanCleanup{Store: store, Config: cfg, Owner: "replica-b", Now: func() time.Time { return fixedNow }}

	var wg sync.WaitGroup
	reports := make([]*CleanupReport, 2)
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); reports[0], errs[0] = replicaA.Run(context.Background()) }()
	go func() { defer wg.Done(); reports[1], errs[1] = replicaB.Run(context.Background()) }()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d Run: %v", i, err)
		}
	}

	ranCount := 0
	for _, r := range reports {
		if r.Ran {
			ranCount++
		}
	}
	if ranCount != 1 {
		t.Fatalf("expected exactly 1 of 2 concurrent replicas to actually run the sweep, got %d: %+v / %+v", ranCount, reports[0], reports[1])
	}
	// The orphan must have been deleted exactly once (not raced/double-run),
	// and by whichever single replica won the lease.
	mustExist(t, store, orphan, false)
}
