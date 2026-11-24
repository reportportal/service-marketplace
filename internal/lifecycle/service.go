package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrForbidden     = errors.New("forbidden")
	ErrCeilingNotSet = errors.New("compatibility ceiling not set")
)

// FieldViolation is a caller-correctable rejection of one request field.
type FieldViolation struct {
	Field   string
	Message string
}

func (f FieldViolation) Error() string { return f.Message }

type Service struct {
	Store       storage.ObjectStore
	Invalidator cdn.Invalidator
	Publisher   *publish.Service
	// Logger receives the audit lines for removal, erasure and restore. Nil means log.Default().
	Logger *log.Logger
}

// audit writes one line per destructive or reversing operator action. There is no audit log yet
// (FR-OP-08 is deferred) and an erased plugin leaves no tombstone behind, so this line is the only
// record of who removed what, when, and why; it is deliberately a single greppable line.
func (s *Service) audit(format string, args ...any) {
	logger := s.Logger
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf("audit: "+format, args...)
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

const maxCeilingReasonLen = 2000

// SetCompatibilityCeiling stores or replaces the upper bound on one published version.
// The author's range is left as published. A bound whose intersection with that range
// is empty is refused: stopping a version on every release is a block.
func (s *Service) SetCompatibilityCeiling(ctx context.Context, pluginID, version, bound, reason, setBy string) (*domain.CompatibilityCeiling, error) {
	reason = strings.TrimSpace(reason)
	bound = strings.TrimSpace(bound)
	if reason == "" {
		return nil, FieldViolation{Field: "reason", Message: "required"}
	}
	if utf8.RuneCountInString(reason) > maxCeilingReasonLen {
		return nil, FieldViolation{Field: "reason", Message: "must be at most 2000 characters"}
	}
	now := time.Now().UTC()
	var stored domain.CompatibilityCeiling
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		if len(data) == 0 {
			return nil, ErrNotFound
		}
		var st domain.PluginState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		if st.Removed != nil {
			return nil, ErrNotFound
		}
		var author string
		found := false
		for _, v := range st.Versions {
			if v.Version == version {
				author = v.Compatibility
				found = true
				break
			}
		}
		if !found {
			return nil, ErrNotFound
		}
		if _, err := domain.EffectiveReportPortalRange(author, bound); err != nil {
			return nil, ceilingViolation(err, author)
		}
		canonical, err := domain.CanonicalCeiling(bound)
		if err != nil {
			return nil, ceilingViolation(err, author)
		}
		stored = domain.CompatibilityCeiling{
			Version:      version,
			ReportPortal: canonical,
			Reason:       reason,
			SetAt:        now,
			SetBy:        setBy,
		}
		replaced := false
		for i := range st.CompatibilityCeilings {
			if st.CompatibilityCeilings[i].Version == version {
				st.CompatibilityCeilings[i] = stored
				replaced = true
				break
			}
		}
		if !replaced {
			st.CompatibilityCeilings = append(st.CompatibilityCeilings, stored)
		}
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if s.Publisher != nil {
		if err := s.Publisher.RebuildIndex(ctx); err != nil {
			return nil, err
		}
	}
	s.invalidateCeiling(ctx, pluginID, version)
	return &stored, nil
}

// ClearCompatibilityCeiling removes the ceiling on one version. The effective range
// returns to the author's range. A version that has no ceiling is not found.
func (s *Service) ClearCompatibilityCeiling(ctx context.Context, pluginID, version string) error {
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		if len(data) == 0 {
			return nil, ErrNotFound
		}
		var st domain.PluginState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		if st.Removed != nil {
			return nil, ErrNotFound
		}
		foundVersion := false
		for _, v := range st.Versions {
			if v.Version == version {
				foundVersion = true
				break
			}
		}
		if !foundVersion {
			return nil, ErrNotFound
		}
		next := make([]domain.CompatibilityCeiling, 0, len(st.CompatibilityCeilings))
		cleared := false
		for _, c := range st.CompatibilityCeilings {
			if c.Version == version {
				cleared = true
				continue
			}
			next = append(next, c)
		}
		if !cleared {
			return nil, ErrCeilingNotSet
		}
		if len(next) == 0 {
			st.CompatibilityCeilings = nil
		} else {
			st.CompatibilityCeilings = next
		}
		return json.MarshalIndent(st, "", "  ")
	}, 5)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if s.Publisher != nil {
		if err := s.Publisher.RebuildIndex(ctx); err != nil {
			return err
		}
	}
	s.invalidateCeiling(ctx, pluginID, version)
	return nil
}

func ceilingViolation(err error, author string) error {
	switch {
	case errors.Is(err, domain.ErrInvalidCeilingBound):
		return FieldViolation{Field: "reportportal", Message: "must be a single upper bound, < or <= a ReportPortal CalVer such as <27.0"}
	case errors.Is(err, domain.ErrEmptyCeilingIntersection):
		return FieldViolation{Field: "reportportal", Message: "ceiling leaves no ReportPortal release inside the author's range; block the version instead"}
	case errors.Is(err, domain.ErrNoAuthorRange):
		return FieldViolation{Field: "reportportal", Message: "version has no author compatibility range to intersect"}
	case errors.Is(err, domain.ErrUnreadableAuthorRange):
		return FieldViolation{Field: "reportportal", Message: fmt.Sprintf("this version's own range %q can't take a ceiling; a ceiling needs a range written like \">=25.1, <27.0\" (operators >=, >, <=, <, =)", author)}
	default:
		return err
	}
}

func (s *Service) invalidateCeiling(ctx context.Context, pluginID, version string) {
	if s.Invalidator == nil {
		return
	}
	_ = s.Invalidator.Invalidate(ctx, []string{
		"/" + storage.PluginStatePath(pluginID),
		"/" + storage.PathIndex,
		"/" + storage.VersionPrefix(pluginID, version) + "*",
	})
}

func (s *Service) RemovePlugin(ctx context.Context, pluginID, reason, removedBy string) (*domain.PluginTombstone, error) {
	now := time.Now().UTC()
	tomb := domain.PluginTombstone{Removed: now, RemovalReason: reason, RemovedBy: removedBy}
	err := storage.WriteWithRetry(ctx, s.Store, storage.PluginStatePath(pluginID), func(data []byte, gen int64) ([]byte, error) {
		// WriteWithRetry hands over no data for an object that does not exist; decoding that
		// would surface as a 500 for what is simply an unknown id.
		if len(data) == 0 {
			return nil, ErrNotFound
		}
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

	// A failure here is reported, not swallowed: the tombstone is already written, so answering
	// 200 over a jar that is still on storage would claim a removal that did not finish. The
	// tombstone is what makes the retry possible — erasing the plugin (ErasePlugin) finishes the job.
	if err := s.deletePluginObjects(ctx, pluginID, true); err != nil {
		return nil, err
	}

	_ = s.Publisher.RebuildIndex(ctx)
	_ = s.Invalidator.Invalidate(ctx, pluginInvalidationPaths(pluginID))
	s.audit("plugin %q removed with tombstone by %q, reason %q", pluginID, removedBy, reason)
	return &tomb, nil
}

// ErasePlugin removes a plugin and leaves nothing behind: every object, plugin.json included, so
// the id is as free as one that was never used. Reads answer 404 rather than 410, and the
// plugin's allow-listed repository can publish to the id again at once.
//
// It is the counterpart of RemovePlugin for the cases the tombstone is wrong for — clearing out a
// dev registry, retiring an id after a rename. What the tombstone buys (clients told "removed",
// the id held closed) is exactly what those cases do not want and legal or abuse removals do, so
// that is the operator's call, per removal.
//
// It also works on a plugin that is already removed. That is the point: a tombstoned id is
// otherwise unusable, and Restore keeps the tombstone's history where this does not.
//
// The reason is optional here: with no tombstone there is no public payload to carry it. It is
// still written to the audit line when given.
func (s *Service) ErasePlugin(ctx context.Context, pluginID, reason, erasedBy string) (*domain.PluginErased, error) {
	obj, err := s.Store.Read(ctx, storage.PluginStatePath(pluginID))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// Only for the audit line. A plugin.json that no longer decodes is a reason to erase the
	// plugin, not to refuse to.
	var prev domain.PluginState
	_ = json.Unmarshal(obj.Data, &prev)

	// plugin.json goes last: if anything fails part-way the plugin is still listed by its state
	// and the operator can simply run the erase again, rather than being left with orphaned
	// jars under an id the registry no longer knows about.
	if err := s.deletePluginObjects(ctx, pluginID, false); err != nil {
		return nil, err
	}

	_ = s.Publisher.RebuildIndex(ctx)
	_ = s.Invalidator.Invalidate(ctx, pluginInvalidationPaths(pluginID))

	now := time.Now().UTC()
	prevState := "live"
	if prev.Removed != nil {
		prevState = "removed at " + prev.Removed.Format(time.RFC3339) + " by " + prev.RemovedBy
	}
	s.audit("plugin %q erased without tombstone by %q (was %s), reason %q", pluginID, erasedBy, prevState, reason)
	return &domain.PluginErased{ID: pluginID, Erased: now, ErasedBy: erasedBy, RemovalReason: reason}, nil
}

// deletePluginObjects deletes every object belonging to a plugin: its public tree and its private
// one (premium jars, in their own bucket in production). plugin.json is kept when keepState is
// set — that is the tombstone — and otherwise deleted last.
func (s *Service) deletePluginObjects(ctx context.Context, pluginID string, keepState bool) error {
	var files []string
	for _, prefix := range []string{storage.PluginPrefix(pluginID), storage.PrivatePluginPrefix(pluginID)} {
		found, err := s.Store.ListPrefix(ctx, prefix)
		if err != nil {
			return err
		}
		files = append(files, found...)
	}

	state := storage.PluginStatePath(pluginID)
	for _, f := range files {
		if strings.TrimPrefix(f, "/") == state {
			continue
		}
		if err := s.Store.Delete(ctx, f); err != nil {
			return err
		}
	}
	if !keepState {
		return s.Store.Delete(ctx, state)
	}
	return nil
}

// pluginInvalidationPaths is everything a CDN may still hold for a plugin after its objects are
// gone: the listing, and the whole subtree — manifests, jars, changelogs, screenshots, advisories
// and plugin.json. Invalidating only plugin.json would leave a removed plugin's jar downloadable
// from the edge until it expired.
func pluginInvalidationPaths(pluginID string) []string {
	return []string{"/" + storage.PathIndex, "/plugins/" + pluginID + "/*"}
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
	s.audit("plugin %q restored by %q", pluginID, restoredBy)
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
