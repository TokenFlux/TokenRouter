package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// GatewayCompletionRecorders 对外发布两条已装配完成链，保留原倍率缓存作用域。
type GatewayCompletionRecorders struct{ Forward, OpenAI *completion.Recorder }

// ProvideGatewayCompletionRecorders 直接组合原生价格、资金、观测和提交后能力，不从旧网关取回实例。
func ProvideGatewayCompletionRecorders(
	rates *gatewayBillingRates,
	calculator *billing.Calculator,
	prices *billing.PriceResolver,
	funds completion.Store,
	logs usage.UsageLogRepository,
	channels *routing.ChannelService,
	eligibility *billing.Eligibility,
	quotas billing.UserPlatformQuotaRepository,
	deferred *account.DeferredService,
	notifications *billing.BalanceNotifyService,
	keys *apikey.APIKeyService,
	accounts *accountpostgres.AccountStore,
	health *accountHealthRuntime,
	settings *gateway.RuntimeSettings,
	tasks *lifecycle.Tasks,
	cfg *config.Config,
) GatewayCompletionRecorders {
	options := completion.RecorderOptions{DefaultMultiplier: 1, Now: timezone.NewCalendar(time.Local).Now}
	if cfg != nil {
		options.Simple = cfg.RunMode == config.RunModeSimple
		options.DefaultMultiplier = cfg.Default.RateMultiplier
	}
	subscriptions, _ := funds.(completion.SubscriptionReader)
	effects := func() *completion.CommitEffects {
		value := &completion.CommitEffects{
			Activity: deferred,
			Observe:  gatewaytelemetry.ObserveCompletion,
			Funds: billing.SettlementEffects{
				Cache:          eligibility,
				Quotas:         quotas,
				FlusherEnabled: cfg != nil && cfg.Database.UserPlatformQuotaFlusherEnabled,
				Background:     tasks.Go,
				Observe:        logging.LegacyPrintf,
				BalanceWarning: func(id int64, balance float64, err error) {
					slog.Warn("invalidate balance cache after exhausted deduction failed", "user_id", id, "new_balance", balance, "error", err)
				},
			},
		}
		value.Auth = keys
		if notifications != nil {
			value.Notifications = notifications
		}
		return value
	}
	stats := func() *billing.PriceResolver {
		var source billing.AccountStatsSource
		if channels != nil {
			source = gatewayprovider.AccountStatsSource{Service: channels}
		}
		return billing.NewPriceResolver(nil, calculator, nil, nil, source)
	}
	common := func(rate completion.RateReader) completion.Dependencies {
		return completion.Dependencies{
			Emit:          gatewaytelemetry.CompletionBillingEvent,
			Calculator:    calculator,
			Prices:        prices,
			AccountStats:  stats(),
			Funds:         funds,
			Subscriptions: subscriptions,
			Rates:         rate,
			Models:        gatewayprovider.CompletionModels{},
			Logs:          completion.SnapshotLogWriter(logs),
			Effects:       effects(),
			Observe:       gatewaytelemetry.ObserveCompletion,
		}
	}
	forward := common(rates.Forward)
	if settings != nil {
		forward.CacheInjection = settings
	}
	openai := common(rates.OpenAI)
	openai.Accounts = completionAccounts{store: accounts}
	openai.Health = completionHealth{core: health.Health}
	return GatewayCompletionRecorders{Forward: completion.NewRecorder(forward, options), OpenAI: completion.NewRecorder(openai, options)}
}

// completionAccounts 只在影子统计需要时读取母账号，不将凭据传给完成核心。
type completionAccounts struct{ store *accountpostgres.AccountStore }

func (p completionAccounts) CredentialAccount(ctx context.Context, in completion.AccountSnapshot) (*completion.AccountSnapshot, error) {
	original := &account.Record{ID: in.ID, Platform: in.Platform, Type: in.Type, ParentAccountID: in.CredentialAccountID}
	value, err := account.ResolveCredentialRecord(ctx, p.store.GetByID, original)
	if err != nil {
		return nil, err
	}
	if value == original {
		return &in, nil
	}
	return gatewayprovider.ProjectCompletionAccount(value), nil
}

type completionHealth struct{ core *account.HealthService }

func (p completionHealth) ResetOpenAI403Counter(ctx context.Context, id int64) {
	p.core.ResetForbiddenCounter(ctx, id)
}
