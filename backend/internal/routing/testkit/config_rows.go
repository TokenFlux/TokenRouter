// Package testkit 构造模型配置测试数据，缓存编译与查询均使用 routing 的唯一实现。
package testkit

import (
	"context"
	"time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// ConfigRows 仅提供可替换的持久化输入；查询缓存仍由真实模型配置服务管理。
type ConfigRows struct {
	routing.PricingConfigRepository
	Values    []Configuration
	Platforms map[int64]string
}

func (r ConfigRows) ListAll(context.Context) ([]Configuration, error) { return r.Values, nil }
func (r ConfigRows) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.Platforms, nil
}

func (r ConfigRows) GetByID(_ context.Context, id int64) (*Configuration, error) {
	for i := range r.Values {
		if r.Values[i].ID == id {
			return r.Values[i].Clone(), nil
		}
	}
	return nil, routing.ErrPricingConfigNotFound
}

// PricingConfig 保留原分组绑定、数据副本、时钟及定价时区加载器。
func PricingConfig(groupID int64, platform string, value Configuration) *routing.PricingConfigService {
	cloned := value.Clone()
	cloned.GroupIDs = []int64{groupID}
	return NewPricingConfigService(ConfigRows{Values: []Configuration{*cloned}, Platforms: map[int64]string{groupID: platform}}, nil, routing.PricingConfigOptions{Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}
