package lifecycle

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/reportportal/service-marketplace/internal/storage"
)

// HousekeepingOutcome reports whether a lifecycle mutation's downstream
// housekeeping (index rebuild, CDN invalidation) completed after its
// primary write already committed.
//
// No existing requirement specifies this contract -- the assessment finding
// this closes (lifecycle-index-rebuild-errors-discarded) says exactly that,
// and recommends an amendment. This type is this package's implementation
// of what that amendment would demand, not a citation of one: a lifecycle
// mutation whose downstream housekeeping fails after the primary write
// commits reports partial completion (Degraded() == true, with a
// human-readable reason per failed step) rather than plain success, and the
// failure is both recorded durably (recordHousekeepingFailure, so it can be
// found and retried out of band) and attempted inline via the normal
// RebuildIndex/Invalidate call, which already retries transient conflicts
// itself (storage.WriteWithRetry, 5 attempts). What this package does NOT
// yet provide is an automated consumer that re-drives a recorded failure --
// see the accompanying report for that gap.
//
// The primary write is never rolled back, and the mutation never returns an
// error, because of a housekeeping failure: the data IS written, and
// answering as though it were not would be its own defect.
type HousekeepingOutcome struct {
	Warnings []string
}

// Degraded reports whether any housekeeping step failed.
func (o HousekeepingOutcome) Degraded() bool {
	return len(o.Warnings) > 0
}

// housekeepingFailureRecord is the durable shape written under
// storage.PathHousekeepingFailures. It is deliberately a local, wire/storage
// -only type distinct from anything already reachable from an HTTP
// response.
type housekeepingFailureRecord struct {
	PluginID string    `json:"pluginId"`
	Action   string    `json:"action"`
	Step     string    `json:"step"`
	Error    string    `json:"error"`
	FailedAt time.Time `json:"failedAt"`
}

// runHousekeeping runs a lifecycle mutation's downstream housekeeping after
// its primary write has already committed. Both steps are best-effort: a
// failure is recorded (in the returned HousekeepingOutcome, and durably via
// recordHousekeepingFailure) but never returned as an error from the
// mutation itself.
func (s *Service) runHousekeeping(ctx context.Context, pluginID, action string, rebuildIndex bool, invalidatePaths []string) HousekeepingOutcome {
	var out HousekeepingOutcome
	if rebuildIndex {
		if err := s.Publisher.RebuildIndex(ctx); err != nil {
			out.Warnings = append(out.Warnings, "index rebuild failed: "+err.Error())
			s.recordHousekeepingFailure(ctx, pluginID, action, "index_rebuild", err)
		}
	}
	if err := s.Invalidator.Invalidate(ctx, invalidatePaths); err != nil {
		out.Warnings = append(out.Warnings, "CDN invalidation failed: "+err.Error())
		s.recordHousekeepingFailure(ctx, pluginID, action, "cdn_invalidate", err)
	}
	return out
}

// recordHousekeepingFailure durably persists one housekeeping failure so it
// can be found and retried out of band, per the "must be retried or
// recorded" half of the amendment this package implements. Each failure
// gets its own object (storage.HousekeepingFailurePath) rather than an
// appended list, so concurrent failures never contend on a single CAS
// write. This write is itself best-effort: if it fails, the failure is
// still visible to the immediate caller via HousekeepingOutcome, just not
// durably -- logged so an operator watching logs is not left with nothing.
func (s *Service) recordHousekeepingFailure(ctx context.Context, pluginID, action, step string, cause error) {
	now := time.Now().UTC()
	rec := housekeepingFailureRecord{PluginID: pluginID, Action: action, Step: step, Error: cause.Error(), FailedAt: now}
	data, err := json.Marshal(rec)
	if err != nil {
		log.Printf("lifecycle: could not marshal housekeeping-failure record for %s/%s/%s: %v", pluginID, action, step, err)
		return
	}
	key := storage.HousekeepingFailurePath(pluginID, action, step, now)
	if _, err := s.Store.Write(ctx, key, data, 0); err != nil {
		log.Printf("lifecycle: could not durably record housekeeping failure for %s/%s/%s (in-memory warning was still returned to the caller): %v", pluginID, action, step, err)
	}
}
