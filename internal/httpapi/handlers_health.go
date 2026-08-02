package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/reportportal/service-marketplace/internal/storage"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.Ready(r.Context()); err != nil {
		writeError(w, &APIError{Status: http.StatusServiceUnavailable, Code: CodeStorageUnavailable, Message: "Registry storage is temporarily unavailable", Headers: map[string]string{"Retry-After": "1"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// handleCDNProxy serves GET /cdn/*, the edge every plugin artifact,
// manifest, changelog and screenshot is fetched through (directly, or via a
// real CDN fronting this route). Its guarantees are deliberately
// storage-backend-independent: it reads and verifies signatures exclusively
// through the generic storage.ObjectStore (s.deps.Store), never the
// local-filesystem-only s.deps.LocalStore, so a GCS-backed deployment
// enforces signed-URL expiry and signature exactly as a local one does —
// both backends share one signature implementation
// (internal/storage.verifySignature) rather than keeping two in sync by
// hand. See internal/storage/signing_test.go's
// TestSignedURLVerificationIsBackendIndependent and this package's
// TestHandleCDNProxyRegisteredRegardlessOfBackend.
//
// It deliberately does NOT consult plugin.json's per-version Complete flag
// (domain.VersionMeta.Complete) before serving an object's bytes, even
// though a committed-but-incomplete version's jar/manifest can exist in
// storage before markVersionComplete ever runs (see
// internal/publish.Service.publish's write-then-commit ordering). Three
// reasons, not an oversight:
//
//  1. Cost: this is the single highest-volume byte-serving path in the
//     service. Gating it on completeness would mean a second read (an extra
//     plugin.json fetch, parsed and searched for the version in question)
//     on every request, for every object type this proxy serves — not just
//     artifacts — doubling I/O on the hottest path for a check almost every
//     request doesn't need.
//  2. It can never be watertight anyway: a client that already fetched the
//     URL during the brief write-then-commit window has the bytes
//     regardless of what this edge does on a later request, and a real CDN
//     sitting in front of this route caches the response independent of
//     this process's decisions.
//  3. The boundary that actually matters — the API never hands out or
//     advertises an incomplete version's URL to anyone — is already closed
//     (stage 3: every API read path 404s an incomplete version; see
//     domain.IsVersionComplete's callers). Reaching this edge for an
//     incomplete version at all requires already knowing the exact,
//     unguessable-in-practice version string; that is a narrow enough
//     target that duplicating publish's own completeness bookkeeping here
//     is the wrong trade.
//
// TestHandleCDNProxyServesIncompleteVersionBytes pins this as the current,
// deliberate behaviour — if a future change enforces completeness here too,
// that test's failure is the signal to update this comment, not evidence of
// a regression.
//
// Point 4 (byte-serving cost on non-local backends): registering /cdn/*
// unconditionally (see routes()) is what makes signed-URL enforcement
// backend-independent — GCS deployments now get the exact same auth/
// rejection, private/-requires-signature, and signature-verification
// guarantees a local deployment always had, instead of relying on bucket
// ACLs to do the equivalent job out of band. That is a real gain and is not
// being given up here.
//
// But it also means a GCS deployment's highest-volume path — a public jar
// download with no signature attached at all — would otherwise be fully
// buffered into this process's heap (storage.ObjectStore.Read returns
// []byte; GCSStore.Read does an io.ReadAll under the hood) and re-served
// byte-for-byte, something GCS deployments never paid for before (the
// public bucket/CDN served those bytes directly). Streaming instead of
// buffering would fix that too, but it would mean changing
// storage.ObjectStore.Read's signature (internal/storage/store.go) and
// GCSStore.Read (internal/storage/gcs.go) — both outside this file's
// ownership for this change, and a large enough interface change to ripple
// through internal/publish, internal/catalogue, internal/lifecycle and
// internal/license, none of which this change touches otherwise.
//
// The alternative actually taken: keep /cdn/* registered unconditionally
// (so nothing that needs THIS handler's enforcement — auth/ objects,
// private/ objects, or any request that presents a signature at all, on any
// backend — ever skips it) but hand a plain, unsigned request for a public
// object straight to the backend's own public origin instead of reading it
// through this process, whenever that origin is something other than this
// same /cdn route. s.deps.LocalStore == nil is exactly that signal: it is
// nil if and only if this deployment is not LocalStore (see routes()' and
// cmd/marketplace/main.go's STORAGE_TYPE wiring) — and LocalStore.PublicURL
// deliberately points right back into this same /cdn/* route, so this
// shortcut is naturally a no-op for local deployments (skipped entirely:
// the condition is false) rather than a redirect loop. See
// TestHandleCDNProxyPublicObjectRedirectsOnNonLocalBackend and
// TestHandleCDNProxyLocalStorePublicObjectIsServedDirectly.
func (s *Server) handleCDNProxy(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/cdn/")
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" {
		http.NotFound(w, r)
		return
	}
	// Canonicalize before any authorization decision. storage.IsAuthObject
	// and storage.IsPrivateObject must see exactly the path that will
	// actually be read, not a pre-traversal-collapse or pre-case-alias
	// spelling of it. See storage.CanonicalizeObjectPath's doc comment for
	// the full reasoning (both the ".." traversal case and the case-alias
	// case belong to the same class of bug: a string that fails a
	// case-sensitive/exact-prefix guard but still resolves, once the
	// storage layer gets hold of it, to the exact protected bytes). Every
	// downstream step — the guard checks, the signature check and the read
	// — judges this one canonicalized string, never r.URL.Path or raw
	// directly.
	objectPath, err := storage.CanonicalizeObjectPath(raw)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		// storage.ErrReservedNamespaceAlias: raw is a case alias of a
		// reserved namespace (e.g. "Auth/...", "PRIVATE/..."). Nothing in
		// this system ever legitimately produces or requests one of these,
		// so it gets exactly the same response a direct hit on the
		// namespace does — never a 404, which would (a) suggest the alias
		// was merely a wrong path rather than a rejected one, and (b) still
		// leave open the directory-existence oracle this same rejection
		// closes for the bare-root case (see hasReservedPrefix's comment).
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Object is not publicly accessible"})
		return
	}

	// Never expose entitlement / session denylist objects via CDN.
	if storage.IsAuthObject(objectPath) {
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Object is not publicly accessible"})
		return
	}
	exp := r.URL.Query().Get("exp")
	sig := r.URL.Query().Get("sig")
	private := storage.IsPrivateObject(objectPath)
	// Private (premium) objects always require a valid signed URL.
	if private {
		if exp == "" || sig == "" || !s.deps.Store.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	} else if exp != "" || sig != "" {
		// If a signature is presented for a public object, it must be valid.
		if !s.deps.Store.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	}
	// A public object with no signature is served from here, on every
	// backend. Do NOT "optimise" this by redirecting to
	// s.deps.Store.PublicURL(objectPath): both LocalStore.PublicURL and
	// GCSStore.PublicURL are cdnBase + "/" + CDNPath(objectPath), and
	// cmd/marketplace/main.go hands the same CDN_BASE_URL to the store and
	// to this service — which serves /cdn. The "public origin" is therefore
	// this very route, and redirecting to it loops until the client gives
	// up (pinned by
	// TestHandleCDNProxy_PublicObjectOnNonLocalBackend_DoesNotRedirectToItself).
	// Buffering the object is the deliberate cost of registering /cdn/*
	// unconditionally so signature enforcement does not depend on which
	// backend is configured.
	obj, err := s.deps.Store.Read(r.Context(), objectPath)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Object not found"})
			return
		}
		writeError(w, err)
		return
	}
	_, _ = w.Write(obj.Data)
}
