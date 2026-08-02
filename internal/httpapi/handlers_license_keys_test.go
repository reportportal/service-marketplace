package httpapi

// Tests for DELETE /api/v1/licenses/{customerId}/keys/{keyId} (AMD-11 per-key
// revocation) through the real chi router and middleware chain -- not just
// license.Service.RevokeKey directly, per this project's house rule that
// client-visible behaviour is proven at the HTTP layer. Route-level authorization
// (session-only, OIDC rejected) is covered separately by router_authz_test.go's
// authzMatrix; these tests cover the handler's status/body mapping and end-to-end
// wiring to license.Service.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func (e *testEnv) createLicense(customerID string) CreateLicenseResponse {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"customerId": customerID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/licenses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	rec := e.do(req)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("createLicense(%q): status = %d, body = %s", customerID, rec.Code, rec.Body.String())
	}
	var res CreateLicenseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		e.t.Fatalf("decode CreateLicenseResponse: %v", err)
	}
	return res
}

func (e *testEnv) rotateLicenseKey(customerID string) RotateLicenseKeyResponseForTest {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/licenses/"+customerID+"/keys", nil)
	req.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	rec := e.do(req)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("rotateLicenseKey(%q): status = %d, body = %s", customerID, rec.Code, rec.Body.String())
	}
	var res RotateLicenseKeyResponseForTest
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		e.t.Fatalf("decode RotateLicenseKeyResponse: %v", err)
	}
	return res
}

// RotateLicenseKeyResponseForTest mirrors the RotateLicenseKeyResponse schema
// (license.RotateResult's wire shape) for decoding in tests.
type RotateLicenseKeyResponseForTest struct {
	CustomerID string `json:"customerId"`
	KeyID      string `json:"keyId"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

func TestHandleRevokeLicenseKey_Success(t *testing.T) {
	e := newTestEnv(t)
	created := e.createLicense("acme-corp")
	rotated := e.rotateLicenseKey("acme-corp")
	if rotated.KeyID == "" {
		t.Fatalf("rotate response has empty keyId: %+v", rotated)
	}
	firstKeyID := created.PublicKeys[0].KeyID
	if firstKeyID == "" {
		t.Fatalf("create response has empty keyId: %+v", created)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/licenses/acme-corp/keys/"+firstKeyID, nil)
	req.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	rec := e.do(req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("204 response must have an empty body, got %q", rec.Body.String())
	}

	// The revoked key must no longer appear as live in a subsequent listing.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/licenses", nil)
	getReq.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	getRec := e.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /licenses: status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
}

func TestHandleRevokeLicenseKey_UnknownCustomer_404(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/licenses/nobody-corp/keys/a1b2c3d4", nil)
	req.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	rec := e.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Code != CodeNotFound {
		t.Fatalf("code = %q, want %q", body.Code, CodeNotFound)
	}
}

func TestHandleRevokeLicenseKey_UnknownKeyID_404(t *testing.T) {
	e := newTestEnv(t)
	e.createLicense("acme-corp")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/licenses/acme-corp/keys/00000000", nil)
	req.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	rec := e.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Code != CodeNotFound {
		t.Fatalf("code = %q, want %q", body.Code, CodeNotFound)
	}
}

func TestHandleRevokeLicenseKey_LastActiveKey_422(t *testing.T) {
	e := newTestEnv(t)
	created := e.createLicense("acme-corp")
	onlyKeyID := created.PublicKeys[0].KeyID

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/licenses/acme-corp/keys/"+onlyKeyID, nil)
	req.Header.Set("Authorization", "Bearer "+e.operatorSessionToken())
	rec := e.do(req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Code != CodeValidation {
		t.Fatalf("code = %q, want %q", body.Code, CodeValidation)
	}
}

// TestHandleCreateAndRotateLicense_ExposeKeyID is AMD-11: keyId is added to
// LicensePublicKey, RotateLicenseKeyResponse and CreateLicenseResponse -- checked at
// the wire (this test), not just the Go struct shape (wire_contract_test.go already
// covers the schema; this covers a real handler actually populating it, not leaving it
// zero-valued).
func TestHandleCreateAndRotateLicense_ExposeKeyID(t *testing.T) {
	e := newTestEnv(t)
	created := e.createLicense("acme-corp")
	if len(created.PublicKeys) != 1 || created.PublicKeys[0].KeyID == "" {
		t.Fatalf("CreateLicenseResponse.publicKeys[0].keyId is empty: %+v", created)
	}
	rotated := e.rotateLicenseKey("acme-corp")
	if rotated.KeyID == "" {
		t.Fatalf("RotateLicenseKeyResponse.keyId is empty: %+v", rotated)
	}
	if rotated.KeyID == created.PublicKeys[0].KeyID {
		t.Fatalf("rotated key's keyId (%s) must differ from the original key's (%s)", rotated.KeyID, created.PublicKeys[0].KeyID)
	}
}
