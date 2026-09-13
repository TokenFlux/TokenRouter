// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	strings "strings"
)

func (v ChannelValidation) NormalizeGroupPricing(platform string, pricing []ChannelModelPricing) ([]ChannelModelPricing, error) {
	out := make([]ChannelModelPricing, len(pricing))
	for i := range pricing {
		out[i] = pricing[i].Clone()
		out[i].ID = 0
		out[i].ChannelID = 0
		if strings.TrimSpace(out[i].Platform) == "" {
			out[i].Platform = platform
		}
		for j := range out[i].Models {
			out[i].Models[j] = strings.TrimSpace(out[i].Models[j])
		}
		if len(out[i].Models) == 0 {
			return nil, infraerrors.New(infraerrors.Category(400), "GROUP_MODEL_PRICING_MODELS_REQUIRED", "group model pricing entry requires at least one model")
		}
	}
	if err := v.PricingEntries(out); err != nil {
		return nil, err
	}
	return out, nil
}
