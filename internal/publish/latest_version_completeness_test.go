package publish

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

// TestPublishCrashBeforeArtifactsDoesNotAdvanceLatestVersionOrBreakUnrelatedRebuild
// is MAJOR 1's end-to-end regression test (branch report): publish()
// advanced st.LatestVersion inside the FIRST compare-and-swap, before any
// artifact byte existed. rebuildIndex reads the manifest of LatestVersion.
// So if publishing plugin-a's 1.1.0 crashed after that first CAS committed
// but before the manifest was written, a LATER rebuild -- triggered by
// publishing some completely unrelated plugin-b -- hit an unreadable
// manifest for plugin-a. Under this branch's hardened rebuildIndex (which
// refuses to write a partial index rather than silently drop the plugin),
// that turned "publish plugin-b" into a hard failure for a problem entirely
// caused by plugin-a.
//
// This test reproduces the crash with real fault injection (not a hand-seeded
// plugin.json): plugin-a publishes 1.0.0 for real, then a 1.1.0 publish is
// interrupted by making its jar write fail. It asserts two things:
//  1. plugin-a's own plugin.json still names 1.0.0 as latestVersion --
//     LatestVersion must never name a version whose artifacts are missing.
//  2. A subsequent, wholly unrelated publish of plugin-b (which triggers its
//     own rebuildIndex over ALL plugins, including plugin-a) succeeds, and
//     the resulting index.json still lists plugin-a at latestVersion
//     1.0.0 -- neither silently dropped (the pre-hardening bug) nor a fatal
//     rebuild failure (the post-hardening-merge bug) is acceptable.
//
// Mutation this kills: moving `st.LatestVersion = domain.LatestVersion(...)`
// back into publish()'s first CAS (i.e. reverting MAJOR 1) makes assertion
// (1) fail immediately (latestVersion becomes "1.1.0"), and assertion (2)
// then fails too, because rebuildIndex's buildIndexData refuses to read
// 1.1.0's still-missing manifest and the whole publish-plugin-b call errors
// out.
func TestPublishCrashBeforeArtifactsDoesNotAdvanceLatestVersionOrBreakUnrelatedRebuild(t *testing.T) {
	ctx := context.Background()
	backing := newLocalStore(t)
	fs := storagetest.Wrap(backing)
	svc := newTestService(t, fs)

	base := testManifest("plugin-a", "1.0.0")
	jar0, err := BuildTestJAR(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishFirst(ctx, &Bundle{JAR: jar0, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("seed PublishFirst: %v", err)
	}

	next := testManifest("plugin-a", "1.1.0")
	jar1, err := BuildTestJAR(next)
	if err != nil {
		t.Fatal(err)
	}

	boom := errors.New("simulated crash writing 1.1.0's jar")
	artPath := storage.VersionArtifactPath("plugin-a", "1.1.0", string(domain.AccessPublic))
	fs.FailN(storagetest.OpWrite, artPath, boom, 1)

	if _, _, err := svc.PublishVersion(ctx, "plugin-a", &Bundle{JAR: jar1, JARFilename: "p.jar"}, "operator", false); !errors.Is(err, boom) {
		t.Fatalf("expected the injected jar write failure to surface, got %v", err)
	}

	stObj, err := backing.Read(ctx, storage.PluginStatePath("plugin-a"))
	if err != nil {
		t.Fatal(err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		t.Fatal(err)
	}
	if st.LatestVersion != "1.0.0" {
		t.Fatalf("latestVersion = %q, want %q: it must never name a version whose artifacts are missing", st.LatestVersion, "1.0.0")
	}

	// An unrelated publish elsewhere triggers its own rebuildIndex -- this
	// must succeed and must not delist plugin-a, even though plugin-a's
	// plugin.json now legitimately references a committed-but-incomplete
	// 1.1.0 in its Versions history.
	other := testManifest("plugin-b", "1.0.0")
	jarB, err := BuildTestJAR(other)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedSvc := newTestService(t, backing) // the real, unfaulted store
	if _, err := unrelatedSvc.PublishFirst(ctx, &Bundle{JAR: jarB, JARFilename: "p.jar"}, "operator"); err != nil {
		t.Fatalf("unrelated publish (which triggers rebuildIndex over every plugin) failed: %v -- an interrupted sibling publish must never break an unrelated rebuild", err)
	}

	idxObj, err := backing.Read(ctx, storage.PathIndex)
	if err != nil {
		t.Fatal(err)
	}
	var idx domain.Index
	if err := json.Unmarshal(idxObj.Data, &idx); err != nil {
		t.Fatal(err)
	}
	foundA := false
	for _, p := range idx.Plugins {
		if p.ID == "plugin-a" {
			foundA = true
			if p.LatestVersion != "1.0.0" {
				t.Fatalf("index.json plugin-a latestVersion = %q, want %q", p.LatestVersion, "1.0.0")
			}
		}
	}
	if !foundA {
		t.Fatalf("plugin-a missing from index.json after an unrelated rebuild -- it must stay listed at its last complete version, not disappear")
	}
}
