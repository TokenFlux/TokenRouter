package provider

import (
	"net/http"

	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokRetryableOnSameAccount 标记共享 failover 循环可在同账号重试的瞬态错误。
// 模型容量压力允许有限重试；免费额度和计费耗尽属于账号状态，应立即切换账号。
func GrokRetryableOnSameAccount(account *ExecutionAccount, statusCode int, responseBody []byte) bool {
	if account == nil || !account.View().IsGrok() {
		return false
	}
	// 显式错误码策略优先于池模式默认状态列表，命中后不得在同一账号重试。
	if account.View().IsCustomErrorCodesEnabled() && account.View().ShouldHandleErrorCode(statusCode) {
		return false
	}
	decision := grok.ClassifyGrokUpstreamFailure(statusCode, responseBody, "")
	switch decision.Class {
	case grok.GrokFailureFreeUsage, grok.GrokFailureBilling, grok.GrokFailureCompatibility:
		// 额度和权益耗尽不能靠同账号重放恢复。
		return false
	case grok.GrokFailureModelCapacity:
		if statusCode == http.StatusTooManyRequests {
			return true
		}
	}
	return account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(statusCode)
}

func GrokSameAccountRetryMetadata(account *ExecutionAccount, statusCode int, responseBody []byte) (bool, time.Duration, time.Time, int) {
	if !GrokRetryableOnSameAccount(account, statusCode, responseBody) {
		return false, 0, time.Time{}, 0
	}
	decision := grok.ClassifyGrokUpstreamFailure(statusCode, responseBody, "")
	if decision.Class != grok.GrokFailureModelCapacity {
		return true, 0, time.Time{}, 0
	}
	// 每次上游尝试都会重新构造错误，因此错误上的截止时间不能覆盖整个请求；
	// 对容量错误显式限制为一次重放，即使第一次尝试超过名义 30 秒窗口也仍然生效。
	return true, 500 * time.Millisecond, time.Now().Add(30 * time.Second), 1
}
