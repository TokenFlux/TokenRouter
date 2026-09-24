package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokHealthInput 显式携带当前尝试的模型与技术观测，不读取网关 context 参数。
type GrokHealthInput struct {
	Observation     HealthObservation
	Models          []string
	QuotaModel      string
	TeamModel       string
	RequestScoped   bool
	ServerTransient bool
}

func firstGrokHealthModel(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return strings.TrimSpace(models[0])
}

// ObserveError 保留显式策略、精确模型状态、配额及默认冷却的原先后顺序。
func (s *GrokHealth) ObserveError(ctx context.Context, value *accountcore.Record, input GrokHealthInput) accountcore.UpstreamErrorDecision {
	statusCode, headers, responseBody := input.Observation.Status, input.Observation.Headers, input.Observation.Body
	canonicalModel := input.Models

	if s == nil || value == nil {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	if input.RequestScoped {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	if model := firstGrokHealthModel(canonicalModel); model != "" {
		canonicalModel = []string{s.NormalizeModel(value, model)}
	}
	stateCtx, cancel := grokStateContext(ctx)
	defer cancel()
	observation := input.Observation
	observation.Headers = nil
	observation.ModelProvided = len(canonicalModel) > 0
	if len(canonicalModel) > 0 {
		observation.Model = canonicalModel[0]
	}
	if model := firstGrokHealthModel(canonicalModel); model != "" {
		observation.EffectiveModel = model
	}

	now := time.Now()
	quotaSnapshot := grok.ParseQuotaObservation(headers, statusCode, now)
	quotaModel := firstGrokHealthModel(canonicalModel)
	if quotaModel == "" {
		quotaModel = input.QuotaModel
	}
	accountcore.StampGrokQuotaPlan(accountcore.CloneRecord(value), quotaSnapshot, quotaModel, grok.ResolveGrokTextResponsesModelID, grok.ApplyGrok45ResponsesPlanSignal)
	// 模型容量 429 属于请求压力，只保存额度观测，不安装账号级限流状态。
	snapshotFailure := grok.ClassifyGrokUpstreamFailure(statusCode, responseBody, quotaModel)
	s.StoreSnapshot(stateCtx, value, quotaSnapshot, snapshotFailure.Class != grok.GrokFailureModelCapacity, input.TeamModel)

	decision := accountcore.ErrorDecisionWithoutPersistence(value, statusCode)
	if s.Health != nil {
		if value.IsPoolMode() || value.IsCustomErrorCodesEnabled() {
			decision.Policy = s.Health.ApplyExplicitErrorPolicy(stateCtx, accountcore.CloneRecord(value), observation)
			decision.StopScheduling = decision.Policy == accountcore.ErrorPolicyCustomMatched || decision.Policy == accountcore.ErrorPolicyTempUnscheduled
		} else {
			decision = accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case accountcore.ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.Runtime.BlockAccountScheduling(value, time.Time{}, "upstream_disable")
		return decision
	case accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}

	if s.Health != nil && len(canonicalModel) > 0 &&
		s.Health.Models.Observe(stateCtx, accountcore.CloneRecord(value), canonicalModel[0], statusCode, responseBody, observation.Thinking, observation.ImagesEndpoint) {
		decision.StopScheduling = true
		return decision
	}
	// 普通账号先保留模型不存在等精确处理，再应用管理员临时规则。
	if s.Health != nil && statusCode != http.StatusUnauthorized &&
		!value.IsPoolMode() && !value.IsCustomErrorCodesEnabled() &&
		s.Health.Core.HandleTempUnschedulable(stateCtx, accountcore.CloneRecord(value), statusCode, responseBody, observation.EffectiveModel) {
		decision.Policy = accountcore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if firstGrokHealthModel(canonicalModel) == "" {
			s.Runtime.BlockAccountScheduling(value, time.Time{}, "upstream_disable")
		}
		return decision
	}

	// Grok API Key 的 5xx 与 OpenAI API Key 共用账号+最终模型的瞬态冷却；
	// OAuth 和模型未知的请求继续沿用账号级退避，避免扩大既有行为变化。
	model := firstGrokHealthModel(canonicalModel)
	if model == "" {
		model = quotaModel
	}
	if value.Type == capability.AccountTypeAPIKey && model != "" && statusCode >= 500 &&
		input.ServerTransient {
		s.recordModelTransient(value, model)
		return decision
	}

	// 响应体中的免费额度、账单、空输出和容量语义优先于通用状态码处理。
	failure := grok.ClassifyGrokUpstreamFailure(statusCode, responseBody, model)
	if failure.ShouldCooldown && failure.Class != grok.GrokFailureNone && failure.Class != grok.GrokFailureRateLimit {
		if failure.Class == grok.GrokFailureFreeUsage {
			if resetAt, limited := accountcore.GrokRateLimitResetAtForAccount(value, quotaSnapshot, now); limited && resetAt.After(now) {
				if failure.Model != "" && accountcore.IsGrokModelSpecificFreeUsage(strings.ToLower(failure.Reason), failure.Model) {
					accountcore.MarkGrokModelQuotaBlock(value.ID, failure.Model, resetAt)
					decision.StopScheduling = true
					return decision
				}
				s.RateLimit(stateCtx, value, resetAt, input.TeamModel)
				decision.StopScheduling = true
				return decision
			}
		}
		if s.ApplyFailure(stateCtx, value, failure, input.TeamModel) {
			decision.StopScheduling = true
			return decision
		}
	}

	switch statusCode {
	case http.StatusUnauthorized:
		s.TempUnschedule(stateCtx, value, 10*time.Minute, "grok credentials unauthorized")
		decision.StopScheduling = true
		return decision
	case http.StatusPaymentRequired:
		// 402 表示当前账号计费不可用，短期排除以避免后续请求反复命中。
		s.TempUnschedule(stateCtx, value, 30*time.Minute, "grok payment required")
		decision.StopScheduling = true
		return decision
	case http.StatusForbidden:
		if s.applyForbiddenPolicy(stateCtx, value, responseBody, observation.EffectiveModel) {
			decision.StopScheduling = true
			return decision
		}
		s.TempUnschedule(stateCtx, value, 30*time.Minute, "grok access or entitlement denied")
		decision.StopScheduling = true
		return decision
	case http.StatusMethodNotAllowed:
		// 当前账号不支持所选 Grok 端点，临时排除可避免粘性会话反复命中同一账号。
		s.TempUnschedule(stateCtx, value, 30*time.Minute, "grok endpoint not supported (405)")
		decision.StopScheduling = true
		return decision
	case http.StatusTooManyRequests:
		// 快照处理已同时写入运行时和持久化限流状态。
		decision.StopScheduling = true
		return decision
	default:
		if statusCode >= 500 {
			s.TempUnschedule(stateCtx, value, 2*time.Minute, "grok upstream temporary error")
			decision.StopScheduling = true
			return decision
		}
	}
	return decision
}

// ApplyFailure 将分类结果映射到账户健康状态；返回 true 表示已完整处理，
// 调用方不能再次应用状态码默认逻辑。
func (s *GrokHealth) ApplyFailure(
	ctx context.Context,
	value *accountcore.Record,
	decision grok.GrokUpstreamFailureDecision, teamModel string,
) bool {
	if s == nil || value == nil || !decision.ShouldCooldown || decision.Cooldown <= 0 {
		return false
	}
	// 原因保持简短稳定，供运维界面和 temp_unschedulable_reason 使用。
	var reason string
	switch decision.Class {
	case grok.GrokFailureFreeUsage:
		reason = "grok free usage exhausted"
		// 模型级免费额度耗尽只软封禁该模型，使同账号其它模型仍可调度。
		low := strings.ToLower(decision.Reason)
		if decision.Model != "" && accountcore.IsGrokModelSpecificFreeUsage(low, decision.Model) {
			until := time.Now().Add(decision.Cooldown)
			accountcore.MarkGrokModelQuotaBlock(value.ID, decision.Model, until)
			// 上游已明确限定到单模型，账号级冷却会错误移除健康的其它模型。
			return true
		}
	case grok.GrokFailureBilling:
		low := strings.ToLower(decision.Reason)
		if strings.Contains(low, "spending") || strings.Contains(low, "credits") {
			// 消费上限或 credits 耗尽属于账单窗口条件，保留账号可恢复状态并由常规限流恢复解除。
			s.RateLimit(ctx, value, accountcore.GrokSpendingLimitResetAt(accountcore.CloneRecord(value), time.Now()), teamModel)
			return true
		}
		// 保留历史 402/payment 原因，兼容运维界面和回归测试。
		reason = "grok payment required"
	case grok.GrokFailureEmptyUpstream:
		reason = "grok empty model output"
	case grok.GrokFailureModelCapacity:
		// 容量压力只作用于请求模型，账号切换仍由外层有界重试决定。
		if model := strings.TrimSpace(decision.Model); model != "" {
			cooldown := decision.Cooldown
			if cooldown <= 0 {
				cooldown = 3 * time.Minute
			}
			accountcore.MarkGrokModelTransientBlock(value.ID, model, time.Now().Add(cooldown))
		}
		return true
	case grok.GrokFailureRateLimit:
		// 不含免费额度语义的纯 429 继续走 Retry-After 与额度请求头快照路径。
		return false
	case grok.GrokFailureServer:
		reason = "grok upstream temporary error"
	case grok.GrokFailureCompatibility:
		// 请求形状不兼容时只交给外层换号，不修改账号健康状态。
		return true
	default:
		return false
	}
	s.TempUnschedule(ctx, value, decision.Cooldown, reason)
	return true
}

// applyForbiddenPolicy 保留非内容拒绝 403 的管理员规则；未匹配时使用原权益冷却。
func (s *GrokHealth) applyForbiddenPolicy(ctx context.Context, value *accountcore.Record, responseBody []byte, effectiveModel string) bool {
	if value == nil || !value.IsTempUnschedulableEnabled() {
		return false
	}

	matches := accountcore.MatchTempUnschedulableRules(accountcore.CloneRecord(value), http.StatusForbidden, responseBody)
	if len(matches) == 0 {
		return false
	}

	match := matches[0]
	// 存储库可用时复用中心策略实现，以保持既有原因和缓存格式并避免重复写入。
	if s != nil && s.Health != nil &&
		s.Health.Limits.Plans !=
			nil {
		stateCtx, cancel := grokStateContext(ctx)
		handled := s.Health.Core.TryTempUnschedulable(stateCtx, accountcore.CloneRecord(value), http.StatusForbidden, responseBody, value.Platform != capability.PlatformAntigravity, effectiveModel)

		cancel()
		if handled {
			return true
		}
	}

	// 服务未完整构造时（例如单元测试网关）仍遵循配置时长，不能静默回退到 30 分钟。
	cooldown := time.Duration(match.Rule.DurationMinutes) * time.Minute
	if cooldown > 0 {
		s.TempUnschedule(ctx, value, cooldown, "grok configured forbidden rule")
	}
	return true
}

// recordModelTransient 使用与选择器共享的账号/模型状态，并保留原日志字段。
func (s *GrokHealth) recordModelTransient(value *accountcore.Record, model string) {
	if s.ModelTransient == nil || value == nil {
		return
	}
	model = accountcore.NormalizeTransientModel(model)
	decision := s.ModelTransient.RecordFailure(value.ID, model, time.Now())
	if decision.FailureStreak == 0 {
		return
	}
	slog.Warn("openai_model_transient_state", "account_id", value.ID, "platform", value.Platform, "model", model, "failure_streak", decision.FailureStreak, "cooldown_ms", decision.Cooldown.Milliseconds(), "block_scope", "account_model")
}
