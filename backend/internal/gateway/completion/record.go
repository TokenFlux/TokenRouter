package completion

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// record.go calculates charges, applies settlement, and writes analysis records
// for captured completion work. Observed usage is recorded after entrypoints
// decide that an outcome qualifies for completion. These routines accept that
// captured work, including caller-selected partial failures and zero-usage
// records, and interpret either separate native input/cache buckets or
// inclusive OpenAI input. They do not infer network success from the presence
// of usage or repeat a provider attempt after a price failure.
//
// Preliminary funding selects captured or inferred subscription evidence before
// balance overrides; protocol entrypoints then apply their own pricing instant.
// Calculated user charges and provider statistics use distinct model and rate
// choices. Financial Apply runs before WriteUsage: committed allocations replace
// provisional funding metadata, while a settlement error retains measured counts
// and base costs with ActualCost zero for reconciliation. An analysis write failure
// is observed after that outcome and cannot cause a second funds application.
// Direct protocol methods normalize their input result in place; Record provides
// the ordinary snapshotting entrypoint.

// preliminaryFunding holds subscription evidence and rates before time-dependent
// pricing or transactional allocation changes the recorded funding outcome.
type preliminaryFunding struct {
	// subscription retains the selected captured or reader-provided evidence;
	// final eligibility and allocation remain the responsibility of the funds transaction.
	subscription *billing.UserSubscription
	// baseMultiplier prices the provisional charge using the selected plan,
	// balance override, group rate, or recorder default before peak scaling.
	baseMultiplier float64
	// subscriptionMultiplier and balanceMultiplier supply transaction defaults from
	// the group or recorder. A balance override replaces balanceMultiplier only
	// when preliminary selection produces no subscription; plan overrides affect
	// baseMultiplier.
	subscriptionMultiplier float64 // preliminary subscription allocation factor
	balanceMultiplier      float64 // preliminary balance allocation factor
}

// RecordAnthropic records usage whose ordinary input and cache buckets are already
// separate. Callers provide complete nonnil input, result, key, payer, provider,
// and pricing dependencies and own result mutation for normalization.
//
// Nil options are created locally; supplied options have PricingAt replaced by
// the recorder's current clock. Cache switching and TTL overrides precede pricing,
// then captured funding policy selects subscription or balance factors. Configured
// model source chooses one price identity while row metadata retains the request
// and outbound models. Funds are applied before analysis writes; settlement errors
// return after an unsettled zero-ActualCost row is attempted, and log errors are
// observed without replacing the funds result.
func (s *Recorder) RecordAnthropic(ctx context.Context, input *Input, opts *PricingOptions) error {
	if opts == nil {
		opts = &PricingOptions{}
	}
	result := input.Result
	apiKey := s.keyWithBillingSettings(ctx, input.APIKey)
	user := input.User
	provider := input.Provider
	subscription := input.Subscription
	s.normalizeResult(result, provider, false, provider, input.RequestID)

	// Sticky-switch billing reclassifies observed uncached input before pricing; add
	// it to existing cache reads once and clear input so those tokens are not charged twice.
	if input.ForceCacheBilling && result.Usage.InputTokens > 0 {
		s.printf("service.gateway", "force_cache_billing: %d input_tokens → cache_read_input_tokens (provider=%d)",
			result.Usage.InputTokens, provider.ID)
		result.Usage.CacheReadInputTokens += result.Usage.InputTokens
		result.Usage.InputTokens = 0
	}

	// The selected override redistributes cache-write TTL detail before cost lookup;
	// its logged marker reflects positive classified detail rather than the helper's change flag.
	cacheTTLOverridden := false
	if overrideTarget := s.cacheOverrideTarget(ctx, input); overrideTarget != "" {
		applyCacheOverride(&result.Usage, overrideTarget)
		cacheTTLOverridden = (result.Usage.CacheCreation5mTokens + result.Usage.CacheCreation1hTokens) > 0
	}

	funding := s.resolvePreliminaryFunding(ctx, apiKey, subscription, user.ID)
	subscription = funding.subscription
	// Prepare peak and media candidates before the final card mode is known;
	// RatesForMode later supplies the applicable peak scale for transaction allocation.
	// The stored user/group base remains reusable across different recording instants.
	rateNow := s.now()
	opts.PricingAt = rateNow
	multiplier, imageMultiplier := ComputePeakAwareMultipliers(apiKey, funding.baseMultiplier, rateNow)

	billingModel := ForwardResultBillingModel(result.Model, result.UpstreamModel)
	if input.BillingModelSource == BillingModelSourceUpstream && result.UpstreamModel != "" {
		billingModel = result.UpstreamModel
	}
	if input.BillingModelSource == BillingModelSourceGroupMapped && input.GroupMappedModel != "" {
		billingModel = input.GroupMappedModel
	}
	if input.BillingModelSource == BillingModelSourceRequested && input.OriginalModel != "" {
		billingModel = input.OriginalModel
	}

	// Keep the pre-group requested model for analysis independently of the selected
	// pricing source; WriteUsage can additionally apply captured client display evidence.
	requestedModel := result.Model
	if input.OriginalModel != "" {
		requestedModel = input.OriginalModel
	}

	cost := s.CalculateRecordUsageCost(ctx, result, apiKey, provider, billingModel, requestedModel, input.BillingModelSource, input.GroupMappedModel, multiplier, imageMultiplier, opts)

	// This preliminary subscription choice supplies row metadata before Apply; an
	// applied transaction result replaces it with the allocation that actually funded usage.
	isSubscriptionBilling := subscription != nil
	billingType := BillingTypeBalance
	if isSubscriptionBilling {
		billingType = BillingTypeSubscription
	}

	providerRateMultiplier := provider.RateMultiplier
	usageLog := s.BuildRecordUsageLog(ctx, input, result, apiKey, user, provider, subscription,
		requestedModel, multiplier, imageMultiplier, providerRateMultiplier, billingType, cacheTTLOverridden, cost, opts)

	// Provider statistics use their own rule ordering and Qoder requested-alias
	// preference; user pricing already selected its separate configured billing identity.
	if apiKey.GroupID != nil {
		s.applyProviderStatsCost(ctx, usageLog,
			provider.ID, *apiKey.GroupID, result.UpstreamModel, requestedModel, input.GroupMappedModel,
			// Native input excludes both cache buckets, so statistics receive those buckets
			// separately, including cache-write lifetimes for distinct provider statistics
			// rates; OpenAI recording constructs the analogous disjoint ordinary input.
			UsageTokens{
				InputTokens:           result.Usage.InputTokens,
				OutputTokens:          result.Usage.OutputTokens,
				CacheCreationTokens:   result.Usage.CacheCreationInputTokens,
				CacheCreation5mTokens: result.Usage.CacheCreation5mTokens,
				CacheCreation1hTokens: result.Usage.CacheCreation1hTokens,
				CacheReadTokens:       result.Usage.CacheReadInputTokens,
				ImageOutputTokens:     result.Usage.ImageOutputTokens,
			},
		)
	}

	subscriptionMultiplier, balanceMultiplier, subscriptionMultiplierScale := RatesForMode(
		apiKey, cost, funding.subscriptionMultiplier, funding.balanceMultiplier, rateNow,
	)
	// Commit or deduplicate the financial command before writing analysis, so a
	// later log retry can reuse this row without submitting another funds operation.
	_, billingErr := s.Apply(ctx, input.SettlementKey(), usageLog, &usageBillingParams{
		Cost:                            cost,
		User:                            user,
		APIKey:                          apiKey,
		Provider:                        provider,
		Subscription:                    subscription,
		RequestPayloadHash:              input.RequestPayloadHash,
		ProviderRateMultiplier:          providerRateMultiplier,
		SubscriptionRateMultiplier:      subscriptionMultiplier,
		SubscriptionRateMultiplierScale: subscriptionMultiplierScale,
		BalanceRateMultiplier:           balanceMultiplier,
		QuotaUpdates:                    input.QuotaUpdates,
	})

	if billingErr != nil {
		// The failed funds result leaves measured work and calculated base buckets useful
		// for reconciliation; mark the unsuccessful charge zero before attempting its row write.
		usageLog.ActualCost = 0
		s.WriteUsage(ctx, usageLog, "service.gateway")
		return billingErr
	}
	s.WriteUsage(ctx, usageLog, "service.gateway")
	return nil
}

// BuildRecordUsageLog constructs native-bucket analysis metadata before settlement.
// Callers supply nonnil input, result, key, payer, and provider values; optional
// costs leave amount fields zero and an absent subscription leaves its ID nil.
//
// Actor and payer identities occupy separate columns, and SettlementKey remains
// distinct from the diagnostic RequestID. The raw response declaration supplies
// nullable mismatch evidence against the actual outbound model. Media counts and
// the calculator mode choose the provisional rate, which ApplyResultToLog may later
// replace with committed allocation data. Returned optional pointers and size maps
// can remain shared with the captured arguments; persistence wrappers clone them.
// CreatedAt uses current wall time independently of the pricing options or clock.
func (s *Recorder) BuildRecordUsageLog(
	ctx context.Context,
	input *Input,
	result *Result,
	apiKey *KeySnapshot,
	user *PayerSnapshot,
	provider *ProviderSnapshot,
	subscription *billing.UserSubscription,
	requestedModel string,
	multiplier float64,
	imageMultiplier float64,
	providerRateMultiplier float64,
	billingType int8,
	cacheTTLOverridden bool,
	cost *CostBreakdown,
	opts *PricingOptions,
) *UsageLog {
	// Persist elapsed duration in whole milliseconds, discarding finer-resolution remainder.
	durationMs := int(result.Duration.Milliseconds())
	if observed := result.Timings.DurationMs(); observed != nil {
		durationMs = *observed
	}
	requestID := input.RequestID
	usageLog := &UsageLog{
		Timings:               result.Timings.Clone(),
		UserID:                actorID(apiKey, user),
		BillingUserID:         user.ID,
		TeamID:                apiKey.TeamID,
		APIKeyID:              apiKey.ID,
		ProviderID:            provider.ID,
		Platform:              provider.Platform,
		RequestID:             requestID,
		BillingKey:            input.SettlementKey(),
		UpstreamRequestID:     result.UpstreamRequestID,
		Model:                 result.Model,
		RequestedModel:        requestedModel,
		UpstreamModel:         OptionalTrimmedStringPtr(result.UpstreamModel),
		UpstreamResponseModel: OptionalTrimmedStringPtr(result.UpstreamResponseModel),
		UpstreamModelMismatch: responseModelMismatch(result),
		ReasoningEffort:       result.ReasoningEffort,
		RequestedReasoningEffort: CoalesceRequestedReasoningEffort(
			result.RequestedReasoningEffort,
			input.RequestedReasoningEffort,
		),
		ServiceTier:            OptionalTrimmedStringPtr(ForwardServiceTier(result)),
		InboundEndpoint:        OptionalTrimmedStringPtr(input.InboundEndpoint),
		UpstreamEndpoint:       OptionalTrimmedStringPtr(input.UpstreamEndpoint),
		InputTokens:            result.Usage.InputTokens,
		OutputTokens:           result.Usage.OutputTokens,
		CacheCreationTokens:    result.Usage.CacheCreationInputTokens,
		CacheReadTokens:        result.Usage.CacheReadInputTokens,
		CacheCreation5mTokens:  result.Usage.CacheCreation5mTokens,
		CacheCreation1hTokens:  result.Usage.CacheCreation1hTokens,
		ImageOutputTokens:      result.Usage.ImageOutputTokens,
		RateMultiplier:         multiplier,
		ProviderRateMultiplier: &providerRateMultiplier,
		BillingType:            billingType,
		BillingMode:            ResolveBillingMode(result, cost),
		Stream:                 result.Stream,
		DurationMs:             &durationMs,

		ImageCount:         result.ImageCount,
		ImageFrameCount:    result.ImageFrameCount,
		ImageSize:          OptionalTrimmedStringPtr(result.ImageSize),
		ImageInputSize:     OptionalTrimmedStringPtr(result.ImageInputSize),
		ImageOutputSize:    OptionalTrimmedStringPtr(result.ImageOutputSize),
		ImageSizeSource:    OptionalTrimmedStringPtr(result.ImageSizeSource),
		ImageSizeBreakdown: result.ImageSizeBreakdown,
		CacheTTLOverridden: cacheTTLOverridden,
		PricingConfigID:    OptionalInt64Ptr(input.PricingConfigID),
		ModelMappingChain:  OptionalTrimmedStringPtr(input.ModelMappingChain),
		UserAgent:          OptionalTrimmedStringPtr(input.UserAgent),
		IPAddress:          OptionalTrimmedStringPtr(input.IPAddress),
		SessionID:          OptionalTrimmedStringPtr(input.ClientSessionID),
		GroupID:            apiKey.GroupID,
		SubscriptionID:     optionalSubscriptionID(subscription),
		CreatedAt:          time.Now(),
	}
	if result.ImageCount > 0 && (cost == nil || cost.BillingMode != string(BillingModeToken)) {
		usageLog.RateMultiplier = imageMultiplier
	}
	if cost != nil {
		usageLog.InputCost = cost.InputCost
		usageLog.OutputCost = cost.OutputCost
		usageLog.ImageOutputCost = cost.ImageOutputCost
		usageLog.CacheCreationCost = cost.CacheCreationCost
		usageLog.CacheReadCost = cost.CacheReadCost
		usageLog.TotalCost = cost.TotalCost
		usageLog.ActualCost = cost.ActualCost
		usageLog.LongContextBillingApplied = cost.LongContextBillingApplied
	}

	return usageLog
}

// RecordOpenAI splits inclusive input observations and records eligible captured
// work. Missing input/result or required owner/provider snapshots returns an error;
// a nonnil eligible provider can have forbidden evidence reset before later validation.
// Callers own the input result during normalization or use Record to snapshot it.
//
// Credential-parent evidence controls OAuth tier and free-Fast eligibility while
// attribution uses the executing provider. A nonzero PricingAt fixes this turn's
// price instant; otherwise the recorder clock supplies it. Cache read and write
// buckets are subtracted once from total input, with a zero floor for excess cache
// counts. Missing-price errors retain a mode-tagged zero cost, whereas other price
// errors stop before funds or analysis persistence.
//
// Free Fast recalculates the standard user base while retaining premium TotalCost
// for provider statistics. Apply then resolves final funding before WriteUsage;
// a settlement error writes measured work with ActualCost zero and is returned,
// while log errors are observed after the financial result and do not resettle.
func (s *Recorder) RecordOpenAI(ctx context.Context, input *Input) error {
	if input == nil {
		return errors.New("openai usage input is nil")
	}
	result := input.Result
	if result == nil {
		return errors.New("openai usage result is nil")
	}
	if s.health != nil && input.Provider != nil && input.Provider.ResetForbiddenCounterOnSuccess {
		s.health.ResetOpenAI403Counter(ctx, input.Provider.ID)
	}
	apiKey, user, provider, subscription := s.keyWithBillingSettings(ctx, input.APIKey), input.User, input.Provider, input.Subscription
	if apiKey == nil || user == nil || provider == nil {
		return errors.New("openai usage input requires api key, user, and provider")
	}
	billingProvider := provider
	if s.providers != nil {
		var err error
		billingProvider, err = s.providers.CredentialProvider(ctx, *provider)
		if err != nil {
			return err
		}
	}
	s.normalizeResult(result, billingProvider, true, provider, input.RequestID)

	// Cache reads and writes overlap inclusive OpenAI input; subtract both once,
	// then floor excess cache observations at zero to prevent negative ordinary-input charges.
	actualInputTokens := max(result.Usage.InputTokens-result.Usage.CacheReadInputTokens-result.Usage.CacheCreationInputTokens, 0)

	tokens := UsageTokens{
		InputTokens:          actualInputTokens,
		ImageInputTokens:     result.Usage.ImageInputTokens,
		ImageCacheReadTokens: result.Usage.ImageCacheReadTokens,
		OutputTokens:         result.Usage.OutputTokens,
		CacheCreationTokens:  result.Usage.CacheCreationInputTokens,
		CacheReadTokens:      result.Usage.CacheReadInputTokens,
		ImageOutputTokens:    result.Usage.ImageOutputTokens,
	}

	funding := s.resolvePreliminaryFunding(ctx, apiKey, subscription, user.ID)
	subscription = funding.subscription
	// Keep media and tool factors at the captured base while preparing a peak
	// candidate for token mode; the final card mode supplies transaction allocation scale.
	baseMultiplier := funding.baseMultiplier
	rateNow := s.now()
	if !input.PricingAt.IsZero() {
		rateNow = input.PricingAt
	}
	multiplier, imageMultiplier := ComputePeakAwareMultipliers(apiKey, baseMultiplier, rateNow)
	videoMultiplier := baseMultiplier

	var cost *CostBreakdown
	var err error
	billingModel := OpenAIUsageBillingModel(result, input.PricingUsageFields)
	billingModels := s.models.Candidates(billingModel, result.BillingModel, input.GroupMappedModel, input.OriginalModel, result.UpstreamModel, result.Model)
	billingModels = s.FilterCompatibleClaudeBillingCandidates(ctx, provider, apiKey, billingModels)
	serviceTier := ""
	if result.ServiceTier != nil {
		serviceTier = strings.TrimSpace(*result.ServiceTier)
	}
	cost, err = s.CalculateOpenAIRecordUsageCostAt(
		ctx,
		result,
		apiKey,
		billingModels,
		multiplier,
		imageMultiplier,
		videoMultiplier,
		baseMultiplier,
		tokens,
		serviceTier,
		rateNow,
	)
	if err != nil {
		if !IsUsagePricingUnavailableError(err) {
			return err
		}
		s.observeEvent(BillingEvent{Kind: "pricing_missing", Component: "service.openai_gateway", Models: billingModels, RequestedModel: input.OriginalModel, MappedModel: input.GroupMappedModel, UpstreamModel: result.UpstreamModel, KeyID: apiKey.ID, ProviderID: provider.ID, Err: err})
		// Missing selected-model pricing still leaves observed media and tokens to record;
		// retain the attempted mode and continue with a zero-cost financial command.
		mode := string(BillingModeToken)
		if cost != nil && cost.BillingMode != "" {
			mode = cost.BillingMode
		}
		cost = &CostBreakdown{BillingMode: mode}
	}

	// Recalculate a standard user base independently of the premium model cost,
	// then pass that base to subscription/balance allocation while statistics retain TotalCost.
	var billingBaseAmountUSD *float64
	if GroupBillsOpenAIFastAtStandard(apiKey, billingProvider, serviceTier) && cost != nil {
		standardCost, standardErr := s.CalculateOpenAIRecordUsageCostAt(
			ctx,
			result,
			apiKey,
			billingModels,
			multiplier,
			imageMultiplier,
			videoMultiplier,
			baseMultiplier,
			tokens,
			"",
			rateNow,
		)
		if standardErr != nil {
			if !IsUsagePricingUnavailableError(standardErr) {
				return standardErr
			}
			// A missing standard price makes the user allocation base zero; premium TotalCost
			// remains available to provider statistics and its separate quota-cost calculation.
			s.observeEvent(BillingEvent{Kind: "standard_pricing_missing", Component: "service.openai_gateway", RequestID: input.RequestID, Err: standardErr})
			standardCost = &CostBreakdown{}
		}
		standardBase := standardCost.TotalCost
		billingBaseAmountUSD = &standardBase
		cost.ActualCost = standardCost.ActualCost
	}

	// This preliminary subscription choice supplies row metadata before Apply; an
	// applied transaction result replaces it with the allocation that actually funded usage.
	isSubscriptionBilling := subscription != nil
	billingType := BillingTypeBalance
	if isSubscriptionBilling {
		billingType = BillingTypeSubscription
	}

	// Persist elapsed duration in whole milliseconds, discarding finer-resolution remainder.
	durationMs := int(result.Duration.Milliseconds())
	if observed := result.Timings.DurationMs(); observed != nil {
		durationMs = *observed
	}
	providerRateMultiplier := provider.RateMultiplier
	requestID := input.RequestID

	// Keep the pre-group requested model for analysis independently of the selected
	// pricing source; WriteUsage can additionally apply captured client display evidence.
	requestedModel := result.Model
	if input.OriginalModel != "" {
		requestedModel = input.OriginalModel
	}

	usageLog := &UsageLog{
		Timings:               result.Timings.Clone(),
		UserID:                actorID(apiKey, user),
		BillingUserID:         user.ID,
		TeamID:                apiKey.TeamID,
		APIKeyID:              apiKey.ID,
		ProviderID:            provider.ID,
		Platform:              provider.Platform,
		RequestID:             requestID,
		BillingKey:            input.SettlementKey(),
		UpstreamRequestID:     result.UpstreamRequestID,
		Model:                 result.Model,
		RequestedModel:        requestedModel,
		UpstreamModel:         OptionalTrimmedStringPtr(result.UpstreamModel),
		UpstreamResponseModel: OptionalTrimmedStringPtr(result.UpstreamResponseModel),
		UpstreamModelMismatch: responseModelMismatch(result),
		ServiceTier:           result.ServiceTier,
		ReasoningEffort:       result.ReasoningEffort,
		RequestedReasoningEffort: CoalesceRequestedReasoningEffort(
			result.RequestedReasoningEffort,
			input.RequestedReasoningEffort,
		),
		InboundEndpoint:      OptionalTrimmedStringPtr(input.InboundEndpoint),
		UpstreamEndpoint:     OptionalTrimmedStringPtr(input.UpstreamEndpoint),
		InputTokens:          actualInputTokens,
		OutputTokens:         result.Usage.OutputTokens,
		CacheCreationTokens:  result.Usage.CacheCreationInputTokens,
		CacheReadTokens:      result.Usage.CacheReadInputTokens,
		ImageInputTokens:     result.Usage.ImageInputTokens,
		ImageCacheReadTokens: result.Usage.ImageCacheReadTokens,
		ImageOutputTokens:    result.Usage.ImageOutputTokens,
		ImageCount:           result.ImageCount,
		ImageFrameCount:      result.ImageFrameCount,
		ImageSize:            OptionalTrimmedStringPtr(result.ImageSize),
		ImageInputSize:       OptionalTrimmedStringPtr(result.ImageInputSize),
		ImageOutputSize:      OptionalTrimmedStringPtr(result.ImageOutputSize),
		ImageSizeSource:      OptionalTrimmedStringPtr(result.ImageSizeSource),
		ImageSizeBreakdown:   result.ImageSizeBreakdown,
	}
	isVideoUsage := IsGrokVideoUsageResult(result, billingModels)
	// Video identity remains in the analysis row even when a configured token card
	// priced its measured usage; normalized duration and resolution describe the completed output.
	if isVideoUsage {
		usageLog.VideoCount = result.VideoCount
		usageLog.VideoResolution = OptionalTrimmedStringPtr(NormalizeVideoBillingResolutionOrDefault(result.VideoResolution))
		videoDurationSeconds := NormalizeVideoBillingDurationSecondsOrDefault(result.VideoDurationSeconds)
		usageLog.VideoDurationSeconds = &videoDurationSeconds
	}
	if cost != nil {
		usageLog.InputCost = cost.InputCost
		usageLog.ImageInputCost = cost.ImageInputCost
		usageLog.OutputCost = cost.OutputCost
		usageLog.ImageOutputCost = cost.ImageOutputCost
		usageLog.CacheCreationCost = cost.CacheCreationCost
		usageLog.CacheReadCost = cost.CacheReadCost
		usageLog.TotalCost = cost.TotalCost
		usageLog.ActualCost = cost.ActualCost
		usageLog.LongContextBillingApplied = cost.LongContextBillingApplied
	}
	if isVideoUsage && (cost == nil || cost.BillingMode != string(BillingModeToken)) {
		usageLog.RateMultiplier = videoMultiplier
	} else if result.ImageCount > 0 && (cost == nil || cost.BillingMode != string(BillingModeToken)) {
		usageLog.RateMultiplier = imageMultiplier
	} else {
		usageLog.RateMultiplier = multiplier
	}
	usageLog.ProviderRateMultiplier = &providerRateMultiplier
	usageLog.BillingType = billingType
	usageLog.Stream = result.Stream
	usageLog.NativeCompactionV2 = input.NativeCompactionV2
	if input.CyberBlocked {
		usageLog.RequestType = RequestTypeCyberBlocked
	}
	usageLog.OpenAIWSMode = result.OpenAIWSMode
	usageLog.DurationMs = &durationMs
	usageLog.CreatedAt = time.Now()

	usageLog.PricingConfigID = OptionalInt64Ptr(input.PricingConfigID)
	usageLog.ModelMappingChain = OptionalTrimmedStringPtr(input.ModelMappingChain)

	// A configured token card can price positive video/image quantities; retain that
	// actual calculator mode before using media counts as an unpriced-row classification.
	if cost != nil && cost.BillingMode != "" {
		billingMode := cost.BillingMode
		usageLog.BillingMode = &billingMode
	} else if isVideoUsage {
		billingMode := string(BillingModeVideo)
		usageLog.BillingMode = &billingMode
	} else if result.ImageCount > 0 {
		billingMode := string(BillingModeImage)
		usageLog.BillingMode = &billingMode
	} else {
		billingMode := string(BillingModeToken)
		usageLog.BillingMode = &billingMode
	}

	if input.UserAgent != "" {
		usageLog.UserAgent = &input.UserAgent
	}

	if input.IPAddress != "" {
		usageLog.IPAddress = &input.IPAddress
	}

	// Store the captured client session ID; empty values become nil.
	usageLog.SessionID = OptionalTrimmedStringPtr(input.ClientSessionID)

	if apiKey.GroupID != nil {
		usageLog.GroupID = apiKey.GroupID
	}
	if subscription != nil {
		usageLog.SubscriptionID = &subscription.ID
	}

	// Provider statistics use their own rule ordering and Qoder requested-alias
	// preference; user pricing already selected its separate configured billing identity.
	if apiKey.GroupID != nil {
		s.applyProviderStatsCost(ctx, usageLog,
			provider.ID, *apiKey.GroupID, result.UpstreamModel, requestedModel, input.GroupMappedModel,
			tokens)
	}

	subscriptionMultiplier, balanceMultiplier, subscriptionMultiplierScale := RatesForMode(
		apiKey, cost, funding.subscriptionMultiplier, funding.balanceMultiplier, rateNow,
	)
	// Financial application precedes the analysis write even for zero-cost or partial
	// records; only an applied transaction can replace provisional allocation metadata.
	billingErr := func() error {
		_, err := s.Apply(ctx, input.SettlementKey(), usageLog, &usageBillingParams{
			Cost:                            cost,
			User:                            user,
			APIKey:                          apiKey,
			Provider:                        provider,
			Subscription:                    subscription,
			RequestPayloadHash:              input.RequestPayloadHash,
			ProviderRateMultiplier:          providerRateMultiplier,
			SubscriptionRateMultiplier:      subscriptionMultiplier,
			SubscriptionRateMultiplierScale: subscriptionMultiplierScale,
			BalanceRateMultiplier:           balanceMultiplier,
			QuotaUpdates:                    input.QuotaUpdates,
			BillingBaseAmountUSD:            billingBaseAmountUSD,
		})
		return err
	}()

	if billingErr != nil {
		// The failed funds result leaves measured work and calculated base buckets useful
		// for reconciliation; mark the unsuccessful charge zero before attempting its row write.
		usageLog.ActualCost = 0
		s.WriteUsage(ctx, usageLog, "service.openai_gateway")
		return billingErr
	}
	s.WriteUsage(ctx, usageLog, "service.openai_gateway")
	return nil
}

// resolvePreliminaryFunding selects subscription evidence and the rates used
// before peak pricing and settlement. Callers supply a nonnil captured key and
// the payer identity after loading current billing settings.
//
// Funding mode first decides whether to retain, ignore, or infer a subscription.
// A selected subscription suppresses the balance override lookup and uses its
// covered plan rate for the provisional charge. Allocation defaults retain group
// rates because settlement reloads plan rates itself. Missing group evidence
// leaves recorder defaults, and automatic eligibility errors permit balance pricing.
func (s *Recorder) resolvePreliminaryFunding(
	ctx context.Context,
	key *KeySnapshot,
	current *billing.UserSubscription,
	userID int64,
) preliminaryFunding {
	defaultMultiplier := s.defaultMultiplier
	funding := preliminaryFunding{
		baseMultiplier:         defaultMultiplier,
		subscriptionMultiplier: defaultMultiplier,
		balanceMultiplier:      defaultMultiplier,
	}
	funding.subscription = s.resolveSubscription(ctx, key, current, userID, key.GroupID)
	if key.GroupID != nil && key.Group != nil {
		groupDefault := key.Group.RateMultiplier
		funding.subscriptionMultiplier = groupDefault
		funding.balanceMultiplier = groupDefault
		if funding.subscription == nil {
			funding.balanceMultiplier = s.resolveUserGroupRateMultiplier(ctx, userID, *key.GroupID, groupDefault)
		}
	}
	if key.GroupID != nil && key.Group != nil && funding.subscription == nil {
		funding.baseMultiplier = funding.balanceMultiplier
	} else {
		// Plan coverage can retain the recorder default when the captured group is
		// excluded; pass no balance resolver because its eligible lookup already ran above.
		funding.baseMultiplier = ResolveUsageRateMultiplier(
			ctx, userID, key.GroupID, key.Group, funding.baseMultiplier, funding.subscription, nil,
		)
	}
	return funding
}

// RecordCyber snapshots eligible Cyber inputs and invokes ordinary completion
// with CyberBlocked set. Missing required snapshots/result or a blank model skips
// the call; zero measured usage can still produce a priced or zero-cost row.
//
// The usual CaptureCyber result uses ordinary OpenAI buckets, whose row construction
// writes the Cyber classification. A caller-supplied NativeUsage result instead
// selects RecordAnthropic, whose current row construction does not copy that flag.
// The source input remains unchanged, and any returned recording error is reported
// through printf rather than returned from this method.
func (s *Recorder) RecordCyber(ctx context.Context, in *Input) {
	if s == nil || in == nil || in.APIKey == nil || in.User == nil || in.Provider == nil || in.Result == nil || strings.TrimSpace(in.Result.Model) == "" {
		return
	}
	snapshot := Snapshot(in)
	snapshot.Result.Model = strings.TrimSpace(snapshot.Result.Model)
	snapshot.CyberBlocked = true
	// Cyber reporting retains its independently captured input and normal funds/log
	// ordering. A funds error can follow a reconciliation write attempt; provider or
	// pricing errors can return earlier and are observed here as well.
	if err := s.Record(ctx, snapshot, true); err != nil {
		s.printf("service.openai_gateway", "cyber usage record failed: request_id=%s err=%v", snapshot.RequestID, err)
	}
}
