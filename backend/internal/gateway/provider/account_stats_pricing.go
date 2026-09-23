package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// AccountStatsSource 只投影旧渠道读取，规则由 billing 决定；S06 退出。
type AccountStatsSource struct{ Service *routing.ChannelService }

func (s AccountStatsSource) AccountStatsGroup(ctx context.Context, id int64) (*billing.AccountStatsChannel, error) {
	channel, err := s.Service.GetChannelForGroup(ctx, id)
	if err != nil || channel == nil {
		return nil, err
	}
	return &billing.AccountStatsChannel{Rules: channel.AccountStatsPricingRules, ApplyUserPrice: channel.ApplyPricingToAccountStats}, nil
}
func (s AccountStatsSource) AccountStatsPlatform(ctx context.Context, id int64) billing.AccountStatsPlatform {
	platform := s.Service.GetGroupPlatform(ctx, id)
	return billing.AccountStatsPlatform{ID: platform, PreferRequestedModel: platform == capability.PlatformQoder}
}
