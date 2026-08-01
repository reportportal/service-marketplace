package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/reportportal/service-marketplace/internal/analytics"
	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/config"
	"github.com/reportportal/service-marketplace/internal/license"
	"github.com/reportportal/service-marketplace/internal/lifecycle"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

type Deps struct {
	Config     *config.Config
	Store      storage.ObjectStore
	LocalStore *storage.LocalStore
	Catalogue  *catalogue.Service
	Publish    *publish.Service
	Lifecycle  *lifecycle.Service
	License    *license.Service
	Analytics  *analytics.GA4Client
	Sessions   *auth.SessionManager
	AdminAuth  *auth.AdminAuthenticator
	GitHub     *auth.GitHubOAuth
	OIDC       *auth.PublishOIDCVerifier
}

type Server struct {
	deps   Deps
	router chi.Router
}

func NewServer(deps Deps) *Server {
	s := &Server{deps: deps}
	s.router = s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))
	r.Use(s.ensureXSRF)

	r.Get("/health", s.handleHealth)
	r.Get("/ready", s.handleReady)

	if s.deps.LocalStore != nil {
		r.Get("/cdn/*", s.handleCDNProxy)
	}

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/plugins", s.handleListPlugins)
		api.Get("/plugins/{pluginId}", s.handleGetPlugin)
		api.Get("/plugins/{pluginId}/versions", s.handleListVersions)
		api.Get("/plugins/{pluginId}/versions/{version}", s.handleGetVersion)
		api.Get("/plugins/{pluginId}/versions/{version}/artifact", s.handleGetArtifact)

		api.Get("/auth/config", s.handleAuthConfig)
		api.Post("/auth/login", s.handleAdminLogin)
		api.Get("/auth/github/login", s.handleGitHubLogin)
		api.Get("/auth/github/callback", s.handleGitHubCallback)
		api.With(s.requireSession).Post("/auth/logout", s.handleLogout)

		api.With(s.requireSessionRejectOIDC).Post("/plugins", s.handlePublishFirst)
		api.With(s.requireSessionRejectOIDC).Patch("/plugins/{pluginId}", s.handleUpdatePlugin)
		api.With(s.requireSessionRejectOIDC).Delete("/plugins/{pluginId}", s.handleRemovePlugin)
		api.With(s.requireSessionOrPublishOIDC).Post("/plugins/{pluginId}/versions", s.handlePublishVersion)
		api.With(s.requireSessionRejectOIDC).Post("/plugins/{pluginId}/versions/{version}/block", s.handleBlockVersion)
		api.With(s.requireSessionRejectOIDC).Post("/plugins/{pluginId}/versions/{version}/advisory", s.handleAttachAdvisory)

		api.With(s.requireSessionRejectOIDC).Get("/licenses", s.handleListLicenses)
		api.With(s.requireSessionRejectOIDC).Post("/licenses", s.handleCreateLicense)
		api.With(s.requireSessionRejectOIDC).Delete("/licenses/{customerId}", s.handleRevokeLicense)
		api.With(s.requireSessionRejectOIDC).Post("/licenses/{customerId}/keys", s.handleRotateLicenseKey)
	})

	fileServer := http.StripPrefix("/operator/", operatorFileServer())
	r.Handle("/operator/*", fileServer)
	r.Get("/operator", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/operator/", http.StatusFound)
	})

	return r
}

func (s *Server) ensureXSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(auth.XSRFCookieName); err != nil {
			if tok, err := auth.NewXSRFToken(); err == nil {
				w.Header().Add("Set-Cookie", auth.BuildXSRFCookie(tok, isHTTPS(r)))
			}
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxOIDCPlugin
	ctxOIDCSubject
)

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := s.authenticateSession(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if isUnsafe(r.Method) && hasSessionCookie(r) {
			xsrfCookie, _ := r.Cookie(auth.XSRFCookieName)
			xsrfHeader := r.Header.Get("X-XSRF-TOKEN")
			cookieVal := ""
			if xsrfCookie != nil {
				cookieVal = xsrfCookie.Value
			}
			if err := auth.ValidateCSRF(cookieVal, xsrfHeader); err != nil {
				writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeCSRFInvalid, Message: "CSRF token invalid"})
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxSession, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireSessionOrPublishOIDC is the credential gate for
// POST /api/v1/plugins/{pluginId}/versions — the single route AMD-02 and
// AMD-15 designate for GitHub Actions OIDC publish tokens. It accepts an
// operator session (bearer or cookie) exactly like requireSession, or a
// GitHub Actions OIDC bearer token verified against publishOidcTrust:
// success carries both the token's allow-listed plugin id (ctxOIDCPlugin,
// checked against the URL pluginId by handlePublishVersion) and its verified
// subject (ctxOIDCSubject, so operatorIdentity can record who actually
// published — see ADR-014 accountability) into the request context.
//
// An OIDC token whose repo claim has no entry in publishOidcTrust at all
// (auth.ErrForbidden) is refused with 403, not the generic 401 an absent or
// garbage credential gets — AMD-15: "non-allow-listed callers ... receive
// 403 regardless of whether the plugin exists".
//
// There is deliberately no boolean parameter here (contrast the old
// requireSessionOrOIDC(publishOnly bool), whose publishOnly was accepted and
// never read — finding F1). This middleware has exactly one caller and does
// exactly one thing; a route that should not accept OIDC uses
// requireSessionRejectOIDC or plain requireSession instead, not this
// function with an argument nobody checks.
func (s *Server) requireSessionOrPublishOIDC(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer := bearerToken(r); bearer != "" {
			if claims, err := s.deps.Sessions.Verify(r.Context(), bearer); err == nil {
				if isUnsafe(r.Method) && hasSessionCookie(r) {
					xsrfCookie, _ := r.Cookie(auth.XSRFCookieName)
					xsrfHeader := r.Header.Get("X-XSRF-TOKEN")
					cookieVal := ""
					if xsrfCookie != nil {
						cookieVal = xsrfCookie.Value
					}
					if err := auth.ValidateCSRF(cookieVal, xsrfHeader); err != nil {
						writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeCSRFInvalid, Message: "CSRF token invalid"})
						return
					}
				}
				ctx := context.WithValue(r.Context(), ctxSession, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if subject, pluginID, err := s.deps.OIDC.Verify(r.Context(), bearer); err == nil {
				ctx := context.WithValue(r.Context(), ctxOIDCPlugin, pluginID)
				ctx = context.WithValue(ctx, ctxOIDCSubject, subject)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			} else if errors.Is(err, auth.ErrForbidden) {
				writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "OIDC token is not allow-listed to publish any plugin"})
				return
			}
			writeError(w, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "Invalid or missing bearer token"})
			return
		}
		if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
			s.requireSession(next).ServeHTTP(w, r)
			return
		}
		writeError(w, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "Invalid or missing bearer token"})
	})
}

// requireSessionRejectOIDC is the credential gate for every operator route
// AMD-02 scopes GitHub Actions OIDC tokens OUT of: POST /api/v1/plugins
// (the first-publish route AMD-15/D-05 reserves for operator sessions only
// — first publish via CI auto-creates through POST .../versions instead;
// see requireSessionOrPublishOIDC), PATCH /api/v1/plugins/{pluginId},
// DELETE /api/v1/plugins/{pluginId}, POST .../versions/{version}/block,
// POST .../versions/{version}/advisory, and every /api/v1/licenses/*
// route.
//
// A bearer token that verifies as a well-formed, correctly-signed GitHub
// Actions OIDC token — whether or not its repo claim is allow-listed for
// some plugin — is a recognized credential, just the wrong type for these
// routes, so it is refused with 403 TOKEN_TYPE_NOT_PERMITTED
// (AMD-02-oidc-token-scope: "... regardless of allow-list membership").
// Anything else (no credential, garbage bearer value, expired/revoked
// session) falls through to the ordinary requireSession 401 handling.
func (s *Server) requireSessionRejectOIDC(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer := bearerToken(r); bearer != "" {
			if _, _, err := s.deps.OIDC.Verify(r.Context(), bearer); err == nil || errors.Is(err, auth.ErrForbidden) {
				writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeTokenTypeNotPermitted, Message: "GitHub Actions OIDC tokens are not accepted on this route; use an operator session"})
				return
			}
		}
		s.requireSession(next).ServeHTTP(w, r)
	})
}

func (s *Server) authenticateSession(r *http.Request) (*auth.SessionClaims, error) {
	bearer := bearerToken(r)
	if bearer == "" {
		if c, err := r.Cookie(auth.SessionCookieName); err == nil {
			bearer = c.Value
		}
	}
	if bearer == "" {
		return nil, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "Invalid or missing bearer token"}
	}
	claims, err := s.deps.Sessions.Verify(r.Context(), bearer)
	if err != nil {
		// Sessions.Verify returns unwrapped auth-package sentinel errors
		// (auth.ErrUnauthorized) for an expired, revoked, or malformed
		// token. writeError's fallback for anything that isn't an *APIError
		// is 500 INTERNAL_ERROR, which would otherwise report a rejected
		// credential as a server fault instead of 401.
		//
		// Finding F2-invalid-session-returns-500-not-401 (RECURS). Governed
		// by AMD-13-session-jwt-lifetime (requirements/AMENDMENTS-v1.md:
		// "any operator request with an expired token returns 401") and the
		// 401 Unauthorized response declared on every operator-session
		// route in docs/openapi/service-marketplace-v1.yaml. See
		// TestAuthenticateSessionRejectedCredentialReturns401 for the
		// regression test. WS-AUTHZ: this mapping already ships — do not
		// re-implement it.
		return nil, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "Invalid or missing bearer token"}
	}
	return claims, nil
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func hasSessionCookie(r *http.Request) bool {
	c, err := r.Cookie(auth.SessionCookieName)
	return err == nil && c.Value != ""
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func sessionFrom(ctx context.Context) *auth.SessionClaims {
	v, _ := ctx.Value(ctxSession).(*auth.SessionClaims)
	return v
}

// operatorIdentity returns the identity of the authenticated caller for
// whatever records the actor (currently domain.PluginTombstone.RemovedBy via
// lifecycle.RemovePlugin). Finding F2-oidc-identity-not-recorded
// (go-assessment.json, RECURS): this used to fall back to the fixed literal
// "github-actions" for every OIDC-authenticated call because the verified
// subject was read and discarded with `_` in the router (ADR-014 requires
// accountability — a specific repo/workflow, not an unconditional constant).
// It now returns the token's own `sub` claim (e.g.
// "repo:org/plugin-jira:ref:refs/heads/main") via ctxOIDCSubject.
func operatorIdentity(ctx context.Context) string {
	if s := sessionFrom(ctx); s != nil {
		return s.Subject
	}
	if sub := oidcSubjectFrom(ctx); sub != "" {
		return sub
	}
	// Unreachable in practice: every route that calls operatorIdentity is
	// gated by requireSession or requireSessionOrPublishOIDC, both of which
	// populate ctxSession or ctxOIDCSubject before the handler runs. Kept as
	// an explicit, greppable marker rather than silently returning "" so a
	// future caller wired without one of those gates fails loudly instead of
	// recording an empty actor.
	return "unknown-caller"
}

func oidcPluginFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxOIDCPlugin).(string)
	return v
}

func oidcSubjectFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxOIDCSubject).(string)
	return v
}
