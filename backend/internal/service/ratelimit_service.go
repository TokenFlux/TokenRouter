package service

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// RateLimitService 处理限流和过载状态管理
type RateLimitService struct {
	upstreamHealth        *accountprovider.UpstreamHealth
	rateLimits            *accountprovider.RateLimitObserver
	health                *accountcore.HealthService
	recovery              *accountcore.RecoveryService
	accountRepo           gatewayprovider.ExecutionAccountStore
	usageRepo             usage.UsageLogRepository
	cfg                   *config.Config
	tempUnschedCache      accountcore.TempUnschedCache
	openAIAPIKeyHealth    accountcore.OpenAIAPIKeyHealthCache
	timeoutCounterCache   accountcore.TimeoutCounterCache
	openAI403CounterCache accountcore.OpenAI403CounterCache
	settingService        *gatewayprovider.RuntimeReaders
	tokenCacheInvalidator accountcore.TokenCacheInvalidator
	runtimeBlocker        AccountRuntimeBlocker

	// 旧入口只引用 app 绑定的原生共享拥有者。
	teamLinkedOnce sync.Once
	teamLinked     *accountcore.TeamLinkedHealth
}

type AccountRuntimeBlocker interface {
	BlockAccountScheduling(account *gatewayprovider.ExecutionAccount, until time.Time, reason string)
	ClearAccountSchedulingBlock(accountID int64)
}

// openAI429RetryDeferrer 允许 OpenAI OAuth 在请求级同账号重试窗口内延迟
// 持久化 429 冷却；API Key 和没有该能力的调用方仍走原有限流路径。
type openAI429RetryDeferrer interface {
	ShouldRetryOpenAIOAuth429(account *gatewayprovider.ExecutionAccount, headers http.Header, responseBody []byte) bool
}

// SuccessfulTestRecoveryResult 保留旧消费者的同一值类型。

// AccountRecoveryOptions 保留旧消费者的同一值类型。

// NewRateLimitService 创建RateLimitService实例
func NewRateLimitService(accountRepo gatewayprovider.ExecutionAccountStore, usageRepo usage.UsageLogRepository, cfg *config.Config, tempUnschedCache accountcore.TempUnschedCache) *RateLimitService {
	return &RateLimitService{accountRepo: accountRepo, usageRepo: usageRepo, cfg: cfg, tempUnschedCache: tempUnschedCache}
}

// SetTimeoutCounterCache 设置超时计数器缓存（可选依赖）
func (s *RateLimitService) SetTimeoutCounterCache(cache accountcore.TimeoutCounterCache) {
	s.timeoutCounterCache = cache
}

// SetOpenAIAPIKeyHealthCache 设置 OpenAI API Key 健康熔断计数缓存。
func (s *RateLimitService) SetOpenAIAPIKeyHealthCache(cache accountcore.OpenAIAPIKeyHealthCache) {
	if s == nil {
		return
	}
	s.openAIAPIKeyHealth = cache
}

// SetOpenAI403CounterCache 设置 OpenAI 403 连续失败计数器（可选依赖）
func (s *RateLimitService) SetOpenAI403CounterCache(cache accountcore.OpenAI403CounterCache) {
	s.openAI403CounterCache = cache
}

// SetSettingService 设置系统设置服务（可选依赖）
func (s *RateLimitService) SetSettingService(settingService *gatewayprovider.RuntimeReaders) {
	s.settingService = settingService
}

// SetTokenCacheInvalidator 设置 token 缓存清理器（可选依赖）
func (s *RateLimitService) SetTokenCacheInvalidator(invalidator accountcore.TokenCacheInvalidator) {
	s.tokenCacheInvalidator = invalidator
}

func (s *RateLimitService) SetAccountRuntimeBlocker(blocker AccountRuntimeBlocker) {
	s.runtimeBlocker = blocker
}

func (s *RateLimitService) notifyAccountSchedulingBlocked(account *gatewayprovider.ExecutionAccount, until time.Time, reason string) {
	if s == nil || s.runtimeBlocker == nil || account == nil {
		return
	}
	s.runtimeBlocker.BlockAccountScheduling(account, until, reason)
}

func (s *RateLimitService) notifyAccountSchedulingBlockCleared(accountID int64) {
	if s == nil || s.runtimeBlocker == nil || accountID <= 0 {
		return
	}
	s.runtimeBlocker.ClearAccountSchedulingBlock(accountID)
}

// ApplyAccountSchedulingThreshold 只投影旧输入和本地健康结果，不拥有阈值规则。
func (s *RateLimitService) ApplyAccountSchedulingThreshold(ctx context.Context, value *gatewayprovider.ExecutionAccount) bool {
	if s == nil {
		return false
	}
	if s == nil {
		return false
	}
	view := gatewayprovider.ExecutionRecord(value)
	paused := s.HealthCore().ApplyAccountSchedulingThreshold(ctx, view)
	if value != nil && view != nil {
		value.Record.TempUnschedulableUntil = view.TempUnschedulableUntil
		value.Record.TempUnschedulableReason = view.TempUnschedulableReason
		value.Record.Extra = view.Extra
	}
	return paused
}

func (s *RateLimitService) handleAuthError(ctx context.Context, account *gatewayprovider.ExecutionAccount, errorMsg string) {
	s.HealthCore().ApplyAuthenticationFailure(ctx, gatewayprovider.ExecutionRecord(account), errorMsg)
}

func (s *RateLimitService) get429FallbackCooldown(ctx context.Context, account *gatewayprovider.ExecutionAccount) (time.Duration, bool) {
	return s.HealthCore().Fallback429Cooldown(ctx, gatewayprovider.ExecutionRecord(account))
}

func (s *RateLimitService) ResetOpenAI403Counter(ctx context.Context, accountID int64) {
	if s != nil {
		s.HealthCore().ResetForbiddenCounter(ctx, accountID)
	}
}

// 旧临时规则入口只解析模型上下文。
func (s *RateLimitService) HandleTempUnschedulable(ctx context.Context, value *gatewayprovider.ExecutionAccount, status int, body []byte, models ...string) bool {
	return s.HealthCore().HandleTempUnschedulable(ctx, gatewayprovider.ExecutionRecord(value), status, body, requeststate.HealthModel(ctx, models))
}

const upstreamModelNotFoundReason = accountcore.ModelNotFoundReason

func (s *RateLimitService) tryTempUnschedulable(ctx context.Context, account *gatewayprovider.ExecutionAccount, statusCode int, responseBody []byte, requestedModel ...string) bool {
	return s.tryTempUnschedulableWith401Escalation(ctx, account, statusCode, responseBody, true, requestedModel...)
}

// tryTempUnschedulableWith401Escalation 只投影平台升级许可与旧请求模型上下文。
func (s *RateLimitService) tryTempUnschedulableWith401Escalation(ctx context.Context, value *gatewayprovider.ExecutionAccount, status int, body []byte, escalate bool, models ...string) bool {
	return s.HealthCore().TryTempUnschedulable(ctx, gatewayprovider.ExecutionRecord(value), status, body, escalate && (value == nil || value.Record.Platform != capability.PlatformAntigravity), requeststate.HealthModel(ctx, models))
}

// HandleStreamTimeout 委托账号健康核心。
func (s *RateLimitService) HandleStreamTimeout(ctx context.Context, account *gatewayprovider.ExecutionAccount, model string) bool {
	return s.HealthCore().HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(account), model)
}

func (s *RateLimitService) UpdateSessionWindow(ctx context.Context, value *gatewayprovider.ExecutionAccount, headers http.Header) {
	observation := accountprovider.SessionWindowObservation(headers)
	// 空观测保持原短路顺序，不触碰未装配的可选健康依赖。
	if observation.Status == "" {
		return
	}
	s.HealthCore().UpdateSessionWindow(ctx, gatewayprovider.ExecutionRecord(value), observation)
}

// 图片健康旧入口只投影账号，状态规则与供应商解析均委托原生所有者。
func (s *RateLimitService) HandleOpenAIImageRateLimit(ctx context.Context, value *gatewayprovider.ExecutionAccount, status int, headers http.Header, body []byte) bool {
	if s == nil {
		return false
	}
	return accountprovider.ObserveOpenAIImageRateLimit(ctx, s.HealthCore(), gatewayprovider.ExecutionRecord(value), status, headers, body)
}
func (s *RateLimitService) HandleOpenAIImageCapabilityLoss(ctx context.Context, value *gatewayprovider.ExecutionAccount, status int, body []byte) bool {
	if s == nil {
		return false
	}
	return accountprovider.ObserveOpenAIImageCapabilityLoss(ctx, s.HealthCore(), gatewayprovider.ExecutionRecord(value), status, body)
}

// BindRateLimitObserver 由组合根绑定唯一生产观测适配器。
func (s *RateLimitService) BindRateLimitObserver(observer *accountprovider.RateLimitObserver) {
	s.rateLimits = observer
}
func (s *RateLimitService) RateLimitObserver() *accountprovider.RateLimitObserver {
	if s.rateLimits != nil {
		return s.rateLimits
	}
	return &accountprovider.RateLimitObserver{Health: s.HealthCore(), Plans: s.accountRepo, RetryOpenAI: s.DeferOpenAI429, NextGeminiDaily: nextGeminiDailyResetUnix}
}

// DeferOpenAI429 只投影原网关的同账号恢复决定，不拥有第二次重试循环。
func (s *RateLimitService) DeferOpenAI429(value *accountcore.Record, headers http.Header, body []byte) bool {
	deferrer, ok := s.runtimeBlocker.(openAI429RetryDeferrer)
	return ok && deferrer.ShouldRetryOpenAIOAuth429(gatewayprovider.NewExecutionAccount(value), headers, body)
}

// 模型错误的旧调用只提取当次请求意图，不再拥有状态规则。
func (s *RateLimitService) HandleUpstreamModelNotFound(ctx context.Context, value *gatewayprovider.ExecutionAccount, model string, status int, body []byte) bool {
	if s == nil {
		return false
	}
	observer := accountprovider.ModelHealth{Health: s.HealthCore(), CodexRules: gatewayprovider.CodexModelRules(), IsImageModel: media.IsGPTImageGenerationModel}
	return observer.Observe(ctx, gatewayprovider.ExecutionRecord(value), model, status, body, requeststate.HealthThinking(ctx), requeststate.OpenAIImagesEndpointFromContext(ctx))
}
func modelRateLimitKeyForUpstreamModelNotFound(ctx context.Context, value *gatewayprovider.ExecutionAccount, model string) string {
	observer := accountprovider.ModelHealth{CodexRules: gatewayprovider.CodexModelRules()}
	return observer.LimitKey(gatewayprovider.ExecutionRecord(value), model, requeststate.HealthThinking(ctx))
}

func (s *RateLimitService) handleDefaultUpstreamError(ctx context.Context, value *gatewayprovider.ExecutionAccount, status int, headers http.Header, body []byte, models ...string) bool {
	record := gatewayprovider.ExecutionRecord(value)
	result := s.UpstreamHealth().HandleDefault(ctx, record, gatewayprovider.HealthObservationFromContext(ctx, status, headers, body, models))
	if value != nil && record != nil {
		value.Record.Credentials, value.Record.Extra = record.Credentials, record.Extra
	}
	return result
}

// BindUpstreamHealth 在生产图完成后绑定唯一平台观测适配器。
func (s *RateLimitService) BindUpstreamHealth(observer *accountprovider.UpstreamHealth) {
	s.upstreamHealth = observer
}
func (s *RateLimitService) UpstreamHealth() *accountprovider.UpstreamHealth {
	if s.upstreamHealth != nil {
		return s.upstreamHealth
	}
	health := s.HealthCore()
	return &accountprovider.UpstreamHealth{Core: health, Team: s.TeamLinkedHealth(), Limits: s.RateLimitObserver(), Models: &accountprovider.ModelHealth{Health: health, CodexRules: gatewayprovider.CodexModelRules(), IsImageModel: media.IsGPTImageGenerationModel}}
}

// Spark 旧入口只投影记录和当次模型意图，冷却保持模型范围。
func (s *RateLimitService) HandleOpenAICodexSparkRateLimit(ctx context.Context, value *gatewayprovider.ExecutionAccount, model string, status int, headers http.Header, body []byte) bool {
	if s == nil {
		return false
	}
	return s.UpstreamHealth().Models.ObserveSparkRateLimit(ctx, gatewayprovider.ExecutionRecord(value), model, status, headers, body, requeststate.HealthThinking(ctx))
}
