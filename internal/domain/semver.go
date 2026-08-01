package domain

import "golang.org/x/mod/semver"

// CompareVersions compares two version strings by SemVer 2.0 precedence.
// It returns -1, 0, or +1 as a < b, a == b, or a > b, matching the
// stdlib-style contract of sort.Slice comparators and cmp.Compare.
//
// Precedence follows semver.org §11 exactly: major, then minor, then patch
// compared numerically (so "1.10.0" > "1.9.0" — a plain string compare
// gets this backwards); a version with a pre-release has lower precedence
// than the same core without one; pre-release identifiers are compared
// dot-segment by dot-segment, numeric segments numerically and
// alphanumeric segments lexically, with numeric always losing to
// alphanumeric and a longer identifier list winning a common prefix; and
// build metadata (the "+..." suffix) is ignored entirely.
//
// This delegates to golang.org/x/mod/semver rather than a hand-rolled
// comparator: it is the same comparator the Go toolchain itself uses for
// module version ordering, is already a transitive dependency of half the
// Go ecosystem, and getting pre-release precedence right by hand (numeric
// vs. alphanumeric identifier comparison, "longer wins a common prefix",
// build-metadata exclusion) is exactly the kind of subtle-but-well-trodden
// logic a purpose-built, heavily-tested library is safer than a bespoke
// implementation for. Its one wrinkle — versions must carry a "v" prefix —
// is handled locally by prepending "v" rather than reformatting call
// sites, since manifest.Version/domain.ValidateVersion never include one.
func CompareVersions(a, b string) int {
	return semver.Compare("v"+a, "v"+b)
}

// IsPreRelease reports whether v carries a SemVer pre-release component
// (e.g. "2.0.0-rc.1"). Build metadata alone (e.g. "2.0.0+build.5") does
// not make a version a pre-release.
func IsPreRelease(v string) bool {
	return semver.Prerelease("v"+v) != ""
}

// LatestVersion implements AMD-07's latestVersion pointer semantics: the
// SemVer-maximum among the versions that are (a) not blocked and (b) not
// pre-release. Callers recompute it — never patch it incrementally — on
// every publish, block, and unblock, so publishing a semver-lower version
// (the §6.2 legacy-hotfix workflow: patching an older line after a newer
// major already exists) never moves the pointer, and blocking or
// unblocking the current latest promotes or restores the correct
// replacement.
//
// If no version satisfies both filters, latestVersion still must not go
// empty while versions exist: AMD-07 spells this out for the case where
// every version is blocked ("latestVersion keeps the semver-max version
// and consumers rely on the blocked flag") — the pointer falls back to the
// plain SemVer-maximum of the full list, filters dropped, deliberately
// generalizing that same fallback to the symmetric case of a plugin whose
// only published versions are all pre-release (AMD-07 does not name this
// case explicitly; treating it the same as "all blocked" keeps the
// pointer non-empty and well-defined rather than leaving it stale or
// blank).
//
// versions should be the plugin's full known version list; blocked its
// current BlockedVersions set. Returns "" only when versions is empty.
func LatestVersion(versions []VersionMeta, blocked []BlockedVersion) string {
	blockedSet := make(map[string]struct{}, len(blocked))
	for _, b := range blocked {
		blockedSet[b.Version] = struct{}{}
	}

	if best, ok := maxVersion(versions, func(v string) bool {
		_, blocked := blockedSet[v]
		return !blocked && !IsPreRelease(v)
	}); ok {
		return best
	}

	// Nothing is both unblocked and released — fall back to the overall
	// SemVer-maximum so the pointer never goes empty while versions exist.
	best, _ := maxVersion(versions, func(string) bool { return true })
	return best
}

// maxVersion returns the SemVer-maximum VersionMeta.Version among versions
// satisfying include, and whether any did. Ties (equal precedence, e.g.
// differing only in build metadata) keep the first candidate encountered,
// i.e. versions' own order — in practice publish order, so the
// earliest-published of two build-metadata variants wins deterministically.
func maxVersion(versions []VersionMeta, include func(string) bool) (string, bool) {
	var best string
	found := false
	for _, v := range versions {
		if !include(v.Version) {
			continue
		}
		if !found || CompareVersions(v.Version, best) > 0 {
			best = v.Version
			found = true
		}
	}
	return best, found
}
