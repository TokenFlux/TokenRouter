package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 模拟最新身份读取后、执行健康写入前管理员替换凭据。
type s06CNDecisionRepo struct {
	*cnUsageMonitorRepo
	changed bool
}

func (r *s06CNDecisionRepo) changeIdentity(id int64) {
	if r.changed {
		return
	}
	r.changed = true
	r.accounts[id].UpdatedAt = r.accounts[id].UpdatedAt.Add(time.Second)
	r.accounts[id].Credentials = map[string]any{"api_key": "new-admin-key", "account_mode": AccountModePayG}
}
func (r *s06CNDecisionRepo) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.changeIdentity(id)
	return r.cnUsageMonitorRepo.SetTempUnschedulable(ctx, id, until, reason)
}
func (r *s06CNDecisionRepo) SetCNUsageDecisionCAS(ctx context.Context, id int64, expected time.Time, until time.Time, reason string, clear bool) (bool, error) {
	r.changeIdentity(id)
	if !r.accounts[id].UpdatedAt.Equal(expected) {
		return false, nil
	}
	if clear {
		return true, r.ClearTempUnschedulable(ctx, id)
	}
	return true, r.cnUsageMonitorRepo.SetTempUnschedulable(ctx, id, until, reason)
}
func TestS06CNMonitorOldIdentityCannotPauseNewCredentials(t *testing.T) {
	value := newCNUsageMonitorAccount(1, PlatformKimi, AccountModePayG)
	repo := &s06CNDecisionRepo{cnUsageMonitorRepo: &cnUsageMonitorRepo{accounts: map[int64]*Account{1: value}, byPlatform: map[string][]int64{PlatformKimi: {1}}, casResult: true}}
	upstream := &cnUsageMonitorHTTP{body: `{"code":0,"data":{"available_balance":0.1}}`}
	cfg := testUpstreamUsageConfig()
	cfg.Gateway.CNProviders.BalanceThreshold = 0.5
	usage := NewUpstreamUsageService(repo, upstream, cfg, nil)
	monitor := newCNMonitorLegacyFixture(repo, usage, cfg)
	monitor.RunOnce(context.Background())
	require.True(t, repo.changed)
	require.Empty(t, repo.pauseReason, "旧查询健康结论不得写到管理员替换后的身份")
	require.Nil(t, value.TempUnschedulableUntil)
}
