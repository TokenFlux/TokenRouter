// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package routing_test

import (
	context "context"
	slices "slices"
	sort "sort"
	time "time"

	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// channelModelKey 渠道缓存复合键（显式包含 platform 防止跨平台同名模型冲突）
type channelModelKey struct {
	groupID  int64
	platform string // 平台标识
	model    string // lowercase
}

// channelGroupPlatformKey 通配符定价缓存键
type channelGroupPlatformKey struct {
	groupID  int64
	platform string
}

// wildcardPricingEntry 通配符定价条目
type wildcardPricingEntry struct {
	prefix  string
	pricing *routing.ChannelModelPricing
}

// wildcardMappingEntry 通配符映射条目
type wildcardMappingEntry struct {
	prefix string
	target string
}

// channelCache 渠道缓存快照（扁平化哈希结构，热路径 O(1) 查找）
type channelCache struct {
	sourceChannels []routing.Channel
	fromChannels   bool
	// 热路径查找
	pricingByGroupModel     map[channelModelKey]*routing.ChannelModelPricing    // (groupID, platform, model) → 定价
	wildcardByGroupPlatform map[channelGroupPlatformKey][]*wildcardPricingEntry // (groupID, platform) → 通配符定价（按配置顺序，先匹配先使用）
	mappingByGroupModel     map[channelModelKey]string                          // (groupID, platform, model) → 映射目标
	wildcardMappingByGP     map[channelGroupPlatformKey][]*wildcardMappingEntry // (groupID, platform) → 通配符映射（按配置顺序，先匹配先使用）
	channelByGroupID        map[int64]*routing.Channel                          // groupID → 渠道
	groupPlatform           map[int64]string                                    // groupID → platform

	// 冷路径（CRUD 操作）
	byID     map[int64]*routing.Channel
	loadedAt time.Time
}

// newEmptyChannelCache 创建空的渠道缓存（所有 map 已初始化）
func newEmptyChannelCache() *channelCache {
	return &channelCache{
		pricingByGroupModel:     make(map[channelModelKey]*routing.ChannelModelPricing),
		wildcardByGroupPlatform: make(map[channelGroupPlatformKey][]*wildcardPricingEntry),
		mappingByGroupModel:     make(map[channelModelKey]string),
		wildcardMappingByGP:     make(map[channelGroupPlatformKey][]*wildcardMappingEntry),
		channelByGroupID:        make(map[int64]*routing.Channel),
		groupPlatform:           make(map[int64]string),
		byID:                    make(map[int64]*routing.Channel),
	}
}

type legacyChannelFixtureRepo struct {
	routing.ChannelRepository
	channels  []routing.Channel
	platforms map[int64]string
}

func (r *legacyChannelFixtureRepo) ListAll(context.Context) ([]routing.Channel, error) {
	return r.channels, nil
}
func (r *legacyChannelFixtureRepo) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.platforms, nil
}
func (r *legacyChannelFixtureRepo) GetByID(_ context.Context, id int64) (*routing.Channel, error) {
	for i := range r.channels {
		if r.channels[i].ID == id {
			return r.channels[i].Clone(), nil
		}
	}
	return nil, routing.ErrChannelNotFound
}
func seedChannelFixture(fixture *channelCache) *routing.ChannelService {
	var channels []routing.Channel
	if fixture.fromChannels {
		channels = fixture.sourceChannels
	} else {
		ids := make([]int64, 0, len(fixture.channelByGroupID))
		for gid := range fixture.channelByGroupID {
			ids = append(ids, gid)
		}
		slices.Sort(ids)
		for _, gid := range ids {
			ch := fixture.channelByGroupID[gid].Clone()
			ch.GroupIDs = []int64{gid}
			// 展平价卡夹具只作数据反投影，不复制查找或定价算法。
			keys := make([]channelModelKey, 0, len(fixture.pricingByGroupModel))
			for key := range fixture.pricingByGroupModel {
				if key.groupID == gid {
					keys = append(keys, key)
				}
			}
			sort.Slice(keys, func(i, j int) bool {
				if keys[i].platform != keys[j].platform {
					return keys[i].platform < keys[j].platform
				}
				return keys[i].model < keys[j].model
			})
			for _, key := range keys {
				price := fixture.pricingByGroupModel[key].Clone()
				price.Platform = key.platform
				price.Models = []string{key.model}
				ch.ModelPricing = append(ch.ModelPricing, price)
			}
			for key, entries := range fixture.wildcardByGroupPlatform {
				if key.groupID == gid {
					for _, entry := range entries {
						price := entry.pricing.Clone()
						price.Platform = key.platform
						price.Models = []string{entry.prefix + "*"}
						ch.ModelPricing = append(ch.ModelPricing, price)
					}
				}
			}
			if ch.ModelMapping == nil {
				ch.ModelMapping = make(map[string]map[string]string)
			}
			for key, target := range fixture.mappingByGroupModel {
				if key.groupID == gid {
					if ch.ModelMapping[key.platform] == nil {
						ch.ModelMapping[key.platform] = make(map[string]string)
					}
					ch.ModelMapping[key.platform][key.model] = target
				}
			}
			for key, entries := range fixture.wildcardMappingByGP {
				if key.groupID == gid {
					if ch.ModelMapping[key.platform] == nil {
						ch.ModelMapping[key.platform] = make(map[string]string)
					}
					for _, entry := range entries {
						ch.ModelMapping[key.platform][entry.prefix+"*"] = entry.target
					}
				}
			}
			channels = append(channels, *ch)
		}
	}
	return routing.NewChannelService(&legacyChannelFixtureRepo{channels: channels, platforms: fixture.groupPlatform}, nil, routing.ChannelOptions{Now: time.Now, LoadLocation: provider.LoadPricingLocation})
}
