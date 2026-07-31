package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const (
	SessionCookieName = "mp_operator_session"
	OAuthStateCookie  = "mp_oauth_state"
	XSRFCookieName    = "XSRF-TOKEN"
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

type SessionManager struct {
	secret []byte
	issuer string
	ttl    time.Duration
	mu     sync.RWMutex
	revoked map[string]time.Time
}

func NewSessionManager(secret, issuer string, ttlSeconds int) *SessionManager {
	return &SessionManager{
		secret:  []byte(secret),
		issuer:  issuer,
		ttl:     time.Duration(ttlSeconds) * time.Second,
		revoked: map[string]time.Time{},
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
	jti := parsed.JwtID()
	m.mu.RLock()
	if _, revoked := m.revoked[jti]; revoked {
		m.mu.RUnlock()
		return nil, ErrUnauthorized
	}
	m.mu.RUnlock()
	sub := parsed.Subject()
	exp := parsed.Expiration()
	return &SessionClaims{Subject: sub, JTI: jti, Exp: exp}, nil
}

func (m *SessionManager) Revoke(jti string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[jti] = time.Now().UTC()
}

func (m *SessionManager) TTLSeconds() int {
	sec := int(m.ttl.Seconds())
	if sec > 3600 {
		return 3600
	}
	return sec
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
	if cookieToken == "" || headerToken == "" || cookieToken != headerToken {
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

func BuildXSRFCookie(token string, secure bool) string {
	c := fmt.Sprintf("%s=%s; Path=/; SameSite=Lax", XSRFCookieName, token)
	if secure {
		c += "; Secure"
	}
	return c
}
