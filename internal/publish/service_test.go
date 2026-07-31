package publish

import (
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
)

func TestExtractManifest(t *testing.T) {
	m := &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryOther, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	jar, err := BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExtractManifest(jar)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Version != m.Version {
		t.Fatalf("unexpected manifest: %+v", got)
	}
}
