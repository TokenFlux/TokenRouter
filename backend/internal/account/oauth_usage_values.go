// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	usageview "github.com/TokenFlux/TokenRouter/internal/account/usageview"
	time "time"
)

// WindowStats 窗口期统计
//
// cost: 账号口径费用（COALESCE(account_stats_cost, total_cost) * account_rate_multiplier）
// standard_cost: 标准费用（total_cost，不含倍率）
// user_cost: 用户/API Key 口径费用（actual_cost，受分组倍率影响）
type WindowStats struct {
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	Cost         float64 `json:"cost"`
	StandardCost float64 `json:"standard_cost"`
	UserCost     float64 `json:"user_cost"`
}

// UsageProgress 使用量进度
type UsageProgress struct {
	Utilization      float64      `json:"utilization"`            // 使用率百分比 (0-100+，100表示100%)
	ResetsAt         *time.Time   `json:"resets_at"`              // 重置时间
	RemainingSeconds int          `json:"remaining_seconds"`      // 距重置剩余秒数
	WindowStats      *WindowStats `json:"window_stats,omitempty"` // 窗口期统计（从窗口开始到当前的使用量）
	UsedRequests     int64        `json:"used_requests,omitempty"`
	LimitRequests    int64        `json:"limit_requests,omitempty"`
}

// AntigravityModelQuota Antigravity 单个模型的配额信息
type AntigravityModelQuota struct {
	Utilization int    `json:"utilization"` // 使用率 0-100
	ResetTime   string `json:"reset_time"`  // 重置时间 ISO8601
}

// AntigravityModelDetail Antigravity 单个模型的详细能力信息
type AntigravityModelDetail struct {
	DisplayName        string          `json:"display_name,omitempty"`
	SupportsImages     *bool           `json:"supports_images,omitempty"`
	SupportsThinking   *bool           `json:"supports_thinking,omitempty"`
	ThinkingBudget     *int            `json:"thinking_budget,omitempty"`
	Recommended        *bool           `json:"recommended,omitempty"`
	MaxTokens          *int            `json:"max_tokens,omitempty"`
	MaxOutputTokens    *int            `json:"max_output_tokens,omitempty"`
	SupportedMimeTypes map[string]bool `json:"supported_mime_types,omitempty"`
}

// AICredit 表示 Antigravity 账号的 AI Credits 余额信息。
type AICredit struct {
	CreditType     string  `json:"credit_type,omitempty"`
	Amount         float64 `json:"amount,omitempty"`
	MinimumBalance float64 `json:"minimum_balance,omitempty"`
}

type QoderQuotaProgress struct {
	Total          float64 `json:"total"`
	Used           float64 `json:"used"`
	Remaining      float64 `json:"remaining"`
	Percentage     float64 `json:"percentage"`
	Unit           string  `json:"unit,omitempty"`
	DetailURL      string  `json:"detail_url,omitempty"`
	Cap            float64 `json:"cap,omitempty"`
	Available      bool    `json:"available,omitempty"`
	OrganizationID string  `json:"organization_id,omitempty"`
}

type QoderQuotaInfo struct {
	UserID               string              `json:"user_id,omitempty"`
	UserType             string              `json:"user_type,omitempty"`
	UsageType            string              `json:"usage_type,omitempty"`
	TotalUsagePercentage float64             `json:"total_usage_percentage"`
	IsQuotaExceeded      bool                `json:"is_quota_exceeded"`
	ExpiresAt            *time.Time          `json:"expires_at,omitempty"`
	UpgradeURL           string              `json:"upgrade_url,omitempty"`
	AddCreditsURL        string              `json:"add_credits_url,omitempty"`
	UserQuota            *QoderQuotaProgress `json:"user_quota,omitempty"`
	AddOnQuota           *QoderQuotaProgress `json:"add_on_quota,omitempty"`
	OrgResourcePackage   *QoderQuotaProgress `json:"org_resource_package,omitempty"`
	IsPlanQuotaProrated  bool                `json:"is_plan_quota_prorated,omitempty"`
	LastUpdatedAt        *time.Time          `json:"last_updated_at,omitempty"`
	SnapshotFromAccount  bool                `json:"snapshot_from_account,omitempty"`
}

// UsageInfo 账号使用量信息
type UsageInfo struct {
	Source             string         `json:"source,omitempty"`               // "passive" or "active"
	UpdatedAt          *time.Time     `json:"updated_at,omitempty"`           // 更新时间
	FiveHour           *UsageProgress `json:"five_hour"`                      // 5小时窗口
	SevenDay           *UsageProgress `json:"seven_day,omitempty"`            // 7天窗口
	SevenDaySonnet     *UsageProgress `json:"seven_day_sonnet,omitempty"`     // 7天Sonnet窗口
	SevenDayFable      *UsageProgress `json:"seven_day_fable,omitempty"`      // 7天Fable窗口（响应头 7d_oi）
	GeminiSharedDaily  *UsageProgress `json:"gemini_shared_daily,omitempty"`  // Gemini shared pool RPD (Google One / Code Assist)
	GeminiProDaily     *UsageProgress `json:"gemini_pro_daily,omitempty"`     // Gemini Pro 日配额
	GeminiFlashDaily   *UsageProgress `json:"gemini_flash_daily,omitempty"`   // Gemini Flash 日配额
	GeminiSharedMinute *UsageProgress `json:"gemini_shared_minute,omitempty"` // Gemini shared pool RPM (Google One / Code Assist)
	GeminiProMinute    *UsageProgress `json:"gemini_pro_minute,omitempty"`    // Gemini Pro RPM
	GeminiFlashMinute  *UsageProgress `json:"gemini_flash_minute,omitempty"`  // Gemini Flash RPM

	// QuotaAutoPaused 表示 OpenAI 账号当前因 5h/7d 配额阈值被自动暂停调度。
	QuotaAutoPaused bool `json:"quota_auto_paused"`

	// Antigravity 多模型配额
	AntigravityQuota map[string]*AntigravityModelQuota `json:"antigravity_quota,omitempty"`

	// Grok / xAI 被动额度快照
	GrokRequestQuota       *usageview.QuotaWindow `json:"grok_request_quota,omitempty"`
	GrokTokenQuota         *usageview.QuotaWindow `json:"grok_token_quota,omitempty"`
	GrokRetryAfterSeconds  *int                   `json:"grok_retry_after_seconds,omitempty"`
	GrokEntitlementStatus  string                 `json:"grok_entitlement_status,omitempty"`
	GrokQuotaSnapshotState string                 `json:"grok_quota_snapshot_state,omitempty"`
	GrokLastQuotaProbeAt   string                 `json:"grok_last_quota_probe_at,omitempty"`
	GrokLastHeadersSeenAt  string                 `json:"grok_last_headers_seen_at,omitempty"`
	GrokLastStatusCode     int                    `json:"grok_last_status_code,omitempty"`
	GrokFreeTokenLimit     int64                  `json:"grok_free_token_limit,omitempty"`
	GrokLocalUsage         *WindowStats           `json:"grok_local_usage,omitempty"`
	GrokLocalUsage24h      *WindowStats           `json:"grok_local_usage_24h,omitempty"`
	GrokLocalUsage7d       *WindowStats           `json:"grok_local_usage_7d,omitempty"`
	GrokLocalUsageMonthly  *WindowStats           `json:"grok_local_usage_monthly,omitempty"`
	// ThirtyDay 是官方月度账单窗口，按 used/monthlyLimit 计算百分比。
	ThirtyDay   *UsageProgress            `json:"thirty_day,omitempty"`
	GrokBilling *usageview.BillingSummary `json:"grok_billing,omitempty"`

	// Antigravity 账号级信息
	SubscriptionTier    string `json:"subscription_tier,omitempty"`     // 归一化订阅等级: FREE/PRO/ULTRA/UNKNOWN
	SubscriptionTierRaw string `json:"subscription_tier_raw,omitempty"` // 上游原始订阅等级名称

	// Antigravity 模型详细能力信息（与 antigravity_quota 同 key）
	AntigravityQuotaDetails map[string]*AntigravityModelDetail `json:"antigravity_quota_details,omitempty"`

	// Antigravity AI Credits 余额
	AICredits []AICredit `json:"ai_credits,omitempty"`

	// Qoder 上游账号月度 credits
	QoderQuota *QoderQuotaInfo `json:"qoder_quota,omitempty"`

	// Antigravity 废弃模型转发规则 (old_model_id -> new_model_id)
	ModelForwardingRules map[string]string `json:"model_forwarding_rules,omitempty"`

	// Antigravity 账号是否被上游禁止 (HTTP 403)
	IsForbidden     bool   `json:"is_forbidden,omitempty"`
	ForbiddenReason string `json:"forbidden_reason,omitempty"`
	ForbiddenType   string `json:"forbidden_type,omitempty"` // "validation" / "violation" / "forbidden"
	ValidationURL   string `json:"validation_url,omitempty"` // 验证/申诉链接

	// 状态标记（从 ForbiddenType / HTTP 错误码推导）
	NeedsVerify bool `json:"needs_verify,omitempty"` // 需要人工验证（forbidden_type=validation）
	IsBanned    bool `json:"is_banned,omitempty"`    // 账号被封（forbidden_type=violation）
	NeedsReauth bool `json:"needs_reauth,omitempty"` // token 失效需重新授权（401）

	// 错误码（机器可读）：forbidden / unauthenticated / rate_limited / network_error
	ErrorCode string `json:"error_code,omitempty"`

	// 获取 usage 时的错误信息（降级返回，而非 500）
	Error string `json:"error,omitempty"`
}

// ClaudeUsageWindow Anthropic /api/oauth/usage 返回的单个用量窗口
type ClaudeUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

// ClaudeUsageResponse Anthropic API返回的usage结构
type ClaudeUsageResponse struct {
	FiveHour struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"five_hour"`
	SevenDay struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day"`
	SevenDaySonnet struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day_sonnet"`
	// Fable 专属 7d 窗口（对应响应头 7d_oi，claim 名为 seven_day_overage_included，
	// 见 anthropic-ratelimit-unified-representative-claim 头）。上游 usage API
	// 若不下发该字段，GetUsage 会用被动采样数据回填。
	SevenDayOverageIncluded ClaudeUsageWindow `json:"seven_day_overage_included"`
}
