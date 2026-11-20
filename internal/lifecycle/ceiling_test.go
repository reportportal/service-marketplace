package lifecycle

import (
	"context"
	"errors"
	"strings"
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

func publishWithRange(t *testing.T, rpRange string) (*Service, *domain.Manifest) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir(), "http://cdn.example", "secret")
	if err != nil {
		t.Fatal(err)
	}
	pub := &publish.Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
	m := &domain.Manifest{
		ID: "plugin-demo", Name: "Demo", Version: "1.4.2", Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryOther, Compatibility: domain.Compatibility{ReportPortal: rpRange},
		Access: domain.AccessPublic,
	}
	jar, err := publish.BuildTestJAR(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pub.PublishFirst(ctx, &publish.Bundle{JAR: jar, JARFilename: "plugin.jar"}, "op"); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: store, Invalidator: cdn.NoopInvalidator{}, Publisher: pub}, m
}

func TestCeilingReasonLengthCountsCharacters(t *testing.T) {
	life, m := publishWithRange(t, ">=25.1")
	ctx := context.Background()
	if _, err := life.SetCompatibilityCeiling(ctx, m.ID, m.Version, "<27.0", strings.Repeat("ж", 2000), "op"); err != nil {
		t.Fatalf("2000 multi-byte characters: %v", err)
	}
	_, err := life.SetCompatibilityCeiling(ctx, m.ID, m.Version, "<27.0", strings.Repeat("ж", 2001), "op")
	var fv FieldViolation
	if !errors.As(err, &fv) || fv.Field != "reason" {
		t.Fatalf("2001 characters err = %v, want reason violation", err)
	}
}

func TestCeilingAuthorRangeMessages(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct{ name, author, wantSub string }{
		{"caret", "^25.1", `own range "^25.1" can't take a ceiling`},
		{"space AND", ">=25.1 <27.5", `own range ">=25.1 <27.5" can't take a ceiling`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			life, m := publishWithRange(t, tt.author)
			_, err := life.SetCompatibilityCeiling(ctx, m.ID, m.Version, "<27.0", "reason", "op")
			var fv FieldViolation
			if !errors.As(err, &fv) || !strings.Contains(fv.Message, tt.wantSub) {
				t.Fatalf("err = %v, want message containing %q", err, tt.wantSub)
			}
		})
	}
}

func TestCeilingViolationEmptyAuthorRange(t *testing.T) {
	var fv FieldViolation
	if !errors.As(ceilingViolation(domain.ErrNoAuthorRange, ""), &fv) || !strings.Contains(fv.Message, "no author compatibility range") {
		t.Fatalf("violation = %v", fv)
	}
}
