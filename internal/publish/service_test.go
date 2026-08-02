package publish

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

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

// TestRebuildIndex_PopulatesCommittedVersionSet proves index.json's
// per-plugin Versions field carries the plugin's full committed version set,
// not just latestVersion. AMD-27 names index.json (not plugin.json) as the
// orphan-cleanup reference set specifically so a non-latest-but-still-live
// version directory is never misread as unreferenced garbage; that is only
// possible if index.json actually records every committed version, not just
// the newest one. If rebuildIndex reverted to recording only latestVersion
// (or the field were left empty), this test fails because "1.0.0" would be
// missing from the set even though it was never blocked or removed.
func TestRebuildIndex_PopulatesCommittedVersionSet(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	svc := &Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
	ctx := context.Background()

	base := &domain.Manifest{
		ID: "plugin-multi", Name: "Multi", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}

	m1 := *base
	m1.Version = "1.0.0"
	jar1, err := BuildTestJAR(&m1)
	if err != nil {
		t.Fatalf("BuildTestJAR v1: %v", err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar1, JARFilename: "p.jar", Screenshots: map[string][]byte{}}, "op"); err != nil {
		t.Fatalf("PublishFirst: %v", err)
	}

	m2 := *base
	m2.Version = "2.0.0"
	jar2, err := BuildTestJAR(&m2)
	if err != nil {
		t.Fatalf("BuildTestJAR v2: %v", err)
	}
	if _, err := svc.PublishVersion(ctx, "plugin-multi", &Bundle{JAR: jar2, JARFilename: "p.jar", Screenshots: map[string][]byte{}}, "op", false); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	obj, err := store.Read(ctx, storage.PathIndex)
	if err != nil {
		t.Fatalf("reading index.json: %v", err)
	}
	var idx domain.Index
	if err := json.Unmarshal(obj.Data, &idx); err != nil {
		t.Fatalf("unmarshal index.json: %v", err)
	}
	if len(idx.Plugins) != 1 {
		t.Fatalf("want 1 plugin in index, got %d: %s", len(idx.Plugins), obj.Data)
	}
	got := append([]string(nil), idx.Plugins[0].Versions...)
	sort.Strings(got)
	want := []string{"1.0.0", "2.0.0"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("index.json plugins[0].versions = %v, want %v (raw: %s)", got, want, obj.Data)
	}
	if !idx.Complete {
		t.Fatalf("index.json complete = false, want true after a fully successful rebuild")
	}
}

// testManifest builds a minimal valid manifest for pluginID/version, for
// tests that only care that publish succeeds, not about manifest content.
func testManifest(pluginID, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: pluginID, Name: pluginID, Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

func publishOne(t *testing.T, svc *Service, pluginID, version string) {
	t.Helper()
	m := testManifest(pluginID, version)
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR(%s): %v", pluginID, err)
	}
	if _, err := svc.PublishFirst(context.Background(), &Bundle{JAR: jar, JARFilename: "p.jar", Screenshots: map[string][]byte{}}, "op"); err != nil {
		t.Fatalf("PublishFirst(%s): %v", pluginID, err)
	}
}

// TestRebuildIndex_FailsRatherThanSilentlyDroppingUnreadablePlugin is the
// BLOCKING fix this file exists for. Before this change, rebuildIndex
// silently `continue`d past any plugin whose plugin.json failed to read --
// producing a brand new index.json that looks perfectly healthy (fewer
// plugins, each with a real Versions list) but that has quietly stopped
// referencing plugin-bad's already-committed version. Every version
// directory belonging to that dropped plugin then reads as unreferenced
// garbage to internal/lifecycle.OrphanCleanup and gets swept.
//
// This test publishes plugin-good and plugin-bad for real (so index.json
// legitimately references both), then makes plugin-bad's plugin.json
// unreadable (simulating bit rot / corruption discovered only when the next
// rebuild -- triggered by some unrelated operator action -- reads it back)
// and asserts two things: RebuildIndex must return an error instead of
// silently succeeding, and index.json on disk must be byte-for-byte
// unchanged -- the last known-good document, which still protects
// plugin-bad's version, is left in place rather than overwritten with an
// incomplete one.
func TestRebuildIndex_FailsRatherThanSilentlyDroppingUnreadablePlugin(t *testing.T) {
	root := t.TempDir()
	backing, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	svc := &Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}

	publishOne(t, svc, "plugin-good", "1.0.0")
	publishOne(t, svc, "plugin-bad", "1.0.0")

	before, err := backing.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("reading index.json before corruption: %v", err)
	}

	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpRead, storage.PluginStatePath("plugin-bad"), errors.New("boom: plugin-bad/plugin.json unreadable"))
	faultySvc := &Service{Store: fs, Invalidator: cdn.NoopInvalidator{}}

	if err := faultySvc.RebuildIndex(context.Background()); err == nil {
		t.Fatalf("RebuildIndex succeeded despite plugin-bad's plugin.json being unreadable -- it must fail rather than silently write an index that drops plugin-bad")
	}

	after, err := backing.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("reading index.json after failed rebuild: %v", err)
	}
	if string(after.Data) != string(before.Data) {
		t.Fatalf("index.json was overwritten by a failed rebuild\nbefore: %s\nafter:  %s", before.Data, after.Data)
	}

	var idx domain.Index
	if err := json.Unmarshal(after.Data, &idx); err != nil {
		t.Fatalf("unmarshal index.json: %v", err)
	}
	found := false
	for _, p := range idx.Plugins {
		if p.ID == "plugin-bad" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plugin-bad is missing from index.json after a failed rebuild -- it must still be referenced by the last known-good document: %s", after.Data)
	}
}

// racingWriteStore wraps a storage.ObjectStore and, on the first Write call
// matching path, runs onFirst *before* delegating to the real write. It
// exists to make a concurrent-writer race deterministic in a single-threaded
// test: onFirst lands a second, independent commit into the backing store in
// the exact gap between "the caller already decided what bytes to write" and
// "the caller's CAS write actually lands" -- the same gap two real replicas'
// concurrent rebuildIndex calls race across, without needing goroutines or
// sleeps to reproduce.
type racingWriteStore struct {
	storage.ObjectStore
	path    string
	onFirst func()
	fired   bool
}

func (r *racingWriteStore) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	if objectPath == r.path && !r.fired {
		r.fired = true
		r.onFirst()
	}
	return r.ObjectStore.Write(ctx, objectPath, data, expectedGen)
}

// TestRebuildIndex_RecomputesAfterConcurrentWriteInsteadOfClobberingIt is the
// BLOCKING fix: "completeness is not currency". domain.Index.Complete
// attests only that the rebuild which PRODUCED a document resolved every
// plugin *it saw* -- it says nothing about whether that document is still
// current by the time it lands. Two replicas can legitimately race two
// different publishes' rebuildIndex calls against the same index.json; the
// loser's CAS write fails and storage.WriteWithRetry retries -- but if that
// retry blindly resubmits the SAME already-computed bytes instead of
// re-deriving them from storage, the loser can still win the *second* race
// and silently overwrite the winner's newer, equally-Complete index.json
// with its own older snapshot, permanently dropping the winner's plugin from
// the reference set. internal/lifecycle.OrphanCleanup cannot tell that
// regressed document apart from a genuinely complete one -- it still decodes
// Complete: true -- so the winner's version directory reads as unreferenced
// and, once MinAge elapses, gets swept: the exact in-flight-publish data
// loss AMD-27 exists to prevent, just entered through index.json's own write
// path instead of past the sweep's age guard.
//
// This test reproduces that race deterministically: the "loser" computes its
// index snapshot from storage (seeing only plugin-good), then -- exactly at
// the moment it attempts its CAS write -- a "winner" publish of a second,
// unrelated plugin commits for real (including its own successful
// rebuildIndex), bumping index.json's generation out from under the loser.
// The loser's write then naturally fails with a generation conflict and
// retries. The fix under test is what that retry does next.
func TestRebuildIndex_RecomputesAfterConcurrentWriteInsteadOfClobberingIt(t *testing.T) {
	root := t.TempDir()
	backing, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	winner := &Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
	publishOne(t, winner, "plugin-good", "1.0.0")

	race := &racingWriteStore{ObjectStore: backing, path: storage.PathIndex}
	race.onFirst = func() {
		// Lands *during* the loser's CAS write attempt, after the loser
		// already listed storage and built its snapshot from a state that
		// did not include plugin-race.
		publishOne(t, winner, "plugin-race", "1.0.0")
	}
	loser := &Service{Store: race, Invalidator: cdn.NoopInvalidator{}}

	if err := loser.RebuildIndex(context.Background()); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}

	obj, err := backing.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("reading index.json: %v", err)
	}
	var idx domain.Index
	if err := json.Unmarshal(obj.Data, &idx); err != nil {
		t.Fatalf("unmarshal index.json: %v", err)
	}
	if !idx.Complete {
		t.Fatalf("index.json complete = false, want true: %s", obj.Data)
	}
	ids := map[string]bool{}
	for _, p := range idx.Plugins {
		ids[p.ID] = true
	}
	if !ids["plugin-good"] || !ids["plugin-race"] {
		t.Fatalf("index.json plugins = %v, want both plugin-good and plugin-race present -- the loser's retry must be re-derived from current storage, not re-submit its stale snapshot over the winner's newer commit: %s", idx.Plugins, obj.Data)
	}
}

// rebuildIndexFailureCase captures the common shape of the MAJOR fix's four
// remaining fail-branch tests: publish plugin-good for real (so index.json
// legitimately protects a real version), run an optional setup step (for
// cases that need plugin-bad legitimately published before it is corrupted,
// e.g. so its manifest exists to later be deleted), snapshot index.json,
// break plugin-bad in exactly one way via corrupt, then assert RebuildIndex
// fails and index.json is left byte-for-byte as it was right before the
// failed attempt -- never overwritten with a document that silently drops
// plugin-bad. Each test below arms a different single fail-branch in
// buildIndexData; reverting any ONE branch back to `continue` (the pre-fix
// bug) must fail exactly the corresponding test and no other, which is what
// "each covered independently" means for this file's mutation-testing
// requirement.
func rebuildIndexFailureCase(t *testing.T, setup, corrupt func(t *testing.T, backing *storage.LocalStore)) {
	t.Helper()
	root := t.TempDir()
	backing, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	svc := &Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
	publishOne(t, svc, "plugin-good", "1.0.0")
	if setup != nil {
		setup(t, backing)
	}

	before, err := backing.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("reading index.json before corruption: %v", err)
	}

	corrupt(t, backing)

	if err := svc.RebuildIndex(context.Background()); err == nil {
		t.Fatalf("RebuildIndex succeeded despite plugin-bad's corrupted state -- it must fail rather than silently write an index that drops plugin-bad")
	}

	after, err := backing.Read(context.Background(), storage.PathIndex)
	if err != nil {
		t.Fatalf("reading index.json after failed rebuild: %v", err)
	}
	if string(after.Data) != string(before.Data) {
		t.Fatalf("index.json was overwritten by a failed rebuild\nbefore: %s\nafter:  %s", before.Data, after.Data)
	}
}

// TestRebuildIndex_FailsOnUnparseablePluginJSON covers buildIndexData's
// "plugin.json unparseable" branch independently: reverting only that branch
// to `continue` would leave this test as the sole one to fail, proving the
// branch is load-bearing rather than decorative.
func TestRebuildIndex_FailsOnUnparseablePluginJSON(t *testing.T) {
	rebuildIndexFailureCase(t, nil, func(t *testing.T, backing *storage.LocalStore) {
		if _, err := backing.Write(context.Background(), storage.PluginStatePath("plugin-bad"), []byte("{not valid json"), 0); err != nil {
			t.Fatalf("seed unparseable plugin.json: %v", err)
		}
	})
}

// TestRebuildIndex_FailsOnInconsistentLatestVersion covers buildIndexData's
// "LatestVersion empty but Versions non-empty" branch independently: a
// plugin.json that parses fine but is logically inconsistent (some version
// was recorded as committed but nothing was ever marked latest) must also
// refuse to write a partial index, not silently drop the plugin.
func TestRebuildIndex_FailsOnInconsistentLatestVersion(t *testing.T) {
	rebuildIndexFailureCase(t, nil, func(t *testing.T, backing *storage.LocalStore) {
		st := domain.PluginState{
			ID:       "plugin-bad",
			Tier:     domain.TierPartner,
			Versions: []domain.VersionMeta{{Version: "1.0.0"}},
			// LatestVersion deliberately left empty.
		}
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatalf("marshal inconsistent plugin state: %v", err)
		}
		if _, err := backing.Write(context.Background(), storage.PluginStatePath("plugin-bad"), data, 0); err != nil {
			t.Fatalf("seed inconsistent plugin.json: %v", err)
		}
	})
}

// TestRebuildIndex_FailsOnUnreadableManifest covers buildIndexData's "latest
// version manifest unreadable" branch independently: plugin-bad is published
// for real (so its plugin.json legitimately points at a real latestVersion),
// then its manifest.json is deleted out from under it -- simulating bit rot
// discovered only when a later rebuild reads it back.
func TestRebuildIndex_FailsOnUnreadableManifest(t *testing.T) {
	rebuildIndexFailureCase(t,
		func(t *testing.T, backing *storage.LocalStore) {
			badSvc := &Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
			publishOne(t, badSvc, "plugin-bad", "1.0.0")
		},
		func(t *testing.T, backing *storage.LocalStore) {
			if err := backing.Delete(context.Background(), storage.VersionManifestPath("plugin-bad", "1.0.0")); err != nil {
				t.Fatalf("delete plugin-bad manifest.json: %v", err)
			}
		})
}

// TestRebuildIndex_FailsOnUnparseableManifest covers buildIndexData's "latest
// version manifest unparseable" branch independently: the manifest object
// exists (so the read succeeds) but its content is corrupt JSON.
func TestRebuildIndex_FailsOnUnparseableManifest(t *testing.T) {
	rebuildIndexFailureCase(t,
		func(t *testing.T, backing *storage.LocalStore) {
			badSvc := &Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
			publishOne(t, badSvc, "plugin-bad", "1.0.0")
		},
		func(t *testing.T, backing *storage.LocalStore) {
			existing, err := backing.Read(context.Background(), storage.VersionManifestPath("plugin-bad", "1.0.0"))
			if err != nil {
				t.Fatalf("reading plugin-bad manifest.json before corruption: %v", err)
			}
			if _, err := backing.Write(context.Background(), storage.VersionManifestPath("plugin-bad", "1.0.0"), []byte("{not valid json"), existing.Generation); err != nil {
				t.Fatalf("corrupt plugin-bad manifest.json: %v", err)
			}
		})
}
