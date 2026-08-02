package httpapi

import (
	"net/http"
	"path"
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
func (s *Server) handleCDNProxy(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/cdn/")
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" {
		http.NotFound(w, r)
		return
	}
	// Canonicalize before any authorization decision. storage.IsAuthObject
	// and storage.IsPrivateObject must see exactly the path that will
	// actually be read, not a pre-traversal-collapse alias of it — a
	// request for "x/../auth/authorized_keys.json" has a raw prefix of
	// "x/", so a naive IsAuthObject(raw) check never sees "auth/" at all,
	// even though the object that ends up being read, once the storage
	// layer resolves "..", is exactly the protected
	// "auth/authorized_keys.json". path.Clean here, applied BEFORE the
	// guard checks and used for the guard checks, the signature check and
	// the read alike, closes that: every one of those four steps now judges
	// the same canonical string. See
	// TestHandleCDNProxyGuardCannotBeWalkedAroundByTraversal.
	objectPath := path.Clean("/" + raw)
	if objectPath == "/" {
		http.NotFound(w, r)
		return
	}
	objectPath = strings.TrimPrefix(objectPath, "/")

	// Never expose entitlement / session denylist objects via CDN.
	if storage.IsAuthObject(objectPath) {
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Object is not publicly accessible"})
		return
	}
	exp := r.URL.Query().Get("exp")
	sig := r.URL.Query().Get("sig")
	// Private (premium) objects always require a valid signed URL.
	if storage.IsPrivateObject(objectPath) {
		if exp == "" || sig == "" || !s.deps.Store.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	} else if exp != "" || sig != "" {
		// If a signature is presented for a public object, it must be valid.
		if exp == "" || sig == "" || !s.deps.Store.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	}
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
