package lifecycle

import (
	"context"
	"testing"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

type recordingInvalidator struct {
	paths []string
}

func (r *recordingInvalidator) Invalidate(_ context.Context, paths []string) error {
	r.paths = append(r.paths, paths...)
	return nil
}

func TestSetCeilingInvalidatesTheSameSurfacesAsABlock(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.example", "secret")
	if err != nil {
		t.Fatal(err)
	}
	pub := &publish.Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
	rec := &recordingInvalidator{}
	life := &Service{Store: store, Invalidator: rec, Publisher: pub}

	m := &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "1.4.2", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryOther, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pub.PublishFirst(ctx, &publish.Bundle{JAR: jar, JARFilename: "plugin.jar"}, "op"); err != nil {
		t.Fatal(err)
	}

	if _, err := life.SetCompatibilityCeiling(ctx, m.ID, m.Version, "<27.0", "RP 27.0 removes ExtensionPoint X", "operator@example.com"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/" + storage.PluginStatePath(m.ID),
		"/" + storage.PathIndex,
		"/" + storage.VersionPrefix(m.ID, m.Version) + "*",
	}
	if len(rec.paths) != len(want) {
		t.Fatalf("invalidated %v, want %v", rec.paths, want)
	}
	for i := range want {
		if rec.paths[i] != want[i] {
			t.Fatalf("invalidated[%d] = %q, want %q", i, rec.paths[i], want[i])
		}
	}
}
