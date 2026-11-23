package auth

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func newTestAdmin(t *testing.T) (*AdminAuthenticator, *time.Time) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	a := NewAdminAuthenticator(true, "admin", string(hash))
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	return a, &now
}

func TestAdminLockoutAfterFiveFailures(t *testing.T) {
	a, now := newTestAdmin(t)
	for i := range adminMaxFailures {
		if err := a.Authenticate("ip1", "admin", "wrong"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := a.Authenticate("ip1", "admin", "right"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("locked out: got %v", err)
	}
	// another client is not affected
	if err := a.Authenticate("ip2", "admin", "right"); err != nil {
		t.Fatalf("other client: %v", err)
	}
	// still locked just before the window ends; this call also prunes, so
	// the next one, within adminPruneEvery, is decided by the lockout check
	*now = now.Add(adminLockout - adminPruneEvery/2)
	if err := a.Authenticate("ip1", "admin", "right"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("before the window ends: got %v", err)
	}
	*now = now.Add(adminPruneEvery / 2)
	if err := a.Authenticate("ip1", "admin", "right"); err != nil {
		t.Fatalf("after the lockout: %v", err)
	}
}

// varying the username must neither reset the limit nor add a record per name
func TestAdminUnknownUsernamesShareOneRecord(t *testing.T) {
	a, _ := newTestAdmin(t)
	for i := range adminMaxFailures {
		_ = a.Authenticate("ip1", fmt.Sprintf("user%d", i), "wrong")
	}
	if err := a.Authenticate("ip1", "someone-else", "wrong"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("sixth unknown username: got %v", err)
	}
	if n := len(a.failures); n != 1 {
		t.Fatalf("records %d, want 1", n)
	}
	// guessing usernames does not lock out the real one
	if err := a.Authenticate("ip1", "admin", "right"); err != nil {
		t.Fatalf("real username: %v", err)
	}
}

func TestAdminFailureRecordsArePruned(t *testing.T) {
	a, now := newTestAdmin(t)
	for i := range 100 {
		_ = a.Authenticate(fmt.Sprintf("ip%d", i), "admin", "wrong")
	}
	if n := len(a.failures); n != 100 {
		t.Fatalf("records %d, want 100", n)
	}
	*now = now.Add(adminLockout + adminPruneEvery)
	_ = a.Authenticate("ip-new", "admin", "wrong")
	if n, m := len(a.failures), len(a.lastFail); n != 1 || m != 1 {
		t.Fatalf("records after the lockout window: failures %d, lastFail %d, want 1", n, m)
	}
}

func TestAdminSuccessClearsRecord(t *testing.T) {
	a, _ := newTestAdmin(t)
	_ = a.Authenticate("ip1", "admin", "wrong")
	if err := a.Authenticate("ip1", "admin", "right"); err != nil {
		t.Fatal(err)
	}
	if n, m := len(a.failures), len(a.lastFail); n != 0 || m != 0 {
		t.Fatalf("records: failures %d, lastFail %d, want 0", n, m)
	}
}

// failures older than the window do not count, whether or not a prune ran
func TestAdminOldFailuresDoNotCount(t *testing.T) {
	a, now := newTestAdmin(t)
	for range adminMaxFailures - 1 {
		_ = a.Authenticate("ip1", "admin", "wrong")
	}
	// keep pruning from running on the next call
	*now = now.Add(adminLockout + adminPruneEvery/2)
	a.lastPrune = *now
	_ = a.Authenticate("ip1", "admin", "wrong")
	if err := a.Authenticate("ip1", "admin", "right"); err != nil {
		t.Fatalf("one recent failure must not lock: %v", err)
	}
}
