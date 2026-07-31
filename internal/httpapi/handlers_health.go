package httpapi

import (
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

func (s *Server) handleCDNProxy(w http.ResponseWriter, r *http.Request) {
	objectPath := strings.TrimPrefix(r.URL.Path, "/cdn/")
	objectPath = strings.TrimPrefix(objectPath, "/")
	if objectPath == "" {
		http.NotFound(w, r)
		return
	}
	// Never expose entitlement / session denylist objects via CDN.
	if storage.IsAuthObject(objectPath) {
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Object is not publicly accessible"})
		return
	}
	exp := r.URL.Query().Get("exp")
	sig := r.URL.Query().Get("sig")
	// Private (premium) objects always require a valid signed URL.
	if storage.IsPrivateObject(objectPath) {
		if exp == "" || sig == "" || !s.deps.LocalStore.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	} else if exp != "" || sig != "" {
		// If a signature is presented for a public object, it must be valid.
		if exp == "" || sig == "" || !s.deps.LocalStore.VerifySignedURL(objectPath, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	}
	if err := s.deps.LocalStore.ServeFile(objectPath, w); err != nil {
		if err == storage.ErrNotFound {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Object not found"})
			return
		}
		writeError(w, err)
	}
}
