// 平台账号内重试只使用技术输入和观测端口，不持有业务实体、数据库或 Gin。
package antigravity

import (
	"errors"
	"fmt"
	"sync"
	"time" // 限流相关常量
	// AntigravityRateLimitThreshold 限流等待/切换阈值
	// - 智能重试：retryDelay < 此阈值时等待后重试，>= 此阈值时直接限流模型
	// - 预检查：剩余限流时间 < 此阈值时等待，>= 此阈值时切换账号
)

const (
	AntigravityStickySessionTTL         = time.Hour
	AntigravityMaxRetries               = 3
	AntigravityRetryBaseDelay           = 1 * time.Second
	AntigravityRetryMaxDelay            = 16 * time.Second
	AntigravityRateLimitThreshold       = 7 * time.Second
	AntigravitySmartRetryMinWait        = 1 * time.Second  // 智能重试最小等待时间
	AntigravitySmartRetryMaxAttempts    = 1                // 智能重试最大次数（仅重试 1 次，防止重复限流/长期等待）
	AntigravityDefaultRateLimitDuration = 30 * time.Second // 默认限流时间（无 retryDelay 时使用）

	// MODEL_CAPACITY_EXHAUSTED 专用重试参数
	// 模型容量不足时，所有账号共享同一容量池，切换账号无意义
	// 使用固定 1s 间隔重试，最多重试 60 次
	AntigravityModelCapacityRetryMaxAttempts = 60
	AntigravityModelCapacityRetryWait        = 1 * time.Second

	// Google RPC 状态和类型常量
	GoogleRPCStatusResourceExhausted      = "RESOURCE_EXHAUSTED"
	GoogleRPCStatusUnavailable            = "UNAVAILABLE"
	GoogleRPCTypeRetryInfo                = "type.googleapis.com/google.rpc.RetryInfo"
	GoogleRPCTypeErrorInfo                = "type.googleapis.com/google.rpc.ErrorInfo"
	GoogleRPCReasonModelCapacityExhausted = "MODEL_CAPACITY_EXHAUSTED"
	GoogleRPCReasonRateLimitExceeded      = "RATE_LIMIT_EXCEEDED"

	// 单账号 503 退避重试：Service 层原地重试的最大次数
	// 在 HandleSmartRetry 中，对于 shouldRateLimitModel（长延迟 ≥ 7s）的情况，
	// 多账号模式下会设限流+切换账号；但单账号模式下改为原地等待+重试。
	AntigravitySingleAccountSmartRetryMaxAttempts = 3

	// 单账号 503 退避重试：原地重试时单次最大等待时间
	// 防止上游返回过长的 retryDelay 导致请求卡住太久
	AntigravitySingleAccountSmartRetryMaxWait = 15 * time.Second

	// 单账号 503 退避重试：原地重试的总累计等待时间上限
	// 超过此上限将不再重试，直接返回 503
	AntigravitySingleAccountSmartRetryTotalMaxWait = 30 * time.Second

	// MODEL_CAPACITY_EXHAUSTED 全局去重：重试全部失败后的 cooldown 时间
	AntigravityModelCapacityCooldown = 10 * time.Second
)

// MODEL_CAPACITY_EXHAUSTED 全局去重：避免多个并发请求同时对同一模型进行容量耗尽重试
var (
	modelCapacityExhaustedMu    sync.RWMutex
	modelCapacityExhaustedUntil = make(map[string]time.Time) // modelName -> cooldown until
)

// AntigravityAccountSwitchError 账号切换信号
// 当账号限流时间超过阈值时，通知上层切换账号
type AntigravityAccountSwitchError struct {
	OriginalAccountID int64
	RateLimitedModel  string
	IsStickySession   bool // 是否为粘性会话切换（决定是否缓存计费）
}

func (e *AntigravityAccountSwitchError) Error() string {
	return fmt.Sprintf("account %d model %s rate limited, need switch",
		e.OriginalAccountID, e.RateLimitedModel)
}

// IsAntigravityAccountSwitchError 检查错误是否为账号切换信号
func IsAntigravityAccountSwitchError(err error) (*AntigravityAccountSwitchError, bool) {
	var switchErr *AntigravityAccountSwitchError
	if errors.As(err, &switchErr) {
		return switchErr, true
	}
	return nil, false
}
