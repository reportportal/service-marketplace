package publish

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/storage"
)

func newTestPublishService(t *testing.T) *Service {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return &Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
}

func manifestFor(id, version string) *domain.Manifest {
	return &domain.Manifest{
		ID: id, Name: "Demo", Version: version, Description: "d",
		Author: domain.Author{Name: "A"}, License: "Apache-2.0",
		Category: domain.CategoryImport, Compatibility: domain.Compatibility{ReportPortal: ">=25.1"},
		Access: domain.AccessPublic,
	}
}

func readPluginState(t *testing.T, svc *Service, pluginID string) domain.PluginState {
	t.Helper()
	obj, err := svc.Store.Read(context.Background(), storage.PluginStatePath(pluginID))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}
	var st domain.PluginState
	if err := json.Unmarshal(obj.Data, &st); err != nil {
		t.Fatalf("unmarshal plugin.json: %v", err)
	}
	return st
}

func publishVersion(t *testing.T, svc *Service, pluginID, version string, first bool) {
	t.Helper()
	jar, err := BuildTestJAR(manifestFor(pluginID, version))
	if err != nil {
		t.Fatalf("BuildTestJAR: %v", err)
	}
	bundle := &Bundle{JAR: jar}
	ctx := context.Background()
	if first {
		if _, err := svc.PublishFirst(ctx, bundle, "operator"); err != nil {
			t.Fatalf("PublishFirst(%s): %v", version, err)
		}
		return
	}
	if _, _, err := svc.PublishVersion(ctx, pluginID, bundle, "operator", false); err != nil {
		t.Fatalf("PublishVersion(%s): %v", version, err)
	}
}

// TestPublish_LatestVersion_1_9_vs_1_10 pins down the exact bug described in
// the F1 finding: publishing 1.10.0 and then 1.9.0 must not regress
// latestVersion to 1.9.0 -- the pointer must stay the SemVer-maximum, not
// "whatever was published last".
//
// Mutation this kills: reverting publish()'s recompute to the original
// unconditional `st.LatestVersion = m.Version` -- that makes this test fail
// because the last call publishes 1.9.0.
func TestPublish_LatestVersion_1_9_vs_1_10(t *testing.T) {
	svc := newTestPublishService(t)
	const pluginID = "plugin-demo"

	publishVersion(t, svc, pluginID, "1.10.0", true)
	publishVersion(t, svc, pluginID, "1.9.0", false)

	st := readPluginState(t, svc, pluginID)
	if st.LatestVersion != "1.10.0" {
		t.Fatalf("latestVersion = %q, want %q (1.9.0 published after 1.10.0 must not become latest)", st.LatestVersion, "1.10.0")
	}
}

// TestPublish_LatestVersion_LegacyHotfixDoesNotRegress is the §6.2
// legacy-hotfix case AMD-07 calls out explicitly: publishing a final patch
// to an older line (1.4.3) after a newer major (2.0.0) already exists must
// leave latestVersion at 2.0.0, not regress the catalogue.
//
// Mutation this kills: the same unconditional last-publish-wins assignment
// -- it would set latestVersion to 1.4.3 here.
func TestPublish_LatestVersion_LegacyHotfixDoesNotRegress(t *testing.T) {
	svc := newTestPublishService(t)
	const pluginID = "plugin-jira-cloud"

	publishVersion(t, svc, pluginID, "2.0.0", true)
	publishVersion(t, svc, pluginID, "1.4.3", false)

	st := readPluginState(t, svc, pluginID)
	if st.LatestVersion != "2.0.0" {
		t.Fatalf("latestVersion = %q, want %q (legacy hotfix publish must not move the pointer)", st.LatestVersion, "2.0.0")
	}
}

// TestPublish_LatestVersion_PreReleaseNeverWins publishes a pre-release with
// a numerically higher core (2.0.0-rc.1) after a released 1.0.0 exists.
// Because raw SemVer precedence ranks 2.0.0-rc.1 above 1.0.0 (major 2 > 1),
// only the explicit pre-release exclusion in AMD-07 keeps latestVersion at
// the released 1.0.0.
//
// Mutation this kills: dropping the IsPreRelease filter from
// domain.LatestVersion (i.e. using plain CompareVersions over all
// versions) -- that would advertise the release candidate as latest.
func TestPublish_LatestVersion_PreReleaseNeverWins(t *testing.T) {
	svc := newTestPublishService(t)
	const pluginID = "plugin-demo"

	publishVersion(t, svc, pluginID, "1.0.0", true)
	publishVersion(t, svc, pluginID, "2.0.0-rc.1", false)

	st := readPluginState(t, svc, pluginID)
	if st.LatestVersion != "1.0.0" {
		t.Fatalf("latestVersion = %q, want %q (a pre-release must never outrank a released version)", st.LatestVersion, "1.0.0")
	}
}
