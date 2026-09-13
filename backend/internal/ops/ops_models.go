package ops

import (
	"encoding/json"
	"strings"
	"time"
)

type OpsSystemLog struct {
	ID              int64          `json:"id"`
	CreatedAt       time.Time      `json:"created_at"`
	Host            string         `json:"host"`
	Level           string         `json:"level"`
	Component       string         `json:"component"`
	Message         string         `json:"message"`
	RequestID       string         `json:"request_id"`
	ClientRequestID string         `json:"client_request_id"`
	UserID          *int64         `json:"user_id"`
	APIKeyID        *int64         `json:"api_key_id"`
	AccountID       *int64         `json:"account_id"`
	Platform        string         `json:"platform"`
	Model           string         `json:"model"`
	Extra           map[string]any `json:"extra,omitempty"`
}

// OpsRequestTiming 是从 http.access 系统日志提取的单请求阶段耗时。
// 所有数值均为相对于 Sub2API 入口的毫秒数；缺失阶段保持 nil。
type OpsRequestTiming struct {
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

// OpsRequestTimingFromExtra 只提取阶段字段，避免把系统日志中的其它内容带入使用记录接口。
func OpsRequestTimingFromExtra(extra map[string]any) *OpsRequestTiming {
	if len(extra) == 0 {
		return nil
	}
	timing := &OpsRequestTiming{
		RequestContentLength:           timingInt64Ptr(extra["request_content_length"]),
		AccountSlotAcquiredMs:          timingInt64Ptr(extra["account_slot_acquired_ms"]),
		UpstreamGetConnMs:              timingInt64Ptr(extra["upstream_get_conn_ms"]),
		UpstreamGotConnMs:              timingInt64Ptr(extra["upstream_got_conn_ms"]),
		UpstreamWroteRequestMs:         timingInt64Ptr(extra["upstream_wrote_request_ms"]),
		UpstreamFirstResponseByteMs:    timingInt64Ptr(extra["upstream_first_response_byte_ms"]),
		UpstreamFirstSSEDataMs:         timingInt64Ptr(extra["upstream_first_sse_data_ms"]),
		FirstVisibleOutputMs:           timingInt64Ptr(extra["first_visible_output_ms"]),
		FirstDownstreamFlushMs:         timingInt64Ptr(extra["first_downstream_flush_ms"]),
		UpstreamGetConnCount:           timingInt64Ptr(extra["upstream_get_conn_count"]),
		UpstreamGotConnCount:           timingInt64Ptr(extra["upstream_got_conn_count"]),
		UpstreamAttemptCount:           timingInt64Ptr(extra["upstream_attempt_count"]),
		UpstreamFirstResponseByteCount: timingInt64Ptr(extra["upstream_first_response_byte_count"]),
	}
	if value, ok := extra["upstream_connection_reused"].(bool); ok {
		timing.UpstreamConnectionReused = value
	}
	if value, ok := extra["upstream_wrote_request_error"].(bool); ok {
		timing.UpstreamWroteRequestError = value
	}
	if timing.RequestContentLength == nil && timing.AccountSlotAcquiredMs == nil && timing.UpstreamGetConnMs == nil &&
		timing.UpstreamGotConnMs == nil && timing.UpstreamWroteRequestMs == nil && timing.UpstreamFirstResponseByteMs == nil &&
		timing.UpstreamFirstSSEDataMs == nil && timing.FirstVisibleOutputMs == nil && timing.FirstDownstreamFlushMs == nil &&
		timing.UpstreamGetConnCount == nil && timing.UpstreamGotConnCount == nil && timing.UpstreamAttemptCount == nil &&
		timing.UpstreamFirstResponseByteCount == nil && !timing.UpstreamConnectionReused && !timing.UpstreamWroteRequestError {
		return nil
	}
	return timing
}

// timingInt64Ptr 解析 JSON 数字并保留零值；零毫秒是有效的“立即发生”阶段。
func timingInt64Ptr(value any) *int64 {
	var parsed int64
	switch v := value.(type) {
	case int:
		parsed = int64(v)
	case int64:
		parsed = v
	case float64:
		parsed = int64(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			parsed = n
		} else {
			return nil
		}
	case string:
		if n, err := json.Number(strings.TrimSpace(v)).Int64(); err == nil {
			parsed = n
		} else {
			return nil
		}
	default:
		return nil
	}
	if parsed < 0 {
		return nil
	}
	return &parsed
}

type OpsErrorLog struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`

	// Standardized classification
	// - 阶段：request|auth|account_auth|routing|upstream|network|internal
	// - owner: client|provider|platform
	// - source: client_request|upstream_http|gateway
	Phase string `json:"phase"`
	Type  string `json:"type"`

	Owner  string `json:"error_owner"`
	Source string `json:"error_source"`

	Severity string `json:"severity"`

	StatusCode int    `json:"status_code"`
	Platform   string `json:"platform"`
	Model      string `json:"model"`

	Resolved           bool       `json:"resolved"`
	ResolvedAt         *time.Time `json:"resolved_at"`
	ResolvedByUserID   *int64     `json:"resolved_by_user_id"`
	ResolvedByUserName string     `json:"resolved_by_user_name"`
	ResolvedStatusRaw  string     `json:"-"`

	ClientRequestID string `json:"client_request_id"`
	RequestID       string `json:"request_id"`
	Message         string `json:"message"`

	UserID      *int64 `json:"user_id"`
	UserEmail   string `json:"user_email"`
	APIKeyID    *int64 `json:"api_key_id"`
	AccountID   *int64 `json:"account_id"`
	AccountName string `json:"account_name"`
	GroupID     *int64 `json:"group_id"`
	GroupName   string `json:"group_name"`

	ClientIP    *string `json:"client_ip"`
	RequestPath string  `json:"request_path"`
	Stream      bool    `json:"stream"`

	InboundEndpoint  string `json:"inbound_endpoint"`
	UpstreamEndpoint string `json:"upstream_endpoint"`
	RequestedModel   string `json:"requested_model"`
	UpstreamModel    string `json:"upstream_model"`
	RequestType      *int16 `json:"request_type"`
	UserAgent        string `json:"user_agent"`

	// 关联 api_key 名称（LEFT JOIN api_keys 取得；软删只覆盖 key 列，name 保留，故已删 key 仍有原名）。
	APIKeyName    string `json:"api_key_name,omitempty"`
	APIKeyDeleted bool   `json:"api_key_deleted,omitempty"`
}

type OpsErrorLogDetail struct {
	OpsErrorLog

	ErrorBody string `json:"error_body"`

	// Upstream context (optional)
	UpstreamStatusCode   *int   `json:"upstream_status_code,omitempty"`
	UpstreamErrorMessage string `json:"upstream_error_message,omitempty"`
	UpstreamErrorDetail  string `json:"upstream_error_detail,omitempty"`
	UpstreamErrors       string `json:"upstream_errors,omitempty"` // JSON array (string) for display/parsing

	// Timings (optional)
	AuthLatencyMs      *int64 `json:"auth_latency_ms"`
	RoutingLatencyMs   *int64 `json:"routing_latency_ms"`
	UpstreamLatencyMs  *int64 `json:"upstream_latency_ms"`
	ResponseLatencyMs  *int64 `json:"response_latency_ms"`
	TimeToFirstTokenMs *int64 `json:"time_to_first_token_ms"`

	// vNext metric semantics
	IsBusinessLimited bool `json:"is_business_limited"`

	// 绑定且未删除的 Key 前缀，在错误发生时保存快照。
	APIKeyPrefix string `json:"api_key_prefix,omitempty"`
}

type OpsErrorLogFilter struct {
	StartTime *time.Time
	EndTime   *time.Time

	Platform  string
	GroupID   *int64
	AccountID *int64

	StatusCodes      []int
	StatusCodesOther bool
	Phase            string // Recovered provider rows bypass status>=400 only with the explicit opt-in below.
	Owner            string
	Source           string
	Resolved         *bool
	Query            string
	UserQuery        string // Search by user email

	// Optional correlation keys for exact matching.
	RequestID       string
	ClientRequestID string

	// User-scoped filters (used by the user-facing error requests endpoint and
	// by admin drill-down from the usage page).
	UserID   *int64
	APIKeyID *int64

	// Model matches against requested_model first, then model.
	Model string
	// ModelFuzzy 为 true 时 Model 走 ILIKE 模糊匹配（仅用户端启用）；false（默认）保持精确 =，管理端语义不变。
	ModelFuzzy bool

	// ExcludeCountTokens drops count_tokens probe errors (is_count_tokens=true).
	ExcludeCountTokens bool

	// IncludeRecoveredUpstream 允许提供方健康视图绕过 status>=400 守卫，
	// 从而展示 upstream/account_auth 阶段中 status<400 的恢复记录。
	// 普通请求错误接口不设置该开关，继续保持客户端错误语义。
	IncludeRecoveredUpstream bool

	// ErrorPhasesAny 和 ErrorTypesAny 增加普通 ANY() 条件，不改变单值 Phase 的匹配语义。
	// 开启 IncludeRecoveredUpstream 且阶段列表仅含 upstream/account_auth 时也会绕过守卫；
	// 其它 ANY 条件不会自行放宽 status>=400。字段用于映射前端的粗粒度错误分类。
	ErrorPhasesAny []string
	ErrorTypesAny  []string

	// View 控制列表端点的错误分类：errors 展示可处理错误，excluded 仅展示排除项，all 展示全部。
	View string

	// IgnoredStatusCodes 是客户端侧状态码忽略列表；nil 表示使用系统默认值，空切片表示不按状态码忽略。
	IgnoredStatusCodes []int

	Page     int
	PageSize int

	// SortBy 和 SortOrder 用于与用量列表一致的服务端排序。
	// 仓储层只允许 created_at、model 和 status_code，其他列回退到 created_at；默认降序。
	SortBy    string
	SortOrder string
}

// SetSort 将原始排序参数归一化后写入过滤器，供管理端和用户端错误列表共用。
func (f *OpsErrorLogFilter) SetSort(sortBy, sortOrder string) {
	f.SortBy = strings.TrimSpace(sortBy)
	f.SortOrder = strings.TrimSpace(sortOrder)
}

type OpsErrorLogList struct {
	Errors   []*OpsErrorLog `json:"errors"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}
