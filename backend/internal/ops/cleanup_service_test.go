package ops

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// makeOverlayService constructs an exclusively owned unstarted cleanup fixture
// with deployment values and a supplied settings reader. No cron or backend is
// installed; tests invoke effective computation directly before any concurrent
// use.
func makeOverlayService(repo SettingValueReader, base CleanupOptions) *OpsCleanupService {
	cfg := &Options{}
	cfg.Ops.Cleanup = base
	return &OpsCleanupService{
		cfg:         cfg,
		settingRepo: repo,
	}
}

// writeAdvancedSettings serializes supplied retention fields into the
// advanced-settings fixture record and fatals on encoding or write failure. It
// leaves unspecified advanced fields zero so cleanup tests exercise that
// reader's own overlay rules.
func writeAdvancedSettings(t *testing.T, repo *runtimeSettingRepoStub, dr OpsDataRetentionSettings) {
	t.Helper()
	adv := OpsAdvancedSettings{DataRetention: dr}
	raw, err := json.Marshal(adv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := repo.Set(context.Background(), SettingKeyOpsAdvancedSettings, string(raw)); err != nil {
		t.Fatalf("set: %v", err)
	}
}

// TestComputeEffective_FallbackToCfgWhenSettingsAbsent supplies complete
// deployment cleanup values with no saved record. It protects retention,
// pacing, enabled, and schedule remaining equal to the supplied base.
func TestComputeEffective_FallbackToCfgWhenSettingsAbsent(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	base := CleanupOptions{
		Enabled:                    false,
		Schedule:                   "0 2 * * *",
		BatchSize:                  opsCleanupDefaultBatchSize,
		BatchPauseMS:               int(opsCleanupDefaultBatchPause / time.Millisecond),
		ErrorLogRetentionDays:      30,
		MinuteMetricsRetentionDays: 30,
		HourlyMetricsRetentionDays: 30,
	}
	svc := makeOverlayService(repo, base)

	svc.computeEffectiveLocked(context.Background())

	if svc.effective != base {
		t.Fatalf("expected effective == cfg base, got %#v", svc.effective)
	}
}

// TestComputeEffective_SettingsOverridesAll stores enabled cleanup with
// zero/seven/fourteen-day retention and new schedule text. It protects saved
// overrides while omitted pacing retains the deployment defaults in this
// fixture.
func TestComputeEffective_SettingsOverridesAll(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	writeAdvancedSettings(t, repo, OpsDataRetentionSettings{
		CleanupEnabled:             true,
		CleanupSchedule:            "0 * * * *",
		ErrorLogRetentionDays:      0,
		MinuteMetricsRetentionDays: 7,
		HourlyMetricsRetentionDays: 14,
	})
	base := CleanupOptions{
		Enabled:                    false,
		Schedule:                   "0 2 * * *",
		BatchSize:                  opsCleanupDefaultBatchSize,
		BatchPauseMS:               int(opsCleanupDefaultBatchPause / time.Millisecond),
		ErrorLogRetentionDays:      30,
		MinuteMetricsRetentionDays: 30,
		HourlyMetricsRetentionDays: 30,
	}
	svc := makeOverlayService(repo, base)

	svc.computeEffectiveLocked(context.Background())

	want := CleanupOptions{
		Enabled:                    true,
		Schedule:                   "0 * * * *",
		BatchSize:                  opsCleanupDefaultBatchSize,
		BatchPauseMS:               int(opsCleanupDefaultBatchPause / time.Millisecond),
		ErrorLogRetentionDays:      0,
		MinuteMetricsRetentionDays: 7,
		HourlyMetricsRetentionDays: 14,
	}
	if svc.effective != want {
		t.Fatalf("effective mismatch:\nwant %#v\n got %#v", want, svc.effective)
	}
}

// TestComputeEffective_SystemLogRetentionUsesRuntimeConfig stores advanced
// retention plus a positive independent logger override. It protects separate
// seven-day error retention and one-day system-log retention in effective
// values.
func TestComputeEffective_SystemLogRetentionUsesRuntimeConfig(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	writeAdvancedSettings(t, repo, OpsDataRetentionSettings{
		CleanupEnabled:             true,
		CleanupSchedule:            "0 3 * * *",
		CleanupBatchSize:           1000,
		CleanupPauseMS:             200,
		ErrorLogRetentionDays:      7,
		MinuteMetricsRetentionDays: 7,
		HourlyMetricsRetentionDays: 7,
	})
	runtimeRaw, err := json.Marshal(OpsRuntimeLogConfig{RetentionDays: 1})
	if err != nil {
		t.Fatalf("marshal runtime config: %v", err)
	}
	if err := repo.Set(context.Background(), SettingKeyOpsRuntimeLogConfig, string(runtimeRaw)); err != nil {
		t.Fatalf("set runtime config: %v", err)
	}

	base := CleanupOptions{
		Enabled:                    true,
		Schedule:                   "0 3 * * *",
		BatchSize:                  1000,
		BatchPauseMS:               200,
		ErrorLogRetentionDays:      30,
		SystemLogRetentionDays:     30,
		MinuteMetricsRetentionDays: 30,
		HourlyMetricsRetentionDays: 30,
	}
	svc := makeOverlayService(repo, base)
	svc.computeEffectiveLocked(context.Background())

	if svc.effective.ErrorLogRetentionDays != 7 {
		t.Fatalf("error retention = %d, want 7", svc.effective.ErrorLogRetentionDays)
	}
	if svc.effective.SystemLogRetentionDays != 1 {
		t.Fatalf("system retention = %d, want 1", svc.effective.SystemLogRetentionDays)
	}
}

// TestComputeEffective_EmptyScheduleFallbackToCfg supplies a whitespace-only
// saved schedule and positive retention. It protects retained deployment
// schedule alongside accepted saved enablement and five-day error retention.
func TestComputeEffective_EmptyScheduleFallbackToCfg(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	writeAdvancedSettings(t, repo, OpsDataRetentionSettings{
		CleanupEnabled:             true,
		CleanupSchedule:            "   ", // Whitespace is treated as empty after trimming.
		ErrorLogRetentionDays:      5,
		MinuteMetricsRetentionDays: 5,
		HourlyMetricsRetentionDays: 5,
	})
	base := CleanupOptions{
		Enabled:                    false,
		Schedule:                   "0 2 * * *",
		ErrorLogRetentionDays:      30,
		MinuteMetricsRetentionDays: 30,
		HourlyMetricsRetentionDays: 30,
	}
	svc := makeOverlayService(repo, base)

	svc.computeEffectiveLocked(context.Background())

	if svc.effective.Schedule != "0 2 * * *" {
		t.Fatalf("expected schedule fallback to cfg, got %q", svc.effective.Schedule)
	}
	if !svc.effective.Enabled {
		t.Fatalf("expected enabled=true from settings")
	}
	if svc.effective.ErrorLogRetentionDays != 5 {
		t.Fatalf("expected retention=5 from settings, got %d", svc.effective.ErrorLogRetentionDays)
	}
}

// TestComputeEffective_NegativeRetentionFallsBackToCfg supplies negative
// advanced retention fields and distinct deployment day values. It protects
// each deployment retention remaining selected rather than treating negative
// values as zero clearing.
func TestComputeEffective_NegativeRetentionFallsBackToCfg(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	writeAdvancedSettings(t, repo, OpsDataRetentionSettings{
		CleanupEnabled:             true,
		CleanupSchedule:            "0 * * * *",
		ErrorLogRetentionDays:      -1,
		MinuteMetricsRetentionDays: -1,
		HourlyMetricsRetentionDays: -1,
	})
	base := CleanupOptions{
		Enabled:                    false,
		Schedule:                   "0 2 * * *",
		ErrorLogRetentionDays:      30,
		MinuteMetricsRetentionDays: 60,
		HourlyMetricsRetentionDays: 90,
	}
	svc := makeOverlayService(repo, base)

	svc.computeEffectiveLocked(context.Background())

	if svc.effective.ErrorLogRetentionDays != 30 ||
		svc.effective.MinuteMetricsRetentionDays != 60 ||
		svc.effective.HourlyMetricsRetentionDays != 90 {
		t.Fatalf("expected retention fallback to cfg, got %#v", svc.effective)
	}
}

// TestComputeEffective_BadJSONFallsBackToCfg supplies malformed advanced text
// over a complete deployment base. It protects effective equality with that
// base, including enabled state and already supplied pacing values.
func TestComputeEffective_BadJSONFallsBackToCfg(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	if err := repo.Set(context.Background(), SettingKeyOpsAdvancedSettings, "{not json"); err != nil {
		t.Fatalf("set: %v", err)
	}
	base := CleanupOptions{
		Enabled:                    true,
		Schedule:                   "0 3 * * *",
		BatchSize:                  opsCleanupDefaultBatchSize,
		BatchPauseMS:               int(opsCleanupDefaultBatchPause / time.Millisecond),
		ErrorLogRetentionDays:      30,
		MinuteMetricsRetentionDays: 30,
		HourlyMetricsRetentionDays: 30,
	}
	svc := makeOverlayService(repo, base)

	svc.computeEffectiveLocked(context.Background())

	if svc.effective != base {
		t.Fatalf("expected fallback to cfg on bad JSON, got %#v", svc.effective)
	}
}

// TestReload_BeforeStart_IsNoop invokes Reload on an unstarted zero-value
// fixture. It protects successful no-work return before scheduling or
// dependency reads are required for a running cleanup service.
func TestReload_BeforeStart_IsNoop(t *testing.T) {
	svc := &OpsCleanupService{}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatalf("Reload before Start should return nil, got %v", err)
	}
}

// TestReload_AfterStop_IsNoop supplies both started and stopped flags before
// Reload. It protects successful no-work return for a lifecycle already
// permanently closed to schedule replacement.
func TestReload_AfterStop_IsNoop(t *testing.T) {
	svc := &OpsCleanupService{started: true, stopped: true}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatalf("Reload after Stop should return nil, got %v", err)
	}
}

// TestStart_IdempotentSecondCall supplies started true but no
// persistence/backend dependencies before calling Start. It protects return
// through the missing-dependency guard; this fixture does not exercise a
// successfully registered second-start check.
func TestStart_IdempotentSecondCall(t *testing.T) {
	svc := &OpsCleanupService{started: true}
	svc.Start() // missing dependencies stop this fixture first
}

// TestRefreshEffectiveBeforeRun_UpdatesSnapshot replaces saved retention after
// computing a deployment-only fixture. It protects the refreshed seven-day
// value in a copied effective snapshot, with no cron timing or target deletion
// exercised.
func TestRefreshEffectiveBeforeRun_UpdatesSnapshot(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	base := CleanupOptions{
		Enabled:               true,
		Schedule:              "0 2 * * *",
		ErrorLogRetentionDays: 30,
	}
	svc := makeOverlayService(repo, base)
	svc.computeEffectiveLocked(context.Background())

	if svc.effective.ErrorLogRetentionDays != 30 {
		t.Fatalf("initial retention should be 30, got %d", svc.effective.ErrorLogRetentionDays)
	}

	// Replace persisted retention in this exclusively owned fixture before the
	// next refresh, separating current effective values from the subsequent stored
	// override.
	writeAdvancedSettings(t, repo, OpsDataRetentionSettings{
		CleanupEnabled:        true,
		CleanupSchedule:       "0 * * * *",
		ErrorLogRetentionDays: 7,
	})

	svc.refreshEffectiveBeforeRun(context.Background())
	snap := svc.snapshotEffective()
	if snap.ErrorLogRetentionDays != 7 {
		t.Fatalf("after refresh, retention should be 7, got %d", snap.ErrorLogRetentionDays)
	}
}
