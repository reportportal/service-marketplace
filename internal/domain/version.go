package domain

import (
	"strconv"
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
		if c := compareInt(aCore[i], bCore[i]); c != 0 {
			return c
		}
	}
	return comparePrerelease(aPre, bPre)
}

// splitVersion returns the three core numbers and the pre-release string, build metadata
// discarded.
func splitVersion(v string) ([3]int, string) {
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	var core [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		core[i], _ = strconv.Atoi(part)
	}
	return core, pre
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
	aNum, aErr := strconv.Atoi(a)
	bNum, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		return compareInt(aNum, bNum)
	case aErr == nil:
		return -1 // numeric identifiers always have lower precedence than alphanumeric ones
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}
