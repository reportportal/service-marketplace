// Package storagetest provides a fault-injecting storage.ObjectStore double
// for tests across the codebase. It exists as a separate, importable package
// (rather than *_test.go files inside internal/storage) so that other
// packages' tests can depend on it without pulling test-only code into the
// production storage package.
package storagetest

import (
	"context"
	"sync"
	"time"

	"github.com/reportportal/service-marketplace/internal/storage"
)

// Op identifies which ObjectStore method a fault applies to.
type Op string

const (
	OpRead       Op = "read"
	OpWrite      Op = "write"
	OpDelete     Op = "delete"
	OpExists     Op = "exists"
	OpListPrefix Op = "list_prefix"
	OpStat       Op = "stat"
	OpSignedURL  Op = "signed_url"
	OpReady      Op = "ready"
)

// AnyKey arms a fault for every object key under a given Op, for tests that
// want to model the backend being down entirely rather than one bad object.
const AnyKey = ""

type fault struct {
	err       error
	remaining int // <0 = fires on every matching call; N>0 = fires on the next N, then clears
}

// FaultStore wraps a storage.ObjectStore and lets a test arm an error keyed
// by (operation, object key) — "fail the write of index.json" — rather than
// by call count, which breaks the moment an unrelated step adds a write.
// Calls that don't match an armed fault pass through to the wrapped store
// unchanged, so FaultStore can wrap a real backend (e.g. LocalStore over a
// temp dir) and stay behaviourally correct except where a fault is armed.
type FaultStore struct {
	storage.ObjectStore
	mu     sync.Mutex
	faults map[string]*fault
	calls  map[string]int
}

// Wrap returns a FaultStore delegating to inner until a fault is armed.
func Wrap(inner storage.ObjectStore) *FaultStore {
	return &FaultStore{ObjectStore: inner, faults: map[string]*fault{}, calls: map[string]int{}}
}

func faultKey(op Op, objectPath string) string {
	return string(op) + "|" + objectPath
}

// Fail arms err to fire on every future call matching (op, objectPath).
// Pass AnyKey to match every key for that operation.
func (f *FaultStore) Fail(op Op, objectPath string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults[faultKey(op, objectPath)] = &fault{err: err, remaining: -1}
}

// FailN arms err to fire on the next n calls matching (op, objectPath); the
// (n+1)th and later matching calls pass through once the fault is exhausted.
func (f *FaultStore) FailN(op Op, objectPath string, err error, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults[faultKey(op, objectPath)] = &fault{err: err, remaining: n}
}

// Clear disarms a previously armed fault for (op, objectPath).
func (f *FaultStore) Clear(op Op, objectPath string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.faults, faultKey(op, objectPath))
}

// Calls reports how many times (op, objectPath) was invoked, for assertions
// like "the retry loop attempted this write exactly twice."
func (f *FaultStore) Calls(op Op, objectPath string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[faultKey(op, objectPath)]
}

// trigger records the call and returns the armed error, if any, checking the
// exact key first and falling back to the AnyKey wildcard for that op.
func (f *FaultStore) trigger(op Op, objectPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[faultKey(op, objectPath)]++
	for _, k := range []string{faultKey(op, objectPath), faultKey(op, AnyKey)} {
		ft, ok := f.faults[k]
		if !ok {
			continue
		}
		if ft.remaining > 0 {
			ft.remaining--
			if ft.remaining == 0 {
				delete(f.faults, k)
			}
		} else if ft.remaining == 0 {
			delete(f.faults, k)
			continue
		}
		return ft.err
	}
	return nil
}

func (f *FaultStore) Read(ctx context.Context, objectPath string) (*storage.Object, error) {
	if err := f.trigger(OpRead, objectPath); err != nil {
		return nil, err
	}
	return f.ObjectStore.Read(ctx, objectPath)
}

func (f *FaultStore) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	if err := f.trigger(OpWrite, objectPath); err != nil {
		return 0, err
	}
	return f.ObjectStore.Write(ctx, objectPath, data, expectedGen)
}

func (f *FaultStore) Delete(ctx context.Context, objectPath string) error {
	if err := f.trigger(OpDelete, objectPath); err != nil {
		return err
	}
	return f.ObjectStore.Delete(ctx, objectPath)
}

func (f *FaultStore) Exists(ctx context.Context, objectPath string) (bool, error) {
	if err := f.trigger(OpExists, objectPath); err != nil {
		return false, err
	}
	return f.ObjectStore.Exists(ctx, objectPath)
}

func (f *FaultStore) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	if err := f.trigger(OpListPrefix, prefix); err != nil {
		return nil, err
	}
	return f.ObjectStore.ListPrefix(ctx, prefix)
}

func (f *FaultStore) Stat(ctx context.Context, objectPath string) (*storage.ObjectMeta, error) {
	if err := f.trigger(OpStat, objectPath); err != nil {
		return nil, err
	}
	return f.ObjectStore.Stat(ctx, objectPath)
}

func (f *FaultStore) SignedURL(ctx context.Context, objectPath string, ttl time.Duration) (string, time.Time, error) {
	if err := f.trigger(OpSignedURL, objectPath); err != nil {
		return "", time.Time{}, err
	}
	return f.ObjectStore.SignedURL(ctx, objectPath, ttl)
}

func (f *FaultStore) Ready(ctx context.Context) error {
	if err := f.trigger(OpReady, AnyKey); err != nil {
		return err
	}
	return f.ObjectStore.Ready(ctx)
}
