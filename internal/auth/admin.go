package auth

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ErrTooManyAttempts = errors.New("too many attempts")

type AdminAuthenticator struct {
	Enabled      bool
	Username     string
	PasswordHash string
	mu           sync.Mutex
	failures     map[string]int
	lastFail     map[string]time.Time
}

func NewAdminAuthenticator(enabled bool, username, passwordHash string) *AdminAuthenticator {
	return &AdminAuthenticator{
		Enabled:      enabled,
		Username:     username,
		PasswordHash: passwordHash,
		failures:     map[string]int{},
		lastFail:     map[string]time.Time{},
	}
}

func (a *AdminAuthenticator) Configured() bool {
	return a.Enabled && a.PasswordHash != ""
}

func (a *AdminAuthenticator) Authenticate(clientKey, username, password string) error {
	if !a.Configured() {
		return ErrForbidden
	}
	key := clientKey + "|" + username
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failures[key] >= 5 {
		if time.Since(a.lastFail[key]) < 15*time.Minute {
			return ErrTooManyAttempts
		}
		a.failures[key] = 0
	}
	if username != a.Username {
		a.recordFailure(key)
		return ErrUnauthorized
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)); err != nil {
		a.recordFailure(key)
		return ErrUnauthorized
	}
	a.failures[key] = 0
	return nil
}

func (a *AdminAuthenticator) recordFailure(key string) {
	a.failures[key]++
	a.lastFail[key] = time.Now()
}
