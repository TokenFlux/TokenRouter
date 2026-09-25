//go:build integration

package routing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// 通过路由存储验证分组字段的保存与回读。
func (s *GroupRepoSuite) TestMediaCardsRoundTrip() {
	zero, price := 0.0, 0.15
	group := &routing.Group{Name: "media-card-roundtrip", Platform: capability.PlatformGrok, Status: billing.StatusActive, RateMultiplier: 1.5, AllowImageGeneration: true, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.6,
		ModelPricing: []routing.ChannelModelPricing{
			{Models: []string{"grok-imagine-image"}, Platform: capability.PlatformGrok, BillingMode: routing.BillingModeImage, PerRequestPrice: &zero},
			{Models: []string{"grok-imagine-video"}, Platform: capability.PlatformGrok, BillingMode: routing.BillingModeVideo, PerRequestPrice: &price, Intervals: []routing.PricingInterval{{TierLabel: "720p", PerRequestPrice: &zero}}},
		},

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformGrok),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformGrok),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))
	got, err := s.repo.GetByIDLite(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(group.ModelPricing, got.ModelPricing)
	got.ModelPricing[1].Intervals[0].PerRequestPrice = &price
	s.Require().NoError(s.repo.Update(s.ctx, got))
	updated, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(got.ModelPricing, updated.ModelPricing)
	s.Require().Equal(0.5, updated.BatchImageDiscountMultiplier)
	s.Require().Equal(0.6, updated.BatchImageHoldMultiplier)
	s.Require().True(updated.AllowImageGeneration)
}
