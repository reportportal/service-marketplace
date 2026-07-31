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
)

type Config struct {
	StorageType           StorageType
	StorageLocalRoot      string
	StorageSigningSecret  string
	GCSBucket             string
	GCSPrivateBucket      string
	CDNBaseURL            string
	CDNURLMap             string
	AdminLoginEnabled     bool
	AdminUsername         string
	AdminPasswordHash     string
	JWTSecret             string
	JWTIssuer             string
	JWTTTLSeconds         int
	GitHubOAuthClientID   string
	GitHubOAuthClientSecret string
	GitHubOAuthOrg        string
	GitHubOAuthAllowedTeam string
	GitHubOAuthRedirectURL string
	PublishOIDCAudience   string
	PublishOIDCAllowedSources map[string]string
	GA4MeasurementID      string
	GA4APISecret          string
	HTTPAddr              string
	OrphanCleanupInterval time.Duration
	GCPProject            string
}

func Load() (*Config, error) {
	cfg := &Config{
		StorageType:           StorageType(strings.ToLower(getEnv("STORAGE_TYPE", "local"))),
		StorageLocalRoot:      getEnv("STORAGE_LOCAL_ROOT", "./data"),
		StorageSigningSecret:  getEnv("STORAGE_SIGNING_SECRET", "dev-signing-secret"),
		GCSBucket:             getEnv("GCS_BUCKET", ""),
		GCSPrivateBucket:      getEnv("GCS_PRIVATE_BUCKET", ""),
		CDNBaseURL:            strings.TrimRight(getEnv("CDN_BASE_URL", "http://localhost:8080/cdn"), "/"),
		CDNURLMap:             getEnv("CDN_URL_MAP", ""),
		AdminLoginEnabled:     getEnvBool("ADMIN_LOGIN_ENABLED", true),
		AdminUsername:         getEnv("ADMIN_USERNAME", "admin"),
		AdminPasswordHash:     getEnv("ADMIN_PASSWORD_HASH", ""),
		JWTSecret:             getEnv("JWT_SECRET", "dev-jwt-secret-change-me"),
		JWTIssuer:             getEnv("JWT_ISSUER", "service-marketplace"),
		JWTTTLSeconds:         getEnvInt("JWT_TTL_SECONDS", 3600),
		GitHubOAuthClientID:   getEnv("GITHUB_OAUTH_CLIENT_ID", ""),
		GitHubOAuthClientSecret: getEnv("GITHUB_OAUTH_CLIENT_SECRET", ""),
		GitHubOAuthOrg:        getEnv("GITHUB_OAUTH_ORG", ""),
		GitHubOAuthAllowedTeam: getEnv("GITHUB_OAUTH_ALLOWED_TEAM", ""),
		GitHubOAuthRedirectURL: getEnv("GITHUB_OAUTH_REDIRECT_URL", ""),
		PublishOIDCAudience:   getEnv("PUBLISH_OIDC_AUDIENCE", ""),
		GA4MeasurementID:      getEnv("GA4_MEASUREMENT_ID", ""),
		GA4APISecret:          getEnv("GA4_API_SECRET", ""),
		HTTPAddr:              getEnv("HTTP_ADDR", ":8080"),
		OrphanCleanupInterval: getEnvDuration("ORPHAN_CLEANUP_INTERVAL", 5*time.Minute),
		GCPProject:            getEnv("GCP_PROJECT", ""),
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
	return cfg, nil
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
