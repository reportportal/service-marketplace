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
	writeJSON(w, http.StatusOK, map[string]bool{
		"githubEnabled":     s.deps.GitHub.Enabled(),
		"adminLoginEnabled": s.deps.AdminAuth.Configured(),
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
	if err := s.deps.AdminAuth.Authenticate(clientIP(r, s.deps.Config.TrustedProxyHops), req.Username, req.Password); err != nil {
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
	w.Header().Add("Set-Cookie", auth.BuildSessionCookie(token, s.deps.Sessions.TTLSeconds(), isHTTPS(r)))
	writeJSON(w, http.StatusOK, map[string]any{
		"accessToken": token,
		"tokenType":   "Bearer",
		"expiresIn":   s.deps.Sessions.TTLSeconds(),
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
	w.Header().Add("Set-Cookie", auth.BuildOAuthStateCookie(state, isHTTPS(r)))
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
	w.Header().Add("Set-Cookie", auth.BuildSessionCookie(token, s.deps.Sessions.TTLSeconds(), isHTTPS(r)))
	http.Redirect(w, r, "/operator/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if claims := sessionFrom(r.Context()); claims != nil {
		s.deps.Sessions.Revoke(r.Context(), claims.JTI, claims.Exp)
	}
	w.Header().Add("Set-Cookie", auth.ClearSessionCookie(isHTTPS(r)))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListLicenses(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.License.List(r.Context())
	if err != nil {
		writeError(w, mapStorageErr(err))
		return
	}
	if items == nil {
		items = []domain.LicenseEntitlement{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entitlements": items})
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
	out := map[string]any{
		"customerId": res.Entitlement.CustomerID,
		"tier":       res.Entitlement.Tier,
		"createdAt":  res.Entitlement.CreatedAt.Format("2006-01-02"),
		"expiresAt":  res.Entitlement.ExpiresAt,
		"publicKeys": res.Entitlement.PublicKeys,
		"privateKey": res.PrivateKey,
	}
	writeJSON(w, http.StatusCreated, out)
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
