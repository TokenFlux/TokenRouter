// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

type CreateGroupInput struct {
	RoutingPolicy GroupRoutingPolicy
	Name          string
	Description   string
	Platform      string
	// SchedulerType 为空时使用基础调度器，保持新分组的历史默认行为。
	SchedulerType string
	// AdvancedSchedulerOverrides 未设置字段继承网关通用高级调度设置。
	AdvancedSchedulerOverrides GroupAdvancedSchedulerOverrides
	DisplayBrand               string
	SortOrder                  *int
	RateMultiplier             float64
	IsExclusive                bool
	IsDefault                  bool
	// SessionIsolationEnabled 开启后拒绝其它分组已归属的显式会话切入。
	SessionIsolationEnabled bool
	// LongContextPricingEnabled 为 nil 时默认开启，以兼容未发送新字段的客户端。
	LongContextPricingEnabled *bool
	ModelPricing              []ModelPricingEntry
	// 图片生成权限与批量图片策略，价格统一由模型价卡提供。
	AllowImageGeneration         bool
	AllowBatchImageGeneration    bool
	BatchImageDiscountMultiplier *float64
	BatchImageHoldMultiplier     *float64
	// 高峰时段倍率配置（PeakRateMultiplier 为 nil 时按 1.0 处理）
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier *float64
	// Codex alpha/search 网页搜索单次价格（USD/次，仅 openai 平台使用）；nil/负数按默认价 0.01 处理
	WebSearchPricePerCall *float64
	// 搜索工具每千次单价。
	SearchPricePer1k *float64
	// Grok Voice 显式定价（分组级）
	AudioRealtimePricePerMin     *float64
	AudioTTSPricePerMillionChars *float64
	AudioSTTPricePerHour         *float64
	ClaudeCodeOnly               bool   // 仅允许 Claude Code 客户端
	FallbackGroupID              *int64 // 降级分组 ID
	// 无效请求兜底分组 ID（仅 anthropic 平台使用）
	FallbackGroupIDOnInvalidRequest *int64
	// UnavailableFallbackGroupID 当前分组不可用时 API Key 优先回退到的分组 ID。
	UnavailableFallbackGroupID *int64
	// 模型路由配置（仅 anthropic 平台使用）
	ModelRouting        map[string][]int64
	ModelRoutingEnabled bool // 是否启用模型路由
	MCPXMLInject        *bool
	// 支持的模型系列（仅 antigravity 平台使用）
	SupportedModelScopes []string
	// AllowedProtocols 为 nil 时使用平台默认值；显式空数组对所有平台都合法。
	LegacyProtocolInput  bool
	AllowedProtocols     []protocol.ProtocolID
	ProtocolFallbacks    map[protocol.ProtocolID]protocol.ProtocolID
	ResponsesImagePolicy string
	// AllowMessagesDispatch 仅在 OpenAI 分组且新字段缺省时作为兼容输入。
	AllowMessagesDispatch bool
	AllowLive             bool
	// ForceOpenAIFast 仅对 OpenAI 分组启用组级 Fast 强制策略。
	ForceOpenAIFast bool
	// 新策略优先于旧布尔输入，省略时保持兼容。
	OpenAIFastPolicy *string
	// FreeOpenAIFast 仅对 OpenAI 分组启用 Standard 计费策略。
	FreeOpenAIFast              bool
	DefaultMappedModel          string
	RequireOAuthOnly            bool
	RequirePrivacySet           bool
	MessagesDispatchModelConfig OpenAIMessagesDispatchModelConfig
	ModelsListConfig            GroupModelsListConfig
	// AvailabilityProbeConfig 控制分组主动可用性探测。
	AvailabilityProbeConfig GroupAvailabilityProbeConfig
	// RPMLimit 分组 RPM 上限（0 = 不限制）
	RPMLimit int
	// MaxReasoningEffort OpenAI/Anthropic 请求的推理强度上限，空字符串表示不限制。
	MaxReasoningEffort string
	// MaxReasoningEffortOverLimit 超过上限时的访问控制：downgrade（默认）或 deny。
	MaxReasoningEffortOverLimit string
	// ReasoningEffortMappings OpenAI/Codex 推理强度精确映射。
	ReasoningEffortMappings []ReasoningEffortMapping
	// 从指定分组复制账号（创建分组后在同一事务内绑定）
	CopyAccountsFromGroupIDs []int64
}

type UpdateGroupInput struct {
	RoutingPolicy *GroupRoutingPolicy
	Name          string
	Description   *string
	Platform      string
	// SchedulerType 为 nil 时保留原值。
	SchedulerType *string
	// AdvancedSchedulerOverrides 为 nil 时保留原值；空对象表示清除全部覆盖并恢复继承。
	AdvancedSchedulerOverrides *GroupAdvancedSchedulerOverrides
	DisplayBrand               *string
	SortOrder                  *int
	RateMultiplier             *float64 // 使用指针以支持设置为0
	IsExclusive                *bool
	IsDefault                  *bool
	// SessionIsolationEnabled 控制目标分组是否开启会话隔离。
	SessionIsolationEnabled   *bool
	Status                    string
	LongContextPricingEnabled *bool
	ModelPricing              *[]ModelPricingEntry
	// 图片生成权限与批量图片策略，价格统一由模型价卡提供。
	AllowImageGeneration         *bool
	AllowBatchImageGeneration    *bool
	BatchImageDiscountMultiplier *float64
	BatchImageHoldMultiplier     *float64
	// 高峰时段倍率配置（nil 表示不修改）
	PeakRateEnabled    *bool
	PeakStart          *string
	PeakEnd            *string
	PeakRateMultiplier *float64
	// Codex alpha/search 网页搜索单次价格（USD/次）；nil 表示不修改，负数表示清除回默认价 0.01
	WebSearchPricePerCall *float64
	// 搜索工具单价；nil 不修改，负数清除。
	SearchPricePer1k *float64
	// Grok Voice 显式定价；nil 表示不修改，负数表示清除。
	AudioRealtimePricePerMin     *float64
	AudioTTSPricePerMillionChars *float64
	AudioSTTPricePerHour         *float64
	ClaudeCodeOnly               *bool  // 仅允许 Claude Code 客户端
	FallbackGroupID              *int64 // 降级分组 ID
	// 无效请求兜底分组 ID（仅 anthropic 平台使用）
	FallbackGroupIDOnInvalidRequest *int64
	// UnavailableFallbackGroupID 当前分组不可用时 API Key 优先回退到的分组 ID。
	UnavailableFallbackGroupID *int64
	// 模型路由配置（仅 anthropic 平台使用）
	ModelRouting        map[string][]int64
	ModelRoutingEnabled *bool // 是否启用模型路由
	MCPXMLInject        *bool
	// 支持的模型系列（仅 antigravity 平台使用）
	SupportedModelScopes *[]string
	// AllowedProtocols 为 nil 时保留原值；非 nil 表示显式替换完整集合。
	LegacyProtocolInput  bool
	AllowedProtocols     *[]protocol.ProtocolID
	ProtocolFallbacks    map[protocol.ProtocolID]protocol.ProtocolID
	ResponsesImagePolicy string
	// AllowMessagesDispatch 仅在 OpenAI 分组且新字段缺省时作为兼容输入。
	AllowMessagesDispatch *bool
	AllowLive             *bool
	// ForceOpenAIFast 为 nil 时保留原值；仅对 OpenAI 分组生效。
	ForceOpenAIFast *bool
	// 新策略优先于旧布尔输入，省略时保持兼容。
	OpenAIFastPolicy *string
	// FreeOpenAIFast 为 nil 时保留原值；仅对 OpenAI 分组生效。
	FreeOpenAIFast              *bool
	DefaultMappedModel          *string
	RequireOAuthOnly            *bool
	RequirePrivacySet           *bool
	MessagesDispatchModelConfig *OpenAIMessagesDispatchModelConfig
	ModelsListConfig            *GroupModelsListConfig
	// AvailabilityProbeConfig 为 nil 时不修改探测配置。
	AvailabilityProbeConfig *GroupAvailabilityProbeConfig
	// RPMLimit 分组 RPM 上限（0 = 不限制），nil 表示未提供不改动。
	RPMLimit *int
	// MaxReasoningEffort 空字符串表示清除上限；nil 表示未提供不改动。
	MaxReasoningEffort *string
	// MaxReasoningEffortOverLimit 空字符串视为 downgrade；nil 表示未提供不改动。
	MaxReasoningEffortOverLimit *string
	// ReasoningEffortMappings nil 表示不修改，空数组表示清空，非空数组表示替换。
	ReasoningEffortMappings *[]ReasoningEffortMapping
	// 从指定分组复制账号（同步操作：先清空当前分组的账号绑定，再绑定源分组的账号）
	CopyAccountsFromGroupIDs []int64
}
