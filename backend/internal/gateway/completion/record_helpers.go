package completion

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

func OptionalTrimmedStringPtr(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// CoalesceRequestedReasoningEffort 优先保留处理器或传输层已捕获的客户端档位，
// 再使用尚未改写的请求体推导值；两者都为空时保留 NULL 表示客户端未声明。
func CoalesceRequestedReasoningEffort(requested, forwarded *string) *string {
	if requested != nil {
		if value := strings.TrimSpace(*requested); value != "" {
			return &value
		}
	}
	if forwarded != nil {
		if value := strings.TrimSpace(*forwarded); value != "" {
			return &value
		}
	}
	return nil
}

func ForwardResultBillingModel(requestedModel, upstreamModel string) string {
	if trimmed := strings.TrimSpace(requestedModel); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(upstreamModel)
}

func OptionalInt64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func ClaudeServiceTier(speed string) string {
	if strings.EqualFold(strings.TrimSpace(speed), "fast") {
		return "priority"
	}
	return ""
}

func ForwardServiceTier(result *Result) string {
	if result == nil {
		return ""
	}
	if tier := strings.TrimSpace(stringValueOrEmpty(result.ServiceTier)); tier != "" {
		return tier
	}
	return ClaudeServiceTier(result.Usage.Speed)
}

func ResolveBillingMode(result *Result, cost *CostBreakdown) *string {
	var mode string
	switch {
	case cost != nil && cost.BillingMode != "":
		mode = cost.BillingMode
	case result.ImageCount > 0:
		mode = string(BillingModeImage)
	default:
		mode = string(BillingModeToken)
	}
	return &mode
}

func optionalSubscriptionID(subscription *billing.UserSubscription) *int64 {
	if subscription != nil {
		return &subscription.ID
	}
	return nil
}

func RatesForMode(apiKey *KeySnapshot, cost *CostBreakdown, subscriptionBase, balanceBase float64, pricingAt time.Time) (subscriptionRate, balanceRate, scale float64) {
	scale = 1
	if cost == nil || cost.BillingMode == "" || cost.BillingMode == string(BillingModeToken) {
		if apiKey != nil && apiKey.Group != nil {
			scale = apiKey.Group.PeakMultiplierAt(pricingAt)
		}
	}
	return subscriptionBase * scale, balanceBase * scale, scale
}

func RateOrFallback(value, fallback float64) float64 {
	if value > 0 || fallback == 0 {
		return value
	}
	return fallback
}

func SubscriptionPlanIncludesGroup(plan *billing.SubscriptionPlan, groupID int64) bool {
	if plan == nil || groupID <= 0 {
		return false
	}
	if len(plan.GroupIDs) == 0 {
		return true
	}
	for _, id := range plan.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

func SubscriptionPlanGroupRateMultiplier(plan *billing.SubscriptionPlan, groupID int64) (float64, bool) {
	if plan == nil || groupID <= 0 {
		return 0, false
	}
	if !SubscriptionPlanIncludesGroup(plan, groupID) {
		return 0, false
	}
	if multiplier, ok := plan.GroupRateMultipliers[groupID]; ok && multiplier > 0 {
		return multiplier, true
	}
	return 0, false
}

func ResolveUsageRateMultiplier(
	ctx context.Context,
	userID int64,
	groupID *int64,
	group *GroupSnapshot,
	defaultMultiplier float64,
	subscription *billing.UserSubscription,
	resolveUserGroupRate func(context.Context, int64, int64, float64) float64,
) float64 {
	multiplier := defaultMultiplier
	if groupID == nil || group == nil {
		return multiplier
	}
	if subscription != nil {
		if multiplier, ok := SubscriptionPlanGroupRateMultiplier(subscription.Plan, *groupID); ok {
			return multiplier
		}
		if SubscriptionPlanIncludesGroup(subscription.Plan, *groupID) {
			return group.RateMultiplier
		}
		return multiplier
	}
	groupDefault := group.RateMultiplier
	if resolveUserGroupRate == nil {
		return groupDefault
	}
	return resolveUserGroupRate(ctx, userID, *groupID, groupDefault)
}

func ComputePeakAwareMultipliers(apiKey *KeySnapshot, base float64, now time.Time) (text, image float64) {
	image = base
	peak := 1.0
	if apiKey != nil && apiKey.Group != nil {
		peak = apiKey.Group.PeakMultiplierAt(now)
	}
	text = base * peak
	return
}

func firstUsageBillingModel(candidates []string) string {
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func webSearchPricePerCallFromAPIKey(apiKey *KeySnapshot) *float64 {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return apiKey.Group.WebSearchPricePerCall
}

func groupSearchPricePer1kFromAPIKey(apiKey *KeySnapshot) *float64 {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return apiKey.Group.SearchPricePer1k
}

func groupAudioPriceConfigFromAPIKey(key *KeySnapshot) *pricing.AudioPriceConfig {
	if key == nil || key.Group == nil {
		return nil
	}
	return key.Group.AudioPrice
}

func (s *Recorder) resolveSubscription(ctx context.Context, key *KeySnapshot, current *billing.UserSubscription, userID int64, groupID *int64) *billing.UserSubscription {
	switch key.BillingMode {
	case billing.APIKeyBillingModeBalance:
		return nil
	case billing.APIKeyBillingModeSubscription:
		return current
	}
	return ResolveSubscription(ctx, current, s.subscriptions, userID, groupID)
}

func (s *Recorder) resolveUserGroupRateMultiplier(ctx context.Context, userID, groupID int64, fallback float64) float64 {
	if s.rates == nil {
		return fallback
	}
	return s.rates.Resolve(ctx, userID, groupID, fallback)
}

func (s *Recorder) resolveConfigPricingForUsage(ctx context.Context, model string, key *KeySnapshot) (*ResolvedPricing, string) {
	return s.ResolveConfigPricing(ctx, model, key), model
}

func actorID(key *KeySnapshot, user *PayerSnapshot) int64 {
	if key.ActorUserPresent || key.ActorUserID != 0 {
		return key.ActorUserID
	}
	return user.ID
}

func platformFromKey(key *KeySnapshot) string {
	if key == nil || key.Group == nil {
		return ""
	}
	return key.Group.Platform
}

func stringValueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func stableVideoRequestID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, "grok-video:") {
		return id
	}
	return "grok-video:" + id
}

func NormalizeImageBillingTierOrDefault(v string) string {
	return pricing.NormalizeImageBillingTierOrDefault(v)
}

func NormalizeVideoBillingResolutionOrDefault(v string) string {
	return pricing.NormalizeVideoBillingResolutionOrDefault(v)
}

func NormalizeVideoBillingDurationSecondsOrDefault(v int) int {
	return pricing.NormalizeVideoBillingDurationSecondsOrDefault(v)
}
func normalizeBillingServiceTier(v string) string { return pricing.NormalizeBillingServiceTier(v) }
func applyCostBreakdownMultiplier(v *CostBreakdown, m float64) {
	pricing.ApplyCostBreakdownMultiplier(v, m)
}

func maxReasoningEffortBillingMultiplier(model, effort string, p *pricing.ModelPricing) float64 {
	return pricing.MaxReasoningEffortBillingMultiplier(model, effort, p)
}

// cacheOverrideTarget 保留账号优先及设置查询时机，不提前读取动态设置。
func (s *Recorder) cacheOverrideTarget(ctx context.Context, input *Input) string {
	if input.CacheOverrideTarget != "" {
		return input.CacheOverrideTarget
	}
	if input.Account == nil {
		return ""
	}
	if input.Account.CacheTTLOverrideEnabled {
		return input.Account.CacheTTLOverrideTarget
	}
	if input.Account.AnthropicOAuthOrSetupToken && s.cacheInjection != nil && s.cacheInjection.IsAnthropicCacheTTL1hInjectionEnabled(ctx) {
		return "5m"
	}
	return ""
}

func (s *Recorder) observeEvent(event BillingEvent) {
	if s.emit != nil {
		s.emit(event)
	}
}

// ResolveSubscription 保留当前订阅优先及按需读取，资金模式裁决由调用者在此前完成。
func ResolveSubscription(ctx context.Context, current *billing.UserSubscription, reader SubscriptionReader, userID int64, groupID *int64) *billing.UserSubscription {
	if current != nil {
		return current
	}
	if groupID == nil || *groupID <= 0 || userID <= 0 || reader == nil {
		return nil
	}
	sub, err := reader.ResolveUsableSubscriptionForGroup(ctx, userID, *groupID)
	if err != nil {
		return nil
	}
	return sub
}
