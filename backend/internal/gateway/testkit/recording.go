// Package testkit 只组合原生完成依赖，业务计算、资金与副作用仍由真实模块执行。
package testkit

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// Recording 保存测试显式设置的原生依赖，不拥有算法、缓存副本或旧网关实体。
type Recording struct {
	Dependencies  completion.Dependencies
	Options       completion.RecorderOptions
	GroupPolicies *routing.PricingConfigService
	Effects       completion.CommitEffects
	AccountLookup func(context.Context, int64) (*account.Record, error)
}

// NewRecording 保留原记录夹具的默认倍率、缓存期限与原生计算器构造。
func NewRecording(logs usage.UsageLogRepository, funds completion.Store, rates billing.UserGroupRateRepository, withResolver bool) *Recording {
	calculator := billingtestkit.Calculator(1.1, nil, nil)
	fixture := &Recording{
		Options: completion.RecorderOptions{DefaultMultiplier: 1.1},
		Dependencies: completion.Dependencies{
			Calculator: calculator, Funds: funds, Logs: completion.SnapshotLogWriter(logs),
			Rates:  billing.NewGroupRateResolver(rates, nil, 30*time.Second, nil, "service.openai_gateway.test", logging.LegacyPrintf),
			Models: provider.CompletionModels{}, Emit: gatewaytelemetry.CompletionBillingEvent, Observe: gatewaytelemetry.ObserveCompletion,
		},
		Effects: completion.CommitEffects{
			Activity: &account.DeferredService{}, Observe: gatewaytelemetry.ObserveCompletion,
			Funds: billing.SettlementEffects{
				Background: func(_ string, fn func()) bool { go fn(); return true },
				Observe:    logging.LegacyPrintf,
				BalanceWarning: func(id int64, balance float64, err error) {
					slog.Warn("invalidate balance cache after exhausted deduction failed", "user_id", id, "new_balance", balance, "error", err)
				},
			},
		},
	}
	if withResolver {
		fixture.Dependencies.Prices = billingtestkit.PriceResolver(nil, calculator)
	}
	return fixture
}

// Core 在与原独立夹具相同的时点绑定输入引用，状态仍沿用 Dependencies 中的同一实例。
func (f *Recording) Core(updater provider.QuotaUpdater, openAI bool) *completion.Recorder {
	deps := f.Dependencies
	deps.Subscriptions, _ = deps.Funds.(completion.SubscriptionReader)
	var stats billing.AccountStatsSource
	if f.GroupPolicies != nil {
		stats = provider.AccountStatsSource{Service: f.GroupPolicies}
	}
	deps.AccountStats = billing.NewPriceResolver(nil, deps.Calculator, nil, nil, stats)
	effects := f.Effects
	if auth, ok := updater.(completion.AuthInvalidator); ok {
		effects.Auth = auth
	}
	deps.Effects = &effects
	if openAI {
		deps.Accounts = recordAccounts{lookup: f.AccountLookup}
	}
	return completion.NewRecorder(deps, f.Options)
}

// RecordOpenAI 测试通过生产捕获和完成入口验证观测与资金结果。
func (f *Recording) RecordOpenAI(ctx context.Context, in *provider.OpenAICapture) error {
	var updater provider.QuotaUpdater
	if in != nil {
		updater = in.APIKeyService
	}
	return f.Core(updater, true).Record(ctx, provider.CaptureOpenAI(ctx, in), true)
}

// RecordMessages 与普通 Messages 完成链共享相同捕获与记录实现。
func (f *Recording) RecordMessages(ctx context.Context, in *provider.MessagesCapture) error {
	return f.Core(in.APIKeyService, false).Record(ctx, provider.CaptureMessages(ctx, in), false)
}

type recordAccounts struct {
	lookup func(context.Context, int64) (*account.Record, error)
}

func (p recordAccounts) CredentialAccount(ctx context.Context, in completion.AccountSnapshot) (*completion.AccountSnapshot, error) {
	source := &account.Record{ID: in.ID, Platform: in.Platform, Type: in.Type, ParentAccountID: in.CredentialAccountID}
	record, err := account.ResolveCredentialRecord(ctx, p.lookup, source)
	if err != nil {
		return nil, err
	}
	if record == source {
		return &in, nil
	}
	return provider.ProjectCompletionAccount(record), nil
}
