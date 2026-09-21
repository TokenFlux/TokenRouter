package app

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	gemini "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// provideAntigravityRetry 在健康配置绑定完成后组合唯一平台适配，尚存网关只持有同一实例。
func provideAntigravityRetry(source *service.AntigravityGatewayService, store *accountpostgres.AccountStore, counter account.Internal500CounterCache, limits *service.RateLimitService, _ *account.RecoveryService, snapshots *service.SchedulerSnapshotService, transport httpclient.UpstreamTransport, cfg *config.Config) *provider.AntigravityRetry {
	health := &account.AntigravityHealth{Store: store, Counter: counter, ModelKeys: provider.AntigravityModelLimitKeys, Error: slog.Error, Warn: slog.Warn, Info: slog.Info,
		Logf: func(format string, values ...any) {
			logging.LegacyPrintf("service.antigravity_gateway", format, values...)
		},
	}
	if snapshots != nil {
		health.Publish = func(ctx context.Context, value *account.Record) error {
			return snapshots.UpdateAccountInCache(ctx, service.AccountFromRecord(value))
		}
	}
	core := &provider.AntigravityRetry{Health: health, Policy: limits.HealthCore(), Do: transport.Do,
		BaseURL: func(value *account.Record) string {
			return antigravity.ResolveAntigravityForwardBaseURL(os.Getenv("GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL"), provider.AntigravityPaidTier(value))
		},
		BodyLimit: func() int64 {
			limit := int64(512 << 10)
			if cfg.Gateway.LogUpstreamErrorBody && cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
				return int64(cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
			}
			return limit
		},
		LogConfig:      func() (bool, int) { return cfg.Gateway.LogUpstreamErrorBody, cfg.Gateway.LogUpstreamErrorBodyMaxBytes },
		TruncateString: logredact.TruncateUTF8, SafeURL: logredact.SafeUpstreamURL,
	}
	source.BindAntigravityErrorObserver(&provider.AntigravityErrorObserver{
		Health: health,
		LogConfig: func() (bool, int) {
			limit := 2048
			if cfg.Gateway.LogUpstreamErrorBodyMaxBytes > 0 {
				limit = cfg.Gateway.LogUpstreamErrorBodyMaxBytes
			}
			return cfg.Gateway.LogUpstreamErrorBody, limit
		},
		TruncateString: logredact.TruncateUTF8,
		ResetTime: func(body []byte) *int64 {
			return gemini.ParseGeminiRateLimitResetTime(body, func() *int64 { v := account.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix(); return &v })
		},
		DefaultDuration: func() time.Duration {
			return provider.AntigravityFallbackDuration(cfg.Gateway.AntigravityFallbackCooldownMinutes, os.Getenv("GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS"))
		},
		SetRateLimited: store.SetRateLimited, Other: limits.UpstreamHealth(),
	})
	source.BindAntigravityHealth(health)
	source.BindAntigravityRetry(core)
	return core
}

// provideAntigravityProbe 与转发共用重试、账号健康和尝试拥有者；不登记第二个后台实例。
func provideAntigravityProbe(tokens *account.AntigravityTokenSource, retry *provider.AntigravityRetry, activity *gatewayRequestActivity) *provider.AntigravityProbe {
	return &provider.AntigravityProbe{Tokens: tokens, Retry: retry, Enter: activity.Enter}
}
