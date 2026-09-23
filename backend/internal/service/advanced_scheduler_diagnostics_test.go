package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"
)

type advancedSchedulerDiagnosticSourceStub struct {
	account  *gatewayprovider.ExecutionAccount
	group    *routing.Group
	accounts []gatewayprovider.ExecutionAccount
	pool     []gatewayprovider.ExecutionAccount
}

type advancedSchedulerDiagnosticConcurrencyCache struct {
	scheduler.ConcurrencyCache
	requests [][]scheduler.AccountWithConcurrency
}

func (c *advancedSchedulerDiagnosticConcurrencyCache) GetAccountsLoadBatch(_ context.Context, accounts []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	c.requests = append(c.requests, append([]scheduler.AccountWithConcurrency(nil), accounts...))
	result := make(map[int64]*scheduler.AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		result[account.ID] = &scheduler.AccountLoadInfo{AccountID: account.ID}
	}
	return result, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) GetAccount(_ context.Context, _ int64) (*gatewayprovider.ExecutionAccount, error) {
	return s.account, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) GetGroup(_ context.Context, _ int64) (*routing.Group, error) {
	return s.group, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) ListAccountsForSchedulerScoreFilter(_ context.Context, _, _, _, _ string, _ int64, _ string) ([]gatewayprovider.ExecutionAccount, error) {
	return s.accounts, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) ListSchedulableAccountsForAdvancedSchedulerScore(_ context.Context, _ *int64, _ string) ([]gatewayprovider.ExecutionAccount, error) {
	return s.pool, nil
}

func advancedSchedulerDiagnosticBool(value bool) *bool {
	return &value
}

func advancedSchedulerDiagnosticInt(value int) *int {
	return &value
}

func advancedSchedulerDiagnosticFloat(value float64) *float64 {
	return &value
}

func findAdvancedSchedulerDiagnosticMetric(metrics []policy.AdvancedSchedulerScoreDiagnosticMetric, key string) *policy.AdvancedSchedulerScoreDiagnosticMetric {
	for index := range metrics {
		if metrics[index].Key == key {
			return &metrics[index]
		}
	}
	return nil
}

func findAdvancedSchedulerDiagnosticSetting(settings []policy.AdvancedSchedulerScoreDiagnosticSetting, key string) *policy.AdvancedSchedulerScoreDiagnosticSetting {
	for index := range settings {
		if settings[index].Key == key {
			return &settings[index]
		}
	}
	return nil
}

func findAdvancedSchedulerDiagnosticPolicy(signals []policy.AdvancedSchedulerScoreDiagnosticPolicySignal, key string) *policy.AdvancedSchedulerScoreDiagnosticPolicySignal {
	for index := range signals {
		if signals[index].Key == key {
			return &signals[index]
		}
	}
	return nil
}

func TestAdvancedSchedulerScoreDiagnosticService_UsesActualFormulaAndSafeDTO(t *testing.T) {
	group := &routing.Group{
		ID:            301,
		Name:          "advanced",
		Platform:      capability.PlatformGemini,
		SchedulerType: routing.GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled:  advancedSchedulerDiagnosticBool(true),
			LBTopK:                 advancedSchedulerDiagnosticInt(2),
			WeightPriority:         advancedSchedulerDiagnosticFloat(2),
			WeightLoad:             advancedSchedulerDiagnosticFloat(1),
			WeightQueue:            advancedSchedulerDiagnosticFloat(0),
			WeightErrorRate:        advancedSchedulerDiagnosticFloat(0),
			WeightTTFT:             advancedSchedulerDiagnosticFloat(0),
			WeightReset:            advancedSchedulerDiagnosticFloat(0),
			WeightQuotaHeadroom:    advancedSchedulerDiagnosticFloat(0),
			WeightPreviousResponse: advancedSchedulerDiagnosticFloat(0),
			WeightSessionSticky:    advancedSchedulerDiagnosticFloat(3),
		},
	}
	target := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 101,
		Name:          "target",
		Platform:      capability.PlatformGemini,
		Type:          capability.AccountTypeOAuth,
		Status:        billing.StatusActive,
		Schedulable:   true,
		Priority:      1,
		Credentials:   map[string]any{"access_token": "secret-token"},
		AccountGroups: []accountcore.GroupMembership{{GroupID: group.ID, Group: (*accessview.GroupConfig)(group)}}},
	}
	other := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 102,
		Name:        "other",
		Platform:    capability.PlatformGemini,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Priority:    2},
	}
	source := &advancedSchedulerDiagnosticSourceStub{
		account:  target,
		group:    group,
		accounts: []gatewayprovider.ExecutionAccount{*target, other},
		pool:     []gatewayprovider.ExecutionAccount{*target, other},
	}

	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(source, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{
		GroupID:         group.ID,
		StickyAccountID: target.Record.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, result.Detail)
	require.True(t, result.Detail.Eligible)
	require.NotNil(t, result.Detail.Score)
	require.InDelta(t, 2.5, result.Detail.Score.BaseScore, 0.000001)
	require.InDelta(t, 3, result.Detail.Score.StickyBonus, 0.000001)
	require.InDelta(t, 5.5, result.Detail.Score.FinalScore, 0.000001)
	require.Equal(t, "top_k_weighted", result.Detail.Score.SelectionMode)
	require.NotNil(t, result.Detail.Score.SelectionWeight)
	require.InDelta(t, 6, *result.Detail.Score.SelectionWeight, 0.000001)
	require.NotNil(t, result.Detail.Score.SelectionProbability)
	require.InDelta(t, 6.0/7.0, *result.Detail.Score.SelectionProbability, 0.000001)
	require.Contains(t, result.Detail.Score.Formula, "2.0000×1.0000")

	loadMetric := findAdvancedSchedulerDiagnosticMetric(result.Detail.Metrics, "load")
	require.NotNil(t, loadMetric)
	require.True(t, loadMetric.Neutral)
	require.False(t, loadMetric.Available)
	require.InDelta(t, 0.5, loadMetric.NormalizedValue, 0.000001)

	errorMetric := findAdvancedSchedulerDiagnosticMetric(result.Detail.Metrics, "error_rate")
	require.NotNil(t, errorMetric)
	require.True(t, errorMetric.Neutral)
	require.Equal(t, "0%（未观测）", errorMetric.RawValue)
	require.InDelta(t, 1.0, errorMetric.NormalizedValue, 0.000001)
	require.Contains(t, errorMetric.Normalization, "错误率按 0% 计算")

	prioritySetting := findAdvancedSchedulerDiagnosticSetting(result.Detail.EffectiveSettings, "weight_priority")
	require.NotNil(t, prioritySetting)
	require.Equal(t, "group_override", prioritySetting.Source)
	require.Equal(t, "2.0000", prioritySetting.Value)

	payload, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(payload), "credentials")
	require.NotContains(t, string(payload), "secret-token")
	require.NotContains(t, strings.ToLower(string(payload)), "access_token")
}

func TestAdvancedSchedulerScoreDiagnosticService_UsesProcessConfigBeforeFallback(t *testing.T) {
	group := &routing.Group{ID: 501, Name: "advanced", Platform: capability.PlatformGemini, SchedulerType: routing.GroupSchedulerTypeAdvanced}
	target := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5011,
		Name:          "target",
		Platform:      capability.PlatformGemini,
		Status:        billing.StatusActive,
		Schedulable:   true,
		Priority:      1,
		AccountGroups: []accountcore.GroupMembership{{GroupID: group.ID, Group: (*accessview.GroupConfig)(group)}}},
	}
	other := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5012, Name: "other", Platform: capability.PlatformGemini, Status: billing.StatusActive, Schedulable: true, Priority: 2}}
	source := &advancedSchedulerDiagnosticSourceStub{
		account:  target,
		group:    group,
		accounts: []gatewayprovider.ExecutionAccount{*target, other},
		pool:     []gatewayprovider.ExecutionAccount{*target, other},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{AdvancedScheduler: config.GatewayAdvancedSchedulerConfig{
			LBTopK: 1,
			ScoreWeights: config.GatewayAdvancedSchedulerScoreWeights{
				Priority: 4,
			},
		}},
	}

	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(source, nil), cfg)

	result, err := diagnostics.GetDetail(context.Background(), target.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})
	require.NoError(t, err)
	require.NotNil(t, result.Detail)
	require.NotNil(t, result.Detail.Score)
	require.InDelta(t, 4, result.Detail.Score.BaseScore, 0.000001)
	require.Equal(t, 1, result.Detail.CandidatePool.TopK)
	prioritySetting := findAdvancedSchedulerDiagnosticSetting(result.Detail.EffectiveSettings, "weight_priority")
	require.NotNil(t, prioritySetting)
	require.Equal(t, "process_default", prioritySetting.Source)
}

func TestAdvancedSchedulerScoreDiagnosticService_HardStickyForcesAccountOutsideTopK(t *testing.T) {
	group := &routing.Group{
		ID:            701,
		Name:          "advanced",
		Platform:      capability.PlatformGemini,
		SchedulerType: routing.GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled: advancedSchedulerDiagnosticBool(false),
			LBTopK:                advancedSchedulerDiagnosticInt(1),
			WeightPriority:        advancedSchedulerDiagnosticFloat(1),
			WeightLoad:            advancedSchedulerDiagnosticFloat(0),
			WeightQueue:           advancedSchedulerDiagnosticFloat(0),
			WeightErrorRate:       advancedSchedulerDiagnosticFloat(0),
			WeightTTFT:            advancedSchedulerDiagnosticFloat(0),
			WeightReset:           advancedSchedulerDiagnosticFloat(0),
			WeightQuotaHeadroom:   advancedSchedulerDiagnosticFloat(0),
		},
	}
	target := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7011, Name: "sticky", Platform: capability.PlatformGemini, Status: billing.StatusActive,
		Schedulable: true, Priority: 100, AccountGroups: []accountcore.GroupMembership{{GroupID: group.ID, Group: (*accessview.GroupConfig)(group)}}},
	}
	best := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7012, Name: "best", Platform: capability.PlatformGemini, Status: billing.StatusActive, Schedulable: true, Priority: 1}}
	source := &advancedSchedulerDiagnosticSourceStub{
		account: target, group: group, accounts: []gatewayprovider.ExecutionAccount{*target, best}, pool: []gatewayprovider.ExecutionAccount{best, *target},
	}
	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(source, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{
		GroupID: group.ID, StickyAccountID: target.Record.ID,
	})

	require.NoError(t, err)
	require.True(t, result.Detail.Eligible)
	require.NotNil(t, result.Detail.Score)
	require.False(t, result.Detail.Score.InTopK)
	require.Equal(t, "sticky_forced_first", result.Detail.Score.SelectionMode)
	require.NotNil(t, result.Detail.Score.SelectionProbability)
	require.Equal(t, 1.0, *result.Detail.Score.SelectionProbability)
	signal := findAdvancedSchedulerDiagnosticPolicy(result.Detail.PolicySignals, "session_sticky")
	require.NotNil(t, signal)
	require.Equal(t, "forced_first", signal.State)
}

func TestAdvancedSchedulerScoreDiagnosticService_SubscriptionPriorityUsesSubscriptionPool(t *testing.T) {
	group := &routing.Group{
		ID:            801,
		Name:          "advanced",
		Platform:      capability.PlatformOpenAI,
		SchedulerType: routing.GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
			SubscriptionPriorityEnabled: advancedSchedulerDiagnosticBool(true),
		},
	}
	target := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8011, Name: "regular", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Status: billing.StatusActive, Schedulable: true, AccountGroups: []accountcore.GroupMembership{{GroupID: group.ID, Group: (*accessview.GroupConfig)(group)}}},
	}
	subscription := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8012, Name: "subscription", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Schedulable: true, Credentials: map[string]any{"plan_type": "plus"}},
	}
	source := &advancedSchedulerDiagnosticSourceStub{
		account: target, group: group, accounts: []gatewayprovider.ExecutionAccount{*target, subscription}, pool: []gatewayprovider.ExecutionAccount{*target, subscription},
	}
	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(source, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})

	require.NoError(t, err)
	require.False(t, result.Detail.Eligible)
	require.Contains(t, result.Detail.HardFilterReasons, "subscription_priority_deferred")
	require.Equal(t, 2, result.Detail.CandidatePool.TotalCandidates)
	require.Equal(t, 1, result.Detail.CandidatePool.EligibleCandidates)
	require.Equal(t, 1, result.Detail.CandidatePool.ExclusionReasons["subscription_priority_deferred"])
	require.Equal(t, subscription.Record.ID, result.Detail.CandidatePool.Candidates[0].ID)
	signal := findAdvancedSchedulerDiagnosticPolicy(result.Detail.PolicySignals, "subscription_priority")
	require.NotNil(t, signal)
	require.Equal(t, "active_pool", signal.State)
}

func TestAdvancedSchedulerScoreDiagnosticService_CountsMoreThanOneThousandExcludedAccounts(t *testing.T) {
	group := &routing.Group{ID: 901, Name: "advanced", Platform: capability.PlatformGemini, SchedulerType: routing.GroupSchedulerTypeAdvanced}
	target := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9011, Name: "target", Platform: capability.PlatformGemini, Status: billing.StatusActive, Schedulable: true,
		AccountGroups: []accountcore.GroupMembership{{GroupID: group.ID, Group: (*accessview.GroupConfig)(group)}}},
	}
	allAccounts := make([]gatewayprovider.ExecutionAccount, 0, 1002)
	allAccounts = append(allAccounts, *target)
	for index := 0; index < 1001; index++ {
		allAccounts = append(allAccounts, gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: int64(9100 + index), Platform: capability.PlatformGemini, Status: billing.StatusDisabled}})
	}
	source := &advancedSchedulerDiagnosticSourceStub{account: target, group: group, accounts: allAccounts, pool: []gatewayprovider.ExecutionAccount{*target}}
	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(source, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})

	require.NoError(t, err)
	require.Equal(t, 1002, result.Detail.CandidatePool.TotalCandidates)
	require.Equal(t, 1001, result.Detail.CandidatePool.ExcludedCandidates)
	require.Equal(t, 1001, result.Detail.CandidatePool.ExclusionReasons["account_inactive"])
}

func TestAdvancedSchedulerScoreDiagnosticService_StableSortsLargeCandidatePool(t *testing.T) {
	group := &routing.Group{
		ID: 1001, Name: "advanced", Platform: capability.PlatformGemini, SchedulerType: routing.GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
			WeightPriority:      advancedSchedulerDiagnosticFloat(1),
			WeightLoad:          advancedSchedulerDiagnosticFloat(0),
			WeightQueue:         advancedSchedulerDiagnosticFloat(0),
			WeightErrorRate:     advancedSchedulerDiagnosticFloat(0),
			WeightTTFT:          advancedSchedulerDiagnosticFloat(0),
			WeightReset:         advancedSchedulerDiagnosticFloat(0),
			WeightQuotaHeadroom: advancedSchedulerDiagnosticFloat(0),
		},
	}
	accounts := make([]gatewayprovider.ExecutionAccount, 0, 1101)
	for index := 1100; index >= 0; index-- {
		accounts = append(accounts, gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: int64(10000 + index), Platform: capability.PlatformGemini, Status: billing.StatusActive,
			Schedulable: true, Priority: index % 7},
		})
	}
	target := &accounts[len(accounts)-1]
	target.Record.AccountGroups = []accountcore.GroupMembership{{GroupID: group.ID, Group: (*accessview.GroupConfig)(group)}}
	source := &advancedSchedulerDiagnosticSourceStub{account: target, group: group, accounts: accounts, pool: accounts}
	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(source, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})

	require.NoError(t, err)
	require.Len(t, result.Detail.CandidatePool.Candidates, 1101)
	for index := 1; index < len(result.Detail.CandidatePool.Candidates); index++ {
		previous := result.Detail.CandidatePool.Candidates[index-1]
		current := result.Detail.CandidatePool.Candidates[index]
		require.True(t, previous.Priority < current.Priority ||
			(previous.Priority == current.Priority && previous.ID < current.ID),
			"candidate order must follow score comparator at index %d", index)
	}
}

func TestAdvancedSchedulerScoreDiagnosticService_LoadUsesEffectiveLoadFactor(t *testing.T) {
	cache := &advancedSchedulerDiagnosticConcurrencyCache{}
	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(nil, scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
		Event: logging.Event,
	},
	)))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1101, Concurrency: 2, LoadFactor: advancedSchedulerDiagnosticInt(7)}}

	diagnostics.loadMap(context.Background(), []*gatewayprovider.ExecutionAccount{account})

	require.Len(t, cache.requests, 1)
	require.Equal(t, []scheduler.AccountWithConcurrency{{ID: account.Record.ID, MaxConcurrency: 7}}, cache.requests[0])
}

func TestAdvancedSchedulerScoreDiagnosticService_FiltersModelRuntimeBlock(t *testing.T) {
	group := &routing.Group{ID: 1201, Platform: capability.PlatformGemini, SchedulerType: routing.GroupSchedulerTypeAdvanced}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12011, Platform: capability.PlatformGemini, Status: billing.StatusActive, Schedulable: true,
		Extra: map[string]any{"model_rate_limits": map[string]any{
			"gemini-3-pro": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		}}},
	}
	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(nil, nil))

	reason := diagnostics.diagnosticHardFilterReason(
		context.Background(), account, group,
		policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID, RequestedModel: "gemini-3-pro"}, time.Now(),
	)

	require.Equal(t, "model_runtime_blocked", reason)
}

func TestAdvancedSchedulerScoreDiagnosticService_EscapedStickyUsesRegularWindowCostGate(t *testing.T) {
	group := &routing.Group{
		ID: 1301, Platform: capability.PlatformAnthropic, SchedulerType: routing.GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled: advancedSchedulerDiagnosticBool(false),
		},
	}
	target := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 13011, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true,
		Extra:         map[string]any{"window_cost_limit": 10.0, "window_cost_sticky_reserve": 5.0},
		AccountGroups: []accountcore.GroupMembership{{GroupID: group.ID, Group: (*accessview.GroupConfig)(group)}}},
	}
	other := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 13012, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}}
	source := &advancedSchedulerDiagnosticSourceStub{
		account: target, group: group, accounts: []gatewayprovider.ExecutionAccount{*target, other}, pool: []gatewayprovider.ExecutionAccount{*target, other},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{AdvancedScheduler: config.GatewayAdvancedSchedulerConfig{
		StickyEscapeEnabled: true, StickyEscapeTTFTMs: 15000, StickyEscapeErrorRate: 0.55,
	}}}

	feedback := scheduler.NewRuntimeStats(time.Now)
	for range 4 {
		feedback.Report(target.Record.ID, false, nil)
	}
	diagnostics := withSchedulerParametersForTest(NewAdvancedSchedulerScoreDiagnosticService(source, nil))
	diagnostics.BindSchedulerRuntime(feedback, scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, schedulerParameterDefaultsForTest(cfg)))
	diagnostics.SetSchedulingServices(withSchedulerParametersForTest(&GatewayService{
		cfg: cfg, windowCostCache: &sessionLimitCacheHotpathStub{batchData: map[int64]float64{target.Record.ID: 11}},
		usageLogRepo: &usageLogWindowBatchRepoStub{},
	}), nil)

	result, err := diagnostics.GetDetail(context.Background(), target.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{
		GroupID: group.ID, StickyAccountID: target.Record.ID,
	})

	require.NoError(t, err)
	require.False(t, result.Detail.Eligible)
	require.Contains(t, result.Detail.HardFilterReasons, "window_cost_exceeded")
	signal := findAdvancedSchedulerDiagnosticPolicy(result.Detail.PolicySignals, "session_sticky")
	require.NotNil(t, signal)
	require.Equal(t, "escaped", signal.State)
}
