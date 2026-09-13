package ops

import (
	"context"
	"encoding/json"
	"testing"
)

func TestGetOpsAdvancedSettings_DefaultSnapshotHidesOpenAITokenStats(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}

	cfg, err := svc.GetOpsAdvancedSettings(context.Background())
	if err != nil {
		t.Fatalf("GetOpsAdvancedSettings() error = %v", err)
	}
	if cfg.DisplayOpenAITokenStats {
		t.Fatalf("DisplayOpenAITokenStats = true, want false by default")
	}
	if !cfg.DisplayAlertEvents {
		t.Fatalf("DisplayAlertEvents = false, want true by default")
	}
	if got := cfg.IgnoredStatusCodes; len(got) != 2 || got[0] != 401 || got[1] != 403 {
		t.Fatalf("IgnoredStatusCodes = %#v, want [401 403]", got)
	}
	if cfg.DataRetention.CleanupSchedule != "0 3 * * *" {
		t.Fatalf("CleanupSchedule = %q, want 0 3 * * *", cfg.DataRetention.CleanupSchedule)
	}
	if cfg.DataRetention.CleanupBatchSize != 1000 || cfg.DataRetention.CleanupPauseMS != 200 {
		t.Fatalf("cleanup tuning = %d/%d, want 1000/200", cfg.DataRetention.CleanupBatchSize, cfg.DataRetention.CleanupPauseMS)
	}
	if repo.getValueCalls != 0 || repo.getMultipleCalls != 0 {
		t.Fatalf("hot-path snapshot read touched repository: get=%d get_multiple=%d", repo.getValueCalls, repo.getMultipleCalls)
	}
}
func TestUpdateOpsAdvancedSettings_PersistsOpenAITokenStatsVisibility(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}

	cfg := defaultOpsAdvancedSettings()
	cfg.DisplayOpenAITokenStats = true
	cfg.DisplayAlertEvents = false

	updated, err := svc.UpdateOpsAdvancedSettings(context.Background(), cfg)
	if err != nil {
		t.Fatalf("UpdateOpsAdvancedSettings() error = %v", err)
	}
	if !updated.DisplayOpenAITokenStats {
		t.Fatalf("DisplayOpenAITokenStats = false, want true")
	}
	if updated.DisplayAlertEvents {
		t.Fatalf("DisplayAlertEvents = true, want false")
	}
	readsAfterUpdate := repo.getValueCalls + repo.getMultipleCalls

	reloaded, err := svc.GetOpsAdvancedSettings(context.Background())
	if err != nil {
		t.Fatalf("GetOpsAdvancedSettings() after update error = %v", err)
	}
	if !reloaded.DisplayOpenAITokenStats {
		t.Fatalf("reloaded DisplayOpenAITokenStats = false, want true")
	}
	if reloaded.DisplayAlertEvents {
		t.Fatalf("reloaded DisplayAlertEvents = true, want false")
	}
	if got := repo.getValueCalls + repo.getMultipleCalls; got != readsAfterUpdate {
		t.Fatalf("snapshot reload performed repository read: before=%d after=%d", readsAfterUpdate, got)
	}
}
func TestGetOpsAdvancedSettings_BackfillsNewDisplayFlagsFromDefaults(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}

	legacyCfg := map[string]any{
		"data_retention": map[string]any{
			"cleanup_enabled":               false,
			"cleanup_schedule":              "0 2 * * *",
			"error_log_retention_days":      30,
			"minute_metrics_retention_days": 30,
			"hourly_metrics_retention_days": 30,
		},
		"aggregation": map[string]any{
			"aggregation_enabled": false,
		},
		"ignore_count_tokens_errors":    true,
		"ignore_context_canceled":       true,
		"ignore_no_available_accounts":  false,
		"ignore_invalid_api_key_errors": true,
		"auto_refresh_enabled":          false,
		"auto_refresh_interval_seconds": 30,
	}
	raw, err := json.Marshal(legacyCfg)
	if err != nil {
		t.Fatalf("marshal legacy config: %v", err)
	}
	repo.values[SettingKeyOpsAdvancedSettings] = string(raw)

	cfg, err := svc.GetOpsAdvancedSettings(context.Background())
	if err != nil {
		t.Fatalf("GetOpsAdvancedSettings() error = %v", err)
	}
	if cfg.DisplayOpenAITokenStats {
		t.Fatalf("DisplayOpenAITokenStats = true, want false default backfill")
	}
	if !cfg.DisplayAlertEvents {
		t.Fatalf("DisplayAlertEvents = false, want true default backfill")
	}
	if got := cfg.IgnoredStatusCodes; len(got) != 2 || got[0] != 401 || got[1] != 403 {
		t.Fatalf("IgnoredStatusCodes = %#v, want default backfill [401 403]", got)
	}
	if cfg.DataRetention.CleanupBatchSize != 1000 || cfg.DataRetention.CleanupPauseMS != 200 {
		t.Fatalf("legacy cleanup tuning = %d/%d, want default backfill 1000/200", cfg.DataRetention.CleanupBatchSize, cfg.DataRetention.CleanupPauseMS)
	}
}
func TestUpdateOpsAdvancedSettings_NormalizesIgnoredStatusCodes(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}

	cfg := defaultOpsAdvancedSettings()
	cfg.IgnoredStatusCodes = []int{403, 401, 401}

	updated, err := svc.UpdateOpsAdvancedSettings(context.Background(), cfg)
	if err != nil {
		t.Fatalf("UpdateOpsAdvancedSettings() error = %v", err)
	}
	if got := updated.IgnoredStatusCodes; len(got) != 2 || got[0] != 401 || got[1] != 403 {
		t.Fatalf("IgnoredStatusCodes = %#v, want normalized [401 403]", got)
	}
}
func TestUpdateOpsAdvancedSettings_AllowsEmptyIgnoredStatusCodes(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}

	cfg := defaultOpsAdvancedSettings()
	cfg.IgnoredStatusCodes = []int{}

	updated, err := svc.UpdateOpsAdvancedSettings(context.Background(), cfg)
	if err != nil {
		t.Fatalf("UpdateOpsAdvancedSettings() error = %v", err)
	}
	if len(updated.IgnoredStatusCodes) != 0 {
		t.Fatalf("IgnoredStatusCodes = %#v, want empty", updated.IgnoredStatusCodes)
	}
}
