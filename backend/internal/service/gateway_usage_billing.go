package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
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

type apiKeyAuthCacheInvalidator interface {
	InvalidateAuthCacheByKey(ctx context.Context, key string)
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
	// 强制持久结算 ID 必须优先于客户端或本地上下文 ID，避免独立搜索或异步视频因复用客户端 ID 被合并。
	if requestID := strings.TrimSpace(upstreamRequestID); requestID != "" {
		if isForcedUsageBillingRequestID(requestID) {
			return requestID
		}
	}
	if ctx != nil {
		if clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string); strings.TrimSpace(clientRequestID) != "" {
			return "client:" + strings.TrimSpace(clientRequestID)
		}
		if requestID, _ := ctx.Value(ctxkey.RequestID).(string); strings.TrimSpace(requestID) != "" {
			return "local:" + strings.TrimSpace(requestID)
		}
	}
	if requestID := strings.TrimSpace(upstreamRequestID); requestID != "" {
		return requestID
	}
	return "generated:" + generateRequestID()
}

func isForcedUsageBillingRequestID(requestID string) bool {
	id := strings.TrimSpace(requestID)
	return strings.HasPrefix(id, "web_search:") ||
		strings.HasPrefix(id, "grok-video:") ||
		strings.HasPrefix(id, "grok_audio:") ||
		strings.HasPrefix(id, "grok_realtime:")
}

// StableGrokAudioBillingRequestID 为单次 TTS/STT HTTP 调用生成持久用量去重键，优先沿用上游请求 ID。
func StableGrokAudioBillingRequestID(upstreamRequestID string) string {
	upstreamRequestID = strings.TrimSpace(upstreamRequestID)
	if strings.HasPrefix(upstreamRequestID, "grok_audio:") {
		return upstreamRequestID
	}
	if upstreamRequestID == "" {
		upstreamRequestID = generateRequestID()
	}
	return "grok_audio:" + upstreamRequestID
}

// StableGrokRealtimeBillingRequestID 为单个 Realtime WebSocket 会话生成持久用量去重键。
func StableGrokRealtimeBillingRequestID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if strings.HasPrefix(sessionID, "grok_realtime:") {
		return sessionID
	}
	if sessionID == "" {
		sessionID = generateRequestID()
	}
	return "grok_realtime:" + sessionID
}

func resolveUsageBillingPayloadFingerprint(ctx context.Context, requestPayloadHash string) string {
	if payloadHash := strings.TrimSpace(requestPayloadHash); payloadHash != "" {
		return payloadHash
	}
	if ctx != nil {
		if clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string); strings.TrimSpace(clientRequestID) != "" {
			return "client:" + strings.TrimSpace(clientRequestID)
		}
		if requestID, _ := ctx.Value(ctxkey.RequestID).(string); strings.TrimSpace(requestID) != "" {
			return "local:" + strings.TrimSpace(requestID)
		}
	}
	return ""
}

func buildUsageBillingCommand(requestID string, usageLog *UsageLog, p *usageBillingParams) *UsageBillingCommand {
	if p == nil || p.Cost == nil || p.APIKey == nil || p.User == nil || p.Account == nil {
		return nil
	}

	cmd := &UsageBillingCommand{
		RequestID:          requestID,
		APIKeyID:           p.APIKey.ID,
		APIKeyBillingMode:  APIKeyEffectiveBillingMode(p.APIKey),
		UserID:             p.User.ID,
		ActorUserID:        p.User.ID,
		AccountID:          p.Account.ID,
		AccountType:        p.Account.Type,
		RequestPayloadHash: strings.TrimSpace(p.RequestPayloadHash),
	}
	if p.APIKey.PreferredSubscriptionID != nil {
		preferredSubscriptionID := *p.APIKey.PreferredSubscriptionID
		cmd.PreferredSubscriptionID = &preferredSubscriptionID
	}
	if p.APIKey.ActorUser != nil {
		cmd.ActorUserID = p.APIKey.ActorUser.ID
	}
	if p.APIKey.TeamID != nil {
		teamID := *p.APIKey.TeamID
		cmd.TeamID = &teamID
	}
	if p.APIKey.GroupID != nil && *p.APIKey.GroupID > 0 {
		groupID := *p.APIKey.GroupID
		cmd.GroupID = &groupID
	}
	if usageLog != nil {
		cmd.Model = usageLog.Model
		cmd.BillingType = usageLog.BillingType
		cmd.InputTokens = usageLog.InputTokens
		cmd.OutputTokens = usageLog.OutputTokens
		cmd.CacheCreationTokens = usageLog.CacheCreationTokens
		cmd.CacheReadTokens = usageLog.CacheReadTokens
		cmd.ImageCount = usageLog.ImageCount
		if usageLog.ServiceTier != nil {
			cmd.ServiceTier = *usageLog.ServiceTier
		}
		if usageLog.ReasoningEffort != nil {
			cmd.ReasoningEffort = *usageLog.ReasoningEffort
		}
	}

	if p.Cost.ActualCost > 0 {
		cmd.BillableAmountUSD = p.Cost.ActualCost
	}
	baseAmount := p.Cost.TotalCost
	if p.BillingBaseAmountUSD != nil {
		baseAmount = *p.BillingBaseAmountUSD
	}
	if baseAmount > 0 {
		cmd.BaseAmountUSD = baseAmount
		applyUsageBillingRateMultipliers(cmd, p)
	}
	if p.shouldDeductAPIKeyQuota() {
		cmd.APIKeyQuotaCost = p.Cost.ActualCost
	}
	if p.shouldUpdateRateLimits() {
		cmd.APIKeyRateLimitCost = p.Cost.ActualCost
	}
	accountQuotaCost := resolveUsageBillingAccountQuotaCost(usageLog, p)
	if p.shouldUpdateAccountQuota(accountQuotaCost) {
		cmd.AccountQuotaCost = accountQuotaCost
	}

	cmd.Normalize()
	return cmd
}

// resolveUsageBillingAccountQuotaCost 让账号额度与账号统计使用同一成本口径。
// AccountStatsCost 的显式零值必须保留，只有 nil 才回退用户计费基础成本。
func resolveUsageBillingAccountQuotaCost(usageLog *UsageLog, p *usageBillingParams) float64 {
	if p == nil || p.Cost == nil {
		return 0
	}
	baseCost := p.Cost.TotalCost
	if usageLog != nil && usageLog.AccountStatsCost != nil {
		baseCost = *usageLog.AccountStatsCost
	}
	if baseCost <= 0 || p.AccountRateMultiplier <= 0 {
		return 0
	}
	return baseCost * p.AccountRateMultiplier
}

func applyUsageBillingRateMultipliers(cmd *UsageBillingCommand, p *usageBillingParams) {
	if cmd == nil || p == nil || p.Cost == nil {
		return
	}
	baseAmount := p.Cost.TotalCost
	if p.BillingBaseAmountUSD != nil {
		baseAmount = *p.BillingBaseAmountUSD
	}
	if baseAmount <= 0 {
		return
	}

	effectiveRate := p.Cost.ActualCost / baseAmount

	cmd.SubscriptionRateMultiplier = usageBillingRateOrFallback(p.SubscriptionRateMultiplier, effectiveRate)
	cmd.SubscriptionRateMultiplierScale = p.SubscriptionRateMultiplierScale
	if cmd.SubscriptionRateMultiplierScale <= 0 {
		cmd.SubscriptionRateMultiplierScale = 1
	}
	cmd.BalanceRateMultiplier = usageBillingRateOrFallback(p.BalanceRateMultiplier, effectiveRate)
}

// usageBillingRatesForMode 按最终计费模式决定资金分配倍率；订阅与余额各自保留基础倍率。
// 按张、按秒和按次费用不含高峰因子，不能用首个订阅的实际倍率覆盖余额按量倍率。
func usageBillingRatesForMode(apiKey *APIKey, cost *CostBreakdown, subscriptionBase, balanceBase float64, pricingAt time.Time) (subscriptionRate, balanceRate, scale float64) {
	scale = 1
	if cost == nil || cost.BillingMode == "" || cost.BillingMode == string(BillingModeToken) {
		if apiKey != nil && apiKey.Group != nil {
			scale = apiKey.Group.PeakMultiplierAt(pricingAt)
		}
	}
	return subscriptionBase * scale, balanceBase * scale, scale
}

func usageBillingRateOrFallback(value, fallback float64) float64 {
	if value > 0 || fallback == 0 {
		return value
	}
	return fallback
}

func applyUsageBilling(ctx context.Context, requestID string, usageLog *UsageLog, p *usageBillingParams, deps *billingDeps, repo UsageBillingRepository) (bool, error) {
	if p == nil || deps == nil {
		return false, nil
	}

	cmd := buildUsageBillingCommand(requestID, usageLog, p)
	if repo == nil {
		return false, fmt.Errorf("usage billing repository is required")
	}
	if cmd == nil || cmd.RequestID == "" {
		return false, fmt.Errorf("usage billing command is invalid")
	}

	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()

	result, err := repo.Apply(billingCtx, cmd)
	if err != nil {
		return false, err
	}

	if result == nil || !result.Applied {
		deps.deferredService.ScheduleLastUsedUpdate(p.Account.ID)
		return false, nil
	}

	applyUsageBillingResultToUsageLog(usageLog, result)
	if result.APIKeyQuotaExhausted {
		if invalidator, ok := p.APIKeyService.(apiKeyAuthCacheInvalidator); ok && p.APIKey != nil && p.APIKey.Key != "" {
			invalidator.InvalidateAuthCacheByKey(billingCtx, p.APIKey.Key)
		}
	}

	finalizeUsageBilling(p, deps, result)
	return true, nil
}

func syncBalanceCacheAfterDeduction(ctx context.Context, p *usageBillingParams, deps *billingDeps, result *UsageBillingApplyResult) {
	if p == nil || p.User == nil || deps == nil || deps.billingCacheService == nil || result == nil || result.BalanceAmountUSD <= 0 {
		return
	}
	if result.NewBalance != nil && deps.billingCacheService.balanceBelowEligibilityThreshold(*result.NewBalance) {
		if err := deps.billingCacheService.InvalidateUserBalance(ctx, p.User.ID); err != nil {
			slog.Warn("invalidate balance cache after exhausted deduction failed",
				"user_id", p.User.ID,
				"new_balance", *result.NewBalance,
				"error", err,
			)
		}
		return
	}
	deps.billingCacheService.QueueDeductBalance(p.User.ID, result.BalanceAmountUSD)
}

// notifyBalanceLow 在扣费后发送余额不足通知。
// 当 DB 事务返回 result.NewBalance 时直接还原 oldBalance，避免读取过期 Redis 和并发扣费竞态。
func notifyBalanceLow(p *usageBillingParams, deps *billingDeps, result *UsageBillingApplyResult) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in notifyBalanceLow", "recover", r)
		}
	}()
	balanceAmount := 0.0
	if result != nil {
		balanceAmount = result.BalanceAmountUSD
	}
	if balanceAmount <= 0 || p.User == nil || deps.balanceNotifyService == nil {
		slog.Debug("notifyBalanceLow: skipped",
			"balance_amount", balanceAmount,
			"user_nil", p.User == nil,
			"service_nil", deps.balanceNotifyService == nil,
		)
		return
	}

	oldBalance := resolveOldBalance(p, result)
	slog.Debug("notifyBalanceLow: calling CheckBalanceAfterDeduction",
		"user_id", p.User.ID,
		"old_balance", oldBalance,
		"cost", balanceAmount,
		"notify_enabled", p.User.BalanceNotifyEnabled,
		"threshold", p.User.BalanceNotifyThreshold,
		"result_has_new_balance", result != nil && result.NewBalance != nil,
	)
	deps.balanceNotifyService.CheckBalanceAfterDeduction(context.Background(), p.User, oldBalance, balanceAmount)
}

// resolveOldBalance returns the pre-deduction balance.
// Prefers the DB transaction result (newBalance + cost) over snapshot.
func resolveOldBalance(p *usageBillingParams, result *UsageBillingApplyResult) float64 {
	if result != nil && result.NewBalance != nil {
		return *result.NewBalance + result.BalanceAmountUSD
	}
	// Legacy fallback: snapshot balance from request context
	return p.User.Balance
}

// notifyAccountQuota sends account quota threshold notification after increment.
// When result.QuotaState is available (from DB transaction RETURNING), it is passed directly
// to avoid a separate DB read that may see stale or concurrently-modified data.
func notifyAccountQuota(p *usageBillingParams, deps *billingDeps, result *UsageBillingApplyResult) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in notifyAccountQuota", "recover", r)
		}
	}()
	if p.Cost.TotalCost <= 0 || p.Account == nil || !p.Account.IsAPIKeyOrBedrock() || deps.balanceNotifyService == nil {
		slog.Debug("notifyAccountQuota: skipped",
			"total_cost", p.Cost.TotalCost,
			"account_nil", p.Account == nil,
			"is_apikey_or_bedrock", p.Account != nil && p.Account.IsAPIKeyOrBedrock(),
			"service_nil", deps.balanceNotifyService == nil,
		)
		return
	}
	accountCost := p.Cost.TotalCost * p.AccountRateMultiplier
	var quotaState *AccountQuotaState
	if result != nil {
		quotaState = result.QuotaState
	}
	slog.Debug("notifyAccountQuota: calling CheckAccountQuotaAfterIncrement",
		"account_id", p.Account.ID,
		"account_cost", accountCost,
		"has_quota_state", quotaState != nil,
	)
	deps.balanceNotifyService.CheckAccountQuotaAfterIncrement(context.Background(), p.Account, accountCost, quotaState)
}

func detachedBillingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, usageBillingTimeout)
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
	if repo == nil || usageLog == nil {
		return
	}
	usageCtx, cancel := detachedBillingContext(ctx)
	defer cancel()

	if writer, ok := repo.(usageLogBestEffortWriter); ok {
		if err := writer.CreateBestEffort(usageCtx, usageLog); err != nil {
			logger.LegacyPrintf(logKey, "Create usage log failed: %v", err)
			// 已结算或待对账的用量事实都必须尽力落库：dropped（批处理队列超时）同样走同步兜底，
			// 否则会出现缺少 usage_log 的对账缺口；结算失败记录以 ActualCost=0 标识未实际扣费。
			// 重复写入由 usage_logs 的 ON CONFLICT (request_id, api_key_id) DO NOTHING 防护。
			fallbackCtx := usageCtx
			if usageCtx.Err() != nil {
				// usageCtx 已耗尽（best-effort 入队阻塞到期限）：换新的 detached 窗口，避免兜底必然失败。
				var fallbackCancel context.CancelFunc
				fallbackCtx, fallbackCancel = detachedBillingContext(context.Background())
				defer fallbackCancel()
			}
			if _, syncErr := repo.Create(fallbackCtx, usageLog); syncErr != nil {
				logger.LegacyPrintf(logKey, "Create usage log sync fallback failed: %v", syncErr)
			}
		}
		return
	}

	if _, err := repo.Create(usageCtx, usageLog); err != nil {
		logger.LegacyPrintf(logKey, "Create usage log failed: %v", err)
	}
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
	if opts == nil {
		opts = &recordUsageOpts{}
	}
	result := input.Result
	apiKey := input.APIKey
	user := input.User
	account := input.Account
	subscription := input.Subscription
	ApplyForwardImageBillingResolution(result)
	logServiceTierBillingDowngrade("service.gateway", account, result.RequestID, ApplyForwardServiceTierBillingResolution(result))

	// 强制缓存计费：将 input_tokens 转为 cache_read_input_tokens
	// 用于粘性会话切换时的特殊计费处理
	if input.ForceCacheBilling && result.Usage.InputTokens > 0 {
		logger.LegacyPrintf("service.gateway", "force_cache_billing: %d input_tokens → cache_read_input_tokens (account=%d)",
			result.Usage.InputTokens, account.ID)
		result.Usage.CacheReadInputTokens += result.Usage.InputTokens
		result.Usage.InputTokens = 0
	}

	// Cache TTL Override: 确保计费时 token 分类与账号设置一致。
	// 账号级设置优先；全局 1h 请求注入开启时，默认把 usage 计费归回 5m。
	cacheTTLOverridden := false
	if overrideTarget, ok := s.resolveCacheTTLUsageOverrideTarget(ctx, account); ok {
		applyCacheTTLOverride(&result.Usage, overrideTarget)
		cacheTTLOverridden = (result.Usage.CacheCreation5mTokens + result.Usage.CacheCreation1hTokens) > 0
	}

	// 获取费率倍数（优先级：用户专属 > 分组默认 > 系统默认）
	multiplier := 1.0
	if s.cfg != nil {
		multiplier = s.cfg.Default.RateMultiplier
	}
	subscriptionMultiplier := multiplier
	balanceMultiplier := multiplier
	subscription = resolveUsageSubscriptionForAPIKey(ctx, apiKey, subscription, s.userSubRepo, usageSubscriptionResolverFrom(s.usageBillingRepo), user.ID, apiKey.GroupID)
	if apiKey.GroupID != nil && apiKey.Group != nil {
		groupDefault := apiKey.Group.RateMultiplier
		subscriptionMultiplier = groupDefault
		balanceMultiplier = groupDefault
		if subscription == nil {
			balanceMultiplier = s.getUserGroupRateMultiplier(ctx, user.ID, *apiKey.GroupID, groupDefault)
		}
	}
	if apiKey.GroupID != nil && apiKey.Group != nil && subscription == nil {
		multiplier = balanceMultiplier
	} else {
		multiplier = resolveUsageRateMultiplier(ctx, user.ID, apiKey.GroupID, apiKey.Group, multiplier, subscription, nil)
	}
	// token 倍率叠加高峰因子（token 计费含图片 token，图片按次倍率不受影响）。高峰因子按请求时刻现算，
	// 不并入上面的 getUserGroupRateMultiplier，以免污染 user:group 倍率缓存。
	rateNow := timezone.Now()
	if s.usageBillingNow != nil {
		rateNow = s.usageBillingNow()
	}
	opts.PricingAt = rateNow
	multiplier, imageMultiplier := computePeakAwareMultipliers(apiKey, multiplier, rateNow)

	// 确定计费模型
	billingModel := forwardResultBillingModel(result.Model, result.UpstreamModel)
	if input.BillingModelSource == BillingModelSourceUpstream && result.UpstreamModel != "" {
		billingModel = result.UpstreamModel
	}
	if input.BillingModelSource == BillingModelSourceChannelMapped && input.ChannelMappedModel != "" {
		billingModel = input.ChannelMappedModel
	}
	if input.BillingModelSource == BillingModelSourceRequested && input.OriginalModel != "" {
		billingModel = input.OriginalModel
	}

	// 确定 RequestedModel（渠道映射前的原始模型）
	requestedModel := result.Model
	if input.OriginalModel != "" {
		requestedModel = input.OriginalModel
	}

	// 计算费用
	cost := s.calculateRecordUsageCost(ctx, result, apiKey, account, billingModel, requestedModel, input.BillingModelSource, input.ChannelMappedModel, multiplier, imageMultiplier, opts)

	// 预填 billing_type 仅用于 simple mode / 持久化前对象，真实扣费结果会在统一扣费后回填。
	isSubscriptionBilling := subscription != nil
	billingType := BillingTypeBalance
	if isSubscriptionBilling {
		billingType = BillingTypeSubscription
	}

	// 创建使用日志
	accountRateMultiplier := account.BillingRateMultiplier()
	usageLog := s.buildRecordUsageLog(ctx, input, result, apiKey, user, account, subscription,
		requestedModel, multiplier, imageMultiplier, accountRateMultiplier, billingType, cacheTTLOverridden, cost, opts)

	// 计算账号统计定价费用（Qoder 会先按原始请求 alias、再按渠道 route key / 最终 upstream 匹配自定义规则）
	if apiKey.GroupID != nil {
		applyAccountStatsCost(ctx, usageLog, s.channelService, s.billingService,
			account.ID, *apiKey.GroupID, result.UpstreamModel, requestedModel, input.ChannelMappedModel,
			// Anthropic's input_tokens excludes cache_read and cache_creation (billed separately);
			// OpenAI gateway uses actualInputTokens which also excludes cache_read for the same reason.
			UsageTokens{
				InputTokens:         result.Usage.InputTokens,
				OutputTokens:        result.Usage.OutputTokens,
				CacheCreationTokens: result.Usage.CacheCreationInputTokens,
				CacheReadTokens:     result.Usage.CacheReadInputTokens,
				ImageOutputTokens:   result.Usage.ImageOutputTokens,
			},
			cost.TotalCost,
		)
	}

	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		writeUsageLogBestEffort(ctx, s.usageLogRepo, usageLog, "service.gateway")
		logger.LegacyPrintf("service.gateway", "[SIMPLE MODE] Usage recorded (not billed): user=%d, tokens=%d", usageLog.UserID, usageLog.TotalTokens())
		s.deferredService.ScheduleLastUsedUpdate(account.ID)
		return nil
	}

	// 配额平台由 handler 在请求 ctx 内经 QuotaPlatform() 算定并通过 input 传入；
	// 后扣运行在 worker 池的 background ctx 上，无法再从 ctx 取 ForcePlatform。
	// 缺省（未设置）时回退到分组平台，保持对其它调用方的兼容。
	quotaPlatform := input.QuotaPlatform
	if quotaPlatform == "" {
		quotaPlatform = PlatformFromAPIKey(apiKey)
	}
	subscriptionMultiplier, balanceMultiplier, subscriptionMultiplierScale := usageBillingRatesForMode(apiKey, cost, subscriptionMultiplier, balanceMultiplier, rateNow)
	requestID := usageLog.RequestID
	_, billingErr := applyUsageBilling(ctx, requestID, usageLog, &usageBillingParams{
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
	}, s.billingDeps(), s.usageBillingRepo)

	if billingErr != nil {
		// 结算事务失败时仍保留已计算的用量与成本明细；ActualCost 置零明确表示本次未成功结算。
		usageLog.ActualCost = 0
		writeUsageLogBestEffort(ctx, s.usageLogRepo, usageLog, "service.gateway")
		return billingErr
	}
	writeUsageLogBestEffort(ctx, s.usageLogRepo, usageLog, "service.gateway")
	return nil
}

// calculateRecordUsageCost 根据请求类型和选项计算费用。
func (s *GatewayService) calculateRecordUsageCost(
	ctx context.Context,
	result *ForwardResult,
	apiKey *APIKey,
	account *Account,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	channelMappedModel string,
	multiplier float64,
	imageMultiplier float64,
	opts *recordUsageOpts,
) *CostBreakdown {
	if opts == nil {
		opts = &recordUsageOpts{}
	}
	// 图片生成：渠道定价为令牌计费时走令牌路径，否则走图片计费
	if result.ImageCount > 0 {
		if resolved, pricingModel := s.resolveChannelPricingForUsage(ctx, billingModel, apiKey); resolved != nil && resolved.Mode == BillingModeToken {
			return s.calculateTokenCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, channelMappedModel, multiplier, opts)
		} else if resolved != nil {
			return s.calculateImageCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, channelMappedModel, pricingModel, resolved, imageMultiplier, opts.PricingAt)
		}
		return s.calculateImageCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, channelMappedModel, billingModel, nil, imageMultiplier, opts.PricingAt)
	}

	// 语音用量优先按分组模型的连续单位价格结算，未配置时沿用分组通用音频价。
	if result.AudioUsage != nil {
		resolved, pricingModel := s.resolveChannelPricingForUsage(
			ctx, billingModel, apiKey,
		)
		if resolved != nil && resolved.Mode == BillingModePerRequest {
			gid := apiKey.Group.ID
			cost, err := s.billingService.CalculateCostUnified(CostInput{
				Ctx:            ctx,
				Model:          pricingModel,
				GroupID:        &gid,
				Group:          apiKey.Group,
				UsageUnits:     result.AudioUsage.DurationOrUnits,
				SizeTier:       result.AudioUsage.Mode,
				RateMultiplier: multiplier,
				PricingAt:      opts.PricingAt,
				Resolver:       s.resolver,
				Resolved:       resolved,
			})
			if err == nil {
				return cost
			}
		}
		cfg := groupAudioPriceConfigFromAPIKey(apiKey)
		return s.billingService.CalculateAudioCost(result.AudioUsage.Mode, result.AudioUsage.DurationOrUnits, cfg, multiplier)
	}

	// Token 费用与搜索附加费分别计算，搜索不会替代模型本身的 token 费用。
	tokenCost := s.calculateTokenCost(ctx, result, apiKey, account, billingModel, requestedModel, billingModelSource, channelMappedModel, multiplier, opts)
	if result.SearchCount > 0 {
		price := groupSearchPricePer1kFromAPIKey(apiKey)
		if price != nil && *price == 0 {
			logger.LegacyPrintf("service.gateway", "[Billing] search_price_per_1k explicit 0; search free group_model=%s count=%d", billingModel, result.SearchCount)
		}
		searchCost := s.billingService.CalculateSearchCost(result.SearchCount, price, multiplier)
		if searchCost != nil && (searchCost.TotalCost > 0 || searchCost.ActualCost > 0) {
			if tokenCost == nil {
				return searchCost
			}
			tokenCost.TotalCost += searchCost.TotalCost
			tokenCost.ActualCost += searchCost.ActualCost
		}
	}
	return tokenCost
}

// resolveChannelPricing 返回分组或渠道配置参与解析的价格，纯倍率保留内置价格来源。
func (s *GatewayService) resolveChannelPricing(ctx context.Context, billingModel string, apiKey *APIKey) *ResolvedPricing {
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

// calculateImageCost 使用分组或渠道价卡，未配置时按内置图片单价计费。
func (s *GatewayService) calculateImageCost(
	ctx context.Context,
	result *ForwardResult,
	apiKey *APIKey,
	account *Account,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	channelMappedModel string,
	resolvedModel string,
	resolved *ResolvedPricing,
	multiplier float64,
	pricingAt time.Time,
) *CostBreakdown {
	sizeTier := NormalizeImageBillingTierOrDefault(result.ImageSize)
	if resolved == nil {
		resolved, resolvedModel = s.resolveChannelPricingForUsage(ctx, billingModel, apiKey)
	}
	if resolved != nil {
		tokens := UsageTokens{
			InputTokens:       result.Usage.InputTokens,
			OutputTokens:      result.Usage.OutputTokens,
			ImageOutputTokens: result.Usage.ImageOutputTokens,
		}
		gid := apiKey.Group.ID
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx:            ctx,
			Model:          resolvedModel,
			GroupID:        &gid,
			Group:          apiKey.Group,
			Tokens:         tokens,
			RequestCount:   result.ImageCount,
			SizeTier:       sizeTier,
			RateMultiplier: multiplier,
			PricingAt:      pricingAt,
			Resolver:       s.resolver,
			Resolved:       resolved,
		})
		if err != nil {
			logger.LegacyPrintf("service.gateway", "Calculate image token cost failed: %v", err)
			return &CostBreakdown{ActualCost: 0}
		}
		return cost
	}

	return s.billingService.CalculateImageCost(billingModel, sizeTier, result.ImageCount, multiplier)
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
	serviceTier := forwardResultServiceTier(result)
	tokens := UsageTokens{
		InputTokens:           result.Usage.InputTokens,
		OutputTokens:          result.Usage.OutputTokens,
		CacheCreationTokens:   result.Usage.CacheCreationInputTokens,
		CacheReadTokens:       result.Usage.CacheReadInputTokens,
		CacheCreation5mTokens: result.Usage.CacheCreation5mTokens,
		CacheCreation1hTokens: result.Usage.CacheCreation1hTokens,
		ImageOutputTokens:     result.Usage.ImageOutputTokens,
	}

	var cost *CostBreakdown
	var err error
	if opts == nil {
		opts = &recordUsageOpts{}
	}

	// 分组或渠道显式价格优先，并保留渠道选定的计费模型来源。
	if resolved, resolvedModel := s.resolveChannelPricingForUsage(ctx, billingModel, apiKey); resolved != nil {
		gid := apiKey.Group.ID
		cost, err = s.billingService.CalculateCostUnified(CostInput{
			Ctx:             ctx,
			Model:           resolvedModel,
			GroupID:         &gid,
			Group:           apiKey.Group,
			Tokens:          tokens,
			RequestCount:    1,
			RateMultiplier:  multiplier,
			PricingAt:       opts.PricingAt,
			ServiceTier:     serviceTier,
			ReasoningEffort: stringValueOrEmpty(result.ReasoningEffort),
			Resolver:        s.resolver,
			Resolved:        resolved,
		})
	} else {
		switch {
		case s.resolver != nil && apiKey.Group != nil:
			gid := apiKey.Group.ID
			cost, err = s.billingService.CalculateCostUnified(CostInput{
				Ctx:             ctx,
				Model:           billingModel,
				GroupID:         &gid,
				Group:           apiKey.Group,
				Tokens:          tokens,
				RequestCount:    1,
				RateMultiplier:  multiplier,
				PricingAt:       opts.PricingAt,
				ServiceTier:     serviceTier,
				ReasoningEffort: stringValueOrEmpty(result.ReasoningEffort),
				Resolver:        s.resolver,
			})
		default:
			cost, err = s.billingService.CalculateCostWithServiceTier(billingModel, tokens, multiplier, serviceTier)
			if err == nil {
				applyCostBreakdownMultiplier(cost, maxReasoningEffortBillingMultiplier(billingModel, stringValueOrEmpty(result.ReasoningEffort), nil))
			}
		}
	}
	if err != nil {
		logger.LegacyPrintf("service.gateway", "Calculate cost failed: %v", err)
		return &CostBreakdown{ActualCost: 0}
	}
	return cost
}

// buildRecordUsageLog 构建使用日志并设置计费模式。
func (s *GatewayService) buildRecordUsageLog(
	ctx context.Context,
	input *recordUsageCoreInput,
	result *ForwardResult,
	apiKey *APIKey,
	user *User,
	account *Account,
	subscription *UserSubscription,
	requestedModel string,
	multiplier float64,
	imageMultiplier float64,
	accountRateMultiplier float64,
	billingType int8,
	cacheTTLOverridden bool,
	cost *CostBreakdown,
	opts *recordUsageOpts,
) *UsageLog {
	durationMs := int(result.Duration.Milliseconds())
	requestID := resolveUsageBillingRequestID(ctx, result.RequestID)
	usageLog := &UsageLog{
		UserID:            usageActorUserID(apiKey, user),
		BillingUserID:     user.ID,
		TeamID:            apiKey.TeamID,
		APIKeyID:          apiKey.ID,
		AccountID:         account.ID,
		RequestID:         requestID,
		UpstreamRequestID: usageUpstreamRequestIDPtr(account, result.UpstreamHeaders, false),
		Model:             result.Model,
		RequestedModel:    requestedModel,
		UpstreamModel:     optionalTrimmedStringPtr(result.UpstreamModel),
		ReasoningEffort:   result.ReasoningEffort,
		RequestedReasoningEffort: coalesceRequestedReasoningEffort(
			result.RequestedReasoningEffort,
			CanonicalRequestedReasoningEffort(input.RequestBody, result.Model),
		),
		ServiceTier:           optionalTrimmedStringPtr(forwardResultServiceTier(result)),
		InboundEndpoint:       optionalTrimmedStringPtr(input.InboundEndpoint),
		UpstreamEndpoint:      optionalTrimmedStringPtr(input.UpstreamEndpoint),
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
		BillingMode:           resolveBillingMode(result, cost),
		Stream:                result.Stream,
		DurationMs:            &durationMs,
		FirstTokenMs:          result.FirstTokenMs,
		ImageCount:            result.ImageCount,
		ImageSize:             optionalTrimmedStringPtr(result.ImageSize),
		ImageInputSize:        optionalTrimmedStringPtr(result.ImageInputSize),
		ImageOutputSize:       optionalTrimmedStringPtr(result.ImageOutputSize),
		ImageSizeSource:       optionalTrimmedStringPtr(result.ImageSizeSource),
		ImageSizeBreakdown:    result.ImageSizeBreakdown,
		CacheTTLOverridden:    cacheTTLOverridden,
		ChannelID:             optionalInt64Ptr(input.ChannelID),
		ModelMappingChain:     optionalTrimmedStringPtr(input.ModelMappingChain),
		UserAgent:             optionalTrimmedStringPtr(input.UserAgent),
		IPAddress:             optionalTrimmedStringPtr(input.IPAddress),
		SessionID:             optionalTrimmedStringPtr(input.ClientSessionID),
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

// claudeUsageServiceTier 将 Claude usage.speed 复用为内部统一的 Fast 计费层级。
func claudeUsageServiceTier(speed string) string {
	if strings.EqualFold(strings.TrimSpace(speed), "fast") {
		return OpenAIFastTierPriority
	}
	return ""
}

// forwardResultServiceTier 统一通用网关结果的请求/实际档位来源。
func forwardResultServiceTier(result *ForwardResult) string {
	if result == nil {
		return ""
	}
	if tier := strings.TrimSpace(stringValueOrEmpty(result.ServiceTier)); tier != "" {
		return tier
	}
	return claudeUsageServiceTier(result.Usage.Speed)
}

// resolveBillingMode 根据计费结果和请求类型确定计费模式。
func resolveBillingMode(result *ForwardResult, cost *CostBreakdown) *string {
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

func optionalSubscriptionID(subscription *UserSubscription) *int64 {
	if subscription != nil {
		return &subscription.ID
	}
	return nil
}
