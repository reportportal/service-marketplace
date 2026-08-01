package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reportportal/service-marketplace/internal/storage"
	"github.com/reportportal/service-marketplace/internal/storage/storagetest"
)

func TestHandleHealthAlwaysOK(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleReadyOKWhenStorageHealthy(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleReadyReports503WhenStorageIsDown is the storagetest.FaultStore's
// motivating use case: fail Ready() specifically (not "the Nth call"),
// leaving every other operation untouched, and confirm /ready degrades
// correctly instead of 500ing or reporting healthy.
func TestHandleReadyReports503WhenStorageIsDown(t *testing.T) {
	env := newTestEnv(t)
	faulty := storagetest.Wrap(env.Store)
	faulty.Fail(storagetest.OpReady, storagetest.AnyKey, storage.ErrUnavailable)
	srv := env.serverWithStore(faulty)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Code != CodeStorageUnavailable {
		t.Fatalf("expected code %q, got %q", CodeStorageUnavailable, body.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected a Retry-After header on a storage-unavailable response")
	}

	// The fault must not have leaked onto unrelated operations: a plain
	// Read for an unrelated key still works.
	if _, err := faulty.Read(context.Background(), "index.json"); err != nil && !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Read should be unaffected by an OpReady-only fault, got %v", err)
	}
}
