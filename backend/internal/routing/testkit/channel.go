// Package testkit 构造渠道测试数据，缓存编译与查询均使用 routing 的唯一实现。
package testkit

import (
	"context"
	"time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// ChannelRows 仅提供可替换的持久化输入；查询缓存仍由真实渠道服务管理。
type ChannelRows struct {
	routing.ChannelRepository
	Values    []routing.Channel
	Platforms map[int64]string
}

func (r ChannelRows) ListAll(context.Context) ([]routing.Channel, error) { return r.Values, nil }
func (r ChannelRows) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.Platforms, nil
}
func (r ChannelRows) GetByID(_ context.Context, id int64) (*routing.Channel, error) {
	for i := range r.Values {
		if r.Values[i].ID == id {
			return r.Values[i].Clone(), nil
		}
	}
	return nil, routing.ErrChannelNotFound
}

// Channel 保留原分组绑定、数据副本、时钟及定价时区加载器。
func Channel(groupID int64, platform string, value routing.Channel) *routing.ChannelService {
	cloned := value.Clone()
	cloned.GroupIDs = []int64{groupID}
	return routing.NewChannelService(ChannelRows{Values: []routing.Channel{*cloned}, Platforms: map[int64]string{groupID: platform}}, nil, routing.ChannelOptions{Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}
