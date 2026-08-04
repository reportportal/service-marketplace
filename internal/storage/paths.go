package storage

import (
	"errors"
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

// Reserved top-level namespaces only; do not case-fold plugin version segments.
var reservedNamespaces = []string{"auth", "private"}

// ErrReservedNamespaceAlias means the path is a case/NTFS alias of auth/ or private/.
var ErrReservedNamespaceAlias = errors.New("object path is a case alias of a reserved namespace")

func hasReservedPrefix(p, ns string) bool {
	return p == ns || strings.HasPrefix(p, ns+"/")
}

// IsPrivateObject reports objects that must never be served without a signed URL.
func IsPrivateObject(objectPath string) bool {
	p := strings.TrimPrefix(objectPath, "/")
	return hasReservedPrefix(p, "private") || hasReservedPrefix(p, "auth")
}

func IsAuthObject(objectPath string) bool {
	p := strings.TrimPrefix(objectPath, "/")
	return hasReservedPrefix(p, "auth")
}

// CanonicalizeObjectPath cleans a caller path and rejects reserved-namespace aliases.
func CanonicalizeObjectPath(raw string) (string, error) {
	// Reject NTFS aliases (backslash, trailing dot/space) rather than normalize them.
	if strings.ContainsRune(raw, '\\') {
		return "", ErrReservedNamespaceAlias
	}
	for _, seg := range strings.Split(strings.TrimPrefix(raw, "/"), "/") {
		if seg != strings.TrimRight(seg, ". ") {
			return "", ErrReservedNamespaceAlias
		}
	}

	cleaned := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	if cleaned == "/" {
		return "", ErrNotFound
	}
	p := strings.TrimPrefix(cleaned, "/")
	seg := p
	if i := strings.IndexByte(p, '/'); i >= 0 {
		seg = p[:i]
	}
	for _, ns := range reservedNamespaces {
		if seg != ns && strings.EqualFold(seg, ns) {
			return "", ErrReservedNamespaceAlias
		}
	}
	return p, nil
}

func SanitizeScreenshotFilename(filename string) (string, error) {
	base := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	base = strings.ToLower(base)
	if !screenshotNamePattern.MatchString(base) {
		return "", fmt.Errorf("invalid screenshot filename")
	}
	return base, nil
}
