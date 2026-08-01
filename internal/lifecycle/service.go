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

// SetTier changes a plugin's trust tier. Its downstream housekeeping (index
// rebuild, CDN invalidation) is best-effort and reported via the returned
// HousekeepingOutcome rather than folded into the error return: by the time
// housekeeping runs, the tier change has already been durably committed to
// plugin.json, so a housekeeping failure must never be reported the same
// way a failure of the primary write would be (see HousekeepingOutcome's
// doc comment).
func (s *Service) SetTier(ctx context.Context, pluginID string, tier domain.TrustTier) (*domain.PluginState, HousekeepingOutcome, error) {
	if tier != domain.TierOfficial {
		return nil, HousekeepingOutcome{}, ErrForbidden
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
			return nil, HousekeepingOutcome{}, ErrNotFound
		}
		return nil, HousekeepingOutcome{}, err
	}
	hk := s.runHousekeeping(ctx, pluginID, "set_tier", true, []string{"/" + storage.PathIndex})
	return &out, hk, nil
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
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}
	_ = s.Invalidator.Invalidate(ctx, []string{"/" + storage.PluginStatePath(pluginID)})
	return &blocked, nil
}

// RemovePlugin tombstones a plugin and hard-deletes its version artifacts.
// As with SetTier, downstream housekeeping (index rebuild, CDN invalidation)
// is best-effort and reported via the returned HousekeepingOutcome: the
// tombstone is already durably committed to plugin.json by the time
// housekeeping runs, so a housekeeping failure must never make this call
// report as though the removal itself failed.
func (s *Service) RemovePlugin(ctx context.Context, pluginID, reason, removedBy string) (*domain.PluginTombstone, HousekeepingOutcome, error) {
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
		return nil, HousekeepingOutcome{}, err
	}

	files, _ := s.Store.ListPrefix(ctx, storage.PluginPrefix(pluginID))
	for _, f := range files {
		if strings.HasSuffix(f, "plugin.json") {
			continue
		}
		_ = s.Store.Delete(ctx, f)
	}

	hk := s.runHousekeeping(ctx, pluginID, "remove_plugin", true, []string{"/" + storage.PathIndex, "/" + storage.PluginStatePath(pluginID)})
	return &tomb, hk, nil
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
