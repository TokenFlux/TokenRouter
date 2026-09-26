// 用量归一化、查价、事实构造和资金完成顺序在此保持唯一实现。
package completion

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

func (s *Recorder) RecordAnthropic(ctx context.Context, input *Input, opts *PricingOptions) error {
	if opts == nil {
		opts = &PricingOptions{}
	}
	result := input.Result
	apiKey := input.APIKey
	user := input.User
	account := input.Account
	subscription := input.Subscription
	s.normalizeResult(result, account, false, account)

	// 强制缓存计费：将 input_tokens 转为 cache_read_input_tokens
	// 用于粘性会话切换时的特殊计费处理
	if input.ForceCacheBilling && result.Usage.InputTokens > 0 {
		s.printf("service.gateway", "force_cache_billing: %d input_tokens → cache_read_input_tokens (account=%d)",
			result.Usage.InputTokens, account.ID)
		result.Usage.CacheReadInputTokens += result.Usage.InputTokens
		result.Usage.InputTokens = 0
	}

	// Cache TTL Override: 确保计费时 token 分类与账号设置一致。
	// 账号级设置优先；全局 1h 请求注入开启时，默认把 usage 计费归回 5m。
	cacheTTLOverridden := false
	if overrideTarget := s.cacheOverrideTarget(ctx, input); overrideTarget != "" {
		applyCacheOverride(&result.Usage, overrideTarget)
		cacheTTLOverridden = (result.Usage.CacheCreation5mTokens + result.Usage.CacheCreation1hTokens) > 0
	}

	// 获取费率倍数（优先级：用户专属 > 分组默认 > 系统默认）
	multiplier := s.defaultMultiplier
	subscriptionMultiplier := multiplier
	balanceMultiplier := multiplier
	subscription = s.resolveSubscription(ctx, apiKey, subscription, user.ID, apiKey.GroupID)
	if apiKey.GroupID != nil && apiKey.Group != nil {
		groupDefault := apiKey.Group.RateMultiplier
		subscriptionMultiplier = groupDefault
		balanceMultiplier = groupDefault
		if subscription == nil {
			balanceMultiplier = s.resolveUserGroupRateMultiplier(ctx, user.ID, *apiKey.GroupID, groupDefault)
		}
	}
	if apiKey.GroupID != nil && apiKey.Group != nil && subscription == nil {
		multiplier = balanceMultiplier
	} else {
		multiplier = ResolveUsageRateMultiplier(ctx, user.ID, apiKey.GroupID, apiKey.Group, multiplier, subscription, nil)
	}
	// token 倍率叠加高峰因子（token 计费含图片 token，图片按次倍率不受影响）。高峰因子按请求时刻现算，
	// 不并入上面的 getUserGroupRateMultiplier，以免污染 user:group 倍率缓存。
	rateNow := s.now()
	opts.PricingAt = rateNow
	multiplier, imageMultiplier := ComputePeakAwareMultipliers(apiKey, multiplier, rateNow)

	// 确定计费模型
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

	// 确定 RequestedModel（分组映射前的原始模型）
	requestedModel := result.Model
	if input.OriginalModel != "" {
		requestedModel = input.OriginalModel
	}

	// 计算费用
	cost := s.CalculateRecordUsageCost(ctx, result, apiKey, account, billingModel, requestedModel, input.BillingModelSource, input.GroupMappedModel, multiplier, imageMultiplier, opts)

	// 预填 billing_type 仅用于 simple mode / 持久化前对象，真实扣费结果会在统一扣费后回填。
	isSubscriptionBilling := subscription != nil
	billingType := BillingTypeBalance
	if isSubscriptionBilling {
		billingType = BillingTypeSubscription
	}

	// 创建使用日志
	accountRateMultiplier := account.RateMultiplier
	usageLog := s.BuildRecordUsageLog(ctx, input, result, apiKey, user, account, subscription,
		requestedModel, multiplier, imageMultiplier, accountRateMultiplier, billingType, cacheTTLOverridden, cost, opts)

	// 计算账号统计定价费用（Qoder 会先按原始请求 alias、再按分组映射模型 / 最终 upstream 匹配自定义规则）
	if apiKey.GroupID != nil {
		s.applyAccountStatsCost(ctx, usageLog,
			account.ID, *apiKey.GroupID, result.UpstreamModel, requestedModel, input.GroupMappedModel,
			// Anthropic's input_tokens excludes cache_read and cache_creation (billed separately);
			// OpenAI gateway uses actualInputTokens which also excludes cache_read for the same reason.
			UsageTokens{
				InputTokens:         result.Usage.InputTokens,
				OutputTokens:        result.Usage.OutputTokens,
				CacheCreationTokens: result.Usage.CacheCreationInputTokens,
				CacheReadTokens:     result.Usage.CacheReadInputTokens,
				ImageOutputTokens:   result.Usage.ImageOutputTokens,
			},
		)
	}

	if s.simple {
		s.WriteUsage(ctx, usageLog, "service.gateway")
		s.printf("service.gateway", "[SIMPLE MODE] Usage recorded (not billed): user=%d, tokens=%d", usageLog.UserID, usageLog.TotalTokens())
		s.effects.AccountUsed(account.ID)
		return nil
	}

	// 配额平台由 handler 在请求 ctx 内经 QuotaPlatform() 算定并通过 input 传入；
	// 后扣运行在 worker 池的 background ctx 上，无法再从 ctx 取 ForcePlatform。
	// 缺省（未设置）时回退到分组平台，保持对其它调用方的兼容。
	quotaPlatform := input.QuotaPlatform
	if quotaPlatform == "" {
		quotaPlatform = platformFromKey(apiKey)
	}
	subscriptionMultiplier, balanceMultiplier, subscriptionMultiplierScale := RatesForMode(apiKey, cost, subscriptionMultiplier, balanceMultiplier, rateNow)
	requestID := usageLog.RequestID
	_, billingErr := s.Apply(ctx, requestID, usageLog, &usageBillingParams{
		Cost:                            cost,
		User:                            user,
		APIKey:                          apiKey,
		Account:                         account,
		Subscription:                    subscription,
		RequestPayloadHash:              input.RequestPayloadHash,
		AccountRateMultiplier:           accountRateMultiplier,
		SubscriptionRateMultiplier:      subscriptionMultiplier,
		SubscriptionRateMultiplierScale: subscriptionMultiplierScale,
		BalanceRateMultiplier:           balanceMultiplier,
		QuotaUpdates:                    input.QuotaUpdates,
		Platform:                        quotaPlatform,
	})

	if billingErr != nil {
		// 结算事务失败时仍保留已计算的用量与成本明细；ActualCost 置零明确表示本次未成功结算。
		usageLog.ActualCost = 0
		s.WriteUsage(ctx, usageLog, "service.gateway")
		return billingErr
	}
	s.WriteUsage(ctx, usageLog, "service.gateway")
	return nil
}

func (s *Recorder) BuildRecordUsageLog(
	ctx context.Context,
	input *Input,
	result *Result,
	apiKey *KeySnapshot,
	user *PayerSnapshot,
	account *AccountSnapshot,
	subscription *billing.UserSubscription,
	requestedModel string,
	multiplier float64,
	imageMultiplier float64,
	accountRateMultiplier float64,
	billingType int8,
	cacheTTLOverridden bool,
	cost *CostBreakdown,
	opts *PricingOptions,
) *UsageLog {
	durationMs := int(result.Duration.Milliseconds())
	requestID := input.RequestID
	usageLog := &UsageLog{
		UserID:            actorID(apiKey, user),
		BillingUserID:     user.ID,
		TeamID:            apiKey.TeamID,
		APIKeyID:          apiKey.ID,
		AccountID:         account.ID,
		RequestID:         requestID,
		UpstreamRequestID: result.UpstreamRequestID,
		Model:             result.Model,
		RequestedModel:    requestedModel,
		UpstreamModel:     OptionalTrimmedStringPtr(result.UpstreamModel),
		ReasoningEffort:   result.ReasoningEffort,
		RequestedReasoningEffort: CoalesceRequestedReasoningEffort(
			result.RequestedReasoningEffort,
			input.RequestedReasoningEffort,
		),
		ServiceTier:           OptionalTrimmedStringPtr(ForwardServiceTier(result)),
		InboundEndpoint:       OptionalTrimmedStringPtr(input.InboundEndpoint),
		UpstreamEndpoint:      OptionalTrimmedStringPtr(input.UpstreamEndpoint),
		InputTokens:           result.Usage.InputTokens,
		OutputTokens:          result.Usage.OutputTokens,
		CacheCreationTokens:   result.Usage.CacheCreationInputTokens,
		CacheReadTokens:       result.Usage.CacheReadInputTokens,
		CacheCreation5mTokens: result.Usage.CacheCreation5mTokens,
		CacheCreation1hTokens: result.Usage.CacheCreation1hTokens,
		ImageOutputTokens:     result.Usage.ImageOutputTokens,
		RateMultiplier:        multiplier,
		AccountRateMultiplier: &accountRateMultiplier,
		BillingType:           billingType,
		BillingMode:           ResolveBillingMode(result, cost),
		Stream:                result.Stream,
		DurationMs:            &durationMs,
		FirstTokenMs:          result.FirstTokenMs,
		ImageCount:            result.ImageCount,
		ImageSize:             OptionalTrimmedStringPtr(result.ImageSize),
		ImageInputSize:        OptionalTrimmedStringPtr(result.ImageInputSize),
		ImageOutputSize:       OptionalTrimmedStringPtr(result.ImageOutputSize),
		ImageSizeSource:       OptionalTrimmedStringPtr(result.ImageSizeSource),
		ImageSizeBreakdown:    result.ImageSizeBreakdown,
		CacheTTLOverridden:    cacheTTLOverridden,
		PricingConfigID:       OptionalInt64Ptr(input.PricingConfigID),
		ModelMappingChain:     OptionalTrimmedStringPtr(input.ModelMappingChain),
		UserAgent:             OptionalTrimmedStringPtr(input.UserAgent),
		IPAddress:             OptionalTrimmedStringPtr(input.IPAddress),
		SessionID:             OptionalTrimmedStringPtr(input.ClientSessionID),
		GroupID:               apiKey.GroupID,
		SubscriptionID:        optionalSubscriptionID(subscription),
		CreatedAt:             time.Now(),
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

func (s *Recorder) RecordOpenAI(ctx context.Context, input *Input) error {
	if input == nil {
		return errors.New("openai usage input is nil")
	}
	result := input.Result
	if result == nil {
		return errors.New("openai usage result is nil")
	}
	if s.health != nil && input.Account != nil && (input.Account.OpenAI || input.Account.CNProvider) {
		s.health.ResetOpenAI403Counter(ctx, input.Account.ID)
	}
	apiKey, user, account, subscription := input.APIKey, input.User, input.Account, input.Subscription
	if apiKey == nil || user == nil || account == nil {
		return errors.New("openai usage input requires api key, user, and account")
	}
	billingAccount := account
	if s.accounts != nil {
		var err error
		billingAccount, err = s.accounts.CredentialAccount(ctx, *account)
		if err != nil {
			return err
		}
	}
	s.normalizeResult(result, billingAccount, true, account)

	// OpenAI input_tokens 是总输入，包含缓存读取和缓存写入明细。
	// 将三类 token 拆成互斥桶，避免缓存写入同时按普通输入和 cache_write 重复计费。
	actualInputTokens := result.Usage.InputTokens - result.Usage.CacheReadInputTokens - result.Usage.CacheCreationInputTokens
	if actualInputTokens < 0 {
		actualInputTokens = 0
	}

	// Calculate cost
	tokens := UsageTokens{
		InputTokens:         actualInputTokens,
		ImageInputTokens:    result.Usage.ImageInputTokens,
		OutputTokens:        result.Usage.OutputTokens,
		CacheCreationTokens: result.Usage.CacheCreationInputTokens,
		CacheReadTokens:     result.Usage.CacheReadInputTokens,
		ImageOutputTokens:   result.Usage.ImageOutputTokens,
	}

	// Get rate multiplier
	multiplier := s.defaultMultiplier
	subscriptionMultiplier := multiplier
	balanceMultiplier := multiplier
	subscription = s.resolveSubscription(ctx, apiKey, subscription, user.ID, apiKey.GroupID)
	if apiKey.GroupID != nil && apiKey.Group != nil {
		subscriptionMultiplier = apiKey.Group.RateMultiplier
		balanceMultiplier = apiKey.Group.RateMultiplier
		if subscription == nil {
			balanceMultiplier = s.resolveUserGroupRateMultiplier(ctx, user.ID, *apiKey.GroupID, apiKey.Group.RateMultiplier)
		}
	}
	if apiKey.GroupID != nil && apiKey.Group != nil && subscription == nil {
		multiplier = balanceMultiplier
	} else {
		multiplier = ResolveUsageRateMultiplier(ctx, user.ID, apiKey.GroupID, apiKey.Group, multiplier, subscription, nil)
	}
	// token 倍率叠加高峰因子（token 计费含图片 token，图片按次倍率不受影响）。高峰因子按请求时刻现算，
	// 不并入上面的 Resolve，以免污染 user:group 倍率缓存。
	baseMultiplier := multiplier
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
	billingModels = s.FilterCNProviderBillingModelCandidates(ctx, account, apiKey, billingModels)
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
		s.observeEvent(BillingEvent{Kind: "pricing_missing", Component: "service.openai_gateway", Models: billingModels, RequestedModel: input.OriginalModel, MappedModel: input.GroupMappedModel, UpstreamModel: result.UpstreamModel, KeyID: apiKey.ID, AccountID: account.ID, Err: err})
		cost = &CostBreakdown{BillingMode: string(BillingModeToken)}
	}

	// 免费 Fast 只减免用户侧费用。保留 Fast 的 TotalCost 供账号统计和审计，
	// 并记录 Standard 基础金额供统一订阅/余额分配使用。
	var billingBaseAmountUSD *float64
	if GroupBillsOpenAIFastAtStandard(apiKey, billingAccount, serviceTier) && cost != nil {
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
			// 标准价不可用时沿用既有缺价行为：不向用户扣费，但保留 Fast
			// 成本用于账号统计，避免一次新策略把成功请求变成计费错误。
			s.observeEvent(BillingEvent{Kind: "standard_pricing_missing", Component: "service.openai_gateway", RequestID: result.RequestID, Err: standardErr})
			standardCost = &CostBreakdown{}
		}
		standardBase := standardCost.TotalCost
		billingBaseAmountUSD = &standardBase
		cost.ActualCost = standardCost.ActualCost
	}

	// 预填 billing_type 仅用于 simple mode / 持久化前对象，真实扣费结果会在统一扣费后回填。
	isSubscriptionBilling := subscription != nil
	billingType := BillingTypeBalance
	if isSubscriptionBilling {
		billingType = BillingTypeSubscription
	}

	// Create usage log
	durationMs := int(result.Duration.Milliseconds())
	accountRateMultiplier := account.RateMultiplier
	requestID := input.RequestID
	if result.OpenAIWSMode {
		if upstreamRequestID := strings.TrimSpace(result.RequestID); upstreamRequestID != "" {
			requestID = upstreamRequestID
		}
	}
	// 异步 Grok 视频始终使用稳定任务 ID 去重，使状态与内容轮询共享一笔费用。
	// 否则 Redis 领取记录丢失时，上下文局部的客户端或本地 ID 会让每次轮询新增记录。
	if result.VideoCount > 0 {
		if stable := stableVideoRequestID(firstNonEmpty(
			strings.TrimPrefix(strings.TrimSpace(result.RequestID), "grok-video:"),
			strings.TrimSpace(result.ResponseID),
			strings.TrimPrefix(strings.TrimSpace(requestID), "grok-video:"),
		)); stable != "" {
			requestID = stable
		}
	}

	// 确定 RequestedModel（分组映射前的原始模型）
	requestedModel := result.Model
	if input.OriginalModel != "" {
		requestedModel = input.OriginalModel
	}

	usageLog := &UsageLog{
		UserID:            actorID(apiKey, user),
		BillingUserID:     user.ID,
		TeamID:            apiKey.TeamID,
		APIKeyID:          apiKey.ID,
		AccountID:         account.ID,
		RequestID:         requestID,
		UpstreamRequestID: result.UpstreamRequestID,
		Model:             result.Model,
		RequestedModel:    requestedModel,
		UpstreamModel:     OptionalTrimmedStringPtr(result.UpstreamModel),
		ServiceTier:       result.ServiceTier,
		ReasoningEffort:   result.ReasoningEffort,
		RequestedReasoningEffort: CoalesceRequestedReasoningEffort(
			result.RequestedReasoningEffort,
			input.RequestedReasoningEffort,
		),
		InboundEndpoint:     OptionalTrimmedStringPtr(input.InboundEndpoint),
		UpstreamEndpoint:    OptionalTrimmedStringPtr(input.UpstreamEndpoint),
		InputTokens:         actualInputTokens,
		OutputTokens:        result.Usage.OutputTokens,
		CacheCreationTokens: result.Usage.CacheCreationInputTokens,
		CacheReadTokens:     result.Usage.CacheReadInputTokens,
		ImageInputTokens:    result.Usage.ImageInputTokens,
		ImageOutputTokens:   result.Usage.ImageOutputTokens,
		ImageCount:          result.ImageCount,
		ImageSize:           OptionalTrimmedStringPtr(result.ImageSize),
		ImageInputSize:      OptionalTrimmedStringPtr(result.ImageInputSize),
		ImageOutputSize:     OptionalTrimmedStringPtr(result.ImageOutputSize),
		ImageSizeSource:     OptionalTrimmedStringPtr(result.ImageSizeSource),
		ImageSizeBreakdown:  result.ImageSizeBreakdown,
	}
	isVideoUsage := IsGrokVideoUsageResult(result, billingModels)
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
	usageLog.AccountRateMultiplier = &accountRateMultiplier
	usageLog.BillingType = billingType
	usageLog.Stream = result.Stream
	usageLog.NativeCompactionV2 = input.NativeCompactionV2
	if input.CyberBlocked {
		usageLog.RequestType = RequestTypeCyberBlocked
	}
	usageLog.OpenAIWSMode = result.OpenAIWSMode
	usageLog.DurationMs = &durationMs
	usageLog.FirstTokenMs = result.FirstTokenMs
	usageLog.CreatedAt = time.Now()
	// 记录共享价格配置 ID、模型映射链和计费模式
	usageLog.PricingConfigID = OptionalInt64Ptr(input.PricingConfigID)
	usageLog.ModelMappingChain = OptionalTrimmedStringPtr(input.ModelMappingChain)
	// 设置计费模式
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
	// 添加 UserAgent
	if input.UserAgent != "" {
		usageLog.UserAgent = &input.UserAgent
	}

	// 添加 IPAddress
	if input.IPAddress != "" {
		usageLog.IPAddress = &input.IPAddress
	}

	// 添加 SessionID（客户端显式会话标识；缺失/无效时保持 nil）
	usageLog.SessionID = OptionalTrimmedStringPtr(input.ClientSessionID)

	if apiKey.GroupID != nil {
		usageLog.GroupID = apiKey.GroupID
	}
	if subscription != nil {
		usageLog.SubscriptionID = &subscription.ID
	}

	// 计算账号统计定价费用（Qoder 会先按原始请求 alias、再按分组映射模型 / 最终 upstream 匹配自定义规则）
	if apiKey.GroupID != nil {
		s.applyAccountStatsCost(ctx, usageLog,
			account.ID, *apiKey.GroupID, result.UpstreamModel, requestedModel, input.GroupMappedModel,
			tokens)
	}

	if s.simple {
		s.WriteUsage(ctx, usageLog, "service.openai_gateway")
		s.printf("service.openai_gateway", "[SIMPLE MODE] Usage recorded (not billed): user=%d, tokens=%d", usageLog.UserID, usageLog.TotalTokens())
		s.effects.AccountUsed(account.ID)
		return nil
	}

	// 后扣运行在 worker 池的 background ctx 上，无法再从 ctx 读取 ForcePlatform；
	// 未设置时回退到分组平台，兼容测试和内部直接调用方。
	quotaPlatform := input.QuotaPlatform
	if quotaPlatform == "" {
		quotaPlatform = platformFromKey(apiKey)
	}

	subscriptionMultiplier, balanceMultiplier, subscriptionMultiplierScale := RatesForMode(apiKey, cost, subscriptionMultiplier, balanceMultiplier, rateNow)
	billingErr := func() error {
		_, err := s.Apply(ctx, requestID, usageLog, &usageBillingParams{
			Cost:                            cost,
			User:                            user,
			APIKey:                          apiKey,
			Account:                         account,
			Subscription:                    subscription,
			RequestPayloadHash:              input.RequestPayloadHash,
			AccountRateMultiplier:           accountRateMultiplier,
			SubscriptionRateMultiplier:      subscriptionMultiplier,
			SubscriptionRateMultiplierScale: subscriptionMultiplierScale,
			BalanceRateMultiplier:           balanceMultiplier,
			QuotaUpdates:                    input.QuotaUpdates,
			Platform:                        quotaPlatform,
			BillingBaseAmountUSD:            billingBaseAmountUSD,
		})
		return err
	}()

	if billingErr != nil {
		// 结算事务失败时仍保留已计算的用量与成本明细；ActualCost 置零明确表示本次未成功结算。
		usageLog.ActualCost = 0
		s.WriteUsage(ctx, usageLog, "service.openai_gateway")
		return billingErr
	}
	s.WriteUsage(ctx, usageLog, "service.openai_gateway")
	return nil
}
