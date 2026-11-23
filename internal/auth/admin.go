package auth

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ErrTooManyAttempts = errors.New("too many attempts")

const (
	adminMaxFailures = 5
	adminLockout     = 15 * time.Minute
	// adminPruneEvery is how often failure records older than adminLockout
	// are dropped, so the records do not grow without bound.
	adminPruneEvery = time.Minute
)

type AdminAuthenticator struct {
	Enabled      bool
	Username     string
	PasswordHash string
	mu           sync.Mutex
	failures     map[string]int
	lastFail     map[string]time.Time
	lastPrune    time.Time
	now          func() time.Time // tests
}

func NewAdminAuthenticator(enabled bool, username, passwordHash string) *AdminAuthenticator {
	return &AdminAuthenticator{
		Enabled:      enabled,
		Username:     username,
		PasswordHash: passwordHash,
		failures:     map[string]int{},
		lastFail:     map[string]time.Time{},
		now:          time.Now,
	}
}

func (a *AdminAuthenticator) Configured() bool {
	return a.Enabled && a.PasswordHash != ""
}

func (a *AdminAuthenticator) Authenticate(clientKey, username, password string) error {
	if !a.Configured() {
		return ErrForbidden
	}
	// Unknown usernames share one record per client, so varying the username
	// neither resets the limit nor adds records.
	bucket := ""
	if username == a.Username {
		bucket = username
	}
	key := clientKey + "|" + bucket
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneLocked(now)
	if a.failures[key] >= adminMaxFailures {
		if now.Sub(a.lastFail[key]) < adminLockout {
			return ErrTooManyAttempts
		}
		a.failures[key] = 0
	}
	if username != a.Username {
		a.recordFailure(key, now)
		return ErrUnauthorized
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)); err != nil {
		a.recordFailure(key, now)
		return ErrUnauthorized
	}
	delete(a.failures, key)
	delete(a.lastFail, key)
	return nil
}

func (a *AdminAuthenticator) recordFailure(key string, now time.Time) {
	a.failures[key]++
	a.lastFail[key] = now
}

// pruneLocked drops records whose last failure is older than adminLockout:
// they would no longer block anything.
func (a *AdminAuthenticator) pruneLocked(now time.Time) {
	if now.Sub(a.lastPrune) < adminPruneEvery {
		return
	}
	a.lastPrune = now
	for key, at := range a.lastFail {
		if now.Sub(at) >= adminLockout {
			delete(a.failures, key)
			delete(a.lastFail, key)
		}
	}
}
