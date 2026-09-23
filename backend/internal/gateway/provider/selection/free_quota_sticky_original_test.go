//go:build unit

package selection

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestGetSchedulableAccount_AppliesGrokFreeSoftGate(t *testing.T) {
	// 缓存预热后，粘性或非列表路径不得返回超过门禁的免费 OAuth 账号。
	// 首次粘性命中失败开放并安排异步刷新，后续命中使用缓存。
	cfg := &config.Config{}
	cfg.Gateway.Grok.FreeQuotaSoftGateEnabled = true
	cfg.Gateway.Grok.FreeQuotaTokenLimit = 500_000
	cfg.Gateway.Grok.FreeQuotaSoftGatePercent = 95
	cfg.Gateway.Grok.FreeQuotaWindowHours = 24
	cfg.Gateway.Grok.FreeQuotaStatsCacheSeconds = 60

	account := healthyGrokOAuthGatewayTestAccount(8801, "tok")
	account.Record.Credentials["subscription_tier"] = "free"
	account.Record.Status = billing.StatusActive
	account.Record.Schedulable = true

	repo := &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}
	usageRepo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usage.AccountStats{
		account.Record.ID: {Tokens: 480_000}, // above 95% of 500k
	}}
	// 清理共享网关免费层门禁缓存，保证测试结果稳定。
	var tasks sync.WaitGroup
	t.Cleanup(tasks.Wait)
	gate := newGrokFreeQuotaTestGate(cfg, usageRepo, func(_ string, work func()) bool {
		tasks.Add(1)
		go func() { defer tasks.Done(); work() }()
		return true
	})
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, FreeQuota: gate}, cfg)

	got, err := svc.getSchedulableAccount(context.Background(), account.Record.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "first sticky hit fail-opens while free-gate stats refresh")

	require.Eventually(t, func() bool {
		got, err := svc.getSchedulableAccount(context.Background(), account.Record.ID)
		return err == nil && got == nil
	}, 2*time.Second, 10*time.Millisecond, "over free soft-gate sticky hit must miss after cache warm")
}

type grokFreeQuotaUsageRepoStub struct {
	usage.UsageLogRepository

	mu      sync.Mutex
	stats   map[int64]*usage.AccountStats
	err     error
	calls   int
	lastIDs []int64
	start   time.Time
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

func newGrokFreeQuotaTestGate(cfg *config.Config, reader usage.UsageLogRepository, background func(string, func()) bool) *account.FreeQuotaGate {
	return account.NewFreeQuotaGate(func() account.FreeQuotaOptions {
		v := cfg.Gateway.Grok
		return account.FreeQuotaOptions{Enabled: v.FreeQuotaSoftGateEnabled, TokenLimit: v.FreeQuotaTokenLimit, Percent: v.FreeQuotaSoftGatePercent, WindowHours: v.FreeQuotaWindowHours, CacheSeconds: v.FreeQuotaStatsCacheSeconds}
	}, func(ctx context.Context, ids []int64, start time.Time) (map[int64]int64, error) {
		return usage.ReadAccountTokenWindow(ctx, reader, ids, start)
	}, background, time.Now, nil, nil)
}

func healthyGrokOAuthGatewayTestAccount(id int64, token string) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: account.Record{LoadLocation: time.LoadLocation, ID: id,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  token,
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(2 * account.GrokTokenRefreshSkew).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		}},
	}
}

func TestOpenAIAccountSchedulerLoadBalanceAppliesGrokFreeQuotaGate(t *testing.T) {
	cfg := grokFreeQuotaTestConfig()
	cfg.RunMode = config.RunModeSimple
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: account.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"subscription_tier": "free"}}},
		{Record: account.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"subscription_tier": "pro"}}},
	}
	reader := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usage.AccountStats{1: {Tokens: 480_000}}}
	factory := freeQuotaFactoryForTest(t, cfg, reader)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:                Reads{Accounts: selectionAccountFixture{accounts: accounts}},
		FreeQuota:            factory(),
		NewAdvancedFreeQuota: factory,
	}, cfg)
	picker := &compatiblePicker{service: svc, stats: scheduler.NewRuntimeStats(time.Now)}

	// 通过后台刷新预热缓存，使负载均衡路径能够看到软性门禁结果。
	_ = picker.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Eventually(t, func() bool {
		filtered := picker.filterGrokFreeQuotaAccounts(context.Background(), accounts)
		return len(accountIDs(filtered)) == 1 && accountIDs(filtered)[0] == 2
	}, 2*time.Second, 10*time.Millisecond)

	core, scope := picker.platformSelector()
	result, _, _, _, err := core.SelectByLoadBalance(context.Background(), scheduler.PlatformSelectionInput{Platform: capability.PlatformGrok})
	selection := scope.restore(result)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.Record.ID)
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

func accountIDs(accounts []gatewayprovider.ExecutionAccount) []int64 {
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].Record.ID)
	}
	return ids
}

// 仅为选择入口装配真实门禁，统计和阈值算法不在测试中重建。
func TestOpenAIGetSchedulableAccount_AppliesGrokFreeSoftGate(t *testing.T) {
	// 关闭高级调度器时，旧版 OpenAI 兼容粘性路径仍必须对 Grok 执行免费层门禁。
	cfg := &config.Config{}
	cfg.Gateway.Grok.FreeQuotaSoftGateEnabled = true
	cfg.Gateway.Grok.FreeQuotaTokenLimit = 500_000
	cfg.Gateway.Grok.FreeQuotaSoftGatePercent = 95
	cfg.Gateway.Grok.FreeQuotaWindowHours = 24
	cfg.Gateway.Grok.FreeQuotaStatsCacheSeconds = 60

	account := healthyGrokOAuthGatewayTestAccount(8802, "tok")
	account.Record.Credentials["subscription_tier"] = "free"
	account.Record.Status = billing.StatusActive
	account.Record.Schedulable = true

	repo := &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}
	usageRepo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usage.AccountStats{
		account.Record.ID: {Tokens: 480_000},
	}}
	factory := freeQuotaFactoryForTest(t, cfg, usageRepo)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:                Reads{Accounts: repo},
		FreeQuota:            factory(),
		NewAdvancedFreeQuota: factory,
	}, cfg)

	got, err := svc.getSchedulableAccount(context.Background(), account.Record.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "first sticky hit fail-opens while free-gate stats refresh")

	require.Eventually(t, func() bool {
		got, err := svc.getSchedulableAccount(context.Background(), account.Record.ID)
		return err == nil && got == nil
	}, 2*time.Second, 10*time.Millisecond, "OpenAI legacy sticky must apply free soft-gate after cache warm")
}

// freeQuotaFactoryForTest 保留各池独立缓存，测试结束等待已接受的刷新。
func freeQuotaFactoryForTest(t *testing.T, cfg *config.Config, source usage.UsageLogRepository) func() *account.FreeQuotaGate {
	var tasks sync.WaitGroup
	t.Cleanup(tasks.Wait)
	background := func(_ string, work func()) bool {
		tasks.Add(1)
		go func() { defer tasks.Done(); work() }()
		return true
	}
	return func() *account.FreeQuotaGate { return newGrokFreeQuotaTestGate(cfg, source, background) }
}
