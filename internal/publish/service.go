package publish

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

const (
	maxEntries         = 10000
	maxManifestBytes   = 1 << 20
	maxScreenshotBytes = 2 << 20
	maxScreenshots     = 5
)

var (
	ErrValidation   = errors.New("validation error")
	ErrConflict     = errors.New("conflict")
	ErrNotFound     = errors.New("not found")
	ErrPayloadLarge = errors.New("payload too large")
)

type ValidationErrors struct {
	Errors []domain.ValidationError
}

func (e ValidationErrors) Error() string {
	return "validation failed"
}

type Bundle struct {
	JAR          []byte
	JARFilename  string
	Changelog    []byte
	Screenshots  map[string][]byte
}

type Result struct {
	PluginID string `json:"pluginId"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
}

type Service struct {
	Store       storage.ObjectStore
	Invalidator cdn.Invalidator
}

func (s *Service) ParseMultipart(r *multipart.Reader) (*Bundle, error) {
	b := &Bundle{Screenshots: map[string][]byte{}}
	for {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := part.FormName()
		filename := part.FileName()
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		switch name {
		case "jar":
			if !strings.HasSuffix(strings.ToLower(filename), ".jar") {
				return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "jar", Message: "jar file required"}}}
			}
			b.JAR = data
			b.JARFilename = filename
		case "changelog":
			b.Changelog = data
		case "screenshots":
			if err := addScreenshot(b, filename, data); err != nil {
				return nil, err
			}
		default:
			if strings.HasPrefix(name, "screenshots") {
				if err := addScreenshot(b, filename, data); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(b.JAR) == 0 {
		return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "jar", Message: "jar file required"}}}
	}
	return b, nil
}

func addScreenshot(b *Bundle, filename string, data []byte) error {
	if len(b.Screenshots) >= maxScreenshots {
		return ValidationErrors{Errors: []domain.ValidationError{{Field: "screenshots", Message: "at most 5 screenshots"}}}
	}
	if len(data) > maxScreenshotBytes {
		return ErrPayloadLarge
	}
	safe, err := storage.SanitizeScreenshotFilename(filename)
	if err != nil {
		return ValidationErrors{Errors: []domain.ValidationError{{Field: "screenshots", Message: "invalid screenshot filename"}}}
	}
	ext := strings.ToLower(filepath.Ext(safe))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		return ValidationErrors{Errors: []domain.ValidationError{{Field: "screenshots", Message: "PNG/JPEG only"}}}
	}
	b.Screenshots[safe] = data
	return nil
}

func ExtractManifest(jarData []byte) (*domain.Manifest, error) {
	zr, err := zip.NewReader(bytes.NewReader(jarData), int64(len(jarData)))
	if err != nil {
		return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "jar", Message: "invalid jar/zip archive"}}}
	}
	var manifestData []byte
	var entries int
	for _, f := range zr.File {
		entries++
		if entries > maxEntries {
			return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "jar", Message: "too many archive entries"}}}
		}
		if f.Name == "marketplace-manifest.json" || strings.HasSuffix(f.Name, "/marketplace-manifest.json") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			limited := io.LimitReader(rc, maxManifestBytes+1)
			manifestData, err = io.ReadAll(limited)
			rc.Close()
			if err != nil {
				return nil, err
			}
			if len(manifestData) > maxManifestBytes {
				return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "manifest", Message: "manifest exceeds 1MB"}}}
			}
		}
	}
	if manifestData == nil {
		return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "manifest", Message: "marketplace-manifest.json not found"}}}
	}
	var m domain.Manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "manifest", Message: "invalid manifest json"}}}
	}
	if m.Access == "" {
		m.Access = domain.AccessPublic
	}
	if errs := domain.ValidateManifest(&m); len(errs) > 0 {
		return nil, ValidationErrors{Errors: errs}
	}
	return &m, nil
}

func (s *Service) PublishFirst(ctx context.Context, bundle *Bundle, operator string) (*Result, error) {
	m, err := ExtractManifest(bundle.JAR)
	if err != nil {
		return nil, err
	}
	if _, err := s.Store.Read(ctx, storage.PluginStatePath(m.ID)); err == nil {
		return nil, ErrConflict
	} else if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	return s.publish(ctx, m, bundle, operator, true)
}

// PublishVersion publishes a new version for pluginID (FR-OP-02). When
// autoCreate is true and no plugin.json exists yet for pluginID, it creates
// the plugin entry (tier: official) as part of writing this version instead
// of returning ErrNotFound — AMD-15-ci-first-publish / D-05 (adopted
// "auto-create"): an allow-listed GitHub Actions OIDC publish to a
// not-yet-existing pluginId is the sanctioned first-CI-publish path, since
// the publishOidcTrust allow-list is already an operator-curated grant. The
// caller (handlePublishVersion) sets autoCreate only once it has confirmed
// the request is OIDC-authenticated AND allow-listed for this exact
// pluginID; an operator-session call always passes false, so a session
// publishing to a not-yet-existing plugin still 404s — first publish via the
// Operator UI goes through PublishFirst instead.
func (s *Service) PublishVersion(ctx context.Context, pluginID string, bundle *Bundle, operator string, autoCreate bool) (*Result, error) {
	m, err := ExtractManifest(bundle.JAR)
	if err != nil {
		return nil, err
	}
	if m.ID != pluginID {
		return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "manifest.id", Message: "Manifest id does not match URL pluginId"}}}
	}
	stObj, err := s.Store.Read(ctx, storage.PluginStatePath(pluginID))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			if autoCreate {
				// s.publish's WriteWithRetry mutator already initializes a
				// fresh domain.PluginState{Tier: TierOfficial} when no prior
				// plugin.json exists (see below), so auto-create needs no
				// separate creation step here — just skip the 404.
				return s.publish(ctx, m, bundle, operator, true)
			}
			return nil, ErrNotFound
		}
		return nil, err
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		return nil, err
	}
	if st.Removed != nil {
		return nil, ErrNotFound
	}
	for _, v := range st.Versions {
		if v.Version == m.Version {
			if ok, _ := s.Store.Exists(ctx, storage.VersionArtifactPath(pluginID, m.Version, string(m.Access))); ok {
				return nil, ErrConflict
			}
		}
	}
	return s.publish(ctx, m, bundle, operator, false)
}

func (s *Service) publish(ctx context.Context, m *domain.Manifest, bundle *Bundle, operator string, first bool) (*Result, error) {
	sha := storage.HashSHA256(bundle.JAR)
	artPath := storage.VersionArtifactPath(m.ID, m.Version, string(m.Access))
	if _, err := s.Store.Write(ctx, artPath, bundle.JAR, 0); err != nil && !errors.Is(err, storage.ErrConflict) {
		return nil, err
	}
	manifestBytes, _ := json.MarshalIndent(m, "", "  ")
	if _, err := s.Store.Write(ctx, storage.VersionManifestPath(m.ID, m.Version), manifestBytes, 0); err != nil {
		return nil, err
	}
	if len(bundle.Changelog) > 0 {
		if _, err := s.Store.Write(ctx, storage.VersionChangelogPath(m.ID, m.Version), bundle.Changelog, 0); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(bundle.Screenshots))
	for name := range bundle.Screenshots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path, err := storage.VersionScreenshotPath(m.ID, m.Version, name)
		if err != nil {
			return nil, ValidationErrors{Errors: []domain.ValidationError{{Field: "screenshots", Message: err.Error()}}}
		}
		if _, err := s.Store.Write(ctx, path, bundle.Screenshots[name], 0); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(m.ID), func(data []byte, gen int64) ([]byte, error) {
		var st domain.PluginState
		if len(data) > 0 {
			if err := json.Unmarshal(data, &st); err != nil {
				return nil, err
			}
		} else {
			st = domain.PluginState{ID: m.ID, Tier: domain.TierOfficial, Versions: []domain.VersionMeta{}}
		}
		found := false
		for i, v := range st.Versions {
			if v.Version == m.Version {
				st.Versions[i].SHA256 = sha
				st.Versions[i].PublishedAt = now
				found = true
				break
			}
		}
		if !found {
			st.Versions = append(st.Versions, domain.VersionMeta{Version: m.Version, PublishedAt: now, SHA256: sha})
		}
		st.LatestVersion = m.Version
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}

	err = s.rebuildIndex(ctx)
	if err != nil {
		return nil, err
	}

	paths := []string{"/" + storage.PathIndex, "/" + storage.PluginStatePath(m.ID)}
	_ = s.Invalidator.Invalidate(ctx, paths)

	return &Result{PluginID: m.ID, Version: m.Version, SHA256: sha}, nil
}

func (s *Service) RebuildIndex(ctx context.Context) error {
	return s.rebuildIndex(ctx)
}

func (s *Service) rebuildIndex(ctx context.Context) error {
	pluginDirs, err := s.Store.ListPrefix(ctx, "plugins/")
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	var plugins []domain.IndexPlugin
	for _, p := range pluginDirs {
		parts := strings.Split(p, "/")
		if len(parts) < 2 {
			continue
		}
		id := parts[1]
		if _, ok := seen[id]; ok {
			continue
		}
		if !strings.HasSuffix(p, "plugin.json") {
			continue
		}
		seen[id] = struct{}{}
		obj, err := s.Store.Read(ctx, storage.PluginStatePath(id))
		if err != nil {
			continue
		}
		var st domain.PluginState
		if err := json.Unmarshal(obj.Data, &st); err != nil || st.Removed != nil || st.LatestVersion == "" {
			continue
		}
		mObj, err := s.Store.Read(ctx, storage.VersionManifestPath(id, st.LatestVersion))
		if err != nil {
			continue
		}
		var m domain.Manifest
		if err := json.Unmarshal(mObj.Data, &m); err != nil {
			continue
		}
		plugins = append(plugins, domain.IndexPlugin{
			ID:            id,
			Name:          m.Name,
			LatestVersion: st.LatestVersion,
			Description:   m.Description,
			Category:      m.Category,
			Access:        m.Access,
			Tier:          st.Tier,
		})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	idx := domain.Index{Plugins: plugins}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return storage.WriteWithRetry(ctx, s.Store, storage.PathIndex, func(existing []byte, gen int64) ([]byte, error) {
		return data, nil
	}, 5)
}

func BuildTestJAR(m *domain.Manifest) ([]byte, error) {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	w, err := zw.Create("marketplace-manifest.json")
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(w).Encode(m); err != nil {
		return nil, err
	}
	w2, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprint(w2, "Manifest-Version: 1.0\n")
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
