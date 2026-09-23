package requeststate

import "context"

// cacheBillingKey 只属于当前尝试的执行标记，不对外暴露可变全局键。
type cacheBillingKey struct{}

// IsForceCacheBilling 保留原布尔读取及错误类型值的缺省行为。
func IsForceCacheBilling(ctx context.Context) bool {
	value, _ := ctx.Value(cacheBillingKey{}).(bool)
	return value
}

// WithForceCacheBilling 派生尝试 context，不改写父请求或任何资金事实。
func WithForceCacheBilling(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheBillingKey{}, true)
}
