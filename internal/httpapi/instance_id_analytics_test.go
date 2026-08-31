package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/analytics"
	"github.com/reportportal/service-marketplace/internal/domain"
)

// capturedGA collects the Measurement Protocol bodies the registry emits, so a test can read
// the client_id GA would have been told.
type capturedGA struct {
	bodies chan map[string]any
}

func (c *capturedGA) RoundTrip(r *http.Request) (*http.Response, error) {
	var payload map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}
	select {
	case c.bodies <- payload:
	default:
	}
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: r}, nil
}

func (c *capturedGA) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case payload := <-c.bodies:
		return payload
	case <-time.After(2 * time.Second):
		t.Fatal("no GA event was emitted")
		return nil
	}
}

// gaEnv is a test env whose analytics client is enabled and pointed at a capturing transport.
func gaEnv(t *testing.T) (*testEnv, *capturedGA) {
	t.Helper()
	env := newTestEnv(t)
	captured := &capturedGA{bodies: make(chan map[string]any, 4)}
	env.Server.deps.Analytics = &analytics.GA4Client{
		MeasurementID: "M1",
		APISecret:     "S1",
		HTTPClient:    &http.Client{Transport: captured},
	}
	return env, captured
}

func publishForArtifact(t *testing.T, env *testEnv) {
	t.Helper()
	m := premiumManifest(testOIDCPluginID, "https://reportportal.io/contact")
	m.Access = domain.AccessPublic
	if rec := publishViaHTTP(t, env, m); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestArtifactRequestReportsTheInstanceIdAsGAClientId is the registry half of FR-L-06: the
// header service-api sends becomes the GA client_id, which is the only thing that lets GA tell
// twenty downloads by one instance from twenty instances downloading once (FR-OP-09/10).
//
// Kills reading the wrong header name, or dropping clientID on the way to TrackArtifactRequest.
func TestArtifactRequestReportsTheInstanceIdAsGAClientId(t *testing.T) {
	env, captured := gaEnv(t)
	publishForArtifact(t, env)
	const instanceID = "3f2b1a44-0000-4000-8000-000000000001"

	req := env.newRequest(http.MethodGet,
		"/api/v1/plugins/"+testOIDCPluginID+"/versions/1.0.0/artifact", credNone, nil, "")
	req.Header.Set("X-RP-Instance-Id", instanceID)
	env.do(req)

	if got := captured.next(t)["client_id"]; got != instanceID {
		t.Errorf("GA client_id = %v, want the instance id %q", got, instanceID)
	}
}

// TestArtifactRequestWithoutAnInstanceIdStillCounts is the opt-out half: the download works and
// the event still goes out, against an id of its own — so the request is counted, and only the
// unique-instance figure loses it.
func TestArtifactRequestWithoutAnInstanceIdStillCounts(t *testing.T) {
	env, captured := gaEnv(t)
	publishForArtifact(t, env)

	req := env.newRequest(http.MethodGet,
		"/api/v1/plugins/"+testOIDCPluginID+"/versions/1.0.0/artifact", credNone, nil, "")
	env.do(req)

	got, _ := captured.next(t)["client_id"].(string)
	if got == "" {
		t.Fatal("no client_id was sent, so the request would not be counted at all")
	}
	if got == "X-RP-Instance-Id" || len(got) < 10 {
		t.Errorf("client_id = %q, want a generated anonymous id", got)
	}
}
