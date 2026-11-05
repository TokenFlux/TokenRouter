package app

import (
	"context"
	"log/slog"
	"time"

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
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/requestlog"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// gateway_completion_billing.go constructs gateway completion recorders and
// resolves credential ownership for provider statistics.
// Messages and OpenAI completion use separate rate readers with shared pricing,
// funds, and usage storage. OpenAI additionally resolves credential-parent
// providers and clears forbidden counters, while both recorders send settlement
// side effects through the application task tracker.
//
// Credential resolution starts from the completion snapshot identifier and
// parent reference. When the resolver returns that input record, the existing
// snapshot is returned intact so fields absent from the temporary record
// remain available. A resolved parent receives its own projected snapshot.
// Lookup errors stop the conversion, preventing statistics from silently using
// a different credential owner after an unsuccessful read.

// GatewayCompletionRecorders holds the two recorders bound to platform attempt
// runtimes.
type GatewayCompletionRecorders struct {
	// Forward and OpenAI are passed unchanged into their respective attempt
	// bindings. Each uses the matching rate cache supplied by gatewayBillingRates.
	Forward, OpenAI *completion.Recorder // Forward: Messages completion recorder; OpenAI: compatible completion recorder
}

// completionProviders resolves credential ownership for OpenAI completion
// provider statistics.
type completionProviders struct {
	store *providerpostgres.ProviderStore // credential parent reader
}

// completionHealth connects successful OpenAI completion to provider forbidden-
// counter reset.
type completionHealth struct {
	core *provider.HealthService // provider health commands
}

// ProvideGatewayCompletionRecorders constructs Messages and OpenAI recorders
// around shared billing dependencies. rates, health, and tasks are required by
// assembly; nil cfg selects multiplier one, optional settings enable Forward
// cache injection, and subscriptions are available when funds implements their
// read interface.
func ProvideGatewayCompletionRecorders(
	requests *requestlog.Service,
	rates *gatewayBillingRates,
	calculator *billing.Calculator,
	prices *billing.PriceResolver,
	funds completion.Store,
	logs usage.UsageLogRepository,
	modelConfigs *routing.PricingConfigService,
	eligibility *billing.Eligibility,
	deferred *provider.DeferredService,
	notifications *billing.BalanceNotifyService,
	keys *apikey.APIKeyService,
	providers *providerpostgres.ProviderStore,
	health *providerHealthRuntime,
	settings *gateway.RuntimeSettings,
	tasks *lifecycle.Tasks,
	cfg *config.Config,
) GatewayCompletionRecorders {
	options := completion.RecorderOptions{DefaultMultiplier: 1, Now: timezone.NewCalendar(time.Local).Now}
	if cfg != nil {
		options.DefaultMultiplier = cfg.Billing.FallbackRateMultiplier
	}
	subscriptions, _ := funds.(completion.SubscriptionReader)
	effects := func() *completion.CommitEffects {
		return gatewayCommitEffects(deferred, eligibility, notifications, keys, tasks, cfg)
	}
	stats := func() *billing.PriceResolver {
		var source billing.ProviderStatsSource
		if modelConfigs != nil {
			source = gatewayprovider.ProviderStatsSource{Service: modelConfigs}
		}
		return billing.NewPriceResolver(nil, calculator, nil, nil, source)
	}
	common := func(rate completion.RateReader) completion.Dependencies {
		return completion.Dependencies{
			RequestRecords: requests.Observe,
			Emit:           gatewaytelemetry.CompletionBillingEvent,
			Calculator:     calculator,
			Prices:         prices,
			ProviderStats:  stats(),
			Funds:          funds,
			Subscriptions:  subscriptions,
			Rates:          rate,
			Models:         gatewayprovider.CompletionModels{},
			Logs:           completion.SnapshotLogWriter(logs),
			Effects:        effects(),
			Observe:        gatewaytelemetry.ObserveCompletion,
		}
	}
	forward := common(rates.Forward)
	if settings != nil {
		forward.CacheInjection = settings
	}
	openai := common(rates.OpenAI)
	openai.Providers = completionProviders{store: providers}
	openai.Health = completionHealth{core: health.Health}
	return GatewayCompletionRecorders{Forward: completion.NewRecorder(forward, options), OpenAI: completion.NewRecorder(openai, options)}
}

// CredentialProvider resolves the credential-owning provider for a completion
// snapshot. When resolution returns the input record, it returns the supplied
// snapshot; a parent record is converted through ProjectCompletionProvider, and
// lookup errors stop the conversion.
func (p completionProviders) CredentialProvider(ctx context.Context, in completion.ProviderSnapshot) (*completion.ProviderSnapshot, error) {
	original := &provider.Record{ID: in.ID, Platform: in.Platform, Type: in.Type, ParentProviderID: in.CredentialProviderID}
	value, err := provider.ResolveCredentialRecord(ctx, p.store.GetByID, original)
	if err != nil {
		return nil, err
	}
	if value == original {
		return &in, nil
	}
	return gatewayprovider.ProjectCompletionProvider(value), nil
}

// ResetOpenAI403Counter delegates successful-completion counter reset to
// provider health.
func (p completionHealth) ResetOpenAI403Counter(ctx context.Context, id int64) {
	p.core.ResetForbiddenCounter(ctx, id)
}

// gatewayCommitEffects binds activity, funds-cache, authentication, and
// optional notification effects. tasks is required for tracked settlement work;
// the returned handlers run when completion invokes post-commit processing, and
// cfg is currently unused by this adapter.
func gatewayCommitEffects(deferred *provider.DeferredService, eligibility *billing.Eligibility, notifications *billing.BalanceNotifyService, keys *apikey.APIKeyService, tasks *lifecycle.Tasks, cfg *config.Config) *completion.CommitEffects {
	value := &completion.CommitEffects{
		Activity: deferred,
		Observe:  gatewaytelemetry.ObserveCompletion,
		Funds: billing.SettlementEffects{
			Cache:      eligibility,
			Background: tasks.Go,
			Observe:    logging.LegacyPrintf,
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
