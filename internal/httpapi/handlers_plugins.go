package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/reportportal/service-marketplace/internal/analytics"
	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/license"
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
	items := make([]PluginListItemResponse, len(plugins))
	for i, p := range plugins {
		items[i] = newPluginListItemResponse(p)
	}
	writeJSON(w, http.StatusOK, PluginListResponse{Plugins: items})
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
	pluginID := chiParam(r, "pluginId")
	version := chiParam(r, "version")
	detail, st, err := s.deps.Catalogue.GetVersion(r.Context(), pluginID, version)
	if err != nil {
		if errors.Is(err, catalogue.ErrNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Plugin not found"})
			return
		}
		if errors.Is(err, catalogue.ErrVersionNotFound) {
			writeError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "Version not found"})
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
	}
	if detail.Advisory != nil {
		adv := newSecurityAdvisoryResponse(*detail.Advisory)
		out.Advisory = &adv
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
	// The blocked branch may only answer for a version that exists as far as a
	// client is concerned. st.Versions arrives already filtered to complete
	// versions by catalogue.Service.loadPlugin, whereas st.BlockedVersions is
	// deliberately never filtered (blocking is a separate axis from
	// completeness). An operator CAN block a committed-but-incomplete version --
	// lifecycle.BlockVersion validates against its own unfiltered read of
	// plugin.json -- and without this guard that version would answer
	// 403-with-reason here while the version list omitted it and its detail
	// 404'd: a contradiction that also confirms the existence of a version the
	// rest of the API denies. "Does not exist" wins over "exists but is
	// un-installable"; falling through leaves GetVersion below to 404 it.
	versionVisible := false
	for _, v := range st.Versions {
		if v.Version == version {
			versionVisible = true
			break
		}
	}
	if versionVisible {
		for _, bv := range st.BlockedVersions {
			if bv.Version == version {
				track("public", analytics.ResultBlocked)
				writeJSON(w, http.StatusForbidden, BlockedArtifactErrorResponse{
					Blocked: true, BlockedAt: bv.BlockedAt, Reason: bv.Reason,
				})
				return
			}
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
			writeError(w, &APIError{Status: http.StatusUnauthorized, Code: CodeLicenseJWTMissing, Message: "License JWT is missing"})
			return
		}
		claims, err := s.deps.License.VerifyToken(r.Context(), token)
		if err != nil {
			track(access, analytics.ResultNoLicense)
			writeError(w, licenseErrorResponse(err))
			return
		}
		// AMD-12: authorization succeeds only if the verified pluginId claim
		// string-equals the URL-path pluginId. This is checked AFTER
		// VerifyToken has already confirmed the signature and AMD-10's
		// entitlement expiry -- claims.PluginID is trustworthy here.
		if claims.PluginID != pluginID {
			track(access, analytics.ResultNoLicense)
			writeError(w, &APIError{Status: http.StatusForbidden, Code: CodeLicenseEntitlementDenied, Message: "License entitlement does not cover this plugin"})
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

// licenseErrorResponse renders AMD-09's premium-artifact license error table
// (requirements/AMENDMENTS-v1.md) for an error returned by
// license.Service.VerifyToken. The single most likely defect here is putting
// a row on the wrong side of the 401/403 split, so each case below is
// annotated with the exact table row it implements:
//
//   - "JWT unparseable, bad signature, expired exp, or unknown customerId"
//     -> 401 LICENSE_JWT_INVALID. This covers auth.ErrLicenseTokenInvalid
//     (malformed/bad-signature/missing-claim), auth.ErrLicenseTokenExpired
//     (the JWT's own short-lived exp), auth.ErrLicenseKeyInvalid (AMD-11:
//     unknown or revoked kid -- deliberately the SAME bucket as a bad
//     signature, since a kid that doesn't resolve to a usable key is just
//     another shape of "this token doesn't verify"), and license.ErrNotFound
//     (no entitlement at all for the token's customerId).
//   - "JWT valid but entitlement ... expired" -> 403 LICENSE_EXPIRED
//     (license.ErrEntitlementExpired, AMD-10). Distinct from the JWT's own
//     exp above: this is the entitlement's ExpiresAt, checked only after the
//     signature already verified.
//   - auth.ErrLicenseTokenMissing is handled by handleGetArtifact's own
//     empty-bearerToken check before VerifyToken is ever called, so it is not
//     expected to reach here; mapped defensively to the same missing-token
//     code rather than falling through to the generic default.
//
// pluginId-mismatch (403 LICENSE_ENTITLEMENT_DENIED) is NOT handled here --
// it is only decidable from the verified claims VerifyToken returns on
// success, so handleGetArtifact checks it itself after a nil error.
//
// Any other error (e.g. storage failures VerifyToken propagates) is not an
// AMD-09 row at all; the caller falls back to writeError's generic mapping
// via mapStorageErr.
func licenseErrorResponse(err error) error {
	switch {
	case errors.Is(err, auth.ErrLicenseTokenMissing):
		return &APIError{Status: http.StatusUnauthorized, Code: CodeLicenseJWTMissing, Message: "License JWT is missing"}
	case errors.Is(err, auth.ErrLicenseTokenExpired),
		errors.Is(err, auth.ErrLicenseKeyInvalid),
		errors.Is(err, auth.ErrLicenseTokenInvalid),
		errors.Is(err, license.ErrNotFound):
		return &APIError{Status: http.StatusUnauthorized, Code: CodeLicenseJWTInvalid, Message: "License JWT is malformed, unsigned by a known key, or expired"}
	case errors.Is(err, license.ErrEntitlementExpired):
		return &APIError{Status: http.StatusForbidden, Code: CodeLicenseExpired, Message: "License entitlement has expired"}
	default:
		return mapStorageErr(err)
	}
}

// handlePublishFirst backs POST /api/v1/plugins (FR-OP-01). It is gated by
// requireSessionRejectOIDC (AMD-02/AMD-15: this route is operator-session
// only), so r.Context() never carries an OIDC plugin id here — unlike the
// old code, there is no OIDC branch to keep in sync with that guarantee.
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

// handlePublishVersion backs POST /api/v1/plugins/{pluginId}/versions
// (FR-OP-02), gated by requireSessionOrPublishOIDC. An OIDC-authenticated
// call (oidcPlugin != "") has already been confirmed allow-listed for this
// exact URL pluginId by the check below, so PublishVersion is told to
// auto-create the plugin entry (tier: official) when it doesn't exist yet
// instead of 404ing — AMD-15-ci-first-publish / D-05 ("auto-create"). An
// operator-session call never auto-creates: first publish via the Operator
// UI goes through POST /api/v1/plugins (handlePublishFirst) instead.
//
// AMD-04-duplicate-publish-contract: idempotent is true when this call
// resolved a byte-identical republish of an already-committed version,
// which maps to 200 instead of 201 with no objects written.
//
// AMD-06-removal-lifecycle: a tombstoned plugin.json makes PublishVersion
// return *publish.RemovedError regardless of autoCreate — this route never
// resurrects (D-06 explicitly reserves that to POST /api/v1/plugins).
func (s *Server) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	pluginID := chiParam(r, "pluginId")
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
	res, idempotent, err := s.deps.Publish.PublishVersion(r.Context(), pluginID, bundle, operatorIdentity(r.Context()), autoCreate)
	if err != nil {
		var removed *publish.RemovedError
		if errors.As(err, &removed) {
			writeJSON(w, http.StatusGone, removed.Tombstone)
			return
		}
		writeError(w, mapPublishErr(err))
		return
	}
	status := http.StatusCreated
	if idempotent {
		status = http.StatusOK
	}
	writeJSON(w, status, res)
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
	if errors.Is(err, publish.ErrPluginExists) {
		return &APIError{Status: http.StatusConflict, Code: CodePluginAlreadyExists, Message: "A plugin with this id already exists; publish a new version via POST /api/v1/plugins/{id}/versions instead"}
	}
	if errors.Is(err, publish.ErrVersionConflict) {
		return &APIError{Status: http.StatusConflict, Code: CodeVersionAlreadyPublished, Message: "This version has already been published with different content"}
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
	st, hk, err := s.deps.Lifecycle.SetTier(r.Context(), pluginID, req.Tier)
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
	blockedVersions := make([]BlockedVersionResponse, len(st.BlockedVersions))
	for i, bv := range st.BlockedVersions {
		blockedVersions[i] = newBlockedVersionResponse(bv)
	}
	resp := PluginOperatorStateResponse{
		ID: st.ID, Tier: st.Tier, LatestVersion: st.LatestVersion, BlockedVersions: blockedVersions,
	}
	// The tier change itself already committed successfully by this point
	// (see lifecycle.HousekeepingOutcome's doc comment) -- a degraded
	// housekeeping outcome is surfaced as a warning on the 200 response, not
	// as an error, so the caller can still tell the two apart.
	if hk.Degraded() {
		resp.Warnings = hk.Warnings
	}
	writeJSON(w, http.StatusOK, resp)
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
	tomb, hk, err := s.deps.Lifecycle.RemovePlugin(r.Context(), pluginID, req.RemovalReason, operatorIdentity(r.Context()))
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
	// The removal itself already committed successfully by this point (see
	// lifecycle.HousekeepingOutcome's doc comment) -- a degraded
	// housekeeping outcome is surfaced as a warning on the 200 response, not
	// as an error.
	if hk.Degraded() {
		tomb.Warnings = hk.Warnings
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
	writeJSON(w, http.StatusOK, newBlockedVersionResponse(*bv))
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
	writeJSON(w, http.StatusOK, newSecurityAdvisoryResponse(*adv))
}
