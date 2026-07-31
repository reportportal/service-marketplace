package httpapi

import (
	"context"
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

		api.With(s.requireSessionOrOIDC(true)).Post("/plugins", s.handlePublishFirst)
		api.With(s.requireSession).Patch("/plugins/{pluginId}", s.handleUpdatePlugin)
		api.With(s.requireSession).Delete("/plugins/{pluginId}", s.handleRemovePlugin)
		api.With(s.requireSessionOrOIDC(true)).Post("/plugins/{pluginId}/versions", s.handlePublishVersion)
		api.With(s.requireSession).Post("/plugins/{pluginId}/versions/{version}/block", s.handleBlockVersion)
		api.With(s.requireSession).Post("/plugins/{pluginId}/versions/{version}/advisory", s.handleAttachAdvisory)

		api.With(s.requireSession).Get("/licenses", s.handleListLicenses)
		api.With(s.requireSession).Post("/licenses", s.handleCreateLicense)
		api.With(s.requireSession).Delete("/licenses/{customerId}", s.handleRevokeLicense)
		api.With(s.requireSession).Post("/licenses/{customerId}/keys", s.handleRotateLicenseKey)
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

func (s *Server) requireSessionOrOIDC(publishOnly bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
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
				if _, pluginID, err := s.deps.OIDC.Verify(r.Context(), bearer); err == nil {
					ctx := context.WithValue(r.Context(), ctxOIDCPlugin, pluginID)
					next.ServeHTTP(w, r.WithContext(ctx))
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
}

func (s *Server) authenticateSession(r *http.Request) (*auth.SessionClaims, error) {
	if bearer := bearerToken(r); bearer != "" {
		return s.deps.Sessions.Verify(r.Context(), bearer)
	}
	if c, err := r.Cookie(auth.SessionCookieName); err == nil {
		return s.deps.Sessions.Verify(r.Context(), c.Value)
	}
	return nil, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "Invalid or missing bearer token"}
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

func operatorIdentity(ctx context.Context) string {
	if s := sessionFrom(ctx); s != nil {
		return s.Subject
	}
	return "github-actions"
}
