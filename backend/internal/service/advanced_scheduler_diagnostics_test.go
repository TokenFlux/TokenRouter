package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type advancedSchedulerDiagnosticSourceStub struct {
	account  *Account
	group    *Group
	accounts []Account
	pool     []Account
}

type advancedSchedulerDiagnosticConcurrencyCache struct {
	ConcurrencyCache
	requests [][]AccountWithConcurrency
}

func (c *advancedSchedulerDiagnosticConcurrencyCache) GetAccountsLoadBatch(_ context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	c.requests = append(c.requests, append([]AccountWithConcurrency(nil), accounts...))
	result := make(map[int64]*AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		result[account.ID] = &AccountLoadInfo{AccountID: account.ID}
	}
	return result, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) GetAccount(_ context.Context, _ int64) (*Account, error) {
	return s.account, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) GetGroup(_ context.Context, _ int64) (*Group, error) {
	return s.group, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) ListAccountsForSchedulerScoreFilter(_ context.Context, _, _, _, _ string, _ int64, _ string) ([]Account, error) {
	return s.accounts, nil
}

func (s *advancedSchedulerDiagnosticSourceStub) ListSchedulableAccountsForAdvancedSchedulerScore(_ context.Context, _ *int64, _ string) ([]Account, error) {
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

func findAdvancedSchedulerDiagnosticMetric(metrics []AdvancedSchedulerScoreDiagnosticMetric, key string) *AdvancedSchedulerScoreDiagnosticMetric {
	for index := range metrics {
		if metrics[index].Key == key {
			return &metrics[index]
		}
	}
	return nil
}

func findAdvancedSchedulerDiagnosticSetting(settings []AdvancedSchedulerScoreDiagnosticSetting, key string) *AdvancedSchedulerScoreDiagnosticSetting {
	for index := range settings {
		if settings[index].Key == key {
			return &settings[index]
		}
	}
	return nil
}

func findAdvancedSchedulerDiagnosticPolicy(signals []AdvancedSchedulerScoreDiagnosticPolicySignal, key string) *AdvancedSchedulerScoreDiagnosticPolicySignal {
	for index := range signals {
		if signals[index].Key == key {
			return &signals[index]
		}
	}
	return nil
}

func TestAdvancedSchedulerScoreDiagnosticService_UsesActualFormulaAndSafeDTO(t *testing.T) {
	group := &Group{
		ID:            301,
		Name:          "advanced",
		Platform:      PlatformGemini,
		SchedulerType: GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
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
	target := &Account{
		ID:            101,
		Name:          "target",
		Platform:      PlatformGemini,
		Type:          AccountTypeOAuth,
		Status:        StatusActive,
		Schedulable:   true,
		Priority:      1,
		Credentials:   map[string]any{"access_token": "secret-token"},
		AccountGroups: []AccountGroup{{GroupID: group.ID, Group: group}},
	}
	other := Account{
		ID:          102,
		Name:        "other",
		Platform:    PlatformGemini,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Priority:    2,
	}
	source := &advancedSchedulerDiagnosticSourceStub{
		account:  target,
		group:    group,
		accounts: []Account{*target, other},
		pool:     []Account{*target, other},
	}
	rateLimitService := NewRateLimitService(nil, nil, nil, nil, nil)
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(source, nil, rateLimitService)

	result, err := diagnostics.GetDetail(context.Background(), target.ID, AdvancedSchedulerScoreDiagnosticRequest{
		GroupID:         group.ID,
		StickyAccountID: target.ID,
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

func TestAdvancedSchedulerRuntimeFeedbackSnapshot_RecordsSamplesAndObservedTime(t *testing.T) {
	stats := newAdvancedAccountRuntimeStats()
	ttft := 240
	stats.report(401, true, &ttft)
	stats.report(401, false, nil)

	snapshot := stats.feedbackSnapshot(401)
	require.True(t, snapshot.HasFeedback)
	require.EqualValues(t, 2, snapshot.ErrorSamples)
	require.NotNil(t, snapshot.LastObservedAt)
	require.True(t, snapshot.HasTTFT)
	require.EqualValues(t, 1, snapshot.TTFTSamples)
	require.NotNil(t, snapshot.LastTTFTAt)
	require.InDelta(t, 0.2, snapshot.ErrorRate, 0.000001)
	require.InDelta(t, 240, snapshot.TTFT, 0.000001)
}

func TestAdvancedSchedulerScoreDiagnosticService_UsesProcessConfigBeforeFallback(t *testing.T) {
	group := &Group{ID: 501, Name: "advanced", Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeAdvanced}
	target := &Account{
		ID:            5011,
		Name:          "target",
		Platform:      PlatformGemini,
		Status:        StatusActive,
		Schedulable:   true,
		Priority:      1,
		AccountGroups: []AccountGroup{{GroupID: group.ID, Group: group}},
	}
	other := Account{ID: 5012, Name: "other", Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 2}
	source := &advancedSchedulerDiagnosticSourceStub{
		account:  target,
		group:    group,
		accounts: []Account{*target, other},
		pool:     []Account{*target, other},
	}
	rateLimitService := NewRateLimitService(nil, nil, &config.Config{
		Gateway: config.GatewayConfig{AdvancedScheduler: config.GatewayAdvancedSchedulerConfig{
			LBTopK: 1,
			ScoreWeights: config.GatewayAdvancedSchedulerScoreWeights{
				Priority: 4,
			},
		}},
	}, nil, nil)
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(source, nil, rateLimitService)

	result, err := diagnostics.GetDetail(context.Background(), target.ID, AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})
	require.NoError(t, err)
	require.NotNil(t, result.Detail)
	require.NotNil(t, result.Detail.Score)
	require.InDelta(t, 4, result.Detail.Score.BaseScore, 0.000001)
	require.Equal(t, 1, result.Detail.CandidatePool.TopK)
	prioritySetting := findAdvancedSchedulerDiagnosticSetting(result.Detail.EffectiveSettings, "weight_priority")
	require.NotNil(t, prioritySetting)
	require.Equal(t, "process_default", prioritySetting.Source)
}

func TestDiagnosticPolicySignalsOnlyReturnsContextualOrEnabledStrategies(t *testing.T) {
	group := &Group{ID: 601, Platform: PlatformOpenAI, SchedulerType: GroupSchedulerTypeAdvanced}
	effective := advancedSchedulerEffectiveSettings{}

	// 基准诊断明确标出请求未提供、因而无法评估的能力门禁。
	baselineSignals := diagnosticPolicySignals(group, AdvancedSchedulerScoreDiagnosticRequest{}, effective, diagnosticPolicyOutcome{})
	require.Len(t, baselineSignals, 1)
	require.Equal(t, "request_capabilities", baselineSignals[0].Key)
	require.Equal(t, "not_evaluated", baselineSignals[0].State)

	effective.stickyWeightedEnabled = true
	effective.subscriptionPriorityEnabled = true
	signals := diagnosticPolicySignals(group, AdvancedSchedulerScoreDiagnosticRequest{StickyAccountID: 99}, effective, diagnosticPolicyOutcome{
		sessionStickyState:     "weighted",
		subscriptionPoolActive: true,
	})
	require.Len(t, signals, 3)
	require.Equal(t, "session_sticky", signals[0].Key)
	require.Equal(t, "weighted", signals[0].State)
	require.Equal(t, "subscription_priority", signals[1].Key)
	require.Equal(t, "active_pool", signals[1].State)
}

func TestDiagnosticWeightedPreviousResponseIsIgnoredOutsideOpenAI(t *testing.T) {
	group := &Group{ID: 602, Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeAdvanced}
	outcome := diagnosticHardStickyPolicyOutcome(
		[]*Account{{ID: 99, Platform: PlatformGemini}},
		group,
		AdvancedSchedulerScoreDiagnosticRequest{PreviousResponseAccountID: 99, StickyAccountID: 99},
		advancedSchedulerEffectiveSettings{stickyWeightedEnabled: true},
		nil,
		advancedStickyEscapeConfig{},
	)

	require.Zero(t, outcome.forcedAccountID)
	require.Equal(t, "ignored", outcome.previousResponseState)
	require.Equal(t, "weighted", outcome.sessionStickyState)
}

func TestAdvancedSchedulerScoreDiagnosticService_HardStickyForcesAccountOutsideTopK(t *testing.T) {
	group := &Group{
		ID:            701,
		Name:          "advanced",
		Platform:      PlatformGemini,
		SchedulerType: GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
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
	target := &Account{
		ID: 7011, Name: "sticky", Platform: PlatformGemini, Status: StatusActive,
		Schedulable: true, Priority: 100, AccountGroups: []AccountGroup{{GroupID: group.ID, Group: group}},
	}
	best := Account{ID: 7012, Name: "best", Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 1}
	source := &advancedSchedulerDiagnosticSourceStub{
		account: target, group: group, accounts: []Account{*target, best}, pool: []Account{best, *target},
	}
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(source, nil, NewRateLimitService(nil, nil, &config.Config{}, nil, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.ID, AdvancedSchedulerScoreDiagnosticRequest{
		GroupID: group.ID, StickyAccountID: target.ID,
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
	group := &Group{
		ID:            801,
		Name:          "advanced",
		Platform:      PlatformOpenAI,
		SchedulerType: GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
			SubscriptionPriorityEnabled: advancedSchedulerDiagnosticBool(true),
		},
	}
	target := &Account{
		ID: 8011, Name: "regular", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, AccountGroups: []AccountGroup{{GroupID: group.ID, Group: group}},
	}
	subscription := Account{
		ID: 8012, Name: "subscription", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Credentials: map[string]any{"plan_type": "plus"},
	}
	source := &advancedSchedulerDiagnosticSourceStub{
		account: target, group: group, accounts: []Account{*target, subscription}, pool: []Account{*target, subscription},
	}
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(source, nil, NewRateLimitService(nil, nil, &config.Config{}, nil, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.ID, AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})

	require.NoError(t, err)
	require.False(t, result.Detail.Eligible)
	require.Contains(t, result.Detail.HardFilterReasons, "subscription_priority_deferred")
	require.Equal(t, 2, result.Detail.CandidatePool.TotalCandidates)
	require.Equal(t, 1, result.Detail.CandidatePool.EligibleCandidates)
	require.Equal(t, 1, result.Detail.CandidatePool.ExclusionReasons["subscription_priority_deferred"])
	require.Equal(t, subscription.ID, result.Detail.CandidatePool.Candidates[0].ID)
	signal := findAdvancedSchedulerDiagnosticPolicy(result.Detail.PolicySignals, "subscription_priority")
	require.NotNil(t, signal)
	require.Equal(t, "active_pool", signal.State)
}

func TestAdvancedSchedulerScoreDiagnosticService_CountsMoreThanOneThousandExcludedAccounts(t *testing.T) {
	group := &Group{ID: 901, Name: "advanced", Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeAdvanced}
	target := &Account{
		ID: 9011, Name: "target", Platform: PlatformGemini, Status: StatusActive, Schedulable: true,
		AccountGroups: []AccountGroup{{GroupID: group.ID, Group: group}},
	}
	allAccounts := make([]Account, 0, 1002)
	allAccounts = append(allAccounts, *target)
	for index := 0; index < 1001; index++ {
		allAccounts = append(allAccounts, Account{ID: int64(9100 + index), Platform: PlatformGemini, Status: StatusDisabled})
	}
	source := &advancedSchedulerDiagnosticSourceStub{account: target, group: group, accounts: allAccounts, pool: []Account{*target}}
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(source, nil, NewRateLimitService(nil, nil, &config.Config{}, nil, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.ID, AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})

	require.NoError(t, err)
	require.Equal(t, 1002, result.Detail.CandidatePool.TotalCandidates)
	require.Equal(t, 1001, result.Detail.CandidatePool.ExcludedCandidates)
	require.Equal(t, 1001, result.Detail.CandidatePool.ExclusionReasons["account_inactive"])
}

func TestAdvancedSchedulerScoreDiagnosticService_StableSortsLargeCandidatePool(t *testing.T) {
	group := &Group{
		ID: 1001, Name: "advanced", Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
			WeightPriority:      advancedSchedulerDiagnosticFloat(1),
			WeightLoad:          advancedSchedulerDiagnosticFloat(0),
			WeightQueue:         advancedSchedulerDiagnosticFloat(0),
			WeightErrorRate:     advancedSchedulerDiagnosticFloat(0),
			WeightTTFT:          advancedSchedulerDiagnosticFloat(0),
			WeightReset:         advancedSchedulerDiagnosticFloat(0),
			WeightQuotaHeadroom: advancedSchedulerDiagnosticFloat(0),
		},
	}
	accounts := make([]Account, 0, 1101)
	for index := 1100; index >= 0; index-- {
		accounts = append(accounts, Account{
			ID: int64(10000 + index), Platform: PlatformGemini, Status: StatusActive,
			Schedulable: true, Priority: index % 7,
		})
	}
	target := &accounts[len(accounts)-1]
	target.AccountGroups = []AccountGroup{{GroupID: group.ID, Group: group}}
	source := &advancedSchedulerDiagnosticSourceStub{account: target, group: group, accounts: accounts, pool: accounts}
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(source, nil, NewRateLimitService(nil, nil, &config.Config{}, nil, nil))

	result, err := diagnostics.GetDetail(context.Background(), target.ID, AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID})

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
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(nil, NewConcurrencyService(cache), nil)
	account := &Account{ID: 1101, Concurrency: 2, LoadFactor: advancedSchedulerDiagnosticInt(7)}

	diagnostics.loadMap(context.Background(), []*Account{account})

	require.Len(t, cache.requests, 1)
	require.Equal(t, []AccountWithConcurrency{{ID: account.ID, MaxConcurrency: 7}}, cache.requests[0])
}

func TestAdvancedSchedulerScoreDiagnosticService_FiltersModelRuntimeBlock(t *testing.T) {
	group := &Group{ID: 1201, Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeAdvanced}
	account := &Account{
		ID: 12011, Platform: PlatformGemini, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{modelRateLimitsKey: map[string]any{
			"gemini-3-pro": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		}},
	}
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(nil, nil, nil)

	reason := diagnostics.diagnosticHardFilterReason(
		context.Background(), account, group,
		AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID, RequestedModel: "gemini-3-pro"}, time.Now(),
	)

	require.Equal(t, "model_runtime_blocked", reason)
}

func TestAdvancedSchedulerScoreDiagnosticService_EscapedStickyUsesRegularWindowCostGate(t *testing.T) {
	group := &Group{
		ID: 1301, Platform: PlatformAnthropic, SchedulerType: GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled: advancedSchedulerDiagnosticBool(false),
		},
	}
	target := &Account{
		ID: 13011, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Extra:         map[string]any{"window_cost_limit": 10.0, "window_cost_sticky_reserve": 5.0},
		AccountGroups: []AccountGroup{{GroupID: group.ID, Group: group}},
	}
	other := Account{ID: 13012, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	source := &advancedSchedulerDiagnosticSourceStub{
		account: target, group: group, accounts: []Account{*target, other}, pool: []Account{*target, other},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{AdvancedScheduler: config.GatewayAdvancedSchedulerConfig{
		StickyEscapeEnabled: true, StickyEscapeTTFTMs: 15000, StickyEscapeErrorRate: 0.55,
	}}}
	rateLimitService := NewRateLimitService(nil, nil, cfg, nil, nil)
	for range 4 {
		rateLimitService.AdvancedSchedulerRuntimeStats().report(target.ID, false, nil)
	}
	diagnostics := NewAdvancedSchedulerScoreDiagnosticService(source, nil, rateLimitService)
	diagnostics.SetSchedulingServices(&GatewayService{
		cfg: cfg, sessionLimitCache: &sessionLimitCacheHotpathStub{batchData: map[int64]float64{target.ID: 11}},
		usageLogRepo: &usageLogWindowBatchRepoStub{},
	}, nil)

	result, err := diagnostics.GetDetail(context.Background(), target.ID, AdvancedSchedulerScoreDiagnosticRequest{
		GroupID: group.ID, StickyAccountID: target.ID,
	})

	require.NoError(t, err)
	require.False(t, result.Detail.Eligible)
	require.Contains(t, result.Detail.HardFilterReasons, "window_cost_exceeded")
	signal := findAdvancedSchedulerDiagnosticPolicy(result.Detail.PolicySignals, "session_sticky")
	require.NotNil(t, signal)
	require.Equal(t, "escaped", signal.State)
}
