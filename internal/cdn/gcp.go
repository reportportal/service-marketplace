package cdn

import (
	"context"
	"fmt"
	"log"
	"strings"

	compute "google.golang.org/api/compute/v1"
)

type GCPInvalidator struct {
	URLMap  string
	Project string
	Logger  *log.Logger
}

func (g *GCPInvalidator) Invalidate(ctx context.Context, paths []string) error {
	if g.Logger == nil {
		g.Logger = log.Default()
	}
	if g.URLMap == "" {
		g.Logger.Printf("cdn: invalidation skipped (CDN_URL_MAP not configured), paths=%v", paths)
		return nil
	}
	if g.Project == "" {
		g.Logger.Printf("cdn: invalidation logged (no GCP_PROJECT), urlMap=%s paths=%v", g.URLMap, paths)
		return nil
	}
	svc, err := compute.NewService(ctx)
	if err != nil {
		return fmt.Errorf("compute client: %w", err)
	}
	hostRules := make([]*compute.CacheInvalidationRule, 0, len(paths))
	for _, p := range paths {
		path := p
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		hostRules = append(hostRules, &compute.CacheInvalidationRule{Path: path})
	}
	req := &compute.CacheInvalidationRule{}
	if len(hostRules) == 1 {
		req = hostRules[0]
	} else {
		// Invalidate each path; GCP accepts one path per call for UrlMaps.InvalidateCache.
		for _, rule := range hostRules {
			op, err := svc.UrlMaps.InvalidateCache(g.Project, g.URLMap, rule).Context(ctx).Do()
			if err != nil {
				return fmt.Errorf("invalidate %s: %w", rule.Path, err)
			}
			g.Logger.Printf("cdn: invalidation started urlMap=%s path=%s op=%s", g.URLMap, rule.Path, op.Name)
		}
		return nil
	}
	op, err := svc.UrlMaps.InvalidateCache(g.Project, g.URLMap, req).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("invalidate: %w", err)
	}
	g.Logger.Printf("cdn: invalidation started urlMap=%s path=%s op=%s", g.URLMap, req.Path, op.Name)
	return nil
}
