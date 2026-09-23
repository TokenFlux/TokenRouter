package billing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// projectPriceGroup 仅组合测试价卡输入，计算使用唯一原生实现。
func projectPriceGroup(group *routing.Group) *billing.PriceGroup {
	if group == nil {
		return nil
	}
	return &billing.PriceGroup{ModelPricing: group.ModelPricing, LongContextPricingEnabled: group.LongContextPricingEnabled}
}
