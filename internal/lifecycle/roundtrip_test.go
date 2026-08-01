package lifecycle

// domain.BlockedVersion and domain.SecurityAdvisory are dual-purpose the same way
// domain.LicenseEntitlement/LicensePublicKey were (see internal/httpapi/
// wire_storage_separation_test.go and e8501ac): they are the literal shape
// json.Marshal/json.Unmarshal uses inside plugins/{id}/plugin.json (via
// domain.PluginState.BlockedVersions and .VersionStates[version].Advisory, read/
// written directly by this package's Service), and until this change they were also
// marshalled straight onto the HTTP response. That coupling is now closed on the wire
// side (see internal/httpapi.BlockedVersionResponse/SecurityAdvisoryResponse) but the
// storage side needs its own guarantee: these tests pin plugin.json's actual on-disk
// shape against a hand-maintained, independent mirror of it, the same way
// internal/license/roundtrip_test.go pins auth/authorized_keys.json. If a future change
// to satisfy the wire contract is made on domain.PluginState/BlockedVersion/
// SecurityAdvisory directly instead of on the httpapi response types, it will move
// plugin.json's shape and fail here -- not just leave the coupling silently
// reintroduced with no test catching it.
//
//   - a plugin.json written by the release before this change must still load
//     correctly (upgrade safety)
//   - a plugin.json written by this code must still parse under that same shape
//     (rollback safety / no silent format drift)

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

// previousReleaseBlockedVersion, previousReleaseVersionMeta,
// previousReleaseSecurityAdvisory, previousReleaseVersionState and
// previousReleasePluginState mirror domain.PluginState's json shape independently of
// the production type -- round-tripping domain.PluginState through itself would prove
// nothing about compatibility with what is actually on disk (or with what a
// differently-typed reader, like an older binary, would see).
type previousReleaseBlockedVersion struct {
	Version   string    `json:"version"`
	BlockedAt time.Time `json:"blockedAt"`
	Reason    string    `json:"reason"`
}

type previousReleaseVersionMeta struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"publishedAt,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
}

type previousReleaseSecurityAdvisory struct {
	Severity   string    `json:"severity"`
	Text       string    `json:"text"`
	AttachedAt time.Time `json:"attachedAt"`
}

type previousReleaseVersionState struct {
	Advisory *previousReleaseSecurityAdvisory `json:"advisory,omitempty"`
}

type previousReleasePluginState struct {
	ID              string                                 `json:"id"`
	Tier            string                                 `json:"tier"`
	LatestVersion   string                                 `json:"latestVersion"`
	Versions        []previousReleaseVersionMeta           `json:"versions"`
	BlockedVersions []previousReleaseBlockedVersion        `json:"blockedVersions,omitempty"`
	Removed         *time.Time                             `json:"removed,omitempty"`
	RemovalReason   string                                 `json:"removalReason,omitempty"`
	RemovedBy       string                                 `json:"removedBy,omitempty"`
	VersionStates   map[string]previousReleaseVersionState `json:"versionStates,omitempty"`
}

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewLocalStore(root, "", "")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	pub := &publish.Service{Store: store, Invalidator: cdn.NoopInvalidator{}}
	return &Service{Store: store, Invalidator: cdn.NoopInvalidator{}, Publisher: pub}, root
}

func pluginStatePath(root, pluginID string) string {
	return filepath.Join(root, filepath.FromSlash(storage.PluginStatePath(pluginID)))
}

// TestBlockVersion_PreservesDocumentWrittenByPreviousRelease seeds a plugin.json with
// the exact bytes the previous release would have written -- an existing blocked
// version and an existing attached advisory -- then calls BlockVersion to block a
// second version. It asserts both that the read path parses the seeded document (the
// call succeeds and does not reject the pre-existing blockedVersions/versionStates
// data) and that what Service.BlockVersion writes back keeps the previously-blocked
// version and the previously-attached advisory intact and still readable under the
// exact same struct shape (rollback safety).
func TestBlockVersion_PreservesDocumentWrittenByPreviousRelease(t *testing.T) {
	svc, root := newTestService(t)
	ctx := context.Background()
	pluginID := "plugin-jira-cloud"

	v1Published := time.Now().UTC().AddDate(0, -6, 0).Truncate(time.Second)
	v2Published := time.Now().UTC().AddDate(0, -1, 0).Truncate(time.Second)
	blockedAt := time.Now().UTC().AddDate(0, -1, 0).Truncate(time.Second)
	attachedAt := time.Now().UTC().AddDate(0, 0, -3).Truncate(time.Second)

	seed := fmt.Sprintf(`{
  "id": %q,
  "tier": "official",
  "latestVersion": "2.0.0",
  "versions": [
    {"version": "1.0.0", "publishedAt": %q, "sha256": "aaa"},
    {"version": "2.0.0", "publishedAt": %q, "sha256": "bbb"}
  ],
  "blockedVersions": [
    {"version": "1.0.0", "blockedAt": %q, "reason": "CVE-2025-0001"}
  ],
  "versionStates": {
    "2.0.0": {"advisory": {"severity": "high", "text": "CVE-2026-1234", "attachedAt": %q}}
  }
}`, pluginID, v1Published.Format(time.RFC3339), v2Published.Format(time.RFC3339),
		blockedAt.Format(time.RFC3339), attachedAt.Format(time.RFC3339))

	path := pluginStatePath(root, pluginID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed plugin.json: %v", err)
	}

	blocked, err := svc.BlockVersion(ctx, pluginID, "2.0.0", "supersedes 1.0.0")
	if err != nil {
		t.Fatalf("BlockVersion (reading a document written by the previous release): %v", err)
	}
	if blocked.Version != "2.0.0" || blocked.Reason != "supersedes 1.0.0" {
		t.Fatalf("unexpected new BlockedVersion: %+v", blocked)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written document: %v", err)
	}
	var old previousReleasePluginState
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("a plugin.json written by this code must still parse under the previous release's struct shape (rollback safety): %v\nraw: %s", err, raw)
	}

	if old.ID != pluginID || old.Tier != "official" || old.LatestVersion != "2.0.0" {
		t.Fatalf("plugin identity/tier/latestVersion lost: %+v", old)
	}
	if len(old.Versions) != 2 {
		t.Fatalf("versions lost on rewrite: %+v", old.Versions)
	}
	if len(old.BlockedVersions) != 2 {
		t.Fatalf("want 2 blockedVersions (the pre-existing 1.0.0 block plus the new 2.0.0 block), got %d: %+v", len(old.BlockedVersions), old.BlockedVersions)
	}
	var sawV1, sawV2 bool
	for _, bv := range old.BlockedVersions {
		if bv.Version == "1.0.0" {
			sawV1 = true
			if bv.Reason != "CVE-2025-0001" || !bv.BlockedAt.Equal(blockedAt) {
				t.Fatalf("pre-existing blockedVersion 1.0.0 was altered by an unrelated write: %+v", bv)
			}
		}
		if bv.Version == "2.0.0" {
			sawV2 = true
			if bv.Reason != "supersedes 1.0.0" {
				t.Fatalf("new blockedVersion 2.0.0 has wrong reason: %+v", bv)
			}
		}
	}
	if !sawV1 || !sawV2 {
		t.Fatalf("blockedVersions missing an entry: %+v", old.BlockedVersions)
	}

	vs, ok := old.VersionStates["2.0.0"]
	if !ok || vs.Advisory == nil {
		t.Fatalf("pre-existing advisory on 2.0.0 was dropped by an unrelated write: %+v", old.VersionStates)
	}
	if vs.Advisory.Severity != "high" || vs.Advisory.Text != "CVE-2026-1234" || !vs.Advisory.AttachedAt.Equal(attachedAt) {
		t.Fatalf("pre-existing advisory on 2.0.0 was altered by an unrelated write: %+v", vs.Advisory)
	}
}

// TestAttachAdvisory_WritesDocumentThePreviousReleaseCanStillRead exercises
// Service.AttachAdvisory against a real LocalStore, then parses the bytes it actually
// wrote using the previous release's struct shape.
func TestAttachAdvisory_WritesDocumentThePreviousReleaseCanStillRead(t *testing.T) {
	svc, root := newTestService(t)
	ctx := context.Background()
	pluginID := "plugin-jira-cloud"

	seed := fmt.Sprintf(`{
  "id": %q,
  "tier": "official",
  "latestVersion": "1.0.0",
  "versions": [{"version": "1.0.0", "publishedAt": %q, "sha256": "aaa"}]
}`, pluginID, time.Now().UTC().Format(time.RFC3339))
	path := pluginStatePath(root, pluginID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed plugin.json: %v", err)
	}

	if _, err := svc.AttachAdvisory(ctx, pluginID, "1.0.0", "critical", "CVE-2026-9999 remote code execution"); err != nil {
		t.Fatalf("AttachAdvisory: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written document: %v", err)
	}
	var old previousReleasePluginState
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("a plugin.json written by this code must still parse under the previous release's struct shape (rollback safety): %v\nraw: %s", err, raw)
	}
	vs, ok := old.VersionStates["1.0.0"]
	if !ok || vs.Advisory == nil {
		t.Fatalf("advisory not present in written document: %+v", old.VersionStates)
	}
	if vs.Advisory.Severity != "critical" || vs.Advisory.Text != "CVE-2026-9999 remote code execution" || vs.Advisory.AttachedAt.IsZero() {
		t.Fatalf("advisory fields lost or renamed on write: %+v", vs.Advisory)
	}
}
