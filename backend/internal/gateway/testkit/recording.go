package testkit

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// recording.go constructs usage-recording fixtures with production capture,
// calculation, and billing services. The recording fixture supplies a
// calculator, default multiplier and rate-cache settings, then builds a
// recorder using the current dependency and effect selections for each
// assertion.

// Recording holds dependencies and options for usage-recording tests.
type Recording struct {
	Dependencies   completion.Dependencies                                // base production completion recording services
	Options        completion.RecorderOptions                             // production recorder option values
	GroupPolicies  *routing.PricingConfigService                          // optional group pricing policy reader
	Effects        completion.CommitEffects                               // completion effect callback configuration
	ProviderLookup func(context.Context, int64) (*provider.Record, error) // credential provider fixture read callback
}

// recordProviders resolves credential ownership using the fixture's provider
// lookup callback.
type recordProviders struct {
	lookup func(context.Context, int64) (*provider.Record, error) // retained credential provider read callback
}

// NewRecording constructs a usage fixture with multiplier 1.1, a thirty-second
// user-group rate cache, and a billing calculator. withResolver installs
// default price resolution; effect background work starts detached goroutines
// that tests await when asserting asynchronous results.
func NewRecording(logs usage.UsageLogRepository, funds completion.Store, rates billing.UserGroupRateRepository, withResolver bool) *Recording {
	calculator := billingtestkit.Calculator(nil, nil)
	fixture := &Recording{
		Options: completion.RecorderOptions{DefaultMultiplier: 1.1},
		Dependencies: completion.Dependencies{
			Calculator: calculator, Funds: funds, Logs: completion.SnapshotLogWriter(logs),
			Rates:  billing.NewGroupRateResolver(rates, nil, 30*time.Second, nil, "service.openai_gateway.test", logging.LegacyPrintf),
			Models: gatewayadapter.CompletionModels{}, Emit: gatewaytelemetry.CompletionBillingEvent, Observe: gatewaytelemetry.ObserveCompletion,
		},
		Effects: completion.CommitEffects{
			Activity: &provider.DeferredService{}, Observe: gatewaytelemetry.ObserveCompletion,
			Funds: billing.SettlementEffects{
				// Admit effects immediately on detached goroutines; completion is separate
				// from acceptance, so tests arrange their own wait before asynchronous
				// assertions.
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

// Core builds a production recorder from the fixture's current services and
// options. It copies dependency and effect structs while retaining their
// referenced services; subscription and authentication capabilities are
// detected from the supplied implementations. OpenAI mode installs
// credential-provider lookup.
func (f *Recording) Core(updater gatewayadapter.QuotaUpdater, openAI bool) *completion.Recorder {
	deps := f.Dependencies
	deps.Subscriptions, _ = deps.Funds.(completion.SubscriptionReader)
	var stats billing.ProviderStatsSource
	if f.GroupPolicies != nil {
		stats = gatewayadapter.ProviderStatsSource{Service: f.GroupPolicies}
	}
	deps.ProviderStats = billing.NewPriceResolver(nil, deps.Calculator, nil, nil, stats)
	effects := f.Effects
	if updater != nil {
		effects.Auth = updater
	}
	deps.Effects = &effects
	if openAI {
		deps.Providers = recordProviders{lookup: f.ProviderLookup}
	}
	return completion.NewRecorder(deps, f.Options)
}

// RecordOpenAI captures and records an OpenAI completion through a freshly
// built recorder. Nil input selects no quota updater; production CaptureOpenAI
// determines the snapshot and Record determines whether it can settle.
func (f *Recording) RecordOpenAI(ctx context.Context, in *gatewayadapter.OpenAICapture) error {
	var updater gatewayadapter.QuotaUpdater
	if in != nil {
		updater = in.APIKeyService
	}
	return f.Core(updater, true).Record(ctx, gatewayadapter.CaptureOpenAI(ctx, in), true)
}

// RecordMessages captures and records a Messages completion using its supplied
// key service. Callers provide a nonnil capture input; the constructed
// recorder applies production funding, quota, and usage rules.
func (f *Recording) RecordMessages(ctx context.Context, in *gatewayadapter.MessagesCapture) error {
	return f.Core(in.APIKeyService, false).Record(ctx, gatewayadapter.CaptureMessages(ctx, in), false)
}

// CredentialProvider resolves an optional credential parent from the
// completion provider snapshot. If resolution retains the constructed source,
// it returns the supplied snapshot by value; a parent becomes
// production-projected completion metadata and read errors propagate.
func (p recordProviders) CredentialProvider(ctx context.Context, in completion.ProviderSnapshot) (*completion.ProviderSnapshot, error) {
	source := &provider.Record{ID: in.ID, Platform: in.Platform, Type: in.Type, ParentProviderID: in.CredentialProviderID}
	record, err := provider.ResolveCredentialRecord(ctx, p.lookup, source)
	if err != nil {
		return nil, err
	}
	if record == source {
		return &in, nil
	}
	return gatewayadapter.ProjectCompletionProvider(record), nil
}
