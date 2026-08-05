package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	PathIndex          = "index.json"
	PathAuthorizedKeys = "auth/authorized_keys.json"
	PathSessionDeny    = "auth/session-denylist"
	PathOAuthState     = "auth/oauth-state"
	PathLoginLockout   = "auth/login-lockout"

	// PathOrphanCleanupLease backs the single-runner CAS lease for
	// internal/lifecycle.OrphanCleanup, so N replicas do not each run their
	// own sweep concurrently. Under "private/" so it is routed to the
	// private bucket on GCS and never CDN-served, the same reasoning as the
	// auth/ paths above.
	PathOrphanCleanupLease = "private/system/orphan-cleanup-lease.json"

	// PathHousekeepingFailures is the prefix under which lifecycle mutations
	// durably record a downstream housekeeping failure (index rebuild or CDN
	// invalidation) that happened after their primary write already
	// committed, so it can be found and retried out of band. See
	// internal/lifecycle.Service.recordHousekeepingFailure.
	PathHousekeepingFailures = "private/system/housekeeping-failures"
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

// HousekeepingFailurePath returns a fresh, collision-resistant object key
// for one recorded housekeeping failure. Each failure gets its own object
// (rather than an appended list) so concurrent failures never contend on a
// single CAS write.
func HousekeepingFailurePath(pluginID, action, step string, at time.Time) string {
	return path.Join(PathHousekeepingFailures, fmt.Sprintf("%s-%s-%s-%d.json", pluginID, action, step, at.UnixNano()))
}

func CDNPath(objectPath string) string {
	return strings.TrimPrefix(objectPath, "/")
}

// Reserved top-level namespaces only; do not case-fold plugin version segments.
var reservedNamespaces = []string{"auth", "private"}

// ErrReservedNamespaceAlias means the path is a case/NTFS alias of auth/ or private/.
var ErrReservedNamespaceAlias = errors.New("object path is a case alias of a reserved namespace")

// hasReservedPrefix matches the bare namespace root as well as its descendants:
// path.Clean reduces "/cdn/auth/" and "/cdn/auth/x/.." to "auth", which a
// HasPrefix(p, ns+"/") check would miss.
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

// CanonicalizeObjectPath cleans a caller-supplied path and rejects reserved-namespace
// aliases. Case-insensitive filesystems (the default local backend on macOS/Windows)
// resolve "Auth/x" and "auth/x" to the same bytes while the case-sensitive guards --
// which is the only correct behaviour on GCS -- see different strings. Aliases are
// refused, never rewritten: case is load-bearing object identity on GCS.
func CanonicalizeObjectPath(raw string) (string, error) {
	// NTFS aliases: '\\' is a separator there and trailing dots/spaces are stripped,
	// so "auth\\x" and "auth./x" reach the protected object while the '/'-only segment
	// split below sees neither as the "auth" namespace. No key this codebase produces
	// contains them, so refuse rather than normalise.
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
