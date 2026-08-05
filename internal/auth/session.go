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
		// Fail closed: unknown store errors must not revive a revoked session.
		return !errors.Is(err, storage.ErrNotFound)
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

// OAuthStateStore holds one-time opaque OAuth CSRF states (not session
// JWTs), backed by the shared object store — the same pattern Denylist
// uses above — so a login started on one replica can be consumed by the
// callback landing on a different one behind a load balancer with no
// sticky sessions. Before this, states lived only in a per-process map:
// with N replicas, GET /auth/github/login on replica A stored the state
// only in A's memory, and the callback landing on replica B always failed
// with "Invalid OAuth state" (assessment finding
// F4-inmemory-state-not-shared-across-replicas). Issue/Consume are called
// once per login (human-interactive, low volume), so unlike Denylist this
// deliberately has no local hot cache — every call round-trips the store
// when one is configured, trading a small amount of latency for one code
// path that is obviously correct across any number of replicas.
type OAuthStateStore struct {
	Store storage.ObjectStore
	mu    sync.Mutex
	local map[string]time.Time // used only when Store is nil (tests / no backing store)
}

func NewOAuthStateStore(store storage.ObjectStore) *OAuthStateStore {
	return &OAuthStateStore{Store: store, local: map[string]time.Time{}}
}

type oauthStateRecord struct {
	Exp string `json:"exp"`
}

func (s *OAuthStateStore) Issue(ctx context.Context) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(10 * time.Minute)
	if s.Store == nil {
		s.mu.Lock()
		s.local[id] = exp
		s.mu.Unlock()
		return id, nil
	}
	payload, err := json.Marshal(oauthStateRecord{Exp: exp.UTC().Format(time.RFC3339)})
	if err != nil {
		return "", err
	}
	if _, err := s.Store.Write(ctx, storage.OAuthStatePath(id), payload, 0); err != nil {
		return "", err
	}
	return id, nil
}

// Consume reports whether state was a validly issued, unexpired, not
// previously consumed state, and consumes it (a state can never be reused
// even if this returns true a second time from a racing caller — see the
// commit message for the accepted race window).
func (s *OAuthStateStore) Consume(ctx context.Context, state string) bool {
	if state == "" {
		return false
	}
	if s.Store == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		exp, ok := s.local[state]
		if !ok {
			return false
		}
		delete(s.local, state)
		return time.Now().Before(exp)
	}
	obj, err := s.Store.Read(ctx, storage.OAuthStatePath(state))
	if err != nil {
		return false
	}
	_ = s.Store.Delete(ctx, storage.OAuthStatePath(state))
	var rec oauthStateRecord
	if err := json.Unmarshal(obj.Data, &rec); err != nil {
		return false
	}
	expAt, err := time.Parse(time.RFC3339, rec.Exp)
	if err != nil {
		return false
	}
	return time.Now().Before(expAt)
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
