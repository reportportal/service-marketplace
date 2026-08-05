package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// capturingTransport records the single request it receives and signals done
// once RoundTrip has been called, so async callers can wait deterministically.
type capturingTransport struct {
	mu   sync.Mutex
	req  *http.Request
	body []byte
	done chan struct{}
	err  error
}

func newCapturingTransport() *capturingTransport {
	return &capturingTransport{done: make(chan struct{})}
}

func (c *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	c.mu.Lock()
	c.req = req
	c.body = body
	c.mu.Unlock()
	close(c.done)
	if c.err != nil {
		return nil, c.err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
}

func (c *capturingTransport) waitForRequest(t *testing.T) (*http.Request, []byte) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for GA4 request")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.req, c.body
}

func decodePayload(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	return payload
}

func eventParams(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	events, ok := payload["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("expected one event, got %#v", payload["events"])
	}
	event, ok := events[0].(map[string]any)
	if !ok {
		t.Fatalf("event not an object: %#v", events[0])
	}
	params, ok := event["params"].(map[string]any)
	if !ok {
		t.Fatalf("params not an object: %#v", event["params"])
	}
	return params
}

// Table-pins Enabled(): only true when both MeasurementID and APISecret are
// set. Kills mutants that swap && for || or drop one of the two checks.
func TestEnabled(t *testing.T) {
	cases := []struct {
		name          string
		measurementID string
		apiSecret     string
		want          bool
	}{
		{"both set", "M1", "S1", true},
		{"missing secret", "M1", "", false},
		{"missing measurement id", "", "S1", false},
		{"neither set", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &GA4Client{MeasurementID: tc.measurementID, APISecret: tc.apiSecret}
			if got := c.Enabled(); got != tc.want {
				t.Errorf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Pins that an unconfigured client (the default deployment) never makes an
// HTTP call. Kills a mutant that drops or inverts the Enabled() guard.
func TestTrackArtifactRequest_NotEnabledMakesNoRequest(t *testing.T) {
	var called atomic.Bool
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called.Store(true)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})
	c := &GA4Client{HTTPClient: &http.Client{Transport: transport}}

	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")

	time.Sleep(50 * time.Millisecond)
	if called.Load() {
		t.Error("unconfigured client made an HTTP request")
	}
}

// Pins that TrackArtifactRequest never blocks the caller, even when the GA4
// endpoint hangs. Kills a mutant that drops the `go func(){...}()` wrapper.
func TestTrackArtifactRequest_DoesNotBlockCaller(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		select {
		case <-block:
		case <-req.Context().Done():
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}}

	start := time.Now()
	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("TrackArtifactRequest blocked the caller for %v", elapsed)
	}
}

// Pins that an empty clientID gets replaced with a generated UUID. Kills a
// mutant that drops the uuid.NewString() fallback (empty client_id on wire).
func TestTrackArtifactRequest_GeneratesClientIDWhenEmpty(t *testing.T) {
	transport := newCapturingTransport()
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}}

	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")

	_, body := transport.waitForRequest(t)
	payload := decodePayload(t, body)
	clientID, _ := payload["client_id"].(string)
	if _, err := uuid.Parse(clientID); err != nil {
		t.Errorf("client_id = %q, want generated UUID: %v", clientID, err)
	}
}

// Pins that a caller-supplied clientID passes through unchanged. Kills a
// mutant that always overwrites it with a fresh uuid.NewString().
func TestTrackArtifactRequest_PassesThroughGivenClientID(t *testing.T) {
	transport := newCapturingTransport()
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}}

	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "existing-client-id")

	_, body := transport.waitForRequest(t)
	payload := decodePayload(t, body)
	if got := payload["client_id"]; got != "existing-client-id" {
		t.Errorf("client_id = %v, want %q", got, "existing-client-id")
	}
}

// Pins the wire shape of the GA4 event: name and every param field. Kills
// mutants that rename, drop, or swap plugin_id/version/access_tier/result.
func TestTrackArtifactRequest_PayloadShape(t *testing.T) {
	transport := newCapturingTransport()
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}}

	c.TrackArtifactRequest(context.Background(), "plugin-x", "2.3.4", "pro", ResultBlocked, "cid-1")

	_, body := transport.waitForRequest(t)
	payload := decodePayload(t, body)
	events, _ := payload["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected one event, got %#v", payload["events"])
	}
	event := events[0].(map[string]any)
	if event["name"] != "plugin_artifact_request" {
		t.Errorf("event name = %v, want plugin_artifact_request", event["name"])
	}
	params := eventParams(t, payload)
	want := map[string]any{
		"plugin_id":      "plugin-x",
		"plugin_version": "2.3.4",
		"access_tier":    "pro",
		"result":         "blocked",
	}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("params[%q] = %v, want %v", k, params[k], v)
		}
	}
	if len(params) != len(want) {
		t.Errorf("params has %d fields %#v, want exactly %#v", len(params), params, want)
	}
}

// Pins the collect URL and its measurement_id/api_secret query params, and
// the JSON content type. Kills mutants that misname a param or the endpoint.
func TestTrackArtifactRequest_RequestURLAndHeaders(t *testing.T) {
	transport := newCapturingTransport()
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}}

	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")

	req, _ := transport.waitForRequest(t)
	if req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.Method)
	}
	if req.URL.Host != "www.google-analytics.com" || req.URL.Path != "/mp/collect" {
		t.Errorf("url = %s, want host www.google-analytics.com path /mp/collect", req.URL)
	}
	q := req.URL.Query()
	if q.Get("measurement_id") != "M1" {
		t.Errorf("measurement_id = %q, want M1", q.Get("measurement_id"))
	}
	if q.Get("api_secret") != "S1" {
		t.Errorf("api_secret = %q, want S1", q.Get("api_secret"))
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// Pins that the outbound request carries a bounded deadline, so a hung GA4
// endpoint can't leak the goroutine forever. Kills a mutant that drops the
// context.WithTimeout wrapping.
func TestTrackArtifactRequest_RequestHasDeadline(t *testing.T) {
	transport := newCapturingTransport()
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}}

	before := time.Now()
	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")

	req, _ := transport.waitForRequest(t)
	deadline, ok := req.Context().Deadline()
	if !ok {
		t.Fatal("request context has no deadline")
	}
	if d := deadline.Sub(before); d <= 0 || d > 6*time.Second {
		t.Errorf("deadline %v from call is out of expected 0-5s range", d)
	}
}

// Pins that a transport error with no Logger configured does not panic.
// Kills a mutant that drops the `c.Logger != nil` guard before Printf.
func TestTrackArtifactRequest_TransportErrorNoLoggerDoesNotPanic(t *testing.T) {
	transport := newCapturingTransport()
	transport.err = errors.New("boom")
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}, Logger: nil}

	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")

	transport.waitForRequest(t)
	// Give the goroutine's post-RoundTrip logging branch a chance to run;
	// an unguarded nil Logger would panic and crash the whole test binary.
	time.Sleep(50 * time.Millisecond)
}

// Pins that a nil HTTPClient falls back to http.DefaultClient rather than
// panicking. Kills a mutant that drops the `client == nil` fallback.
func TestTrackArtifactRequest_NilHTTPClientUsesDefault(t *testing.T) {
	transport := newCapturingTransport()
	origTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = origTransport })

	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: nil}
	c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")

	transport.waitForRequest(t)
}

// Pins that concurrent calls from many goroutines are race-free (the
// handler may invoke this from many request goroutines at once).
func TestTrackArtifactRequest_ConcurrentCallsAreRaceFree(t *testing.T) {
	var count atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		count.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})
	c := &GA4Client{MeasurementID: "M1", APISecret: "S1", HTTPClient: &http.Client{Transport: transport}, Logger: log.Default()}

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.TrackArtifactRequest(context.Background(), "plugin-x", "1.0.0", "free", ResultSuccess, "")
		}(i)
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for count.Load() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := count.Load(); got != n {
		t.Errorf("received %d requests, want %d", got, n)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
