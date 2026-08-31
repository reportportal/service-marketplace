package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
)

// premiumManifest is a premium plugin, which the manifest schema requires to declare a
// contactUrl: there is nothing to install without a licence, so the enquiry link is the
// only action such a plugin has.
func premiumManifest(pluginID, contactURL string) *domain.Manifest {
	return &domain.Manifest{
		ID: pluginID, Name: "Quality Gate", Version: "1.0.0", Description: "d",
		Author: domain.Author{Name: "ReportPortal"}, License: "Apache-2.0",
		Category: domain.CategoryOther, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPremium, ContactURL: contactURL,
	}
}

// TestListingCarriesContactUrl pins contactUrl onto the catalogue listing, not only onto
// plugin detail. A consumer builds the premium row from the listing: with no install to
// offer, the row's one action is the enquiry, and without this field it is drawn with
// nowhere to go — which is exactly what the plugins page did, a Discover Premium button
// whose click did nothing at all.
//
// Kills dropping ContactURL from domain.IndexPlugin or from the index rebuild.
func TestListingCarriesContactUrl(t *testing.T) {
	env := newTestEnv(t)
	const contactURL = "https://reportportal.io/pricing/service-packages/"

	if rec := publishViaHTTP(t, env, premiumManifest(testOIDCPluginID, contactURL)); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	item := listItem(t, env, testOIDCPluginID)
	raw, ok := item["contactUrl"]
	if !ok {
		t.Fatalf("GET /api/v1/plugins: contactUrl missing from the listing; the manifest declared %q", contactURL)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("contactUrl is not a JSON string: %v (raw %s)", err, raw)
	}
	if got != contactURL {
		t.Errorf("contactUrl = %q, want %q", got, contactURL)
	}
}

// TestListingOmitsContactUrlWhenUndeclared is the other half: the field is absent rather
// than empty for a plugin that declares none, so a consumer can treat "missing" as "no
// enquiry link" without also having to treat "" as the same thing.
func TestListingOmitsContactUrlWhenUndeclared(t *testing.T) {
	env := newTestEnv(t)

	m := premiumManifest(testOIDCPluginID, "")
	m.Access = domain.AccessPublic // public: no contactUrl required, and none given
	if rec := publishViaHTTP(t, env, m); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	if _, present := listItem(t, env, testOIDCPluginID)["contactUrl"]; present {
		t.Errorf("listing carries a contactUrl key for a plugin that declared none")
	}
}

// TestListingCarriesAuthor pins the author onto the catalogue listing. A consumer builds the
// row from the listing alone, and a row names who wrote the plugin: without this field the
// only options are to print nothing or to guess, and the guess that was actually shipped
// attributed every third party's plugin to ReportPortal.
//
// Kills dropping Author from domain.IndexPlugin or from the index rebuild.
func TestListingCarriesAuthor(t *testing.T) {
	env := newTestEnv(t)
	m := premiumManifest(testOIDCPluginID, "https://reportportal.io/contact")
	m.Author = domain.Author{Name: "Acme Integrations", Email: "dev@acme.example", URL: "https://acme.example"}

	if rec := publishViaHTTP(t, env, m); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	raw, ok := listItem(t, env, testOIDCPluginID)["author"]
	if !ok {
		t.Fatalf("GET /api/v1/plugins: author missing from the listing")
	}
	var got domain.Author
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("author is not an object: %v (raw %s)", err, raw)
	}
	if got != m.Author {
		t.Errorf("author = %+v, want %+v", got, m.Author)
	}
}
