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
			// objects written -- PROVIDED every object this entry's commit
			// claims (jar, manifest, and any changelog/screenshots the
			// original publish included) actually exists. publish()'s
			// CAS-before-artifacts order (see its doc comment) makes
			// plugin.json's commit and those writes separate steps, so a
			// crash at any point between the commit and the LAST of those
			// writes can leave a committed entry that is missing some (not
			// necessarily just the jar) of its objects.
			//
			// domain.IsVersionComplete is the authoritative answer to "is
			// this version whole": Complete is set true only by a dedicated
			// follow-up compare-and-swap (markVersionComplete) that runs
			// after every object write for this version has already
			// succeeded, so it can never be true while something is still
			// missing -- and nil (a legacy record predating this field) is
			// provably whole by construction, per VersionMeta.Complete's doc
			// comment. Checking it here needs no storage round-trip at all,
			// and — unlike a "does the jar exist" probe — does not depend on
			// the healer somehow knowing which optional objects (changelog,
			// screenshots) the ORIGINAL interrupted attempt's bundle
			// happened to include.
			if domain.IsVersionComplete(v) {
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
//     before it. The new entry starts with Complete: false; it is only set
//     true once every write below has succeeded (see markVersionComplete).
//   - branch 2 (committed, identical content) leaves it false — AMD-04
//     "no objects are written" — UNLESS the committed entry's own
//     Complete flag is false, which can only happen if a previous attempt's
//     own CAS committed the version but crashed before every one of its
//     writes (jar, manifest, changelog, screenshots, AND the follow-up
//     Complete-marking CAS) finished; healing that is safe here specifically
//     because this attempt's SHA-256 is already confirmed equal to the
//     committed one, so it can never be a route for different content to
//     slip past branch 3 below. See markVersionComplete's doc comment for
//     why a flag on the commit record, not a storage existence probe,
//     answers "is this version whole".
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
				// the entry, just refresh PublishedAt. writeArtifacts is
				// only set here if the entry isn't Complete yet — i.e. a
				// previous attempt committed this version but crashed
				// before every one of its writes finished; see
				// markVersionComplete for what fully clears that flag.
				st.Versions[i].PublishedAt = now
				if !domain.IsVersionComplete(st.Versions[i]) {
					writeArtifacts = true
				}
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
			st.Versions = append(st.Versions, domain.VersionMeta{Version: m.Version, PublishedAt: now, SHA256: sha, Complete: boolPtr(false)})
			writeArtifacts = true
		}
		// AMD-07's latestVersion recompute (domain.LatestVersion) does NOT
		// happen here, deliberately -- see MAJOR 1 in the branch report.
		// This CAS can commit a brand-new or healing version's record
		// (SHA256, Complete: false) before a single artifact byte for it
		// exists; setting LatestVersion to the result of a recompute run
		// against THAT state could name a version domain.LatestVersion
		// itself would reject as incomplete if the crash landed one CAS
		// later than that recompute. The recompute instead runs inside
		// markVersionComplete's CAS, atomically with flipping this entry's
		// Complete to true, so there is never a moment where LatestVersion
		// names a version whose artifacts are still missing. See that
		// function's doc comment.
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		return nil, err
	}

	// writeArtifacts is now exactly "the CAS above found this version's
	// commit record not yet Complete" (see the mutator), which is the
	// authoritative answer to "does this version still need its objects
	// (re)written" — no storage existence probe needed, and no risk of the
	// probe being wrong about which objects a partial attempt actually
	// finished (see markVersionComplete's doc comment).
	artPath := storage.VersionArtifactPath(m.ID, m.Version, string(m.Access))
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
		// Every object this version comprises has now been written
		// successfully. Record that fact on the commit itself so a future
		// healing decision never again has to guess it from storage state —
		// see markVersionComplete's doc comment for why this is a separate,
		// necessary CAS rather than something ordering alone can fold into
		// the writes above.
		if err := s.markVersionComplete(ctx, m.ID, m.Version); err != nil {
			return nil, err
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
// boolPtr returns a pointer to b. Used for domain.VersionMeta.Complete,
// which is a tri-state *bool (nil/true/false all mean different things —
// see its doc comment) so every write this package makes must produce an
// explicit, non-nil pointer rather than the field's nil zero value.
func boolPtr(b bool) *bool { return &b }

func upsertObject(ctx context.Context, store storage.ObjectStore, path string, data []byte) error {
	return storage.WriteWithRetry(ctx, store, path, func([]byte, int64) ([]byte, error) {
		return data, nil
	}, 5)
}

// markVersionComplete flips the named version's VersionMeta.Complete to true
// via its own compare-and-swap on plugin.json, run by publish() only after
// every object that version comprises (jar, manifest, and any changelog/
// screenshots the bundle included) has already been written successfully.
//
// This exists because ordering the writes differently cannot, by itself,
// close the crash window this function closes. publish() must keep
// committing plugin.json BEFORE any artifact byte is written — reordering
// that back is reverting the prior BLOCKING fix (see publish()'s doc
// comment) and reopens the loser-overwrites-committed-bytes hole. But
// whichever object is written LAST in the sequence that follows the commit
// is, by construction, exactly as vulnerable as the jar was before this fix:
// a crash after it still leaves nothing to distinguish "every object landed"
// from "the commit merely started" — reordering only relocates the same gap
// to a different step, and the set of objects a version comprises isn't even
// fixed (changelog and screenshots are optional per bundle), so a healer
// can't reliably infer completeness later from "which objects exist" without
// somehow also knowing what the ORIGINAL interrupted attempt's bundle
// contained.
//
// A durable completion marker sidesteps both problems: it is written once,
// deliberately, after this attempt's own writes (the only ones it needs to
// reason about) have all succeeded, and every future healing decision reads
// it directly instead of re-deriving it from partial, bundle-dependent
// storage state. See domain.VersionMeta.Complete and this file's use of it
// in PublishVersion and publish()'s CAS callback.
//
// A crash between the last artifact write succeeding and this CAS landing
// leaves Complete false even though every object is already correct; that is
// safe and self-healing, not a new failure mode — the next same-content
// republish takes the healing branch again, blindly (but harmlessly)
// re-writes the already-correct bytes, and retries this same CAS. See
// TestPublishVersionHealsAfterCrashDuringCompletionMarkerWrite.
//
// This CAS is also where AMD-07's latestVersion recompute (domain.
// LatestVersion) happens — MAJOR 1 (branch report). publish()'s own CAS
// deliberately does NOT set LatestVersion (see its doc comment): it commits
// a version's record before that version's artifacts exist, so recomputing
// there could name a version that isn't actually installable yet. Folding
// the recompute into THIS CAS instead means the pointer and this entry's
// Complete flip from false to true in the exact same atomic write — there is
// no intermediate state where LatestVersion could observe "Complete just
// became true" without also observing the recomputed pointer, or vice versa.
// That makes the bad state MAJOR 1 fixes structurally unreachable, not just
// less likely: LatestVersion can never name a version domain.LatestVersion
// itself would reject as incomplete, because the only place it is ever
// written is a CAS that runs after — and atomically with — that version (or
// whichever version domain.LatestVersion selects instead) being marked
// complete.
func (s *Service) markVersionComplete(ctx context.Context, pluginID, version string) error {
	return storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		var st domain.PluginState
		if len(data) > 0 {
			if err := json.Unmarshal(data, &st); err != nil {
				return nil, err
			}
		}
		for i := range st.Versions {
			if st.Versions[i].Version == version {
				st.Versions[i].Complete = boolPtr(true)
				break
			}
		}
		st.LatestVersion = domain.LatestVersion(st.Versions, st.BlockedVersions)
		return json.MarshalIndent(st, "", "  ")
	}, 5)
}

func (s *Service) RebuildIndex(ctx context.Context) error {
	return s.rebuildIndex(ctx)
}

// rebuildIndex recomputes index.json from every plugins/{id}/plugin.json in
// storage and CAS-writes it via storage.WriteWithRetry. It either writes a
// document it can vouch for in full (domain.Index.Complete == true, every
// non-removed plugin represented with its real Versions set) or it writes
// nothing at all and returns an error -- it never writes a partial index.
//
// That "all or nothing" rule is deliberate, not an oversight the old
// silently-`continue`-past-errors version had: a rebuild that omits a real,
// non-removed plugin because its plugin.json happened to be unreadable this
// one tick is indistinguishable, from index.json's own contents, from that
// plugin having been legitimately removed -- and
// internal/lifecycle.OrphanCleanup treats "absent from index.json" as proof
// a version is safe to delete. Writing such a document would be actively
// worse than writing nothing: leaving the previous (older, but honest)
// index.json in place keeps protecting that plugin's versions until whatever
// made its plugin.json unreadable is fixed and a rebuild can complete
// cleanly. "Carry the plugin through as unresolved" (fabricating an entry
// from a storage listing instead) was considered and rejected for this exact
// failure: without a readable plugin.json there is no verified version list
// for that plugin to carry through, and reconstructing one from a raw
// directory listing would just be a second, less-audited orphan-detector
// duplicating what OrphanCleanup itself already does against index.json.
//
// Completeness alone is not currency (the BLOCKING fix this comment
// documents). domain.Index.Complete attests only that the rebuild which
// PRODUCED a document resolved every plugin *it saw* -- it says nothing
// about whether that snapshot is still current by the time the document
// lands, and two replicas' rebuildIndex calls (triggered by two unrelated,
// concurrent publishes) can legitimately race the same index.json. The
// previous version called storage.WriteWithRetry with a closure that ignored
// its (existing, gen) arguments and always resubmitted one already-computed
// snapshot: on a CAS conflict (another replica's index.json commit already
// landed) it would retry the write with the SAME stale bytes against the new
// generation -- and could win that second race, silently regressing
// index.json to a snapshot that no longer references the other replica's
// just-committed version. That document still decodes Complete: true, so
// OrphanCleanup has no way to tell it apart from a genuinely current one:
// the dropped version reads as unreferenced and, once MinAge elapses, gets
// swept. That is the in-flight-publish data loss AMD-27 exists to prevent,
// reached through index.json's own write path instead of past the sweep's
// age guard -- "the reference data was honest once" is not the same claim as
// "the reference data is honest now".
//
// The fix: buildIndexData below re-lists and re-reads storage from scratch
// on every WriteWithRetry attempt, not just once before entering the retry
// loop. A CAS conflict therefore forces a fresh re-derivation of what
// "complete" means before the next write attempt, so whichever attempt
// finally wins the CAS race has, by construction, observed the storage state
// that caused it to need a retry in the first place -- the standard
// optimistic-concurrency argument that makes the storage layer's existing
// CAS primitive sufficient here without inventing a second coordination
// mechanism (a lease-guarded rebuild, option considered and rejected: it
// would add its own contention/expiry machinery to solve exactly the problem
// CAS-with-recompute already solves for free). The cost is that a rebuild
// contended by concurrent writers can re-enumerate and re-read the entire
// plugins/ tree up to maxAttempts times instead of once; at catalogue scale
// (~20 plugins, per the plan) that is cheap, and it only happens under
// genuine contention, not on the uncontended common path.
func (s *Service) rebuildIndex(ctx context.Context) error {
	return storage.WriteWithRetry(ctx, s.Store, storage.PathIndex, func(_ []byte, _ int64) ([]byte, error) {
		return s.buildIndexData(ctx)
	}, 5)
}

// buildIndexData scans storage fresh -- every call, not memoized -- and
// returns the marshalled index.json bytes it can vouch for in full, or an
// error if any non-removed plugin could not be resolved. See rebuildIndex's
// doc comment for why it must be re-run on every CAS retry attempt rather
// than computed once.
func (s *Service) buildIndexData(ctx context.Context) ([]byte, error) {
	pluginDirs, err := s.Store.ListPrefix(ctx, "plugins/")
	if err != nil {
		return nil, err
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
			return nil, fmt.Errorf("rebuildIndex: plugin %q: plugin.json unreadable, refusing to write a partial index: %w", id, err)
		}
		var st domain.PluginState
		if err := json.Unmarshal(obj.Data, &st); err != nil {
			return nil, fmt.Errorf("rebuildIndex: plugin %q: plugin.json unparseable, refusing to write a partial index: %w", id, err)
		}
		if st.Removed != nil {
			// Legitimate exclusion, not a failure: RemovePlugin already
			// hard-deletes every non-plugin.json artifact as part of the
			// primary write, before housekeeping (and therefore this rebuild)
			// ever runs, so there is nothing left for this plugin to
			// reference.
			continue
		}
		if len(st.Versions) == 0 {
			// Genuinely versionless: nothing published yet, nothing in
			// storage at risk. Legitimate exclusion.
			continue
		}
		// NOT PUBLISHED YET vs CORRUPT is a real distinction buildIndexData
		// must not collapse (see this branch's own report, MAJOR finding):
		// a plugin every one of whose versions is still incomplete --
		// including the common case of a first publish interrupted before
		// its completion marker ever landed -- is a plugin with nothing to
		// list, not damaged data. It reappears here on its own the moment
		// ANY version's publish actually finishes: markVersionComplete
		// flips that version's Complete flag AND recomputes LatestVersion
		// in the same atomic CAS (see that function's doc comment), so
		// there is no window where this plugin has a complete version yet
		// still reads as "nothing to list".
		hasCompleteVersion := false
		for _, v := range st.Versions {
			if domain.IsVersionComplete(v) {
				hasCompleteVersion = true
				break
			}
		}
		if !hasCompleteVersion {
			// Legitimate exclusion, same rationale as the versionless case
			// above -- just reached via versions that exist but never
			// finished, instead of no versions at all.
			continue
		}
		if st.LatestVersion == "" {
			// This IS the corrupt case, and it is a narrower one than the
			// old code checked: at least one version above is complete, so
			// domain.LatestVersion(st.Versions, st.BlockedVersions) -- the
			// only function that ever writes this field (markVersionComplete
			// here, and lifecycle.Service.BlockVersion's own recompute) --
			// is guaranteed to return a non-empty string; its fallback names
			// the SemVer-max complete version even when every complete
			// version is blocked. Reaching this with LatestVersion still ""
			// means the persisted document disagrees with that invariant.
			// Unlike the exclusion above, this can't be resolved by waiting
			// for a future publish -- refuse to write a partial index.
			return nil, fmt.Errorf("rebuildIndex: plugin %q has a complete version but no latestVersion, refusing to write a partial index", id)
		}
		mObj, err := s.Store.Read(ctx, storage.VersionManifestPath(id, st.LatestVersion))
		if err != nil {
			return nil, fmt.Errorf("rebuildIndex: plugin %q: latest version %q manifest unreadable, refusing to write a partial index: %w", id, st.LatestVersion, err)
		}
		var m domain.Manifest
		if err := json.Unmarshal(mObj.Data, &m); err != nil {
			return nil, fmt.Errorf("rebuildIndex: plugin %q: latest version %q manifest unparseable, refusing to write a partial index: %w", id, st.LatestVersion, err)
		}
		versions := make([]string, 0, len(st.Versions))
		for _, v := range st.Versions {
			versions = append(versions, v.Version)
		}
		sort.Strings(versions)
		plugins = append(plugins, domain.IndexPlugin{
			ID:            id,
			Name:          m.Name,
			LatestVersion: st.LatestVersion,
			Description:   m.Description,
			Category:      m.Category,
			Access:        m.Access,
			Tier:          st.Tier,
			Versions:      versions,
		})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	idx := domain.Index{Plugins: plugins, Complete: true}
	return json.MarshalIndent(idx, "", "  ")
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
