package lifecycle

// This file covers the second half of the workstream brief: "lifecycle
// mutations discard the errors from their own housekeeping." SetTier and
// RemovePlugin each commit a primary write (the plugin.json CAS) and then
// run downstream housekeeping (index rebuild, CDN invalidation) whose errors
// were previously thrown away with `_ = ...`. Every test here proves two
// things at once, because getting either one wrong is its own defect:
//   - the primary write is NOT rolled back and the mutation does NOT return
//     an error just because housekeeping failed (the data IS written --
//     answering as though it were not is its own defect), and
//   - the caller CAN tell the mutation was degraded, via the returned
//     HousekeepingOutcome, rather than being told plain "success".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

// erroringInvalidator always fails Invalidate with err, to exercise the CDN
// invalidation half of the housekeeping contract independently of the index
// rebuild half.
type erroringInvalidator struct{ err error }

func (e erroringInvalidator) Invalidate(ctx context.Context, paths []string) error { return e.err }

// newTestPublisher builds a publish.Service over store with a no-op
// invalidator, purely to drive Service.RebuildIndex from these tests --
// SetTier/RemovePlugin's own CDN invalidation is exercised separately via
// Service.Invalidator, not through this publisher.
func newTestPublisher(store storage.ObjectStore) *publish.Service {
	return &publish.Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
}

// seedLifecyclePlugin seeds a plugin.json claiming version 1.0.0, AND the
// version manifest object that backs it -- rebuildIndex resolves a plugin's
// display fields (name/description/category) from its latest version's
// manifest, and (since the BLOCKING fix making rebuildIndex refuse to write
// a partial index) now hard-fails the whole rebuild if that manifest is
// missing. A plugin.json with no corresponding manifest is not "a plugin
// that successfully published a version" -- it is exactly the unresolvable
// state rebuildIndex must refuse to paper over, so tests that want a
// genuinely healthy plugin must seed both.
func seedLifecyclePlugin(t *testing.T, store storage.ObjectStore, pluginID string) {
	t.Helper()
	seed := fmt.Sprintf(`{"id": %q, "tier": "partner", "latestVersion": "1.0.0", "versions": [{"version": "1.0.0", "sha256": "aaa"}]}`, pluginID)
	if _, err := store.Write(context.Background(), storage.PluginStatePath(pluginID), []byte(seed), 0); err != nil {
		t.Fatalf("seed plugin.json: %v", err)
	}
	manifest := fmt.Sprintf(`{"id": %q, "name": %q, "version": "1.0.0", "description": "d", "author": {"name": "A"}, "license": "Apache-2.0", "category": "import", "compatibility": {"reportPortal": ">=25.1"}, "access": "public"}`, pluginID, pluginID)
	if _, err := store.Write(context.Background(), storage.VersionManifestPath(pluginID, "1.0.0"), []byte(manifest), 0); err != nil {
		t.Fatalf("seed version manifest: %v", err)
	}
}

func readPluginState(t *testing.T, store storage.ObjectStore, pluginID string) domain.PluginState {
	t.Helper()
	obj, err := store.Read(context.Background(), storage.PluginStatePath(pluginID))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatalf("unmarshal plugin.json: %v", err)
	}
	return st
}

// TestSetTier_CommitsPrimaryWriteEvenWhenIndexRebuildFails is the "do not
// let the invalidation failure be fatal to an already-committed mutation"
// assertion: plugin.json's tier really changes on disk, and SetTier still
// returns success (err == nil), even though its index-rebuild housekeeping
// failed outright.
func TestSetTier_CommitsPrimaryWriteEvenWhenIndexRebuildFails(t *testing.T) {
	backing := newCleanupStore(t)
	seedLifecyclePlugin(t, backing, "plugin-x")

	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpWrite, storage.PathIndex, errors.New("boom: index write unavailable"))

	pub := newTestPublisher(fs)
	svc := &Service{Store: fs, Invalidator: cdn.NoopInvalidator{}, Publisher: pub}

	st, hk, err := svc.SetTier(context.Background(), "plugin-x", domain.TierOfficial)
	if err != nil {
		t.Fatalf("SetTier returned an error for an already-committed mutation: %v", err)
	}
	if st.Tier != domain.TierOfficial {
		t.Fatalf("returned state has stale tier: %+v", st)
	}
	if !hk.Degraded() {
		t.Fatalf("HousekeepingOutcome.Degraded() = false, want true (index rebuild failed)")
	}
	if len(hk.Warnings) != 1 || !strings.Contains(hk.Warnings[0], "index rebuild") {
		t.Fatalf("Warnings = %v, want exactly one mentioning \"index rebuild\"", hk.Warnings)
	}

	onDisk := readPluginState(t, backing, "plugin-x")
	if onDisk.Tier != domain.TierOfficial {
		t.Fatalf("plugin.json on disk still has old tier %q -- primary write was not actually committed", onDisk.Tier)
	}
}

// TestSetTier_CommitsPrimaryWriteEvenWhenInvalidateFails is the CDN half of
// the same contract.
func TestSetTier_CommitsPrimaryWriteEvenWhenInvalidateFails(t *testing.T) {
	backing := newCleanupStore(t)
	seedLifecyclePlugin(t, backing, "plugin-x")

	pub := newTestPublisher(backing)
	svc := &Service{Store: backing, Invalidator: erroringInvalidator{err: errors.New("boom: cdn down")}, Publisher: pub}

	st, hk, err := svc.SetTier(context.Background(), "plugin-x", domain.TierOfficial)
	if err != nil {
		t.Fatalf("SetTier returned an error for an already-committed mutation: %v", err)
	}
	if st.Tier != domain.TierOfficial {
		t.Fatalf("returned state has stale tier: %+v", st)
	}
	if !hk.Degraded() {
		t.Fatalf("HousekeepingOutcome.Degraded() = false, want true (CDN invalidation failed)")
	}
	if len(hk.Warnings) != 1 || !strings.Contains(strings.ToLower(hk.Warnings[0]), "cdn") {
		t.Fatalf("Warnings = %v, want exactly one mentioning CDN invalidation", hk.Warnings)
	}

	onDisk := readPluginState(t, backing, "plugin-x")
	if onDisk.Tier != domain.TierOfficial {
		t.Fatalf("plugin.json on disk still has old tier %q -- primary write was not actually committed", onDisk.Tier)
	}
}

// TestSetTier_NoWarningsOnFullSuccess is the control: with working
// housekeeping, Degraded() is false and Warnings is empty.
func TestSetTier_NoWarningsOnFullSuccess(t *testing.T) {
	backing := newCleanupStore(t)
	seedLifecyclePlugin(t, backing, "plugin-x")
	pub := newTestPublisher(backing)
	svc := &Service{Store: backing, Invalidator: cdn.NoopInvalidator{}, Publisher: pub}

	_, hk, err := svc.SetTier(context.Background(), "plugin-x", domain.TierOfficial)
	if err != nil {
		t.Fatalf("SetTier: %v", err)
	}
	if hk.Degraded() {
		t.Fatalf("Degraded() = true, want false: %+v", hk)
	}
	if len(hk.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want empty", hk.Warnings)
	}
}

// TestRemovePlugin_CommitsTombstoneEvenWhenIndexRebuildFails mirrors the
// SetTier case for RemovePlugin: the tombstone is durably committed to
// plugin.json and returned to the caller even though the index rebuild that
// should have dropped this plugin from index.json failed.
func TestRemovePlugin_CommitsTombstoneEvenWhenIndexRebuildFails(t *testing.T) {
	backing := newCleanupStore(t)
	seedLifecyclePlugin(t, backing, "plugin-x")

	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpWrite, storage.PathIndex, errors.New("boom: index write unavailable"))

	pub := newTestPublisher(fs)
	svc := &Service{Store: fs, Invalidator: cdn.NoopInvalidator{}, Publisher: pub}

	tomb, hk, err := svc.RemovePlugin(context.Background(), "plugin-x", "policy violation", "operator@example.com")
	if err != nil {
		t.Fatalf("RemovePlugin returned an error for an already-committed mutation: %v", err)
	}
	if tomb.RemovalReason != "policy violation" {
		t.Fatalf("unexpected tombstone: %+v", tomb)
	}
	if !hk.Degraded() {
		t.Fatalf("Degraded() = false, want true (index rebuild failed)")
	}

	onDisk := readPluginState(t, backing, "plugin-x")
	if onDisk.Removed == nil {
		t.Fatalf("plugin.json on disk was never tombstoned -- primary write was not actually committed")
	}
}

// TestSetTier_RecordsHousekeepingFailureDurably proves the "must be retried
// or recorded" half of the amendment this package implements: a durable
// record is written under storage.PathHousekeepingFailures that a
// reconciliation process could later find and retry, rather than the
// failure existing only as an in-memory warning the caller may or may not
// act on.
func TestSetTier_RecordsHousekeepingFailureDurably(t *testing.T) {
	backing := newCleanupStore(t)
	seedLifecyclePlugin(t, backing, "plugin-x")

	fs := storagetest.Wrap(backing)
	fs.Fail(storagetest.OpWrite, storage.PathIndex, errors.New("boom: index write unavailable"))

	pub := newTestPublisher(fs)
	svc := &Service{Store: fs, Invalidator: cdn.NoopInvalidator{}, Publisher: pub}

	if _, _, err := svc.SetTier(context.Background(), "plugin-x", domain.TierOfficial); err != nil {
		t.Fatalf("SetTier: %v", err)
	}

	files, err := backing.ListPrefix(context.Background(), storage.PathHousekeepingFailures)
	if err != nil {
		t.Fatalf("ListPrefix: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want exactly 1 durable housekeeping-failure record, got %d: %v", len(files), files)
	}
	obj, err := backing.Read(context.Background(), files[0])
	if err != nil {
		t.Fatalf("reading recorded failure: %v", err)
	}
	var rec struct {
		PluginID string `json:"pluginId"`
		Action   string `json:"action"`
		Step     string `json:"step"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(obj.Data, &rec); err != nil {
		t.Fatalf("unmarshal recorded failure: %v", err)
	}
	if rec.PluginID != "plugin-x" || rec.Step != "index_rebuild" || rec.Error == "" {
		t.Fatalf("recorded failure has unexpected shape: %+v", rec)
	}
}
