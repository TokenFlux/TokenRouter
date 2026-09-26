// Package dto 拥有用量用户/管理员 HTTP 形状，保持原字段与省略语义。
package dto

import (
	"time"

	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

type (
	User             = identitydto.User[APIKey]
	APIKey           = keydto.APIKey[Group]
	Group            = routingdto.Group
	UserSubscription = billinghttp.UserSubscription
)

// UsageLog 是普通用户接口使用的 usage log DTO（不包含管理员字段）。
type UsageLog struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	TeamID    *int64 `json:"team_id,omitempty"`
	APIKeyID  int64  `json:"api_key_id"`
	AccountID int64  `json:"account_id"`
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	// ServiceTier records the OpenAI service tier used for billing, e.g. "priority" / "flex".
	ServiceTier *string `json:"service_tier,omitempty"`
	// ReasoningEffort 是最终转发给上游的推理档位。
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	// RequestedReasoningEffort 是策略与模型映射前客户端请求的推理档位。
	RequestedReasoningEffort *string `json:"requested_reasoning_effort,omitempty"`
	// InboundEndpoint is the client-facing API endpoint path, e.g. /v1/chat/completions.
	InboundEndpoint *string `json:"inbound_endpoint,omitempty"`
	// UpstreamEndpoint is the normalized upstream endpoint path, e.g. /v1/responses.
	UpstreamEndpoint *string `json:"upstream_endpoint,omitempty"`

	GroupID        *int64 `json:"group_id"`
	SubscriptionID *int64 `json:"subscription_id"`

	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`

	CacheCreation5mTokens int `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int `json:"cache_creation_1h_tokens"`

	InputCost             float64                     `json:"input_cost"`
	OutputCost            float64                     `json:"output_cost"`
	CacheCreationCost     float64                     `json:"cache_creation_cost"`
	CacheReadCost         float64                     `json:"cache_read_cost"`
	TotalCost             float64                     `json:"total_cost"`
	ActualCost            float64                     `json:"actual_cost"`
	SubscriptionAmountUSD float64                     `json:"subscription_amount_usd"`
	BalanceAmountUSD      float64                     `json:"balance_amount_usd"`
	BillingAllocations    []billing.BillingAllocation `json:"billing_allocations,omitempty"`
	RateMultiplier        float64                     `json:"rate_multiplier"`
	// LongContextBillingApplied 表示该请求是否实际应用长上下文加价。
	LongContextBillingApplied bool `json:"long_context_billing_applied"`

	BillingType  int8   `json:"billing_type"`
	RequestType  string `json:"request_type"`
	Stream       bool   `json:"stream"`
	OpenAIWSMode bool   `json:"openai_ws_mode"`
	// NativeCompactionV2 表示 OpenAI 原生远程 compaction v2 请求。
	NativeCompactionV2 bool `json:"native_compaction_v2"`
	DurationMs         *int `json:"duration_ms"`
	FirstTokenMs       *int `json:"first_token_ms"`

	// 图片生成字段
	ImageCount         int            `json:"image_count"`
	ImageSize          *string        `json:"image_size"`
	ImageInputSize     *string        `json:"image_input_size"`
	ImageOutputSize    *string        `json:"image_output_size"`
	ImageInputTokens   int            `json:"image_input_tokens"`
	ImageInputCost     float64        `json:"image_input_cost"`
	ImageOutputTokens  int            `json:"image_output_tokens"`
	ImageOutputCost    float64        `json:"image_output_cost"`
	ImageSizeSource    *string        `json:"image_size_source"`
	ImageSizeBreakdown map[string]int `json:"image_size_breakdown"`
	MediaType          *string        `json:"media_type"`

	// User-Agent
	UserAgent *string `json:"user_agent"`
	// IPAddress 对用量记录所有者可见。
	IPAddress *string `json:"ip_address,omitempty"`
	// SessionID 是客户端显式提供的请求关联标识，例如 session_id 或
	// X-Session-Id 请求头；缺失时不返回该字段。
	SessionID *string `json:"session_id,omitempty"`

	// Cache TTL Override 标记
	CacheTTLOverridden bool `json:"cache_ttl_overridden"`

	// BillingMode 计费模式：token/image
	BillingMode *string `json:"billing_mode,omitempty"`

	CreatedAt time.Time `json:"created_at"`

	User         *User             `json:"user,omitempty"`
	APIKey       *APIKey           `json:"api_key,omitempty"`
	Group        *Group            `json:"group,omitempty"`
	Subscription *UserSubscription `json:"subscription,omitempty"`
}

// AdminUsageLog 是管理员接口使用的 usage log DTO（包含管理员字段）。
type AdminUsageLog struct {
	UsageLog

	// DetailedTiming 是从 http.access 系统日志关联出的单请求阶段耗时，仅管理员使用记录可见。
	DetailedTiming *UsageLogTiming `json:"detailed_timing,omitempty"`

	// UpstreamModel is the actual model sent to the upstream provider after mapping.
	// Omitted when no mapping was applied (requested model was used as-is).
	UpstreamModel *string `json:"upstream_model,omitempty"`
	// PricingConfigID 共享价格配置 ID
	PricingConfigID *int64 `json:"pricing_config_id,omitempty"`
	// ModelMappingChain 模型映射链，如 "a→b→c"
	ModelMappingChain *string `json:"model_mapping_chain,omitempty"`
	// UpstreamRequestID 是直接上游声明的请求标识，仅管理端可见。
	UpstreamRequestID *string `json:"upstream_request_id,omitempty"`
	// BillingTier 计费层级标签（per_request/image 模式）
	BillingTier *string `json:"billing_tier,omitempty"`

	// AccountRateMultiplier 账号计费倍率快照（nil 表示按 1.0 处理）
	AccountRateMultiplier *float64 `json:"account_rate_multiplier"`
	// AccountStatsCost 自定义定价规则计算的账号统计费用（nil 表示使用默认公式）
	AccountStatsCost *float64 `json:"account_stats_cost,omitempty"`

	// IPAddress 用户请求 IP
	IPAddress *string `json:"ip_address,omitempty"`

	// Account 最小账号信息（避免泄露敏感字段）
	Account *AccountSummary `json:"account,omitempty"`
}

// UsageLogTiming 展示请求进入网关后各阶段相对于入口的毫秒数。
type UsageLogTiming struct {
	RequestContentLength           *int64 `json:"request_content_length,omitempty"`
	AccountSlotAcquiredMs          *int64 `json:"account_slot_acquired_ms,omitempty"`
	UpstreamGetConnMs              *int64 `json:"upstream_get_conn_ms,omitempty"`
	UpstreamGotConnMs              *int64 `json:"upstream_got_conn_ms,omitempty"`
	UpstreamWroteRequestMs         *int64 `json:"upstream_wrote_request_ms,omitempty"`
	UpstreamFirstResponseByteMs    *int64 `json:"upstream_first_response_byte_ms,omitempty"`
	UpstreamFirstSSEDataMs         *int64 `json:"upstream_first_sse_data_ms,omitempty"`
	FirstVisibleOutputMs           *int64 `json:"first_visible_output_ms,omitempty"`
	FirstDownstreamFlushMs         *int64 `json:"first_downstream_flush_ms,omitempty"`
	UpstreamGetConnCount           *int64 `json:"upstream_get_conn_count,omitempty"`
	UpstreamGotConnCount           *int64 `json:"upstream_got_conn_count,omitempty"`
	UpstreamAttemptCount           *int64 `json:"upstream_attempt_count,omitempty"`
	UpstreamFirstResponseByteCount *int64 `json:"upstream_first_response_byte_count,omitempty"`
	UpstreamConnectionReused       bool   `json:"upstream_connection_reused,omitempty"`
	UpstreamWroteRequestError      bool   `json:"upstream_wrote_request_error,omitempty"`
}
type UsageCleanupFilters struct {
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	UserID      *int64    `json:"user_id,omitempty"`
	APIKeyID    *int64    `json:"api_key_id,omitempty"`
	AccountID   *int64    `json:"account_id,omitempty"`
	GroupID     *int64    `json:"group_id,omitempty"`
	Model       *string   `json:"model,omitempty"`
	RequestType *string   `json:"request_type,omitempty"`
	Stream      *bool     `json:"stream,omitempty"`
	BillingType *int8     `json:"billing_type,omitempty"`
}
type UsageCleanupTask struct {
	ID           int64               `json:"id"`
	Status       string              `json:"status"`
	Filters      UsageCleanupFilters `json:"filters"`
	CreatedBy    int64               `json:"created_by"`
	DeletedRows  int64               `json:"deleted_rows"`
	ErrorMessage *string             `json:"error_message,omitempty"`
	CanceledBy   *int64              `json:"canceled_by,omitempty"`
	CanceledAt   *time.Time          `json:"canceled_at,omitempty"`
	StartedAt    *time.Time          `json:"started_at,omitempty"`
	FinishedAt   *time.Time          `json:"finished_at,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// AccountSummary is a minimal account info for usage log display.
// It intentionally excludes sensitive fields like Credentials, Proxy, etc.
type AccountSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
