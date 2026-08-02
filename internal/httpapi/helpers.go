package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/domain"
)

func jsonDecode(r *http.Request, v any) error {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		return &APIError{Status: http.StatusUnsupportedMediaType, Code: CodeUnsupportedMediaType, Message: "Request media type is not supported"}
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return &APIError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: "Request is malformed or is missing a required parameter"}
	}
	return nil
}

// authVerifyLicense is a MINIMAL, behavior-preserving adapter kept building against
// auth.VerifyLicenseJWT's new signature (chunk 1: internal/auth/license.go). It is
// deliberately NOT a real implementation of AMD-09/AMD-10/AMD-11/AMD-25 at the artifact
// endpoint -- that whole call site (this function, authVerifyLicenseUnverifiedCustomer,
// and their caller in handlers_plugins.go) still needs to be replaced with
// license.Service.VerifyToken, which already implements kid-aware key selection
// (AMD-11), revocation (AMD-11/AMD-25) and entitlement-expiry (AMD-10) end to end; see
// its doc comment in internal/license/service.go. Wrapping raw public-key strings into
// domain.LicensePublicKey with KeyID/RevokedAt left zero preserves this file's exact
// prior behaviour (try every key the customer ever had, kid-blind, revocation-blind) --
// it does not enforce AMD-11 revocation or the AMD-25 propagation bound at this layer;
// only license.Service.VerifyToken does that today. Left for whichever chunk owns the
// artifact endpoint's error mapping (AMD-09) to replace outright.
func authVerifyLicense(token string, publicKeys []string) (*auth.LicenseClaims, error) {
	keys := make([]domain.LicensePublicKey, len(publicKeys))
	for i, pk := range publicKeys {
		keys[i] = domain.LicensePublicKey{PublicKey: pk}
	}
	return auth.VerifyLicenseJWT(token, keys, nil)
}

func authVerifyLicenseUnverifiedCustomer(token string) (*auth.LicenseClaims, error) {
	parsed, err := jwt.Parse([]byte(token), jwt.WithVerify(false))
	if err != nil {
		return nil, auth.ErrUnauthorized
	}
	cid, _ := parsed.Get("customerId")
	pid, _ := parsed.Get("pluginId")
	exp := parsed.Expiration()
	customerID, _ := cid.(string)
	pluginID, _ := pid.(string)
	if customerID == "" {
		return nil, auth.ErrUnauthorized
	}
	_ = jwa.EdDSA
	return &auth.LicenseClaims{CustomerID: customerID, PluginID: pluginID, Exp: exp}, nil
}
