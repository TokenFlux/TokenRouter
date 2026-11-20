package provider

import (
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/provider/usageview"
)

// oauth_usage_values.go defines provider usage display values for upstream
// quotas and local request accounting.
// Provider usage displays combine upstream quota observations with local
// request, token, and cost totals. Utilization and countdowns are
// platform-specific estimates, while optional windows and pointers retain
// absent observations for the administration UI; CloneUsageInfo supplies
// independent mutable snapshots for cache and query consumers.

// WindowStats contains local totals for one provider observation window. Cost
// sums COALESCE(provider_stats_cost, total_cost) multiplied by
// COALESCE(provider_rate_multiplier, 1); StandardCost sums total_cost before
// multipliers, and UserCost sums actual_cost charged to the user or API key.
type WindowStats struct {
	Requests int64   `json:"requests"` // local request count
	Tokens   int64   `json:"tokens"`   // local token count
	Cost     float64 `json:"cost"`     // provider attributed cost total
	// cost before billing multipliers
	StandardCost float64 `json:"standard_cost"`
	UserCost     float64 `json:"user_cost"` // user charged cost total
}

// UsageProgress carries utilization, reset timing, and optional local window
// totals.
type UsageProgress struct {
	// percent used; may exceed 100
	Utilization float64 `json:"utilization"`
	// optional quota reset timestamp
	ResetsAt *time.Time `json:"resets_at"`
	// platform countdown in seconds
	RemainingSeconds int `json:"remaining_seconds"`
	// optional local window totals
	WindowStats *WindowStats `json:"window_stats,omitempty"`
	// observed quota request count
	UsedRequests int64 `json:"used_requests,omitempty"`
	// configured quota request limit
	LimitRequests int64 `json:"limit_requests,omitempty"`
}

// AntigravityModelQuota carries displayed utilization, upstream reset text, and
// optional local usage statistics for one model or family quota window.
type AntigravityModelQuota struct {
	// percent used, truncated from remaining fraction
	Utilization int          `json:"utilization"`
	ResetTime   string       `json:"reset_time"`             // upstream model reset text
	WindowStats *WindowStats `json:"window_stats,omitempty"` // optional local window totals
}

// AntigravityQuotaGroup summarizes weekly and 5-hour usage windows for one
// Antigravity model family.
type AntigravityQuotaGroup struct {
	// Weekly and FiveHour hold optional rolling usage windows reported by
	// retrieveUserQuotaSummary or derived from per-model quota observations.
	Weekly   *AntigravityModelQuota `json:"weekly,omitempty"`    // 7-day rolling quota window
	FiveHour *AntigravityModelQuota `json:"five_hour,omitempty"` // 5-hour rolling quota window
}

// AntigravityQuotaSummary groups Antigravity usage windows into Gemini and
// Claude/GPT model families matching the Antigravity desktop Usage view.
type AntigravityQuotaSummary struct {
	Gemini    *AntigravityQuotaGroup `json:"gemini,omitempty"`     // Gemini models quota windows
	ClaudeGPT *AntigravityQuotaGroup `json:"claude_gpt,omitempty"` // Claude and GPT models quota windows
}

// AntigravityModelDetail carries optional capabilities reported by an upstream
// model response.
type AntigravityModelDetail struct {
	// upstream model display name
	DisplayName string `json:"display_name,omitempty"`
	// SupportsImages, SupportsThinking, ThinkingBudget, Recommended, MaxTokens,
	// MaxOutputTokens, and SupportedMimeTypes can borrow model response values
	// through AntigravityQuota.BuildUsageInfo. CloneUsageInfo copies their mutable
	// storage before consumers independently edit cached display observations.
	// optional image capability flag
	SupportsImages *bool `json:"supports_images,omitempty"`
	// optional thinking capability flag
	SupportsThinking *bool `json:"supports_thinking,omitempty"`
	// optional thinking token budget
	ThinkingBudget *int `json:"thinking_budget,omitempty"`
	// optional upstream recommendation flag
	Recommended *bool `json:"recommended,omitempty"`
	// optional total token capacity
	MaxTokens *int `json:"max_tokens,omitempty"`
	// optional output token capacity
	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`
	// supported MIME type flags
	SupportedMimeTypes map[string]bool `json:"supported_mime_types,omitempty"`
}

// AICredit represents one upstream Antigravity credit kind and balance.
type AICredit struct {
	// upstream credit type name
	CreditType string `json:"credit_type,omitempty"`
	// observed upstream credit balance
	Amount float64 `json:"amount,omitempty"`
	// upstream minimum credit balance
	MinimumBalance float64 `json:"minimum_balance,omitempty"`
}

// QoderQuotaProgress describes one personal, add-on, or organization credit
// pool.
type QoderQuotaProgress struct {
	Total float64 `json:"total"` // observed pool credit total
	Used  float64 `json:"used"`  // observed pool credits used
	// observed pool credits remaining
	Remaining float64 `json:"remaining"`
	// observed pool utilization percentage
	Percentage float64 `json:"percentage"`
	// upstream pool unit label
	Unit string `json:"unit,omitempty"`
	// upstream pool details URL
	DetailURL string `json:"detail_url,omitempty"`
	// optional pool capacity value
	Cap float64 `json:"cap,omitempty"`
	// observed pool availability flag
	Available bool `json:"available,omitempty"`
	// owning upstream organization identifier
	OrganizationID string `json:"organization_id,omitempty"`
}

// QoderQuotaInfo combines Qoder account metadata and optional credit-pool
// observations.
type QoderQuotaInfo struct {
	DisplayMode string                `json:"display_mode,omitempty"`       // upstream usage presentation mode
	Enterprise  *QoderEnterpriseUsage `json:"enterprise,omitempty"`         // external enterprise usage destination
	Dedicated   []QoderDedicatedQuota `json:"dedicated_packages,omitempty"` // scoped credit package observations
	// upstream quota user identifier
	UserID string `json:"user_id,omitempty"`
	// upstream account type label
	UserType string `json:"user_type,omitempty"`
	// upstream usage accounting label
	UsageType string `json:"usage_type,omitempty"`
	// combined upstream utilization percentage
	TotalUsagePercentage float64 `json:"total_usage_percentage"`
	// observed quota exhaustion flag
	IsQuotaExceeded bool `json:"is_quota_exceeded"`
	// optional quota cycle expiry
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// upstream plan upgrade URL
	UpgradeURL string `json:"upgrade_url,omitempty"`
	// upstream credit purchase URL
	AddCreditsURL string `json:"add_credits_url,omitempty"`
	// optional personal quota pool
	UserQuota *QoderQuotaProgress `json:"user_quota,omitempty"`
	// optional additional quota pool
	AddOnQuota *QoderQuotaProgress `json:"add_on_quota,omitempty"`
	// optional organization resource pool
	OrgResourcePackage *QoderQuotaProgress `json:"org_resource_package,omitempty"`
	// upstream plan proration flag
	IsPlanQuotaProrated bool `json:"is_plan_quota_prorated,omitempty"`
	// quota snapshot observation time
	LastUpdatedAt *time.Time `json:"last_updated_at,omitempty"`
	// stored provider snapshot indicator
	SnapshotFromProvider bool `json:"snapshot_from_provider,omitempty"`
}

// QoderEnterpriseUsage describes an external usage view without numeric quota.
type QoderEnterpriseUsage struct {
	OpenMode    string `json:"open_mode"`              // upstream navigation mode
	DetailURL   string `json:"detail_url"`             // upstream detail destination
	FallbackURL string `json:"fallback_url,omitempty"` // optional browser fallback destination
}

// QoderQuotaLabel describes a dedicated credit package's upstream usage scope.
type QoderQuotaLabel struct {
	Dimension string `json:"dimension"` // restriction dimension name
	Value     string `json:"value"`     // restriction display value
}

// QoderDedicatedQuota retains restricted credits separately from general pools.
type QoderDedicatedQuota struct {
	QoderQuotaProgress                   // observed dedicated credit amounts
	ID                 string            `json:"id"`                   // upstream package identifier
	Name               string            `json:"name"`                 // upstream package name
	Description        string            `json:"description"`          // upstream scope explanation
	Status             string            `json:"status"`               // upstream package state
	ExpiresAt          *time.Time        `json:"expires_at,omitempty"` // optional package expiration
	Labels             []QoderQuotaLabel `json:"labels,omitempty"`     // upstream scope display labels
}

// UsageInfo carries platform quota observations, local totals, and usage-query
// diagnostics. Callers synchronize mutable sharing or use CloneUsageInfo
// before editing values obtained from caches or shared queries.
type UsageInfo struct {
	// platform observation source label
	Source string `json:"source,omitempty"`
	// UpdatedAt records observation time and is borrowed by BuildUsageInfo.
	// CloneUsageInfo allocates a separate timestamp before independent
	// cache-result edits.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// short upstream quota window
	// FiveHour, SevenDay, SevenDaySonnet, and SevenDayFable are independent
	// observations. Builders differ in when they include windows, and absence
	// leaves the corresponding upstream quota unknown to display consumers.
	FiveHour *UsageProgress `json:"five_hour"`
	// general weekly quota window
	SevenDay *UsageProgress `json:"seven_day,omitempty"`
	// Sonnet weekly quota window
	SevenDaySonnet *UsageProgress `json:"seven_day_sonnet,omitempty"`
	// Fable 7d_oi quota window
	SevenDayFable *UsageProgress `json:"seven_day_fable,omitempty"`
	// combined Gemini daily requests
	// GeminiSharedDaily, GeminiProDaily, GeminiFlashDaily, GeminiSharedMinute,
	// GeminiProMinute, and GeminiFlashMinute separate shared pools from class
	// limits. GetGeminiUsage chooses shared or per-class windows for each
	// duration.
	GeminiSharedDaily *UsageProgress `json:"gemini_shared_daily,omitempty"`
	// Pro class daily requests
	GeminiProDaily *UsageProgress `json:"gemini_pro_daily,omitempty"`
	// Flash class daily requests
	GeminiFlashDaily *UsageProgress `json:"gemini_flash_daily,omitempty"`
	// combined Gemini minute requests
	GeminiSharedMinute *UsageProgress `json:"gemini_shared_minute,omitempty"`
	// Pro class minute requests
	GeminiProMinute *UsageProgress `json:"gemini_pro_minute,omitempty"`
	// Flash class minute requests
	GeminiFlashMinute *UsageProgress `json:"gemini_flash_minute,omitempty"`

	// QuotaAutoPaused reports the OpenAI threshold pause observed by quota
	// handling. Other platform quota fields can remain available alongside this
	// flag.
	QuotaAutoPaused bool `json:"quota_auto_paused"`

	// quota observations by model
	AntigravityQuota map[string]*AntigravityModelQuota `json:"antigravity_quota,omitempty"`
	// grouped Antigravity weekly and 5-hour windows
	AntigravityQuotaSummary *AntigravityQuotaSummary `json:"antigravity_quota_summary,omitempty"`

	// GrokRequestQuota and GrokTokenQuota retain optional passive quota windows;
	// GrokRetryAfterSeconds accompanies those observations, while probe metadata
	// records when and how the account was last queried. CloneUsageInfo copies
	// their mutable values before independent display updates.
	// Grok request quota observation
	GrokRequestQuota *usageview.QuotaWindow `json:"grok_request_quota,omitempty"`
	// Grok token quota observation
	GrokTokenQuota *usageview.QuotaWindow `json:"grok_token_quota,omitempty"`
	// observed retry delay seconds
	GrokRetryAfterSeconds *int `json:"grok_retry_after_seconds,omitempty"`
	// upstream account entitlement label
	GrokEntitlementStatus string `json:"grok_entitlement_status,omitempty"`
	// passive quota snapshot state
	GrokQuotaSnapshotState string `json:"grok_quota_snapshot_state,omitempty"`
	// last active quota probe
	GrokLastQuotaProbeAt string `json:"grok_last_quota_probe_at,omitempty"`
	// last observed quota headers
	GrokLastHeadersSeenAt string `json:"grok_last_headers_seen_at,omitempty"`
	// last observed upstream status
	GrokLastStatusCode int `json:"grok_last_status_code,omitempty"`
	// configured free token allowance
	GrokFreeTokenLimit int64 `json:"grok_free_token_limit,omitempty"`
	// selected local quota totals
	GrokLocalUsage *WindowStats `json:"grok_local_usage,omitempty"`
	// rolling daily local totals
	GrokLocalUsage24h *WindowStats `json:"grok_local_usage_24h,omitempty"`
	// rolling weekly local totals
	GrokLocalUsage7d *WindowStats `json:"grok_local_usage_7d,omitempty"`
	// billing month local totals
	GrokLocalUsageMonthly *WindowStats `json:"grok_local_usage_monthly,omitempty"`
	// ThirtyDay describes the upstream billing month when available. Grok
	// billing handling derives utilization from its used and monthly-limit
	// observations.
	// upstream monthly billing window
	ThirtyDay *UsageProgress `json:"thirty_day,omitempty"`
	// upstream billing account summary
	GrokBilling *usageview.BillingSummary `json:"grok_billing,omitempty"`

	// normalized subscription display tier
	SubscriptionTier string `json:"subscription_tier,omitempty"`
	// unmodified upstream subscription label
	SubscriptionTierRaw string `json:"subscription_tier_raw,omitempty"`

	// AntigravityQuotaDetails uses the same model keys as AntigravityQuota.
	// BuildUsageInfo omits both entries when the model response has no quota.
	// capability observations by model
	AntigravityQuotaDetails map[string]*AntigravityModelDetail `json:"antigravity_quota_details,omitempty"`

	// upstream credit balance entries
	AICredits []AICredit `json:"ai_credits,omitempty"`

	// monthly Qoder account credits
	QoderQuota *QoderQuotaInfo `json:"qoder_quota,omitempty"`

	// Antigravity deprecated-model redirects: old_model_id -> new_model_id.
	// deprecated model redirect mapping
	ModelForwardingRules map[string]string `json:"model_forwarding_rules,omitempty"`

	// observed Antigravity forbidden response
	IsForbidden bool `json:"is_forbidden,omitempty"`
	// upstream forbidden diagnostic text
	ForbiddenReason string `json:"forbidden_reason,omitempty"`
	// validation, violation, or forbidden
	ForbiddenType string `json:"forbidden_type,omitempty"`
	// upstream verification appeal URL
	ValidationURL string `json:"validation_url,omitempty"`

	// NeedsVerify, IsBanned, and NeedsReauth are distinct account recovery
	// signals derived by platform error handling. Verification, violation, and
	// unauthenticated observations can therefore direct different administrator
	// actions.
	// account manual verification required
	NeedsVerify bool `json:"needs_verify,omitempty"`
	// account violation recovery required
	IsBanned bool `json:"is_banned,omitempty"`
	// account authorization renewal required
	NeedsReauth bool `json:"needs_reauth,omitempty"`

	// Machine-readable failure code, such as forbidden, unauthenticated,
	// rate_limited, or network_error.
	// machine-readable usage failure category
	ErrorCode string `json:"error_code,omitempty"`

	Error string `json:"error,omitempty"` // usage query display diagnostic
}

type (
	// ClaudeUsageWindow aliases the upstream scalar utilization and reset
	// representation.
	ClaudeUsageWindow = wire.ClaudeUsageWindow
	// ClaudeUsageResponse aliases the upstream collection of Claude quota windows.
	ClaudeUsageResponse = wire.ClaudeUsageResponse
)
