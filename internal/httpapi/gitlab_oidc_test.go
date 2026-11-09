package httpapi

import (
	"net/http"
	"testing"
)

func TestGitLabOIDCPublishAuthz(t *testing.T) {
	env := newTestEnv(t)

	t.Run("allow-listed project passes the publish gate", func(t *testing.T) {
		req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+testGitLabPluginID+"/versions", credNone, nil, "")
		req.Header.Set("Authorization", "Bearer "+env.gitlabOIDCToken(testGitLabProject))
		rec := env.do(req)
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("allow-listed gitlab token was refused: %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown project is forbidden", func(t *testing.T) {
		req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+testGitLabPluginID+"/versions", credNone, nil, "")
		req.Header.Set("Authorization", "Bearer "+env.gitlabOIDCToken("EPM-RPP/not-allow-listed"))
		rec := env.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("unknown gitlab project: got %d body=%s, want 403", rec.Code, rec.Body.String())
		}
	})

	t.Run("operator route rejects a valid gitlab token", func(t *testing.T) {
		req := env.newRequest(http.MethodPatch, "/api/v1/plugins/"+testGitLabPluginID, credNone, []byte(`{}`), "application/json")
		req.Header.Set("Authorization", "Bearer "+env.gitlabOIDCToken(testGitLabProject))
		rec := env.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("PATCH with gitlab token: got %d body=%s, want 403", rec.Code, rec.Body.String())
		}
		body := decodeErrorEnvelope(t, rec)
		if body.Code != CodeTokenTypeNotPermitted {
			t.Fatalf("error code %q, want %q", body.Code, CodeTokenTypeNotPermitted)
		}
	})

	t.Run("github publish token still passes the publish gate", func(t *testing.T) {
		req := env.newRequest(http.MethodPost, "/api/v1/plugins/"+testOIDCPluginID+"/versions", credOIDCPublish, nil, "")
		rec := env.do(req)
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("github oidc token was refused: %d body=%s", rec.Code, rec.Body.String())
		}
	})
}
