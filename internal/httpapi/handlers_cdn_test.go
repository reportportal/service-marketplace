package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/storage"
)

// TestHandleCDNProxyGuardCannotBeWalkedAroundByTraversal is the regression
// test for the guard-bypass this branch closes: IsAuthObject (and
// IsPrivateObject) must judge the exact object path that will actually be
// read, not a pre-canonicalization alias of it. Before the fix,
// handleCDNProxy checked storage.IsAuthObject against the raw, uncleaned
// request path while LocalStore's own path resolution (abs()) cleans "."
// and ".." segments internally — so a request for
// "/cdn/x/../auth/authorized_keys.json" was NOT caught by IsAuthObject (its
// raw prefix is "x/", not "auth/") but resolved, once cleaned, to exactly
// the protected object "auth/authorized_keys.json" and was served.
func TestHandleCDNProxyGuardCannotBeWalkedAroundByTraversal(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	secret := "top-secret-authorized-keys-payload"
	if _, err := env.Store.Write(ctx, storage.PathAuthorizedKeys, []byte(secret), 0); err != nil {
		t.Fatalf("seed auth object: %v", err)
	}

	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/x/../"+storage.PathAuthorizedKeys, nil))

	if rec.Code == http.StatusOK {
		t.Fatalf("traversal alias of an auth object must not be served; got 200 body=%s", rec.Body.String())
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (the same guard a direct request gets), got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Code != CodeForbidden {
		t.Fatalf("expected code %q, got %q", CodeForbidden, body.Code)
	}
}

// TestHandleCDNProxyDirectAuthPathStillForbidden pins the ordinary,
// non-traversal case alongside the alias case above, so the fix can't pass
// by accident (e.g. by rejecting every request containing "..").
//
// It presents a signature that VerifySignedURL accepts (auth/ paths are
// also IsPrivateObject, so a signature-only check would let this through):
// storage.IsAuthObject must be an absolute bar, checked and enforced before
// signature verification is ever consulted, not merely one of two
// alternative gates a valid signature can satisfy instead.
func TestHandleCDNProxyDirectAuthPathStillForbidden(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	if _, err := env.Store.Write(ctx, storage.PathAuthorizedKeys, []byte("secret"), 0); err != nil {
		t.Fatalf("seed auth object: %v", err)
	}
	url, _, err := env.Store.SignedURL(ctx, storage.PathAuthorizedKeys, time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	query := url[len("http://cdn.test/"+storage.PathAuthorizedKeys):]

	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+storage.PathAuthorizedKeys+query, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 even with a valid signature attached, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCDNProxyRegisteredRegardlessOfBackend proves the /cdn edge's
// signed-URL enforcement is storage-backend-independent by construction: it
// runs the exact same router against a storage.ObjectStore double that is
// NOT a *storage.LocalStore, wired the same way a GCS deployment would be
// (Deps.LocalStore left nil — see router.go and cmd/marketplace/main.go,
// which only sets LocalStore when STORAGE_TYPE=local). Before this branch,
// router.go registered GET /cdn/* only when Deps.LocalStore != nil, so this
// exact request 404'd (no route at all) regardless of what Deps.Store held.
func TestHandleCDNProxyRegisteredRegardlessOfBackend(t *testing.T) {
	env := newTestEnv(t)
	backend := newFakeObjectStore()
	backend.objects["index.json"] = []byte(`{"plugins":[]}`)

	d := env.Server.deps
	d.Store = backend
	d.LocalStore = nil // exactly what a GCS deployment wires (see router.go)
	srv := NewServer(d)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cdn/index.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /cdn to be served from a non-LocalStore backend with LocalStore==nil, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"plugins":[]}` {
		t.Fatalf("expected the fake backend's bytes, got %q", rec.Body.String())
	}
}

// TestHandleCDNProxySignedURLEnforcementIsBackendIndependent exercises the
// full enforcement matrix (missing signature, invalid signature, valid
// signature) for a private object against the same fakeObjectStore double,
// pinning that a non-LocalStore backend gets identical treatment to
// LocalStore for the exact same inputs.
func TestHandleCDNProxySignedURLEnforcementIsBackendIndependent(t *testing.T) {
	env := newTestEnv(t)
	backend := newFakeObjectStore()
	const objectPath = "private/plugins/p/versions/1.0.0/p-1.0.0.jar"
	backend.objects[objectPath] = []byte("jar-bytes")

	d := env.Server.deps
	d.Store = backend
	d.LocalStore = nil
	srv := NewServer(d)

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	if rec := get("/cdn/" + objectPath); rec.Code != http.StatusForbidden {
		t.Fatalf("no signature on a private object: expected 403, got %d", rec.Code)
	}
	if rec := get("/cdn/" + objectPath + "?exp=1&sig=deadbeef"); rec.Code != http.StatusForbidden {
		t.Fatalf("garbage signature: expected 403, got %d", rec.Code)
	}

	url, _, err := backend.SignedURL(context.Background(), objectPath, time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	// url is cdnBase+"/"+CDNPath+"?exp=..&sig=.." — reuse just the query.
	target := "/cdn/" + objectPath + url[len(backend.cdnBase()+"/"+objectPath):]
	if rec := get(target); rec.Code != http.StatusOK {
		t.Fatalf("valid signature: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCDNProxyExpiryUsesInjectedClock proves the expiry boundary is
// judged against LocalStore.Now, not wall time: a URL signed to expire one
// minute from an injected "now" is accepted while that same injected clock
// is still before the boundary, and rejected once the store's injected
// clock is moved past it — without sleeping.
func TestHandleCDNProxyExpiryUsesInjectedClock(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const pluginID, version = "plugin-clock", "1.0.0"
	artPath := storage.VersionArtifactPath(pluginID, version, "premium")
	if _, err := env.Store.Write(ctx, artPath, []byte("jar-bytes"), 0); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	env.Store.Now = func() time.Time { return fixedNow }
	url, _, err := env.Store.SignedURL(ctx, artPath, time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	target := "/cdn/" + artPath + url[len("http://cdn.test/"+artPath):]

	// Still before expiry per the injected clock.
	env.Store.Now = func() time.Time { return fixedNow.Add(30 * time.Second) }
	if rec := env.do(httptest.NewRequest(http.MethodGet, target, nil)); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 before the injected-clock expiry boundary, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Past expiry per the injected clock (real wall time is irrelevant either way).
	env.Store.Now = func() time.Time { return fixedNow.Add(90 * time.Second) }
	if rec := env.do(httptest.NewRequest(http.MethodGet, target, nil)); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 after the injected-clock expiry boundary, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCDNProxyServesIncompleteVersionBytes documents and pins the
// deliberate decision (see handleCDNProxy's doc comment) that the /cdn edge
// does NOT consult plugin.json's per-version Complete flag before serving
// an object's bytes: it serves whatever exists at the requested storage key,
// same as it always has, even for a version stage 3 already makes every API
// read path 404. If a future change enforces completeness at the edge, this
// test's failure is the flag that the doc comment needs updating too, not a
// silent behavioural drift.
func TestHandleCDNProxyServesIncompleteVersionBytes(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const pluginID, version = "plugin-incomplete", "1.0.0"
	manifestPath := storage.VersionManifestPath(pluginID, version)
	if _, err := env.Store.Write(ctx, manifestPath, []byte(`{"id":"plugin-incomplete"}`), 0); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	// Deliberately no plugin.json at all for this plugin — furthest thing
	// from "complete" a version can be, short of the object not existing.

	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+manifestPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected the edge to serve an object that exists regardless of publish-completeness, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- fakeObjectStore -----------------------------------------------------

// fakeObjectStore is a minimal, entirely independent storage.ObjectStore
// implementation (an in-memory map, not backed by LocalStore or GCSStore in
// any way) used to prove handleCDNProxy and its router registration do not
// special-case LocalStore. It shares the same signing scheme as the real
// backends purely because that scheme is the wire contract clients need to
// reproduce (a real second backend, e.g. GCSStore, shares it via
// verifySignature — see internal/storage/signing.go); fakeObjectStore is not
// part of that production sharing itself, so a regression there would not
// be masked by this double happening to match it.
type fakeObjectStore struct {
	objects map[string][]byte
	secret  string
	now     func() time.Time
}

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: map[string][]byte{}, secret: "fake-store-signing-secret"}
}

func (f *fakeObjectStore) cdnBase() string { return "http://fake-cdn.test/cdn" }

func (f *fakeObjectStore) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now().UTC()
}

func (f *fakeObjectStore) Read(ctx context.Context, objectPath string) (*storage.Object, error) {
	data, ok := f.objects[objectPath]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return &storage.Object{Path: objectPath, Data: data}, nil
}

func (f *fakeObjectStore) Write(ctx context.Context, objectPath string, data []byte, expectedGen int64) (int64, error) {
	f.objects[objectPath] = data
	return 1, nil
}

func (f *fakeObjectStore) Delete(ctx context.Context, objectPath string) error {
	delete(f.objects, objectPath)
	return nil
}

func (f *fakeObjectStore) Exists(ctx context.Context, objectPath string) (bool, error) {
	_, ok := f.objects[objectPath]
	return ok, nil
}

func (f *fakeObjectStore) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range f.objects {
		out = append(out, k)
	}
	return out, nil
}

func (f *fakeObjectStore) Stat(ctx context.Context, objectPath string) (*storage.ObjectMeta, error) {
	data, ok := f.objects[objectPath]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return &storage.ObjectMeta{Path: objectPath, Size: int64(len(data))}, nil
}

func (f *fakeObjectStore) PublicURL(objectPath string) string {
	return f.cdnBase() + "/" + objectPath
}

func (f *fakeObjectStore) SignedURL(ctx context.Context, objectPath string, ttl time.Duration) (string, time.Time, error) {
	expiresAt := f.clock().Add(ttl)
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	sig := fakeSign(f.secret, objectPath, exp)
	return f.cdnBase() + "/" + objectPath + "?exp=" + exp + "&sig=" + sig, expiresAt, nil
}

func (f *fakeObjectStore) VerifySignedURL(objectPath, exp, sig string) bool {
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || f.clock().Unix() > expUnix {
		return false
	}
	return fakeSign(f.secret, objectPath, exp) == sig
}

func (f *fakeObjectStore) Ready(ctx context.Context) error { return nil }
func (f *fakeObjectStore) Type() string                    { return "fake" }

func fakeSign(secret, objectPath, exp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(objectPath + "|" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}
