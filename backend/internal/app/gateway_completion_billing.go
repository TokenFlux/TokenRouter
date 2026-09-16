package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// GatewayCompletionRecorders 把已装配的共享价格、资金、日志与副作用端口交给新请求链。
// Recorder 自身不创建第二份缓存或后台任务；完成队列继续由应用独立持有。
type GatewayCompletionRecorders struct {
	Forward *completion.Recorder
	OpenAI  *completion.Recorder
}

// ProvideGatewayCompletionRecorders 供父装配同时替换旧请求完成回调。
// 请求须先生成 completion.Input 快照，再交给既有完成执行器，禁止闭包保留 Gin。
func ProvideGatewayCompletionRecorders(forward *service.GatewayService, openai *service.OpenAIGatewayService, keys *service.APIKeyService) GatewayCompletionRecorders {
	return GatewayCompletionRecorders{Forward: forward.CompletionRecorder(keys), OpenAI: openai.CompletionRecorder(keys)}
}

// ProjectCompletionAccess 从已认证原生快照读取付款与行为主体，不复制访问或资金规则。
// 调用前 KeyView 必须已经反映最终授权分组；本函数不重新选组或把 RoutePlan ID 当作价卡。
func ProjectCompletionAccess(access *apikey.AccessSnapshot, location *time.Location) (*completion.KeySnapshot, *completion.PayerSnapshot) {
	if access == nil || access.KeyView() == nil {
		return nil, nil
	}
	key := access.KeyView()
	projected := &completion.KeySnapshot{
		ID:                      access.KeyID,
		Key:                     key.Key,
		ActorUserID:             access.ActorUserID,
		ActorUserPresent:        true,
		GroupID:                 key.GroupID,
		TeamID:                  access.TeamID,
		PreferredSubscriptionID: key.PreferredSubscriptionID,
		BillingMode:             apikey.APIKeyEffectiveBillingMode(key),
		Quota:                   key.Quota,
		HasRateLimits:           key.HasRateLimits(),
	}
	if g := key.Group; g != nil {
		projected.Group = &completion.GroupSnapshot{
			ID:                    g.ID,
			Platform:              g.Platform,
			Price:                 &billing.PriceGroup{ModelPricing: g.ModelPricing, LongContextPricingEnabled: g.LongContextPricingEnabled},
			RateMultiplier:        g.RateMultiplier,
			PeakRateEnabled:       g.PeakRateEnabled,
			PeakStart:             g.PeakStart,
			PeakEnd:               g.PeakEnd,
			PeakRateMultiplier:    g.PeakRateMultiplier,
			Location:              location,
			FreeOpenAIFast:        g.FreeOpenAIFast,
			SupportsOpenAIFast:    routing.GroupSupportsOpenAIFast(g.Platform),
			WebSearchPricePerCall: g.WebSearchPricePerCall,
			SearchPricePer1k:      g.SearchPricePer1k,
			AudioPrice:            &pricing.AudioPriceConfig{RealtimePerMin: g.AudioRealtimePricePerMin, TTSPerMChars: g.AudioTTSPricePerMillionChars, STTPerHour: g.AudioSTTPricePerHour},
		}
	}
	var payer *completion.PayerSnapshot
	if u := key.User; u != nil {
		payer = &completion.PayerSnapshot{
			ID:      access.PayerUserID,
			Balance: u.Balance,
			Notification: &billing.UserSummary{
				ID:                         u.ID,
				Email:                      u.Email,
				Username:                   u.Username,
				Balance:                    u.Balance,
				BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
				BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
				BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
				BalanceNotifyExtraEmails:   u.BalanceNotifyExtraEmails,
				TotalRecharged:             u.TotalRecharged,
			},
		}
	}
	snapshot := completion.Snapshot(&completion.Input{APIKey: projected, User: payer})
	return snapshot.APIKey, snapshot.User
}

// ProjectCompletionAccount 在受控账号读取边界截取资金字段；凭据与 Extra 不进入结果。
// account.AccountSnapshot 不包含倍率和额度，所以不得仅凭选号投影补造这些值。
func ProjectCompletionAccount(v *account.Record) *completion.AccountSnapshot {
	if v == nil {
		return nil
	}
	notification := &billing.QuotaNotifyAccount{
		ID:       v.ID,
		Name:     v.Name,
		Platform: v.Platform,
		Dimensions: []billing.QuotaNotifyDimension{

			{
				Name:          "daily",
				Enabled:       v.GetQuotaNotifyDailyEnabled(),
				Threshold:     v.GetQuotaNotifyDailyThreshold(),
				ThresholdType: v.GetQuotaNotifyDailyThresholdType(),
				CurrentUsed:   v.GetQuotaDailyUsed(),
				Limit:         v.GetQuotaDailyLimit(),
			},

			{
				Name:          "weekly",
				Enabled:       v.GetQuotaNotifyWeeklyEnabled(),
				Threshold:     v.GetQuotaNotifyWeeklyThreshold(),
				ThresholdType: v.GetQuotaNotifyWeeklyThresholdType(),
				CurrentUsed:   v.GetQuotaWeeklyUsed(),
				Limit:         v.GetQuotaWeeklyLimit(),
			},

			{
				Name:          "total",
				Enabled:       v.GetQuotaNotifyTotalEnabled(),
				Threshold:     v.GetQuotaNotifyTotalThreshold(),
				ThresholdType: v.GetQuotaNotifyTotalThresholdType(),
				CurrentUsed:   v.GetQuotaUsed(),
				Limit:         v.GetQuotaLimit(),
			},
		},
	}
	return completion.SnapshotAccount(&completion.AccountSnapshot{
		ID:                         v.ID,
		Type:                       v.Type,
		Platform:                   v.Platform,
		CredentialAccountID:        v.ParentAccountID,
		RateMultiplier:             v.BillingRateMultiplier(),
		OpenAI:                     v.IsOpenAI(),
		CNProvider:                 v.IsCNProvider(),
		OAuthLike:                  v.IsOpenAIOAuthLike(),
		QuotaEligible:              v.IsAPIKeyOrBedrock(),
		HasQuotaLimit:              v.HasAnyQuotaLimit(),
		CacheTTLOverrideEnabled:    v.IsCacheTTLOverrideEnabled(),
		CacheTTLOverrideTarget:     v.GetCacheTTLOverrideTarget(),
		AnthropicOAuthOrSetupToken: v.IsAnthropicOAuthOrSetupToken(),
		Notification:               notification,
	})
}

// ProjectCompletionAttempt 只截取观测到的用量；是否提交失败结果仍由请求用例决定。
// upstreamID 已按账号指定响应头在 HTTP 边界取出，本函数不持有 Header 或媒体正文。
func ProjectCompletionAttempt(v upstream.AttemptResult, upstreamID *string) *completion.Result {
	result := &completion.Result{
		RequestID:         v.RequestID,
		ResponseID:        v.ResponseID,
		Model:             v.Model,
		UpstreamModel:     v.UpstreamModel,
		UpstreamRequestID: upstreamID,
		Stream:            v.Stream,
		Duration:          v.Duration,
		FirstTokenMs:      v.FirstTokenMs,
		ImageCount:        v.ObservedImages,
		ImageOutputSizes:  v.ImageOutputSizes,
		SearchCount:       v.SearchCount,

		Usage: completion.TokenUsage{
			InputTokens:              v.Usage.InputTokens,
			OutputTokens:             v.Usage.OutputTokens,
			CacheCreationInputTokens: v.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     v.Usage.CacheReadInputTokens,
			CacheCreation5mTokens:    v.Usage.CacheCreation5mTokens,
			CacheCreation1hTokens:    v.Usage.CacheCreation1hTokens,
			ImageInputTokens:         v.ImageInputTokens,
			ImageOutputTokens:        v.Usage.ImageOutputTokens,
			Speed:                    v.Usage.Speed,
		},
	}
	if v.ServiceTier != "" {
		result.ServiceTier = &v.ServiceTier
	}
	if v.ReasoningEffort != "" {
		result.ReasoningEffort = &v.ReasoningEffort
	}
	if v.AudioUsage != nil {
		result.AudioUsage = &completion.AudioUsage{Mode: v.AudioUsage.Mode, DurationOrUnits: v.AudioUsage.DurationOrUnits}
	}
	return completion.SnapshotResult(result)
}
