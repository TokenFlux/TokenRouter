package service

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// NewModelPricingResolver 仅组合测试输入，测试直接使用原生解析器。
func NewModelPricingResolver(channels *routing.ChannelService, calculator *billing.Calculator) *billing.PriceResolver {
	var source billing.ChannelPrices
	var stats billing.AccountStatsSource
	if channels != nil {
		source = channels
		stats = LegacyAccountStatsSource{Service: channels}
	}
	return billing.NewPriceResolver(source, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	}, stats)
}
