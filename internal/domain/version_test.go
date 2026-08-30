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
