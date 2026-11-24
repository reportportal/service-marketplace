package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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

// SetTier records a plugin's provenance.
//
// Only `official` is accepted, and that is ADR-003 rather than an oversight: `partner` is reserved
// in the schema so published entries need no migration when its onboarding arrives, but the
// workflow that would earn a plugin that tier is Phase 3. A tier this registry cannot decide how to
// grant is one it must not let an operator assert by hand.
//
// So `domain.TierPartner` existing while this refuses it is the intended state, not a gap. If that
// changes, it changes here and in the onboarding that justifies it — together.
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
		// Blocking the newest build is the one thing that makes "latest" go down, and the listing
		// has to follow it: the artifact route answers 403 for a blocked version, so a catalogue
		// still naming it offers an install that can only fail.
		st.LatestVersion = domain.LatestInstallableVersion(st.Versions, st.BlockedVersions)
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}
	// The index carries latestVersion and the range declared by it, so it is stale the moment the
	// line above moves. Invalidating the state alone left the catalogue offering the blocked build
	// until the next unrelated publish happened to rebuild.
	if s.Publisher != nil {
		if err := s.Publisher.RebuildIndex(ctx); err != nil {
			return nil, err
		}
	}
	_ = s.Invalidator.Invalidate(ctx, []string{
		"/" + storage.PluginStatePath(pluginID),
		"/" + storage.PathIndex,
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

// RestorePlugin lifts a removal, so the id can be published to again.
//
// It reopens the id, not the plugin. Removal hard-deleted every version's artifacts and kept only
// plugin.json (RemovePlugin), so there is nothing to bring back: the state is reset to "known id,
// no versions" and the plugin reappears in the catalogue on its next publish. Keeping the old
// version list would have the catalogue and the version endpoints naming builds whose manifests
// and jars no longer exist.
//
// What survives is the operator's own record: the tier, and the blocked versions. A version
// number that was blocked stays blocked if it is published again — restoring an id must not be a
// way to put a withdrawn build back on offer.
//
// Not idempotent: restoring a plugin that is not removed is ErrConflict rather than a quiet
// success, so two operators acting on the same row do not both believe they did it.
func (s *Service) RestorePlugin(ctx context.Context, pluginID, restoredBy string) (*domain.PluginRestored, error) {
	now := time.Now().UTC()
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		if len(data) == 0 {
			return nil, ErrNotFound
		}
		var st domain.PluginState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		if st.Removed == nil {
			return nil, ErrConflict
		}
		st.Removed = nil
		st.RemovalReason = ""
		st.RemovedBy = ""
		st.Versions = []domain.VersionMeta{}
		st.LatestVersion = ""
		st.VersionStates = nil
		st.Restored = &now
		st.RestoredBy = restoredBy
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}
	// The index never listed a removed plugin and a restored one has no version to list, so it is
	// unchanged; only the state document a CDN may still be serving as a tombstone is stale.
	_ = s.Invalidator.Invalidate(ctx, []string{"/" + storage.PluginStatePath(pluginID)})
	return &domain.PluginRestored{ID: pluginID, Restored: now, RestoredBy: restoredBy}, nil
}

// ListRemoved returns every removed plugin, newest removal first.
//
// The public catalogue deliberately leaves these out (the index is rebuilt without them), so the
// only other way to see one is to already know its id and ask for it. This reads the plugin
// states themselves, which is also why it is an operator route: a public list would announce the
// id of every plugin withdrawn for a legal or abuse reason.
//
// A state that cannot be decoded is skipped, as the index rebuild skips it; failing the whole
// list for one damaged document would hide the rest.
func (s *Service) ListRemoved(ctx context.Context) ([]domain.RemovedPlugin, error) {
	files, err := s.Store.ListPrefix(ctx, "plugins/")
	if err != nil {
		return nil, err
	}
	out := []domain.RemovedPlugin{}
	for _, f := range files {
		parts := strings.Split(f, "/")
		if len(parts) != 3 || parts[2] != "plugin.json" {
			continue
		}
		obj, err := s.Store.Read(ctx, f)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return nil, err
		}
		var st domain.PluginState
		if err := json.Unmarshal(obj.Data, &st); err != nil || st.Removed == nil {
			continue
		}
		out = append(out, domain.RemovedPlugin{
			ID:            parts[1],
			Removed:       *st.Removed,
			RemovalReason: st.RemovalReason,
			RemovedBy:     st.RemovedBy,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Removed.Equal(out[j].Removed) {
			return out[i].Removed.After(out[j].Removed)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
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
