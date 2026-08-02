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

// reservedNamespaces are this system's own, fully-owned top-level object-key
// namespaces, spelled here — and ONLY here — in their one canonical,
// lowercase form. Every key under these prefixes is built exclusively from
// this codebase's own literal constants (PathAuthorizedKeys, PathSessionDeny,
// PathOAuthState, PathLoginLockout, PathOrphanCleanupLease,
// PathHousekeepingFailures) and never from caller-supplied casing. That is
// deliberately NOT true of the rest of the key space:
// "plugins/<pluginID>/versions/<version>/..." embeds a caller-supplied
// version string that legitimately preserves mixed case (semver
// prerelease/build metadata is case-sensitive — see domain.versionPattern's
// \w class), so canonicalisation must never reach past this fixed,
// two-element list. See CanonicalizeObjectPath's doc comment for why that
// boundary matters.
var reservedNamespaces = []string{"auth", "private"}

// ErrReservedNamespaceAlias is returned by CanonicalizeObjectPath when raw's
// first path segment is a case-insensitive match for a reservedNamespaces
// entry without being byte-identical to it — see that function's doc
// comment.
var ErrReservedNamespaceAlias = errors.New("object path is a case alias of a reserved namespace")

// hasReservedPrefix reports whether p is exactly ns, or a descendant of it
// (ns + "/..."). The equality arm matters on its own: path.Clean collapses
// "/cdn/auth/", "/cdn/auth" and "/cdn/auth/authorized_keys.json/.." all down
// to the bare object path "auth" (no trailing segment), which a
// HasPrefix(p, ns+"/")-only check does not match — so a request for that
// bare alias used to fall through every guard, reach
// Store.Read("auth"), and hit the storage backend's directory-vs-file
// distinction (EISDIR on a local filesystem) instead of the 403 a direct
// hit on any real object under auth/ gets. That is a directory-existence
// oracle: an unauthenticated caller can distinguish "auth/ exists as a
// directory" from "this key doesn't exist" by the response shape alone.
// Treating the bare root itself as reserved closes that off with the exact
// same FORBIDDEN response every other reserved-namespace hit gets.
func hasReservedPrefix(p, ns string) bool {
	return p == ns || strings.HasPrefix(p, ns+"/")
}

// IsPrivateObject reports objects that must never be served without a signed
// URL (and auth/ must never be CDN-served at all).
func IsPrivateObject(objectPath string) bool {
	p := strings.TrimPrefix(objectPath, "/")
	return hasReservedPrefix(p, "private") || hasReservedPrefix(p, "auth")
}

func IsAuthObject(objectPath string) bool {
	p := strings.TrimPrefix(objectPath, "/")
	return hasReservedPrefix(p, "auth")
}

// CanonicalizeObjectPath is the single choke-point every untrusted,
// caller-supplied object-path string must pass through before any
// authorization decision is made against it, and before it is used to read
// or write anything. Its only current caller is
// internal/httpapi.handleCDNProxy (the raw /cdn/* request path) — the one
// place in this system where an object-path string originates from an
// attacker rather than from this codebase's own path-builder functions
// above.
//
// WHY this exists: LocalStore's default deployment target (macOS/APFS,
// Windows/NTFS — see internal/config.Config's STORAGE_TYPE default of
// "local") is case-insensitive-but-case-preserving. os.ReadFile("Auth/x")
// and os.ReadFile("auth/x") resolve to the exact same bytes on such a
// filesystem, even though they are, textually, different strings. That means
// IsAuthObject and IsPrivateObject — ordinary case-sensitive prefix
// matches, which is the ONLY correct behaviour on GCS, where "Auth/x" and
// "auth/x" are genuinely different objects — can be defeated by a caller
// who simply respells a protected key's case: "Auth/authorized_keys.json"
// fails both predicates yet reads the exact bytes of the protected
// "auth/authorized_keys.json".
//
// WHERE the fix belongs: not inside IsPrivateObject/IsAuthObject themselves.
// Lower-casing those two predicates' comparisons would stop this one
// symptom but leaves the actual disease — one object, many spellings, all
// resolving to the same bytes — for the next guard someone writes against
// objectPath to walk straight into, with no compiler or reviewer able to
// tell it apart from a normal, correct prefix check. And a *global*
// canonicalisation (lower-casing every objectPath everywhere) would be
// worse, not better: it would corrupt GCS deployments, where case is a
// meaningful, load-bearing part of object identity, and would clobber
// already-published version strings that legitimately contain uppercase
// letters. So the fix cannot be "rewrite the key to its canonical form and
// carry on" anywhere in this system.
//
// What DOES work: refuse, at the one place an attacker-controlled string
// enters the system, anything that is not already spelled in the one
// canonical form this system itself ever produces for its own reserved
// namespaces — rather than silently rewriting it to that form and
// proceeding (which would just move the bug from "bypasses the guard" to
// "guard is bypassable but happens to still deny", indistinguishable from
// outside and one refactor away from regressing). A case alias of "auth" or
// "private" can only ever be an attacker probing for exactly this bug —
// nothing in this codebase ever legitimately produces or requests one — so
// there is no compatibility cost to rejecting it outright.
//
// This intentionally does NOT touch plugins/<pluginID>/versions/<version>/...
// segments: pluginID is already validated lowercase-only
// (domain.pluginIDPattern), and version legitimately preserves
// caller-supplied mixed case, so widening this check beyond
// reservedNamespaces would re-introduce the exact "over-reaching"
// canonicalisation risk described above for no security benefit.
func CanonicalizeObjectPath(raw string) (string, error) {
	// The NTFS half of the same aliasing class. On Windows the Win32 layer
	// treats '\' as a path separator and strips trailing dots and spaces
	// from a component, so "auth\authorized_keys.json" and
	// "auth./authorized_keys.json" both resolve to the protected object --
	// while the segment split below (which scans for '/' only) sees a single
	// segment that matches no reserved namespace, and IsAuthObject's
	// HasPrefix(p, "auth/") does not match either. LocalStore.abs then
	// resolves it via filepath.FromSlash/Clean, which DO honour '\' on
	// Windows.
	//
	// Refusing outright rather than normalising, for the same reason the
	// case aliases are refused: no key this codebase produces ever contains
	// a backslash or a trailing dot or space (see the path builders above
	// and SanitizeScreenshotFilename), so there is no compatibility cost,
	// and a rejected spelling cannot silently become a different object.
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
