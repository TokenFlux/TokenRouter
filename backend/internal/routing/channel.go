// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	pricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"maps"
	"slices"
	strings "strings"
	time "time"
)

// BillingMode 保留价卡类型的旧入口。
type BillingMode = pricing.BillingMode

const BillingModeToken = pricing.BillingModeToken

const BillingModePerRequest = pricing.BillingModePerRequest

const BillingModeImage = pricing.BillingModeImage

const BillingModeVideo = pricing.BillingModeVideo

const (
	BillingModelSourceRequested     = "requested"
	BillingModelSourceUpstream      = "upstream"
	BillingModelSourceChannelMapped = "channel_mapped"
)

// Channel 渠道实体
type Channel struct {
	ID                 int64
	Name               string
	Description        string
	Status             string
	BillingModelSource string         // "requested", "upstream", or "channel_mapped"
	RestrictModels     bool           // 是否限制模型（仅允许定价列表中的模型）
	Features           string         // 渠道特性描述（JSON 数组），用于支付页面展示
	FeaturesConfig     map[string]any // 渠道功能配置（如 web search emulation）
	CreatedAt          time.Time
	UpdatedAt          time.Time

	// 关联的分组 ID 列表
	GroupIDs []int64
	// 模型定价列表（每条含 Platform 字段）
	ModelPricing []ChannelModelPricing
	// 渠道级模型映射（按平台分组：platform → {src→dst}）
	ModelMapping map[string]map[string]string

	// 账号统计定价
	ApplyPricingToAccountStats bool                      // 是否应用渠道模型定价到账号统计
	AccountStatsPricingRules   []AccountStatsPricingRule // 自定义账号统计定价规则（按 SortOrder 排序，先命中为准）
}

// AccountStatsPricingRule 保留价卡类型的旧入口。
type AccountStatsPricingRule = pricing.AccountStatsPricingRule

// ChannelModelPricing 保留价卡类型的旧入口。
type ChannelModelPricing = pricing.ChannelModelPricing

// ChannelTimePricing 保留价卡类型的旧入口。
type ChannelTimePricing = pricing.ChannelTimePricing

// ChannelTimePricingPeriod 保留价卡类型的旧入口。
type ChannelTimePricingPeriod = pricing.ChannelTimePricingPeriod

// PricingInterval 保留价卡类型的旧入口。
type PricingInterval = pricing.PricingInterval

// IsActive 判断渠道是否启用
func (c *Channel) IsActive() bool {
	return c.Status == StatusActive
}

// GetModelPricing 根据模型名查找渠道定价，未找到返回 nil。
// 精确匹配，大小写不敏感。返回值拷贝，不污染缓存。
func (c *Channel) GetModelPricing(model string) *ChannelModelPricing {
	modelLower := strings.ToLower(model)

	for i := range c.ModelPricing {
		for _, m := range c.ModelPricing[i].Models {
			if strings.ToLower(m) == modelLower {
				cp := c.ModelPricing[i].Clone()
				return &cp
			}
		}
	}

	return nil
}

// FindMatchingInterval 委托唯一纯定价实现，保留旧调用签名。
func FindMatchingInterval(intervals []PricingInterval, totalTokens int) *PricingInterval {
	return pricing.FindMatchingInterval(intervals, totalTokens)
}

// Clone 返回 Channel 的深拷贝
func (c *Channel) Clone() *Channel {
	if c == nil {
		return nil
	}
	cp := *c
	if c.GroupIDs != nil {
		cp.GroupIDs = make([]int64, len(c.GroupIDs))
		copy(cp.GroupIDs, c.GroupIDs)
	}
	if c.ModelPricing != nil {
		cp.ModelPricing = make([]ChannelModelPricing, len(c.ModelPricing))
		for i := range c.ModelPricing {
			cp.ModelPricing[i] = c.ModelPricing[i].Clone()
		}
	}
	if c.ModelMapping != nil {
		cp.ModelMapping = make(map[string]map[string]string, len(c.ModelMapping))
		for platform, mapping := range c.ModelMapping {
			inner := make(map[string]string, len(mapping))
			for k, v := range mapping {
				inner[k] = v
			}
			cp.ModelMapping[platform] = inner
		}
	}
	if c.FeaturesConfig != nil {
		cp.FeaturesConfig = DeepCopyFeaturesConfig(c.FeaturesConfig)
	}
	if c.AccountStatsPricingRules != nil {
		cp.AccountStatsPricingRules = make([]AccountStatsPricingRule, len(c.AccountStatsPricingRules))
		for i, rule := range c.AccountStatsPricingRules {
			cp.AccountStatsPricingRules[i] = rule
			if rule.GroupIDs != nil {
				cp.AccountStatsPricingRules[i].GroupIDs = make([]int64, len(rule.GroupIDs))
				copy(cp.AccountStatsPricingRules[i].GroupIDs, rule.GroupIDs)
			}
			if rule.AccountIDs != nil {
				cp.AccountStatsPricingRules[i].AccountIDs = make([]int64, len(rule.AccountIDs))
				copy(cp.AccountStatsPricingRules[i].AccountIDs, rule.AccountIDs)
			}
			if rule.Pricing != nil {
				cp.AccountStatsPricingRules[i].Pricing = make([]ChannelModelPricing, len(rule.Pricing))
				for j := range rule.Pricing {
					cp.AccountStatsPricingRules[i].Pricing[j] = rule.Pricing[j].Clone()
				}
			}
		}
	}
	return &cp
}

// IsWebSearchEmulationEnabled 返回该渠道是否为指定平台启用了 web search 模拟。
func (c *Channel) IsWebSearchEmulationEnabled(platform string) bool {
	if c == nil || c.FeaturesConfig == nil {
		return false
	}
	wse, ok := c.FeaturesConfig[featureKeyWebSearchEmulation].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := wse[platform].(bool)
	return ok && enabled
}

// IsBedrockCCCompatEnabled 返回该渠道是否启用了 Bedrock CC 兼容模式。
// 兼容新版布尔开关与既有按平台保存的 map 结构，避免旧 UI 配置失效。
func (c *Channel) IsBedrockCCCompatEnabled(platform string) bool {
	if c == nil || c.FeaturesConfig == nil {
		return false
	}
	raw, ok := c.FeaturesConfig[featureKeyBedrockCCCompat]
	if !ok {
		return false
	}
	if enabled, ok := raw.(bool); ok {
		return enabled
	}
	if byPlatform, ok := raw.(map[string]any); ok {
		enabled, ok := byPlatform[platform].(bool)
		return ok && enabled
	}
	if byPlatform, ok := raw.(map[string]bool); ok {
		return byPlatform[platform]
	}
	return false
}

// DeepCopyFeaturesConfig 隔离配置中的嵌套 JSON 值，保持原数值类型。
func DeepCopyFeaturesConfig(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		if inner, ok := value.(map[string]any); ok {
			dst[key] = DeepCopyFeaturesConfig(inner)
		} else {
			dst[key] = cloneFeatureValue(value)
		}
	}
	return dst
}

func cloneFeatureValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if v == nil {
			return map[string]any(nil)
		}
		return DeepCopyFeaturesConfig(v)
	case []any:
		if v == nil {
			return []any(nil)
		}
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneFeatureValue(v[i])
		}
		return out
	case map[string]bool:
		return maps.Clone(v)
	case map[string]string:
		return maps.Clone(v)
	case []string:
		return slices.Clone(v)
	default:
		return value
	}
}

// ValidateIntervals 委托唯一纯定价实现，保留旧调用签名。
func ValidateIntervals(intervals []PricingInterval, mode BillingMode) error {
	return pricing.ValidateIntervals(intervals, mode)
}

// ChannelUsageFields 渠道相关的使用记录字段（嵌入到各平台的 RecordUsageInput 中）
type ChannelUsageFields struct {
	ChannelID          int64  // 渠道 ID（0 = 无渠道）
	OriginalModel      string // 用户原始请求模型（渠道映射前）
	ChannelMappedModel string // 渠道映射后的模型名（无映射时等于 OriginalModel）
	BillingModelSource string // 计费模型来源："requested" / "upstream" / "channel_mapped"
	ModelMappingChain  string // 映射链描述，如 "a→b→c"
}
