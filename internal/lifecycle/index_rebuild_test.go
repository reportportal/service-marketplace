package lifecycle

// TestBlockVersion_RegeneratesCatalogueIndex is the other half of AMD-07's
// block trigger. §6.4's Cloud CDN invalidation matrix names index.json among
// the GCS paths written -- and invalidated -- by a block, alongside the
// version's own plugin.json. SetTier and RemovePlugin in this file already
// call s.Publisher.RebuildIndex; before this fix BlockVersion did not,
// so it only ever recomputed latestVersion inside plugin.json.
//
// Every other BlockVersion test in this package (TestBlockVersion_
// RecomputesLatestVersion, TestBlockVersion_AllBlockedKeepsSemverMax) reads
// the fix back through loadPlugin -- i.e. through plugin.json, the exact
// path BlockVersion always wrote correctly. That is precisely why the
// missing index.json regeneration survived: nothing in the suite ever
// looked at the composed catalogue. This test reads index.json through
// internal/catalogue.Service.ListPlugins instead, the same object the
// public listing endpoint composes its response from.
//
// Mutation this kills: deleting the `_ = s.Publisher.RebuildIndex(ctx)` line
// added to BlockVersion -- index.json would keep advertising "2.0.0" as
// latestVersion forever after the block, even though plugin.json correctly
// shows "1.0.0".
import (
	"context"
	"testing"

	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
)

func manifestForBlockTest(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

func publishForBlockTest(t *testing.T, svc *Service, pluginID, version string, first bool) {
	t.Helper()
	jar, err := publish.BuildTestJAR(manifestForBlockTest(pluginID, version))
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	bundle := &publish.Bundle{JAR: jar}
	ctx := context.Background()
	if first {
		if _, err := svc.Publisher.PublishFirst(ctx, bundle, "operator"); err != nil {
			t.Fatalf("PublishFirst(%s): %v", version, err)
		}
		return
	}
	if _, err := svc.Publisher.PublishVersion(ctx, pluginID, bundle, "operator", false); err != nil {
		t.Fatalf("PublishVersion(%s): %v", version, err)
	}
}

func TestBlockVersion_RegeneratesCatalogueIndex(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	const pluginID = "plugin-demo"

	// Real publishes (not seeded JSON) so index.json is populated the same
	// way production traffic populates it.
	publishForBlockTest(t, svc, pluginID, "1.0.0", true)
	publishForBlockTest(t, svc, pluginID, "2.0.0", false)

	cat := &catalogue.Service{Store: svc.Store}

	before, err := cat.ListPlugins(ctx, "", "")
	if err != nil {
		t.Fatalf("ListPlugins (before block): %v", err)
	}
	if len(before) != 1 || before[0].LatestVersion != "2.0.0" {
		t.Fatalf("sanity check failed: catalogue before block = %+v, want single entry with latestVersion 2.0.0", before)
	}

	if _, err := svc.BlockVersion(ctx, pluginID, "2.0.0", "CVE-2026-0003"); err != nil {
		t.Fatalf("BlockVersion: %v", err)
	}

	after, err := cat.ListPlugins(ctx, "", "")
	if err != nil {
		t.Fatalf("ListPlugins (after block): %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("catalogue after block = %+v, want exactly one entry", after)
	}
	if after[0].LatestVersion != "1.0.0" {
		t.Fatalf("catalogue latestVersion = %q, want %q -- the composed catalogue must promote the same way plugin.json does after a block, not keep advertising the blocked version", after[0].LatestVersion, "1.0.0")
	}
}
