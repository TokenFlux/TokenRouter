package audit

import (
	"context"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// ErrAuditLogNotFound 审计日志不存在。
var ErrAuditLogNotFound = infraerrors.NotFound("AUDIT_LOG_NOT_FOUND", "audit log not found")

// 审计日志相关常量。
const (
	// AuditAuthMethodJWT / AuditAuthMethodAdminAPIKey 与 auth 中间件写入的 auth_method 对齐。
	AuditAuthMethodJWT         = "jwt"
	AuditAuthMethodAdminAPIKey = "admin_api_key"

	// auditRequestBodyMaxBytes 请求体脱敏后入库的最大长度（字节），超出截断。
	auditRequestBodyMaxBytes = 16 * 1024
	// AuditRequestBodyCaptureLimit 请求体参与脱敏解析的原始大小上限（字节）。
	// 审计中间件按此上限截断读取，超出的请求体仅记录占位符不解析。
	AuditRequestBodyCaptureLimit = 256 * 1024
)

// 内置审计动作名（认证/安全事件与特殊操作使用固定值，普通请求由路由自动推导）。
const (
	AuditActionLogin                  = "auth.login"
	AuditActionLogin2FA               = "auth.login.2fa"
	AuditActionRegister               = "auth.register"
	AuditActionTokenRefresh           = "auth.token.refresh"
	AuditActionSessionBindingMismatch = "auth.session_binding.mismatch"
	AuditActionStepUpVerify           = "auth.step_up.verify"
	AuditActionAuditLogClear          = "admin.audit_log.clear"
	AuditActionUserSubscriptionRevoke = "user.subscriptions.revoke"
)

// AuditLog 一条管理面操作审计记录。
type AuditLog struct {
	ID               int64          `json:"id"`
	CreatedAt        time.Time      `json:"created_at"`
	ActorUserID      *int64         `json:"actor_user_id,omitempty"`
	ActorEmail       string         `json:"actor_email"`
	ActorRole        string         `json:"actor_role"`
	AuthMethod       string         `json:"auth_method"`
	CredentialMasked string         `json:"credential_masked"`
	Action           string         `json:"action"`
	Method           string         `json:"method"`
	Path             string         `json:"path"`
	RequestID        string         `json:"request_id"`
	ClientIP         string         `json:"client_ip"`
	UserAgent        string         `json:"user_agent"`
	RequestBody      string         `json:"request_body,omitempty"`
	StatusCode       int            `json:"status_code"`
	LatencyMs        int64          `json:"latency_ms"`
	Extra            map[string]any `json:"extra,omitempty"`
}

// AuditLogFilter 审计日志列表查询条件。
type AuditLogFilter struct {
	Page     int
	PageSize int

	StartTime   *time.Time
	EndTime     *time.Time
	ActorUserID *int64
	ActorEmail  string
	AuthMethod  string
	Action      string
	Method      string
	ClientIP    string
	// Success: nil 全部；true 仅 2xx/3xx；false 仅 >=400。
	Success *bool
	// Query 对 path / action / actor_email 做模糊匹配。
	Query string
}

// AuditLogList 分页结果。
type AuditLogList struct {
	Logs     []*AuditLog
	Total    int
	Page     int
	PageSize int
}

// AuditLogRepository 审计日志持久化端口。
// 注意：接口刻意不提供单条删除能力——审计日志只允许追加与全量清空。
type AuditLogRepository interface {
	BatchInsert(ctx context.Context, logs []*AuditLog) (int64, error)
	// Insert 同步写入单条（用于清空留痕等必须落库的记录）。
	Insert(ctx context.Context, log *AuditLog) error
	List(ctx context.Context, filter *AuditLogFilter) (*AuditLogList, error)
	GetByID(ctx context.Context, id int64) (*AuditLog, error)
	Count(ctx context.Context) (int64, error)
	// ClearWithTrace 同事务清空并写入必须持久的留痕。
	ClearWithTrace(context.Context, *AuditLog) (int64, error)
	// TruncateAll 只保留旧仓储兼容，生产清空使用闭合操作。
	TruncateAll(ctx context.Context) error
	// DeleteBefore 按保留期批量删除，返回本批删除行数（幂等，可多实例并发）。
	DeleteBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
}
