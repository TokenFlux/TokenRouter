package dto

import (
	"time"

	proxydto "github.com/TokenFlux/TokenRouter/internal/egress/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

// provider.go defines provider administration responses for configuration,
// health, quotas, and loaded relationships.
// Provider administration displays configuration together with stored health
// and scheduling windows. Classifier-selected credential secrets use
// presence flags, and Ollama-managed Extra becomes a separate eligible usage
// view so the browser can inspect configuration without receiving those managed
// storage values. The mapper determines optional fields from platform and
// effective getter rules.
//
// Nil pointers and omitted JSON fields carry different meanings from explicit
// false or zero. Cost, session, RPM, TLS, cache and quota controls appear under
// their own eligibility predicates; some effective defaults are displayed even
// when a neighboring limit is disabled. Loaded relationships are added by the
// full mapper, while parent account fields are enriched separately for
// credential shadows.

// Provider is the redacted administrator view of provider configuration, stored
// health and relationships.
type Provider struct {
	ID                      int64          `json:"id"`                                   // TokenRouter provider identifier
	Name                    string         `json:"name"`                                 // provider display name
	Notes                   *string        `json:"notes"`                                // administrator description text
	Platform                string         `json:"platform"`                             // upstream platform identifier
	Type                    string         `json:"type"`                                 // provider authentication kind
	QoderModelCatalogStatus map[string]any `json:"qoder_model_catalog_status,omitempty"` // saved account model properties
	// Credentials and CredentialsStatus separate retained configuration from known
	// sensitive values. The mapper removes classifier-selected keys and reports
	// configured secret presence through has_<key> flags; unknown keys remain
	// visible.
	// redacted credential configuration values
	Credentials map[string]any `json:"credentials"`
	// configured sensitive-key presence flags
	CredentialsStatus map[string]bool `json:"credentials_status,omitempty"`
	// Extra omits Ollama-managed session, auto-refresh and snapshot keys. Upstream
	// usage configuration is reduced to its permitted display fields before this
	// object reaches JSON encoding.
	// redacted provider configuration values
	Extra map[string]any `json:"extra"`
	// OllamaCloudUsage is populated only when the cloned provider passes usage
	// eligibility. Shared-account observations come from usage resolution before
	// mapping; RuntimePresenter subsequently adds the service's encryption-key
	// configuration flag to this response state.
	// eligible Ollama usage display state
	OllamaCloudUsage *provider.OllamaCloudUsageState `json:"ollama_cloud_usage,omitempty"`
	// selected egress proxy identifier
	ProxyID *int64 `json:"proxy_id"`
	// pre-fallback proxy identifier
	ProxyFallbackOriginID *int64 `json:"proxy_fallback_origin_id"`
	// pre-fallback proxy display name
	ProxyFallbackOriginName *string `json:"proxy_fallback_origin_name,omitempty"`
	// configured provider request capacity
	Concurrency int `json:"concurrency"`
	// scheduler capacity override value
	LoadFactor *int `json:"load_factor,omitempty"`
	Priority   int  `json:"priority"` // scheduler priority value
	// effective provider billing multiplier
	RateMultiplier float64 `json:"rate_multiplier"`
	Status         string  `json:"status"` // stored provider health state
	// stored provider error text
	ErrorMessage string `json:"error_message"`
	// last provider activity timestamp
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *int64     `json:"expires_at"` // Unix seconds
	// pause on expiry
	AutoPauseOnExpired bool `json:"auto_pause_on_expired"`
	// provider creation timestamp
	CreatedAt time.Time `json:"created_at"`
	// provider last-update timestamp
	UpdatedAt time.Time `json:"updated_at"`

	// administrator scheduling eligibility switch
	Schedulable bool `json:"schedulable"`

	// provider limit observation timestamp
	RateLimitedAt *time.Time `json:"rate_limited_at"`
	// provider limit reset timestamp
	RateLimitResetAt *time.Time `json:"rate_limit_reset_at"`
	// upstream overload avoidance deadline
	OverloadUntil *time.Time `json:"overload_until"`

	// temporary scheduling avoidance deadline
	TempUnschedulableUntil *time.Time `json:"temp_unschedulable_until"`
	// temporary scheduling avoidance reason
	TempUnschedulableReason string `json:"temp_unschedulable_reason"`

	// QuotaAutoPaused carries the observed OpenAI threshold-pause flag. The
	// shallow mapper copies its supplied record value while runtime presentation
	// owns any later status enrichment.
	// observed OpenAI quota-pause display
	QuotaAutoPaused bool `json:"quota_auto_paused"`

	// recorded usage window start
	SessionWindowStart *time.Time `json:"session_window_start"`
	// recorded usage window end
	SessionWindowEnd *time.Time `json:"session_window_end"`
	// recorded usage window state
	SessionWindowStatus string `json:"session_window_status"`

	// WindowCostLimit and WindowCostStickyReserve are Anthropic OAuth or Setup
	// Token display controls. The mapper includes each positive getter result
	// independently, so a reserve default can appear with no limit.
	// USD
	WindowCostLimit *float64 `json:"window_cost_limit,omitempty"`
	// USD
	WindowCostStickyReserve *float64 `json:"window_cost_sticky_reserve,omitempty"`

	// MaxSessions and SessionIdleTimeoutMin are Anthropic OAuth or Setup Token
	// controls populated independently from positive getter results. The default
	// idle timeout can appear even when the session limit is disabled.
	// configured concurrent session limit
	MaxSessions *int `json:"max_sessions,omitempty"`
	// minutes
	SessionIdleTimeoutMin *int `json:"session_idle_timeout_minutes,omitempty"`

	// BaseRPM, RPMStrategy and RPMStickyBuffer appear together for eligible
	// Anthropic providers with a positive base RPM. The strategy and sticky buffer
	// come from RuntimeConfig's effective getters.
	// base requests per minute
	BaseRPM *int `json:"base_rpm,omitempty"`
	// request-rate admission strategy label
	RPMStrategy *string `json:"rpm_strategy,omitempty"`
	// additional sticky request capacity
	RPMStickyBuffer *int `json:"rpm_sticky_buffer,omitempty"`
	// UserMsgQueueMode appears for eligible Anthropic providers when RuntimeConfig
	// selects a nonempty mode. Unsupported settings resolve to the global policy
	// and leave this response field absent.
	// effective user-message queue selector
	UserMsgQueueMode *string `json:"user_msg_queue_mode,omitempty"`

	// EnableTLSFingerprint, TLSFingerprintProfileID and TLSFingerprintRouterID are
	// populated for supported provider types. Enablement requires true, while
	// nonzero profile and router selectors are exposed independently of that flag.
	// configured TLS fingerprint enablement
	EnableTLSFingerprint *bool `json:"enable_tls_fingerprint,omitempty"`
	// configured TLS profile selector
	TLSFingerprintProfileID *int64 `json:"tls_fingerprint_profile_id,omitempty"`
	// configured TLS router selector
	TLSFingerprintRouterID *int64 `json:"tls_fingerprint_router_id,omitempty"`

	// OpenAIOAuthClientPolicy is populated for OpenAI OAuth providers from the
	// normalized effective getter. Other provider types leave this optional
	// display field absent.
	// normalized OpenAI client-policy selector
	OpenAIOAuthClientPolicy *string `json:"openai_oauth_client_policy,omitempty"`

	// EnableSessionIDMasking appears when an eligible Anthropic provider enables
	// session-ID masking. The record's masking predicate supplies eligibility
	// before this display flag is set.
	// configured session identifier masking
	EnableSessionIDMasking *bool `json:"session_id_masking_enabled,omitempty"`

	// CacheTTLOverrideEnabled and CacheTTLOverrideTarget appear together when the
	// eligible Anthropic provider enables its cache billing override. The target
	// comes from the effective record getter, including its default.
	// configured cache billing override
	CacheTTLOverrideEnabled *bool `json:"cache_ttl_override_enabled,omitempty"`
	// selected cache billing TTL category
	CacheTTLOverrideTarget *string `json:"cache_ttl_override_target,omitempty"`

	// CustomBaseURLEnabled appears when an eligible Anthropic provider enables
	// relay configuration. CustomBaseURL is additionally populated only when the
	// effective URL getter returns nonempty text.
	// configured relay URL enablement
	CustomBaseURLEnabled *bool `json:"custom_base_url_enabled,omitempty"`
	// configured upstream relay URL
	CustomBaseURL *string `json:"custom_base_url,omitempty"`

	// QuotaLimit, QuotaUsed, QuotaDailyLimit, QuotaDailyUsed, QuotaWeeklyLimit,
	// QuotaWeeklyUsed, QuotaMonthlyLimit and QuotaMonthlyUsed are populated for
	// API Key or Bedrock providers. Each
	// positive limit admits its corresponding usage value into the response.
	// total provider cost limit
	QuotaLimit *float64 `json:"quota_limit,omitempty"`
	// stored total provider consumption
	QuotaUsed *float64 `json:"quota_used,omitempty"`
	// daily provider cost limit
	QuotaDailyLimit *float64 `json:"quota_daily_limit,omitempty"`
	// QuotaDailyUsed displays zero when the record's daily period is expired. This
	// mapper performs no quota storage reset and leaves persisted usage
	// maintenance to its owning use case.
	// displayed daily provider consumption
	QuotaDailyUsed *float64 `json:"quota_daily_used,omitempty"`
	// weekly provider cost limit
	QuotaWeeklyLimit *float64 `json:"quota_weekly_limit,omitempty"`
	// QuotaWeeklyUsed displays zero when the record's weekly period is expired.
	// This mapper performs no quota storage reset and leaves persisted usage
	// maintenance to its owning use case.
	// displayed weekly provider consumption
	QuotaWeeklyUsed *float64 `json:"quota_weekly_used,omitempty"`
	// monthly provider cost limit
	QuotaMonthlyLimit *float64 `json:"quota_monthly_limit,omitempty"`
	// QuotaMonthlyUsed displays zero when the record's monthly period is expired.
	// This mapper performs no quota storage reset and leaves persisted usage
	// maintenance to its owning use case.
	// displayed monthly provider consumption
	QuotaMonthlyUsed *float64 `json:"quota_monthly_used,omitempty"`

	// QuotaDailyResetMode and QuotaDailyResetHour appear for fixed daily resets;
	// QuotaWeeklyResetMode, QuotaWeeklyResetDay and QuotaWeeklyResetHour appear
	// for fixed weekly resets. QuotaMonthlyResetMode, QuotaMonthlyResetDay and
	// QuotaMonthlyResetHour describe monthly scheduling. Any fixed mode admits
	// QuotaResetTimezone.
	// fixed daily reset selector
	QuotaDailyResetMode *string `json:"quota_daily_reset_mode,omitempty"`
	// daily reset clock hour
	QuotaDailyResetHour *int `json:"quota_daily_reset_hour,omitempty"`
	// fixed weekly reset selector
	QuotaWeeklyResetMode *string `json:"quota_weekly_reset_mode,omitempty"`
	// weekly reset weekday selector
	QuotaWeeklyResetDay *int `json:"quota_weekly_reset_day,omitempty"`
	// weekly reset clock hour
	QuotaWeeklyResetHour *int `json:"quota_weekly_reset_hour,omitempty"`
	// fixed monthly reset selector
	QuotaMonthlyResetMode *string `json:"quota_monthly_reset_mode,omitempty"`
	// monthly reset calendar date
	QuotaMonthlyResetDay *int `json:"quota_monthly_reset_day,omitempty"`
	// monthly reset clock hour
	QuotaMonthlyResetHour *int `json:"quota_monthly_reset_hour,omitempty"`
	// quota reset timezone name
	QuotaResetTimezone *string `json:"quota_reset_timezone,omitempty"`
	// stored daily reset timestamp text
	QuotaDailyResetAt *string `json:"quota_daily_reset_at,omitempty"`
	// stored weekly reset timestamp text
	QuotaWeeklyResetAt *string `json:"quota_weekly_reset_at,omitempty"`
	// stored monthly reset timestamp text
	QuotaMonthlyResetAt *string `json:"quota_monthly_reset_at,omitempty"`

	// QuotaNotifyDailyEnabled, QuotaNotifyWeeklyEnabled, QuotaNotifyMonthlyEnabled and
	// QuotaNotifyTotalEnabled appear only when their getters report true. Their
	// QuotaNotifyDailyThreshold, QuotaNotifyWeeklyThreshold,
	// QuotaNotifyMonthlyThreshold and
	// QuotaNotifyTotalThreshold are independently included for positive values,
	// including disabled notification configurations.
	// daily notification enablement flag
	QuotaNotifyDailyEnabled *bool `json:"quota_notify_daily_enabled,omitempty"`
	// daily notification threshold value
	QuotaNotifyDailyThreshold *float64 `json:"quota_notify_daily_threshold,omitempty"`
	// weekly notification enablement flag
	QuotaNotifyWeeklyEnabled *bool `json:"quota_notify_weekly_enabled,omitempty"`
	// weekly notification threshold value
	QuotaNotifyWeeklyThreshold *float64 `json:"quota_notify_weekly_threshold,omitempty"`
	// monthly notification enablement flag
	QuotaNotifyMonthlyEnabled *bool `json:"quota_notify_monthly_enabled,omitempty"`
	// monthly notification threshold value
	QuotaNotifyMonthlyThreshold *float64 `json:"quota_notify_monthly_threshold,omitempty"`
	// total notification enablement flag
	QuotaNotifyTotalEnabled *bool `json:"quota_notify_total_enabled,omitempty"`
	// total notification threshold value
	QuotaNotifyTotalThreshold *float64 `json:"quota_notify_total_threshold,omitempty"`

	// ParentProviderID identifies a credential shadow's parent. RuntimePresenter
	// later resolves matching parents to fill the parent account and privacy
	// display fields; this mapper initially leaves those fields empty.
	// credential parent provider identifier
	ParentProviderID *int64 `json:"parent_provider_id,omitempty"`
	// shadow quota dimension selector
	QuotaDimension string `json:"quota_dimension,omitempty"`

	// ParentEmail, ParentPlanType, ParentPrivacyMode, ParentSubscriptionExpiresAt
	// and ParentChatGPTAccountID are filled together by RuntimePresenter from a
	// successfully loaded parent. Missing parents and failed parent queries leave
	// these display fields unchanged.
	// parent account email hint
	ParentEmail string `json:"parent_email,omitempty"`
	// parent subscription plan label
	ParentPlanType string `json:"parent_plan_type,omitempty"`
	// parent privacy observation mode
	ParentPrivacyMode string `json:"parent_privacy_mode,omitempty"`
	// parent subscription expiry text
	ParentSubscriptionExpiresAt string `json:"parent_subscription_expires_at,omitempty"`
	// parent upstream account identifier
	ParentChatGPTAccountID string `json:"parent_chatgpt_account_id,omitempty"`

	Proxy *Proxy `json:"proxy,omitempty"` // selected proxy display details
	// ProviderGroups and Groups are populated by ProviderFromRecord from loaded
	// associations. ProviderFromRecordShallow supplies GroupIDs but leaves these
	// nested relationship objects absent.
	// provider group association views
	ProviderGroups []ProviderGroup `json:"provider_groups,omitempty"`

	// associated routing group identifiers
	GroupIDs []int64 `json:"group_ids,omitempty"`
	// associated routing group views
	Groups []*Group `json:"groups,omitempty"`
}

// ProviderGroup is a loaded provider-to-routing-group association for
// administrator JSON presentation.
type ProviderGroup struct {
	// associated TokenRouter provider identifier
	ProviderID int64 `json:"provider_id"`
	// associated routing group identifier
	GroupID int64 `json:"group_id"`
	// association creation timestamp
	CreatedAt time.Time `json:"created_at"`

	// Provider is mapped shallowly when a membership includes a provider. This
	// limits association recursion while retaining the provider's redacted
	// configuration display.
	// optional shallow provider view
	Provider *Provider `json:"provider,omitempty"`
	// optional routing group view
	Group *Group `json:"group,omitempty"`
}

// Group reuses the routing administrator DTO for loaded provider associations.
type Group = routingdto.Group

// Proxy reuses the egress administrator DTO for a provider's selected proxy.
type Proxy = proxydto.Proxy
