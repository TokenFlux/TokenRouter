package service

import (
	"context"
	"testing"
)

func TestGetOpsAdvancedSettings_DefaultHidesOpenAITokenStats(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}

	cfg, err := svc.GetOpsAdvancedSettings(context.Background())
	if err != nil {
		t.Fatalf("GetOpsAdvancedSettings() error = %v", err)
	}
	if cfg.DisplayOpenAITokenStats {
		t.Fatalf("DisplayOpenAITokenStats = true, want false by default")
	}
	if repo.setCalls != 1 {
		t.Fatalf("expected defaults to be persisted once, got %d", repo.setCalls)
	}
}

func TestUpdateOpsAdvancedSettings_PersistsOpenAITokenStatsVisibility(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}

	cfg := defaultOpsAdvancedSettings()
	cfg.DisplayOpenAITokenStats = true

	updated, err := svc.UpdateOpsAdvancedSettings(context.Background(), cfg)
	if err != nil {
		t.Fatalf("UpdateOpsAdvancedSettings() error = %v", err)
	}
	if !updated.DisplayOpenAITokenStats {
		t.Fatalf("DisplayOpenAITokenStats = false, want true")
	}

	reloaded, err := svc.GetOpsAdvancedSettings(context.Background())
	if err != nil {
		t.Fatalf("GetOpsAdvancedSettings() after update error = %v", err)
	}
	if !reloaded.DisplayOpenAITokenStats {
		t.Fatalf("reloaded DisplayOpenAITokenStats = false, want true")
	}
}
