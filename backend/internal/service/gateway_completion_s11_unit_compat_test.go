//go:build unit

// 历史 unit 测试入口只投影并调用唯一完成实现，不进入生产构建。
package service

import (
	"context"
	"time"

	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

func buildUsageBillingCommand(requestID string, usageLog *UsageLog, p *usageBillingParams) *UsageBillingCommand {
	return completion.BuildCommand(requestID, UsageLogView(usageLog), completionSettlement(p))
}

// calculateOpenAIRecordUsageCost 保留旧 unit 测试的兼容入口。
//

func (s *OpenAIGatewayService) calculateOpenAIRecordUsageCost(
	ctx context.Context,
	result *OpenAIForwardResult,
	apiKey *APIKey,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	webSearchMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
) (*CostBreakdown, error) {
	return s.calculateOpenAIRecordUsageCostAt(ctx, result, apiKey, billingModels, multiplier, imageMultiplier, videoMultiplier, webSearchMultiplier, tokens, serviceTier, time.Time{})
}

// calculateOpenAIRecordUsageCostAt 使用固定请求时刻计算费用，确保渠道分时倍率与高峰倍率同刻。
func (s *OpenAIGatewayService) calculateOpenAIRecordUsageCostAt(
	ctx context.Context,
	result *OpenAIForwardResult,
	apiKey *APIKey,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	webSearchMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
	pricingAt time.Time,
) (*CostBreakdown, error) {
	return s.CompletionRecorder(nil).CalculateOpenAIRecordUsageCostAt(ctx, completionOpenAIResult(result, nil), completionKey(apiKey), billingModels, multiplier, imageMultiplier, videoMultiplier, webSearchMultiplier, tokens, serviceTier, pricingAt)
}

// isUsagePricingUnavailableError 判断错误是否仅表示模型缺少可用定价。
func isUsagePricingUnavailableError(err error) bool {
	return completion.IsUsagePricingUnavailableError(err)
}

// filterCNProviderBillingModelCandidates 防止国产供应商把客户端 claude 模型名
// 落入全局 Claude/Sonnet 兜底价。管理员显式配置的分组或渠道价格仍然有效。
func (s *OpenAIGatewayService) filterCNProviderBillingModelCandidates(
	ctx context.Context,
	account *Account,
	apiKey *APIKey,
	candidates []string,
) []string {
	return s.CompletionRecorder(nil).FilterCNProviderBillingModelCandidates(ctx, completionAccount(account), completionKey(apiKey), candidates)
}

func (s *OpenAIGatewayService) resolveOpenAIChannelPricing(ctx context.Context, billingModel string, apiKey *APIKey) *ResolvedPricing {
	return s.CompletionRecorder(nil).ResolveOpenAIChannelPricing(ctx, billingModel, completionKey(apiKey))
}

// usageBillingParams 统一扣费所需的参数
type usageBillingParams struct {
	Cost                            *CostBreakdown
	User                            *User
	APIKey                          *APIKey
	Account                         *Account
	Subscription                    *UserSubscription
	RequestPayloadHash              string
	AccountRateMultiplier           float64
	SubscriptionRateMultiplier      float64
	SubscriptionRateMultiplierScale float64
	BalanceRateMultiplier           float64
	APIKeyService                   APIKeyQuotaUpdater
	Platform                        string // 来自 APIKey 关联 Group 的平台标识
	// BillingBaseAmountUSD 是用户资金分配使用的未倍率基础金额；nil 时沿用 Cost.TotalCost。
	// 免费 Fast 需要把用户基础价切换为 Standard，同时保留 Fast 的账号统计基础成本。
	BillingBaseAmountUSD *float64
}

// 旧分组定价测试只转接唯一 completion 实现。
func (s *GatewayService) resolveChannelPricingForUsage(ctx context.Context, model string, key *APIKey) (*ResolvedPricing, string) {
	return s.CompletionRecorder(nil).ResolveChannelPricing(ctx, model, completionKey(key)), model
}

func completionSettlement(p *usageBillingParams) *completion.SettlementInput {
	if p == nil {
		return nil
	}
	return &completion.SettlementInput{
		Cost:                            p.Cost,
		User:                            completionPayer(p.User),
		APIKey:                          completionKey(p.APIKey),
		Account:                         completionAccount(p.Account),
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

func subscriptionPlanIncludesGroup(plan *SubscriptionPlan, groupID int64) bool {
	return completion.SubscriptionPlanIncludesGroup(plan, groupID)
}
