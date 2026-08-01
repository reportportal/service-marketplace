package publish

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
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
}
