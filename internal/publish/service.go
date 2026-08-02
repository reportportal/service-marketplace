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
	ErrValidation = errors.New("validation error")
	ErrNotFound   = errors.New("not found")
	// ErrPluginExists is returned by PublishFirst when plugins/{id}/plugin.json
	// already exists with removed == nil (AMD-04-duplicate-publish-contract):
	// "POST /api/v1/plugins for an id whose plugin.json exists with
	// removed == null -> 409 Conflict, code PLUGIN_ALREADY_EXISTS, directing
	// the caller to POST /plugins/{id}/versions."
	ErrPluginExists = errors.New("plugin already exists")
	// ErrVersionConflict is returned by PublishVersion for AMD-04 branch 3: a
	// committed version republished with a different SHA-256.
	ErrVersionConflict = errors.New("version already published with different content")
	// ErrRemoved is the sentinel errors.Is(err, ErrRemoved) matches against;
	// callers that need the tombstone payload use errors.As with *RemovedError.
	ErrRemoved      = errors.New("plugin removed")
	ErrPayloadLarge = errors.New("payload too large")
)

// RemovedError carries the tombstone of a plugin that is removed, for every
// write that AMD-06-removal-lifecycle gates behind 410: "Writes against a
// tombstoned plugin: POST .../versions, block/unblock, PATCH tier, and
// advisory operations return 410 with the tombstone payload." The one
// exception is POST /api/v1/plugins itself, which never returns this error —
// it is the resurrection path (see PublishFirst).
type RemovedError struct {
	Tombstone domain.PluginTombstone
}

func (e *RemovedError) Error() string { return "plugin is removed" }

// Is lets callers write errors.Is(err, ErrRemoved) without importing
// *RemovedError, mirroring the sentinel-first convention used elsewhere in
// this package (ErrNotFound, ErrPluginExists, ...).
func (e *RemovedError) Is(target error) bool { return target == ErrRemoved }

type ValidationErrors struct {
	Errors []domain.ValidationError
}

func (e ValidationErrors) Error() string {
	return "validation failed"
}

type Bundle struct {
	JAR         []byte
	JARFilename string
	Changelog   []byte
	Screenshots map[string][]byte
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
	obj, err := s.Store.Read(ctx, storage.PluginStatePath(m.ID))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// This outer Read is only a fast-path belief that the id is
			// free; it can be stale by the time publish()'s own
			// compare-and-swap runs (a competing create/resurrect may win
			// the race in the gap). firstPublish=true makes every CAS
			// attempt re-derive "already exists" from what it actually
			// reads instead of trusting this Read.
			return s.publish(ctx, m, bundle, operator, true)
		}
		return nil, err
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		return nil, err
	}
	if st.Removed == nil {
		return nil, ErrPluginExists
	}
	// AMD-06-removal-lifecycle / D-06 (adopted): "POST /api/v1/plugins
	// (operator session JWT only) is the explicit resurrection path: it
	// resets removed/removalReason/removedBy to null, publishes the
	// uploaded version per the §6.4 write order, and regenerates
	// index.json." This route is gated by requireSessionRejectOIDC
	// (router.go, AMD-02/AMD-15), so a GitHub OIDC token never reaches this
	// branch — a compromised CI token cannot resurrect a plugin an operator
	// deliberately removed. PublishVersion (the OIDC-reachable auto-create
	// path) deliberately never resurrects; see its own Removed handling.
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
//
// The bool return is true iff this call resolved AMD-04-duplicate-publish-
// contract's idempotent branch (a committed version republished with
// byte-identical content) — the caller maps that to 200 instead of 201, and
// no storage object is written on that path.
//
// AMD-06-removal-lifecycle: a tombstoned plugin is never auto-created into
// and never resurrected by this route — resurrection is exclusively
// PublishFirst's job (D-06: "explicitly NOT through the CI auto-create
// path"). A tombstoned plugin.json returns *RemovedError (410) here
// regardless of autoCreate/caller type.
func (s *Service) PublishVersion(ctx context.Context, pluginID string, bundle *Bundle, operator string, autoCreate bool) (*Result, bool, error) {
	m, err := ExtractManifest(bundle.JAR)
	if err != nil {
		return nil, false, err
	}
	if m.ID != pluginID {
		return nil, false, ValidationErrors{Errors: []domain.ValidationError{{Field: "manifest.id", Message: "Manifest id does not match URL pluginId"}}}
	}
	stObj, err := s.Store.Read(ctx, storage.PluginStatePath(pluginID))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			if autoCreate {
				// s.publish's WriteWithRetry mutator already initializes a
				// fresh domain.PluginState{Tier: TierOfficial} when no prior
				// plugin.json exists (see below), so auto-create needs no
				// separate creation step here — just skip the 404.
				res, err := s.publish(ctx, m, bundle, operator, false)
				return res, false, err
			}
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	var st domain.PluginState
	if err := json.Unmarshal(stObj.Data, &st); err != nil {
		return nil, false, err
	}
	if st.Removed != nil {
		return nil, false, &RemovedError{Tombstone: domain.PluginTombstone{
			Removed: *st.Removed, RemovalReason: st.RemovalReason, RemovedBy: st.RemovedBy,
		}}
	}

	// AMD-04-duplicate-publish-contract three-branch rule, using the
	// commit-point definition corrected by docs/decisions/AMD-30-commit-
	// point-granularity.md (proposed as AMD-30 in requirements/
	// AMENDMENTS-v1.md; not applied there directly because requirements/ is
	// untracked outside the primary checkout — see that file for why and
	// for the exact amendment text to paste in). AMD-04's amendment text as
	// currently written defines "committed" as "referenced by index.json",
	// but index.json (domain.IndexPlugin) only ever carries a plugin's
	// single latestVersion pointer — it cannot answer "is version 1.4.3
	// committed" once a later version is latest, since it never lists more
	// than one version per plugin. Read literally, every non-latest version
	// would permanently lose FR-R-05's immutability guarantee the instant a
	// newer version publishes. This codebase instead persists the full
	// per-version history in plugin.json (domain.PluginState.Versions),
	// checked and committed at or before the point index.json would have
	// been consulted — never later — so it is the durable, always-
	// consultable record "committed" is checked against here; see
	// docs/decisions/AMD-30-commit-point-granularity.md for the full
	// analysis of what breaks under the literal reading and why this is not
	// merely a comment-argued deviation.
	sha := storage.HashSHA256(bundle.JAR)
	healIdenticalCommit := false
	for _, v := range st.Versions {
		if v.Version != m.Version {
			continue
		}
		if v.SHA256 == sha {
			// Branch 2: committed + identical content -> idempotent 200, no
			// objects written -- PROVIDED the artifact this entry claims to
			// commit actually exists. publish()'s CAS-before-artifacts order
			// (see its doc comment) makes plugin.json's commit and the
			// artifact write two separate steps, so a crash in between can
			// leave a committed entry with no bytes at its path; this is the
			// one case that fast path must not short-circuit past, or the
			// version would be unreachable forever (no PublishVersion call
			// would ever be allowed to write the missing bytes again).
			artPath := storage.VersionArtifactPath(pluginID, m.Version, string(m.Access))
			exists, existsErr := s.Store.Exists(ctx, artPath)
			if existsErr != nil {
				return nil, false, existsErr
			}
			if exists {
				return &Result{PluginID: pluginID, Version: m.Version, SHA256: sha}, true, nil
			}
			healIdenticalCommit = true
			break
		}
		// Branch 3: committed + different content -> 409.
		return nil, false, ErrVersionConflict
	}
	// Branch 1 (not committed) or a same-content heal of a committed entry
	// whose artifact write never completed — either way publish()'s own CAS
	// re-derives the decision fresh; healIdenticalCommit only changes
	// whether the caller sees this as the AMD-04 branch-2 idempotent
	// outcome (still true here — identical content) or a fresh branch-1
	// write.
	res, err := s.publish(ctx, m, bundle, operator, false)
	return res, healIdenticalCommit, err
}

// publish decides AMD-04-duplicate-publish-contract's branch — and, for
// firstPublish, resurrection/already-exists — via the plugin.json
// compare-and-swap FIRST, and only then writes the per-version artifacts
// (jar, manifest, changelog, screenshots), then regenerates index.json.
//
// The CAS-before-artifacts order is deliberate and is itself the fix for a
// finding that survived two rounds: this function used to write the
// artifact bytes unconditionally, before ever consulting plugin.json, on
// the theory that the caller had already established the version was not
// committed. That belief can go stale in the gap between the caller's read
// and this call — a competing publish can commit the same version, with
// different content, in between — and a losing attempt's blind artifact
// overwrite could land AFTER the winner's commit, corrupting the
// supposedly-immutable bytes even though this same CAS then (correctly)
// discovered the conflict and returned ErrVersionConflict. Asserting only
// that error/status code let the bug through review twice; the fix is
// ordering, not the decision logic, which was already correct. Now: every
// attempt's decision is re-derived from a fresh read (as before), but a
// losing attempt returns straight out of the CAS — as ErrVersionConflict,
// ErrPluginExists, or *RemovedError — before this function has touched a
// single artifact byte, so there is no window left for its content to land
// on top of an already-committed version. See
// TestPublishConcurrentLoserNeverOverwritesWinnersCommittedArtifactBytes.
//
// writeArtifacts is set by the CAS callback and tells the caller-side code
// below whether THIS successful CAS attempt is the one that needs the
// artifacts (re)written:
//
//   - branch 1 (not committed) always sets it — this is the "overwrite any
//     orphaned per-version objects... regardless of byte-equality with the
//     partial state" case AMD-04 requires, using upsertObject's blind
//     overwrite exactly as before, just moved after the commit instead of
//     before it.
//   - branch 2 (committed, identical content) leaves it false — AMD-04
//     "no objects are written" — UNLESS the artifact is physically missing,
//     which can only happen if a previous attempt's own CAS committed the
//     version but crashed before finishing its artifact writes; healing
//     that is safe here specifically because this attempt's SHA-256 is
//     already confirmed equal to the committed one, so it can never be a
//     route for different content to slip past branch 3 below.
//   - branch 3 (committed, different content) and every ErrPluginExists /
//     *RemovedError path return an error from WriteWithRetry with no
//     successful write at all, so writeArtifacts is never consulted.
//
// firstPublish is true only for PublishFirst's two call sites and carries
// that route's whole contract into the compare-and-swap loop, re-derived
// from what each attempt actually reads rather than trusted from the
// caller's earlier, possibly-stale Read:
//
//   - tombstoned (st.Removed != nil) -> unconditionally resurrect (clear
//     Removed/RemovalReason/RemovedBy) as part of the same CAS that records
//     the new version. AMD-06-removal-lifecycle / D-06: PublishFirst never
//     returns ErrRemoved, so this applies whether the tombstone was already
//     there when the attempt started or landed mid-flight.
//   - live (st.Removed == nil) but plugin.json already has content -> the id
//     already exists; AMD-04-duplicate-publish-contract requires 409
//     ErrPluginExists here, even if the caller's own outer Read raced and
//     saw ErrNotFound (ANOTHER publish created the id in the gap between
//     that Read and this attempt/retry).
//
// When firstPublish is false (PublishVersion's branch-1/auto-create calls),
// the CAS instead aborts with *RemovedError the moment any attempt observes
// a tombstone — the TOCTOU the go-assessment flagged: "PublishVersion
// checks the removed flag once at the top of the flow, but its own
// plugin.json compare-and-swap callback never re-checks it".
//
// Independent of firstPublish, every attempt also re-derives AMD-04's
// committed-version decision (branches 1/2/3) from what it just read: a
// caller may have seen "not committed" once, before the loop, but a
// competing publish can commit that exact version — with different content
// — in the gap before a retry's write lands. Reusing the caller's stale
// belief here instead of re-checking would silently overwrite a
// now-immutable committed version instead of returning ErrVersionConflict.
func (s *Service) publish(ctx context.Context, m *domain.Manifest, bundle *Bundle, operator string, firstPublish bool) (*Result, error) {
	sha := storage.HashSHA256(bundle.JAR)
	now := time.Now().UTC()

	var writeArtifacts bool
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(m.ID), func(data []byte, gen int64) ([]byte, error) {
		writeArtifacts = false
		var st domain.PluginState
		if len(data) > 0 {
			if err := json.Unmarshal(data, &st); err != nil {
				return nil, err
			}
		} else {
			st = domain.PluginState{ID: m.ID, Tier: domain.TierOfficial, Versions: []domain.VersionMeta{}}
		}
		if firstPublish {
			if st.Removed != nil {
				// AMD-06/D-06: PublishFirst always resurrects, never
				// returns ErrRemoved — whether the tombstone was already
				// there or a racing removal/resurrection landed it since
				// the last read.
				st.Removed = nil
				st.RemovalReason = ""
				st.RemovedBy = ""
			} else if len(data) > 0 {
				// The id already has a live plugin.json. The caller's own
				// pre-check may have seen ErrNotFound before a competing
				// create/resurrect won the race; re-derive "already
				// exists" from this attempt's own read, not that stale
				// belief.
				return nil, ErrPluginExists
			}
		} else if st.Removed != nil {
			return nil, &RemovedError{Tombstone: domain.PluginTombstone{
				Removed: *st.Removed, RemovalReason: st.RemovalReason, RemovedBy: st.RemovedBy,
			}}
		}
		found := false
		for i, v := range st.Versions {
			if v.Version != m.Version {
				continue
			}
			found = true
			if v.SHA256 == sha {
				// Already committed with byte-identical content — the
				// caller's own AMD-04 branch-2 fast path normally catches
				// this before ever calling publish(), so reaching it here
				// means a retry discovered a competing publish of the
				// exact same content landed in the gap. Idempotent: keep
				// the entry, just refresh PublishedAt. writeArtifacts stays
				// false; the caller below only writes if the artifact is
				// actually missing.
				st.Versions[i].PublishedAt = now
				break
			}
			// Committed with DIFFERENT content: a competing publish won
			// this version in the gap between the caller's "not
			// committed" decision and this attempt/retry. AMD-04 branch 3
			// requires a conflict here, not silently overwriting the
			// checksum a concurrent writer already committed.
			return nil, ErrVersionConflict
		}
		if !found {
			st.Versions = append(st.Versions, domain.VersionMeta{Version: m.Version, PublishedAt: now, SHA256: sha})
			writeArtifacts = true
		}
		st.LatestVersion = m.Version
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}

	artPath := storage.VersionArtifactPath(m.ID, m.Version, string(m.Access))
	if !writeArtifacts {
		// Branch 2 landed: heal a previous attempt's commit whose own
		// artifact write never completed (e.g. crashed between its CAS
		// above and its artifact writes below). Only reachable when this
		// attempt's SHA-256 already matched the committed one, so the bytes
		// written here can never differ from what's already recorded.
		exists, existsErr := s.Store.Exists(ctx, artPath)
		if existsErr != nil {
			return nil, existsErr
		}
		writeArtifacts = !exists
	}

	if writeArtifacts {
		if err := upsertObject(ctx, s.Store, artPath, bundle.JAR); err != nil {
			return nil, err
		}
		manifestBytes, _ := json.MarshalIndent(m, "", "  ")
		if err := upsertObject(ctx, s.Store, storage.VersionManifestPath(m.ID, m.Version), manifestBytes); err != nil {
			return nil, err
		}
		if len(bundle.Changelog) > 0 {
			if err := upsertObject(ctx, s.Store, storage.VersionChangelogPath(m.ID, m.Version), bundle.Changelog); err != nil {
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
			if err := upsertObject(ctx, s.Store, path, bundle.Screenshots[name]); err != nil {
				return nil, err
			}
		}
	}

	err = s.rebuildIndex(ctx)
	if err != nil {
		return nil, err
	}

	paths := []string{"/" + storage.PathIndex, "/" + storage.PluginStatePath(m.ID)}
	_ = s.Invalidator.Invalidate(ctx, paths)

	return &Result{PluginID: m.ID, Version: m.Version, SHA256: sha}, nil
}

// upsertObject overwrites path with data unconditionally, whatever
// generation currently exists there (including none). Every caller in this
// file has already established the target version is not committed, so an
// existing object at path is by definition an orphan from an interrupted
// earlier attempt — AMD-04-duplicate-publish-contract branch 1 requires it
// be overwritten, not silently kept (the old create-only Write(..., 0) here
// swallowed storage.ErrConflict for the jar only, which kept stale/possibly
// truncated bytes on a retry while the manifest write below it had no such
// swallow and just failed outright).
func upsertObject(ctx context.Context, store storage.ObjectStore, path string, data []byte) error {
	return storage.WriteWithRetry(ctx, store, path, func([]byte, int64) ([]byte, error) {
		return data, nil
	}, 5)
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
