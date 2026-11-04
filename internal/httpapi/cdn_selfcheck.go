package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
// ingress the probe cannot succeed until the pod is ready. Network errors,
// 5xx (an LB with no ready backend yet) and the registry's own JSON 404 (an
// older replica without the probe route, mid-rollout) are inconclusive; only
// MisconfiguredAfter definite wrong answers without an ok in between mark it
// misconfigured.
//
// Run has two phases: a startup phase that retries with backoff until the
// answer is ok or misconfigured (or Deadline passes, leaving it unverified),
// then a slow re-check every Interval that logs transitions only.
type CDNSelfCheck struct {
	Store    storage.ObjectStore
	Disabled bool
	Client   *http.Client
	Logger   *log.Logger

	// Zero values take the production defaults applied in Run.
	InitialBackoff     time.Duration
	MaxBackoff         time.Duration
	Deadline           time.Duration
	Interval           time.Duration
	MisconfiguredAfter int

	mu     sync.Mutex
	status string
	reason string

	// wrong counts definite wrong answers since the last ok; Run's goroutine only.
	wrong int
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

// Run probes until ctx ends. It never returns an error: startup must not
// depend on the public host.
func (c *CDNSelfCheck) Run(ctx context.Context) {
	if c.Disabled {
		return
	}
	logger := c.Logger
	if logger == nil {
		logger = log.Default()
	}
	// Judge the first response, never a redirect target: a client such as
	// Java's HttpURLConnection will not follow e.g. an http→https hop, and a
	// redirect loop must not be mistaken for a network error.
	client := &http.Client{Timeout: 10 * time.Second}
	if c.Client != nil {
		copied := *c.Client
		client = &copied
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	backoff := durationOr(c.InitialBackoff, time.Second)
	maxBackoff := durationOr(c.MaxBackoff, time.Minute)
	deadline := time.Now().Add(durationOr(c.Deadline, 15*time.Minute))

	// Startup phase. The first attempt also waits, so the listener is up.
	var lastTransient string
	for {
		if !sleepCtx(ctx, backoff) {
			return
		}
		if msg := c.observe(ctx, client, logger, true); msg != "" {
			lastTransient = msg
		}
		if status, _ := c.Result(); status == CDNCheckOK || status == CDNCheckMisconfigured {
			break
		}
		backoff = min(backoff*2, maxBackoff)
		if !time.Now().Add(backoff).Before(deadline) {
			reason := "no conclusive answer from CDN_BASE_URL yet (network error, 5xx, or an older replica without the probe route): " + lastTransient
			logger.Printf("WARNING cdn self-check: %s (set CDN_SELF_CHECK=false if the registry cannot reach its own public host)", reason)
			c.set(CDNCheckUnverified, reason)
			break
		}
	}

	// Steady phase: cheap periodic re-check, logging transitions only.
	ticker := time.NewTicker(durationOr(c.Interval, 10*time.Minute))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.observe(ctx, client, logger, false)
		}
	}
}

// observe makes one probe and applies its result to the state. It returns
// the message of an inconclusive attempt, "" otherwise. verbose logs every
// attempt (startup); otherwise only state transitions are logged.
func (c *CDNSelfCheck) observe(ctx context.Context, client *http.Client, logger *log.Logger, verbose bool) string {
	threshold := c.MisconfiguredAfter
	if threshold <= 0 {
		threshold = 3
	}
	prev, _ := c.Result()
	ok, definite, msg := c.probe(ctx, client)
	switch {
	case ok:
		c.wrong = 0
		switch prev {
		case CDNCheckMisconfigured:
			logger.Printf("cdn self-check: RECOVERED, CDN_BASE_URL now reaches this registry's /cdn handler")
		case CDNCheckOK:
		default:
			logger.Printf("cdn self-check: ok, CDN_BASE_URL reaches this registry's /cdn handler")
		}
		c.set(CDNCheckOK, "")
	case definite:
		c.wrong++
		if c.wrong >= threshold {
			if prev != CDNCheckMisconfigured {
				logger.Printf("ERROR cdn self-check: %s", msg)
			}
			c.set(CDNCheckMisconfigured, msg)
		} else if verbose {
			logger.Printf("cdn self-check: wrong answer %d/%d, retrying: %s", c.wrong, threshold, msg)
		}
	default:
		if verbose {
			logger.Printf("cdn self-check: inconclusive, retrying: %s", msg)
		}
		return msg
	}
	return ""
}

// probe makes one attempt. definite is true when something other than this
// registry answered; false with !ok means the attempt proved nothing.
func (c *CDNSelfCheck) probe(ctx context.Context, client *http.Client) (ok, definite bool, msg string) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return false, false, fmt.Sprintf("nonce: %v", err)
	}
	// Five minutes leaves room for clock skew between replicas.
	signed, _, err := c.Store.SignedURL(ctx, cdnProbePrefix+hex.EncodeToString(nonce), 5*time.Minute)
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if sig != "" && resp.Header.Get(cdnProbeHeader) == sig && resp.StatusCode < 300 {
		return true, false, ""
	}
	desc := fmt.Sprintf("GET %s answered %d (server: %q)", shown, resp.StatusCode, resp.Header.Get("Server"))
	if loc := resp.Header.Get("Location"); loc != "" {
		desc += fmt.Sprintf(" redirecting to %q", loc)
	}
	if resp.StatusCode >= 500 || isRegistryNotFound(resp, body) {
		return false, false, desc
	}
	return false, true, desc + " without this registry's probe answer: CDN_BASE_URL does not reach this registry's /cdn handler " +
		"(or reaches one with a different STORAGE_SIGNING_SECRET), so signed premium URLs cannot be verified and public " +
		"artifact/changelog URLs are served by something else; CDN_BASE_URL usually needs to end with /cdn"
}

// isRegistryNotFound reports the /cdn handler's own "Object not found"
// envelope: what a replica predating the probe route answers after
// verifying the signature. It proves the URL reached a registry, not that the
// probe failed, so a rolling update must not count it against the new pods.
func isRegistryNotFound(resp *http.Response, body []byte) bool {
	if resp.StatusCode != http.StatusNotFound || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return false
	}
	var e ErrorResponse
	return json.Unmarshal(body, &e) == nil && e.Code == CodeNotFound
}

// sleepCtx waits d or until ctx ends, reporting whether ctx is still live.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func durationOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
