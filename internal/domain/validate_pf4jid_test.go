package domain

import (
	"strings"
	"testing"
)

// realPF4JPluginIDs is the complete set of Plugin-Id values the official plugins ship
// today (requirements/integration/STAGE0-id-mapping-decision.md). Every one of them must
// validate: a rejection here is a registry bug, not a test to relax.
var realPF4JPluginIDs = []string{
	"Azure DevOps", "JIRA Cloud", "GitLab", "Monday", "RobotFramework",
	"GitHub", "github", "jira", "rally", "junit", "mobitru", "saucelabs",
	"slack", "telegram", "quality gate", "test-execution",
}

// pf4jId carries the plugin's identity in PF4J, not in the registry, so the registry's
// own id rule must NOT be applied to it — not even to the ids that happen to look like
// one. "quality gate" is the case that proves the rule is never applied selectively: it
// is lowercase and hyphen-free like a registry id, yet contains a space.
func TestValidatePF4JIDAcceptsRealPluginIDs(t *testing.T) {
	for _, id := range append(realPF4JPluginIDs, strings.Repeat("a", 64)) {
		if ve := ValidatePF4JID(id); ve != nil {
			t.Errorf("ValidatePF4JID(%q) = %v, want nil", id, ve)
		}
	}
}

func TestValidatePF4JIDRejectsUnsafeValues(t *testing.T) {
	cases := map[string]string{
		"empty":                "",
		"over 64 chars":        strings.Repeat("a", 65),
		"newline (log inject)": "GitHub\nBUILD SUCCESSFUL",
		"tab":                  "Git\tHub",
		"NUL":                  "GitHub\x00",
		"carriage return":      "GitHub\r",
		"forward slash":        "bts/github",
		"backslash":            `Azure\DevOps`,
		"parent directory":     "..",
		"traversal segment":    "../../etc/passwd",
		"leading space":        " GitHub",
		"trailing space":       "GitHub ",
		"zero-width space":     "Git​Hub",
		"cyrillic homoglyph":   "Сloud", // looks like "Cloud", never matches it
	}
	for name, id := range cases {
		ve := ValidatePF4JID(id)
		if ve == nil {
			t.Errorf("%s: ValidatePF4JID(%q) = nil, want a validation error", name, id)
			continue
		}
		if ve.Field != "pf4jId" {
			t.Errorf("%s: error field = %q, want %q", name, ve.Field, "pf4jId")
		}
	}
}

func validPF4JIDManifest() *Manifest {
	return &Manifest{
		ID:            "plugin-bts-azure",
		Name:          "Azure DevOps",
		Version:       "1.0.0",
		Description:   "desc",
		Author:        Author{Name: "Author"},
		License:       "Apache-2.0",
		Category:      CategoryBugTracking,
		Compatibility: Compatibility{ReportPortal: ">=25.1"},
		Access:        AccessPublic,
	}
}

// Absent (nil) means "no pf4jId declared" and stays valid — manifests published before
// the field existed must keep publishing.
func TestValidateManifestWithoutPF4JIDStaysValid(t *testing.T) {
	m := validPF4JIDManifest()
	if errs := ValidateManifest(m); len(errs) != 0 {
		t.Fatalf("manifest without pf4jId: got %v, want no errors", errs)
	}
	if m.PF4JID != nil {
		t.Errorf("ValidateManifest invented a pf4jId: %q", *m.PF4JID)
	}
}

func TestValidateManifestPF4JID(t *testing.T) {
	t.Run("valid declared value passes", func(t *testing.T) {
		m := validPF4JIDManifest()
		id := "Azure DevOps"
		m.PF4JID = &id
		if errs := ValidateManifest(m); len(errs) != 0 {
			t.Fatalf("got %v, want no errors", errs)
		}
	})

	// Declared-but-empty is rejected rather than silently treated as undeclared, so
	// "no pf4jId" has exactly one representation on the wire: the key is absent.
	for name, id := range map[string]string{
		"empty string":  "",
		"blank":         "   ",
		"over 64 chars": strings.Repeat("a", 65),
		"newline":       "GitHub\nx",
		"slash":         "bts/github",
	} {
		t.Run(name+" is rejected", func(t *testing.T) {
			m := validPF4JIDManifest()
			m.PF4JID = &id
			errs := ValidateManifest(m)
			found := false
			for _, e := range errs {
				if e.Field == "manifest.pf4jId" {
					found = true
				}
			}
			if !found {
				t.Fatalf("pf4jId %q: got %v, want an error on field manifest.pf4jId", id, errs)
			}
		})
	}
}
