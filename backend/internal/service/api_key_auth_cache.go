package service

import "time"

// APIKeyAuthSnapshot API Key 认证缓存快照（仅包含认证所需字段）
type APIKeyAuthSnapshot struct {
	Version  int    `json:"version"`
	APIKeyID int64  `json:"api_key_id"`
	UserID   int64  `json:"user_id"`
	TeamID   *int64 `json:"team_id,omitempty"`
	// TeamOwnerDisabled 表示团队 Owner 的独立锁定状态。
	TeamOwnerDisabled bool                               `json:"team_owner_disabled"`
	CreatedAt         time.Time                          `json:"created_at"`
	GroupID           *int64                             `json:"group_id,omitempty"`
	IsComposite       bool                               `json:"is_composite"`
	CompositeGroups   []APIKeyAuthCompositeGroupSnapshot `json:"composite_groups,omitempty"`
	Name              string                             `json:"name"`
	Status            string                             `json:"status"`
	// FastModePolicy 随鉴权快照下发，供网关热路径读取。
	FastModePolicy string `json:"fast_mode_policy"`
	// BillingMode 与指定订阅必须随鉴权快照下发，避免请求期回读 API Key。
	BillingMode             string `json:"billing_mode"`
	PreferredSubscriptionID *int64 `json:"preferred_subscription_id,omitempty"`
	// ModelMapping 随鉴权快照下发，避免请求期查询数据库。
	ModelMapping   map[string]string        `json:"model_mapping,omitempty"`
	IPWhitelist    []string                 `json:"ip_whitelist,omitempty"`
	IPBlacklist    []string                 `json:"ip_blacklist,omitempty"`
	User           APIKeyAuthUserSnapshot   `json:"user"`
	ActorUser      *APIKeyAuthActorSnapshot `json:"actor_user,omitempty"`
	Team           *APIKeyAuthTeamSnapshot  `json:"team,omitempty"`
	TeamMembership *TeamMembership          `json:"team_membership,omitempty"`
	Group          *APIKeyAuthGroupSnapshot `json:"group,omitempty"`

	// Quota fields for API Key independent quota feature
	Quota     float64 `json:"quota"`      // Quota limit in USD (0 = unlimited)
	QuotaUsed float64 `json:"quota_used"` // Used quota amount

	// Expiration field for API Key expiration feature
	ExpiresAt *time.Time `json:"expires_at,omitempty"` // Expiration time (nil = never expires)

	// Rate limit configuration (only limits, not usage - usage read from Redis at check time)
	RateLimit5h float64 `json:"rate_limit_5h"`
	RateLimit1d float64 `json:"rate_limit_1d"`
	RateLimit7d float64 `json:"rate_limit_7d"`
	// FallbackToDefaultGroupWhenUnavailable 控制停用分组请求级回退。
	FallbackToDefaultGroupWhenUnavailable bool `json:"fallback_to_default_group_when_unavailable"`
}

// APIKeyAuthCompositeGroupSnapshot 缓存一个复合前缀对应的完整鉴权分组。
type APIKeyAuthCompositeGroupSnapshot struct {
	ID                       int64                    `json:"id"`
	GroupID                  int64                    `json:"group_id"`
	Prefix                   string                   `json:"prefix"`
	NormalizedPrefix         string                   `json:"normalized_prefix"`
	SortOrder                int                      `json:"sort_order"`
	DataSharingNoticeVersion int                      `json:"data_sharing_notice_version"`
	DataSharingConfirmedAt   *time.Time               `json:"data_sharing_confirmed_at,omitempty"`
	UserGroupRPMOverride     *int                     `json:"user_group_rpm_override,omitempty"`
	Group                    *APIKeyAuthGroupSnapshot `json:"group,omitempty"`
}

// APIKeyAuthActorSnapshot 只缓存验证成员账号状态所需的字段。
type APIKeyAuthActorSnapshot struct {
	ID       int64  `json:"id"`
	Status   string `json:"status"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

// APIKeyAuthTeamSnapshot 缓存团队运行状态，避免每次请求读取团队表。
type APIKeyAuthTeamSnapshot struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// APIKeyAuthUserSnapshot 用户快照
type APIKeyAuthUserSnapshot struct {
	ID            int64   `json:"id"`
	Status        string  `json:"status"`
	Role          string  `json:"role"`
	Balance       float64 `json:"balance"`
	Concurrency   int     `json:"concurrency"`
	AllowedGroups []int64 `json:"allowed_groups,omitempty"`

	// Balance notification fields (required for CheckBalanceAfterDeduction)
	Email                      string             `json:"email"`
	Username                   string             `json:"username"`
	BalanceNotifyEnabled       bool               `json:"balance_notify_enabled"`
	BalanceNotifyThresholdType string             `json:"balance_notify_threshold_type"`
	BalanceNotifyThreshold     *float64           `json:"balance_notify_threshold,omitempty"`
	BalanceNotifyExtraEmails   []NotifyEmailEntry `json:"balance_notify_extra_emails,omitempty"`
	TotalRecharged             float64            `json:"total_recharged"`

	// RPMLimit 用户级每分钟请求数上限（0 = 不限制）；用于 billing_cache_service.checkRPM 兜底判断。
	RPMLimit int `json:"rpm_limit"`

	// UserGroupRPMOverride 该 API Key 对应的 (user, group) 专属 RPM 覆盖值。
	// nil = 无 override（回退到 group/user 级）；0 = 不限流；>0 = 专属上限。
	UserGroupRPMOverride *int `json:"user_group_rpm_override,omitempty"`

	// DisabledPublicGroups 记录该用户被禁止使用的公开分组 ID，用于认证热路径拦截已有 Key。
	DisabledPublicGroups []int64 `json:"disabled_public_groups,omitempty"`
}

// APIKeyAuthGroupSnapshot 分组快照
type APIKeyAuthGroupSnapshot struct {
	ID            int64              `json:"id"`
	Name          string             `json:"name"`
	Platform      string             `json:"platform"`
	SchedulerType GroupSchedulerType `json:"scheduler_type"`
	// AdvancedSchedulerOverrides 随认证快照下发，避免请求期回读分组配置。
	AdvancedSchedulerOverrides      GroupAdvancedSchedulerOverrides `json:"advanced_scheduler_overrides"`
	IsExclusive                     bool                            `json:"is_exclusive"`
	Status                          string                          `json:"status"`
	RateMultiplier                  float64                         `json:"rate_multiplier"`
	DataSharingEnabled              bool                            `json:"data_sharing_enabled"`
	SessionIsolationEnabled         bool                            `json:"session_isolation_enabled"`
	AllowImageGeneration            bool                            `json:"allow_image_generation"`
	AllowBatchImageGeneration       bool                            `json:"allow_batch_image_generation"`
	ImageRateIndependent            bool                            `json:"image_rate_independent"`
	ImageRateMultiplier             float64                         `json:"image_rate_multiplier"`
	ImagePrice1K                    *float64                        `json:"image_price_1k,omitempty"`
	ImagePrice2K                    *float64                        `json:"image_price_2k,omitempty"`
	ImagePrice4K                    *float64                        `json:"image_price_4k,omitempty"`
	VideoRateIndependent            bool                            `json:"video_rate_independent"`
	VideoRateMultiplier             float64                         `json:"video_rate_multiplier"`
	VideoPrice480P                  *float64                        `json:"video_price_480p,omitempty"`
	VideoPrice720P                  *float64                        `json:"video_price_720p,omitempty"`
	VideoPrice1080P                 *float64                        `json:"video_price_1080p,omitempty"`
	VideoModelPrices                map[string]map[string]float64   `json:"video_model_prices,omitempty"`
	WebSearchPricePerCall           *float64                        `json:"web_search_price_per_call,omitempty"`
	SearchPricePer1k                *float64                        `json:"search_price_per_1k,omitempty"`
	AudioRealtimePricePerMin        *float64                        `json:"audio_realtime_price_per_min,omitempty"`
	AudioTTSPricePerMillionChars    *float64                        `json:"audio_tts_price_per_million_chars,omitempty"`
	AudioSTTPricePerHour            *float64                        `json:"audio_stt_price_per_hour,omitempty"`
	LongContextPricingEnabled       bool                            `json:"long_context_pricing_enabled"`
	ModelPricing                    []ChannelModelPricing           `json:"model_pricing,omitempty"`
	ClaudeCodeOnly                  bool                            `json:"claude_code_only"`
	FallbackGroupID                 *int64                          `json:"fallback_group_id,omitempty"`
	FallbackGroupIDOnInvalidRequest *int64                          `json:"fallback_group_id_on_invalid_request,omitempty"`
	UnavailableFallbackGroupID      *int64                          `json:"unavailable_fallback_group_id,omitempty"`

	// Model routing is used by gateway account selection, so it must be part of auth cache snapshot.
	// Only anthropic groups use these fields; others may leave them empty.
	ModelRouting        map[string][]int64 `json:"model_routing,omitempty"`
	ModelRoutingEnabled bool               `json:"model_routing_enabled"`
	MCPXMLInject        bool               `json:"mcp_xml_inject"`

	// 支持的模型系列（仅 antigravity 平台使用）
	SupportedModelScopes []string `json:"supported_model_scopes,omitempty"`

	// AllowedClientProtocols 不使用 omitempty，确保空集合按 [] 写入快照。
	AllowedClientProtocols      []GroupClientProtocol             `json:"allowed_client_protocols"`
	AllowLive                   bool                              `json:"allow_live"`
	DefaultMappedModel          string                            `json:"default_mapped_model,omitempty"`
	MessagesDispatchModelConfig OpenAIMessagesDispatchModelConfig `json:"messages_dispatch_model_config,omitempty"`
	ModelsListConfig            GroupModelsListConfig             `json:"models_list_config,omitempty"`

	// RPMLimit 分组级每分钟请求数上限（0 = 不限制）；用于 billing_cache_service.checkRPM 级联判断。
	RPMLimit int `json:"rpm_limit"`

	// MaxReasoningEffort OpenAI/Codex 请求的推理强度上限，空字符串表示不限制。
	MaxReasoningEffort string `json:"max_reasoning_effort,omitempty"`
	// ReasoningEffortMappings 在应用上限前改写显式的推理强度值。
	ReasoningEffortMappings []ReasoningEffortMapping `json:"reasoning_effort_mappings"`

	// 高峰时段倍率：PeakRateEnabled 为 true 且请求时刻处于 [PeakStart, PeakEnd) 时，
	// token 计费倍率额外乘以 PeakRateMultiplier（详见 Group.PeakMultiplierAt）。
	// 必须随快照缓存，否则扣费路径拿到的 apiKey.Group 缺字段、高峰倍率失效。
	PeakRateEnabled    bool    `json:"peak_rate_enabled"`
	PeakStart          string  `json:"peak_start"`
	PeakEnd            string  `json:"peak_end"`
	PeakRateMultiplier float64 `json:"peak_rate_multiplier"`
}

// APIKeyAuthCacheEntry 缓存条目，支持负缓存
type APIKeyAuthCacheEntry struct {
	NotFound bool                `json:"not_found"`
	Snapshot *APIKeyAuthSnapshot `json:"snapshot,omitempty"`
}
