package service

import (
	"context"
	"time"
)

// IsSchedulableForModel 结合模型级限流判断是否可调度。
// 保持旧签名以兼容既有调用方；默认使用 context.Background()。
func (a *Account) IsSchedulableForModel(requestedModel string) bool {
	return a.IsSchedulableForModelWithContext(context.Background(), requestedModel)
}

func (a *Account) IsSchedulableForModelWithContext(ctx context.Context, requestedModel string) bool {
	if a == nil {
		return false
	}
	if !a.allowsProtocolRequest(ctx) || !a.IsSchedulable() {
		return false
	}
	return a.modelRateLimitAllowsScheduling(ctx, requestedModel)
}

// modelRateLimitAllowsScheduling 仅检查模型级限流；调用方已经从可调度账号查询取得账号时，
// 可复用本方法补齐模型维度检查，避免再次依赖账号状态字段的投影完整性。
func (a *Account) modelRateLimitAllowsScheduling(ctx context.Context, requestedModel string) bool {
	return accountModelPolicy(a).AllowsModel(ctx, requestedModel)
}

// GetRateLimitRemainingTime 获取限流剩余时间（模型级限流）
// 返回 0 表示未限流或已过期
func (a *Account) GetRateLimitRemainingTime(requestedModel string) time.Duration {
	return a.GetRateLimitRemainingTimeWithContext(context.Background(), requestedModel)
}

// GetRateLimitRemainingTimeWithContext 获取限流剩余时间（模型级限流）
// 返回 0 表示未限流或已过期
func (a *Account) GetRateLimitRemainingTimeWithContext(ctx context.Context, requestedModel string) time.Duration {
	if a == nil {
		return 0
	}
	return a.GetModelRateLimitRemainingTimeWithContext(ctx, requestedModel)
}
