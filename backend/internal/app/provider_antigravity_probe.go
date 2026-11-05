package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// provider_antigravity_probe.go assembles Antigravity authorization, retry
// health, probes, and quota queries. Antigravity probes and forwarding share
// retry health, transport, and request activity. Assembly supplies paid-tier
// endpoint choice, bounded response inspection, rate-limit reset parsing, and
// quota proxy lookup, keeping their request-time callbacks attached to the same
// provider services.

// provideAntigravityAuthorization binds platform authorization to stored proxy
// lookup. Its callback returns an empty URL and false for failed or absent
// proxy reads; proxies is required when that callback is invoked.
func provideAntigravityAuthorization(proxies egress.ProxyRepository) *provider.AntigravityAuthorization {
	options := provideradapter.AntigravityAuthorizationOptions(func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxies.GetByID(ctx, id)
		// Authorization treats an unreadable or absent selected proxy as unavailable.
		// Return the callback's availability flag with an empty URL for the platform flow to handle.
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return provider.NewAntigravityAuthorization(options)
}

// provideAntigravityRetry constructs shared platform retry with provider health
// and transport callbacks. runtime and cfg are required; snapshot publication
// is added when supplied, and response inspection keeps at least 512 KiB or the
// larger enabled diagnostic limit.
func provideAntigravityRetry(store *providerpostgres.ProviderStore, counter provider.Internal500CounterCache, runtime *providerHealthRuntime, snapshots *scheduler.SnapshotService, transport httpclient.UpstreamTransport, cfg *config.Config) *provideradapter.AntigravityRetry {
	health := &provider.AntigravityHealth{
		Store:     store,
		Counter:   counter,
		ModelKeys: provideradapter.AntigravityModelLimitKeys,
		Error:     slog.Error,
		Warn:      slog.Warn,
		Info:      slog.Info,

		Logf: func(format string, values ...any) {
			logging.LegacyPrintf("service.antigravity_gateway", format, values...)
		},
	}
	if snapshots != nil {
		health.Publish = func(ctx context.Context, value *provider.Record) error {
			return snapshots.UpdateProviderInCache(ctx, codec.WrapRecord(value))
		}
	}
	core := &provideradapter.AntigravityRetry{
		Health: health,
		Policy: runtime.Health,
		Do:     transport.Do,

		BaseURL: func(value *provider.Record) string {
			return antigravity.ResolveAntigravityForwardBaseURL(cfg.Provider.Antigravity.ForwardEndpoint, provideradapter.AntigravityPaidTier(value))
		},

		BodyLimit: func() int64 {
			// Retry classification reads response bodies independently of whether detailed logging is enabled.
			limit := int64(512 << 10)
			if cfg.Gateway.UpstreamErrorBodyLog.Enabled && cfg.Gateway.UpstreamErrorBodyLog.MaxBytes > int(limit) {
				return int64(cfg.Gateway.UpstreamErrorBodyLog.MaxBytes)
			}
			return limit
		},

		LogConfig: func() (bool, int) {
			return cfg.Gateway.UpstreamErrorBodyLog.Enabled, cfg.Gateway.UpstreamErrorBodyLog.MaxBytes
		},

		TruncateString: logredact.TruncateUTF8,
		SafeURL:        logredact.SafeUpstreamURL,
	}
	return core
}

// provideAntigravityProbe shares tokens and retry with the gateway request
// activity barrier.
func provideAntigravityProbe(tokens *provider.AntigravityTokenSource, retry *provideradapter.AntigravityRetry, activity *gatewayRequestActivity) *provideradapter.AntigravityProbe {
	return &provideradapter.AntigravityProbe{Tokens: tokens, Retry: retry, Enter: activity.Enter}
}

// provideAntigravityErrorObserver binds retry health and provider updates with
// request-time diagnostics. cfg supplies a positive error-body limit or the
// 2048-byte default, while Gemini parsing and Antigravity cooldown resolution
// supply platform-specific fallback rules.
func provideAntigravityErrorObserver(core *provideradapter.AntigravityRetry, store *providerpostgres.ProviderStore, runtime *providerHealthRuntime, cfg *config.Config) *provideradapter.AntigravityErrorObserver {
	health := core.Health

	return &provideradapter.AntigravityErrorObserver{
		Health: health,

		LogConfig: func() (bool, int) {
			limit := 2048
			if cfg.Gateway.UpstreamErrorBodyLog.MaxBytes > 0 {
				limit = cfg.Gateway.UpstreamErrorBodyLog.MaxBytes
			}
			return cfg.Gateway.UpstreamErrorBodyLog.Enabled, limit
		},

		TruncateString: logredact.TruncateUTF8,

		ResetTime: func(body []byte) *int64 {
			// Missing reset details use Gemini's daily deadline as a Unix-second value.
			// This keeps the observer's reset callback in the timestamp representation expected by its consumer.
			return gemini.ParseGeminiRateLimitResetTime(body, func() *int64 { v := provider.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix(); return &v })
		},

		DefaultDuration: func() time.Duration {
			return provideradapter.AntigravityFallbackDuration(cfg.Provider.Antigravity.FallbackCooldownSeconds)
		},

		SetRateLimited: store.SetRateLimited,
		Other:          runtime.Observer,
	}
}

// provideAntigravityQuota binds bounded model reads and optional proxy lookup
// to quota queries. Missing repository, failed lookup, and absent proxy return
// an empty URL with false, leaving the authorization adapter to interpret proxy
// availability.
func provideAntigravityQuota(cfg *config.Config, proxies egress.ProxyRepository) *provider.AntigravityQuota {
	limit := resolveModelsListReadLimit(cfg)
	options := provideradapter.AntigravityQuotaOptions(limit, func(ctx context.Context, id int64) (string, bool) {
		if proxies == nil {
			return "", false
		}
		proxy, err := proxies.GetByID(ctx, id)
		// Authorization treats an unreadable or absent selected proxy as unavailable.
		// Return the callback's availability flag with an empty URL for the platform flow to handle.
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return &provider.AntigravityQuota{Options: options}
}
