package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// provideAccountRecovery 直接投影配置、原生设置和技术端口；构造期间不回源或启动工作。
func provideAccountRecovery(
	store *accountpostgres.AccountStore,
	cache account.TempUnschedCache,
	source *service.RateLimitService,
	precheck *account.GeminiPrecheck,
	cfg *config.Config,
	settings *account.RuntimeSettings,
	timeouts account.TimeoutCounterCache,
	forbidden account.OpenAI403CounterCache,
	tokens account.TokenCacheInvalidator,
	blocker service.AccountRuntimeBlocker,
) *account.RecoveryService {
	options := account.HealthOptions{
		Now: time.Now, Warn: slog.Warn, Info: slog.Info,
		SessionWindows: store, TimeoutCounter: timeouts, ForbiddenCounter: forbidden,
		UnauthorizedCooldownMinutes: cfg.RateLimit.OAuth401CooldownMinutes,
		CNIntervalMinutes:           cfg.Gateway.CNProviders.IntervalMinutes,
		OverloadMinutes:             cfg.RateLimit.OverloadCooldownMinutes,
		APIKeyHealthWarn:            accountprovider.LogAPIKeyHealthWarning,
	}
	if counter, ok := cache.(account.OpenAIAPIKeyHealthCache); ok {
		options.APIKeyHealthCounter = counter
	}
	if settings != nil {
		options.APIKeyHealthSettings = settings.GetOpenAIAPIKeyHealthBreakerSettings
		options.RateLimit429Settings = settings.GetRateLimit429CooldownSettings
		options.ForbiddenSettings = settings.GetOpenAI403CooldownSettings
		options.OverloadSettings = settings.GetOverloadCooldownSettings
		options.HasThresholdSettings = func() bool { return true }
		options.Thresholds = settings.GetAccountSchedulingThresholds
		options.StreamSettings = func(ctx context.Context) (*account.StreamTimeoutSettings, error, bool) {
			value, err := settings.GetStreamTimeoutSettings(ctx)
			return value, err, true
		}
	}
	if tokens != nil {
		options.InvalidateUnauthorizedToken = tokens.InvalidateToken
	}
	if blocker != nil {
		options.Block = func(value *account.Record, until time.Time, reason string) {
			blocker.BlockAccountScheduling(service.AccountFromRecord(value), until, reason)
		}
	}

	// 恢复与窗口观测互相调用；绑定完成后才对外发布，构造本身不执行这些回调。
	var recovery *account.RecoveryService
	options.ClearWindowRateLimit = func(ctx context.Context, id int64) error {
		return recovery.ClearRateLimit(ctx, id)
	}
	health := account.NewHealthService(store, cache, options)
	recoveryOptions := account.RecoveryOptions{Now: time.Now, Warn: slog.Warn, ResetCounter: health.ResetForbiddenCounter}
	if blocker != nil {
		recoveryOptions.ClearSchedulingBlock = blocker.ClearAccountSchedulingBlock
	}
	if tokens != nil {
		recoveryOptions.InvalidateToken = tokens.InvalidateToken
	}
	recovery = account.NewRecoveryService(store, cache, recoveryOptions)

	limits := &accountprovider.RateLimitObserver{Health: health, Plans: store, NextGeminiDaily: func() *int64 {
		reset := account.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix()
		return &reset
	}}
	if retry, ok := blocker.(interface {
		ShouldRetryOpenAIOAuth429(*service.Account, http.Header, []byte) bool
	}); ok {
		limits.RetryOpenAI = func(value *account.Record, headers http.Header, body []byte) bool {
			return retry.ShouldRetryOpenAIOAuth429(service.AccountFromRecord(value), headers, body)
		}
	}
	team := account.NewTeamLinkedHealth(store, account.TeamLinkedOptions{Now: options.Now, Warn: options.Warn, Block: options.Block})
	models := &accountprovider.ModelHealth{Health: health, CodexRules: openai.CodexModelRules{
		ImageOnly: media.IsImageGenerationModel, LastSegment: capability.LastOpenAIModelSegment,
		CanonicalAlias: capability.CanonicalizeOpenAIModelAliasSpelling, KnownModel: modelidentity.NormalizeOpenAI,
		SupportsEffort: capability.OpenAIModelSupportsReasoningEffort,
	}, IsImageModel: media.IsGPTImageGenerationModel}

	// 尚未清零的调用方只取得这些唯一原生实例，不从旧聚合服务读取配置或持久化端口。
	source.BindGeminiPrecheck(precheck)
	source.BindHealth(health)
	source.BindRecovery(recovery)
	source.BindRateLimitObserver(limits)
	source.BindTeamLinkedHealth(team)
	source.BindUpstreamHealth(&accountprovider.UpstreamHealth{Core: health, Team: team, Limits: limits, Models: models})
	return recovery
}
