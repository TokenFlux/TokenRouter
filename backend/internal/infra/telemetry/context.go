// Package telemetry 拥有请求关联与时延标识；业务执行状态由网关显式投影。
package telemetry

// ContextKey 保留既有字符串键的类型身份，旧 ctxkey 以别名兼容。
type ContextKey string

const (
	RequestID              ContextKey = "ctx_request_id"
	ClientRequestID        ContextKey = "ctx_client_request_id"
	ParentClientRequestID  ContextKey = "ctx_parent_client_request_id"
	RequestStartedAt       ContextKey = "ctx_request_started_at"
	AccountSlotAcquiredAt  ContextKey = "ctx_account_slot_acquired_at"
	FirstSSEDataAt         ContextKey = "ctx_first_sse_data_at"
	FirstDownstreamFlushAt ContextKey = "ctx_first_downstream_flush_at"
	FirstVisibleOutputAt   ContextKey = "ctx_first_visible_output_at"
	Model                  ContextKey = "ctx_model"
	ClientModel            ContextKey = "ctx_client_model"
	Platform               ContextKey = "ctx_platform"
	AccountID              ContextKey = "ctx_account_id"
	RetryCount             ContextKey = "ctx_retry_count"
)
