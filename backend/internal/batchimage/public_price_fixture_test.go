//go:build unit

package batchimage_test

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// 渠道与价卡夹具只提供原输入，编译和报价仍调用真实模块。
type publicChannelFixture struct {
	routing.ChannelRepository
	channel   routing.Channel
	platforms map[int64]string
}

func (r *publicChannelFixture) ListAll(context.Context) ([]routing.Channel, error) {
	return []routing.Channel{r.channel}, nil
}
func (r *publicChannelFixture) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.platforms, nil
}
func makePublicChannelFixture(channel routing.Channel, platforms map[int64]string) *publicChannelFixture {
	return &publicChannelFixture{channel: channel, platforms: platforms}
}
func newPublicChannelFixture(repo *publicChannelFixture) *routing.ChannelService {
	return routing.NewChannelService(repo, nil, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}
func publicPriceResolverFixture() *billing.PriceResolver {
	return billing.NewPriceResolver(nil, billingtestkit.Calculator(0, nil, nil), modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	})
}
func testImageModelPricing(prices map[string]*float64) []routing.ChannelModelPricing {
	card := routing.ChannelModelPricing{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ChannelModelPricing{card}
}
