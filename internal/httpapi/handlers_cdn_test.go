package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

// TestHandleCDNProxyCaseAliasOfAuthObjectRefused is the regression test for
// the review-blocking finding: LocalStore's default deployment target
// (macOS/APFS, Windows) is case-insensitive-but-case-preserving, so
// os.ReadFile("Auth/authorized_keys.json") and
// os.ReadFile("auth/authorized_keys.json") read the exact same bytes even
// though storage.IsAuthObject/IsPrivateObject are (correctly, for GCS's
// sake) case-sensitive prefix matches that only the second spelling passes.
// Kills: reverting IsAuthObject/hasReservedPrefix to a plain
// strings.HasPrefix(p, "auth/") (case-sensitive, no reserved-namespace-alias
// rejection in CanonicalizeObjectPath) — every case listed below would then
// return 200 with the secret payload instead of 403.
func TestHandleCDNProxyCaseAliasOfAuthObjectRefused(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	secret := "top-secret-authorized-keys-payload"
	if _, err := env.Store.Write(ctx, storage.PathAuthorizedKeys, []byte(secret), 0); err != nil {
		t.Fatalf("seed auth object: %v", err)
	}

	for _, alias := range []string{
		"/cdn/Auth/authorized_keys.json",
		"/cdn/AUTH/AUTHORIZED_KEYS.JSON",
		"/cdn/aUtH/authorized_keys.json",
	} {
		t.Run(alias, func(t *testing.T) {
			rec := env.do(httptest.NewRequest(http.MethodGet, alias, nil))
			if rec.Code == http.StatusOK {
				t.Fatalf("case alias of an auth object must not be served; got 200 body=%s", rec.Body.String())
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Code != CodeForbidden {
				t.Fatalf("expected code %q, got %q", CodeForbidden, body.Code)
			}
		})
	}
}

// TestHandleCDNProxyCaseAliasOfPrivateObjectRefused is the same class of bug
// as TestHandleCDNProxyCaseAliasOfAuthObjectRefused, against a private
// (premium) artifact rather than auth/authorized_keys.json — proving the fix
// is in the shared reserved-namespace check, not something special-cased to
// the auth/ prefix alone. Kills the same mutation, isolated to the
// "private" entry of reservedNamespaces / the IsPrivateObject predicate.
func TestHandleCDNProxyCaseAliasOfPrivateObjectRefused(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const pluginID, version = "plugin-case-alias", "1.0.0"
	artPath := storage.VersionArtifactPath(pluginID, version, "premium")
	if _, err := env.Store.Write(ctx, artPath, []byte("jar-bytes"), 0); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	// A case alias of the reserved "private/" segment, keeping the rest of
	// the path byte-identical.
	aliasPath := "Private/" + strings.TrimPrefix(artPath, "private/")

	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+aliasPath, nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("case alias of a private object must not be served; got 200 body=%s", rec.Body.String())
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCDNProxyBareReservedRootRefused is the regression test for
// finding #2: path.Clean collapses "/cdn/auth/", "/cdn/auth" and
// "/cdn/auth/authorized_keys.json/.." all down to the bare object path
// "auth" (no trailing "/..."), which a HasPrefix(p, "auth/")-only check does
// not match. Before the fix that reached storage.Store.Read("auth") — a
// directory on LocalStore's filesystem — producing an unauthenticated 500
// (EISDIR) distinguishable from the 404 an absent key returns: a
// directory-existence oracle. Kills: reverting hasReservedPrefix's `p == ns
// ||` arm (i.e. back to a bare HasPrefix(p, ns+"/") check) — every case
// below would then reach Store.Read("auth") instead of being rejected
// before it.
func TestHandleCDNProxyBareReservedRootRefused(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	if _, err := env.Store.Write(ctx, storage.PathAuthorizedKeys, []byte("secret"), 0); err != nil {
		t.Fatalf("seed auth object: %v", err)
	}

	for _, target := range []string{
		"/cdn/auth/",
		"/cdn/auth",
		"/cdn/auth/authorized_keys.json/..",
	} {
		t.Run(target, func(t *testing.T) {
			rec := env.do(httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("bare reserved-namespace root must not reach the storage read (directory-existence oracle); got 500 body=%s", rec.Body.String())
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 (the same guard a direct object hit gets), got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestHandleCDNProxySignatureNotValidForDifferentObject is a router-level
// pin for the signature contract finding (#3): a signature that verifies
// for object A must NOT verify for object B, even with the same exp. This
// exercises the real router + real LocalStore (not fakeObjectStore, and not
// storage's own table test), so a regression here is caught exactly where a
// client would see it.
//
// Kills two mutations to internal/storage/signing.go's verifySignature,
// both leaving TestSignedURLVerificationIsBackendIndependent's sibling
// coverage aside — this test must go red on its own, at the httpapi level:
//  1. `return hmac.Equal(...)` replaced with `return true` (expiry gate
//     left intact): object B's request would then get 200 instead of 403,
//     because ANY signature verifies once the expiry check passes.
//  2. signObjectPath's HMAC input changed from objectPath+"|"+exp to just
//     exp (dropping objectPath): object A's signature would then equal a
//     valid signature for object B (same exp, same secret), so object B's
//     request would get 200 instead of 403.
func TestHandleCDNProxySignatureNotValidForDifferentObject(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const pluginID, version = "plugin-sig-a", "1.0.0"
	const otherPluginID = "plugin-sig-b"
	artPathA := storage.VersionArtifactPath(pluginID, version, "premium")
	artPathB := storage.VersionArtifactPath(otherPluginID, version, "premium")
	if _, err := env.Store.Write(ctx, artPathA, []byte("jar-bytes-a"), 0); err != nil {
		t.Fatalf("seed artifact A: %v", err)
	}
	if _, err := env.Store.Write(ctx, artPathB, []byte("jar-bytes-b"), 0); err != nil {
		t.Fatalf("seed artifact B: %v", err)
	}

	urlA, _, err := env.Store.SignedURL(ctx, artPathA, time.Minute)
	if err != nil {
		t.Fatalf("SignedURL for A: %v", err)
	}
	query := urlA[len("http://cdn.test/"+artPathA):] // "?exp=...&sig=..."

	// Sanity: A's own signature is accepted for A.
	if rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+artPathA+query, nil)); rec.Code != http.StatusOK {
		t.Fatalf("expected object A's own signature to be accepted for A, got %d body=%s", rec.Code, rec.Body.String())
	}

	// A's signature reused for B (same query string, different object path)
	// must be refused.
	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+artPathB+query, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403: a signature valid for object A must not verify for object B, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCDNProxyTamperedSignatureRefused is the router-level pin for the
// other half of the signature contract: a signature with one bit flipped,
// presented alongside its own genuine, still-future exp, must be refused.
// Kills the same `return true` mutation to verifySignature as the test
// above (this one in isolation: it never involves a second object at all,
// so it also independently catches a hypothetical "compare only the
// expiry, ignore sig entirely" mutation that a same-object/reused-signature
// test alone would not distinguish from correct behaviour).
func TestHandleCDNProxyTamperedSignatureRefused(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const pluginID, version = "plugin-sig-tamper", "1.0.0"
	artPath := storage.VersionArtifactPath(pluginID, version, "premium")
	if _, err := env.Store.Write(ctx, artPath, []byte("jar-bytes"), 0); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	url, _, err := env.Store.SignedURL(ctx, artPath, time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	query := url[len("http://cdn.test/"+artPath):]

	// Sanity: the genuine signature is accepted.
	if rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+artPath+query, nil)); rec.Code != http.StatusOK {
		t.Fatalf("expected the genuine signature to be accepted, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Flip the last hex character of sig (deterministically, whatever
	// digit it actually is), keeping exp — and thus the still-future
	// expiry — untouched.
	sigIdx := strings.Index(query, "sig=")
	if sigIdx < 0 {
		t.Fatalf("test bug: no sig= in query %q", query)
	}
	last := query[len(query)-1]
	flipped := byte('0')
	if last == '0' {
		flipped = '1'
	}
	tampered := query[:len(query)-1] + string(flipped)
	if tampered == query {
		t.Fatalf("test bug: could not derive a tampered query from %q", query)
	}

	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+artPath+tampered, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a tampered signature with a still-future expiry, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCDNProxyPublicObjectWithInvalidSignatureRejected pins finding
// #5: if a request for a PUBLIC object presents a signature at all, that
// signature must be valid — the branch is unreachable from anywhere else in
// this codebase (nothing ever mints a signed URL for a public object; see
// handlers_plugins.go's artifact handler, which only calls SignedURL for
// premium access), so nothing but a direct test pins it. Kills: turning the
// `else if exp != "" || sig != "" { ... }` branch into a no-op (e.g.
// deleting it, or short-circuiting straight to the read) — this would then
// return 200 with the object's bytes instead of 403.
func TestHandleCDNProxyPublicObjectWithInvalidSignatureRejected(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const pluginID, version = "plugin-public-badsig", "1.0.0"
	manifestPath := storage.VersionManifestPath(pluginID, version)
	if _, err := env.Store.Write(ctx, manifestPath, []byte(`{"id":"plugin-public-badsig"}`), 0); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+manifestPath+"?exp="+future+"&sig=deadbeef", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a public object with an invalid signature attached, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCDNProxyRegisteredRegardlessOfBackend proves /cdn/* is reachable
// at all against a storage.ObjectStore double that is NOT a
// *storage.LocalStore, wired the same way a GCS deployment would be
// (Deps.LocalStore left nil — see router.go and cmd/marketplace/main.go,
// which only sets LocalStore when STORAGE_TYPE=local). Before this branch,
// router.go registered GET /cdn/* only when Deps.LocalStore != nil, so this
// exact request 404'd (no route at all) regardless of what Deps.Store held.
//
// It requests a PRIVATE object with a valid signature — the case that
// actually needs this handler's enforcement on every backend — rather than
// a public, unsigned one: a public, unsigned request against a non-local
// backend is redirected straight to the backend's own public origin instead
// of being read through this process (see handleCDNProxy's doc comment and
// TestHandleCDNProxyPublicObjectRedirectsOnNonLocalBackend), so it would not
// exercise "backend actually served through this handler" the way this test
// intends.
func TestHandleCDNProxyRegisteredRegardlessOfBackend(t *testing.T) {
	env := newTestEnv(t)
	backend := newFakeObjectStore()
	const objectPath = "private/plugins/p/versions/1.0.0/p-1.0.0.jar"
	backend.objects[objectPath] = []byte(`jar-bytes`)

	d := env.Server.deps
	d.Store = backend
	d.LocalStore = nil // exactly what a GCS deployment wires (see router.go)
	srv := NewServer(d)

	url, _, err := backend.SignedURL(context.Background(), objectPath, time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	target := "/cdn/" + objectPath + url[len(backend.cdnBase()+"/"+objectPath):]

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /cdn to be served from a non-LocalStore backend with LocalStore==nil, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `jar-bytes` {
		t.Fatalf("expected the fake backend's bytes, got %q", rec.Body.String())
	}
}

// TestHandleCDNProxyPublicObjectRedirectsOnNonLocalBackend pins point 4's
// resolution: a plain, unsigned request for a PUBLIC object against a
// non-LocalStore backend (Deps.LocalStore == nil, exactly what a GCS
// deployment wires) is redirected to that backend's own PublicURL instead
// of being buffered into this process and re-served — see handleCDNProxy's
// doc comment. Mutating that branch away (e.g. always falling through to
// the buffered read) would still return 200 with correct bytes, not fail
// this test on content — so this test pins the redirect response itself,
// not just "some successful response".
func TestHandleCDNProxyPublicObjectRedirectsOnNonLocalBackend(t *testing.T) {
	env := newTestEnv(t)
	backend := newFakeObjectStore()
	backend.objects["index.json"] = []byte(`{"plugins":[]}`)

	d := env.Server.deps
	d.Store = backend
	d.LocalStore = nil
	srv := NewServer(d)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cdn/index.json", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to the backend's public URL, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), backend.PublicURL("index.json"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

// TestHandleCDNProxyLocalStorePublicObjectIsServedDirectly proves the
// non-local-backend redirect shortcut above is a no-op for LocalStore
// deployments specifically, not merely "conditioned on LocalStore != nil and
// happens to also work": LocalStore.PublicURL points right back into this
// same /cdn/* route (cdnBase + "/" + CDNPath(objectPath)), so if the
// redirect condition were ever accidentally satisfied for a LocalStore
// deployment (e.g. the LocalStore == nil check were dropped or inverted),
// this would 302-loop instead of ever returning bytes.
func TestHandleCDNProxyLocalStorePublicObjectIsServedDirectly(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const pluginID, version = "plugin-public-direct", "1.0.0"
	manifestPath := storage.VersionManifestPath(pluginID, version)
	if _, err := env.Store.Write(ctx, manifestPath, []byte(`{"id":"plugin-public-direct"}`), 0); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	rec := env.do(httptest.NewRequest(http.MethodGet, "/cdn/"+manifestPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (served directly, no redirect) from a LocalStore deployment, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"id":"plugin-public-direct"}` {
		t.Fatalf("expected the object's bytes, got %q", rec.Body.String())
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
// special-case LocalStore.
//
// Its SignedURL/VerifySignedURL call storage.SignObjectPath /
// storage.VerifySignature — the exact same exported entry points into
// internal/storage/signing.go's signObjectPath/verifySignature that
// LocalStore and GCSStore call internally — rather than reimplementing the
// HMAC math a second time. That used to not be true (a local fakeSign
// helper duplicated the scheme by hand): a double that reimplements the
// thing under test cannot catch that thing's bugs, so a mutation to the real
// verifySignature (e.g. `return true`, or dropping objectPath from the HMAC
// input) could pass every test built on fakeObjectStore even though the
// production signature check was broken. Sharing the real functions means
// any router-level test run against fakeObjectStore is exercising, and can
// catch regressions in, the exact same code every real backend uses.

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
	sig := storage.SignObjectPath(f.secret, objectPath, exp)
	return f.cdnBase() + "/" + objectPath + "?exp=" + exp + "&sig=" + sig, expiresAt, nil
}

func (f *fakeObjectStore) VerifySignedURL(objectPath, exp, sig string) bool {
	return storage.VerifySignature(f.secret, objectPath, exp, sig, f.clock())
}

func (f *fakeObjectStore) Ready(ctx context.Context) error { return nil }
func (f *fakeObjectStore) Type() string                    { return "fake" }
