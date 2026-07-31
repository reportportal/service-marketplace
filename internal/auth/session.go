package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/reportportal/service-marketplace/internal/storage"
)

const (
	SessionCookieName = "mp_operator_session"
	OAuthStateCookie  = "mp_oauth_state"
	XSRFCookieName    = "XSRF-TOKEN"
	sessionTypClaim   = "typ"
	sessionTypValue   = "session"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidCSRF  = errors.New("csrf token invalid")
)

type SessionClaims struct {
	Subject string
	JTI     string
	Exp     time.Time
}

// Denylist is a shared (object-store backed) session revocation list.
type Denylist struct {
	Store storage.ObjectStore
	mu    sync.Mutex
	local map[string]time.Time // hot cache
}

func NewDenylist(store storage.ObjectStore) *Denylist {
	return &Denylist{Store: store, local: map[string]time.Time{}}
}

func (d *Denylist) Revoke(ctx context.Context, jti string, exp time.Time) error {
	if jti == "" {
		return nil
	}
	d.mu.Lock()
	d.local[jti] = exp
	d.mu.Unlock()
	if d.Store == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{"revokedAt": time.Now().UTC().Format(time.RFC3339), "exp": exp.UTC().Format(time.RFC3339)})
	_, err := d.Store.Write(ctx, storage.SessionDenylistPath(jti), payload, 0)
	if errors.Is(err, storage.ErrConflict) {
		return nil
	}
	return err
}

func (d *Denylist) IsRevoked(ctx context.Context, jti string) bool {
	d.mu.Lock()
	if exp, ok := d.local[jti]; ok {
		d.mu.Unlock()
		return time.Now().Before(exp.Add(time.Minute))
	}
	d.mu.Unlock()
	if d.Store == nil {
		return false
	}
	obj, err := d.Store.Read(ctx, storage.SessionDenylistPath(jti))
	if err != nil {
		return false
	}
	var meta struct {
		Exp string `json:"exp"`
	}
	_ = json.Unmarshal(obj.Data, &meta)
	exp := time.Now().Add(time.Hour)
	if t, err := time.Parse(time.RFC3339, meta.Exp); err == nil {
		exp = t
	}
	d.mu.Lock()
	d.local[jti] = exp
	d.mu.Unlock()
	return true
}

type SessionManager struct {
	secret   []byte
	issuer   string
	ttl      time.Duration
	denylist *Denylist
}

func NewSessionManager(secret, issuer string, ttlSeconds int, denylist *Denylist) *SessionManager {
	return &SessionManager{
		secret:   []byte(secret),
		issuer:   issuer,
		ttl:      time.Duration(ttlSeconds) * time.Second,
		denylist: denylist,
	}
}

func (m *SessionManager) Issue(ctx context.Context, subject string) (string, time.Time, error) {
	jti, err := randomID()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().UTC().Add(m.ttl)
	if exp.Sub(time.Now()) > time.Hour {
		exp = time.Now().UTC().Add(time.Hour)
	}
	tok, err := jwt.NewBuilder().
		Issuer(m.issuer).
		Subject(subject).
		JwtID(jti).
		IssuedAt(time.Now().UTC()).
		Expiration(exp).
		Claim(sessionTypClaim, sessionTypValue).
		Build()
	if err != nil {
		return "", time.Time{}, err
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.HS256, m.secret))
	if err != nil {
		return "", time.Time{}, err
	}
	return string(signed), exp, nil
}

func (m *SessionManager) Verify(ctx context.Context, token string) (*SessionClaims, error) {
	parsed, err := jwt.Parse([]byte(token), jwt.WithKey(jwa.HS256, m.secret), jwt.WithIssuer(m.issuer))
	if err != nil {
		return nil, ErrUnauthorized
	}
	typ, _ := parsed.Get(sessionTypClaim)
	if typ != sessionTypValue {
		return nil, ErrUnauthorized
	}
	jti := parsed.JwtID()
	if m.denylist != nil && m.denylist.IsRevoked(ctx, jti) {
		return nil, ErrUnauthorized
	}
	sub := parsed.Subject()
	exp := parsed.Expiration()
	return &SessionClaims{Subject: sub, JTI: jti, Exp: exp}, nil
}

func (m *SessionManager) Revoke(ctx context.Context, jti string, exp time.Time) {
	if m.denylist != nil {
		_ = m.denylist.Revoke(ctx, jti, exp)
	}
}

func (m *SessionManager) TTLSeconds() int {
	sec := int(m.ttl.Seconds())
	if sec > 3600 {
		return 3600
	}
	return sec
}

// OAuthStateStore holds one-time opaque OAuth CSRF states (not session JWTs).
type OAuthStateStore struct {
	mu    sync.Mutex
	items map[string]time.Time
}

func NewOAuthStateStore() *OAuthStateStore {
	return &OAuthStateStore{items: map[string]time.Time{}}
}

func (s *OAuthStateStore) Issue() (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.items[id] = time.Now().Add(10 * time.Minute)
	s.mu.Unlock()
	return id, nil
}

func (s *OAuthStateStore) Consume(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.items[state]
	if !ok {
		return false
	}
	delete(s.items, state)
	return time.Now().Before(exp)
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func NewXSRFToken() (string, error) {
	return randomID()
}

func ValidateCSRF(cookieToken, headerToken string) error {
	if cookieToken == "" || headerToken == "" {
		return ErrInvalidCSRF
	}
	if len(cookieToken) != len(headerToken) {
		return ErrInvalidCSRF
	}
	if subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

func BuildSessionCookie(token string, maxAge int, secure bool) string {
	c := fmt.Sprintf("%s=%s; Path=/; HttpOnly; SameSite=Lax; Max-Age=%d", SessionCookieName, token, maxAge)
	if secure {
		c += "; Secure"
	}
	return c
}

func ClearSessionCookie(secure bool) string {
	c := SessionCookieName + "=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0"
	if secure {
		c += "; Secure"
	}
	return c
}

func BuildOAuthStateCookie(state string, secure bool) string {
	c := fmt.Sprintf("%s=%s; Path=/api/v1/auth/github; HttpOnly; SameSite=Lax; Max-Age=600", OAuthStateCookie, state)
	if secure {
		c += "; Secure"
	}
	return c
}

func BuildXSRFCookie(token string, secure bool) string {
	c := fmt.Sprintf("%s=%s; Path=/; SameSite=Lax", XSRFCookieName, token)
	if secure {
		c += "; Secure"
	}
	return c
}
