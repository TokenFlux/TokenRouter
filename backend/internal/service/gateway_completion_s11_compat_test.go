// 普通测试使用的历史名称仅在测试构建保留，算法由 completion 唯一实现。
package service

import (
	"context"

	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

// calculateRecordUsageCost 根据请求类型和选项计算费用。
func (s *GatewayService) calculateRecordUsageCost(
	ctx context.Context,
	result *ForwardResult,
	apiKey *APIKey,
	account *Account,
	billingModel string,
	requestedModel string,
	billingModelSource string,
	channelMappedModel string,
	multiplier float64,
	imageMultiplier float64,
	opts *recordUsageOpts,
) *CostBreakdown {
	return s.CompletionRecorder(nil).CalculateRecordUsageCost(ctx, completionForwardResult(result, account), completionKey(apiKey), completionAccount(account), billingModel, requestedModel, billingModelSource, channelMappedModel, multiplier, imageMultiplier, completionPricingOptions(opts))
}

// openAIUsageBillingModel 按渠道计费来源选择模型，并保留图片结果已经解析出的专用定价模型。
func openAIUsageBillingModel(result *OpenAIForwardResult, fields ChannelUsageFields) string {
	return completion.OpenAIUsageBillingModel(completionOpenAIResult(result, nil), fields)
}

// groupBillsOpenAIFastAtStandard 判断分组免费 Fast 是否适用于当前 OpenAI 账号和计费档位。
// 分组策略只改变用户侧价格，不改变实际发往上游的 service_tier。
func groupBillsOpenAIFastAtStandard(apiKey *APIKey, account *Account, serviceTier string) bool {
	return completion.GroupBillsOpenAIFastAtStandard(completionKey(apiKey), completionAccount(account), serviceTier)
}
