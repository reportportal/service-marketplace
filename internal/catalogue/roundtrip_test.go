package catalogue

// domain.IndexPlugin is dual-purpose the same way domain.LicenseEntitlement/
// LicensePublicKey were (see internal/httpapi/wire_storage_separation_test.go and
// e8501ac): it is the literal shape json.Marshal/json.Unmarshal uses for index.json
// (via domain.Index.Plugins, written by internal/publish.Service.rebuildIndex and read
// by this package's Service.loadIndex), and until this change it was also marshalled
// straight onto the GET /api/v1/plugins response. That coupling is now closed on the
// wire side (see internal/httpapi.PluginListItemResponse) but the storage side needs
// its own guarantee: these tests pin index.json's actual on-disk shape against a
// hand-maintained, independent mirror of it, the same way
// internal/license/roundtrip_test.go pins auth/authorized_keys.json. If a future change
// to satisfy the wire contract is made on domain.Index/IndexPlugin directly instead of
// on the httpapi response type, it will move index.json's shape and fail here.
//
//   - an index.json written by the release before this change must still load
//     correctly (upgrade safety)
//   - an index.json written by this code must still parse under that same shape
//     (rollback safety / no silent format drift)
//
// Bytes are seeded/verified as literal JSON on the read side, and by parsing the
// literal bytes a real publish.Service write produced on the write side -- not by
// round-tripping today's domain.IndexPlugin through itself, which would prove nothing
// about compatibility with what is actually on disk.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// previousReleaseIndexPlugin and previousReleaseIndex mirror domain.Index/IndexPlugin's
// json shape independently of the production type. Category/Access/Tier are plain
// strings here on purpose: an independent reader (an older binary, another service)
// only ever sees the JSON string value, never the Go alias type.
type previousReleaseIndexPlugin struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	LatestVersion string `json:"latestVersion"`
	Description   string `json:"description,omitempty"`
	Category      string `json:"category"`
	Access        string `json:"access"`
	Tier          string `json:"tier"`
}

type previousReleaseIndex struct {
	Plugins []previousReleaseIndexPlugin `json:"plugins"`
}

func indexPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(storage.PathIndex))
}

// TestListPlugins_ReadsDocumentWrittenByPreviousRelease seeds the exact bytes the
// previous release would have written to index.json and asserts today's
// Service.ListPlugins still loads every field correctly.
func TestListPlugins_ReadsDocumentWrittenByPreviousRelease(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	seed := `{
  "plugins": [
    {"id": "plugin-a", "name": "Alpha", "latestVersion": "1.0.0", "description": "first", "category": "bug-tracking", "access": "public", "tier": "official"},
    {"id": "plugin-b", "name": "Beta Notify", "latestVersion": "2.0.0", "description": "alerts", "category": "notifications", "access": "premium", "tier": "official"}
  ]
}`
	path := indexPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed index.json: %v", err)
	}

	svc := &Service{Store: store}
	plugins, err := svc.ListPlugins(context.Background(), "", "")
	if err != nil {
		t.Fatalf("ListPlugins (reading a document written by the previous release): %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("want 2 plugins, got %d: %+v", len(plugins), plugins)
	}
	byID := map[string]domain.IndexPlugin{}
	for _, p := range plugins {
		byID[p.ID] = p
	}
	a, ok := byID["plugin-a"]
	if !ok || a.Name != "Alpha" || a.LatestVersion != "1.0.0" || a.Description != "first" ||
		a.Category != domain.CategoryBugTracking || a.Access != domain.AccessPublic || a.Tier != domain.TierOfficial {
		t.Fatalf("plugin-a lost fields reading previous-release document: %+v", a)
	}
	b, ok := byID["plugin-b"]
	if !ok || b.Name != "Beta Notify" || b.Access != domain.AccessPremium || b.Category != domain.CategoryNotifications {
		t.Fatalf("plugin-b lost fields reading previous-release document: %+v", b)
	}
}

// TestRebuildIndex_WritesDocumentThePreviousReleaseCanStillRead exercises the real
// publish path (Service.PublishFirst -> rebuildIndex) against a real LocalStore, then
// parses the index.json bytes it actually wrote using the previous release's struct
// shape -- the rollback direction.
func TestRebuildIndex_WritesDocumentThePreviousReleaseCanStillRead(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	pub := &publish.Service{Store: store, Invalidator: cdn.NoopInvalidator{}}

	m := &domain.Manifest{
		ID: "plugin-jira-cloud", Name: "Jira Cloud", Version: "1.4.2", Description: "Sync issues with Jira",
		Author: domain.Author{Name: "ReportPortal Team"}, License: "Apache-2.0",
		Category: domain.CategoryBugTracking, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	bundle := &publish.Bundle{JAR: jar, JARFilename: "plugin.jar", Screenshots: map[string][]byte{}}
	if _, err := pub.PublishFirst(context.Background(), bundle, "test-operator"); err != nil {
		t.Fatalf("PublishFirst: %v", err)
	}

	raw, err := os.ReadFile(indexPath(root))
	if err != nil {
		t.Fatalf("reading written index.json: %v", err)
	}
	var old previousReleaseIndex
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("an index.json written by this code must still parse under the previous release's struct shape (rollback safety): %v\nraw: %s", err, raw)
	}
	if len(old.Plugins) != 1 {
		t.Fatalf("want 1 plugin, got %d: %s", len(old.Plugins), raw)
	}
	p := old.Plugins[0]
	if p.ID != "plugin-jira-cloud" || p.Name != "Jira Cloud" || p.LatestVersion != "1.4.2" ||
		p.Category != "bug-tracking" || p.Access != "public" || p.Tier != "official" {
		t.Fatalf("plugin fields lost or renamed on write: %+v (raw: %s)", p, raw)
	}
}
