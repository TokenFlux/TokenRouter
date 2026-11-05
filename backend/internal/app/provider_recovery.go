package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
)

// provider_recovery.go constructs shared provider health, recovery, and
// platform observation services. Provider health observation and recovery share
// the same counters, storage, runtime blocks, and clock. Health delegates
// window clearing to the recovery instance assigned during assembly, and the
// exported runtime keeps one observer whose window, team, and model components
// reference that assembled health state.

// providerHealthRuntime retains shared health, recovery, and platform
// observation components.
type providerHealthRuntime struct {
	// Health and Recovery are bound together during construction, and Observer
	// routes platform observations through that Health instance. Consumers retain these same pointers across administration and execution.
	Health   *provider.HealthService         // shared health commands
	Recovery *provider.RecoveryService       // shared recovery commands
	Observer *provideradapter.UpstreamHealth // shared platform observations
}

// provideUpstreamHealth returns the platform observer from the constructed
// health runtime.
func provideUpstreamHealth(runtime *providerHealthRuntime) *provideradapter.UpstreamHealth {
	return runtime.Observer
}

// provideProviderHealthRuntime constructs mutually bound health observation and
// recovery services. cfg is required; optional settings, counters, token
// invalidation, and runtime blocks provide their capabilities, and the
// window-clear closure is called after recovery has been assigned.
func provideProviderHealthRuntime(
	store *providerpostgres.ProviderStore,
	cache provider.TempUnschedCache,
	cfg *config.Config,
	settings *provider.RuntimeSettings,
	timeouts provider.TimeoutCounterCache,
	forbidden provider.OpenAI403CounterCache,
	tokens provider.TokenCacheInvalidator,
	state *provider.RuntimeBlockState,
) *providerHealthRuntime {
	options := provider.HealthOptions{
		Now: time.Now, Warn: slog.Warn, Info: slog.Info,
		SessionWindows: store, TimeoutCounter: timeouts, ForbiddenCounter: forbidden,
		UnauthorizedCooldownMinutes: cfg.Provider.Cooldown.OAuth401Minutes,
		UsageMonitorIntervalMinutes: cfg.Provider.UsageMonitor.IntervalMinutes,
		OverloadMinutes:             cfg.Provider.Cooldown.OverloadMinutes,
		APIKeyHealthWarn:            provideradapter.LogAPIKeyHealthWarning,
	}
	if counter, ok := cache.(provider.OpenAIAPIKeyHealthCache); ok {
		options.APIKeyHealthCounter = counter
	}
	if settings != nil {
		options.APIKeyHealthSettings = settings.GetOpenAIAPIKeyHealthBreakerSettings
		options.RateLimit429Settings = settings.GetRateLimit429CooldownSettings
		options.ForbiddenSettings = settings.GetOpenAI403CooldownSettings
		options.OverloadSettings = settings.GetOverloadCooldownSettings
		options.HasThresholdSettings = func() bool { return true }
		options.Thresholds = settings.GetProviderSchedulingThresholds
		options.StreamSettings = func(ctx context.Context) (*provider.StreamTimeoutSettings, error, bool) {
			value, err := settings.GetStreamTimeoutSettings(ctx)
			return value, err, true
		}
	}
	if tokens != nil {
		options.InvalidateUnauthorizedToken = tokens.InvalidateToken
	}
	if state != nil {
		options.Block = state.BlockProviderScheduling
	}

	// Health window clearing closes over recovery while recovery resets the
	// health counter. Complete both bindings before returning so later callbacks use the assembled instances.
	var recovery *provider.RecoveryService
	options.ClearWindowRateLimit = func(ctx context.Context, id int64) error {
		return recovery.ClearRateLimit(ctx, id)
	}
	health := provider.NewHealthService(store, cache, options)
	recoveryOptions := provider.RecoveryOptions{Now: time.Now, Warn: slog.Warn, ResetCounter: health.ResetForbiddenCounter}
	if state != nil {
		recoveryOptions.ClearSchedulingBlock = state.ClearProviderSchedulingBlock
	}
	if tokens != nil {
		recoveryOptions.InvalidateToken = tokens.InvalidateToken
	}
	recovery = provider.NewRecoveryService(store, cache, recoveryOptions)

	limits := &provideradapter.RateLimitObserver{Health: health, Plans: store, NextGeminiDaily: func() *int64 {
		// Rate-limit observation expects a Unix-second Gemini quota reset timestamp.
		reset := provider.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix()
		return &reset
	}}
	if state != nil {
		limits.RetryOpenAI = func(value *provider.Record, headers http.Header, body []byte) bool {
			return provideradapter.CanRetryOpenAI429(state, value, headers, body)
		}
	}

	team := provider.NewTeamLinkedHealth(store, provider.TeamLinkedOptions{Now: options.Now, Warn: options.Warn, Block: options.Block})
	models := &provideradapter.ModelHealth{Health: health, ImageOutput: gatewayprovider.ResolvedImageOutput}

	return &providerHealthRuntime{
		Health: health, Recovery: recovery, Observer: &provideradapter.UpstreamHealth{Core: health, Team: team, Limits: limits, Models: models},
	}
}

// provideProviderRecovery returns the recovery service from the constructed
// health runtime.
func provideProviderRecovery(runtime *providerHealthRuntime) *provider.RecoveryService {
	return runtime.Recovery
}

// provideProviderRuntimeState constructs shared scheduling containment using
// the system clock.
func provideProviderRuntimeState() *provider.RuntimeBlockState {
	return provider.NewRuntimeBlockState(time.Now)
}
