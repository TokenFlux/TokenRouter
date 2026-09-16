package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

func normalizeAntigravityModelName(model string) string {
	return antigravity.NormalizeAntigravityModelName(model)
}

// resolveAntigravityModelKey 根据请求的模型名解析限流 key
// 返回空字符串表示无法解析
func resolveAntigravityModelKey(requestedModel string) string {
	return normalizeAntigravityModelName(requestedModel)
}

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
	if a == nil {
		return false
	}
	if a.isModelRateLimitedWithContext(ctx, requestedModel) {
		// Antigravity + overages 启用 + 积分未耗尽 → 放行（有积分可用）
		if a.Platform == PlatformAntigravity && a.IsOveragesEnabled() && !a.isCreditsExhausted() {
			return true
		}
		return false
	}
	return true
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
