// 完成输入在同步提交边界冻结；仅投影既有主体、计量与请求身份，不执行资金操作。
package provider

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func ProjectCompletionKey(v *apikey.APIKey) *completion.KeySnapshot {
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
			Price:              ProjectCompletionPriceGroup(g),
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

func ProjectCompletionPayer(v *identity.User) *completion.PayerSnapshot {
	if v == nil {
		return nil
	}
	return &completion.PayerSnapshot{ID: v.ID, Balance: v.Balance, Notification: completionUserSummary(v)}
}

func ProjectMessagesCompletionResult(v *forwardcore.MessagesResult, a *account.Record) *completion.Result {
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

func ProjectOpenAICompletionResult(v *forwardcore.OpenAIResult, a *account.Record) *completion.Result {
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

// completionUserSummary 将身份记录投影为权益和通知所需的只读数据。
func completionUserSummary(u *identity.User) *billing.UserSummary {
	if u == nil {
		return nil
	}
	out := &billing.UserSummary{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		AllowedGroups:              u.AllowedGroups,
		DisabledPublicGroups:       u.DisabledPublicGroups,
		LastActiveAt:               u.LastActiveAt,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RPMLimit,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt,
	}
	if u.BalanceNotifyExtraEmails != nil {
		out.BalanceNotifyExtraEmails = make([]billing.NotifyEmailSummary, len(u.BalanceNotifyExtraEmails))
		copy(out.BalanceNotifyExtraEmails, u.BalanceNotifyExtraEmails)
	}
	return out
}

// ProjectCompletionPriceGroup 只向计费传递价卡，不传递路由配置与运行状态。
func ProjectCompletionPriceGroup(group *routing.Group) *billing.PriceGroup {
	if group == nil {
		return nil
	}
	return &billing.PriceGroup{ModelPricing: group.ModelPricing, LongContextPricingEnabled: group.LongContextPricingEnabled}
}

func groupAudioPriceConfigFromAPIKey(apiKey *apikey.APIKey) *pricing.AudioPriceConfig {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	g := apiKey.Group
	return &pricing.AudioPriceConfig{
		RealtimePerMin: g.AudioRealtimePricePerMin,
		TTSPerMChars:   g.AudioTTSPricePerMillionChars,
		STTPerHour:     g.AudioSTTPricePerHour,
	}
}
