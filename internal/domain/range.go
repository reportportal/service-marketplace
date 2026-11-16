package domain

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrInvalidCeilingBound means the operator value is not a single `<` or `<=`
	// upper bound on a ReportPortal CalVer.
	ErrInvalidCeilingBound = errors.New("invalid compatibility ceiling")
	// ErrEmptyCeilingIntersection means the ceiling excludes every release the
	// author's range still allows. That outcome is a block, not a ceiling.
	ErrEmptyCeilingIntersection = errors.New("empty compatibility ceiling intersection")
	// ErrUnreadableAuthorRange means the published range cannot be intersected,
	// so a ceiling must not be stored against it.
	ErrUnreadableAuthorRange = errors.New("unreadable author compatibility range")
)

// authorComparatorPattern accepts one semver comparator. ReportPortal ranges are
// CalVer (`25.1`, `25.1.2`) and the same operators the compatibility model lists.
var authorComparatorPattern = regexp.MustCompile(`^(>=|<=|>|<|=)\s*((?:0|[1-9]\d*)(?:\.(?:0|[1-9]\d*)){1,2}(?:-[\w.-]+)?(?:\+[\w.-]+)?)$`)

// ceilingPattern is the operator value: one upper bound, no prerelease, no comma.
var ceilingPattern = regexp.MustCompile(`^<(=)?((?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:\.(?:0|[1-9]\d*))?)$`)

type comparator struct {
	op      string
	version string
}

type versionBound struct {
	version   string
	inclusive bool
}

// FindCeiling returns the ceiling record for version, when one is stored.
func FindCeiling(ceilings []CompatibilityCeiling, version string) (CompatibilityCeiling, bool) {
	for _, c := range ceilings {
		if c.Version == version {
			return c, true
		}
	}
	return CompatibilityCeiling{}, false
}

// CanonicalCeiling reports the stored spelling of an operator bound (`<27.0`,
// `<=27.0`). Spacing is dropped. Anything that is not a single upper bound is rejected.
func CanonicalCeiling(raw string) (string, error) {
	c, err := parseCeiling(raw)
	if err != nil {
		return "", err
	}
	return c.op + c.version, nil
}

// EffectiveReportPortalRange intersects the author's compatibility.reportportal
// range with an operator ceiling. An empty ceiling returns author unchanged,
// including its original spelling. A ceiling that does not narrow the range
// does the same. A ceiling that narrows it returns the intersection.
func EffectiveReportPortalRange(author, ceiling string) (string, error) {
	if strings.TrimSpace(ceiling) == "" {
		return author, nil
	}
	if strings.TrimSpace(author) == "" {
		return "", ErrUnreadableAuthorRange
	}
	ceil, err := parseCeiling(ceiling)
	if err != nil {
		return "", err
	}
	comps, err := parseAuthorRange(author)
	if err != nil {
		return "", ErrUnreadableAuthorRange
	}
	all := append(append([]comparator{}, comps...), ceil)
	if intersectionEmpty(all) {
		return "", ErrEmptyCeilingIntersection
	}
	return effectiveString(author, comps, ceil), nil
}

func parseCeiling(raw string) (comparator, error) {
	m := ceilingPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return comparator{}, ErrInvalidCeilingBound
	}
	op := "<"
	if m[1] == "=" {
		op = "<="
	}
	return comparator{op: op, version: m[2]}, nil
}

func parseAuthorRange(raw string) ([]comparator, error) {
	parts := strings.Split(raw, ",")
	out := make([]comparator, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, ErrUnreadableAuthorRange
		}
		m := authorComparatorPattern.FindStringSubmatch(part)
		if m == nil {
			return nil, ErrUnreadableAuthorRange
		}
		out = append(out, comparator{op: m[1], version: m[2]})
	}
	if len(out) == 0 {
		return nil, ErrUnreadableAuthorRange
	}
	return out, nil
}

func effectiveString(author string, comps []comparator, ceiling comparator) string {
	ceilUpper, _ := upperOf(ceiling)
	if strictest, ok := strictestUpper(comps); ok && !upperIsStricter(ceilUpper, strictest) {
		return author
	}
	kept := make([]string, 0, len(comps)+1)
	for _, c := range comps {
		if c.op == "<" || c.op == "<=" {
			upper, _ := upperOf(c)
			if !upperIsStricter(upper, ceilUpper) {
				continue
			}
		}
		kept = append(kept, c.op+c.version)
	}
	kept = append(kept, ceiling.op+ceiling.version)
	return strings.Join(kept, ", ")
}

func intersectionEmpty(comps []comparator) bool {
	var lower, upper versionBound
	hasLower, hasUpper := false, false
	for _, c := range comps {
		if next, ok := lowerOf(c); ok {
			if !hasLower || lowerIsStricter(next, lower) {
				lower = next
				hasLower = true
			}
		}
		if next, ok := upperOf(c); ok {
			if !hasUpper || upperIsStricter(next, upper) {
				upper = next
				hasUpper = true
			}
		}
	}
	if !hasLower || !hasUpper {
		return false
	}
	switch CompareVersions(lower.version, upper.version) {
	case 1:
		return true
	case -1:
		return false
	default:
		return !lower.inclusive || !upper.inclusive
	}
}

func strictestUpper(comps []comparator) (versionBound, bool) {
	var best versionBound
	found := false
	for _, c := range comps {
		next, ok := upperOf(c)
		if !ok {
			continue
		}
		if !found || upperIsStricter(next, best) {
			best = next
			found = true
		}
	}
	return best, found
}

func upperOf(c comparator) (versionBound, bool) {
	switch c.op {
	case "<":
		return versionBound{version: c.version, inclusive: false}, true
	case "<=", "=":
		return versionBound{version: c.version, inclusive: true}, true
	default:
		return versionBound{}, false
	}
}

func lowerOf(c comparator) (versionBound, bool) {
	switch c.op {
	case ">":
		return versionBound{version: c.version, inclusive: false}, true
	case ">=", "=":
		return versionBound{version: c.version, inclusive: true}, true
	default:
		return versionBound{}, false
	}
}

func upperIsStricter(a, b versionBound) bool {
	switch CompareVersions(a.version, b.version) {
	case -1:
		return true
	case 1:
		return false
	default:
		return !a.inclusive && b.inclusive
	}
}

func lowerIsStricter(a, b versionBound) bool {
	switch CompareVersions(a.version, b.version) {
	case 1:
		return true
	case -1:
		return false
	default:
		return !a.inclusive && b.inclusive
	}
}
