package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

func TestQoderModelSyncServiceDefaultPathDisabled(t *testing.T) {
	svc := NewQoderModelSyncService(&config.Config{Pricing: config.PricingConfig{DataDir: t.TempDir()}})

	result, err := svc.SyncModels(context.Background(), QoderModelSyncInput{Source: "local"})

	require.Nil(t, result)
	require.ErrorContains(t, err, "configure qoder.model_sync_script_path")
	require.Empty(t, svc.scriptPath)
}

func TestQoderModelSyncServicePreviewShowsDefaultAliasDiffWithoutApplying(t *testing.T) {
	resetQoderModelAliasesForTest()
	t.Cleanup(resetQoderModelAliasesForTest)

	svc := &QoderModelSyncService{
		scriptPath:  "/tmp/qoder-model-sync-script",
		persistPath: filepath.Join(t.TempDir(), qoderModelAliasesFileName),
		runner: func(_ context.Context, source string) ([]byte, error) {
			require.Equal(t, "local", source)
			return []byte(`[
				{"key":"auto","provider":"Qoder","notes":"changed note","display_name":"Qoder Auto","description":"Backend selected"},
				{"key":"newmodel","provider":"New","notes":"new","display_name":"New Model","description":"New desc"}
			]`), nil
		},
	}

	result, err := svc.SyncModels(context.Background(), QoderModelSyncInput{Source: "local"})
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.Equal(t, 2, result.IncomingCount)
	require.NotContains(t, aliasNames(result.Added), "newmodel")
	require.NotContains(t, aliasNames(result.Models), "newmodel")
	require.NotContains(t, aliasNames(result.Models), "qmodel_latest")
	require.NotContains(t, aliasNames(result.Preserved), "gpt-5-codex")
	require.Contains(t, changeNames(result.Changed), "auto")

	info := resolveQoderModel("newmodel")
	require.Equal(t, "newmodel", info.Key, "preview must not mutate runtime aliases")
	_, err = os.Stat(svc.persistPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestQoderModelSyncServiceApplyPersistsAndKeepsCompatibilityAliases(t *testing.T) {
	resetQoderModelAliasesForTest()
	t.Cleanup(resetQoderModelAliasesForTest)

	persistPath := filepath.Join(t.TempDir(), qoderModelAliasesFileName)
	svc := &QoderModelSyncService{
		scriptPath:  "/tmp/qoder-model-sync-script",
		persistPath: persistPath,
		runner: func(_ context.Context, source string) ([]byte, error) {
			require.Equal(t, "cli", source)
			return []byte(`[
				{"key":"ultimate","provider":"Claude","notes":"synced","display_name":"Claude Opus 4.6","description":"Confirmed"},
				{"key":"auto","provider":"Qoder","notes":"synced","display_name":"Qoder Auto","description":"Backend selected"},
				{"key":"qmodel_latest","provider":"Qwen","notes":"synced","display_name":"Qwen3.7-Max","description":"Qwen route"},
				{"key":"newmodel","provider":"New","notes":"new","display_name":"New Model","description":"New desc"}
			]`), nil
		},
	}

	result, err := svc.SyncModels(context.Background(), QoderModelSyncInput{Source: "cli", Apply: true})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.NotContains(t, aliasNames(result.Preserved), "claude-opus-4-6")
	require.Contains(t, changeNames(result.Changed), "claude-opus-4-6")

	info := resolveQoderModel("newmodel")
	require.Equal(t, "newmodel", info.Key)
	info = resolveQoderModel("gpt-5-codex")
	require.Equal(t, "gpt-5-codex", info.Key)
	info = resolveQoderModel("claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)
	require.Equal(t, "Claude", info.Provider)
	info = resolveQoderModel("qwen3.7-max")
	require.Equal(t, "qmodel_latest", info.Key)
	require.Equal(t, "Qwen", info.Provider)
	require.Equal(t, "Qwen3.7-Max", info.DisplayName)
	info = resolveQoderModel("qmodel_latest")
	require.Equal(t, "qmodel_latest", info.Key)
	require.Empty(t, info.Provider)

	body, err := os.ReadFile(persistPath)
	require.NoError(t, err)
	var persisted qoderModelAliasesPersisted
	require.NoError(t, json.Unmarshal(body, &persisted))
	require.Contains(t, aliasNames(persisted.Models), "claude-opus-4-6")
	require.Contains(t, aliasNames(persisted.Models), "auto")
	require.Contains(t, aliasNames(persisted.Models), "performance")
	require.Contains(t, aliasNames(persisted.Models), "lite")
	require.Contains(t, aliasNames(persisted.Models), "qwen3.7-max")
	require.NotContains(t, aliasNames(persisted.Models), "qmodel_latest")
	require.NotContains(t, aliasNames(persisted.Models), "newmodel")
	require.NotContains(t, aliasNames(persisted.Models), "gpt-5-codex")
}

func TestQoderModelSyncServiceLoadsPersistedAliases(t *testing.T) {
	resetQoderModelAliasesForTest()
	t.Cleanup(resetQoderModelAliasesForTest)

	dir := t.TempDir()
	persistPath := filepath.Join(dir, qoderModelAliasesFileName)
	require.NoError(t, os.WriteFile(persistPath, []byte(`{
		"version": 1,
		"models": [
			{"alias":"claude-opus-4-6","key":"ultimate","source":"system","provider":"Claude"},
			{"alias":"auto","key":"auto","source":"system","provider":"Qoder"},
			{"alias":"persisted","key":"persisted","source":"system","provider":"Persisted"}
		]
	}`), 0o644))

	svc := &QoderModelSyncService{persistPath: persistPath}
	require.NoError(t, svc.loadPersisted())

	info := resolveQoderModel("persisted")
	require.Equal(t, "persisted", info.Key)
	require.Empty(t, info.Provider)
	info = resolveQoderModel("claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)
	require.Equal(t, "Claude", info.Provider)
}

func aliasNames(records []QoderModelAliasRecord) []string {
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.Alias)
	}
	return names
}

func changeNames(records []QoderModelAliasChange) []string {
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.Alias)
	}
	return names
}
