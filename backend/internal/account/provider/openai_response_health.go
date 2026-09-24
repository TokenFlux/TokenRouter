package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// OpenAIResponseHealth 复用选号的运行状态，统一处理 HTTP 和流内语义错误的账号副作用。
type OpenAIResponseHealth struct {
	Health         *UpstreamHealth
	Runtime        *accountcore.RuntimeBlockState
	ModelTransient *accountcore.ModelTransientState
	Deferred       *accountcore.DeferredService
}

// OpenAIResponseHealthInput 固化网关已识别的请求范围及模型观测，账号层不回读 HTTP context。
type OpenAIResponseHealthInput struct {
	Observation                                               HealthObservation
	Models                                                    []string
	SuppressDefaultRateLimit                                  bool
	ContentRejected, RequestScoped, SelfBuiltImage, Transient bool
}

func (s *OpenAIResponseHealth) Apply(ctx context.Context, account *accountcore.Record, input OpenAIResponseHealthInput) accountcore.UpstreamErrorDecision {
	statusCode, headers, responseBody := input.Observation.Status, input.Observation.Headers, input.Observation.Body
	canonicalModel, suppressDefaultRateLimitState := input.Models, input.SuppressDefaultRateLimit

	customStatusMatched := account != nil && account.IsCustomErrorCodesEnabled() && account.ShouldHandleErrorCode(statusCode)
	if input.ContentRejected {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	if input.RequestScoped && !customStatusMatched {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	// 任意非 2xx 上游响应都表示模型请求已实际发送。
	if s != nil {
		s.recordActivity(account)
	}
	// 容量降载只描述当前请求，不代表账号健康异常；交给请求级重试预算恢复，
	// 保持账号可调度，避免误写账号冷却状态。
	if account != nil && account.Platform == capability.PlatformOpenAI && openai.IsOpenAIRequestScopedCapacityShed("", responseBody) {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	stateCtx, cancel := AccountStateContext(ctx)
	defer cancel()
	if account != nil && account.Platform == capability.PlatformOpenAI && openai.IsOpenAIHTTPUpstreamAccessStateError(0, "", responseBody) {
		message := "OpenAI upstream account or workspace is unavailable"
		if upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)); upstreamMsg != "" {
			message = upstreamMsg
		}
		if s != nil && s.Health != nil {
			s.Health.Core.ApplyAuthenticationFailure(stateCtx, accountcore.CloneRecord(account), message)

		}
		if s != nil {
			s.Runtime.BlockAccountScheduling(account, time.Time{}, "openai_access_state")
		}
		return accountcore.UpstreamErrorDecision{StopScheduling: true}
	}

	// 自构造图片请求始终携带匹配的 image_generation 工具，因此 400 "tool choice not found in 'tools'"
	// 表示上游撤销了该账号的图片能力。此处必须受自构造标记保护：透传客户端自行控制
	// tools/tool_choice，否则可能误伤健康账号。
	if input.SelfBuiltImage && openai.IsImageCapabilityLossError(statusCode, responseBody) {
		if s != nil && s.Health != nil {
			_ = ObserveOpenAIImageCapabilityLoss(stateCtx, s.Health.Core, accountcore.CloneRecord(account), statusCode, responseBody)

		}
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}

	if s == nil || account == nil {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	// Team 联动熔断必须先于 model-not-found 与账户级临时不可调度规则的早退。
	if s.Health != nil {
		s.Health.Team.HandleWorkspaceDeactivated(stateCtx, accountcore.CloneRecord(account), statusCode == http.StatusPaymentRequired && ClientRejectionObservation("", responseBody).WorkspaceDeactivated)

	}
	observation := input.Observation
	observation.Headers = nil
	decision := accountcore.ErrorDecisionWithoutPersistence(account, statusCode)
	if s.Health != nil {
		if account.IsPoolMode() || account.IsCustomErrorCodesEnabled() {
			decision.Policy = s.Health.ApplyExplicitErrorPolicy(stateCtx, accountcore.CloneRecord(account), observation)
			decision.StopScheduling = decision.Policy == accountcore.ErrorPolicyCustomMatched || decision.Policy == accountcore.ErrorPolicyTempUnscheduled
		} else {
			decision = accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case accountcore.ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.Runtime.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		return decision
	case accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}

	if !suppressDefaultRateLimitState && openai.IsImageRateLimitError(statusCode, responseBody) {
		if s.Health != nil {
			_ = ObserveOpenAIImageRateLimit(stateCtx, s.Health.Core, accountcore.CloneRecord(account), statusCode, headers, responseBody)

		}
		return decision
	}
	if s.Health != nil && len(canonicalModel) > 0 &&
		s.Health.Models.Observe(stateCtx, accountcore.CloneRecord(account), canonicalModel[0], statusCode, responseBody, observation.Thinking, observation.ImagesEndpoint) {
		decision.StopScheduling = true
		return decision
	}
	// 普通账号先保留模型不存在等精确处理，再应用管理员临时规则。
	// 已知模型的规则只暂停账号与模型组合；模型未知时仍同步整号运行时阻断。
	if s.Health != nil && statusCode != http.StatusUnauthorized &&
		!account.IsPoolMode() && !account.IsCustomErrorCodesEnabled() &&
		s.Health.Core.HandleTempUnschedulable(stateCtx, accountcore.CloneRecord(account), statusCode, responseBody, observation.EffectiveModel) {
		decision.Policy = accountcore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if len(canonicalModel) == 0 || strings.TrimSpace(canonicalModel[0]) == "" {
			s.Runtime.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		}
		return decision
	}
	if statusCode == http.StatusTooManyRequests && s.Health != nil && len(canonicalModel) > 0 &&
		s.Health.Models.ObserveSparkRateLimit(stateCtx, accountcore.CloneRecord(account), canonicalModel[0], statusCode, headers, responseBody, observation.Thinking) {
		return decision
	}
	if suppressDefaultRateLimitState && statusCode == http.StatusTooManyRequests {
		return decision
	}
	if statusCode == http.StatusTooManyRequests {
		s.markOAuth429(stateCtx, account, headers, responseBody)
	}
	if s.Health == nil {
		return decision
	}
	defaultObservation := observation
	defaultObservation.Model, defaultObservation.ModelProvided = "", false
	defaultObservation.Headers = headers
	updated := accountcore.CloneRecord(account)
	decision.StopScheduling = s.Health.HandleDefault(stateCtx, updated, defaultObservation)
	account.Credentials, account.Extra = updated.Credentials, updated.Extra

	modelTempMatched := statusCode != http.StatusUnauthorized && observation.EffectiveModel != "" &&
		len(accountcore.MatchTempUnschedulableRules(accountcore.CloneRecord(account), statusCode, responseBody)) > 0
	if decision.StopScheduling && !modelTempMatched {
		s.Runtime.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
	}
	// pool 模式可重试的上游错误已受请求级同账号重试预算约束；若在此记录通用的
	// 账号+模型瞬态冷却，会在预算用完前阻止下一次已获准的重试。
	poolModeRetryable := account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode)
	if !decision.StopScheduling && account.Platform == capability.PlatformOpenAI && account.Type == capability.AccountTypeAPIKey &&
		input.Transient && !poolModeRetryable {
		model := ""
		if len(canonicalModel) > 0 {
			model = canonicalModel[0]
		}
		s.RecordModelFailure(account, model)
	}
	return decision

}

// recordActivity 仅记录真正到达上游的 Ollama Cloud 请求，继续由 Deferred 合并写入。
func (s *OpenAIResponseHealth) recordActivity(value *accountcore.Record) {
	if s.Deferred != nil && value != nil && accountcore.IsOllamaCloudUsageAccount(accountcore.CloneRecord(value)) {
		s.Deferred.ScheduleLastUsedUpdate(value.ID)
	}
}
func (s *OpenAIResponseHealth) markOAuth429(ctx context.Context, value *accountcore.Record, headers http.Header, body []byte) {
	if s == nil || value == nil || !value.IsOpenAIOAuthLike() || value.IsShadow() {
		return
	}
	disposition, reset := ClassifyOpenAI429(headers, body)
	if disposition == accountcore.OpenAI429Transient && s.Runtime.RetryWindowActive(value.ID) {
		return
	}
	until := time.Now().Add(5 * time.Second)
	if reset != nil && reset.After(time.Now()) {
		until = *reset
	} else if s.Health != nil {
		if cooldown, ok := s.Health.Core.Fallback429Cooldown(ctx, accountcore.CloneRecord(value)); ok && cooldown > 0 {
			until = time.Now().Add(cooldown)
		}
	}
	s.Runtime.BlockAccountScheduling(value, until, "429")
	s.Runtime.ResetRetry(value.ID)
}

// RetryOAuth429 保留已停调、影子与非 transient 429 的拒绝边界。
func (s *OpenAIResponseHealth) RetryOAuth429(value *accountcore.Record, status int, disabled bool, headers http.Header, body []byte) bool {
	if s == nil || disabled || status != http.StatusTooManyRequests {
		return false
	}
	return CanRetryOpenAI429(s.Runtime, value, headers, body)
}
func (s *OpenAIResponseHealth) RetryDeadline(value *accountcore.Record) time.Time {
	if s == nil || value == nil || !value.IsOpenAIOAuthLike() || value.IsShadow() {
		return time.Time{}
	}
	return s.Runtime.RetryDeadline(value.ID)
}

// RecordModelFailure 只记录本次固化的模型，仍与生产选择器共用同一个状态表。
func (s *OpenAIResponseHealth) RecordModelFailure(value *accountcore.Record, model string) {
	if s == nil || value == nil || s.ModelTransient == nil {
		return
	}
	model = accountcore.NormalizeTransientModel(model)
	result := s.ModelTransient.RecordFailure(value.ID, model, time.Now())
	if result.FailureStreak == 0 {
		return
	}
	slog.Warn("openai_model_transient_state", "account_id", value.ID, "platform", value.Platform, "model", model, "failure_streak", result.FailureStreak, "cooldown_ms", result.Cooldown.Milliseconds(), "block_scope", "account_model")
}
