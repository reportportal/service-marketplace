package httpapi

import (
	"net/http"

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
	path := stringsTrimPrefix(r.URL.Path, "/cdn/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	exp := r.URL.Query().Get("exp")
	sig := r.URL.Query().Get("sig")
	if exp != "" && sig != "" {
		if !s.deps.LocalStore.VerifySignedURL(path, exp, sig) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
			return
		}
	}
	if err := s.deps.LocalStore.ServeFile(path, w); err != nil {
		if err == storage.ErrNotFound {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Object not found"})
			return
		}
		writeError(w, err)
	}
}

func stringsTrimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
