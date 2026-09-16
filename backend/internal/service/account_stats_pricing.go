package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// LegacyAccountStatsSource 只投影旧渠道读取，规则由 billing 决定；S06 退出。
type LegacyAccountStatsSource struct{ Service *ChannelService }

func (s LegacyAccountStatsSource) AccountStatsGroup(ctx context.Context, id int64) (*billing.AccountStatsChannel, error) {
	channel, err := s.Service.GetChannelForGroup(ctx, id)
	if err != nil || channel == nil {
		return nil, err
	}
	return &billing.AccountStatsChannel{Rules: channel.AccountStatsPricingRules, ApplyUserPrice: channel.ApplyPricingToAccountStats}, nil
}
func (s LegacyAccountStatsSource) AccountStatsPlatform(ctx context.Context, id int64) billing.AccountStatsPlatform {
	platform := s.Service.GetGroupPlatform(ctx, id)
	return billing.AccountStatsPlatform{ID: platform, PreferRequestedModel: platform == PlatformQoder}
}
