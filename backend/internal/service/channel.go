package service

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// BillingMode 计费模式
type BillingMode string

const (
	BillingModeToken      BillingMode = "token"       // 按 token 区间计费
	BillingModePerRequest BillingMode = "per_request" // 按次计费（支持上下文窗口分层）
	BillingModeImage      BillingMode = "image"       // 图片计费（当前按次，预留 token 计费）
	BillingModeVideo      BillingMode = "video"       // 视频生成计费（按视频生成次数）
)

// IsValid 检查 BillingMode 是否为合法值
func (m BillingMode) IsValid() bool {
	switch m {
	case BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo, "":
		return true
	}
	return false
}

// IsValidUsageFilter 检查 BillingMode 是否可用于使用记录筛选。
func (m BillingMode) IsValidUsageFilter() bool {
	switch m {
	case BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo, "":
		return true
	}
	return false
}

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

// AccountStatsPricingRule 账号统计定价规则
// 每条规则包含匹配条件（分组/账号）和独立的模型定价。
// 多条规则按 SortOrder 排序，先命中为准。
type AccountStatsPricingRule struct {
	ID         int64
	ChannelID  int64
	Name       string
	GroupIDs   []int64
	AccountIDs []int64
	SortOrder  int
	Pricing    []ChannelModelPricing // 规则内的模型定价（复用现有定价结构）
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ChannelModelPricing 渠道模型定价条目
type ChannelModelPricing struct {
	ID                 int64             `json:"id,omitempty"`
	ChannelID          int64             `json:"channel_id,omitempty"`
	Platform           string            `json:"platform"` // 所属平台（anthropic/openai/gemini/...）
	Models             []string          `json:"models"`
	BillingMode        BillingMode       `json:"billing_mode"`
	PriceMultiplier    *float64          `json:"price_multiplier"`     // 最终定价倍率；nil 表示不调整价格
	FastModeMultiplier *float64          `json:"fast_mode_multiplier"` // OpenAI Fast 模式收费倍率；nil 表示沿用模型默认 Fast 定价
	InputPrice         *float64          `json:"input_price"`
	OutputPrice        *float64          `json:"output_price"`
	CacheWritePrice    *float64          `json:"cache_write_price"`
	CacheReadPrice     *float64          `json:"cache_read_price"`
	ImageInputPrice    *float64          `json:"image_input_price"`
	ImageOutputPrice   *float64          `json:"image_output_price"`
	PerRequestPrice    *float64          `json:"per_request_price"`
	Intervals          []PricingInterval `json:"intervals"`
	CreatedAt          time.Time         `json:"created_at,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at,omitempty"`
}

// PricingInterval 定价区间（token 区间 / 按次分层 / 图片分辨率分层）
type PricingInterval struct {
	ID              int64     `json:"id,omitempty"`
	PricingID       int64     `json:"pricing_id,omitempty"`
	MinTokens       int       `json:"min_tokens"`
	MaxTokens       *int      `json:"max_tokens"`
	TierLabel       string    `json:"tier_label"`
	InputPrice      *float64  `json:"input_price"`
	OutputPrice     *float64  `json:"output_price"`
	CacheWritePrice *float64  `json:"cache_write_price"`
	CacheReadPrice  *float64  `json:"cache_read_price"`
	PerRequestPrice *float64  `json:"per_request_price"`
	SortOrder       int       `json:"sort_order"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

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

// FindMatchingInterval 在区间列表中查找匹配 totalTokens 的区间。
// 区间为左开右闭 (min, max]：min 不含，max 包含。
// 第一个区间 min=0 时，0 token 不匹配任何区间（回退到默认价格）。
func FindMatchingInterval(intervals []PricingInterval, totalTokens int) *PricingInterval {
	for i := range intervals {
		iv := &intervals[i]
		if totalTokens > iv.MinTokens && (iv.MaxTokens == nil || totalTokens <= *iv.MaxTokens) {
			return iv
		}
	}
	return nil
}

// GetIntervalForContext 根据总 context token 数查找匹配的区间。
func (p *ChannelModelPricing) GetIntervalForContext(totalTokens int) *PricingInterval {
	return FindMatchingInterval(p.Intervals, totalTokens)
}

// GetTierByLabel 根据标签查找层级（用于 per_request / image 模式）
func (p *ChannelModelPricing) GetTierByLabel(label string) *PricingInterval {
	labelLower := strings.ToLower(label)
	for i := range p.Intervals {
		if strings.ToLower(p.Intervals[i].TierLabel) == labelLower {
			return &p.Intervals[i]
		}
	}
	return nil
}

// HasEffectivePricing 判断该行是否配置了明确价格。
// nil 价格指针表示“未配置”；指向 0 的指针表示显式免费价格，因此仍然有效。
func (p *ChannelModelPricing) HasEffectivePricing() bool {
	if p == nil {
		return false
	}
	mode := p.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	switch mode {
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		if p.PerRequestPrice != nil {
			return true
		}
		for i := range p.Intervals {
			if p.Intervals[i].PerRequestPrice != nil {
				return true
			}
		}
		return false
	default:
		if p.InputPrice != nil ||
			p.OutputPrice != nil ||
			p.CacheWritePrice != nil ||
			p.CacheReadPrice != nil ||
			p.ImageInputPrice != nil ||
			p.ImageOutputPrice != nil {
			return true
		}
		for i := range p.Intervals {
			iv := p.Intervals[i]
			if iv.InputPrice != nil ||
				iv.OutputPrice != nil ||
				iv.CacheWritePrice != nil ||
				iv.CacheReadPrice != nil {
				return true
			}
		}
	}
	return false
}

// Clone 返回 ChannelModelPricing 的拷贝（切片独立，指针字段共享，调用方只读安全）
func (p ChannelModelPricing) Clone() ChannelModelPricing {
	cp := p
	if p.Models != nil {
		cp.Models = make([]string, len(p.Models))
		copy(cp.Models, p.Models)
	}
	if p.Intervals != nil {
		cp.Intervals = make([]PricingInterval, len(p.Intervals))
		copy(cp.Intervals, p.Intervals)
	}
	return cp
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
		cp.FeaturesConfig = deepCopyFeaturesConfig(c.FeaturesConfig)
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

// deepCopyFeaturesConfig creates a deep copy of FeaturesConfig to prevent cache pollution.
func deepCopyFeaturesConfig(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if inner, ok := v.(map[string]any); ok {
			dst[k] = deepCopyFeaturesConfig(inner)
		} else {
			dst[k] = v
		}
	}
	return dst
}

// ValidateIntervals 校验区间列表的合法性。
//
// mode 决定区间语义：
//   - BillingModeToken（含空值）：区间是上下文 token 数分段 (min, max]，
//     按 MinTokens 排序后无重叠，无界区间（MaxTokens=nil）必须是最后一个。
//   - BillingModePerRequest / BillingModeImage：区间是按 tier_label
//     (1K/2K/4K 等) 分层，匹配走 label 不依赖 min/max，因此跳过区间重叠
//     与“无界区间必须最后”校验，仅做单条字段自洽（min/max/价格非负）检查。
//
// 通用规则：MinTokens >= 0；MaxTokens 若非 nil 则 > 0 且 > MinTokens；
// 所有价格字段 >= 0。
func ValidateIntervals(intervals []PricingInterval, mode BillingMode) error {
	if len(intervals) == 0 {
		return nil
	}
	sorted := make([]PricingInterval, len(intervals))
	copy(sorted, intervals)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].MinTokens < sorted[j].MinTokens
	})

	for i := range sorted {
		if err := validateSingleInterval(&sorted[i], i); err != nil {
			return err
		}
	}

	// per_request / image 模式按 tier_label 匹配，不做 token 区间重叠校验
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		return nil
	}
	return validateIntervalOverlap(sorted)
}

// validateSingleInterval 校验单个区间的字段合法性
func validateSingleInterval(iv *PricingInterval, idx int) error {
	if iv.MinTokens < 0 {
		return fmt.Errorf("interval #%d: min_tokens (%d) must be >= 0", idx+1, iv.MinTokens)
	}
	if iv.MaxTokens != nil {
		if *iv.MaxTokens <= 0 {
			return fmt.Errorf("interval #%d: max_tokens (%d) must be > 0", idx+1, *iv.MaxTokens)
		}
		if *iv.MaxTokens <= iv.MinTokens {
			return fmt.Errorf("interval #%d: max_tokens (%d) must be > min_tokens (%d)",
				idx+1, *iv.MaxTokens, iv.MinTokens)
		}
	}
	return validateIntervalPrices(iv, idx)
}

// validateIntervalPrices 校验区间内所有价格字段 >= 0
func validateIntervalPrices(iv *PricingInterval, idx int) error {
	prices := []struct {
		name string
		val  *float64
	}{
		{"input_price", iv.InputPrice},
		{"output_price", iv.OutputPrice},
		{"cache_write_price", iv.CacheWritePrice},
		{"cache_read_price", iv.CacheReadPrice},
		{"per_request_price", iv.PerRequestPrice},
	}
	for _, p := range prices {
		if p.val != nil && *p.val < 0 {
			return fmt.Errorf("interval #%d: %s must be >= 0", idx+1, p.name)
		}
	}
	return nil
}

// validateIntervalOverlap 校验排序后的区间列表无重叠，且无界区间在最后
func validateIntervalOverlap(sorted []PricingInterval) error {
	for i, iv := range sorted {
		// 无界区间必须是最后一个
		if iv.MaxTokens == nil && i < len(sorted)-1 {
			return fmt.Errorf("interval #%d: unbounded interval (max_tokens=null) must be the last one",
				i+1)
		}
		if i == 0 {
			continue
		}
		prev := sorted[i-1]
		// 检查重叠：前一个区间的上界 > 当前区间的下界则重叠
		// (min, max] 语义：prev 覆盖 (prev.Min, prev.Max]，cur 覆盖 (cur.Min, cur.Max]
		if prev.MaxTokens == nil || *prev.MaxTokens > iv.MinTokens {
			return fmt.Errorf("interval #%d and #%d overlap: prev max=%s > cur min=%d",
				i, i+1, formatMaxTokensLabel(prev.MaxTokens), iv.MinTokens)
		}
	}
	return nil
}

func formatMaxTokensLabel(max *int) string {
	if max == nil {
		return "∞"
	}
	return fmt.Sprintf("%d", *max)
}

// ChannelUsageFields 渠道相关的使用记录字段（嵌入到各平台的 RecordUsageInput 中）
type ChannelUsageFields struct {
	ChannelID          int64  // 渠道 ID（0 = 无渠道）
	OriginalModel      string // 用户原始请求模型（渠道映射前）
	ChannelMappedModel string // 渠道映射后的模型名（无映射时等于 OriginalModel）
	BillingModelSource string // 计费模型来源："requested" / "upstream" / "channel_mapped"
	ModelMappingChain  string // 映射链描述，如 "a→b→c"
}
