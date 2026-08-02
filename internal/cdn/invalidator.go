package cdn

import "context"

// Invalidator purges cached CDN content for the given object paths after a
// lifecycle or publish mutation has already committed to storage.
//
// Every path is an origin path rooted at "/" (e.g. "/index.json") and may
// end in a single trailing "/*" to cover everything below that prefix —
// BlockVersion and RemovePlugin both invalidate a whole version or plugin
// subtree this way. Implementations must accept that shape directly rather
// than rejecting or expanding it: expanding a wildcard into concrete keys
// here would mean listing a prefix the very mutation that triggered the
// purge may still be changing (e.g. RemovePlugin invalidates
// "/plugins/{id}/*" after it has already started deleting those objects).
//
// A nil or empty paths slice, and any nil/empty individual path, must be a
// no-op, never a panic or an error.
//
// Every caller invokes Invalidate after its mutation has already been
// committed to storage, so this is inherently best-effort from the caller's
// point of view: see internal/lifecycle.runHousekeeping, whose callers
// report a failed purge as a degraded-but-successful outcome rather than
// failing the mutation itself. Implementations are free to return an error
// for a failed purge (this Go port does, unlike the Java original's
// GcpCdnInvalidationService, which swallows and logs internally) — but must
// never do so as a result of the path *shape* (wildcard, nil, empty) rather
// than an actual failure to reach the CDN.
type Invalidator interface {
	Invalidate(ctx context.Context, paths []string) error
}
