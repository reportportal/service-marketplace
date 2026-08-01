package lifecycle

// TestBlockVersion_InvalidatesVersionOwnPaths closes the remaining gap in
// AMD-07 / §6.4's Cloud CDN invalidation matrix "Block version" row, which
// names three paths:
//
//	/index.json, /plugins/{id}/plugin.json, /plugins/{id}/versions/{ver}/*
//
// A prior fix (index_rebuild_test.go) added the first path by calling
// s.Publisher.RebuildIndex before invalidating. BlockVersion's own
// Invalidate call, however, still only ever named /index.json and
// /plugins/{id}/plugin.json -- the version's own paths
// (manifest.json, the .jar, CHANGELOG.md, screenshots, advisory.json, all
// served under plugins/{id}/versions/{ver}/*) were never invalidated.
//
// The consequence is the one the matrix exists to prevent: a CDN edge that
// already cached the blocked version's manifest/jar keeps serving them --
// with their long max-age=31536000,immutable Cache-Control -- to any client
// that resolves the path directly, even though plugin.json and index.json
// now correctly show it blocked. A client can still download a version the
// registry just blocked.
//
// Mutation this kills: removing the third element
// (VersionPrefix(pluginID, version)+"*") from the paths slice BlockVersion
// passes to Invalidate -- this test fails because that path is absent from
// the recorded invalidation calls, even though plugin.json and index.json
// are (correctly) still invalidated.
import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// recordingInvalidator captures every path passed to Invalidate across all
// calls, so a test can assert on the union without caring how many
// Invalidate calls produced it.
type recordingInvalidator struct {
	calls [][]string
}

func (r *recordingInvalidator) Invalidate(_ context.Context, paths []string) error {
	cp := append([]string(nil), paths...)
	r.calls = append(r.calls, cp)
	return nil
}

func (r *recordingInvalidator) allPaths() []string {
	var out []string
	for _, c := range r.calls {
		out = append(out, c...)
	}
	sort.Strings(out)
	return out
}

func TestBlockVersion_InvalidatesVersionOwnPaths(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "", "")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	inv := &recordingInvalidator{}
	pub := &publish.Service{Store: store, Invalidator: inv}
	svc := &Service{Store: store, Invalidator: inv, Publisher: pub}

	ctx := context.Background()
	const pluginID = "plugin-demo"
	publishForBlockTest(t, svc, pluginID, "1.0.0", true)
	publishForBlockTest(t, svc, pluginID, "2.0.0", false)

	// Publishing issues its own invalidation calls; only what BlockVersion
	// itself triggers is under test here.
	inv.calls = nil

	if _, err := svc.BlockVersion(ctx, pluginID, "2.0.0", "CVE-2026-0003"); err != nil {
		t.Fatalf("BlockVersion: %v", err)
	}

	got := inv.allPaths()
	want := []string{
		"/" + storage.PathIndex,
		"/" + storage.PluginStatePath(pluginID),
		"/" + storage.VersionPrefix(pluginID, "2.0.0") + "*",
	}
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BlockVersion invalidated paths = %v, want %v (§6.4 block row: index.json, the plugin's plugin.json, and the version's own versions/{ver}/* -- a CDN edge otherwise keeps serving the blocked version's manifest/jar)", got, want)
	}
}
