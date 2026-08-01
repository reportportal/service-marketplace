package auth

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/reportportal/service-marketplace/internal/storage"
)

var ErrTooManyAttempts = errors.New("too many attempts")

const (
	loginLockoutMaxFailures = 5
	loginLockoutWindow      = 15 * time.Minute
)

// lockoutRecord is the wire shape persisted at
// storage.LoginLockoutPath(key): the failure count and the timestamp of the
// most recent one, which is all Authenticate needs to evaluate the rolling
// 15-minute window.
type lockoutRecord struct {
	Failures int       `json:"failures"`
	LastFail time.Time `json:"lastFail"`
}

// AdminAuthenticator enforces the ADMIN_PASSWORD fallback login and its
// five-attempts-per-fifteen-minutes lockout. The lockout counters are
// backed by the shared object store — the same pattern Denylist uses in
// session.go — so the protection holds across all replicas instead of per
// process: before this, an attacker round-robined across N replicas behind
// a load balancer got 5 attempts per replica instead of 5 in total,
// diluting the protection by the replica count (assessment finding
// F4-inmemory-state-not-shared-across-replicas). Store may be nil (tests,
// or a deployment that never runs more than one replica), in which case
// AdminAuthenticator falls back to the original per-process map.
type AdminAuthenticator struct {
	Enabled      bool
	Username     string
	PasswordHash string
	Store        storage.ObjectStore

	mu    sync.Mutex
	local map[string]lockoutRecord // used only when Store is nil
}

func NewAdminAuthenticator(enabled bool, username, passwordHash string, store storage.ObjectStore) *AdminAuthenticator {
	return &AdminAuthenticator{
		Enabled:      enabled,
		Username:     username,
		PasswordHash: passwordHash,
		Store:        store,
		local:        map[string]lockoutRecord{},
	}
}

func (a *AdminAuthenticator) Configured() bool {
	return a.Enabled && a.PasswordHash != ""
}

func (a *AdminAuthenticator) Authenticate(ctx context.Context, clientKey, username, password string) error {
	if !a.Configured() {
		return ErrForbidden
	}
	key := clientKey + "|" + username
	rec := a.load(ctx, key)
	if rec.Failures >= loginLockoutMaxFailures && time.Since(rec.LastFail) < loginLockoutWindow {
		return ErrTooManyAttempts
	}
	if username != a.Username {
		a.recordFailure(ctx, key)
		return ErrUnauthorized
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)); err != nil {
		a.recordFailure(ctx, key)
		return ErrUnauthorized
	}
	a.reset(ctx, key)
	return nil
}

func (a *AdminAuthenticator) load(ctx context.Context, key string) lockoutRecord {
	if a.Store == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.local[key]
	}
	obj, err := a.Store.Read(ctx, storage.LoginLockoutPath(key))
	if err != nil {
		// Fail open on a storage read error (not-found or otherwise), the
		// same posture Denylist.IsRevoked takes: an unreachable backing
		// store degrades the lockout to per-process-equivalent behavior
		// rather than locking every admin out.
		return lockoutRecord{}
	}
	var rec lockoutRecord
	_ = json.Unmarshal(obj.Data, &rec)
	return rec
}

func (a *AdminAuthenticator) recordFailure(ctx context.Context, key string) {
	if a.Store == nil {
		a.mu.Lock()
		r := a.local[key]
		r.Failures++
		r.LastFail = time.Now()
		a.local[key] = r
		a.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	// WriteWithRetry's read-modify-CAS-write loop is what makes this safe
	// under concurrent replicas: two replicas recording a failure for the
	// same key at once serialize through the object generation precondition
	// instead of one silently clobbering the other's increment.
	_ = storage.WriteWithRetry(ctx, a.Store, storage.LoginLockoutPath(key), func(existing []byte, _ int64) ([]byte, error) {
		var rec lockoutRecord
		if len(existing) > 0 {
			_ = json.Unmarshal(existing, &rec)
		}
		if rec.Failures >= loginLockoutMaxFailures && now.Sub(rec.LastFail) >= loginLockoutWindow {
			rec = lockoutRecord{} // previous window elapsed: count fresh from this failure
		}
		rec.Failures++
		rec.LastFail = now
		return json.Marshal(rec)
	}, 5)
}

func (a *AdminAuthenticator) reset(ctx context.Context, key string) {
	if a.Store == nil {
		a.mu.Lock()
		delete(a.local, key)
		a.mu.Unlock()
		return
	}
	_ = a.Store.Delete(ctx, storage.LoginLockoutPath(key))
}
