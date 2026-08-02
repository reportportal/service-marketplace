package publish_test

// This file is the family regression suite for the collision described in
// the branch report as the MAJOR finding: two stage-3 fixes that are each
// individually correct combine into a rebuild that fails outright the first
// time any plugin's first publish is interrupted before its completion
// marker lands.
//
//   - rebuildIndex was hardened to fail rather than silently drop an
//     unresolvable plugin (internal/publish/service.go's buildIndexData).
//   - LatestVersion is advanced only inside markVersionComplete's CAS, never
//     publish()'s first CAS (MAJOR 1 in an earlier round).
//
// Together: a brand-new plugin's very first publish, interrupted before the
// completion marker, leaves plugin.json with exactly one incomplete version
// and LatestVersion == "". buildIndexData used to read that as "versions
// exist but nothing was ever marked latest -- unresolvable", and refused to
// write ANY index -- for every plugin, not just the interrupted one.
//
// The fix teaches buildIndexData the difference between "corruption" (a
// document it cannot make sense of) and "a plugin with nothing to list yet"
// (every version incomplete -- no different, in what it owes the catalogue,
// from a plugin nobody has published a single byte for). Every test below
// asserts against internal/catalogue.Service -- the actual read path a
// caller of GET /api/v1/plugins observes -- never against plugin.json
// directly, per the earlier finding in this track that survived because its
// tests inspected the document instead of what the user sees.
//
// Every interrupted-publish state here is reached through the real
// publish.Service using storagetest.FaultStore fault injection, not a
// hand-seeded plugin.json -- matching
// TestPublishCrashBeforeArtifactsDoesNotAdvanceLatestVersionOrBreakUnrelatedRebuild's
// precedent in latest_version_completeness_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/lifecycle"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

func newCatalogueTestStore(t *testing.T) *storage.LocalStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func catalogueManifest(pluginID, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: pluginID, Name: pluginID, Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

// listCatalogue calls the same read path GET /api/v1/plugins uses.
func listCatalogue(t *testing.T, ctx context.Context, store storage.ObjectStore) []domain.IndexPlugin {
	t.Helper()
	cat := &catalogue.Service{Store: store}
	plugins, err := cat.ListPlugins(ctx, "", "")
	if err != nil {
		t.Fatalf("catalogue.ListPlugins: %v", err)
	}
	return plugins
}

func findInCatalogue(plugins []domain.IndexPlugin, pluginID string) *domain.IndexPlugin {
	for i := range plugins {
		if plugins[i].ID == pluginID {
			return &plugins[i]
		}
	}
	return nil
}

// TestRebuildIndex_FirstPublishInterruptedBeforeCompletion_PluginAbsentNotFailedRebuild
// is the direct repro of the MAJOR finding: a brand-new plugin's very first
// publish is interrupted (its jar write fails) before markVersionComplete
// ever runs, leaving exactly one incomplete version and LatestVersion == "".
//
// Mutation this kills: reverting buildIndexData's "no complete version ->
// legitimate exclusion" branch back to unconditionally erroring whenever
// Versions is non-empty and LatestVersion == "" makes the final RebuildIndex
// call below fail with "has 1 version(s) but no latestVersion" instead of
// succeeding with plugin-fresh simply absent.
func TestRebuildIndex_FirstPublishInterruptedBeforeCompletion_PluginAbsentNotFailedRebuild(t *testing.T) {
	ctx := context.Background()
	backing := newCatalogueTestStore(t)
	fs := storagetest.Wrap(backing)
	svc := &publish.Service{Store: fs, Invalidator: cdn.NoopInvalidator{}}

	m := catalogueManifest("plugin-fresh", "1.0.0")
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}

	boom := errors.New("simulated crash before completion marker")
	artPath := storage.VersionArtifactPath("plugin-fresh", "1.0.0", string(domain.AccessPublic))
	fs.FailN(storagetest.OpWrite, artPath, boom, 1)

	if _, err := svc.PublishFirst(ctx, &publish.Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); !errors.Is(err, boom) {
		t.Fatalf("expected the injected jar write failure to surface, got %v", err)
	}

	// A later, wholly clean rebuild (no faults armed) must succeed -- this
	// is what a server restart, or an unrelated plugin's publish, triggers.
	clean := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
	if err := clean.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex after an interrupted first publish must succeed (the plugin has nothing to list yet, it is not corrupt): %v", err)
	}

	plugins := listCatalogue(t, ctx, backing)
	if p := findInCatalogue(plugins, "plugin-fresh"); p != nil {
		t.Fatalf("plugin-fresh appeared in the catalogue with an incomplete-only version set: %+v", *p)
	}
}

// TestRebuildIndex_EveryVersionIncomplete_PluginAbsentNotFailedRebuild extends
// the MAJOR finding beyond "first publish": a plugin with TWO versions, both
// interrupted, must be excluded the same way -- this is not special-cased to
// len(Versions)==1.
func TestRebuildIndex_EveryVersionIncomplete_PluginAbsentNotFailedRebuild(t *testing.T) {
	ctx := context.Background()
	backing := newCatalogueTestStore(t)

	// v1.0.0: interrupted first publish.
	fs1 := storagetest.Wrap(backing)
	svc1 := &publish.Service{Store: fs1, Invalidator: cdn.NoopInvalidator{}}
	m1 := catalogueManifest("plugin-allincomplete", "1.0.0")
	jar1, err := publish.BuildTestJAR(m1)
	if err != nil {
		t.Fatal(err)
	}
	boom1 := errors.New("simulated crash writing 1.0.0's jar")
	art1 := storage.VersionArtifactPath("plugin-allincomplete", "1.0.0", string(domain.AccessPublic))
	fs1.FailN(storagetest.OpWrite, art1, boom1, 1)
	if _, err := svc1.PublishFirst(ctx, &publish.Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); !errors.Is(err, boom1) {
		t.Fatalf("expected the injected 1.0.0 jar write failure to surface, got %v", err)
	}

	// v2.0.0: a second, independently interrupted publish against the same
	// (already-existing, still incomplete) plugin.
	fs2 := storagetest.Wrap(backing)
	svc2 := &publish.Service{Store: fs2, Invalidator: cdn.NoopInvalidator{}}
	m2 := catalogueManifest("plugin-allincomplete", "2.0.0")
	jar2, err := publish.BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}
	boom2 := errors.New("simulated crash writing 2.0.0's jar")
	art2 := storage.VersionArtifactPath("plugin-allincomplete", "2.0.0", string(domain.AccessPublic))
	fs2.FailN(storagetest.OpWrite, art2, boom2, 1)
	if _, _, err := svc2.PublishVersion(ctx, "plugin-allincomplete", &publish.Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator", false); !errors.Is(err, boom2) {
		t.Fatalf("expected the injected 2.0.0 jar write failure to surface, got %v", err)
	}

	clean := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
	if err := clean.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex over a plugin whose every version is incomplete must succeed: %v", err)
	}

	plugins := listCatalogue(t, ctx, backing)
	if p := findInCatalogue(plugins, "plugin-allincomplete"); p != nil {
		t.Fatalf("plugin-allincomplete appeared in the catalogue with zero complete versions: %+v", *p)
	}
}

// TestRebuildIndex_InterruptedFirstPublishLaterRetried_PluginAppears is the
// other half of the MAJOR finding: once ANY version of a previously
// nothing-to-list plugin actually completes, the plugin must appear in the
// catalogue at that version -- markVersionComplete's atomic Complete-flip +
// LatestVersion recompute (see its doc comment) is what makes that true.
//
// Mutation this kills: any change that makes buildIndexData's "no complete
// version" exclusion sticky (e.g. caching the decision, or keying off
// something other than each version's live Complete flag) would leave
// plugin-retry permanently absent even after its retry genuinely finishes.
func TestRebuildIndex_InterruptedFirstPublishLaterRetried_PluginAppears(t *testing.T) {
	ctx := context.Background()
	backing := newCatalogueTestStore(t)

	fs := storagetest.Wrap(backing)
	svc := &publish.Service{Store: fs, Invalidator: cdn.NoopInvalidator{}}
	m := catalogueManifest("plugin-retry", "1.0.0")
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("simulated crash before completion marker")
	artPath := storage.VersionArtifactPath("plugin-retry", "1.0.0", string(domain.AccessPublic))
	fs.FailN(storagetest.OpWrite, artPath, boom, 1)
	if _, err := svc.PublishFirst(ctx, &publish.Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); !errors.Is(err, boom) {
		t.Fatalf("expected the injected jar write failure to surface, got %v", err)
	}

	clean := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
	if err := clean.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex before the retry must succeed: %v", err)
	}
	if p := findInCatalogue(listCatalogue(t, ctx, backing), "plugin-retry"); p != nil {
		t.Fatalf("plugin-retry appeared before its retry even ran: %+v", *p)
	}

	// The retry: same plugin, same version, same content, unfaulted store --
	// PublishVersion's AMD-04 healing branch (writeArtifacts stays true
	// because the committed entry isn't Complete yet) finishes the artifacts
	// and calls markVersionComplete.
	if _, _, err := clean.PublishVersion(ctx, "plugin-retry", &publish.Bundle{JAR: jar, JARFilename: "p.jar"}, "operator", false); err != nil {
		t.Fatalf("retry publish of plugin-retry 1.0.0: %v", err)
	}

	plugins := listCatalogue(t, ctx, backing)
	p := findInCatalogue(plugins, "plugin-retry")
	if p == nil {
		t.Fatalf("plugin-retry still absent from the catalogue after its interrupted first publish was successfully retried")
	}
	if p.LatestVersion != "1.0.0" {
		t.Fatalf("plugin-retry latestVersion = %q, want %q", p.LatestVersion, "1.0.0")
	}
}

// TestRebuildIndex_AllVersionsBlocked_PluginStillListedAtBlockedLatest is a
// neighbouring-state check: a plugin whose only (complete) version is
// blocked is a DIFFERENT state from "no complete version" -- domain.
// LatestVersion's fallback names the SemVer-max complete version even when
// every complete version is blocked (see semver.go), so LatestVersion is
// never "" here and buildIndexData's new "no complete version" branch must
// never fire for it. This guards against a future edit collapsing "blocked"
// into "absent" the way the MAJOR finding collapsed "not yet published" into
// "corrupt".
func TestRebuildIndex_AllVersionsBlocked_PluginStillListedAtBlockedLatest(t *testing.T) {
	ctx := context.Background()
	backing := newCatalogueTestStore(t)
	pubSvc := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}

	m := catalogueManifest("plugin-blocked", "1.0.0")
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pubSvc.PublishFirst(ctx, &publish.Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed publish: %v", err)
	}

	lc := &lifecycle.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}, Publisher: pubSvc}
	if _, err := lc.BlockVersion(ctx, "plugin-blocked", "1.0.0", "cve-test"); err != nil {
		t.Fatalf("BlockVersion: %v", err)
	}

	plugins := listCatalogue(t, ctx, backing)
	p := findInCatalogue(plugins, "plugin-blocked")
	if p == nil {
		t.Fatalf("plugin-blocked missing from the catalogue: blocking its only version must not make it disappear, only un-installable")
	}
	if p.LatestVersion != "1.0.0" {
		t.Fatalf("plugin-blocked latestVersion = %q, want %q", p.LatestVersion, "1.0.0")
	}
}

// TestRebuildIndex_OnlyVersionRemoved_PluginAbsentFromCatalogue is a
// neighbouring-state check: a plugin whose only version completed and was
// then tombstoned via RemovePlugin must stay absent from the catalogue --
// this is the pre-existing st.Removed != nil branch in buildIndexData,
// checked BEFORE the "no complete version" branch this fix adds, and must
// keep taking priority over it.
func TestRebuildIndex_OnlyVersionRemoved_PluginAbsentFromCatalogue(t *testing.T) {
	ctx := context.Background()
	backing := newCatalogueTestStore(t)
	pubSvc := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}

	m := catalogueManifest("plugin-removed", "1.0.0")
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pubSvc.PublishFirst(ctx, &publish.Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed publish: %v", err)
	}

	lc := &lifecycle.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}, Publisher: pubSvc}
	if _, hk, err := lc.RemovePlugin(ctx, "plugin-removed", "test removal", "operator"); err != nil {
		t.Fatalf("RemovePlugin: %v", err)
	} else if hk.Degraded() {
		t.Fatalf("RemovePlugin housekeeping degraded: %v", hk.Warnings)
	}

	plugins := listCatalogue(t, ctx, backing)
	if p := findInCatalogue(plugins, "plugin-removed"); p != nil {
		t.Fatalf("plugin-removed still in the catalogue after removal: %+v", *p)
	}
}

// TestRebuildIndex_MixedCompleteAndIncompleteVersions_OnlyCompleteVersionAdvertised
// is the direct repro of this round's MAJOR finding: buildIndexData decides
// whether to LIST a plugin from whether it has any complete version, but
// still built that plugin's advertised Versions list from every entry in
// plugin.json, unfiltered by completeness. A plugin with one clean publish
// and one interrupted one therefore advertised a version whose jar and
// manifest were never written -- a client that picked it got a 404/500
// instead of an installable plugin.
//
// Reproduces exactly the reviewer's scenario: publish 1.0.0 cleanly, then
// publish 2.0.0 with a fault on the jar write (so neither its jar nor its
// manifest ever lands). The catalogue must list the plugin (it has a
// complete version) at latestVersion 1.0.0, advertising ONLY 1.0.0 -- never
// the incomplete 2.0.0.
//
// Mutation this kills: reverting buildIndexData's versions-collection loop
// to append every st.Versions entry unconditionally (dropping the
// domain.IsVersionComplete filter) makes the assertion on Versions below
// fail, seeing ["1.0.0", "2.0.0"] instead of ["1.0.0"].
func TestRebuildIndex_MixedCompleteAndIncompleteVersions_OnlyCompleteVersionAdvertised(t *testing.T) {
	ctx := context.Background()
	backing := newCatalogueTestStore(t)

	// v1.0.0: a clean, complete publish.
	pubSvc := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
	m1 := catalogueManifest("plugin-mixed", "1.0.0")
	jar1, err := publish.BuildTestJAR(m1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pubSvc.PublishFirst(ctx, &publish.Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed publish of 1.0.0: %v", err)
	}

	// v2.0.0: interrupted before its jar (and therefore its manifest) ever
	// lands.
	fs := storagetest.Wrap(backing)
	svc := &publish.Service{Store: fs, Invalidator: cdn.NoopInvalidator{}}
	m2 := catalogueManifest("plugin-mixed", "2.0.0")
	jar2, err := publish.BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("simulated crash writing 2.0.0's jar")
	art2 := storage.VersionArtifactPath("plugin-mixed", "2.0.0", string(domain.AccessPublic))
	fs.FailN(storagetest.OpWrite, art2, boom, 1)
	if _, _, err := svc.PublishVersion(ctx, "plugin-mixed", &publish.Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator", false); !errors.Is(err, boom) {
		t.Fatalf("expected the injected 2.0.0 jar write failure to surface, got %v", err)
	}

	// The interrupted publish call above returns before ever reaching
	// rebuildIndex (publish() bails out on the artifact-write error), so
	// index.json is still the one written by the clean 1.0.0 publish above --
	// it would trivially satisfy this test's assertions without exercising
	// buildIndexData's filtering at all. Force a fresh rebuild, matching this
	// package's precedent for observing an interrupted-publish state through
	// the actual read path (see TestRebuildIndex_FirstPublishInterruptedBeforeCompletion_PluginAbsentNotFailedRebuild).
	clean := &publish.Service{Store: backing, Invalidator: cdn.NoopInvalidator{}}
	if err := clean.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex over a plugin with one complete and one incomplete version must succeed: %v", err)
	}

	plugins := listCatalogue(t, ctx, backing)
	p := findInCatalogue(plugins, "plugin-mixed")
	if p == nil {
		t.Fatalf("plugin-mixed missing from the catalogue: it has a complete version (1.0.0) and must be listed")
	}
	if p.LatestVersion != "1.0.0" {
		t.Fatalf("plugin-mixed latestVersion = %q, want %q", p.LatestVersion, "1.0.0")
	}
	if len(p.Versions) != 1 || p.Versions[0] != "1.0.0" {
		t.Fatalf("plugin-mixed advertised versions = %v, want [\"1.0.0\"] -- 2.0.0 was never fully written and must not be advertised", p.Versions)
	}
}
