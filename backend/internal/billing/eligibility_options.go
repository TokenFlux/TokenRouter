// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	context "context"
)

// Observe 接收结构化组件和原日志内容，由 app 连接唯一日志后端。
type Observe func(component, format string, args ...any)

func (o Observe) Printf(component, format string, args ...any) {
	if o != nil {
		o(component, format, args...)
	}
}

// EligibilityOptions 是计费准入的独立运行参数。
type EligibilityOptions struct {
	Dates    DateRuntime
	RunMode  string
	Billing  BillingOptions
	Database QuotaMirrorOptions
}
type BillingOptions struct {
	MinimumBalanceReserve               float64
	UserPlatformQuotaCacheTTLSeconds    int
	UserPlatformQuotaSentinelTTLSeconds int
	CircuitBreaker                      CircuitBreakerOptions
}
type QuotaMirrorOptions struct{ UserPlatformQuotaFlusherEnabled bool }
type CircuitBreakerOptions struct {
	Enabled             bool
	FailureThreshold    int
	ResetTimeoutSeconds int
	HalfOpenRequests    int
}

const RunModeSimple = "simple"

// BalanceReader 仅返回权益快照，不允许通过普通实体更新余额。
type BalanceReader interface {
	GetByID(context.Context, int64) (*UserSummary, error)
}
type APIKeyRateLimitLoader interface {
	GetRateLimitData(context.Context, int64) (*APIKeyRateLimitData, error)
}

// KeySnapshot 只携带消费准入字段，不包含凭据与认证状态。
type KeySnapshot struct {
	ID          int64
	BillingMode string
	RateLimit5h float64
	RateLimit1d float64
	RateLimit7d float64
}

func (k *KeySnapshot) HasRateLimits() bool {
	return k.RateLimit5h > 0 || k.RateLimit1d > 0 || k.RateLimit7d > 0
}
func effectiveKeyBillingMode(k *KeySnapshot) string {
	if k == nil {
		return APIKeyBillingModeAuto
	}
	mode, ok := NormalizeAPIKeyBillingMode(k.BillingMode)
	if !ok {
		return APIKeyBillingModeAuto
	}
	return mode
}

// GroupSnapshot 仅标识最终消费分组，不能承担路由策略。
type GroupSnapshot struct{ ID int64 }

// CheckInput 固化资金准入所需投影。
type CheckInput struct {
	Payer        *UserSummary
	Key          *KeySnapshot
	Group        *GroupSnapshot
	Subscription *UserSubscription
	Platform     string
}
