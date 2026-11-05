//go:build integration

package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpg "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// Completion replay reaches persistent billing and usage stores through the
// application's recorder constructors. Both Messages and OpenAI scenarios
// record one input twice, then inspect balance, Key quota, and usage-row counts
// to check settlement deduplication.

// The test constructor aliases use production recorder and rate-cache assembly
// for the persistent replay scenarios.
var (
	NewCompletionRecordersForTest = ProvideGatewayCompletionRecorders
	NewGatewayBillingRatesForTest = provideGatewayBillingRates
)

// nativeCompletionCatalog supplies fixed token prices for persistent completion
// replay.
type nativeCompletionCatalog struct {
	// PriceCatalog supplies unused catalog methods; this fixture implements
	// the pricing lookup needed by the token settlement calculation exercised here.
	billing.PriceCatalog
}

// nativeCompletionSQL exposes test SQL methods for synchronous usage
// persistence.
type nativeCompletionSQL struct {
	*sql.DB // fixture SQL connection
}

// GetModelPricing returns fixed input and output prices for the completion
// fixture.
func (nativeCompletionCatalog) GetModelPricing(string) *pricing.CatalogModelPricing {
	return &pricing.CatalogModelPricing{InputCostPerToken: 0.01, OutputCostPerToken: 0.02}
}

// TestNativeCompletionRuntimeOneFinancialEffect records the same completion
// twice through each application-assembled recorder. The database must show one
// balance deduction, one Key quota increment, and one usage row for each
// request identity.
func TestNativeCompletionRuntimeOneFinancialEffect(t *testing.T) {
	f := newDatabaseFixture(t)
	ctx := t.Context()
	calendar := timezone.NewCalendar(time.UTC)
	providers := providerpg.NewProviderStore(f.client, f.db, providerpg.ProviderStoreOptions{})
	funds := billingpg.NewSettlementStore(f.db, calendar, nil)
	logs := usagepg.NewUsageLogRepositoryWithSQL(f.client, nativeCompletionSQL{f.db}, calendar)
	wheel := timingwheel.New()
	deferred := provider.NewDeferredService(providers, wheel, provider.DeferredOptions{})
	tasks := lifecycle.NewTasks()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// Drain tracked settlement effects before deferred writes and the timing
		// wheel stop, then close usage batchers while the fixture database remains open.
		require.NoError(t, tasks.Stop(cleanup))
		require.NoError(t, deferred.StopContext(cleanup))
		wheel.Stop()
		logs.StopUsageBatchers()
	})
	cfg := &config.Config{}
	cfg.Billing.FallbackRateMultiplier = 1
	rates := NewGatewayBillingRatesForTest(nil, cfg)
	health := NewProviderHealthRuntimeForTest(providers, nil, cfg, nil, nil, nil, nil, nil)
	calculator := billing.NewCalculator(nativeCompletionCatalog{}, billing.CalculatorOptions{})
	prices := billing.NewPriceResolver(nil, calculator, nil, nil, nil)
	recorders := NewCompletionRecordersForTest(nil, rates, calculator, prices, funds, logs, nil, nil, deferred, nil, nil, providers, health, nil, tasks, cfg)
	for _, openAI := range []bool{false, true} {
		name := "messages"
		if openAI {
			name = "openai"
		}
		t.Run(name, func(t *testing.T) {
			user, err := f.client.User.Create().SetEmail("completion-" + name + "@fixture.test").SetPasswordHash("fixture-only").SetBalance(10).Save(ctx)
			require.NoError(t, err)
			key, err := f.client.APIKey.Create().SetUserID(user.ID).SetName("fixture").SetKey("sk-completion-" + name).SetQuota(100).SetBillingMode("balance").Save(ctx)
			require.NoError(t, err)
			selected, err := f.client.Provider.Create().SetName("completion-" + name).SetPlatform("openai").SetType("apikey").Save(ctx)
			require.NoError(t, err)
			requestID := "test-completion-" + name
			input := &completion.Input{
				RequestID:          requestID,
				QuotaUpdates:       true,
				Result:             &completion.Result{RequestID: requestID, Model: "fixture-cost", Usage: completion.TokenUsage{InputTokens: 100}},
				APIKey:             &completion.KeySnapshot{ID: key.ID, Key: key.Key, BillingMode: "balance", Quota: 100},
				User:               &completion.PayerSnapshot{ID: user.ID, Balance: 10},
				Provider:           &completion.ProviderSnapshot{ID: selected.ID, Platform: "openai", Type: "apikey", OpenAI: true, RateMultiplier: 1},
				RequestPayloadHash: "fixture-payload",
			}
			recorder := recorders.Forward
			if openAI {
				recorder = recorders.OpenAI
			}
			require.NoError(t, recorder.Record(ctx, input, openAI))
			require.NoError(t, recorder.Record(ctx, input, openAI))
			var balance, quota float64
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id=$1", key.ID).Scan(&quota))
			// Floating-point amounts allow a small comparison tolerance after SQL round trips.
			require.InDelta(t, 9, balance, 1e-8)
			require.InDelta(t, 1, quota, 1e-8)
			var facts int
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", requestID, key.ID).Scan(&facts))
			require.Equal(t, 1, facts)
		})
	}
}
