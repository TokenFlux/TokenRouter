//go:build unit

// 历史 unit 测试入口只投影并调用唯一完成实现，不进入生产构建。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func buildUsageBillingCommand(requestID string, usageLog *usage.UsageLog, p *usageBillingParams) *billing.UsageBillingCommand {
	return completion.BuildCommand(requestID, querycache.Clone(usageLog), completionSettlement(p))
}

// calculateOpenAIRecordUsageCost 保留旧 unit 测试的兼容入口。
//

// isUsagePricingUnavailableError 判断错误是否仅表示模型缺少可用定价。
func isUsagePricingUnavailableError(err error) bool {
	return completion.IsUsagePricingUnavailableError(err)
}

// usageBillingParams 统一扣费所需的参数
type usageBillingParams struct {
	Cost                            *pricing.CostBreakdown
	User                            *identity.User
	APIKey                          *apikey.APIKey
	Account                         *gatewaycapture.ExecutionAccount
	Subscription                    *billing.UserSubscription
	RequestPayloadHash              string
	AccountRateMultiplier           float64
	SubscriptionRateMultiplier      float64
	SubscriptionRateMultiplierScale float64
	BalanceRateMultiplier           float64
	APIKeyService                   gatewaycapture.QuotaUpdater
	Platform                        string // 来自 APIKey 关联 Group 的平台标识
	// BillingBaseAmountUSD 是用户资金分配使用的未倍率基础金额；nil 时沿用 Cost.TotalCost。
	// 免费 Fast 需要把用户基础价切换为 Standard，同时保留 Fast 的账号统计基础成本。
	BillingBaseAmountUSD *float64
}

func completionSettlement(p *usageBillingParams) *completion.SettlementInput {
	if p == nil {
		return nil
	}
	return &completion.SettlementInput{
		Cost:                            p.Cost,
		User:                            gatewaycapture.ProjectCompletionPayer(p.User),
		APIKey:                          gatewaycapture.ProjectCompletionKey(p.APIKey),
		Account:                         gatewaycapture.ProjectCompletionAccount(gatewaycapture.ExecutionCompletionRecord(p.Account)),
		Subscription:                    p.Subscription,
		RequestPayloadHash:              p.RequestPayloadHash,
		AccountRateMultiplier:           p.AccountRateMultiplier,
		SubscriptionRateMultiplier:      p.SubscriptionRateMultiplier,
		SubscriptionRateMultiplierScale: p.SubscriptionRateMultiplierScale,
		BalanceRateMultiplier:           p.BalanceRateMultiplier,
		QuotaUpdates:                    p.APIKeyService != nil,
		Platform:                        p.Platform,
		BillingBaseAmountUSD:            p.BillingBaseAmountUSD,
	}
}
