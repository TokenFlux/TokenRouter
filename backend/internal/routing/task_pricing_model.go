// 任务报价保持渠道的请求、映射与上游模型选择口径，纯规则不读取配置或存储。
package routing

import "strings"

// BillingModelForPrice 按渠道计费基准选择价格模型，且最早只从 Key 重定向目标开始。
func BillingModelForPrice(mapping ChannelMappingResult, requestedModel, channelMappedModel, upstreamModel string) string {
	switch mapping.BillingModelSource {
	case BillingModelSourceRequested:
		return strings.TrimSpace(requestedModel)
	case BillingModelSourceUpstream:
		return strings.TrimSpace(upstreamModel)
	case BillingModelSourceChannelMapped:
		return strings.TrimSpace(channelMappedModel)
	default:
		return strings.TrimSpace(channelMappedModel)
	}
}
