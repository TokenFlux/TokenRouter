package selection

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
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

// noSlotSchedulerTestConcurrencyCache records forbidden generation-slot
// acquisition in auxiliary selection tests.
type noSlotSchedulerTestConcurrencyCache struct {
	schedulerTestConcurrencyCache     // base compatible capacity fixture
	acquireCalls                  int // forbidden acquisition call count
}

// groupAwareStubOpenAIProviderRepo supplies compatible records with explicit
// group-membership filtering for isolation tests.
type groupAwareStubOpenAIProviderRepo struct {
	selectionProviderFixture // base prepared selection fixture
}

// openAICodexExtraListRepo observes unexpected rate-limit writes during
// compatible quota-read tests.
type openAICodexExtraListRepo struct {
	selectionProviderFixture // base configured provider fixture
	// rateLimitCh is an optional write observation. SetRateLimited sends
	// synchronously, so the test provides a buffer or receiver before permitting a
	// write attempt.
	rateLimitCh chan time.Time // observed reset-time write channel
}

// TestOpenAIGetSchedulableProvider_AppliesGrokFreeSoftGate reads an explicit
// Free OAuth provider above its soft token threshold. Initial unknown usage
// admits it, then the refreshed gate rejects the basic compatible lookup.
func TestOpenAIGetSchedulableProvider_AppliesGrokFreeSoftGate(t *testing.T) {
	cfg := &config.Config{}
	cfg.Scheduling.GrokFreeQuota.Enabled = true
	cfg.Scheduling.GrokFreeQuota.TokenLimit = 500_000
	cfg.Scheduling.GrokFreeQuota.ThresholdPercent = 95
	cfg.Scheduling.GrokFreeQuota.WindowHours = 24
	cfg.Scheduling.GrokFreeQuota.StatsCacheSeconds = 60

	provider := healthyGrokOAuthGatewayTestProvider(8802, "tok")
	provider.Record.Credentials["subscription_tier"] = "free"
	provider.Record.Status = billing.StatusActive
	provider.Record.Schedulable = true

	repo := &mockProviderRepoForPlatform{
		providersByID: map[int64]*gatewayprovider.ExecutionProvider{provider.Record.ID: provider},
	}
	usageRepo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usage.ProviderStats{
		provider.Record.ID: {Tokens: 480_000},
	}}
	factory := freeQuotaFactoryForTest(t, cfg, usageRepo)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:                Reads{Providers: repo},
		FreeQuota:            factory(),
		NewAdvancedFreeQuota: factory,
	}, cfg)

	// Missing usage permits the first lookup while the tracked query refreshes.
	// Later assertions wait for the observed quota gate before expecting rejection.
	got, err := svc.getSchedulableProvider(context.Background(), provider.Record.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "first sticky hit fail-opens while free-gate stats refresh")

	require.Eventually(t, func() bool {
		got, err := svc.getSchedulableProvider(context.Background(), provider.Record.ID)
		return err == nil && got == nil
	}, 2*time.Second, 10*time.Millisecond, "OpenAI legacy sticky must apply free soft-gate after cache warm")
}

// TestOpenAISelectProviderWithLoadAwareness_FiltersUnschedulable gives the
// higher-priority target an active global cooldown. Basic load-aware choice
// returns the eligible backup, protecting provider scheduling state before
// acquisition.
func TestOpenAISelectProviderWithLoadAwareness_FiltersUnschedulable(t *testing.T) {
	now := time.Now()
	resetAt := now.Add(10 * time.Minute)
	groupID := int64(1)

	rateLimited := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
			Platform:         capability.PlatformOpenAI,
			Type:             capability.ProviderTypeAPIKey,
			Status:           billing.StatusActive,
			Schedulable:      true,
			Concurrency:      1,
			Priority:         0,
			RateLimitResetAt: &resetAt,
		},
	}
	available := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{
				rateLimited,
				available,
			}},
		},
		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-5.2", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil {
		t.Fatalf("expected selection with provider")
	}
	if selection.Provider.Record.ID != available.Record.ID {
		t.Fatalf("expected provider %d, got %d", available.Record.ID, selection.Provider.Record.ID)
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAISelectProviderWithLoadAwareness_ImageRateLimitSkipsOnlyImageRequests
// applies the image cooldown key to one target. Image intent chooses the backup
// while text still uses that target, protecting request-specific model limit
// scope.
func TestOpenAISelectProviderWithLoadAwareness_ImageRateLimitSkipsOnlyImageRequests(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	groupID := int64(1)

	imageLimited := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					providercore.OpenAIImageGenerationRateLimitKey: map[string]any{
						"rate_limit_reset_at": future,
					},
				},
			},
		},
	}
	available := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{
				imageLimited,
				available,
			}},
		},
		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, nil)

	imageSelection, err := svc.SelectProviderWithLoadAwareness(requeststate.WithOpenAIImageGenerationIntent(context.Background()), &groupID, "", "gpt-5.4", nil)
	require.NoError(t, err)
	require.NotNil(t, imageSelection)
	require.Equal(t, available.Record.ID, imageSelection.Provider.Record.ID)
	if imageSelection.ReleaseFunc != nil {
		imageSelection.ReleaseFunc()
	}

	textSelection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-5.4", nil)
	require.NoError(t, err)
	require.NotNil(t, textSelection)
	require.Equal(t, imageLimited.Record.ID, textSelection.Provider.Record.ID)
	if textSelection.ReleaseFunc != nil {
		textSelection.ReleaseFunc()
	}
}

// TestOpenAISelectProviderWithLoadAwareness_FiltersUnschedulableWhenNoConcurrencyService
// removes capacity services while one target remains globally rate limited.
// Basic selection still chooses the eligible backup, protecting status checks
// on the non-batch path.
func TestOpenAISelectProviderWithLoadAwareness_FiltersUnschedulableWhenNoConcurrencyService(t *testing.T) {
	now := time.Now()
	resetAt := now.Add(10 * time.Minute)
	groupID := int64(1)

	rateLimited := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1,
			Platform:         capability.PlatformOpenAI,
			Type:             capability.ProviderTypeAPIKey,
			Status:           billing.StatusActive,
			Schedulable:      true,
			Concurrency:      1,
			Priority:         0,
			RateLimitResetAt: &resetAt,
		},
	}
	available := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{
				rateLimited,
				available,
			}},
		},
		Shared: Shared{},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-5.2", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil {
		t.Fatalf("expected selection with provider")
	}
	if selection.Provider.Record.ID != available.Record.ID {
		t.Fatalf("expected provider %d, got %d", available.Record.ID, selection.Provider.Record.ID)
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAISelectProviderForModelWithExclusions_StickyUnschedulableClearsSession
// binds a disabled compatible target and supplies an eligible backup. Slot-free
// choice deletes the stale affinity and records the backup binding for later
// requests.
func TestOpenAISelectProviderForModelWithExclusions_StickyUnschedulableClearsSession(t *testing.T) {
	sessionHash := "session-1"
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusDisabled, Schedulable: true, Concurrency: 1}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), selectionFixtureGroupID(context.Background()), sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected provider 2, got %+v", acc)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to provider 2")
	}
}

// TestOpenAISelectProviderForModelWithExclusions_StickyOutsideGroupClearsSession
// binds a target outside the requested group beside a valid member. Selection
// replaces that affinity with the member, protecting group membership during
// sticky reuse.
func TestOpenAISelectProviderForModelWithExclusions_StickyOutsideGroupClearsSession(t *testing.T) {
	sessionHash := "session-outside-group"
	groupID := int64(1001)
	repo := groupAwareStubOpenAIProviderRepo{
		selectionProviderFixture{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, ProviderGroups: []providercore.GroupMembership{{GroupID: groupID}}}},
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected provider 2, got %+v", acc)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to provider 2")
	}
}

// TestOpenAISelectProviderWithLoadAwareness_StickyUnschedulableClearsSession
// binds a disabled target on the load-aware path. Selection returns the
// eligible backup and replaces the stale session, protecting status checks
// before sticky capacity use.
func TestOpenAISelectProviderWithLoadAwareness_StickyUnschedulableClearsSession(t *testing.T) {
	sessionHash := "session-2"
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusDisabled, Schedulable: true, Concurrency: 1}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(
				selectionConcurrencyFixture{}, schedulercore.Diagnostics{
					Logf:  logging.LegacyPrintf,
					Event: logging.Event,
				}),
			Cache: cache,
		},
	}, nil,
	)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil || selection.Provider.Record.ID != 2 {
		t.Fatalf("expected provider 2, got %+v", selection)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to provider 2")
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAISelectProviderWithLoadAwareness_StickyOutsideGroupClearsSession
// supplies an out-of-group binding while an admitted member has capacity.
// Load-aware choice replaces that affinity with the member, protecting group
// isolation during acquisition.
func TestOpenAISelectProviderWithLoadAwareness_StickyOutsideGroupClearsSession(t *testing.T) {
	sessionHash := "session-load-outside-group"
	groupID := int64(1002)
	repo := groupAwareStubOpenAIProviderRepo{
		selectionProviderFixture{
			providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
				{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, ProviderGroups: []providercore.GroupMembership{{GroupID: groupID}}}},
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil,
	)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil || selection.Provider.Record.ID != 2 {
		t.Fatalf("expected provider 2, got %+v", selection)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to provider 2")
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAISelectProviderForModelWithExclusions_NoModelSupport configures a
// narrower model identity than the requested name. Slot-free selection returns
// no provider and a model-support diagnostic, protecting unsupported-model
// handling.
func TestOpenAISelectProviderForModelWithExclusions_NoModelSupport(t *testing.T) {
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformOpenAI,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"gpt-3.5-turbo": "gpt-3.5-turbo"}},
				},
			},
		},
	}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), selectionFixtureGroupID(context.Background()), "", "gpt-4", nil)
	if err == nil {
		t.Fatalf("expected error for unsupported model")
	}
	if acc != nil {
		t.Fatalf("expected nil provider for unsupported model")
	}
	if !strings.Contains(err.Error(), "does not support the requested model") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestOpenAISelectProviderWithLoadAwareness_LoadBatchErrorFallback injects a
// batch-load read error with two eligible targets. Selection uses basic
// priority ordering and binds the selected backup, protecting recovery from
// unavailable load observations.
func TestOpenAISelectProviderWithLoadAwareness_LoadBatchErrorFallback(t *testing.T) {
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadBatchErr: errors.New("load batch failed"),
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(
				concurrencyCache, schedulercore.Diagnostics{
					Logf:  logging.LegacyPrintf,
					Event: logging.Event,
				}),
			Cache: cache,
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "fallback", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil {
		t.Fatalf("expected selection")
	}
	if selection.Provider.Record.ID != 2 {
		t.Fatalf("expected provider 2, got %d", selection.Provider.Record.ID)
	}
	if cache.sessionBindings["openai:fallback"] != 2 {
		t.Fatalf("expected sticky session updated")
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAISelectProviderWithLoadAwareness_NoSlotFallbackWait gives an
// eligible target a low observed load but failed slot acquisition. Selection
// returns its wait plan, protecting queue fallback when observations and actual
// capacity differ.
func TestOpenAISelectProviderWithLoadAwareness_NoSlotFallbackWait(t *testing.T) {
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		acquireResults: map[int64]bool{1: false},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			1: {ProviderID: 1, LoadRate: 10},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected wait plan fallback")
	}
	if selection.Provider == nil || selection.Provider.Record.ID != 1 {
		t.Fatalf("expected provider 1")
	}
}

// TestOpenAISelectProviderForModelWithExclusions_SetsStickyBinding supplies one
// eligible target and an unbound session hash. Slot-free choice records that
// provider under the compatible prefix, protecting affinity establishment after
// selection.
func TestOpenAISelectProviderForModelWithExclusions_SetsStickyBinding(t *testing.T) {
	sessionHash := "bind"
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), selectionFixtureGroupID(context.Background()), sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 1 {
		t.Fatalf("expected provider 1")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session binding")
	}
}

// TestOpenAISelectProviderWithLoadAwareness_StickyWaitPlan fills the
// session-bound provider's slot while its waiting count permits admission.
// Selection returns a wait plan for that target, protecting short sticky
// waiting before pool fallback.
func TestOpenAISelectProviderWithLoadAwareness_StickyWaitPlan(t *testing.T) {
	sessionHash := "sticky-wait"
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}
	concurrencyCache := selectionConcurrencyFixture{
		acquireResults: map[int64]bool{1: false},
		waitCounts:     map[int64]int{1: 0},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected sticky wait plan")
	}
	if selection.Provider == nil || selection.Provider.Record.ID != 1 {
		t.Fatalf("expected provider 1")
	}
}

// TestOpenAISelectProviderWithLoadAwareness_StickyCapacitySpilloverKeepsBinding
// fills both the bound target's slot and its permitted queue. Another target
// serves this request while the stored binding remains unchanged, protecting
// temporary spillover affinity.
func TestOpenAISelectProviderWithLoadAwareness_StickyCapacitySpilloverKeepsBinding(t *testing.T) {
	sessionHash := "sticky-spillover"
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 6, Priority: 1, GroupIDs: []int64{groupID}}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 6, Priority: 1, GroupIDs: []int64{groupID}}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}
	concurrencyCache := selectionConcurrencyFixture{
		acquireResults: map[int64]bool{1: false, 2: true},
		waitCounts:     map[int64]int{1: 1},
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			1: {ProviderID: 1, LoadRate: 100},
			2: {ProviderID: 2, LoadRate: 10},
		},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = true
	cfg.Scheduling.StickyProviderWait.MaxWaiting = 1

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(2), selection.Provider.Record.ID, "capacity spillover should use the other provider for this request")
	require.True(t, selection.Acquired)
	require.Equal(t, int64(1), cache.sessionBindings["openai:"+sessionHash], "capacity spillover must not migrate the durable sticky binding")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// TestOpenAISelectProviderWithLoadAwareness_PrefersLowerLoad supplies
// equal-priority targets with different load observations. Basic load-aware
// choice selects and binds the lower-load target, protecting candidate ordering
// when capacity remains available.
func TestOpenAISelectProviderWithLoadAwareness_PrefersLowerLoad(t *testing.T) {
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			1: {ProviderID: 1, LoadRate: 80},
			2: {ProviderID: 2, LoadRate: 10},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "load", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil || selection.Provider.Record.ID != 2 {
		t.Fatalf("expected provider 2")
	}
	if cache.sessionBindings["openai:load"] != 2 {
		t.Fatalf("expected sticky session updated")
	}
}

// TestOpenAISelectProviderForModelWithExclusions_StickyExcludedFallback marks
// the session-bound provider excluded for this request. Selection chooses the
// remaining eligible target, protecting request exclusions even when sticky
// affinity is populated.
func TestOpenAISelectProviderForModelWithExclusions_StickyExcludedFallback(t *testing.T) {
	sessionHash := "excluded"
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	excluded := map[int64]struct{}{1: {}}
	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), selectionFixtureGroupID(context.Background()), sessionHash, "gpt-4", excluded)
	if err != nil {
		t.Fatalf("SelectProviderForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected provider 2")
	}
}

// TestOpenAISelectProviderForModelWithExclusions_KeepsCompatibleCrossPlatformSticky
// binds an eligible Anthropic member in a compatible group beside OpenAI.
// Selection returns the Anthropic member, which also has the higher priority
// in this fixture's platform-neutral candidate pool.
func TestOpenAISelectProviderForModelWithExclusions_KeepsCompatibleCrossPlatformSticky(t *testing.T) {
	sessionHash := "non-openai"
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), selectionFixtureGroupID(context.Background()), sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 1 {
		t.Fatalf("expected cross-platform sticky provider 1")
	}
}

// TestOpenAISelectProviderForModelWithExclusions_NoProviders supplies an empty
// compatible pool and empty model input. Selection returns nil with the
// no-provider diagnostic, protecting exhaustion even when model filtering is
// absent.
func TestOpenAISelectProviderForModelWithExclusions_NoProviders(t *testing.T) {
	repo := selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{}}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), selectionFixtureGroupID(context.Background()), "", "", nil)
	if err == nil {
		t.Fatalf("expected error for no providers")
	}
	if acc != nil {
		t.Fatalf("expected nil provider")
	}
	if !strings.Contains(err.Error(), "no available OpenAI providers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestOpenAISelectProviderWithLoadAwareness_NoCandidates makes the entire
// configured pool currently rate limited. Selection returns an error and no
// result, protecting eligibility exhaustion before a wait plan can be offered.
func TestOpenAISelectProviderWithLoadAwareness_NoCandidates(t *testing.T) {
	groupID := int64(1)
	resetAt := time.Now().Add(1 * time.Hour)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, RateLimitResetAt: &resetAt}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err == nil {
		t.Fatalf("expected error for no candidates")
	}
	if selection != nil {
		t.Fatalf("expected nil selection")
	}
}

// TestOpenAISelectProviderWithLoadAwareness_AllFullWaitPlan supplies an
// eligible pool reporting full load. Selection returns a wait plan, protecting
// temporary capacity handling when the provider's hard qualification still
// passes.
func TestOpenAISelectProviderWithLoadAwareness_AllFullWaitPlan(t *testing.T) {
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			1: {ProviderID: 1, LoadRate: 100},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(
				concurrencyCache, schedulercore.Diagnostics{
					Logf:  logging.LegacyPrintf,
					Event: logging.Event,
				}),
			Cache: cache,
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected wait plan")
	}
}

// TestOpenAISelectProviderWithLoadAwareness_LoadBatchErrorNoAcquire combines
// failed load observation with unavailable provider acquisition. Basic recovery
// returns a wait plan, protecting capacity fallback after the batch path cannot
// choose a slot.
func TestOpenAISelectProviderWithLoadAwareness_LoadBatchErrorNoAcquire(t *testing.T) {
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadBatchErr:   errors.New("load batch failed"),
		acquireResults: map[int64]bool{1: false},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected wait plan")
	}
}

// TestOpenAISelectProviderWithLoadAwareness_MissingLoadInfo omits one eligible
// provider from the batch observation map. Basic selection chooses that
// provider under missing-load behavior, protecting optional observations during
// otherwise valid candidate selection.
func TestOpenAISelectProviderWithLoadAwareness_MissingLoadInfo(t *testing.T) {
	groupID := int64(1)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			1: {ProviderID: 1, LoadRate: 50},
		},
		skipDefaultLoad: true,
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil || selection.Provider.Record.ID != 2 {
		t.Fatalf("expected provider 2")
	}
}

// TestOpenAISelectProviderForModelWithExclusions_LeastRecentlyUsed supplies
// equal-priority targets with different prior use timestamps. Slot-free
// selection chooses the older record, protecting recency ordering after
// priority ties.
func TestOpenAISelectProviderForModelWithExclusions_LeastRecentlyUsed(t *testing.T) {
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-1 * time.Hour)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Priority: 1, LastUsedAt: &newTime}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Priority: 1, LastUsedAt: &oldTime}},
		},
	}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Providers: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectProviderForModelWithExclusions(context.Background(), selectionFixtureGroupID(context.Background()), "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected provider 2")
	}
}

// TestOpenAISelectProviderWithLoadAwareness_PreferNeverUsed supplies
// equal-priority, equal-load targets with present and missing use timestamps.
// The never-used record is selected, protecting recency tie-breaking on basic
// load-aware choice.
func TestOpenAISelectProviderWithLoadAwareness_PreferNeverUsed(t *testing.T) {
	groupID := int64(1)
	lastUsed := time.Now().Add(-1 * time.Hour)
	repo := selectionProviderFixture{
		providers: []gatewayprovider.ExecutionProvider{
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, LastUsedAt: &lastUsed}},
			{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.ProviderLoadInfo{
			1: {ProviderID: 1, LoadRate: 10},
			2: {ProviderID: 2, LoadRate: 10},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil || selection.Provider.Record.ID != 2 {
		t.Fatalf("expected provider 2")
	}
}

// TestOpenAISelectProviderForModelWithExclusions_GroupMappedRestrictionRejectsEarly
// maps the client name to a model absent from group policy. Selection returns a
// group-restriction error, protecting early checks before provider eligibility
// is resolved.
func TestOpenAISelectProviderForModelWithExclusions_GroupMappedRestrictionRejectsEarly(t *testing.T) {
	t.Parallel()

	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceGroupMapped,
		ModelPricing: []routing.ModelPricingEntry{
			{Models: []string{"gpt-4o"}},
		},
		ModelMapping: map[string]string{"gpt-4.1": "o3-mini"},
	}, map[int64]string{10: capability.PlatformOpenAI}))

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{{Record: providercore.Record{
				Credentials:  map[string]any{"model_whitelist": []string{"*"}},
				LoadLocation: time.LoadLocation,
				ID:           1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
				Schedulable: true,
			}}}},
		},
		Shared: Shared{GroupPolicies: pricingConfigSvc},
	}, nil)

	groupID := int64(10)
	_, err := svc.SelectProviderForModelWithExclusions(context.Background(), &groupID, "", "gpt-4.1", nil)
	require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
	require.Contains(t, err.Error(), "group model restriction")
}

// TestOpenAISelectProviderForModelWithExclusions_UpstreamRestrictionSkipsDisallowedProvider
// supplies two provider mappings but allows only the lower-priority final
// identity. Selection chooses that provider, protecting upstream restrictions
// after the group policy stage.
func TestOpenAISelectProviderForModelWithExclusions_UpstreamRestrictionSkipsDisallowedProvider(t *testing.T) {
	t.Parallel()

	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelPricing: []routing.ModelPricingEntry{
			{Models: []string{"o3-mini"}},
		},
	}, map[int64]string{10: capability.PlatformOpenAI}))

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
					Schedulable: true, Priority: 10, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-4.1": "gpt-4o"}},
				}},
				{Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
					Schedulable: true, Priority: 20,
					Credentials: map[string]any{"model_mapping": map[string]any{"gpt-4.1": "o3-mini"}},
				}},
			}},
		},
		Shared: Shared{GroupPolicies: pricingConfigSvc},
	}, nil)

	groupID := int64(10)
	provider, err := svc.SelectProviderForModelWithExclusions(context.Background(), &groupID, "", "gpt-4.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(2), provider.Record.ID)
}

// TestOpenAISelectProviderForModelWithExclusions_StickyRestrictedUpstreamFallsBack
// binds the provider whose final model is restricted. Selection clears its
// affinity and chooses the allowed mapping, protecting group upstream policy
// during sticky reuse.
func TestOpenAISelectProviderForModelWithExclusions_StickyRestrictedUpstreamFallsBack(t *testing.T) {
	t.Parallel()

	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelPricing: []routing.ModelPricingEntry{
			{Models: []string{"o3-mini"}},
		},
	}, map[int64]string{10: capability.PlatformOpenAI}))

	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:sticky-session": 1},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{
				{Record: providercore.Record{
					LoadLocation: time.LoadLocation,
					ID:           1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
					Schedulable: true, Priority: 10, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-4.1": "gpt-4o"}},
				}},
				{Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
					Schedulable: true, Priority: 20,
					Credentials: map[string]any{"model_mapping": map[string]any{"gpt-4.1": "o3-mini"}},
				}},
			}},
		},
		Shared: Shared{
			GroupPolicies: pricingConfigSvc,

			Cache: cache,
		},
	}, nil)

	groupID := int64(10)
	provider, err := svc.SelectProviderForModelWithExclusions(context.Background(), &groupID, "sticky-session", "gpt-4.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(2), provider.Record.ID)
	require.Equal(t, 1, cache.deletedSessions["openai:sticky-session"])
	require.Equal(t, int64(2), cache.sessionBindings["openai:sticky-session"])
}

// AcquireProviderSlot counts forbidden auxiliary acquisition and returns a
// fixture error. Tests expect this method to remain uncalled, making an
// unexpected generation-slot request observable through both the error and
// counter.
func (c *noSlotSchedulerTestConcurrencyCache) AcquireProviderSlot(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
	c.acquireCalls++
	return false, errors.New("辅助选择不应申请并发槽")
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AdvancedGroupSkipsConcurrencySlot
// supplies an advanced group with an acquisition fixture that rejects every
// call. Auxiliary selection returns a provider while the acquisition count
// stays zero.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AdvancedGroupSkipsConcurrencySlot(t *testing.T) {
	groupID := int64(10105)
	ctx := requeststate.WithGroup(context.Background(), &routing.Group{
		ID: groupID,

		SchedulerType: routing.GroupSchedulerTypeAdvanced,
		Status:        billing.StatusActive,
		Hydrated:      true,
	})
	concurrencyCache := &noSlotSchedulerTestConcurrencyCache{}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{{Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 36000, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		}}}}},
		Shared: Shared{
			Cache: &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			}),
		},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &groupID, "", "gpt-5.1", nil)

	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(36000), provider.Record.ID)
	require.Zero(t, concurrencyCache.acquireCalls, "仅选提供商入口不得占用真实并发槽")
}

// TestOpenAIGatewayService_SelectProviderForTokenCount_DoesNotAcquireGenerationSlot
// mixes platforms and endpoint capabilities with a full generation target.
// Token counting selects the eligible requested-platform provider and makes no
// generation-slot acquisition.
func TestOpenAIGatewayService_SelectProviderForTokenCount_DoesNotAcquireGenerationSlot(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10115)
	acquiredIDs := make([]int64, 0)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 36501, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
				Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0,
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "openai_capabilities": []any{"chat_completions"}},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 36502, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
				Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5,
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "openai_capabilities": []any{"embeddings"}},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 36503, Platform: capability.PlatformGrok, Type: capability.ProviderTypeAPIKey,
				Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 10,
				Credentials: map[string]any{"model_whitelist": []string{"*"}, "openai_capabilities": []any{"chat_completions"}},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 36504, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
				Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 15,
				Credentials: map[string]any{
					"openai_capabilities": []any{"chat_completions"},
					"model_mapping":       map[string]any{"gpt-4o": "gpt-4o"},
				},
			},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{Providers: schedulerTestOpenAIProviderRepo{providers: providers}},
		Shared: Shared{
			Cache:       &schedulerTestGatewayCache{},
			Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{36501: false}, acquiredIDs: &acquiredIDs}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, &config.Config{})

	provider, err := svc.SelectProviderForTokenCount(
		ctx,
		&groupID,
		"",
		"gpt-5.1",
		providercore.OpenAIEndpointCapabilityTextGeneration,
		capability.PlatformOpenAI,
	)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(36501), provider.Record.ID)
	require.Empty(t, acquiredIDs, "token counting must not acquire a generation slot")
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AutoPauseBy5hThreshold
// places the preferred provider at its five-hour quota threshold. Selection
// chooses the backup, protecting the threshold comparison before ordinary
// priority selection.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AutoPauseBy5hThreshold(t *testing.T) {
	ctx := context.Background()
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35001,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":   95.0,
				"auto_pause_5h_threshold": 0.95,
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35002), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AllowsBelow5hThreshold
// keeps the preferred provider below its five-hour quota threshold. Selection
// retains that higher-priority target, protecting admission before the
// configured pause limit is reached.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AllowsBelow5hThreshold(t *testing.T) {
	ctx := context.Background()
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35101,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":   80.0,
				"auto_pause_5h_threshold": 0.95,
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35102, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35101), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AutoPauseBy7dThreshold
// reaches the preferred target's seven-day utilization threshold while a backup
// remains eligible. Selection returns that backup, protecting the independent
// longer-window pause rule before ordinary priority choice.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_AutoPauseBy7dThreshold(t *testing.T) {
	ctx := context.Background()
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35201,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_7d_used_percent":   95.0,
				"auto_pause_7d_threshold": 0.95,
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35202, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35202), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_UnconfiguredThresholdKeepsLegacyBehavior
// records high quota utilization with no effective pause threshold. The
// preferred target stays selectable, protecting absent-configuration behavior
// independently of raw usage percentages.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_UnconfiguredThresholdKeepsLegacyBehavior(t *testing.T) {
	ctx := context.Background()
	primary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35301, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, Extra: map[string]any{"codex_5h_used_percent": 99.0, "codex_7d_used_percent": 99.0}}}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35302, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35301), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_UsesGlobalDefaultThreshold
// supplies a global pause threshold and no provider override. Selection rejects
// the exhausted preferred target, protecting inheritance of request-captured
// quota settings.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_UsesGlobalDefaultThreshold(t *testing.T) {
	ctx := gatewayprovider.WithQuotaAutoPauseSettings(context.Background(), ops.OpsOpenAIProviderQuotaAutoPauseSettings{DefaultThreshold5h: 0.95})
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35401,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent": 95.0,
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35402, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35402), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_PerProviderDisableOverridesGlobalDefault
// gives an exhausted target an explicit pause-disable flag. It remains selected
// despite the global threshold, protecting per-provider override precedence for
// that window.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_PerProviderDisableOverridesGlobalDefault(t *testing.T) {
	ctx := gatewayprovider.WithQuotaAutoPauseSettings(context.Background(), ops.OpsOpenAIProviderQuotaAutoPauseSettings{DefaultThreshold5h: 0.95})
	// High usage and an unset threshold would normally trigger the global pause rule.
	// The provider disables automatic pausing.
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35701,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":  99.0,
				"auto_pause_5h_disabled": true,
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35702, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35701), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_PerWindowDisableScoped
// disables five-hour pausing while seven-day utilization still exceeds its
// threshold. Selection chooses the backup, protecting independent pause
// configuration for each quota window.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_PerWindowDisableScoped(t *testing.T) {
	ctx := context.Background()
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35801,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":   99.0,
				"codex_7d_used_percent":   99.0,
				"auto_pause_5h_disabled":  true,
				"auto_pause_7d_threshold": 0.95,
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35802, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35802), provider.Record.ID, "7d auto-pause must still fire even though 5h is disabled")
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_StaleUsageWindowResetSkipsPause
// gives an exhausted snapshot a reset time already passed. Selection permits
// the preferred target, protecting stale-window admission that can obtain a
// newer observation.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_StaleUsageWindowResetSkipsPause(t *testing.T) {
	ctx := context.Background()
	// The reset time has passed, so cached usage is stale.
	// Allowing traffic lets response headers refresh the window.
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35501,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":   99.0,
				"auto_pause_5h_threshold": 0.95,
				"codex_5h_reset_at":       time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35502, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35501), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_FreshUsageWindowStillPauses
// gives the exhausted preferred target a future reset time. Selection chooses
// the backup, protecting pause eligibility while that observed window remains
// active.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_FreshUsageWindowStillPauses(t *testing.T) {
	ctx := context.Background()
	// The exhausted window has not reset, so the provider remains paused.
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35601,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":   99.0,
				"auto_pause_5h_threshold": 0.95,
				"codex_5h_reset_at":       time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35602, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35602), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_StaleUsageSnapshotSkipsPause_Issue2994
// keeps the quota reset in the future but ages the observation beyond validity.
// Selection admits the preferred target, protecting snapshot-age handling
// independently of reset time.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_StaleUsageSnapshotSkipsPause_Issue2994(t *testing.T) {
	ctx := context.Background()
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35701,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":   99.0,
				"auto_pause_5h_threshold": 0.95,
				// The future reset time isolates the stale-snapshot rule.
				"codex_5h_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339),
				// This three-hour-old snapshot exceeds the two-hour auto-pause validity period.
				"codex_usage_updated_at": time.Now().Add(-3 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35702, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35701), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_FreshExhaustedSnapshotStillPauses_Issue2994
// supplies a fresh exhausted observation with a future reset. Selection rejects
// the preferred target, protecting quota pausing when both snapshot and window
// remain current.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_FreshExhaustedSnapshotStillPauses_Issue2994(t *testing.T) {
	ctx := context.Background()
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35801,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"codex_5h_used_percent":   99.0,
				"auto_pause_5h_threshold": 0.95,
				"codex_5h_reset_at":       time.Now().Add(time.Hour).Format(time.RFC3339),
				// The one-minute-old snapshot is fresh, so the provider stays paused.
				"codex_usage_updated_at": time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 35802, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(35802), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_SkipsFreshlyRateLimitedSnapshotCandidate
// ranks a stale primary whose full snapshot record is now rate limited.
// Selection chooses the backup, protecting fresh eligibility before returning
// the target.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_SkipsFreshlyRateLimitedSnapshotCandidate(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10102)
	rateLimitedUntil := time.Now().Add(30 * time.Minute)
	stalePrimary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 32001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}}
	staleSecondary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 32002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	freshPrimary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 32001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}, RateLimitResetAt: &rateLimitedUntil}}
	freshSecondary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 32002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	snapshotCache := &openAISnapshotCacheStub{snapshotProviders: []*gatewayprovider.ExecutionProvider{stalePrimary, staleSecondary}, providersByID: map[int64]*gatewayprovider.ExecutionProvider{32001: freshPrimary, 32002: freshSecondary}}
	snapshotService := schedulercore.NewSnapshotService(snapshotCache, nil, nil, nil, nil)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{*freshPrimary, *freshSecondary}},
			Snapshot:  schedulerredis.NewSnapshotReader(snapshotService),
		},
		Shared: Shared{
			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
			Parameters: newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
		},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &groupID, "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(32002), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_ModelRateLimitOnlySkipsThatModel
// cools down one model on the preferred provider. Requests for that model use
// the backup while another model retains the primary, protecting model-specific
// limit scope.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_ModelRateLimitOnlySkipsThatModel(t *testing.T) {
	ctx := context.Background()
	resetAt := time.Now().Add(30 * time.Minute).Format(time.RFC3339)
	primary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 32101,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					"gpt-5.4": map[string]any{
						"rate_limit_reset_at": resetAt,
					},
				},
			},
		},
	}
	secondary := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 32102,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    5,
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:  Reads{Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{primary, secondary}}},
		Shared: Shared{},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.4", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(32102), provider.Record.ID)

	provider, err = svc.SelectProviderForModelWithExclusions(ctx, selectionFixtureGroupID(ctx), "", "gpt-5.3", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(32101), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderForModelWithExclusions_DBRuntimeRecheckSkipsStaleCachedCandidate
// leaves stale snapshots eligible while the database primary is rate limited.
// Selection returns the database-valid backup, protecting full-record runtime
// revalidation.
func TestOpenAIGatewayService_SelectProviderForModelWithExclusions_DBRuntimeRecheckSkipsStaleCachedCandidate(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10104)
	rateLimitedUntil := time.Now().Add(30 * time.Minute)
	stalePrimary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}}
	staleSecondary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	dbPrimary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34001, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}, RateLimitResetAt: &rateLimitedUntil}}
	dbSecondary := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34002, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID}}}
	snapshotCache := &openAISnapshotCacheStub{
		snapshotProviders: []*gatewayprovider.ExecutionProvider{stalePrimary, staleSecondary},
		providersByID:     map[int64]*gatewayprovider.ExecutionProvider{34001: stalePrimary, 34002: staleSecondary},
	}
	snapshotService := schedulercore.NewSnapshotService(snapshotCache, nil, nil, nil, nil)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{dbPrimary, dbSecondary}},
			Snapshot:  schedulerredis.NewSnapshotReader(snapshotService),
		},
		Shared: Shared{
			Parameters: newAdvancedSchedulerParametersForTest(&config.Config{}, "true"),
			Health:     gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{}),
		},
	}, &config.Config{})

	provider, err := svc.SelectProviderForModelWithExclusions(ctx, &groupID, "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, int64(34002), provider.Record.ID)
}

// TestOpenAIGatewayService_SelectProviderWithLoadAwareness_DBFreshGroupRecheckWaitsOnValidProvider
// moves the primary into another database group while the valid backup is busy.
// Selection waits on that backup, protecting membership revalidation before a
// wait result.
func TestOpenAIGatewayService_SelectProviderWithLoadAwareness_DBFreshGroupRecheckWaitsOnValidProvider(t *testing.T) {
	ctx := context.Background()
	groupID, otherGroupID := int64(10107), int64(10108)
	stalePrimary := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34201, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}}}
	staleBackup := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34202, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, GroupIDs: []int64{groupID}}}
	dbPrimary := *stalePrimary
	dbPrimary.Record.GroupIDs = []int64{otherGroupID}
	dbBackup := *staleBackup
	snapshotCache := &openAISnapshotCacheStub{
		snapshotProviders: []*gatewayprovider.ExecutionProvider{stalePrimary, staleBackup},
		providersByID:     map[int64]*gatewayprovider.ExecutionProvider{stalePrimary.Record.ID: stalePrimary, staleBackup.Record.ID: staleBackup},
	}
	cfg := &config.Config{}
	cfg.Scheduling.LoadBatch.Enabled = true
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Snapshot: schedulerredis.NewSnapshotReader(schedulercore.NewSnapshotService(snapshotCache,

				nil, nil, nil, nil)),
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{dbPrimary, dbBackup}},
		},

		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{staleBackup.Record.ID: false}}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, cfg)

	selection, err := svc.SelectProviderWithLoadAwareness(ctx, &groupID, "", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, staleBackup.Record.ID, selection.Provider.Record.ID)
	require.Equal(t, staleBackup.Record.ID, selection.WaitPlan.ProviderID)
}

// TestOpenAIGatewayService_RecheckSelectedOpenAIProviderFromDB_KeepsGroupBoundary
// supplies grouped and ungrouped records with absent or mismatched request
// groups. Database recheck returns no target, protecting a positive matching
// membership requirement.
func TestOpenAIGatewayService_RecheckSelectedOpenAIProviderFromDB_KeepsGroupBoundary(t *testing.T) {
	grouped := gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 34301, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{99}}}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{grouped}},
			Snapshot:  schedulerredis.NewSnapshotReader(schedulercore.NewSnapshotService(&openAISnapshotCacheStub{}, nil, nil, nil, nil)),
		},

		Shared: Shared{},
	}, &config.Config{})

	requestedGroupID := int64(100)

	for _, groupID := range []*int64{nil, &requestedGroupID} {
		fresh := svc.recheckSelectedOpenAIProviderFromDB(context.Background(), &grouped, groupID, capability.PlatformOpenAI, "gpt-5.1", false, "")
		require.Nil(t, fresh)
	}

	ungrouped := grouped
	ungrouped.Record.ID++
	ungrouped.Record.GroupIDs = nil
	standardSvc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: schedulerTestOpenAIProviderRepo{providers: []gatewayprovider.ExecutionProvider{grouped, ungrouped}},
			Snapshot: schedulerredis.NewSnapshotReader(
				schedulercore.NewSnapshotService(&openAISnapshotCacheStub{}, nil, nil,
					nil, nil)),
		},
		Shared: Shared{},
	}, &config.Config{})

	require.Nil(t, standardSvc.recheckSelectedOpenAIProviderFromDB(context.Background(), &grouped, nil, capability.PlatformOpenAI, "gpt-5.1", false, ""))
	require.Nil(t, standardSvc.recheckSelectedOpenAIProviderFromDB(context.Background(), &ungrouped, nil, capability.PlatformOpenAI, "gpt-5.1", false, ""))
}

// TestOpenAIGatewayService_GetSchedulableProvider_ExhaustedCodexExtraDoesNotSetRateLimit
// reads a target with exhausted quota extras and a future reset. Lookup returns
// it with no persisted rate-limit write, protecting separation of quota
// observations from runtime cooldown mutation.
func TestOpenAIGatewayService_GetSchedulableProvider_ExhaustedCodexExtraDoesNotSetRateLimit(t *testing.T) {
	resetAt := time.Now().Add(6 * 24 * time.Hour)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 701,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Extra: map[string]any{
				"codex_7d_used_percent": 100.0,
				"codex_7d_reset_at":     resetAt.UTC().Format(time.RFC3339),
			},
		},
	}
	repo := &openAICodexExtraListRepo{selectionProviderFixture: selectionProviderFixture{providers: []gatewayprovider.ExecutionProvider{provider}}, rateLimitCh: make(chan time.Time, 1)}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Providers: repo,
		}, Shared: Shared{},
	},
		nil)

	fresh, err := svc.getSchedulableProvider(context.Background(), provider.Record.ID)
	require.NoError(t, err)
	require.NotNil(t, fresh)
	require.Nil(t, fresh.Record.RateLimitResetAt)
	// Observe the buffered write channel for a bounded interval so an unexpected
	// persistence callback remains visible even if the tested path schedules it asynchronously.
	select {
	case persisted := <-repo.rateLimitCh:
		t.Fatalf("不应将已耗尽的 codex extra 提升为运行时限流状态: %v", persisted)
	case <-time.After(2 * time.Second):
	}
}

// TestOpenAIGatewayService_ListSchedulableProviders_FiltersThresholdBlockedProviders
// supplies current quota observations above and below an effective scheduling
// threshold. Candidate reads retain the eligible provider and record one health
// pause action, protecting the bound threshold adapter.
func TestOpenAIGatewayService_ListSchedulableProviders_FiltersThresholdBlockedProviders(t *testing.T) {
	settingsRepo := settingstestkit.NewMemory()
	settingsRepo.Data[providercore.SettingKeyProviderSchedulingThresholds] = `{"openai":85}`

	providerRepo := &thresholdSelectionProviderRepoStub{
		providers: []gatewayprovider.ExecutionProvider{
			{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 4101,
					Platform:    capability.PlatformOpenAI,
					Status:      billing.StatusActive,
					Schedulable: true,
					Extra: map[string]any{
						"codex_7d_used_percent": 91.0,
						"codex_7d_reset_at":     time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 4102,
					Platform:    capability.PlatformOpenAI,
					Status:      billing.StatusActive,
					Schedulable: true,
					Extra: map[string]any{
						"codex_7d_used_percent": 40.0,
						"codex_7d_reset_at":     time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339),
					},
				},
			},
		},
	}

	healthObserver := gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: providerRepo, Readers: gatewaytestkit.RuntimeReaders(settings.New(settingsRepo))})
	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{Providers: providerRepo}, Shared: Shared{Health: healthObserver}}, &config.Config{})

	providers, err := svc.listSchedulableProviders(context.Background(), selectionFixtureGroupID(context.Background()), capability.PlatformOpenAI)

	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.Equal(t, int64(4102), providers[0].Record.ID)
	require.Equal(t, 1, providerRepo.TempCalls)
}

// ListSchedulableByGroupIDAndPlatform returns fixture platform matches
// belonging to the requested group.
func (r groupAwareStubOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform && openAIStickyProviderMatchesGroup(&acc, &groupID) {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableUngroupedByPlatform evaluates stored membership with a nil
// request group. The membership predicate rejects all targets, so this
// isolation fixture returns an empty pool for callers lacking an admitted
// group.
func (r groupAwareStubOpenAIProviderRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform && openAIStickyProviderMatchesGroup(&acc, nil) {
			result = append(result, acc)
		}
	}
	return result, nil
}

// SetRateLimited sends the observed reset time synchronously to the optional
// fixture channel. Nil channels are ignored; callers provide capacity or an
// active receiver so an unexpected write can be observed without blocking the
// test.
func (r *openAICodexExtraListRepo) SetRateLimited(_ context.Context, _ int64, at time.Time) error {
	if r.rateLimitCh != nil {
		// This hook reports an unexpected persistence attempt to the waiting test.
		// Use the caller-supplied buffer or receiver so observation can complete synchronously.
		r.rateLimitCh <- at
	}
	return nil
}

// ListSchedulableByGroupIDAndPlatforms filters explicit membership and platform
// before preparing target defaults. Returned values share source record
// references, keeping membership isolation independent of fixture default
// injection.
func (r groupAwareStubOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for i := range r.providers {
		if slices.Contains(platforms, r.providers[i].Record.Platform) && openAIStickyProviderMatchesGroup(&r.providers[i], &groupID) {
			prepareSelectionFixtureProvider(ctx, &r.providers[i], &groupID)
			result = append(result, r.providers[i])
		}
	}
	return result, nil
}

// GetByID prepares a shallow matching target copy, then restores its supplied
// GroupIDs. Missing targets return an error; referenced credentials and other
// record data stay shared with the fixture source.
func (r groupAwareStubOpenAIProviderRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			copy := r.providers[i]
			groups := copy.Record.GroupIDs
			prepareSelectionFixtureProvider(ctx, &copy, nil)
			copy.Record.GroupIDs = groups
			return &copy, nil
		}
	}
	return nil, errors.New("provider not found")
}

// TestOpenAISelectProviderWithLoadAwareness_HydratesSelectedProviderFromSchedulerSnapshot
// selects a lightweight candidate without its execution API key. The result
// receives the full cached key, protecting credential hydration for
// snapshot-only compatible selection.
func TestOpenAISelectProviderWithLoadAwareness_HydratesSelectedProviderFromSchedulerSnapshot(t *testing.T) {
	cache := &snapshotHydrationCache{
		snapshot: []*gatewayprovider.ExecutionProvider{
			{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Status:      billing.StatusActive,
					Schedulable: true,
					Concurrency: 1,
					Priority:    1,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gpt-4": "gpt-4",
						},
					},
				},
			},
		},
		providers: map[int64]*gatewayprovider.ExecutionProvider{
			1: {
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformOpenAI,
					Type:        capability.ProviderTypeAPIKey,
					Status:      billing.StatusActive,
					Schedulable: true,
					Concurrency: 1,
					Priority:    1,
					Credentials: map[string]any{
						"api_key":       "sk-live",
						"model_mapping": map[string]any{"gpt-4": "gpt-4"},
					},
				},
			},
		},
	}

	schedulerSnapshot := newHydrationSnapshotForTest(cache, nil)
	groupID := int64(2)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Snapshot: schedulerredis.NewSnapshotReader(schedulerSnapshot),
		},
		Shared: Shared{Cache: &responseCacheFixture{}},
	}, nil)

	selection, err := svc.SelectProviderWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectProviderWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Provider == nil {
		t.Fatalf("expected selected provider")
	}
	if got := selection.Provider.View().GetOpenAIApiKey(); got != "sk-live" {
		t.Fatalf("expected hydrated api key, got %q", got)
	}
}

// TestOpenAINewAcquiredSelectionResult_ReleasesSlotWhenHydrationFails omits the
// full record of an already acquired target. Preparation returns an error and
// invokes release once, protecting capacity cleanup before exposing hydration
// failure.
func TestOpenAINewAcquiredSelectionResult_ReleasesSlotWhenHydrationFails(t *testing.T) {
	cache := &snapshotHydrationCache{
		providers: map[int64]*gatewayprovider.ExecutionProvider{},
	}
	schedulerSnapshot := newHydrationSnapshotForTest(cache, selectionProviderFixture{})
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Snapshot: schedulerredis.NewSnapshotReader(schedulerSnapshot),
		},
		Shared: Shared{},
	}, nil)

	releaseCalls := 0

	selection, err := svc.newAcquiredSelectionResult(context.Background(), &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, ID: 1001}}, func() {
		releaseCalls++
	})

	if err == nil {
		t.Fatalf("expected hydration error")
	}
	if selection != nil {
		t.Fatalf("expected nil selection on hydration error")
	}
	if releaseCalls != 1 {
		t.Fatalf("expected release to be called once, got %d", releaseCalls)
	}
}
