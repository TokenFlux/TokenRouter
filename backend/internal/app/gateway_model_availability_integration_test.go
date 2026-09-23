//go:build integration

package app_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 诊断必须直接读持久配置；瞬时冷却不把已配置模型误报为不存在。
func TestS16ModelAvailabilityUsesPersistentAccountStore(t *testing.T) {
	f := newDatabaseFixture(t)
	ctx := t.Context()
	group, err := f.client.Group.Create().SetName("s16-diagnostic-group").SetPlatform(capability.PlatformOpenAI).SetAllowedProtocols([]protocol.ProtocolID{protocol.ProtocolOpenAIResponses}).Save(ctx)
	require.NoError(t, err)
	cooldown := time.Now().Add(time.Hour)
	row, err := f.client.Account.Create().SetName("s16-diagnostic-account").SetPlatform(capability.PlatformOpenAI).SetType(capability.AccountTypeAPIKey).SetCredentials(map[string]any{"model_mapping": map[string]any{"public-known": "public-known"}}).SetRateLimitResetAt(cooldown).SetOverloadUntil(cooldown).SetTempUnschedulableUntil(cooldown).Save(ctx)
	require.NoError(t, err)
	_, err = f.client.AccountGroup.Create().SetAccountID(row.ID).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	store := app.NewS16AccountStore(f.client, f.db, nil)
	standard := app.S16ModelAvailability(store, nil, &config.Config{RunMode: config.RunModeStandard})
	simple := app.S16ModelAvailability(store, nil, &config.Config{RunMode: config.RunModeSimple})
	t.Run("configured_cooling_account", func(t *testing.T) {
		result := standard.Compatible.DiagnoseModelAvailabilityForPlatform(ctx, &group.ID, "public-known", capability.PlatformOpenAI)
		require.True(t, result.HasAccountsInPool)
		require.True(t, result.HasModelSupport)
	})
	t.Run("missing_model_with_existing_pool", func(t *testing.T) {
		result := standard.Resolved.DiagnoseModelAvailabilityForPlatform(ctx, &group.ID, "not-configured", capability.PlatformOpenAI)
		require.True(t, result.HasAccountsInPool)
		require.False(t, result.HasModelSupport)
	})
	t.Run("standard_ungrouped_scope", func(t *testing.T) {
		result := standard.Compatible.DiagnoseModelAvailabilityForPlatform(ctx, nil, "public-known", capability.PlatformOpenAI)
		require.False(t, result.HasAccountsInPool)
		require.False(t, result.HasModelSupport)
	})
	t.Run("simple_includes_grouped", func(t *testing.T) {
		result := simple.Compatible.DiagnoseModelAvailabilityForPlatform(ctx, nil, "public-known", capability.PlatformOpenAI)
		require.True(t, result.HasAccountsInPool)
		require.True(t, result.HasModelSupport)
	})
	// 管理禁用属于持久配置，仍应退出诊断池；不运行健康写入或选号。
	require.NoError(t, f.client.Account.UpdateOneID(row.ID).SetStatus(account.StatusDisabled).Exec(ctx))
	result := standard.Compatible.DiagnoseModelAvailabilityForPlatform(ctx, &group.ID, "public-known", capability.PlatformOpenAI)
	require.False(t, result.HasAccountsInPool)
}
