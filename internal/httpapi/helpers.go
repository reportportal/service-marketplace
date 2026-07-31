package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/reportportal/service-marketplace/internal/auth"
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

func authVerifyLicense(token string, publicKeys []string) (*auth.LicenseClaims, error) {
	return auth.VerifyLicenseJWT(token, publicKeys)
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
