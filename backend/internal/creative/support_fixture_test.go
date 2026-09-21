//go:build unit

package creative_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// creativeUserCacheFixture 只替代本组 worker 使用的用户槽位；其它调用会因未注入而失败。
type creativeUserCacheFixture struct {
	scheduler.ConcurrencyCache
	acquireResult bool
}

func (c *creativeUserCacheFixture) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return c.acquireResult, nil
}
func (c *creativeUserCacheFixture) ReleaseUserSlot(context.Context, int64, string) error { return nil }

func testImageModelPricing(prices map[string]*float64) []routing.ChannelModelPricing {
	card := routing.ChannelModelPricing{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ChannelModelPricing{card}
}

// 价卡数据和原基础价格保持一致，缓存编译与报价使用真实模块。
type creativeChannelFixture struct {
	routing.ChannelRepository
	cards    []routing.ChannelModelPricing
	platform string
}

func (r *creativeChannelFixture) ListAll(context.Context) ([]routing.Channel, error) {
	return []routing.Channel{{ID: 1, Name: "test-channel", Status: billing.StatusActive, GroupIDs: []int64{100}, ModelPricing: r.cards}}, nil
}
func (r *creativeChannelFixture) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{100: r.platform}, nil
}
func newResolverWithChannel(t *testing.T, cards []routing.ChannelModelPricing) *billing.PriceResolver {
	t.Helper()
	platform := capability.PlatformAnthropic
	if len(cards) > 0 && cards[0].Platform != "" {
		platform = cards[0].Platform
	}
	calculator := billingtestkit.Calculator(0, nil, map[string]*pricing.ModelPricing{"claude-sonnet-4": {InputPricePerToken: 3e-6, OutputPricePerToken: 15e-6, CacheCreationPricePerToken: 3.75e-6, CacheReadPricePerToken: 0.3e-6, SupportsCacheBreakdown: false}})
	channels := routing.NewChannelService(&creativeChannelFixture{cards: cards, platform: platform}, nil, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
	return billing.NewPriceResolver(channels, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	})
}
