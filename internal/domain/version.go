package domain

import (
	"strings"
)

// CompareVersions orders two versions by SemVer precedence: -1 if a sorts before b,
// +1 if after, 0 if equal. Both must already have passed ValidateVersion; a value that
// has not is compared by its numeric core alone, which is the safe direction — an
// unparseable field reads as 0 and loses rather than winning by accident.
//
// Build metadata is ignored, as SemVer §10 requires. A pre-release sorts below the
// release it precedes (§11.3), and pre-release identifiers are compared field by field:
// numeric ones numerically, others by ASCII, numeric below non-numeric, and a shorter
// run of otherwise equal fields below a longer one (§11.4).
func CompareVersions(a, b string) int {
	aCore, aPre := splitVersion(a)
	bCore, bPre := splitVersion(b)
	for i := 0; i < 3; i++ {
		if c := compareNumeric(aCore[i], bCore[i]); c != 0 {
			return c
		}
	}
	return comparePrerelease(aPre, bPre)
}

// splitVersion returns the three core numbers as digit strings and the pre-release string, build
// metadata discarded.
//
// They stay strings because SemVer puts no ceiling on a core number and ValidateVersion enforces
// none: `(0|[1-9]\d*)` accepts twenty digits as readily as one. Parsed into an int those saturate
// at MaxInt, so two different versions that both overflow compare equal — and the one the registry
// would then call latest is whichever the store happened to return first.
func splitVersion(v string) ([3]string, string) {
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	core := [3]string{"0", "0", "0"}
	for i, part := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		// an unvalidated value keeps the documented behaviour: unparseable reads as 0 and loses
		// rather than winning by accident
		if isNumeric(part) {
			core[i] = part
		}
	}
	return core, pre
}

// isNumeric reports whether s is a SemVer numeric identifier: one or more ASCII digits, and
// nothing else. Deliberately not strconv: Atoi accepts a leading sign, so `-5` and `+5` would pass
// for numbers, and SemVer §9 says a numeric identifier is digits alone.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// compareNumeric orders two digit strings by value, at any width. Leading zeros are dropped first
// so `01` and `1` are one number; then the longer run of digits is the larger number, and equal
// lengths compare lexicographically, which for digits is the same as comparing values.
func compareNumeric(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return compareInt(len(a), len(b))
	}
	return strings.Compare(a, b)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	// "1.0.0-rc.1" precedes "1.0.0": having a pre-release at all is what lowers it.
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	aFields, bFields := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(aFields) && i < len(bFields); i++ {
		if c := comparePrereleaseField(aFields[i], bFields[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(aFields), len(bFields))
}

func comparePrereleaseField(a, b string) int {
	aNum, bNum := isNumeric(a), isNumeric(b)
	switch {
	case aNum && bNum:
		// by value and at any width, for the same reason the core is: `rc.10` must outrank `rc.2`,
		// and neither may saturate into a tie with the other
		return compareNumeric(a, b)
	case aNum:
		return -1 // numeric identifiers always have lower precedence than alphanumeric ones
	case bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// LatestInstallableVersion returns the highest version of the plugin that the registry would
// actually serve: the newest one that is not blocked, or "" when every version is.
//
// `LatestVersion` used to be maintained incrementally at publish — kept only if the arriving
// version compared higher — which is monotonic and therefore cannot fall. Blocking a version makes
// it fall: the newest build is withdrawn and the catalogue must go back to offering the one below
// it. Left as it was, the listing kept naming a version the artifact route answers 403 for, so the
// row offered an install that could only fail.
//
// Recomputing from the whole list rather than adjusting in place also repairs a state whose
// `LatestVersion` was written by an earlier, wrong comparison — the pre-release ordering and the
// wide-number saturation both shipped before they were fixed.
func LatestInstallableVersion(versions []VersionMeta, blocked []BlockedVersion) string {
	withheld := make(map[string]struct{}, len(blocked))
	for _, b := range blocked {
		withheld[b.Version] = struct{}{}
	}
	latest := ""
	for _, v := range versions {
		if _, isBlocked := withheld[v.Version]; isBlocked {
			continue
		}
		if latest == "" || CompareVersions(v.Version, latest) > 0 {
			latest = v.Version
		}
	}
	return latest
}
