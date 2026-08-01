package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	PathIndex          = "index.json"
	PathAuthorizedKeys = "auth/authorized_keys.json"
	PathSessionDeny    = "auth/session-denylist"
	PathOAuthState     = "auth/oauth-state"
	PathLoginLockout   = "auth/login-lockout"
)

var screenshotNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,126}\.(png|jpg|jpeg)$`)

func PluginStatePath(pluginID string) string {
	return path.Join("plugins", pluginID, "plugin.json")
}

func VersionManifestPath(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version, "manifest.json")
}

// VersionArtifactPath returns the object key for a plugin jar.
// Premium jars are stored under private/ so they are never CDN-public.
func VersionArtifactPath(pluginID, version, access string) string {
	name := fmt.Sprintf("%s-%s.jar", pluginID, version)
	if access == "premium" {
		return path.Join("private", "plugins", pluginID, "versions", version, name)
	}
	return path.Join("plugins", pluginID, "versions", version, name)
}

func VersionChangelogPath(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version, "CHANGELOG.md")
}

func VersionScreenshotPath(pluginID, version, filename string) (string, error) {
	safe, err := SanitizeScreenshotFilename(filename)
	if err != nil {
		return "", err
	}
	return path.Join("plugins", pluginID, "versions", version, "screenshots", safe), nil
}

func VersionAdvisoryPath(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version, "advisory.json")
}

func VersionPrefix(pluginID, version string) string {
	return path.Join("plugins", pluginID, "versions", version) + "/"
}

func PluginPrefix(pluginID string) string {
	return path.Join("plugins", pluginID) + "/"
}

func SessionDenylistPath(jti string) string {
	return path.Join(PathSessionDeny, jti+".json")
}

// OAuthStatePath returns the shared-storage object key backing one issued
// OAuth CSRF state, so a login started on one replica can be consumed by
// the callback landing on any other (assessment finding
// F4-inmemory-state-not-shared-across-replicas). state is hashed rather
// than used verbatim: although states are normally our own random IDs
// (safe path segments), Consume also receives the caller-supplied "state"
// query parameter on the callback path, and hashing removes any need to
// separately reason about path-traversal or invalid-path-character input
// from that untrusted value.
func OAuthStatePath(state string) string {
	sum := sha256.Sum256([]byte(state))
	return path.Join(PathOAuthState, hex.EncodeToString(sum[:])+".json")
}

// LoginLockoutPath returns the shared-storage object key backing one admin
// login lockout counter, keyed by clientIP+username, so the
// five-attempts-per-fifteen-minutes lockout is enforced across all
// replicas instead of per-process (assessment finding
// F4-inmemory-state-not-shared-across-replicas). The key is hashed because
// it embeds the attacker-supplied username from the login request body.
func LoginLockoutPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return path.Join(PathLoginLockout, hex.EncodeToString(sum[:])+".json")
}

func CDNPath(objectPath string) string {
	return strings.TrimPrefix(objectPath, "/")
}

// IsPrivateObject reports objects that must never be served without a signed URL
// (and auth/ must never be CDN-served at all).
func IsPrivateObject(objectPath string) bool {
	p := strings.TrimPrefix(objectPath, "/")
	return strings.HasPrefix(p, "private/") || strings.HasPrefix(p, "auth/")
}

func IsAuthObject(objectPath string) bool {
	p := strings.TrimPrefix(objectPath, "/")
	return strings.HasPrefix(p, "auth/")
}

func SanitizeScreenshotFilename(filename string) (string, error) {
	base := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	base = strings.ToLower(base)
	if !screenshotNamePattern.MatchString(base) {
		return "", fmt.Errorf("invalid screenshot filename")
	}
	return base, nil
}
