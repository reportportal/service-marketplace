package config

import "testing"

// TestLoad_OrphanCleanupDisabledByDefault is the load-bearing artefact of the
// go3/cleanup-lifecycle decision: the orphan sweeper
// (internal/lifecycle.OrphanCleanup) ships disabled, and that has to be a
// contract enforced by this test forever after, not a default someone can
// flip while editing an unrelated line. It fails if ORPHAN_CLEANUP_ENABLED
// is left unset and Load() ever produces a Config with the sweep turned on
// -- whether because getEnvBool's default argument changed, because
// ORPHAN_CLEANUP_ENABLED stopped being read at all ("unset means on" by
// omission), or because some other field started implying Enabled. It also
// pins OrphanCleanupDryRun's default to true, because "enabled but dry-run
// defaults true" and "enabled and immediately allowed to delete" are two
// very different postures and only the former is acceptable as any kind of
// default.
func TestLoad_OrphanCleanupDisabledByDefault(t *testing.T) {
	t.Setenv("ORPHAN_CLEANUP_ENABLED", "")
	t.Setenv("ORPHAN_CLEANUP_DRY_RUN", "")
	t.Setenv("ALLOW_INSECURE_DEFAULTS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OrphanCleanupEnabled {
		t.Fatalf("OrphanCleanupEnabled = true with ORPHAN_CLEANUP_ENABLED unset, want false: the sweeper must never be on by default")
	}
	if !cfg.OrphanCleanupDryRun {
		t.Fatalf("OrphanCleanupDryRun = false with ORPHAN_CLEANUP_DRY_RUN unset, want true: turning the sweeper on and trusting it to delete must stay two separate operator actions")
	}
}

// TestLoad_OrphanCleanupEnabledFailsClosedOnUnrecognisedValue proves the
// "no partially-configured state that sweeps" half of the contract from the
// other direction: an operator's typo or an unexpected value in
// ORPHAN_CLEANUP_ENABLED must fail closed (stay disabled), never parse as
// truthy by accident.
func TestLoad_OrphanCleanupEnabledFailsClosedOnUnrecognisedValue(t *testing.T) {
	for _, v := range []string{"on", "enabled", "yes", "1yes", " true", "TRUE "} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("ORPHAN_CLEANUP_ENABLED", v)
			t.Setenv("ALLOW_INSECURE_DEFAULTS", "true")

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.OrphanCleanupEnabled {
				t.Fatalf("OrphanCleanupEnabled = true for ORPHAN_CLEANUP_ENABLED=%q, want false (unrecognised value must fail closed)", v)
			}
		})
	}
}

// TestLoad_OrphanCleanupEnabledRequiresExplicitTrue is the positive control
// for the two tests above: the deliberate, visible opt-in path must still
// work, so this isn't a guard that can never be turned off by design -- only
// one that never turns itself on.
func TestLoad_OrphanCleanupEnabledRequiresExplicitTrue(t *testing.T) {
	t.Setenv("ORPHAN_CLEANUP_ENABLED", "true")
	t.Setenv("ALLOW_INSECURE_DEFAULTS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OrphanCleanupEnabled {
		t.Fatalf("OrphanCleanupEnabled = false with ORPHAN_CLEANUP_ENABLED=true, want true: explicit opt-in must still work")
	}
}

// TestLoad_EnablingWithoutSettingDryRunStaysDryRun proves an operator can
// set only ORPHAN_CLEANUP_ENABLED=true (forgetting or not yet deciding about
// dry-run) without that alone granting delete authority: DryRun's own
// default (true) still applies, so this partially-configured state observes
// rather than deletes.
func TestLoad_EnablingWithoutSettingDryRunStaysDryRun(t *testing.T) {
	t.Setenv("ORPHAN_CLEANUP_ENABLED", "true")
	t.Setenv("ORPHAN_CLEANUP_DRY_RUN", "")
	t.Setenv("ALLOW_INSECURE_DEFAULTS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OrphanCleanupDryRun {
		t.Fatalf("OrphanCleanupDryRun = false with only ORPHAN_CLEANUP_ENABLED set, want true: enabling alone must never also grant delete authority")
	}
}
