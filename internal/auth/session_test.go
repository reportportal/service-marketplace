package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
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
	key, err := jwk.Import[jwk.Key](priv)
	if err != nil {
		t.Fatal(err)
	}
	_ = key.Set(jwk.KeyIDKey, "test-key")
	_ = key.Set(jwk.AlgorithmKey, jwa.RS256())
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
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), key))
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

// TestPublishOIDCVerifierRemoteJWKS covers the cached JWKS fetch path that the
// offline KeySet tests skip.
func TestPublishOIDCVerifierRemoteJWKS(t *testing.T) {
	key := newTestRSAKey(t, "remote-key")
	pub, err := key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	set := jwk.NewSet()
	_ = set.AddKey(pub)
	body, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	claims := func() *jwt.Builder {
		return jwt.NewBuilder().
			Issuer(GitHubActionsIssuer).
			Subject("repo:reportportal/plugin-jira:ref:refs/heads/main").
			Audience([]string{"marketplace"}).
			Claim("repository", "reportportal/plugin-jira").
			Expiration(time.Now().Add(time.Hour)).
			IssuedAt(time.Now())
	}
	newVerifier := func(url string) *PublishOIDCVerifier {
		return &PublishOIDCVerifier{
			Audience:       "marketplace",
			AllowedSources: map[string]string{"reportportal/plugin-jira": "plugin-jira"},
			JWKSURL:        url,
		}
	}

	t.Run("verifies against fetched keys", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
		defer srv.Close()
		v := newVerifier(srv.URL)
		for range 5 {
			_, pid, err := v.Verify(context.Background(), signOIDC(t, key, claims()))
			if err != nil {
				t.Fatal(err)
			}
			if pid != "plugin-jira" {
				t.Fatalf("plugin id %q", pid)
			}
		}
		// a ready set is served from the cache, not refetched per request
		if n := hits.Load(); n > 2 {
			t.Fatalf("upstream hits %d, want at most 2", n)
		}
	})

	// the refetch is not tied to the request that started it
	t.Run("a request that gives up does not cancel the refetch for others", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write(body)
		}))
		defer srv.Close()
		v := newVerifier(srv.URL)
		token := signOIDC(t, key, claims())

		var wg sync.WaitGroup
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, _, _ = v.Verify(ctx, token)
		})
		time.Sleep(50 * time.Millisecond)
		var err error
		wg.Go(func() {
			_, _, err = v.Verify(context.Background(), token)
		})
		wg.Wait()
		if err != nil {
			t.Fatalf("second request: %v", err)
		}
	})

	t.Run("unreachable JWKS fails closed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		}))
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, _, err := newVerifier(srv.URL).Verify(ctx, signOIDC(t, key, claims()))
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
	})

	// The cache's own retry after a failed first fetch waits 15 minutes, so
	// the next request has to refetch.
	t.Run("recovers on the next request after a failed first fetch", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if hits.Add(1) == 1 {
				http.Error(w, "down", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(body)
		}))
		defer srv.Close()
		v := newVerifier(srv.URL)
		v.retryBackoff = time.Nanosecond
		token := signOIDC(t, key, claims())

		// the first call may or may not see the failed fetch, depending on
		// whether the background fetch or the request's refetch goes first
		_, _, _ = v.Verify(context.Background(), token)
		_, pid, err := v.Verify(context.Background(), token)
		if err != nil {
			t.Fatalf("second verify: %v (upstream hits %d)", err, hits.Load())
		}
		if pid != "plugin-jira" {
			t.Fatalf("plugin id %q", pid)
		}
	})

	t.Run("refetches of an unready set are rate-limited", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			http.Error(w, "down", http.StatusInternalServerError)
		}))
		defer srv.Close()
		v := newVerifier(srv.URL)
		token := signOIDC(t, key, claims())

		for i := range 5 {
			start := time.Now()
			_, _, err := v.Verify(context.Background(), token)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("call %d: expected ErrUnauthorized, got %v", i, err)
			}
			if i > 0 && time.Since(start) > time.Second {
				t.Fatalf("call %d took %v; inside the backoff it should fail at once", i, time.Since(start))
			}
		}
		// the background first fetch plus one request-driven refetch
		if n := hits.Load(); n > 2 {
			t.Fatalf("upstream hits %d, want at most 2", n)
		}
	})

	// requests that arrive during a refetch share it rather than queueing
	// a fetch each
	t.Run("concurrent requests share one refetch", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write(body)
		}))
		defer srv.Close()
		v := newVerifier(srv.URL)
		token := signOIDC(t, key, claims())

		var wg sync.WaitGroup
		errs := make([]error, 5)
		for i := range errs {
			wg.Go(func() {
				time.Sleep(time.Duration(i) * 40 * time.Millisecond)
				_, _, errs[i] = v.Verify(context.Background(), token)
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		// the background first fetch plus one shared refetch
		if n := hits.Load(); n > 2 {
			t.Fatalf("upstream hits %d, want at most 2", n)
		}
	})

	// a hung endpoint must not hold a request past its own deadline, even
	// while an earlier request with a longer deadline is still waiting
	t.Run("a hung endpoint does not hold requests past their deadline", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)
		v := newVerifier(srv.URL)
		token := signOIDC(t, key, claims())

		var wg sync.WaitGroup
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _, _ = v.Verify(ctx, token)
		})
		time.Sleep(50 * time.Millisecond)
		took := make([]time.Duration, 3)
		errs := make([]error, 3)
		for i := range took {
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				start := time.Now()
				_, _, errs[i] = v.Verify(ctx, token)
				took[i] = time.Since(start)
			})
		}
		wg.Wait()
		for i := range took {
			// took is read after wg.Wait, which also waits for the 2s caller
			if !errors.Is(errs[i], ErrUnauthorized) {
				t.Fatalf("call %d: expected ErrUnauthorized, got %v", i, errs[i])
			}
			if took[i] > time.Second {
				t.Fatalf("call %d took %v with a 200ms deadline", i, took[i])
			}
		}
	})

	t.Run("token signed with a different key is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		}))
		defer srv.Close()
		other := newTestRSAKey(t, "remote-key") // same kid, different key material
		_, _, err := newVerifier(srv.URL).Verify(context.Background(), signOIDC(t, other, claims()))
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
	})
}

func newTestRSAKey(t *testing.T, kid string) jwk.Key {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key, err := jwk.Import[jwk.Key](priv)
	if err != nil {
		t.Fatal(err)
	}
	_ = key.Set(jwk.KeyIDKey, kid)
	_ = key.Set(jwk.AlgorithmKey, jwa.RS256())
	return key
}

func signOIDC(t *testing.T, key jwk.Key, b *jwt.Builder) string {
	t.Helper()
	tok, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), key))
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}
