package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

type freeQuotaBindingFixture struct {
	gate    *account.FreeQuotaGate
	factory func() *account.FreeQuotaGate
}

func (f *freeQuotaBindingFixture) BindFreeQuotaGate(gate *account.FreeQuotaGate) {
	f.gate = gate
}

func (f *freeQuotaBindingFixture) BindFreeQuotaGates(gate *account.FreeQuotaGate, factory func() *account.FreeQuotaGate) {
	f.gate, f.factory = gate, factory
}

type freeQuotaReaderFixture struct {
	usage.UsageLogRepository
	calls atomic.Int64
}

func (f *freeQuotaReaderFixture) GetAccountWindowStatsBatch(_ context.Context, _ []int64, _ time.Time) (map[int64]*usage.AccountStats, error) {
	f.calls.Add(1)
	return map[int64]*usage.AccountStats{7: {Tokens: 480_000}}, nil
}

// 验证原三种选择作用域没有合并，批量统计只在各自首次缺失时读取。
func TestFreeQuotaBindingPreservesCacheScopesAndTaskOwner(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Grok.FreeQuotaSoftGateEnabled = true
	cfg.Gateway.Grok.FreeQuotaTokenLimit = 500_000
	cfg.Gateway.Grok.FreeQuotaSoftGatePercent = 95
	cfg.Gateway.Grok.FreeQuotaWindowHours = 24
	cfg.Gateway.Grok.FreeQuotaStatsCacheSeconds = 60
	tasks := lifecycle.NewTasks()
	reader := &freeQuotaReaderFixture{}
	general, openai := &freeQuotaBindingFixture{}, &freeQuotaBindingFixture{}
	bindAccountFreeQuota(cfg, reader, tasks, general, openai)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, tasks.Stop(cleanup))
	})
	candidates := []account.FreeQuotaCandidate{{ID: 7, Eligible: true}}
	gates := []*account.FreeQuotaGate{general.gate, openai.gate, openai.factory(), openai.factory()}
	for _, gate := range gates {
		require.Empty(t, gate.Blocked(candidates), "首次缺失保持放行")
		require.NoError(t, tasks.Wait(ctx))
		require.True(t, gate.Blocked(candidates)[7])
	}
	require.Equal(t, int64(4), reader.calls.Load())
	// 已发布的缓存通过原任务拥有者关闭后仍可读取，缺失账号不再启动查询。
	require.NoError(t, tasks.Stop(ctx))
	require.Empty(t, general.gate.Blocked([]account.FreeQuotaCandidate{{ID: 8, Eligible: true}}))
	require.Equal(t, int64(4), reader.calls.Load())
}
