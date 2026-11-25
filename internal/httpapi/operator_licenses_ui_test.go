package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/reportportal/service-marketplace/web"
)

// The repo has no JS test runner, so each test drives the real /api/v1/licenses routes the
// console calls and then checks that the embedded console source issues those same calls.

type uiEntitlement struct {
	CustomerID string            `json:"customerId"`
	Tier       string            `json:"tier"`
	ExpiresAt  string            `json:"expiresAt"`
	PublicKeys []json.RawMessage `json:"publicKeys"`
}

func operatorAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := web.OperatorFS.ReadFile("operator/" + name)
	if err != nil {
		t.Fatalf("read embedded operator/%s: %v", name, err)
	}
	return string(b)
}

func assertContains(t *testing.T, asset, src string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(src, want) {
			t.Errorf("embedded %s does not contain %q", asset, want)
		}
	}
}

func listLicensesViaHTTP(t *testing.T, env *testEnv) []uiEntitlement {
	t.Helper()
	rec := env.do(env.newRequest(http.MethodGet, "/api/v1/licenses", credOperatorSession, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /licenses: status %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Entitlements []uiEntitlement `json:"entitlements"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /licenses: decode: %v body=%s", err, rec.Body.String())
	}
	return body.Entitlements
}

func createLicenseViaHTTP(t *testing.T, env *testEnv, body string) string {
	t.Helper()
	rec := env.do(env.newRequest(http.MethodPost, "/api/v1/licenses", credOperatorSession, []byte(body), "application/json"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /licenses %s: status %d body=%s", body, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestOperatorUIListsEntitlementsFromGetLicenses(t *testing.T) {
	env := newTestEnv(t)
	createLicenseViaHTTP(t, env, `{"customerId":"acme-co"}`)
	createLicenseViaHTTP(t, env, `{"customerId":"globex","expiresAt":"2999-01-31"}`)

	got := listLicensesViaHTTP(t, env)
	if len(got) != 2 {
		t.Fatalf("GET /licenses returned %d entitlements, want 2: %+v", len(got), got)
	}
	byID := map[string]uiEntitlement{}
	for _, e := range got {
		byID[e.CustomerID] = e
	}
	if e := byID["acme-co"]; e.Tier != "premium" || len(e.PublicKeys) != 1 {
		t.Errorf("acme-co = %+v, want tier premium and 1 public key", e)
	}
	if e := byID["globex"]; e.ExpiresAt != "2999-01-31" {
		t.Errorf("globex expiresAt = %q, want 2999-01-31", e.ExpiresAt)
	}

	// The console reads the list from GET /licenses and renders the fields the response carries.
	js := operatorAsset(t, "app.js")
	assertContains(t, "app.js", js,
		"api('/licenses')",
		"data.entitlements",
		"e.customerId",
		"e.tier",
		"e.publicKeys",
		"e.expiresAt",
	)
	html := operatorAsset(t, "index.html")
	assertContains(t, "index.html", html, `id="license-list"`, `id="license-empty"`)
}

func TestOperatorUICreateFlowCapturesCustomerIDAndOptionalExpiresAt(t *testing.T) {
	env := newTestEnv(t)

	// The body the console builds: customerId always, expiresAt only when the date is filled in.
	createLicenseViaHTTP(t, env, `{"customerId":"no-expiry"}`)
	createLicenseViaHTTP(t, env, `{"customerId":"with-expiry","expiresAt":"2999-12-31"}`)

	byID := map[string]uiEntitlement{}
	for _, e := range listLicensesViaHTTP(t, env) {
		byID[e.CustomerID] = e
	}
	if e, ok := byID["no-expiry"]; !ok || e.ExpiresAt != "" {
		t.Errorf("no-expiry = %+v (present=%v), want present with no expiresAt", e, ok)
	}
	if e, ok := byID["with-expiry"]; !ok || e.ExpiresAt != "2999-12-31" {
		t.Errorf("with-expiry = %+v (present=%v), want expiresAt 2999-12-31", e, ok)
	}

	js := operatorAsset(t, "app.js")
	assertContains(t, "app.js", js,
		"getElementById('customer-id').value",
		"getElementById('expires-at').value",
		"if (exp) body.expiresAt = exp",
		"api('/licenses', { method: 'POST', body })",
	)
	html := operatorAsset(t, "index.html")
	assertContains(t, "index.html", html,
		`id="create-license-form"`,
		`id="customer-id"`,
		`id="expires-at"`,
		`type="date"`,
	)
	// customerId is required; expiresAt is optional.
	if strings.Contains(inputTag(t, html, `id="expires-at"`), "required") {
		t.Errorf("expires-at input must be optional")
	}
	if !strings.Contains(inputTag(t, html, `id="customer-id"`), "required") {
		t.Errorf("customer-id input must be required")
	}
}

// inputTag returns the <input ...> element containing marker.
func inputTag(t *testing.T, html, marker string) string {
	t.Helper()
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("index.html has no %s", marker)
	}
	start := strings.LastIndex(html[:i], "<input")
	end := strings.Index(html[i:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("index.html: no <input> around %s", marker)
	}
	return html[start : i+end]
}

func TestOperatorUIRevokeAndRotateCallLicenseAPIs(t *testing.T) {
	env := newTestEnv(t)
	createLicenseViaHTTP(t, env, `{"customerId":"acme-co"}`)
	createLicenseViaHTTP(t, env, `{"customerId":"globex"}`)

	// Rotate: POST /licenses/{customerId}/keys returns the new key pair once and adds a public key.
	rec := env.do(env.newRequest(http.MethodPost, "/api/v1/licenses/acme-co/keys", credOperatorSession, nil, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /licenses/acme-co/keys: status %d body=%s", rec.Code, rec.Body.String())
	}
	var rotated struct {
		CustomerID string `json:"customerId"`
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("rotate: decode: %v body=%s", err, rec.Body.String())
	}
	if rotated.CustomerID != "acme-co" || rotated.PrivateKey == "" || rotated.PublicKey == "" {
		t.Errorf("rotate result = %+v, want customerId acme-co with private and public key", rotated)
	}
	for _, e := range listLicensesViaHTTP(t, env) {
		if e.CustomerID == "acme-co" && len(e.PublicKeys) != 2 {
			t.Errorf("acme-co has %d public keys after rotate, want 2", len(e.PublicKeys))
		}
	}

	// Rotating an unknown customer is the 404 the console surfaces and refreshes on.
	rec = env.do(env.newRequest(http.MethodPost, "/api/v1/licenses/nobody/keys", credOperatorSession, nil, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rotate unknown: status %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
	decodeErrorEnvelope(t, rec)

	// Revoke: DELETE /licenses/{customerId} drops the entitlement from the list.
	rec = env.do(env.newRequest(http.MethodDelete, "/api/v1/licenses/acme-co", credOperatorSession, nil, ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /licenses/acme-co: status %d body=%s", rec.Code, rec.Body.String())
	}
	got := listLicensesViaHTTP(t, env)
	if len(got) != 1 || got[0].CustomerID != "globex" {
		t.Errorf("after revoke list = %+v, want only globex", got)
	}
	rec = env.do(env.newRequest(http.MethodDelete, "/api/v1/licenses/acme-co", credOperatorSession, nil, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoke twice: status %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
	decodeErrorEnvelope(t, rec)

	// The console wires a Revoke and a Rotate action per row to those two routes.
	js := operatorAsset(t, "app.js")
	assertContains(t, "app.js", js,
		"revokeBtn.textContent = 'Revoke'",
		"revokeBtn.addEventListener('click', () => revokeLicense(e))",
		"api(`/licenses/${encodeURIComponent(entitlement.customerId)}`, { method: 'DELETE' })",
		"rotateBtn.textContent = 'Rotate key'",
		"rotateBtn.addEventListener('click', () => rotateLicenseKey(e))",
		"api(`/licenses/${encodeURIComponent(entitlement.customerId)}/keys`, { method: 'POST' })",
		"window.confirm(",
		"formatApiError(err)",
		"document.getElementById('license-result').textContent = JSON.stringify(res, null, 2)",
	)
	assertContains(t, "index.html", operatorAsset(t, "index.html"), `id="license-result"`, `id="license-error"`)
}
