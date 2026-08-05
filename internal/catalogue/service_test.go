package catalogue

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

func TestListPluginsFilter(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "http://cdn", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	idx := domain.Index{Plugins: []domain.IndexPlugin{
		{ID: "plugin-a", Name: "Alpha", LatestVersion: "1.0.0", Description: "first", Category: domain.CategoryBugTracking, Access: domain.AccessPublic, Tier: domain.TierOfficial},
		{ID: "plugin-b", Name: "Beta Notify", LatestVersion: "2.0.0", Description: "alerts", Category: domain.CategoryNotifications, Access: domain.AccessPremium, Tier: domain.TierOfficial},
	}}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(ctx, storage.PathIndex, data, 0); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: store}

	all, err := svc.ListPlugins(ctx, "", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 plugins, got %d err=%v", len(all), err)
	}
	cat, err := svc.ListPlugins(ctx, "notifications", "")
	if err != nil || len(cat) != 1 || cat[0].ID != "plugin-b" {
		t.Fatalf("category filter failed: %+v err=%v", cat, err)
	}
	search, err := svc.ListPlugins(ctx, "", "alpha")
	if err != nil || len(search) != 1 || search[0].ID != "plugin-a" {
		t.Fatalf("search filter failed: %+v err=%v", search, err)
	}
}
