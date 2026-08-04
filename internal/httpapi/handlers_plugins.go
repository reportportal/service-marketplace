package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/reportportal/service-marketplace/internal/analytics"
	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/lifecycle"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

func chiParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

func requirePluginID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chiParam(r, "pluginId")
	if ve := domain.ValidatePluginID(id); ve != nil {
		writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: "pluginId", Message: ve.Message}}})
		return "", false
	}
	return id, true
}

func requirePluginVersion(w http.ResponseWriter, r *http.Request) (pluginID, version string, ok bool) {
	pluginID, ok = requirePluginID(w, r)
	if !ok {
		return "", "", false
	}
	version = chiParam(r, "version")
	if ve := domain.ValidateVersion(version); ve != nil {
		writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: "version", Message: ve.Message}}})
		return "", "", false
	}
	return pluginID, version, true
}

func mapStorageErr(err error) error {
	if errors.Is(err, storage.ErrConflict) {
		return &APIError{Status: http.StatusConflict, Code: CodeStorageConflict, Message: "The stored object changed while this request was being applied; retry", Headers: map[string]string{"Retry-After": "1"}}
	}
	if errors.Is(err, storage.ErrUnavailable) {
		return &APIError{Status: http.StatusServiceUnavailable, Code: CodeStorageUnavailable, Message: "Registry storage is temporarily unavailable", Headers: map[string]string{"Retry-After": "1"}}
	}
	return err
}

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	plugins, err := s.deps.Catalogue.ListPlugins(r.Context(), r.URL.Query().Get("category"), r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, err)
		return
	}
	if plugins == nil {
		plugins = []domain.IndexPlugin{}
	}
	writeJSON(w, http.StatusOK, PluginListResponse{Plugins: plugins})
}

func (s *Server) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	pluginID, ok := requirePluginID(w, r)
	if !ok {
		return
	}
	m, st, err := s.deps.Catalogue.GetPlugin(r.Context(), pluginID)
	if err != nil {
		if errors.Is(err, catalogue.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"})
			return
		}
		writeError(w, err)
		return
	}
	if st.Removed != nil {
		writeJSON(w, http.StatusGone, catalogue.TombstoneFromState(st))
		return
	}
	writeJSON(w, http.StatusOK, pluginDetailResponse(*m, st))
}

func pluginDetailResponse(m domain.Manifest, st *domain.PluginState) PluginDetailResponse {
	return PluginDetailResponse{
		ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description,
		Author: m.Author, License: m.License, Category: m.Category,
		Compatibility: m.Compatibility, Homepage: m.Homepage, Access: m.Access,
		ContactURL: m.ContactURL, Tier: st.Tier, LatestVersion: st.LatestVersion,
	}
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	pluginID, ok := requirePluginID(w, r)
	if !ok {
		return
	}
	st, err := s.deps.Catalogue.ListVersions(r.Context(), pluginID)
	if err != nil {
		if errors.Is(err, catalogue.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"})
			return
		}
		writeError(w, err)
		return
	}
	if st.Removed != nil {
		writeJSON(w, http.StatusGone, catalogue.TombstoneFromState(st))
		return
	}
	blocked := map[string]domain.BlockedVersion{}
	for _, bv := range st.BlockedVersions {
		blocked[bv.Version] = bv
	}
	versions := make([]PluginVersionSummary, 0, len(st.Versions))
	for _, v := range st.Versions {
		item := PluginVersionSummary{Version: v.Version}
		if !v.PublishedAt.IsZero() {
			publishedAt := v.PublishedAt
			item.PublishedAt = &publishedAt
		}
		if bv, ok := blocked[v.Version]; ok {
			item.Blocked = true
			blockedAt := bv.BlockedAt
			item.BlockedAt = &blockedAt
			item.BlockReason = bv.Reason
		}
		versions = append(versions, item)
	}
	writeJSON(w, http.StatusOK, PluginVersionListResponse{PluginID: pluginID, Versions: versions})
}

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	pluginID, version, ok := requirePluginVersion(w, r)
	if !ok {
		return
	}
	detail, st, err := s.deps.Catalogue.GetVersion(r.Context(), pluginID, version)
	if err != nil {
		if errors.Is(err, catalogue.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"})
			return
		}
		writeError(w, err)
		return
	}
	if st.Removed != nil {
		writeJSON(w, http.StatusGone, catalogue.TombstoneFromState(st))
		return
	}
	m := detail.Manifest
	screenshotURLs := detail.ScreenshotURLs
	if screenshotURLs == nil {
		// PluginVersionDetail.screenshotUrls is a required, non-nullable array; an empty
		// Go slice marshals as "[]", but a nil one marshals as JSON null and violates the
		// schema.
		screenshotURLs = []string{}
	}
	out := PluginVersionDetailResponse{
		ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description,
		Author: m.Author, License: m.License, Category: m.Category,
		Compatibility: m.Compatibility, Homepage: m.Homepage, Access: m.Access,
		ContactURL:     m.ContactURL,
		Tier:           st.Tier,
		Blocked:        detail.Blocked,
		SHA256:         detail.SHA256,
		ScreenshotURLs: screenshotURLs,
		Advisory:       detail.Advisory,
	}
	if detail.BlockedAt != nil {
		blockedAt := *detail.BlockedAt
		out.BlockedAt = &blockedAt
		out.BlockReason = detail.BlockReason
	}
	if detail.ChangelogURL != nil {
		out.ChangelogURL = detail.ChangelogURL
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	pluginID, version, ok := requirePluginVersion(w, r)
	if !ok {
		return
	}
	clientID := r.Header.Get("X-RP-Instance-Id")
	track := func(access, result string) {
		s.deps.Analytics.TrackArtifactRequest(r.Context(), pluginID, version, access, result, clientID)
	}

	st, err := s.deps.Catalogue.ListVersions(r.Context(), pluginID)
	if err != nil {
		if errors.Is(err, catalogue.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"})
			return
		}
		writeError(w, err)
		return
	}
	if st.Removed != nil {
		track("public", analytics.ResultRemoved)
		writeJSON(w, http.StatusGone, catalogue.TombstoneFromState(st))
		return
	}
	for _, bv := range st.BlockedVersions {
		if bv.Version == version {
			track("public", analytics.ResultBlocked)
			writeJSON(w, http.StatusForbidden, BlockedArtifactErrorResponse{
				Blocked: true, BlockedAt: bv.BlockedAt, Reason: bv.Reason,
			})
			return
		}
	}
	detail, _, err := s.deps.Catalogue.GetVersion(r.Context(), pluginID, version)
	if err != nil {
		writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Version not found"})
		return
	}
	artPath := storage.VersionArtifactPath(pluginID, version, string(detail.Manifest.Access))
	access := string(detail.Manifest.Access)
	if detail.Manifest.Access == domain.AccessPremium {
		token := bearerToken(r)
		if token == "" {
			track(access, analytics.ResultNoLicense)
			writeError(w, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "License JWT required"})
			return
		}
		keys, err := s.publicKeysForLicense(r, token)
		if err != nil {
			track(access, analytics.ResultNoLicense)
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid license"})
			return
		}
		claims, err := authVerifyLicense(token, keys)
		if err != nil || claims.PluginID != pluginID {
			track(access, analytics.ResultNoLicense)
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "Invalid license"})
			return
		}
		url, expiresAt, err := s.deps.Store.SignedURL(r.Context(), artPath, 60*time.Second)
		if err != nil {
			writeError(w, err)
			return
		}
		track(access, analytics.ResultSuccess)
		writeJSON(w, http.StatusOK, PremiumArtifactResponse{DownloadURL: url, ExpiresAt: expiresAt})
		return
	}
	track(access, analytics.ResultSuccess)
	http.Redirect(w, r, s.deps.Store.PublicURL(artPath), http.StatusFound)
}

func (s *Server) publicKeysForLicense(r *http.Request, token string) ([]string, error) {
	claims, err := authVerifyLicenseUnverifiedCustomer(token)
	if err != nil {
		return nil, err
	}
	return s.deps.License.PublicKeysForCustomer(r.Context(), claims.CustomerID)
}

// handlePublishFirst: operator session only (no OIDC).
func (s *Server) handlePublishFirst(w http.ResponseWriter, r *http.Request) {
	bundle, err := s.parsePublishBundle(r)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := s.deps.Publish.PublishFirst(r.Context(), bundle, operatorIdentity(r.Context()))
	if err != nil {
		writeError(w, mapPublishErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// handlePublishVersion: session or publish OIDC; OIDC may auto-create the plugin.
func (s *Server) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	pluginID, ok := requirePluginID(w, r)
	if !ok {
		return
	}
	oidcPlugin := oidcPluginFrom(r.Context())
	if oidcPlugin != "" && oidcPlugin != pluginID {
		writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: "OIDC token is not allowed to publish this pluginId"})
		return
	}
	bundle, err := s.parsePublishBundle(r)
	if err != nil {
		writeError(w, err)
		return
	}
	autoCreate := oidcPlugin != ""
	res, err := s.deps.Publish.PublishVersion(r.Context(), pluginID, bundle, operatorIdentity(r.Context()), autoCreate)
	if err != nil {
		writeError(w, mapPublishErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) parsePublishBundle(r *http.Request) (*publish.Bundle, error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return nil, &APIError{Status: http.StatusUnsupportedMediaType, Code: CodeUnsupportedMediaType, Message: "Request media type is not supported"}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 160<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: "Request is malformed or is missing a required parameter"}
	}
	bundle, err := s.deps.Publish.ParseMultipart(reader)
	if err != nil {
		if errors.Is(err, publish.ErrPayloadLarge) {
			return nil, &APIError{Status: http.StatusRequestEntityTooLarge, Code: CodePayloadTooLarge, Message: "Publish bundle exceeds the maximum upload size accepted by the registry"}
		}
		var ve publish.ValidationErrors
		if errors.As(err, &ve) {
			fields := make([]FieldError, len(ve.Errors))
			for i, e := range ve.Errors {
				fields[i] = FieldError{Field: e.Field, Message: e.Message}
			}
			return nil, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Publish bundle validation failed", Errors: fields}
		}
		return nil, err
	}
	return bundle, nil
}

func mapPublishErr(err error) error {
	if errors.Is(err, publish.ErrConflict) {
		return &APIError{Status: http.StatusConflict, Code: CodeConflict, Message: "Plugin or version already exists"}
	}
	if errors.Is(err, publish.ErrNotFound) {
		return &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"}
	}
	var ve publish.ValidationErrors
	if errors.As(err, &ve) {
		fields := make([]FieldError, len(ve.Errors))
		for i, e := range ve.Errors {
			fields[i] = FieldError{Field: e.Field, Message: e.Message}
		}
		return &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Publish bundle validation failed", Errors: fields}
	}
	return mapStorageErr(err)
}

func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	pluginID, ok := requirePluginID(w, r)
	if !ok {
		return
	}
	var req struct {
		Tier domain.TrustTier `json:"tier"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.deps.Lifecycle.SetTier(r.Context(), pluginID, req.Tier)
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"})
			return
		}
		if errors.Is(err, lifecycle.ErrForbidden) {
			writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: "tier", Message: "only official tier accepted in phase 1"}}})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, PluginOperatorStateResponse{
		ID: st.ID, Tier: st.Tier, LatestVersion: st.LatestVersion, BlockedVersions: st.BlockedVersions,
	})
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	pluginID, ok := requirePluginID(w, r)
	if !ok {
		return
	}
	var req struct {
		RemovalReason string `json:"removalReason"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.RemovalReason) == "" {
		writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: "removalReason", Message: "required"}}})
		return
	}
	tomb, err := s.deps.Lifecycle.RemovePlugin(r.Context(), pluginID, req.RemovalReason, operatorIdentity(r.Context()))
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"})
			return
		}
		if errors.Is(err, lifecycle.ErrConflict) {
			writeError(w, &APIError{Status: http.StatusConflict, Code: CodeConflict, Message: "Plugin already removed"})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, tomb)
}

func (s *Server) handleBlockVersion(w http.ResponseWriter, r *http.Request) {
	pluginID, version, ok := requirePluginVersion(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed", Errors: []FieldError{{Field: "reason", Message: "required"}}})
		return
	}
	bv, err := s.deps.Lifecycle.BlockVersion(r.Context(), pluginID, version, req.Reason)
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin or version not found"})
			return
		}
		if errors.Is(err, lifecycle.ErrConflict) {
			writeError(w, &APIError{Status: http.StatusConflict, Code: CodeConflict, Message: "Version already blocked"})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, bv)
}

func (s *Server) handleAttachAdvisory(w http.ResponseWriter, r *http.Request) {
	pluginID, version, ok := requirePluginVersion(w, r)
	if !ok {
		return
	}
	var req struct {
		Severity domain.AdvisorySeverity `json:"severity"`
		Text     string                  `json:"text"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(string(req.Severity)) == "" || strings.TrimSpace(req.Text) == "" {
		writeError(w, &APIError{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "Validation failed"})
		return
	}
	adv, err := s.deps.Lifecycle.AttachAdvisory(r.Context(), pluginID, version, req.Severity, req.Text)
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin or version not found"})
			return
		}
		writeError(w, mapStorageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, adv)
}
