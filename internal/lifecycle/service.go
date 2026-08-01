package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrForbidden = errors.New("forbidden")
)

type Service struct {
	Store       storage.ObjectStore
	Invalidator cdn.Invalidator
	Publisher   *publish.Service
}

func (s *Service) loadPlugin(ctx context.Context, pluginID string) (*domain.PluginState, error) {
	obj, err := s.Store.Read(ctx, storage.PluginStatePath(pluginID))
	if err != nil {
		return nil, err
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Service) SetTier(ctx context.Context, pluginID string, tier domain.TrustTier) (*domain.PluginState, error) {
	if tier != domain.TierOfficial {
		return nil, ErrForbidden
	}
	var out domain.PluginState
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		if len(data) == 0 {
			return nil, ErrNotFound
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		if out.Removed != nil {
			return nil, ErrNotFound
		}
		out.Tier = tier
		return json.MarshalIndent(out, "", "  ")
	}, 5)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = s.Publisher.RebuildIndex(ctx)
	_ = s.Invalidator.Invalidate(ctx, []string{"/" + storage.PathIndex})
	return &out, nil
}

func (s *Service) BlockVersion(ctx context.Context, pluginID, version, reason string) (*domain.BlockedVersion, error) {
	now := time.Now().UTC()
	var blocked domain.BlockedVersion
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		var st domain.PluginState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		if st.Removed != nil {
			return nil, ErrNotFound
		}
		found := false
		for _, v := range st.Versions {
			if v.Version == version {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrNotFound
		}
		for _, bv := range st.BlockedVersions {
			if bv.Version == version {
				return nil, ErrConflict
			}
		}
		blocked = domain.BlockedVersion{Version: version, BlockedAt: now, Reason: reason}
		st.BlockedVersions = append(st.BlockedVersions, blocked)
		// AMD-07: recompute latestVersion on block too, not just on
		// publish -- otherwise blocking the version currently advertised
		// as latest leaves it advertised forever even though nobody can
		// install it.
		st.LatestVersion = domain.LatestVersion(st.Versions, st.BlockedVersions)
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}
	// AMD-07 / §6.4 invalidation matrix (Block version row) names three
	// paths: /index.json, /plugins/{id}/plugin.json, and
	// /plugins/{id}/versions/{ver}/*. index.json is among the GCS paths
	// written and invalidated by a block, not just the plugin's own
	// plugin.json -- the catalogue listing is composed from index.json, so
	// without this the blocked-but-still-latest version keeps being
	// advertised there even after plugin.json is corrected. Mirrors
	// SetTier/RemovePlugin's call below: best-effort, not treated as fatal
	// to the block itself (rebuildIndex has its own retry/failure surface).
	//
	// The version's own paths must be invalidated too: their manifest and
	// jar are served with a long, immutable Cache-Control
	// (max-age=31536000), so without an explicit invalidation a CDN edge
	// that already cached them keeps serving the blocked version's metadata
	// and artifact indefinitely -- letting a client resolve and download
	// exactly the version the registry just blocked, defeating the point of
	// blocking it.
	_ = s.Publisher.RebuildIndex(ctx)
	_ = s.Invalidator.Invalidate(ctx, []string{
		"/" + storage.PathIndex,
		"/" + storage.PluginStatePath(pluginID),
		"/" + storage.VersionPrefix(pluginID, version) + "*",
	})
	return &blocked, nil
}

func (s *Service) RemovePlugin(ctx context.Context, pluginID, reason, removedBy string) (*domain.PluginTombstone, error) {
	now := time.Now().UTC()
	tomb := domain.PluginTombstone{Removed: now, RemovalReason: reason, RemovedBy: removedBy}
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		var st domain.PluginState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		if st.Removed != nil {
			return nil, ErrConflict
		}
		st.Removed = &now
		st.RemovalReason = reason
		st.RemovedBy = removedBy
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}

	files, _ := s.Store.ListPrefix(ctx, storage.PluginPrefix(pluginID))
	for _, f := range files {
		if strings.HasSuffix(f, "plugin.json") {
			continue
		}
		_ = s.Store.Delete(ctx, f)
	}

	_ = s.Publisher.RebuildIndex(ctx)
	_ = s.Invalidator.Invalidate(ctx, []string{"/" + storage.PathIndex, "/" + storage.PluginStatePath(pluginID)})
	return &tomb, nil
}

func (s *Service) AttachAdvisory(ctx context.Context, pluginID, version string, sev domain.AdvisorySeverity, text string) (*domain.SecurityAdvisory, error) {
	now := time.Now().UTC()
	adv := domain.SecurityAdvisory{Severity: sev, Text: text, AttachedAt: now}
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		var st domain.PluginState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		if st.Removed != nil {
			return nil, ErrNotFound
		}
		found := false
		for _, v := range st.Versions {
			if v.Version == version {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrNotFound
		}
		if st.VersionStates == nil {
			st.VersionStates = map[string]domain.VersionState{}
		}
		st.VersionStates[version] = domain.VersionState{Advisory: &adv}
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}
	_ = s.Invalidator.Invalidate(ctx, []string{"/" + storage.PluginStatePath(pluginID)})
	return &adv, nil
}

func (s *Service) StartOrphanCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanupOrphans(ctx)
			}
		}
	}()
}

func (s *Service) cleanupOrphans(ctx context.Context) {
	files, err := s.Store.ListPrefix(ctx, "plugins/")
	if err != nil {
		return
	}
	for _, f := range files {
		if !strings.Contains(f, "/versions/") {
			continue
		}
		parts := strings.Split(f, "/")
		if len(parts) < 4 {
			continue
		}
		pluginID := parts[1]
		version := parts[3]
		st, err := s.loadPlugin(ctx, pluginID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				_ = s.Store.Delete(ctx, f)
			}
			continue
		}
		if st.Removed != nil {
			continue
		}
		listed := false
		for _, v := range st.Versions {
			if v.Version == version {
				listed = true
				break
			}
		}
		if !listed {
			_ = s.Store.Delete(ctx, f)
		}
	}
}
