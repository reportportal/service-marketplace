package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/reportportal/service-marketplace/internal/storage"
)

// cdnProbePrefix is a reserved object namespace that handleCDNProxy answers
// itself and never reads from storage. Nothing the registry writes lives
// under it (every object is index.json, plugins/, private/ or auth/), and the
// leading-dot segment survives storage.CanonicalizeObjectPath unchanged.
const cdnProbePrefix = ".well-known/marketplace-cdn-probe/"

// cdnProbeHeader carries the verified signature back to the prober. Only a
// handler holding STORAGE_SIGNING_SECRET can decide to echo it, so a bucket,
// a load balancer default backend or another registry cannot pass the check
// by answering 200.
const cdnProbeHeader = "X-Marketplace-CDN-Probe"

// isCDNProbePath reports whether a canonical object path is a self-check probe.
func isCDNProbePath(objectPath string) bool {
	return strings.HasPrefix(objectPath, cdnProbePrefix)
}

// serveCDNProbe answers a self-check probe: it only succeeds for a signature
// this registry's store accepts, and the answer must never be cached (every
// probe path is unique anyway, but a stale 200 would defeat the check).
func (s *Server) serveCDNProbe(w http.ResponseWriter, objectPath, exp, sig string) {
	w.Header().Set("Cache-Control", "no-store")
	if exp == "" || sig == "" || !s.deps.Store.VerifySignedURL(objectPath, exp, sig) {
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid signed URL"})
		return
	}
	w.Header().Set(cdnProbeHeader, sig)
	w.WriteHeader(http.StatusNoContent)
}

// CDN self-check states, reported by /ready under checks.cdn.status.
const (
	CDNCheckDisabled      = "disabled"
	CDNCheckPending       = "pending"
	CDNCheckOK            = "ok"
	CDNCheckMisconfigured = "misconfigured"
	CDNCheckUnverified    = "unverified"
)

// CDNSelfCheck verifies that the URLs the registry hands out (built from
// CDN_BASE_URL by Store.PublicURL / Store.SignedURL) reach this registry's
// /cdn handler. It signs a unique probe path with Store.SignedURL — the exact
// code path premium artifact URLs take — GETs it, and requires the handler's
// echo of the signature.
//
// It is advisory by design: /ready stays 200 whatever it finds, because
// /ready gates traffic, and when CDN_BASE_URL routes back through the same
// ingress the probe cannot succeed until the pod is ready. Network errors and
// 5xx (an LB with no ready backend yet) are inconclusive and retried; only
// MisconfiguredAfter definite wrong answers mark the deployment degraded.
type CDNSelfCheck struct {
	Store    storage.ObjectStore
	Disabled bool
	Client   *http.Client
	Logger   *log.Logger

	// Zero values take the production defaults applied in Run.
	InitialBackoff     time.Duration
	MaxBackoff         time.Duration
	Deadline           time.Duration
	MisconfiguredAfter int

	mu     sync.Mutex
	status string
	reason string
}

// Result returns the current status and, for a non-ok status, why.
func (c *CDNSelfCheck) Result() (status, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Disabled {
		return CDNCheckDisabled, ""
	}
	if c.status == "" {
		return CDNCheckPending, ""
	}
	return c.status, c.reason
}

func (c *CDNSelfCheck) set(status, reason string) {
	c.mu.Lock()
	c.status, c.reason = status, reason
	c.mu.Unlock()
}

// Run probes until it gets a definite answer, ctx ends or Deadline passes.
// It never returns an error: startup must not depend on the public host.
func (c *CDNSelfCheck) Run(ctx context.Context) {
	if c.Disabled {
		return
	}
	logger := c.Logger
	if logger == nil {
		logger = log.Default()
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	backoff := durationOr(c.InitialBackoff, time.Second)
	maxBackoff := durationOr(c.MaxBackoff, time.Minute)
	threshold := c.MisconfiguredAfter
	if threshold <= 0 {
		threshold = 3
	}
	deadline := time.Now().Add(durationOr(c.Deadline, 15*time.Minute))

	wrong := 0
	var lastTransient string
	for {
		ok, definite, msg := c.probe(ctx, client)
		switch {
		case ok:
			if status, _ := c.Result(); status == CDNCheckMisconfigured {
				logger.Printf("cdn self-check: RECOVERED, CDN_BASE_URL now reaches this registry's /cdn handler")
			} else {
				logger.Printf("cdn self-check: ok, CDN_BASE_URL reaches this registry's /cdn handler")
			}
			c.set(CDNCheckOK, "")
			return
		case definite:
			wrong++
			if wrong >= threshold {
				if status, _ := c.Result(); status != CDNCheckMisconfigured {
					logger.Printf("ERROR cdn self-check: %s", msg)
				}
				c.set(CDNCheckMisconfigured, msg)
			} else {
				logger.Printf("cdn self-check: wrong answer %d/%d, retrying: %s", wrong, threshold, msg)
			}
		default:
			lastTransient = msg
			logger.Printf("cdn self-check: inconclusive, retrying: %s", msg)
		}

		if !time.Now().Add(backoff).Before(deadline) {
			if status, _ := c.Result(); status != CDNCheckMisconfigured {
				reason := "gave up before getting an answer from CDN_BASE_URL: " + lastTransient
				logger.Printf("WARNING cdn self-check: %s (set CDN_SELF_CHECK=false if the registry cannot reach its own public host)", reason)
				c.set(CDNCheckUnverified, reason)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// probe makes one attempt. definite is true when something other than this
// registry answered; false with !ok means the attempt proved nothing.
func (c *CDNSelfCheck) probe(ctx context.Context, client *http.Client) (ok, definite bool, msg string) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return false, false, fmt.Sprintf("nonce: %v", err)
	}
	signed, _, err := c.Store.SignedURL(ctx, cdnProbePrefix+hex.EncodeToString(nonce), time.Minute)
	if err != nil {
		return false, false, fmt.Sprintf("sign probe URL: %v", err)
	}
	// Log the probe URL without its signature; the path alone shows the base.
	shown, _, _ := strings.Cut(signed, "?")
	u, err := url.Parse(signed)
	if err != nil {
		return false, true, fmt.Sprintf("CDN_BASE_URL does not form a valid URL (%s): %v", shown, err)
	}
	sig := u.Query().Get("sig")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if err != nil {
		return false, true, fmt.Sprintf("CDN_BASE_URL does not form a valid URL (%s): %v", shown, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, false, fmt.Sprintf("GET %s: %v", shown, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	if sig != "" && resp.Header.Get(cdnProbeHeader) == sig && resp.StatusCode < 300 {
		return true, false, ""
	}
	desc := fmt.Sprintf("GET %s answered %d (server: %q)", shown, resp.StatusCode, resp.Header.Get("Server"))
	if resp.StatusCode >= 500 {
		return false, false, desc
	}
	return false, true, desc + " without this registry's probe answer: CDN_BASE_URL does not reach this registry's /cdn handler " +
		"(or reaches one with a different STORAGE_SIGNING_SECRET), so signed premium URLs cannot be verified and public " +
		"artifact/changelog URLs are served by something else; CDN_BASE_URL usually needs to end with /cdn"
}

func durationOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
