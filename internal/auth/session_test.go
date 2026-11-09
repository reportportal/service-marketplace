package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

func TestSessionManagerIssueVerify(t *testing.T) {
	m := NewSessionManager("test-secret-key-32bytes-long!!", "issuer", 3600, NewDenylist(nil))
	ctx := context.Background()
	token, exp, err := m.Issue(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if exp.Before(time.Now()) {
		t.Fatal("exp should be in future")
	}
	claims, err := m.Verify(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "admin" {
		t.Fatalf("unexpected subject %s", claims.Subject)
	}
	m.Revoke(ctx, claims.JTI, claims.Exp)
	if _, err := m.Verify(ctx, token); err != ErrUnauthorized {
		t.Fatalf("expected unauthorized after revoke, got %v", err)
	}
}

func TestDenylistIsRevokedFailsClosedOnStoreError(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "http://cdn.test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	faulty := storagetest.Wrap(store)
	faulty.Fail(storagetest.OpRead, storagetest.AnyKey, storage.ErrUnavailable)

	d := NewDenylist(faulty)
	if !d.IsRevoked(context.Background(), "any-jti") {
		t.Fatal("store read error must be treated as revoked")
	}

	// Not-found remains not-revoked.
	d2 := NewDenylist(store)
	if d2.IsRevoked(context.Background(), "missing-jti") {
		t.Fatal("missing denylist entry must not be treated as revoked")
	}
}

func TestValidateCSRF(t *testing.T) {
	if err := ValidateCSRF("abc", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCSRF("abc", "def"); err != ErrInvalidCSRF {
		t.Fatalf("expected csrf error, got %v", err)
	}
}

func TestPublishOIDCVerifier(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key, err := jwk.FromRaw(priv)
	if err != nil {
		t.Fatal(err)
	}
	_ = key.Set(jwk.KeyIDKey, "test-key")
	_ = key.Set(jwk.AlgorithmKey, jwa.RS256)
	pub, err := key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	set := jwk.NewSet()
	_ = set.AddKey(pub)

	v := &PublishOIDCVerifier{
		Audience: "marketplace",
		AllowedSources: map[string]string{
			"reportportal/plugin-jira": "plugin-jira",
		},
		KeySet: set,
	}
	tok, err := jwt.NewBuilder().
		Issuer("https://token.actions.githubusercontent.com").
		Subject("repo:reportportal/plugin-jira:ref:refs/heads/main").
		Audience([]string{"marketplace"}).
		Claim("repository", "reportportal/plugin-jira").
		Expiration(time.Now().Add(time.Hour)).
		IssuedAt(time.Now()).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, key))
	if err != nil {
		t.Fatal(err)
	}
	_, pid, err := v.Verify(context.Background(), string(signed))
	if err != nil {
		t.Fatal(err)
	}
	if pid != "plugin-jira" {
		t.Fatalf("unexpected plugin id %s", pid)
	}
}

func TestPublishOIDCVerifierGitLab(t *testing.T) {
	githubKey := newTestRSAKey(t, "github-key")
	gitlabKey := newTestRSAKey(t, "gitlab-key")
	githubSet := jwk.NewSet()
	gitlabSet := jwk.NewSet()
	githubPub, err := githubKey.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	gitlabPub, err := gitlabKey.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	_ = githubSet.AddKey(githubPub)
	_ = gitlabSet.AddKey(gitlabPub)

	v := &PublishOIDCVerifier{
		Audience: "marketplace",
		AllowedSources: map[string]string{
			"reportportal/plugin-jira":    "plugin-jira",
			"EPM-RPP/plugin-billing-epam": "billing",
		},
		KeySet:       githubSet,
		GitLabIssuer: "https://git.epam.com",
		GitLabKeySet: gitlabSet,
	}

	t.Run("github still publishes", func(t *testing.T) {
		tok := signOIDC(t, githubKey, jwt.NewBuilder().
			Issuer(GitHubActionsIssuer).
			Subject("repo:reportportal/plugin-jira:ref:refs/heads/main").
			Audience([]string{"marketplace"}).
			Claim("repository", "reportportal/plugin-jira").
			Expiration(time.Now().Add(time.Hour)).
			IssuedAt(time.Now()))
		_, pid, err := v.Verify(context.Background(), tok)
		if err != nil {
			t.Fatal(err)
		}
		if pid != "plugin-jira" {
			t.Fatalf("plugin id %s", pid)
		}
	})

	t.Run("gitlab project_path publishes billing", func(t *testing.T) {
		tok := signOIDC(t, gitlabKey, jwt.NewBuilder().
			Issuer("https://git.epam.com").
			Subject("project_path:EPM-RPP/plugin-billing-epam:ref_type:branch:ref:main").
			Audience([]string{"marketplace"}).
			Claim("project_path", "EPM-RPP/plugin-billing-epam").
			Expiration(time.Now().Add(time.Hour)).
			IssuedAt(time.Now()))
		_, pid, err := v.Verify(context.Background(), tok)
		if err != nil {
			t.Fatal(err)
		}
		if pid != "billing" {
			t.Fatalf("plugin id %s", pid)
		}
	})

	t.Run("gitlab subject fallback", func(t *testing.T) {
		tok := signOIDC(t, gitlabKey, jwt.NewBuilder().
			Issuer("https://git.epam.com").
			Subject("project_path:EPM-RPP/plugin-billing-epam:ref_type:branch:ref:main").
			Audience([]string{"marketplace"}).
			Expiration(time.Now().Add(time.Hour)).
			IssuedAt(time.Now()))
		_, pid, err := v.Verify(context.Background(), tok)
		if err != nil {
			t.Fatal(err)
		}
		if pid != "billing" {
			t.Fatalf("plugin id %s", pid)
		}
	})

	t.Run("unknown gitlab project is forbidden", func(t *testing.T) {
		tok := signOIDC(t, gitlabKey, jwt.NewBuilder().
			Issuer("https://git.epam.com").
			Subject("project_path:EPM-RPP/other:ref_type:branch:ref:main").
			Audience([]string{"marketplace"}).
			Claim("project_path", "EPM-RPP/other").
			Expiration(time.Now().Add(time.Hour)).
			IssuedAt(time.Now()))
		_, _, err := v.Verify(context.Background(), tok)
		if err != ErrForbidden {
			t.Fatalf("got %v, want forbidden", err)
		}
	})

	t.Run("gitlab token signed with github key is unauthorized", func(t *testing.T) {
		tok := signOIDC(t, githubKey, jwt.NewBuilder().
			Issuer("https://git.epam.com").
			Subject("project_path:EPM-RPP/plugin-billing-epam:ref_type:branch:ref:main").
			Audience([]string{"marketplace"}).
			Claim("project_path", "EPM-RPP/plugin-billing-epam").
			Expiration(time.Now().Add(time.Hour)).
			IssuedAt(time.Now()))
		_, _, err := v.Verify(context.Background(), tok)
		if err != ErrUnauthorized {
			t.Fatalf("got %v, want unauthorized", err)
		}
	})

	t.Run("gitlab disabled rejects gitlab issuer", func(t *testing.T) {
		githubOnly := &PublishOIDCVerifier{
			Audience:       "marketplace",
			AllowedSources: v.AllowedSources,
			KeySet:         githubSet,
		}
		tok := signOIDC(t, gitlabKey, jwt.NewBuilder().
			Issuer("https://git.epam.com").
			Subject("project_path:EPM-RPP/plugin-billing-epam:ref_type:branch:ref:main").
			Audience([]string{"marketplace"}).
			Claim("project_path", "EPM-RPP/plugin-billing-epam").
			Expiration(time.Now().Add(time.Hour)).
			IssuedAt(time.Now()))
		_, _, err := githubOnly.Verify(context.Background(), tok)
		if err != ErrUnauthorized {
			t.Fatalf("got %v, want unauthorized", err)
		}
	})
}

func newTestRSAKey(t *testing.T, kid string) jwk.Key {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key, err := jwk.FromRaw(priv)
	if err != nil {
		t.Fatal(err)
	}
	_ = key.Set(jwk.KeyIDKey, kid)
	_ = key.Set(jwk.AlgorithmKey, jwa.RS256)
	return key
}

func signOIDC(t *testing.T, key jwk.Key, b *jwt.Builder) string {
	t.Helper()
	tok, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, key))
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}
