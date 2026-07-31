package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

var ErrNotFound = errors.New("not found")

type Service struct {
	Store storage.ObjectStore
}

func (s *Service) loadIndex(ctx context.Context) (*domain.Index, error) {
	obj, err := s.Store.Read(ctx, storage.PathIndex)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return &domain.Index{Plugins: []domain.IndexPlugin{}}, nil
		}
		return nil, err
	}
	var idx domain.Index
	if err := json.Unmarshal(obj.Data, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
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

func (s *Service) loadManifest(ctx context.Context, pluginID, version string) (*domain.Manifest, error) {
	obj, err := s.Store.Read(ctx, storage.VersionManifestPath(pluginID, version))
	if err != nil {
		return nil, err
	}
	var m domain.Manifest
	if err := json.Unmarshal(obj.Data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Service) ListPlugins(ctx context.Context, category, search string) ([]domain.IndexPlugin, error) {
	idx, err := s.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.IndexPlugin, 0, len(idx.Plugins))
	search = strings.ToLower(strings.TrimSpace(search))
	for _, p := range idx.Plugins {
		if category != "" && string(p.Category) != category {
			continue
		}
		if search != "" {
			hay := strings.ToLower(p.Name + " " + p.Description + " " + p.ID)
			if !strings.Contains(hay, search) {
				continue
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) GetPlugin(ctx context.Context, pluginID string) (*domain.Manifest, *domain.PluginState, error) {
	st, err := s.loadPlugin(ctx, pluginID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	if st.Removed != nil {
		return nil, st, nil
	}
	if st.LatestVersion == "" {
		return nil, st, ErrNotFound
	}
	m, err := s.loadManifest(ctx, pluginID, st.LatestVersion)
	if err != nil {
		return nil, st, err
	}
	return m, st, nil
}

func (s *Service) ListVersions(ctx context.Context, pluginID string) (*domain.PluginState, error) {
	st, err := s.loadPlugin(ctx, pluginID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return st, nil
}

type VersionDetail struct {
	Manifest       domain.Manifest
	Tier           domain.TrustTier
	Blocked        bool
	BlockedAt      *time.Time
	BlockReason    string
	Advisory       *domain.SecurityAdvisory
	SHA256         string
	ChangelogURL   *string
	ScreenshotURLs []string
}

func (s *Service) GetVersion(ctx context.Context, pluginID, version string) (*VersionDetail, *domain.PluginState, error) {
	st, err := s.loadPlugin(ctx, pluginID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	if st.Removed != nil {
		return nil, st, nil
	}
	m, err := s.loadManifest(ctx, pluginID, version)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, st, ErrNotFound
		}
		return nil, st, err
	}
	detail := &VersionDetail{
		Manifest: *m,
		Tier:     st.Tier,
	}
	for _, bv := range st.BlockedVersions {
		if bv.Version == version {
			detail.Blocked = true
			t := bv.BlockedAt
			detail.BlockedAt = &t
			detail.BlockReason = bv.Reason
			break
		}
	}
	if st.VersionStates != nil {
		if vs, ok := st.VersionStates[version]; ok && vs.Advisory != nil {
			detail.Advisory = vs.Advisory
		}
	}
	artPath := storage.VersionArtifactPath(pluginID, version)
	if obj, err := s.Store.Read(ctx, artPath); err == nil {
		detail.SHA256 = storage.HashSHA256(obj.Data)
	}
	if ok, _ := s.Store.Exists(ctx, storage.VersionChangelogPath(pluginID, version)); ok {
		u := s.Store.PublicURL(storage.VersionChangelogPath(pluginID, version))
		detail.ChangelogURL = &u
	}
	prefix := storage.VersionPrefix(pluginID, version) + "screenshots/"
	files, _ := s.Store.ListPrefix(ctx, prefix)
	sort.Strings(files)
	for _, f := range files {
		detail.ScreenshotURLs = append(detail.ScreenshotURLs, s.Store.PublicURL(f))
	}
	return detail, st, nil
}

func TombstoneFromState(st *domain.PluginState) domain.PluginTombstone {
	return domain.PluginTombstone{
		Removed:       *st.Removed,
		RemovalReason: st.RemovalReason,
		RemovedBy:     st.RemovedBy,
	}
}
