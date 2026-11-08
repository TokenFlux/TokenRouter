package provider

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// completion_snapshot.go converts forwarding results and account data into
// completion snapshots.
// Completion records need model, usage, identity, and pricing observations
// after the request's mutable inputs can change. These adapters extract the
// fields used by completion and then delegate mutable result, Key, and
// provider copying to completion's snapshot helpers. Native Messages usage
// carried by an OpenAI result replaces its compatibility usage as a complete
// set, keeping cache-duration and Speed observations together.
//
// Full user summaries and lightweight forwarding results have different
// copying rules: some nested values remain borrowed until CaptureMessages or
// CaptureOpenAI calls completion.Snapshot. Callers serialize capture with
// source mutations and submit the returned worker snapshot only after capture
// is complete. Model candidate lookup uses the same provider-specific identity
// rules as usage pricing.

// CompletionModels supplies usage-pricing candidate identities to the
// completion recorder.
type CompletionModels struct{}

// ChatForwardResult transfers compatibility response observations to an OpenAI
// forwarding result with the supplied billing model. Nil input returns nil;
// nested headers and observation pointers are borrowed until later completion
// capture copies required values.
func ChatForwardResult(result *openai.CompatResponseResult, billingModel string) *forwardcore.OpenAIResult {
	if result == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{RequestID: result.RequestID, ReasoningEffort: result.ReasoningEffort, ServiceTier: result.ResolvedTier, ResponseID: result.ResponseID, ClientDisconnect: result.ClientDisconnect, UpstreamHeaders: result.UpstreamHeaders, Usage: result.Usage, Model: result.Model, BillingModel: billingModel, UpstreamModel: result.UpstreamModel, UpstreamResponseServiceTier: result.ServiceTier, Stream: result.Stream, Duration: result.Duration, SearchCount: result.SearchCount}
}

// Candidates delegates model and alternate identities to the shared
// usage-pricing candidate resolver.
func (CompletionModels) Candidates(model string, alternates ...string) []string {
	return modelidentity.UsageCandidates(model, alternates...)
}

// ProjectCompletionProvider extracts provider classification, multiplier,
// credential-owner identity, and quota-notification data for completion. Nil
// input returns nil, and SnapshotProvider separates the optional parent ID and
// notification collections before returning.
func ProjectCompletionProvider(v *provider.Record) *completion.ProviderSnapshot {
	if v == nil {
		return nil
	}
	out := &completion.ProviderSnapshot{
		ID:                             v.ID,
		CacheTTLOverrideEnabled:        v.IsCacheTTLOverrideEnabled(),
		CacheTTLOverrideTarget:         v.GetCacheTTLOverrideTarget(),
		AnthropicOAuthOrSetupToken:     v.IsAnthropicOAuthOrSetupToken(),
		Type:                           v.Type,
		Platform:                       v.Platform,
		RateMultiplier:                 v.BillingRateMultiplier(),
		OpenAI:                         v.IsOpenAI(),
		ResetForbiddenCounterOnSuccess: v.UsesCountedForbiddenPolicy(),
		RequiresExplicitClaudePricing:  v.RequiresExplicitClaudePricing(),
		OAuthLike:                      v.IsOpenAIOAuthLike(),
		QuotaEligible:                  v.IsAPIKeyOrBedrock(),
		HasQuotaLimit:                  v.HasAnyQuotaLimit(),
		CredentialProviderID:           v.ParentProviderID,
		Notification:                   provideradapter.QuotaNotification(&provider.Record{ID: v.ID, Name: v.Name, Platform: v.Platform, Type: v.Type, Extra: v.Extra}),
	}
	return completion.SnapshotProvider(out)
}

// ProjectCompletionKey captures Key identity, effective billing mode,
// rate-limit presence, and the group multiplier. Nil input returns nil;
// SnapshotKey copies optional IDs and group values, while the group uses
// time.Local and permits Fast pricing.
func ProjectCompletionKey(v *apikey.APIKey) *completion.KeySnapshot {
	if v == nil {
		return nil
	}

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
			ID: g.ID,

			RateMultiplier:     g.RateMultiplier,
			Location:           time.Local,
			SupportsOpenAIFast: true,
		}
	}
	return completion.SnapshotKey(out)
}

// ProjectCompletionPayer captures payer ID and balance with a user
// notification summary. Nil input returns nil; the summary retains some user
// references until completion.Snapshot narrows and copies the notification
// inputs.
func ProjectCompletionPayer(v *identity.User) *completion.PayerSnapshot {
	if v == nil {
		return nil
	}
	return &completion.PayerSnapshot{ID: v.ID, Balance: v.Balance, Notification: completionUserSummary(v)}
}

// ProjectMessagesCompletionResult converts Messages usage, model observations,
// image metadata, and optional audio units for completion. Nil input returns
// nil; header-derived upstream IDs follow provider configuration, and
// SnapshotResult copies its supported mutable metadata before return.
func ProjectMessagesCompletionResult(v *forwardcore.MessagesResult, a *provider.Record) *completion.Result {
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
		UpstreamResponseModel:       v.UpstreamResponseModel,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		Stream:                      v.Stream,
		Duration:                    v.Duration,
		Timings:                     v.Timings,
		ImageCount:                  v.ImageCount,
		ImageFrameCount:             v.ImageFrameCount,
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

// ProjectOpenAICompletionResult converts OpenAI forwarding observations into
// completion usage and metadata. Nil input returns nil, and header-derived
// upstream IDs are omitted for WS turns.
//
// When NativeUsage is present, it replaces the compatibility token buckets
// together and marks the result as native. This retains cache-duration buckets
// and Speed from the original Messages usage so billing does not mix
// incompatible token representations. SnapshotResult then separates the
// mutable pointers and collections used by worker normalization.
func ProjectOpenAICompletionResult(v *forwardcore.OpenAIResult, a *provider.Record) *completion.Result {
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
		UpstreamResponseModel:       v.UpstreamResponseModel,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		Stream:                      v.Stream,
		OpenAIWSMode:                v.OpenAIWSMode,
		Duration:                    v.Duration,
		Timings:                     v.Timings,
		ImageCount:                  v.ImageCount,
		ImageFrameCount:             v.ImageFrameCount,
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
			ImageCacheReadTokens:     v.Usage.ImageCacheReadTokens,
			ImageOutputTokens:        v.Usage.ImageOutputTokens,
		},
	}
	if v.AudioUsage != nil {
		out.AudioUsage = &completion.AudioUsage{Mode: v.AudioUsage.Mode, DurationOrUnits: v.AudioUsage.DurationOrUnits}
	}
	// Replace the token set together when the source was native Messages usage;
	// its cache-duration and Speed fields must accompany that representation into billing.
	if v.NativeUsage != nil {
		out.NativeUsage = true
		out.Usage = completion.TokenUsage{InputTokens: v.NativeUsage.InputTokens, OutputTokens: v.NativeUsage.OutputTokens, CacheCreationInputTokens: v.NativeUsage.CacheCreationInputTokens, CacheReadInputTokens: v.NativeUsage.CacheReadInputTokens, CacheCreation5mTokens: v.NativeUsage.CacheCreation5mTokens, CacheCreation1hTokens: v.NativeUsage.CacheCreation1hTokens, ImageOutputTokens: v.NativeUsage.ImageOutputTokens, Speed: v.NativeUsage.Speed}
	}
	return completion.SnapshotResult(out)
}

// completionUserSummary extracts user fields used when constructing payer
// notification data. Nil input returns nil; extra-email storage is copied,
// while other slices and optional scalar pointers remain borrowed until
// completion.Snapshot selects its notification fields.
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

// ImagesForwardResult converts a media attempt for delivery and billing using
// the supplied image count and parsed request sizes. parsed must be nonnil;
// response headers are cloned, while upstream headers and image-size
// collections remain borrowed.
func ImagesForwardResult(result upstream.AttemptResult, parsed *media.ImageRequest, imageCount int) *forwardcore.OpenAIResult {
	// A requested animation requires validated returned frames before image units
	// are billable. Token evidence remains available after a malformed GIF result.
	if parsed.OutputFormat == "gif" && result.ImageFrameCount == 0 {
		imageCount = 0
	}
	return &forwardcore.OpenAIResult{
		RequestAttempt:  result.RequestAttempt,
		RequestID:       result.RequestID,
		UpstreamHeaders: result.UpstreamHeaders,

		Usage: protocolopenai.ForwardUsage{
			InputTokens:              result.Usage.InputTokens,
			OutputTokens:             result.Usage.OutputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
			ImageInputTokens:         result.ImageInputTokens,
			ImageCacheReadTokens:     result.ImageCacheReadTokens,
			ImageOutputTokens:        result.Usage.ImageOutputTokens,
		},

		Model:            result.Model,
		UpstreamModel:    result.UpstreamModel,
		Stream:           result.Stream,
		ResponseHeaders:  result.UpstreamHeaders.Clone(),
		Duration:         result.Duration,
		Timings:          result.Timings,
		ImageCount:       imageCount,
		ImageFrameCount:  result.ImageFrameCount,
		ImageSize:        parsed.SizeTier,
		ImageInputSize:   parsed.Size,
		ImageOutputSizes: result.ImageOutputSizes,
	}
}
