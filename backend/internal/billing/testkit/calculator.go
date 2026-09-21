package testkit

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// Calculator 组合跨模块计费测试的输入；实例和所有计算仍由 billing 拥有。
func Calculator(multiplier float64, catalog *provider.PricingService, prices map[string]*pricing.ModelPricing) *billing.Calculator {
	var source billing.PriceCatalog
	if catalog != nil {
		source = catalog
	}
	warnings := &provider.PricingWarnings{}
	return billing.NewCalculator(source, billing.CalculatorOptions{
		DefaultRateMultiplier: multiplier,
		FallbackPrices:        prices,
		ModelPolicy:           modelidentity.PricingPolicy,
		Now:                   timezone.NewCalendar(time.Local).Now,
		LoadLocation:          provider.LoadPricingLocation,
		FallbackWarning:       warnings.Fallback,
	})
}
