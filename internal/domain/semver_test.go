package domain

import "testing"

// TestCompareVersions_Precedence pins down SemVer 2.0 precedence for the
// cases that actually discriminate a correct comparator from a naive one:
// numeric-width version cores (1.9.0 vs 1.10.0, which a lexicographic
// string compare gets backwards), pre-release-vs-release, pre-release
// identifier precedence, and build metadata (which MUST NOT affect
// ordering at all per the spec, even when it differs).
//
// Mutation this kills: replacing CompareVersions's body with a plain
// `strings.Compare(a, b)` (or any lexicographic comparison) — that passes
// every case that agrees with lexicographic order by coincidence but fails
// TestCompareVersions_Precedence/1.9.0_lt_1.10.0 and the build-metadata
// case, both of which lexicographic order gets wrong or fails to ignore.
func TestCompareVersions_Precedence(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int // sign of CompareVersions(a, b)
	}{
		{"equal", "1.0.0", "1.0.0", 0},
		{"1.9.0 lt 1.10.0", "1.9.0", "1.10.0", -1},
		{"1.10.0 gt 1.9.0", "1.10.0", "1.9.0", 1},
		{"prerelease lt release", "1.0.0-rc.1", "1.0.0", -1},
		{"release gt prerelease", "1.0.0", "1.0.0-rc.1", 1},
		{"prerelease numeric lt alpha", "1.0.0-1", "1.0.0-alpha", -1},
		{"prerelease alpha lt beta", "1.0.0-alpha", "1.0.0-beta", -1},
		{"prerelease shorter lt longer with common prefix", "1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"build metadata ignored (equal core+prerelease)", "1.0.0+build.1", "1.0.0+build.999", 0},
		{"build metadata ignored even lexicographically reversed", "1.0.0+zzz", "1.0.0+aaa", 0},
		{"major dominates prerelease of higher core", "1.0.0", "2.0.0-rc.1", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareVersions(tc.a, tc.b)
			gotSign := sign(got)
			if gotSign != tc.want {
				t.Fatalf("CompareVersions(%q, %q) = %d (sign %d), want sign %d", tc.a, tc.b, got, gotSign, tc.want)
			}
		})
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// TestIsPreRelease pins down that a "+build" suffix alone does not make a
// version a pre-release, but a "-rc.1" style suffix does.
//
// Mutation this kills: `IsPreRelease` unconditionally returning false (or
// true) — both are refuted by having both a positive and negative case in
// the same table.
func TestIsPreRelease(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"1.0.0", false},
		{"1.0.0+build.1", false},
		{"1.0.0-rc.1", true},
		{"2.0.0-rc.1+build.5", true},
	}
	for _, tc := range cases {
		if got := IsPreRelease(tc.v); got != tc.want {
			t.Errorf("IsPreRelease(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// TestLatestVersion_AMD07 table-drives AMD-07's latestVersion pointer
// semantics directly against domain.LatestVersion: the SemVer-maximum
// among versions that are neither blocked nor pre-release.
func TestLatestVersion_AMD07(t *testing.T) {
	vm := func(versions ...string) []VersionMeta {
		out := make([]VersionMeta, len(versions))
		for i, v := range versions {
			out[i] = VersionMeta{Version: v}
		}
		return out
	}
	bv := func(versions ...string) []BlockedVersion {
		out := make([]BlockedVersion, len(versions))
		for i, v := range versions {
			out[i] = BlockedVersion{Version: v}
		}
		return out
	}

	cases := []struct {
		name     string
		versions []VersionMeta
		blocked  []BlockedVersion
		want     string
	}{
		{
			name:     "last-publish-wins bug: 1.9.0 published after 1.10.0 must not become latest",
			versions: vm("1.10.0", "1.9.0"),
			want:     "1.10.0",
		},
		{
			name:     "publish order reversed still picks the semver-max",
			versions: vm("1.9.0", "1.10.0"),
			want:     "1.10.0",
		},
		{
			name:     "pre-release never outranks a released version, even a numerically lower one",
			versions: vm("1.0.0", "2.0.0-rc.1"),
			want:     "1.0.0",
		},
		{
			name:     "legacy hotfix: publishing an older line's patch after a newer major exists must not move the pointer",
			versions: vm("2.0.0", "1.4.3"),
			want:     "2.0.0",
		},
		{
			name:     "build metadata does not affect the winner",
			versions: vm("1.0.0+build.1", "1.2.0"),
			want:     "1.2.0",
		},
		{
			name:     "blocking the current latest promotes the next-highest non-blocked release",
			versions: vm("1.0.0", "2.0.0"),
			blocked:  bv("2.0.0"),
			want:     "1.0.0",
		},
		{
			name:     "all versions blocked: pointer keeps the semver-max instead of going empty",
			versions: vm("1.0.0", "2.0.0"),
			blocked:  bv("1.0.0", "2.0.0"),
			want:     "2.0.0",
		},
		{
			name:     "only pre-releases published, none blocked: pointer falls back to the overall semver-max",
			versions: vm("1.0.0-alpha", "1.0.0-rc.1"),
			want:     "1.0.0-rc.1",
		},
		{
			name:     "unblocking makes a previously-shadowed version eligible again",
			versions: vm("1.0.0", "2.0.0"),
			blocked:  nil, // simulates the state *after* DELETE .../2.0.0/block
			want:     "2.0.0",
		},
		{
			name:     "no versions at all",
			versions: nil,
			want:     "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LatestVersion(tc.versions, tc.blocked)
			if got != tc.want {
				t.Fatalf("LatestVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLatestVersion_NeverNamesAnIncompleteVersion is MAJOR 1's domain-level
// regression test. publish()'s CAS-before-artifacts order (see
// internal/publish.Service.publish's doc comment) means a version can be
// present in a plugin's Versions list — the SemVer-maximum among them, even
// — while its artifacts (jar, manifest, ...) don't exist yet: the plugin.json
// commit and the artifact writes are separate steps, and a crash in between
// leaves exactly that state, with the entry's Complete explicitly false.
// domain.LatestVersion is the single function every writer of the
// latestVersion pointer (publish, block) goes through, so it is where this
// invariant must be enforced once, not re-implemented per caller: it must
// never select a version domain.IsVersionComplete rejects, in any tier —
// not the primary (unblocked, released) tier, and not either fallback.
//
// Mutation this kills: removing the completeness pre-filter from
// LatestVersion (or checking it only in the primary tier and not the
// fallbacks) — each subtest below arms a different tier and would then
// select the higher, incomplete version instead of the lower, complete one.
func TestLatestVersion_NeverNamesAnIncompleteVersion(t *testing.T) {
	complete := true
	incomplete := false

	t.Run("primary tier: unblocked incomplete version loses to a lower complete one", func(t *testing.T) {
		versions := []VersionMeta{
			{Version: "1.0.0", Complete: &complete},
			{Version: "1.1.0", Complete: &incomplete},
		}
		if got := LatestVersion(versions, nil); got != "1.0.0" {
			t.Fatalf("LatestVersion() = %q, want %q (1.1.0 is the SemVer-max but incomplete)", got, "1.0.0")
		}
	})

	t.Run("legacy nil is complete: an old record still outranks an explicitly incomplete newer one", func(t *testing.T) {
		versions := []VersionMeta{
			{Version: "1.0.0"}, // Complete == nil, i.e. a pre-existing legacy record
			{Version: "1.1.0", Complete: &incomplete},
		}
		if got := LatestVersion(versions, nil); got != "1.0.0" {
			t.Fatalf("LatestVersion() = %q, want %q (nil means legacy/complete, explicit false means incomplete)", got, "1.0.0")
		}
	})

	t.Run("blocked-fallback tier: an incomplete version must not be promoted even when everything complete is blocked", func(t *testing.T) {
		versions := []VersionMeta{
			{Version: "1.0.0", Complete: &complete},
			{Version: "1.1.0", Complete: &incomplete},
		}
		blocked := []BlockedVersion{{Version: "1.0.0"}}
		if got := LatestVersion(versions, blocked); got != "1.0.0" {
			t.Fatalf("LatestVersion() = %q, want %q (falling back to the semver-max must still exclude the incomplete 1.1.0)", got, "1.0.0")
		}
	})

	t.Run("no complete version exists at all: pointer must go empty, never pick an incomplete one", func(t *testing.T) {
		versions := []VersionMeta{
			{Version: "1.0.0", Complete: &incomplete},
			{Version: "1.1.0", Complete: &incomplete},
		}
		if got := LatestVersion(versions, nil); got != "" {
			t.Fatalf("LatestVersion() = %q, want %q (every version is incomplete; the pointer must not name any of them)", got, "")
		}
	})
}
