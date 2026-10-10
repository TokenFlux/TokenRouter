package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// OpenAIResponseHealth 共用调度运行状态，处理 HTTP 及流内错误引起的提供商状态更新。
type OpenAIResponseHealth struct {
	Health         *UpstreamHealth
	Runtime        *providercore.RuntimeBlockState
	ModelTransient *providercore.ModelTransientState
	Deferred       *providercore.DeferredService
}

// OpenAIResponseHealthInput 固化网关已识别的请求范围及模型观测，提供商层不回读 HTTP context。
type OpenAIResponseHealthInput struct {
	Observation                                               HealthObservation
	Models                                                    []string
	SuppressDefaultRateLimit                                  bool
	ContentRejected, RequestScoped, SelfBuiltImage, Transient bool
}

func (s *OpenAIResponseHealth) Apply(ctx context.Context, provider *providercore.Record, input OpenAIResponseHealthInput) providercore.UpstreamErrorDecision {
	statusCode, headers, responseBody := input.Observation.Status, input.Observation.Headers, input.Observation.Body
	canonicalModel, suppressDefaultRateLimitState := input.Models, input.SuppressDefaultRateLimit

	customStatusMatched := provider != nil && provider.IsCustomErrorCodesEnabled() && provider.ShouldHandleErrorCode(statusCode)
	if input.ContentRejected {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	if input.RequestScoped && !customStatusMatched {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	// 任意非 2xx 上游响应都表示模型请求已实际发送。
	if s != nil {
		s.recordActivity(provider)
	}
	// 容量降载由请求级重试处理，
	// 提供商保持可调度状态。
	if provider != nil && provider.Platform == capability.PlatformOpenAI && openai.IsOpenAIRequestScopedCapacityShed("", responseBody) {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	stateCtx, cancel := ProviderStateContext(ctx)
	defer cancel()
	if provider != nil && provider.Platform == capability.PlatformOpenAI && openai.IsOpenAIHTTPUpstreamAccessStateError(0, "", responseBody) {
		message := "OpenAI upstream provider or workspace is unavailable"
		if upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)); upstreamMsg != "" {
			message = upstreamMsg
		}
		if s != nil && s.Health != nil {
			s.Health.Core.ApplyAuthenticationFailure(stateCtx, providercore.CloneRecord(provider), message)
		}
		if s != nil {
			s.Runtime.BlockProviderScheduling(provider, time.Time{}, "openai_access_state")
		}
		return providercore.UpstreamErrorDecision{StopScheduling: true}
	}

	// 自构造图片请求始终携带匹配的 image_generation 工具，因此 400 "tool choice not found in 'tools'"
	// 表示上游撤销了该提供商的图片能力。此处必须受自构造标记保护：透传客户端自行控制
	// tools/tool_choice，否则可能误伤健康提供商。
	if input.SelfBuiltImage && openai.IsImageCapabilityLossError(statusCode, responseBody) {
		if s != nil && s.Health != nil {
			_ = ObserveOpenAIImageCapabilityLoss(stateCtx, s.Health.Core, providercore.CloneRecord(provider), statusCode, responseBody)
		}
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}

	if s == nil || provider == nil {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	// Team 联动熔断必须先于 model-not-found 与提供商级临时不可调度规则的早退。
	if s.Health != nil {
		s.Health.Team.HandleWorkspaceDeactivated(stateCtx, providercore.CloneRecord(provider), statusCode == http.StatusPaymentRequired && ClientRejectionObservation("", responseBody).WorkspaceDeactivated)
	}
	observation := input.Observation
	observation.Headers = nil
	decision := providercore.ErrorDecisionWithoutPersistence(provider, statusCode)
	if s.Health != nil {
		if provider.IsPoolMode() || provider.IsCustomErrorCodesEnabled() {
			decision.Policy = s.Health.ApplyExplicitErrorPolicy(stateCtx, providercore.CloneRecord(provider), observation)
			decision.StopScheduling = decision.Policy == providercore.ErrorPolicyCustomMatched || decision.Policy == providercore.ErrorPolicyTempUnscheduled
		} else {
			decision = providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case providercore.ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.Runtime.BlockProviderScheduling(provider, time.Time{}, "upstream_disable")
		return decision
	case providercore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case providercore.ErrorPolicyCustomSkipped, providercore.ErrorPolicyPoolBypassed:
		return decision
	}

	if !suppressDefaultRateLimitState && openai.IsImageRateLimitError(statusCode, responseBody) {
		if s.Health != nil {
			_ = ObserveOpenAIImageRateLimit(stateCtx, s.Health.Core, providercore.CloneRecord(provider), statusCode, headers, responseBody)
		}
		return decision
	}
	if s.Health != nil && len(canonicalModel) > 0 &&
		s.Health.Models.Observe(stateCtx, providercore.CloneRecord(provider), canonicalModel[0], statusCode, responseBody, observation.Thinking, observation.ImagesEndpoint) {
		decision.StopScheduling = true
		return decision
	}
	// 普通提供商先保留模型不存在等精确处理，再应用管理员临时规则。
	// 已知模型的规则只暂停提供商与模型组合；模型未知时仍同步整号运行时阻断。
	if s.Health != nil && statusCode != http.StatusUnauthorized &&
		!provider.IsPoolMode() && !provider.IsCustomErrorCodesEnabled() &&
		s.Health.Core.HandleTempUnschedulable(stateCtx, providercore.CloneRecord(provider), statusCode, responseBody, observation.EffectiveModel) {
		decision.Policy = providercore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if len(canonicalModel) == 0 || strings.TrimSpace(canonicalModel[0]) == "" {
			s.Runtime.BlockProviderScheduling(provider, time.Time{}, "upstream_disable")
		}
		return decision
	}
	if statusCode == http.StatusTooManyRequests && s.Health != nil && len(canonicalModel) > 0 &&
		s.Health.Models.ObserveSparkRateLimit(stateCtx, providercore.CloneRecord(provider), canonicalModel[0], statusCode, headers, responseBody, observation.Thinking) {
		return decision
	}
	if suppressDefaultRateLimitState && statusCode == http.StatusTooManyRequests {
		return decision
	}
	if statusCode == http.StatusTooManyRequests {
		s.markOAuth429(stateCtx, provider, headers, responseBody)
	}
	if s.Health == nil {
		return decision
	}
	defaultObservation := observation
	defaultObservation.Model, defaultObservation.ModelProvided = "", false
	defaultObservation.Headers = headers
	updated := providercore.CloneRecord(provider)
	decision.StopScheduling = s.Health.HandleDefault(stateCtx, updated, defaultObservation)
	provider.Credentials, provider.Extra = updated.Credentials, updated.Extra

	modelTempMatched := statusCode != http.StatusUnauthorized && observation.EffectiveModel != "" &&
		len(providercore.MatchTempUnschedulableRules(providercore.CloneRecord(provider), statusCode, responseBody)) > 0
	if decision.StopScheduling && !modelTempMatched {
		s.Runtime.BlockProviderScheduling(provider, time.Time{}, "upstream_disable")
	}
	// pool 模式可重试的上游错误已受请求级同提供商重试预算约束；若在此记录通用的
	// 提供商+模型瞬态冷却，会在预算用完前阻止下一次已获准的重试。
	poolModeRetryable := provider.IsPoolMode() && provider.IsPoolModeRetryableStatus(statusCode)
	if !decision.StopScheduling && provider.Platform == capability.PlatformOpenAI && provider.Type == capability.ProviderTypeAPIKey &&
		input.Transient && !poolModeRetryable {
		model := ""
		if len(canonicalModel) > 0 {
			model = canonicalModel[0]
		}
		s.RecordModelFailure(provider, model)
	}
	return decision
}

// recordActivity 仅记录真正到达上游的 Ollama Cloud 请求，继续由 Deferred 合并写入。
func (s *OpenAIResponseHealth) recordActivity(value *providercore.Record) {
	if s.Deferred != nil && value != nil && providercore.IsOllamaCloudUsageProvider(providercore.CloneRecord(value)) {
		s.Deferred.ScheduleLastUsedUpdate(value.ID)
	}
}

func (s *OpenAIResponseHealth) markOAuth429(ctx context.Context, value *providercore.Record, headers http.Header, body []byte) {
	if s == nil || value == nil || !value.IsOpenAIOAuthLike() || value.IsShadow() {
		return
	}
	disposition, reset := ClassifyOpenAI429(headers, body)
	if disposition == providercore.OpenAI429Transient && s.Runtime.RetryWindowActive(value.ID) {
		return
	}
	until := time.Now().Add(5 * time.Second)
	if reset != nil && reset.After(time.Now()) {
		until = *reset
	} else if s.Health != nil {
		if cooldown, ok := s.Health.Core.Fallback429Cooldown(ctx, providercore.CloneRecord(value)); ok && cooldown > 0 {
			until = time.Now().Add(cooldown)
		}
	}
	s.Runtime.BlockProviderScheduling(value, until, "429")
	s.Runtime.ResetRetry(value.ID)
}

// RetryOAuth429 在已停调、影子提供商或非瞬时 429 时拒绝重试。
func (s *OpenAIResponseHealth) RetryOAuth429(value *providercore.Record, status int, disabled bool, headers http.Header, body []byte) bool {
	if s == nil || disabled || status != http.StatusTooManyRequests {
		return false
	}
	return CanRetryOpenAI429(s.Runtime, value, headers, body)
}

func (s *OpenAIResponseHealth) RetryDeadline(value *providercore.Record) time.Time {
	if s == nil || value == nil || !value.IsOpenAIOAuthLike() || value.IsShadow() {
		return time.Time{}
	}
	return s.Runtime.RetryDeadline(value.ID)
}

// RecordModelFailure 只记录本次固化的模型，仍与生产选择器共用同一个状态表。
func (s *OpenAIResponseHealth) RecordModelFailure(value *providercore.Record, model string) {
	if s == nil || value == nil || s.ModelTransient == nil {
		return
	}
	model = providercore.NormalizeTransientModel(model)
	result := s.ModelTransient.RecordFailure(value.ID, model, time.Now())
	if result.FailureStreak == 0 {
		return
	}
	slog.Warn("openai_model_transient_state", "provider_id", value.ID, "platform", value.Platform, "model", model, "failure_streak", result.FailureStreak, "cooldown_ms", result.Cooldown.Milliseconds(), "block_scope", "provider_model")
}
