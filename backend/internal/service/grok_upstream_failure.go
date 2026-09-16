package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type GrokUpstreamFailureClass = nativegrok.GrokUpstreamFailureClass

const GrokFailureNone = nativegrok.GrokFailureNone
const GrokFailureFreeUsage = nativegrok.GrokFailureFreeUsage
const GrokFailureBilling = nativegrok.GrokFailureBilling
const GrokFailureEmptyUpstream = nativegrok.GrokFailureEmptyUpstream
const GrokFailureModelCapacity = nativegrok.GrokFailureModelCapacity
const GrokFailureRateLimit = nativegrok.GrokFailureRateLimit
const GrokFailureAuth = nativegrok.GrokFailureAuth
const GrokFailureServer = nativegrok.GrokFailureServer
const GrokFailureCompatibility = nativegrok.GrokFailureCompatibility

type GrokUpstreamFailureDecision = nativegrok.GrokUpstreamFailureDecision

func classifyGrokUpstreamFailure(statusCode int, responseBody []byte, requestedModel string) GrokUpstreamFailureDecision {
	return nativegrok.ClassifyGrokUpstreamFailure(statusCode, responseBody, requestedModel)
}

// grokRetryableOnSameAccount 标记共享 failover 循环可在同账号重试的瞬态错误。
// 模型容量压力允许有限重试；免费额度和计费耗尽属于账号状态，应立即切换账号。
func grokRetryableOnSameAccount(account *Account, statusCode int, responseBody []byte) bool {
	if account == nil || !account.IsGrok() {
		return false
	}
	// 显式错误码策略优先于池模式默认状态列表，命中后不得在同一账号重试。
	if account.IsCustomErrorCodesEnabled() && account.ShouldHandleErrorCode(statusCode) {
		return false
	}
	decision := classifyGrokUpstreamFailure(statusCode, responseBody, "")
	switch decision.Class {
	case GrokFailureFreeUsage, GrokFailureBilling, GrokFailureCompatibility:
		// Quota/entitlement exhaustion is account state, not transient
		// pressure. Retrying the same account only repeats the failure.
		return false
	case GrokFailureModelCapacity:
		if statusCode == http.StatusTooManyRequests {
			return true
		}
	}
	return account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode)
}

func grokSameAccountRetryMetadata(account *Account, statusCode int, responseBody []byte) (bool, time.Duration, time.Time, int) {
	if !grokRetryableOnSameAccount(account, statusCode, responseBody) {
		return false, 0, time.Time{}, 0
	}
	decision := classifyGrokUpstreamFailure(statusCode, responseBody, "")
	if decision.Class != GrokFailureModelCapacity {
		return true, 0, time.Time{}, 0
	}
	// 每次上游尝试都会重新构造错误，因此错误上的截止时间不能覆盖整个请求；
	// 对容量错误显式限制为一次重放，即使第一次尝试超过名义 30 秒窗口也仍然生效。
	return true, 500 * time.Millisecond, time.Now().Add(30 * time.Second), 1
}

func shouldMarkGrokTeamModelRateLimit(statusCode int, responseBody []byte) bool {
	return nativegrok.ShouldMarkGrokTeamModelRateLimit(statusCode, responseBody)
}

const grokFreeUsageProbeCooldown = nativegrok.GrokFreeUsageProbeCooldown

// applyGrokUpstreamFailureDecision 将分类结果映射到账户健康状态；返回 true 表示已完整处理，
// 调用方不能再次应用状态码默认逻辑。
func (s *OpenAIGatewayService) applyGrokUpstreamFailureDecision(
	ctx context.Context,
	account *Account,
	decision GrokUpstreamFailureDecision,
) bool {
	if s == nil || account == nil || !decision.ShouldCooldown || decision.Cooldown <= 0 {
		return false
	}
	// 原因保持简短稳定，供运维界面和 temp_unschedulable_reason 使用。
	var reason string
	switch decision.Class {
	case GrokFailureFreeUsage:
		reason = "grok free usage exhausted"
		// 模型级免费额度耗尽只软封禁该模型，使同账号其它模型仍可调度。
		low := strings.ToLower(decision.Reason)
		if decision.Model != "" && isGrokModelSpecificFreeUsage(low, decision.Model) {
			until := time.Now().Add(decision.Cooldown)
			markGrokModelQuotaBlock(account.ID, decision.Model, until)
			// 上游已明确限定到单模型，账号级冷却会错误移除健康的其它模型。
			return true
		}
	case GrokFailureBilling:
		low := strings.ToLower(decision.Reason)
		if strings.Contains(low, "spending") || strings.Contains(low, "credits") {
			// 消费上限或 credits 耗尽属于账单窗口条件，保留账号可恢复状态并由常规限流恢复解除。
			s.rateLimitGrok(ctx, account, grokSpendingLimitResetAt(account, time.Now()))
			return true
		}
		// 保留历史 402/payment 原因，兼容运维界面和回归测试。
		reason = "grok payment required"
	case GrokFailureEmptyUpstream:
		reason = "grok empty model output"
	case GrokFailureModelCapacity:
		// Capacity is scoped to the requested model. Never persist an account-wide
		// unschedulable state for this transient class; the failover loop performs
		// a bounded same-account retry before selecting another account.
		_ = persistGrokTransientModelCooldown(account, decision)
		return true
	case GrokFailureRateLimit:
		// 不含免费额度语义的纯 429 继续走 Retry-After 与额度请求头快照路径。
		return false
	case GrokFailureServer:
		reason = "grok upstream temporary error"
	case GrokFailureCompatibility:
		// Deliberately no account mutation. The caller uses ShouldFailover to
		// retry another account; cooling a pool for a request-shape mismatch
		// would remove healthy accounts.
		return true
	default:
		return false
	}
	s.tempUnscheduleGrok(ctx, account, decision.Cooldown, reason)
	return true
}
