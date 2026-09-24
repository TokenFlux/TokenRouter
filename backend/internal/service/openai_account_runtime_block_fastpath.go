package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

const (
	openAIAccountStateUpdateTimeout = 5 * time.Second
	openAIOAuth429FallbackCooldown  = 5 * time.Second
	openAIOAuth429RetryDelay        = 500 * time.Millisecond
	openAIOAuth429MaxRetryDelay     = 8 * time.Second
	openAIOAuth429StormWindow       = 10 * time.Second
)

// 旧状态类型引用网关唯一的请求级预算。

const (
	openAIOAuth429Transient  = accountcore.OpenAI429Transient
	openAIOAuth429Quota5h    = accountcore.OpenAI429Quota5h
	openAIOAuth429Quota7d    = accountcore.OpenAI429Quota7d
	openAIOAuth429QuotaReset = accountcore.OpenAI429QuotaReset
)

func openAIAccountStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, openAIAccountStateUpdateTimeout)
}

func isOpenAIOAuthAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && account.View().IsOpenAIOAuthLike()
}

func isGrokOAuthAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && account.Record.Platform == capability.PlatformGrok && account.Record.Type == capability.AccountTypeOAuth
}

func isOpenAIAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && (account.Record.Platform == capability.PlatformOpenAI || account.Record.Platform == capability.PlatformGrok)
}

// handleOpenAIAccountUpstreamError 的 canonicalModel 必须是账号映射恰好应用一次后，
// 实际用于调度的模型。
func (s *OpenAIGatewayService) handleOpenAIAccountUpstreamError(ctx context.Context, account *gatewayprovider.ExecutionAccount, statusCode int, headers http.Header, responseBody []byte, canonicalModel ...string) bool {
	return s.applyOpenAIAccountUpstreamError(ctx, account, statusCode, headers, responseBody, canonicalModel...).StopScheduling
}

// applyOpenAIAccountUpstreamError 返回完整策略决策，供调用方区分池模式绕过、
// 自定义错误码未命中和真正停止调度的显式策略。
func (s *OpenAIGatewayService) applyOpenAIAccountUpstreamError(ctx context.Context, account *gatewayprovider.ExecutionAccount, statusCode int, headers http.Header, responseBody []byte, canonicalModel ...string) accountcore.UpstreamErrorDecision {
	return s.applyOpenAIAccountUpstreamErrorInternal(ctx, account, statusCode, headers, responseBody, false, canonicalModel...)
}

// applyOpenAIAccountStreamRateLimitError 对 HTTP 200 流内限流仅应用显式策略，
// 不使用正常配额快照响应头写入默认的账号级冷却状态。
func (s *OpenAIGatewayService) applyOpenAIAccountStreamRateLimitError(ctx context.Context, account *gatewayprovider.ExecutionAccount, statusCode int, headers http.Header, responseBody []byte, canonicalModel ...string) accountcore.UpstreamErrorDecision {
	return s.applyOpenAIAccountUpstreamErrorInternal(ctx, account, statusCode, headers, responseBody, true, canonicalModel...)
}

func (s *OpenAIGatewayService) applyOpenAIAccountUpstreamErrorInternal(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	statusCode int,
	headers http.Header,
	responseBody []byte,
	suppressDefaultRateLimitState bool,
	canonicalModel ...string,
) accountcore.UpstreamErrorDecision {
	customStatusMatched := account != nil && account.View().IsCustomErrorCodesEnabled() && account.View().ShouldHandleErrorCode(statusCode)
	if gatewayprovider.IsContentPolicyRejection(responseBody) || gatewayprovider.IsOpenAICyberWarningPayload(responseBody, upstream.ExtractErrorMessage(responseBody)) {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	if gatewayprovider.IsRequestScopedAccountFailure(account, statusCode, responseBody) && !customStatusMatched {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	// 任意非 2xx 上游响应都表示模型请求已实际发送。
	if s != nil {
		scheduleOllamaCloudUsageActivity(s.deferredService, account)
	}
	// 容量降载只描述当前请求，不代表账号健康异常；交给请求级重试预算恢复，
	// 保持账号可调度，避免误写账号冷却状态。
	if account != nil && account.Record.Platform == capability.PlatformOpenAI && openai.IsOpenAIRequestScopedCapacityShed("", responseBody) {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if account != nil && account.Record.Platform == capability.PlatformOpenAI && isOpenAIHTTPUpstreamAccessStateError(statusCode, "", responseBody) {
		message := "OpenAI upstream account or workspace is unavailable"
		if upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)); upstreamMsg != "" {
			message = upstreamMsg
		}
		if s != nil && s.healthObserver != nil {
			s.healthObserver.Core.ApplyAuthenticationFailure(stateCtx, gatewayprovider.ExecutionRecord(account), message)

		}
		if s != nil {
			s.BlockAccountScheduling(account, time.Time{}, "openai_access_state")
		}
		return accountcore.UpstreamErrorDecision{StopScheduling: true}
	}

	// 自构造图片请求始终携带匹配的 image_generation 工具，因此 400 "tool choice not found in 'tools'"
	// 表示上游撤销了该账号的图片能力。此处必须受自构造标记保护：透传客户端自行控制
	// tools/tool_choice，否则可能误伤健康账号。
	if openai.IsOpenAIImagesSelfBuiltRequest(ctx) && openai.IsImageCapabilityLossError(statusCode, responseBody) {
		if s != nil && s.healthObserver != nil {
			_ = accountprovider.ObserveOpenAIImageCapabilityLoss(stateCtx, s.healthObserver.Core, gatewayprovider.ExecutionRecord(account), statusCode, responseBody)

		}
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}

	if s == nil || account == nil {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	// Team 联动熔断必须先于 model-not-found 与账户级临时不可调度规则的早退。
	if s.healthObserver != nil {
		gatewayprovider.ObserveExecutionWorkspaceFailure(stateCtx, s.healthObserver, account, statusCode, responseBody)

	}
	stateCtx = requeststate.WithHealthModel(stateCtx, canonicalModel)
	decision := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(account), statusCode)
	if s.healthObserver != nil {
		if account.View().IsPoolMode() || account.View().IsCustomErrorCodesEnabled() {
			decision.Policy = s.healthObserver.ApplyExplicitErrorPolicy(stateCtx, gatewayprovider.ExecutionRecord(account), gatewayprovider.HealthObservationFromContext(stateCtx, statusCode, nil, responseBody, canonicalModel))
			decision.StopScheduling = decision.Policy == accountcore.ErrorPolicyCustomMatched || decision.Policy == accountcore.ErrorPolicyTempUnscheduled
		} else {
			decision = accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case accountcore.ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		return decision
	case accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}

	if !suppressDefaultRateLimitState && openai.IsImageRateLimitError(statusCode, responseBody) {
		if s.healthObserver != nil {
			_ = accountprovider.ObserveOpenAIImageRateLimit(stateCtx, s.healthObserver.Core, gatewayprovider.ExecutionRecord(account), statusCode, headers, responseBody)

		}
		return decision
	}
	if s.healthObserver != nil && len(canonicalModel) > 0 &&
		gatewayprovider.ObserveExecutionModelFailure(stateCtx, s.healthObserver, account, canonicalModel[0], statusCode,
			responseBody) {
		decision.StopScheduling = true
		return decision
	}
	// 普通账号先保留模型不存在等精确处理，再应用管理员临时规则。
	// 已知模型的规则只暂停账号与模型组合；模型未知时仍同步整号运行时阻断。
	if s.healthObserver != nil && statusCode != http.StatusUnauthorized &&
		!account.View().IsPoolMode() && !account.View().IsCustomErrorCodesEnabled() &&
		gatewayprovider.ObserveExecutionTemporaryFailure(stateCtx, s.healthObserver, account, statusCode, responseBody,
			canonicalModel...) {
		decision.Policy = accountcore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if len(canonicalModel) == 0 || strings.TrimSpace(canonicalModel[0]) == "" {
			s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		}
		return decision
	}
	if statusCode == http.StatusTooManyRequests && s.healthObserver != nil && len(canonicalModel) > 0 &&
		gatewayprovider.ObserveExecutionSparkLimit(stateCtx, s.healthObserver, account, canonicalModel[0], statusCode,
			headers, responseBody) {
		return decision
	}
	if suppressDefaultRateLimitState && statusCode == http.StatusTooManyRequests {
		return decision
	}
	if statusCode == http.StatusTooManyRequests {
		s.markOpenAIOAuth429RateLimited(stateCtx, account, headers, responseBody)
	}
	if s.healthObserver == nil {
		return decision
	}
	decision.StopScheduling = gatewayprovider.ApplyDefaultExecutionHealth(stateCtx, s.healthObserver, account, statusCode, headers, responseBody)

	modelTempMatched := statusCode != http.StatusUnauthorized && requeststate.HealthModel(stateCtx, nil) != "" &&
		len(accountcore.MatchTempUnschedulableRules(gatewayprovider.ExecutionRecord(account), statusCode, responseBody)) > 0
	if decision.StopScheduling && !modelTempMatched {
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
	}
	// pool 模式可重试的上游错误已受请求级同账号重试预算约束；若在此记录通用的
	// 账号+模型瞬态冷却，会在预算用完前阻止下一次已获准的重试。
	poolModeRetryable := account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(statusCode)
	if !decision.StopScheduling && account.Record.Platform == capability.PlatformOpenAI && account.Record.Type == capability.AccountTypeAPIKey &&
		gatewayprovider.IsTransientAccountFailure(statusCode, responseBody) && !poolModeRetryable {
		model := ""
		if len(canonicalModel) > 0 {
			model = canonicalModel[0]
		}
		s.recordOpenAICompatibleModelTransientFailure(account, model)
	}
	return decision
}

func (s *OpenAIGatewayService) markOpenAIOAuth429RateLimited(ctx context.Context, account *gatewayprovider.ExecutionAccount, headers http.Header, responseBody []byte) {
	if s == nil || !isOpenAIOAuthAccount(account) {
		return
	}
	// Spark 影子：不按 /responses 429 的 global x-codex-* 信号做内存运行时熔断(同 handle429,外审第8轮 P1)。
	// 同时避免把 spark 的 429 计入全局 429 storm 计数(recordOpenAIOAuth429),否则会误伤母账号 failover 决策。
	if account.View().IsShadow() {
		return
	}
	s.recordOpenAIOAuth429()
	disposition, resetAt := classifyOpenAIOAuth429(headers, responseBody)
	if disposition == openAIOAuth429Transient && s.openAIOAuth429RetryWindowActive(account) {
		return
	}

	cooldownUntil := time.Now().Add(openAIOAuth429FallbackCooldown)
	if resetAt != nil && resetAt.After(time.Now()) {
		cooldownUntil = *resetAt
	} else if s.healthObserver != nil {
		if cooldown, ok := s.healthObserver.Core.Fallback429Cooldown(ctx, gatewayprovider.ExecutionRecord(account)); ok && cooldown > 0 {
			cooldownUntil = time.Now().Add(cooldown)
		}
	}
	s.BlockAccountScheduling(account, cooldownUntil, "429")
	s.runtimeBlockState().ResetRetry(account.Record.ID)
}

func (s *OpenAIGatewayService) shouldRetryOpenAIOAuth429OnSameAccount(account *gatewayprovider.ExecutionAccount, statusCode int, shouldDisable bool) bool {
	return s.shouldRetryOpenAIOAuth429OnSameAccountWithResponse(account, statusCode, shouldDisable, nil, nil)
}

func (s *OpenAIGatewayService) shouldRetryOpenAIOAuth429OnSameAccountWithResponse(account *gatewayprovider.ExecutionAccount, statusCode int, shouldDisable bool, headers http.Header, responseBody []byte) bool {
	if shouldDisable || statusCode != http.StatusTooManyRequests || !isOpenAIOAuthAccount(account) || account.View().IsShadow() {
		return false
	}
	disposition, _ := classifyOpenAIOAuth429(headers, responseBody)
	if disposition != openAIOAuth429Transient {
		return false
	}
	// markOpenAIOAuth429RateLimited parks the account once the window expires.
	// Do not accidentally create a fresh window after that transition.
	if s.isOpenAIAccountRuntimeBlocked(account) {
		return false
	}
	return s.openAIOAuth429RetryWindowActive(account)
}

// ShouldRetryOpenAIOAuth429 向账号健康观测提供同账号重试判断，延迟持久化账号
// cooldown until the gateway's same-account retry window is exhausted.
func (s *OpenAIGatewayService) ShouldRetryOpenAIOAuth429(value *gatewayprovider.ExecutionAccount, headers http.Header, body []byte) bool {
	if s == nil {
		return false
	}
	return accountprovider.CanRetryOpenAI429(s.runtimeBlockState(), gatewayprovider.ExecutionRecord(value), headers, body)
}

func (s *OpenAIGatewayService) openAIOAuth429RetryWindowActive(account *gatewayprovider.ExecutionAccount) bool {
	if s == nil || !isOpenAIOAuthAccount(account) || account.View().IsShadow() {
		return false
	}
	return s.runtimeBlockState().RetryWindowActive(account.Record.ID)
}

func (s *OpenAIGatewayService) openAIOAuth429RetryDeadline(account *gatewayprovider.ExecutionAccount) time.Time {
	if s == nil || !isOpenAIOAuthAccount(account) || account.View().IsShadow() {
		return time.Time{}
	}
	return s.runtimeBlockState().RetryDeadline(account.Record.ID)
}

func openAIOAuth429SameAccountRetryDelay(headers http.Header, deadline time.Time) time.Duration {
	delay := openAIOAuth429RetryDelay
	now := time.Now()
	if resetAt := openai.ParseRetryAfterResetTime(headers, now); resetAt != nil && resetAt.After(now) {
		delay = resetAt.Sub(now)
	}
	if delay > openAIOAuth429MaxRetryDelay {
		delay = openAIOAuth429MaxRetryDelay
	}
	if remaining := time.Until(deadline); !deadline.IsZero() && delay > remaining {
		delay = remaining
	}
	if delay < 0 {
		return 0
	}
	return delay
}

func (s *OpenAIGatewayService) BlockAccountScheduling(account *gatewayprovider.ExecutionAccount, until time.Time, reason string) {
	if s == nil || !isOpenAIAccount(account) {
		return
	}
	s.runtimeBlockState().Block(account.Record.ID, until, reason)
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlock(id int64) {
	if s != nil {
		s.runtimeBlockState().ClearAccountSchedulingBlock(id)
	}
}

// ManagedRecoveryFence 复用原代次，不为手动恢复安装另一套运行状态。
func (s *OpenAIGatewayService) ManagedRecoveryFence(id int64) uint64 {
	if s == nil {
		return 0
	}
	return s.runtimeBlockState().ManagedRecoveryFence(id)
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlockIfFence(id int64, expected uint64) bool {
	return s != nil && s.runtimeBlockState().ClearAccountSchedulingBlockIfFence(id, expected)
}

func (s *OpenAIGatewayService) isOpenAIAccountRuntimeBlocked(account *gatewayprovider.ExecutionAccount) bool {
	if s == nil || !isOpenAIAccount(account) {
		return false
	}
	return s.runtimeBlockState().Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(gatewayprovider.ExecutionRecord(account)) })
}

func (s *OpenAIGatewayService) getOpenAIAccountModelTransientState() *accountcore.ModelTransientState {
	if s == nil {
		return nil
	}
	s.openaiModelTransientOnce.Do(func() {
		if s.openaiModelTransient == nil {
			s.openaiModelTransient = accountcore.NewModelTransientState(0)
		}
	})
	return s.openaiModelTransient
}

func openAIAccountModelTransientModel(canonicalModel string) string {
	return accountcore.NormalizeTransientModel(canonicalModel)
}

func (s *OpenAIGatewayService) recordOpenAIAccountModelTransientFailure(account *gatewayprovider.ExecutionAccount, canonicalModel string, now time.Time) accountcore.ModelTransientDecision {
	if s == nil || account == nil {
		return accountcore.ModelTransientDecision{}
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return accountcore.ModelTransientDecision{}
	}
	return state.RecordFailure(account.Record.ID, openAIAccountModelTransientModel(canonicalModel), now)
}

// recordOpenAICompatibleModelTransientFailure 统一记录 OpenAI 兼容平台的账号与模型瞬态失败，
// 调用方必须传入已经完成账号映射和平台规范化的最终上游模型。
func (s *OpenAIGatewayService) recordOpenAICompatibleModelTransientFailure(account *gatewayprovider.ExecutionAccount, canonicalModel string) {
	decision := s.recordOpenAIAccountModelTransientFailure(account, canonicalModel, time.Now())
	if decision.FailureStreak == 0 {
		return
	}
	slog.Warn("openai_model_transient_state",
		"account_id", account.Record.ID,
		"platform", account.Record.Platform,
		"model", openAIAccountModelTransientModel(canonicalModel),
		"failure_streak", decision.FailureStreak,
		"cooldown_ms", decision.Cooldown.Milliseconds(),
		"block_scope", "account_model",
	)
}

func (s *OpenAIGatewayService) isOpenAIAccountModelRuntimeBlocked(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if s == nil || account == nil {
		return false
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return false
	}
	canonicalModel := gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(requestedModel)
	return state.IsBlocked(account.Record.ID, openAIAccountModelTransientModel(canonicalModel), time.Now())
}

func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlocked(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	return s != nil && (s.isOpenAIAccountRuntimeBlocked(account) || s.isOpenAIAccountModelRuntimeBlocked(account, requestedModel))
}

func (s *OpenAIGatewayService) recordOpenAIOAuth429() {
	if s == nil {
		return
	}
	now := time.Now()
	windowStart := s.openaiOAuth429WindowStartUnixNano.Load()
	if windowStart == 0 || now.Sub(time.Unix(0, windowStart)) >= openAIOAuth429StormWindow {
		if s.openaiOAuth429WindowStartUnixNano.CompareAndSwap(windowStart, now.UnixNano()) {
			s.openaiOAuth429WindowCount.Store(1)
			return
		}
	}
	s.openaiOAuth429WindowCount.Add(1)
}

func (s *OpenAIGatewayService) ShouldStopOpenAIOAuth429Failover(account *gatewayprovider.ExecutionAccount, statusCode int, failedSwitches int, state *failover.OAuth429State) bool {
	return failover.StopOAuth429(failover.OAuth429Account{OpenAI: isOpenAIOAuthAccount(account), Grok: isGrokOAuthAccount(account)}, statusCode, failedSwitches, state)
}

// 旧重试策略只读取统一窗口观测，仍由调用方决定是否继续。
func classifyOpenAIOAuth429(headers http.Header, body []byte) (accountcore.OpenAI429Disposition, *time.Time) {
	return accountprovider.ClassifyOpenAI429(headers, body)
}
