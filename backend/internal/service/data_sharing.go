package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

const (
	DataShareStatusCompleted  = "completed"
	DataShareStatusTerminated = "terminated"
	DataShareQualityComplete  = "complete"
	DataShareQualityPartial   = "partial"
	DataShareQualityInvalid   = "invalid"
	// DataShareQualityFilterNonInvalid 是列表筛选专用的虚拟质量状态，不会写入数据库。
	DataShareQualityFilterNonInvalid = "non_invalid"
	defaultDataShareDataset          = "tokenrouter-agent"
)

type dataShareCaptureMode string

const (
	dataShareCaptureModeSnapshot           dataShareCaptureMode = ""
	dataShareCaptureModeOpenAIResponsesRaw dataShareCaptureMode = "openai_responses_raw"
	dataShareCaptureModeIncremental        dataShareCaptureMode = "incremental"
)

const dataShareInternalCaptureMetaKey = "_capture"

var (
	ErrDataShareSessionNotFound = infraerrors.NotFound("DATA_SHARE_SESSION_NOT_FOUND", "data share session not found")
	ErrDataShareNoticeMissing   = infraerrors.BadRequest("DATA_SHARE_NOTICE_MISSING", "data sharing notice content is required")
	// ErrDataShareExportPayloadInvalid 表示导出前实时复核发现 payload 仍包含明显坏数据。
	ErrDataShareExportPayloadInvalid = infraerrors.BadRequest("DATA_SHARE_EXPORT_PAYLOAD_INVALID", "data share session failed export quality recheck")
	dataShareCompressionLevel        atomic.Value
)

const defaultDataSharingNoticeContent = "该分组已启用数据共享。使用该分组产生的 Agent 对话数据会被保存，并可能用于训练、评估和改进模型。请确认你已理解并同意该数据共享安排。"
const dataShareSkipRulesCacheTTL = 30 * time.Second
const dataShareExportTicketTTL = 5 * time.Minute
const dataSharePersistenceRetryAttempts = 3
const dataSharePersistenceRetryInitialDelay = 50 * time.Millisecond

// dataShareReplayOverlapMinMessages 限制重放去重至少命中两条连续消息，避免误删真实重复的单条发言。
const dataShareReplayOverlapMinMessages = 2
const dataShareLongReplayMinMessages = 16
const dataShareReplayWindowWidth = 3
const dataShareReplayWindowCandidateLimit = 64
const dataShareAdjacentReplayCompactMaxPasses = 4
const dataShareCompactFixedPointMaxPasses = 4
const dataShareQualityErrorReplayDuplicateBlock = "replay_duplicate_block"
const dataShareReplayRangeHashBase uint64 = 11400714819323198485

// dataShareExportExcludedFields 是导出文件中禁止外发的身份和来源字段。
var dataShareExportExcludedFields = map[string]struct{}{
	"user_email":   {},
	"ip_address":   {},
	"api_key_id":   {},
	"api_key_name": {},
	"account_id":   {},
	"user_id":      {},
	"user_name":    {},
}

var ErrDataShareSkipRulesInvalid = infraerrors.BadRequest("DATA_SHARE_SKIP_RULES_INVALID", "data sharing capture skip rules are invalid")
var ErrDataShareExportTicketInvalid = infraerrors.BadRequest("DATA_SHARE_EXPORT_TICKET_INVALID", "data sharing export ticket is invalid")
var ErrDataShareExportTicketForbidden = infraerrors.Forbidden("DATA_SHARE_EXPORT_TICKET_FORBIDDEN", "data sharing export ticket scope is not allowed")
var ErrDataShareExportArtifactNotFound = infraerrors.NotFound("DATA_SHARE_EXPORT_ARTIFACT_NOT_FOUND", "data share export artifact not found")
var ErrDataShareExportArtifactNotReady = infraerrors.BadRequest("DATA_SHARE_EXPORT_ARTIFACT_NOT_READY", "data share export artifact is not ready")
var ErrDataShareExportArtifactDeleted = infraerrors.NotFound("DATA_SHARE_EXPORT_ARTIFACT_DELETED", "data share export artifact was deleted")
var ErrDataShareExportArtifactUploadInProgress = infraerrors.Conflict("DATA_SHARE_EXPORT_ARTIFACT_UPLOAD_IN_PROGRESS", "data share export artifact upload is already in progress")
var ErrDataShareExportArtifactRemoteUploadInProgress = infraerrors.Conflict("DATA_SHARE_EXPORT_ARTIFACT_REMOTE_UPLOAD_IN_PROGRESS", "data share export artifact remote upload is in progress")
var ErrDataShareExportArtifactStorageInvalid = infraerrors.InternalServer("DATA_SHARE_EXPORT_ARTIFACT_STORAGE_INVALID", "data share export artifact storage is invalid")
var ErrDataShareStorageLimitInvalid = infraerrors.BadRequest("DATA_SHARE_STORAGE_LIMIT_INVALID", "data sharing storage limit must be greater than or equal to 0")
var ErrDataShareCaptureRuntimeInvalid = infraerrors.BadRequest("DATA_SHARE_CAPTURE_RUNTIME_INVALID", "data sharing capture runtime settings are invalid")

const (
	dataShareSkipRuleMatchContains = "contains"
	dataShareSkipRuleMatchEquals   = "equals"
)

// DataShareNotice 是用户切换到数据共享分组前需要确认的须知。
type DataShareNotice struct {
	Content   string    `json:"content"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DataShareCaptureSkipRule 描述数据共享采集前需要跳过的辅助请求模式。
type DataShareCaptureSkipRule struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	ClientFamilies []string `json:"client_families"`
	RequestPaths   []string `json:"request_paths"`
	Models         []string `json:"models"`
	FieldScopes    []string `json:"field_scopes"`
	Patterns       []string `json:"patterns"`
	CaseSensitive  bool     `json:"case_sensitive"`
	MatchMode      string   `json:"match_mode"`
}

// DataShareStorageLimit 描述管理端配置的数据共享采集空间保护阈值。
type DataShareStorageLimit struct {
	LimitBytes          int64   `json:"limit_bytes"`
	CurrentStorageBytes int64   `json:"current_storage_bytes"`
	Enabled             bool    `json:"enabled"`
	Exceeded            bool    `json:"exceeded"`
	UsageRatio          float64 `json:"usage_ratio"`
}

// DataShareCaptureRuntimeSettings 描述可在线更新的数据共享采集运行时配置。
type DataShareCaptureRuntimeSettings struct {
	WorkerCount            int    `json:"worker_count"`
	QueueSize              int    `json:"queue_size"`
	FlushQueueSize         int    `json:"flush_queue_size"`
	TaskTimeoutSeconds     int    `json:"task_timeout_seconds"`
	CompressionLevel       string `json:"compression_level"`
	BufferEnabled          bool   `json:"buffer_enabled"`
	BufferIdleFlushSeconds int    `json:"buffer_idle_flush_seconds"`
	BufferMaxSessions      int    `json:"buffer_max_sessions"`
	BufferMaxPendingEvents int    `json:"buffer_max_pending_events"`
	DurationWindowSize     int    `json:"duration_window_size"`
}

// DataShareCompressionLevel 表示采集 payload 的 zstd 压缩等级。
type DataShareCompressionLevel string

const (
	DataShareCompressionLevelFastest DataShareCompressionLevel = "fastest"
	DataShareCompressionLevelDefault DataShareCompressionLevel = "default"
	DataShareCompressionLevelBetter  DataShareCompressionLevel = "better"
	DataShareCompressionLevelBest    DataShareCompressionLevel = "best"
)

// DataShareSession 保存一条聚合后的 Agent session。
type DataShareSession struct {
	ID                   int64
	TrajectoryID         string
	SessionID            string
	Dataset              string
	Provider             string
	Model                string
	RequestPath          string
	UserAgent            string
	Status               string
	IsFinalSnapshot      bool
	SourceRequestCount   int
	SystemPrompt         *string
	Tools                []map[string]any
	Messages             []map[string]any
	Usage                map[string]any
	Meta                 map[string]any
	SessionJSON          map[string]any
	SessionJSONFinalized bool
	PayloadCompressed    []byte
	PayloadEncoding      string
	PayloadBytes         int64
	Exportable           bool
	QualityStatus        string
	QualityErrors        []string
	StorageBytes         int64
	InputTokens          int64
	OutputTokens         int64
	TotalTokens          int64
	ActualCost           *float64
	UserID               int64
	UserName             string
	UserEmail            string
	APIKeyID             int64
	APIKeyName           string
	GroupID              int64
	GroupName            string
	CreatedAt            time.Time
	EndedAt              *time.Time
	UpdatedAt            time.Time
	captureMode          dataShareCaptureMode
	captureInput         *DataShareCaptureInput
	captureState         *dataShareResponsesCaptureState
	captureRequestItems  []dataShareResponsesInputItem
	captureResponseItems []map[string]any
}

// DataShareSessionFilters 描述列表/统计/导出筛选条件。
type DataShareSessionFilters struct {
	IDs        []int64
	ExcludeIDs []int64
	// SelectAll 表示批量操作覆盖当前筛选条件下的全集，ExcludeIDs 用于排除用户取消勾选的记录。
	SelectAll     bool
	UserID        int64
	UserName      string
	APIKeyID      int64
	APIKeyName    string
	GroupID       int64
	GroupName     string
	Provider      string
	Model         string
	RequestPath   string
	UserAgent     string
	Exportable    *bool
	QualityStatus string
	StartTime     *time.Time
	EndTime       *time.Time
	Search        string
}

// DataShareQualityFilterStatuses 将质量筛选值展开为实际入库的质量状态列表。
func DataShareQualityFilterStatuses(qualityStatus string) []string {
	switch strings.TrimSpace(qualityStatus) {
	case "", "all":
		return nil
	case DataShareQualityFilterNonInvalid:
		return []string{DataShareQualityComplete, DataShareQualityPartial}
	default:
		return []string{strings.TrimSpace(qualityStatus)}
	}
}

// DataShareSessionFilterOptions 描述列表筛选器的全量可选值。
type DataShareSessionFilterOptions struct {
	Models       []string `json:"models"`
	RequestPaths []string `json:"request_paths"`
	UserAgents   []string `json:"user_agents"`
}

// DataShareExportScope 表示数据共享导出下载票据的权限范围。
type DataShareExportScope string

const (
	DataShareExportScopeUser  DataShareExportScope = "user"
	DataShareExportScopeAdmin DataShareExportScope = "admin"
)

// DataShareExportEncoding 表示下载票据期望的导出文件编码。
type DataShareExportEncoding string

const (
	DataShareExportEncodingJSON  DataShareExportEncoding = "json"
	DataShareExportEncodingJSONL DataShareExportEncoding = "jsonl"
	DataShareExportEncodingZstd  DataShareExportEncoding = "zstd"
)

// DataShareExportTicketRequest 描述一次下载票据签发请求。
type DataShareExportTicketRequest struct {
	Scope    DataShareExportScope
	UserID   int64
	Filters  DataShareSessionFilters
	Filename string
	Encoding DataShareExportEncoding
}

// DataShareExportTicket 是前端触发原生下载所需的短期票据响应。
type DataShareExportTicket struct {
	Token       string    `json:"token"`
	DownloadURL string    `json:"download_url"`
	Filename    string    `json:"filename"`
	Encoding    string    `json:"encoding"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// DataShareExportTicketClaims 是签名票据中保存的导出上下文。
type DataShareExportTicketClaims struct {
	Scope      DataShareExportScope    `json:"scope"`
	UserID     int64                   `json:"user_id,omitempty"`
	Filters    DataShareSessionFilters `json:"filters"`
	ArtifactID int64                   `json:"artifact_id,omitempty"`
	Filename   string                  `json:"filename"`
	Encoding   DataShareExportEncoding `json:"encoding,omitempty"`
	ExpiresAt  int64                   `json:"expires_at"`
}

// DataShareExportArtifactStatus 描述预生成导出文件的任务状态。
type DataShareExportArtifactStatus string

const (
	DataShareExportArtifactStatusPending   DataShareExportArtifactStatus = "pending"
	DataShareExportArtifactStatusRunning   DataShareExportArtifactStatus = "running"
	DataShareExportArtifactStatusCompleted DataShareExportArtifactStatus = "completed"
	DataShareExportArtifactStatusFailed    DataShareExportArtifactStatus = "failed"
	DataShareExportArtifactStatusDeleted   DataShareExportArtifactStatus = "deleted"
)

// DataShareExportArtifactRemoteStatus 描述导出文件上传到远端对象存储的状态。
type DataShareExportArtifactRemoteStatus string

const (
	DataShareExportArtifactRemoteStatusNotUploaded DataShareExportArtifactRemoteStatus = "not_uploaded"
	DataShareExportArtifactRemoteStatusUploading   DataShareExportArtifactRemoteStatus = "uploading"
	DataShareExportArtifactRemoteStatusUploaded    DataShareExportArtifactRemoteStatus = "uploaded"
	DataShareExportArtifactRemoteStatusFailed      DataShareExportArtifactRemoteStatus = "failed"
)

// DataShareExportRemoteConfig 描述数据共享导出文件上传到独立 S3/R2 端点的配置。
type DataShareExportRemoteConfig struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"` //nolint:revive // 字段名沿用 AWS 约定
	Prefix          string `json:"prefix"`
	ForcePathStyle  bool   `json:"force_path_style"`
}

// DataShareExportArtifact 记录一次预生成导出文件任务及其本地、远端文件元数据。
type DataShareExportArtifact struct {
	ID                 int64                               `json:"id"`
	Status             DataShareExportArtifactStatus       `json:"status"`
	Filename           string                              `json:"filename"`
	StoragePath        string                              `json:"-"`
	Encoding           string                              `json:"encoding"`
	Filters            DataShareSessionFilters             `json:"filters"`
	SessionCount       int64                               `json:"session_count"`
	FileSize           int64                               `json:"file_size"`
	SHA256             string                              `json:"sha256"`
	ErrorMessage       string                              `json:"error_message"`
	RemoteStatus       DataShareExportArtifactRemoteStatus `json:"remote_status"`
	RemoteBucket       string                              `json:"remote_bucket"`
	RemoteKey          string                              `json:"remote_key"`
	RemoteErrorMessage string                              `json:"remote_error_message"`
	RemoteUploadedAt   *time.Time                          `json:"remote_uploaded_at,omitempty"`
	RemoteUploadBytes  int64                               `json:"remote_upload_bytes"`
	RemoteUploadSpeed  float64                             `json:"remote_upload_speed"`
	CreatedAt          time.Time                           `json:"created_at"`
	StartedAt          *time.Time                          `json:"started_at,omitempty"`
	CompletedAt        *time.Time                          `json:"completed_at,omitempty"`
	DeletedAt          *time.Time                          `json:"deleted_at,omitempty"`
	UpdatedAt          time.Time                           `json:"updated_at"`
}

// DataShareExportArtifactCreateInput 是创建预生成导出任务的输入。
type DataShareExportArtifactCreateInput struct {
	Filename string
	Encoding DataShareExportEncoding
	Filters  DataShareSessionFilters
}

// DataShareExportArtifactRepository 定义导出文件任务元数据的持久化能力。
type DataShareExportArtifactRepository interface {
	Create(ctx context.Context, artifact *DataShareExportArtifact) (*DataShareExportArtifact, error)
	Get(ctx context.Context, id int64) (*DataShareExportArtifact, error)
	List(ctx context.Context, params pagination.PaginationParams) ([]DataShareExportArtifact, *pagination.PaginationResult, error)
	MarkRunning(ctx context.Context, id int64) error
	MarkCompleted(ctx context.Context, id int64, storagePath string, sessionCount int64, fileSize int64, sha256 string) error
	MarkFailed(ctx context.Context, id int64, errorMessage string) error
	MarkRemoteUploading(ctx context.Context, id int64) error
	MarkRemoteUploaded(ctx context.Context, id int64, bucket string, key string) error
	MarkRemoteUploadFailed(ctx context.Context, id int64, errorMessage string) error
	// MarkInterruptedFailed 将服务启动前遗留且无人继续处理的任务标记为失败。
	MarkInterruptedFailed(ctx context.Context, errorMessage string) (int64, error)
	// MarkInterruptedRemoteUploads 将服务启动前遗留的远端上传任务标记为失败。
	MarkInterruptedRemoteUploads(ctx context.Context, errorMessage string) (int64, error)
	MarkDeleted(ctx context.Context, id int64) (storagePath string, err error)
}

// DataShareStoragePoint 用于管理端展示空间增长趋势。
type DataShareStoragePoint struct {
	Date         string `json:"date"`
	StorageBytes int64  `json:"storage_bytes"`
	SessionCount int64  `json:"session_count"`
}

// DataShareGroupStoragePoint 用于管理端按分组展示空间占用。
type DataShareGroupStoragePoint struct {
	GroupID      int64  `json:"group_id"`
	GroupName    string `json:"group_name"`
	StorageBytes int64  `json:"storage_bytes"`
	SessionCount int64  `json:"session_count"`
}

// DataShareRequestPathPoint 用于管理端按用户请求路径展示分布。
type DataShareRequestPathPoint struct {
	RequestPath  string `json:"request_path"`
	StorageBytes int64  `json:"storage_bytes"`
	SessionCount int64  `json:"session_count"`
	TotalTokens  int64  `json:"total_tokens"`
}

// DataShareModelPoint 用于管理端按模型展示分布。
type DataShareModelPoint struct {
	Model        string `json:"model"`
	StorageBytes int64  `json:"storage_bytes"`
	SessionCount int64  `json:"session_count"`
	TotalTokens  int64  `json:"total_tokens"`
}

// DataShareUserAgentPoint 用于管理端按客户端 User-Agent 展示分布。
type DataShareUserAgentPoint struct {
	UserAgent    string `json:"user_agent"`
	StorageBytes int64  `json:"storage_bytes"`
	SessionCount int64  `json:"session_count"`
	TotalTokens  int64  `json:"total_tokens"`
}

// DataShareQualityErrorPoint 用于管理端展示质量错误原因分布。
type DataShareQualityErrorPoint struct {
	ErrorCode    string `json:"error_code"`
	SessionCount int64  `json:"session_count"`
}

// DataShareInvalidUserPoint 用于管理端按用户定位无效会话贡献来源。
type DataShareInvalidUserPoint struct {
	UserID       int64   `json:"user_id"`
	UserName     string  `json:"user_name"`
	UserEmail    string  `json:"user_email"`
	SessionCount int64   `json:"session_count"`
	InvalidCount int64   `json:"invalid_count"`
	InvalidRatio float64 `json:"invalid_ratio"`
	StorageBytes int64   `json:"storage_bytes"`
	TotalTokens  int64   `json:"total_tokens"`
}

// DataShareStats 是管理端数据共享概览指标。
type DataShareStats struct {
	SessionCount            int64                             `json:"session_count"`
	ExportableCount         int64                             `json:"exportable_count"`
	NonExportableCount      int64                             `json:"non_exportable_count"`
	CompleteCount           int64                             `json:"complete_count"`
	PartialCount            int64                             `json:"partial_count"`
	InvalidCount            int64                             `json:"invalid_count"`
	TotalStorageBytes       int64                             `json:"total_storage_bytes"`
	TotalTokens             int64                             `json:"total_tokens"`
	AvgTokensPerSession     float64                           `json:"avg_tokens_per_session"`
	TotalActualCost         float64                           `json:"total_actual_cost"`
	AvgActualCostPerSession float64                           `json:"avg_actual_cost_per_session"`
	StorageTrend            []DataShareStoragePoint           `json:"storage_trend"`
	GroupStorageBreakdown   []DataShareGroupStoragePoint      `json:"group_storage_breakdown"`
	RequestPathBreakdown    []DataShareRequestPathPoint       `json:"request_path_breakdown"`
	ModelBreakdown          []DataShareModelPoint             `json:"model_breakdown"`
	UserAgentBreakdown      []DataShareUserAgentPoint         `json:"user_agent_breakdown"`
	QualityErrorBreakdown   []DataShareQualityErrorPoint      `json:"quality_error_breakdown"`
	InvalidUserBreakdown    []DataShareInvalidUserPoint       `json:"invalid_user_breakdown"`
	CaptureWorker           DataSharingCaptureWorkerPoolStats `json:"capture_worker"`
	CaptureBuffer           DataSharingCaptureBufferStats     `json:"capture_buffer"`
	CaptureDurations        DataShareCaptureDurationStats     `json:"capture_durations"`
}

// DataShareCaptureInput 是网关成功完成请求后的采集输入。
type DataShareCaptureInput struct {
	APIKey            *APIKey
	User              *User
	Account           *Account
	Provider          string
	Model             string
	UpstreamModel     string
	SessionID         string
	RequestID         string
	RequestBody       []byte
	ResponseBody      []byte
	SystemPrompt      string
	Messages          []any
	Tools             []map[string]any
	InputTokens       int
	OutputTokens      int
	CacheReadTokens   int
	CacheCreateTokens int
	ActualCost        *float64
	UserAgent         string
	IPAddress         string
	InboundEndpoint   string
	UpstreamEndpoint  string
	CaptureMode       dataShareCaptureMode
	Turn              int
	CaptureIncomplete bool
}

// DataShareUpsertOptions 是采集写入时的附加保护参数。
type DataShareUpsertOptions struct {
	// StorageLimitBytes 为 0 时不限制；大于 0 时 repository 在新建 session 和合并增量前检查容量。
	StorageLimitBytes int64
	// DurationRecorder 用于 repository 上报压缩、容量检查和数据库写入等内部耗时。
	DurationRecorder DataShareCaptureDurationRecorder
}

// DataShareSessionRepository 定义数据共享 session 的持久化能力。
type DataShareSessionRepository interface {
	GetCaptureByTrajectoryIDWithPayload(ctx context.Context, trajectoryID string) (*DataShareSession, error)
	SaveCaptureSnapshot(ctx context.Context, session *DataShareSession, opts ...DataShareUpsertOptions) error
	List(ctx context.Context, params pagination.PaginationParams, filters DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error)
	ListWithPayload(ctx context.Context, params pagination.PaginationParams, filters DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error)
	GetByID(ctx context.Context, id int64) (*DataShareSession, error)
	Delete(ctx context.Context, id int64) error
	BatchDelete(ctx context.Context, ids []int64, filters DataShareSessionFilters) (int64, error)
	Stats(ctx context.Context, filters DataShareSessionFilters) (*DataShareStats, error)
	FilterOptions(ctx context.Context, filters DataShareSessionFilters) (*DataShareSessionFilterOptions, error)
	TotalStorageBytes(ctx context.Context) (int64, error)
}

type dataShareExportUploadProgress struct {
	uploadedBytes int64
	totalBytes    int64
	startedAt     time.Time
	updatedAt     time.Time
}

// DataSharingService 负责数据共享须知、采集、导出和统计。
type DataSharingService struct {
	repo                     DataShareSessionRepository
	exportArtifactRepo       DataShareExportArtifactRepository
	settingRepo              SettingRepository
	exportStorageDir         string
	exportObjectStoreFactory BackupObjectStoreFactory
	exportSecretEncryptor    SecretEncryptor
	captureWorker            *DataSharingCaptureWorkerPool
	captureBuffer            *DataSharingCaptureBuffer
	captureDurations         *dataShareCaptureDurationRecorder
	defaultRuntimeSettings   DataShareCaptureRuntimeSettings
	captureWorkerNilDropped  atomic.Uint64
	captureWorkerNilLogNanos atomic.Int64
	skipRulesMu              sync.RWMutex
	skipRulesCache           []DataShareCaptureSkipRule
	skipRulesCacheExpiresAt  time.Time
	exportUploadProgressMu   sync.RWMutex
	exportUploadProgress     map[int64]dataShareExportUploadProgress
}

func NewDataSharingService(repo DataShareSessionRepository, settingRepo SettingRepository, captureWorker ...*DataSharingCaptureWorkerPool) *DataSharingService {
	svc := &DataSharingService{
		repo:                   repo,
		settingRepo:            settingRepo,
		defaultRuntimeSettings: *defaultDataShareCaptureRuntimeSettings(),
		captureDurations:       newDataShareCaptureDurationRecorder(defaultDataSharingCaptureDurationWindowSize),
		exportUploadProgress:   make(map[int64]dataShareExportUploadProgress),
	}
	if len(captureWorker) > 0 {
		svc.captureWorker = captureWorker[0]
	}
	if repo != nil {
		bufferOptions := DataSharingCaptureBufferOptions{
			Flush:            svc.flushBufferedCaptureSession,
			Hydrate:          svc.hydrateBufferedCaptureSession,
			DurationRecorder: svc.captureDurations,
		}
		if svc.captureWorker != nil {
			bufferOptions.ScheduleFlush = svc.scheduleBufferedCaptureFlush
		}
		svc.captureBuffer = NewDataSharingCaptureBuffer(bufferOptions)
		svc.captureBuffer.UpdateRuntimeSettings(svc.defaultRuntimeSettings)
	}
	if svc.captureWorker != nil {
		svc.captureWorker.SetHandler(svc.handleCaptureJob)
		svc.captureWorker.SetDurationRecorder(svc.captureDurations)
	}
	return svc
}

// SetExportArtifactRepository 绑定预生成导出文件任务仓储。
func (s *DataSharingService) SetExportArtifactRepository(repo DataShareExportArtifactRepository) {
	if s == nil {
		return
	}
	s.exportArtifactRepo = repo
}

// SetExportStorageDir 设置预生成导出文件的本地保存目录。
func (s *DataSharingService) SetExportStorageDir(dir string) {
	if s == nil {
		return
	}
	s.exportStorageDir = strings.TrimSpace(dir)
}

// SetExportObjectStoreDeps 注入数据共享导出上传 S3/R2 所需的对象存储依赖。
func (s *DataSharingService) SetExportObjectStoreDeps(factory BackupObjectStoreFactory, encryptor SecretEncryptor) {
	if s == nil {
		return
	}
	s.exportObjectStoreFactory = factory
	s.exportSecretEncryptor = encryptor
}
