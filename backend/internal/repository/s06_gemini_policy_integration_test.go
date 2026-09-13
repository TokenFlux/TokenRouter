//go:build integration

package repository

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 真实 settings 表验证 TTL、生效顺序与损坏 JSON 回退，不修改生产设置。
func TestS06GeminiQuotaPolicyLoadsSettingsAndKeepsSnapshot(t *testing.T) {
	ctx := context.Background()
	repo := NewSettingRepository(testEntClient(t))
	key := "s06.gemini.quota.policy"
	require.NoError(t, repo.Delete(ctx, key))
	t.Cleanup(func() { require.NoError(t, repo.Delete(ctx, key)) })
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	core := account.NewGeminiQuotaService(account.GeminiQuotaOptions{Now: func() time.Time { return now }, NotFound: settings.ErrSettingNotFound, StaticPolicy: `{"tiers":{"aistudio_free":{"pro_rpd":10}}}`, LoadPolicy: func(ctx context.Context) (string, error) { return repo.GetValue(ctx, key) }})
	before, _ := core.Policy(ctx).QuotaForTier("aistudio_free")
	require.Equal(t, int64(10), before.ProRPD)
	require.NoError(t, repo.Set(ctx, key, `{"quota_rules":{"aistudio_free":{"gemini_pro":{"rpd":25}}}}`))
	cached, _ := core.Policy(ctx).QuotaForTier("aistudio_free")
	require.Equal(t, int64(10), cached.ProRPD)
	now = now.Add(time.Minute)
	policy := core.Policy(ctx)
	fresh, _ := policy.QuotaForTier("aistudio_free")
	require.Equal(t, int64(25), fresh.ProRPD)
	rpd := int64(999)
	policy.ApplyOverrides(map[string]account.GeminiTierQuotaOverride{"aistudio_free": {ProRPD: &rpd}})
	still, _ := core.Policy(ctx).QuotaForTier("aistudio_free")
	require.Equal(t, int64(25), still.ProRPD)
	require.NoError(t, repo.Set(ctx, key, "not-json"))
	now = now.Add(time.Minute)
	fallback, _ := core.Policy(ctx).QuotaForTier("aistudio_free")
	require.Equal(t, int64(10), fallback.ProRPD)
}
