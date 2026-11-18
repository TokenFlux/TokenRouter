package completion

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// record_helpers.go resolves completion model metadata, funding rates, and
// billing settings. A completion can record the requested model, outbound
// model, raw response model, and requested or executed effort at the same time.
// Display normalization keeps absent observations nullable, and mismatch
// checking compares the actual outbound identity with upstream evidence so a
// client alias does not establish a false model discrepancy. ClientModel
// updates only the requested display name.
//
// Funding selection separates subscription-plan rates from balance user overrides.
// Peak factors apply according to the final price mode, while the preliminary
// subscription lookup respects the key's captured funding policy. Current shared
// settings are loaded into a key copy before pricing, so captured request ownership
// stays fixed while settlement can use the configured charge policy.

// OptionalTrimmedStringPtr returns a trimmed string pointer for nullable usage
// metadata. Blank input returns nil, and nonblank input receives separate scalar
// storage so absence remains distinct from a present value.
func OptionalTrimmedStringPtr(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// CoalesceRequestedReasoningEffort returns the first nonblank trimmed effort from
// the two captured observations. Result.RequestedReasoningEffort is supplied first
// by recording callers, followed by Input.RequestedReasoningEffort; absent evidence
// returns nil and a selected value receives separate scalar storage.
func CoalesceRequestedReasoningEffort(requested, forwarded *string) *string {
	if requested != nil {
		if value := strings.TrimSpace(*requested); value != "" {
			return &value
		}
	}
	// A blank producer observation permits the canonical request-body effort to
	// fill requested-effort metadata; final executed effort remains a separate field.
	if forwarded != nil {
		if value := strings.TrimSpace(*forwarded); value != "" {
			return &value
		}
	}
	return nil
}

// ForwardResultBillingModel chooses a trimmed logical request model before the
// outbound model. An absent logical model uses trimmed upstream evidence, and both
// blank inputs yield empty for later missing-price handling.
func ForwardResultBillingModel(requestedModel, upstreamModel string) string {
	if trimmed := strings.TrimSpace(requestedModel); trimmed != "" {
		return trimmed
	}
	// An absent logical name leaves outbound evidence as the available candidate;
	// later price-source policy can still replace it using the captured routing fields.
	return strings.TrimSpace(upstreamModel)
}

// OptionalInt64Ptr creates nullable identifier metadata from a captured number.
// Exactly zero returns nil, while other values, including negative values, receive
// separate pointer storage; callers validate identifier suitability before persistence.
func OptionalInt64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// ClaudeServiceTier maps a trimmed, case-insensitive Anthropic fast speed to
// priority billing. Every other speed returns empty, allowing callers to retain
// an unspecified tier or choose their own recorded evidence.
func ClaudeServiceTier(speed string) string {
	if strings.EqualFold(strings.TrimSpace(speed), "fast") {
		return "priority"
	}
	return ""
}

// ForwardServiceTier chooses a nonblank trimmed captured tier before translating
// Anthropic Speed. Nil result or absent/non-fast evidence returns empty; the result's
// stored pointer and raw speed are left unchanged by this read.
func ForwardServiceTier(result *Result) string {
	if result == nil {
		return ""
	}
	if tier := strings.TrimSpace(stringValueOrEmpty(result.ServiceTier)); tier != "" {
		return tier
	}
	return ClaudeServiceTier(result.Usage.Speed)
}

// ResolveBillingMode returns the calculator's declared mode before inferring image
// or token mode from a nonnil result. A missing or blank cost mode uses ImageCount
// alone for that inference; native callers do not use it to classify videos.
func ResolveBillingMode(result *Result, cost *CostBreakdown) *string {
	var mode string
	switch {
	case cost != nil && cost.BillingMode != "":
		mode = cost.BillingMode
	// A calculator mode can classify token-priced images; infer from count only
	// when that mode is absent so the row can represent unpriced media work.
	case result.ImageCount > 0:
		mode = string(BillingModeImage)
	default:
		mode = string(BillingModeToken)
	}
	return &mode
}

// optionalSubscriptionID returns a pointer to the supplied subscription's ID.
// Nil subscriptions produce nil. The returned pointer refers to that subscription's
// field, so callers retain or snapshot the object while using the metadata.
func optionalSubscriptionID(subscription *billing.UserSubscription) *int64 {
	if subscription != nil {
		return &subscription.ID
	}
	return nil
}

// RatesForMode returns subscription and balance factors plus the plan-rate scale
// for the final cost mode. Nil or unspecified costs follow token handling, and
// missing key/group data leaves the scale at one.
//
// Only token mode consults the group's peak window at pricingAt. Image, video,
// and per-request modes retain their supplied base factors, so funds allocation
// can use the final calculator mode even when the result contains image or video
// counts. Both factors carry the same peak scale, which billing can also apply
// to a plan-specific rate reloaded inside the transaction.
func RatesForMode(apiKey *KeySnapshot, cost *CostBreakdown, subscriptionBase, balanceBase float64, pricingAt time.Time) (subscriptionRate, balanceRate, scale float64) {
	scale = 1
	if cost == nil || cost.BillingMode == "" || cost.BillingMode == string(BillingModeToken) {
		if apiKey != nil && apiKey.Group != nil {
			scale = apiKey.Group.PeakMultiplierAt(pricingAt)
		}
	}
	return subscriptionBase * scale, balanceBase * scale, scale
}

// RateOrFallback selects a supplied rate when positive or when the inferred
// fallback is exactly zero. Otherwise it returns the fallback, allowing zero-cost
// allocation to retain supplied zero instead of inventing a positive factor.
func RateOrFallback(value, fallback float64) float64 {
	if value > 0 || fallback == 0 {
		return value
	}
	return fallback
}

// SubscriptionPlanIncludesGroup reports whether a positive group is covered by
// a plan. Nil plans or nonpositive groups are rejected; an empty GroupIDs list
// means global coverage for completion's preliminary multiplier selection.
func SubscriptionPlanIncludesGroup(plan *billing.SubscriptionPlan, groupID int64) bool {
	if plan == nil || groupID <= 0 {
		return false
	}
	if len(plan.GroupIDs) == 0 {
		return true
	}
	return slices.Contains(plan.GroupIDs, groupID)
}

// SubscriptionPlanGroupRateMultiplier reads a positive plan-specific rate for a
// covered positive group. Missing plans, excluded groups, absent entries, or
// nonpositive rates return false so callers can select their group/default factor.
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

// ResolveUsageRateMultiplier chooses the preliminary user factor for one funding
// source. Missing group ID or snapshot keeps the supplied system default, and an
// included subscription group prefers a positive plan-specific rate.
//
// A covered plan without an override uses the group rate; an excluded or missing
// plan keeps the system default. Subscription selection does not call the balance
// user override. With no subscription, an available resolveUserGroupRate callback
// receives the group rate as its default, while a missing callback directly uses
// that group rate. Callers resolve funding mode and eligibility before this choice.
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
	// Subscription plans select their own factor before balance user overrides;
	// an out-of-scope plan cannot lend its group pricing to this request.
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
	// Balance-funded work can use a user override when that reader is available;
	// absent resolution keeps the captured group base available for the same usage.
	if resolveUserGroupRate == nil {
		return groupDefault
	}
	return resolveUserGroupRate(ctx, userID, *groupID, groupDefault)
}

// ComputePeakAwareMultipliers separates a peak-scaled factor from the media base
// at the supplied instant. Missing key/group data leaves the peak factor at one,
// and image receives the supplied base unchanged.
//
// The text candidate multiplies that base by the group's evaluated window factor.
// Recording uses these separate candidates while the calculator determines the
// final mode, then RatesForMode computes the factors supplied for funds allocation.
// Keeping the time-dependent factor out of the stored user/group base lets later
// requests evaluate their own recording or WebSocket turn instant.
func ComputePeakAwareMultipliers(apiKey *KeySnapshot, base float64, now time.Time) (text, image float64) {
	image = base
	peak := 1.0
	if apiKey != nil && apiKey.Group != nil {
		peak = apiKey.Group.PeakMultiplierAt(now)
	}
	text = base * peak
	return
}

// firstUsageBillingModel returns the first nonblank trimmed pricing candidate.
// An empty list or entirely blank candidates returns empty, allowing the caller
// to distinguish missing model evidence from an unavailable selected price.
func firstUsageBillingModel(candidates []string) string {
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// webSearchPricePerCallFromAPIKey reads a captured group's optional Alpha Search
// price. Missing key or group returns nil; a supplied pointer is shared here,
// with nil selecting calculator defaults and present zero representing free calls.
func webSearchPricePerCallFromAPIKey(apiKey *KeySnapshot) *float64 {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return apiKey.Group.WebSearchPricePerCall
}

// groupSearchPricePer1kFromAPIKey reads a group's optional search price per
// thousand operations. Missing key/group returns nil; the supplied pointer stays
// shared, and explicit zero is distinguishable from inherited calculator defaults.
func groupSearchPricePer1kFromAPIKey(apiKey *KeySnapshot) *float64 {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return apiKey.Group.SearchPricePer1k
}

// groupAudioPriceConfigFromAPIKey returns the captured group's audio price config.
// Missing key or group returns nil; callers treat the returned config as shared
// with that captured key and let the calculator resolve absent prices.
func groupAudioPriceConfigFromAPIKey(key *KeySnapshot) *pricing.AudioPriceConfig {
	if key == nil || key.Group == nil {
		return nil
	}
	return key.Group.AudioPrice
}

// resolveSubscription chooses preliminary subscription evidence under the key's
// captured funding mode. Balance mode returns nil, locked subscription mode retains
// current, and automatic mode delegates to ResolveSubscription for a possible lookup.
func (s *Recorder) resolveSubscription(ctx context.Context, key *KeySnapshot, current *billing.UserSubscription, userID int64, groupID *int64) *billing.UserSubscription {
	switch key.BillingMode {
	case billing.APIKeyBillingModeBalance:
		return nil
	case billing.APIKeyBillingModeSubscription:
		return current
	}
	return ResolveSubscription(ctx, current, s.subscriptions, userID, groupID)
}

// resolveUserGroupRateMultiplier delegates balance pricing to the optional rate
// reader. An absent reader returns the supplied group default; configured reader
// behavior determines how unavailable user overrides resolve to that fallback.
func (s *Recorder) resolveUserGroupRateMultiplier(ctx context.Context, userID, groupID int64, fallback float64) float64 {
	// Optional balance-rate resolution leaves the group price intact when no
	// reader was bound; subscription selection is performed independently by callers.
	if s.rates == nil {
		return fallback
	}
	return s.rates.Resolve(ctx, userID, groupID, fallback)
}

// resolveConfigPricingForUsage returns a configured card and its catalog identity.
// It retains reference targets even when no card is configured for that target,
// allowing media callers to read the matching catalog prices after this lookup.
func (s *Recorder) resolveConfigPricingForUsage(ctx context.Context, model string, key *KeySnapshot) (*ResolvedPricing, string) {
	if s.resolver == nil || key == nil || key.Group == nil {
		return nil, model
	}
	gid := key.Group.ID
	resolved := s.resolver.Resolve(ctx, PricingInput{Model: model, GroupID: &gid})
	if resolved.LookupModel != nil {
		model = *resolved.LookupModel
	}
	if resolved.HasConfiguredPricing() {
		return resolved, model
	}
	return nil, model
}

// actorID chooses captured actor evidence before the payer's identifier.
// ActorUserPresent permits a zero actor ID, and any nonzero actor ID also wins;
// callers supply nonnil key and payer snapshots for the fallback.
func actorID(key *KeySnapshot, user *PayerSnapshot) int64 {
	if key.ActorUserPresent || key.ActorUserID != 0 {
		return key.ActorUserID
	}
	return user.ID
}

// stringValueOrEmpty dereferences optional string metadata, returning empty for a nil
// pointer so callers can normalize an absent observation. The returned text has
// no presence flag, so callers retain the pointer when absence matters separately.
func stringValueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// firstNonEmpty returns the first exactly nonempty value in supplied order.
// It leaves whitespace intact; callers trim candidates before calling when blank
// text should be treated as missing identity evidence.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// stableVideoRequestID returns a trimmed Grok video operation key when present.
// Blank input returns empty, and already prefixed values stay prefixed once, allowing
// repeated task polling to reuse the same financial seed.
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

// NormalizeImageBillingTierOrDefault delegates image dimensions and tier labels to
// pricing normalization. Missing or unrecognized evidence selects the pricing
// package's 2K default for subsequent image price lookup and usage metadata.
func NormalizeImageBillingTierOrDefault(v string) string {
	return pricing.NormalizeImageBillingTierOrDefault(v)
}

// NormalizeVideoBillingResolutionOrDefault delegates recognized video resolution
// aliases to pricing normalization. Missing values select 480p; custom labels remain,
// so the stored usage resolution and media price lookup use the same category.
func NormalizeVideoBillingResolutionOrDefault(v string) string {
	return pricing.NormalizeVideoBillingResolutionOrDefault(v)
}

// NormalizeVideoBillingDurationSecondsOrDefault delegates video duration selection
// to pricing. Nonpositive seconds select eight seconds; positive input remains
// unchanged for both price units and recorded metadata, including long videos.
func NormalizeVideoBillingDurationSecondsOrDefault(v int) int {
	return pricing.NormalizeVideoBillingDurationSecondsOrDefault(v)
}

// normalizeBillingServiceTier trims and lowercases service-tier evidence through
// pricing before charge selection compares category names.
func normalizeBillingServiceTier(v string) string { return pricing.NormalizeBillingServiceTier(v) }

// cacheOverrideTarget selects the captured cache-write category before provider
// policy and global injection. Callers supply a nonnil Input, and a missing provider
// with no captured override returns empty.
//
// An enabled provider override wins even when its target is blank, preventing
// later global lookup. Otherwise eligible Anthropic OAuth/setup-token providers
// use five-minute billing when the global one-hour injection setting is enabled.
// The captured target and provider-enabled values are used as supplied; protocol
// cache reclassification decides how any nonempty target names a TTL bucket.
func (s *Recorder) cacheOverrideTarget(ctx context.Context, input *Input) string {
	if input.CacheOverrideTarget != "" {
		return input.CacheOverrideTarget
	}
	if input.Provider == nil {
		return ""
	}
	if input.Provider.CacheTTLOverrideEnabled {
		return input.Provider.CacheTTLOverrideTarget
	}
	// This injection setting affects eligible Anthropic credential types after all
	// earlier overrides decline; the selected billing category is then five minutes.
	if input.Provider.AnthropicOAuthOrSetupToken && s.cacheInjection != nil && s.cacheInjection.IsAnthropicCacheTTL1hInjectionEnabled(ctx) {
		return "5m"
	}
	return ""
}

// observeEvent emits a structured billing observation through the optional
// callback. A nil binding skips the diagnostic. Configured callbacks run synchronously
// with the supplied event, so callers keep referenced data stable during emission.
func (s *Recorder) observeEvent(event BillingEvent) {
	if s.emit != nil {
		s.emit(event)
	}
}

// ResolveSubscription retains a supplied subscription or reads preliminary
// automatic eligibility on demand. Missing positive user/group identity or reader
// returns nil; read errors also return nil, leaving final allocation to billing.
// Callers first resolve key funding mode so balance and locked-subscription requests
// use their own selection rules.
func ResolveSubscription(ctx context.Context, current *billing.UserSubscription, reader SubscriptionReader, userID int64, groupID *int64) *billing.UserSubscription {
	if current != nil {
		return current
	}
	if groupID == nil || *groupID <= 0 || userID <= 0 || reader == nil {
		return nil
	}
	sub, err := reader.ResolveUsableSubscriptionForGroup(ctx, userID, *groupID)
	// Preliminary eligibility failure supplies no captured subscription price; the
	// funds transaction still reloads its own eligibility when the command is applied.
	if err != nil {
		return nil
	}
	return sub
}

// keyWithBillingSettings copies the key before loading current shared charge
// settings. Missing key/group returns the copied value, and PriceResolver supplies
// default settings when no effective source is available.
//
// Captured IDs, actor identity, funding mode, and base group rate stay in that
// copy. Peak policy, free-Fast, search, and audio prices are replaced from the
// current settings read for the group's ID; the timezone pointer remains shared
// as immutable data. This lets completion keep request ownership while applying
// settlement-time charge policy without mutating the captured input.
func (s *Recorder) keyWithBillingSettings(ctx context.Context, key *KeySnapshot) *KeySnapshot {
	key = SnapshotKey(key)
	if key == nil || key.Group == nil {
		return key
	}
	g := key.Group
	settings := s.resolver.BillingSettings(ctx, &g.ID)
	g.PeakRateEnabled, g.PeakStart, g.PeakEnd, g.PeakRateMultiplier = settings.PeakRateEnabled, settings.PeakStart, settings.PeakEnd, settings.PeakRateMultiplier
	g.FreeOpenAIFast = settings.FreeOpenAIFast
	g.WebSearchPricePerCall, g.SearchPricePer1k = settings.WebSearchPricePerCall, settings.SearchPricePer1k
	g.AudioPrice = &pricing.AudioPriceConfig{RealtimePerMin: settings.AudioRealtimePricePerMin, TTSPerMChars: settings.AudioTTSPricePerMillionChars, STTPerHour: settings.AudioSTTPricePerHour}
	return key
}

// applyClientModel replaces the usage row's requested display model from a
// nonblank trimmed ClientModel context value. Nil context/row or unusable evidence
// leaves the row unchanged; Model, upstream identity, and pricing inputs remain separate.
func applyClientModel(ctx context.Context, log *usage.UsageLog) {
	if ctx == nil || log == nil {
		return
	}
	if clientModel, ok := ctx.Value(telemetry.ClientModel).(string); ok && strings.TrimSpace(clientModel) != "" {
		log.RequestedModel = strings.TrimSpace(clientModel)
	}
}

// responseModelMismatch compares the raw upstream declaration with the actual
// outbound model. Missing result or blank declaration returns nil, preserving
// unknown diagnostic evidence rather than inferring a response identity.
//
// The outbound model is trimmed, with Result.Model used only when UpstreamModel
// is blank. Comparison ignores case and surrounding whitespace while retaining
// version and vendor-prefix differences. BillingModel is not a comparison input,
// so a separately chosen price identity cannot hide an upstream discrepancy or
// create one from the client's billing alias.
func responseModelMismatch(result *Result) *bool {
	if result == nil || strings.TrimSpace(result.UpstreamResponseModel) == "" {
		return nil
	}
	// Compare the sent identity before any display override, retaining a nullable
	// result when upstream supplied no declaration rather than inventing a match.
	sent := strings.TrimSpace(result.UpstreamModel)
	if sent == "" {
		sent = strings.TrimSpace(result.Model)
	}
	mismatch := !strings.EqualFold(sent, strings.TrimSpace(result.UpstreamResponseModel))
	return &mismatch
}
