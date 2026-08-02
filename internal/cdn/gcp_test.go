package cdn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/api/option"
)

// fakeComputeServer stands in for Cloud Compute's UrlMaps.InvalidateCache
// endpoint (POST .../projects/{project}/global/urlMaps/{urlMap}/invalidateCache),
// recording the "path" field of every CacheInvalidationRule body it receives
// and, if failNext is set, returning a 400 once instead of succeeding — this
// is the only way to exercise GCPInvalidator's real request path (as opposed
// to the URLMap=="" / Project=="" stub short-circuits contract_test.go
// exercises) without live GCP credentials.
type fakeComputeServer struct {
	mu       sync.Mutex
	gotPaths []string
	failNext bool
}

func (f *fakeComputeServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.gotPaths = append(f.gotPaths, body.Path)
		fail := f.failNext
		f.failNext = false
		f.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"induced failure"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"operation-123","status":"DONE"}`))
	}
}

func (f *fakeComputeServer) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.gotPaths))
	copy(out, f.gotPaths)
	return out
}

func newGCPInvalidatorAgainst(srv *httptest.Server) *GCPInvalidator {
	return &GCPInvalidator{
		URLMap:  "marketplace-url-map",
		Project: "test-project",
		ClientOptions: []option.ClientOption{
			option.WithEndpoint(srv.URL),
			option.WithHTTPClient(srv.Client()),
			option.WithoutAuthentication(),
		},
	}
}

// TestGCPInvalidatorAcceptsWildcardPathsOverTheWire is the end-to-end
// regression test for the Java original's bug (see GCPInvalidator's doc
// comment): with Project actually set, Invalidate now reaches the real
// UrlMaps.InvalidateCache request-building code, not just the stub
// short-circuit contract_test.go exercises. A single-element path list is
// the "one path per call" fast path (gcp.go's len(hostRules)==1 branch); the
// multi-path publish/block shape below exercises the per-path loop.
func TestGCPInvalidatorAcceptsWildcardPathsOverTheWire(t *testing.T) {
	fake := &fakeComputeServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	g := newGCPInvalidatorAgainst(srv)
	err := g.Invalidate(context.Background(), []string{"/plugins/plugin-alpha/versions/1.0.0/*"})
	if err != nil {
		t.Fatalf("Invalidate returned an error for a wildcard path, want nil: %v", err)
	}
	got := fake.paths()
	if len(got) != 1 || got[0] != "/plugins/plugin-alpha/versions/1.0.0/*" {
		t.Fatalf("expected the wildcard path forwarded verbatim, got %v", got)
	}
}

func TestGCPInvalidatorAcceptsWildcardAmongMultiplePaths(t *testing.T) {
	fake := &fakeComputeServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	g := newGCPInvalidatorAgainst(srv)
	paths := []string{
		"/index.json",
		"/plugins/plugin-alpha/plugin.json",
		"/plugins/plugin-alpha/versions/1.0.0/*",
	}
	if err := g.Invalidate(context.Background(), paths); err != nil {
		t.Fatalf("Invalidate returned an error, want nil: %v", err)
	}
	got := fake.paths()
	if len(got) != 3 {
		t.Fatalf("expected 3 per-path calls, got %d: %v", len(got), got)
	}
	for _, want := range paths {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("path %q was never forwarded to the Compute API; got %v", want, got)
		}
	}
}

// TestGCPInvalidatorPropagatesRealFailures pins that a genuine Compute API
// failure (as opposed to the path shape itself) still surfaces as an error —
// distinguishing this from a rejection based on the path containing "/*".
// Callers (internal/lifecycle, internal/publish) already treat this as
// best-effort and never fail their own mutation because of it.
func TestGCPInvalidatorPropagatesRealFailures(t *testing.T) {
	fake := &fakeComputeServer{failNext: true}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	g := newGCPInvalidatorAgainst(srv)
	err := g.Invalidate(context.Background(), []string{"/index.json"})
	if err == nil {
		t.Fatalf("expected an error from an induced 400, got nil")
	}
	if !strings.Contains(err.Error(), "invalidate") {
		t.Fatalf("expected the error to be wrapped with context, got: %v", err)
	}
}
