package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// CleanupConfig controls the AMD-27 orphan-cleanup sweep. Its predecessor
// (the goroutine formerly named StartOrphanCleanup/cleanupOrphans on this
// package's Service) had none of these knobs: no age guard, so it raced
// every in-flight publish; no dry-run, so there was no way to observe what
// it would do before trusting it; no per-run abort guard, so a corrupted or
// stale reference document was deleted against; and no cross-replica
// coordination, so N replicas each ran their own sweep concurrently against
// the same storage. Every field below closes exactly one of those.
type CleanupConfig struct {
	// Enabled gates the sweep entirely. The zero value is false: this job is
	// a catalogue-wide data-loss hazard (assessment finding
	// F2-orphan-cleanup) and must be turned on deliberately. When false,
	// Run performs no storage I/O whatsoever, not even a lease read.
	Enabled bool
	// DryRun, when true, runs every guard and computes exactly what would be
	// deleted, but issues no Delete calls. Independent of Enabled so an
	// operator can observe the job's decisions before trusting it to delete
	// anything.
	DryRun bool
	// MinAge is the AMD-27 age guard: a version directory is never a
	// deletion candidate until its newest object is at least this old. This
	// is what makes the sweep safe to run concurrently with an in-flight
	// publish -- a publish's own artifact write is always younger than
	// MinAge for as long as it takes to finish the publish, which is many
	// orders of magnitude shorter than MinAge.
	MinAge time.Duration
	// RunInterval is AMD-27's "once per 24h" schedule: Run no-ops if the
	// last successful sweep attempt (recorded in the lease document,
	// regardless of whether it found anything to delete) was less than this
	// long ago.
	RunInterval time.Duration
	// LeaseTTL bounds how long a single replica holds the cross-replica
	// lease while it runs the sweep, so a crashed holder does not lock every
	// other replica out until the next RunInterval boundary.
	LeaseTTL time.Duration
}

// CleanupReport is what one Run call decided and did, for logging/metrics
// and for tests to assert against directly instead of re-deriving state from
// storage side effects alone.
type CleanupReport struct {
	// Ran is true only if this call actually held the lease and executed a
	// sweep (dry-run or not). False covers every skip reason: disabled, not
	// due yet, or lease held by another replica.
	Ran        bool
	SkipReason string

	DryRun bool
	// Aborted is true if a refuse-to-delete guard fired: the reference
	// document (index.json) failed to load, or loaded but looked wrong. When
	// Aborted, Deleted is always 0.
	Aborted     bool
	AbortReason string

	PluginsInIndex int
	Candidates     int // version directories unreferenced by index.json
	HeldByAge      int // unreferenced, but younger than MinAge
	HeldUnknownAge int // unreferenced, but age could not be determined
	Deleted        int
	DeletedKeys    []string
}

// cleanupLease is the document at storage.PathOrphanCleanupLease. Ownership
// is acquired and renewed via the storage layer's existing CAS primitive
// (ObjectStore.Write with an expected generation) rather than any new
// coordination mechanism: a replica that successfully CAS-writes this
// document with its own Owner/ExpiresAt has the lease; a replica that loses
// the CAS race (or observes a live, unexpired lease from a different Owner)
// does not.
type cleanupLease struct {
	Owner      string    `json:"owner"`
	AcquiredAt time.Time `json:"acquiredAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// LastRunAt drives the once-per-24h schedule independently of the lease
	// expiry above, so a crash-and-restart within the same day does not
	// re-run the sweep early.
	LastRunAt time.Time `json:"lastRunAt"`
}

// OrphanCleanup runs the AMD-27 orphan-cleanup sweep. It is deliberately not
// a method on Service: it needs only a Store, has its own lifecycle
// (started once per process, ticks independently of any HTTP request), and
// keeping it standalone lets tests drive Run directly and synchronously
// instead of through a goroutine+ticker.
//
// # Status: unsupported, ships disabled
//
// This job has delete authority over reference data (index.json) that is
// assembled concurrently with the writes it is judging -- publishes,
// removals, tier changes -- happening on other replicas at the same time.
// Three separate review rounds each found a distinct way to defeat the
// refuse-to-delete guard in sweep below, i.e. to make Run delete a version
// that was never actually orphaned:
//
//  1. Silently-dropped plugins during index rebuild. A full index.json
//     rebuild that hit an unrelated, transient failure resolving ONE
//     plugin used to skip that plugin (a bare `continue`) and write the
//     resulting index anyway. The new document looked completely healthy
//     -- nonzero plugins, each with a real nonzero Versions list -- while
//     quietly omitting every version the dropped plugin had ever
//     legitimately committed. Those versions then read as unreferenced
//     and were swept once MinAge elapsed. Closed by making
//     internal/publish.Service.rebuildIndex all-or-nothing: it now either
//     resolves every known plugin and writes a document it can vouch for
//     in full, or writes nothing and leaves the previous (older, but
//     honest) index.json in place. See domain.Index.Complete and this
//     file's own `!idx.Complete` guard below, which exists specifically
//     to detect a document not written by that all-or-nothing path.
//  2. A stale-but-complete index. domain.Index.Complete attests that the
//     rebuild which produced a document resolved every plugin it saw --
//     it does not attest that the resulting snapshot was still current by
//     the time the document landed. rebuildIndex's CAS-retry loop used to
//     resubmit the SAME already-computed (by then stale) snapshot on
//     every retry instead of recomputing; a replica that lost the initial
//     CAS race to a concurrent publish could still win a later retry with
//     that stale snapshot, silently regressing index.json to a state that
//     no longer referenced the other replica's just-committed version --
//     decoding Complete: true throughout, so guard (1) above could not
//     tell the difference. Closed by re-deriving the snapshot from live
//     storage state on every retry attempt instead of reusing the first
//     one.
//  3. A third, structurally different reproduction, found in the review
//     round that followed the fix for (2) above. It is real and was
//     documented in that round's review; this branch does not attempt to
//     close it. Do not treat "the first two routes are closed" as
//     evidence this guard is now safe -- treat it as evidence that a
//     sweeper judging concurrently-written reference data keeps finding
//     new ways to be wrong, which is the whole reason it ships disabled.
//
// The decision taken after round 3: a sweeper that decides what to delete
// from reference data assembled concurrently with writes needs either real
// coordination with those writers (which this package does not have -- the
// lease in this file coordinates replicas of THIS job with each other, not
// with publishers/removers/tier-changers) or no delete authority at all.
// Rather than attempt a fourth guard under time pressure, this stage ships
// the sweeper disabled by contract (CleanupConfig.Enabled, and
// config.Config.OrphanCleanupEnabled above it -- both default false with no
// "unset means on" path; see TestLoad_OrphanCleanupDisabledByDefault) while
// keeping every guard above intact and keeping the dry-run path
// (CleanupConfig.DryRun) fully working: an operator can run it, see exactly
// what it would delete, and compare that against what should be. That
// dry-run comparison is how this guard will eventually be proven -- or
// proven insufficient again -- not a decorative flag.
type OrphanCleanup struct {
	Store  storage.ObjectStore
	Config CleanupConfig
	// Owner identifies this process in the lease document. Callers should
	// pass something host/process-unique (see cmd/marketplace/main.go);
	// tests pass a fixed string.
	Owner string
	// Now returns the current time. nil means time.Now().UTC(). AMD-27's own
	// acceptance criteria require the age guard to be provable against an
	// injected clock ("aged past 24h (injected clock)"), not wall-clock
	// sleeps, so this is a plain field rather than a hidden time.Now() call.
	Now    func() time.Time
	Logger *log.Logger
}

func (c *OrphanCleanup) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

func (c *OrphanCleanup) logger() *log.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return log.Default()
}

// Run attempts one sweep. It never returns an error for conditions the
// sweep itself can safely no-op on (disabled, not due, lease contention,
// unreadable/suspicious reference data) -- those are reported via
// CleanupReport instead, because none of them are exceptional: they are the
// expected steady-state outcome on most ticks. The error return is reserved
// for failures Run cannot itself interpret (e.g. context cancellation).
func (c *OrphanCleanup) Run(ctx context.Context) (*CleanupReport, error) {
	report := &CleanupReport{DryRun: c.Config.DryRun}

	if !c.Config.Enabled {
		report.SkipReason = "disabled"
		return report, nil
	}

	now := c.now()

	leaseGen, lease, err := c.readLease(ctx)
	if err != nil {
		report.SkipReason = fmt.Sprintf("lease read failed: %v", err)
		return report, nil
	}
	if !lease.LastRunAt.IsZero() && now.Sub(lease.LastRunAt) < c.Config.RunInterval {
		report.SkipReason = "not due yet"
		return report, nil
	}
	if !lease.ExpiresAt.IsZero() && lease.ExpiresAt.After(now) && lease.Owner != c.Owner {
		report.SkipReason = "lease held by " + lease.Owner
		return report, nil
	}

	acquired := cleanupLease{Owner: c.Owner, AcquiredAt: now, ExpiresAt: now.Add(c.Config.LeaseTTL), LastRunAt: lease.LastRunAt}
	data, err := json.Marshal(acquired)
	if err != nil {
		report.SkipReason = fmt.Sprintf("lease marshal failed: %v", err)
		return report, nil
	}
	newGen, err := c.Store.Write(ctx, storage.PathOrphanCleanupLease, data, leaseGen)
	if err != nil {
		// Either a genuine storage error, or -- far more commonly -- another
		// replica won the CAS race between our read and our write. Both are
		// "someone else is handling this tick (or storage is unhappy);
		// try again next tick" rather than a hard failure.
		report.SkipReason = fmt.Sprintf("lease acquisition failed: %v", err)
		return report, nil
	}
	report.Ran = true

	c.sweep(ctx, now, report)

	final := cleanupLease{Owner: c.Owner, AcquiredAt: acquired.AcquiredAt, ExpiresAt: now, LastRunAt: now}
	fdata, err := json.Marshal(final)
	if err == nil {
		if _, err := c.Store.Write(ctx, storage.PathOrphanCleanupLease, fdata, newGen); err != nil {
			// Best-effort: if this loses a race the next tick's lease read
			// simply sees a live lease from whoever won it and skips.
			c.logger().Printf("orphan-cleanup: could not record last-run time: %v", err)
		}
	}

	return report, nil
}

func (c *OrphanCleanup) readLease(ctx context.Context) (int64, cleanupLease, error) {
	obj, err := c.Store.Read(ctx, storage.PathOrphanCleanupLease)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, cleanupLease{}, nil
		}
		return 0, cleanupLease{}, err
	}
	var lease cleanupLease
	if err := json.Unmarshal(obj.Data, &lease); err != nil {
		return obj.Generation, cleanupLease{}, err
	}
	return obj.Generation, lease, nil
}

// sweep performs the guarded scan-and-delete once the lease is held. Every
// early return before the delete loop is a refuse-to-delete guard: the
// reference document must load, and must look trustworthy, before anything
// is treated as safe-to-delete evidence.
func (c *OrphanCleanup) sweep(ctx context.Context, now time.Time, report *CleanupReport) {
	idx, err := c.loadIndex(ctx)
	if err != nil {
		report.Aborted = true
		report.AbortReason = fmt.Sprintf("index.json failed to load: %v", err)
		return
	}

	referenced := map[string]map[string]bool{}
	for _, p := range idx.Plugins {
		set := make(map[string]bool, len(p.Versions))
		for _, v := range p.Versions {
			set[v] = true
		}
		referenced[p.ID] = set
		report.PluginsInIndex++
	}

	candidates, err := c.findCandidates(ctx)
	if err != nil {
		report.Aborted = true
		report.AbortReason = fmt.Sprintf("could not enumerate storage: %v", err)
		return
	}

	// Refuse-to-delete guard. The question this asks is deliberately not
	// "does this index look plausible" (a heuristic like "the plugins it
	// lists collectively carry zero versions", or "it lists zero plugins")
	// -- both of those only catch the specific shapes their author thought
	// of, and a rebuild that silently dropped ONE unreadable plugin out of
	// many produces an index that looks perfectly plausible by either
	// measure: nonzero plugins, each with a real nonzero Versions list. The
	// question this asks instead is "did whoever wrote this document
	// positively attest that it is exhaustive" -- domain.Index.Complete,
	// which internal/publish.Service.rebuildIndex sets to true only when it
	// successfully resolved every known plugin, and which any document not
	// written by that code path (a legacy pre-AMD-27 index.json, a
	// hand-edited one, a partial rebuild that got aborted mid-write by
	// something other than this fix) decodes as false by construction --
	// Go's json zero value for a missing bool field. A sweeper that cannot
	// prove its reference data is complete has no business deleting
	// anything on the strength of it.
	if len(candidates) > 0 && !idx.Complete {
		report.Aborted = true
		report.AbortReason = "index.json is not marked complete (\"complete\" is false or absent) " +
			"while storage holds version directories -- refusing to trust it as proof anything is unreferenced"
		return
	}

	for _, cand := range candidates {
		report.Candidates++
		if referenced[cand.pluginID][cand.version] {
			continue
		}

		newest, ok, err := c.newestObjectTime(ctx, cand.objects)
		if err != nil || !ok {
			report.HeldUnknownAge++
			c.logger().Printf("orphan-cleanup: holding %s/%s, could not determine object age: %v", cand.pluginID, cand.version, err)
			continue
		}
		if now.Sub(newest) < c.Config.MinAge {
			report.HeldByAge++
			continue
		}

		if c.Config.DryRun {
			report.Deleted++
			report.DeletedKeys = append(report.DeletedKeys, cand.objects...)
			continue
		}

		deletedAny := false
		for _, obj := range cand.objects {
			if err := c.Store.Delete(ctx, obj); err != nil {
				c.logger().Printf("orphan-cleanup: delete %s failed: %v", obj, err)
				continue
			}
			deletedAny = true
			report.DeletedKeys = append(report.DeletedKeys, obj)
		}
		if deletedAny {
			report.Deleted++
		}
	}
}

func (c *OrphanCleanup) loadIndex(ctx context.Context) (*domain.Index, error) {
	obj, err := c.Store.Read(ctx, storage.PathIndex)
	if err != nil {
		return nil, err
	}
	var idx domain.Index
	if err := json.Unmarshal(obj.Data, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

type cleanupCandidate struct {
	pluginID string
	version  string
	objects  []string
}

// findCandidates enumerates every version directory under both the public
// (plugins/) and private (private/plugins/, premium jars) trees and groups
// their objects by (pluginID, version). It does not itself decide
// orphan-or-not; sweep does that against the index-referenced set.
func (c *OrphanCleanup) findCandidates(ctx context.Context) ([]cleanupCandidate, error) {
	groups := map[string]*cleanupCandidate{}
	var order []string
	for _, prefix := range []string{"plugins/", "private/plugins/"} {
		files, err := c.Store.ListPrefix(ctx, prefix)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			pluginID, version, ok := parseVersionObjectPath(f)
			if !ok {
				continue
			}
			key := pluginID + "\x00" + version
			g, exists := groups[key]
			if !exists {
				g = &cleanupCandidate{pluginID: pluginID, version: version}
				groups[key] = g
				order = append(order, key)
			}
			g.objects = append(g.objects, f)
		}
	}
	out := make([]cleanupCandidate, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	return out, nil
}

// parseVersionObjectPath extracts (pluginID, version) from an object key
// shaped plugins/{id}/versions/{version}/... or
// private/plugins/{id}/versions/{version}/..., matching
// storage.VersionPrefix/storage.VersionArtifactPath's layout exactly.
// version is taken as the single path segment immediately after
// "versions/" and never re-derived by further splitting, unlike the
// Java-era OrphanCleanupJob (RW-INDEX.stage2.md's
// remainder.substring(0, slash) defect, which misparsed any version
// containing its own "/" and deleted the wrong reconstructed prefix).
func parseVersionObjectPath(objectPath string) (pluginID, version string, ok bool) {
	parts := strings.Split(objectPath, "/")
	offset := 0
	if len(parts) > 0 && parts[0] == "private" {
		offset = 1
	}
	// Need at least: [private/] plugins / {id} / versions / {version} / {filename}
	if len(parts) < offset+5 {
		return "", "", false
	}
	if parts[offset] != "plugins" || parts[offset+2] != "versions" {
		return "", "", false
	}
	return parts[offset+1], parts[offset+3], true
}

// newestObjectTime returns the newest CreatedAt across objects, using
// Stat rather than Read so the age guard never pays to load a jar's full
// body just to learn its timestamp. ok is false if objects is empty or any
// Stat call fails -- the caller (sweep) treats "cannot determine" as "hold,
// do not delete", never as "old enough".
func (c *OrphanCleanup) newestObjectTime(ctx context.Context, objects []string) (time.Time, bool, error) {
	var newest time.Time
	for _, obj := range objects {
		meta, err := c.Store.Stat(ctx, obj)
		if err != nil {
			return time.Time{}, false, err
		}
		if meta.CreatedAt.After(newest) {
			newest = meta.CreatedAt
		}
	}
	if newest.IsZero() {
		return time.Time{}, false, nil
	}
	return newest, true, nil
}

// StartOrphanCleanup starts the AMD-27 orphan-cleanup ticker in the
// background and returns the OrphanCleanup instance driving it (mainly so
// callers/tests can inspect or trigger Run directly). Safe to call even
// when cfg.Enabled is false -- see CleanupConfig.Enabled -- ticks simply
// no-op. tickInterval is how often the goroutine wakes up to check whether
// a sweep is due; the sweep itself only actually runs once per
// cfg.RunInterval, via the lease's LastRunAt.
func (s *Service) StartOrphanCleanup(ctx context.Context, cfg CleanupConfig, tickInterval time.Duration, owner string) *OrphanCleanup {
	job := &OrphanCleanup{Store: s.Store, Config: cfg, Owner: owner}
	go func() {
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := job.Run(ctx); err != nil {
					log.Printf("orphan-cleanup: %v", err)
				}
			}
		}
	}()
	return job
}
