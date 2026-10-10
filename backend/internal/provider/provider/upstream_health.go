package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	anthropicupstream "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/jev"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
)

// HealthObservation 固化本次错误的模型与端点意图，不从隐式业务 Context 回读。
type HealthObservation struct {
	Status         int
	Headers        http.Header
	Body           []byte
	Model          string
	ModelProvided  bool
	EffectiveModel string
	Thinking       *bool
	ImagesEndpoint bool
}

// UpstreamHealth 组合供应商观测和提供商健康状态处理。
type UpstreamHealth struct {
	Core   *providercore.HealthService
	Team   *providercore.TeamLinkedHealth
	Limits *RateLimitObserver
	Models *ModelHealth
}

// QuotaWindowObservation 保存供应商解析出的配额窗口，provider 执行健康状态规则。
func QuotaWindowObservation(value *anthropicupstream.WindowLimit) *providercore.QuotaWindowObservation {
	if value == nil {
		return nil
	}
	return &providercore.QuotaWindowObservation{Window: value.Window, ResetAt: value.ResetAt, FiveHourReset: value.FiveHourReset, Reason: value.Reason}
}

// SessionWindowObservation 将会话响应头转换为观测字段。
func SessionWindowObservation(headers http.Header) providercore.SessionWindowObservation {
	return providercore.SessionWindowObservation{Status: headers.Get("anthropic-ratelimit-unified-5h-status"), Reset: headers.Get("anthropic-ratelimit-unified-5h-reset"), Passive: anthropicupstream.PassiveUsageFields(headers)}
}

// ClientRejectionObservation 按关键词和 detail.code 分类客户端拒绝错误。
func ClientRejectionObservation(message string, body []byte) providercore.ClientRejectionObservation {
	lower := strings.ToLower(message)
	return providercore.ClientRejectionObservation{Message: message, OrganizationDisabled: strings.Contains(lower, "organization has been disabled"), CreditBalanceExhausted: strings.Contains(lower, "credit balance"), IdentityVerificationRequired: strings.Contains(lower, "identity verification is required"), WorkspaceDeactivated: gjson.GetBytes(body, "detail.code").String() == "deactivated_workspace"}
}

// ForbiddenObservation 解析拒绝原因、验证链接和并发限制消息。
func ForbiddenObservation(value *providercore.Record, message string, body []byte) providercore.ForbiddenObservation {
	kind := antigravity.ClassifyForbiddenType(string(body))
	url := ""
	if kind == providercore.ForbiddenTypeValidation {
		url = antigravity.ExtractValidationURL(string(body))
	}
	return providercore.ForbiddenObservation{Message: message, Body: body, HTML: upstream.IsHTMLResponse(body), Kind: kind, ValidationURL: url, ConcurrentRequestLimited: CNConcurrencyLimit403(value, message), ConcurrencyReason: "cn_concurrency_limit: " + kimi.ConcurrentRequestLimitMessage}
}

// CNConcurrencyLimit403 检查 Kimi 提供商是否返回并发限制消息。
func CNConcurrencyLimit403(value *providercore.Record, message string) bool {
	return value != nil && value.Platform == providercore.PlatformKimi && kimi.IsConcurrencyLimitMessage(message)
}

// LogAPIKeyHealthWarning 将 API Key 健康事件写入应用日志。
func LogAPIKeyHealthWarning(message string, fields ...any) {
	attrs := make([]zap.Field, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if ok {
			attrs = append(attrs, zap.Any(key, fields[i+1]))
		}
	}
	logging.L().Warn(message, attrs...)
}

// UnauthorizedObservation 提取供应商认证失败和令牌撤销信息。
func UnauthorizedObservation(body []byte, message string) providercore.UnauthorizedObservation {
	code := upstream.ExtractErrorCode(body)
	return providercore.UnauthorizedObservation{Message: message, TokenRevoked: code == "token_invalidated" || code == "token_revoked", PermanentlyUnauthorized: gjson.GetBytes(body, "detail").String() == "Unauthorized"}
}

func (s *UpstreamHealth) CheckErrorPolicy(ctx context.Context, provider *providercore.Record, observation HealthObservation) providercore.ErrorPolicyResult {
	statusCode, responseBody := observation.Status, observation.Body
	if provider == nil {
		return providercore.ErrorPolicyNone
	}
	return s.Core.CheckErrorPolicy(ctx, provider, statusCode, responseBody, observation.EffectiveModel, provider == nil || provider.Platform != capability.PlatformAntigravity)
}

// ApplyExplicitErrorPolicy 检查并应用管理员配置的错误策略。
// 自定义错误码命中后写入提供商错误，后续跳过内置状态码分支。
func (s *UpstreamHealth) ApplyExplicitErrorPolicy(ctx context.Context, provider *providercore.Record, observation HealthObservation) providercore.ErrorPolicyResult {
	statusCode, responseBody := observation.Status, observation.Body
	result := s.CheckErrorPolicy(ctx, provider, observation)
	if result != providercore.ErrorPolicyCustomMatched || provider == nil {
		return result
	}
	message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)))
	if message == "" {
		message = "Custom error code triggered"
	}
	s.Core.ApplyCustomErrorCode(ctx, provider, statusCode, message)
	return result
}

// ApplyUpstreamError 先执行配置的错误策略，非池模式下再执行平台默认处理。
func (s *UpstreamHealth) ApplyUpstreamError(ctx context.Context, provider *providercore.Record, observation HealthObservation) providercore.UpstreamErrorDecision {
	statusCode, responseBody := observation.Status, observation.Body
	// Team 联动熔断先于池模式、自定义错误码和临时不可调度检查。
	// HandleWorkspaceDeactivated 会去掉 fastpath 入口的重复调用。
	s.Team.HandleWorkspaceDeactivated(ctx, provider, statusCode == http.StatusPaymentRequired && ClientRejectionObservation("", responseBody).WorkspaceDeactivated)
	policy := providercore.ErrorPolicyNone
	// 池模式和自定义错误码先执行配置策略，其余提供商进入平台默认处理。
	if provider != nil && (provider.IsPoolMode() || provider.IsCustomErrorCodesEnabled()) {
		policy = s.ApplyExplicitErrorPolicy(ctx, provider, observation)
	}
	decision := providercore.UpstreamErrorDecision{Policy: policy}
	switch policy {
	case providercore.ErrorPolicyCustomMatched, providercore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case providercore.ErrorPolicyCustomSkipped, providercore.ErrorPolicyPoolBypassed:
		return decision
	}
	decision.StopScheduling = s.HandleDefault(ctx, provider, observation)
	return decision
}

// HandleDefault 在非池模式且配置策略未命中时，更新平台默认的提供商状态。
func (s *UpstreamHealth) HandleDefault(ctx context.Context, provider *providercore.Record, observation HealthObservation) (shouldDisable bool) {
	statusCode, headers, responseBody := observation.Status, observation.Headers, observation.Body
	if provider == nil {
		return false
	}
	// Jev 的限流和过载响应按 Retry-After 设置提供商冷却。
	if provider.Platform == capability.PlatformJev && (statusCode == http.StatusTooManyRequests || statusCode == 529) {
		if reset := jev.RetryAfterResetTime(headers, time.Now()); reset != nil {
			s.Core.ApplyObservedRateLimit(ctx, provider, *reset)
			return false
		}
		// 没有重试时间的 529 先检查管理员配置的临时暂停规则。
		if statusCode == 529 && s.Core.TryTempUnschedulable(ctx, provider, statusCode, responseBody, true, observation.EffectiveModel) {
			return true
		}
	}

	if statusCode == 529 {
		s.Core.ApplyOverload(ctx, provider)
		return false
	}

	if observation.ModelProvided && s.Models.Observe(ctx, provider, observation.Model, statusCode, responseBody, observation.Thinking, observation.ImagesEndpoint) {
		return true
	}

	// Anthropic 官方 5h/7d 窗口耗尽先于本地临时不可调度规则处理。
	// 否则宽泛的 429 关键字规则可能把多小时/多天冷却误缩短成本地暂停时间。
	if statusCode == http.StatusTooManyRequests && provider.Platform == capability.PlatformAnthropic {
		// 7d_oi 是 Fable 模型专属的 7d 窗口：只标记模型级限流，提供商对其他模型仍可调度。
		fableLimited := s.observeFableWindow(ctx, provider, headers)
		if s.observeExhaustedWindow(ctx, provider, headers) {
			return false
		}
		if fableLimited {
			return false
		}
	}

	// 529 表示全局上游过载，先记录全局过载冷却。
	if statusCode == 529 {
		s.Core.ApplyOverload(ctx, provider)
		return false
	}
	// 非池提供商优先处理具体状态，401 进入认证刷新和默认冷却流程。
	if statusCode != http.StatusUnauthorized && s.Core.TryTempUnschedulable(ctx, provider, statusCode, responseBody, provider.Platform != capability.PlatformAntigravity, observation.EffectiveModel) {
		return true
	}

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	if upstreamMsg != "" {
		upstreamMsg = logredact.TruncateLine([]byte(upstreamMsg), 512)
	}

	switch statusCode {
	case 400:
		shouldDisable = s.Core.ApplyBadRequest(ctx, provider, ClientRejectionObservation(upstreamMsg, responseBody))

	case 401:
		shouldDisable = s.Core.ApplyUnauthorized(ctx, provider, UnauthorizedObservation(responseBody, upstreamMsg))

	case 402:
		shouldDisable = s.Core.ApplyPaymentRequired(ctx, provider, ClientRejectionObservation(upstreamMsg, responseBody))

	case 403:
		logging.LegacyPrintf(
			"service.ratelimit",
			"[HandleUpstreamErrorRaw] provider_id=%d platform=%s type=%s status=403 request_id=%s cf_ray=%s upstream_msg=%s raw_body=%s",
			provider.ID,
			provider.Platform,
			provider.Type,
			strings.TrimSpace(headers.Get("x-request-id")),
			strings.TrimSpace(headers.Get("cf-ray")),
			upstreamMsg,
			logredact.TruncateLine(responseBody, 1024),
		)
		shouldDisable = s.Core.ApplyForbiddenObservation(ctx, provider, ForbiddenObservation(provider, upstreamMsg, responseBody))
	case 429:
		s.Limits.Observe429(ctx, provider, headers, responseBody)
		shouldDisable = false
	case 529:
		s.Core.ApplyOverload(ctx, provider)
		shouldDisable = false
	default:
		if statusCode >= 500 {
			// 未启用自定义错误码时：仅记录 5xx 错误
			slog.Warn("provider_upstream_error", "provider_id", provider.ID, "status_code", statusCode)
			shouldDisable = false
		}
	}

	return shouldDisable
}

func (s *UpstreamHealth) observeFableWindow(ctx context.Context, value *providercore.Record, headers http.Header) bool {
	limit := anthropicupstream.SelectFableWindowLimit(headers, time.Now())
	if limit == nil {
		return false
	}
	return s.Core.ApplyFableQuotaWindow(ctx, value, QuotaWindowObservation(limit), anthropicupstream.PassiveUsageFields(headers))
}

func (s *UpstreamHealth) observeExhaustedWindow(ctx context.Context, value *providercore.Record, headers http.Header) bool {
	now := time.Now()
	return s.Core.ApplyExhaustedQuotaWindow(ctx, value, QuotaWindowObservation(anthropicupstream.SelectExhaustedWindow(headers, now)), now)
}
