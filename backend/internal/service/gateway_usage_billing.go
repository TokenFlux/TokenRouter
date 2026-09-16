package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

func (s *GatewayService) getUserGroupRateMultiplier(ctx context.Context, userID, groupID int64, groupDefaultMultiplier float64) float64 {
	if s == nil {
		return groupDefaultMultiplier
	}
	resolver := s.userGroupRateResolver
	if resolver == nil {
		resolver = newUserGroupRateResolver(
			s.userGroupRateRepo,
			s.userGroupRateCache,
			resolveUserGroupRateCacheTTL(s.cfg),
			&s.userGroupRateSF,
			"service.gateway",
		)
	}
	return resolver.Resolve(ctx, userID, groupID, groupDefaultMultiplier)
}

// RecordUsageInput 记录使用量的输入参数。
// 异步 worker 只接收计费所需快照，不能持有 ParsedRequest/RequestBodyRef 这类大请求体引用。
type RecordUsageInput struct {
	Result             *ForwardResult
	APIKey             *APIKey
	User               *User
	Account            *Account
	Subscription       *UserSubscription  // 可选：订阅信息
	InboundEndpoint    string             // 入站端点（客户端请求路径）
	UpstreamEndpoint   string             // 上游端点（标准化后的上游路径）
	UserAgent          string             // 请求的 User-Agent
	IPAddress          string             // 请求的客户端 IP 地址
	ClientSessionID    string             // 客户端显式会话标识（session_id / X-Session-Id 等请求头），仅用于用量行会话关联
	RequestPayloadHash string             // 请求体语义哈希，用于降低 request_id 误复用时的静默误去重风险
	RequestBody        []byte             // 原始请求体，用于解析客户端请求的计费推理档位
	ForceCacheBilling  bool               // 强制缓存计费：将 input_tokens 转为 cache_read 计费（用于粘性会话切换）
	APIKeyService      APIKeyQuotaUpdater // 可选：用于更新API Key配额
	QuotaPlatform      string             // user×platform 配额计量平台：handler 在请求 ctx 内经 QuotaPlatform() 算定后传入（后扣运行在 worker 池 background ctx 上，取不到 ForcePlatform）

	ChannelUsageFields // 渠道映射信息（由 handler 在 Forward 前解析）
}

// APIKeyQuotaUpdater defines the interface for updating API Key quota and rate limit usage
type APIKeyQuotaUpdater interface {
	UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error
	UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error
}

type usageLogBestEffortWriter interface {
	CreateBestEffort(ctx context.Context, log *UsageLog) error
}

// PlatformFromAPIKey 从 APIKey 关联的 Group 推导 platform 名称。
// apiKey 为 nil 或 Group 信息缺失时返回空串（调用方据此 short-circuit quota 累加）。
// 导出供 handler 层调用。
func PlatformFromAPIKey(apiKey *APIKey) string {
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}

// QuotaPlatform 返回 user×platform 配额计量使用的平台标识。
// 强制平台路由（如 /antigravity）优先按 ctx 中的 ForcePlatform 计量，否则回退到
// APIKey 关联 Group 的平台。
//
// 注意：必须用带 ForcePlatform 的请求 context 调用（如 handler 的 c.Request.Context()）。
// 后扣运行在 worker 池的 background ctx 上没有 ForcePlatform，因此后扣平台由 handler
// 预先算定、经 RecordUsageInput.QuotaPlatform 传入，不要在后扣链路用 worker ctx 调用本函数。
func QuotaPlatform(ctx context.Context, apiKey *APIKey) string {
	if fp, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && fp != "" {
		return fp
	}
	return PlatformFromAPIKey(apiKey)
}

func resolveUsageBillingRequestID(ctx context.Context, upstreamRequestID string) string {
	return completion.ResolveRequestID(completionRequestIdentity(ctx, upstreamRequestID, ""), generateRequestID)
}

// StableGrokAudioBillingRequestID 为单次 TTS/STT HTTP 调用生成持久用量去重键，优先沿用上游请求 ID。
func StableGrokAudioBillingRequestID(upstreamRequestID string) string {
	return completion.StableAudioRequestID(upstreamRequestID, generateRequestID)
}

// StableGrokRealtimeBillingRequestID 为单个 Realtime WebSocket 会话生成持久用量去重键。
func StableGrokRealtimeBillingRequestID(sessionID string) string {
	return completion.StableRealtimeRequestID(sessionID, generateRequestID)
}

func resolveUsageBillingPayloadFingerprint(ctx context.Context, requestPayloadHash string) string {
	return completion.PayloadFingerprint(completionRequestIdentity(ctx, "", requestPayloadHash))
}

// settlementEffects 只投影旧请求完成入口需要的资金端口，不持有额外缓存或队列。
func settlementEffects(deps *billingDeps) billing.SettlementEffects {
	var cache *billing.Eligibility
	if deps.billingCacheService != nil {
		cache = deps.billingCacheService.Eligibility
	}
	return billing.SettlementEffects{Cache: cache, Quotas: deps.userPlatformQuotaRepo, FlusherEnabled: deps.cfg != nil && deps.cfg.Database.UserPlatformQuotaFlusherEnabled, Background: RunBackgroundTask, Observe: logger.LegacyPrintf, BalanceWarning: func(id int64, balance float64, err error) {
		slog.Warn("invalidate balance cache after exhausted deduction failed", "user_id", id, "new_balance", balance, "error", err)
	}}
}

func detachStreamUpstreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	if !stream {
		return ctx, func() {}
	}
	return context.WithoutCancel(ctx), func() {}
}

func detachUpstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	return context.WithoutCancel(ctx), func() {}
}

// billingDeps 扣费逻辑依赖的服务（由各 gateway service 提供）
type billingDeps struct {
	accountRepo           AccountRepository
	userRepo              UserRepository
	userSubRepo           UserSubscriptionRepository
	billingCacheService   *BillingCacheService
	deferredService       *DeferredService
	balanceNotifyService  *BalanceNotifyService
	userPlatformQuotaRepo UserPlatformQuotaRepository
	cfg                   *config.Config
}

func (s *GatewayService) billingDeps() *billingDeps {
	return &billingDeps{
		accountRepo:           s.accountRepo,
		userRepo:              s.userRepo,
		userSubRepo:           s.userSubRepo,
		billingCacheService:   s.billingCacheService,
		deferredService:       s.deferredService,
		balanceNotifyService:  s.balanceNotifyService,
		userPlatformQuotaRepo: s.userPlatformQuotaRepo,
		cfg:                   s.cfg,
	}
}

func writeUsageLogBestEffort(ctx context.Context, repo UsageLogRepository, usageLog *UsageLog, logKey string) {
	completion.NewRecorder(completion.Dependencies{Logs: completionWriter(repo), Observe: completionObserver}, completion.RecorderOptions{}).WriteUsage(ctx, UsageLogView(usageLog), logKey)
}

// recordUsageOpts 保存请求级计费时刻。长上下文规则已改由模型目录驱动。
type recordUsageOpts struct {
	// PricingAt 固定本次请求的计费时刻，供渠道分时倍率和高峰倍率共用。
	PricingAt time.Time
}

// RecordUsage 记录使用量并扣费（或更新订阅用量）
func (s *GatewayService) RecordUsage(ctx context.Context, input *RecordUsageInput) error {
	return s.recordUsageCore(ctx, &recordUsageCoreInput{
		Result:             input.Result,
		APIKey:             input.APIKey,
		User:               input.User,
		Account:            input.Account,
		Subscription:       input.Subscription,
		InboundEndpoint:    input.InboundEndpoint,
		UpstreamEndpoint:   input.UpstreamEndpoint,
		UserAgent:          input.UserAgent,
		IPAddress:          input.IPAddress,
		ClientSessionID:    input.ClientSessionID,
		RequestPayloadHash: input.RequestPayloadHash,
		RequestBody:        input.RequestBody,
		ForceCacheBilling:  input.ForceCacheBilling,
		APIKeyService:      input.APIKeyService,
		QuotaPlatform:      input.QuotaPlatform,
		ChannelUsageFields: input.ChannelUsageFields,
	}, &recordUsageOpts{})
}

// RecordUsageLongContextInput 是历史兼容结构。长上下文字段已不再直接控制计费，
// 新代码应使用 RecordUsageInput。
type RecordUsageLongContextInput struct {
	Result                *ForwardResult
	APIKey                *APIKey
	User                  *User
	Account               *Account
	Subscription          *UserSubscription  // 可选：订阅信息
	InboundEndpoint       string             // 入站端点（客户端请求路径）
	UpstreamEndpoint      string             // 上游端点（标准化后的上游路径）
	UserAgent             string             // 请求的 User-Agent
	IPAddress             string             // 请求的客户端 IP 地址
	ClientSessionID       string             // 客户端显式会话标识（session_id / X-Session-Id 等请求头），仅用于用量行会话关联
	RequestPayloadHash    string             // 请求体语义哈希，用于降低 request_id 误复用时的静默误去重风险
	RequestBody           []byte             // 原始请求体，用于解析客户端请求的计费推理档位
	LongContextThreshold  int                // 已废弃：保留字段以兼容旧调用方
	LongContextMultiplier float64            // 已废弃：保留字段以兼容旧调用方
	ForceCacheBilling     bool               // 强制缓存计费：将 input_tokens 转为 cache_read 计费（用于粘性会话切换）
	APIKeyService         APIKeyQuotaUpdater // API Key 配额服务（可选）
	QuotaPlatform         string             // user×platform 配额计量平台：handler 在请求 ctx 内经 QuotaPlatform() 算定后传入（后扣运行在 worker 池 background ctx 上，取不到 ForcePlatform）

	ChannelUsageFields // 渠道映射信息（由 handler 在 Forward 前解析）
}

// RecordUsageWithLongContext 兼容旧入口，实际委托统一目录定价路径。
func (s *GatewayService) RecordUsageWithLongContext(ctx context.Context, input *RecordUsageLongContextInput) error {
	return s.recordUsageCore(ctx, &recordUsageCoreInput{
		Result:             input.Result,
		APIKey:             input.APIKey,
		User:               input.User,
		Account:            input.Account,
		Subscription:       input.Subscription,
		InboundEndpoint:    input.InboundEndpoint,
		UpstreamEndpoint:   input.UpstreamEndpoint,
		UserAgent:          input.UserAgent,
		IPAddress:          input.IPAddress,
		ClientSessionID:    input.ClientSessionID,
		RequestPayloadHash: input.RequestPayloadHash,
		RequestBody:        input.RequestBody,
		ForceCacheBilling:  input.ForceCacheBilling,
		APIKeyService:      input.APIKeyService,
		QuotaPlatform:      input.QuotaPlatform,
		ChannelUsageFields: input.ChannelUsageFields,
	}, &recordUsageOpts{})
}

// recordUsageCoreInput 是 recordUsageCore 的公共输入字段，从两种输入结构体中提取。
type recordUsageCoreInput struct {
	Result             *ForwardResult
	APIKey             *APIKey
	User               *User
	Account            *Account
	Subscription       *UserSubscription
	InboundEndpoint    string
	UpstreamEndpoint   string
	UserAgent          string
	IPAddress          string
	ClientSessionID    string
	RequestPayloadHash string
	RequestBody        []byte
	ForceCacheBilling  bool
	APIKeyService      APIKeyQuotaUpdater
	QuotaPlatform      string
	ChannelUsageFields
}

// recordUsageCore 是 RecordUsage 和历史兼容入口的统一实现。
// @project-doc docs/domains/routing_and_billing.md#usage_settlement
func (s *GatewayService) recordUsageCore(ctx context.Context, input *recordUsageCoreInput, opts *recordUsageOpts) error {
	in := CompletionForwardInput(ctx, &RecordUsageInput{Result: input.Result, APIKey: input.APIKey, User: input.User, Account: input.Account, Subscription: input.Subscription, InboundEndpoint: input.InboundEndpoint, UpstreamEndpoint: input.UpstreamEndpoint, UserAgent: input.UserAgent, IPAddress: input.IPAddress, ClientSessionID: input.ClientSessionID, RequestPayloadHash: input.RequestPayloadHash, RequestBody: input.RequestBody, ForceCacheBilling: input.ForceCacheBilling, APIKeyService: input.APIKeyService, QuotaPlatform: input.QuotaPlatform, ChannelUsageFields: input.ChannelUsageFields})
	return s.CompletionRecorder(input.APIKeyService).Record(ctx, in, false)
}

// calculateTokenCost 计算 Token 计费：根据 opts 决定走普通/长上下文/渠道统一计费。
func (s *GatewayService) calculateTokenCost(
	ctx context.Context,
	result *ForwardResult,
	apiKey *APIKey,
	account *Account,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	channelMappedModel string,
	multiplier float64,
	opts *recordUsageOpts,
) *CostBreakdown {
	return s.CompletionRecorder(nil).CalculateTokenCost(ctx, completionForwardResult(result, account), completionKey(apiKey), completionAccount(account), billingModel, requestedModel, billingModelSource, channelMappedModel, multiplier, completionPricingOptions(opts))
}

// claudeUsageServiceTier 将 Claude usage.speed 复用为内部统一的 Fast 计费层级。
func claudeUsageServiceTier(speed string) string {
	return completion.ClaudeServiceTier(speed)
}
