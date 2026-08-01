package auth

import (
	"context"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/reportportal/service-marketplace/internal/storage"
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

func TestAdminLockoutResetsOnSuccessAcrossReplicas(t *testing.T) {
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.test", "secret")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	passwordHash := testBcryptHash(t, "correct-horse-battery-staple")
	replicaA := NewAdminAuthenticator(true, "admin", passwordHash, store)
	replicaB := NewAdminAuthenticator(true, "admin", passwordHash, store)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		_ = replicaA.Authenticate(ctx, "203.0.113.9", "admin", "wrong-password")
	}
	// A successful login on replica B must clear the shared failure count
	// recorded by replica A.
	if err := replicaB.Authenticate(ctx, "203.0.113.9", "admin", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("expected successful login, got %v", err)
	}
	// The account should not be locked -- a 5th "failure" worth of headroom
	// must be available again on either replica.
	if err := replicaA.Authenticate(ctx, "203.0.113.9", "admin", "wrong-password"); err != ErrUnauthorized {
		t.Fatalf("expected ordinary ErrUnauthorized after reset, got %v", err)
	}
}

// TestAdminLockoutConcurrentFailuresRaceSafe drives concurrent failed
// logins for the same key from multiple goroutines across two replica
// instances sharing one store. It exists to be run under `go test -race`:
// the CAS increment in recordFailure (storage.WriteWithRetry) must be the
// only thing serializing access to the shared counter, with no data race
// on either AdminAuthenticator's own local state.
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
