package selection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
)

// openAIProviderLoadPlan retains scored candidate values for fixture ordering
// and score assertions.
type openAIProviderLoadPlan struct {
	candidates []schedulercore.CandidateScore // scored fixture candidates
}

// buildOpenAIProviderLoadPlan delegates scoring to the platform core with
// fixture targets and loads. It copies candidate IDs, priorities, and scores
// from the plan snapshot into the value used by assertions.
func (s *compatiblePicker) buildOpenAIProviderLoadPlan(ctx context.Context, req schedulercore.PlatformSelectionInput, values []*gatewayprovider.ExecutionProvider, loads map[int64]*schedulercore.ProviderLoadInfo) openAIProviderLoadPlan {
	core, scope := s.platformSelector()
	plan := core.BuildPlan(ctx, req, scope.pointers(values), loads)
	var candidates []schedulercore.CandidateScore
	for _, value := range plan.CandidatesSnapshot() {
		candidates = append(candidates, schedulercore.CandidateScore{Provider: &schedulercore.ScoreProvider{ID: value.Provider.ID, Priority: value.Provider.Priority}, Score: value.Score})
	}
	return openAIProviderLoadPlan{candidates: candidates}
}

// openAIResetTestScheduler constructs the fixed scoring fixture with the
// supplied reset weight.
func openAIResetTestScheduler(reset float64) *compatiblePicker {
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.ScoreWeights = config.GatewayAdvancedSchedulerScoreWeights{
		Priority:      1.0,
		Load:          1.0,
		Queue:         0.7,
		ErrorRate:     0.8,
		TTFT:          0.5,
		Reset:         reset,
		QuotaHeadroom: 0,
	}
	return &compatiblePicker{service: newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{}, Shared: Shared{}}, cfg)}
}

// openAIQuotaHeadroomTestScheduler constructs a scoring fixture weighted
// exclusively by observed quota headroom.
func openAIQuotaHeadroomTestScheduler(quotaHeadroom float64) *compatiblePicker {
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.ScoreWeights = config.GatewayAdvancedSchedulerScoreWeights{QuotaHeadroom: quotaHeadroom}
	return &compatiblePicker{service: newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{}, Shared: Shared{}}, cfg)}
}

// openAIPlanScores indexes fixture candidate scores by their persisted provider
// identifiers.
func openAIPlanScores(plan openAIProviderLoadPlan) map[int64]float64 {
	scores := make(map[int64]float64, len(plan.candidates))
	for _, c := range plan.candidates {
		scores[c.Provider.ID] = c.Score
	}
	return scores
}

// TestBuildOpenAIProviderLoadPlan_ResetWeightPrefersSoonestReset supplies
// equal-priority providers with different future window resets. Positive reset
// weight gives the earlier reset a higher score, protecting time-aware
// candidate preference.
func TestBuildOpenAIProviderLoadPlan_ResetWeightPrefersSoonestReset(t *testing.T) {
	now := time.Now()
	soon := now.Add(1 * time.Hour)
	later := now.Add(20 * time.Hour)
	filtered := []*gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Priority: 0, SessionWindowEnd: &later}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Priority: 0, SessionWindowEnd: &soon}},
	}
	sched := openAIResetTestScheduler(5.0)

	plan := sched.buildOpenAIProviderLoadPlan(context.Background(), schedulercore.PlatformSelectionInput{}, filtered, map[int64]*schedulercore.ProviderLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Greater(t, scores[2], scores[1], "重置时间最早的提供商（ID=2）得分更高")
}

// TestBuildOpenAIProviderLoadPlan_ResetWeightZeroNoEffect supplies different
// future reset times with the reset weight disabled. Both providers retain
// equal scores, protecting the configured zero weight during candidate
// calculation.
func TestBuildOpenAIProviderLoadPlan_ResetWeightZeroNoEffect(t *testing.T) {
	now := time.Now()
	soon := now.Add(1 * time.Hour)
	later := now.Add(20 * time.Hour)
	filtered := []*gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Priority: 0, SessionWindowEnd: &later}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Priority: 0, SessionWindowEnd: &soon}},
	}
	sched := openAIResetTestScheduler(0.0)

	plan := sched.buildOpenAIProviderLoadPlan(context.Background(), schedulercore.PlatformSelectionInput{}, filtered, map[int64]*schedulercore.ProviderLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Equal(t, scores[1], scores[2], "Reset 权重为 0 时两提供商得分相同")
}

// TestBuildOpenAIProviderLoadPlan_BillingRatesDoNotAffectScoreOrOrder gives
// otherwise equal candidates different billing multipliers and probe extras.
// Scores remain equal and provider ID resolves their stable order, protecting
// scheduling inputs from billing data.
func TestBuildOpenAIProviderLoadPlan_BillingRatesDoNotAffectScoreOrOrder(t *testing.T) {
	expensiveRate := 100.0
	cheapRate := 0.01
	filtered := []*gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Priority: 0, RateMultiplier: &expensiveRate,
				Extra: map[string]any{
					"upstream_billing_probe": map[string]any{
						"status": "ok",
						"data":   map[string]any{"effective_rate_multiplier": expensiveRate},
					},
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Priority: 0, RateMultiplier: &cheapRate,
				Extra: map[string]any{
					"upstream_billing_probe": map[string]any{
						"status": "ok",
						"data":   map[string]any{"effective_rate_multiplier": cheapRate},
					},
				},
			},
		},
	}
	sched := openAIResetTestScheduler(0)

	plan := sched.buildOpenAIProviderLoadPlan(context.Background(), schedulercore.PlatformSelectionInput{}, filtered, map[int64]*schedulercore.ProviderLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Equal(t, scores[1], scores[2])

	ranked := schedulercore.SelectTopK(plan.candidates, len(plan.candidates))
	require.Equal(t, []int64{1, 2}, []int64{ranked[0].Provider.ID, ranked[1].Provider.ID})
}

// TestBuildOpenAIProviderLoadPlan_ResetWeightIgnoresNilWindow compares a
// missing reset observation with an upcoming window reset. The known upcoming
// reset receives the higher score under positive reset weight.
func TestBuildOpenAIProviderLoadPlan_ResetWeightIgnoresNilWindow(t *testing.T) {
	now := time.Now()
	soon := now.Add(2 * time.Hour)
	filtered := []*gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Priority: 0, SessionWindowEnd: nil}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Priority: 0, SessionWindowEnd: &soon}},
	}
	sched := openAIResetTestScheduler(5.0)

	plan := sched.buildOpenAIProviderLoadPlan(context.Background(), schedulercore.PlatformSelectionInput{}, filtered, map[int64]*schedulercore.ProviderLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Greater(t, scores[2], scores[1], "拥有活跃窗口的提供商得分高于无窗口提供商")
}

// TestBuildOpenAIProviderLoadPlan_QuotaHeadroomPrefersHigher7dRemaining
// supplies fresh primary quota snapshots with different utilization. Positive
// headroom weight scores the provider with more remaining quota higher,
// protecting quota-aware scheduling preference.
func TestBuildOpenAIProviderLoadPlan_QuotaHeadroomPrefersHigher7dRemaining(t *testing.T) {
	now := time.Now()
	filtered := []*gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
				Priority: 0,
				Extra: map[string]any{
					"codex_primary_used_percent": 80.0,
					"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
					"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
				Priority: 0,
				Extra: map[string]any{
					"codex_primary_used_percent": 20.0,
					"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
					"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
				},
			},
		},
	}
	sched := openAIQuotaHeadroomTestScheduler(1.0)

	plan := sched.buildOpenAIProviderLoadPlan(context.Background(), schedulercore.PlatformSelectionInput{}, filtered, map[int64]*schedulercore.ProviderLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Greater(t, scores[2], scores[1], "7d 剩余额度更高的提供商得分应更高")
}

// TestBuildOpenAIProviderLoadPlan_QuotaHeadroomZeroNoEffect varies fresh quota
// usage while headroom weight is zero. The candidate scores stay equal,
// protecting optional quota observations from influencing disabled score
// factors.
func TestBuildOpenAIProviderLoadPlan_QuotaHeadroomZeroNoEffect(t *testing.T) {
	now := time.Now()
	filtered := []*gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
				Priority: 0,
				Extra: map[string]any{
					"codex_primary_used_percent": 80.0,
					"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
					"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
				},
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
				Priority: 0,
				Extra: map[string]any{
					"codex_primary_used_percent": 20.0,
					"codex_primary_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
					"codex_usage_updated_at":     now.Add(-time.Minute).Format(time.RFC3339),
				},
			},
		},
	}
	sched := openAIResetTestScheduler(0)

	plan := sched.buildOpenAIProviderLoadPlan(context.Background(), schedulercore.PlatformSelectionInput{}, filtered, map[int64]*schedulercore.ProviderLoadInfo{})
	scores := openAIPlanScores(plan)
	require.Equal(t, scores[1], scores[2], "quota_headroom 权重为 0 时不应影响打分")
}

// TestOpenAIGatewayService_SelectProviderWithScheduler_DBFreshGroupRecheckReleasesMovedProvider
// ranks a stale candidate moved out of the group in the database. Revalidation
// releases its acquired slot before selecting the valid backup and returning
// that target.
func TestOpenAIGatewayService_SelectProviderWithScheduler_DBFreshGroupRecheckReleasesMovedProvider(t *testing.T) {
	ctx := context.Background()
	groupID, otherGroupID := int64(10105), int64(10106)
	stalePrimary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34101, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}}
	staleBackup := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34102, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, GroupIDs: []int64{groupID}}}
	dbPrimary := *stalePrimary
	dbPrimary.Record.GroupIDs = []int64{otherGroupID}
	dbBackup := *staleBackup
	snapshotCache := &openAISnapshotCacheStub{
		snapshotProviders: []*gatewayprovider.ExecutionProvider{stalePrimary, staleBackup},
		providersByID:     map[int64]*gatewayprovider.ExecutionProvider{stalePrimary.Record.ID: stalePrimary, staleBackup.Record.ID: staleBackup},
	}
	acquiredIDs, releasedIDs := []int64{}, []int64{}
	cfg := &config.Config{}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{dbPrimary, dbBackup}},
			Snapshot: schedulerredis.NewSnapshotReader(
				schedulercore.NewSnapshotService(snapshotCache, nil, nil, nil, nil)),
		},

		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquiredIDs, releasedIDs: &releasedIDs}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, cfg)

	scheduler := &compatiblePicker{service: svc}

	core, scope := scheduler.platformSelector()
	candidates := []schedulercore.PlatformCandidateScore{
		{Provider: scope.provider(stalePrimary), LoadInfo: &schedulercore.ProviderLoadInfo{ProviderID: stalePrimary.Record.ID}},
		{Provider: scope.provider(staleBackup), LoadInfo: &schedulercore.ProviderLoadInfo{ProviderID: staleBackup.Record.ID}},
	}
	result, _, err := core.TryOrderBounded(ctx, schedulercore.PlatformSelectionInput{GroupID: &groupID, Platform: capability.PlatformOpenAI, RequestedModel: "gpt-5.1"}, candidates)
	selection := scope.restore(result)

	require.NoError(t, err)
	require.Equal(t, staleBackup.Record.ID, selection.Provider.Record.ID)
	require.Equal(t, []int64{stalePrimary.Record.ID, staleBackup.Record.ID}, acquiredIDs)
	require.Equal(t, []int64{stalePrimary.Record.ID}, releasedIDs)
	selection.ReleaseFunc()
}
