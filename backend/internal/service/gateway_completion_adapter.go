// 完成边界只负责旧形状的字段投影和端口转接；计费顺序由 completion 唯一拥有。
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"go.uber.org/zap"

	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

func completionKey(v *apikey.APIKey) *completion.KeySnapshot {
	if v == nil {
		return nil
	}
	// 只读判断直接使用必要字段，避免旧兼容方法回写并替换请求的分组引用。
	policy := &apikey.APIKey{
		BillingMode: v.BillingMode,
		RateLimit5h: v.RateLimit5h,
		RateLimit1d: v.RateLimit1d,
		RateLimit7d: v.RateLimit7d,
	}
	out := &completion.KeySnapshot{
		ID:                      v.ID,
		Key:                     v.Key,
		GroupID:                 v.GroupID,
		TeamID:                  v.TeamID,
		PreferredSubscriptionID: v.PreferredSubscriptionID,
		BillingMode:             apikey.APIKeyEffectiveBillingMode(policy),
		Quota:                   v.Quota,
		HasRateLimits:           policy.HasRateLimits(),
	}
	if v.ActorUser != nil {
		out.ActorUserID = v.ActorUser.ID
		out.ActorUserPresent = true
	}
	if g := v.Group; g != nil {
		out.Group = &completion.GroupSnapshot{
			ID:                 g.ID,
			Platform:           g.Platform,
			Price:              projectPriceGroup(g),
			RateMultiplier:     g.RateMultiplier,
			PeakRateEnabled:    g.PeakRateEnabled,
			PeakStart:          g.PeakStart,
			PeakEnd:            g.PeakEnd,
			PeakRateMultiplier: g.PeakRateMultiplier,
			Location:           time.Local,

			FreeOpenAIFast:        g.FreeOpenAIFast,
			SupportsOpenAIFast:    routing.GroupSupportsOpenAIFast(g.Platform),
			WebSearchPricePerCall: g.WebSearchPricePerCall,
			SearchPricePer1k:      g.GetSearchPricePer1k(),
			AudioPrice:            groupAudioPriceConfigFromAPIKey(v),
		}
	}
	return completion.SnapshotKey(out)
}
func completionPayer(v *identity.User) *completion.PayerSnapshot {
	if v == nil {
		return nil
	}
	return &completion.PayerSnapshot{ID: v.ID, Balance: v.Balance, Notification: BillingUserSummary(v)}
}
func completionAccount(v *Account) *completion.AccountSnapshot {
	if v == nil {
		return nil
	}
	out := &completion.AccountSnapshot{
		ID:                         v.ID,
		CacheTTLOverrideEnabled:    v.IsCacheTTLOverrideEnabled(),
		CacheTTLOverrideTarget:     v.GetCacheTTLOverrideTarget(),
		AnthropicOAuthOrSetupToken: v.IsAnthropicOAuthOrSetupToken(),
		Type:                       v.Type,
		Platform:                   v.Platform,
		RateMultiplier:             v.BillingRateMultiplier(),
		OpenAI:                     v.IsOpenAI(),
		CNProvider:                 v.IsCNProvider(),
		OAuthLike:                  v.IsOpenAIOAuthLike(),
		QuotaEligible:              v.IsAPIKeyOrBedrock(),
		HasQuotaLimit:              v.HasAnyQuotaLimit(),
		CredentialAccountID:        v.ParentAccountID,
		Notification:               accountprovider.QuotaNotification(&account.Record{ID: v.ID, Name: v.Name, Platform: v.Platform, Type: v.Type, Extra: v.Extra}),
	}
	return completion.SnapshotAccount(out)
}
func completionForwardResult(v *forwardcore.MessagesResult, a *Account) *completion.Result {
	if v == nil {
		return nil
	}
	out := &completion.Result{
		RequestID:                   v.RequestID,
		Model:                       v.Model,
		UpstreamModel:               v.UpstreamModel,
		UpstreamRequestID:           usageUpstreamRequestIDPtr(a, v.UpstreamHeaders, false),
		ServiceTier:                 v.ServiceTier,
		UpstreamResponseServiceTier: v.UpstreamResponseServiceTier,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		Stream:                      v.Stream,
		Duration:                    v.Duration,
		FirstTokenMs:                v.FirstTokenMs,
		ImageCount:                  v.ImageCount,
		ImageSize:                   v.ImageSize,
		ImageInputSize:              v.ImageInputSize,
		ImageOutputSize:             v.ImageOutputSize,
		ImageOutputSizes:            v.ImageOutputSizes,
		ImageSizeSource:             v.ImageSizeSource,
		ImageSizeBreakdown:          v.ImageSizeBreakdown,
		SearchCount:                 v.SearchCount,

		Usage: completion.TokenUsage{
			InputTokens:              v.Usage.InputTokens,
			OutputTokens:             v.Usage.OutputTokens,
			CacheCreationInputTokens: v.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     v.Usage.CacheReadInputTokens,
			CacheCreation5mTokens:    v.Usage.CacheCreation5mTokens,
			CacheCreation1hTokens:    v.Usage.CacheCreation1hTokens,
			ImageOutputTokens:        v.Usage.ImageOutputTokens,
			Speed:                    v.Usage.Speed,
		},
	}
	if v.AudioUsage != nil {
		out.AudioUsage = &completion.AudioUsage{Mode: v.AudioUsage.Mode, DurationOrUnits: v.AudioUsage.DurationOrUnits}
	}
	return completion.SnapshotResult(out)
}
func completionOpenAIResult(v *forwardcore.OpenAIResult, a *Account) *completion.Result {
	if v == nil {
		return nil
	}
	out := &completion.Result{
		RequestID:                   v.RequestID,
		ResponseID:                  v.ResponseID,
		Model:                       v.Model,
		BillingModel:                v.BillingModel,
		UpstreamModel:               v.UpstreamModel,
		UpstreamRequestID:           usageUpstreamRequestIDPtr(a, v.UpstreamHeaders, v.OpenAIWSMode),
		ServiceTier:                 v.ServiceTier,
		UpstreamResponseServiceTier: v.UpstreamResponseServiceTier,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		Stream:                      v.Stream,
		OpenAIWSMode:                v.OpenAIWSMode,
		Duration:                    v.Duration,
		FirstTokenMs:                v.FirstTokenMs,
		ImageCount:                  v.ImageCount,
		ImageSize:                   v.ImageSize,
		ImageInputSize:              v.ImageInputSize,
		ImageOutputSize:             v.ImageOutputSize,
		ImageOutputSizes:            v.ImageOutputSizes,
		ImageSizeSource:             v.ImageSizeSource,
		ImageSizeBreakdown:          v.ImageSizeBreakdown,
		VideoCount:                  v.VideoCount,
		VideoResolution:             v.VideoResolution,
		VideoDurationSeconds:        v.VideoDurationSeconds,
		SearchCount:                 v.SearchCount,
		WebSearchCalls:              v.WebSearchCalls,

		Usage: completion.TokenUsage{
			InputTokens:              v.Usage.InputTokens,
			OutputTokens:             v.Usage.OutputTokens,
			CacheCreationInputTokens: v.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     v.Usage.CacheReadInputTokens,
			ImageInputTokens:         v.Usage.ImageInputTokens,
			ImageOutputTokens:        v.Usage.ImageOutputTokens,
		},
	}
	if v.AudioUsage != nil {
		out.AudioUsage = &completion.AudioUsage{Mode: v.AudioUsage.Mode, DurationOrUnits: v.AudioUsage.DurationOrUnits}
	}
	return completion.SnapshotResult(out)
}

// CompletionForwardInput 在完成提交边界固化实际用量和主体，不持有原始请求体。
func CompletionForwardInput(ctx context.Context, in *RecordUsageInput) *completion.Input {
	if in == nil {
		return nil
	}
	out := &completion.Input{
		Result:             completionForwardResult(in.Result, in.Account),
		APIKey:             completionKey(in.APIKey),
		User:               completionPayer(in.User),
		Account:            completionAccount(in.Account),
		Subscription:       in.Subscription,
		InboundEndpoint:    in.InboundEndpoint,
		UpstreamEndpoint:   in.UpstreamEndpoint,
		UserAgent:          in.UserAgent,
		IPAddress:          in.IPAddress,
		ClientSessionID:    in.ClientSessionID,
		RequestPayloadHash: resolveUsageBillingPayloadFingerprint(ctx, in.RequestPayloadHash),
		QuotaPlatform:      in.QuotaPlatform,
		ForceCacheBilling:  in.ForceCacheBilling,
		QuotaUpdates:       in.APIKeyService != nil,
		ChannelUsageFields: in.ChannelUsageFields,
	}
	if in.Result != nil {
		out.RequestID = resolveUsageBillingRequestID(ctx, in.Result.RequestID)
		out.RequestedReasoningEffort = CanonicalRequestedReasoningEffort(in.RequestBody, in.Result.Model)
	}
	return completion.Snapshot(out)
}

// CompletionOpenAIInput 保留 WS turn 固定时刻及独立账单模型链。
func CompletionOpenAIInput(ctx context.Context, in *OpenAIRecordUsageInput) *completion.Input {
	if in == nil {
		return nil
	}
	out := &completion.Input{
		Result:                   completionOpenAIResult(in.Result, in.Account),
		APIKey:                   completionKey(in.APIKey),
		User:                     completionPayer(in.User),
		Account:                  completionAccount(in.Account),
		Subscription:             in.Subscription,
		InboundEndpoint:          in.InboundEndpoint,
		UpstreamEndpoint:         in.UpstreamEndpoint,
		UserAgent:                in.UserAgent,
		IPAddress:                in.IPAddress,
		ClientSessionID:          in.ClientSessionID,
		RequestPayloadHash:       resolveUsageBillingPayloadFingerprint(ctx, in.RequestPayloadHash),
		QuotaPlatform:            in.QuotaPlatform,
		QuotaUpdates:             in.APIKeyService != nil,
		CyberBlocked:             in.CyberBlocked,
		NativeCompactionV2:       in.NativeCompactionV2,
		PricingAt:                in.PricingAt,
		ChannelUsageFields:       in.ChannelUsageFields,
		RequestedReasoningEffort: CanonicalRequestedReasoningEffort(in.RequestBody, in.OriginalModel, in.ChannelMappedModel),
	}
	if in.Result != nil {
		out.RequestID = resolveUsageBillingRequestID(ctx, in.Result.RequestID)
	}
	return completion.Snapshot(out)
}

type completionAccounts struct{ repo AccountRepository }

func (a completionAccounts) CredentialAccount(ctx context.Context, in completion.AccountSnapshot) (*completion.AccountSnapshot, error) {
	// 只恢复母账号解析需要的标识；凭据和原始实体不会传回完成核心。
	source := &Account{ID: in.ID, Platform: in.Platform, Type: in.Type, ParentAccountID: in.CredentialAccountID}
	v, err := resolveCredentialAccount(ctx, a.repo, source)
	if err != nil {
		return nil, err
	}
	if v == source {
		return &in, nil
	}
	return completionAccount(v), nil
}

type completionModels struct{}

func (completionModels) Candidates(model string, alternates ...string) []string {
	return modelidentity.UsageCandidates(model, alternates...)
}

func completionObserver(component, message string) {
	logging.LegacyPrintf(component, "%s", message)
}
func completionEffects(d *billingDeps, updater APIKeyQuotaUpdater) *completion.CommitEffects {
	out := &completion.CommitEffects{Funds: settlementEffects(d), Activity: d.deferredService, Observe: completionObserver}
	if auth, ok := updater.(completion.AuthInvalidator); ok {
		out.Auth = auth
	}
	if d.balanceNotifyService != nil {
		out.Notifications = d.balanceNotifyService
	}
	return out
}
func completionOptions(cfg *config.Config, now func() time.Time) completion.RecorderOptions {
	o := completion.RecorderOptions{DefaultMultiplier: 1, Now: timezone.NewCalendar(time.Local).
		Now}
	if cfg != nil {
		o.Simple = cfg.RunMode == config.RunModeSimple
		o.DefaultMultiplier = cfg.Default.RateMultiplier
	}
	if now != nil {
		o.Now = now
	}
	return o
}

// CompletionRecorder 只装配引用现有实例的无状态完成器，供新请求链直接使用。
func (s *GatewayService) CompletionRecorder(updater APIKeyQuotaUpdater) *completion.Recorder {
	var calculator *billing.Calculator
	if s.billingService != nil {
		calculator = s.billingService
	}
	var prices *billing.PriceResolver
	if s.resolver != nil {
		prices = s.resolver
	}
	rates := s.userGroupRateResolver
	if rates == nil {
		rates = billing.NewGroupRateResolver(s.userGroupRateRepo, s.userGroupRateCache, resolveUserGroupRateCacheTTL(s.cfg), &s.userGroupRateSF, "service.gateway", logging.LegacyPrintf)
	}
	var cache completion.CacheInjectionPolicy
	if s.settingService != nil {
		cache = s.settingService.Gateway
	}
	return completion.NewRecorder(completion.Dependencies{
		Emit:           completionBillingEvent,
		CacheInjection: cache,
		Calculator:     calculator,
		Prices:         prices,
		AccountStats:   completionStats(s.channelService, calculator),
		Funds:          s.usageBillingRepo,
		Subscriptions:  usageSubscriptionResolverFrom(s.usageBillingRepo),
		Rates:          rates,
		Models:         completionModels{},
		Logs:           completion.SnapshotLogWriter(s.usageLogRepo),
		Effects:        completionEffects(s.billingDeps(), updater),
		Observe:        completionObserver,
	}, completionOptions(s.cfg, s.usageBillingNow))
}
func (s *OpenAIGatewayService) CompletionRecorder(updater APIKeyQuotaUpdater) *completion.Recorder {
	var calculator *billing.Calculator
	if s.billingService != nil {
		calculator = s.billingService
	}
	var prices *billing.PriceResolver
	if s.resolver != nil {
		prices = s.resolver
	}
	rates := s.userGroupRateResolver
	if rates == nil {
		rates = billing.NewGroupRateResolver(nil, nil, resolveUserGroupRateCacheTTL(s.cfg), nil, "service.openai_gateway", logging.LegacyPrintf)
	}
	var health completion.HealthObserver
	if s.rateLimitService != nil {
		health = s.rateLimitService
	}
	return completion.NewRecorder(completion.Dependencies{
		Emit:          completionBillingEvent,
		Calculator:    calculator,
		Prices:        prices,
		AccountStats:  completionStats(s.channelService, calculator),
		Funds:         s.usageBillingRepo,
		Subscriptions: usageSubscriptionResolverFrom(s.usageBillingRepo),
		Rates:         rates,
		Accounts:      completionAccounts{s.accountRepo},
		Health:        health,
		Models:        completionModels{},
		Logs:          completion.SnapshotLogWriter(s.usageLogRepo),
		Effects:       completionEffects(s.billingDeps(), updater),
		Observe:       completionObserver,
	}, completionOptions(s.cfg, s.usageBillingNow))
}
func completionStats(channels *routing.ChannelService, calculator *billing.Calculator) *billing.PriceResolver {
	var source billing.AccountStatsSource
	if channels != nil {
		source = LegacyAccountStatsSource{Service: channels}
	}
	return billing.NewPriceResolver(nil, calculator, nil, nil, source)
}

func completionPricingOptions(v *recordUsageOpts) *completion.PricingOptions {
	if v == nil {
		return nil
	}
	return &completion.PricingOptions{PricingAt: v.PricingAt}
}

func completionRequestIdentity(ctx context.Context, upstream, payload string) completion.RequestIdentity {
	out := completion.RequestIdentity{Upstream: upstream, PayloadHash: payload}
	if ctx != nil {
		out.Client, _ = ctx.Value(telemetry.ClientRequestID).(string)
		out.Local, _ = ctx.Value(telemetry.RequestID).(string)
	}
	return out
}

// completionBillingEvent 保留缺价、免费搜索与档位降级的级别及结构化字段。
func completionBillingEvent(e completion.BillingEvent) {
	switch e.Kind {
	case "pricing_missing":
		logging.L().With(zap.String("component", e.Component), zap.Strings("billing_models", e.Models), zap.String("requested_model", e.RequestedModel), zap.String("mapped_model", e.MappedModel), zap.String("upstream_model", e.UpstreamModel), zap.Int64("api_key_id", e.KeyID), zap.Int64("account_id", e.AccountID)).Warn("openai_usage.pricing_missing_record_zero_cost", zap.Error(e.Err))
	case "standard_pricing_missing":
		logging.L().With(zap.String("component", e.Component), zap.String("request_id", e.RequestID)).Warn("openai_usage.standard_pricing_missing_free_fast_zero_cost", zap.Error(e.Err))
	case "search_free":
		logging.L().Info("openai_usage.search_price_per_1k_explicit_free", zap.Int("search_count", e.SearchCount), zap.String("model", e.Model), zap.Int64("api_key_id", e.KeyID), zap.Any("group_id", e.GroupID))
	case "tier_downgrade":
		slog.Info("billing.service_tier_downgraded", "component", e.Component, "request_id", e.RequestID, "requested_tier", e.RequestedTier, "response_tier", e.ObservedTier, "billed_tier", e.BilledTier, "platform", e.Platform, "account_id", e.AccountID)
	}
}
