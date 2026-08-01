package lifecycle

// TestBlockVersion_RecomputesLatestVersion and
// TestBlockVersion_AllBlockedKeepsSemverMax pin down AMD-07's third
// recompute trigger: BlockVersion must recompute latestVersion, not leave
// whatever the pointer already was untouched. Before this fix,
// Service.BlockVersion never wrote to st.LatestVersion at all, so blocking
// the version currently advertised as latest left it advertised forever
// even though nobody can install it (F1, RECURS).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
)

func seedPluginJSON(t *testing.T, svc *Service, pluginID string, st domain.PluginState) {
	t.Helper()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed state: %v", err)
	}
	if _, err := svc.Store.Write(context.Background(), "plugins/"+pluginID+"/plugin.json", data, 0); err != nil {
		t.Fatalf("seed plugin.json: %v", err)
	}
}

// Mutation this kills: deleting the `st.LatestVersion = domain.LatestVersion(...)`
// line added to BlockVersion (reverting to the pre-fix behaviour that never
// touches the pointer) -- latestVersion would stay "2.0.0" instead of
// promoting to "1.0.0", exactly the bug this test exists to catch.
func TestBlockVersion_RecomputesLatestVersion(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	const pluginID = "plugin-demo"

	seedPluginJSON(t, svc, pluginID, domain.PluginState{
		ID: pluginID, Tier: domain.TierOfficial, LatestVersion: "2.0.0",
		Versions: []domain.VersionMeta{{Version: "1.0.0"}, {Version: "2.0.0"}},
	})

	if _, err := svc.BlockVersion(ctx, pluginID, "2.0.0", "CVE-2026-0001"); err != nil {
		t.Fatalf("BlockVersion: %v", err)
	}

	st, err := svc.loadPlugin(ctx, pluginID)
	if err != nil {
		t.Fatalf("loadPlugin: %v", err)
	}
	if st.LatestVersion != "1.0.0" {
		t.Fatalf("latestVersion = %q, want %q (blocking the current latest must promote the next-highest non-blocked version)", st.LatestVersion, "1.0.0")
	}
}

// Mutation this kills: dropping the "all blocked -> semver-max fallback"
// branch inside domain.LatestVersion (e.g. returning "" when the filtered
// set is empty) -- latestVersion would end up "" instead of staying at the
// semver-max "2.0.0" per AMD-07's explicit all-blocked fallback.
func TestBlockVersion_AllBlockedKeepsSemverMax(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	const pluginID = "plugin-demo"

	// Seed latestVersion deliberately wrong (stale at "1.0.0") so the
	// assertion below can only pass if BlockVersion actually recomputes the
	// pointer via the fallback, not by coincidentally leaving a
	// already-correct seeded value untouched.
	seedPluginJSON(t, svc, pluginID, domain.PluginState{
		ID: pluginID, Tier: domain.TierOfficial, LatestVersion: "1.0.0",
		Versions:        []domain.VersionMeta{{Version: "1.0.0"}, {Version: "2.0.0"}},
		BlockedVersions: []domain.BlockedVersion{{Version: "1.0.0", Reason: "superseded"}},
	})

	if _, err := svc.BlockVersion(ctx, pluginID, "2.0.0", "CVE-2026-0002"); err != nil {
		t.Fatalf("BlockVersion: %v", err)
	}

	st, err := svc.loadPlugin(ctx, pluginID)
	if err != nil {
		t.Fatalf("loadPlugin: %v", err)
	}
	if st.LatestVersion != "2.0.0" {
		t.Fatalf("latestVersion = %q, want %q (all versions blocked: pointer must keep the semver-max, not go empty)", st.LatestVersion, "2.0.0")
	}
	if len(st.BlockedVersions) != 2 {
		t.Fatalf("want both versions blocked, got %+v", st.BlockedVersions)
	}
}
