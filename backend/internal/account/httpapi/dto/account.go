// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	proxydto "github.com/TokenFlux/TokenRouter/internal/egress/httpapi/dto"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	time "time"
)

type Account struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Notes    *string `json:"notes"`
	Platform string  `json:"platform"`
	Type     string  `json:"type"`
	// Credentials 经 RedactCredentials 处理后只含非敏感子键；敏感 token / api_key / 私钥
	// 的存在性通过 CredentialsStatus（has_<key>）暴露，原始值不返回前端。
	Credentials             map[string]any                 `json:"credentials"`
	CredentialsStatus       map[string]bool                `json:"credentials_status,omitempty"`
	Extra                   map[string]any                 `json:"extra"`
	OllamaCloudUsage        *account.OllamaCloudUsageState `json:"ollama_cloud_usage,omitempty"`
	ProxyID                 *int64                         `json:"proxy_id"`
	ProxyFallbackOriginID   *int64                         `json:"proxy_fallback_origin_id"`
	ProxyFallbackOriginName *string                        `json:"proxy_fallback_origin_name,omitempty"`
	Concurrency             int                            `json:"concurrency"`
	LoadFactor              *int                           `json:"load_factor,omitempty"`
	Priority                int                            `json:"priority"`
	RateMultiplier          float64                        `json:"rate_multiplier"`
	Status                  string                         `json:"status"`
	ErrorMessage            string                         `json:"error_message"`
	LastUsedAt              *time.Time                     `json:"last_used_at"`
	ExpiresAt               *int64                         `json:"expires_at"`
	AutoPauseOnExpired      bool                           `json:"auto_pause_on_expired"`
	CreatedAt               time.Time                      `json:"created_at"`
	UpdatedAt               time.Time                      `json:"updated_at"`

	Schedulable bool `json:"schedulable"`

	RateLimitedAt    *time.Time `json:"rate_limited_at"`
	RateLimitResetAt *time.Time `json:"rate_limit_reset_at"`
	OverloadUntil    *time.Time `json:"overload_until"`

	TempUnschedulableUntil  *time.Time `json:"temp_unschedulable_until"`
	TempUnschedulableReason string     `json:"temp_unschedulable_reason"`

	// QuotaAutoPaused 表示 OpenAI 账号当前因 5h/7d 配额阈值被自动暂停调度。
	QuotaAutoPaused bool `json:"quota_auto_paused"`

	SessionWindowStart  *time.Time `json:"session_window_start"`
	SessionWindowEnd    *time.Time `json:"session_window_end"`
	SessionWindowStatus string     `json:"session_window_status"`

	// 5h窗口费用控制（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	WindowCostLimit         *float64 `json:"window_cost_limit,omitempty"`
	WindowCostStickyReserve *float64 `json:"window_cost_sticky_reserve,omitempty"`

	// 会话数量控制（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	MaxSessions           *int `json:"max_sessions,omitempty"`
	SessionIdleTimeoutMin *int `json:"session_idle_timeout_minutes,omitempty"`

	// RPM 限制（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	BaseRPM          *int    `json:"base_rpm,omitempty"`
	RPMStrategy      *string `json:"rpm_strategy,omitempty"`
	RPMStickyBuffer  *int    `json:"rpm_sticky_buffer,omitempty"`
	UserMsgQueueMode *string `json:"user_msg_queue_mode,omitempty"`

	// TLS指纹伪装（仅 Anthropic OAuth/SetupToken 账号有效）
	// 从 extra 字段提取，方便前端显示和编辑
	EnableTLSFingerprint    *bool  `json:"enable_tls_fingerprint,omitempty"`
	TLSFingerprintProfileID *int64 `json:"tls_fingerprint_profile_id,omitempty"`
	TLSFingerprintRouterID  *int64 `json:"tls_fingerprint_router_id,omitempty"`

	// OpenAI OAuth 客户端访问策略。
	OpenAIOAuthClientPolicy *string `json:"openai_oauth_client_policy,omitempty"`

	// 会话ID伪装（仅 Anthropic OAuth/SetupToken 账号有效）
	// 启用后将在15分钟内固定 metadata.user_id 中的 session ID
	// 从 extra 字段提取，方便前端显示和编辑
	EnableSessionIDMasking *bool `json:"session_id_masking_enabled,omitempty"`

	// 缓存 TTL 强制替换（仅 Anthropic OAuth/SetupToken 账号有效）
	// 启用后将所有 cache creation tokens 归入指定的 TTL 类型计费
	CacheTTLOverrideEnabled *bool   `json:"cache_ttl_override_enabled,omitempty"`
	CacheTTLOverrideTarget  *string `json:"cache_ttl_override_target,omitempty"`

	// 自定义 Base URL 中继转发（仅 Anthropic OAuth/SetupToken 账号有效）
	CustomBaseURLEnabled *bool   `json:"custom_base_url_enabled,omitempty"`
	CustomBaseURL        *string `json:"custom_base_url,omitempty"`

	// API Key 账号配额限制
	QuotaLimit       *float64 `json:"quota_limit,omitempty"`
	QuotaUsed        *float64 `json:"quota_used,omitempty"`
	QuotaDailyLimit  *float64 `json:"quota_daily_limit,omitempty"`
	QuotaDailyUsed   *float64 `json:"quota_daily_used,omitempty"`
	QuotaWeeklyLimit *float64 `json:"quota_weekly_limit,omitempty"`
	QuotaWeeklyUsed  *float64 `json:"quota_weekly_used,omitempty"`

	// 配额固定时间重置配置
	QuotaDailyResetMode  *string `json:"quota_daily_reset_mode,omitempty"`
	QuotaDailyResetHour  *int    `json:"quota_daily_reset_hour,omitempty"`
	QuotaWeeklyResetMode *string `json:"quota_weekly_reset_mode,omitempty"`
	QuotaWeeklyResetDay  *int    `json:"quota_weekly_reset_day,omitempty"`
	QuotaWeeklyResetHour *int    `json:"quota_weekly_reset_hour,omitempty"`
	QuotaResetTimezone   *string `json:"quota_reset_timezone,omitempty"`
	QuotaDailyResetAt    *string `json:"quota_daily_reset_at,omitempty"`
	QuotaWeeklyResetAt   *string `json:"quota_weekly_reset_at,omitempty"`

	// 配额通知配置
	QuotaNotifyDailyEnabled    *bool    `json:"quota_notify_daily_enabled,omitempty"`
	QuotaNotifyDailyThreshold  *float64 `json:"quota_notify_daily_threshold,omitempty"`
	QuotaNotifyWeeklyEnabled   *bool    `json:"quota_notify_weekly_enabled,omitempty"`
	QuotaNotifyWeeklyThreshold *float64 `json:"quota_notify_weekly_threshold,omitempty"`
	QuotaNotifyTotalEnabled    *bool    `json:"quota_notify_total_enabled,omitempty"`
	QuotaNotifyTotalThreshold  *float64 `json:"quota_notify_total_threshold,omitempty"`

	// 影子账号关系（spark 维度影子）
	ParentAccountID *int64 `json:"parent_account_id,omitempty"`
	QuotaDimension  string `json:"quota_dimension,omitempty"`

	// 影子账号回填的母账号信息（仅影子非空，源自母账号 Credentials/Extra）
	ParentEmail                 string `json:"parent_email,omitempty"`
	ParentPlanType              string `json:"parent_plan_type,omitempty"`
	ParentPrivacyMode           string `json:"parent_privacy_mode,omitempty"`
	ParentSubscriptionExpiresAt string `json:"parent_subscription_expires_at,omitempty"`
	ParentChatGPTAccountID      string `json:"parent_chatgpt_account_id,omitempty"`

	Proxy         *Proxy         `json:"proxy,omitempty"`
	AccountGroups []AccountGroup `json:"account_groups,omitempty"`

	GroupIDs []int64  `json:"group_ids,omitempty"`
	Groups   []*Group `json:"groups,omitempty"`
}

type AccountGroup struct {
	AccountID int64     `json:"account_id"`
	GroupID   int64     `json:"group_id"`
	CreatedAt time.Time `json:"created_at"`

	Account *Account `json:"account,omitempty"`
	Group   *Group   `json:"group,omitempty"`
}

type Group = routingdto.Group

type Proxy = proxydto.Proxy
