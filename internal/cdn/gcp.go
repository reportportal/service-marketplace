package cdn

import (
	"context"
	"fmt"
	"log"
	"strings"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// GCPInvalidator purges CDN-cached content via Cloud Compute's
// UrlMaps.InvalidateCache. Every path passed to Invalidate is forwarded
// verbatim (see Invalidator's doc comment on the wildcard shape) — this type
// must never reject a path just because it ends in "/*"; Cloud CDN's
// invalidateCache API accepts that trailing wildcard form directly, and
// every publish, block and remove call passes exactly such a path as their
// last, best-effort step after their real write has already committed (see
// internal/lifecycle/service.go and internal/publish/service.go). Compare
// the Java original's GcpCdnInvalidationService, which threw
// IllegalArgumentException on any path containing "/*" and turned every one
// of those committed mutations into a 500 on the gcs profile before that was
// fixed (commit fa03885, "Accept wildcard CDN paths instead of 500-ing
// committed mutations") — internal/cdn/contract_test.go pins the Go port
// against regressing the same way.
type GCPInvalidator struct {
	URLMap  string
	Project string
	Logger  *log.Logger

	// ClientOptions is appended to the options compute.NewService is built
	// with. Nil (the production default) changes nothing — NewService is
	// called exactly as before. Tests use it to point the Compute client at
	// an httptest server instead of the real API (option.WithEndpoint +
	// option.WithHTTPClient + option.WithoutAuthentication), which is the
	// only way to exercise the real UrlMaps.InvalidateCache request path
	// without live GCP credentials.
	ClientOptions []option.ClientOption
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
	svc, err := compute.NewService(ctx, g.ClientOptions...)
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
