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
// SemVer-maximum among the versions that are (a) complete, (b) not blocked,
// and (c) not pre-release. Callers recompute it — never patch it
// incrementally — on every publish, block, and unblock, so publishing a
// semver-lower version (the §6.2 legacy-hotfix workflow: patching an older
// line after a newer major already exists) never moves the pointer, and
// blocking or unblocking the current latest promotes or restores the
// correct replacement.
//
// Filter (a) is MAJOR 1 (branch report): publish()'s CAS-before-artifacts
// order (see internal/publish.Service.publish's doc comment) means a
// version can be present in versions — even be its SemVer-maximum — before
// its artifacts (jar, manifest, ...) exist, with domain.IsVersionComplete
// false for that entry. If this function ever returned such a version, the
// caller would durably commit it as latestVersion, and any later
// rebuildIndex (possibly triggered by an entirely unrelated plugin's
// publish) would then try to read that version's manifest and fail. This
// filter is applied before EVERY tier below, including both fallbacks: an
// incomplete version must never become latestVersion by any path, not just
// the primary one.
//
// If no complete version satisfies both remaining filters, latestVersion
// still must not go empty while a complete version exists: AMD-07 spells
// this out for the case where every version is blocked ("latestVersion
// keeps the semver-max version and consumers rely on the blocked flag") —
// the pointer falls back to the SemVer-maximum of the complete versions,
// blocked/pre-release filters dropped, deliberately generalizing that same
// fallback to the symmetric case of a plugin whose only published versions
// are all pre-release (AMD-07 does not name this case explicitly; treating
// it the same as "all blocked" keeps the pointer non-empty and well-defined
// rather than leaving it stale or blank). If NO version is complete, the
// pointer goes empty — naming an incomplete version is worse than naming
// none, per MAJOR 1.
//
// versions should be the plugin's full known version list; blocked its
// current BlockedVersions set. Returns "" when versions is empty, or when
// no version in it is complete.
func LatestVersion(versions []VersionMeta, blocked []BlockedVersion) string {
	blockedSet := make(map[string]struct{}, len(blocked))
	for _, b := range blocked {
		blockedSet[b.Version] = struct{}{}
	}

	complete := make([]VersionMeta, 0, len(versions))
	for _, v := range versions {
		if IsVersionComplete(v) {
			complete = append(complete, v)
		}
	}

	if best, ok := maxVersion(complete, func(v string) bool {
		_, blocked := blockedSet[v]
		return !blocked && !IsPreRelease(v)
	}); ok {
		return best
	}

	// Nothing complete is both unblocked and released — fall back to the
	// SemVer-maximum among the complete versions only, so the pointer never
	// names an incomplete one even via this fallback.
	best, _ := maxVersion(complete, func(string) bool { return true })
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
