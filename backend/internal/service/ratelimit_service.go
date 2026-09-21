package service

import (
	"context"
	"net/http"
	"strings"
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
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// RateLimitService 处理限流和过载状态管理
type RateLimitService struct {
	upstreamHealth         *accountprovider.UpstreamHealth
	rateLimits             *accountprovider.RateLimitObserver
	geminiPrecheck         *accountcore.GeminiPrecheck
	geminiPrecheckOnce     sync.Once
	health                 *accountcore.HealthService
	recovery               *accountcore.RecoveryService
	accountRepo            AccountRepository
	usageRepo              usage.UsageLogRepository
	cfg                    *config.Config
	geminiQuotaService     *accountcore.GeminiQuotaService
	tempUnschedCache       accountcore.TempUnschedCache
	openAIAPIKeyHealth     accountcore.OpenAIAPIKeyHealthCache
	timeoutCounterCache    accountcore.TimeoutCounterCache
	openAI403CounterCache  accountcore.OpenAI403CounterCache
	settingService         *gatewayprovider.RuntimeReaders
	tokenCacheInvalidator  accountcore.TokenCacheInvalidator
	runtimeBlocker         AccountRuntimeBlocker
	advancedSchedulerMu    sync.Mutex
	advancedSchedulerStats *advancedAccountRuntimeStats

	// 旧入口只引用 app 绑定的原生共享拥有者。
	teamLinkedOnce sync.Once
	teamLinked     *accountcore.TeamLinkedHealth
}

type AccountRuntimeBlocker interface {
	BlockAccountScheduling(account *Account, until time.Time, reason string)
	ClearAccountSchedulingBlock(accountID int64)
}

// openAI429RetryDeferrer 允许 OpenAI OAuth 在请求级同账号重试窗口内延迟
// 持久化 429 冷却；API Key 和没有该能力的调用方仍走原有限流路径。
type openAI429RetryDeferrer interface {
	ShouldRetryOpenAIOAuth429(account *Account, headers http.Header, responseBody []byte) bool
}

// SuccessfulTestRecoveryResult 保留旧消费者的同一值类型。

// AccountRecoveryOptions 保留旧消费者的同一值类型。

// NewRateLimitService 创建RateLimitService实例
func NewRateLimitService(accountRepo AccountRepository, usageRepo usage.UsageLogRepository, cfg *config.Config, geminiQuotaService *accountcore.GeminiQuotaService, tempUnschedCache accountcore.TempUnschedCache) *RateLimitService {
	return NewRateLimitServiceWithScheduler(accountRepo, usageRepo, cfg, geminiQuotaService, tempUnschedCache, nil)
}

// NewRateLimitServiceWithScheduler 接收组合根唯一反馈，不改变旧构造器的函数类型。
func NewRateLimitServiceWithScheduler(accountRepo AccountRepository, usageRepo usage.UsageLogRepository, cfg *config.Config, geminiQuotaService *accountcore.GeminiQuotaService, tempUnschedCache accountcore.TempUnschedCache, sharedStats *scheduler.RuntimeStats) *RateLimitService {
	var stats *advancedAccountRuntimeStats
	if sharedStats != nil {
		stats = &advancedAccountRuntimeStats{core: sharedStats}
	} else {
		stats = newAdvancedAccountRuntimeStats()
	}
	return &RateLimitService{
		accountRepo:            accountRepo,
		usageRepo:              usageRepo,
		cfg:                    cfg,
		geminiQuotaService:     geminiQuotaService,
		tempUnschedCache:       tempUnschedCache,
		advancedSchedulerStats: stats,
	}
}

// AdvancedSchedulerRuntimeStats 返回所有平台共享的高级调度运行时反馈。
// 诊断与真实请求都从此处读取，避免不同网关实例形成彼此隔离的评分样本。
func (s *RateLimitService) AdvancedSchedulerRuntimeStats() *advancedAccountRuntimeStats {
	if s == nil {
		return nil
	}
	s.advancedSchedulerMu.Lock()
	defer s.advancedSchedulerMu.Unlock()
	if s.advancedSchedulerStats == nil {
		s.advancedSchedulerStats = newAdvancedAccountRuntimeStats()
	}
	return s.advancedSchedulerStats
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

// IsAdvancedSchedulerStickyWeightedEnabled 判断高级调度是否启用粘性加权。
func (s *RateLimitService) IsAdvancedSchedulerStickyWeightedEnabled(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	gateway := &OpenAIGatewayService{rateLimitService: s}
	return gateway.isAdvancedSchedulerStickyWeightedEnabled(ctx)
}

func (s *RateLimitService) notifyAccountSchedulingBlocked(account *Account, until time.Time, reason string) {
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
func (s *RateLimitService) ApplyAccountSchedulingThreshold(ctx context.Context, value *Account) bool {
	if s == nil {
		return false
	}
	if s == nil {
		return false
	}
	view := AccountRecordView(value)
	paused := s.HealthCore().ApplyAccountSchedulingThreshold(ctx, view)
	if value != nil && view != nil {
		value.TempUnschedulableUntil = view.TempUnschedulableUntil
		value.TempUnschedulableReason = view.TempUnschedulableReason
		value.Extra = view.Extra
	}
	return paused
}

type UpstreamErrorDecision accountcore.UpstreamErrorDecision

func (d UpstreamErrorDecision) ShouldReturnGenericError() bool {
	return accountcore.UpstreamErrorDecision(d).ShouldReturnGenericError()
}

func (d UpstreamErrorDecision) ShouldFailover(account *Account, statusCode int, defaultFailover bool) bool {
	return accountcore.UpstreamErrorDecision(d).ShouldFailover(errorPolicyRecord(account), statusCode, defaultFailover)
}

func (d UpstreamErrorDecision) ShouldFailoverWithDefaults(
	account *Account,
	statusCode int,
	nonPoolDefault bool,
	poolDefault bool,
) bool {
	return accountcore.UpstreamErrorDecision(d).ShouldFailoverWithDefaults(errorPolicyRecord(account), statusCode, nonPoolDefault, poolDefault)
}

func (d UpstreamErrorDecision) RetryableOnSameAccount(account *Account, statusCode int) bool {
	return accountcore.UpstreamErrorDecision(d).RetryableOnSameAccount(errorPolicyRecord(account), statusCode)
}

func upstreamErrorDecisionWithoutPersistence(account *Account, statusCode int) UpstreamErrorDecision {
	return UpstreamErrorDecision(accountcore.ErrorDecisionWithoutPersistence(errorPolicyRecord(account), statusCode))
}

// HandleUpstreamError 处理上游错误响应，标记账号状态
// 返回是否应该停止该账号的调度
func (s *RateLimitService) HandleUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, requestedModel ...string) (shouldDisable bool) {
	return s.ApplyUpstreamError(ctx, account, statusCode, headers, responseBody, requestedModel...).StopScheduling
}

// PreCheckUsage 只投影旧输入，规则与缓存由账号核心拥有。
func (s *RateLimitService) PreCheckUsage(ctx context.Context, account *Account, requestedModel string) (bool, error) {
	return s.GeminiPrecheckCore().PreCheckUsage(ctx, AccountRecordView(account), requestedModel)
}

// PreCheckUsageBatch 只投影旧输入，规则与缓存由账号核心拥有。
func (s *RateLimitService) PreCheckUsageBatch(ctx context.Context, accounts []*Account, requestedModel string) (map[int64]bool, error) {
	return s.GeminiPrecheckCore().PreCheckUsageBatch(ctx, accountRecordPointers(accounts), requestedModel)
}

// GeminiCooldown 只投影旧输入，规则与缓存由账号核心拥有。
func (s *RateLimitService) GeminiCooldown(ctx context.Context, account *Account) time.Duration {
	return s.GeminiPrecheckCore().GeminiCooldown(ctx, AccountRecordView(account))
}

func (s *RateLimitService) handleAuthError(ctx context.Context, account *Account, errorMsg string) {
	s.HealthCore().ApplyAuthenticationFailure(ctx, AccountRecordView(account), errorMsg)
}

func (s *RateLimitService) get429FallbackCooldown(ctx context.Context, account *Account) (time.Duration, bool) {
	return s.HealthCore().Fallback429Cooldown(ctx, AccountRecordView(account))
}

// ClearRateLimit 委托账号健康用例。
func (s *RateLimitService) ClearRateLimit(ctx context.Context, accountID int64) error {
	return s.RecoveryCore().ClearRateLimit(ctx, accountID)
}

func (s *RateLimitService) ResetOpenAI403Counter(ctx context.Context, accountID int64) {
	if s != nil {
		s.HealthCore().ResetForbiddenCounter(ctx, accountID)
	}
}

// RecoverAccountState 委托账号健康用例。
func (s *RateLimitService) RecoverAccountState(ctx context.Context, accountID int64, options accountcore.AccountRecoveryOptions) (*accountcore.SuccessfulTestRecovery, error) {
	return s.RecoveryCore().RecoverAccountState(ctx, accountID, options)
}

// RecoverAccountAfterSuccessfulTest 委托账号健康用例。
func (s *RateLimitService) RecoverAccountAfterSuccessfulTest(ctx context.Context, accountID int64) (*accountcore.SuccessfulTestRecovery, error) {
	return s.RecoveryCore().RecoverAccountAfterSuccessfulTest(ctx, accountID)
}

// ClearTempUnschedulable 委托账号健康用例。
func (s *RateLimitService) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	return s.RecoveryCore().ClearTempUnschedulable(ctx, accountID)
}

// GetTempUnschedStatus 委托账号健康用例。
func (s *RateLimitService) GetTempUnschedStatus(ctx context.Context, accountID int64) (*accountcore.TempUnschedState, error) {
	return s.RecoveryCore().GetTempUnschedStatus(ctx, accountID)
}

// 旧临时规则入口只解析模型上下文。
func (s *RateLimitService) HandleTempUnschedulable(ctx context.Context, value *Account, status int, body []byte, models ...string) bool {
	return s.HealthCore().HandleTempUnschedulable(ctx, AccountRecordView(value), status, body, tempUnschedulableModel(ctx, models))
}

const upstreamModelNotFoundReason = accountcore.ModelNotFoundReason

func firstRequestedModel(requestedModel []string) string {
	if len(requestedModel) == 0 {
		return ""
	}
	return strings.TrimSpace(requestedModel[0])
}

type tempUnschedulableModelContextKey struct{}

func withTempUnschedulableModel(ctx context.Context, requestedModel []string) context.Context {
	model := firstRequestedModel(requestedModel)
	if model == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tempUnschedulableModelContextKey{}, model)
}

func tempUnschedulableModel(ctx context.Context, requestedModel []string) string {
	if model := firstRequestedModel(requestedModel); model != "" {
		return model
	}
	if ctx == nil {
		return ""
	}
	model, _ := ctx.Value(tempUnschedulableModelContextKey{}).(string)
	return strings.TrimSpace(model)
}

type tempUnschedulableRuleMatch struct {
	rule           accountcore.TempUnschedulableRule
	ruleIndex      int
	matchedKeyword string
}

// matchTempUnschedulableRules 仅投影旧私有结果字段，匹配实现唯一归账号核心。
func matchTempUnschedulableRules(value *Account, statusCode int, responseBody []byte) []tempUnschedulableRuleMatch {
	matches := accountcore.MatchTempUnschedulableRules(AccountRecordView(value), statusCode, responseBody)
	if matches == nil {
		return nil
	}
	out := make([]tempUnschedulableRuleMatch, 0, len(matches))
	for _, m := range matches {
		out = append(out, tempUnschedulableRuleMatch{rule: m.Rule, ruleIndex: m.RuleIndex, matchedKeyword: m.MatchedKeyword})
	}
	return out
}

func (s *RateLimitService) tryTempUnschedulable(ctx context.Context, account *Account, statusCode int, responseBody []byte, requestedModel ...string) bool {
	return s.tryTempUnschedulableWith401Escalation(ctx, account, statusCode, responseBody, true, requestedModel...)
}

// tryTempUnschedulableWith401Escalation 只投影平台升级许可与旧请求模型上下文。
func (s *RateLimitService) tryTempUnschedulableWith401Escalation(ctx context.Context, value *Account, status int, body []byte, escalate bool, models ...string) bool {
	return s.HealthCore().TryTempUnschedulable(ctx, AccountRecordView(value), status, body, escalate && (value == nil || value.Platform != capability.PlatformAntigravity), tempUnschedulableModel(ctx, models))
}

// HandleStreamTimeout 委托账号健康核心。
func (s *RateLimitService) HandleStreamTimeout(ctx context.Context, account *Account, model string) bool {
	return s.HealthCore().HandleStreamTimeout(ctx, AccountRecordView(account), model)
}

// SchedulerFeedback 仅暴露已注入的新反馈实例，供组合根与新消费者直接绑定。
func (s *RateLimitService) SchedulerFeedback() *scheduler.RuntimeStats {
	return schedulerStats(s.AdvancedSchedulerRuntimeStats())
}

func (s *RateLimitService) UpdateSessionWindow(ctx context.Context, value *Account, headers http.Header) {
	observation := accountprovider.SessionWindowObservation(headers)
	// 空观测保持原短路顺序，不触碰未装配的可选健康依赖。
	if observation.Status == "" {
		return
	}
	s.HealthCore().UpdateSessionWindow(ctx, AccountRecordView(value), observation)
}

// 图片健康旧入口只投影账号，状态规则与供应商解析均委托原生所有者。
func (s *RateLimitService) HandleOpenAIImageRateLimit(ctx context.Context, value *Account, status int, headers http.Header, body []byte) bool {
	if s == nil {
		return false
	}
	return accountprovider.ObserveOpenAIImageRateLimit(ctx, s.HealthCore(), AccountRecordView(value), status, headers, body)
}
func (s *RateLimitService) HandleOpenAIImageCapabilityLoss(ctx context.Context, value *Account, status int, body []byte) bool {
	if s == nil {
		return false
	}
	return accountprovider.ObserveOpenAIImageCapabilityLoss(ctx, s.HealthCore(), AccountRecordView(value), status, body)
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
	return ok && deferrer.ShouldRetryOpenAIOAuth429(AccountFromRecord(value), headers, body)
}

// 模型错误的旧调用只提取当次请求意图，不再拥有状态规则。
func (s *RateLimitService) HandleUpstreamModelNotFound(ctx context.Context, value *Account, model string, status int, body []byte) bool {
	if s == nil {
		return false
	}
	observer := accountprovider.ModelHealth{Health: s.HealthCore(), CodexRules: gatewayprovider.CodexModelRules(), IsImageModel: media.IsGPTImageGenerationModel}
	return observer.Observe(ctx, AccountRecordView(value), model, status, body, modelHealthThinking(ctx), requeststate.OpenAIImagesEndpointFromContext(ctx))
}
func modelRateLimitKeyForUpstreamModelNotFound(ctx context.Context, value *Account, model string) string {
	observer := accountprovider.ModelHealth{CodexRules: gatewayprovider.CodexModelRules()}
	return observer.LimitKey(AccountRecordView(value), model, modelHealthThinking(ctx))
}
func modelHealthThinking(ctx context.Context) *bool {
	if enabled, ok := requeststate.ThinkingEnabledFromContext(ctx); ok {
		return &enabled
	}
	return nil
}

// healthObservation 在旧边界提取请求局部状态，新观测用例不再依赖 Context 键。
func healthObservation(ctx context.Context, status int, headers http.Header, body []byte, models []string) accountprovider.HealthObservation {
	input := accountprovider.HealthObservation{Status: status, Headers: headers, Body: body, EffectiveModel: tempUnschedulableModel(ctx, models), ModelProvided: len(models) > 0, Thinking: modelHealthThinking(ctx), ImagesEndpoint: requeststate.OpenAIImagesEndpointFromContext(ctx)}
	if len(models) > 0 {
		input.Model = models[0]
	}
	return input
}
func (s *RateLimitService) CheckErrorPolicy(ctx context.Context, value *Account, status int, body []byte, models ...string) accountcore.ErrorPolicyResult {
	return s.UpstreamHealth().CheckErrorPolicy(ctx, AccountRecordView(value), healthObservation(ctx, status, nil, body, models))
}
func (s *RateLimitService) ApplyExplicitErrorPolicy(ctx context.Context, value *Account, status int, body []byte, models ...string) accountcore.ErrorPolicyResult {
	return s.UpstreamHealth().ApplyExplicitErrorPolicy(ctx, AccountRecordView(value), healthObservation(ctx, status, nil, body, models))
}
func (s *RateLimitService) ApplyUpstreamError(ctx context.Context, value *Account, status int, headers http.Header, body []byte, models ...string) UpstreamErrorDecision {
	record := AccountRecordView(value)
	result := s.UpstreamHealth().ApplyUpstreamError(ctx, record, healthObservation(ctx, status, headers, body, models))
	if value != nil && record != nil {
		value.Credentials, value.Extra = record.Credentials, record.Extra
	}
	return UpstreamErrorDecision(result)
}
func (s *RateLimitService) handleDefaultUpstreamError(ctx context.Context, value *Account, status int, headers http.Header, body []byte, models ...string) bool {
	record := AccountRecordView(value)
	result := s.UpstreamHealth().HandleDefault(ctx, record, healthObservation(ctx, status, headers, body, models))
	if value != nil && record != nil {
		value.Credentials, value.Extra = record.Credentials, record.Extra
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
func (s *RateLimitService) HandleOpenAICodexSparkRateLimit(ctx context.Context, value *Account, model string, status int, headers http.Header, body []byte) bool {
	if s == nil {
		return false
	}
	return s.UpstreamHealth().Models.ObserveSparkRateLimit(ctx, AccountRecordView(value), model, status, headers, body, modelHealthThinking(ctx))
}
