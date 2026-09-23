//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

type grokFreeQuotaUsageRepoStub struct {
	usage.UsageLogRepository

	mu      sync.Mutex
	stats   map[int64]*usage.AccountStats
	err     error
	calls   int
	lastIDs []int64
	start   time.Time
}

type grokFreeQuotaAccountRepoStub struct {
	gatewayprovider.ExecutionAccountStore

	accounts []gatewayprovider.ExecutionAccount
}

func (r *grokFreeQuotaAccountRepoStub) ListSchedulableByPlatform(context.Context, string) ([]gatewayprovider.ExecutionAccount, error) {
	return append([]gatewayprovider.ExecutionAccount(nil), r.accounts...), nil
}

func (r *grokFreeQuotaUsageRepoStub) GetAccountWindowStatsBatch(_ context.Context, accountIDs []int64, start time.Time) (map[int64]*usage.AccountStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastIDs = append([]int64(nil), accountIDs...)
	r.start = start
	if r.err != nil {
		return nil, r.err
	}
	result := make(map[int64]*usage.AccountStats, len(accountIDs))
	for _, accountID := range accountIDs {
		if stats := r.stats[accountID]; stats != nil {
			copyStats := *stats
			result[accountID] = &copyStats
		}
	}
	return result, nil
}

func grokFreeQuotaTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.Grok.FreeQuotaSoftGateEnabled = true
	cfg.Gateway.Grok.FreeQuotaTokenLimit = 500_000
	cfg.Gateway.Grok.FreeQuotaSoftGatePercent = 95
	cfg.Gateway.Grok.FreeQuotaWindowHours = 24
	cfg.Gateway.Grok.FreeQuotaStatsCacheSeconds = 60
	return cfg
}

func TestOpenAIAccountSchedulerLoadBalanceAppliesGrokFreeQuotaGate(t *testing.T) {
	cfg := grokFreeQuotaTestConfig()
	cfg.RunMode = config.RunModeSimple
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: account.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"subscription_tier": "free"}}},
		{Record: account.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"subscription_tier": "pro"}}},
	}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:         cfg,
		accountRepo: &grokFreeQuotaAccountRepoStub{accounts: accounts},
		usageLogRepo: &grokFreeQuotaUsageRepoStub{stats: map[int64]*usage.AccountStats{
			1: {Tokens: 480_000}, // over 95% of 500k
		}},
	}))
	bindGrokFreeQuotaTestService(svc)
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: scheduler.NewRuntimeStats(time.Now)}

	// 通过后台刷新预热缓存，使负载均衡路径能够看到软性门禁结果。
	_ = scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Eventually(t, func() bool {
		filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
		return len(accountIDs(filtered)) == 1 && accountIDs(filtered)[0] == 2
	}, 2*time.Second, 10*time.Millisecond)

	selection, _, _, _, err := scheduler.selectByLoadBalance(context.Background(), OpenAIAccountScheduleRequest{Platform: capability.PlatformGrok})
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.Record.ID)
}

func accountIDs(accounts []gatewayprovider.ExecutionAccount) []int64 {
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].Record.ID)
	}
	return ids
}

// 仅为选择入口装配真实门禁，统计和阈值算法不在测试中重建。
func bindGrokFreeQuotaTestService(svc *OpenAIGatewayService) {
	factory := func() *account.FreeQuotaGate {
		return newGrokFreeQuotaTestGate(svc.cfg, svc.usageLogRepo, svc.RunBackgroundTask)
	}
	svc.BindFreeQuotaGates(factory(), factory)
}
func newGrokFreeQuotaTestGate(cfg *config.Config, reader usage.UsageLogRepository, background func(string, func()) bool) *account.FreeQuotaGate {
	return account.NewFreeQuotaGate(func() account.FreeQuotaOptions {
		v := cfg.Gateway.Grok
		return account.FreeQuotaOptions{Enabled: v.FreeQuotaSoftGateEnabled, TokenLimit: v.FreeQuotaTokenLimit, Percent: v.FreeQuotaSoftGatePercent, WindowHours: v.FreeQuotaWindowHours, CacheSeconds: v.FreeQuotaStatsCacheSeconds}
	}, func(ctx context.Context, ids []int64, start time.Time) (map[int64]int64, error) {
		return usage.ReadAccountTokenWindow(ctx, reader, ids, start)
	}, background, time.Now, nil, nil)
}
