package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/reportportal/service-marketplace/internal/storage"
)

func newBackingStore(t *testing.T) *storage.LocalStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "signing-secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

// TestFailMatchesExactOpAndKey is the motivating case from the workstream brief:
// a test must be able to say "fail the write of index.json" without also
// failing writes to other keys.
func TestFailMatchesExactOpAndKey(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	fs := Wrap(newBackingStore(t))
	fs.Fail(OpWrite, "index.json", boom)

	if _, err := fs.Write(ctx, "index.json", []byte("{}"), 0); !errors.Is(err, boom) {
		t.Fatalf("expected armed fault on index.json write, got %v", err)
	}
	if _, err := fs.Write(ctx, "plugins/p/plugin.json", []byte("{}"), 0); err != nil {
		t.Fatalf("write to a different key must pass through unfaulted, got %v", err)
	}
}

// TestFailDoesNotMatchDifferentOp proves the fault is keyed by operation too:
// arming a write fault must not affect reads of the same key.
func TestFailDoesNotMatchDifferentOp(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	fs := Wrap(newBackingStore(t))
	if _, err := fs.Write(ctx, "index.json", []byte("{}"), 0); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	fs.Fail(OpWrite, "index.json", boom)

	if _, err := fs.Read(ctx, "index.json"); err != nil {
		t.Fatalf("read must not be affected by a write-only fault, got %v", err)
	}
}

// TestFailNClearsAfterCount verifies the fault survives exactly N matching
// calls (modelling "the first attempt fails, the retry succeeds") rather than
// being keyed to an absolute call index.
func TestFailNClearsAfterCount(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	fs := Wrap(newBackingStore(t))
	fs.FailN(OpWrite, "index.json", boom, 1)

	if _, err := fs.Write(ctx, "index.json", []byte(`{"n":1}`), 0); !errors.Is(err, boom) {
		t.Fatalf("first write should hit the armed fault, got %v", err)
	}
	if _, err := fs.Write(ctx, "index.json", []byte(`{"n":1}`), 0); err != nil {
		t.Fatalf("second write should pass through once the fault is exhausted, got %v", err)
	}
	if _, err := fs.Write(ctx, "index.json", []byte(`{"n":2}`), 1); err != nil {
		t.Fatalf("third write should be unaffected, got %v", err)
	}
}

// TestAnyKeyMatchesEveryKeyForOp verifies the wildcard object key applies the
// fault to every object under that operation, for tests that want to model a
// storage backend that is down entirely rather than one bad object.
func TestAnyKeyMatchesEveryKeyForOp(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	fs := Wrap(newBackingStore(t))
	fs.Fail(OpRead, AnyKey, boom)

	if _, err := fs.Read(ctx, "index.json"); !errors.Is(err, boom) {
		t.Fatalf("expected AnyKey fault to apply to index.json, got %v", err)
	}
	if _, err := fs.Read(ctx, "plugins/p/plugin.json"); !errors.Is(err, boom) {
		t.Fatalf("expected AnyKey fault to apply to any other key, got %v", err)
	}
}

func TestClearDisarmsFault(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	fs := Wrap(newBackingStore(t))
	fs.Fail(OpWrite, "index.json", boom)
	fs.Clear(OpWrite, "index.json")

	if _, err := fs.Write(ctx, "index.json", []byte("{}"), 0); err != nil {
		t.Fatalf("expected cleared fault to not fire, got %v", err)
	}
}

func TestCallsCountsMatchingInvocations(t *testing.T) {
	ctx := context.Background()
	fs := Wrap(newBackingStore(t))
	if _, err := fs.Write(ctx, "index.json", []byte(`{"n":1}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Write(ctx, "index.json", []byte(`{"n":2}`), 1); err != nil {
		t.Fatal(err)
	}
	if got := fs.Calls(OpWrite, "index.json"); got != 2 {
		t.Fatalf("expected 2 recorded writes, got %d", got)
	}
	if got := fs.Calls(OpWrite, "plugins/p/plugin.json"); got != 0 {
		t.Fatalf("expected 0 calls for an untouched key, got %d", got)
	}
}

// TestFaultStoreImplementsObjectStore is a compile-time-ish sanity check that
// unfaulted methods (PublicURL, Type) still promote through to the wrapped
// store rather than needing to be reimplemented.
func TestFaultStoreImplementsObjectStore(t *testing.T) {
	var _ storage.ObjectStore = Wrap(newBackingStore(t))
}

func TestPublicURLAndTypePassThroughUnfaulted(t *testing.T) {
	backing := newBackingStore(t)
	fs := Wrap(backing)
	if fs.Type() != backing.Type() {
		t.Fatalf("Type() should pass through: got %q want %q", fs.Type(), backing.Type())
	}
	if fs.PublicURL("index.json") != backing.PublicURL("index.json") {
		t.Fatalf("PublicURL() should pass through")
	}
}
