package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	anthropicupstream "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func (s *UpstreamHealth) CheckErrorPolicy(ctx context.Context, account *accountcore.Record, observation HealthObservation) accountcore.ErrorPolicyResult {
	statusCode, responseBody := observation.Status, observation.Body
	if account == nil {
		return accountcore.ErrorPolicyNone
	}
	return s.Core.CheckErrorPolicy(ctx, account, statusCode, responseBody, observation.EffectiveModel, account == nil || account.Platform != capability.PlatformAntigravity)
}

// ApplyExplicitErrorPolicy 检查并应用管理员显式配置的错误策略。
// 自定义错误码命中时在这里统一写入账号错误，避免 400、429、529 被内置分支覆盖。
func (s *UpstreamHealth) ApplyExplicitErrorPolicy(ctx context.Context, account *accountcore.Record, observation HealthObservation) accountcore.ErrorPolicyResult {
	statusCode, responseBody := observation.Status, observation.Body
	result := s.CheckErrorPolicy(ctx, account, observation)
	if result != accountcore.ErrorPolicyCustomMatched || account == nil {
		return result
	}
	message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)))
	if message == "" {
		message = "Custom error code triggered"
	}
	s.Core.ApplyCustomErrorCode(ctx, account, statusCode, message)
	return result
}

// ApplyUpstreamError 先执行显式策略，再在非池模式下执行平台默认账号状态处理。
func (s *UpstreamHealth) ApplyUpstreamError(ctx context.Context, account *accountcore.Record, observation HealthObservation) accountcore.UpstreamErrorDecision {
	statusCode, responseBody := observation.Status, observation.Body
	// Team 联动熔断必须先于池模式、自定义错误码和临时不可调度的各类早退；
	// fastpath 入口的重复调用由方法内去重吸收。
	s.Team.HandleWorkspaceDeactivated(ctx, account, statusCode == http.StatusPaymentRequired && ClientRejectionObservation("", responseBody).WorkspaceDeactivated)
	policy := accountcore.ErrorPolicyNone
	// 非池账号未启用自定义错误码时，模型不存在和官方硬窗口等精确状态必须
	// 先于宽泛的临时不可调度规则；池模式和自定义错误码仍作为前置显式策略。
	if account != nil && (account.IsPoolMode() || account.IsCustomErrorCodesEnabled()) {
		policy = s.ApplyExplicitErrorPolicy(ctx, account, observation)
	}
	decision := accountcore.UpstreamErrorDecision{Policy: policy}
	switch policy {
	case accountcore.ErrorPolicyCustomMatched, accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}
	decision.StopScheduling = s.HandleDefault(ctx, account, observation)
	return decision
}

// handleDefaultUpstreamError 只处理非池模式、未命中显式策略时的平台默认账号状态。
func (s *UpstreamHealth) HandleDefault(ctx context.Context, account *accountcore.Record, observation HealthObservation) (shouldDisable bool) {
	statusCode, headers, responseBody := observation.Status, observation.Headers, observation.Body
	if account == nil {
		return false
	}

	if statusCode == 529 {
		s.Core.ApplyOverload(ctx, account)
		return false
	}

	if observation.ModelProvided && s.Models.Observe(ctx, account, observation.Model, statusCode, responseBody, observation.Thinking, observation.ImagesEndpoint) {
		return true
	}

	// Anthropic 官方 5h/7d 窗口耗尽属于账号级硬限流，必须先于本地临时不可调度规则处理。
	// 否则宽泛的 429 关键字规则可能把多小时/多天冷却误缩短成本地暂停时间。
	if statusCode == http.StatusTooManyRequests && account.Platform == capability.PlatformAnthropic {
		// 7d_oi 是 Fable 模型专属的 7d 窗口：只标记模型级限流，账号对其他模型仍可调度。
		fableLimited := s.observeFableWindow(ctx, account, headers)
		if s.observeExhaustedWindow(ctx, account, headers) {
			return false
		}
		if fableLimited {
			return false
		}
	}

	// 529 表示全局上游过载，必须先记录全局过载冷却，不能被账号级临时规则抢先消费。
	if statusCode == 529 {
		s.Core.ApplyOverload(ctx, account)
		return false
	}
	// 非池账号保留既有精确状态优先级；401 继续进入认证刷新与默认冷却逻辑。
	if statusCode != http.StatusUnauthorized && s.Core.TryTempUnschedulable(ctx, account, statusCode, responseBody, account.Platform != capability.PlatformAntigravity, observation.EffectiveModel) {
		return true
	}

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	if upstreamMsg != "" {
		upstreamMsg = logredact.TruncateLine([]byte(upstreamMsg), 512)
	}

	switch statusCode {
	case 400:
		shouldDisable = s.Core.ApplyBadRequest(ctx, account, ClientRejectionObservation(upstreamMsg, responseBody))

	case 401:
		shouldDisable = s.Core.ApplyUnauthorized(ctx, account, UnauthorizedObservation(responseBody, upstreamMsg))

	case 402:
		shouldDisable = s.Core.ApplyPaymentRequired(ctx, account, ClientRejectionObservation(upstreamMsg, responseBody))

	case 403:
		logging.LegacyPrintf(
			"service.ratelimit",
			"[HandleUpstreamErrorRaw] account_id=%d platform=%s type=%s status=403 request_id=%s cf_ray=%s upstream_msg=%s raw_body=%s",
			account.ID,
			account.Platform,
			account.Type,
			strings.TrimSpace(headers.Get("x-request-id")),
			strings.TrimSpace(headers.Get("cf-ray")),
			upstreamMsg,
			logredact.TruncateLine(responseBody, 1024),
		)
		shouldDisable = s.Core.ApplyForbiddenObservation(ctx, account, ForbiddenObservation(account, upstreamMsg, responseBody))
	case 429:
		s.Limits.Observe429(ctx, account, headers, responseBody)
		shouldDisable = false
	case 529:
		s.Core.ApplyOverload(ctx, account)
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

// UpstreamHealth 只组合供应商观测与账号健康核心，不持有旧服务、配置或存储客户端。
type UpstreamHealth struct {
	Core   *accountcore.HealthService
	Team   *accountcore.TeamLinkedHealth
	Limits *RateLimitObserver
	Models *ModelHealth
}

func (s *UpstreamHealth) observeFableWindow(ctx context.Context, value *accountcore.Record, headers http.Header) bool {
	limit := anthropicupstream.SelectFableWindowLimit(headers, time.Now())
	if limit == nil {
		return false
	}
	return s.Core.ApplyFableQuotaWindow(ctx, value, QuotaWindowObservation(limit), anthropicupstream.PassiveUsageFields(headers))
}
func (s *UpstreamHealth) observeExhaustedWindow(ctx context.Context, value *accountcore.Record, headers http.Header) bool {
	now := time.Now()
	return s.Core.ApplyExhaustedQuotaWindow(ctx, value, QuotaWindowObservation(anthropicupstream.SelectExhaustedWindow(headers, now)), now)
}
