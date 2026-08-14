//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func advancedSchedulerRegressionBool(value bool) *bool        { return &value }
func advancedSchedulerRegressionInt(value int) *int           { return &value }
func advancedSchedulerRegressionFloat(value float64) *float64 { return &value }

func advancedSchedulerRegressionOverrides() GroupAdvancedSchedulerOverrides {
	return GroupAdvancedSchedulerOverrides{
		StickyWeightedEnabled:  advancedSchedulerRegressionBool(true),
		LBTopK:                 advancedSchedulerRegressionInt(1),
		WeightPriority:         advancedSchedulerRegressionFloat(0),
		WeightLoad:             advancedSchedulerRegressionFloat(0),
		WeightQueue:            advancedSchedulerRegressionFloat(0),
		WeightErrorRate:        advancedSchedulerRegressionFloat(0),
		WeightTTFT:             advancedSchedulerRegressionFloat(0),
		WeightReset:            advancedSchedulerRegressionFloat(0),
		WeightQuotaHeadroom:    advancedSchedulerRegressionFloat(0),
		WeightPreviousResponse: advancedSchedulerRegressionFloat(0),
		WeightSessionSticky:    advancedSchedulerRegressionFloat(0),
	}
}

func advancedSchedulerRegressionGroup(id int64, platform string, overrides GroupAdvancedSchedulerOverrides) *Group {
	return &Group{
		ID: id, Name: "advanced", Platform: platform, Status: StatusActive, Hydrated: true,
		SchedulerType: GroupSchedulerTypeAdvanced, AdvancedSchedulerOverrides: overrides,
	}
}

func advancedSchedulerRegressionAccountRepo(accounts []Account) *mockAccountRepoForPlatform {
	repo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: make(map[int64]*Account, len(accounts))}
	for index := range repo.accounts {
		repo.accountsByID[repo.accounts[index].ID] = &repo.accounts[index]
	}
	return repo
}

func TestGatewayAdvancedSchedulerKeepsFullLoadCandidatesForWaitAndNoSlotSelection(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.WeightPriority = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1301, PlatformAnthropic, overrides)
	accounts := []Account{
		{ID: 13011, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 1},
		{ID: 13012, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 2},
	}
	repo := advancedSchedulerRegressionAccountRepo(accounts)
	concurrencyCache := &mockConcurrencyCache{
		acquireResults: map[int64]bool{13011: false, 13012: false},
		loadMap: map[int64]*AccountLoadInfo{
			13011: {AccountID: 13011, LoadRate: 100},
			13012: {AccountID: 13012, LoadRate: 100},
		},
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{group.ID: group}},
		cache: &mockGatewayCacheForPlatform{}, cfg: cfg, concurrencyService: NewConcurrencyService(concurrencyCache),
	}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "claude-sonnet-4", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(13011), selection.Account.ID)
	require.True(t, selection.AdvancedScheduler)
	acquireCalls := concurrencyCache.acquireAccountCalls

	account, err := svc.SelectAccountForModelWithExclusions(ctx, &group.ID, "", "claude-sonnet-4", nil)

	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(13011), account.ID)
	require.Equal(t, acquireCalls, concurrencyCache.acquireAccountCalls, "无槽选择不得申请真实并发槽")
}

func TestGatewayAdvancedSchedulerForcePlatformUsesGroupOverrides(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.WeightLoad = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1401, PlatformAnthropic, overrides)
	accounts := []Account{
		{ID: 14011, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 1, Extra: map[string]any{"mixed_scheduling": true}},
		{ID: 14012, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 100, Extra: map[string]any{"mixed_scheduling": true}},
	}
	repo := advancedSchedulerRegressionAccountRepo(accounts)
	concurrencyCache := &mockConcurrencyCache{
		acquireResults: map[int64]bool{14011: true, 14012: true},
		loadMap: map[int64]*AccountLoadInfo{
			14011: {AccountID: 14011, LoadRate: 90},
			14012: {AccountID: 14012, LoadRate: 0},
		},
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{group.ID: group}},
		cache: &mockGatewayCacheForPlatform{}, cfg: cfg, concurrencyService: NewConcurrencyService(concurrencyCache),
	}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, PlatformAntigravity)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "force", "gemini-2.5-pro", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(14012), selection.Account.ID)
	require.True(t, selection.AdvancedScheduler)
	svc.ReportAdvancedAccountScheduleResult(selection, selection.Account.ID, false, nil)
	require.EqualValues(t, 1, svc.advancedSchedulerStats().feedbackSnapshot(14012).ErrorSamples)
	require.Zero(t, svc.advancedSchedulerStats().feedbackSnapshot(14011).ErrorSamples)
}

func TestGatewayAdvancedSchedulerWeightedStickyKeepsStickyOnlyAccount(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.WeightSessionSticky = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1501, PlatformAnthropic, overrides)
	accounts := []Account{
		{
			ID: 15011, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive,
			Schedulable: true, Concurrency: 2, Extra: map[string]any{"window_cost_limit": 10.0, "window_cost_sticky_reserve": 5.0},
		},
		{ID: 15012, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 2},
	}
	repo := advancedSchedulerRegressionAccountRepo(accounts)
	cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": 15011}}
	windowCache := &sessionLimitCacheHotpathStub{batchData: map[int64]float64{15011: 11}}
	concurrencyCache := &mockConcurrencyCache{acquireResults: map[int64]bool{15011: true, 15012: true}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{group.ID: group}},
		cache: cache, cfg: cfg, concurrencyService: NewConcurrencyService(concurrencyCache),
		sessionLimitCache: windowCache, usageLogRepo: &usageLogWindowBatchRepoStub{},
	}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "sticky", "claude-sonnet-4", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(15011), selection.Account.ID)
	require.True(t, selection.AdvancedScheduler)
}

func TestGatewayAdvancedSchedulerEscapesNonOpenAIHardSticky(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.StickyWeightedEnabled = advancedSchedulerRegressionBool(false)
	overrides.WeightErrorRate = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1601, PlatformGemini, overrides)
	accounts := []Account{
		{ID: 16011, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 1},
		{ID: 16012, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: 2},
	}
	repo := advancedSchedulerRegressionAccountRepo(accounts)
	cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": 16011}}
	concurrencyCache := &mockConcurrencyCache{acquireResults: map[int64]bool{16011: true, 16012: true}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.AdvancedScheduler.StickyEscapeEnabled = true
	cfg.Gateway.AdvancedScheduler.StickyEscapeTTFTMs = 15000
	cfg.Gateway.AdvancedScheduler.StickyEscapeErrorRate = 0.55
	svc := &GatewayService{
		accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{group.ID: group}},
		cache: cache, cfg: cfg, concurrencyService: NewConcurrencyService(concurrencyCache),
		advancedAccountStats: newAdvancedAccountRuntimeStats(),
	}
	for range 4 {
		svc.advancedSchedulerStats().report(16011, false, nil)
	}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "sticky", "gemini-3-pro", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(16012), selection.Account.ID)
	require.Equal(t, int64(16011), cache.sessionBindings["sticky"], "逃逸时保留原粘性绑定")
	require.True(t, selection.AdvancedScheduler)
}
