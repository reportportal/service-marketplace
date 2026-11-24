package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jwx-go/jwkfetch/v4"
	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/httprc/v3/errsink"
	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

var ErrGitHubUnavailable = errors.New("github oauth unavailable")

type GitHubOAuth struct {
	ClientID     string
	ClientSecret string
	Org          string
	AllowedTeam  string
	RedirectURL  string
	HTTPClient   *http.Client
	Sessions     *SessionManager
	States       *OAuthStateStore
}

func (g *GitHubOAuth) Enabled() bool {
	return g.ClientID != "" && g.ClientSecret != "" && g.Org != ""
}

func (g *GitHubOAuth) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", g.ClientID)
	q.Set("redirect_uri", g.RedirectURL)
	q.Set("scope", "read:org")
	q.Set("state", state)
	return "https://github.com/login/oauth/authorize?" + q.Encode()
}

func (g *GitHubOAuth) IssueState(ctx context.Context) (string, error) {
	if g.States == nil {
		g.States = NewOAuthStateStore()
	}
	return g.States.Issue()
}

func (g *GitHubOAuth) ConsumeState(state string) bool {
	if g.States == nil {
		return false
	}
	return g.States.Consume(state)
}

func (g *GitHubOAuth) Exchange(ctx context.Context, code string) (string, error) {
	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	body := url.Values{}
	body.Set("client_id", g.ClientID)
	body.Set("client_secret", g.ClientSecret)
	body.Set("code", code)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("github oauth: %s", out.Error)
	}
	return out.AccessToken, nil
}

func (g *GitHubOAuth) VerifyMembership(ctx context.Context, accessToken string) (string, error) {
	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/user/memberships/orgs/%s", g.Org), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return "", ErrForbidden
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github membership: %s", string(b))
	}
	var membership struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&membership); err != nil {
		return "", err
	}
	if membership.State != "active" {
		return "", ErrForbidden
	}

	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	userReq.Header.Set("Authorization", "Bearer "+accessToken)
	userReq.Header.Set("Accept", "application/vnd.github+json")
	userResp, err := client.Do(userReq)
	if err != nil {
		return "", err
	}
	defer userResp.Body.Close()
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		return "", err
	}
	if g.AllowedTeam != "" {
		if err := g.checkTeam(ctx, client, accessToken, user.Login); err != nil {
			return "", err
		}
	}
	return user.Login, nil
}

func (g *GitHubOAuth) checkTeam(ctx context.Context, client *http.Client, accessToken, login string) error {
	url := fmt.Sprintf("https://api.github.com/orgs/%s/teams/%s/memberships/%s", g.Org, g.AllowedTeam, login)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrForbidden
	}
	var membership struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&membership); err != nil {
		return err
	}
	if membership.State != "active" {
		return ErrForbidden
	}
	return nil
}

func (g *GitHubOAuth) Callback(ctx context.Context, code, state string) (string, time.Time, error) {
	if !g.Enabled() {
		return "", time.Time{}, ErrGitHubUnavailable
	}
	if !g.ConsumeState(state) {
		return "", time.Time{}, ErrUnauthorized
	}
	token, err := g.Exchange(ctx, code)
	if err != nil {
		return "", time.Time{}, err
	}
	login, err := g.VerifyMembership(ctx, token)
	if err != nil {
		return "", time.Time{}, err
	}
	return g.Sessions.Issue(ctx, login)
}

const (
	// GitHubActionsIssuer is the only issuer accepted for GitHub Actions publish tokens.
	GitHubActionsIssuer = "https://token.actions.githubusercontent.com"
	gitHubActionsJWKS   = "https://token.actions.githubusercontent.com/.well-known/jwks"
	gitLabJWKSPath      = "/oauth/discovery/keys"
)

type oidcIssuerKind int

const (
	oidcIssuerGitHub oidcIssuerKind = iota
	oidcIssuerGitLab
)

type PublishOIDCVerifier struct {
	Audience       string
	AllowedSources map[string]string
	// JWKSURL overrides the GitHub Actions JWKS endpoint. Empty uses the public default.
	JWKSURL string
	// KeySet, when set, verifies GitHub Actions tokens offline (tests).
	KeySet jwk.Set

	// GitLabIssuer enables GitLab CI OIDC when non-empty. A token is checked
	// against this issuer only after its signature matches GitLab's keys.
	// Empty leaves GitHub Actions as the only accepted issuer.
	GitLabIssuer string
	// GitLabJWKSURL overrides GitLab's key endpoint. Empty uses
	// <GitLabIssuer>/oauth/discovery/keys.
	GitLabJWKSURL string
	// GitLabKeySet, when set, verifies GitLab tokens offline (tests).
	GitLabKeySet jwk.Set

	cache    *jwkfetch.Cache
	cacheErr error
	once     sync.Once

	// refetchMu guards refetches: the refetch in flight per URL and when
	// the last one failed.
	refetchMu sync.Mutex
	refetches map[string]*jwksRefetchState
	// retryBackoff overrides jwksRetryBackoff when non-zero (tests).
	retryBackoff time.Duration
}

type jwksRefetchState struct {
	inflight *jwksRefetch
	failedAt time.Time
}

// jwksRefetch is one refetch of a key set; done closes when it finishes.
type jwksRefetch struct {
	done chan struct{}
	err  error
}

// jwksReadyTimeout bounds how long a request waits for a JWKS fetch.
const jwksReadyTimeout = 10 * time.Second

// jwksRetryBackoff is how long after a failed refetch requests fail at once
// instead of refetching. The cache's own retry after a failed first fetch
// comes only after the 15-minute minimum interval, so requests drive the
// retry, and this gap stops callers without a valid token from driving
// fetches faster.
const jwksRetryBackoff = 5 * time.Second

func (v *PublishOIDCVerifier) githubJWKS() string {
	if v.JWKSURL != "" {
		return v.JWKSURL
	}
	return gitHubActionsJWKS
}

func (v *PublishOIDCVerifier) gitLabJWKS() string {
	if v.GitLabJWKSURL != "" {
		return v.GitLabJWKSURL
	}
	return strings.TrimRight(v.GitLabIssuer, "/") + gitLabJWKSPath
}

func (v *PublishOIDCVerifier) ensureCache() error {
	v.once.Do(func() {
		client := httprc.NewClient(httprc.WithErrorSink(errsink.NewFunc(
			func(_ context.Context, err error) {
				log.Printf("WARNING publish oidc: jwks fetch: %v", err)
			})))
		// jwkfetch's default transport ignores HTTP(S)_PROXY; use the
		// standard one, as the other outbound clients here do.
		transport := http.DefaultTransport
		if t, ok := transport.(*http.Transport); ok {
			transport = t.Clone()
		}
		httpClient := jwkfetch.WrapHTTPClientDefaults(&http.Client{Transport: transport})
		cache, err := jwkfetch.NewCache(context.Background(), client,
			jwkfetch.WithHTTPClient(httpClient))
		if err != nil {
			v.cacheErr = err
			return
		}
		// WithWaitReady(false): registering must not block or fail on a cold
		// or unreachable JWKS endpoint. cachedKeySet fetches under the
		// request context instead.
		urls := []string{v.githubJWKS()}
		if v.GitLabIssuer != "" && v.GitLabKeySet == nil {
			urls = append(urls, v.gitLabJWKS())
		}
		for _, u := range urls {
			if err := cache.Register(context.Background(), u,
				jwkfetch.WithMinInterval(15*time.Minute),
				jwkfetch.WithWaitReady(false),
			); err != nil {
				v.cacheErr = err
				return
			}
		}
		v.cache = cache
	})
	return v.cacheErr
}

// cachedKeySet returns the cached key set for url. While the set is not
// ready (the first fetch is pending or failed), the request starts a
// refetch or joins the one in flight, and waits for it under ctx. After a
// failed refetch, requests fail at once for jwksRetryBackoff. It fails
// closed: an unready set is an error.
func (v *PublishOIDCVerifier) cachedKeySet(ctx context.Context, url string) (jwk.Set, error) {
	if err := v.ensureCache(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if set, err := v.readyKeySet(ctx, url); set != nil || err != nil {
		return set, err
	}

	f, err := v.startRefetch(url)
	if err != nil {
		return nil, err
	}
	select {
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		if set, err := v.readyKeySet(ctx, url); set != nil || err != nil {
			return set, err
		}
		return nil, fmt.Errorf("jwks %s not ready", url)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readyKeySet returns the key set for url, or nil while it is not fetched.
func (v *PublishOIDCVerifier) readyKeySet(ctx context.Context, url string) (jwk.Set, error) {
	r, err := v.cache.LookupResource(ctx, url)
	if err != nil {
		return nil, err
	}
	return r.Resource(), nil
}

// startRefetch returns the refetch in flight for url, starting one if none
// is running and the last one did not fail within the backoff.
func (v *PublishOIDCVerifier) startRefetch(url string) (*jwksRefetch, error) {
	v.refetchMu.Lock()
	defer v.refetchMu.Unlock()
	if v.refetches == nil {
		v.refetches = map[string]*jwksRefetchState{}
	}
	st := v.refetches[url]
	if st == nil {
		st = &jwksRefetchState{}
		v.refetches[url] = st
	}
	if st.inflight != nil {
		return st.inflight, nil
	}
	backoff := v.retryBackoff
	if backoff == 0 {
		backoff = jwksRetryBackoff
	}
	if time.Since(st.failedAt) < backoff {
		return nil, fmt.Errorf("jwks %s not ready", url)
	}
	f := &jwksRefetch{done: make(chan struct{})}
	st.inflight = f
	go v.refetch(url, st, f)
	return f, nil
}

// refetch runs detached from any request, so a caller that gives up does
// not cancel it for the others waiting on it.
func (v *PublishOIDCVerifier) refetch(url string, st *jwksRefetchState, f *jwksRefetch) {
	ctx, cancel := context.WithTimeout(context.Background(), jwksReadyTimeout)
	defer cancel()
	_, err := v.cache.Refresh(ctx, url)
	if err != nil {
		log.Printf("WARNING publish oidc: jwks refetch: %v", err)
	}

	v.refetchMu.Lock()
	st.inflight = nil
	if err != nil {
		st.failedAt = time.Now()
	}
	v.refetchMu.Unlock()
	f.err = err
	close(f.done)
}

// keySetForIssuer picks the key set from the unverified iss claim. The
// signature is checked against that set afterwards, so a forged iss only
// selects a key the caller still has to satisfy.
func (v *PublishOIDCVerifier) keySetForIssuer(ctx context.Context, iss string) (jwk.Set, oidcIssuerKind, error) {
	switch {
	case iss == GitHubActionsIssuer:
		if v.KeySet != nil {
			return v.KeySet, oidcIssuerGitHub, nil
		}
		set, err := v.cachedKeySet(ctx, v.githubJWKS())
		if err != nil {
			return nil, 0, err
		}
		return set, oidcIssuerGitHub, nil
	case v.GitLabIssuer != "" && iss == v.GitLabIssuer:
		if v.GitLabKeySet != nil {
			return v.GitLabKeySet, oidcIssuerGitLab, nil
		}
		set, err := v.cachedKeySet(ctx, v.gitLabJWKS())
		if err != nil {
			return nil, 0, err
		}
		return set, oidcIssuerGitLab, nil
	default:
		return nil, 0, ErrUnauthorized
	}
}

func (v *PublishOIDCVerifier) Verify(ctx context.Context, token string) (subject, pluginID string, err error) {
	unverified, err := jwt.ParseInsecure([]byte(token))
	if err != nil {
		return "", "", ErrUnauthorized
	}
	iss, _ := unverified.Issuer()
	set, kind, err := v.keySetForIssuer(ctx, iss)
	if err != nil {
		return "", "", ErrUnauthorized
	}
	parsed, err := jwt.Parse([]byte(token),
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
		jwt.WithIssuer(iss),
	)
	if err != nil {
		return "", "", ErrUnauthorized
	}
	if v.Audience == "" {
		return "", "", ErrUnauthorized
	}
	aud, _ := parsed.Audience()
	okAud := false
	for _, a := range aud {
		if a == v.Audience {
			okAud = true
			break
		}
	}
	if !okAud {
		return "", "", ErrUnauthorized
	}
	source := sourceID(kind, parsed)
	if source == "" {
		return "", "", ErrForbidden
	}
	pid, ok := v.AllowedSources[source]
	if !ok {
		return "", "", ErrForbidden
	}
	sub, _ := parsed.Subject()
	return sub, pid, nil
}

func sourceID(kind oidcIssuerKind, parsed jwt.Token) string {
	switch kind {
	case oidcIssuerGitHub:
		repoClaim, _ := parsed.Field("repository")
		repo, _ := repoClaim.(string)
		if repo == "" {
			sub, _ := parsed.Subject()
			repo = extractRepo(sub)
		}
		return repo
	case oidcIssuerGitLab:
		pathClaim, _ := parsed.Field("project_path")
		path, _ := pathClaim.(string)
		if path == "" {
			sub, _ := parsed.Subject()
			path = extractGitLabProject(sub)
		}
		return path
	default:
		return ""
	}
}

func extractRepo(sub string) string {
	// repo:org/name:ref:refs/heads/main
	parts := strings.Split(sub, ":")
	if len(parts) >= 2 && parts[0] == "repo" {
		return parts[1]
	}
	return ""
}

func extractGitLabProject(sub string) string {
	// project_path:group/project:ref_type:branch:ref:main
	parts := strings.Split(sub, ":")
	if len(parts) >= 2 && parts[0] == "project_path" {
		return parts[1]
	}
	return ""
}

type LicenseClaims struct {
	CustomerID string
	PluginID   string
	Exp        time.Time
}

// PeekUnverifiedCustomerID reads the customerId claim WITHOUT verifying the
// signature, for one purpose only: choosing which entitlement's public keys
// VerifyLicenseJWT should be given as candidates. Nothing may be authorized on the
// strength of the value it returns — a forged claim only ever buys the attacker a
// candidate key set their signature still has to satisfy. Validation (exp) is
// deliberately skipped here so an elapsed token is reported by VerifyLicenseJWT,
// which checks it against a real signature, rather than by this lookup.
func PeekUnverifiedCustomerID(token string) (string, error) {
	parsed, err := jwt.ParseInsecure([]byte(token))
	if err != nil {
		return "", ErrUnauthorized
	}
	cid, _ := parsed.Field("customerId")
	customerID, _ := cid.(string)
	if customerID == "" {
		return "", ErrUnauthorized
	}
	return customerID, nil
}

func VerifyLicenseJWT(token string, publicKeys []string) (*LicenseClaims, error) {
	var lastErr error
	for _, pkB64 := range publicKeys {
		raw, err := base64.StdEncoding.DecodeString(pkB64)
		if err != nil {
			lastErr = err
			continue
		}
		if len(raw) != ed25519.PublicKeySize {
			lastErr = ErrUnauthorized
			continue
		}
		pub := ed25519.PublicKey(raw)
		key, err := jwt.Parse([]byte(token), jwt.WithKey(jwa.EdDSA(), pub))
		if err != nil {
			lastErr = err
			continue
		}
		cid, _ := key.Field("customerId")
		pid, _ := key.Field("pluginId")
		exp, _ := key.Expiration()
		customerID, _ := cid.(string)
		pluginID, _ := pid.(string)
		if customerID == "" || pluginID == "" {
			return nil, ErrUnauthorized
		}
		return &LicenseClaims{CustomerID: customerID, PluginID: pluginID, Exp: exp}, nil
	}
	if lastErr != nil {
		return nil, ErrUnauthorized
	}
	return nil, ErrUnauthorized
}
