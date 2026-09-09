package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
)

// APIKeyRateLimitCacheData holds rate limit usage data cached in Redis.
type APIKeyRateLimitCacheData struct {
	Usage5h  float64 `json:"usage_5h"`
	Usage1d  float64 `json:"usage_1d"`
	Usage7d  float64 `json:"usage_7d"`
	Window5h int64   `json:"window_5h"` // unix timestamp, 0 = not started
	Window1d int64   `json:"window_1d"`
	Window7d int64   `json:"window_7d"`
}

// UserPlatformQuotaKey 标识一个 user×platform，用于脏集出入与批量读。
type UserPlatformQuotaKey struct {
	UserID   int64
	Platform string
}

// UserPlatformQuotaCacheEntry Redis hash 反序列化结果。
//
// SchemaVersion 用于向后兼容：
//   - 0（旧 entry，无 SchemaVersion 字段）→ 视为 cache MISS，强制 refresh
//   - 1（当前版本）→ 包含 limits 和 window_start，可免 DB 查询
//
// limit 字段为 nil 表示"无限额"（DB 中对应列为 NULL）。
const UserPlatformQuotaCacheSchemaV1 = int64(1)

type UserPlatformQuotaCacheEntry struct {
	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64
	Version         int64
	SchemaVersion   int64

	// 以下字段仅在 SchemaVersion >= 1 时有效
	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64

	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time
}

// BillingCache defines cache operations for billing service
type BillingCache interface {
	// Balance operations
	GetUserBalance(ctx context.Context, userID int64) (float64, error)
	SetUserBalance(ctx context.Context, userID int64, balance float64) error
	DeductUserBalance(ctx context.Context, userID int64, amount float64) error
	InvalidateUserBalance(ctx context.Context, userID int64) error

	// API Key rate limit operations
	GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*APIKeyRateLimitCacheData, error)
	SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *APIKeyRateLimitCacheData) error
	UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error
	InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error

	// user × platform quota 缓存
	GetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) (*UserPlatformQuotaCacheEntry, bool, error)
	SetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string, entry *UserPlatformQuotaCacheEntry, ttl time.Duration) error
	DeleteUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) error
	// IncrUserPlatformQuotaUsageCache 在缓存命中时累加用量；缓存未命中（key 不存在）静默返回 nil。
	// markDirty=true 时将该 key 的 member 写入 Redis 脏集，供 flusher 批量回写 DB。
	IncrUserPlatformQuotaUsageCache(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) error

	// 脏集读写，供 flusher 使用。
	PopDirtyUserPlatformQuotaKeys(ctx context.Context, n int) ([]UserPlatformQuotaKey, error)
	ReaddDirtyUserPlatformQuotaKeys(ctx context.Context, keys []UserPlatformQuotaKey) error
	BatchGetUserPlatformQuotaCache(ctx context.Context, keys []UserPlatformQuotaKey) ([]*UserPlatformQuotaCacheEntry, error)
}

// ModelPricing 模型价格配置（per-token价格，与LiteLLM格式一致）
type ModelPricing struct {
	InputPricePerToken                 float64  // 每token输入价格 (USD)
	InputPricePerTokenPriority         float64  // priority service tier 下每token输入价格 (USD)
	ImageInputPricePerToken            float64  // 图片输入 token 价格 (USD)，为 0 时回退到普通输入价格
	OutputPricePerToken                float64  // 每token输出价格 (USD)
	OutputPricePerTokenPriority        float64  // priority service tier 下每token输出价格 (USD)
	CacheCreationPricePerToken         float64  // 缓存创建每token价格 (USD)
	CacheCreationPricePerTokenPriority float64  // priority service tier 下缓存创建每token价格 (USD)
	CacheCreationPriceExplicit         bool     // 是否由渠道/区间定价显式设定（为 true 时即使 == 0 也不回退）
	cacheCreationPriorityDerived       bool     // priority 缓存写价是否由 Fast 兜底策略推导
	CacheReadPricePerToken             float64  // 缓存读取每token价格 (USD)
	CacheReadPricePerTokenPriority     float64  // priority service tier 下缓存读取每token价格 (USD)
	CacheCreation5mPrice               float64  // 5分钟缓存创建每token价格 (USD)
	CacheCreation1hPrice               float64  // 1小时缓存创建每token价格 (USD)
	SupportsCacheBreakdown             bool     // 是否支持详细的缓存分类
	SupportsServiceTier                bool     // 是否支持 service_tier（Fast/Flex）
	FastModeMultiplier                 *float64 // 渠道配置的 Fast 模式收费倍率；nil 表示沿用模型默认 Fast 定价
	FastMultiplier                     *float64 // 新版渠道 Fast/priority 倍率
	FlexMultiplier                     *float64 // 渠道配置的 Flex 倍率
	// MaxReasoningEffortMultiplier 仅在最终推理档位为 max 时应用。
	MaxReasoningEffortMultiplier  *float64
	LongContextInputThreshold     int     // 超过阈值后按整次会话提升输入价格
	LongContextThresholdInclusive bool    // 达到阈值即应用（xAI）；默认严格大于以兼容既有模型
	LongContextInputMultiplier    float64 // 长上下文整次会话输入倍率
	LongContextOutputMultiplier   float64 // 长上下文整次会话输出倍率
	ImageOutputPricePerToken      float64 // 图片输出 token 价格 (USD)
	ImageOutputPriceExplicit      bool    // 是否由渠道定价显式设定，显式设定后不再回退
}

func normalizeBillingServiceTier(serviceTier string) string {
	return strings.ToLower(strings.TrimSpace(serviceTier))
}

func usePriorityServiceTierPricing(serviceTier string, pricing *ModelPricing) bool {
	if pricing == nil {
		return false
	}
	tier := normalizeBillingServiceTier(serviceTier)
	if tier != "priority" && tier != "fast" {
		return false
	}
	if pricing.FastModeMultiplier != nil || pricing.FastMultiplier != nil {
		return false
	}
	return pricing.InputPricePerTokenPriority > 0 || pricing.OutputPricePerTokenPriority > 0 ||
		pricing.CacheCreationPricePerTokenPriority > 0 || pricing.CacheReadPricePerTokenPriority > 0
}

func serviceTierCostMultiplier(serviceTier string) float64 {
	switch normalizeBillingServiceTier(serviceTier) {
	case "priority", "fast", OpenAIFastTierUltrafast:
		return 2.0
	case "flex":
		return 0.5
	default:
		return 1.0
	}
}

// normalizedFastModeMultiplier 返回渠道 Fast 倍率；负值按 0 防御处理。
func normalizedFastModeMultiplier(pricing *ModelPricing) (float64, bool) {
	if pricing == nil {
		return 1, false
	}
	configured := pricing.FastModeMultiplier
	if configured == nil {
		configured = pricing.FastMultiplier
	}
	if configured == nil {
		return 1, false
	}
	if *configured < 0 {
		return 0, true
	}
	return *configured, true
}

// configuredServiceTierMultiplier 返回渠道显式层级倍率；未配置时沿用官方默认倍率。
func configuredServiceTierMultiplier(serviceTier string, pricing *ModelPricing) float64 {
	if pricing != nil {
		switch normalizeBillingServiceTier(serviceTier) {
		case "priority", "fast":
			if multiplier, configured := normalizedFastModeMultiplier(pricing); configured {
				return multiplier
			}
		case "flex":
			if pricing.FlexMultiplier != nil {
				return *pricing.FlexMultiplier
			}
		}
	}
	return serviceTierCostMultiplier(serviceTier)
}

// applyChannelFastModeMultiplier 将渠道 Fast 倍率写入最终定价元数据。
func applyChannelFastModeMultiplier(pricing *ModelPricing, channelPricing *ChannelModelPricing) {
	if pricing == nil || channelPricing == nil {
		return
	}
	multiplierPtr := channelPricing.FastMultiplier
	if multiplierPtr == nil {
		multiplierPtr = channelPricing.FastModeMultiplier
	}
	if multiplierPtr == nil {
		return
	}
	multiplier := *multiplierPtr
	if multiplier < 0 {
		multiplier = 0
	}
	pricing.FastModeMultiplier = &multiplier
	pricing.FastMultiplier = &multiplier
}

func applyChannelFlexMultiplier(pricing *ModelPricing, channelPricing *ChannelModelPricing) {
	if pricing == nil || channelPricing == nil || channelPricing.FlexMultiplier == nil {
		return
	}
	multiplier := *channelPricing.FlexMultiplier
	if multiplier < 0 {
		multiplier = 0
	}
	pricing.FlexMultiplier = &multiplier
}

// UsageTokens 使用的token数量
type UsageTokens struct {
	InputTokens           int
	ImageInputTokens      int
	OutputTokens          int
	CacheCreationTokens   int
	CacheReadTokens       int
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	ImageOutputTokens     int
}

// CostBreakdown 费用明细
type CostBreakdown struct {
	InputCost                 float64 // 文本输入费用（不含图片输入，图片输入单独记入 ImageInputCost）
	ImageInputCost            float64 // 图片输入 token 费用（如 gpt-image-2 图片编辑）
	OutputCost                float64
	ImageOutputCost           float64
	CacheCreationCost         float64
	CacheReadCost             float64
	TotalCost                 float64
	ActualCost                float64 // 应用倍率后的实际费用
	BillingMode               string  // 计费模式（"token"/"per_request"/"image"），由 CalculateCostUnified 填充
	LongContextBillingApplied bool    // 长上下文规则是否实际增加费用
}

// applyCostBreakdownMultiplier 将渠道分时倍率应用到所有 token 费用桶。
func applyCostBreakdownMultiplier(cost *CostBreakdown, multiplier float64) {
	if cost == nil || multiplier == 1 {
		return
	}
	cost.InputCost *= multiplier
	cost.ImageInputCost *= multiplier
	cost.OutputCost *= multiplier
	cost.ImageOutputCost *= multiplier
	cost.CacheCreationCost *= multiplier
	cost.CacheReadCost *= multiplier
	cost.TotalCost *= multiplier
	cost.ActualCost *= multiplier
}

const claudeFable51MaxReasoningEffortMultiplier = 3.0

// isClaudeFable51Model 判断模型是否属于 Fable 5.1，允许常见的分隔符写法。
func isClaudeFable51Model(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, marker := range []string{"fable-5-1", "fable-5.1", "fable5.1", "fable51"} {
		if at := strings.Index(model, marker); at >= 0 {
			after := at + len(marker)
			if after == len(model) || model[after] < '0' || model[after] > '9' {
				return true
			}
		}
	}
	return false
}

func defaultMaxReasoningEffortMultiplier(model string) *float64 {
	if !isClaudeFable51Model(model) {
		return nil
	}
	multiplier := claudeFable51MaxReasoningEffortMultiplier
	return &multiplier
}

// maxReasoningEffortBillingMultiplier 返回 max 档位的模型/渠道倍率。
func maxReasoningEffortBillingMultiplier(model, effort string, pricing *ModelPricing) float64 {
	if NormalizeClaudeOutputEffort(effort) == nil || !strings.EqualFold(strings.TrimSpace(effort), "max") {
		return 1
	}
	if pricing != nil && pricing.MaxReasoningEffortMultiplier != nil && *pricing.MaxReasoningEffortMultiplier > 0 {
		return *pricing.MaxReasoningEffortMultiplier
	}
	if multiplier := defaultMaxReasoningEffortMultiplier(model); multiplier != nil {
		return *multiplier
	}
	return 1
}

func resolvedChannelTimeMultiplier(resolved *ResolvedPricing, at time.Time) float64 {
	if resolved == nil || resolved.Mode != BillingModeToken || resolved.channelPricing == nil {
		return 1
	}
	return resolved.channelPricing.TimePricing.MultiplierAt(at)
}

// ErrModelPricingUnavailable 表示当前所有定价来源都无法为请求模型提供价格。
var ErrModelPricingUnavailable = errors.New("pricing not found")

// DeepSeek 官方价卡以美元/token 表示；峰值时段为工作日 UTC 01:00–04:00
// 与 06:00–10:00，峰值价格是低谷价格的 2 倍。
const (
	deepseekFlashOffPeakInputPrice  = 2.2e-7
	deepseekFlashOffPeakOutputPrice = 6.6e-7
	deepseekFlashOffPeakCacheRead   = 7e-9
	deepseekProOffPeakInputPrice    = 6.6e-7
	deepseekProOffPeakOutputPrice   = 1.98e-6
	deepseekProOffPeakCacheRead     = 2.2e-8
)

// isDeepSeekModel 判断模型名是否属于 DeepSeek 系列，未知后缀也按 Flash 价卡处理。
func isDeepSeekModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-")
}

// deepseekPeakMultiplierAt 返回 DeepSeek 官方峰谷倍率。周末按北京时间判断，
// 其余日期按 UTC 窗口判断，避免服务器时区影响计费结果。
func deepseekPeakMultiplierAt(now time.Time) float64 {
	beijing := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	if beijing.Weekday() == time.Saturday || beijing.Weekday() == time.Sunday {
		return 1
	}
	hour := now.UTC().Hour()
	if (hour >= 1 && hour < 4) || (hour >= 6 && hour < 10) {
		return 2
	}
	return 1
}

// applyDeepSeekOfficialPricing 用官方低谷价覆盖远端或旧的 DeepSeek 价卡，
// 保留其它能力字段，确保渠道/分组显式价格不会经过此函数。
func applyDeepSeekOfficialPricing(model string, pricing *ModelPricing) *ModelPricing {
	if pricing == nil || !isDeepSeekModel(model) {
		return pricing
	}
	cloned := *pricing
	if strings.Contains(strings.ToLower(strings.TrimSpace(model)), "deepseek-v4-pro") {
		cloned.InputPricePerToken = deepseekProOffPeakInputPrice
		cloned.OutputPricePerToken = deepseekProOffPeakOutputPrice
		cloned.CacheReadPricePerToken = deepseekProOffPeakCacheRead
	} else {
		cloned.InputPricePerToken = deepseekFlashOffPeakInputPrice
		cloned.OutputPricePerToken = deepseekFlashOffPeakOutputPrice
		cloned.CacheReadPricePerToken = deepseekFlashOffPeakCacheRead
	}
	return &cloned
}

// applyDeepSeekPeakPricing 在默认模型价卡上叠加官方峰值倍率；自定义价格不应调用。
func applyDeepSeekPeakPricing(model string, pricing *ModelPricing, pricingAt time.Time) *ModelPricing {
	if pricing == nil || !isDeepSeekModel(model) {
		return pricing
	}
	if pricingAt.IsZero() {
		pricingAt = timezone.Now()
	}
	multiplier := deepseekPeakMultiplierAt(pricingAt)
	if multiplier <= 1 {
		return pricing
	}
	cloned := *pricing
	cloned.InputPricePerToken *= multiplier
	cloned.OutputPricePerToken *= multiplier
	cloned.CacheReadPricePerToken *= multiplier
	return &cloned
}

// BillingService 计费服务
type BillingService struct {
	cfg            *config.Config
	pricingService *PricingService
	fallbackPrices map[string]*ModelPricing // 硬编码回退价格

	// fallbackWarnSeen 记录已输出 fallback pricing 警告的模型名，避免热路径每次请求重复刷日志。
	fallbackWarnSeen sync.Map
}

// NewBillingService 创建计费服务实例
func NewBillingService(cfg *config.Config, pricingService *PricingService) *BillingService {
	s := &BillingService{
		cfg:            cfg,
		pricingService: pricingService,
		fallbackPrices: make(map[string]*ModelPricing),
	}

	// 初始化硬编码回退价格（当动态价格不可用时使用）
	s.initFallbackPricing()

	return s
}

// initFallbackPricing 初始化硬编码回退价格（当动态价格不可用时使用）
// 价格单位：USD per token（与LiteLLM格式一致）
func (s *BillingService) initFallbackPricing() {
	// Claude 4.5 Opus
	s.fallbackPrices["claude-opus-4.5"] = &ModelPricing{
		InputPricePerToken:         5e-6,    // $5 per MTok
		OutputPricePerToken:        25e-6,   // $25 per MTok
		CacheCreationPricePerToken: 6.25e-6, // $6.25 per MTok
		CacheReadPricePerToken:     0.5e-6,  // $0.50 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 4 Sonnet
	s.fallbackPrices["claude-sonnet-4"] = &ModelPricing{
		InputPricePerToken:         3e-6,    // $3 per MTok
		OutputPricePerToken:        15e-6,   // $15 per MTok
		CacheCreationPricePerToken: 3.75e-6, // $3.75 per MTok
		CacheReadPricePerToken:     0.3e-6,  // $0.30 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3.5 Sonnet
	s.fallbackPrices["claude-3-5-sonnet"] = &ModelPricing{
		InputPricePerToken:         3e-6,    // $3 per MTok
		OutputPricePerToken:        15e-6,   // $15 per MTok
		CacheCreationPricePerToken: 3.75e-6, // $3.75 per MTok
		CacheReadPricePerToken:     0.3e-6,  // $0.30 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3.5 Haiku
	s.fallbackPrices["claude-3-5-haiku"] = &ModelPricing{
		InputPricePerToken:         1e-6,    // $1 per MTok
		OutputPricePerToken:        5e-6,    // $5 per MTok
		CacheCreationPricePerToken: 1.25e-6, // $1.25 per MTok
		CacheReadPricePerToken:     0.1e-6,  // $0.10 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3 Opus
	s.fallbackPrices["claude-3-opus"] = &ModelPricing{
		InputPricePerToken:         15e-6,    // $15 per MTok
		OutputPricePerToken:        75e-6,    // $75 per MTok
		CacheCreationPricePerToken: 18.75e-6, // $18.75 per MTok
		CacheReadPricePerToken:     1.5e-6,   // $1.50 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3 Haiku
	s.fallbackPrices["claude-3-haiku"] = &ModelPricing{
		InputPricePerToken:         0.25e-6, // $0.25 per MTok
		OutputPricePerToken:        1.25e-6, // $1.25 per MTok
		CacheCreationPricePerToken: 0.3e-6,  // $0.30 per MTok
		CacheReadPricePerToken:     0.03e-6, // $0.03 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 4.6 Opus (与4.5同价)
	s.fallbackPrices["claude-opus-4.6"] = s.fallbackPrices["claude-opus-4.5"]

	// Claude 4.7 Opus (暂与4.6同价，待官方定价更新)
	s.fallbackPrices["claude-opus-4.7"] = s.fallbackPrices["claude-opus-4.6"]

	// Claude 4.8 Opus（官方常规定价 $5/$25 per MTok，Fast mode 为 2 倍）
	s.fallbackPrices["claude-opus-4.8"] = &ModelPricing{
		InputPricePerToken:         5e-6,    // 每百万 token $5
		OutputPricePerToken:        25e-6,   // 每百万 token $25
		CacheCreationPricePerToken: 6.25e-6, // 默认按 5 分钟缓存写入价
		CacheReadPricePerToken:     0.5e-6,  // 每百万 token $0.50
		CacheCreation5mPrice:       6.25e-6,
		CacheCreation1hPrice:       10e-6,
		SupportsCacheBreakdown:     true,
		SupportsServiceTier:        true,
	}
	s.fallbackPrices["claude-opus-5"] = s.fallbackPrices["claude-opus-4.8"]

	// Claude Fable 5.x 的输入/输出和缓存写入价格相同；5.1 的缓存读取价降为每百万 token 0.25 美元。
	s.fallbackPrices["claude-fable-5"] = &ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheCreation5mPrice:       12.5e-6,
		CacheCreation1hPrice:       20e-6,
		CacheReadPricePerToken:     1e-6,
		SupportsCacheBreakdown:     true,
	}
	s.fallbackPrices["claude-fable-5-1"] = &ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheCreation5mPrice:       12.5e-6,
		CacheCreation1hPrice:       20e-6,
		CacheReadPricePerToken:     0.25e-6,
		SupportsCacheBreakdown:     true,
	}

	// Gemini 3.1 Pro
	s.fallbackPrices["gemini-3.1-pro"] = &ModelPricing{
		InputPricePerToken:         2e-6,   // $2 per MTok
		OutputPricePerToken:        12e-6,  // $12 per MTok
		CacheCreationPricePerToken: 2e-6,   // $2 per MTok
		CacheReadPricePerToken:     0.2e-6, // $0.20 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Gemini 3.5 Flash（Google AI 定价：输入 $1.50、输出 $9、缓存输入 $0.15/百万 token）
	s.fallbackPrices["gemini-3.5-flash"] = &ModelPricing{
		InputPricePerToken:     1.5e-6,
		OutputPricePerToken:    9e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}

	// Gemini 3.6 Flash（Google AI 定价：输入 $1.50、输出 $7.50、缓存输入
	// $0.15/百万 token）。下方会匹配 Antigravity 的 -high/-low/-medium/-tiered
	// 别名，避免远端定价不可用时把有 token 的请求记为 $0。
	s.fallbackPrices["gemini-3.6-flash"] = &ModelPricing{
		InputPricePerToken:     1.5e-6,
		OutputPricePerToken:    7.5e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}

	// OpenAI GPT-5.4（业务指定价格）
	s.fallbackPrices["gpt-5.4"] = &ModelPricing{
		InputPricePerToken:             2.5e-6,  // $2.5 per MTok
		InputPricePerTokenPriority:     5e-6,    // $5 per MTok
		OutputPricePerToken:            15e-6,   // $15 per MTok
		OutputPricePerTokenPriority:    30e-6,   // $30 per MTok
		CacheCreationPricePerToken:     2.5e-6,  // $2.5 per MTok
		CacheReadPricePerToken:         0.25e-6, // $0.25 per MTok
		CacheReadPricePerTokenPriority: 0.5e-6,  // $0.5 per MTok
		SupportsCacheBreakdown:         false,
	}
	// OpenAI GPT-5.5（按官方发布价格兜底）
	s.fallbackPrices["gpt-5.5"] = &ModelPricing{
		InputPricePerToken:             5e-6,    // $5 per MTok
		InputPricePerTokenPriority:     12.5e-6, // $12.5 per MTok
		OutputPricePerToken:            30e-6,   // $30 per MTok
		OutputPricePerTokenPriority:    75e-6,   // $75 per MTok
		CacheCreationPricePerToken:     5e-6,    // $5 per MTok
		CacheReadPricePerToken:         0.5e-6,  // $0.5 per MTok
		CacheReadPricePerTokenPriority: 1.25e-6, // $1.25 per MTok
		SupportsCacheBreakdown:         false,
	}
	// OpenAI GPT-5.5 Pro（按官方发布价格兜底）
	s.fallbackPrices["gpt-5.5-pro"] = &ModelPricing{
		InputPricePerToken:             30e-6,  // $30 per MTok
		InputPricePerTokenPriority:     75e-6,  // $75 per MTok
		OutputPricePerToken:            180e-6, // $180 per MTok
		OutputPricePerTokenPriority:    450e-6, // $450 per MTok
		CacheCreationPricePerToken:     30e-6,  // $30 per MTok
		CacheReadPricePerToken:         3e-6,   // $3 per MTok
		CacheReadPricePerTokenPriority: 7.5e-6, // $7.5 per MTok
		SupportsCacheBreakdown:         false,
	}

	// OpenAI GPT-6 Astra 官方标准价格（USD/token）。
	s.fallbackPrices["gpt-6-astra"] = &ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheReadPricePerToken:     1e-6,
		SupportsServiceTier:        true,
	}

	// OpenAI GPT-5.6 官方价格（USD/token）。缓存写入为输入价的 1.25 倍。
	s.fallbackPrices["gpt-5.6-sol"] = &ModelPricing{
		InputPricePerToken:                 5e-6,
		InputPricePerTokenPriority:         10e-6,
		OutputPricePerToken:                30e-6,
		OutputPricePerTokenPriority:        60e-6,
		CacheCreationPricePerToken:         6.25e-6,
		CacheCreationPricePerTokenPriority: 12.5e-6,
		CacheReadPricePerToken:             0.5e-6,
		CacheReadPricePerTokenPriority:     1e-6,
		SupportsServiceTier:                true,
	}
	s.fallbackPrices["gpt-5.6-terra"] = &ModelPricing{
		InputPricePerToken:                 2e-6,
		InputPricePerTokenPriority:         4e-6,
		OutputPricePerToken:                12e-6,
		OutputPricePerTokenPriority:        24e-6,
		CacheCreationPricePerToken:         2.5e-6,
		CacheCreationPricePerTokenPriority: 5e-6,
		CacheReadPricePerToken:             0.2e-6,
		CacheReadPricePerTokenPriority:     0.4e-6,
		SupportsServiceTier:                true,
	}
	s.fallbackPrices["gpt-5.6-luna"] = &ModelPricing{
		InputPricePerToken:                 0.2e-6,
		InputPricePerTokenPriority:         0.4e-6,
		OutputPricePerToken:                1.2e-6,
		OutputPricePerTokenPriority:        2.4e-6,
		CacheCreationPricePerToken:         0.25e-6,
		CacheCreationPricePerTokenPriority: 0.5e-6,
		CacheReadPricePerToken:             0.02e-6,
		CacheReadPricePerTokenPriority:     0.04e-6,
		SupportsServiceTier:                true,
	}

	s.fallbackPrices["gpt-5.4-mini"] = &ModelPricing{
		InputPricePerToken:     7.5e-7,
		OutputPricePerToken:    4.5e-6,
		CacheReadPricePerToken: 7.5e-8,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["gpt-5.4-nano"] = &ModelPricing{
		InputPricePerToken:     2e-7,
		OutputPricePerToken:    1.25e-6,
		CacheReadPricePerToken: 2e-8,
		SupportsCacheBreakdown: false,
	}
	// OpenAI GPT-5.2（本地兜底）
	s.fallbackPrices["gpt-5.2"] = &ModelPricing{
		InputPricePerToken:             1.75e-6,
		InputPricePerTokenPriority:     3.5e-6,
		OutputPricePerToken:            14e-6,
		OutputPricePerTokenPriority:    28e-6,
		CacheCreationPricePerToken:     1.75e-6,
		CacheReadPricePerToken:         0.175e-6,
		CacheReadPricePerTokenPriority: 0.35e-6,
		SupportsCacheBreakdown:         false,
	}
	// Codex 族兜底统一按 GPT-5.3 Codex 价格计费
	s.fallbackPrices["gpt-5.3-codex"] = &ModelPricing{
		InputPricePerToken:             1.5e-6, // $1.5 per MTok
		InputPricePerTokenPriority:     3e-6,   // $3 per MTok
		OutputPricePerToken:            12e-6,  // $12 per MTok
		OutputPricePerTokenPriority:    24e-6,  // $24 per MTok
		CacheCreationPricePerToken:     1.5e-6, // $1.5 per MTok
		CacheReadPricePerToken:         0.15e-6,
		CacheReadPricePerTokenPriority: 0.3e-6,
		SupportsCacheBreakdown:         false,
	}

	// ============================================================
	// 国产大模型兜底定价（数据源：各家官方定价页，美元口径）
	// 顺序：DeepSeek → 智谱 GLM → 月之暗面 Kimi → MiniMax → 豆包 Embedding
	// 覆盖逻辑见同文件 getFallbackPricing()
	// ============================================================

	// ---- DeepSeek 系列 ----
	// 资料来源：https://api-docs.deepseek.com/quick_start/pricing
	// 下面存储官方低谷价；高峰倍率由 applyDeepSeekPeakPricing 按请求时刻计算。
	s.fallbackPrices["deepseek-v4-pro"] = &ModelPricing{
		InputPricePerToken:     deepseekProOffPeakInputPrice,
		OutputPricePerToken:    deepseekProOffPeakOutputPrice,
		CacheReadPricePerToken: deepseekProOffPeakCacheRead,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["deepseek-v4-flash"] = &ModelPricing{
		InputPricePerToken:     deepseekFlashOffPeakInputPrice,
		OutputPricePerToken:    deepseekFlashOffPeakOutputPrice,
		CacheReadPricePerToken: deepseekFlashOffPeakCacheRead,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["deepseek-v4-flash-vision-exp"] = &ModelPricing{
		InputPricePerToken:     deepseekFlashOffPeakInputPrice,
		OutputPricePerToken:    deepseekFlashOffPeakOutputPrice,
		CacheReadPricePerToken: deepseekFlashOffPeakCacheRead,
		SupportsCacheBreakdown: false,
	}

	// ---- 智谱 GLM（Z.AI）----
	// 资料来源：https://docs.z.ai/guides/overview/pricing（美元/百万 token）
	// CacheReadPricePerToken 对应缓存命中价；未公开缓存写入价时按 0 处理。
	// GLM-5.2 与 GLM-5.1 的公开价格一致。
	s.fallbackPrices["glm-5.2"] = &ModelPricing{
		InputPricePerToken:     1.4e-6,
		OutputPricePerToken:    4.4e-6,
		CacheReadPricePerToken: 0.26e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-5.1"] = &ModelPricing{
		InputPricePerToken:     1.4e-6,
		OutputPricePerToken:    4.4e-6,
		CacheReadPricePerToken: 0.26e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-5"] = &ModelPricing{
		InputPricePerToken:     1e-6,
		OutputPricePerToken:    3.2e-6,
		CacheReadPricePerToken: 0.2e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-5-turbo"] = &ModelPricing{
		InputPricePerToken:     1.2e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.24e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.7"] = &ModelPricing{
		InputPricePerToken:     0.6e-6,
		OutputPricePerToken:    2.2e-6,
		CacheReadPricePerToken: 0.11e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.7-flashx"] = &ModelPricing{
		InputPricePerToken:     0.07e-6,
		OutputPricePerToken:    0.4e-6,
		CacheReadPricePerToken: 0.01e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.6"] = &ModelPricing{
		InputPricePerToken:     0.6e-6,
		OutputPricePerToken:    2.2e-6,
		CacheReadPricePerToken: 0.11e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.5"] = &ModelPricing{
		InputPricePerToken:     0.6e-6,
		OutputPricePerToken:    2.2e-6,
		CacheReadPricePerToken: 0.11e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.5-x"] = &ModelPricing{
		InputPricePerToken:     2.2e-6,
		OutputPricePerToken:    8.9e-6,
		CacheReadPricePerToken: 0.45e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.5-air"] = &ModelPricing{
		InputPricePerToken:     0.2e-6,
		OutputPricePerToken:    1.1e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.5-airx"] = &ModelPricing{
		InputPricePerToken:     1.1e-6,
		OutputPricePerToken:    4.5e-6,
		CacheReadPricePerToken: 0.22e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4-32b-0414-128k"] = &ModelPricing{
		InputPricePerToken:     0.1e-6,
		OutputPricePerToken:    0.1e-6,
		SupportsCacheBreakdown: false,
	}
	// GLM Flash 在 z.ai 上免费，保留零价条目防止未知别名误计费。
	s.fallbackPrices["glm-4.5-flash"] = &ModelPricing{
		InputPricePerToken:     0,
		OutputPricePerToken:    0,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["glm-4.7-flash"] = &ModelPricing{
		InputPricePerToken:     0,
		OutputPricePerToken:    0,
		SupportsCacheBreakdown: false,
	}

	// ---- 月之暗面 Kimi（K 系列）----
	// 资料来源：https://platform.moonshot.cn/docs/pricing/overview
	// Moonshot V1 与旧 K2 型号未保留清晰美元价，不做宽泛回退。
	s.fallbackPrices["kimi-k3"] = &ModelPricing{
		InputPricePerToken:     3e-6,
		OutputPricePerToken:    15e-6,
		CacheReadPricePerToken: 0.30e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["kimi-k2.6"] = &ModelPricing{
		InputPricePerToken:     0.95e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}
	// kimi-for-coding 走 Kimi Coding 接口，按当前 K2.6 coding 档位兜底计费。
	s.fallbackPrices["kimi-for-coding"] = &ModelPricing{
		InputPricePerToken:     0.95e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["kimi-k2.5"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    3e-6,
		CacheReadPricePerToken: 0.098e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["kimi-k2-thinking"] = &ModelPricing{
		InputPricePerToken:     0.56e-6,
		OutputPricePerToken:    2.24e-6,
		CacheReadPricePerToken: 0.14e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["kimi-k2"] = &ModelPricing{
		InputPricePerToken:     0.56e-6,
		OutputPricePerToken:    2.24e-6,
		CacheReadPricePerToken: 0.14e-6,
		SupportsCacheBreakdown: false,
	}

	// ---- MiniMax M 系列 ----
	// 资料来源：https://platform.minimax.io/docs/guides/pricing-paygo
	// M3 长上下文高价档不在兜底中拆分，沿用标准档以避免高估。
	s.fallbackPrices["minimax-m3"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    2.40e-6,
		CacheReadPricePerToken: 0.12e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["minimax-m2.7"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.06e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["minimax-m2.7-highspeed"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    2.40e-6,
		CacheReadPricePerToken: 0.06e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["minimax-m2.5"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["minimax-m2.1"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["minimax-m2"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}

	// ---- 火山方舟 豆包 Embedding（多模态向量化）----
	// doubao-embedding-vision 回传图文 token 拆分，文本与图片输入按不同价计费。
	s.fallbackPrices["doubao-embedding-vision"] = &ModelPricing{
		InputPricePerToken:      0.098e-6, // ¥0.7/MTok ≈ $0.098
		ImageInputPricePerToken: 0.252e-6, // ¥1.8/MTok ≈ $0.252
		OutputPricePerToken:     0,
		SupportsCacheBreakdown:  false,
	}

	// xAI Grok 4.5：20 万 token 以下每百万输入 $2、缓存输入 $0.30、输出 $6。
	s.fallbackPrices["grok-4.5"] = &ModelPricing{
		InputPricePerToken:            2e-6,
		OutputPricePerToken:           6e-6,
		CacheReadPricePerToken:        0.3e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}

	// xAI Grok 4.6：20 万 token 以下每百万输入 $2、缓存输入 $0.50、输出 $6；
	// 达到 20 万后输入、缓存输入和输出均按 2 倍结算。
	s.fallbackPrices["grok-4.6"] = &ModelPricing{
		InputPricePerToken:            2e-6,
		OutputPricePerToken:           6e-6,
		CacheReadPricePerToken:        0.5e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}

	// xAI Grok 4.3：20 万 token 以下每百万输入 $1.25、缓存输入 $0.20、输出 $2.50。
	s.fallbackPrices["grok-4.3"] = &ModelPricing{
		InputPricePerToken:            1.25e-6,
		OutputPricePerToken:           2.5e-6,
		CacheReadPricePerToken:        0.2e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}
	// Grok 4.20 variants share the official $1.25 / $0.20 / $2.50 card
	// (and $2.50 / $0.40 / $5 long-context rates) with Grok 4.3.
	s.fallbackPrices["grok-4.20"] = &ModelPricing{
		InputPricePerToken:            1.25e-6,
		OutputPricePerToken:           2.5e-6,
		CacheReadPricePerToken:        0.2e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}

	// Grok 3 Mini 保留独立历史价格，避免按 Grok 4.5 通用回退价计费。
	s.fallbackPrices["grok-3-mini"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    0.50e-6,
		CacheReadPricePerToken: 0.075e-6,
		SupportsCacheBreakdown: false,
	}
	s.fallbackPrices["grok-3-mini-fast"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}
	// xAI Grok Build 0.1 官方价格为输入 $1、缓存输入 $0.20、输出 $2/百万 token。
	// Composer 仅通过 Grok Build 提供且没有独立公开价格，因此其别名沿用该编程模型价格，
	// 避免被静默按零费用结算。
	s.fallbackPrices["grok-build-0.1"] = &ModelPricing{
		InputPricePerToken:            1e-6,
		OutputPricePerToken:           2e-6,
		CacheReadPricePerToken:        0.2e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}
}

// getFallbackPricing 根据模型系列获取回退价格
func (s *BillingService) getFallbackPricing(model string) *ModelPricing {
	modelLower := strings.ToLower(model)

	// 按模型系列匹配
	// Fable 5.1 的别名必须先于 Fable 5，避免降级到旧缓存读取价。
	if strings.Contains(modelLower, "fable-5-1") || strings.Contains(modelLower, "fable-5.1") ||
		strings.Contains(modelLower, "fable5.1") || strings.Contains(modelLower, "fable51") {
		return s.fallbackPrices["claude-fable-5-1"]
	}
	if strings.Contains(modelLower, "fable-5") || strings.Contains(modelLower, "fable5") {
		return s.fallbackPrices["claude-fable-5"]
	}
	if strings.Contains(modelLower, "opus") {
		if strings.Contains(modelLower, "opus-5") || strings.Contains(modelLower, "opus5") {
			return s.fallbackPrices["claude-opus-5"]
		}
		if strings.Contains(modelLower, "4.8") || strings.Contains(modelLower, "4-8") {
			return s.fallbackPrices["claude-opus-4.8"]
		}
		if strings.Contains(modelLower, "4.7") || strings.Contains(modelLower, "4-7") {
			return s.fallbackPrices["claude-opus-4.7"]
		}
		if strings.Contains(modelLower, "4.6") || strings.Contains(modelLower, "4-6") {
			return s.fallbackPrices["claude-opus-4.6"]
		}
		if strings.Contains(modelLower, "4.5") || strings.Contains(modelLower, "4-5") {
			return s.fallbackPrices["claude-opus-4.5"]
		}
		return s.fallbackPrices["claude-3-opus"]
	}
	if strings.Contains(modelLower, "sonnet") {
		if strings.Contains(modelLower, "4") && !strings.Contains(modelLower, "3") {
			return s.fallbackPrices["claude-sonnet-4"]
		}
		return s.fallbackPrices["claude-3-5-sonnet"]
	}
	if strings.Contains(modelLower, "haiku") {
		if strings.Contains(modelLower, "3-5") || strings.Contains(modelLower, "3.5") {
			return s.fallbackPrices["claude-3-5-haiku"]
		}
		return s.fallbackPrices["claude-3-haiku"]
	}
	// Claude 未知型号统一回退到 Sonnet，避免计费中断。
	if strings.Contains(modelLower, "claude") {
		return s.fallbackPrices["claude-sonnet-4"]
	}
	if strings.Contains(modelLower, "gemini-3.1-pro") || strings.Contains(modelLower, "gemini-3-1-pro") {
		return s.fallbackPrices["gemini-3.1-pro"]
	}
	if strings.Contains(modelLower, "gemini-3.5-flash") || strings.Contains(modelLower, "gemini-3-5-flash") {
		return s.fallbackPrices["gemini-3.5-flash"]
	}
	if strings.Contains(modelLower, "gemini-3.6-flash") || strings.Contains(modelLower, "gemini-3-6-flash") {
		return s.fallbackPrices["gemini-3.6-flash"]
	}

	// DeepSeek 官方模型按专属价卡，版本化名称和其它 deepseek-* 按 Flash 价卡兜底。
	if strings.Contains(modelLower, "deepseek-v4-flash-vision-exp") {
		return s.fallbackPrices["deepseek-v4-flash-vision-exp"]
	}
	if strings.Contains(modelLower, "deepseek-v4-flash") {
		return s.fallbackPrices["deepseek-v4-flash"]
	}
	if strings.Contains(modelLower, "deepseek-v4-pro") {
		return s.fallbackPrices["deepseek-v4-pro"]
	}
	if strings.HasPrefix(modelLower, "deepseek-") {
		return s.fallbackPrices["deepseek-v4-flash"]
	}
	// 带小数点的具体型号必须先于裸 glm-5 匹配，避免被子串规则抢走。
	if strings.Contains(modelLower, "glm-5.2") {
		return s.fallbackPrices["glm-5.2"]
	}
	if strings.Contains(modelLower, "glm-5.1") {
		return s.fallbackPrices["glm-5.1"]
	}
	if strings.Contains(modelLower, "glm-5-turbo") || strings.Contains(modelLower, "glm-5turbo") {
		return s.fallbackPrices["glm-5-turbo"]
	}
	if strings.Contains(modelLower, "glm-5") {
		return s.fallbackPrices["glm-5"]
	}
	if strings.Contains(modelLower, "glm-4.7-flashx") {
		return s.fallbackPrices["glm-4.7-flashx"]
	}
	if strings.Contains(modelLower, "glm-4.7-flash") {
		return s.fallbackPrices["glm-4.7-flash"]
	}
	if strings.Contains(modelLower, "glm-4.7") {
		return s.fallbackPrices["glm-4.7"]
	}
	if strings.Contains(modelLower, "glm-4.6") {
		return s.fallbackPrices["glm-4.6"]
	}
	if strings.Contains(modelLower, "glm-4.5-flash") {
		return s.fallbackPrices["glm-4.5-flash"]
	}
	if strings.Contains(modelLower, "glm-4.5-x") || strings.Contains(modelLower, "glm-4.5x") {
		return s.fallbackPrices["glm-4.5-x"]
	}
	if strings.Contains(modelLower, "glm-4.5-airx") || strings.Contains(modelLower, "glm-4.5airx") {
		return s.fallbackPrices["glm-4.5-airx"]
	}
	if strings.Contains(modelLower, "glm-4.5-air") || strings.Contains(modelLower, "glm-4.5air") {
		return s.fallbackPrices["glm-4.5-air"]
	}
	if strings.Contains(modelLower, "glm-4.5") {
		return s.fallbackPrices["glm-4.5"]
	}
	if strings.Contains(modelLower, "glm-4-32b") {
		return s.fallbackPrices["glm-4-32b-0414-128k"]
	}
	if strings.Contains(modelLower, "kimi-for-coding") {
		return s.fallbackPrices["kimi-for-coding"]
	}
	// Kimi Code 使用无厂商前缀的 bare ID；这里只做完整 ID 或路径尾段匹配，
	// 避免把客户端上下文语法 kimi-k3[1m] 和其他近似名称误计为 K3。
	if modelLower == "kimi-k3" || strings.HasSuffix(modelLower, "/kimi-k3") ||
		modelLower == "k3" || modelLower == "k3-256k" ||
		strings.HasSuffix(modelLower, "/k3") || strings.HasSuffix(modelLower, "/k3-256k") {
		return s.fallbackPrices["kimi-k3"]
	}
	if strings.Contains(modelLower, "kimi-k2.6") || strings.Contains(modelLower, "kimi-k2-6") {
		return s.fallbackPrices["kimi-k2.6"]
	}
	if strings.Contains(modelLower, "kimi-k2.5") || strings.Contains(modelLower, "kimi-k2-5") {
		return s.fallbackPrices["kimi-k2.5"]
	}
	if strings.Contains(modelLower, "kimi-k2-thinking") {
		return s.fallbackPrices["kimi-k2-thinking"]
	}
	if strings.Contains(modelLower, "kimi-k2") || strings.Contains(modelLower, "kimi/k2") {
		return s.fallbackPrices["kimi-k2"]
	}
	if strings.Contains(modelLower, "minimax-m3") {
		return s.fallbackPrices["minimax-m3"]
	}
	if strings.Contains(modelLower, "minimax-m2.7-highspeed") || strings.Contains(modelLower, "minimax-m2-7-highspeed") {
		return s.fallbackPrices["minimax-m2.7-highspeed"]
	}
	if strings.Contains(modelLower, "minimax-m2.7") || strings.Contains(modelLower, "minimax-m2-7") {
		return s.fallbackPrices["minimax-m2.7"]
	}
	if strings.Contains(modelLower, "minimax-m2.5") || strings.Contains(modelLower, "minimax-m2-5") {
		return s.fallbackPrices["minimax-m2.5"]
	}
	if strings.Contains(modelLower, "minimax-m2.1") || strings.Contains(modelLower, "minimax-m2-1") {
		return s.fallbackPrices["minimax-m2.1"]
	}
	if strings.Contains(modelLower, "minimax-m2") || strings.Contains(modelLower, "minimax-m-2") {
		return s.fallbackPrices["minimax-m2"]
	}
	if strings.Contains(modelLower, "doubao-embedding-vision") {
		return s.fallbackPrices["doubao-embedding-vision"]
	}

	// OpenAI 仅匹配已知 GPT/Codex 族，避免未知 OpenAI 型号误计价。
	if normalized := normalizeKnownOpenAICodexModel(modelLower); normalized != "" {
		switch normalized {
		case "gpt-6-astra":
			return s.fallbackPrices["gpt-6-astra"]
		case "gpt-5.6-sol":
			return s.fallbackPrices["gpt-5.6-sol"]
		case "gpt-5.6-terra":
			return s.fallbackPrices["gpt-5.6-terra"]
		case "gpt-5.6-luna":
			return s.fallbackPrices["gpt-5.6-luna"]
		case "gpt-5.5-pro":
			return s.fallbackPrices["gpt-5.5-pro"]
		case "gpt-5.5":
			return s.fallbackPrices["gpt-5.5"]
		case "gpt-5.4-mini":
			return s.fallbackPrices["gpt-5.4-mini"]
		case "gpt-5.4-nano":
			return s.fallbackPrices["gpt-5.4-nano"]
		case "gpt-5.4":
			return s.fallbackPrices["gpt-5.4"]
		case "gpt-5.2":
			return s.fallbackPrices["gpt-5.2"]
		case "gpt-5.3-codex", "gpt-5.3-codex-spark":
			return s.fallbackPrices["gpt-5.3-codex"]
		}
	}

	switch modelLower {
	case "grok", "grok-latest", "grok-4.6", "grok-4.6-latest":
		return s.fallbackPrices["grok-4.6"]
	case "grok-4.5", "grok-4.5-latest":
		return s.fallbackPrices["grok-4.5"]
	case "grok-3-mini":
		return s.fallbackPrices["grok-3-mini"]
	case "grok-3-mini-fast":
		return s.fallbackPrices["grok-3-mini-fast"]
	case "grok-4.3":
		return s.fallbackPrices["grok-4.3"]
	case "grok-4.20-0309-reasoning",
		"grok-4.20-0309-non-reasoning",
		"grok-4.20-multi-agent-0309",
		"grok-4.20-reasoning",
		"grok-4.20-non-reasoning":
		return s.fallbackPrices["grok-4.20"]
	case "grok-build", "grok-build-latest", "grok-build-0.1", "grok-composer", "grok-composer-2.5-fast", "composer-2.5":
		return s.fallbackPrices["grok-build-0.1"]
	}

	// 未知 Grok 文本模型（如 grok-5、日期快照或带供应商前缀的名称）沿用当前默认文本价，
	// 避免新模型上线后被静默按零费用结算。
	if pricing := s.grokUnknownTextFamilyFallback(modelLower); pricing != nil {
		return pricing
	}

	return nil
}

func (s *BillingService) grokUnknownTextFamilyFallback(model string) *ModelPricing {
	if s == nil || !isGrokUnknownTextFamilyModel(model) {
		return nil
	}
	return s.fallbackPrices["grok-4.6"]
}

func isGrokUnknownTextFamilyModel(model string) bool {
	native := strings.ToLower(strings.TrimSpace(xai.StripGrokProviderPrefix(model)))
	if isGrokMediaFamilyModel(native) {
		return false
	}
	switch {
	case native == "grok", native == "grok-latest":
		return true
	case strings.HasPrefix(native, "grok-build"),
		strings.HasPrefix(native, "grok-composer"),
		strings.HasPrefix(native, "composer-"):
		return true
	case len(native) > 5 && strings.HasPrefix(native, "grok-"):
		rest := native[len("grok-"):]
		return rest[0] >= '0' && rest[0] <= '9'
	default:
		return false
	}
}

// isGrokMediaFamilyModel 判断模型 ID 是否属于按图片、视频或音频单位计费的媒体族。
// 带版本号的媒体 ID 不能进入未知文本兜底；vision 多模态对话仍按 token 计费。
func isGrokMediaFamilyModel(native string) bool {
	for _, marker := range []string{"imagine", "image", "video", "audio", "speech", "tts", "transcribe", "realtime"} {
		if strings.Contains(native, marker) {
			return true
		}
	}
	return false
}

// GetModelPricing 获取模型价格配置
func (s *BillingService) GetModelPricing(model string) (*ModelPricing, error) {
	// 标准化模型名称（转小写）
	model = strings.ToLower(model)

	// 1. 优先从动态价格服务获取
	if s.pricingService != nil {
		litellmPricing := s.pricingService.GetModelPricing(model)
		// 仅有图片价、无 token 价的条目（如 LiteLLM 的 imagen 类模型）不能用于
		// token 计费：直接返回会把 token 流量按 $0 计费。跳过后走 fallback，
		// 无 fallback 则 fail-closed（ErrModelPricingUnavailable）。
		// 图片计费路径（getDefaultImagePrice / getImageUnitPrice）直接读
		// PricingService，不受影响。
		if litellmPricing != nil && litellmPricing.TokenPricingAbsent {
			litellmPricing = nil
		}
		if litellmPricing != nil {
			// 启用 5m/1h 分类计费的条件：
			// 1. 存在 1h 价格
			// 2. 1h 价格 > 5m 价格（防止 LiteLLM 数据错误导致少收费）
			price5m := litellmPricing.CacheCreationInputTokenCost
			price1h := litellmPricing.CacheCreationInputTokenCostAbove1hr
			enableBreakdown := price1h > 0 && price1h > price5m
			return s.applyModelSpecificPricingPolicy(model, &ModelPricing{
				InputPricePerToken:                 litellmPricing.InputCostPerToken,
				InputPricePerTokenPriority:         litellmPricing.InputCostPerTokenPriority,
				OutputPricePerToken:                litellmPricing.OutputCostPerToken,
				OutputPricePerTokenPriority:        litellmPricing.OutputCostPerTokenPriority,
				CacheCreationPricePerToken:         litellmPricing.CacheCreationInputTokenCost,
				CacheCreationPricePerTokenPriority: litellmPricing.CacheCreationInputTokenCostPriority,
				CacheReadPricePerToken:             litellmPricing.CacheReadInputTokenCost,
				CacheReadPricePerTokenPriority:     litellmPricing.CacheReadInputTokenCostPriority,
				CacheCreation5mPrice:               price5m,
				CacheCreation1hPrice:               price1h,
				SupportsCacheBreakdown:             enableBreakdown,
				SupportsServiceTier:                litellmPricing.SupportsServiceTier,
				// xAI 的目录语义是达到阈值即进入高档，其他提供商保持严格大于。
				LongContextThresholdInclusive: strings.EqualFold(litellmPricing.LiteLLMProvider, "xai"),
				LongContextInputThreshold:     litellmPricing.LongContextInputTokenThreshold,
				LongContextInputMultiplier:    litellmPricing.LongContextInputCostMultiplier,
				LongContextOutputMultiplier:   litellmPricing.LongContextOutputCostMultiplier,
				ImageInputPricePerToken:       litellmPricing.InputCostPerImageToken,
				ImageOutputPricePerToken:      litellmPricing.OutputCostPerImageToken,
				MaxReasoningEffortMultiplier:  defaultMaxReasoningEffortMultiplier(model),
			}), nil
		}
	}

	// 2. 使用硬编码回退价格
	fallback := s.getFallbackPricing(model)
	if fallback != nil {
		if _, seen := s.fallbackWarnSeen.LoadOrStore(model, struct{}{}); !seen {
			log.Printf("[Billing] Using fallback pricing for model: %s", model)
		}
		cloned := *fallback
		if cloned.MaxReasoningEffortMultiplier == nil {
			cloned.MaxReasoningEffortMultiplier = defaultMaxReasoningEffortMultiplier(model)
		}
		return s.applyModelSpecificPricingPolicy(model, &cloned), nil
	}

	return nil, fmt.Errorf("%w for model: %s", ErrModelPricingUnavailable, model)
}

// channelTierOverridePrice 根据模型目录中的层级比例推导渠道层级价格。
// 渠道只覆盖普通价时，不能把 priority/Fast 价格也压成普通价。
func channelTierOverridePrice(baseStandard, baseTier, channelStandard float64) float64 {
	if baseStandard > 0 && baseTier > 0 {
		return channelStandard * (baseTier / baseStandard)
	}
	return 0
}

// applyChannelTokenPriceOverrides 应用渠道 token 价格，同时保留模型内置层级比例。
func applyChannelTokenPriceOverrides(pricing *ModelPricing, channelPricing *ChannelModelPricing) {
	if pricing == nil || channelPricing == nil {
		return
	}
	if channelPricing.InputPrice != nil {
		priority := channelTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *channelPricing.InputPrice)
		pricing.InputPricePerToken = *channelPricing.InputPrice
		pricing.InputPricePerTokenPriority = priority
	}
	if channelPricing.OutputPrice != nil {
		priority := channelTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *channelPricing.OutputPrice)
		pricing.OutputPricePerToken = *channelPricing.OutputPrice
		pricing.OutputPricePerTokenPriority = priority
	}
	if channelPricing.CacheWritePrice != nil {
		basePriority := pricing.CacheCreationPricePerTokenPriority
		if pricing.cacheCreationPriorityDerived {
			// 兜底推导的 priority 价不代表模型原生目录配置；渠道显式
			// 覆盖缓存写价时，应继续保持“未配置 priority”的 fork 语义。
			basePriority = 0
		}
		priority := channelTierOverridePrice(pricing.CacheCreationPricePerToken, basePriority, *channelPricing.CacheWritePrice)
		pricing.CacheCreationPricePerToken = *channelPricing.CacheWritePrice
		pricing.CacheCreationPricePerTokenPriority = priority
		pricing.CacheCreationPriceExplicit = true
		pricing.cacheCreationPriorityDerived = false
		pricing.CacheCreation5mPrice = *channelPricing.CacheWritePrice
		if channelPricing.CacheWrite1hPrice == nil {
			// 兼容旧配置：未拆分时继续让 cache_write_price 覆盖两个 TTL 档位。
			pricing.CacheCreation1hPrice = *channelPricing.CacheWritePrice
		}
	}
	if channelPricing.CacheWrite1hPrice != nil {
		pricing.CacheCreation1hPrice = *channelPricing.CacheWrite1hPrice
		pricing.SupportsCacheBreakdown = true
	}
	if channelPricing.CacheReadPrice != nil {
		priority := channelTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *channelPricing.CacheReadPrice)
		pricing.CacheReadPricePerToken = *channelPricing.CacheReadPrice
		pricing.CacheReadPricePerTokenPriority = priority
	}
}

// GetModelPricingWithChannel 获取模型定价，渠道配置的价格覆盖默认值。
// 渠道存在时，未配置的图片输出价格归零，不回退到默认定价。
func (s *BillingService) GetModelPricingWithChannel(model string, channelPricing *ChannelModelPricing) (*ModelPricing, error) {
	pricing, err := s.GetModelPricing(model)
	if err != nil {
		return nil, err
	}
	if channelPricing == nil {
		return pricing, nil
	}
	// 防止修改 fallbackPrices 中的共享指针
	cloned := *pricing
	pricing = &cloned
	applyChannelTokenPriceOverrides(pricing, channelPricing)
	if channelPricing.ImageOutputPrice != nil {
		pricing.ImageOutputPricePerToken = *channelPricing.ImageOutputPrice
	} else {
		pricing.ImageOutputPricePerToken = 0
	}
	pricing.ImageOutputPriceExplicit = true
	applyChannelImageInputPrice(channelPricing, pricing)
	multiplier, configured := normalizedPriceMultiplier(channelPricing)
	if configured {
		pricing = multiplyModelPricing(pricing, multiplier)
	}
	applyChannelFastModeMultiplier(pricing, channelPricing)
	applyChannelFlexMultiplier(pricing, channelPricing)
	if channelPricing.MaxReasoningEffortMultiplier != nil {
		pricing.MaxReasoningEffortMultiplier = channelPricing.MaxReasoningEffortMultiplier
	}
	return pricing, nil
}

// --- 统一计费入口 ---

// CostInput 统一计费输入
type CostInput struct {
	Ctx             context.Context
	Model           string
	GroupID         *int64 // 用于渠道定价查找
	Group           *Group
	Tokens          UsageTokens
	RequestCount    int     // 按次计费时使用
	UsageUnits      float64 // 音频等连续计量单位（分钟/小时/百万字符）
	SizeTier        string  // 按次/图片模式的层级标签（"1K","2K","4K","HD" 等）
	RateMultiplier  float64
	PricingAt       time.Time             // 渠道分时定价使用的计费时刻
	ServiceTier     string                // "priority","flex","" 等
	ReasoningEffort string                // 最终转发的推理档位；max 可触发模型/渠道倍率
	Resolver        *ModelPricingResolver // 定价解析器
	Resolved        *ResolvedPricing      // 可选：预解析的定价结果（避免重复 Resolve 调用）
}

// CalculateCostUnified 统一计费入口，支持三种计费模式。
// 使用 ModelPricingResolver 解析定价，然后根据 BillingMode 分发计算。
func (s *BillingService) CalculateCostUnified(input CostInput) (*CostBreakdown, error) {
	if input.Resolver == nil {
		// 无 Resolver，回退到旧路径
		breakdown, err := s.calculateCostInternal(input.Model, input.Tokens, input.RateMultiplier, input.ServiceTier, nil)
		if err == nil {
			applyCostBreakdownMultiplier(breakdown, maxReasoningEffortBillingMultiplier(input.Model, input.ReasoningEffort, nil))
		}
		return breakdown, err
	}

	// 优先使用预解析结果，避免重复 Resolve 调用
	resolved := input.Resolved
	if resolved == nil {
		resolved = input.Resolver.Resolve(input.Ctx, PricingInput{
			Model:   input.Model,
			GroupID: input.GroupID,
			Group:   input.Group,
		})
	}

	// 保存时强制 > 0；若仍有负数泄漏（缓存/迁移残留），按 0 处理避免按 1x 误扣。
	if input.RateMultiplier < 0 {
		input.RateMultiplier = 0
	}

	var breakdown *CostBreakdown
	var err error
	switch resolved.Mode {
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		breakdown, err = s.calculatePerRequestCost(resolved, input)
	default: // BillingModeToken
		breakdown, err = s.calculateTokenCost(resolved, input)
	}
	if err == nil && breakdown != nil {
		breakdown.BillingMode = string(resolved.Mode)
		if breakdown.BillingMode == "" {
			breakdown.BillingMode = string(BillingModeToken)
		}
	}
	return breakdown, err
}

// calculateTokenCost 按 token 区间计费
func (s *BillingService) calculateTokenCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	totalContext := input.Tokens.InputTokens + input.Tokens.CacheCreationTokens + input.Tokens.CacheReadTokens

	pricing := input.Resolver.GetIntervalPricing(resolved, totalContext)
	if pricing == nil {
		return nil, fmt.Errorf("no pricing available for model: %s: %w", input.Model, ErrModelPricingUnavailable)
	}

	pricing = s.applyModelSpecificPricingPolicyEx(input.Model, pricing, resolved.Source == PricingSourceLiteLLM || resolved.Source == PricingSourceFallback)
	if resolved.Source == PricingSourceLiteLLM || resolved.Source == PricingSourceFallback {
		pricing = applyDeepSeekPeakPricing(input.Model, pricing, input.PricingAt)
	}

	// 长上下文定价仅在无区间定价且分组允许时应用（区间定价已包含上下文分层）。
	applyLongCtx := len(resolved.Intervals) == 0 && resolved.longContextPricingEnabled

	breakdown := s.computeTokenBreakdown(pricing, input.Tokens, input.RateMultiplier, input.ServiceTier, applyLongCtx)
	applyCostBreakdownMultiplier(breakdown, resolvedChannelTimeMultiplier(resolved, input.PricingAt))
	applyCostBreakdownMultiplier(breakdown, maxReasoningEffortBillingMultiplier(input.Model, input.ReasoningEffort, pricing))
	return breakdown, nil
}

// computeTokenBreakdown 是 token 计费的核心逻辑，由 calculateTokenCost 和 calculateCostInternal 共用。
// applyLongCtx 控制是否检查长上下文定价（区间定价已自含上下文分层，不需要额外应用）。
func (s *BillingService) computeTokenBreakdown(
	pricing *ModelPricing, tokens UsageTokens,
	rateMultiplier float64, serviceTier string,
	applyLongCtx bool,
) *CostBreakdown {
	// 保存时强制 > 0；若仍有负数泄漏，按 0 处理避免按 1x 误扣。
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}

	inputPrice := pricing.InputPricePerToken
	outputPrice := pricing.OutputPricePerToken
	cacheReadPrice := pricing.CacheReadPricePerToken
	cacheCreationPrice := pricing.CacheCreationPricePerToken
	cacheCreationMultiplier := 1.0
	tierMultiplier := 1.0

	tier := normalizeBillingServiceTier(serviceTier)
	if tier == "priority" || tier == "fast" {
		if fastMultiplier, configured := normalizedFastModeMultiplier(pricing); configured {
			// 渠道显式倍率以普通模式最终价为基准，避免和模型内置 priority 价重复叠乘。
			tierMultiplier = fastMultiplier
		} else if usePriorityServiceTierPricing(serviceTier, pricing) {
			if pricing.InputPricePerTokenPriority > 0 {
				inputPrice = pricing.InputPricePerTokenPriority
			}
			if pricing.OutputPricePerTokenPriority > 0 {
				outputPrice = pricing.OutputPricePerTokenPriority
			}
			if pricing.CacheReadPricePerTokenPriority > 0 {
				cacheReadPrice = pricing.CacheReadPricePerTokenPriority
			}
			if pricing.CacheCreationPricePerTokenPriority > 0 {
				cacheCreationPrice = pricing.CacheCreationPricePerTokenPriority
			}
		} else {
			tierMultiplier = serviceTierCostMultiplier(serviceTier)
		}
	} else {
		tierMultiplier = configuredServiceTierMultiplier(serviceTier, pricing)
	}

	longContextPricingEligible := applyLongCtx && s.shouldApplySessionLongContextPricing(tokens, pricing)
	var baselineCost *CostBreakdown
	if longContextPricingEligible {
		baselineCost = s.computeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, false)
		// 未配置的一侧倍率按 1 计，避免部分覆盖条目把对应分项算成免费。
		longContextInputMultiplier := longContextMultiplierOrOne(pricing.LongContextInputMultiplier)
		inputPrice *= longContextInputMultiplier
		outputPrice *= longContextMultiplierOrOne(pricing.LongContextOutputMultiplier)
		// 缓存读取本质上是输入侧的复用，应与 input 一同应用长上下文倍率；
		// 否则 cache hit 越多，少计的费用越多（见 #2293）。
		cacheReadPrice *= longContextInputMultiplier
		// 缓存创建（cache_write）也是输入侧操作，三档价格（标准 / 5m / 1h）
		// 都通过 computeCacheCreationCost 直接读取 pricing.*，不会经过这里
		// 的倍率修改，因此显式向下传一个倍率，避免长上下文场景下被漏乘。
		cacheCreationMultiplier = longContextInputMultiplier
	}

	bd := &CostBreakdown{}
	// 分离图片输入 token 与文本输入 token（多模态 embedding、图片编辑等图文不同价场景）。
	// InputCost 仅计文本输入，图片输入费用单独记入 ImageInputCost，便于对账；总额不变。
	// ImageInputTokens 为 0 时（绝大多数 chat/vision 流量）走原始单价路径，行为不变。
	if tokens.ImageInputTokens > 0 {
		imageInputTokens := tokens.ImageInputTokens
		textInputTokens := tokens.InputTokens - imageInputTokens
		if textInputTokens < 0 {
			textInputTokens = 0
			imageInputTokens = tokens.InputTokens
		}
		imageInputPrice := pricing.ImageInputPricePerToken
		if imageInputPrice == 0 {
			imageInputPrice = inputPrice
		}
		bd.InputCost = float64(textInputTokens) * inputPrice
		bd.ImageInputCost = float64(imageInputTokens) * imageInputPrice
	} else {
		bd.InputCost = float64(tokens.InputTokens) * inputPrice
	}

	// 分离图片输出 token 与文本输出 token
	textOutputTokens := tokens.OutputTokens - tokens.ImageOutputTokens
	if textOutputTokens < 0 {
		textOutputTokens = 0
	}
	bd.OutputCost = float64(textOutputTokens) * outputPrice

	// 图片输出 token 费用（独立费率）
	if tokens.ImageOutputTokens > 0 {
		imgPrice := pricing.ImageOutputPricePerToken
		if imgPrice == 0 && !pricing.ImageOutputPriceExplicit {
			imgPrice = outputPrice
		}
		bd.ImageOutputCost = float64(tokens.ImageOutputTokens) * imgPrice
	}

	// 缓存创建费用
	bd.CacheCreationCost = s.computeCacheCreationCost(pricing, tokens, cacheCreationPrice, cacheCreationMultiplier)

	bd.CacheReadCost = float64(tokens.CacheReadTokens) * cacheReadPrice

	if tierMultiplier != 1.0 {
		bd.InputCost *= tierMultiplier
		bd.ImageInputCost *= tierMultiplier
		bd.OutputCost *= tierMultiplier
		bd.ImageOutputCost *= tierMultiplier
		bd.CacheCreationCost *= tierMultiplier
		bd.CacheReadCost *= tierMultiplier
	}

	bd.TotalCost = bd.InputCost + bd.ImageInputCost + bd.OutputCost + bd.ImageOutputCost +
		bd.CacheCreationCost + bd.CacheReadCost
	bd.ActualCost = bd.TotalCost * rateMultiplier
	bd.LongContextBillingApplied = baselineCost != nil && bd.ActualCost > baselineCost.ActualCost

	return bd
}

// computeCacheCreationCost 计算缓存创建费用（支持 5m/1h 分类或标准计费）。
// multiplier 用于长上下文等场景下的整体价格缩放（普通调用传 1.0 即可）。
func (s *BillingService) computeCacheCreationCost(pricing *ModelPricing, tokens UsageTokens, price, multiplier float64) float64 {
	if pricing.SupportsCacheBreakdown && (pricing.CacheCreation5mPrice > 0 || pricing.CacheCreation1hPrice > 0) {
		cacheCreation5mTokens, cacheCreation1hTokens := normalizeCacheCreationBreakdown(tokens)
		if cacheCreation5mTokens == 0 && cacheCreation1hTokens == 0 && tokens.CacheCreationTokens > 0 {
			// API 未返回 ephemeral 明细，回退到全部按 5m 单价计费
			return float64(tokens.CacheCreationTokens) * pricing.CacheCreation5mPrice * multiplier
		}
		return float64(cacheCreation5mTokens)*pricing.CacheCreation5mPrice*multiplier +
			float64(cacheCreation1hTokens)*pricing.CacheCreation1hPrice*multiplier
	}
	return float64(tokens.CacheCreationTokens) * price * multiplier
}

// normalizeCacheCreationBreakdown 在聚合值为正且 TTL 明细相互矛盾时封顶明细，
// 并在整数 token 约束下尽量保留上游报告的比例。
func normalizeCacheCreationBreakdown(tokens UsageTokens) (int, int) {
	cacheCreation5mTokens := tokens.CacheCreation5mTokens
	cacheCreation1hTokens := tokens.CacheCreation1hTokens
	aggregate := tokens.CacheCreationTokens
	if cacheCreation5mTokens < 0 {
		cacheCreation5mTokens = 0
	}
	if cacheCreation1hTokens < 0 {
		cacheCreation1hTokens = 0
	}
	if aggregate <= 0 || (cacheCreation5mTokens <= aggregate && cacheCreation1hTokens <= aggregate-cacheCreation5mTokens) {
		return cacheCreation5mTokens, cacheCreation1hTokens
	}

	detailTotal := float64(cacheCreation5mTokens) + float64(cacheCreation1hTokens)
	normalized5mTokens := math.Round(float64(aggregate) * float64(cacheCreation5mTokens) / detailTotal)
	if normalized5mTokens >= float64(aggregate) {
		cacheCreation5mTokens = aggregate
	} else {
		cacheCreation5mTokens = int(normalized5mTokens)
	}
	return cacheCreation5mTokens, aggregate - cacheCreation5mTokens
}

// calculatePerRequestCost 按次/图片计费
func (s *BillingService) calculatePerRequestCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	units := input.UsageUnits
	if units <= 0 {
		count := input.RequestCount
		if count <= 0 {
			count = 1
		}
		units = float64(count)
	}

	var unitPrice float64
	var priceFound bool

	if input.SizeTier != "" {
		unitPrice, priceFound = input.Resolver.GetRequestTierPriceValue(resolved, input.SizeTier)
	}

	if !priceFound {
		totalContext := input.Tokens.InputTokens + input.Tokens.CacheCreationTokens + input.Tokens.CacheReadTokens
		unitPrice, priceFound = input.Resolver.GetRequestTierPriceByContextValue(resolved, totalContext)
	}

	// 回退到默认按次价格
	if !priceFound {
		unitPrice = resolved.DefaultPerRequestPrice
	}

	totalCost := unitPrice * units
	actualCost := totalCost * input.RateMultiplier

	return &CostBreakdown{
		TotalCost:  totalCost,
		ActualCost: actualCost,
	}, nil
}

// CalculateCost 计算使用费用
func (s *BillingService) CalculateCost(model string, tokens UsageTokens, rateMultiplier float64) (*CostBreakdown, error) {
	return s.calculateCostInternal(model, tokens, rateMultiplier, "", nil)
}

func (s *BillingService) CalculateCostWithServiceTier(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	return s.calculateCostInternal(model, tokens, rateMultiplier, serviceTier, nil)
}

func (s *BillingService) calculateCostInternal(model string, tokens UsageTokens, rateMultiplier float64, serviceTier string, channelPricing *ChannelModelPricing) (*CostBreakdown, error) {
	var pricing *ModelPricing
	var err error
	if channelPricing != nil {
		pricing, err = s.GetModelPricingWithChannel(model, channelPricing)
	} else {
		pricing, err = s.GetModelPricing(model)
	}
	if err != nil {
		return nil, err
	}
	if channelPricing == nil {
		pricing = applyDeepSeekPeakPricing(model, pricing, time.Time{})
	}

	return s.computeTokenBreakdown(pricing, tokens, rateMultiplier, serviceTier, true), nil
}

func (s *BillingService) applyModelSpecificPricingPolicy(model string, pricing *ModelPricing) *ModelPricing {
	return s.applyModelSpecificPricingPolicyEx(model, pricing, true)
}

// applyModelSpecificPricingPolicyEx 应用模型专属定价修正；forceDeepSeekRates 为 false
// 时保留分组/渠道对 DeepSeek 的显式价格，避免官方价覆盖运营者配置。
func (s *BillingService) applyModelSpecificPricingPolicyEx(model string, pricing *ModelPricing, forceDeepSeekRates bool) *ModelPricing {
	if pricing == nil {
		return nil
	}
	if forceDeepSeekRates && isDeepSeekModel(model) {
		return applyDeepSeekOfficialPricing(model, pricing)
	}
	normalized := normalizeKnownOpenAICodexModel(model)
	isGPT56 := isOpenAIGPT56Model(normalized)
	needsCacheCreationPolicy := isGPT56 && !pricing.CacheCreationPriceExplicit && (pricing.CacheCreationPricePerToken <= 0 ||
		(pricing.InputPricePerTokenPriority > 0 && pricing.CacheCreationPricePerTokenPriority <= 0))
	fastRatio := openAIModelFastPricingRatio(normalized)
	if !needsCacheCreationPolicy && fastRatio <= 0 {
		return pricing
	}
	cloned := *pricing
	if isGPT56 && !cloned.CacheCreationPriceExplicit {
		if cloned.CacheCreationPricePerToken <= 0 {
			cloned.CacheCreationPricePerToken = cloned.InputPricePerToken * 1.25
		}
		if cloned.CacheCreationPricePerTokenPriority <= 0 {
			cloned.CacheCreationPricePerTokenPriority = cloned.InputPricePerTokenPriority * 1.25
		}
	}
	if fastRatio > 0 {
		enforceOpenAIFastPricingRatio(&cloned, fastRatio)
	}
	return &cloned
}

// longContextMultiplierOrOne 将未配置的长上下文倍率归一为 1。
func longContextMultiplierOrOne(multiplier float64) float64 {
	if multiplier <= 0 {
		return 1
	}
	return multiplier
}

// openAIModelFastPricingRatio 返回业务口径下 OpenAI GPT-5.x 模型 Fast/priority
// 的标准价倍率：gpt-5.6 系列与 gpt-5.4 为 2x，gpt-5.5 为 2.5x。未定义 Fast
// 档的模型（如 gpt-5.5-pro、gpt-5.4-mini/nano）返回 0。
func openAIModelFastPricingRatio(normalized string) float64 {
	switch normalized {
	case "gpt-5.4", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
		return 2.0
	case "gpt-5.5":
		return 2.5
	default:
		return 0
	}
}

// enforceOpenAIFastPricingRatio 把 priority 档价格改写为「标准价 × ratio」。
// 本地/远程 LiteLLM 目录可能只带官方旧口径（如 gpt-5.5 priority 仍标 2x），
// 直接采用会导致 Fast 模式少计费；这里按业务倍率兜底修正，且对已正确的
// fallback 条目（2x/2.5x）是幂等的。computeTokenBreakdown 在 priority 价格
// 存在时走显式档位价、不再叠加通用 tier 倍率，因此不会重复乘价。
func enforceOpenAIFastPricingRatio(pricing *ModelPricing, ratio float64) {
	if pricing == nil || ratio <= 0 {
		return
	}
	pricing.InputPricePerTokenPriority = pricing.InputPricePerToken * ratio
	pricing.OutputPricePerTokenPriority = pricing.OutputPricePerToken * ratio
	if pricing.CacheReadPricePerToken > 0 {
		pricing.CacheReadPricePerTokenPriority = pricing.CacheReadPricePerToken * ratio
	}
	// 渠道显式覆盖缓存写价格时，保留其是否配置 priority 的原语义，
	// 不因模型 Fast 兜底倍率凭空生成未配置的档位价。
	if !pricing.CacheCreationPriceExplicit && pricing.CacheCreationPricePerToken > 0 {
		hadNativePriority := pricing.CacheCreationPricePerTokenPriority > 0
		pricing.CacheCreationPricePerTokenPriority = pricing.CacheCreationPricePerToken * ratio
		pricing.cacheCreationPriorityDerived = !hadNativePriority && pricing.CacheCreationPricePerTokenPriority > 0
	}
}

func (s *BillingService) shouldApplySessionLongContextPricing(tokens UsageTokens, pricing *ModelPricing) bool {
	if pricing == nil || pricing.LongContextInputThreshold <= 0 {
		return false
	}
	if pricing.LongContextInputMultiplier <= 1 && pricing.LongContextOutputMultiplier <= 1 {
		return false
	}
	totalInputTokens := tokens.InputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
	if pricing.LongContextThresholdInclusive {
		return totalInputTokens >= pricing.LongContextInputThreshold
	}
	return totalInputTokens > pricing.LongContextInputThreshold
}

// CalculateCostWithConfig 使用配置中的默认倍率计算费用
func (s *BillingService) CalculateCostWithConfig(model string, tokens UsageTokens) (*CostBreakdown, error) {
	multiplier := s.cfg.Default.RateMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}
	return s.CalculateCost(model, tokens, multiplier)
}

// CalculateCostWithLongContext 计算费用，支持长上下文双倍计费
// threshold: 阈值（如 200000），超过此值的部分按 extraMultiplier 倍计费
// extraMultiplier: 超出部分的倍率（如 2.0 表示双倍）
//
// 示例：缓存 210k + 输入 10k = 220k，阈值 200k，倍率 2.0
// 拆分为：范围内 (200k, 0) + 范围外 (10k, 10k)
// 范围内正常计费，范围外 × 2 计费
func (s *BillingService) CalculateCostWithLongContext(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64) (*CostBreakdown, error) {
	return s.CalculateCostWithLongContextAndServiceTier(model, tokens, rateMultiplier, threshold, extraMultiplier, "")
}

// CalculateCostWithLongContextAndServiceTier 同时应用长上下文与 Fast/Flex 层级价格。
func (s *BillingService) CalculateCostWithLongContextAndServiceTier(model string, tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64, serviceTier string) (*CostBreakdown, error) {
	// 未启用长上下文计费，直接走正常计费
	if threshold <= 0 || extraMultiplier <= 1 {
		return s.CalculateCostWithServiceTier(model, tokens, rateMultiplier, serviceTier)
	}

	// 计算总输入 token（缓存读取 + 新输入）
	total := tokens.CacheReadTokens + tokens.InputTokens
	if total <= threshold {
		return s.CalculateCostWithServiceTier(model, tokens, rateMultiplier, serviceTier)
	}

	// 拆分成范围内和范围外
	var inRangeCacheTokens, inRangeInputTokens int
	var outRangeCacheTokens, outRangeInputTokens int

	if tokens.CacheReadTokens >= threshold {
		// 缓存已超过阈值：范围内只有缓存，范围外是超出的缓存+全部输入
		inRangeCacheTokens = threshold
		inRangeInputTokens = 0
		outRangeCacheTokens = tokens.CacheReadTokens - threshold
		outRangeInputTokens = tokens.InputTokens
	} else {
		// 缓存未超过阈值：范围内是全部缓存+部分输入，范围外是剩余输入
		inRangeCacheTokens = tokens.CacheReadTokens
		inRangeInputTokens = threshold - tokens.CacheReadTokens
		outRangeCacheTokens = 0
		outRangeInputTokens = tokens.InputTokens - inRangeInputTokens
	}

	// 范围内部分：正常计费
	inRangeTokens := UsageTokens{
		InputTokens:           inRangeInputTokens,
		OutputTokens:          tokens.OutputTokens, // 输出只算一次
		CacheCreationTokens:   tokens.CacheCreationTokens,
		CacheReadTokens:       inRangeCacheTokens,
		CacheCreation5mTokens: tokens.CacheCreation5mTokens,
		CacheCreation1hTokens: tokens.CacheCreation1hTokens,
		ImageOutputTokens:     tokens.ImageOutputTokens,
	}
	inRangeCost, err := s.CalculateCostWithServiceTier(model, inRangeTokens, rateMultiplier, serviceTier)
	if err != nil {
		return nil, err
	}

	// 范围外部分：× extraMultiplier 计费
	outRangeTokens := UsageTokens{
		InputTokens:     outRangeInputTokens,
		CacheReadTokens: outRangeCacheTokens,
	}
	outRangeCost, err := s.CalculateCostWithServiceTier(model, outRangeTokens, rateMultiplier*extraMultiplier, serviceTier)
	if err != nil {
		return inRangeCost, fmt.Errorf("out-range cost: %w", err)
	}

	// 合并成本
	return &CostBreakdown{
		InputCost:                 inRangeCost.InputCost + outRangeCost.InputCost,
		ImageInputCost:            inRangeCost.ImageInputCost + outRangeCost.ImageInputCost,
		OutputCost:                inRangeCost.OutputCost,
		ImageOutputCost:           inRangeCost.ImageOutputCost,
		CacheCreationCost:         inRangeCost.CacheCreationCost,
		CacheReadCost:             inRangeCost.CacheReadCost + outRangeCost.CacheReadCost,
		TotalCost:                 inRangeCost.TotalCost + outRangeCost.TotalCost,
		ActualCost:                inRangeCost.ActualCost + outRangeCost.ActualCost,
		LongContextBillingApplied: outRangeCost.ActualCost > 0,
	}, nil
}

// ListSupportedModels 列出所有支持的模型（现在总是返回true，因为有模糊匹配）
func (s *BillingService) ListSupportedModels() []string {
	models := make([]string, 0)
	// 返回回退价格支持的模型系列
	for model := range s.fallbackPrices {
		models = append(models, model)
	}
	return models
}

// IsModelSupported 检查模型是否支持（现在总是返回true，因为有模糊匹配回退）
func (s *BillingService) IsModelSupported(model string) bool {
	// 所有Claude模型都有回退价格支持
	modelLower := strings.ToLower(model)
	return strings.Contains(modelLower, "claude") ||
		strings.Contains(modelLower, "opus") ||
		strings.Contains(modelLower, "sonnet") ||
		strings.Contains(modelLower, "haiku")
}

// GetEstimatedCost 估算费用（用于前端展示）
func (s *BillingService) GetEstimatedCost(model string, estimatedInputTokens, estimatedOutputTokens int) (float64, error) {
	tokens := UsageTokens{
		InputTokens:  estimatedInputTokens,
		OutputTokens: estimatedOutputTokens,
	}

	breakdown, err := s.CalculateCostWithConfig(model, tokens)
	if err != nil {
		return 0, err
	}

	return breakdown.ActualCost, nil
}

// GetPricingServiceStatus 获取价格服务状态
func (s *BillingService) GetPricingServiceStatus() map[string]any {
	if s.pricingService != nil {
		return s.pricingService.GetStatus()
	}
	return map[string]any{
		"model_count":  len(s.fallbackPrices),
		"last_updated": "using fallback",
		"local_hash":   "N/A",
	}
}

// ForceUpdatePricing 强制更新价格数据
func (s *BillingService) ForceUpdatePricing() error {
	if s.pricingService != nil {
		return s.pricingService.ForceUpdate()
	}
	return fmt.Errorf("pricing service not initialized")
}

// ModelDisplayPricing 是面向前端展示的模型价格快照。
// 所有价格都已经应用了分组倍率，直接表示实际扣费单价。
type ModelDisplayPricing struct {
	PricingMode             string
	PriceStatus             string
	InputPricePerToken      float64
	ImageInputPricePerToken float64
	OutputPricePerToken     float64
	CacheWritePricePerToken float64
	// CacheWrite1hPricePerToken 是可选的 1 小时缓存写入展示单价。
	CacheWrite1hPricePerToken     float64
	CacheReadPricePerToken        float64
	ImageOutputPricePerToken      float64
	FastInputPricePerToken        float64
	FastImageInputPricePerToken   float64
	FastOutputPricePerToken       float64
	FastCacheWritePricePerToken   float64
	FastCacheWrite1hPricePerToken float64
	FastCacheReadPricePerToken    float64
	FastImageOutputPricePerToken  float64
	ContextIntervals              []ModelDisplayPricingInterval
	ImagePrice1K                  float64
	ImagePrice2K                  float64
	ImagePrice4K                  float64
}

// ModelDisplayPricingInterval 是按上下文 token 区间展示的模型价格。
type ModelDisplayPricingInterval struct {
	MinTokens                     int
	MaxTokens                     *int
	InputPricePerToken            float64
	ImageInputPricePerToken       float64
	OutputPricePerToken           float64
	CacheWritePricePerToken       float64
	CacheWrite1hPricePerToken     float64
	CacheReadPricePerToken        float64
	ImageOutputPricePerToken      float64
	FastInputPricePerToken        float64
	FastImageInputPricePerToken   float64
	FastOutputPricePerToken       float64
	FastCacheWritePricePerToken   float64
	FastCacheWrite1hPricePerToken float64
	FastCacheReadPricePerToken    float64
	FastImageOutputPricePerToken  float64
}

// GetDisplayPricing 返回用于模型广场展示的价格信息。
// 它会优先识别图片模型并展示按图计费，否则展示按 token 计费。
func (s *BillingService) GetDisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	return s.getDisplayPricing(model, rateMultiplier)
}

// getDisplayPricing 使用分组倍率计算模型广场展示价格。
func (s *BillingService) getDisplayPricing(model string, rateMultiplier float64) ModelDisplayPricing {
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}

	rawPricing := s.getRawModelPricing(model)
	if hasExplicitImageGenerationPricing(rawPricing) || looksLikeImageModel(model) {
		return buildImageDisplayPricing(
			s.getDefaultImagePrice(model, "1K")*rateMultiplier,
			s.getDefaultImagePrice(model, "2K")*rateMultiplier,
			s.getDefaultImagePrice(model, "4K")*rateMultiplier,
		)
	}

	pricing, err := s.GetModelPricing(model)
	if err != nil || pricing == nil || !hasAnyDisplayTokenPricing(pricing) {
		return unknownDisplayPricing()
	}

	return buildTokenDisplayPricing(pricing, rateMultiplier)
}

// getDisplayPricingWithResolvedMultipliers 优先使用已解析的渠道价格计算展示价格。
func (s *BillingService) getDisplayPricingWithResolvedMultipliers(model string, rateMultiplier float64, resolved *ResolvedPricing) ModelDisplayPricing {
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	if resolved.IsUnpriced() {
		return unknownDisplayPricing()
	}
	if pricing, ok := displayPricingFromResolved(model, rateMultiplier, resolved); ok {
		return pricing
	}
	return s.getDisplayPricing(model, rateMultiplier)
}

// displayPricingFromResolved 将已解析的计费配置转换成模型广场展示价格。
func displayPricingFromResolved(model string, rateMultiplier float64, resolved *ResolvedPricing) (ModelDisplayPricing, bool) {
	if resolved == nil {
		return ModelDisplayPricing{}, false
	}

	switch resolved.Mode {
	case BillingModeToken:
		pricing := resolvedDisplayTokenPricing(resolved)
		if pricing != nil && !resolved.longContextPricingEnabled {
			pricing = withoutLongContextDisplayPricing(pricing)
		}
		if pricing != nil && (hasAnyDisplayTokenPricing(pricing) || resolved.HasEffectiveOverridePricing()) {
			return buildTokenDisplayPricing(pricing, rateMultiplier), true
		}
		intervals := resolvedDisplayPricingIntervals(resolved, rateMultiplier)
		if len(intervals) > 0 {
			return buildTokenIntervalDisplayPricing(intervals), true
		}
		return ModelDisplayPricing{}, false
	case BillingModeImage, BillingModePerRequest:
		if resolved.Source != PricingSourceGroup && resolved.Source != PricingSourceChannel {
			return ModelDisplayPricing{}, false
		}
		if resolved.Mode == BillingModePerRequest && !looksLikeImageModel(model) {
			return ModelDisplayPricing{}, false
		}
		price1K, price2K, price4K := resolvedImageTierPrices(resolved)
		if price1K <= 0 && price2K <= 0 && price4K <= 0 && !resolved.HasEffectiveOverridePricing() {
			return ModelDisplayPricing{}, false
		}
		return buildImageDisplayPricing(
			price1K*rateMultiplier,
			price2K*rateMultiplier,
			price4K*rateMultiplier,
		), true
	default:
		return ModelDisplayPricing{}, false
	}
}

// withoutLongContextDisplayPricing 只移除内置长上下文展示元数据，不改变基础单价。
func withoutLongContextDisplayPricing(pricing *ModelPricing) *ModelPricing {
	if pricing == nil {
		return nil
	}
	cloned := *pricing
	cloned.LongContextInputThreshold = 0
	cloned.LongContextInputMultiplier = 0
	cloned.LongContextOutputMultiplier = 0
	return &cloned
}

// resolvedTokenPriceRange 是正上下文范围的实际价格；nil 表示该范围缺价。
type resolvedTokenPriceRange struct {
	minTokens int
	maxTokens *int
	pricing   *ModelPricing
}

// resolvedTokenPriceRanges 补齐显式区间前后与中间的默认价范围，不修改原价卡。
func resolvedTokenPriceRanges(resolved *ResolvedPricing) []resolvedTokenPriceRange {
	if resolved == nil || len(resolved.Intervals) == 0 {
		return nil
	}
	intervals := append([]PricingInterval(nil), resolved.Intervals...)
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].MinTokens < intervals[j].MinTokens })
	ranges := make([]resolvedTokenPriceRange, 0, len(intervals)*2+1)
	cursor := 0
	for i := range intervals {
		interval := &intervals[i]
		if interval.MinTokens > cursor {
			maxTokens := interval.MinTokens
			ranges = append(ranges, resolvedTokenPriceRange{cursor, &maxTokens, withoutLongContextDisplayPricing(resolved.tokenPricingForInterval(nil))})
		}
		ranges = append(ranges, resolvedTokenPriceRange{interval.MinTokens, interval.MaxTokens, resolved.tokenPricingForInterval(interval)})
		if interval.MaxTokens == nil {
			return ranges
		}
		cursor = *interval.MaxTokens
	}
	return append(ranges, resolvedTokenPriceRange{cursor, nil, withoutLongContextDisplayPricing(resolved.tokenPricingForInterval(nil))})
}

func resolvedDisplayTokenPricing(resolved *ResolvedPricing) *ModelPricing {
	if resolved == nil {
		return nil
	}
	if len(resolved.Intervals) == 0 {
		return resolved.tokenPricingForInterval(nil)
	}
	ranges := resolvedTokenPriceRanges(resolved)
	pricing := ranges[0].pricing
	// 缺价范围或不同价格不能压平成一个覆盖所有上下文的单价。
	for _, priceRange := range ranges {
		if priceRange.pricing == nil || !sameDisplayTokenPricing(pricing, priceRange.pricing) {
			return nil
		}
	}
	return pricing
}

// resolvedDisplayPricingIntervals 展示全部有定价的范围，缺价范围不伪装成免费。
func resolvedDisplayPricingIntervals(resolved *ResolvedPricing, rateMultiplier float64) []ModelDisplayPricingInterval {
	var intervals []ModelDisplayPricingInterval
	for _, priceRange := range resolvedTokenPriceRanges(resolved) {
		if priceRange.pricing != nil {
			intervals = append(intervals, modelPricingDisplayInterval(priceRange.minTokens, priceRange.maxTokens, priceRange.pricing, rateMultiplier))
		}
	}
	return intervals
}

func pricingIntervalHasEffectiveTokenPricing(interval PricingInterval) bool {
	return interval.InputPrice != nil ||
		interval.OutputPrice != nil ||
		interval.CacheWritePrice != nil ||
		interval.CacheWrite1hPrice != nil ||
		interval.CacheReadPrice != nil
}

func sameDisplayTokenPricing(a *ModelPricing, b *ModelPricing) bool {
	if a == nil || b == nil {
		return a == b
	}
	// 普通价和 Fast 价都相同才能合并，避免默认段与显式段的服务层级差异被隐藏。
	return modelPricingDisplayInterval(0, nil, a, 1) == modelPricingDisplayInterval(0, nil, b, 1)
}

func resolvedImageTierPrices(resolved *ResolvedPricing) (float64, float64, float64) {
	if resolved == nil {
		return 0, 0, 0
	}

	defaultPrice := resolved.DefaultPerRequestPrice
	price1K := resolvedRequestTierPrice(resolved.RequestTiers, "1K", defaultPrice)
	price2K := resolvedRequestTierPrice(resolved.RequestTiers, "2K", defaultPrice)
	price4K := resolvedRequestTierPrice(resolved.RequestTiers, "4K", defaultPrice)
	return price1K, price2K, price4K
}

func resolvedRequestTierPrice(tiers []PricingInterval, label string, defaultPrice float64) float64 {
	for _, tier := range tiers {
		if strings.EqualFold(tier.TierLabel, label) && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice
		}
	}
	return defaultPrice
}

func buildTokenDisplayPricing(pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricing {
	if intervals := longContextDisplayPricingIntervals(pricing, rateMultiplier); len(intervals) > 0 {
		return buildTokenIntervalDisplayPricing(intervals)
	}

	cacheWritePrice, cacheWrite1hPrice := cacheCreationDisplayPrices(pricing)
	displayPricing := ModelDisplayPricing{
		PricingMode:               "token",
		PriceStatus:               "priced",
		InputPricePerToken:        pricing.InputPricePerToken * rateMultiplier,
		ImageInputPricePerToken:   pricing.ImageInputPricePerToken * rateMultiplier,
		OutputPricePerToken:       pricing.OutputPricePerToken * rateMultiplier,
		CacheWritePricePerToken:   cacheWritePrice * rateMultiplier,
		CacheWrite1hPricePerToken: cacheWrite1hPrice * rateMultiplier,
		CacheReadPricePerToken:    pricing.CacheReadPricePerToken * rateMultiplier,
		ImageOutputPricePerToken:  pricing.ImageOutputPricePerToken * rateMultiplier,
	}
	if fastPricing, ok := fastModeDisplayPricing(pricing); ok {
		displayPricing.FastInputPricePerToken = fastPricing.InputPricePerToken * rateMultiplier
		displayPricing.FastImageInputPricePerToken = fastPricing.ImageInputPricePerToken * rateMultiplier
		displayPricing.FastOutputPricePerToken = fastPricing.OutputPricePerToken * rateMultiplier
		fastCacheWritePrice, fastCacheWrite1hPrice := cacheCreationDisplayPrices(fastPricing)
		displayPricing.FastCacheWritePricePerToken = fastCacheWritePrice * rateMultiplier
		displayPricing.FastCacheWrite1hPricePerToken = fastCacheWrite1hPrice * rateMultiplier
		displayPricing.FastCacheReadPricePerToken = fastPricing.CacheReadPricePerToken * rateMultiplier
		displayPricing.FastImageOutputPricePerToken = fastPricing.ImageOutputPricePerToken * rateMultiplier
	}
	return displayPricing
}

// cacheCreationDisplayPrices 返回展示用的 5m/1h 缓存写入单价。
// 未启用 TTL 明细时只返回兼容旧配置的聚合单价。
func cacheCreationDisplayPrices(pricing *ModelPricing) (float64, float64) {
	if pricing == nil {
		return 0, 0
	}
	short := pricing.CacheCreationPricePerToken
	if pricing.SupportsCacheBreakdown && pricing.CacheCreation5mPrice > 0 {
		short = pricing.CacheCreation5mPrice
	}
	if !pricing.SupportsCacheBreakdown {
		return short, 0
	}
	return short, pricing.CacheCreation1hPrice
}

// longContextDisplayPricingIntervals 将内置长上下文倍率转换成模型广场可展示的两段价格。
func longContextDisplayPricingIntervals(pricing *ModelPricing, rateMultiplier float64) []ModelDisplayPricingInterval {
	if !hasLongContextDisplayPricing(pricing) {
		return nil
	}

	maxTokens := pricing.LongContextInputThreshold
	baseInterval := modelPricingDisplayInterval(0, &maxTokens, pricing, rateMultiplier)

	longContextPricing := applyLongContextDisplayMultipliers(pricing)
	longContextInterval := modelPricingDisplayInterval(pricing.LongContextInputThreshold, nil, longContextPricing, rateMultiplier)

	return []ModelDisplayPricingInterval{baseInterval, longContextInterval}
}

// applyLongContextDisplayMultipliers 按结算规则生成长上下文展示价格，不修改原始模型定价。
// 缓存创建与读取都属于输入侧，普通价、priority 价及缓存时长明细必须使用同一输入倍率。
func applyLongContextDisplayMultipliers(pricing *ModelPricing) *ModelPricing {
	if pricing == nil {
		return nil
	}
	adjusted := *pricing
	// 与结算路径保持一致：覆盖文件只声明一侧倍率时，另一侧按 1x 展示，不能显示为免费。
	inputMultiplier := longContextMultiplierOrOne(pricing.LongContextInputMultiplier)
	outputMultiplier := longContextMultiplierOrOne(pricing.LongContextOutputMultiplier)
	adjusted.InputPricePerToken *= inputMultiplier
	adjusted.InputPricePerTokenPriority *= inputMultiplier
	adjusted.OutputPricePerToken *= outputMultiplier
	adjusted.OutputPricePerTokenPriority *= outputMultiplier
	adjusted.CacheCreationPricePerToken *= inputMultiplier
	adjusted.CacheCreationPricePerTokenPriority *= inputMultiplier
	adjusted.CacheCreation5mPrice *= inputMultiplier
	adjusted.CacheCreation1hPrice *= inputMultiplier
	adjusted.CacheReadPricePerToken *= inputMultiplier
	adjusted.CacheReadPricePerTokenPriority *= inputMultiplier
	return &adjusted
}

func hasLongContextDisplayPricing(pricing *ModelPricing) bool {
	return hasAnyDisplayTokenPricing(pricing) &&
		pricing.LongContextInputThreshold > 0 &&
		(pricing.LongContextInputMultiplier > 1 || pricing.LongContextOutputMultiplier > 1)
}

func modelPricingDisplayInterval(minTokens int, maxTokens *int, pricing *ModelPricing, rateMultiplier float64) ModelDisplayPricingInterval {
	cacheWritePrice, cacheWrite1hPrice := cacheCreationDisplayPrices(pricing)
	interval := ModelDisplayPricingInterval{
		MinTokens:                 minTokens,
		MaxTokens:                 maxTokens,
		InputPricePerToken:        pricing.InputPricePerToken * rateMultiplier,
		ImageInputPricePerToken:   pricing.ImageInputPricePerToken * rateMultiplier,
		OutputPricePerToken:       pricing.OutputPricePerToken * rateMultiplier,
		CacheWritePricePerToken:   cacheWritePrice * rateMultiplier,
		CacheWrite1hPricePerToken: cacheWrite1hPrice * rateMultiplier,
		CacheReadPricePerToken:    pricing.CacheReadPricePerToken * rateMultiplier,
		ImageOutputPricePerToken:  pricing.ImageOutputPricePerToken * rateMultiplier,
	}
	if fastPricing, ok := fastModeDisplayPricing(pricing); ok {
		interval.FastInputPricePerToken = fastPricing.InputPricePerToken * rateMultiplier
		interval.FastImageInputPricePerToken = fastPricing.ImageInputPricePerToken * rateMultiplier
		interval.FastOutputPricePerToken = fastPricing.OutputPricePerToken * rateMultiplier
		fastCacheWritePrice, fastCacheWrite1hPrice := cacheCreationDisplayPrices(fastPricing)
		interval.FastCacheWritePricePerToken = fastCacheWritePrice * rateMultiplier
		interval.FastCacheWrite1hPricePerToken = fastCacheWrite1hPrice * rateMultiplier
		interval.FastCacheReadPricePerToken = fastPricing.CacheReadPricePerToken * rateMultiplier
		interval.FastImageOutputPricePerToken = fastPricing.ImageOutputPricePerToken * rateMultiplier
	}
	return interval
}

func fastModeDisplayPricing(pricing *ModelPricing) (*ModelPricing, bool) {
	if !hasFastModeDisplayPricing(pricing) {
		return nil, false
	}
	if multiplier, configured := normalizedFastModeMultiplier(pricing); configured {
		fastPricing := multiplyModelPricing(pricing, multiplier)
		fastPricing.FastModeMultiplier = nil
		return fastPricing, true
	}

	fastPricing := *pricing
	if usePriorityServiceTierPricing(OpenAIFastTierPriority, pricing) {
		if pricing.InputPricePerTokenPriority > 0 {
			fastPricing.InputPricePerToken = pricing.InputPricePerTokenPriority
		}
		if pricing.OutputPricePerTokenPriority > 0 {
			fastPricing.OutputPricePerToken = pricing.OutputPricePerTokenPriority
		}
		if pricing.CacheCreationPricePerTokenPriority > 0 {
			fastPricing.CacheCreationPricePerToken = pricing.CacheCreationPricePerTokenPriority
		}
		if pricing.CacheReadPricePerTokenPriority > 0 {
			fastPricing.CacheReadPricePerToken = pricing.CacheReadPricePerTokenPriority
		}
		return &fastPricing, true
	}

	multiplier := serviceTierCostMultiplier(OpenAIFastTierPriority)
	fastPricing.InputPricePerToken *= multiplier
	fastPricing.ImageInputPricePerToken *= multiplier
	fastPricing.OutputPricePerToken *= multiplier
	fastPricing.CacheCreationPricePerToken *= multiplier
	fastPricing.CacheReadPricePerToken *= multiplier
	fastPricing.ImageOutputPricePerToken *= multiplier
	return &fastPricing, true
}

// resolvedHasFastModeDisplayPricing 同时检查默认价与有效区间，覆盖只有区间单价的自定义模型。
func resolvedHasFastModeDisplayPricing(resolved *ResolvedPricing) bool {
	if resolved == nil || resolved.Mode != BillingModeToken {
		return false
	}
	if hasFastModeDisplayPricing(resolved.tokenPricingForInterval(nil)) {
		return true
	}
	for i := range resolved.Intervals {
		pricing := resolved.tokenPricingForInterval(&resolved.Intervals[i])
		if hasFastModeDisplayPricing(pricing) {
			return true
		}
	}
	return false
}

func hasFastModeDisplayPricing(pricing *ModelPricing) bool {
	return hasAnyDisplayTokenPricing(pricing) &&
		(pricing.FastModeMultiplier != nil ||
			pricing.SupportsServiceTier ||
			pricing.InputPricePerTokenPriority > 0 ||
			pricing.OutputPricePerTokenPriority > 0 ||
			pricing.CacheCreationPricePerTokenPriority > 0 ||
			pricing.CacheReadPricePerTokenPriority > 0)
}

// buildTokenIntervalDisplayPricing 标记此模型需要按上下文区间展示价格。
func buildTokenIntervalDisplayPricing(intervals []ModelDisplayPricingInterval) ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode:      "token",
		PriceStatus:      "priced",
		ContextIntervals: intervals,
	}
}

func buildImageDisplayPricing(price1K, price2K, price4K float64) ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode:  "image",
		PriceStatus:  "priced",
		ImagePrice1K: price1K,
		ImagePrice2K: price2K,
		ImagePrice4K: price4K,
	}
}

func unknownDisplayPricing() ModelDisplayPricing {
	return ModelDisplayPricing{
		PricingMode: "unknown",
		PriceStatus: "unpriced",
	}
}

const (
	defaultImageGenerationPrice = 0.134

	defaultGrokImagineImagePrice1K        = 0.02
	defaultGrokImagineImagePrice2K        = 0.02
	defaultGrokImagineImageQualityPrice1K = 0.05
	defaultGrokImagineImageQualityPrice2K = 0.07
	defaultGrokImagineImage20Price1K      = 0.06 // default quality is Medium
	defaultGrokImagineImage20Price2K      = 0.08

	// 视频默认价为 xAI 官方**每秒**输出价格（USD/s），总价 = 每秒价 × 时长（秒）。
	defaultGrokImagineVideoPrice480P    = 0.05
	defaultGrokImagineVideoPrice720P    = 0.07
	defaultGrokImagineVideo15Price480P  = 0.08
	defaultGrokImagineVideo15Price720P  = 0.14
	defaultGrokImagineVideo15Price1080P = 0.25

	// Codex alpha/search 网页搜索单次默认价：OpenAI 官方 web search 定价 $10/1000 次。
	defaultWebSearchPricePerCall = 0.01

	// xAI 服务端网页/X 搜索与代码执行按每千次 $5 计费。
	defaultSearchPricePer1k = 5.0

	// 通用实时语音默认采用 think-fast-1.0 价格；think-fast-2.0 可通过
	// 分组或渠道逐模型价格独立配置。
	defaultAudioRealtimePricePerMin     = 0.05
	defaultAudioTTSPricePerMillionChars = 15.0
	defaultAudioSTTPricePerHour         = 0.10
)

// CalculateWebSearchCost 计算 Codex alpha/search 网页搜索按次费用。
// callCount: 搜索调用次数（每次请求为 1）
// groupPrice: 分组配置的单次价格（nil 表示使用默认价 0.01；0 表示免费）
// rateMultiplier: 分组费率倍数
func (s *BillingService) CalculateWebSearchCost(callCount int, groupPrice *float64, rateMultiplier float64) *CostBreakdown {
	if callCount <= 0 {
		return &CostBreakdown{}
	}
	unitPrice := defaultWebSearchPricePerCall
	if groupPrice != nil && *groupPrice >= 0 {
		unitPrice = *groupPrice
	}
	totalCost := unitPrice * float64(callCount)

	// 应用倍率（保存时强制 > 0；负数按 0 处理避免按 1x 误扣）
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  totalCost * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// CalculateSearchCost 按每千次调用结算搜索工具；nil 使用默认价，显式 0 表示免费。
func (s *BillingService) CalculateSearchCost(numCalls int, groupPricePer1k *float64, rateMultiplier float64) *CostBreakdown {
	if numCalls <= 0 {
		return &CostBreakdown{}
	}
	pricePer1k := defaultSearchPricePer1k
	if groupPricePer1k != nil {
		if *groupPricePer1k < 0 {
			return &CostBreakdown{}
		}
		pricePer1k = *groupPricePer1k
	}
	if pricePer1k == 0 {
		return &CostBreakdown{}
	}
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	unit := pricePer1k / 1000.0
	total := unit * float64(numCalls)
	return &CostBreakdown{
		TotalCost:   total,
		ActualCost:  total * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

type audioPriceConfig struct {
	RealtimePerMin *float64
	TTSPerMChars   *float64
	STTPerHour     *float64
}

// CalculateAudioCost 分别按分钟、百万字符和小时结算 Realtime、TTS 与 STT；
// 分组价格缺失时使用默认值，显式 0 表示对应模式免费。
func (s *BillingService) CalculateAudioCost(mode string, durationOrUnits float64, groupConfig *audioPriceConfig, rateMultiplier float64) *CostBreakdown {
	if durationOrUnits <= 0 {
		return &CostBreakdown{}
	}
	var unitPrice float64
	switch strings.ToLower(mode) {
	case "realtime":
		unitPrice = defaultAudioRealtimePricePerMin
		if groupConfig != nil && groupConfig.RealtimePerMin != nil {
			unitPrice = *groupConfig.RealtimePerMin
		}
	case "tts":
		unitPrice = defaultAudioTTSPricePerMillionChars
		if groupConfig != nil && groupConfig.TTSPerMChars != nil {
			unitPrice = *groupConfig.TTSPerMChars
		}
	case "stt":
		unitPrice = defaultAudioSTTPricePerHour
		if groupConfig != nil && groupConfig.STTPerHour != nil {
			unitPrice = *groupConfig.STTPerHour
		}
	default:
		return &CostBreakdown{}
	}
	if unitPrice <= 0 {
		return &CostBreakdown{}
	}
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	total := unitPrice * durationOrUnits
	return &CostBreakdown{
		TotalCost:   total,
		ActualCost:  total * rateMultiplier,
		BillingMode: string(BillingModePerRequest),
	}
}

// CalculateImageCost 计算图片生成费用
// model: 请求的模型名称（用于获取 LiteLLM 默认价格）
// imageSize: 图片尺寸 "1K", "2K", "4K"
// imageCount: 生成的图片数量
// rateMultiplier: 费率倍数
func (s *BillingService) CalculateImageCost(model string, imageSize string, imageCount int, rateMultiplier float64) *CostBreakdown {
	if imageCount <= 0 {
		return &CostBreakdown{}
	}
	imageSize = NormalizeImageBillingTierOrDefault(imageSize)

	// 获取单价
	unitPrice := s.getDefaultImagePrice(model, imageSize)

	// 计算总费用
	totalCost := unitPrice * float64(imageCount)

	// 应用倍率（保存时强制 > 0；负数按 0 处理避免按 1x 误扣）
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	actualCost := totalCost * rateMultiplier

	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  actualCost,
		BillingMode: string(BillingModeImage),
	}
}

// CalculateVideoCost 计算视频生成费用（按秒计费，与 xAI 口径一致）。
// model: 请求的模型名称（用于获取默认价格）
// resolution: 视频分辨率 "480p", "720p", "1080p"
// videoCount: 生成的视频数量
// durationSeconds: 单个视频时长（秒），<=0 时按上游默认时长计
// rateMultiplier: 费率倍数
func (s *BillingService) CalculateVideoCost(model string, resolution string, videoCount int, durationSeconds int, rateMultiplier float64) *CostBreakdown {
	if videoCount <= 0 {
		return &CostBreakdown{}
	}
	resolution = NormalizeVideoBillingResolutionOrDefault(resolution)
	durationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)

	perSecondPrice := s.getDefaultVideoPrice(model, resolution)
	totalCost := perSecondPrice * float64(durationSeconds) * float64(videoCount)

	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	actualCost := totalCost * rateMultiplier

	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  actualCost,
		BillingMode: string(BillingModeVideo),
	}
}

// getDefaultImagePrice 获取 LiteLLM 默认图片价格
func (s *BillingService) getDefaultImagePrice(model string, imageSize string) float64 {
	if price, ok := getDefaultGrokImagineImagePrice(model, imageSize); ok {
		return price
	}

	basePrice := 0.0

	// 从 PricingService 获取 output_cost_per_image
	if s.pricingService != nil {
		pricing := s.pricingService.GetModelPricing(model)
		if pricing != nil && pricing.OutputCostPerImage > 0 {
			basePrice = pricing.OutputCostPerImage
		}
	}

	// 如果没有找到价格，使用硬编码默认值（$0.134，来自 gemini-3-pro-image-preview）
	if basePrice <= 0 {
		basePrice = defaultImageGenerationPrice
	}

	// 2K 尺寸 1.5 倍，4K 尺寸翻倍
	if imageSize == "2K" {
		return basePrice * 1.5
	}
	if imageSize == "4K" {
		return basePrice * 2
	}

	return basePrice
}

func (s *BillingService) getRawModelPricing(model string) *LiteLLMModelPricing {
	if s == nil || s.pricingService == nil {
		return nil
	}
	return s.pricingService.GetModelPricing(model)
}

// hasExplicitImageGenerationPricing 仅把明确标记为图片生成的按图价格视为图片计费。
// 部分聊天模型也携带 output_cost_per_image 元数据，不能据此覆盖其 token 定价。
func hasExplicitImageGenerationPricing(pricing *LiteLLMModelPricing) bool {
	return pricing != nil &&
		pricing.OutputCostPerImage > 0 &&
		strings.EqualFold(strings.TrimSpace(pricing.Mode), "image_generation")
}

func hasAnyDisplayTokenPricing(pricing *ModelPricing) bool {
	if pricing == nil {
		return false
	}
	return pricing.InputPricePerToken > 0 ||
		pricing.ImageInputPricePerToken > 0 ||
		pricing.OutputPricePerToken > 0 ||
		pricing.CacheCreationPricePerToken > 0 ||
		pricing.CacheCreation1hPrice > 0 ||
		pricing.CacheReadPricePerToken > 0 ||
		pricing.ImageOutputPricePerToken > 0
}

func looksLikeImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}

	return strings.Contains(model, "-image") ||
		strings.Contains(model, "image-") ||
		strings.Contains(model, "/image") ||
		strings.HasPrefix(model, "imagen-") ||
		strings.Contains(model, "gpt-image") ||
		strings.Contains(model, "dall-e")
}

func (s *BillingService) getDefaultVideoPrice(model string, resolution string) float64 {
	if price, ok := getDefaultGrokImagineVideoPrice(model, resolution); ok {
		return price
	}

	// 内置 LiteLLM 数据没有视频输出价格，暂用历史模型默认价作为每秒单价兜底；
	// 分组视频价格仍可独立覆盖图片价格。
	return s.getDefaultImagePrice(model, ImageBillingSize2K)
}

func getDefaultGrokImagineImagePrice(model string, imageSize string) (float64, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case "grok-imagine-image-2.0":
		return getGrokImagineImageTierPrice(
			imageSize,
			defaultGrokImagineImage20Price1K,
			defaultGrokImagineImage20Price2K,
		), true
	case "grok-imagine-image-quality":
		return getGrokImagineImageTierPrice(
			imageSize,
			defaultGrokImagineImageQualityPrice1K,
			defaultGrokImagineImageQualityPrice2K,
		), true
	case "grok-imagine", "grok-imagine-image", "grok-imagine-edit":
		return getGrokImagineImageTierPrice(
			imageSize,
			defaultGrokImagineImagePrice1K,
			defaultGrokImagineImagePrice2K,
		), true
	default:
		return 0, false
	}
}

func getGrokImagineImageTierPrice(imageSize string, price1K float64, price2K float64) float64 {
	switch NormalizeImageBillingTierOrDefault(imageSize) {
	case ImageBillingSize1K:
		return price1K
	case ImageBillingSize2K, ImageBillingSize4K:
		return price2K
	default:
		return price2K
	}
}

func getDefaultGrokImagineVideoPrice(model string, resolution string) (float64, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "grok-imagine-video-1.5"):
		switch NormalizeVideoBillingResolutionOrDefault(resolution) {
		case VideoBillingResolution480P:
			return defaultGrokImagineVideo15Price480P, true
		case VideoBillingResolution720P:
			return defaultGrokImagineVideo15Price720P, true
		case VideoBillingResolution1080P:
			return defaultGrokImagineVideo15Price1080P, true
		default:
			return defaultGrokImagineVideo15Price480P, true
		}
	case strings.HasPrefix(model, "grok-imagine-video"):
		switch NormalizeVideoBillingResolutionOrDefault(resolution) {
		case VideoBillingResolution480P:
			return defaultGrokImagineVideoPrice480P, true
		case VideoBillingResolution720P, VideoBillingResolution1080P:
			return defaultGrokImagineVideoPrice720P, true
		default:
			return defaultGrokImagineVideoPrice480P, true
		}
	default:
		return 0, false
	}
}
