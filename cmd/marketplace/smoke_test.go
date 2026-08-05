package main_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// This package has no other tests: main.go's wiring -- config load, store
// construction, dependency assembly, route registration, shutdown -- is not
// exercised anywhere else. internal/httpapi covers the handlers against a
// hand-assembled Server, which cannot catch a dependency main() forgets to
// pass or a route it never registers.
//
// So this is deliberately a wiring smoke test, not a second copy of the
// business-logic suite: start the real binary, prove one publish round trip
// reaches storage and comes back out through /cdn, prove the guards are
// actually mounted, and prove SIGTERM exits.

const smokePassword = "smoke-test-password"

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// smokeJAR builds a minimal plugin jar: a zip holding marketplace-manifest.json.
func smokeJAR(t *testing.T, id, version, access, contactURL string) []byte {
	t.Helper()
	m := map[string]any{
		"id":            id,
		"name":          "Smoke " + id,
		"version":       version,
		"description":   "Wiring smoke-test artifact for the marketplace registry.",
		"author":        map[string]string{"name": "smoke"},
		"license":       "Apache-2.0",
		"category":      "other",
		"access":        access,
		"compatibility": map[string]string{"reportportal": ">=5.0.0"},
	}
	if contactURL != "" {
		m["contactUrl"] = contactURL
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("marketplace-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func publishBody(t *testing.T, jar []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("jar", "plugin.jar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(jar); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

type smokeEnv struct {
	base  string
	token string
	t     *testing.T
}

func (e *smokeEnv) do(method, path string, body io.Reader, hdr map[string]string) (int, []byte) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.base+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	// Never auto-follow: the artifact route's redirect target is part of what
	// this test is checking.
	c := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp.StatusCode, data
}

func (e *smokeEnv) status(method, path string, hdr map[string]string) int {
	e.t.Helper()
	code, _ := e.do(method, path, nil, hdr)
	return code
}

// startMarketplace builds and runs the real binary, returning its base URL.
func startMarketplace(t *testing.T) *smokeEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("smoke test builds and runs the binary")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "marketplace")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(smokePassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"STORAGE_TYPE=local",
		"STORAGE_LOCAL_ROOT="+filepath.Join(dir, "data"),
		fmt.Sprintf("HTTP_ADDR=127.0.0.1:%d", port),
		"CDN_BASE_URL="+base+"/cdn",
		"STORAGE_SIGNING_SECRET=storage-signing-secret-long-enough-for-validation",
		"JWT_SECRET=jwt-secret-long-enough-for-validation-000000000000",
		"ADMIN_PASSWORD_HASH="+string(hash),
		// Keep the child from inheriting a developer's own settings.
		"GITHUB_OAUTH_CLIENT_ID=", "GITHUB_OAUTH_CLIENT_SECRET=",
		"PUBLISH_OIDC_ALLOWED_SOURCES={}", "CDN_URL_MAP=", "GA4_MEASUREMENT_ID=",
	)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Errorf("did not exit within 10s of SIGTERM; log:\n%s", logs.String())
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return &smokeEnv{base: base, t: t}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("never became healthy; log:\n%s", logs.String())
	return nil
}

// TestSmokeWiring proves main() assembles a working service: the binary boots
// on a real socket, an operator can log in, a publish reaches storage and is
// served back out through /cdn byte-for-byte, and the reserved-namespace and
// license guards are actually mounted on the running router.
//
// Mutations verified to turn this red: setting Deps.Lifecycle to nil in
// main(), and dropping the r.Get("/cdn/*", ...) registration from the router.
// Neither is visible to internal/httpapi's hand-assembled Server.
//
// Known blind spot: pointing the store at a different root still passes,
// because everything this test asserts is created through the API and so
// stays self-consistent. It checks that the wiring works, not that it points
// where the configuration says.
func TestSmokeWiring(t *testing.T) {
	e := startMarketplace(t)

	t.Run("health and ready", func(t *testing.T) {
		if got := e.status(http.MethodGet, "/health", nil); got != http.StatusOK {
			t.Errorf("/health = %d, want 200", got)
		}
		if got := e.status(http.MethodGet, "/ready", nil); got != http.StatusOK {
			t.Errorf("/ready = %d, want 200", got)
		}
	})

	t.Run("operator routes reject anonymous callers", func(t *testing.T) {
		for _, p := range []string{"/api/v1/plugins", "/api/v1/licenses"} {
			if got := e.status(http.MethodPost, p, nil); got != http.StatusUnauthorized {
				t.Errorf("anonymous POST %s = %d, want 401", p, got)
			}
		}
	})

	t.Run("reserved namespaces are guarded on the running router", func(t *testing.T) {
		for _, p := range []string{
			"/cdn/auth/authorized_keys.json",
			"/cdn/Auth/authorized_keys.json", // case alias
			"/cdn/auth",                      // bare root
		} {
			if got := e.status(http.MethodGet, p, nil); got != http.StatusForbidden {
				t.Errorf("GET %s = %d, want 403", p, got)
			}
		}
	})

	// Log in once; the rest of the flow needs an operator session.
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": smokePassword})
	code, data := e.do(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body),
		map[string]string{"Content-Type": "application/json"})
	if code != http.StatusOK {
		t.Fatalf("login = %d: %s", code, data)
	}
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &login); err != nil || login.AccessToken == "" {
		t.Fatalf("login response has no accessToken: %s", data)
	}
	e.token = login.AccessToken

	t.Run("publish round trip reaches storage and comes back through /cdn", func(t *testing.T) {
		jar := smokeJAR(t, "smoke-plugin", "1.0.0", "public", "")
		pb, ct := publishBody(t, jar)
		code, data := e.do(http.MethodPost, "/api/v1/plugins", pb,
			map[string]string{"Authorization": "Bearer " + e.token, "Content-Type": ct})
		if code != http.StatusCreated {
			t.Fatalf("publish = %d: %s", code, data)
		}
		var pub struct {
			SHA256 string `json:"sha256"`
		}
		if err := json.Unmarshal(data, &pub); err != nil {
			t.Fatal(err)
		}

		if code, data := e.do(http.MethodGet, "/api/v1/plugins", nil, nil); code != http.StatusOK ||
			!bytes.Contains(data, []byte(`"smoke-plugin"`)) {
			t.Fatalf("catalogue = %d %s, want the published plugin", code, data)
		}

		// The artifact route redirects to /cdn on this same host. It must be
		// SERVED there, not redirected again -- a self-redirect loops forever.
		code, _ = e.do(http.MethodGet, "/api/v1/plugins/smoke-plugin/versions/1.0.0/artifact", nil, nil)
		if code != http.StatusFound {
			t.Fatalf("artifact = %d, want 302", code)
		}
		code, jarBytes := e.do(http.MethodGet,
			"/cdn/plugins/smoke-plugin/versions/1.0.0/smoke-plugin-1.0.0.jar", nil, nil)
		if code != http.StatusOK {
			t.Fatalf("/cdn artifact = %d, want 200 (a 302 here would be a redirect loop)", code)
		}
		if !bytes.Equal(jarBytes, jar) {
			t.Errorf("downloaded %d bytes, want the %d published", len(jarBytes), len(jar))
		}
	})

	t.Run("premium artifact needs a license and its jar is signature-gated", func(t *testing.T) {
		jar := smokeJAR(t, "smoke-premium", "1.0.0", "premium", "https://example.com/contact")
		pb, ct := publishBody(t, jar)
		if code, data := e.do(http.MethodPost, "/api/v1/plugins", pb,
			map[string]string{"Authorization": "Bearer " + e.token, "Content-Type": ct}); code != http.StatusCreated {
			t.Fatalf("publish premium = %d: %s", code, data)
		}

		if got := e.status(http.MethodGet,
			"/api/v1/plugins/smoke-premium/versions/1.0.0/artifact", nil); got != http.StatusUnauthorized {
			t.Errorf("premium artifact without a license = %d, want 401", got)
		}
		if got := e.status(http.MethodGet,
			"/cdn/private/plugins/smoke-premium/versions/1.0.0/smoke-premium-1.0.0.jar", nil); got != http.StatusForbidden {
			t.Errorf("unsigned private jar off /cdn = %d, want 403", got)
		}
	})

	t.Run("removal tombstones every read path", func(t *testing.T) {
		rb, _ := json.Marshal(map[string]string{"removalReason": "smoke test"})
		if code, data := e.do(http.MethodDelete, "/api/v1/plugins/smoke-plugin", bytes.NewReader(rb),
			map[string]string{"Authorization": "Bearer " + e.token, "Content-Type": "application/json"}); code != http.StatusOK {
			t.Fatalf("remove = %d: %s", code, data)
		}
		for _, p := range []string{
			"/api/v1/plugins/smoke-plugin",
			"/api/v1/plugins/smoke-plugin/versions",
			"/api/v1/plugins/smoke-plugin/versions/1.0.0",
			"/api/v1/plugins/smoke-plugin/versions/1.0.0/artifact",
		} {
			if got := e.status(http.MethodGet, p, nil); got != http.StatusGone {
				t.Errorf("GET %s after removal = %d, want 410", p, got)
			}
		}
	})
}
