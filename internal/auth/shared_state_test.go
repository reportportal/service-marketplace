package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

func testBcryptHash(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

// These tests are the regression coverage for assessment finding
// F4-inmemory-state-not-shared-across-replicas: the OAuth CSRF-state store
// and the admin login lockout counters lived only in the memory of
// whichever replica handled the request, unlike the session denylist
// (Denylist, above in this package), which is correctly backed by the
// shared object store. Both tests simulate two replicas by constructing two
// independent instances that share one storage.ObjectStore, mirroring how
// two Denylist instances would behave in the existing tests for that type.

// TestOAuthStateSharedAcrossReplicas reproduces the availability bug
// directly: GET /auth/github/login on replica A issues a state that must be
// consumable by GET /auth/github/callback landing on replica B.
func TestOAuthStateSharedAcrossReplicas(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	replicaA := NewOAuthStateStore(store)
	replicaB := NewOAuthStateStore(store)
	ctx := context.Background()

	state, err := replicaA.Issue(ctx)
	if err != nil {
		t.Fatalf("Issue on replica A: %v", err)
	}

	// The callback lands on a *different* replica than the one that issued
	// the state (no sticky sessions) -- this must still succeed.
	if !replicaB.Consume(ctx, state) {
		t.Fatalf("replica B could not consume a state issued by replica A -- every cross-replica GitHub login would fail with 'Invalid OAuth state'")
	}

	// One-time use: a second consumption of the same state, from either
	// replica, must fail.
	if replicaA.Consume(ctx, state) {
		t.Fatalf("state was consumable twice")
	}
}

func TestOAuthStateUnknownStateRejected(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	s := NewOAuthStateStore(store)
	if s.Consume(context.Background(), "never-issued") {
		t.Fatalf("an unissued state must not be consumable")
	}
}

// TestAdminLockoutSharedAcrossReplicas reproduces the diluted-protection bug:
// an attacker round-robined across N replicas must still be limited to 5
// failed attempts total for a given clientKey+username, not 5 per replica.
func TestAdminLockoutSharedAcrossReplicas(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	passwordHash := testBcryptHash(t, "correct-horse-battery-staple")
	replicaA := NewAdminAuthenticator(true, "admin", passwordHash, store)
	replicaB := NewAdminAuthenticator(true, "admin", passwordHash, store)
	ctx := context.Background()

	replicas := []*AdminAuthenticator{replicaA, replicaB}
	// 5 failed attempts, alternating replica, must exhaust the shared
	// allowance -- regardless of how many distinct replica instances see
	// the requests.
	for i := 0; i < 5; i++ {
		r := replicas[i%2]
		if err := r.Authenticate(ctx, "203.0.113.5", "admin", "wrong-password"); err != ErrUnauthorized {
			t.Fatalf("attempt %d: expected ErrUnauthorized, got %v", i+1, err)
		}
	}

	// The 6th attempt, on the *other* replica than the 5th, must already be
	// locked out -- a per-process counter would allow this (5 more on this
	// replica) instead.
	if err := replicaA.Authenticate(ctx, "203.0.113.5", "admin", "wrong-password"); err != ErrTooManyAttempts {
		t.Fatalf("expected shared lockout after 5 failures across replicas, got %v", err)
	}
	if err := replicaB.Authenticate(ctx, "203.0.113.5", "admin", "wrong-password"); err != ErrTooManyAttempts {
		t.Fatalf("expected shared lockout on the other replica too, got %v", err)
	}
}

// TestAdminLockoutResetsOnSuccessAcrossReplicas is the regression test for
// the Store-backed half of AdminAuthenticator.reset(): a successful login
// must clear the *shared* failure count, not just whatever replicaB happens
// to remember locally (replicaB never recorded any of the failures itself,
// so it has nothing local to clear either way).
//
// The single post-reset assertion this test used to end on -- one more
// failed attempt returns the ordinary ErrUnauthorized, not
// ErrTooManyAttempts -- cannot actually tell a working reset() apart from a
// reset() that does nothing at all: 4 pre-reset failures leaves the stored
// count at 4 either way (delete leaves it at 0, a no-op leaves it at 4),
// and 4 < loginLockoutMaxFailures(5) in both cases, so that one attempt
// passes regardless of whether reset() ran. A prior review caught exactly
// this: mutating reset() into a total no-op (removing both the store
// delete and the local-map delete) left the entire suite, including this
// test, green.
//
// This version instead asserts the actual consequence directly -- the
// shared store record is gone -- and then behaviorally proves a *full*
// fresh allowance is available (not just the one attempt of headroom the
// stale count of 4 would have coincidentally tolerated anyway).
func TestAdminLockoutResetsOnSuccessAcrossReplicas(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	passwordHash := testBcryptHash(t, "correct-horse-battery-staple")
	replicaA := NewAdminAuthenticator(true, "admin", passwordHash, store)
	replicaB := NewAdminAuthenticator(true, "admin", passwordHash, store)
	ctx := context.Background()
	key := "203.0.113.9|admin"

	for i := 0; i < 4; i++ {
		_ = replicaA.Authenticate(ctx, "203.0.113.9", "admin", "wrong-password")
	}
	// A successful login on replica B must clear the shared failure count
	// recorded by replica A.
	if err := replicaB.Authenticate(ctx, "203.0.113.9", "admin", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("expected successful login, got %v", err)
	}

	// Assert the consequence directly: the shared lockout record for this
	// key must no longer exist in the store, and replica A -- which never
	// initiated the reset -- must independently read the count back as
	// zero from that same store.
	if _, err := store.Read(ctx, storage.LoginLockoutPath(key)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected reset() to delete the shared lockout record, got err=%v", err)
	}
	if rec := replicaA.load(ctx, key); rec.Failures != 0 {
		t.Fatalf("expected a zeroed failure count after reset, got %d", rec.Failures)
	}

	// Behavioral confirmation: a full fresh allowance of
	// loginLockoutMaxFailures failures must be available again on either
	// replica -- Authenticate's lockout guard checks the count *before*
	// incrementing it, so it takes exactly loginLockoutMaxFailures calls to
	// reach the threshold and one more to observe ErrTooManyAttempts. A
	// stale, unreset count of 4 would already trip that guard on the 2nd of
	// these loop iterations (4+1=5 satisfies the threshold one full round
	// early), which is exactly what distinguishes a real reset from the
	// no-op mutation described above.
	for i := 0; i < loginLockoutMaxFailures; i++ {
		if err := replicaA.Authenticate(ctx, "203.0.113.9", "admin", "wrong-password"); err != ErrUnauthorized {
			t.Fatalf("post-reset attempt %d: expected ordinary ErrUnauthorized, got %v", i+1, err)
		}
	}
	if err := replicaB.Authenticate(ctx, "203.0.113.9", "admin", "wrong-password"); err != ErrTooManyAttempts {
		t.Fatalf("expected lockout after a fresh %d failures post-reset, got %v", loginLockoutMaxFailures, err)
	}
}

// TestAdminLockoutResetClearsLocalMapWhenStoreNil is the regression test
// for the other half of reset() the Store-backed test above cannot reach:
// AdminAuthenticator.local, used only when Store is nil (a single-replica
// deployment, or any existing test that doesn't wire storage). Before this
// test, that branch of reset() had no coverage at all on this branch, so
// mutating it into a no-op alongside the store-delete branch (the exact
// mutation described on the Store-backed test above) was invisible in
// principle, not just in practice.
func TestAdminLockoutResetClearsLocalMapWhenStoreNil(t *testing.T) {
	passwordHash := testBcryptHash(t, "correct-horse-battery-staple")
	a := NewAdminAuthenticator(true, "admin", passwordHash, nil)
	ctx := context.Background()
	key := "198.51.100.4|admin"

	for i := 0; i < 4; i++ {
		_ = a.Authenticate(ctx, "198.51.100.4", "admin", "wrong-password")
	}
	if rec := a.load(ctx, key); rec.Failures != 4 {
		t.Fatalf("setup: expected 4 recorded failures, got %d", rec.Failures)
	}

	if err := a.Authenticate(ctx, "198.51.100.4", "admin", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("expected successful login, got %v", err)
	}

	// Assert the consequence directly: reset() must delete the local-map
	// entry outright, not merely leave a record with Failures==0 behind
	// under the key (load's zero-value fallback for an absent key would
	// make that indistinguishable from the behavioral check below, but not
	// from this direct one).
	a.mu.Lock()
	_, present := a.local[key]
	a.mu.Unlock()
	if present {
		t.Fatalf("expected reset() to delete the local-map entry for %q, but it is still present", key)
	}

	// Behavioral confirmation, mirroring the Store-backed variant: a full
	// fresh allowance of loginLockoutMaxFailures failures must be
	// available, which a stale count of 4 would fail on the 2nd attempt
	// below (see the comment on the Store-backed test above for why the
	// loop runs loginLockoutMaxFailures times, not loginLockoutMaxFailures-1).
	for i := 0; i < loginLockoutMaxFailures; i++ {
		if err := a.Authenticate(ctx, "198.51.100.4", "admin", "wrong-password"); err != ErrUnauthorized {
			t.Fatalf("post-reset attempt %d: expected ordinary ErrUnauthorized, got %v", i+1, err)
		}
	}
	if err := a.Authenticate(ctx, "198.51.100.4", "admin", "wrong-password"); err != ErrTooManyAttempts {
		t.Fatalf("expected lockout after a fresh %d failures post-reset, got %v", loginLockoutMaxFailures, err)
	}
}

// TestAdminLockoutConcurrentFailuresRaceSafe drives concurrent failed
// logins for the same key from multiple goroutines across two replica
// instances sharing one store. It exists to be run under `go test -race`,
// which is what it actually proves: no goroutine touches
// AdminAuthenticator's own local state (a.local, a.mu) unsynchronized while
// Store is set, since recordFailure's Store!=nil branch never touches
// a.local at all -- if a future change reintroduced dual bookkeeping there
// without holding a.mu, this is what would catch it.
//
// What this test does *not* reliably prove, despite its final assertion
// reading that way: that storage.WriteWithRetry's CAS retry loop is
// necessary for correctness. 20 goroutines racing against a
// same-machine, low-latency LocalStore rarely produce more than a couple
// of genuine write conflicts, and the final check only requires the shared
// count to clear a threshold of 5 -- replacing WriteWithRetry with a
// single non-retried write that silently drops the increment on conflict
// (verified by hand: temporarily made that swap, not committed) still
// passed this test every time, because losing a handful of 20 updates
// still crosses 5. TestAdminLockoutRecordFailureRetriesThroughStorageConflicts
// below is the deterministic test that actually closes that gap by forcing
// a conflict instead of hoping goroutine scheduling produces one.
func TestAdminLockoutConcurrentFailuresRaceSafe(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	passwordHash := testBcryptHash(t, "correct-horse-battery-staple")
	replicaA := NewAdminAuthenticator(true, "admin", passwordHash, store)
	replicaB := NewAdminAuthenticator(true, "admin", passwordHash, store)
	ctx := context.Background()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			r := replicaA
			if i%2 == 0 {
				r = replicaB
			}
			_ = r.Authenticate(ctx, "203.0.113.77", "admin", "wrong-password")
		}(i)
	}
	wg.Wait()

	// After the dust settles, the shared counter must reflect a real
	// lockout -- not "no lockout at all", which is what a data race or a
	// lost-update bug in the CAS loop would produce.
	if err := replicaA.Authenticate(ctx, "203.0.113.77", "admin", "wrong-password"); err != ErrTooManyAttempts {
		t.Fatalf("expected lockout after %d concurrent failures, got %v", goroutines, err)
	}
}

// TestAdminLockoutRecordFailureRetriesThroughStorageConflicts is the
// deterministic counterpart to TestAdminLockoutConcurrentFailuresRaceSafe
// above (see that test's doc comment for why the concurrent version cannot
// reliably tell a working CAS retry loop apart from a broken one). Instead
// of hoping goroutine scheduling produces a write conflict, it forces one
// with storagetest.FaultStore and asserts the actual consequence directly:
// the failure is still recorded despite the forced conflicts, and more
// than one Write call happened -- proof of an actual retry, not luck.
func TestAdminLockoutRecordFailureRetriesThroughStorageConflicts(t *testing.T) {
	inner, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	fs := storagetest.Wrap(inner)
	passwordHash := testBcryptHash(t, "correct-horse-battery-staple")
	a := NewAdminAuthenticator(true, "admin", passwordHash, fs)
	ctx := context.Background()
	key := "203.0.113.200|admin"
	path := storage.LoginLockoutPath(key)

	// Force the first two writes of this record to lose the CAS race, the
	// way a genuinely concurrent replica's write would, and only let the
	// third attempt land.
	fs.FailN(storagetest.OpWrite, path, storage.ErrConflict, 2)

	if err := a.Authenticate(ctx, "203.0.113.200", "admin", "wrong-password"); err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	if calls := fs.Calls(storagetest.OpWrite, path); calls < 3 {
		t.Fatalf("expected recordFailure to retry past the forced conflicts (>=3 Write calls: 2 that lost the CAS race, 1 that landed), got %d", calls)
	}
	if rec := a.load(ctx, key); rec.Failures != 1 {
		t.Fatalf("expected the failure to be recorded despite the forced conflicts, got Failures=%d", rec.Failures)
	}
}
