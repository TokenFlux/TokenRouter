// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	time "time"

	domain "github.com/TokenFlux/TokenRouter/internal/domain"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type OpenAIMessagesDispatchModelConfig = routing.OpenAIMessagesDispatchModelConfig

type GroupModelsListConfig = routing.GroupModelsListConfig

type GroupAvailabilityProbeConfig = routing.GroupAvailabilityProbeConfig

type ReasoningEffortMapping = routing.ReasoningEffortMapping

type GroupAdvancedSchedulerOverrides = routing.GroupAdvancedSchedulerOverrides

type GroupSchedulerType = routing.GroupSchedulerType

const GroupSchedulerTypeBasic = routing.GroupSchedulerTypeBasic

const GroupSchedulerTypeAdvanced = routing.GroupSchedulerTypeAdvanced

func NormalizeGroupSchedulerType(value string) (GroupSchedulerType, error) {
	return routing.NormalizeGroupSchedulerType(value)
}

type Group struct {
	ID          int64
	Name        string
	Description string
	Platform    string
	// SchedulerType 决定该分组使用基础或高级调度器。
	SchedulerType GroupSchedulerType
	// AdvancedSchedulerOverrides 仅对高级调度分组生效，未设置字段继承网关通用设置。
	AdvancedSchedulerOverrides GroupAdvancedSchedulerOverrides
	DisplayBrand               string
	RateMultiplier             float64
	// 高峰时段倍率：peak_rate_enabled 为 true 且当前时刻处于 [PeakStart, PeakEnd) 时，
	// token 计费倍率额外乘以 PeakRateMultiplier。详见 PeakMultiplierAt。
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	IsExclusive        bool
	IsDefault          bool
	Status             string
	Hydrated           bool // indicates the group was loaded from a trusted repository source
	// DuplicateOperationID 仅用于恢复已提交的一键复制结果，不得映射到 API DTO。
	DuplicateOperationID string

	// SessionIsolationEnabled 表示目标分组是否拒绝其它分组已归属的显式会话切入。
	SessionIsolationEnabled bool

	// 图片生成权限与批量图片策略，价格统一由模型价卡提供。
	AllowImageGeneration         bool
	AllowBatchImageGeneration    bool
	BatchImageDiscountMultiplier float64
	BatchImageHoldMultiplier     float64
	// Codex alpha/search 网页搜索单次价格（USD/次，仅 openai 平台使用）；
	// nil 表示使用默认价 defaultWebSearchPricePerCall（官方 $10/1000 次）。
	WebSearchPricePerCall *float64

	// 搜索工具每千次调用的显式定价。
	SearchPricePer1k *float64
	// Grok Voice 显式定价（分组级，不按文本 RateMultiplier）。
	AudioRealtimePricePerMin     *float64
	AudioTTSPricePerMillionChars *float64
	AudioSTTPricePerHour         *float64

	// ModelPricing 为命中模型覆盖渠道与内置基础价格。
	// LongContextPricingEnabled 仅控制内置长上下文倍率，不改变分组或渠道自定义区间。
	LongContextPricingEnabled bool
	ModelPricing              []ChannelModelPricing

	// Claude Code 客户端限制
	ClaudeCodeOnly  bool
	FallbackGroupID *int64
	// 无效请求兜底分组（仅 anthropic 平台使用）
	FallbackGroupIDOnInvalidRequest *int64
	// UnavailableFallbackGroupID 表示当前分组停用时 API Key 优先回退到的分组。
	UnavailableFallbackGroupID *int64

	// 模型路由配置
	// key: 模型匹配模式（支持 * 通配符，如 "claude-opus-*"）
	// value: 优先账号 ID 列表
	ModelRouting        map[string][]int64
	ModelRoutingEnabled bool

	// MCP XML 协议注入开关（仅 antigravity 平台使用）
	MCPXMLInject bool

	// 支持的模型系列（仅 antigravity 平台使用）
	// 可选值: claude, gemini_text, gemini_image
	SupportedModelScopes []string

	// 分组排序
	SortOrder int

	// AllowedProtocols 是分组允许的完整客户端协议与业务入口集合，空集合表示全部关闭。
	AllowedProtocols     []domain.ProtocolID
	ProtocolFallbacks    map[domain.ProtocolID]domain.ProtocolID
	ResponsesImagePolicy string
	// AllowMessagesDispatch 是从协议集合派生并持久化的弃用兼容镜像。
	AllowMessagesDispatch bool
	AllowLive             bool
	// ForceOpenAIFast 强制 OpenAI 分组请求使用 service_tier=priority。
	ForceOpenAIFast bool
	// OpenAIFastPolicy 保存管理员选择的互斥加速策略。
	OpenAIFastPolicy string
	// FreeOpenAIFast 让 OpenAI 分组的 Fast 请求按 Standard 价格向用户计费。
	FreeOpenAIFast              bool
	RequireOAuthOnly            bool // 仅允许非 apikey 类型账号关联（OpenAI/Antigravity/Anthropic/Gemini）
	RequirePrivacySet           bool // 调度时仅允许 privacy 已成功设置的账号（OpenAI/Antigravity/Anthropic/Gemini）
	DefaultMappedModel          string
	MessagesDispatchModelConfig OpenAIMessagesDispatchModelConfig
	ModelsListConfig            GroupModelsListConfig
	// AvailabilityProbeConfig 控制该分组的主动可用性探测。
	AvailabilityProbeConfig GroupAvailabilityProbeConfig

	// RPMLimit 分组级每分钟请求数上限（0 = 不限制）。
	// 一旦设置即接管该分组用户的限流（覆盖用户级 rpm_limit），可被 user-group rpm_override 进一步覆盖。
	RPMLimit int

	// MaxReasoningEffort 限制实际生效的 OpenAI/Anthropic 推理强度。
	// 空字符串表示不限制；Anthropic 不支持 minimal。
	MaxReasoningEffort string
	// MaxReasoningEffortOverLimit 控制显式推理强度超过上限时降档或拒绝。
	MaxReasoningEffortOverLimit string
	// ReasoningEffortMappings 在应用上限前改写请求中显式指定的值。
	ReasoningEffortMappings []ReasoningEffortMapping

	CreatedAt time.Time
	UpdatedAt time.Time

	AccountGroups           []AccountGroup
	AccountCount            int64
	ActiveAccountCount      int64
	RateLimitedAccountCount int64
}

func (g *Group) UsesAdvancedScheduler() bool {
	return groupRules(g).UsesAdvancedScheduler()
}

func (g *Group) IsActive() bool {
	return groupRules(g).IsActive()
}

func IsGroupContextValid(group *Group) bool {
	return routing.IsGroupContextValid(groupRules(group))
}

func (g *Group) GetRoutingAccountIDs(requestedModel string) []int64 {
	return groupRules(g).GetRoutingAccountIDs(requestedModel)
}

func parseMinutes(hhmm string) (int, bool) {
	return routing.ParseMinutes(hhmm)
}

func (g *Group) PeakMultiplierAt(now time.Time) float64 {
	return groupRules(g).PeakMultiplierAt(now.In(timezone.Location()))
}

func ValidatePeakRateConfig(enabled bool, start, end string, multiplier float64) error {
	return routing.ValidatePeakRateConfig(enabled, start, end, multiplier)
}

func NormalizePeakRateConfig(enabled bool, start, end string, multiplier float64) (bool, string, string, float64) {
	return routing.NormalizePeakRateConfig(enabled, start, end, multiplier)
}

// computePeakAwareMultipliers 把"基础 token 倍率 base"（已含系统/分组/用户级倍率，但不含高峰）
// 拆分为最终 token 倍率与图片按次倍率：图片按次倍率基于 base 现算、不受高峰影响；token 倍率在 base 上叠加高峰因子。
// gateway_service.recordUsageCore 与 openai_gateway_service.RecordUsage 共用此函数，
// 锁死"高峰因子只乘入 token 倍率、图片按次倍率不受影响"这一叠加顺序——任何调换都会被 group_peak_rate_test 覆盖。
func computePeakAwareMultipliers(apiKey *APIKey, base float64, now time.Time) (text, image float64) {
	image = base
	peak := 1.0
	if apiKey != nil && apiKey.Group != nil {
		peak = apiKey.Group.PeakMultiplierAt(now)
	}
	text = base * peak
	return
}

func (g *Group) GetSearchPricePer1k() *float64 {
	return groupRules(g).GetSearchPricePer1k()
}
