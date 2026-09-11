package service

// 本文件由 openai_gateway_service.go 纯移动拆分而来：用量记录、计费成本计算与
// Codex 用量快照。仅做代码搬迁，无任何行为变更。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"go.uber.org/zap"
)

// OpenAIRecordUsageInput input for recording usage
type OpenAIRecordUsageInput struct {
	Result             *OpenAIForwardResult
	APIKey             *APIKey
	User               *User
	Account            *Account
	Subscription       *UserSubscription
	InboundEndpoint    string
	UpstreamEndpoint   string
	UserAgent          string // 请求的 User-Agent
	IPAddress          string // 请求的客户端 IP 地址
	ClientSessionID    string // 客户端显式会话标识（session_id / X-Session-Id 等请求头），仅用于用量行会话关联
	RequestPayloadHash string
	RequestBody        []byte // 原始请求体，用于解析客户端请求的计费推理档位
	// PricingAt 是 WS turn 开始时刻；普通 HTTP 调用留空并在记录时取当前时间。
	PricingAt     time.Time
	APIKeyService APIKeyQuotaUpdater
	QuotaPlatform string // user×platform 配额计量平台，由 handler 在请求 ctx 内算定后传入。
	CyberBlocked  bool
	// NativeCompactionV2 表示请求体运行时被识别为原生远程 compaction v2。
	NativeCompactionV2 bool
	ChannelUsageFields
}

// CyberPolicyUsageInput 是 forward 错误路径中 cyber_policy 命中的补记用量入参。
type CyberPolicyUsageInput struct {
	APIKey             *APIKey
	Account            *Account
	Subscription       *UserSubscription
	RequestID          string
	Model              string
	Stream             bool
	InputTokens        int
	OutputTokens       int
	InboundEndpoint    string
	UpstreamEndpoint   string
	UserAgent          string
	IPAddress          string
	ClientSessionID    string
	RequestPayloadHash string
	APIKeyService      APIKeyQuotaUpdater
	QuotaPlatform      string
	// NativeCompactionV2 保留错误路径中原生 compaction 标记。
	NativeCompactionV2 bool
	ChannelUsageFields
}

// RecordCyberPolicyUsageLog 为未进入正常成功用量路径的 cyber_policy 命中补记用量。
func (s *OpenAIGatewayService) RecordCyberPolicyUsageLog(ctx context.Context, in CyberPolicyUsageInput) {
	if s == nil || in.APIKey == nil || in.APIKey.User == nil || in.Account == nil || strings.TrimSpace(in.Model) == "" {
		return
	}
	result := &OpenAIForwardResult{
		RequestID: in.RequestID,
		Model:     strings.TrimSpace(in.Model),
		Usage: OpenAIUsage{
			InputTokens:  in.InputTokens,
			OutputTokens: in.OutputTokens,
		},
		Stream:   in.Stream,
		Duration: 0,
	}
	if err := s.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result:             result,
		APIKey:             in.APIKey,
		User:               in.APIKey.User,
		Account:            in.Account,
		Subscription:       in.Subscription,
		InboundEndpoint:    in.InboundEndpoint,
		UpstreamEndpoint:   in.UpstreamEndpoint,
		UserAgent:          in.UserAgent,
		IPAddress:          in.IPAddress,
		ClientSessionID:    in.ClientSessionID,
		RequestPayloadHash: in.RequestPayloadHash,
		APIKeyService:      in.APIKeyService,
		QuotaPlatform:      in.QuotaPlatform,
		ChannelUsageFields: in.ChannelUsageFields,
		CyberBlocked:       true,
		NativeCompactionV2: in.NativeCompactionV2,
	}); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber usage record failed: request_id=%s err=%v", in.RequestID, err)
	}
}

// resolveUserGroupRateMultiplier 解析 OpenAI 用量计费共用的用户/分组缓存倍率。
func (s *OpenAIGatewayService) resolveUserGroupRateMultiplier(ctx context.Context, userID, groupID int64, groupDefaultMultiplier float64) float64 {
	if s == nil {
		return groupDefaultMultiplier
	}
	resolver := s.userGroupRateResolver
	if resolver == nil {
		resolver = newUserGroupRateResolver(nil, nil, resolveUserGroupRateCacheTTL(s.cfg), nil, "service.openai_gateway")
	}
	return resolver.Resolve(ctx, userID, groupID, groupDefaultMultiplier)
}

// openAIUsageBillingModel 按渠道计费来源选择模型，并保留图片结果已经解析出的专用定价模型。
func openAIUsageBillingModel(result *OpenAIForwardResult, fields ChannelUsageFields) string {
	if result == nil {
		return ""
	}
	billingModel := forwardResultBillingModel(result.Model, result.UpstreamModel)
	explicitBillingModel := strings.TrimSpace(result.BillingModel)
	if explicitBillingModel != "" {
		billingModel = explicitBillingModel
	}

	switch fields.BillingModelSource {
	case BillingModelSourceUpstream:
		// 图片轮次的上游文本模型不是图片计价 SKU，必须保留显式解析出的图片模型。
		if upstreamModel := strings.TrimSpace(result.UpstreamModel); upstreamModel != "" && (result.ImageCount <= 0 || explicitBillingModel == "") {
			billingModel = upstreamModel
		}
	case BillingModelSourceChannelMapped:
		mappedModel := strings.TrimSpace(fields.ChannelMappedModel)
		if mappedModel != "" && mappedModel != strings.TrimSpace(fields.OriginalModel) {
			billingModel = mappedModel
		}
	case BillingModelSourceRequested:
		if requestedModel := strings.TrimSpace(fields.OriginalModel); requestedModel != "" {
			billingModel = requestedModel
		}
	}
	return billingModel
}

// groupBillsOpenAIFastAtStandard 判断分组免费 Fast 是否适用于当前 OpenAI 账号和计费档位。
// 分组策略只改变用户侧价格，不改变实际发往上游的 service_tier。
func groupBillsOpenAIFastAtStandard(apiKey *APIKey, account *Account, serviceTier string) bool {
	if apiKey == nil || apiKey.Group == nil || !apiKey.Group.FreeOpenAIFast {
		return false
	}
	if account == nil || !account.IsOpenAI() || !groupSupportsOpenAIFast(apiKey.Group.Platform) {
		return false
	}
	switch normalizeBillingServiceTier(serviceTier) {
	case "priority", "fast":
		return true
	default:
		return false
	}
}

// RecordUsage records usage and deducts balance
func (s *OpenAIGatewayService) RecordUsage(ctx context.Context, input *OpenAIRecordUsageInput) error {
	if input == nil {
		return errors.New("openai usage input is nil")
	}
	result := input.Result
	if result == nil {
		return errors.New("openai usage result is nil")
	}
	if s.rateLimitService != nil && input.Account != nil &&
		(input.Account.Platform == PlatformOpenAI || input.Account.IsCNProvider()) {
		s.rateLimitService.ResetOpenAI403Counter(ctx, input.Account.ID)
	}

	apiKey := input.APIKey
	user := input.User
	account := input.Account
	subscription := input.Subscription
	if apiKey == nil || user == nil || account == nil {
		return errors.New("openai usage input requires api key, user, and account")
	}
	billingAccount, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return err
	}
	if !isGrokVideoUsageResult(result, nil) {
		ApplyOpenAIImageBillingResolution(result)
	}
	logServiceTierBillingDowngrade("service.openai_gateway", account, result.RequestID, ApplyOpenAIServiceTierBillingResolution(billingAccount, result))

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
	multiplier := 1.0
	if s.cfg != nil {
		multiplier = s.cfg.Default.RateMultiplier
	}
	subscriptionMultiplier := multiplier
	balanceMultiplier := multiplier
	subscription = resolveUsageSubscriptionForAPIKey(ctx, apiKey, subscription, s.userSubRepo, usageSubscriptionResolverFrom(s.usageBillingRepo), user.ID, apiKey.GroupID)
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
		multiplier = resolveUsageRateMultiplier(ctx, user.ID, apiKey.GroupID, apiKey.Group, multiplier, subscription, nil)
	}
	// token 倍率叠加高峰因子（token 计费含图片 token，图片按次倍率不受影响）。高峰因子按请求时刻现算，
	// 不并入上面的 Resolve，以免污染 user:group 倍率缓存。
	baseMultiplier := multiplier
	rateNow := timezone.Now()
	if s.usageBillingNow != nil {
		rateNow = s.usageBillingNow()
	}
	if !input.PricingAt.IsZero() {
		rateNow = input.PricingAt
	}
	multiplier, imageMultiplier := computePeakAwareMultipliers(apiKey, baseMultiplier, rateNow)
	videoMultiplier := baseMultiplier

	var cost *CostBreakdown
	billingModel := openAIUsageBillingModel(result, input.ChannelUsageFields)
	billingModels := usageBillingModelCandidates(
		billingModel,
		result.BillingModel,
		input.ChannelMappedModel,
		input.OriginalModel,
		result.UpstreamModel,
		result.Model,
	)
	billingModels = s.filterCNProviderBillingModelCandidates(ctx, account, apiKey, billingModels)
	serviceTier := ""
	if result.ServiceTier != nil {
		serviceTier = strings.TrimSpace(*result.ServiceTier)
	}
	cost, err = s.calculateOpenAIRecordUsageCostAt(
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
		if !isUsagePricingUnavailableError(err) {
			return err
		}
		logger.L().With(
			zap.String("component", "service.openai_gateway"),
			zap.Strings("billing_models", billingModels),
			zap.String("requested_model", input.OriginalModel),
			zap.String("mapped_model", input.ChannelMappedModel),
			zap.String("upstream_model", result.UpstreamModel),
			zap.Int64("api_key_id", apiKey.ID),
			zap.Int64("account_id", account.ID),
		).Warn("openai_usage.pricing_missing_record_zero_cost", zap.Error(err))
		cost = &CostBreakdown{BillingMode: string(BillingModeToken)}
	}

	// 免费 Fast 只减免用户侧费用。保留 Fast 的 TotalCost 供账号统计和审计，
	// 并记录 Standard 基础金额供统一订阅/余额分配使用。
	var billingBaseAmountUSD *float64
	if groupBillsOpenAIFastAtStandard(apiKey, billingAccount, serviceTier) && cost != nil {
		standardCost, standardErr := s.calculateOpenAIRecordUsageCostAt(
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
			if !isUsagePricingUnavailableError(standardErr) {
				return standardErr
			}
			// 标准价不可用时沿用既有缺价行为：不向用户扣费，但保留 Fast
			// 成本用于账号统计，避免一次新策略把成功请求变成计费错误。
			logger.L().With(
				zap.String("component", "service.openai_gateway"),
				zap.String("request_id", result.RequestID),
			).Warn("openai_usage.standard_pricing_missing_free_fast_zero_cost", zap.Error(standardErr))
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
	accountRateMultiplier := account.BillingRateMultiplier()
	requestID := resolveUsageBillingRequestID(ctx, result.RequestID)
	if result.OpenAIWSMode {
		if upstreamRequestID := strings.TrimSpace(result.RequestID); upstreamRequestID != "" {
			requestID = upstreamRequestID
		}
	}
	// 异步 Grok 视频始终使用稳定任务 ID 去重，使状态与内容轮询共享一笔费用。
	// 否则 Redis 领取记录丢失时，上下文局部的客户端或本地 ID 会让每次轮询新增记录。
	if result.VideoCount > 0 {
		if stable := StableGrokVideoBillingRequestID(firstNonEmpty(
			strings.TrimPrefix(strings.TrimSpace(result.RequestID), "grok-video:"),
			strings.TrimSpace(result.ResponseID),
			strings.TrimPrefix(strings.TrimSpace(requestID), "grok-video:"),
		)); stable != "" {
			requestID = stable
		}
	}

	// 确定 RequestedModel（渠道映射前的原始模型）
	requestedModel := result.Model
	if input.OriginalModel != "" {
		requestedModel = input.OriginalModel
	}

	usageLog := &UsageLog{
		UserID:            usageActorUserID(apiKey, user),
		BillingUserID:     user.ID,
		TeamID:            apiKey.TeamID,
		APIKeyID:          apiKey.ID,
		AccountID:         account.ID,
		RequestID:         requestID,
		UpstreamRequestID: usageUpstreamRequestIDPtr(account, result.UpstreamHeaders, result.OpenAIWSMode),
		Model:             result.Model,
		RequestedModel:    requestedModel,
		UpstreamModel:     optionalTrimmedStringPtr(result.UpstreamModel),
		ServiceTier:       result.ServiceTier,
		ReasoningEffort:   result.ReasoningEffort,
		RequestedReasoningEffort: coalesceRequestedReasoningEffort(
			result.RequestedReasoningEffort,
			CanonicalRequestedReasoningEffort(input.RequestBody, input.OriginalModel, input.ChannelMappedModel),
		),
		InboundEndpoint:     optionalTrimmedStringPtr(input.InboundEndpoint),
		UpstreamEndpoint:    optionalTrimmedStringPtr(input.UpstreamEndpoint),
		InputTokens:         actualInputTokens,
		OutputTokens:        result.Usage.OutputTokens,
		CacheCreationTokens: result.Usage.CacheCreationInputTokens,
		CacheReadTokens:     result.Usage.CacheReadInputTokens,
		ImageInputTokens:    result.Usage.ImageInputTokens,
		ImageOutputTokens:   result.Usage.ImageOutputTokens,
		ImageCount:          result.ImageCount,
		ImageSize:           optionalTrimmedStringPtr(result.ImageSize),
		ImageInputSize:      optionalTrimmedStringPtr(result.ImageInputSize),
		ImageOutputSize:     optionalTrimmedStringPtr(result.ImageOutputSize),
		ImageSizeSource:     optionalTrimmedStringPtr(result.ImageSizeSource),
		ImageSizeBreakdown:  result.ImageSizeBreakdown,
	}
	isVideoUsage := isGrokVideoUsageResult(result, billingModels)
	if isVideoUsage {
		usageLog.VideoCount = result.VideoCount
		usageLog.VideoResolution = optionalTrimmedStringPtr(NormalizeVideoBillingResolutionOrDefault(result.VideoResolution))
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
	// 设置渠道信息
	usageLog.ChannelID = optionalInt64Ptr(input.ChannelID)
	usageLog.ModelMappingChain = optionalTrimmedStringPtr(input.ModelMappingChain)
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
	usageLog.SessionID = optionalTrimmedStringPtr(input.ClientSessionID)

	if apiKey.GroupID != nil {
		usageLog.GroupID = apiKey.GroupID
	}
	if subscription != nil {
		usageLog.SubscriptionID = &subscription.ID
	}

	// 计算账号统计定价费用（Qoder 会先按原始请求 alias、再按渠道 route key / 最终 upstream 匹配自定义规则）
	if apiKey.GroupID != nil {
		applyAccountStatsCost(ctx, usageLog, s.channelService, s.billingService,
			account.ID, *apiKey.GroupID, result.UpstreamModel, requestedModel, input.ChannelMappedModel,
			tokens, cost.TotalCost,
		)
	}

	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		writeUsageLogBestEffort(ctx, s.usageLogRepo, usageLog, "service.openai_gateway")
		logger.LegacyPrintf("service.openai_gateway", "[SIMPLE MODE] Usage recorded (not billed): user=%d, tokens=%d", usageLog.UserID, usageLog.TotalTokens())
		s.deferredService.ScheduleLastUsedUpdate(account.ID)
		return nil
	}

	// 后扣运行在 worker 池的 background ctx 上，无法再从 ctx 读取 ForcePlatform；
	// 未设置时回退到分组平台，兼容测试和内部直接调用方。
	quotaPlatform := input.QuotaPlatform
	if quotaPlatform == "" {
		quotaPlatform = PlatformFromAPIKey(apiKey)
	}

	subscriptionMultiplier, balanceMultiplier, subscriptionMultiplierScale := usageBillingRatesForMode(apiKey, cost, subscriptionMultiplier, balanceMultiplier, rateNow)
	billingErr := func() error {
		_, err := applyUsageBilling(ctx, requestID, usageLog, &usageBillingParams{
			Cost:                            cost,
			User:                            user,
			APIKey:                          apiKey,
			Account:                         account,
			Subscription:                    subscription,
			RequestPayloadHash:              resolveUsageBillingPayloadFingerprint(ctx, input.RequestPayloadHash),
			AccountRateMultiplier:           accountRateMultiplier,
			SubscriptionRateMultiplier:      subscriptionMultiplier,
			SubscriptionRateMultiplierScale: subscriptionMultiplierScale,
			BalanceRateMultiplier:           balanceMultiplier,
			APIKeyService:                   input.APIKeyService,
			Platform:                        quotaPlatform,
			BillingBaseAmountUSD:            billingBaseAmountUSD,
		}, s.billingDeps(), s.usageBillingRepo)
		return err
	}()

	if billingErr != nil {
		// 结算事务失败时仍保留已计算的用量与成本明细；ActualCost 置零明确表示本次未成功结算。
		usageLog.ActualCost = 0
		writeUsageLogBestEffort(ctx, s.usageLogRepo, usageLog, "service.openai_gateway")
		return billingErr
	}
	writeUsageLogBestEffort(ctx, s.usageLogRepo, usageLog, "service.openai_gateway")
	return nil
}

// calculateOpenAIRecordUsageCost 保留旧 unit 测试的兼容入口。
//
//nolint:unused // 该入口只供带 unit tag 的历史计费测试调用。
func (s *OpenAIGatewayService) calculateOpenAIRecordUsageCost(
	ctx context.Context,
	result *OpenAIForwardResult,
	apiKey *APIKey,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	webSearchMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
) (*CostBreakdown, error) {
	return s.calculateOpenAIRecordUsageCostAt(ctx, result, apiKey, billingModels, multiplier, imageMultiplier, videoMultiplier, webSearchMultiplier, tokens, serviceTier, time.Time{})
}

// calculateOpenAIRecordUsageCostAt 使用固定请求时刻计算费用，确保渠道分时倍率与高峰倍率同刻。
func (s *OpenAIGatewayService) calculateOpenAIRecordUsageCostAt(
	ctx context.Context,
	result *OpenAIForwardResult,
	apiKey *APIKey,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	webSearchMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
	pricingAt time.Time,
) (*CostBreakdown, error) {
	billingModel := firstUsageBillingModel(billingModels)
	if result != nil && result.WebSearchCalls > 0 {
		// Codex alpha/search 网页搜索按次计费：上游不返回 usage/token 字段，单价只取
		// 分组覆盖价（nil 时默认 0.01 = 官方 $10/1000 次），不参与渠道级模型定价。
		// 倍率与 image/video 按次口径一致：使用不含高峰因子的基础倍率
		//（用户专属 > 分组 rate_multiplier > 系统默认），与分组表单的价格预览承诺一致。
		return s.billingService.CalculateWebSearchCost(result.WebSearchCalls, webSearchPricePerCallFromAPIKey(apiKey), webSearchMultiplier), nil
	}
	if isGrokVideoUsageResult(result, billingModels) {
		if resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey); resolved == nil || resolved.Mode != BillingModeToken {
			return s.calculateOpenAIVideoCost(ctx, billingModel, apiKey, result, videoMultiplier), nil
		}
	}
	if result != nil && result.AudioUsage != nil {
		if resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey); resolved != nil &&
			(resolved.Mode == BillingModePerRequest) {
			gid := apiKey.Group.ID
			return s.billingService.CalculateCostUnified(CostInput{
				Ctx: ctx, Model: billingModel, GroupID: &gid, Group: apiKey.Group,
				UsageUnits: result.AudioUsage.DurationOrUnits, SizeTier: result.AudioUsage.Mode,
				RateMultiplier: webSearchMultiplier, Resolver: s.resolver, Resolved: resolved,
			})
		}
		cfg := groupAudioPriceConfigFromAPIKey(apiKey)
		return s.billingService.CalculateAudioCost(result.AudioUsage.Mode, result.AudioUsage.DurationOrUnits, cfg, webSearchMultiplier), nil
	}

	if result != nil && result.ImageCount > 0 {
		// 渠道定价为令牌计费时走令牌路径，否则走图片计费
		if resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey); resolved == nil || resolved.Mode != BillingModeToken {
			return s.calculateOpenAIImageCost(ctx, billingModel, apiKey, result, imageMultiplier), nil
		}
	}

	// Token 成本与搜索附加费分开计算，搜索费不得掩盖 token 定价失败。
	var tokenCost *CostBreakdown
	var lastErr error
	if len(billingModels) > 0 && billingModel != "" {
		for _, candidate := range billingModels {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			cost, err := s.calculateOpenAIRecordUsageTokenCostAt(
				ctx,
				apiKey,
				candidate,
				multiplier,
				pricingAt,
				tokens,
				serviceTier,
				forwardResultReasoningEffort(result),
			)
			if err == nil {
				tokenCost = cost
				break
			}
			lastErr = err
		}
	}

	var searchCost *CostBreakdown
	if result != nil && result.SearchCount > 0 {
		price := groupSearchPricePer1kFromAPIKey(apiKey)
		if price != nil && *price == 0 {
			logger.L().Info("openai_usage.search_price_per_1k_explicit_free",
				zap.Int("search_count", result.SearchCount),
				zap.String("model", billingModel),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
			)
		}
		searchCost = s.billingService.CalculateSearchCost(result.SearchCount, price, webSearchMultiplier)
	}

	tokenBillingAttempted := len(billingModels) > 0 && billingModel != ""
	if tokenCost == nil {
		if tokenBillingAttempted {
			if lastErr == nil {
				lastErr = fmt.Errorf("%w: no non-empty billing model candidates", ErrModelPricingUnavailable)
			}
			return nil, fmt.Errorf("calculate OpenAI usage cost failed for billing models %s: %w", strings.Join(billingModels, ","), lastErr)
		}
		// 仅搜索且没有模型的纯工具路径允许单独计算搜索费用。
		if searchCost != nil {
			return searchCost, nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("%w: openai usage billing model is empty", ErrModelPricingUnavailable)
		}
		return nil, fmt.Errorf("calculate OpenAI usage cost failed for billing models %s: %w", strings.Join(billingModels, ","), lastErr)
	}
	if searchCost == nil || (searchCost.TotalCost == 0 && searchCost.ActualCost == 0) {
		return tokenCost, nil
	}
	// 费用由令牌费用与搜索附加费相加。
	tokenCost.TotalCost += searchCost.TotalCost
	tokenCost.ActualCost += searchCost.ActualCost
	return tokenCost, nil
}

// isGrokVideoBillingModel 判断模型是否属于 Grok 视频生成系列。
func isGrokVideoBillingModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "grok-imagine-video")
}

// isGrokVideoUsageResult 结合视频数量和模型候选识别 Grok 视频用量。
func isGrokVideoUsageResult(result *OpenAIForwardResult, billingModels []string) bool {
	if result == nil || result.VideoCount <= 0 {
		return false
	}
	// VideoCount 本身就是异步视频完成计费的权威依据。
	// 存在模型族时优先匹配，但不得因重命名或映射丢失视频计费模式。
	candidates := append([]string{}, billingModels...)
	candidates = append(candidates, result.BillingModel, result.Model, result.UpstreamModel)
	for _, candidate := range candidates {
		if isGrokVideoBillingModel(candidate) {
			return true
		}
	}
	return true
}

// isUsagePricingUnavailableError 判断错误是否仅表示模型缺少可用定价。
func isUsagePricingUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrModelPricingUnavailable) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no pricing available") || strings.Contains(msg, "pricing not found")
}

func (s *OpenAIGatewayService) calculateOpenAIRecordUsageTokenCostAt(
	ctx context.Context,
	apiKey *APIKey,
	billingModel string,
	multiplier float64,
	pricingAt time.Time,
	tokens UsageTokens,
	serviceTier string,
	reasoningEffort string,
) (*CostBreakdown, error) {
	if s.resolver != nil && apiKey.Group != nil {
		gid := apiKey.Group.ID
		return s.billingService.CalculateCostUnified(CostInput{
			Ctx:             ctx,
			Model:           billingModel,
			GroupID:         &gid,
			Group:           apiKey.Group,
			Tokens:          tokens,
			RequestCount:    1,
			RateMultiplier:  multiplier,
			PricingAt:       pricingAt,
			ServiceTier:     serviceTier,
			ReasoningEffort: reasoningEffort,
			Resolver:        s.resolver,
		})
	}
	return s.billingService.CalculateCostUnified(CostInput{
		Ctx: ctx, Model: billingModel, Tokens: tokens, RateMultiplier: multiplier,
		ServiceTier: serviceTier, ReasoningEffort: reasoningEffort,
	})
}

// forwardResultReasoningEffort 只使用实际转发档位，避免按客户端改写前的 max 扣费。
func forwardResultReasoningEffort(result *OpenAIForwardResult) string {
	if result == nil || result.ReasoningEffort == nil {
		return ""
	}
	return *result.ReasoningEffort
}

// calculateOpenAIImageCost 对分组和渠道使用同一按张价卡路径，未配置时使用内置单价。
func (s *OpenAIGatewayService) calculateOpenAIImageCost(ctx context.Context, billingModel string, apiKey *APIKey, result *OpenAIForwardResult, multiplier float64) *CostBreakdown {
	sizeTier := NormalizeImageBillingTierOrDefault(result.ImageSize)
	resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey)
	if resolved != nil {
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: apiKey.GroupID, Group: apiKey.Group,
			RequestCount: result.ImageCount, SizeTier: sizeTier, RateMultiplier: multiplier,
			Resolver: s.resolver, Resolved: resolved,
		})
		if err != nil {
			logger.LegacyPrintf("service.openai_gateway", "Calculate image model card cost failed: %v", err)
			return &CostBreakdown{}
		}
		return cost
	}
	return s.billingService.CalculateImageCost(billingModel, sizeTier, result.ImageCount, multiplier)
}

// calculateOpenAIVideoCost 的 video 价卡按总秒数计费，按次价卡按输出数量计费。
func (s *OpenAIGatewayService) calculateOpenAIVideoCost(ctx context.Context, billingModel string, apiKey *APIKey, result *OpenAIForwardResult, multiplier float64) *CostBreakdown {
	videoCount := result.VideoCount
	if videoCount <= 0 {
		videoCount = 1
	}
	resolution := NormalizeVideoBillingResolutionOrDefault(result.VideoResolution)
	durationSeconds := NormalizeVideoBillingDurationSecondsOrDefault(result.VideoDurationSeconds)
	resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey)
	if resolved != nil {
		units := float64(videoCount)
		if resolved.Mode == BillingModeVideo {
			units *= float64(durationSeconds)
		}
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: billingModel, GroupID: apiKey.GroupID, Group: apiKey.Group,
			RequestCount: videoCount, UsageUnits: units, SizeTier: resolution, RateMultiplier: multiplier,
			Resolver: s.resolver, Resolved: resolved,
		})
		if err != nil {
			logger.LegacyPrintf("service.openai_gateway", "Calculate video model card cost failed: %v", err)
			return &CostBreakdown{}
		}
		return cost
	}
	return s.billingService.CalculateVideoCost(billingModel, resolution, videoCount, durationSeconds, multiplier)
}

// filterCNProviderBillingModelCandidates 防止国产供应商把客户端 claude 模型名
// 落入全局 Claude/Sonnet 兜底价。管理员显式配置的分组或渠道价格仍然有效。
func (s *OpenAIGatewayService) filterCNProviderBillingModelCandidates(
	ctx context.Context,
	account *Account,
	apiKey *APIKey,
	candidates []string,
) []string {
	if account == nil || !account.IsCNProvider() {
		return candidates
	}
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if isCNProviderClaudeFallbackCandidate(candidate) {
			// 纯倍率可以参与媒体计费，但不能替国产模型建立 Claude 的显式基础价。
			resolved := s.resolveOpenAIChannelPricing(ctx, candidate, apiKey)
			if !resolved.HasEffectiveOverridePricing() {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

// isCNProviderClaudeFallbackCandidate 与全局 Claude 系列 fallback 的触发词保持
// 对齐，避免裸 opus/sonnet/haiku 别名绕过 CN 计费保护。
func isCNProviderClaudeFallbackCandidate(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "claude") ||
		strings.Contains(model, "opus") ||
		strings.Contains(model, "sonnet") ||
		strings.Contains(model, "haiku")
}

func (s *OpenAIGatewayService) resolveOpenAIChannelPricing(ctx context.Context, billingModel string, apiKey *APIKey) *ResolvedPricing {
	if s.resolver == nil || apiKey == nil || apiKey.Group == nil {
		return nil
	}
	gid := apiKey.Group.ID
	resolved := s.resolver.Resolve(ctx, PricingInput{Model: billingModel, GroupID: &gid, Group: apiKey.Group})
	if resolved.HasConfiguredPricing() {
		return resolved
	}
	return nil
}

// ParseCodexRateLimitHeaders extracts Codex usage limits from response headers.
// Exported for use in ratelimit_service when handling OpenAI 429 responses.
func ParseCodexRateLimitHeaders(headers http.Header) *OpenAICodexUsageSnapshot {
	snapshot := &OpenAICodexUsageSnapshot{}
	hasData := false

	// Helper to parse float64 from header
	parseFloat := func(key string) *float64 {
		if v := headers.Get(key); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return &f
			}
		}
		return nil
	}

	// Helper to parse int from header
	parseInt := func(key string) *int {
		if v := headers.Get(key); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				return &i
			}
		}
		return nil
	}

	// Primary (weekly) limits
	if v := parseFloat("x-codex-primary-used-percent"); v != nil {
		snapshot.PrimaryUsedPercent = v
		hasData = true
	}
	if v := parseInt("x-codex-primary-reset-after-seconds"); v != nil {
		snapshot.PrimaryResetAfterSeconds = v
		hasData = true
	}
	if v := parseInt("x-codex-primary-window-minutes"); v != nil {
		snapshot.PrimaryWindowMinutes = v
		hasData = true
	}

	// Secondary (5h) limits
	if v := parseFloat("x-codex-secondary-used-percent"); v != nil {
		snapshot.SecondaryUsedPercent = v
		hasData = true
	}
	if v := parseInt("x-codex-secondary-reset-after-seconds"); v != nil {
		snapshot.SecondaryResetAfterSeconds = v
		hasData = true
	}
	if v := parseInt("x-codex-secondary-window-minutes"); v != nil {
		snapshot.SecondaryWindowMinutes = v
		hasData = true
	}

	// Overflow ratio
	if v := parseFloat("x-codex-primary-over-secondary-limit-percent"); v != nil {
		snapshot.PrimaryOverSecondaryPercent = v
		hasData = true
	}

	if !hasData {
		return nil
	}

	snapshot.UpdatedAt = time.Now().Format(time.RFC3339)
	return snapshot
}

func codexSnapshotBaseTime(snapshot *OpenAICodexUsageSnapshot, fallback time.Time) time.Time {
	if snapshot == nil {
		return fallback
	}
	if snapshot.UpdatedAt == "" {
		return fallback
	}
	base, err := time.Parse(time.RFC3339, snapshot.UpdatedAt)
	if err != nil {
		return fallback
	}
	return base
}

func codexResetAtRFC3339(base time.Time, resetAfterSeconds *int) *string {
	if resetAfterSeconds == nil {
		return nil
	}
	sec := *resetAfterSeconds
	if sec < 0 {
		sec = 0
	}
	resetAt := base.Add(time.Duration(sec) * time.Second).Format(time.RFC3339)
	return &resetAt
}

func buildCodexUsageExtraUpdates(snapshot *OpenAICodexUsageSnapshot, fallbackNow time.Time) map[string]any {
	if snapshot == nil {
		return nil
	}

	baseTime := codexSnapshotBaseTime(snapshot, fallbackNow)
	updates := make(map[string]any)

	// 保存原始 primary/secondary 字段，便于排查问题
	if snapshot.PrimaryUsedPercent != nil {
		updates["codex_primary_used_percent"] = *snapshot.PrimaryUsedPercent
	}
	if snapshot.PrimaryResetAfterSeconds != nil {
		updates["codex_primary_reset_after_seconds"] = *snapshot.PrimaryResetAfterSeconds
	}
	if snapshot.PrimaryWindowMinutes != nil {
		updates["codex_primary_window_minutes"] = *snapshot.PrimaryWindowMinutes
	}
	if snapshot.SecondaryUsedPercent != nil {
		updates["codex_secondary_used_percent"] = *snapshot.SecondaryUsedPercent
	}
	if snapshot.SecondaryResetAfterSeconds != nil {
		updates["codex_secondary_reset_after_seconds"] = *snapshot.SecondaryResetAfterSeconds
	}
	if snapshot.SecondaryWindowMinutes != nil {
		updates["codex_secondary_window_minutes"] = *snapshot.SecondaryWindowMinutes
	}
	if snapshot.PrimaryOverSecondaryPercent != nil {
		updates["codex_primary_over_secondary_percent"] = *snapshot.PrimaryOverSecondaryPercent
	}
	updates["codex_usage_updated_at"] = baseTime.Format(time.RFC3339)

	// 归一化到 5h/7d 规范字段
	if normalized := snapshot.Normalize(); normalized != nil {
		if normalized.Used5hPercent != nil {
			updates["codex_5h_used_percent"] = *normalized.Used5hPercent
		}
		if normalized.Reset5hSeconds != nil {
			updates["codex_5h_reset_after_seconds"] = *normalized.Reset5hSeconds
		}
		if normalized.Window5hMinutes != nil {
			updates["codex_5h_window_minutes"] = *normalized.Window5hMinutes
		}
		if normalized.Used7dPercent != nil {
			updates["codex_7d_used_percent"] = *normalized.Used7dPercent
		}
		if normalized.Reset7dSeconds != nil {
			updates["codex_7d_reset_after_seconds"] = *normalized.Reset7dSeconds
		}
		if normalized.Window7dMinutes != nil {
			updates["codex_7d_window_minutes"] = *normalized.Window7dMinutes
		}
		if reset5hAt := codexResetAtRFC3339(baseTime, normalized.Reset5hSeconds); reset5hAt != nil {
			updates["codex_5h_reset_at"] = *reset5hAt
		}
		if reset7dAt := codexResetAtRFC3339(baseTime, normalized.Reset7dSeconds); reset7dAt != nil {
			updates["codex_7d_reset_at"] = *reset7dAt
		}
	}

	return updates
}

// updateCodexUsageSnapshot saves the Codex usage snapshot to account's Extra field
// updateCodexUsageSnapshot 把 /responses 的 x-codex-* 全局头快照写入账号 codex_* Extra。
// ⚠️ 调用方必须排除 spark 影子账号(account.IsShadow()):影子的 codex_* 仅由 QueryUsage
// (/wham/usage bengalfox 道)更新,不能被全局头口径污染(外审第7轮 P1)。本函数仅持 accountID,
// 无法在此自检影子,故守卫前置到各调用点。
func (s *OpenAIGatewayService) updateCodexUsageSnapshot(ctx context.Context, accountID int64, snapshot *OpenAICodexUsageSnapshot) {
	if snapshot == nil {
		return
	}
	if s == nil || s.accountRepo == nil {
		return
	}

	now := time.Now()
	updates := buildCodexUsageExtraUpdates(snapshot, now)
	if len(updates) == 0 {
		return
	}
	if !s.getCodexSnapshotThrottle().Allow(accountID, now) {
		return
	}
	RunBackgroundTask("service/openai_gateway_usage.go:updateCodexUsageSnapshot", BackgroundCall0(func() {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.accountRepo.UpdateExtra(updateCtx, accountID, updates)
	}))
}

func (s *OpenAIGatewayService) UpdateCodexUsageSnapshotFromHeaders(ctx context.Context, accountID int64, headers http.Header) {
	if accountID <= 0 || headers == nil {
		return
	}
	if snapshot := ParseCodexRateLimitHeaders(headers); snapshot != nil {
		s.updateCodexUsageSnapshot(ctx, accountID, snapshot)
	}
}
