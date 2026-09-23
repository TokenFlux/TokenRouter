//go:build integration

package app_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpg "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
	"github.com/stretchr/testify/require"
)

type nativeCompletionCatalog struct{ billing.PriceCatalog }

func (nativeCompletionCatalog) GetModelPricing(string) *pricing.LiteLLMModelPricing {
	return &pricing.LiteLLMModelPricing{InputCostPerToken: 0.01, OutputCostPerToken: 0.02}
}

// 直接执行同一存储 SQL，避免本装配契约额外启动写入批处理。
type nativeCompletionSQL struct{ *sql.DB }

// 两种生产记录器都走真实结算与事实存储；重放不能再次扣款或写第二条事实。
func TestS16NativeCompletionRuntimeOneFinancialEffect(t *testing.T) {
	f := newDatabaseFixture(t)
	ctx := t.Context()
	calendar := timezone.NewCalendar(time.UTC)
	accounts := accountpg.NewAccountStore(f.client, f.db, accountpg.AccountStoreOptions{})
	funds := billingpg.NewSettlementStore(f.db, calendar, nil)
	logs := usagepg.NewUsageLogRepositoryWithSQL(f.client, nativeCompletionSQL{f.db}, calendar)
	wheel := timingwheel.New()
	deferred := account.NewDeferredService(accounts, wheel, account.DeferredOptions{})
	tasks := lifecycle.NewTasks()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, tasks.Stop(cleanup))
		require.NoError(t, deferred.StopContext(cleanup))
		wheel.Stop()
		logs.StopUsageBatchers()
	})
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	rates := app.NewS16GatewayBillingRates(nil, cfg)
	health := app.NewS16AccountHealthRuntime(accounts, nil, cfg, nil, nil, nil, nil, nil)
	calculator := billing.NewCalculator(nativeCompletionCatalog{}, billing.CalculatorOptions{DefaultRateMultiplier: 1})
	prices := billing.NewPriceResolver(nil, calculator, nil, nil, nil)
	recorders := app.NewS16CompletionRecorders(rates, calculator, prices, funds, logs, nil, nil, nil, deferred, nil, nil, accounts, health, nil, tasks, cfg)
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
			selected, err := f.client.Account.Create().SetName("completion-" + name).SetPlatform("openai").SetType("apikey").Save(ctx)
			require.NoError(t, err)
			requestID := "s16-completion-" + name
			input := &completion.Input{
				RequestID:          requestID,
				QuotaUpdates:       true,
				Result:             &completion.Result{RequestID: requestID, Model: "fixture-cost", Usage: completion.TokenUsage{InputTokens: 100}},
				APIKey:             &completion.KeySnapshot{ID: key.ID, Key: key.Key, BillingMode: "balance", Quota: 100},
				User:               &completion.PayerSnapshot{ID: user.ID, Balance: 10},
				Account:            &completion.AccountSnapshot{ID: selected.ID, Platform: "openai", Type: "apikey", OpenAI: true, RateMultiplier: 1},
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
			require.InDelta(t, 9, balance, 1e-8)
			require.InDelta(t, 1, quota, 1e-8)
			var facts int
			require.NoError(t, f.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", requestID, key.ID).Scan(&facts))
			require.Equal(t, 1, facts)
		})
	}
}
