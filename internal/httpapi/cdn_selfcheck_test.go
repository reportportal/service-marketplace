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
	"sync"
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

// syncBuffer is a log sink safe to read while Run is still writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startCheck runs c in the background with test-sized timings. stop cancels
// it and fails the test unless Run returns promptly.
func startCheck(t *testing.T, c *CDNSelfCheck) (logs *syncBuffer, stop func()) {
	t.Helper()
	logs = &syncBuffer{}
	c.Logger = log.New(logs, "", 0)
	c.InitialBackoff = time.Millisecond
	c.MaxBackoff = 5 * time.Millisecond
	if c.Deadline == 0 {
		c.Deadline = 2 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Errorf("Run did not return within 2s of ctx cancel")
			}
		})
	}
	t.Cleanup(stop)
	return logs, stop
}

// waitStatus polls until the check reports want, or fails after 5s.
func waitStatus(t *testing.T, c *CDNSelfCheck, logs *syncBuffer, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if status, _ := c.Result(); status == want {
			return
		}
	}
	status, reason := c.Result()
	t.Fatalf("status = %q (%s), want %q; logs:\n%s", status, reason, want, logs)
}

// fastCheck runs a self-check until its startup phase settles (anything but
// pending), stops it, and returns its result and log output.
func fastCheck(t *testing.T, c *CDNSelfCheck) (status, reason, logs string) {
	t.Helper()
	buf, stop := startCheck(t, c)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if status, _ = c.Result(); status != CDNCheckPending {
			break
		}
	}
	stop()
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
			c := &CDNSelfCheck{Store: probeStore(t, base, "test-signing-secret"), Deadline: 500 * time.Millisecond}
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

// The rolling-update case: an older replica has no probe route, verifies the
// signature, and answers the /cdn handler's own JSON 404. That reached a
// registry, so it must stay inconclusive rather than become three strikes.
func TestCDNSelfCheckOldReplicaNotFoundIsInconclusive(t *testing.T) {
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Object not found"})
	}))
	defer old.Close()

	c := &CDNSelfCheck{Store: probeStore(t, old.URL+"/cdn", "test-signing-secret"), Deadline: 500 * time.Millisecond}
	status, _, logs := fastCheck(t, c)
	if status != CDNCheckUnverified || strings.Contains(logs, "ERROR") {
		t.Fatalf("status = %q, want unverified with no ERROR; logs:\n%s", status, logs)
	}

	// A plain 404 (chi's text 404, a bucket's XML 404) is still a wrong answer.
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	c = &CDNSelfCheck{Store: probeStore(t, plain.URL+"/cdn", "test-signing-secret")}
	if status, _, logs := fastCheck(t, c); status != CDNCheckMisconfigured {
		t.Fatalf("plain 404: status = %q, want misconfigured; logs:\n%s", status, logs)
	}
}

// A redirect is judged as-is, never followed: e.g. an http→https hop that
// lands on a bucket, which a Java client would not follow either.
func TestCDNSelfCheckRedirectIsWrongAnswer(t *testing.T) {
	var bucketHits atomic.Int32
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucketHits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer bucket.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, bucket.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	c := &CDNSelfCheck{Store: probeStore(t, redirector.URL+"/cdn", "test-signing-secret")}
	status, reason, logs := fastCheck(t, c)
	if status != CDNCheckMisconfigured {
		t.Fatalf("status = %q, want misconfigured; logs:\n%s", status, logs)
	}
	if !strings.Contains(reason, "302") || !strings.Contains(reason, bucket.URL+"/cdn/") {
		t.Fatalf("reason should name the 302 and its Location: %q", reason)
	}
	if bucketHits.Load() != 0 {
		t.Fatalf("redirect was followed %d time(s)", bucketHits.Load())
	}
}

// After startup the check keeps re-probing on Interval and logs transitions:
// ok → misconfigured (after the strike threshold) → RECOVERED.
func TestCDNSelfCheckReprobesAfterStartup(t *testing.T) {
	env := newTestEnv(t)
	var broken atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		env.Server.Handler().ServeHTTP(w, r)
	}))
	defer ts.Close()

	c := &CDNSelfCheck{Store: probeStore(t, ts.URL+"/cdn", "test-signing-secret"), Interval: 2 * time.Millisecond}
	logs, _ := startCheck(t, c)
	waitStatus(t, c, logs, CDNCheckOK)

	broken.Store(true)
	waitStatus(t, c, logs, CDNCheckMisconfigured)
	broken.Store(false)
	waitStatus(t, c, logs, CDNCheckOK)

	got := logs.String()
	if strings.Count(got, "ERROR cdn self-check") != 1 || strings.Count(got, "RECOVERED") != 1 {
		t.Fatalf("expected exactly one ERROR and one RECOVERED transition log, got:\n%s", got)
	}
	if strings.Contains(got, "wrong answer") {
		t.Fatalf("steady-state strikes must not be logged, only transitions:\n%s", got)
	}
}

func TestCDNSelfCheckRunReturnsOnCancel(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()

	// Parked in the first wait: cancel must interrupt it, not wait it out.
	// (The steady phase is covered by every startCheck's stop.)
	c2 := &CDNSelfCheck{Store: probeStore(t, ts.URL+"/cdn", "test-signing-secret"), InitialBackoff: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c2.Run(ctx)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly after ctx cancel")
	}
}
