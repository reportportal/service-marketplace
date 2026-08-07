package domain

import "testing"

func TestValidateManifest(t *testing.T) {
	m := &Manifest{
		ID:            "plugin-test",
		Name:          "Test Plugin",
		Version:       "1.0.0",
		Description:   "desc",
		Author:        Author{Name: "Author"},
		License:       "Apache-2.0",
		Category:      CategoryBugTracking,
		Compatibility: Compatibility{ReportPortal: ">=25.1"},
		Access:        AccessPublic,
	}
	if errs := ValidateManifest(m); len(errs) != 0 {
		t.Fatalf("expected valid manifest, got %v", errs)
	}

	m.Access = AccessPremium
	if errs := ValidateManifest(m); len(errs) == 0 {
		t.Fatal("expected contactUrl required for premium")
	}
	m.ContactURL = "https://example.com/pricing"
	if errs := ValidateManifest(m); len(errs) != 0 {
		t.Fatalf("expected valid premium manifest, got %v", errs)
	}

	m.ID = "INVALID"
	if errs := ValidateManifest(m); len(errs) == 0 {
		t.Fatal("expected invalid id error")
	}
}

func TestValidateVersion(t *testing.T) {
	if ValidateVersion("1.0.0") != nil {
		t.Fatal("expected valid version")
	}
	if ValidateVersion("1.0../0") == nil {
		t.Fatal("expected invalid version with ..")
	}
}
