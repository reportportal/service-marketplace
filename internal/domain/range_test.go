package domain

import (
	"errors"
	"testing"
)

func TestEffectiveReportPortalRange(t *testing.T) {
	tests := []struct {
		name    string
		author  string
		ceiling string
		want    string
		err     error
	}{
		{name: "no ceiling copies the author range", author: ">=25.1", want: ">=25.1"},
		{name: "open author range gains the ceiling", author: ">=25.1", ceiling: "<27.0", want: ">=25.1, <27.0"},
		{name: "inclusive ceiling", author: ">=25.1", ceiling: "<=27.0", want: ">=25.1, <=27.0"},
		{name: "looser ceiling leaves the author spelling", author: ">=25.1, <26.0", ceiling: "<27.0", want: ">=25.1, <26.0"},
		{name: "tighter ceiling replaces the author upper bound", author: ">=25.1, <28.0", ceiling: "<27.0", want: ">=25.1, <27.0"},
		{name: "exclusive ceiling replaces an inclusive author bound", author: ">=25.1, <=27.0", ceiling: "<27.0", want: ">=25.1, <27.0"},
		{name: "floor comparator is kept", author: ">25.1", ceiling: "<27.0", want: ">25.1, <27.0"},
		{name: "pinned version inside the ceiling is unchanged", author: "=25.5.0", ceiling: "<=25.5.0", want: "=25.5.0"},
		{name: "pinned version under a higher ceiling is unchanged", author: "=25.5.0", ceiling: "<26.0", want: "=25.5.0"},
		{name: "ceiling equal to the author upper bound is unchanged", author: ">=25.1, <27.0", ceiling: "<27.0", want: ">=25.1, <27.0"},
		{name: "patch calver", author: ">=25.1", ceiling: "<25.1.2", want: ">=25.1, <25.1.2"},
		{name: "ceiling on the floor is empty", author: ">=27.0", ceiling: "<27.0", err: ErrEmptyCeilingIntersection},
		{name: "ceiling below the floor is empty", author: ">=25.1, <26.0", ceiling: "<25.1", err: ErrEmptyCeilingIntersection},
		{name: "pinned version excluded", author: "=25.5.0", ceiling: "<25.5.0", err: ErrEmptyCeilingIntersection},
		{name: "not an upper bound", author: ">=25.1", ceiling: ">=27.0", err: ErrInvalidCeilingBound},
		{name: "major only", author: ">=25.1", ceiling: "<27", err: ErrInvalidCeilingBound},
		{name: "two bounds", author: ">=25.1", ceiling: "<27.0, <28.0", err: ErrInvalidCeilingBound},
		{name: "prerelease ceiling", author: ">=25.1", ceiling: "<27.0.0-rc.1", err: ErrInvalidCeilingBound},
		{name: "missing author range", ceiling: "<27.0", err: ErrNoAuthorRange},
		{name: "unreadable author range", author: "25.1 - 26.0", ceiling: "<27.0", err: ErrUnreadableAuthorRange},
		{name: "caret author range", author: "^25.1", ceiling: "<27.0", err: ErrUnreadableAuthorRange},
		{name: "space-AND author range", author: ">=25.1 <27.0", ceiling: "<27.0", err: ErrUnreadableAuthorRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EffectiveReportPortalRange(tt.author, tt.ceiling)
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("EffectiveReportPortalRange(%q, %q) err = %v, want %v", tt.author, tt.ceiling, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("EffectiveReportPortalRange(%q, %q): %v", tt.author, tt.ceiling, err)
			}
			if got != tt.want {
				t.Fatalf("EffectiveReportPortalRange(%q, %q) = %q, want %q", tt.author, tt.ceiling, got, tt.want)
			}
		})
	}
}

func TestCanonicalCeilingSpacing(t *testing.T) {
	if got, err := CanonicalCeiling("  <=27.0 "); err != nil || got != "<=27.0" {
		t.Fatalf("CanonicalCeiling trimmed = %q, %v", got, err)
	}
	if _, err := CanonicalCeiling("< 27.0"); !errors.Is(err, ErrInvalidCeilingBound) {
		t.Fatalf("CanonicalCeiling with inner space err = %v, want ErrInvalidCeilingBound", err)
	}
}
