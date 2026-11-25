package httpapi

// US-MRKT-LIC-008: the Operator console Licenses tab lists, creates, revokes, and
// rotates entitlements. The repository has no JavaScript runner, so these tests read
// the console assets the router serves and check that each action is wired to the API
// call it claims, then drive that same call through the real router.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// operatorAsset returns the body the router serves for /operator/<name>; "" is the console page.
func (e *testEnv) operatorAsset(name string) string {
	e.t.Helper()
	rec := e.do(e.newRequest(http.MethodGet, "/operator/"+name, credNone, nil, ""))
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET /operator/%s: status %d, want 200", name, rec.Code)
	}
	return rec.Body.String()
}

// jsFunction returns the source of the top-level `async function name(...)` in src,
// up to the next top-level function, so assertions stay scoped to one handler.
func jsFunction(t *testing.T, src, name string) string {
	t.Helper()
	start := regexp.MustCompile(`(?m)^(async )?function ` + regexp.QuoteMeta(name) + `\(`).FindStringIndex(src)
	if start == nil {
		t.Fatalf("app.js has no function %s", name)
	}
	rest := src[start[1]:]
	if next := regexp.MustCompile(`(?m)^(async )?function `).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return rest
}

func assertContains(t *testing.T, where, src string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(src, want) {
			t.Fatalf("%s does not contain %q", where, want)
		}
	}
}

func TestOperatorUILicensesTabListsFromGetLicenses(t *testing.T) {
	e := newTestEnv(t)
	html := e.operatorAsset("")
	assertContains(t, "console page", html,
		`data-tab="licenses"`, `id="tab-licenses"`, `id="license-list"`, `id="license-empty"`)

	js := e.operatorAsset("app.js")
	assertContains(t, "app.js", js, `if (tabName === 'licenses') loadLicenses();`)
	load := jsFunction(t, js, "loadLicenses")
	assertContains(t, "loadLicenses", load,
		`api('/licenses')`, `data.entitlements`, `e.customerId`, `getElementById('license-list')`)

	if rec := e.do(e.newRequest(http.MethodPost, "/api/v1/licenses", credOperatorSession,
		[]byte(`{"customerId":"acme-corp"}`), "application/json")); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/licenses: status %d, body %s", rec.Code, rec.Body.String())
	}
	rec := e.do(e.newRequest(http.MethodGet, "/api/v1/licenses", credOperatorSession, nil, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/licenses: status %d, body %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Entitlements []struct {
			CustomerID string `json:"customerId"`
		} `json:"entitlements"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Entitlements) != 1 || list.Entitlements[0].CustomerID != "acme-corp" {
		t.Fatalf("entitlements = %+v, want one for acme-corp", list.Entitlements)
	}
}

func TestOperatorUICreateLicenseFormCapturesCustomerIDAndOptionalExpiresAt(t *testing.T) {
	e := newTestEnv(t)
	html := e.operatorAsset("")
	customer := regexp.MustCompile(`<input[^>]*id="customer-id"[^>]*>`).FindString(html)
	if customer == "" || !strings.Contains(customer, "required") {
		t.Fatalf("customer-id input = %q, want a required input", customer)
	}
	expires := regexp.MustCompile(`<input[^>]*id="expires-at"[^>]*>`).FindString(html)
	if expires == "" || strings.Contains(expires, "required") || !strings.Contains(expires, `type="date"`) {
		t.Fatalf("expires-at input = %q, want an optional date input", expires)
	}

	js := e.operatorAsset("app.js")
	assertContains(t, "app.js", js,
		`const body = { customerId: document.getElementById('customer-id').value };`,
		`const exp = document.getElementById('expires-at').value;`,
		`if (exp) body.expiresAt = exp;`,
		`api('/licenses', { method: 'POST', body })`)

	// Both bodies the form can send: without expiresAt, and with the YYYY-MM-DD a date input yields.
	for _, body := range []string{`{"customerId":"no-expiry"}`, `{"customerId":"with-expiry","expiresAt":"2030-01-31"}`} {
		rec := e.do(e.newRequest(http.MethodPost, "/api/v1/licenses", credOperatorSession, []byte(body), "application/json"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/v1/licenses %s: status %d, body %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestOperatorUILicenseRevokeAndRotateCallCorrespondingAPIs(t *testing.T) {
	e := newTestEnv(t)
	js := e.operatorAsset("app.js")

	load := jsFunction(t, js, "loadLicenses")
	assertContains(t, "loadLicenses", load,
		`addEventListener('click', () => rotateLicenseKey(e))`,
		`addEventListener('click', () => revokeLicense(e))`)

	revoke := jsFunction(t, js, "revokeLicense")
	assertContains(t, "revokeLicense", revoke,
		"window.confirm(",
		"api(`/licenses/${encodeURIComponent(entitlement.customerId)}`, { method: 'DELETE' })",
		"loadLicenses()")

	rotate := jsFunction(t, js, "rotateLicenseKey")
	assertContains(t, "rotateLicenseKey", rotate,
		"api(`/licenses/${encodeURIComponent(entitlement.customerId)}/keys`, { method: 'POST' })",
		"getElementById('license-result')",
		"loadLicenses()")

	if rec := e.do(e.newRequest(http.MethodPost, "/api/v1/licenses", credOperatorSession,
		[]byte(`{"customerId":"acme-corp"}`), "application/json")); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/licenses: status %d, body %s", rec.Code, rec.Body.String())
	}
	rec := e.do(e.newRequest(http.MethodPost, "/api/v1/licenses/acme-corp/keys", credOperatorSession, nil, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/licenses/acme-corp/keys: status %d, body %s", rec.Code, rec.Body.String())
	}
	var rotated struct {
		PrivateKey string `json:"privateKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil || rotated.PrivateKey == "" {
		t.Fatalf("rotate response %s: want a privateKey (err %v)", rec.Body.String(), err)
	}
	if rec := e.do(e.newRequest(http.MethodDelete, "/api/v1/licenses/acme-corp", credOperatorSession, nil, "")); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/v1/licenses/acme-corp: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(e.newRequest(http.MethodPost, "/api/v1/licenses/acme-corp/keys", credOperatorSession, nil, "")); rec.Code != http.StatusNotFound {
		t.Fatalf("rotate after revoke: status %d, want 404", rec.Code)
	}
}
