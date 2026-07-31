package storage

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	PathIndex          = "index.json"
	PathAuthorizedKeys = "auth/authorized_keys.json"
	PathSessionDeny    = "auth/session-denylist"
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
