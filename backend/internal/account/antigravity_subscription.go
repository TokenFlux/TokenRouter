// Antigravity 账号套餐及异常投影不改变用户权益。
package account

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

const antigravitySubscriptionAbnormal = "abnormal"

// AntigravitySubscriptionResult 表示订阅检测后的规范化结果。
type AntigravitySubscriptionResult struct {
	PlanType           string
	SubscriptionStatus string
	SubscriptionError  string
}

// NormalizeAntigravitySubscription 从 LoadCodeAssistResponse 提取 plan_type + 异常状态。
// 使用 GetTier()（返回 tier ID）+ TierIDToPlanType 映射。
func NormalizeAntigravitySubscription(resp *google.AntigravityLoadCodeAssistResponse) AntigravitySubscriptionResult {
	if resp == nil {
		return AntigravitySubscriptionResult{PlanType: "Free"}
	}
	tierID := resp.GetTier()
	planType := google.AntigravityTierIDToPlanType(tierID)
	if len(resp.IneligibleTiers) > 0 {
		if planType == "" || planType == "Free" {
			planType = "Abnormal"
		}
		result := AntigravitySubscriptionResult{
			PlanType:           planType,
			SubscriptionStatus: antigravitySubscriptionAbnormal,
		}
		if resp.IneligibleTiers[0] != nil {
			result.SubscriptionError = strings.TrimSpace(resp.IneligibleTiers[0].ReasonMessage)
		}
		return result
	}
	return AntigravitySubscriptionResult{
		PlanType: planType,
	}
}
