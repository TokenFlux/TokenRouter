// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type billingChannelStats struct{ Service *routing.ChannelService }

func (s billingChannelStats) AccountStatsGroup(ctx context.Context, id int64) (*billing.AccountStatsChannel, error) {
	channel, err := s.Service.GetChannelForGroup(ctx, id)
	if err != nil || channel == nil {
		return nil, err
	}
	return &billing.AccountStatsChannel{Rules: channel.AccountStatsPricingRules, ApplyUserPrice: channel.ApplyPricingToAccountStats}, nil
}
func (s billingChannelStats) AccountStatsPlatform(ctx context.Context, id int64) billing.AccountStatsPlatform {
	platform := s.Service.GetGroupPlatform(ctx, id)
	return billing.AccountStatsPlatform{ID: platform, PreferRequestedModel: platform == routing.PlatformQoder}
}
