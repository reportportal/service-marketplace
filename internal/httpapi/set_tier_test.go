package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
)

// TestOnlyOfficialTierCanBeGranted pins ADR-003 rather than a behaviour someone might tidy away.
//
// `domain.TrustTier` declares `partner`, and nothing in this registry will ever set it. That reads
// like a gap — it was recorded as one — so it is worth stating outright: the tiers are reserved in
// the schema so entries published today need no migration when partner onboarding arrives, and the
// workflow that would earn a plugin that tier is Phase 3. Until it exists, a tier the registry
// cannot decide how to grant is one an operator must not be able to assert by hand.
//
// Kills relaxing the guard in lifecycle.SetTier to accept every declared tier.
func TestOnlyOfficialTierCanBeGranted(t *testing.T) {
	env := newTestEnv(t)
	if rec := publishViaHTTP(t, env, listingManifest(testOIDCPluginID, "1.0.0", ">=25.1")); rec.Code != http.StatusCreated {
		t.Fatalf("publish: status %d body=%s", rec.Code, rec.Body.String())
	}

	setTier := func(tier domain.TrustTier) int {
		body, err := json.Marshal(map[string]string{"tier": string(tier)})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		target := "/api/v1/plugins/" + testOIDCPluginID

		return env.do(env.newRequest(http.MethodPatch, target, credOperatorSession, body,
			"application/json")).Code
	}

	if status := setTier(domain.TierOfficial); status != http.StatusOK {
		t.Errorf("granting official: status %d, want 200", status)
	}
	if status := setTier(domain.TierPartner); status == http.StatusOK {
		t.Errorf("granting partner succeeded; ADR-003 defers that tier to Phase 3")
	}
}
