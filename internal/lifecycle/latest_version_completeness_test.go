package lifecycle

// TestBlockVersion_DoesNotPromoteIncompleteVersionToLatest is MAJOR 1's
// lifecycle-level regression test (branch report). After the merge that
// brought in AMD-07's recompute, internal/lifecycle.Service.BlockVersion
// became a SECOND writer of st.LatestVersion (alongside
// internal/publish.Service.publish/markVersionComplete), via the exact same
// domain.LatestVersion(st.Versions, st.BlockedVersions) call. That recompute
// must never select a version whose artifacts are missing any more than
// publish()'s own recompute may -- a plugin.json can legitimately hold a
// committed-but-incomplete entry (see domain.VersionMeta.Complete's doc
// comment: publish()'s CAS commits a version before its artifacts exist),
// and blocking some unrelated, older version must not accidentally promote
// that incomplete entry into latestVersion just because it happens to be the
// SemVer-maximum.
//
// This test seeds a plugin with two versions: 1.0.0 (a legacy/complete
// record, Complete == nil) as the current latest, and 1.1.0 committed but
// explicitly incomplete -- exactly what MAJOR 1's fix to publish() leaves
// behind after a crash between its plugin.json CAS and its artifact writes.
// Blocking 1.0.0 (the only complete version) must fall back to keeping IT as
// latestVersion (AMD-07's "all [eligible] blocked -> keep the semver-max"
// rule, scoped to complete versions), never promote 1.1.0.
//
// Mutation this kills: removing the completeness filter from
// domain.LatestVersion (or scoping it to only the primary unblocked+released
// tier and not the blocked-fallback tier) -- blocking 1.0.0 would then
// select 1.1.0 in the primary tier (unblocked, non-pre-release, and the
// SemVer-max) despite it being incomplete, and this test's assertion fails.
import (
	"context"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
)

func TestBlockVersion_DoesNotPromoteIncompleteVersionToLatest(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	const pluginID = "plugin-demo"

	incomplete := false
	seedPluginJSON(t, svc, pluginID, domain.PluginState{
		ID: pluginID, Tier: domain.TierOfficial, LatestVersion: "1.0.0",
		Versions: []domain.VersionMeta{
			{Version: "1.0.0"}, // Complete == nil: legacy/complete
			{Version: "1.1.0", Complete: &incomplete}, // committed, artifacts missing
		},
	})

	if _, err := svc.BlockVersion(ctx, pluginID, "1.0.0", "test block"); err != nil {
		t.Fatalf("BlockVersion: %v", err)
	}

	st, err := svc.loadPlugin(ctx, pluginID)
	if err != nil {
		t.Fatalf("loadPlugin: %v", err)
	}
	if st.LatestVersion != "1.0.0" {
		t.Fatalf("latestVersion = %q, want %q: blocking the only complete version must fall back to keeping it, never promote the incomplete 1.1.0", st.LatestVersion, "1.0.0")
	}
}
