package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/license"
)

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, AuthConfigResponse{
		GithubEnabled:     s.deps.GitHub.Enabled(),
		AdminLoginEnabled: s.deps.AdminAuth.Configured(),
	})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if !s.deps.AdminAuth.Configured() {
		writeError(w, &APIError{Status: http.StatusServiceUnavailable, Code: CodeServiceUnavailable, Message: "Admin login is not configured"})
		return
	}
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, &APIError{Status: http.StatusUnsupportedMediaType, Code: CodeUnsupportedMediaType, Message: "Request media type is not supported"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, &APIError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: "Request is malformed or is missing a required parameter"})
		return
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: "username", Message: "required"}}})
		return
	}
	if err := s.deps.AdminAuth.Authenticate(r.Context(), clientIP(r, s.deps.Config.TrustedProxyHops), req.Username, req.Password); err != nil {
		if errors.Is(err, auth.ErrTooManyAttempts) {
			writeError(w, &APIError{Status: http.StatusTooManyRequests, Code: CodeTooManyRequests, Message: "Too many login attempts for this username"})
			return
		}
		writeError(w, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "Invalid credentials"})
		return
	}
	token, _, err := s.deps.Sessions.Issue(r.Context(), req.Username)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Add("Set-Cookie", auth.BuildSessionCookie(token, s.deps.Sessions.TTLSeconds(), isHTTPS(r, s.deps.Config.TrustedProxyHops)))
	writeJSON(w, http.StatusOK, AuthTokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   s.deps.Sessions.TTLSeconds(),
	})
}

func (s *Server) handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if !s.deps.GitHub.Enabled() {
		writeError(w, &APIError{Status: http.StatusServiceUnavailable, Code: CodeServiceUnavailable, Message: "GitHub OAuth is not configured"})
		return
	}
	state, err := s.deps.GitHub.IssueState(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Add("Set-Cookie", auth.BuildOAuthStateCookie(state, isHTTPS(r, s.deps.Config.TrustedProxyHops)))
	http.Redirect(w, r, s.deps.GitHub.AuthorizeURL(state), http.StatusFound)
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	if !s.deps.GitHub.Enabled() {
		writeError(w, &APIError{Status: http.StatusServiceUnavailable, Code: CodeServiceUnavailable, Message: "GitHub OAuth is not configured"})
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(auth.OAuthStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		writeError(w, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "Invalid OAuth state"})
		return
	}
	// Clear state cookie immediately.
	w.Header().Add("Set-Cookie", auth.OAuthStateCookie+"=; Path=/api/v1/auth/github; HttpOnly; SameSite=Lax; Max-Age=0")
	token, _, err := s.deps.GitHub.Callback(r.Context(), code, state)
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Operator access denied"})
			return
		}
		writeError(w, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "GitHub authentication failed"})
		return
	}
	w.Header().Add("Set-Cookie", auth.BuildSessionCookie(token, s.deps.Sessions.TTLSeconds(), isHTTPS(r, s.deps.Config.TrustedProxyHops)))
	http.Redirect(w, r, "/operator/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if claims := sessionFrom(r.Context()); claims != nil {
		s.deps.Sessions.Revoke(r.Context(), claims.JTI, claims.Exp)
	}
	w.Header().Add("Set-Cookie", auth.ClearSessionCookie(isHTTPS(r, s.deps.Config.TrustedProxyHops)))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListLicenses(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.License.List(r.Context())
	if err != nil {
		writeError(w, mapStorageErr(err))
		return
	}
	entitlements := make([]LicenseEntitlementResponse, len(items))
	for i, e := range items {
		entitlements[i] = newLicenseEntitlementResponse(e)
	}
	writeJSON(w, http.StatusOK, LicenseEntitlementListResponse{Entitlements: entitlements})
}

func (s *Server) handleCreateLicense(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CustomerID string  `json:"customerId"`
		ExpiresAt  *string `json:"expiresAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, &APIError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: "Request is malformed or is missing a required parameter"})
		return
	}
	var expires *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse("2006-01-02", *req.ExpiresAt)
		if err != nil {
			writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: "expiresAt", Message: "invalid date"}}})
			return
		}
		expires = &t
	}
	res, err := s.deps.License.Create(r.Context(), req.CustomerID, expires)
	if err != nil {
		if ve, ok := err.(*domain.ValidationError); ok {
			writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: ve.Field, Message: ve.Message}}})
			return
		}
		if errors.Is(err, license.ErrConflict) {
			writeError(w, &APIError{Status: http.StatusConflict, Code: CodeConflict, Message: "Customer entitlement already exists"})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	entitlement := newLicenseEntitlementResponse(res.Entitlement)
	writeJSON(w, http.StatusCreated, CreateLicenseResponse{
		CustomerID: entitlement.CustomerID,
		Tier:       entitlement.Tier,
		IssuedAt:   entitlement.IssuedAt,
		ExpiresAt:  entitlement.ExpiresAt,
		PublicKeys: entitlement.PublicKeys,
		PrivateKey: res.PrivateKey,
	})
}

func (s *Server) handleRevokeLicense(w http.ResponseWriter, r *http.Request) {
	customerID := chiParam(r, "customerId")
	if err := s.deps.License.Revoke(r.Context(), customerID); err != nil {
		if errors.Is(err, license.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Entitlement not found"})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRevokeLicenseKey backs DELETE /api/v1/licenses/{customerId}/keys/{keyId}
// (AMD-11 per-key revocation, distinct from handleRevokeLicense's whole-entitlement
// DELETE /api/v1/licenses/{customerId}, which is unchanged). 404 covers both "no
// entitlement for customerId" and "no key resolves to keyId" -- AMD-11 only
// distinguishes "absent" (404) from "last active key" (422), not which kind of
// absence. AMD-25: license.Service.RevokeKey commits the revocation via the same
// storage.WriteWithRetry CAS path Create/RotateKey use, and license.Service.VerifyToken
// re-reads storage on every call (no key cache sits in front of it), so this 204 makes
// the revocation visible to the very next verification -- well inside AMD-25's 30s
// bound.
func (s *Server) handleRevokeLicenseKey(w http.ResponseWriter, r *http.Request) {
	customerID := chiParam(r, "customerId")
	keyID := chiParam(r, "keyId")
	if err := s.deps.License.RevokeKey(r.Context(), customerID, keyID); err != nil {
		if errors.Is(err, license.ErrNotFound) || errors.Is(err, license.ErrKeyNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "License key not found"})
			return
		}
		if errors.Is(err, license.ErrLastActiveKey) {
			writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Cannot revoke the entitlement's last active key; revoke the whole entitlement instead"})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRotateLicenseKey(w http.ResponseWriter, r *http.Request) {
	customerID := chiParam(r, "customerId")
	res, err := s.deps.License.RotateKey(r.Context(), customerID)
	if err != nil {
		if errors.Is(err, license.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Entitlement not found"})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, res)
}
