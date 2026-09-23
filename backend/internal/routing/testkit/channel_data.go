// 本文件将原测试输入转换为渠道记录，编译和查价均使用真实渠道服务。
package testkit

import (
	slices "slices"
	sort "sort"
	time "time"

	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// ModelKey 渠道测试输入复合键（显式包含 Platform 防止跨平台同名模型冲突）
type ModelKey struct {
	GroupID  int64
	Platform string // 平台标识
	Model    string // lowercase
}

// GroupPlatform 通配符定价输入键
type GroupPlatform struct {
	GroupID  int64
	Platform string
}

// PricePattern 通配符定价条目
type PricePattern struct {
	Prefix  string
	Pricing *routing.ChannelModelPricing
}

// ModelPattern 通配符映射条目
type ModelPattern struct {
	Prefix string
	Target string
}

// ChannelData 渠道测试输入，不持有运行缓存或查价算法
type ChannelData struct {
	Channels []routing.Channel
	UseRows  bool
	// 测试指定的价格和模型输入
	Prices        map[ModelKey]*routing.ChannelModelPricing // (GroupID, Platform, Model) → 定价
	PricePatterns map[GroupPlatform][]*PricePattern         // (GroupID, Platform) → 通配符定价（按配置顺序，先匹配先使用）
	Models        map[ModelKey]string                       // (GroupID, Platform, Model) → 映射目标
	ModelPatterns map[GroupPlatform][]*ModelPattern         // (GroupID, Platform) → 通配符映射（按配置顺序，先匹配先使用）
	ByGroup       map[int64]*routing.Channel                // GroupID → 渠道
	Platforms     map[int64]string                          // GroupID → Platform

	// 原测试记录的目录元数据
	ByID     map[int64]*routing.Channel
	LoadedAt time.Time
}

// NewChannelData 创建空的渠道输入（所有 map 已初始化）
func NewChannelData() *ChannelData {
	return &ChannelData{
		Prices:        make(map[ModelKey]*routing.ChannelModelPricing),
		PricePatterns: make(map[GroupPlatform][]*PricePattern),
		Models:        make(map[ModelKey]string),
		ModelPatterns: make(map[GroupPlatform][]*ModelPattern),
		ByGroup:       make(map[int64]*routing.Channel),
		Platforms:     make(map[int64]string),
		ByID:          make(map[int64]*routing.Channel),
	}
}

// ChannelDataFromRows 只记录旧测试的输入，真正编译在 routing 的生产入口执行。
func ChannelDataFromRows(channels []routing.Channel, platforms map[int64]string) *ChannelData {
	fixture := NewChannelData()
	fixture.Channels = channels
	fixture.UseRows = true
	fixture.Platforms = platforms
	return fixture
}

// ChannelFromData 将夹具数据装配到唯一的生产渠道实现。
func ChannelFromData(fixture *ChannelData) *routing.ChannelService {
	var channels []routing.Channel
	if fixture.UseRows {
		channels = fixture.Channels
	} else {
		ids := make([]int64, 0, len(fixture.ByGroup))
		for gid := range fixture.ByGroup {
			ids = append(ids, gid)
		}
		slices.Sort(ids)
		for _, gid := range ids {
			ch := fixture.ByGroup[gid].Clone()
			ch.GroupIDs = []int64{gid}
			// 展平价卡夹具只作数据反投影，不复制查找或定价算法。
			keys := make([]ModelKey, 0, len(fixture.Prices))
			for key := range fixture.Prices {
				if key.GroupID == gid {
					keys = append(keys, key)
				}
			}
			sort.Slice(keys, func(i, j int) bool {
				if keys[i].Platform != keys[j].Platform {
					return keys[i].Platform < keys[j].Platform
				}
				return keys[i].Model < keys[j].Model
			})
			for _, key := range keys {
				price := fixture.Prices[key].Clone()
				price.Platform = key.Platform
				price.Models = []string{key.Model}
				ch.ModelPricing = append(ch.ModelPricing, price)
			}
			for key, entries := range fixture.PricePatterns {
				if key.GroupID == gid {
					for _, entry := range entries {
						price := entry.Pricing.Clone()
						price.Platform = key.Platform
						price.Models = []string{entry.Prefix + "*"}
						ch.ModelPricing = append(ch.ModelPricing, price)
					}
				}
			}
			if ch.ModelMapping == nil {
				ch.ModelMapping = make(map[string]map[string]string)
			}
			for key, target := range fixture.Models {
				if key.GroupID == gid {
					if ch.ModelMapping[key.Platform] == nil {
						ch.ModelMapping[key.Platform] = make(map[string]string)
					}
					ch.ModelMapping[key.Platform][key.Model] = target
				}
			}
			for key, entries := range fixture.ModelPatterns {
				if key.GroupID == gid {
					if ch.ModelMapping[key.Platform] == nil {
						ch.ModelMapping[key.Platform] = make(map[string]string)
					}
					for _, entry := range entries {
						ch.ModelMapping[key.Platform][entry.Prefix+"*"] = entry.Target
					}
				}
			}
			channels = append(channels, *ch)
		}
	}
	return routing.NewChannelService(ChannelRows{Values: channels, Platforms: fixture.Platforms}, nil, routing.ChannelOptions{Now: time.Now, LoadLocation: provider.LoadPricingLocation})
}
