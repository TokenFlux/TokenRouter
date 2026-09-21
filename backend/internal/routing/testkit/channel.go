// Package testkit 构造渠道测试数据，缓存编译与查询均使用 routing 的唯一实现。
package testkit

import (
	"context"
	"time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

type channelRows struct {
	routing.ChannelRepository
	values    []routing.Channel
	platforms map[int64]string
}

func (r channelRows) ListAll(context.Context) ([]routing.Channel, error) { return r.values, nil }
func (r channelRows) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.platforms, nil
}
func (r channelRows) GetByID(_ context.Context, id int64) (*routing.Channel, error) {
	for i := range r.values {
		if r.values[i].ID == id {
			return r.values[i].Clone(), nil
		}
	}
	return nil, routing.ErrChannelNotFound
}

// Channel 保留原分组绑定、数据副本、时钟及定价时区加载器。
func Channel(groupID int64, platform string, value routing.Channel) *routing.ChannelService {
	cloned := value.Clone()
	cloned.GroupIDs = []int64{groupID}
	return routing.NewChannelService(channelRows{values: []routing.Channel{*cloned}, platforms: map[int64]string{groupID: platform}}, nil, routing.ChannelOptions{Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}
