package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

// Each case starts from a token Verify accepts and breaks one claim, so a
// failure points at the one check that let it through.
func TestPublishOIDCVerifierRejects(t *testing.T) {
	key := newTestRSAKey(t, "github-key")
	pub, err := key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		t.Fatal(err)
	}
	newVerifier := func() *PublishOIDCVerifier {
		return &PublishOIDCVerifier{
			Audience:       "marketplace",
			AllowedSources: map[string]string{"reportportal/plugin-jira": "plugin-jira"},
			KeySet:         set,
		}
	}
	valid := func() *jwt.Builder {
		return jwt.NewBuilder().
			Issuer(GitHubActionsIssuer).
			Subject("repo:reportportal/plugin-jira:ref:refs/heads/main").
			Audience([]string{"marketplace"}).
			Claim("repository", "reportportal/plugin-jira").
			IssuedAt(time.Now()).
			Expiration(time.Now().Add(time.Hour))
	}

	if _, _, err := newVerifier().Verify(context.Background(), signOIDC(t, key, valid())); err != nil {
		t.Fatalf("baseline token must verify: %v", err)
	}

	otherKey := newTestRSAKey(t, "other-key")
	impostor := newTestRSAKey(t, "github-key") // same kid, different key
	noKid := newTestRSAKey(t, "")
	if err := noKid.Remove(jwk.KeyIDKey); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		verifier func(*PublishOIDCVerifier)
		token    string
	}{
		{name: "other audience",
			token: signOIDC(t, key, valid().Audience([]string{"someone-else"}))},
		{name: "no audience",
			token: signOIDC(t, key, valid().Audience(nil))},
		{name: "verifier without an audience accepts nothing",
			verifier: func(v *PublishOIDCVerifier) { v.Audience = "" },
			token:    signOIDC(t, key, valid().Audience([]string{""}))},
		{name: "expired",
			token: signOIDC(t, key, valid().
				IssuedAt(time.Now().Add(-2*time.Hour)).
				Expiration(time.Now().Add(-time.Hour)))},
		{name: "not yet valid",
			token: signOIDC(t, key, valid().NotBefore(time.Now().Add(time.Hour)))},
		{name: "unknown kid",
			token: signOIDC(t, otherKey, valid())},
		{name: "known kid, different key",
			token: signOIDC(t, impostor, valid())},
		{name: "no kid",
			token: signOIDC(t, noKid, valid())},
		{name: "unknown issuer",
			token: signOIDC(t, key, valid().Issuer("https://token.actions.example.com"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVerifier()
			if tc.verifier != nil {
				tc.verifier(v)
			}
			_, _, err := v.Verify(context.Background(), tc.token)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("expected ErrUnauthorized, got %v", err)
			}
		})
	}
}

func TestSessionManagerVerifyRejects(t *testing.T) {
	const secret = "test-secret-key-32bytes-long!!"
	m := NewSessionManager(secret, "issuer", 3600, NewDenylist(nil))
	valid := func() *jwt.Builder {
		return jwt.NewBuilder().
			Issuer("issuer").
			Subject("admin").
			JwtID("jti-1").
			IssuedAt(time.Now()).
			Expiration(time.Now().Add(time.Hour)).
			Claim(sessionTypClaim, sessionTypValue)
	}
	sign := func(t *testing.T, b *jwt.Builder, key string) string {
		t.Helper()
		tok, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		signed, err := jwt.Sign(tok, jwt.WithKey(jwa.HS256(), []byte(key)))
		if err != nil {
			t.Fatal(err)
		}
		return string(signed)
	}

	if _, err := m.Verify(context.Background(), sign(t, valid(), secret)); err != nil {
		t.Fatalf("baseline token must verify: %v", err)
	}

	cases := []struct {
		name  string
		token string
	}{
		{"other issuer", sign(t, valid().Issuer("someone-else"), secret)},
		{"no issuer", sign(t, jwt.NewBuilder().Subject("admin").JwtID("jti-1").
			Expiration(time.Now().Add(time.Hour)).Claim(sessionTypClaim, sessionTypValue), secret)},
		{"other typ", sign(t, valid().Claim(sessionTypClaim, "oauth-state"), secret)},
		{"no typ", sign(t, jwt.NewBuilder().Issuer("issuer").Subject("admin").JwtID("jti-1").
			Expiration(time.Now().Add(time.Hour)), secret)},
		{"expired", sign(t, valid().IssuedAt(time.Now().Add(-2*time.Hour)).
			Expiration(time.Now().Add(-time.Hour)), secret)},
		{"other secret", sign(t, valid(), "another-secret-key-32bytes-long!")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.Verify(context.Background(), tc.token); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("expected ErrUnauthorized, got %v", err)
			}
		})
	}
}
