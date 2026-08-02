package publish

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

// --- AMD-04-duplicate-publish-contract -------------------------------------

func newTestService(t *testing.T, store storage.ObjectStore) *Service {
	t.Helper()
	return &Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
}

func newLocalStore(t *testing.T) *storage.LocalStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func testManifest(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

// TestPublishVersionIdempotentRetryReturns200NoObjectsWritten is the
// regression test for AMD-04-duplicate-publish-contract branch 2: "Version
// committed AND the uploaded jar's SHA-256 equals the stored checksum -> 200
// with the existing PublishResponse; no objects are written." This is what
// makes a CI retry after a lost 201 response safe.
//
// Mutation that makes this fail: replace the SHA-256 comparison in
// PublishVersion with the old "artifact file exists" check (no idempotent
// branch at all) — see git history at internal/publish/service.go before
// this change; the retry then returns ErrConflict-shaped 409 instead of a
// no-op 200, and idempotent is never true.
func TestPublishVersionIdempotentRetryReturns200NoObjectsWritten(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m := testManifest("plugin-demo", "1.0.0")
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	before, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatalf("read plugin.json before retry: %v", err)
	}

	res, idempotent, err := svc.PublishVersion(ctx, m.ID, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator", false)
	if err != nil {
		t.Fatalf("PublishVersion (byte-identical retry) = %v, want nil error", err)
	}
	if !idempotent {
		t.Fatalf("idempotent = false, want true for a byte-identical republish of a committed version")
	}
	wantSHA := storage.HashSHA256(jar)
	if res.SHA256 != wantSHA {
		t.Fatalf("SHA256 = %q, want %q", res.SHA256, wantSHA)
	}

	after, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatalf("read plugin.json after retry: %v", err)
	}
	if after.Generation != before.Generation {
		t.Fatalf("plugin.json generation changed (%d -> %d): AMD-04 branch 2 requires no objects be written on an idempotent retry", before.Generation, after.Generation)
	}
}

// TestPublishVersionDifferentContentReturns409VersionAlreadyPublished is the
// regression test for AMD-04 branch 3: "Version committed AND the SHA-256
// differs -> 409 Conflict, ErrorResponse.code = VERSION_ALREADY_PUBLISHED".
//
// Mutation that makes this fail: dropping the SHA-256 comparison collapses
// branches 2 and 3 into "always ErrConflict", which happens to still return
// an error here — so the mutation that actually defeats this test is
// removing the version-conflict check entirely (falling through to
// s.publish, silently overwriting a committed version's checksum), which
// violates FR-R-05 immutability and makes this test's error assertion fail
// with err == nil.
func TestPublishVersionDifferentContentReturns409VersionAlreadyPublished(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m1 := testManifest("plugin-demo", "1.0.0")
	m1.Description = "first upload"
	jar1, err := BuildTestJAR(m1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	m2 := testManifest("plugin-demo", "1.0.0")
	m2.Description = "different bytes, same id/version"
	jar2, err := BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}
	if storage.HashSHA256(jar1) == storage.HashSHA256(jar2) {
		t.Fatalf("test fixture bug: jar1 and jar2 hash the same")
	}

	_, idempotent, err := svc.PublishVersion(ctx, "plugin-demo", &Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator", false)
	if idempotent {
		t.Fatalf("idempotent = true, want false for a conflicting republish")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("PublishVersion (different content, same version) = %v, want ErrVersionConflict", err)
	}
}

// TestPublishVersionNotCommittedRetryAfterFailedCASLeavesNothingBehind is the
// regression test for AMD-04 branch 1 under the CAS-before-artifacts order
// (see publish()'s doc comment): a publish attempt whose plugin.json CAS
// itself fails must not have written any artifact bytes either — the CAS is
// now the first thing publish() does, precisely so a losing/interrupted
// attempt can never leave bytes behind for a later, unrelated retry to
// either trust or need to overwrite. A subsequent retry with completely
// different content then succeeds cleanly against untouched state.
//
// This supersedes the pre-fix "retry overwrites an orphaned artifact" test:
// under the old artifacts-before-plugin.json order, an interrupted attempt
// legitimately could leave an orphaned artifact for a retry to blindly
// overwrite (upsertObject still does that — see
// TestPublishVersionHealsMissingArtifactAfterInterruptedArtifactWrite for
// the case that still exercises it), but a plugin.json-CAS failure
// specifically can no longer produce that state, because nothing is written
// before that CAS succeeds.
//
// Mutation that makes this fail: writing the artifact before attempting the
// plugin.json CAS (i.e. reverting the BLOCKING fix) makes the "nothing was
// written" precondition assertion below fail — the orphaned 2.0.0 artifact
// would exist after the injected plugin.json failure.
func TestPublishVersionNotCommittedRetryAfterFailedCASLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)
	fs := storagetest.Wrap(backing)
	svc := newTestService(t, fs)

	base := testManifest("plugin-demo", "1.0.0")
	jar0, err := BuildTestJAR(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar0, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	mA := testManifest("plugin-demo", "2.0.0")
	mA.Description = "attempt A"
	jarA, err := BuildTestJAR(mA)
	if err != nil {
		t.Fatal(err)
	}

	boom := errors.New("simulated network blip")
	fs.FailN(storagetest.OpWrite, storage.PluginStatePath(base.ID), boom, 1)

	if _, _, err := svc.PublishVersion(ctx, base.ID, &Bundle{JAR: jarA, JARFilename: "p.jar"}, "operator", false); !errors.Is(err, boom) {
		t.Fatalf("expected the injected plugin.json write failure to surface, got %v", err)
	}

	artPath := storage.VersionArtifactPath(base.ID, "2.0.0", string(domain.AccessPublic))
	if ok, _ := backing.Exists(ctx, artPath); ok {
		t.Fatalf("the 2.0.0 artifact must not exist: publish() failed at the plugin.json CAS, before ever writing artifact bytes")
	}
	stObj, err := backing.Read(ctx, storage.PluginStatePath(base.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		t.Fatal(err)
	}
	for _, v := range st.Versions {
		if v.Version == "2.0.0" {
			t.Fatalf("test precondition: plugin.json must not reference 2.0.0 after the interrupted attempt")
		}
	}

	mB := testManifest("plugin-demo", "2.0.0")
	mB.Description = "attempt B (retry, different bytes)"
	jarB, err := BuildTestJAR(mB)
	if err != nil {
		t.Fatal(err)
	}
	if storage.HashSHA256(jarA) == storage.HashSHA256(jarB) {
		t.Fatalf("test fixture bug: jarA and jarB hash the same")
	}

	res, idempotent, err := svc.PublishVersion(ctx, base.ID, &Bundle{JAR: jarB, JARFilename: "p.jar"}, "operator", false)
	if err != nil {
		t.Fatalf("retry after interrupted publish: %v", err)
	}
	if idempotent {
		t.Fatalf("idempotent = true, want false: a not-committed retry is a fresh write, not AMD-04 branch 2")
	}
	wantSHA := storage.HashSHA256(jarB)
	if res.SHA256 != wantSHA {
		t.Fatalf("SHA256 = %q, want %q (retry content)", res.SHA256, wantSHA)
	}

	artObj, err := backing.Read(ctx, artPath)
	if err != nil {
		t.Fatalf("read written artifact: %v", err)
	}
	if got := storage.HashSHA256(artObj.Data); got != wantSHA {
		t.Fatalf("stored artifact bytes = %q, want %q (retry content)", got, wantSHA)
	}
}

// TestPublishVersionHealsMissingArtifactAfterInterruptedArtifactWrite is the
// regression test for the "healing" branch publish() takes when its
// plugin.json CAS lands (committing a version+SHA-256) but the artifact
// write that's supposed to follow never completes — the one legitimate way,
// under the CAS-before-artifacts order, for a committed version to
// (temporarily) have no bytes at its artifact path. A same-content republish
// must still succeed and actually write the missing bytes, exactly as
// AMD-04 branch 1's "idempotent retry of an interrupted publish... overwrite
// any orphaned per-version objects" intends — upsertObject's blind overwrite
// (see its doc comment) is still what performs the write, just reached via
// the healing path instead of the fresh-commit path.
//
// Mutation that makes this fail: making publish() trust a plain "SHA-256
// already matches" branch-2 result without checking whether the artifact
// object actually exists leaves the committed version permanently
// unreachable — no download would ever serve a jar for it again, and this
// test's final read of the artifact would return storage.ErrNotFound.
func TestPublishVersionHealsMissingArtifactAfterInterruptedArtifactWrite(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)
	svc := newTestService(t, backing)

	m := testManifest("plugin-demo", "2.0.0")
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	sha := storage.HashSHA256(jar)

	// Simulate a crash between publish()'s plugin.json CAS committing 2.0.0
	// and its own subsequent artifact writes: plugin.json records the
	// version, but no bytes exist at the artifact path yet.
	st := domain.PluginState{
		ID: m.ID, Tier: domain.TierOfficial, LatestVersion: m.Version,
		Versions: []domain.VersionMeta{{Version: m.Version, PublishedAt: time.Now().UTC(), SHA256: sha}},
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backing.Write(ctx, storage.PluginStatePath(m.ID), body, 0); err != nil {
		t.Fatalf("seed committed-but-artifactless plugin.json: %v", err)
	}
	artPath := storage.VersionArtifactPath(m.ID, m.Version, string(domain.AccessPublic))
	if ok, _ := backing.Exists(ctx, artPath); ok {
		t.Fatalf("test precondition: artifact must not exist yet")
	}

	res, idempotent, err := svc.PublishVersion(ctx, m.ID, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator", false)
	if err != nil {
		t.Fatalf("healing republish (identical content): %v", err)
	}
	if !idempotent {
		t.Fatalf("idempotent = false, want true: SHA-256 already matched the committed one")
	}
	if res.SHA256 != sha {
		t.Fatalf("SHA256 = %q, want %q", res.SHA256, sha)
	}

	artObj, err := backing.Read(ctx, artPath)
	if err != nil {
		t.Fatalf("read healed artifact: %v", err)
	}
	if got := storage.HashSHA256(artObj.Data); got != sha {
		t.Fatalf("healed artifact SHA-256 = %q, want %q", got, sha)
	}
}

// versionRaceStore wraps a storagetest.FaultStore and, the moment the
// wrapped store's Write to pluginPath returns the fault armed on it, runs
// winner() synchronously before propagating that error back to the caller.
// It exists to make deterministic what an unseeded goroutine race can only
// hope to hit: a competing publish committing the SAME target version to
// plugin.json in the exact window between this call's first failed
// compare-and-swap attempt and its retry.
type versionRaceStore struct {
	*storagetest.FaultStore
	pluginPath string
	fired      bool
	winner     func()
}

func (v *versionRaceStore) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	n, err := v.FaultStore.Write(ctx, objectPath, data, expectedGen)
	if err != nil && !v.fired && objectPath == v.pluginPath {
		v.fired = true
		v.winner()
	}
	return n, err
}

// TestPublishVersionConcurrentSameVersionCommitMidPublishReturnsConflict is
// the regression test for the second half of the go-assessment TOCTOU
// finding: PublishVersion's "is this version already committed" decision
// (AMD-04-duplicate-publish-contract branches 1/2/3) is computed once from a
// single Read before publish()'s plugin.json compare-and-swap loop even
// starts. A losing racer whose own pre-check saw "not committed" must not
// get to reuse that stale belief on every CAS retry: once a competing
// publish commits the SAME version with DIFFERENT content in between, the
// retry must observe that and fail with ErrVersionConflict (AMD-04 branch
// 3), never silently overwrite the committed checksum with its own stale
// content.
//
// This is deterministic, not a goroutine race that hopes to hit a window: a
// storagetest.FaultStore fault forces the loser's first plugin.json write to
// fail, and a wrapper hook lands the winner's complete, independent publish
// of the same version (different bytes) synchronously in that exact gap
// before the loser's WriteWithRetry re-reads and retries.
//
// Mutation that makes this fail: reverting publish()'s WriteWithRetry
// callback to the old "found -> blindly overwrite SHA256/PublishedAt on the
// matching entry" logic (no re-check against the freshly read data) makes
// the retry succeed and stamp plugin.json with the loser's SHA-256 even
// though the winner's bytes are what's actually sitting at the versioned
// artifact path — err comes back nil instead of ErrVersionConflict, and the
// asserted SHA256 mismatches.
func TestPublishVersionConcurrentSameVersionCommitMidPublishReturnsConflict(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)

	base := testManifest("plugin-demo", "1.0.0")
	jar0, err := BuildTestJAR(base)
	if err != nil {
		t.Fatal(err)
	}
	seedSvc := newTestService(t, backing)
	if _, err := seedSvc.PublishFirst(ctx, &Bundle{JAR: jar0, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	mLoser := testManifest("plugin-demo", "2.0.0")
	mLoser.Description = "loser content"
	jarLoser, err := BuildTestJAR(mLoser)
	if err != nil {
		t.Fatal(err)
	}
	mWinner := testManifest("plugin-demo", "2.0.0")
	mWinner.Description = "winner content, commits mid-publish"
	jarWinner, err := BuildTestJAR(mWinner)
	if err != nil {
		t.Fatal(err)
	}
	if storage.HashSHA256(jarLoser) == storage.HashSHA256(jarWinner) {
		t.Fatalf("test fixture bug: jarLoser and jarWinner hash the same")
	}

	fs := storagetest.Wrap(backing)
	fs.FailN(storagetest.OpWrite, storage.PluginStatePath(base.ID), storage.ErrConflict, 1)
	race := &versionRaceStore{FaultStore: fs, pluginPath: storage.PluginStatePath(base.ID)}
	race.winner = func() {
		winnerSvc := newTestService(t, backing)
		if _, _, werr := winnerSvc.PublishVersion(ctx, base.ID, &Bundle{JAR: jarWinner, JARFilename: "p.jar"}, "ci", false); werr != nil {
			t.Fatalf("winner publish (racing in the gap): %v", werr)
		}
	}
	loserSvc := newTestService(t, race)

	_, idempotent, err := loserSvc.PublishVersion(ctx, base.ID, &Bundle{JAR: jarLoser, JARFilename: "p.jar"}, "operator", false)
	if !race.fired {
		t.Fatalf("test bug: the simulated concurrent commit never fired")
	}
	if idempotent {
		t.Fatalf("idempotent = true, want false: loser and winner content differ")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("PublishVersion racing a mid-flight same-version commit = %v, want ErrVersionConflict", err)
	}

	obj, err := backing.Read(ctx, storage.PluginStatePath(base.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatal(err)
	}
	wantSHA := storage.HashSHA256(jarWinner)
	found := false
	for _, v := range st.Versions {
		if v.Version != "2.0.0" {
			continue
		}
		found = true
		if v.SHA256 != wantSHA {
			t.Fatalf("plugin.json 2.0.0 SHA256 = %q, want winner's %q — loser's retry must not have overwritten the committed checksum", v.SHA256, wantSHA)
		}
	}
	if !found {
		t.Fatalf("plugin.json must still record the winner's committed 2.0.0 version")
	}
}

// TestPublishConcurrentLoserNeverOverwritesWinnersCommittedArtifactBytes is
// the regression test for the BLOCKING finding: publish() wrote the version
// artifact (and manifest/changelog/screenshots) BEFORE the plugin.json
// compare-and-swap that decides which AMD-04 branch applies. The CAS
// correctly re-derives the decision from a fresh read (see
// TestPublishVersionConcurrentSameVersionCommitMidPublishReturnsConflict),
// but by the time it runs the bytes may already be overwritten — a losing
// publisher's blind artifact write can land AFTER a winning publisher has
// already committed a different SHA-256 for the same version, corrupting
// the supposedly-immutable bytes even though the loser's own CAS attempt
// then (correctly) discovers the conflict and returns 409/ErrVersionConflict.
// The 409 was never the problem; the bytes were.
//
// This calls the unexported publish() directly (same package) with a
// "loser" bundle for a version the "winner" has already fully, genuinely
// committed — modelling exactly what PublishVersion's outer pre-check (a
// plain Read taken once, before publish() is even invoked) cannot prevent
// by itself: publish() being entered with a "not committed" belief that is
// already stale.
//
// This is deliberately NOT built on the existing versionRaceStore hook
// (see TestPublishVersionConcurrentSameVersionCommitMidPublishReturnsConflict):
// that hook only fires the winner once the loser's own first write to
// plugin.json fails, which — under the pre-fix code, where the artifact
// write happens unconditionally before that CAS is ever attempted — is
// always chronologically AFTER the loser has already written its own
// artifact bytes. The winner's own later, equally unconditional artifact
// write then papers over the corruption by writing last, which is exactly
// how this bug survived two rounds of review that asserted only the status
// code. Constructing the sequence the other way around — winner commits
// completely first, loser's publish() call lands second — is what actually
// exercises the corrupting order.
//
// Mutation that makes this fail: reordering publish() back to writing
// artifacts before the plugin.json CAS (i.e. reverting this fix) makes the
// artifact-bytes assertion below fail — the stored jar and manifest end up
// holding the loser's bytes even though err is still (correctly)
// ErrVersionConflict.
func TestPublishConcurrentLoserNeverOverwritesWinnersCommittedArtifactBytes(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)
	svc := newTestService(t, backing)

	base := testManifest("plugin-demo", "1.0.0")
	jar0, err := BuildTestJAR(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar0, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	mWinner := testManifest("plugin-demo", "2.0.0")
	mWinner.Description = "winner content, committed first"
	jarWinner, err := BuildTestJAR(mWinner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PublishVersion(ctx, base.ID, &Bundle{JAR: jarWinner, JARFilename: "p.jar"}, "ci", false); err != nil {
		t.Fatalf("winner publish: %v", err)
	}

	mLoser := testManifest("plugin-demo", "2.0.0")
	mLoser.Description = "loser content, decided 'not committed' before the winner landed"
	jarLoser, err := BuildTestJAR(mLoser)
	if err != nil {
		t.Fatal(err)
	}
	if storage.HashSHA256(jarWinner) == storage.HashSHA256(jarLoser) {
		t.Fatalf("test fixture bug: jarWinner and jarLoser hash the same")
	}

	_, err = svc.publish(ctx, mLoser, &Bundle{JAR: jarLoser, JARFilename: "p.jar"}, "operator", false)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("losing publish() = %v, want ErrVersionConflict", err)
	}

	artPath := storage.VersionArtifactPath(base.ID, "2.0.0", string(domain.AccessPublic))
	artObj, err := backing.Read(ctx, artPath)
	if err != nil {
		t.Fatalf("read artifact after the race: %v", err)
	}
	wantSHA := storage.HashSHA256(jarWinner)
	if got := storage.HashSHA256(artObj.Data); got != wantSHA {
		t.Fatalf("stored artifact SHA-256 = %q, want winner's %q — the losing publish()'s blind overwrite corrupted the already-committed version's bytes", got, wantSHA)
	}

	manObj, err := backing.Read(ctx, storage.VersionManifestPath(base.ID, "2.0.0"))
	if err != nil {
		t.Fatalf("read manifest after the race: %v", err)
	}
	var gotManifest domain.Manifest
	if err := json.Unmarshal(manObj.Data, &gotManifest); err != nil {
		t.Fatal(err)
	}
	if gotManifest.Description != mWinner.Description {
		t.Fatalf("stored manifest description = %q, want winner's %q — the losing publish() overwrote the committed manifest too", gotManifest.Description, mWinner.Description)
	}
}

// TestPublishFirstConcurrentCreateMidPublishReturnsPluginExists is the
// regression test for a third instance of the same stale-decision pattern:
// PublishFirst decides "this id does not exist yet" from a single outer
// Read, then reuses that belief unconditionally deep inside publish()'s
// compare-and-swap retry loop instead of re-deriving it from what the loop
// actually observes. A competing PublishFirst that creates the plugin in
// the gap must make the loser 409 with ErrPluginExists (AMD-04's "id whose
// plugin.json exists with removed == null"), not silently merge its own
// version into the winner's freshly created plugin.
//
// Mutation that makes this fail: passing resurrect=false unconditionally
// from PublishFirst's ErrNotFound branch (today's code) never re-checks
// existence inside the CAS loop at all, so the retry just appends the
// loser's version onto the winner's plugin.json — err comes back nil
// instead of ErrPluginExists.
func TestPublishFirstConcurrentCreateMidPublishReturnsPluginExists(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)

	mLoser := testManifest("plugin-race", "1.0.0")
	jarLoser, err := BuildTestJAR(mLoser)
	if err != nil {
		t.Fatal(err)
	}
	mWinner := testManifest("plugin-race", "9.9.9")
	jarWinner, err := BuildTestJAR(mWinner)
	if err != nil {
		t.Fatal(err)
	}

	fs := storagetest.Wrap(backing)
	fs.FailN(storagetest.OpWrite, storage.PluginStatePath("plugin-race"), storage.ErrConflict, 1)
	race := &versionRaceStore{FaultStore: fs, pluginPath: storage.PluginStatePath("plugin-race")}
	race.winner = func() {
		winnerSvc := newTestService(t, backing)
		if _, werr := winnerSvc.PublishFirst(ctx, &Bundle{JAR: jarWinner, JARFilename: "p.jar"}, "operator-2"); werr != nil {
			t.Fatalf("winner PublishFirst (racing in the gap): %v", werr)
		}
	}
	loserSvc := newTestService(t, race)

	_, err = loserSvc.PublishFirst(ctx, &Bundle{JAR: jarLoser, JARFilename: "p.jar"}, "operator-1")
	if !race.fired {
		t.Fatalf("test bug: the simulated concurrent create never fired")
	}
	if !errors.Is(err, ErrPluginExists) {
		t.Fatalf("PublishFirst racing a mid-flight competing create = %v, want ErrPluginExists", err)
	}

	obj, err := backing.Read(ctx, storage.PluginStatePath("plugin-race"))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Versions) != 1 || st.Versions[0].Version != "9.9.9" {
		t.Fatalf("plugin.json versions = %+v, want only the winner's 9.9.9", st.Versions)
	}
}

// --- AMD-06-removal-lifecycle / D-06 ----------------------------------------

// TestPublishFirstExistingLivePluginReturns409PluginAlreadyExists guards the
// non-tombstoned half of AMD-04/AMD-06's PublishFirst rule: a live
// plugin.json (removed == nil) always 409s, directing the caller to the
// versions route — it is never treated as a resurrection.
//
// Mutation that makes this fail: dropping the `st.Removed == nil` guard
// (treating any existing plugin.json as a resurrection candidate) makes
// PublishFirst silently overwrite a live plugin's state instead of
// returning ErrPluginExists.
func TestPublishFirstExistingLivePluginReturns409PluginAlreadyExists(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m := testManifest("plugin-demo", "1.0.0")
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar, JARFilename: "p.jar"}, "operator"); !errors.Is(err, ErrPluginExists) {
		t.Fatalf("PublishFirst on an existing live plugin = %v, want ErrPluginExists", err)
	}
}

// TestPublishFirstResurrectsTombstonedPlugin is the regression test for
// AMD-06-removal-lifecycle / D-06 (adopted): "POST /api/v1/plugins ... is
// the explicit resurrection path: it resets removed/removalReason/removedBy
// to null, publishes the uploaded version ..., and regenerates index.json."
// It also proves the other half of D-06 — "explicitly NOT through the CI
// auto-create path" — by asserting PublishVersion(autoCreate=true) still
// 410s against the same tombstone.
//
// Mutation that makes this fail: PublishFirst's old unconditional
// `if _, err := s.Store.Read(...); err == nil { return nil, ErrConflict }`
// (no Removed inspection at all) makes the resurrection call return
// ErrPluginExists-shaped 409 instead of succeeding, and the tombstone is
// never cleared.
func TestPublishFirstResurrectsTombstonedPlugin(t *testing.T) {
	ctx := context.Background()
	store := newLocalStore(t)
	svc := newTestService(t, store)

	m := testManifest("plugin-demo", "1.0.0")
	jar1, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	// Simulate lifecycle.Service.RemovePlugin's tombstone write directly
	// (importing internal/lifecycle here would be an import cycle: it
	// already imports internal/publish).
	tombstoneAt := time.Now().UTC()
	obj, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatal(err)
	}
	st.Removed = &tombstoneAt
	st.RemovalReason = "DMCA claim"
	st.RemovedBy = "operator@example.com"
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(ctx, storage.PluginStatePath(m.ID), body, obj.Generation); err != nil {
		t.Fatalf("seed tombstone: %v", err)
	}

	// D-06: the auto-create (CI/OIDC) path must never resurrect.
	_, _, err = svc.PublishVersion(ctx, m.ID, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator", true)
	var removedErr *RemovedError
	if !errors.As(err, &removedErr) {
		t.Fatalf("PublishVersion(autoCreate=true) on a tombstoned plugin = %v, want *RemovedError (410) — the auto-create path must never resurrect", err)
	}

	m2 := testManifest("plugin-demo", "1.0.1")
	jar2, err := BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.PublishFirst(ctx, &Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator")
	if err != nil {
		t.Fatalf("PublishFirst resurrection: %v", err)
	}
	if res.Version != "1.0.1" {
		t.Fatalf("resurrection published version %q, want 1.0.1", res.Version)
	}

	obj2, err := store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st2 domain.PluginState
	if err := json.Unmarshal(obj2.Data, &st2); err != nil {
		t.Fatal(err)
	}
	if st2.Removed != nil {
		t.Fatalf("resurrection must clear Removed, still set to %v", st2.Removed)
	}
	if st2.RemovalReason != "" || st2.RemovedBy != "" {
		t.Fatalf("resurrection must clear RemovalReason/RemovedBy, got %q/%q", st2.RemovalReason, st2.RemovedBy)
	}
	if st2.LatestVersion != "1.0.1" {
		t.Fatalf("LatestVersion = %q, want 1.0.1", st2.LatestVersion)
	}
}

// midPublishRemover wraps a real store and, on the SECOND read of
// pluginID's plugin.json, injects a concurrent tombstone commit before
// returning that read's result — modelling lifecycle.Service.RemovePlugin
// racing in exactly the window between PublishVersion's own top-level
// Removed check (the first read) and publish()'s plugin.json
// compare-and-swap, whose own initial read (via storage.WriteWithRetry) is
// the second. Under the CAS-before-artifacts order (see publish()'s doc
// comment), that CAS read is now the very first storage operation
// publish() performs, so "the second read of plugin.json" is precisely the
// seam this test needs to hit; hooking the jar write (as a pre-reorder
// version of this fixture did) would fire far too late, after artifact
// writes no longer happen before the CAS at all.
type midPublishRemover struct {
	storage.ObjectStore
	t        *testing.T
	pluginID string
	reads    int
	fired    bool
}

func (m *midPublishRemover) Read(ctx context.Context, objectPath string) (*storage.Object, error) {
	if objectPath == storage.PluginStatePath(m.pluginID) {
		m.reads++
		if m.reads == 2 && !m.fired {
			m.fired = true
			obj, rerr := m.ObjectStore.Read(ctx, objectPath)
			if rerr != nil {
				m.t.Fatalf("midPublishRemover: read plugin.json: %v", rerr)
			}
			var st domain.PluginState
			if uerr := json.Unmarshal(obj.Data, &st); uerr != nil {
				m.t.Fatalf("midPublishRemover: unmarshal plugin.json: %v", uerr)
			}
			now := time.Now().UTC()
			st.Removed = &now
			st.RemovalReason = "raced removal"
			st.RemovedBy = "operator@example.com"
			body, merr := json.MarshalIndent(st, "", "  ")
			if merr != nil {
				m.t.Fatalf("midPublishRemover: marshal: %v", merr)
			}
			if _, werr := m.ObjectStore.Write(ctx, objectPath, body, obj.Generation); werr != nil {
				m.t.Fatalf("midPublishRemover: write tombstone: %v", werr)
			}
		}
	}
	return m.ObjectStore.Read(ctx, objectPath)
}

// TestPublishVersionObservesRemovalThatCommitsMidPublish is the regression
// test for the go-assessment finding "PublishVersion checks the removed
// flag once at the top of the flow, but its own plugin.json
// compare-and-swap callback never re-checks it — so a removal that commits
// mid-publish is not observed." A DELETE that lands in the window between
// PublishVersion's initial read and publish()'s own plugin.json CAS must
// still be observed by that CAS, not silently overwritten by the in-flight
// publish.
//
// Mutation that makes this fail: removing the `if st.Removed != nil`
// re-check inside publish()'s WriteWithRetry callback (leaving only
// PublishVersion's earlier one-time check) makes this call succeed and
// append 2.0.0 to a tombstoned plugin.json instead of returning
// *RemovedError.
func TestPublishVersionObservesRemovalThatCommitsMidPublish(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)

	m := testManifest("plugin-demo", "1.0.0")
	jar1, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	seedSvc := newTestService(t, backing)
	if _, err := seedSvc.PublishFirst(ctx, &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	remover := &midPublishRemover{ObjectStore: backing, t: t, pluginID: m.ID}
	svc := newTestService(t, remover)

	m2 := testManifest("plugin-demo", "2.0.0")
	jar2, err := BuildTestJAR(m2)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = svc.PublishVersion(ctx, m.ID, &Bundle{JAR: jar2, JARFilename: "p.jar"}, "operator", false)
	var removedErr *RemovedError
	if !errors.As(err, &removedErr) {
		t.Fatalf("PublishVersion racing a mid-flight removal = %v, want *RemovedError (410)", err)
	}
	if !remover.fired {
		t.Fatalf("test bug: the simulated concurrent removal never fired")
	}

	obj, err := backing.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatal(err)
	}
	for _, v := range st.Versions {
		if v.Version == "2.0.0" {
			t.Fatalf("plugin.json must not record 2.0.0 when removal committed mid-publish")
		}
	}
}
