package billing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// 测试只投影配置，不保存第二份计算器状态。
func newCalculator(cfg *config.Config, catalog *provider.PricingService) *billing.Calculator {
	return newCalculatorWithPrices(cfg, catalog, nil)
}

func newCalculatorWithPrices(cfg *config.Config, catalog *provider.PricingService, prices map[string]*pricing.ModelPricing) *billing.Calculator {
	multiplier := 0.0
	if cfg != nil {
		multiplier = cfg.Default.RateMultiplier
	}
	return testkit.Calculator(multiplier, catalog, prices)
}
