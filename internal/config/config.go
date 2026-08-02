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
	// OrphanCleanupInterval is how often the orphan-cleanup goroutine wakes
	// up to check whether a sweep is due. The sweep itself only actually
	// runs once per OrphanCleanupRunInterval (AMD-27's "once per 24h"),
	// gated by the cross-replica lease's LastRunAt -- see
	// internal/lifecycle.OrphanCleanup.
	OrphanCleanupInterval time.Duration
	// OrphanCleanupEnabled gates the sweep entirely. Defaults to false, and
	// that default is a contract, not a placeholder default that will
	// eventually flip: three independent review rounds each found a
	// distinct way to defeat internal/lifecycle.OrphanCleanup's
	// refuse-to-delete guard (see the doc comment on
	// lifecycle.OrphanCleanup for the specifics), and the third was never
	// closed by a code fix. Enabling this sweeper is UNSUPPORTED pending a
	// proven guard -- it may delete committed plugin versions. There is no
	// combination of the other Orphan* settings below that makes enabling
	// it safe by itself; see TestLoad_OrphanCleanupDisabledByDefault
	// (config_test.go), which fails if this default is ever flipped.
	OrphanCleanupEnabled bool
	// OrphanCleanupDryRun defaults to true even once Enabled is set, so
	// turning the job on and trusting it to delete are two separate,
	// deliberate operator actions.
	OrphanCleanupDryRun bool
	// OrphanCleanupMinAge is the AMD-27 age guard.
	OrphanCleanupMinAge time.Duration
	// OrphanCleanupRunInterval is AMD-27's "once per 24h" schedule.
	OrphanCleanupRunInterval time.Duration
	// OrphanCleanupLeaseTTL bounds how long one replica holds the
	// single-runner lease before another replica may take over.
	OrphanCleanupLeaseTTL time.Duration
	GCPProject            string
	TrustedProxyHops      int
	AllowInsecureDefaults bool
}

func Load() (*Config, error) {
	cfg := &Config{
		StorageType:              StorageType(strings.ToLower(getEnv("STORAGE_TYPE", "local"))),
		StorageLocalRoot:         getEnv("STORAGE_LOCAL_ROOT", "./data"),
		StorageSigningSecret:     getEnv("STORAGE_SIGNING_SECRET", ""),
		GCSBucket:                getEnv("GCS_BUCKET", ""),
		GCSPrivateBucket:         getEnv("GCS_PRIVATE_BUCKET", ""),
		CDNBaseURL:               strings.TrimRight(getEnv("CDN_BASE_URL", "http://localhost:8080/cdn"), "/"),
		CDNURLMap:                getEnv("CDN_URL_MAP", ""),
		AdminLoginEnabled:        getEnvBool("ADMIN_LOGIN_ENABLED", true),
		AdminUsername:            getEnv("ADMIN_USERNAME", "admin"),
		AdminPasswordHash:        getEnv("ADMIN_PASSWORD_HASH", ""),
		JWTSecret:                getEnv("JWT_SECRET", ""),
		JWTIssuer:                getEnv("JWT_ISSUER", "service-marketplace"),
		JWTTTLSeconds:            getEnvInt("JWT_TTL_SECONDS", 3600),
		GitHubOAuthClientID:      getEnv("GITHUB_OAUTH_CLIENT_ID", ""),
		GitHubOAuthClientSecret:  getEnv("GITHUB_OAUTH_CLIENT_SECRET", ""),
		GitHubOAuthOrg:           getEnv("GITHUB_OAUTH_ORG", ""),
		GitHubOAuthAllowedTeam:   getEnv("GITHUB_OAUTH_ALLOWED_TEAM", ""),
		GitHubOAuthRedirectURL:   getEnv("GITHUB_OAUTH_REDIRECT_URL", ""),
		PublishOIDCAudience:      getEnv("PUBLISH_OIDC_AUDIENCE", ""),
		GA4MeasurementID:         getEnv("GA4_MEASUREMENT_ID", ""),
		GA4APISecret:             getEnv("GA4_API_SECRET", ""),
		HTTPAddr:                 getEnv("HTTP_ADDR", ":8080"),
		OrphanCleanupInterval:    getEnvDuration("ORPHAN_CLEANUP_INTERVAL", 5*time.Minute),
		OrphanCleanupEnabled:     getEnvBool("ORPHAN_CLEANUP_ENABLED", false),
		OrphanCleanupDryRun:      getEnvBool("ORPHAN_CLEANUP_DRY_RUN", true),
		OrphanCleanupMinAge:      getEnvDuration("ORPHAN_CLEANUP_MIN_AGE", 24*time.Hour),
		OrphanCleanupRunInterval: getEnvDuration("ORPHAN_CLEANUP_RUN_INTERVAL", 24*time.Hour),
		OrphanCleanupLeaseTTL:    getEnvDuration("ORPHAN_CLEANUP_LEASE_TTL", 15*time.Minute),
		GCPProject:               getEnv("GCP_PROJECT", ""),
		TrustedProxyHops:         getEnvInt("TRUSTED_PROXY_HOPS", 0),
		AllowInsecureDefaults:    getEnvBool("ALLOW_INSECURE_DEFAULTS", false),
	}

	if cfg.GitHubOAuthRedirectURL == "" {
		cfg.GitHubOAuthRedirectURL = "http://localhost:8080/api/v1/auth/github/callback"
	}

	raw := getEnv("PUBLISH_OIDC_ALLOWED_SOURCES", "{}")
	cfg.PublishOIDCAllowedSources = map[string]string{}
	if err := json.Unmarshal([]byte(raw), &cfg.PublishOIDCAllowedSources); err != nil {
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
		cfg.TrustedProxyHops = 0
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

func getEnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
