package publish

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// TestPublishVersionLegacyCompleteVersionTakesNoWriteFastPathEvenWithDifferentChangelog
// is the regression test for MAJOR 2 (branch report): a plugin.json version
// entry written before VersionMeta.Complete existed has no "complete" key in
// its JSON at all -- it decodes to whatever Complete's zero value is. Under a
// plain `Complete bool` that zero value (false) is indistinguishable from "an
// interrupted new-format publish left this incomplete", so the very first
// same-content republish of an already-whole legacy version took the healing
// branch instead of AMD-04 branch 2's no-write fast path: it silently
// overwrote the changelog (and, via the same writeArtifacts flag, the
// screenshot set) with whatever the new request's bundle happened to carry,
// while still reporting 200 idempotent.
//
// This test seeds a plugin.json with a version entry that has no Complete
// value set at all, plus a real jar/manifest/changelog already on disk
// (exactly what the pre-Complete-field write protocol -- write every object,
// then commit -- would have left behind), then republishes the identical jar
// with a DIFFERENT changelog and asserts: idempotent=true, plugin.json's
// generation is unchanged (no write at all, not even a PublishedAt refresh),
// and the changelog object on disk is byte-for-byte untouched.
//
// Mutation this kills: reverting VersionMeta.Complete from *bool back to a
// plain bool (or dropping IsVersionComplete's "absent means legacy, provably
// complete" branch) -- the zero value then reads as "incomplete", the
// changelog gets overwritten, plugin.json's generation changes, and this
// test's byte-comparison assertions fail.
func TestPublishVersionLegacyCompleteVersionTakesNoWriteFastPathEvenWithDifferentChangelog(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m := testManifest("plugin-legacy", "1.0.0")
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	sha := storage.HashSHA256(jar)
	originalChangelog := []byte("original notes")

	// Seed storage exactly as the pre-Complete-field write protocol would
	// have left it: plugin.json's version entry has no "complete" key at
	// all, and every object it claims (jar, manifest, changelog) genuinely
	// exists already.
	st := domain.PluginState{
		ID: m.ID, Tier: domain.TierOfficial, LatestVersion: m.Version,
		Versions: []domain.VersionMeta{{Version: m.Version, PublishedAt: time.Now().UTC(), SHA256: sha}},
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	beforeGen, err := store.Write(ctx, storage.PluginStatePath(m.ID), body, 0)
	if err != nil {
		t.Fatalf("seed legacy plugin.json: %v", err)
	}
	if _, err := store.Write(ctx, storage.VersionArtifactPath(m.ID, m.Version, string(m.Access)), jar, 0); err != nil {
		t.Fatalf("seed jar: %v", err)
	}
	manifestBytes, _ := json.MarshalIndent(m, "", "  ")
	if _, err := store.Write(ctx, storage.VersionManifestPath(m.ID, m.Version), manifestBytes, 0); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	if _, err := store.Write(ctx, storage.VersionChangelogPath(m.ID, m.Version), originalChangelog, 0); err != nil {
		t.Fatalf("seed changelog: %v", err)
	}

	res, idempotent, err := svc.PublishVersion(ctx, m.ID, &Bundle{
		JAR: jar, JARFilename: "p.jar", Changelog: []byte("a completely different changelog"),
	}, "operator", false)
	if err != nil {
		t.Fatalf("republish of a legacy complete version: %v", err)
	}
	if !idempotent {
		t.Fatalf("idempotent = false, want true: a legacy version with no Complete key must be treated as already whole")
	}
	if res.SHA256 != sha {
		t.Fatalf("SHA256 = %q, want %q", res.SHA256, sha)
	}

	after, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != beforeGen {
		t.Fatalf("plugin.json generation changed (%d -> %d): a legacy complete version must take the no-write fast path", beforeGen, after.Generation)
	}

	cl, err := store.Read(ctx, storage.VersionChangelogPath(m.ID, m.Version))
	if err != nil {
		t.Fatal(err)
	}
	if string(cl.Data) != string(originalChangelog) {
		t.Fatalf("changelog overwritten: got %q, want unchanged %q -- AMD-04 branch 2 promises no objects are written on an idempotent republish", cl.Data, originalChangelog)
	}
}
