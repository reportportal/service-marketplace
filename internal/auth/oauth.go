package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
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

	cache *jwk.Cache
	once  sync.Once
}

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

func (v *PublishOIDCVerifier) ensureCache() {
	v.once.Do(func() {
		v.cache = jwk.NewCache(context.Background())
		_ = v.cache.Register(v.githubJWKS(), jwk.WithMinRefreshInterval(15*time.Minute))
		if v.GitLabIssuer != "" && v.GitLabKeySet == nil {
			_ = v.cache.Register(v.gitLabJWKS(), jwk.WithMinRefreshInterval(15*time.Minute))
		}
	})
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
		v.ensureCache()
		set, err := v.cache.Get(ctx, v.githubJWKS())
		if err != nil {
			return nil, 0, err
		}
		return set, oidcIssuerGitHub, nil
	case v.GitLabIssuer != "" && iss == v.GitLabIssuer:
		if v.GitLabKeySet != nil {
			return v.GitLabKeySet, oidcIssuerGitLab, nil
		}
		v.ensureCache()
		set, err := v.cache.Get(ctx, v.gitLabJWKS())
		if err != nil {
			return nil, 0, err
		}
		return set, oidcIssuerGitLab, nil
	default:
		return nil, 0, ErrUnauthorized
	}
}

func (v *PublishOIDCVerifier) Verify(ctx context.Context, token string) (subject, pluginID string, err error) {
	unverified, err := jwt.Parse([]byte(token), jwt.WithVerify(false), jwt.WithValidate(false))
	if err != nil {
		return "", "", ErrUnauthorized
	}
	set, kind, err := v.keySetForIssuer(ctx, unverified.Issuer())
	if err != nil {
		return "", "", ErrUnauthorized
	}
	parsed, err := jwt.Parse([]byte(token),
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
		jwt.WithIssuer(unverified.Issuer()),
	)
	if err != nil {
		return "", "", ErrUnauthorized
	}
	if v.Audience == "" {
		return "", "", ErrUnauthorized
	}
	aud := parsed.Audience()
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
	return parsed.Subject(), pid, nil
}

func sourceID(kind oidcIssuerKind, parsed jwt.Token) string {
	switch kind {
	case oidcIssuerGitHub:
		repoClaim, _ := parsed.Get("repository")
		repo, _ := repoClaim.(string)
		if repo == "" {
			repo = extractRepo(parsed.Subject())
		}
		return repo
	case oidcIssuerGitLab:
		pathClaim, _ := parsed.Get("project_path")
		path, _ := pathClaim.(string)
		if path == "" {
			path = extractGitLabProject(parsed.Subject())
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
	parsed, err := jwt.Parse([]byte(token), jwt.WithVerify(false), jwt.WithValidate(false))
	if err != nil {
		return "", ErrUnauthorized
	}
	cid, _ := parsed.Get("customerId")
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
		key, err := jwt.Parse([]byte(token), jwt.WithKey(jwa.EdDSA, pub))
		if err != nil {
			lastErr = err
			continue
		}
		cid, _ := key.Get("customerId")
		pid, _ := key.Get("pluginId")
		exp := key.Expiration()
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
