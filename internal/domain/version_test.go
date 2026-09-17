package domain

import "testing"

func TestCompareVersionsOrdersByPrecedenceNotByLength(t *testing.T) {
	// Each pair is strictly ascending. The string comparison that CompareVersions replaces
	// gets 1.5.2 > 1.5.10 and 1.9.0 > 1.10.0 wrong, which is the whole point.
	ascending := [][2]string{
		{"1.5.2", "1.5.10"},
		{"1.9.0", "1.10.0"},
		{"2.4.0", "10.0.0"},
		{"1.0.0-alpha", "1.0.0"},
		{"1.0.0-alpha", "1.0.0-alpha.1"},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta"},
		{"1.0.0-alpha.beta", "1.0.0-beta"},
		{"1.0.0-beta.2", "1.0.0-beta.11"},
		{"1.0.0-rc.1", "1.0.0"},
	}
	for _, pair := range ascending {
		lo, hi := pair[0], pair[1]
		if got := CompareVersions(lo, hi); got != -1 {
			t.Errorf("CompareVersions(%q, %q) = %d, want -1", lo, hi, got)
		}
		if got := CompareVersions(hi, lo); got != 1 {
			t.Errorf("CompareVersions(%q, %q) = %d, want 1", hi, lo, got)
		}
	}
}

func TestCompareVersionsIgnoresBuildMetadata(t *testing.T) {
	if got := CompareVersions("1.2.3+build.9", "1.2.3+build.1"); got != 0 {
		t.Errorf("build metadata must not affect precedence, got %d", got)
	}
	if got := CompareVersions("1.2.3", "1.2.3"); got != 0 {
		t.Errorf("CompareVersions of equal versions = %d, want 0", got)
	}
}

// A core number wider than an int64 is accepted by ValidateVersion — `(0|[1-9]\d*)` puts no
// ceiling on the digits — and used to be parsed with strconv.Atoi, which saturates at MaxInt on
// overflow rather than failing. Two different versions then compared equal, and whichever the
// store returned first became "latest".
//
// Kills reverting splitVersion to int parsing.
func TestCompareVersionsAtWidthsAnIntCannotHold(t *testing.T) {
	huge := [2]string{"99999999999999999999.0.0", "99999999999999999998.0.0"}
	if err := ValidateVersion(huge[0]); err != nil {
		t.Fatalf("premise wrong: %q is rejected, so overflow is unreachable: %v", huge[0], err)
	}
	if got := CompareVersions(huge[1], huge[0]); got != -1 {
		t.Errorf("CompareVersions(%q, %q) = %d, want -1", huge[1], huge[0], got)
	}

	// and the same in a pre-release identifier, where the saturation is identical
	lo, hi := "1.0.0-rc.99999999999999999998", "1.0.0-rc.99999999999999999999"
	if got := CompareVersions(lo, hi); got != -1 {
		t.Errorf("CompareVersions(%q, %q) = %d, want -1", lo, hi, got)
	}
}

// Leading zeros are not a different number. ValidateVersion rejects them in the core, but a
// pre-release identifier may carry them, and `rc.01` and `rc.1` name one build.
func TestCompareVersionsTreatsLeadingZerosAsOneNumber(t *testing.T) {
	if got := CompareVersions("1.0.0-rc.01", "1.0.0-rc.1"); got != 0 {
		t.Errorf("CompareVersions(rc.01, rc.1) = %d, want 0", got)
	}
	if got := CompareVersions("1.0.0-rc.010", "1.0.0-rc.9"); got != 1 {
		t.Errorf("CompareVersions(rc.010, rc.9) = %d, want 1", got)
	}
}

// SemVer §9 defines a numeric identifier as digits alone. strconv.Atoi also accepts a sign, so a
// pre-release field of `-5` used to be read as the number -5 — sorting it below every other
// numeric identifier, when it is alphanumeric and belongs above all of them.
//
// Kills replacing isNumeric with an Atoi error check.
func TestSignedPreReleaseIdentifierIsAlphanumeric(t *testing.T) {
	// `1.0.0--5` splits at the first hyphen, so the pre-release is `-5`
	if got := CompareVersions("1.0.0-1", "1.0.0--5"); got != -1 {
		t.Errorf("a numeric identifier must sort below an alphanumeric one, got %d", got)
	}
}

// CompareVersions documents that a value which never passed ValidateVersion is compared by its
// numeric core alone, and that an unparseable field reads as 0 — losing rather than winning by
// accident. Nothing pinned that, so the filter that implements it could be removed silently.
//
// Kills dropping the isNumeric guard in splitVersion.
func TestUnvalidatedVersionLosesRatherThanWins(t *testing.T) {
	if err := ValidateVersion("x.0.0"); err == nil {
		t.Fatal("premise wrong: x.0.0 should not validate")
	}
	if got := CompareVersions("x.0.0", "0.0.1"); got != -1 {
		t.Errorf("an unparseable major must read as 0 and lose, got %d", got)
	}
}
