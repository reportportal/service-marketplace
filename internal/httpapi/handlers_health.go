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
	if s.deps.CDNSelfCheck == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	// The CDN self-check only ever degrades the body, never the status code:
	// failing readiness would take the whole API out of rotation over a
	// download-URL problem, and could deadlock a correct deployment whose
	// CDN_BASE_URL routes back to this pod only once it is ready.
	status, reason := s.deps.CDNSelfCheck.Result()
	cdn := map[string]string{"status": status}
	if reason != "" {
		cdn["reason"] = reason
	}
	overall := "ready"
	if status == CDNCheckMisconfigured {
		overall = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": overall, "checks": map[string]any{"cdn": cdn}})
}

// handleCDNProxy serves GET /cdn/* via ObjectStore with path guards and signed-URL checks.
func (s *Server) handleCDNProxy(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/cdn/")
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" {
		http.NotFound(w, r)
		return
	}
	objectPath, err := storage.CanonicalizeObjectPath(raw)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Object is not publicly accessible"})
		return
	}

	if storage.IsAuthObject(objectPath) {
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Object is not publicly accessible"})
		return
	}
	exp := r.URL.Query().Get("exp")
	sig := r.URL.Query().Get("sig")
	if isCDNProbePath(objectPath) {
		s.serveCDNProbe(w, objectPath, exp, sig)
		return
	}
	private := storage.IsPrivateObject(objectPath)
	if private {
		if exp == "" || sig == "" || !s.deps.Store.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	} else if exp != "" || sig != "" {
		if !s.deps.Store.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	}
	// Serve bytes here; do not redirect to PublicURL (same host as /cdn → loop).
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
