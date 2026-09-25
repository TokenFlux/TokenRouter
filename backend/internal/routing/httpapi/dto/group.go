// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

type Group struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Platform       string         `json:"platform"`
	DisplayBrand   string         `json:"display_brand"`
	RateMultiplier float64        `json:"rate_multiplier"`
	Capacity       *GroupCapacity `json:"capacity,omitempty"`
	IsExclusive    bool           `json:"is_exclusive"`
	IsDefault      bool           `json:"is_default"`
	Status         string         `json:"status"`
	// 会话隔离开启后，目标分组会拒绝其它分组已归属的显式会话切入。
	SessionIsolationEnabled   bool `json:"session_isolation_enabled"`
	LongContextPricingEnabled bool `json:"long_context_pricing_enabled"`

	// 图片生成权限与批量图片策略，价格统一由模型价卡提供。
	AllowImageGeneration         bool    `json:"-"`
	AllowBatchImageGeneration    bool    `json:"-"`
	BatchImageDiscountMultiplier float64 `json:"batch_image_discount_multiplier"`
	BatchImageHoldMultiplier     float64 `json:"batch_image_hold_multiplier"`
	// 高峰时段倍率配置
	PeakRateEnabled    bool    `json:"peak_rate_enabled"`
	PeakStart          string  `json:"peak_start"`
	PeakEnd            string  `json:"peak_end"`
	PeakRateMultiplier float64 `json:"peak_rate_multiplier"`
	// Codex alpha/search 网页搜索单次价格（USD/次）；null 表示使用默认价 0.01
	WebSearchPricePerCall        *float64 `json:"web_search_price_per_call"`
	SearchPricePer1k             *float64 `json:"search_price_per_1k"`
	AudioRealtimePricePerMin     *float64 `json:"audio_realtime_price_per_min"`
	AudioTtsPricePerMillionChars *float64 `json:"audio_tts_price_per_million_chars"`
	AudioSttPricePerHour         *float64 `json:"audio_stt_price_per_hour"`

	// Claude Code 客户端限制
	ClaudeCodeOnly  bool   `json:"claude_code_only"`
	FallbackGroupID *int64 `json:"fallback_group_id"`
	// 无效请求兜底分组
	FallbackGroupIDOnInvalidRequest *int64 `json:"fallback_group_id_on_invalid_request"`
	// 当前分组不可用时 API Key 优先回退到的分组。
	UnavailableFallbackGroupID *int64 `json:"unavailable_fallback_group_id"`

	// AllowedProtocols 是分组允许的完整客户端协议与业务入口集合。
	AllowedProtocols     []protocol.ProtocolID                       `json:"allowed_protocols"`
	ProtocolFallbacks    map[protocol.ProtocolID]protocol.ProtocolID `json:"protocol_fallbacks"`
	ResponsesImagePolicy string                                      `json:"responses_image_policy"`
	// AllowMessagesDispatch 是从协议集合派生的弃用兼容字段。
	AllowMessagesDispatch bool `json:"-"`
	// OpenAI Live 接口开关
	AllowLive bool `json:"-"`

	// 账号过滤控制（仅 OpenAI/Antigravity 平台有效）
	RequireOAuthOnly  bool `json:"require_oauth_only"`
	RequirePrivacySet bool `json:"require_privacy_set"`

	// RPMLimit 分组级每分钟请求数上限（0 = 不限制），设置后覆盖用户级 rpm_limit。
	RPMLimit int `json:"rpm_limit"`
	// MaxReasoningEffort OpenAI/Codex 请求的推理强度上限，空字符串表示不限制。
	MaxReasoningEffort string `json:"max_reasoning_effort"`
	// MaxReasoningEffortOverLimit 超过上限时的访问控制：downgrade（默认）或 deny。
	MaxReasoningEffortOverLimit string `json:"max_reasoning_effort_over_limit"`
	// ReasoningEffortMappings OpenAI/Codex 推理强度映射，可按模型精确名、前缀或后缀限定。
	ReasoningEffortMappings []routing.ReasoningEffortMapping `json:"reasoning_effort_mappings"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type GroupCapacity struct {
	ConcurrencyUsed int `json:"concurrency_used"`
	ConcurrencyMax  int `json:"concurrency_max"`
	SessionsUsed    int `json:"sessions_used"`
	SessionsMax     int `json:"sessions_max"`
	RPMUsed         int `json:"rpm_used"`
	RPMMax          int `json:"rpm_max"`
}

// AdminGroup 是管理员接口使用的 group DTO（包含敏感/内部字段）。
// 注意：普通用户接口不得返回 model_routing/account_count/account_groups 等内部信息。
type AdminGroup[A any] struct {
	Group
	// ForceOpenAIFast 仅管理端可见，用于控制 OpenAI 分组的 Fast 策略。
	ForceOpenAIFast bool `json:"force_openai_fast"`
	// OpenAIFastPolicy 保存管理员选择的互斥加速策略。
	OpenAIFastPolicy string `json:"openai_fast_policy"`
	// FreeOpenAIFast 仅管理端可见，用于控制 OpenAI 分组的 Fast 计费。
	FreeOpenAIFast bool `json:"free_openai_fast"`
	// SchedulerType 仅管理端可见，用于配置分组调度器。
	SchedulerType string `json:"scheduler_type"`
	// AdvancedSchedulerOverrides 仅管理端可见；空字段继承网关通用设置。
	AdvancedSchedulerOverrides routing.GroupAdvancedSchedulerOverrides `json:"advanced_scheduler_overrides"`
	// ModelPricing 是分组覆盖渠道与内置价格的管理员价卡。
	ModelPricing []routing.ChannelModelPricing `json:"model_pricing"`

	// 模型路由配置（仅 anthropic 平台使用）
	ModelRouting        map[string][]int64 `json:"model_routing"`
	ModelRoutingEnabled bool               `json:"model_routing_enabled"`

	// MCP XML 协议注入（仅 antigravity 平台使用）
	MCPXMLInject bool `json:"mcp_xml_inject"`

	// OpenAI Messages 调度配置（仅 openai 平台使用）
	DefaultMappedModel          string                                    `json:"default_mapped_model"`
	MessagesDispatchModelConfig routing.OpenAIMessagesDispatchModelConfig `json:"messages_dispatch_model_config"`
	ModelsListConfig            routing.GroupModelsListConfig             `json:"models_list_config"`
	// AvailabilityProbeConfig 控制分组主动可用性探测，仅管理员接口返回。
	AvailabilityProbeConfig routing.GroupAvailabilityProbeConfig `json:"availability_probe_config"`

	// 支持的模型系列（仅 antigravity 平台使用）
	SupportedModelScopes    []string `json:"supported_model_scopes"`
	AccountGroups           []A      `json:"account_groups,omitempty"`
	AccountCount            int64    `json:"account_count,omitempty"`
	ActiveAccountCount      int64    `json:"active_account_count,omitempty"`
	RateLimitedAccountCount int64    `json:"rate_limited_account_count,omitempty"`

	// 分组排序
	SortOrder int `json:"sort_order"`
}
