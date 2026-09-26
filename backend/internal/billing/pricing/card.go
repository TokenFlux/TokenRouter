package pricing

import (
	"fmt"
	"math"
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
	BillingModeVideo      BillingMode = "video"       // 视频生成计费（按输出秒数）
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

// AccountStatsPricingRule 账号统计定价规则
// 每条规则包含匹配条件（分组/账号）和独立的模型定价。
// 多条规则按 SortOrder 排序，先命中为准。
type AccountStatsPricingRule struct {
	ID              int64
	PricingConfigID int64
	Name            string
	GroupIDs        []int64
	AccountIDs      []int64
	SortOrder       int
	Pricing         []ModelPricingEntry // 规则内的模型定价（复用现有定价结构）
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ModelPricingEntry 价卡模型定价条目
type ModelPricingEntry struct {
	ID                 int64       `json:"id,omitempty"`
	PricingConfigID    int64       `json:"pricing_config_id,omitempty"`
	Platform           string      `json:"platform"` // 所属平台（anthropic/openai/gemini/...）
	Models             []string    `json:"models"`
	BillingMode        BillingMode `json:"billing_mode"`
	PriceMultiplier    *float64    `json:"price_multiplier"`     // 最终定价倍率；nil 表示不调整价格
	FastModeMultiplier *float64    `json:"fast_mode_multiplier"` // OpenAI Fast 模式收费倍率；nil 表示沿用模型默认 Fast 定价
	// FastMultiplier 是新的通用 Fast/priority 倍率；为空时兼容旧字段。
	FastMultiplier *float64 `json:"fast_multiplier,omitempty"`
	// FlexMultiplier 是价卡级 Flex 倍率；为空时使用系统默认 0.5。
	FlexMultiplier *float64 `json:"flex_multiplier,omitempty"`
	// MaxReasoningEffortMultiplier 仅在最终转发档位为 max 时应用；nil 沿用模型默认倍率。
	MaxReasoningEffortMultiplier *float64 `json:"max_reasoning_effort_multiplier,omitempty"`
	InputPrice                   *float64 `json:"input_price"`
	OutputPrice                  *float64 `json:"output_price"`
	CacheWritePrice              *float64 `json:"cache_write_price"`
	// CacheWrite1hPrice 是可选的 1 小时缓存写入单价；为空时沿用 CacheWritePrice。
	CacheWrite1hPrice *float64           `json:"cache_write_1h_price"`
	CacheReadPrice    *float64           `json:"cache_read_price"`
	ImageInputPrice   *float64           `json:"image_input_price"`
	ImageOutputPrice  *float64           `json:"image_output_price"`
	PerRequestPrice   *float64           `json:"per_request_price"`
	Intervals         []PricingInterval  `json:"intervals"`
	TimePricing       *TimePricingConfig `json:"time_pricing,omitempty"`
	CreatedAt         time.Time          `json:"created_at,omitempty"`
	UpdatedAt         time.Time          `json:"updated_at,omitempty"`
}

// TimePricingConfig 价卡模型定价的分时倍率配置。
type TimePricingConfig struct {
	Timezone     string              `json:"timezone"`
	WeekdaysOnly bool                `json:"weekdays_only,omitempty"`
	Periods      []TimePricingPeriod `json:"periods"`
}

// TimePricingPeriod 是秒级左闭右开区间，并兼容历史 HH:mm 数据。
type TimePricingPeriod struct {
	StartTime  string  `json:"start_time"`
	EndTime    string  `json:"end_time"`
	Multiplier float64 `json:"multiplier"`
}

// PricingInterval 定价区间（token 区间 / 按次分层 / 图片分辨率分层）
type PricingInterval struct {
	ID              int64    `json:"id,omitempty"`
	PricingID       int64    `json:"pricing_id,omitempty"`
	MinTokens       int      `json:"min_tokens"`
	MaxTokens       *int     `json:"max_tokens"`
	TierLabel       string   `json:"tier_label"`
	InputPrice      *float64 `json:"input_price"`
	OutputPrice     *float64 `json:"output_price"`
	CacheWritePrice *float64 `json:"cache_write_price"`
	// CacheWrite1hPrice 是该区间的可选 1 小时缓存写入单价。
	CacheWrite1hPrice    *float64  `json:"cache_write_1h_price"`
	CacheReadPrice       *float64  `json:"cache_read_price"`
	InputMultiplier      *float64  `json:"input_multiplier,omitempty"`
	OutputMultiplier     *float64  `json:"output_multiplier,omitempty"`
	CacheWriteMultiplier *float64  `json:"cache_write_multiplier,omitempty"`
	CacheReadMultiplier  *float64  `json:"cache_read_multiplier,omitempty"`
	PerRequestPrice      *float64  `json:"per_request_price"`
	SortOrder            int       `json:"sort_order"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
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
func (p *ModelPricingEntry) GetIntervalForContext(totalTokens int) *PricingInterval {
	return FindMatchingInterval(p.Intervals, totalTokens)
}

// GetTierByLabel 根据标签查找层级（用于 per_request / image 模式）
func (p *ModelPricingEntry) GetTierByLabel(label string) *PricingInterval {
	labelLower := strings.ToLower(label)
	for i := range p.Intervals {
		if strings.ToLower(p.Intervals[i].TierLabel) == labelLower {
			return &p.Intervals[i]
		}
	}
	return nil
}

// HasEffectivePricing 判断该行是否配置了价格或可继承基础价的倍率。
// nil 价格指针表示“未配置”；指向 0 的指针表示显式免费价格，因此仍然有效。
func (p *ModelPricingEntry) HasEffectivePricing() bool {
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
			p.CacheWrite1hPrice != nil ||
			p.CacheReadPrice != nil ||
			p.ImageInputPrice != nil ||
			p.ImageOutputPrice != nil ||
			p.FastMultiplier != nil ||
			p.FlexMultiplier != nil ||
			p.MaxReasoningEffortMultiplier != nil ||
			(p.TimePricing != nil && len(p.TimePricing.Periods) > 0) {
			return true
		}
		for i := range p.Intervals {
			iv := p.Intervals[i]
			if iv.InputPrice != nil ||
				iv.OutputPrice != nil ||
				iv.CacheWritePrice != nil ||
				iv.CacheWrite1hPrice != nil ||
				iv.CacheReadPrice != nil ||
				iv.InputMultiplier != nil ||
				iv.OutputMultiplier != nil ||
				iv.CacheWriteMultiplier != nil ||
				iv.CacheReadMultiplier != nil {
				return true
			}
		}
	}
	return false
}

// Clone 返回 ModelPricingEntry 的拷贝；模型、区间和分时配置切片彼此独立。
func (p ModelPricingEntry) Clone() ModelPricingEntry {
	cp := p
	// 金额指针也必须独立，复制分组或覆盖倍率时不能修改源价卡。
	ClonePricingAmounts(&cp.PriceMultiplier, &cp.FastModeMultiplier, &cp.FastMultiplier, &cp.FlexMultiplier,
		&cp.MaxReasoningEffortMultiplier, &cp.InputPrice, &cp.OutputPrice, &cp.CacheWritePrice,
		&cp.CacheWrite1hPrice, &cp.CacheReadPrice, &cp.ImageInputPrice, &cp.ImageOutputPrice, &cp.PerRequestPrice)
	if p.Models != nil {
		cp.Models = make([]string, len(p.Models))
		copy(cp.Models, p.Models)
	}
	if p.Intervals != nil {
		cp.Intervals = make([]PricingInterval, len(p.Intervals))
		copy(cp.Intervals, p.Intervals)
		for i := range cp.Intervals {
			iv := &cp.Intervals[i]
			ClonePricingAmounts(&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice, &iv.CacheWrite1hPrice,
				&iv.CacheReadPrice, &iv.InputMultiplier, &iv.OutputMultiplier, &iv.CacheWriteMultiplier,
				&iv.CacheReadMultiplier, &iv.PerRequestPrice)
			if iv.MaxTokens != nil {
				maxTokens := *iv.MaxTokens
				iv.MaxTokens = &maxTokens
			}
		}
	}
	if p.TimePricing != nil {
		cp.TimePricing = &TimePricingConfig{
			Timezone:     p.TimePricing.Timezone,
			WeekdaysOnly: p.TimePricing.WeekdaysOnly,
		}
		if p.TimePricing.Periods != nil {
			cp.TimePricing.Periods = append([]TimePricingPeriod(nil), p.TimePricing.Periods...)
		}
	}
	return cp
}

// ClonePricingAmounts 复制可空金额值，保留 nil 与显式零价的区别。
func ClonePricingAmounts(fields ...**float64) {
	for _, field := range fields {
		if *field != nil {
			value := **field
			*field = &value
		}
	}
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
		if err := ValidateSingleInterval(&sorted[i], i); err != nil {
			return err
		}
	}

	// per_request / image 模式按 tier_label 匹配，不做 token 区间重叠校验
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		return nil
	}
	return ValidateIntervalOverlap(sorted)
}

// ValidateSingleInterval 校验单个区间的字段合法性
func ValidateSingleInterval(iv *PricingInterval, idx int) error {
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
	return ValidateIntervalPrices(iv, idx)
}

// ValidateIntervalPrices 校验区间内所有价格字段 >= 0
func ValidateIntervalPrices(iv *PricingInterval, idx int) error {
	prices := []struct {
		name string
		val  *float64
	}{
		{"input_price", iv.InputPrice},
		{"output_price", iv.OutputPrice},
		{"cache_write_price", iv.CacheWritePrice},
		{"cache_write_1h_price", iv.CacheWrite1hPrice},
		{"cache_read_price", iv.CacheReadPrice},
		{"per_request_price", iv.PerRequestPrice},
	}
	for _, p := range prices {
		if p.val != nil && (math.IsNaN(*p.val) || math.IsInf(*p.val, 0) || *p.val < 0) {
			return fmt.Errorf("interval #%d: %s must be >= 0", idx+1, p.name)
		}
	}
	multipliers := []struct {
		name string
		val  *float64
	}{
		{"input_multiplier", iv.InputMultiplier},
		{"output_multiplier", iv.OutputMultiplier},
		{"cache_write_multiplier", iv.CacheWriteMultiplier},
		{"cache_read_multiplier", iv.CacheReadMultiplier},
	}
	for _, multiplier := range multipliers {
		if multiplier.val != nil && (math.IsNaN(*multiplier.val) || math.IsInf(*multiplier.val, 0) || *multiplier.val <= 0) {
			return fmt.Errorf("interval #%d: %s must be > 0", idx+1, multiplier.name)
		}
	}
	return nil
}

// ValidateIntervalOverlap 校验排序后的区间列表无重叠，且无界区间在最后
func ValidateIntervalOverlap(sorted []PricingInterval) error {
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
				i, i+1, FormatMaxTokensLabel(prev.MaxTokens), iv.MinTokens)
		}
	}
	return nil
}

func FormatMaxTokensLabel(max *int) string {
	if max == nil {
		return "∞"
	}
	return fmt.Sprintf("%d", *max)
}
