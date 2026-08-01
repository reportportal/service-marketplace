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
