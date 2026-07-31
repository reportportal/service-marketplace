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
	writeJSON(w, http.StatusOK, map[string]any{"plugins": plugins})
}

func (s *Server) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	pluginID := chiParam(r, "pluginId")
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
	out := manifestToDetail(*m, st)
	writeJSON(w, http.StatusOK, out)
}

func manifestToDetail(m domain.Manifest, st *domain.PluginState) map[string]any {
	out := map[string]any{
		"id": m.ID, "name": m.Name, "version": m.Version, "description": m.Description,
		"author": m.Author, "license": m.License, "category": m.Category,
		"compatibility": m.Compatibility, "access": m.Access, "tier": st.Tier,
		"latestVersion": st.LatestVersion,
	}
	if m.Homepage != "" {
		out["homepage"] = m.Homepage
	}
	if m.ContactURL != "" {
		out["contactUrl"] = m.ContactURL
	}
	return out
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	pluginID := chiParam(r, "pluginId")
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
	versions := make([]map[string]any, 0, len(st.Versions))
	for _, v := range st.Versions {
		item := map[string]any{"version": v.Version, "blocked": false}
		if !v.PublishedAt.IsZero() {
			item["publishedAt"] = v.PublishedAt
		}
		if bv, ok := blocked[v.Version]; ok {
			item["blocked"] = true
			item["blockedAt"] = bv.BlockedAt
			item["blockReason"] = bv.Reason
		}
		versions = append(versions, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pluginId": pluginID, "versions": versions})
}

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	pluginID := chiParam(r, "pluginId")
	version := chiParam(r, "version")
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
	out := manifestToDetail(detail.Manifest, st)
	out["version"] = detail.Manifest.Version
	out["blocked"] = detail.Blocked
	out["sha256"] = detail.SHA256
	out["screenshotUrls"] = detail.ScreenshotURLs
	if detail.BlockedAt != nil {
		out["blockedAt"] = detail.BlockedAt
		out["blockReason"] = detail.BlockReason
	}
	if detail.ChangelogURL != nil {
		out["changelogUrl"] = *detail.ChangelogURL
	}
	if detail.Advisory != nil {
		out["advisory"] = detail.Advisory
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	pluginID := chiParam(r, "pluginId")
	version := chiParam(r, "version")
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
			writeJSON(w, http.StatusForbidden, map[string]any{
				"blocked": true, "blockedAt": bv.BlockedAt, "reason": bv.Reason,
			})
			return
		}
	}
	detail, _, err := s.deps.Catalogue.GetVersion(r.Context(), pluginID, version)
	if err != nil {
		writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Version not found"})
		return
	}
	artPath := storage.VersionArtifactPath(pluginID, version)
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
		writeJSON(w, http.StatusOK, map[string]any{"downloadUrl": url, "expiresAt": expiresAt})
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

func (s *Server) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	pluginID := chiParam(r, "pluginId")
	bundle, err := s.parsePublishBundle(r)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := s.deps.Publish.PublishVersion(r.Context(), pluginID, bundle, operatorIdentity(r.Context()))
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
	pluginID := chiParam(r, "pluginId")
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
	writeJSON(w, http.StatusOK, map[string]any{
		"id": st.ID, "tier": st.Tier, "latestVersion": st.LatestVersion, "blockedVersions": st.BlockedVersions,
	})
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	pluginID := chiParam(r, "pluginId")
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
	pluginID := chiParam(r, "pluginId")
	version := chiParam(r, "version")
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
	pluginID := chiParam(r, "pluginId")
	version := chiParam(r, "version")
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
