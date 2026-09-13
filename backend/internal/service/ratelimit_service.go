package service

import (
	"bytes"
	"context"
	"encoding/json"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/tidwall/gjson"
)

// RateLimitService 处理限流和过载状态管理
type RateLimitService struct {
	geminiPrecheck         *accountcore.GeminiPrecheck
	geminiPrecheckOnce     sync.Once
	health                 *accountcore.HealthService
	recovery               *accountcore.RecoveryService
	accountRepo            AccountRepository
	usageRepo              UsageLogRepository
	cfg                    *config.Config
	geminiQuotaService     *GeminiQuotaService
	tempUnschedCache       TempUnschedCache
	openAIAPIKeyHealth     OpenAIAPIKeyHealthCache
	timeoutCounterCache    TimeoutCounterCache
	openAI403CounterCache  OpenAI403CounterCache
	settingService         *SettingService
	tokenCacheInvalidator  TokenCacheInvalidator
	runtimeBlocker         AccountRuntimeBlocker
	advancedSchedulerMu    sync.Mutex
	advancedSchedulerStats *advancedAccountRuntimeStats

	// OpenAI Team 联动熔断的进程内去重：teamID → 去重窗口截止时间
	openaiTeamLinkedMu     sync.Mutex
	openaiTeamLinkedRecent map[string]time.Time
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
type SuccessfulTestRecoveryResult = accountcore.SuccessfulTestRecovery

// AccountRecoveryOptions 保留旧消费者的同一值类型。
type AccountRecoveryOptions = accountcore.AccountRecoveryOptions

const (
	openAI403CooldownMinutesDefault      = accountcore.OpenAI403CooldownMinutesDefault
	openAI403DisableThresholdDefault     = accountcore.OpenAI403DisableThresholdDefault
	openAI403CounterWindowMinutesDefault = accountcore.OpenAI403CounterWindowMinutesDefault
	defaultRateLimit429CooldownSeconds   = accountcore.DefaultRateLimit429CooldownSeconds
)

const (
	openAIImageRateLimitDefaultCooldown = time.Minute
	openAIImageRateLimitReason          = "openai_image_rate_limited"
	openAIImageCapabilityLossCooldown   = 30 * time.Minute
	openAIImageCapabilityLossReason     = "openai_image_capability_lost"
)

var openAIImageTryAgainPattern = regexp.MustCompile(`(?i)try again in\s+([0-9]+(?:\.[0-9]+)?)\s*(ms|s|sec|secs|second|seconds|m|min|mins|minute|minutes)`)

var openCodeGoUsageLimitResetPattern = regexp.MustCompile(`(?i)\bresets\s+in\s+`)

var openCodeGoUsageLimitDurationPartPattern = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)\s*(s|sec|secs|second|seconds|m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days|w|week|weeks)\b`)

// NewRateLimitService 创建RateLimitService实例
func NewRateLimitService(accountRepo AccountRepository, usageRepo UsageLogRepository, cfg *config.Config, geminiQuotaService *GeminiQuotaService, tempUnschedCache TempUnschedCache) *RateLimitService {
	return &RateLimitService{
		accountRepo:            accountRepo,
		usageRepo:              usageRepo,
		cfg:                    cfg,
		geminiQuotaService:     geminiQuotaService,
		tempUnschedCache:       tempUnschedCache,
		advancedSchedulerStats: newAdvancedAccountRuntimeStats(),
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
func (s *RateLimitService) SetTimeoutCounterCache(cache TimeoutCounterCache) {
	s.timeoutCounterCache = cache
}

// SetOpenAIAPIKeyHealthCache 设置 OpenAI API Key 健康熔断计数缓存。
func (s *RateLimitService) SetOpenAIAPIKeyHealthCache(cache OpenAIAPIKeyHealthCache) {
	if s == nil {
		return
	}
	s.openAIAPIKeyHealth = cache
}

// SetOpenAI403CounterCache 设置 OpenAI 403 连续失败计数器（可选依赖）
func (s *RateLimitService) SetOpenAI403CounterCache(cache OpenAI403CounterCache) {
	s.openAI403CounterCache = cache
}

// SetSettingService 设置系统设置服务（可选依赖）
func (s *RateLimitService) SetSettingService(settingService *SettingService) {
	s.settingService = settingService
}

// SetTokenCacheInvalidator 设置 token 缓存清理器（可选依赖）
func (s *RateLimitService) SetTokenCacheInvalidator(invalidator TokenCacheInvalidator) {
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

type ErrorPolicyResult = accountcore.ErrorPolicyResult

const (
	ErrorPolicyNone            = accountcore.ErrorPolicyNone
	ErrorPolicyCustomSkipped   = accountcore.ErrorPolicyCustomSkipped
	ErrorPolicyCustomMatched   = accountcore.ErrorPolicyCustomMatched
	ErrorPolicyTempUnscheduled = accountcore.ErrorPolicyTempUnscheduled
	ErrorPolicyPoolBypassed    = accountcore.ErrorPolicyPoolBypassed
	ErrorPolicyMatched         = accountcore.ErrorPolicyMatched
	ErrorPolicySkipped         = accountcore.ErrorPolicySkipped
)

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

func (s *RateLimitService) CheckErrorPolicy(ctx context.Context, account *Account, statusCode int, responseBody []byte, requestedModel ...string) ErrorPolicyResult {
	if account == nil {
		return ErrorPolicyNone
	}
	return s.HealthCore().CheckErrorPolicy(ctx, AccountRecordView(account), statusCode, responseBody, tempUnschedulableModel(ctx, requestedModel), account == nil || account.Platform != PlatformAntigravity)
}

// ApplyExplicitErrorPolicy 检查并应用管理员显式配置的错误策略。
// 自定义错误码命中时在这里统一写入账号错误，避免 400、429、529 被内置分支覆盖。
func (s *RateLimitService) ApplyExplicitErrorPolicy(ctx context.Context, account *Account, statusCode int, responseBody []byte, requestedModel ...string) ErrorPolicyResult {
	result := s.CheckErrorPolicy(ctx, account, statusCode, responseBody, requestedModel...)
	if result != ErrorPolicyCustomMatched || account == nil {
		return result
	}
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(responseBody)))
	if message == "" {
		message = "Custom error code triggered"
	}
	s.handleCustomErrorCode(ctx, account, statusCode, message)
	return result
}

// HandleUpstreamError 处理上游错误响应，标记账号状态
// 返回是否应该停止该账号的调度
func (s *RateLimitService) HandleUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, requestedModel ...string) (shouldDisable bool) {
	return s.ApplyUpstreamError(ctx, account, statusCode, headers, responseBody, requestedModel...).StopScheduling
}

// ApplyUpstreamError 先执行显式策略，再在非池模式下执行平台默认账号状态处理。
func (s *RateLimitService) ApplyUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, requestedModel ...string) UpstreamErrorDecision {
	// Team 联动熔断必须先于池模式、自定义错误码和临时不可调度的各类早退；
	// fastpath 入口的重复调用由方法内去重吸收。
	s.maybeHandleOpenAITeamLinkedError(ctx, account, statusCode, responseBody)
	policy := ErrorPolicyNone
	// 非池账号未启用自定义错误码时，模型不存在和官方硬窗口等精确状态必须
	// 先于宽泛的临时不可调度规则；池模式和自定义错误码仍作为前置显式策略。
	if account != nil && (account.IsPoolMode() || account.IsCustomErrorCodesEnabled()) {
		policy = s.ApplyExplicitErrorPolicy(ctx, account, statusCode, responseBody, requestedModel...)
	}
	decision := UpstreamErrorDecision{Policy: policy}
	switch policy {
	case ErrorPolicyCustomMatched, ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case ErrorPolicyCustomSkipped, ErrorPolicyPoolBypassed:
		return decision
	}
	decision.StopScheduling = s.handleDefaultUpstreamError(ctx, account, statusCode, headers, responseBody, requestedModel...)
	return decision
}

// handleDefaultUpstreamError 只处理非池模式、未命中显式策略时的平台默认账号状态。
func (s *RateLimitService) handleDefaultUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, requestedModel ...string) (shouldDisable bool) {
	if account == nil {
		return false
	}
	ctx = withTempUnschedulableModel(ctx, requestedModel)

	if statusCode == 529 {
		s.handle529(ctx, account)
		return false
	}

	if len(requestedModel) > 0 && s.HandleUpstreamModelNotFound(ctx, account, requestedModel[0], statusCode, responseBody) {
		return true
	}

	// Anthropic 官方 5h/7d 窗口耗尽属于账号级硬限流，必须先于本地临时不可调度规则处理。
	// 否则宽泛的 429 关键字规则可能把多小时/多天冷却误缩短成本地暂停时间。
	if statusCode == http.StatusTooManyRequests && account.Platform == PlatformAnthropic {
		// 7d_oi 是 Fable 模型专属的 7d 窗口：只标记模型级限流，账号对其他模型仍可调度。
		fableLimited := s.persistAnthropicFableWindowLimit(ctx, account, headers)
		if s.persistAnthropicExhaustedWindowLimit(ctx, account, headers) {
			return false
		}
		if fableLimited {
			return false
		}
	}

	// 529 表示全局上游过载，必须先记录全局过载冷却，不能被账号级临时规则抢先消费。
	if statusCode == 529 {
		s.handle529(ctx, account)
		return false
	}
	// 非池账号保留既有精确状态优先级；401 继续进入认证刷新与默认冷却逻辑。
	if statusCode != http.StatusUnauthorized && s.tryTempUnschedulable(ctx, account, statusCode, responseBody, firstRequestedModel(requestedModel)) {
		return true
	}

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(responseBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	if upstreamMsg != "" {
		upstreamMsg = truncateForLog([]byte(upstreamMsg), 512)
	}

	switch statusCode {
	case 400:
		// "organization has been disabled" → 永久禁用
		if strings.Contains(strings.ToLower(upstreamMsg), "organization has been disabled") {
			msg := "Organization disabled (400): " + upstreamMsg
			s.handleAuthError(ctx, account, msg)
			shouldDisable = true
		} else if account.Platform == PlatformAnthropic && strings.Contains(strings.ToLower(upstreamMsg), "credit balance") {
			// Anthropic API key 余额不足（语义等同 402），停止调度
			msg := "Credit balance exhausted (400): " + upstreamMsg
			s.handleAuthError(ctx, account, msg)
			shouldDisable = true
		} else if strings.Contains(strings.ToLower(upstreamMsg), "identity verification is required") {
			// KYC 身份验证要求 → 永久禁用，账号需完成身份验证后才能恢复
			msg := "Identity verification required (400): " + upstreamMsg
			s.handleAuthError(ctx, account, msg)
			shouldDisable = true
		}
		// 其他 400 错误（如参数问题）不处理，不禁用账号
	case 401:
		// 外审第9轮:Spark 影子无独立凭据,401 是母账号 token 问题——失效缓存 / refresh_token 判断 /
		// 永久禁用 / 临时不可调度都必须落到凭据 owner(母账号),否则影子(无 refresh_token)必中
		// "refresh_token missing"永久禁用分支、母账号 token cache 也不会被清,把母账号可恢复的 token
		// 问题变成影子永久死亡。母账号被标记 temp-unschedulable 后由 parentHealthyForShadow 级联排除影子。
		// 非影子时 resolveCredentialAccount 返回自身;母账号缺失/损坏(orphan 影子,罕见)时回退到原 account。
		authAccount := account
		if resolved, rerr := resolveCredentialAccount(ctx, s.accountRepo, account); rerr == nil && resolved != nil {
			authAccount = resolved
		}
		// OpenAI: token_invalidated / token_revoked 表示 token 被永久作废（非过期），直接标记 error
		openai401Code := extractUpstreamErrorCode(responseBody)
		if authAccount.Platform == PlatformOpenAI && (openai401Code == "token_invalidated" || openai401Code == "token_revoked") {
			msg := "Token revoked (401): account authentication permanently revoked"
			if upstreamMsg != "" {
				msg = "Token revoked (401): " + upstreamMsg
			}
			s.handleAuthError(ctx, authAccount, msg)
			shouldDisable = true
			break
		}
		// OpenAI: {"detail":"Unauthorized"} 表示 token 完全无效（非标准 OpenAI 错误格式），直接标记 error
		if authAccount.Platform == PlatformOpenAI && gjson.GetBytes(responseBody, "detail").String() == "Unauthorized" {
			msg := "Unauthorized (401): account authentication failed permanently"
			if upstreamMsg != "" {
				msg = "Unauthorized (401): " + upstreamMsg
			}
			s.handleAuthError(ctx, authAccount, msg)
			shouldDisable = true
			break
		}
		// OAuth 账号在 401 错误时临时不可调度（给 token 刷新窗口）；非 OAuth 账号保持原有 SetError 行为。
		if authAccount.Type == AccountTypeOAuth {
			// 1. 失效缓存
			if s.tokenCacheInvalidator != nil {
				if err := s.tokenCacheInvalidator.InvalidateToken(ctx, authAccount); err != nil {
					slog.Warn("oauth_401_invalidate_cache_failed", "account_id", authAccount.ID, "error", err)
				}
			}
			// 缺少 refresh_token 的 OAuth 账号无法在冷却期内自愈（后台刷新服务也会跳过），
			// 直接走 SetError 永久禁用，避免冷却结束后再被选中产生一发无意义的 502。
			if strings.TrimSpace(authAccount.GetCredential("refresh_token")) == "" {
				msg := "Authentication failed (401): refresh_token missing, cannot recover"
				if upstreamMsg != "" {
					msg = "OAuth 401 (no refresh_token): " + upstreamMsg
				}
				s.handleAuthError(ctx, authAccount, msg)
				shouldDisable = true
				break
			}
			// 2. 临时不可调度，替代 SetError（保持 status=active 让刷新服务能拾取）
			// 注意：此处不再写回 account.Credentials/expires_at。
			// 原实现使用请求开始时的 account 快照整列覆盖 credentials JSONB（见
			// persistAccountCredentials → accountRepository.UpdateCredentials → SetCredentials），
			// 在另一个 worker 刚刷新完 refresh_token 的窄窗口内会把新 refresh_token 回滚为旧值，
			// 导致下一周期用旧 refresh_token 调上游拿到 invalid_grant 后，
			// tryRecoverFromRefreshRace 重读 DB 发现 currentRT == usedRT 也救不回来，账号被错误 disable。
			// 这里仅依赖 InvalidateToken + SetTempUnschedulable 让账号在冷却期内不被调度，
			// 冷却结束后由 token_provider 的 NeedsRefresh / token_refresh_service 走带分布式锁的正路刷新。
			msg := "Authentication failed (401): invalid or expired credentials"
			if upstreamMsg != "" {
				msg = "OAuth 401: " + upstreamMsg
			}
			if authAccount.Platform == PlatformAntigravity {
				extraUpdates := antigravityForceTokenRefreshExtra("401_invalid")
				if err := s.accountRepo.UpdateExtra(ctx, authAccount.ID, extraUpdates); err != nil {
					slog.Warn("antigravity_401_force_refresh_mark_failed", "account_id", authAccount.ID, "error", err)
				} else {
					if authAccount.Extra == nil {
						authAccount.Extra = make(map[string]any, len(extraUpdates))
					}
					for k, v := range extraUpdates {
						authAccount.Extra[k] = v
					}
					slog.Info("antigravity_401_force_refresh_marked", "account_id", authAccount.ID)
				}
			}
			cooldownMinutes := s.cfg.RateLimit.OAuth401CooldownMinutes
			if cooldownMinutes <= 0 {
				cooldownMinutes = 10
			}
			until := time.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
			s.notifyAccountSchedulingBlocked(authAccount, until, "oauth_401")
			if err := s.accountRepo.SetTempUnschedulable(ctx, authAccount.ID, until, msg); err != nil {
				slog.Warn("oauth_401_set_temp_unschedulable_failed", "account_id", authAccount.ID, "error", err)
			}
			shouldDisable = true
		} else {
			// 非 OAuth：保持 SetError 行为
			msg := "Authentication failed (401): invalid or expired credentials"
			if upstreamMsg != "" {
				msg = "Authentication failed (401): " + upstreamMsg
			}
			s.handleAuthError(ctx, authAccount, msg)
			shouldDisable = true
		}
	case 402:
		// 国产供应商：余额不足是可恢复状态（充值/检测恢复后由周期任务自动解除），
		// 不能走 handleAuthError 永久置 status=error。改为可恢复的临时停调。
		if account.IsCNProvider() {
			s.handleCNProviderInsufficientBalance(ctx, account, upstreamMsg)
			shouldDisable = true
			break
		}
		// OpenAI: deactivated_workspace 表示工作区已停用，直接标记 error
		if account.Platform == PlatformOpenAI && gjson.GetBytes(responseBody, "detail.code").String() == "deactivated_workspace" {
			msg := "Workspace deactivated (402): workspace has been deactivated"
			s.handleAuthError(ctx, account, msg)
			shouldDisable = true
			break
		}
		// 支付要求：余额不足或计费问题，停止调度
		msg := "Payment required (402): insufficient balance or billing issue"
		if upstreamMsg != "" {
			msg = "Payment required (402): " + upstreamMsg
		}
		s.handleAuthError(ctx, account, msg)
		shouldDisable = true
	case 403:
		logger.LegacyPrintf(
			"service.ratelimit",
			"[HandleUpstreamErrorRaw] account_id=%d platform=%s type=%s status=403 request_id=%s cf_ray=%s upstream_msg=%s raw_body=%s",
			account.ID,
			account.Platform,
			account.Type,
			strings.TrimSpace(headers.Get("x-request-id")),
			strings.TrimSpace(headers.Get("cf-ray")),
			upstreamMsg,
			truncateForLog(responseBody, 1024),
		)
		shouldDisable = s.handle403(ctx, account, upstreamMsg, responseBody)
	case 429:
		s.handle429(ctx, account, headers, responseBody)
		shouldDisable = false
	case 529:
		s.handle529(ctx, account)
		shouldDisable = false
	default:
		if statusCode >= 500 {
			// 未启用自定义错误码时：仅记录5xx错误
			slog.Warn("account_upstream_error", "account_id", account.ID, "status_code", statusCode)
			shouldDisable = false
		}
	}

	return shouldDisable
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

func buildForbiddenErrorMessage(prefix string, upstreamMsg string, responseBody []byte, fallback string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, " ") {
		prefix += " "
	}

	if msg := strings.TrimSpace(upstreamMsg); msg != "" {
		return prefix + msg
	}

	rawBody := bytes.TrimSpace(responseBody)
	if len(rawBody) > 0 {
		if json.Valid(rawBody) {
			var compact bytes.Buffer
			if err := json.Compact(&compact, rawBody); err == nil {
				return prefix + truncateForLog(compact.Bytes(), 512)
			}
		}
		return prefix + truncateForLog(rawBody, 512)
	}

	return prefix + fallback
}

// handle403 处理 403 Forbidden 错误
// Antigravity 平台区分 validation/violation/generic 三种类型，均 SetError 永久禁用；
// OpenAI 与国产供应商账号的 403 使用 HTML 豁免和累计冷却；
// 其他平台保持原有 SetError 行为。
func (s *RateLimitService) handle403(ctx context.Context, account *Account, upstreamMsg string, responseBody []byte) (shouldDisable bool) {
	if account.Platform == PlatformAntigravity {
		return s.handleAntigravity403(ctx, account, upstreamMsg, responseBody)
	}
	// Kimi 将账号级并发/业务限制返回为 403；保留切号信号，但不把精确文案
	// 送入会永久禁用账号的累计 403 计数器。
	if isCNProviderConcurrencyLimit403(account, upstreamMsg) {
		s.handleCNProviderConcurrencyLimit403(ctx, account)
		return true
	}
	if account.Platform == PlatformOpenAI || account.IsCNProvider() {
		return s.handleOpenAI403(ctx, account, upstreamMsg, responseBody)
	}
	// 非 Antigravity 平台：保持原有行为
	msg := buildForbiddenErrorMessage(
		"Access forbidden (403):",
		upstreamMsg,
		responseBody,
		"account may be suspended or lack permissions",
	)
	s.handleAuthError(ctx, account, msg)
	return true
}

func (s *RateLimitService) handleOpenAI403(ctx context.Context, account *Account, upstreamMsg string, responseBody []byte) (shouldDisable bool) {
	// 上游代理或 CDN 在请求到达 OpenAI API 前拦截时，可能返回 HTML 403，
	// 这只能证明当前链路或端点被阻断，不能证明账号凭据或权限失效。
	// 若继续计数或写账号状态，同一个错误请求会在 failover 中逐个处罚账号，
	// 最终把整组账号错误地下线。这里只跳过账号处罚，保留调用方既有切号行为。
	if isHTMLResponse(responseBody) {
		slog.Warn(
			"openai_403_html_body_skips_account_penalty",
			"account_id", account.ID,
			"upstream_message", upstreamMsg,
		)
		return false
	}

	msg := buildForbiddenErrorMessage(
		"Access forbidden (403):",
		upstreamMsg,
		responseBody,
		"account may be suspended or lack permissions",
	)
	return s.HealthCore().ApplyForbidden(ctx, AccountRecordView(account), msg)
}

// handleAntigravity403 处理 Antigravity 平台的 403 错误
// validation（需要验证）→ 永久 SetError（需人工去 Google 验证后恢复）
// violation（违规封号）→ 永久 SetError（需人工处理）
// generic（通用禁止）→ 永久 SetError
func (s *RateLimitService) handleAntigravity403(ctx context.Context, account *Account, upstreamMsg string, responseBody []byte) (shouldDisable bool) {
	fbType := classifyForbiddenType(string(responseBody))

	switch fbType {
	case forbiddenTypeValidation:
		// VALIDATION_REQUIRED: 永久禁用，需人工去 Google 验证后手动恢复
		msg := buildForbiddenErrorMessage(
			"Validation required (403):",
			upstreamMsg,
			responseBody,
			"account needs Google verification",
		)
		if validationURL := extractValidationURL(string(responseBody)); validationURL != "" {
			msg += " | validation_url: " + validationURL
		}
		s.handleAuthError(ctx, account, msg)
		return true

	case forbiddenTypeViolation:
		// 违规封号: 永久禁用，需人工处理
		msg := buildForbiddenErrorMessage(
			"Account violation (403):",
			upstreamMsg,
			responseBody,
			"terms of service violation",
		)
		s.handleAuthError(ctx, account, msg)
		return true

	default:
		// 通用 403: 保持原有行为
		msg := buildForbiddenErrorMessage(
			"Access forbidden (403):",
			upstreamMsg,
			responseBody,
			"account may be suspended or lack permissions",
		)
		s.handleAuthError(ctx, account, msg)
		return true
	}
}

func (s *RateLimitService) handleCustomErrorCode(ctx context.Context, account *Account, statusCode int, errorMsg string) {
	s.HealthCore().ApplyCustomErrorCode(ctx, AccountRecordView(account), statusCode, errorMsg)
}

// handle429 处理429限流错误
// 解析响应头获取重置时间，标记账号为限流状态
func (s *RateLimitService) handle429(ctx context.Context, account *Account, headers http.Header, responseBody []byte) {
	// Spark 影子：限流/熔断状态 100% 由 QueryUsage(/wham/usage body 的 codex_bengalfox)驱动。
	// /responses 的 429 携带的 x-codex-*/usage_limit_reached 是 global codex 道(plan/spec §8),
	// 套到影子会把 spark 误耦合到 global 窗口——即便 spark 仍有配额也会被冷却到 global reset,
	// 单影子场景直接变成无可用账号(外审第8轮 P1)。整段跳过;影子的 codex_* 仅由 account_usage 的
	// QueryUsage→persistOpenAICodexProbeSnapshot 维护,枯竭由调度守卫处理。
	if account.IsShadow() {
		return
	}
	if account.Platform == PlatformOpenAI && account.IsOpenAIOAuthLike() {
		if deferrer, ok := s.runtimeBlocker.(openAI429RetryDeferrer); ok && deferrer.ShouldRetryOpenAIOAuth429(account, headers, responseBody) {
			return
		}
	}
	// 国产供应商（kimi/zhipu/deepseek）的 429 走专用可恢复路径：余额不足 → 临时停调，
	// Coding Plan 窗口耗尽 → 冷却到快照重置点。未命中则继续默认 429 逻辑。
	if account.IsCNProvider() {
		if s.applyCNProviderReactive429(ctx, account, headers, responseBody) {
			return
		}
	}
	// 1. OpenAI 平台：优先尝试解析 x-codex-* 响应头（用于 rate_limit_exceeded）
	if account.Platform == PlatformOpenAI {
		persistOpenAI429PlanType(ctx, s.accountRepo, account, responseBody)
		s.persistOpenAICodexSnapshot(ctx, account, headers)
		if resetAt := s.calculateOpenAI429ResetTime(headers); resetAt != nil {
			s.notifyAccountSchedulingBlocked(account, *resetAt, "429")
			if err := s.accountRepo.SetRateLimited(ctx, account.ID, *resetAt); err != nil {
				slog.Warn("rate_limit_set_failed", "account_id", account.ID, "error", err)
				return
			}
			slog.Info("openai_account_rate_limited", "account_id", account.ID, "reset_at", *resetAt)
			return
		}
	}

	// 2. Anthropic 平台：尝试解析 per-window 头（5h / 7d），选择实际触发的窗口
	if result := calculateAnthropic429ResetTime(headers); result != nil {
		s.notifyAccountSchedulingBlocked(account, result.resetAt, "429")
		if err := s.accountRepo.SetRateLimited(ctx, account.ID, result.resetAt); err != nil {
			slog.Warn("rate_limit_set_failed", "account_id", account.ID, "error", err)
			return
		}

		// 更新 session window：优先使用 5h-reset 头精确计算，否则从 resetAt 反推
		windowEnd := result.resetAt
		if result.fiveHourReset != nil {
			windowEnd = *result.fiveHourReset
		}
		windowStart := windowEnd.Add(-5 * time.Hour)
		if err := s.accountRepo.UpdateSessionWindow(ctx, account.ID, &windowStart, &windowEnd, "rejected"); err != nil {
			slog.Warn("rate_limit_update_session_window_failed", "account_id", account.ID, "error", err)
		}

		slog.Info("anthropic_account_rate_limited", "account_id", account.ID, "reset_at", result.resetAt, "reset_in", time.Until(result.resetAt).Truncate(time.Second))
		return
	}

	// 3. 尝试从响应头解析重置时间（Anthropic 聚合头，向后兼容）
	resetTimestamp := headers.Get("anthropic-ratelimit-unified-reset")

	// 4. 如果响应头没有，尝试从响应体解析（OpenAI usage_limit_reached, Gemini）
	if resetTimestamp == "" {
		switch account.Platform {
		case PlatformOpenAI:
			// 尝试解析 OpenAI 的 usage_limit_reached 错误
			if resetAt := parseOpenAIRateLimitResetTime(responseBody); resetAt != nil {
				resetTime := time.Unix(*resetAt, 0)
				s.notifyAccountSchedulingBlocked(account, resetTime, "429")
				if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetTime); err != nil {
					slog.Warn("rate_limit_set_failed", "account_id", account.ID, "error", err)
					return
				}
				slog.Info("account_rate_limited", "account_id", account.ID, "platform", account.Platform, "reset_at", resetTime, "reset_in", time.Until(resetTime).Truncate(time.Second))
				return
			}
		case PlatformGemini, PlatformAntigravity:
			// 尝试解析 Gemini 格式（用于其他平台）
			if resetAt := ParseGeminiRateLimitResetTime(responseBody); resetAt != nil {
				resetTime := time.Unix(*resetAt, 0)
				s.notifyAccountSchedulingBlocked(account, resetTime, "429")
				if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetTime); err != nil {
					slog.Warn("rate_limit_set_failed", "account_id", account.ID, "error", err)
					return
				}
				slog.Info("account_rate_limited", "account_id", account.ID, "platform", account.Platform, "reset_at", resetTime, "reset_in", time.Until(resetTime).Truncate(time.Second))
				return
			}
		}

		// Anthropic 平台：没有限流重置时间的 429 可能是非真实限流（如 Extra usage required），
		// 不适合按 5h/7d 窗口长时间封禁；但完全不标记会导致账号永不冷却，
		// 调度器会让每个请求反复命中同一批持续返回 429 的账号并耗尽 failover 预算。
		// 因此使用可配置的秒级兜底回避，管理端仍可调整或关闭。
		if account.Platform == PlatformAnthropic {
			slog.Warn("rate_limit_429_no_reset_time",
				"account_id", account.ID,
				"platform", account.Platform,
				"reason", "no rate limit reset time in headers, likely not a real rate limit")
			s.apply429FallbackRateLimit(ctx, account, "anthropic_no_reset_time")
			return
		}

		// 其他平台：没有重置时间，使用可配置的秒级默认回避，避免误伤长时间不可调度。
		s.apply429FallbackRateLimit(ctx, account, "no_reset_time")
		return
	}

	// 解析Unix时间戳
	ts, err := strconv.ParseInt(resetTimestamp, 10, 64)
	if err != nil {
		slog.Warn("rate_limit_reset_parse_failed", "reset_timestamp", resetTimestamp, "error", err)
		s.apply429FallbackRateLimit(ctx, account, "reset_parse_failed")
		return
	}

	resetAt := time.Unix(ts, 0)

	// 标记限流状态
	s.notifyAccountSchedulingBlocked(account, resetAt, "429")
	if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetAt); err != nil {
		slog.Warn("rate_limit_set_failed", "account_id", account.ID, "error", err)
		return
	}

	// 根据重置时间反推5h窗口
	windowEnd := resetAt
	windowStart := resetAt.Add(-5 * time.Hour)
	if err := s.accountRepo.UpdateSessionWindow(ctx, account.ID, &windowStart, &windowEnd, "rejected"); err != nil {
		slog.Warn("rate_limit_update_session_window_failed", "account_id", account.ID, "error", err)
	}

	slog.Info("account_rate_limited", "account_id", account.ID, "reset_at", resetAt)
}

func (s *RateLimitService) apply429FallbackRateLimit(ctx context.Context, account *Account, reason string) {
	s.HealthCore().Apply429Fallback(ctx, AccountRecordView(account), reason)
}

func (s *RateLimitService) get429FallbackCooldown(ctx context.Context, account *Account) (time.Duration, bool) {
	return s.HealthCore().Fallback429Cooldown(ctx, AccountRecordView(account))
}

// calculateOpenAI429ResetTime 从 OpenAI 429 响应头计算正确的重置时间
// 返回 nil 表示无法从响应头中确定重置时间
func calculateOpenAI429ResetTime(headers http.Header) *time.Time {
	snapshot := ParseCodexRateLimitHeaders(headers)
	if snapshot == nil {
		return nil
	}

	normalized := snapshot.Normalize()
	if normalized == nil {
		return nil
	}

	now := time.Now()

	// 判断哪个限制被触发（used_percent >= 100）
	is7dExhausted := normalized.Used7dPercent != nil && *normalized.Used7dPercent >= 100
	is5hExhausted := normalized.Used5hPercent != nil && *normalized.Used5hPercent >= 100

	// 优先使用被触发限制的重置时间
	if is7dExhausted && normalized.Reset7dSeconds != nil {
		resetAt := now.Add(time.Duration(*normalized.Reset7dSeconds) * time.Second)
		slog.Info("openai_429_7d_limit_exhausted", "reset_after_seconds", *normalized.Reset7dSeconds, "reset_at", resetAt)
		return &resetAt
	}
	if is5hExhausted && normalized.Reset5hSeconds != nil {
		resetAt := now.Add(time.Duration(*normalized.Reset5hSeconds) * time.Second)
		slog.Info("openai_429_5h_limit_exhausted", "reset_after_seconds", *normalized.Reset5hSeconds, "reset_at", resetAt)
		return &resetAt
	}

	// 都未达到100%但收到429，使用较长的重置时间
	var maxResetSecs int
	if normalized.Reset7dSeconds != nil && *normalized.Reset7dSeconds > maxResetSecs {
		maxResetSecs = *normalized.Reset7dSeconds
	}
	if normalized.Reset5hSeconds != nil && *normalized.Reset5hSeconds > maxResetSecs {
		maxResetSecs = *normalized.Reset5hSeconds
	}
	if maxResetSecs > 0 {
		resetAt := now.Add(time.Duration(maxResetSecs) * time.Second)
		slog.Info("openai_429_using_max_reset", "max_reset_seconds", maxResetSecs, "reset_at", resetAt)
		return &resetAt
	}

	return nil
}

func (s *RateLimitService) calculateOpenAI429ResetTime(headers http.Header) *time.Time {
	return calculateOpenAI429ResetTime(headers)
}

// anthropic429Result holds the parsed Anthropic 429 rate-limit information.
type anthropic429Result struct {
	resetAt       time.Time  // The correct reset time to use for SetRateLimited
	fiveHourReset *time.Time // 5h window reset timestamp (for session window calculation), nil if not available
}

type anthropicWindowLimit struct {
	window        string
	resetAt       time.Time
	fiveHourReset *time.Time
	reason        string
}

func selectAnthropicExhaustedWindow(headers http.Header, now time.Time) *anthropicWindowLimit {
	reset5h, ok5hReset := parseAnthropicWindowReset(headers, "5h", now)
	reset7d, ok7dReset := parseAnthropicWindowReset(headers, "7d", now)

	exceeded5h := isAnthropic5hRejected(headers) || isAnthropicWindowExceeded(headers, "5h")
	exceeded7d := isAnthropicWindowExceeded(headers, "7d")

	if exceeded7d && ok7dReset {
		limit := &anthropicWindowLimit{
			window:  "7d",
			resetAt: reset7d,
			reason:  "anthropic_7d_window_exhausted",
		}
		if ok5hReset {
			limit.fiveHourReset = &reset5h
		}
		return limit
	}
	if exceeded5h && ok5hReset {
		return &anthropicWindowLimit{
			window:        "5h",
			resetAt:       reset5h,
			fiveHourReset: &reset5h,
			reason:        "anthropic_5h_window_exhausted",
		}
	}
	return nil
}

func isAnthropic5hRejected(headers http.Header) bool {
	return isAnthropicWindowRejected(headers, "5h")
}

func isAnthropicWindowRejected(headers http.Header, window string) bool {
	return strings.EqualFold(strings.TrimSpace(headers.Get("anthropic-ratelimit-unified-"+window+"-status")), "rejected")
}

func parseAnthropicWindowReset(headers http.Header, window string, now time.Time) (time.Time, bool) {
	maxAge := 8 * 24 * time.Hour
	if window == "5h" {
		maxAge = 6 * time.Hour
	}
	return parseAnthropicResetTimestamp(headers.Get("anthropic-ratelimit-unified-"+window+"-reset"), now, maxAge)
}

// parseAnthropicResetTimestamp 解析 Anthropic reset 头的 Unix 时间戳（自动识别毫秒），
// 并校验落在 (now, now+maxAge] 的合理区间内。
func parseAnthropicResetTimestamp(raw string, now time.Time, maxAge time.Duration) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	if ts > 1e11 {
		ts = ts / 1000
	}
	resetAt := time.Unix(ts, 0)
	if !resetAt.After(now) || resetAt.After(now.Add(maxAge)) {
		return time.Time{}, false
	}
	return resetAt, true
}

func shouldPersistAnthropicWindowLimit(account *Account, limit *anthropicWindowLimit, now time.Time) bool {
	if account == nil || limit == nil || !limit.resetAt.After(now) {
		return false
	}
	if account.RateLimitResetAt == nil {
		return true
	}
	if !account.RateLimitResetAt.After(now) {
		return true
	}
	return limit.resetAt.After(*account.RateLimitResetAt)
}

func (s *RateLimitService) persistAnthropicExhaustedWindowLimit(ctx context.Context, account *Account, headers http.Header) bool {
	if s == nil || s.accountRepo == nil || account == nil {
		return false
	}
	now := time.Now()
	limit := selectAnthropicExhaustedWindow(headers, now)
	if limit == nil {
		return false
	}
	if !shouldPersistAnthropicWindowLimit(account, limit, now) {
		slog.Info("anthropic_window_rate_limit_kept",
			"account_id", account.ID,
			"window", limit.window,
			"reset_at", limit.resetAt,
			"existing_reset_at", account.RateLimitResetAt)
		s.updateAnthropicRejectedSessionWindow(ctx, account, limit)
		return true
	}

	s.notifyAccountSchedulingBlocked(account, limit.resetAt, limit.reason)
	if err := s.accountRepo.SetRateLimited(ctx, account.ID, limit.resetAt); err != nil {
		slog.Warn("anthropic_window_rate_limit_set_failed",
			"account_id", account.ID,
			"window", limit.window,
			"reset_at", limit.resetAt,
			"error", err)
		return true
	}
	slog.Info("anthropic_window_rate_limited",
		"account_id", account.ID,
		"window", limit.window,
		"reset_at", limit.resetAt,
		"reset_in", time.Until(limit.resetAt).Truncate(time.Second))
	s.updateAnthropicRejectedSessionWindow(ctx, account, limit)
	return true
}

func (s *RateLimitService) updateAnthropicRejectedSessionWindow(ctx context.Context, account *Account, limit *anthropicWindowLimit) {
	if s == nil || s.accountRepo == nil || account == nil || limit == nil {
		return
	}
	// fork 需要维护 Anthropic 5h session window 状态，抢占 temp-unsched 后也不能跳过。
	windowEnd := limit.resetAt
	if limit.fiveHourReset != nil {
		windowEnd = *limit.fiveHourReset
	}
	windowStart := windowEnd.Add(-5 * time.Hour)
	if err := s.accountRepo.UpdateSessionWindow(ctx, account.ID, &windowStart, &windowEnd, "rejected"); err != nil {
		slog.Warn("rate_limit_update_session_window_failed", "account_id", account.ID, "error", err)
	}
}

const anthropicFableWindowReason = "anthropic_7d_oi_window_exhausted"

// selectAnthropicFableWindowLimit 解析 Anthropic 7d_oi 模型窗口响应头。
// 该窗口只约束 Fable 家族，不能让账号对其它模型失去调度资格。
// surpassed-threshold 使用浮点数而非布尔值，因此 status=rejected 或
// utilization >= 1.0 都视为超限；缺少专用 reset 时回退到聚合 reset。
func selectAnthropicFableWindowLimit(headers http.Header, now time.Time) *anthropicWindowLimit {
	if !isAnthropicWindowRejected(headers, "7d_oi") && !isAnthropicWindowExceeded(headers, "7d_oi") {
		return nil
	}
	resetAt, ok := parseAnthropicWindowReset(headers, "7d_oi", now)
	if !ok {
		resetAt, ok = parseAnthropicAggregateReset(headers, now)
	}
	if !ok {
		return nil
	}
	return &anthropicWindowLimit{
		window:  "7d_oi",
		resetAt: resetAt,
		reason:  anthropicFableWindowReason,
	}
}

// parseAnthropicAggregateReset 按 7 天窗口的合理范围解析聚合 reset 响应头。
func parseAnthropicAggregateReset(headers http.Header, now time.Time) (time.Time, bool) {
	return parseAnthropicResetTimestamp(headers.Get("anthropic-ratelimit-unified-reset"), now, 8*24*time.Hour)
}

// persistAnthropicFableWindowLimit 在 7d_oi 耗尽时写入 Fable 家族模型限流。
// 返回 true 表示该窗口触发了 429，调用方不能继续把整个账号标记为限流。
func (s *RateLimitService) persistAnthropicFableWindowLimit(ctx context.Context, account *Account, headers http.Header) bool {
	if s == nil || s.accountRepo == nil || account == nil {
		return false
	}
	now := time.Now()
	limit := selectAnthropicFableWindowLimit(headers, now)
	if limit == nil {
		return false
	}
	// 429 响应头本身携带最新的窗口用量（7d_oi utilization=1.0）。限流期内
	// Fable 请求不再调度到该账号，若不在此处采样，7d F 进度条会冻结在
	// 限流前的旧值直到窗口重置。
	s.samplePassiveUsageFromHeaders(ctx, account, headers)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, anthropicFableRateLimitKey, limit.resetAt, limit.reason); err != nil {
		slog.Warn("anthropic_fable_window_rate_limit_set_failed",
			"account_id", account.ID,
			"scope", anthropicFableRateLimitKey,
			"reset_at", limit.resetAt,
			"error", err)
		return true
	}
	slog.Info("anthropic_fable_window_model_rate_limited",
		"account_id", account.ID,
		"scope", anthropicFableRateLimitKey,
		"reset_at", limit.resetAt,
		"reset_in", time.Until(limit.resetAt).Truncate(time.Second))
	return true
}

// calculateAnthropic429ResetTime parses Anthropic's per-window rate-limit headers
// to determine which window (5h or 7d) actually triggered the 429.
//
// Headers used:
//   - anthropic-ratelimit-unified-5h-utilization / anthropic-ratelimit-unified-5h-surpassed-threshold
//   - anthropic-ratelimit-unified-5h-reset
//   - anthropic-ratelimit-unified-7d-utilization / anthropic-ratelimit-unified-7d-surpassed-threshold
//   - anthropic-ratelimit-unified-7d-reset
//
// Returns nil when the per-window headers are absent (caller should fall back to
// the aggregated anthropic-ratelimit-unified-reset header).
func calculateAnthropic429ResetTime(headers http.Header) *anthropic429Result {
	reset5hStr := headers.Get("anthropic-ratelimit-unified-5h-reset")
	reset7dStr := headers.Get("anthropic-ratelimit-unified-7d-reset")

	if reset5hStr == "" && reset7dStr == "" {
		return nil
	}

	var reset5h, reset7d *time.Time
	if ts, err := strconv.ParseInt(reset5hStr, 10, 64); err == nil {
		t := time.Unix(ts, 0)
		reset5h = &t
	}
	if ts, err := strconv.ParseInt(reset7dStr, 10, 64); err == nil {
		t := time.Unix(ts, 0)
		reset7d = &t
	}

	is5hExceeded := isAnthropicWindowExceeded(headers, "5h")
	is7dExceeded := isAnthropicWindowExceeded(headers, "7d")

	slog.Info("anthropic_429_window_analysis",
		"is_5h_exceeded", is5hExceeded,
		"is_7d_exceeded", is7dExceeded,
		"reset_5h", reset5hStr,
		"reset_7d", reset7dStr,
	)

	// Select the correct reset time based on which window(s) are exceeded.
	var chosen *time.Time
	switch {
	case is5hExceeded && is7dExceeded:
		// Both exceeded → prefer 7d (longer cooldown), fall back to 5h
		chosen = reset7d
		if chosen == nil {
			chosen = reset5h
		}
	case is5hExceeded:
		chosen = reset5h
	case is7dExceeded:
		chosen = reset7d
	default:
		// Neither flag clearly exceeded — pick the sooner reset as best guess
		chosen = pickSooner(reset5h, reset7d)
	}

	if chosen == nil {
		return nil
	}
	return &anthropic429Result{resetAt: *chosen, fiveHourReset: reset5h}
}

// isAnthropicWindowExceeded checks whether a given Anthropic rate-limit window
// (e.g. "5h" or "7d") has been exceeded, using utilization and surpassed-threshold headers.
func isAnthropicWindowExceeded(headers http.Header, window string) bool {
	prefix := "anthropic-ratelimit-unified-" + window + "-"

	// Check surpassed-threshold first (most explicit signal)
	if st := headers.Get(prefix + "surpassed-threshold"); strings.EqualFold(st, "true") {
		return true
	}

	// Fall back to utilization >= 1.0
	if utilStr := headers.Get(prefix + "utilization"); utilStr != "" {
		if util, err := strconv.ParseFloat(utilStr, 64); err == nil && util >= 1.0-1e-9 {
			// Use a small epsilon to handle floating point: treat 0.9999999... as >= 1.0
			return true
		}
	}

	return false
}

// pickSooner returns whichever of the two time pointers is earlier.
// If only one is non-nil, it is returned. If both are nil, returns nil.
func pickSooner(a, b *time.Time) *time.Time {
	switch {
	case a != nil && b != nil:
		if a.Before(*b) {
			return a
		}
		return b
	case a != nil:
		return a
	default:
		return b
	}
}

func (s *RateLimitService) persistOpenAICodexSnapshot(ctx context.Context, account *Account, headers http.Header) {
	if s == nil || s.accountRepo == nil || account == nil || headers == nil {
		return
	}
	// spark 影子的 codex_* 仅由 QueryUsage(/wham/usage bengalfox 道)更新,不能被 /responses 的
	// x-codex-* 全局头快照污染(外审第7轮 P1,与 updateCodexUsageSnapshot 同口径)。
	if account.IsShadow() {
		return
	}
	snapshot := ParseCodexRateLimitHeaders(headers)
	if snapshot == nil {
		return
	}
	updates := buildCodexUsageExtraUpdates(snapshot, time.Now())
	if len(updates) == 0 {
		return
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
		slog.Warn("openai_codex_snapshot_persist_failed", "account_id", account.ID, "error", err)
	}
}

// parseOpenAIRateLimitResetTime 解析 OpenAI 兼容格式的 429 响应，返回重置时间的 Unix 时间戳
// OpenAI 的 usage_limit_reached 错误格式：
//
//	{
//	  "error": {
//	    "message": "The usage limit has been reached",
//	    "type": "usage_limit_reached",
//	    "resets_at": 1769404154,
//	    "resets_in_seconds": 133107
//	  }
//	}
func parseOpenAIRateLimitResetTime(body []byte) *int64 {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		return nil
	}

	// 检查是否为已知的账号用量限制类型。
	errType, _ := errObj["type"].(string)
	if errType != "usage_limit_reached" && errType != "rate_limit_exceeded" && errType != "GoUsageLimitError" {
		return nil
	}

	// 优先使用 resets_at（Unix 时间戳）
	if resetsAt, ok := errObj["resets_at"].(float64); ok {
		ts := int64(resetsAt)
		return &ts
	}
	if resetsAt, ok := errObj["resets_at"].(string); ok {
		if ts, err := strconv.ParseInt(resetsAt, 10, 64); err == nil {
			return &ts
		}
	}

	// 如果没有 resets_at，尝试使用 resets_in_seconds
	if resetsInSeconds, ok := errObj["resets_in_seconds"].(float64); ok {
		ts := time.Now().Unix() + int64(resetsInSeconds)
		return &ts
	}
	if resetsInSeconds, ok := errObj["resets_in_seconds"].(string); ok {
		if sec, err := strconv.ParseInt(resetsInSeconds, 10, 64); err == nil {
			ts := time.Now().Unix() + sec
			return &ts
		}
	}

	// OpenCode Go 订阅只在可读错误消息中提供重置时间，例如
	// "Weekly usage limit reached. Resets in 2 days."。
	if errType == "GoUsageLimitError" {
		message, _ := errObj["message"].(string)
		if resetAfter := parseOpenCodeGoUsageLimitResetDuration(message); resetAfter > 0 {
			ts := time.Now().Add(resetAfter).Unix()
			return &ts
		}
	}

	return nil
}

// parseOpenCodeGoUsageLimitResetDuration 解析 OpenCode Go 消息中的单段或复合重置时长。
func parseOpenCodeGoUsageLimitResetDuration(message string) time.Duration {
	resetPrefix := openCodeGoUsageLimitResetPattern.FindStringIndex(message)
	if resetPrefix == nil {
		return 0
	}

	remainder := message[resetPrefix[1]:]
	var total time.Duration
	for {
		remainder = strings.TrimSpace(remainder)
		matches := openCodeGoUsageLimitDurationPartPattern.FindStringSubmatchIndex(remainder)
		if matches == nil {
			break
		}

		value, err := strconv.ParseFloat(remainder[matches[2]:matches[3]], 64)
		if err != nil || value <= 0 {
			return 0
		}

		unit := openCodeGoUsageLimitDurationUnit(remainder[matches[4]:matches[5]])
		if unit <= 0 {
			return 0
		}

		const maxDuration = time.Duration(1<<63 - 1)
		if value >= float64(maxDuration)/float64(unit) {
			return 0
		}
		part := time.Duration(value * float64(unit))
		if part <= 0 || total > maxDuration-part {
			return 0
		}
		total += part
		remainder = remainder[matches[1]:]
	}

	return total
}

func openCodeGoUsageLimitDurationUnit(raw string) time.Duration {
	switch strings.ToLower(raw) {
	case "s", "sec", "secs", "second", "seconds":
		return time.Second
	case "m", "min", "mins", "minute", "minutes":
		return time.Minute
	case "h", "hr", "hrs", "hour", "hours":
		return time.Hour
	case "d", "day", "days":
		return 24 * time.Hour
	case "w", "week", "weeks":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

func parseOpenAIRateLimitPlanType(body []byte) string {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		return ""
	}

	errType, _ := errObj["type"].(string)
	if errType != "usage_limit_reached" && errType != "rate_limit_exceeded" {
		return ""
	}

	planType, _ := errObj["plan_type"].(string)
	return strings.ToLower(strings.TrimSpace(planType))
}

// persistOpenAI429PlanType 将 429 错误中观测到的计划类型同步到账户凭据。
func persistOpenAI429PlanType(ctx context.Context, repo AccountRepository, account *Account, body []byte) {
	if repo == nil || account == nil || account.Platform != PlatformOpenAI {
		return
	}
	// spark 影子账号恒不持凭据:即便收到带 plan_type 的 429,也不能把 plan_type 写进影子 credentials
	// ——该路径走 repo.BulkUpdate 直写、不经 persistAccountCredentials 守卫(外审第7轮 P1)。
	// plan_type 由母账号在自己的请求上维护,影子跳过。
	if account.IsCredentialShadow() {
		return
	}

	planType := parseOpenAIRateLimitPlanType(body)
	if planType == "" {
		return
	}

	current := strings.TrimSpace(account.GetCredential("plan_type"))
	if strings.EqualFold(current, planType) {
		return
	}

	if _, err := repo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{
		Credentials: map[string]any{"plan_type": planType},
	}); err != nil {
		slog.Warn("openai_429_plan_type_sync_failed", "account_id", account.ID, "plan_type", planType, "error", err)
		return
	}

	if account.Credentials == nil {
		account.Credentials = make(map[string]any, 1)
	}
	account.Credentials["plan_type"] = planType
	slog.Info("openai_429_plan_type_synced", "account_id", account.ID, "previous_plan_type", current, "plan_type", planType)
}

func (s *RateLimitService) handle529(ctx context.Context, account *Account) {
	s.HealthCore().ApplyOverload(ctx, AccountRecordView(account))
}

// UpdateSessionWindow 从成功响应更新5h窗口状态
func (s *RateLimitService) UpdateSessionWindow(ctx context.Context, account *Account, headers http.Header) {
	status := headers.Get("anthropic-ratelimit-unified-5h-status")
	if status == "" {
		return
	}

	// 检查是否需要初始化时间窗口
	// 对于 Setup Token 账号，首次成功请求时需要预测时间窗口
	var windowStart, windowEnd *time.Time
	needInitWindow := account.SessionWindowEnd == nil || time.Now().After(*account.SessionWindowEnd)

	// 优先使用响应头中的真实重置时间（比预测更准确）
	if resetStr := headers.Get("anthropic-ratelimit-unified-5h-reset"); resetStr != "" {
		if ts, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
			// 检测可能的毫秒时间戳（秒级约为 1e9，毫秒约为 1e12）
			if ts > 1e11 {
				slog.Warn("account_session_window_header_millis_detected", "account_id", account.ID, "raw_reset", resetStr)
				ts = ts / 1000
			}
			end := time.Unix(ts, 0)
			// 校验时间戳是否在合理范围内（不早于 5h 前，不晚于 7 天后）
			minAllowed := time.Now().Add(-5 * time.Hour)
			maxAllowed := time.Now().Add(7 * 24 * time.Hour)
			if end.Before(minAllowed) || end.After(maxAllowed) {
				slog.Warn("account_session_window_header_out_of_range", "account_id", account.ID, "raw_reset", resetStr, "parsed_end", end)
			} else if needInitWindow || account.SessionWindowEnd == nil || !end.Equal(*account.SessionWindowEnd) {
				// 窗口需要初始化，或者真实重置时间与已存储的不同，则更新
				start := end.Add(-5 * time.Hour)
				windowStart = &start
				windowEnd = &end
				slog.Info("account_session_window_from_header", "account_id", account.ID, "window_start", start, "window_end", end, "status", status)
			}
		} else {
			slog.Warn("account_session_window_header_parse_failed", "account_id", account.ID, "raw_reset", resetStr, "error", err)
		}
	}

	// 回退：如果没有真实重置时间且需要初始化窗口，使用预测
	if windowEnd == nil && needInitWindow && (status == "allowed" || status == "allowed_warning") {
		now := time.Now()
		start := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
		end := start.Add(5 * time.Hour)
		windowStart = &start
		windowEnd = &end
		slog.Info("account_session_window_initialized", "account_id", account.ID, "window_start", start, "window_end", end, "status", status)
	}

	// 窗口重置时清除旧的 utilization 和被动采样数据，避免残留上个窗口的数据
	if windowEnd != nil && needInitWindow {
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
			"session_window_utilization":      nil,
			"passive_usage_7d_utilization":    nil,
			"passive_usage_7d_reset":          nil,
			"passive_usage_7d_oi_utilization": nil,
			"passive_usage_7d_oi_reset":       nil,
			"passive_usage_sampled_at":        nil,
		})
	}

	if err := s.accountRepo.UpdateSessionWindow(ctx, account.ID, windowStart, windowEnd, status); err != nil {
		slog.Warn("session_window_update_failed", "account_id", account.ID, "error", err)
	}

	// 被动采样：从响应头收集 5h + 7d + 7d_oi utilization，合并为一次 DB 写入
	s.samplePassiveUsageFromHeaders(ctx, account, headers)

	// 如果状态为allowed且之前有限流，说明窗口已重置，清除限流状态
	if status == "allowed" && account.IsRateLimited() {
		if err := s.ClearRateLimit(ctx, account.ID); err != nil {
			slog.Warn("rate_limit_clear_failed", "account_id", account.ID, "error", err)
		}
	}
}

// samplePassiveUsageFromHeaders 从 Anthropic 响应头收集 5h/7d/7d_oi 的
// utilization 与 reset 被动采样数据，合并为一次 Extra 写入。无数据时不写。
func (s *RateLimitService) samplePassiveUsageFromHeaders(ctx context.Context, account *Account, headers http.Header) {
	extraUpdates := make(map[string]any, 6)
	// 5h utilization（0-1 小数），供 estimateSetupTokenUsage 使用
	if utilStr := headers.Get("anthropic-ratelimit-unified-5h-utilization"); utilStr != "" {
		if util, err := strconv.ParseFloat(utilStr, 64); err == nil {
			extraUpdates["session_window_utilization"] = util
		}
	}
	// 7d utilization（0-1 小数）
	if utilStr := headers.Get("anthropic-ratelimit-unified-7d-utilization"); utilStr != "" {
		if util, err := strconv.ParseFloat(utilStr, 64); err == nil {
			extraUpdates["passive_usage_7d_utilization"] = util
		}
	}
	// 7d reset timestamp
	if resetStr := headers.Get("anthropic-ratelimit-unified-7d-reset"); resetStr != "" {
		if ts, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
			if ts > 1e11 {
				ts = ts / 1000
			}
			extraUpdates["passive_usage_7d_reset"] = ts
		}
	}
	// 7d_oi (Fable 专属 7d 窗口) utilization（0-1 小数）
	if utilStr := headers.Get("anthropic-ratelimit-unified-7d_oi-utilization"); utilStr != "" {
		if util, err := strconv.ParseFloat(utilStr, 64); err == nil {
			extraUpdates["passive_usage_7d_oi_utilization"] = util
		}
	}
	// 7d_oi reset timestamp
	if resetStr := headers.Get("anthropic-ratelimit-unified-7d_oi-reset"); resetStr != "" {
		if ts, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
			if ts > 1e11 {
				ts = ts / 1000
			}
			extraUpdates["passive_usage_7d_oi_reset"] = ts
		}
	}
	if len(extraUpdates) > 0 {
		extraUpdates["passive_usage_sampled_at"] = time.Now().UTC().Format(time.RFC3339)
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, extraUpdates); err != nil {
			slog.Warn("passive_usage_update_failed", "account_id", account.ID, "error", err)
		}
	}
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
func (s *RateLimitService) RecoverAccountState(ctx context.Context, accountID int64, options AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	return s.RecoveryCore().RecoverAccountState(ctx, accountID, options)
}

// RecoverAccountAfterSuccessfulTest 委托账号健康用例。
func (s *RateLimitService) RecoverAccountAfterSuccessfulTest(ctx context.Context, accountID int64) (*SuccessfulTestRecoveryResult, error) {
	return s.RecoveryCore().RecoverAccountAfterSuccessfulTest(ctx, accountID)
}

// ClearTempUnschedulable 委托账号健康用例。
func (s *RateLimitService) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	return s.RecoveryCore().ClearTempUnschedulable(ctx, accountID)
}

// GetTempUnschedStatus 委托账号健康用例。
func (s *RateLimitService) GetTempUnschedStatus(ctx context.Context, accountID int64) (*TempUnschedState, error) {
	return s.RecoveryCore().GetTempUnschedStatus(ctx, accountID)
}

func (s *RateLimitService) HandleTempUnschedulable(ctx context.Context, account *Account, statusCode int, responseBody []byte, requestedModel ...string) bool {
	if account == nil {
		return false
	}
	if !account.ShouldHandleErrorCode(statusCode) {
		return false
	}
	ctx = withTempUnschedulableModel(ctx, requestedModel)
	return s.tryTempUnschedulableWith401Escalation(ctx, account, statusCode, responseBody, !account.IsPoolMode(), firstRequestedModel(requestedModel))
}

// HandleOpenAIImageRateLimit 将 OpenAI 生图限流写入能力维度限流，而不是封禁整个账号。
func (s *RateLimitService) HandleOpenAIImageRateLimit(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte) bool {
	if s == nil || account == nil || s.accountRepo == nil {
		return false
	}
	if account.Platform != PlatformOpenAI {
		return false
	}
	// 池模式由上游账号池负责能力切换，不能写入本地生图能力限流。
	if account.IsPoolMode() {
		return false
	}
	if !account.ShouldHandleErrorCode(statusCode) {
		slog.Info("openai_image_rate_limit_skipped_by_error_code_policy", "account_id", account.ID, "status_code", statusCode)
		return false
	}
	if !isOpenAIImageRateLimitError(statusCode, responseBody) {
		return false
	}

	resetAt := openAIImageRateLimitResetAt(headers, responseBody)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, openAIImageGenerationRateLimitKey, resetAt, openAIImageRateLimitReason); err != nil {
		slog.Warn("openai_image_rate_limit_set_model_rate_limit_failed", "account_id", account.ID, "scope", openAIImageGenerationRateLimitKey, "error", err)
		return true
	}
	slog.Info("openai_image_rate_limited", "account_id", account.ID, "scope", openAIImageGenerationRateLimitKey, "reset_at", resetAt, "reset_in", time.Until(resetAt).Truncate(time.Second))
	return true
}

// HandleOpenAICodexSparkRateLimit 将 Spark 独立配额窗口记录为模型级限流。
// Spark 的 x-codex-* 使用率和 reset 时间只代表 Spark 模型维度，不能写入账号级
// RateLimitResetAt，否则同一 OAuth 账号上的其他模型也会被错误停调。
func (s *RateLimitService) HandleOpenAICodexSparkRateLimit(ctx context.Context, account *Account, requestedModel string, statusCode int, headers http.Header, responseBody []byte) bool {
	if s == nil || account == nil || s.accountRepo == nil || statusCode != http.StatusTooManyRequests || !isOpenAIOAuthAccount(account) {
		return false
	}
	if !isCodexSparkModel(requestedModel) || !account.ShouldHandleErrorCode(statusCode) {
		return false
	}

	modelKey := normalizeCodexModel(modelRateLimitKeyForUpstreamModelNotFound(ctx, account, requestedModel))
	if modelKey == "" {
		return false
	}
	now := time.Now()
	disposition, resetAt := classifyOpenAIOAuth429(headers, responseBody)
	// Spark 只有明确耗尽 5h/7d 窗口时才能使用上游长 reset；普通瞬时 429
	// 即使携带全局 reset 头，也只能使用短时回避，避免错误冷却数天。
	if disposition != openAIOAuth429Quota5h && disposition != openAIOAuth429Quota7d {
		resetAt = nil
	}
	if resetAt == nil || !resetAt.After(now) {
		cooldown, ok := s.get429FallbackCooldown(ctx, account)
		if !ok || cooldown <= 0 {
			cooldown = time.Duration(defaultRateLimit429CooldownSeconds) * time.Second
		}
		reset := now.Add(cooldown)
		resetAt = &reset
	}
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, modelKey, *resetAt, openAICodexSparkRateLimitReason); err != nil {
		slog.Warn("openai_codex_spark_model_rate_limit_set_failed", "account_id", account.ID, "model", modelKey, "error", err)
	}
	slog.Info("openai_codex_spark_model_rate_limited", "account_id", account.ID, "model", modelKey, "reset_at", *resetAt)
	return true
}

// HandleOpenAIImageCapabilityLoss 在上游明确拒绝图片工具时暂时冷却账号的图片调度。
func (s *RateLimitService) HandleOpenAIImageCapabilityLoss(ctx context.Context, account *Account, statusCode int, responseBody []byte) bool {
	if s == nil || account == nil || s.accountRepo == nil {
		return false
	}
	if account.Platform != PlatformOpenAI {
		return false
	}
	if !account.ShouldHandleErrorCode(statusCode) {
		slog.Info("openai_image_capability_loss_skipped_by_error_code_policy", "account_id", account.ID, "status_code", statusCode)
		return false
	}
	if !isOpenAIImageCapabilityLossError(statusCode, responseBody) {
		return false
	}

	resetAt := time.Now().Add(openAIImageCapabilityLossCooldown)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, openAIImageGenerationRateLimitKey, resetAt, openAIImageCapabilityLossReason); err != nil {
		slog.Warn("openai_image_capability_loss_set_model_rate_limit_failed", "account_id", account.ID, "scope", openAIImageGenerationRateLimitKey, "error", err)
		return true
	}
	slog.Info("openai_image_capability_lost", "account_id", account.ID, "scope", openAIImageGenerationRateLimitKey, "reset_at", resetAt, "reset_in", time.Until(resetAt).Truncate(time.Second))
	return true
}

// isOpenAIImageCapabilityLossError 判断上游是否拒绝了 sub2api 自己写入请求体的 image_generation 工具选择。
// 该判定仅对自构造图片请求有意义，因为这类请求始终包含匹配的 image_generation 项；
// 上游仍称其不存在即表示账号失去该能力。
func isOpenAIImageCapabilityLossError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest || len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "image_generation") &&
		strings.Contains(lower, "not found in 'tools' parameter")
}

func isOpenAIImageRateLimitError(statusCode int, body []byte) bool {
	if statusCode != http.StatusTooManyRequests || len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{
		"for limit gpt-image",
		"input-images per min",
		"gpt-image-2-codex",
		"gpt-image",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func openAIImageRateLimitResetAt(headers http.Header, body []byte) time.Time {
	now := time.Now()
	if resetAt := parseRetryAfterResetTime(headers, now); resetAt != nil && resetAt.After(now) {
		return *resetAt
	}
	if resetAt := calculateOpenAI429ResetTime(headers); resetAt != nil && resetAt.After(now) {
		return *resetAt
	}
	if resetUnix := parseOpenAIRateLimitResetTime(body); resetUnix != nil {
		if resetAt := time.Unix(*resetUnix, 0); resetAt.After(now) {
			return resetAt
		}
	}
	if cooldown := parseOpenAIImageTryAgainCooldown(body); cooldown > 0 {
		return now.Add(cooldown)
	}
	return now.Add(openAIImageRateLimitDefaultCooldown)
}

func parseRetryAfterResetTime(headers http.Header, now time.Time) *time.Time {
	if headers == nil {
		return nil
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return nil
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		resetAt := now.Add(time.Duration(seconds * float64(time.Second)))
		return &resetAt
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		return &parsed
	}
	return nil
}

func parseOpenAIImageTryAgainCooldown(body []byte) time.Duration {
	if len(body) == 0 {
		return 0
	}
	match := openAIImageTryAgainPattern.FindSubmatch(body)
	if len(match) != 3 {
		return 0
	}
	value, err := strconv.ParseFloat(string(match[1]), 64)
	if err != nil || value <= 0 {
		return 0
	}
	switch strings.ToLower(string(match[2])) {
	case "ms":
		return time.Duration(value * float64(time.Millisecond))
	case "s", "sec", "secs", "second", "seconds":
		return time.Duration(value * float64(time.Second))
	case "m", "min", "mins", "minute", "minutes":
		return time.Duration(value * float64(time.Minute))
	default:
		return 0
	}
}

const upstreamModelNotFoundCooldown = 30 * time.Minute
const upstreamModelNotFoundReason = "upstream_404_model_not_found"
const upstreamCodexPlanGatedModelCooldown = 30 * time.Minute
const upstreamCodexPlanGatedModelReason = "upstream_400_codex_plan_gated_model"

const tempUnschedMessageMaxBytes = accountcore.TempUnschedMessageMaxBytes

// HandleUpstreamModelNotFound 在上游确定性无法提供目标模型时临时冷却账号与模型组合，
// 包括 404 模型不存在及 ChatGPT OAuth 套餐门控返回的 Codex 400。返回 true 表示当前请求
// 应切换账号；冷却结束前调度器会跳过该组合，避免反复选择必然失败的账号。
func (s *RateLimitService) HandleUpstreamModelNotFound(ctx context.Context, account *Account, upstreamModel string, statusCode int, responseBody []byte) bool {
	if s == nil || account == nil || s.accountRepo == nil {
		return false
	}
	// 池模式不根据聚合上游的一次 404 暂停本地账号与模型组合。
	if account.IsPoolMode() {
		return false
	}
	if !account.ShouldHandleErrorCode(statusCode) {
		return false
	}
	var cooldown time.Duration
	var reason string
	switch {
	case isUpstreamModelNotFoundError(statusCode, responseBody):
		cooldown, reason = upstreamModelNotFoundCooldown, upstreamModelNotFoundReason
	case isOpenAIOAuthAccount(account) && isOpenAICodexPlanGatedModelError(statusCode, responseBody):
		cooldown, reason = upstreamCodexPlanGatedModelCooldown, upstreamCodexPlanGatedModelReason
	default:
		return false
	}
	modelKey := modelRateLimitKeyForUpstreamModelNotFound(ctx, account, upstreamModel)
	if modelKey == "" {
		return false
	}
	if shouldSkipCodexPlanGatedImageModelCooldown(ctx, reason, upstreamModel, modelKey) {
		return true
	}
	resetAt := time.Now().Add(cooldown)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, modelKey, resetAt, reason); err != nil {
		slog.Warn("upstream_model_not_found_set_model_rate_limit_failed", "account_id", account.ID, "model", modelKey, "reason", reason, "error", err)
		return true
	}
	slog.Info("upstream_model_not_found_model_rate_limited", "account_id", account.ID, "model", modelKey, "reason", reason, "reset_at", resetAt)
	return true
}

// shouldSkipCodexPlanGatedImageModelCooldown 判断图片模型是否只是被 Codex 文本端点拒绝。
// 文本端点错配仍需切号，但不能让这次错误影响同一账号通过专用 Images 端点提供服务；
// 专用 Images 端点上的拒绝则代表真实能力缺失，必须保留模型冷却。
func shouldSkipCodexPlanGatedImageModelCooldown(ctx context.Context, reason, upstreamModel, modelKey string) bool {
	if reason != upstreamCodexPlanGatedModelReason || OpenAIImagesEndpointFromContext(ctx) {
		return false
	}
	return IsGPTImageGenerationModel(upstreamModel) || IsGPTImageGenerationModel(modelKey)
}

func modelRateLimitKeyForUpstreamModelNotFound(ctx context.Context, account *Account, upstreamModel string) string {
	modelKey := strings.TrimSpace(upstreamModel)
	if account == nil || modelKey == "" {
		return modelKey
	}
	if account.Platform == PlatformAntigravity {
		if resolved := strings.TrimSpace(resolveFinalAntigravityModelKey(ctx, account, modelKey)); resolved != "" {
			return resolved
		}
		return modelKey
	}
	if account.Platform == PlatformOpenAI || account.Platform == PlatformGrok {
		// OpenAI 兼容入口传入的已经是最终上游模型，此处只做平台规范化，
		// 不能再次应用账号映射，否则链式映射会把状态写到未实际请求的模型上。
		if resolved := strings.TrimSpace(normalizeOpenAIModelForUpstream(account, modelKey)); resolved != "" {
			return resolved
		}
		return modelKey
	}
	if mapped := strings.TrimSpace(account.GetMappedModel(modelKey)); mapped != "" {
		return mapped
	}
	return modelKey
}

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
	rule           TempUnschedulableRule
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
	return s.HealthCore().TryTempUnschedulable(ctx, AccountRecordView(value), status, body, escalate && (value == nil || value.Platform != PlatformAntigravity), tempUnschedulableModel(ctx, models))
}

// truncateTempUnschedMessage 委托账号纯规则。
func truncateTempUnschedMessage(body []byte, maxBytes int) string {
	return accountcore.TruncateTempUnschedMessage(body, maxBytes)
}

// HandleStreamTimeout 委托账号健康核心。
func (s *RateLimitService) HandleStreamTimeout(ctx context.Context, account *Account, model string) bool {
	return s.HealthCore().HandleStreamTimeout(ctx, AccountRecordView(account), model)
}
