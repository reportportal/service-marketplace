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

// ErrVersionNotFound is returned by GetVersion when the PLUGIN exists (its
// plugin.json is readable and it is not tombstoned) but the requested
// version does not exist as far as any client read path is concerned --
// either because it is genuinely absent from st.Versions, or because it is
// present but committed-but-incomplete (domain.IsVersionComplete false; see
// that function's doc comment). It is distinct from ErrNotFound (the plugin
// itself doesn't exist) so callers can tell "Plugin not found" from "Version
// not found" apart -- see handlers_plugins.go's handleGetVersion, which used
// to collapse both into "Plugin not found" for a plugin that demonstrably
// does exist.
var ErrVersionNotFound = errors.New("version not found")

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

// loadPlugin is the client read boundary's single chokepoint: internal/
// catalogue is imported only by cmd/marketplace/main.go and internal/
// httpapi (never by internal/publish or internal/lifecycle, which each keep
// their own private, unfiltered loadPlugin reading the same plugin.json), so
// enforcing "a committed-but-incomplete version does not exist" here -- by
// dropping every VersionMeta the store's write side committed but never
// finished (domain.IsVersionComplete false) -- reaches every client-facing
// read (ListVersions, GetVersion, GetPlugin via st.LatestVersion, and
// handleGetArtifact, which reaches the store only through those two) without
// touching publish's or lifecycle's own view of the full, unfiltered
// history they need for healing and AMD-04 duplicate-publish checks.
func (s *Service) loadPlugin(ctx context.Context, pluginID string) (*domain.PluginState, error) {
	obj, err := s.Store.Read(ctx, storage.PluginStatePath(pluginID))
	if err != nil {
		return nil, err
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		return nil, err
	}
	complete := make([]domain.VersionMeta, 0, len(st.Versions))
	for _, v := range st.Versions {
		if domain.IsVersionComplete(v) {
			complete = append(complete, v)
		}
	}
	st.Versions = complete
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
	// Visibility is decided by the committed document (st.Versions, already
	// filtered to complete entries by loadPlugin), not by which bytes happen
	// to exist in storage: a manifest object can exist for a version that is
	// committed-but-incomplete (crashed before markVersionComplete) or even
	// for a stray orphan never recorded in plugin.json at all, and neither
	// may be served.
	found := false
	for _, v := range st.Versions {
		if v.Version == version {
			found = true
			break
		}
	}
	if !found {
		return nil, st, ErrVersionNotFound
	}
	m, err := s.loadManifest(ctx, pluginID, version)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, st, ErrVersionNotFound
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
	artPath := storage.VersionArtifactPath(pluginID, version, string(detail.Manifest.Access))
	if obj, err := s.Store.Read(ctx, artPath); err == nil {
		detail.SHA256 = storage.HashSHA256(obj.Data)
	} else {
		for _, v := range st.Versions {
			if v.Version == version && v.SHA256 != "" {
				detail.SHA256 = v.SHA256
				break
			}
		}
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
