package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/storage"
)

// probeStore returns a store that signs URLs under cdnBase with the same
// secret newTestEnv's store uses, i.e. what a sibling replica configured with
// CDN_BASE_URL=cdnBase would hand out.
func probeStore(t *testing.T, cdnBase, secret string) storage.ObjectStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), cdnBase, secret)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

// fastCheck runs a self-check with test-sized timings and returns its result
// and log output.
func fastCheck(t *testing.T, c *CDNSelfCheck) (status, reason, logs string) {
	t.Helper()
	var buf bytes.Buffer
	c.Logger = log.New(&buf, "", 0)
	c.InitialBackoff = time.Millisecond
	c.MaxBackoff = 5 * time.Millisecond
	if c.Deadline == 0 {
		c.Deadline = 2 * time.Second
	}
	c.Run(context.Background())
	status, reason = c.Result()
	return status, reason, buf.String()
}

func TestCDNProbeAnsweredOnlyForValidSignature(t *testing.T) {
	env := newTestEnv(t)
	signed, _, err := env.Store.SignedURL(context.Background(), cdnProbePrefix+"abc123", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	u, _ := url.Parse(signed)

	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn"+u.RequestURI(), nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(cdnProbeHeader); got != u.Query().Get("sig") {
		t.Fatalf("probe header = %q, want the request signature echoed", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("probe answer must not be cacheable, Cache-Control=%q", rec.Header().Get("Cache-Control"))
	}

	for name, target := range map[string]string{
		"unsigned": "/cdn/" + cdnProbePrefix + "abc123",
		"tampered": "/cdn/" + cdnProbePrefix + "other" + "?" + u.RawQuery,
	} {
		t.Run(name, func(t *testing.T) {
			rec := env.do(httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", rec.Code)
			}
			if rec.Header().Get(cdnProbeHeader) != "" {
				t.Fatal("probe header must only be sent for a verified signature")
			}
		})
	}
}

func TestCDNSelfCheckPassesWhenBaseReachesHandler(t *testing.T) {
	env := newTestEnv(t)
	ts := httptest.NewServer(env.Server.Handler())
	defer ts.Close()

	c := &CDNSelfCheck{Store: probeStore(t, ts.URL+"/cdn", "test-signing-secret")}
	status, reason, logs := fastCheck(t, c)
	if status != CDNCheckOK {
		t.Fatalf("status = %q (%s), want ok; logs:\n%s", status, reason, logs)
	}
}

// TestCDNSelfCheckFailsWhenBaseReachesBucket reproduces the dev incident:
// CDN_BASE_URL without /cdn, so the LB serves the URL from the GCS bucket.
func TestCDNSelfCheckFailsWhenBaseReachesBucket(t *testing.T) {
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "UploadServer")
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<?xml version='1.0' encoding='UTF-8'?><Error><Code>AccessDenied</Code></Error>`))
	}))
	defer bucket.Close()

	env := newTestEnv(t)
	c := &CDNSelfCheck{Store: probeStore(t, bucket.URL, "test-signing-secret")}
	status, reason, logs := fastCheck(t, c)
	if status != CDNCheckMisconfigured {
		t.Fatalf("status = %q, want misconfigured; logs:\n%s", status, logs)
	}
	for _, want := range []string{bucket.URL + "/" + cdnProbePrefix, "403", "UploadServer", "/cdn"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not mention %q", reason, want)
		}
	}
	if strings.Contains(reason, "sig=") {
		t.Errorf("reason must not include the signature: %q", reason)
	}
	if !strings.Contains(logs, "ERROR cdn self-check") {
		t.Errorf("expected an ERROR log line, got:\n%s", logs)
	}

	env.Server.deps.CDNSelfCheck = c
	rec := env.do(httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/ready must stay 200 (advisory check), got %d", rec.Code)
	}
	var body struct {
		Status string
		Checks struct {
			CDN struct{ Status, Reason string }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /ready: %v", err)
	}
	if body.Status != "degraded" || body.Checks.CDN.Status != CDNCheckMisconfigured || body.Checks.CDN.Reason == "" {
		t.Fatalf("unexpected /ready body: %s", rec.Body.String())
	}
}

// A registry that answers but cannot verify our signature (different
// STORAGE_SIGNING_SECRET) cannot verify premium URLs either.
func TestCDNSelfCheckFailsAgainstRegistryWithOtherSecret(t *testing.T) {
	env := newTestEnv(t)
	ts := httptest.NewServer(env.Server.Handler())
	defer ts.Close()

	c := &CDNSelfCheck{Store: probeStore(t, ts.URL+"/cdn", "a-different-signing-secret")}
	if status, _, logs := fastCheck(t, c); status != CDNCheckMisconfigured {
		t.Fatalf("status = %q, want misconfigured; logs:\n%s", status, logs)
	}
}

// Network errors and 5xx (an LB with no ready backend yet) prove nothing:
// they must never mark the deployment misconfigured.
func TestCDNSelfCheckTransientErrorsNeverFail(t *testing.T) {
	var hits atomic.Int32
	lb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer lb.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	for name, base := range map[string]string{"502": lb.URL + "/cdn", "connection refused": closedURL + "/cdn"} {
		t.Run(name, func(t *testing.T) {
			c := &CDNSelfCheck{Store: probeStore(t, base, "test-signing-secret"), Deadline: 50 * time.Millisecond}
			if status, _ := c.Result(); status != CDNCheckPending {
				t.Fatalf("status before Run = %q, want pending", status)
			}
			status, reason, logs := fastCheck(t, c)
			if status != CDNCheckUnverified {
				t.Fatalf("status = %q, want unverified; logs:\n%s", status, logs)
			}
			if reason == "" || strings.Contains(logs, "ERROR") {
				t.Fatalf("expected a reason and no ERROR; reason=%q logs:\n%s", reason, logs)
			}

			env := newTestEnv(t)
			env.Server.deps.CDNSelfCheck = c
			rec := env.do(httptest.NewRequest(http.MethodGet, "/ready", nil))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ready"`) {
				t.Fatalf("/ready = %d %s, want 200 ready", rec.Code, rec.Body.String())
			}
		})
	}
	if hits.Load() < 2 {
		t.Fatalf("expected retries against the 502 backend, got %d attempt(s)", hits.Load())
	}
}

// A 5xx in between must not reset or hide a later definite answer: the
// check keeps retrying through it and then succeeds.
func TestCDNSelfCheckRetriesUntilHandlerReachable(t *testing.T) {
	env := newTestEnv(t)
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		env.Server.Handler().ServeHTTP(w, r)
	}))
	defer ts.Close()

	c := &CDNSelfCheck{Store: probeStore(t, ts.URL+"/cdn", "test-signing-secret")}
	if status, _, logs := fastCheck(t, c); status != CDNCheckOK {
		t.Fatalf("status = %q, want ok after transient 503s; logs:\n%s", status, logs)
	}
}

func TestCDNSelfCheckDisabled(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer ts.Close()

	c := &CDNSelfCheck{Store: probeStore(t, ts.URL, "test-signing-secret"), Disabled: true}
	if status, _, _ := fastCheck(t, c); status != CDNCheckDisabled {
		t.Fatalf("status = %q, want disabled", status)
	}
	if hits.Load() != 0 {
		t.Fatalf("disabled check made %d request(s)", hits.Load())
	}

	env := newTestEnv(t)
	env.Server.deps.CDNSelfCheck = c
	rec := env.do(httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"disabled"`) {
		t.Fatalf("/ready = %d %s", rec.Code, rec.Body.String())
	}
}
