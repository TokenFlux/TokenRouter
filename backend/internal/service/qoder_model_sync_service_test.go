package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQoderModelSyncServicePreviewShowsDefaultAliasDiffWithoutApplying(t *testing.T) {
	resetQoderModelAliasesForTest()
	t.Cleanup(resetQoderModelAliasesForTest)

	svc := &QoderModelSyncService{
		scriptPath:  "/tmp/sync_models.py",
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
	require.Contains(t, aliasNames(result.Added), "newmodel")
	require.Contains(t, aliasNames(result.Removed), "ultimate")
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
		scriptPath:  "/tmp/sync_models.py",
		persistPath: persistPath,
		runner: func(_ context.Context, source string) ([]byte, error) {
			require.Equal(t, "cli", source)
			return []byte(`[
				{"key":"ultimate","provider":"Claude","notes":"synced","display_name":"Claude Opus 4.6","description":"Confirmed"},
				{"key":"auto","provider":"Qoder","notes":"synced","display_name":"Qoder Auto","description":"Backend selected"},
				{"key":"newmodel","provider":"New","notes":"new","display_name":"New Model","description":"New desc"}
			]`), nil
		},
	}

	result, err := svc.SyncModels(context.Background(), QoderModelSyncInput{Source: "cli", Apply: true})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Contains(t, aliasNames(result.Preserved), "claude-opus-4-6")

	info := resolveQoderModel("newmodel")
	require.Equal(t, "newmodel", info.Key)
	require.Equal(t, "New", info.Provider)
	info = resolveQoderModel("gpt-5-codex")
	require.Equal(t, "gpt-5-codex", info.Key)
	info = resolveQoderModel("claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)

	body, err := os.ReadFile(persistPath)
	require.NoError(t, err)
	var persisted qoderModelAliasesPersisted
	require.NoError(t, json.Unmarshal(body, &persisted))
	require.Contains(t, aliasNames(persisted.Models), "newmodel")
	require.Contains(t, aliasNames(persisted.Models), "claude-opus-4-6")
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
			{"alias":"persisted","key":"persisted","source":"system","provider":"Persisted"}
		]
	}`), 0o644))

	svc := &QoderModelSyncService{persistPath: persistPath}
	require.NoError(t, svc.loadPersisted())

	info := resolveQoderModel("persisted")
	require.Equal(t, "persisted", info.Key)
	require.Equal(t, "Persisted", info.Provider)
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
