package selection

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingstestkit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usageLogWindowBatchRepoStub supplies distinct batch and individual usage
// results with atomic read counters.
type usageLogWindowBatchRepoStub struct {
	usage.UsageLogRepository // unused usage operations panic

	// batchResult, batchErr, and batchCalls describe grouped usage reads, while
	// singleResult, singleErr, and singleCalls describe individual recovery reads.
	// Returned statistics pointers stay shared with these configured inputs.
	batchResult map[int64]*usage.ProviderStats // shared batched usage result pointers
	// batchErr supplies the grouped usage failure. A nonnil value is returned
	// after incrementing batchCalls and before consulting batchResult.
	batchErr   error        // configured batch usage failure
	batchCalls atomic.Int64 // atomic batch read count

	singleResult map[int64]*usage.ProviderStats // shared individual usage result pointers
	// singleErr supplies the individual usage failure. A nonnil value returns
	// after incrementing singleCalls and bypasses both configured results and the
	// fresh zero-statistics result.
	singleErr   error        // configured individual usage failure
	singleCalls atomic.Int64 // atomic individual read count
}

// sessionLimitCacheHotpathStub supplies batch window costs and records cache
// writes for prefetch scenarios.
type sessionLimitCacheHotpathStub struct {
	billing.WindowCostCache // unused window cache operations

	batchData map[int64]float64 // configured provider window cost observations
	// batchErr supplies the cost-cache query failure. A nonnil value returns
	// before reading batchData or allocating the requested-observation result map.
	batchErr error // configured cost batch failure

	setData map[int64]float64 // recorded provider cost writes
	// setErr supplies the cache-publication failure. A nonnil value returns
	// before allocating or updating setData, leaving the fixture write trace intact.
	setErr error // configured cost write failure
}

// stickyGatewayCacheHotpathStub supplies one sticky target and counts reads for
// prefetch reuse assertions.
type stickyGatewayCacheHotpathStub struct {
	session.GatewayCache // unused ownership operations panic

	stickyID int64        // configured sticky provider identifier
	getCalls atomic.Int64 // atomic sticky lookup count
}

// sessionLimitReleaseCacheStub records per-provider session removal calls or
// returns a configured failure.
type sessionLimitReleaseCacheStub struct {
	schedulercore.SessionLimitCache // unused registration operations panic

	unregistered map[int64][]string // recorded provider session removal calls
	// err supplies the unregister failure. A nonnil value returns before
	// appending to unregistered, so failed removals leave no entry in that trace.
	err error // configured session removal failure
}

// TestCollectSelectionFailureStats supplies one provider for each diagnostic
// rejection category and one passing target. The resulting counts partition the
// whole pool, protecting exclusive category accounting for these separately
// constructed outcomes.
func TestCollectSelectionFailureStats(t *testing.T) {
	svc := NewGeneric(GenericDependencies{}, DefaultOptions())
	model := "gpt-5.4"
	resetAt := time.Now().Add(2 * time.Minute).Format(time.RFC3339)

	providers := []gatewayprovider.
		ExecutionProvider{
		// This record otherwise passes the diagnostic gates. Its explicit request
		// exclusion makes it contribute to the excluded count in this fixture.
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
			},
		},
		// unschedulable
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: false,
			},
		},
		// platform filtered
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3,
				Platform:    capability.PlatformAntigravity,
				Status:      billing.StatusActive,
				Schedulable: true,
			},
		},
		// model unsupported
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 4,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_whitelist": []string{"gpt-image"},
					"model_mapping": map[string]any{
						"gpt-image": "gpt-image",
					},
				},
			},
		},
		// model rate limited
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 5,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Extra: map[string]any{
					"model_rate_limits": map[string]any{
						model: map[string]any{
							"rate_limit_reset_at": resetAt,
						},
					},
				},
			},
		},
		// eligible
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 6,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
			},
		},
	}

	excluded := map[int64]struct{}{1: {}}
	stats := svc.collectSelectionFailureStats(context.Background(), providers, model, capability.PlatformOpenAI, excluded, false)

	if stats.Total != 6 {
		t.Fatalf("total=%d want=6", stats.Total)
	}
	if stats.Excluded != 1 {
		t.Fatalf("excluded=%d want=1", stats.Excluded)
	}
	if stats.Unschedulable != 1 {
		t.Fatalf("unschedulable=%d want=1", stats.Unschedulable)
	}
	if stats.PlatformFiltered != 1 {
		t.Fatalf("platform_filtered=%d want=1", stats.PlatformFiltered)
	}
	if stats.ModelUnsupported != 1 {
		t.Fatalf("model_unsupported=%d want=1", stats.ModelUnsupported)
	}
	if stats.ModelRateLimited != 1 {
		t.Fatalf("model_rate_limited=%d want=1", stats.ModelRateLimited)
	}
	if stats.Eligible != 1 {
		t.Fatalf("eligible=%d want=1", stats.Eligible)
	}
}

// TestDiagnoseSelectionFailure_UnschedulableDetail disables scheduling on an
// otherwise active provider. Diagnosis reports the unschedulable category and
// record-state detail, protecting the specific explanation emitted for this
// otherwise eligible fixture's disabled scheduling state.
func TestDiagnoseSelectionFailure_UnschedulableDetail(t *testing.T) {
	svc := NewGeneric(GenericDependencies{}, DefaultOptions())
	acc := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 7,
			Platform:    capability.PlatformOpenAI,
			Status:      billing.StatusActive,
			Schedulable: false,
		},
	}

	diagnosis := svc.diagnoseSelectionFailure(context.Background(), acc, "gpt-5.4", capability.PlatformOpenAI, map[int64]struct{}{}, false)
	if diagnosis.Category != "unschedulable" {
		t.Fatalf("category=%s want=unschedulable", diagnosis.Category)
	}
	if diagnosis.Detail != "generic_unschedulable" {
		t.Fatalf("detail=%s want=generic_unschedulable", diagnosis.Detail)
	}
}

// TestDiagnoseSelectionFailure_ModelRateLimitedDetail records a future cooldown
// for the requested model. Diagnosis reports model_rate_limited with a
// remaining-duration detail, protecting the explanation for temporary model
// unavailability.
func TestDiagnoseSelectionFailure_ModelRateLimitedDetail(t *testing.T) {
	svc := NewGeneric(GenericDependencies{}, DefaultOptions())
	model := "gpt-5.4"
	resetAt := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	acc := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 8,
			Platform:    capability.PlatformOpenAI,
			Status:      billing.StatusActive,
			Schedulable: true,
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					model: map[string]any{
						"rate_limit_reset_at": resetAt,
					},
				},
			},
		},
	}

	diagnosis := svc.diagnoseSelectionFailure(context.Background(), acc, model, capability.PlatformOpenAI, map[int64]struct{}{}, false)
	if diagnosis.Category != "model_rate_limited" {
		t.Fatalf("category=%s want=model_rate_limited", diagnosis.Category)
	}
	if !strings.Contains(diagnosis.Detail, "remaining=") {
		t.Fatalf("detail=%s want contains remaining=", diagnosis.Detail)
	}
}

// TestGetSchedulableProvider_AppliesGrokFreeSoftGate supplies explicit Free
// OAuth usage above the configured soft threshold. Initial unknown usage admits
// the target, then tracked refresh makes the generic lookup reject it.
func TestGetSchedulableProvider_AppliesGrokFreeSoftGate(t *testing.T) {
	cfg := &config.Config{}
	cfg.Scheduling.GrokFreeQuota.Enabled = true
	cfg.Scheduling.GrokFreeQuota.TokenLimit = 500_000
	cfg.Scheduling.GrokFreeQuota.ThresholdPercent = 95
	cfg.Scheduling.GrokFreeQuota.WindowHours = 24
	cfg.Scheduling.GrokFreeQuota.StatsCacheSeconds = 60

	provider := healthyGrokOAuthGatewayTestProvider(8801, "tok")
	provider.Record.Credentials["subscription_tier"] = "free"
	provider.Record.Status = billing.StatusActive
	provider.Record.Schedulable = true

	repo := &mockProviderRepoForPlatform{
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{provider.Record.ID: provider},
	}
	usageRepo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usage.ProviderStats{
		provider.Record.ID: {Tokens: 480_000}, // above 95% of 500k
	}}
	// This gate owns an independent usage cache. Track its accepted background
	// queries so test cleanup waits for fixture reads after the first admission check.
	var tasks sync.WaitGroup
	t.Cleanup(tasks.Wait)
	gate := newGrokFreeQuotaTestGate(cfg, usageRepo, func(_ string, work func()) bool {
		tasks.Go(func() { ; work() })
		return true
	})
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, FreeQuota: gate}, cfg)

	got, err := svc.getSchedulableProvider(context.Background(), provider.Record.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "first sticky hit fail-opens while free-gate stats refresh")

	require.Eventually(t, func() bool {
		got, err := svc.getSchedulableProvider(context.Background(), provider.Record.ID)
		return err == nil && got == nil
	}, 2*time.Second, 10*time.Millisecond, "over free soft-gate sticky hit must miss after cache warm")
}

// advancedSchedulerRegressionBool returns a pointer representing a supplied
// boolean group override.
func advancedSchedulerRegressionBool(value bool) *bool { return &value }

// advancedSchedulerRegressionInt returns a pointer representing a supplied
// integer group override.
func advancedSchedulerRegressionInt(value int) *int { return &value }

// advancedSchedulerRegressionFloat returns a pointer representing a supplied
// scoring-weight override.
func advancedSchedulerRegressionFloat(value float64) *float64 { return &value }

// advancedSchedulerRegressionOverrides returns explicit zero weights with
// weighted stickiness and Top-K one. Callers change individual fields for each
// scenario, allowing the tests to distinguish supplied zero overrides from
// inheritance.
func advancedSchedulerRegressionOverrides() routing.GroupAdvancedSchedulerOverrides {
	return routing.GroupAdvancedSchedulerOverrides{
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

// advancedSchedulerRegressionGroup creates an active hydrated advanced group
// with supplied overrides. The platform compatibility input is unused, so the
// fixture's group mode applies independently of later forced-platform
// filtering.
func advancedSchedulerRegressionGroup(id int64, platform string, overrides routing.GroupAdvancedSchedulerOverrides) *routing.Group {
	return &routing.Group{
		ID: id, Name: "advanced", Status: billing.StatusActive, Hydrated: true,
		SchedulerType: routing.GroupSchedulerTypeAdvanced, AdvancedSchedulerOverrides: overrides,
	}
}

// advancedSchedulerRegressionProviderRepo retains the candidate slice and
// indexes its element pointers for full reads. Fixture modifications remain
// visible through both queries, letting tests supply the same records to
// ranking and hydration.
func advancedSchedulerRegressionProviderRepo(providers []gatewayprovider.ExecutionProvider) *mockProviderRepoForPlatform {
	repo := &mockProviderRepoForPlatform{providers: providers, providersByID: make(map[int64]*gatewayprovider.ExecutionProvider, len(providers))}
	for index := range repo.providers {
		repo.providersByID[repo.providers[index].Record.ID] = &repo.providers[index]
	}
	return repo
}

// TestGatewayAdvancedSchedulerKeepsFullLoadCandidatesForWaitAndNoSlotSelection
// supplies eligible providers with full load and failed acquisition. Advanced
// generation returns a wait plan, while auxiliary selection chooses from that
// pool without another slot acquisition.
func TestGatewayAdvancedSchedulerKeepsFullLoadCandidatesForWaitAndNoSlotSelection(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.WeightPriority = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1301, capability.PlatformAnthropic, overrides)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 13011, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 2, Priority: 1}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 13012, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 2, Priority: 2}},
	}
	repo := advancedSchedulerRegressionProviderRepo(providers)
	concurrencyCache := &mockConcurrencyCache{
		acquireResults: map[int64]bool{13011: false, 13012: false},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			13011: {ProviderID: 13011, LoadRate: 100},
			13012: {ProviderID: 13012, LoadRate: 100},
		},
	}
	cfg := testConfig()
	cfg.Scheduling.LoadBatch.Enabled = true
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Groups:    &mockGroupRepoForGateway{groups: map[int64]*routing.Group{group.ID: group}},
			Providers: repo,
		},
		Shared: Shared{
			Cache:       &mockGatewayCacheForPlatform{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	ctx := requeststate.WithGroup(context.Background(), group)

	selection, err := svc.SelectProviderWithLoadAwareness(ctx, &group.ID, "", "claude-sonnet-4", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(13011), selection.Provider.Record.ID)
	require.True(t, selection.AdvancedScheduler)
	acquireCalls := concurrencyCache.acquireProviderCalls

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &group.ID, "", "claude-sonnet-4", nil)

	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(13011), provider.Record.ID)
	require.Equal(t, acquireCalls, concurrencyCache.acquireProviderCalls, "无槽选择不得申请真实并发槽")
}

// TestGatewayAdvancedSchedulerForcePlatformUsesGroupOverrides forces
// Antigravity inside an advanced group whose override favors lower load.
// Selection and result feedback follow that group policy, protecting
// forced-platform scheduling and observation alignment.
func TestGatewayAdvancedSchedulerForcePlatformUsesGroupOverrides(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.WeightLoad = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1401, capability.PlatformAnthropic, overrides)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 14011, Platform: capability.PlatformAntigravity, Status: billing.StatusActive, Schedulable: true, Concurrency: 2, Priority: 1, Extra: map[string]any{"mixed_scheduling": true}}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 14012, Platform: capability.PlatformAntigravity, Status: billing.StatusActive, Schedulable: true, Concurrency: 2, Priority: 100, Extra: map[string]any{"mixed_scheduling": true}}},
	}
	repo := advancedSchedulerRegressionProviderRepo(providers)
	concurrencyCache := &mockConcurrencyCache{
		acquireResults: map[int64]bool{14011: true, 14012: true},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			14011: {ProviderID: 14011, LoadRate: 90},
			14012: {ProviderID: 14012, LoadRate: 0},
		},
	}
	cfg := testConfig()
	cfg.Scheduling.LoadBatch.Enabled = true
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Providers: repo,
			Groups:    &mockGroupRepoForGateway{groups: map[int64]*routing.Group{group.ID: group}},
		},
		Shared: Shared{
			Cache:       &mockGatewayCacheForPlatform{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	ctx := requeststate.WithGroup(context.Background(), group)
	ctx = apikey.WithForcePlatform(ctx, capability.PlatformAntigravity)

	selection, err := svc.SelectProviderWithLoadAwareness(ctx, &group.ID, "force", "gemini-2.5-pro", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(14012), selection.Provider.Record.ID)
	require.True(t, selection.AdvancedScheduler)
	svc.ReportAdvancedProviderScheduleResult(selection, selection.Provider.Record.ID, false, nil)
	require.EqualValues(t, 1, svc.advancedSchedulerStats().FeedbackSnapshot(14012).ErrorSamples)
	require.Zero(t, svc.advancedSchedulerStats().FeedbackSnapshot(14011).ErrorSamples)
}

// TestGatewayAdvancedSchedulerWeightedStickyKeepsStickyOnlyProvider puts the
// bound provider's cost inside its reserved sticky allowance. Advanced weighted
// selection retains that eligible candidate, protecting sticky reserve handling
// before score-based choice.
func TestGatewayAdvancedSchedulerWeightedStickyKeepsStickyOnlyProvider(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.WeightSessionSticky = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1501, capability.PlatformAnthropic, overrides)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 15011, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive,
				Schedulable: true, Concurrency: 2, Extra: map[string]any{"window_cost_limit": 10.0, "window_cost_sticky_reserve": 5.0},
			},
		},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 15012, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 2}},
	}
	repo := advancedSchedulerRegressionProviderRepo(providers)
	cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": 15011}}
	windowCache := &sessionLimitCacheHotpathStub{batchData: map[int64]float64{15011: 11}}
	concurrencyCache := &mockConcurrencyCache{acquireResults: map[int64]bool{15011: true, 15012: true}}
	cfg := testConfig()
	cfg.Scheduling.LoadBatch.Enabled = true
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Providers: repo,
			Groups:    &mockGroupRepoForGateway{groups: map[int64]*routing.Group{group.ID: group}},
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
		Window:                  selectionWindowForTest(windowCache, &usageLogWindowBatchRepoStub{}),
		WindowPrefetchAvailable: true,
	}, cfg)

	ctx := requeststate.WithGroup(context.Background(), group)

	selection, err := svc.SelectProviderWithLoadAwareness(ctx, &group.ID, "sticky", "claude-sonnet-4", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(15011), selection.Provider.Record.ID)
	require.True(t, selection.AdvancedScheduler)
}

// TestGatewayAdvancedSchedulerEscapesNonOpenAIHardSticky raises failure
// feedback beyond the group's escape threshold for a Gemini binding. Advanced
// choice uses the healthy member while retaining the stored affinity,
// protecting request-local escape.
func TestGatewayAdvancedSchedulerEscapesNonOpenAIHardSticky(t *testing.T) {
	overrides := advancedSchedulerRegressionOverrides()
	overrides.StickyWeightedEnabled = advancedSchedulerRegressionBool(false)
	overrides.WeightErrorRate = advancedSchedulerRegressionFloat(1)
	group := advancedSchedulerRegressionGroup(1601, capability.PlatformGemini, overrides)
	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 16011, Platform: capability.PlatformGemini, Status: billing.StatusActive, Schedulable: true, Concurrency: 2, Priority: 1}},
		{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 16012, Platform: capability.PlatformGemini, Status: billing.StatusActive, Schedulable: true, Concurrency: 2, Priority: 2}},
	}
	repo := advancedSchedulerRegressionProviderRepo(providers)
	cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": 16011}}
	concurrencyCache := &mockConcurrencyCache{acquireResults: map[int64]bool{16011: true, 16012: true}}
	cfg := testConfig()
	cfg.Scheduling.LoadBatch.Enabled = true
	cfg.Scheduling.Advanced.StickyEscapeEnabled = true
	cfg.Scheduling.Advanced.StickyEscapeTTFTMs = 15000
	cfg.Scheduling.Advanced.StickyEscapeErrorRate = 0.55
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Providers: repo,
			Groups:    &mockGroupRepoForGateway{groups: map[int64]*routing.Group{group.ID: group}},
		},
		Shared: Shared{
			Feedback: schedulercore.NewRuntimeStats(time.Now),
			Cache:    cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,
				schedulercore.Diagnostics{
					Logf:  logging.LegacyPrintf,
					Event: logging.Event,
				}),
		},
	}, cfg)

	for range 4 {
		svc.advancedSchedulerStats().Report(16011, false, nil)
	}
	ctx := requeststate.WithGroup(context.Background(), group)

	selection, err := svc.SelectProviderWithLoadAwareness(ctx, &group.ID, "sticky", "gemini-3-pro", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(16012), selection.Provider.Record.ID)
	require.Equal(t, int64(16011), cache.sessionBindings["sticky"], "逃逸时保留原粘性绑定")
	require.True(t, selection.AdvancedScheduler)
}

// TestIsProviderInGroup varies absent groups, nil providers, and one or several
// stored memberships. A positive matching request group is required, protecting
// selection from implicit membership or ungrouped admission.
func TestIsProviderInGroup(t *testing.T) {
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}},

		nil)

	groupID100 := int64(100)
	groupID200 := int64(200)

	tests := []struct {
		name     string                             // named eligibility scenario
		provider *gatewayprovider.ExecutionProvider // configured provider policy fixture
		groupID  *int64                             // requested fixture group identifier
		expected bool                               // expected eligibility result
	}{
		// A request with no group ID.
		{
			"nil_groupID_ungrouped_provider_nil_groups",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, ProviderGroups: nil}},
			nil, false,
		},
		{
			"nil_groupID_ungrouped_provider_empty_slice",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, ProviderGroups: []providercore.GroupMembership{}}},
			nil, false,
		},
		{
			"nil_groupID_grouped_provider_single",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3, ProviderGroups: []providercore.GroupMembership{{GroupID: 100}}}},
			nil, false,
		},
		{
			"nil_groupID_grouped_provider_multiple",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 4, ProviderGroups: []providercore.GroupMembership{{GroupID: 100}, {GroupID: 200}}}},
			nil, false,
		},
		// A request with a group ID.
		{
			"with_groupID_provider_in_group",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 5, ProviderGroups: []providercore.GroupMembership{{GroupID: 100}}}},
			&groupID100, true,
		},
		{
			"with_groupID_provider_not_in_group",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 6, ProviderGroups: []providercore.GroupMembership{{GroupID: 200}}}},
			&groupID100, false,
		},
		{
			"with_groupID_ungrouped_provider",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 7, ProviderGroups: nil}},
			&groupID100, false,
		},
		{
			"with_groupID_multi_group_provider_match_one",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 8, ProviderGroups: []providercore.GroupMembership{{GroupID: 100}, {GroupID: 200}}}},
			&groupID200, true,
		},
		{
			"with_groupID_multi_group_provider_no_match",
			&gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 9, ProviderGroups: []providercore.GroupMembership{{GroupID: 300}, {GroupID: 400}}}},
			&groupID100, false,
		},
		// Nil provider or group ID cases.
		{
			"nil_provider_nil_groupID",
			nil,
			nil, false,
		},
		{
			"nil_provider_with_groupID",
			nil,
			&groupID100, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.isProviderInGroup(tt.provider, tt.groupID)
			require.Equal(t, tt.expected, got, "isProviderInGroup 结果不符预期")
		})
	}
}

// TestSelectProviderForModelWithExclusions_UsesAdmittedFallbackGroupForGroupRestriction
// supplies matching fallback group state and route plan as admission inputs.
// Slot-free selection returns the expected provider for the model allowed by
// that fixture's group policy.
func TestSelectProviderForModelWithExclusions_UsesAdmittedFallbackGroupForGroupRestriction(t *testing.T) {
	t.Parallel()

	groupID := int64(10)
	fallbackID := int64(11)
	ch := routingtestkit.Configuration{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{fallbackID},
		RestrictModels: true,
		ModelPricing: []routing.ModelPricingEntry{
			{Models: []string{"claude-sonnet-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{
		fallbackID: capability.PlatformAnthropic,
	}))
	providerRepo := &mockProviderRepoForPlatform{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
	}
	for i := range providerRepo.providers {
		providerRepo.providersByID[providerRepo.providers[i].Record.ID] = &providerRepo.providers[i]
	}
	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{
			groupID: {
				ID: groupID,

				Status:          billing.StatusActive,
				ClaudeCodeOnly:  true,
				FallbackGroupID: &fallbackID,
				Hydrated:        true,
			},
			fallbackID: {
				ID: fallbackID,

				Status:   billing.StatusActive,
				Hydrated: true,
			},
		},
	}

	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Providers: providerRepo,

			Groups: groupRepo,
		},
		Shared: Shared{GroupPolicies: pricingConfigSvc},
	}, testConfig())

	ctx := requeststate.WithGroup(context.Background(), groupRepo.groups[fallbackID])
	ctx = requeststate.WithRoutePlan(ctx, routing.Plan(routing.PlanInput{Group: groupRepo.groups[fallbackID]}))
	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &fallbackID, "", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(1), provider.Record.ID)
}

// TestSelectProviderWithLoadAwareness_UsesAdmittedFallbackGroupForGroupRestriction
// supplies matching fallback group state and route plan as admission inputs.
// Load-aware selection returns the expected provider for the model allowed by
// that fixture's group policy.
func TestSelectProviderWithLoadAwareness_UsesAdmittedFallbackGroupForGroupRestriction(t *testing.T) {
	t.Parallel()

	groupID := int64(10)
	fallbackID := int64(11)
	ch := routingtestkit.Configuration{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{fallbackID},
		RestrictModels: true,
		ModelPricing: []routing.ModelPricingEntry{
			{Models: []string{"claude-sonnet-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{
		fallbackID: capability.PlatformAnthropic,
	}))
	providerRepo := &mockProviderRepoForPlatform{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
	}
	for i := range providerRepo.providers {
		providerRepo.providersByID[providerRepo.providers[i].Record.ID] = &providerRepo.providers[i]
	}
	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{
			groupID: {
				ID: groupID,

				Status:          billing.StatusActive,
				ClaudeCodeOnly:  true,
				FallbackGroupID: &fallbackID,
				Hydrated:        true,
			},
			fallbackID: {
				ID: fallbackID,

				Status:   billing.StatusActive,
				Hydrated: true,
			},
		},
	}

	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Providers: providerRepo,

			Groups: groupRepo,
		},
		Shared: Shared{GroupPolicies: pricingConfigSvc},
	}, testConfig())

	ctx := requeststate.WithGroup(context.Background(), groupRepo.groups[fallbackID])
	ctx = requeststate.WithRoutePlan(ctx, routing.Plan(routing.PlanInput{Group: groupRepo.groups[fallbackID]}))
	result, err := svc.SelectProviderWithLoadAwareness(ctx, &fallbackID, "", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Provider)
	require.Equal(t, int64(1), result.Provider.Record.ID)
}

// TestIsModelSupportedByProviderWithContext_QoderUsesGroupMappedProviderLayerModel
// maps a client alias through group and Qoder provider names. Support checks
// the allowed final identity, protecting model-stage order in provider
// eligibility.
func TestIsModelSupportedByProviderWithContext_QoderUsesGroupMappedProviderLayerModel(t *testing.T) {
	t.Parallel()
	ch := routingtestkit.Configuration{
		ID:           1,
		Status:       billing.StatusActive,
		GroupIDs:     []int64{10},
		ModelMapping: map[string]string{"my-qoder": "qmodel"},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: capability.PlatformQoder}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	ctx := svc.withGroupContext(context.Background(), &routing.Group{
		ID: 10,

		Status:   billing.StatusActive,
		Hydrated: true,
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformQoder,
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"qmodel": "ultimate"},
				"model_whitelist": []any{"ultimate"},
			},
		},
	}

	require.True(t, svc.isModelSupportedByProviderWithContext(ctx, provider, "my-qoder"),
		"provider whitelist should be checked after channel mapping and provider mapping")
}

// GetProviderWindowStatsBatch increments its atomic batch counter and returns
// configured statistics for requested IDs. Configured errors return first;
// result maps are new, while stored statistics pointers remain shared with the
// fixture.
func (s *usageLogWindowBatchRepoStub) GetProviderWindowStatsBatch(ctx context.Context, providerIDs []int64, startTime time.Time) (map[int64]*usage.ProviderStats, error) {
	s.batchCalls.Add(1)
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	out := make(map[int64]*usage.ProviderStats, len(providerIDs))
	for _, id := range providerIDs {
		if stats, ok := s.batchResult[id]; ok {
			out[id] = stats
		}
	}
	return out, nil
}

// GetProviderWindowStats increments the individual-read counter and returns its
// configured result or error. Missing IDs get a fresh zero statistics value,
// distinguishing an empty observation from unavailable fixture storage.
func (s *usageLogWindowBatchRepoStub) GetProviderWindowStats(ctx context.Context, providerID int64, startTime time.Time) (*usage.ProviderStats, error) {
	s.singleCalls.Add(1)
	if s.singleErr != nil {
		return nil, s.singleErr
	}
	if stats, ok := s.singleResult[providerID]; ok {
		return stats, nil
	}
	return &usage.ProviderStats{}, nil
}

// GetWindowCostBatch returns configured cost entries for requested IDs in a new
// map. Configured errors take precedence, while missing entries are omitted so
// prefetch can obtain those provider windows from usage storage.
func (s *sessionLimitCacheHotpathStub) GetWindowCostBatch(ctx context.Context, providerIDs []int64) (map[int64]float64, error) {
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	out := make(map[int64]float64, len(providerIDs))
	for _, id := range providerIDs {
		if v, ok := s.batchData[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

// SetWindowCost records the provider cost in fixture storage unless setErr is
// configured. It allocates the write map when needed, allowing assertions on
// best-effort prefetch cache publication.
func (s *sessionLimitCacheHotpathStub) SetWindowCost(ctx context.Context, providerID int64, cost float64) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.setData == nil {
		s.setData = make(map[int64]float64)
	}
	s.setData[providerID] = cost
	return nil
}

// GetSessionProviderID counts reads atomically and returns the configured
// positive sticky ID. Nonpositive IDs produce a cache-miss error, while group
// and hash inputs are ignored by this single-binding fixture.
func (s *stickyGatewayCacheHotpathStub) GetSessionProviderID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	s.getCalls.Add(1)
	if s.stickyID > 0 {
		return s.stickyID, nil
	}
	return 0, errors.New("not found")
}

// SetSessionProviderID accepts sticky fixture writes and returns success. The
// configured stickyID remains its read result, letting prefetch tests measure
// cache reads independently of any later affinity update.
func (s *stickyGatewayCacheHotpathStub) SetSessionProviderID(ctx context.Context, groupID int64, sessionHash string, providerID int64, ttl time.Duration) error {
	return nil
}

// RefreshSessionTTL accepts sticky fixture expiration calls and returns
// success. This fixture retains one configured ID with no expiry state, so
// refresh does not alter the read-count scenario's target.
func (s *stickyGatewayCacheHotpathStub) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

// DeleteSessionProviderID accepts cleanup for this single-binding fixture and
// returns success. It keeps the configured read ID intact, leaving prefetch
// scenarios governed by their supplied hint and lookup counter.
func (s *stickyGatewayCacheHotpathStub) DeleteSessionProviderID(ctx context.Context, groupID int64, sessionHash string) error {
	return nil
}

// SetSessionOwnerGroupID supplies successful owner admission for this
// sticky-read fixture. Ownership is unmodeled, allowing provider-prefetch
// scenarios to bind sessions without adding user/source persistence
// observations.
func (s *stickyGatewayCacheHotpathStub) SetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error) {
	return true, nil
}

// GetSessionOwnerGroupID returns a cache-miss error because this fixture stores
// no owner records. Prefetch scenarios therefore receive an absent owner even
// after the fixture's permissive write path admits binding.
func (s *stickyGatewayCacheHotpathStub) GetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string) (int64, error) {
	return 0, errors.New("not found")
}

// RefreshSessionOwnerTTL accepts owner refresh and returns success in this
// fixture. It stores no ownership expiration data, leaving the configured
// sticky target and lookup counter unchanged for later assertions.
func (s *stickyGatewayCacheHotpathStub) RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, sessionHash string, ttl time.Duration) error {
	return nil
}

// resetGatewayHotpathStatsForTest resets shared cost-window and group-rate
// counters used by these assertions. Callers serialize scenarios using those
// global observations so another test's reads do not replace the measured
// baseline.
func resetGatewayHotpathStatsForTest() {
	billing.SharedWindowCostMetrics().Hit.Store(0)
	billing.SharedWindowCostMetrics().Miss.Store(0)
	billing.SharedWindowCostMetrics().BatchSQL.Store(0)
	billing.SharedWindowCostMetrics().Fallback.Store(0)
	billing.SharedWindowCostMetrics().Errors.Store(0)

	billing.SharedGroupRateMetrics().Hit.Store(0)
	billing.SharedGroupRateMetrics().Miss.Store(0)
	billing.SharedGroupRateMetrics().Load.Store(0)
	billing.SharedGroupRateMetrics().Shared.Store(0)
	billing.SharedGroupRateMetrics().Fallback.Store(0)
}

// TestWithWindowCostPrefetch_BatchReadAndContextReuse supplies one cached
// Anthropic cost and one usage-store miss beside an inapplicable target.
// Prefetch reuses cache, batches the miss, publishes its result, and returns
// request-scoped observations.
func TestWithWindowCostPrefetch_BatchReadAndContextReuse(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	// Use an hourly fixture anchor for cost-window prefetch grouping.
	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
				Platform:           capability.PlatformAnthropic,
				Type:               capability.ProviderTypeOAuth,
				Extra:              map[string]any{"window_cost_limit": 100.0},
				SessionWindowStart: &windowStart,
				SessionWindowEnd:   &windowEnd,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
				Platform:           capability.PlatformAnthropic,
				Type:               capability.ProviderTypeSetupToken,
				Extra:              map[string]any{"window_cost_limit": 100.0},
				SessionWindowStart: &windowStart,
				SessionWindowEnd:   &windowEnd,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3,
				Platform: capability.PlatformAnthropic,
				Type:     capability.ProviderTypeAPIKey,
				Extra:    map[string]any{"window_cost_limit": 100.0},
			},
		},
	}

	cache := &sessionLimitCacheHotpathStub{
		batchData: map[int64]float64{
			1: 11.0,
		},
	}
	repo := &usageLogWindowBatchRepoStub{
		batchResult: map[int64]*usage.ProviderStats{
			2: {StandardCost: 22.0},
		},
	}
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:                   Reads{},
		Shared:                  Shared{},
		Window:                  selectionWindowForTest(cache, repo),
		WindowPrefetchAvailable: true,
	}, nil)

	outCtx := svc.withWindowCostPrefetch(context.Background(), providers)
	require.NotNil(t, outCtx)

	cost1, ok1 := billing.PrefetchedWindowCost(outCtx, 1)
	require.True(t, ok1)
	require.Equal(t, 11.0, cost1)

	cost2, ok2 := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok2)
	require.Equal(t, 22.0, cost2)

	_, ok3 := billing.PrefetchedWindowCost(outCtx, 3)
	require.False(t, ok3)

	require.Equal(t, int64(1), repo.batchCalls.Load())
	require.Equal(t, 22.0, cache.setData[2])

	hit, miss, batchSQL, fallback, errCount := windowMetricsForTest()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(1), miss)
	require.Equal(t, int64(1), batchSQL)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), errCount)
}

// TestWithWindowCostPrefetch_AllHitNoSQL gives every applicable provider a
// cached window cost. Prefetch returns both observations and makes no usage
// queries, protecting the all-hit batch path and its metrics.
func TestWithWindowCostPrefetch_AllHitNoSQL(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	// Use an hourly fixture anchor for cost-window prefetch grouping.
	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
				Platform:           capability.PlatformAnthropic,
				Type:               capability.ProviderTypeOAuth,
				Extra:              map[string]any{"window_cost_limit": 100.0},
				SessionWindowStart: &windowStart,
				SessionWindowEnd:   &windowEnd,
			},
		},
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
				Platform:           capability.PlatformAnthropic,
				Type:               capability.ProviderTypeSetupToken,
				Extra:              map[string]any{"window_cost_limit": 100.0},
				SessionWindowStart: &windowStart,
				SessionWindowEnd:   &windowEnd,
			},
		},
	}

	cache := &sessionLimitCacheHotpathStub{
		batchData: map[int64]float64{
			1: 11.0,
			2: 22.0,
		},
	}
	repo := &usageLogWindowBatchRepoStub{}
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:                   Reads{},
		Shared:                  Shared{},
		Window:                  selectionWindowForTest(cache, repo),
		WindowPrefetchAvailable: true,
	}, nil)

	outCtx := svc.withWindowCostPrefetch(context.Background(), providers)
	cost1, ok1 := billing.PrefetchedWindowCost(outCtx, 1)
	cost2, ok2 := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok1)
	require.True(t, ok2)
	require.Equal(t, 11.0, cost1)
	require.Equal(t, 22.0, cost2)
	require.Equal(t, int64(0), repo.batchCalls.Load())
	require.Equal(t, int64(0), repo.singleCalls.Load())

	hit, miss, batchSQL, fallback, errCount := windowMetricsForTest()
	require.Equal(t, int64(2), hit)
	require.Equal(t, int64(0), miss)
	require.Equal(t, int64(0), batchSQL)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), errCount)
}

// TestWithWindowCostPrefetch_BatchErrorFallbackSingleQuery injects failed batch
// usage reading with an available individual result. Prefetch retains that cost
// and records fallback/error counters, protecting recovery after the grouped
// query fails.
func TestWithWindowCostPrefetch_BatchErrorFallbackSingleQuery(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	// Use an hourly fixture anchor for cost-window prefetch grouping.
	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
				Platform:           capability.PlatformAnthropic,
				Type:               capability.ProviderTypeSetupToken,
				Extra:              map[string]any{"window_cost_limit": 100.0},
				SessionWindowStart: &windowStart,
				SessionWindowEnd:   &windowEnd,
			},
		},
	}

	cache := &sessionLimitCacheHotpathStub{}
	repo := &usageLogWindowBatchRepoStub{
		batchErr: errors.New("batch failed"),
		singleResult: map[int64]*usage.ProviderStats{
			2: {StandardCost: 33.0},
		},
	}
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:                   Reads{},
		Shared:                  Shared{},
		Window:                  selectionWindowForTest(cache, repo),
		WindowPrefetchAvailable: true,
	}, nil)

	outCtx := svc.withWindowCostPrefetch(context.Background(), providers)
	cost, ok := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok)
	require.Equal(t, 33.0, cost)
	require.Equal(t, int64(1), repo.batchCalls.Load())
	require.Equal(t, int64(1), repo.singleCalls.Load())

	_, _, _, fallback, errCount := windowMetricsForTest()
	require.Equal(t, int64(1), fallback)
	require.Equal(t, int64(1), errCount)
}

// TestSelectProviderWithLoadAwareness_StickyReadReuse varies absent, matching,
// and mismatched group-prefetch hints. Basic selection reads affinity once when
// needed and skips it for matching prefetch, protecting group-specific request
// observation reuse.
func TestSelectProviderWithLoadAwareness_StickyReadReuse(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 88,
			Platform:    capability.PlatformAnthropic,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 4,
			Priority:    1,
			LastUsedAt:  &now,
		},
	}

	repo := selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{provider}}
	concurrency := schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event,
	},
	)

	cfg := &config.Config{Scheduling: config.SchedulingConfig{
		LoadBatch: config.SchedulingLoadBatchConfig{Enabled: true},
		StickyProviderWait: config.ProviderWaitConfig{
			MaxWaiting:     3,
			TimeoutSeconds: 1,
		},
		AlternateProviderWait: config.ProviderWaitConfig{
			TimeoutSeconds: 1,
			MaxWaiting:     10,
		},
	}}

	baseCtx := apikey.WithForcePlatform(context.Background(), capability.PlatformAnthropic)

	t.Run("without_prefetch_reads_cache_once", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: provider.Record.ID}
		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},

			Shared: Shared{Cache: cache, Concurrency: concurrency},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(baseCtx, selectionFixtureGroupID(baseCtx), "sess-hash", "", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, provider.Record.ID, result.Provider.Record.ID)
		require.Equal(t, int64(1), cache.getCalls.Load())
	})

	t.Run("with_prefetch_skips_cache_read", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: provider.Record.ID}
		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},

			Shared: Shared{Cache: cache, Concurrency: concurrency},
		}, cfg)

		ctx := requeststate.WithPrefetchedStickySession(baseCtx, provider.Record.ID, *selectionFixtureGroupID(baseCtx))
		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "sess-hash", "", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, provider.Record.ID, result.Provider.Record.ID)
		require.Equal(t, int64(0), cache.getCalls.Load())
	})

	t.Run("with_prefetch_group_mismatch_reads_cache", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: provider.Record.ID}
		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},

			Shared: Shared{Cache: cache, Concurrency: concurrency},
		}, cfg)

		ctx := requeststate.WithPrefetchedStickySession(baseCtx, 999, 77)
		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "sess-hash", "", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, provider.Record.ID, result.Provider.Record.ID)
		require.Equal(t, int64(1), cache.getCalls.Load())
	})
}

// windowMetricsForTest reads shared hit, miss, batch-query, fallback, and error
// counters in assertion order.
func windowMetricsForTest() (int64, int64, int64, int64, int64) {
	m := billing.SharedWindowCostMetrics()
	return m.Hit.Load(), m.Miss.Load(), m.BatchSQL.Load(), m.Fallback.Load(), m.Errors.Load()
}

// TestGatewayService_SelectProviderForModelWithExclusions_ForcePlatform
// supplies Anthropic and Antigravity candidates with authenticated
// forced-platform context. Selection returns the Antigravity member, protecting
// that filter before the normal group model choice.
func TestGatewayService_SelectProviderForModelWithExclusions_ForcePlatform(t *testing.T) {
	ctx := context.Background()
	ctx = apikey.WithForcePlatform(ctx, capability.PlatformAntigravity)

	repo := &mockProviderRepoForPlatform{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
	}
	for i := range repo.providers {
		repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "claude-sonnet-4-5", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
	require.Equal(t, capability.PlatformAntigravity, acc.Record.Platform)
}

// TestGatewayService_isModelSupportedByProvider varies platform defaults,
// custom mapping, and unmatched names with empty scopes. Provider policy
// returns each expected support result, protecting the configured eligibility
// cases independently of selection.
func TestGatewayService_isModelSupportedByProvider(t *testing.T) {
	tests := []struct {
		name     string                             // named eligibility scenario
		provider *gatewayprovider.ExecutionProvider // configured provider policy fixture
		model    string                             // requested model support identity
		expected bool                               // expected eligibility result
	}{
		{
			name:     "Antigravity平台-支持默认映射中的claude模型",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "claude-sonnet-4-5",
			expected: true,
		},
		{
			name:     "Antigravity平台-空白名单允许未命中映射的claude模型",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name:     "Antigravity平台-支持gemini模型",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name:     "Antigravity平台-空白名单允许gpt模型",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "gpt-4",
			expected: true,
		},
		{
			name:     "Anthropic平台-无映射配置-支持所有模型",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic}},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name: "Anthropic平台-有映射配置-未命中映射时按透传支持模型",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-opus-4": "x"}},
				},
			},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name: "Anthropic平台-有映射配置-支持配置的模型",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-sonnet-20241022": "x"}},
				},
			},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name:     "Gemini平台-无映射配置-支持所有模型",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini, Type: capability.ProviderTypeAPIKey}},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name: "Gemini平台-有映射配置-未命中映射时按透传支持模型",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini,
					Type: capability.ProviderTypeAPIKey,
					Credentials: map[string]any{
						"model_mapping": map[string]any{"gemini-2.5-pro": "upstream-model"},
					},
				},
			},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name: "Gemini平台-有映射配置-支持配置的模型",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini,
					Type: capability.ProviderTypeAPIKey,
					Credentials: map[string]any{
						"model_mapping": map[string]any{"gemini-2.5-pro": "gemini-2.5-pro"},
					},
				},
			},
			model:    "gemini-2.5-pro",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gatewayprovider.ExecutionModelPolicy(tt.provider).Supports(context.Background(), tt.model)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestGenericGroupIncludesAntigravityWithoutMixedFlag supplies Antigravity with
// a false retired mixed-scheduling flag inside a normal group. It participates
// in selection, while forcing Anthropic still narrows the same group to that
// platform.
func TestGenericGroupIncludesAntigravityWithoutMixedFlag(t *testing.T) {
	groupID := int64(1)
	values := []gatewayprovider.ExecutionProvider{
		mixedGroupProvider(1, capability.PlatformAnthropic, "*", groupID),
		mixedGroupProvider(2, capability.PlatformAntigravity, "*", groupID),
	}
	values[0].Record.Priority = 2
	values[1].Record.Type = capability.ProviderTypeOAuth
	values[1].Record.Extra = map[string]any{"mixed_scheduling": false}
	repo := advancedSchedulerRegressionProviderRepo(values)
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}}, nil)
	ctx := requeststate.WithGroup(context.Background(), &routing.Group{ID: groupID, Status: billing.StatusActive, Hydrated: true})
	selected, err := svc.SelectProviderForModelWithExclusions(ctx, &groupID, "", "claude-sonnet-4-5", nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Record.ID)
	forced := apikey.WithForcePlatform(ctx, capability.PlatformAnthropic)
	selected, err = svc.SelectProviderForModelWithExclusions(forced, &groupID, "", "claude-sonnet-4-5", nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Record.ID)
}

// TestSelectProviderWithLoadAwareness_FiltersUpstreamRestrictedProviders gives
// sticky and model-preferred shortcuts different final provider mappings. Both
// batch and basic paths choose the allowed upstream identity, protecting group
// restrictions before acquisition.
func TestSelectProviderWithLoadAwareness_FiltersUpstreamRestrictedProviders(t *testing.T) {
	for _, loadBatchEnabled := range []bool{false, true} {
		loadMode := "旧版调度"
		if loadBatchEnabled {
			loadMode = "负载批量调度"
		}
		for _, modelRoutingEnabled := range []bool{false, true} {
			stickyMode := "普通粘性提供商"
			if modelRoutingEnabled {
				stickyMode = "模型路由粘性提供商"
			}
			t.Run(loadMode+"/"+stickyMode, func(t *testing.T) {
				groupID := int64(4210)
				pricingConfig := routingtestkit.Configuration{
					ID:                 76,
					Status:             billing.StatusActive,
					RestrictModels:     true,
					BillingModelSource: routing.BillingModelSourceUpstream,
					ModelMapping:       map[string]string{"client-alias": "group-model"},
					ModelPricing: []routing.ModelPricingEntry{{
						Models: []string{"allowed-upstream"},
					}},
				}
				providers := []gatewayprovider.ExecutionProvider{
					{
						Record: providercore.Record{
							LoadLocation: time.LoadLocation, ID: 1,
							Platform:    capability.PlatformAnthropic,
							Priority:    1,
							Status:      billing.StatusActive,
							Schedulable: true,
							Concurrency: 5,
							ProviderGroups: []providercore.GroupMembership{{
								ProviderID: 1,
								GroupID:    groupID,
							}},
							Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "blocked-upstream"}},
						},
					},
					{
						Record: providercore.Record{
							LoadLocation: time.LoadLocation, ID: 2,
							Platform:    capability.PlatformAnthropic,
							Priority:    2,
							Status:      billing.StatusActive,
							Schedulable: true,
							Concurrency: 5,
							ProviderGroups: []providercore.GroupMembership{{
								ProviderID: 2,
								GroupID:    groupID,
							}},
							Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "allowed-upstream"}},
						},
					},
				}
				providerRepo := &mockProviderRepoForPlatform{providers: providers, providersByID: map[int64]*gatewayprovider.ExecutionProvider{}}
				for i := range providerRepo.providers {
					providerRepo.providersByID[providerRepo.providers[i].Record.ID] = &providerRepo.providers[i]
				}
				group := &routing.Group{
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: modelRoutingEnabled,
				}
				if modelRoutingEnabled {
					group.ModelRouting = map[string][]int64{"group-model": {1, 2}}
				}

				cfg := testConfig()
				cfg.Scheduling.LoadBatch.Enabled = loadBatchEnabled
				svc := newGenericSelectionForTest(GenericDependencies{
					Reads: Reads{
						Providers: providerRepo,

						Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: group}},
					},
					Shared: Shared{
						Concurrency: schedulercore.NewConcurrencyService(&mockConcurrencyCache{}, schedulercore.Diagnostics{
							Logf:  logging.LegacyPrintf,
							Event: logging.Event,
						}),
						GroupPolicies: routingtestkit.PricingConfig(groupID,
							capability.PlatformAnthropic, pricingConfig),
						Cache: &mockGatewayCacheForPlatform{
							sessionBindings: map[string]int64{"sticky": 1},
						},
					},
				}, cfg)

				result, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "sticky", "client-alias", nil)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, int64(2), result.Provider.Record.ID)
			})
		}
	}
}

// TestSelectProviderWithLoadAwareness_AppliesGroupMappingOnce supplies a client
// alias and a second-hop group-mapping trap. Selection uses the provider
// supporting the first intermediate identity, protecting single-hop group
// routing before provider policy.
func TestSelectProviderWithLoadAwareness_AppliesGroupMappingOnce(t *testing.T) {
	groupID := int64(4212)
	pricingConfig := routingtestkit.Configuration{
		ID:           78,
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"client-alias": "group-model", "group-model": "double-mapped-model"},
	}
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Platform:    capability.PlatformGemini,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 5,
			ProviderGroups: []providercore.GroupMembership{{
				ProviderID: 1,
				GroupID:    groupID,
			}},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"group-model": "upstream-model"},
				"model_whitelist": []any{"upstream-model"},
			},
		},
	}
	providerRepo := &mockProviderRepoForPlatform{
		providers:     []gatewayprovider.ExecutionProvider{provider},
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{provider.Record.ID: &provider},
	}
	group := &routing.Group{
		ID: groupID,

		Status:   billing.StatusActive,
		Hydrated: true,
	}
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Providers: providerRepo,

			Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: group}},
		},
		Shared: Shared{GroupPolicies: routingtestkit.PricingConfig(groupID,
			capability.PlatformGemini, pricingConfig)},
	}, testConfig())

	result, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "client-alias", nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, provider.Record.ID, result.Provider.Record.ID)
}

// TestGatewayService_SelectProviderWithLoadAwareness varies sticky affinity,
// model preferences, status, slot outcomes, load observations, and admitted
// groups. Results choose eligible members or wait plans, protecting basic
// ordering, fallback, and resource preparation across those paths.
func TestGatewayService_SelectProviderWithLoadAwareness(t *testing.T) {
	ctx := context.Background()

	t.Run("禁用负载批量查询-降级到传统选择", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, Shared: Shared{Cache: cache, Concurrency: nil}}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(1), result.Provider.Record.ID, "应选择优先级最高的提供商")
	})

	t.Run("模型路由-无ConcurrencyService也生效", func(t *testing.T) {
		groupID := int64(1)
		sessionHash := "sticky"

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, ProviderGroups: []providercore.GroupMembership{{GroupID: groupID}}}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, ProviderGroups: []providercore.GroupMembership{{GroupID: groupID}}}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-a": {1},
						"claude-b": {2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{Cache: cache, Concurrency: nil},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, sessionHash, "claude-b", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID, "切换到 claude-b 时应按模型路由切换提供商")
		require.Equal(t, int64(2), cache.sessionBindings[sessionHash], "粘性绑定应更新为路由选择的提供商")
	})

	t.Run("无ConcurrencyService-降级到传统选择", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, Shared: Shared{Cache: cache, Concurrency: nil}}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID, "应选择优先级最高的提供商")
	})

	t.Run("排除提供商-不选择被排除的提供商", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, Shared: Shared{Concurrency: nil, Cache: cache}}, cfg)

		excludedIDs := map[int64]struct{}{1: {}}
		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "", "claude-3-5-sonnet-20241022", excludedIDs)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID, "不应选择被排除的提供商")
	})

	t.Run("粘性命中-不调用GetByID", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "sticky", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(1), result.Provider.Record.ID)
		require.Equal(t, 0, repo.getByIDCalls, "粘性命中不应调用GetByID")
		require.Equal(t, 0, concurrencyCache.loadBatchCalls, "粘性命中应在负载批量查询前返回")
	})

	t.Run("粘性提供商不在候选集-回退负载感知选择", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "sticky", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID, "粘性提供商不在候选集时应回退到可用提供商")
		require.Equal(t, 0, repo.getByIDCalls, "粘性提供商缺失不应回退到GetByID")
		require.Equal(t, 1, concurrencyCache.loadBatchCalls, "应继续进行负载批量查询")
	})

	t.Run("粘性提供商禁用-清理会话并回退选择", func(t *testing.T) {
		testCtx := apikey.WithForcePlatform(ctx, capability.PlatformAnthropic)
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: false, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}
		repo.listPlatformFunc = func(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
			return repo.providers, nil
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(testCtx, selectionFixtureGroupID(testCtx), "sticky", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID, "粘性提供商禁用时应回退到可用提供商")
		updatedID, ok := cache.sessionBindings["sticky"]
		require.True(t, ok, "粘性会话应更新绑定")
		require.Equal(t, int64(2), updatedID, "粘性会话应绑定到新提供商")
	})

	t.Run("无可用提供商-返回错误", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers:     []gatewayprovider.ExecutionProvider{},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, Shared: Shared{Concurrency: nil, Cache: cache}}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "", "claude-3-5-sonnet-20241022", nil)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
	})

	t.Run("过滤不可调度提供商-限流提供商被跳过", func(t *testing.T) {
		now := time.Now()
		resetAt := now.Add(10 * time.Minute)

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, RateLimitResetAt: &resetAt}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}
		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, Shared: Shared{Cache: cache, Concurrency: nil}}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID, "应跳过限流提供商，选择可用提供商")
	})

	t.Run("过滤不可调度提供商-过载提供商被跳过", func(t *testing.T) {
		now := time.Now()
		overloadUntil := now.Add(10 * time.Minute)

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, OverloadUntil: &overloadUntil}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}
		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo}, Shared: Shared{Concurrency: nil, Cache: cache}}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID, "应跳过过载提供商，选择可用提供商")
	})

	t.Run("粘性提供商槽位满-返回粘性等待计划", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true
		cfg.Scheduling.StickyProviderWait.MaxWaiting = 1

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false},
			waitCounts:     map[int64]int{1: 0},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "sticky", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Provider.Record.ID)
		require.Equal(t, 0, concurrencyCache.loadBatchCalls)
	})

	t.Run("负载批量查询失败-降级旧顺序选择", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadBatchErr: errors.New("load batch failed"),
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "legacy", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID)
		require.Equal(t, int64(2), cache.sessionBindings["legacy"])
	})

	t.Run("模型路由-粘性提供商等待计划", func(t *testing.T) {
		groupID := int64(20)
		sessionHash := "route-sticky"

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true
		cfg.Scheduling.StickyProviderWait.MaxWaiting = 1

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false},
			waitCounts:     map[int64]int{1: 0},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{
						Logf:  logging.LegacyPrintf,
						Event: logging.Event,
					}),
				Cache: cache,
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, sessionHash, "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Provider.Record.ID)
	})

	t.Run("模型路由-粘性提供商命中", func(t *testing.T) {
		groupID := int64(20)
		sessionHash := "route-hit"

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(
					concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, sessionHash, "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(1), result.Provider.Record.ID)
		require.Equal(t, 0, concurrencyCache.loadBatchCalls)
	})

	t.Run("模型路由-粘性提供商缺失-清理并回退", func(t *testing.T) {
		groupID := int64(22)
		sessionHash := "route-missing"

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{
				Groups:    groupRepo,
				Providers: repo,
			},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(
					concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, sessionHash, "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID)
		require.Equal(t, 1, cache.deletedSessions[sessionHash])
		require.Equal(t, int64(2), cache.sessionBindings[sessionHash])
	})

	t.Run("模型路由-按负载选择提供商", func(t *testing.T) {
		groupID := int64(21)

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*schedulercore.ProviderLoadInfo{
				1: {ProviderID: 1, LoadRate: 80},
				2: {ProviderID: 2, LoadRate: 20},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{
						Logf:  logging.LegacyPrintf,
						Event: logging.Event,
					}),
				Cache: cache,
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, "route", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID)
		require.Equal(t, int64(2), cache.sessionBindings["route"])
	})

	t.Run("模型路由-路由提供商全满返回等待计划", func(t *testing.T) {
		groupID := int64(23)

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false, 2: false},
			loadMap: map[int64]*schedulercore.ProviderLoadInfo{
				1: {ProviderID: 1, LoadRate: 10},
				2: {ProviderID: 2, LoadRate: 20},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{
						Logf:  logging.LegacyPrintf,
						Event: logging.Event,
					}),
				Cache: cache,
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, "route-full", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Provider.Record.ID)
	})

	t.Run("模型路由-路由提供商全满-回退普通选择", func(t *testing.T) {
		groupID := int64(22)

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformAnthropic, Priority: 0, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*schedulercore.ProviderLoadInfo{
				1: {ProviderID: 1, LoadRate: 100},
				2: {ProviderID: 2, LoadRate: 100},
				3: {ProviderID: 3, LoadRate: 0},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(
					concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, "fallback", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(3), result.Provider.Record.ID)
		require.Equal(t, int64(3), cache.sessionBindings["fallback"])
	})

	t.Run("负载批量失败且无法获取-兜底等待", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadBatchErr:   errors.New("load batch failed"),
			acquireResults: map[int64]bool{1: false, 2: false},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Provider.Record.ID)
	})

	t.Run("跨平台基础排序-同优先级候选均可调度", func(t *testing.T) {
		groupID := int64(24)

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Type: capability.ProviderTypeAPIKey}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Type: capability.ProviderTypeOAuth}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:   billing.StatusActive,
					Hydrated: true,
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*schedulercore.ProviderLoadInfo{
				1: {ProviderID: 1, LoadRate: 10},
				2: {ProviderID: 2, LoadRate: 10},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(
					concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, "gemini", "gemini-2.5-pro", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Contains(t, []int64{1, 2}, result.Provider.Record.ID)
	})

	t.Run("模型路由-过滤路径覆盖", func(t *testing.T) {
		groupID := int64(70)
		now := time.Now().Add(10 * time.Minute)
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: false, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 4, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{
					Record: providercore.Record{
						Credentials:  map[string]any{"model_whitelist": []string{"*"}},
						LoadLocation: time.LoadLocation, ID: 5,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 5,
						Extra: map[string]any{
							"model_rate_limits": map[string]any{
								"claude-3-5-sonnet-20241022": map[string]any{
									"rate_limit_reset_at": now.Format(time.RFC3339),
								},
							},
						},
					},
				},
				{
					Record: providercore.Record{
						LoadLocation: time.LoadLocation, ID: 6,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 5,
						Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-haiku-20241022": "claude-3-5-haiku-20241022"}},
					},
				},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 7, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2, 3, 4, 5, 6},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(
					concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		excluded := map[int64]struct{}{1: {}}
		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, "", "claude-3-5-sonnet-20241022", excluded)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(4), result.Provider.Record.ID)
	})

	t.Run("ClaudeCode限制-入口已授权回退分组", func(t *testing.T) {
		groupID := int64(60)
		fallbackID := int64(61)

		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:         billing.StatusActive,
					Hydrated:       true,
					ClaudeCodeOnly: true,
					FallbackGroupID: func() *int64 {
						v := fallbackID
						return &v
					}(),
				},
				fallbackID: {
					ID: fallbackID,

					Status:   billing.StatusActive,
					Hydrated: true,
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = false

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Providers: repo, Groups: groupRepo},
			Shared: Shared{Cache: &mockGatewayCacheForPlatform{}, Concurrency: nil},
		},

			cfg)

		admitted := requeststate.WithGroup(ctx, groupRepo.groups[fallbackID])
		admitted = requeststate.WithRoutePlan(admitted, routing.Plan(routing.PlanInput{Group: groupRepo.groups[fallbackID]}))
		result, err := svc.SelectProviderWithLoadAwareness(admitted, &fallbackID, "", "gemini-2.5-pro", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(1), result.Provider.Record.ID)
	})

	t.Run("ClaudeCode限制-无降级返回错误", func(t *testing.T) {
		groupID := int64(62)

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID: groupID,

					Status:         billing.StatusActive,
					Hydrated:       true,
					ClaudeCodeOnly: true,
				},
			},
		}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = false

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Providers: &mockProviderRepoForPlatform{}, Groups: groupRepo},
			Shared: Shared{Cache: &mockGatewayCacheForPlatform{}, Concurrency: nil},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, routing.ErrClaudeCodeOnly)
	})

	t.Run("负载可用但无法获取槽位-兜底等待", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false, 2: false},
			loadMap: map[int64]*schedulercore.ProviderLoadInfo{
				1: {ProviderID: 1, LoadRate: 10},
				2: {ProviderID: 2, LoadRate: 20},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "wait", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Provider.Record.ID)
	})

	t.Run("负载信息缺失-使用默认负载", func(t *testing.T) {
		repo := &mockProviderRepoForPlatform{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
		}
		for i := range repo.providers {
			repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Scheduling.LoadBatch.Enabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*schedulercore.ProviderLoadInfo{
				1: {ProviderID: 1, LoadRate: 50},
			},
			skipDefaultLoad: true,
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Providers: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: schedulercore.NewConcurrencyService(concurrencyCache,

					schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectProviderWithLoadAwareness(ctx, selectionFixtureGroupID(ctx), "missing-load", "claude-3-5-sonnet-20241022", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Provider)
		require.Equal(t, int64(2), result.Provider.Record.ID)
	})
}

// TestGatewayService_GroupResolution_ReusesContextGroup supplies a valid
// hydrated request group. Selection avoids a lightweight admission read while
// retaining the separate full privacy-policy read, protecting the distinction
// between those group consumers.
func TestGatewayService_GroupResolution_ReusesContextGroup(t *testing.T) {
	ctx := context.Background()
	groupID := int64(42)
	group := &routing.Group{
		ID: groupID,

		Status:   billing.StatusActive,
		Hydrated: true,
	}
	ctx = requeststate.WithGroup(ctx, group)

	repo := &mockProviderRepoForPlatform{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
	}
	for i := range repo.providers {
		repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
	}

	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{groupID: group},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo, Groups: groupRepo}, Shared: Shared{}}, testConfig())

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, 1, groupRepo.getByIDCalls) // The separate privacy check reads the full group.
	require.Equal(t, 0, groupRepo.getByIDLiteCalls)
}

// TestGatewayService_GroupResolution_IgnoresInvalidContextGroup supplies an
// incomplete group snapshot and a hydrated repository record. Selection fetches
// lightweight admission data and the full privacy rule, protecting
// incomplete-context replacement at each consumer.
func TestGatewayService_GroupResolution_IgnoresInvalidContextGroup(t *testing.T) {
	ctx := context.Background()
	groupID := int64(42)
	ctxGroup := &routing.Group{
		ID: groupID,

		Status: billing.StatusActive,
	}
	ctx = requeststate.WithGroup(ctx, ctxGroup)

	repo := &mockProviderRepoForPlatform{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
	}
	for i := range repo.providers {
		repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
	}

	group := &routing.Group{
		ID: groupID,

		Status:   billing.StatusActive,
		Hydrated: true,
	}
	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{groupID: group},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo, Groups: groupRepo}, Shared: Shared{}}, testConfig())

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, 1, groupRepo.getByIDCalls) // The separate privacy check reads the full group.
	require.Equal(t, 1, groupRepo.getByIDLiteCalls)
}

// TestGatewayService_GroupContext_OverwritesInvalidContextGroup replaces a
// partial request group with hydrated data. Request state returns equal values
// through a separate snapshot pointer, protecting group copying before
// selection uses its settings.
func TestGatewayService_GroupContext_OverwritesInvalidContextGroup(t *testing.T) {
	groupID := int64(42)
	invalidGroup := &routing.Group{
		ID: groupID,

		Status: billing.StatusActive,
	}
	hydratedGroup := &routing.Group{
		ID: groupID,

		Status:   billing.StatusActive,
		Hydrated: true,
	}

	ctx := requeststate.WithGroup(context.Background(), invalidGroup)
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	ctx = svc.withGroupContext(ctx, hydratedGroup)

	got, ok := requeststate.GroupFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, hydratedGroup, got)
	require.NotSame(t, hydratedGroup, got, "分组状态保存独立快照")
}

// TestGatewayService_GroupResolution_RejectsImplicitFallback supplies a
// restricted source group with an unadmitted fallback. Selection rejects the
// client and makes no fallback group reads, protecting entry ownership of
// fallback admission.
func TestGatewayService_GroupResolution_RejectsImplicitFallback(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	fallbackID := int64(11)
	group := &routing.Group{
		ID: groupID,

		Status:          billing.StatusActive,
		ClaudeCodeOnly:  true,
		FallbackGroupID: &fallbackID,
		Hydrated:        true,
	}
	fallbackGroup := &routing.Group{
		ID: fallbackID,

		Status:   billing.StatusActive,
		Hydrated: true,
	}
	ctx = requeststate.WithGroup(ctx, group)

	repo := &mockProviderRepoForPlatform{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{},
	}
	for i := range repo.providers {
		repo.providersByID[repo.providers[i].Record.ID] = &repo.providers[i]
	}

	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{fallbackID: fallbackGroup},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: repo, Groups: groupRepo}, Shared: Shared{}}, testConfig())

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil)
	require.ErrorIs(t, err, routing.ErrClaudeCodeOnly)
	require.Nil(t, provider)
	require.Zero(t, groupRepo.getByIDCalls)
	require.Zero(t, groupRepo.getByIDLiteCalls)
}

// TestGatewayService_isModelSupportedByProvider_AntigravityModelMapping
// supplies wildcard mappings with a narrow final-model allowlist. Matched
// aliases pass and unrelated names fail, protecting the final identity checked
// by provider support rules.
func TestGatewayService_isModelSupportedByProvider_AntigravityModelMapping(t *testing.T) {
	// Wildcard mappings resolve to models on the provider allowlist.
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity,
			Credentials: map[string]any{
				"model_whitelist": []string{"claude-sonnet-4-5", "gemini-3-flash"},
				"model_mapping": map[string]any{
					"claude-*":   "claude-sonnet-4-5",
					"gemini-3-*": "gemini-3-flash",
				},
			},
		},
	}

	// claude-* maps to an allowed model.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-sonnet-4-5"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-haiku-4-5"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-opus-4-6"))

	// gemini-3-* maps to an allowed model.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gemini-3-flash"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gemini-3-pro-high"))

	// gemini-2.5-* has no mapping to an allowed model.
	require.False(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gemini-2.5-flash"))
	require.False(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gemini-2.5-pro"))

	// The unrelated platform model is excluded.
	require.False(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gpt-4"))

	// An empty model skips filtering.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), ""))
}

// TestGatewayService_isModelSupportedByProvider_AntigravityNoMapping supplies
// neither a custom mapping nor explicit allowlist. Default-mapped and unmatched
// names pass, protecting the provider policy's empty-scope behavior for these
// inputs.
func TestGatewayService_isModelSupportedByProvider_AntigravityNoMapping(t *testing.T) {
	// Without an allowlist, both default-mapped and unmapped models pass.
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity,
			Credentials: map[string]any{},
		},
	}

	// Default-mapped models are accepted.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-sonnet-4-5"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gemini-3-flash"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gemini-2.5-pro"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-haiku-4-5"))

	// Unmapped models pass with their requested names.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-3-5-sonnet-20241022"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-unknown-model"))

	// An empty allowlist also admits names associated with other platforms.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gpt-4"))
}

// TestGatewayService_isModelSupportedByProviderWithContext_ThinkingMode varies
// thinking context, mappings, and allowed Sonnet or Opus identities. Support
// follows each platform's actual suffix rule and final scope, protecting
// context-sensitive eligibility.
func TestGatewayService_isModelSupportedByProviderWithContext_ThinkingMode(t *testing.T) {
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}},

		nil)

	tests := []struct {
		name            string         // named thinking scenario
		modelMapping    map[string]any // configured provider model mapping
		requestedModel  string         // requested client model identity
		thinkingEnabled bool           // request thinking mode flag
		expected        bool           // expected model support result
	}{
		// A thinking-enabled Sonnet request with only the thinking variant configured.
		// The allowlist uses the normalized final model.
		{
			name: "thinking_enabled_matches_final_whitelist",
			modelMapping: map[string]any{
				"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
			},
			requestedModel:  "claude-sonnet-4-5",
			thinkingEnabled: true,
			expected:        true,
		},
		// A thinking-disabled Sonnet request with only the thinking variant configured.
		// The base model is absent from the final-model allowlist.
		{
			name: "thinking_disabled_no_base_mapping_returns_false",
			modelMapping: map[string]any{
				"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
			},
			requestedModel:  "claude-sonnet-4-5",
			thinkingEnabled: false,
			expected:        false,
		},
		// A thinking-enabled Sonnet request with only the base model configured.
		// The final thinking variant is absent from the allowlist.
		{
			name: "thinking_enabled_no_match_non_thinking_mapping",
			modelMapping: map[string]any{
				"claude-sonnet-4-5": "claude-sonnet-4-5",
			},
			requestedModel:  "claude-sonnet-4-5",
			thinkingEnabled: true,
			expected:        false,
		},
		// With both models configured, enabled thinking selects the thinking variant.
		{
			name: "both_models_thinking_enabled_matches_thinking",
			modelMapping: map[string]any{
				"claude-sonnet-4-5":          "claude-sonnet-4-5",
				"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
			},
			requestedModel:  "claude-sonnet-4-5",
			thinkingEnabled: true,
			expected:        true,
		},
		// With both models configured, disabled thinking selects the base model.
		{
			name: "both_models_thinking_disabled_matches_non_thinking",
			modelMapping: map[string]any{
				"claude-sonnet-4-5":          "claude-sonnet-4-5",
				"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
			},
			requestedModel:  "claude-sonnet-4-5",
			thinkingEnabled: false,
			expected:        true,
		},
		// The claude-* wildcard admits both base and thinking variants.
		{
			name: "wildcard_matches_thinking",
			modelMapping: map[string]any{
				"claude-*": "claude-sonnet-4-5",
			},
			requestedModel:  "claude-sonnet-4-5",
			thinkingEnabled: true,
			expected:        true, // The thinking variant matches claude-*.
		},
		// A thinking-only Opus allowlist does not admit the base model.
		// Opus 4.6 does not gain a thinking suffix automatically.
		{
			name: "opus_without_suffix_rewrite_does_not_match_thinking_only_scope",
			modelMapping: map[string]any{
				"claude-opus-4-6-thinking": "claude-opus-4-6-thinking",
			},
			requestedModel:  "claude-opus-4-6",
			thinkingEnabled: true,
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity,
					Credentials: map[string]any{
						"model_whitelist": func() []string {
							var out []string
							for source, value := range tt.modelMapping {
								if strings.Contains(source, "*") {
									out = append(out, source)
									continue
								}
								if model, ok := value.(string); ok {
									out = append(out, model)
								}
							}
							return out
						}(),
						"model_mapping": tt.modelMapping,
					},
				},
			}

			ctx := requeststate.WithThinkingEnabled(context.Background(), tt.thinkingEnabled)
			result := svc.isModelSupportedByProviderWithContext(ctx, provider, tt.requestedModel)

			require.Equal(t, tt.expected, result,
				"isModelSupportedByProviderWithContext(ctx[thinking=%v], provider, %q) = %v, want %v",
				tt.thinkingEnabled, tt.requestedModel, result, tt.expected)
		})
	}
}

// TestGatewayService_isModelSupportedByProvider_CustomMappingNotInDefault
// supplies custom model aliases with no explicit allowlist. Custom targets and
// unmatched names remain supported, protecting mapping extension alongside
// empty-scope passthrough.
func TestGatewayService_isModelSupportedByProvider_CustomMappingNotInDefault(t *testing.T) {
	// The custom mapping includes models absent from the defaults.
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"my-custom-model":   "actual-upstream-model",
					"gpt-4o":            "some-upstream-model",
					"llama-3-70b":       "llama-3-70b-upstream",
					"claude-sonnet-4-5": "claude-sonnet-4-5",
				},
			},
		},
	}

	// Models outside the default mapping are accepted.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "my-custom-model"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gpt-4o"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "llama-3-70b"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-sonnet-4-5"))

	// Unmapped names pass through when no allowlist is configured.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "gpt-3.5-turbo"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "unknown-model"))

	// An empty model skips filtering.
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), ""))
}

// TestGatewayService_isModelSupportedByProviderWithContext_CustomMappingThinking
// supplies base and thinking Sonnet mappings plus an unrelated custom name.
// Support returns true in both thinking contexts and for the custom request,
// covering the positive results from this empty-allowlist fixture.
func TestGatewayService_isModelSupportedByProviderWithContext_CustomMappingThinking(t *testing.T) {
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}},

		nil)

	// Configure both base and thinking variants.
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-sonnet-4-5":          "claude-sonnet-4-5",
					"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
					"my-custom-model":            "upstream-model",
				},
			},
		},
	}

	ctx := requeststate.WithThinkingEnabled(context.Background(), true)
	require.True(t, svc.isModelSupportedByProviderWithContext(ctx, provider, "claude-sonnet-4-5"))

	ctx = requeststate.WithThinkingEnabled(context.Background(), false)
	require.True(t, svc.isModelSupportedByProviderWithContext(ctx, provider, "claude-sonnet-4-5"))

	ctx = requeststate.WithThinkingEnabled(context.Background(), true)
	require.True(t, svc.isModelSupportedByProviderWithContext(ctx, provider, "my-custom-model"))
}

// TestGatewayServiceIsModelSupportedByProvider_BedrockDefaultMappingRestrictsModels
// supplies a US-region Bedrock target using default routes. A recognized alias
// passes while an unsupported dated alias fails, protecting verified regional
// model support.
func TestGatewayServiceIsModelSupportedByProvider_BedrockDefaultMappingRestrictsModels(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "us-east-1",
			},
		},
	}

	if !gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-sonnet-4-5") {
		t.Fatalf("expected default Bedrock alias to be supported")
	}

	if gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-3-5-sonnet-20241022") {
		t.Fatalf("expected unsupported alias to be rejected for Bedrock provider")
	}
}

// TestGatewayServiceIsModelSupportedByProvider_BedrockCustomMappingStillActsAsAllowlist
// adds a wildcard mapping in a configured region. The custom route and a
// default alias pass while an unsupported dated name fails, protecting default
// route availability alongside mapping.
func TestGatewayServiceIsModelSupportedByProvider_BedrockCustomMappingStillActsAsAllowlist(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.ProviderTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "eu-west-1",
				"model_mapping": map[string]any{
					"claude-sonnet-*": "claude-sonnet-4-6",
				},
			},
		},
	}

	if !gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-sonnet-4-6") {
		t.Fatalf("expected matched custom mapping to be supported")
	}

	if !gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-opus-4-6") {
		t.Fatalf("expected default Bedrock alias fallback to remain supported")
	}

	if gatewayprovider.ExecutionModelPolicy(provider).Supports(context.Background(), "claude-3-5-sonnet-20241022") {
		t.Fatalf("expected unsupported model to still be rejected")
	}
}

// selectionWindowForTest constructs a billing window guard with fixture cache
// and usage readers.
func selectionWindowForTest(cache billing.WindowCostCache, source usage.UsageLogRepository) *billing.WindowCostGuard {
	return billing.NewWindowCostGuard(cache, gatewaytestkit.WindowCosts(source), billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(string, ...any) {}, Debug: func(string, ...any) {}})
}

// TestGatewayProviderLayerUsesGroupMappedModelForSupportAndRateLimit maps an
// alias through group and provider to a cooled-down allowed model. Support
// passes, scheduling fails, and sticky invalidation follows that final
// identity.
func TestGatewayProviderLayerUsesGroupMappedModelForSupportAndRateLimit(t *testing.T) {
	groupID := int64(4201)
	pricingConfig := routingtestkit.Configuration{
		ID:           71,
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"client-alias": "group-model"},
	}
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{},
		Shared: Shared{GroupPolicies: routingtestkit.PricingConfig(groupID, capability.PlatformAnthropic,

			pricingConfig)},
	}, nil)

	ctx := svc.withGroupContext(context.Background(), &routing.Group{
		ID: groupID,

		Status:   billing.StatusActive,
		Hydrated: true,
	})
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"group-model": "upstream-model"},
				"model_whitelist": []any{"upstream-model"},
			},
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					"upstream-model": map[string]any{"rate_limit_reset_at": future},
				},
			},
		},
	}

	require.True(t, svc.isModelSupportedByProviderWithContext(ctx, provider, "client-alias"))
	require.False(t, svc.isProviderSchedulableForModelSelection(ctx, provider, "client-alias"))
	require.True(t, svc.shouldClearStickySessionForProviderLayer(ctx, provider, "client-alias"))
}

// TestGatewayService_ListSchedulableProviders_DoesNotFilterUnsupportedThresholdPlatforms
// supplies utilization extras on a platform outside threshold support.
// Candidate reads retain both targets and perform no pause writes, protecting
// the adapter's supported-platform scope.
func TestGatewayService_ListSchedulableProviders_DoesNotFilterUnsupportedThresholdPlatforms(t *testing.T) {
	settingsRepo := settingstestkit.NewMemory()
	settingsRepo.Data[providercore.SettingKeyProviderSchedulingThresholds] = `{"openai":90}`

	providerRepo := &thresholdSelectionProviderRepoStub{
		providers: []gatewayprovider.ExecutionProvider{
			{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 3101,
					Platform:    "kiro",
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{
						"model_whitelist":               []string{"*"},
						"provider_scheduling_threshold": 1,
					},
					Extra: map[string]any{
						"kiro_sched_utilization": 95.0,
						"kiro_sched_reset_at":    time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 3102,
					Platform:    "kiro",
					Status:      billing.StatusActive,
					Schedulable: true,
					Extra: map[string]any{
						"kiro_sched_utilization": 42.0,
						"kiro_sched_reset_at":    time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
					},
				},
			},
		},
	}

	healthObserver := gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: providerRepo, Readers: gatewaytestkit.RuntimeReaders(settings.New(settingsRepo))})
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Providers: providerRepo}, Shared: Shared{Health: healthObserver}}, &config.Config{})

	providers, useMixed, err := svc.listSchedulableProviders(context.Background(), selectionFixtureGroupID(context.Background()), "kiro", false)

	require.NoError(t, err)
	require.False(t, useMixed)
	require.Len(t, providers, 2)
	require.Equal(t, int64(3101), providers[0].Record.ID)
	require.Equal(t, int64(3102), providers[1].Record.ID)
	require.Equal(t, 0, providerRepo.TempCalls)
}

// TestGatewaySelectProviderWithLoadAwareness_HydratesSelectedProviderFromSchedulerSnapshot
// ranks a lightweight Anthropic candidate with no execution key. Selection
// returns its full cached key, protecting snapshot-only credential preparation
// before forwarding.
func TestGatewaySelectProviderWithLoadAwareness_HydratesSelectedProviderFromSchedulerSnapshot(t *testing.T) {
	cache := &snapshotHydrationCache{
		snapshot: []*gatewayprovider.ExecutionProvider{
			{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 9,
					Platform:    capability.PlatformAnthropic,
					Type:        capability.ProviderTypeAPIKey,
					Status:      billing.StatusActive,
					Schedulable: true,
					Concurrency: 1,
					Priority:    1,
				},
			},
		},
		providers: map[int64]*gatewayprovider.ExecutionProvider{
			9: {
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 9,
					Platform:    capability.PlatformAnthropic,
					Type:        capability.ProviderTypeAPIKey,
					Status:      billing.StatusActive,
					Schedulable: true,
					Concurrency: 1,
					Priority:    1,
					Credentials: map[string]any{
						"model_whitelist": []string{"*"},
						"api_key":         "anthropic-live-key",
					},
				},
			},
		},
	}

	schedulerSnapshot := newHydrationSnapshotForTest(cache, nil)
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:  Reads{Snapshot: schedulerredis.NewSnapshotReader(schedulerSnapshot)},
		Shared: Shared{Cache: &mockGatewayCacheForPlatform{}},
	}, testConfig())

	result, err := svc.SelectProviderWithLoadAwareness(context.Background(), selectionFixtureGroupID(context.Background()), "", "claude-3-5-sonnet-20241022", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if result == nil || result.Provider == nil {
		t.Fatalf("expected selected provider")
	}
	if got := result.Provider.View().GetCredential("api_key"); got != "anthropic-live-key" {
		t.Fatalf("expected hydrated api key, got %q", got)
	}
}

// TestGatewaySelectProviderWithLoadAwareness_SkipsAntigravityGeminiFamilyRateLimitedSnapshot
// applies a Gemini-family cooldown to the preferred Antigravity snapshot.
// Selection chooses the eligible backup, protecting family-scoped model
// eligibility before hydration.
func TestGatewaySelectProviderWithLoadAwareness_SkipsAntigravityGeminiFamilyRateLimitedSnapshot(t *testing.T) {
	resetAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	cache := &snapshotHydrationCache{
		snapshot: []*gatewayprovider.ExecutionProvider{
			{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformAntigravity,
					Type:        capability.ProviderTypeOAuth,
					Status:      billing.StatusActive,
					Schedulable: true,
					Concurrency: 1,
					Priority:    1,
					ProviderGroups: []providercore.GroupMembership{
						{ProviderID: 1, GroupID: 22},
					},
					GroupIDs: []int64{22},
					Extra: map[string]any{
						"mixed_scheduling": true, "model_rate_limits": map[string]any{
							"antigravity:gemini": map[string]any{
								"rate_limit_reset_at": resetAt,
							},
						},
					},
				},
			},
			{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
					Platform:    capability.PlatformAntigravity,
					Type:        capability.ProviderTypeOAuth,
					Status:      billing.StatusActive,
					Schedulable: true,
					Concurrency: 1,
					Priority:    2,
					ProviderGroups: []providercore.GroupMembership{
						{ProviderID: 2, GroupID: 22},
					},
					GroupIDs: []int64{22},
					Extra: map[string]any{
						"mixed_scheduling": true,
					},
				},
			},
		},
		providers: map[int64]*gatewayprovider.ExecutionProvider{
			1: {Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true}},
			2: {Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true}},
		},
	}
	groupID := int64(22)
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: {
				ID:     groupID,
				Status: billing.StatusActive, Hydrated: true,
			}}},
			Snapshot: schedulerredis.NewSnapshotReader(newHydrationSnapshotForTest(cache, nil)),
		},
		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(&mockConcurrencyCache{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, &config.Config{Scheduling: config.SchedulingConfig{
		LoadBatch: config.SchedulingLoadBatchConfig{Enabled: true},
		StickyProviderWait: config.ProviderWaitConfig{
			MaxWaiting:     3,
			TimeoutSeconds: 1,
		},
		AlternateProviderWait: config.ProviderWaitConfig{
			TimeoutSeconds: 1,
			MaxWaiting:     10,
		},
	}})

	result, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gemini-3-flash-preview", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if result == nil || result.Provider == nil {
		t.Fatalf("expected selected provider")
	}
	if result.Provider.Record.ID != 2 {
		t.Fatalf("expected scheduler to skip Gemini-family limited antigravity provider 1, got %d", result.Provider.Record.ID)
	}
}

// TestGatewayNewSelectionResultReleasesSlotWhenHydrationFails omits the full
// record for an acquired target. Preparation returns an error and releases
// capacity once, protecting attempt cleanup when lightweight selection cannot
// become execution.
func TestGatewayNewSelectionResultReleasesSlotWhenHydrationFails(t *testing.T) {
	cache := &snapshotHydrationCache{providers: map[int64]*gatewayprovider.ExecutionProvider{}}
	snapshot := newHydrationSnapshotForTest(cache, selectionProviderFixture{})
	gateway := newGenericSelectionForTest(GenericDependencies{
		Reads:  Reads{Snapshot: schedulerredis.NewSnapshotReader(snapshot)},
		Shared: Shared{},
	}, nil)

	calls := 0
	result, err := gateway.newSelectionResult(context.Background(), &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1001}}, true, func() { calls++ }, nil)
	if err == nil || result != nil {
		t.Fatal("补全失败必须返回原错误而非选择结果")
	}
	if calls != 1 {
		t.Fatalf("释放次数=%d，期望 1", calls)
	}
}

// newSessionLimitReleaseCacheStub allocates an empty trace of per-provider
// session removals.
func newSessionLimitReleaseCacheStub() *sessionLimitReleaseCacheStub {
	return &sessionLimitReleaseCacheStub{
		unregistered: make(map[int64][]string),
	}
}

// UnregisterSession appends the provider's session to the fixture removal trace
// unless an error is configured. It does not deduplicate calls, allowing tests
// to observe cleanup delegation separately from cache idempotence.
func (s *sessionLimitReleaseCacheStub) UnregisterSession(_ context.Context, providerID int64, sessionUUID string) error {
	if s.err != nil {
		return s.err
	}
	s.unregistered[providerID] = append(s.unregistered[providerID], sessionUUID)
	return nil
}

// newSessionLimitTestProvider constructs an Anthropic OAuth fixture with one
// allowed active session.
func newSessionLimitTestProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 42,
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"max_sessions": 1},
		},
	}
}

// TestReleaseProviderSession_ReleasesRegisteredSlot supplies an applicable
// Anthropic OAuth target with a configured session limit. Cleanup reaches the
// cache immediately, protecting release before the session's idle lifetime can
// expire.
func TestReleaseProviderSession_ReleasesRegisteredSlot(t *testing.T) {
	cache := newSessionLimitReleaseCacheStub()
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{}, Shared: Shared{}, Sessions: cache,
	}, nil)

	acc := newSessionLimitTestProvider()

	svc.ReleaseProviderSession(context.Background(), acc, "session-hash-1")

	require.Equal(t, []string{"session-hash-1"}, cache.unregistered[42],
		"应立即移除注册的会话槽")
}

// TestReleaseProviderSession_NoOpForInapplicableProviders supplies API Key,
// disabled-limit, blank-session, and nil target cases. Cleanup makes no
// unregister calls, protecting the eligibility rules shared by session
// registration and removal.
func TestReleaseProviderSession_NoOpForInapplicableProviders(t *testing.T) {
	apiKeyAcc := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 43,
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"max_sessions": 1},
		},
	}
	noLimitAcc := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 44,
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeOAuth,
		},
	}
	enabledAcc := newSessionLimitTestProvider()

	cases := []struct {
		name      string                             // named cleanup scenario
		provider  *gatewayprovider.ExecutionProvider // configured cleanup target fixture
		sessionID string                             // supplied session hash identity
	}{
		{"api_key_provider", apiKeyAcc, "session-hash"},
		{"max_sessions_disabled", noLimitAcc, "session-hash"},
		{"empty_session_id", enabledAcc, ""},
		{"nil_provider", nil, "session-hash"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := newSessionLimitReleaseCacheStub()
			svc := newGenericSelectionForTest(GenericDependencies{
				Reads: Reads{}, Shared: Shared{}, Sessions: cache,
			}, nil)

			svc.ReleaseProviderSession(context.Background(), tc.provider, tc.sessionID)
			require.Empty(t, cache.unregistered, "不适用提供商不应触发释放")
		})
	}
}

// TestReleaseProviderSession_NilCacheAndErrorTolerance exercises omitted
// storage and a configured unregister failure. Cleanup returns to the caller in
// both cases, protecting optional-cache and logged-failure handling of session
// removal.
func TestReleaseProviderSession_NilCacheAndErrorTolerance(t *testing.T) {
	// A nil cache is a no-op.
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{}, Shared: Shared{},
	}, nil)

	svc.ReleaseProviderSession(context.Background(), newSessionLimitTestProvider(), "session-hash")

	// A cache error does not panic.
	cache := &sessionLimitReleaseCacheStub{err: errors.New("redis down")}
	svc = newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{}, Shared: Shared{}, Sessions: cache,
	}, nil)

	svc.ReleaseProviderSession(context.Background(), newSessionLimitTestProvider(), "session-hash")
}

// TestReleaseProviderSession_Idempotent releases one applicable session twice
// through a tracing cache. Both calls reach storage, protecting delegated
// repeated removal rather than claiming the selector deduplicates release calls
// itself.
func TestReleaseProviderSession_Idempotent(t *testing.T) {
	cache := newSessionLimitReleaseCacheStub()
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{}, Shared: Shared{}, Sessions: cache,
	}, nil)

	acc := newSessionLimitTestProvider()

	svc.ReleaseProviderSession(context.Background(), acc, "session-hash")
	svc.ReleaseProviderSession(context.Background(), acc, "session-hash")

	require.Len(t, cache.unregistered[42], 2)
}
