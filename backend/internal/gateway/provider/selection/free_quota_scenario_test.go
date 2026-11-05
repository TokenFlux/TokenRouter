package selection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TestOpenAIProviderSchedulerLoadBalanceAppliesGrokFreeQuotaGate warms Free
// OAuth usage above the configured soft threshold. Load-balanced compatible
// selection then excludes the free provider while retaining the paid provider
// in the same pool.
func TestOpenAIProviderSchedulerLoadBalanceAppliesGrokFreeQuotaGate(t *testing.T) {
	cfg := grokFreeQuotaTestConfig()

	providers := []gatewayprovider.ExecutionProvider{
		{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"model_whitelist": []string{"*"}, "subscription_tier": "free"}}},
		{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"model_whitelist": []string{"*"}, "subscription_tier": "pro"}}},
	}
	reader := &grokFreeQuotaUsageRepoStub{stats: map[int64]*usage.ProviderStats{1: {Tokens: 480_000}}}
	factory := freeQuotaFactoryForTest(t, cfg, reader)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads:                Reads{Providers: selectionProviderFixture{providers: providers}},
		FreeQuota:            factory(),
		NewAdvancedFreeQuota: factory,
	}, cfg)
	picker := &compatiblePicker{service: svc, stats: schedulercore.NewRuntimeStats(time.Now)}

	// Initial unknown usage admits candidates while the tracked background query
	// runs. Wait for its cached observation before testing load-balanced exclusion.
	_ = picker.filterGrokFreeQuotaProviders(context.Background(), providers)
	require.Eventually(t, func() bool {
		filtered := picker.filterGrokFreeQuotaProviders(context.Background(), providers)
		return len(providerIDs(filtered)) == 1 && providerIDs(filtered)[0] == 2
	}, 2*time.Second, 10*time.Millisecond)

	core, scope := picker.platformSelector()
	result, _, _, _, err := core.SelectByLoadBalance(context.Background(), schedulercore.PlatformSelectionInput{GroupID: selectionFixtureGroupID(context.Background()), Platform: capability.PlatformGrok})
	selection := scope.restore(result)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Provider)
	require.Equal(t, int64(2), selection.Provider.Record.ID)
}

// grokFreeQuotaTestConfig enables a ninety-five-percent gate over a rolling
// twenty-four-hour token window.
func grokFreeQuotaTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Scheduling.GrokFreeQuota.Enabled = true
	cfg.Scheduling.GrokFreeQuota.TokenLimit = 500_000
	cfg.Scheduling.GrokFreeQuota.ThresholdPercent = 95
	cfg.Scheduling.GrokFreeQuota.WindowHours = 24
	cfg.Scheduling.GrokFreeQuota.StatsCacheSeconds = 60
	return cfg
}

// providerIDs collects fixture identifiers in the same order as filtered
// candidates.
func providerIDs(providers []gatewayprovider.ExecutionProvider) []int64 {
	ids := make([]int64, 0, len(providers))
	for i := range providers {
		ids = append(ids, providers[i].Record.ID)
	}
	return ids
}
