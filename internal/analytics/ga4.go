package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Result values for plugin_artifact_request (FR-R-08).
const (
	ResultSuccess   = "success"
	ResultBlocked   = "blocked"
	ResultRemoved   = "removed"
	ResultNoLicense = "no_license"
)

type GA4Client struct {
	MeasurementID string
	APISecret     string
	HTTPClient    *http.Client
	Logger        *log.Logger
}

func (c *GA4Client) Enabled() bool {
	return c.MeasurementID != "" && c.APISecret != ""
}

// TrackArtifactRequest emits plugin_artifact_request asynchronously (never blocks the response).
func (c *GA4Client) TrackArtifactRequest(ctx context.Context, pluginID, version, accessTier, result, clientID string) {
	if !c.Enabled() {
		return
	}
	if clientID == "" {
		clientID = uuid.NewString()
	}
	go func() {
		payload := map[string]any{
			"client_id": clientID,
			"events": []map[string]any{{
				"name": "plugin_artifact_request",
				"params": map[string]string{
					"plugin_id":      pluginID,
					"plugin_version": version,
					"access_tier":    accessTier,
					"result":         result,
				},
			}},
		}
		body, _ := json.Marshal(payload)
		url := "https://www.google-analytics.com/mp/collect?measurement_id=" + c.MeasurementID + "&api_secret=" + c.APISecret
		reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := c.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			if c.Logger != nil {
				c.Logger.Printf("ga4: %v", err)
			}
			return
		}
		_ = resp.Body.Close()
	}()
}
