package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type StorageType string

const (
	StorageLocal StorageType = "local"
	StorageGCS   StorageType = "gcs"

	insecureJWTDefault     = "dev-jwt-secret-change-me"
	insecureSigningDefault = "dev-signing-secret"
	minSecretLen           = 32
)

type Config struct {
	StorageType               StorageType
	StorageLocalRoot          string
	StorageSigningSecret      string
	GCSBucket                 string
	GCSPrivateBucket          string
	CDNBaseURL                string
	CDNURLMap                 string
	AdminLoginEnabled         bool
	AdminUsername             string
	AdminPasswordHash         string
	JWTSecret                 string
	JWTIssuer                 string
	JWTTTLSeconds             int
	GitHubOAuthClientID       string
	GitHubOAuthClientSecret   string
	GitHubOAuthOrg            string
	GitHubOAuthAllowedTeam    string
	GitHubOAuthRedirectURL    string
	PublishOIDCAudience       string
	PublishOIDCAllowedSources map[string]string
	GA4MeasurementID          string
	GA4APISecret              string
	HTTPAddr                  string
	OrphanCleanupInterval     time.Duration
	GCPProject                string
	TrustedProxyHops          int
	AllowInsecureDefaults     bool
}

func Load() (*Config, error) {
	adminLoginEnabled, err := getEnvBoolE("ADMIN_LOGIN_ENABLED", true)
	if err != nil {
		return nil, err
	}
	jwtTTLSeconds, err := getEnvIntE("JWT_TTL_SECONDS", 3600)
	if err != nil {
		return nil, err
	}
	orphanCleanupInterval, err := getEnvDurationE("ORPHAN_CLEANUP_INTERVAL", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	trustedProxyHops, err := getEnvIntE("TRUSTED_PROXY_HOPS", 0)
	if err != nil {
		return nil, err
	}
	allowInsecureDefaults, err := getEnvBoolE("ALLOW_INSECURE_DEFAULTS", false)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		StorageType:             StorageType(strings.ToLower(getEnv("STORAGE_TYPE", "local"))),
		StorageLocalRoot:        getEnv("STORAGE_LOCAL_ROOT", "./data"),
		StorageSigningSecret:    getEnv("STORAGE_SIGNING_SECRET", ""),
		GCSBucket:               getEnv("GCS_BUCKET", ""),
		GCSPrivateBucket:        getEnv("GCS_PRIVATE_BUCKET", ""),
		CDNBaseURL:              strings.TrimRight(getEnv("CDN_BASE_URL", "http://localhost:8080/cdn"), "/"),
		CDNURLMap:               getEnv("CDN_URL_MAP", ""),
		AdminLoginEnabled:       adminLoginEnabled,
		AdminUsername:           getEnv("ADMIN_USERNAME", "admin"),
		AdminPasswordHash:       getEnv("ADMIN_PASSWORD_HASH", ""),
		JWTSecret:               getEnv("JWT_SECRET", ""),
		JWTIssuer:               getEnv("JWT_ISSUER", "service-marketplace"),
		JWTTTLSeconds:           jwtTTLSeconds,
		GitHubOAuthClientID:     getEnv("GITHUB_OAUTH_CLIENT_ID", ""),
		GitHubOAuthClientSecret: getEnv("GITHUB_OAUTH_CLIENT_SECRET", ""),
		GitHubOAuthOrg:          getEnv("GITHUB_OAUTH_ORG", ""),
		GitHubOAuthAllowedTeam:  getEnv("GITHUB_OAUTH_ALLOWED_TEAM", ""),
		GitHubOAuthRedirectURL:  getEnv("GITHUB_OAUTH_REDIRECT_URL", ""),
		PublishOIDCAudience:     getEnv("PUBLISH_OIDC_AUDIENCE", ""),
		GA4MeasurementID:        getEnv("GA4_MEASUREMENT_ID", ""),
		GA4APISecret:            getEnv("GA4_API_SECRET", ""),
		HTTPAddr:                getEnv("HTTP_ADDR", ":8080"),
		OrphanCleanupInterval:   orphanCleanupInterval,
		GCPProject:              getEnv("GCP_PROJECT", ""),
		TrustedProxyHops:        trustedProxyHops,
		AllowInsecureDefaults:   allowInsecureDefaults,
	}

	if cfg.GitHubOAuthRedirectURL == "" {
		cfg.GitHubOAuthRedirectURL = "http://localhost:8080/api/v1/auth/github/callback"
	}

	raw := getEnv("PUBLISH_OIDC_ALLOWED_SOURCES", "{}")
	cfg.PublishOIDCAllowedSources = map[string]string{}
	if err = json.Unmarshal([]byte(raw), &cfg.PublishOIDCAllowedSources); err != nil {
		return nil, fmt.Errorf("PUBLISH_OIDC_ALLOWED_SOURCES: %w", err)
	}

	if cfg.StorageType != StorageLocal && cfg.StorageType != StorageGCS {
		return nil, fmt.Errorf("invalid STORAGE_TYPE: %s", cfg.StorageType)
	}
	if cfg.JWTTTLSeconds > 3600 {
		cfg.JWTTTLSeconds = 3600
	}
	if cfg.JWTTTLSeconds < 60 {
		cfg.JWTTTLSeconds = 60
	}
	if cfg.TrustedProxyHops < 0 {
		return nil, fmt.Errorf("TRUSTED_PROXY_HOPS: %d is negative", cfg.TrustedProxyHops)
	}

	if err := cfg.validateSecrets(); err != nil {
		return nil, err
	}
	if len(cfg.PublishOIDCAllowedSources) > 0 && strings.TrimSpace(cfg.PublishOIDCAudience) == "" {
		return nil, fmt.Errorf("PUBLISH_OIDC_AUDIENCE is required when PUBLISH_OIDC_ALLOWED_SOURCES is non-empty")
	}
	if cfg.StorageType == StorageGCS && cfg.GCSBucket == "" {
		return nil, fmt.Errorf("GCS_BUCKET is required when STORAGE_TYPE=gcs")
	}
	return cfg, nil
}

func (c *Config) validateSecrets() error {
	if c.AllowInsecureDefaults {
		if c.JWTSecret == "" || c.JWTSecret == insecureJWTDefault {
			c.JWTSecret = insecureJWTDefault
		}
		if c.StorageSigningSecret == "" || c.StorageSigningSecret == insecureSigningDefault {
			c.StorageSigningSecret = insecureSigningDefault
		}
		return nil
	}
	if err := requireSecret("JWT_SECRET", c.JWTSecret, insecureJWTDefault); err != nil {
		return err
	}
	if err := requireSecret("STORAGE_SIGNING_SECRET", c.StorageSigningSecret, insecureSigningDefault); err != nil {
		return err
	}
	return nil
}

func requireSecret(name, value, insecureDefault string) error {
	if value == "" || value == insecureDefault {
		return fmt.Errorf("%s must be set to a strong secret (or set ALLOW_INSECURE_DEFAULTS=true for local development only)", name)
	}
	if len(value) < minSecretLen {
		return fmt.Errorf("%s must be at least %d characters", name, minSecretLen)
	}
	return nil
}

func (c *Config) GitHubOAuthEnabled() bool {
	return c.GitHubOAuthClientID != "" && c.GitHubOAuthClientSecret != "" && c.GitHubOAuthOrg != ""
}

func (c *Config) AdminLoginConfigured() bool {
	return c.AdminLoginEnabled && c.AdminPasswordHash != ""
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// The getEnv*E helpers below deliberately return an error for a value that is
// present but unparseable, rather than standing in the default. A default is
// the right answer for an UNSET variable; for a misspelt one it silently
// contradicts what the operator asked for. That was fail-open on an
// authentication control: ADMIN_LOGIN_ENABLED="flase" left password login
// ENABLED, and TRUSTED_PROXY_HOPS="one" became 0, which stops isHTTPS trusting
// X-Forwarded-Proto and drops Secure from the session cookie behind a
// TLS-terminating ingress.

func getEnvBoolE(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %q is not a boolean", key, v)
	}
	return b, nil
}

func getEnvIntE(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", key, v)
	}
	return n, nil
}

func getEnvDurationE(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration (e.g. 30s, 5m)", key, v)
	}
	return d, nil
}
