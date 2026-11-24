package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// recordingInvalidator remembers every path it was asked to purge.
type recordingInvalidator struct {
	mu    sync.Mutex
	paths []string
}

func (r *recordingInvalidator) Invalidate(_ context.Context, paths []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, paths...)
	return nil
}

func (r *recordingInvalidator) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

type fixture struct {
	svc   *Service
	store *storage.LocalStore
	inv   *recordingInvalidator
	log   *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "test-signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	inv := &recordingInvalidator{}
	buf := &bytes.Buffer{}
	return &fixture{
		store: store,
		inv:   inv,
		log:   buf,
		svc: &Service{
			Store:       store,
			Invalidator: inv,
			Publisher:   &publish.Service{Store: store, Invalidator: cdn.NoopInvalidator{}},
			Logger:      log.New(buf, "", 0),
		},
	}
}

// seed writes a plugin with one public object, one premium jar, and a live plugin.json.
func (f *fixture) seed(t *testing.T, id string) {
	t.Helper()
	ctx := context.Background()
	st, _ := json.Marshal(domain.PluginState{ID: id, Tier: domain.TierOfficial, LatestVersion: "1.0.0"})
	for path, data := range map[string][]byte{
		storage.PluginStatePath(id):                         st,
		storage.VersionManifestPath(id, "1.0.0"):            []byte(`{}`),
		storage.VersionArtifactPath(id, "1.0.0", "premium"): []byte("premium"),
		storage.VersionArtifactPath(id, "1.0.0", "public"):  []byte("public"),
		storage.VersionChangelogPath(id, "1.0.0"):           []byte("changes"),
		storage.VersionAdvisoryPath(id, "1.0.0"):            []byte(`{}`),
	} {
		if _, err := f.store.Write(ctx, path, data, 0); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
}

func (f *fixture) objects(t *testing.T, id string) []string {
	t.Helper()
	var all []string
	for _, p := range []string{storage.PluginPrefix(id), storage.PrivatePluginPrefix(id)} {
		files, err := f.store.ListPrefix(context.Background(), p)
		if err != nil {
			t.Fatalf("list %s: %v", p, err)
		}
		all = append(all, files...)
	}
	return all
}

func contains(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// TestRemovalPurgesTheWholePluginSubtreeOnTheCDN: a removed plugin's jar must stop being served
// from the edge too. Invalidating only plugin.json (what the code did) leaves every manifest and
// jar cached until it expires, which is the opposite of "the bad jar can never be served again".
func TestRemovalPurgesTheWholePluginSubtreeOnTheCDN(t *testing.T) {
	for name, remove := range map[string]func(f *fixture) error{
		"with tombstone": func(f *fixture) error {
			_, err := f.svc.RemovePlugin(context.Background(), "plugin-a", "reason", "op")
			return err
		},
		"erased": func(f *fixture) error {
			_, err := f.svc.ErasePlugin(context.Background(), "plugin-a", "reason", "op")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.seed(t, "plugin-a")
			if err := remove(f); err != nil {
				t.Fatalf("remove: %v", err)
			}
			seen := f.inv.seen()
			for _, want := range []string{"/index.json", "/plugins/plugin-a/*"} {
				if !contains(seen, want) {
					t.Errorf("CDN purge %v is missing %q", seen, want)
				}
			}
		})
	}
}

// TestRemovalNeverTouchesAnotherPlugin: ids share a prefix tree, and "plugin-a" is a prefix of
// "plugin-ab". The listing prefix ends in a slash for exactly this reason.
func TestRemovalNeverTouchesAnotherPlugin(t *testing.T) {
	f := newFixture(t)
	f.seed(t, "plugin-a")
	f.seed(t, "plugin-ab")
	for _, p := range []string{storage.PluginPrefix("plugin-bystander") + "x.json", storage.PrivatePluginPrefix("plugin-bystander") + "x"} {
		if _, err := f.store.Write(context.Background(), p, []byte("kept"), 0); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	if _, err := f.svc.ErasePlugin(context.Background(), "plugin-a", "", "op"); err != nil {
		t.Fatalf("erase: %v", err)
	}
	if got := f.objects(t, "plugin-a"); len(got) != 0 {
		t.Errorf("plugin-a left %v", got)
	}
	if got := f.objects(t, "plugin-ab"); len(got) == 0 {
		t.Errorf("erasing plugin-a deleted plugin-ab's objects")
	}
	if got := f.objects(t, "plugin-bystander"); len(got) != 2 {
		t.Errorf("an unrelated plugin lost objects: %v", got)
	}
}

// TestAuditLine: with no audit log and no tombstone after an erase, the single log line is the only
// record of who did what. It has to name the plugin, the operator, what the plugin was, and why.
func TestAuditLine(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.seed(t, "plugin-live")
	f.seed(t, "plugin-gone")

	if _, err := f.svc.RemovePlugin(ctx, "plugin-gone", "Legal request", "alice"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := f.svc.ErasePlugin(ctx, "plugin-gone", "", "bob"); err != nil {
		t.Fatalf("erase tombstoned: %v", err)
	}
	if _, err := f.svc.ErasePlugin(ctx, "plugin-live", "dev clean-up", "carol"); err != nil {
		t.Fatalf("erase live: %v", err)
	}

	out := f.log.String()
	for _, want := range []string{
		`audit: plugin "plugin-gone" removed with tombstone by "alice", reason "Legal request"`,
		`audit: plugin "plugin-gone" erased without tombstone by "bob" (was removed at `,
		`by alice), reason ""`,
		`audit: plugin "plugin-live" erased without tombstone by "carol" (was live), reason "dev clean-up"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("audit output lacks %q\n---\n%s", want, out)
		}
	}
}

// TestRestoreIsAudited keeps the reversal on the same record as the removal.
func TestRestoreIsAudited(t *testing.T) {
	f := newFixture(t)
	f.seed(t, "plugin-a")
	if _, err := f.svc.RemovePlugin(context.Background(), "plugin-a", "reason", "alice"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := f.svc.RestorePlugin(context.Background(), "plugin-a", "bob"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if want := `audit: plugin "plugin-a" restored by "bob"`; !strings.Contains(f.log.String(), want) {
		t.Errorf("audit output lacks %q\n---\n%s", want, f.log.String())
	}
}

// TestUnknownPluginIsErrNotFound is the service-level half of the 404 fix: no decoding of an empty
// document, and so no 500, for an id that was never published.
func TestUnknownPluginIsErrNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.RemovePlugin(ctx, "plugin-none", "r", "op"); err != ErrNotFound {
		t.Errorf("RemovePlugin on an unknown id: %v, want ErrNotFound", err)
	}
	if _, err := f.svc.ErasePlugin(ctx, "plugin-none", "", "op"); err != ErrNotFound {
		t.Errorf("ErasePlugin on an unknown id: %v, want ErrNotFound", err)
	}
	if _, err := f.svc.RestorePlugin(ctx, "plugin-none", "op"); err != ErrNotFound {
		t.Errorf("RestorePlugin on an unknown id: %v, want ErrNotFound", err)
	}
}

// TestListRemovedIgnoresErasedAndLive: an erased plugin has no plugin.json, so it cannot be listed,
// and a live one is not removed.
func TestListRemovedIgnoresErasedAndLive(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, id := range []string{"plugin-live", "plugin-removed", "plugin-erased"} {
		f.seed(t, id)
	}
	if _, err := f.svc.RemovePlugin(ctx, "plugin-removed", "reason", "op"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := f.svc.ErasePlugin(ctx, "plugin-erased", "", "op"); err != nil {
		t.Fatalf("erase: %v", err)
	}
	got, err := f.svc.ListRemoved(ctx)
	if err != nil {
		t.Fatalf("ListRemoved: %v", err)
	}
	if len(got) != 1 || got[0].ID != "plugin-removed" {
		t.Errorf("ListRemoved = %+v, want only plugin-removed", got)
	}
}
