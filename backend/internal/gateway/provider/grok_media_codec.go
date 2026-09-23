package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// 输入归一化继续复用 billing/pricing 的原纯规则；不提前读取价格或额外查询。
// GrokMediaCodec 投影唯一协议原语所需的价格规范化端口，不保存缓存或请求状态。
func GrokMediaCodec() grok.MediaCodec {
	return grok.MediaCodec{Options: grok.MediaNormalization{
		MaxUploadPartSize:                             upstream.OpenAIImageMaxUploadPartSize,
		ImageTier1K:                                   pricing.ImageBillingSize1K,
		MarshalJSON:                                   wirejson.Marshal,
		NormalizeImageBillingTierOrDefault:            pricing.NormalizeImageBillingTierOrDefault,
		NormalizeVideoBillingResolutionOrDefault:      pricing.NormalizeVideoBillingResolutionOrDefault,
		NormalizeVideoBillingDurationSecondsOrDefault: pricing.NormalizeVideoBillingDurationSecondsOrDefault,
		ClassifyImageBillingTier:                      pricing.ClassifyImageBillingTier,
		ParseImageDimensions:                          pricing.ParseImageBillingDimensions,
	}}
}
