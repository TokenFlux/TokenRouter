package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/tidwall/gjson"
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
	Scope     DataShareExportScope    `json:"scope"`
	UserID    int64                   `json:"user_id,omitempty"`
	Filters   DataShareSessionFilters `json:"filters"`
	Filename  string                  `json:"filename"`
	Encoding  DataShareExportEncoding `json:"encoding,omitempty"`
	ExpiresAt int64                   `json:"expires_at"`
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

// DataSharingService 负责数据共享须知、采集、导出和统计。
type DataSharingService struct {
	repo                     DataShareSessionRepository
	settingRepo              SettingRepository
	captureWorker            *DataSharingCaptureWorkerPool
	captureBuffer            *DataSharingCaptureBuffer
	captureDurations         *dataShareCaptureDurationRecorder
	defaultRuntimeSettings   DataShareCaptureRuntimeSettings
	captureWorkerNilDropped  atomic.Uint64
	captureWorkerNilLogNanos atomic.Int64
	skipRulesMu              sync.RWMutex
	skipRulesCache           []DataShareCaptureSkipRule
	skipRulesCacheExpiresAt  time.Time
}

func NewDataSharingService(repo DataShareSessionRepository, settingRepo SettingRepository, captureWorker ...*DataSharingCaptureWorkerPool) *DataSharingService {
	svc := &DataSharingService{
		repo:                   repo,
		settingRepo:            settingRepo,
		defaultRuntimeSettings: *defaultDataShareCaptureRuntimeSettings(),
		captureDurations:       newDataShareCaptureDurationRecorder(defaultDataSharingCaptureDurationWindowSize),
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

// SetDefaultCaptureRuntimeSettings 设置配置文件提供的默认采集运行时参数，数据库配置仍可覆盖。
func (s *DataSharingService) SetDefaultCaptureRuntimeSettings(settings DataShareCaptureRuntimeSettings) {
	if s == nil {
		return
	}
	s.defaultRuntimeSettings = normalizeDataShareCaptureRuntimeSettings(settings)
	if s.captureBuffer != nil {
		s.captureBuffer.UpdateRuntimeSettings(s.defaultRuntimeSettings)
	}
	if s.captureDurations != nil {
		s.captureDurations.SetWindowSize(s.defaultRuntimeSettings.DurationWindowSize)
	}
}

// LoadRuntimeSettings 从数据库加载运行时配置并同步到当前 worker。
func (s *DataSharingService) LoadRuntimeSettings(ctx context.Context) (*DataShareCaptureRuntimeSettings, error) {
	settings, err := s.GetCaptureRuntimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.applyCaptureRuntimeSettings(settings)
	return settings, nil
}

// GetNotice 返回当前数据共享须知；未配置时返回默认模板和版本 1。
func (s *DataSharingService) GetNotice(ctx context.Context) (*DataShareNotice, error) {
	return defaultDataSharingNotice(ctx, s.settingRepo)
}

// UpdateNotice 更新数据共享须知并递增版本号。
func (s *DataSharingService) UpdateNotice(ctx context.Context, content string) (*DataShareNotice, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrDataShareNoticeMissing
	}
	current, err := s.GetNotice(ctx)
	if err != nil {
		return nil, err
	}
	version := current.Version + 1
	if version < 1 {
		version = 1
	}
	updates := map[string]string{
		SettingKeyDataSharingNoticeContent: content,
		SettingKeyDataSharingNoticeVersion: strconv.Itoa(version),
	}
	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return nil, err
	}
	return &DataShareNotice{Content: content, Version: version, UpdatedAt: time.Now()}, nil
}

// ConfirmNotice 校验用户确认的数据共享须知版本。
func (s *DataSharingService) ConfirmNotice(ctx context.Context, version int) (*DataShareNotice, error) {
	notice, err := s.GetNotice(ctx)
	if err != nil {
		return nil, err
	}
	if version <= 0 || version != notice.Version {
		return nil, ErrDataSharingConsentRequired
	}
	return notice, nil
}

// GetCaptureSkipRules 返回当前生效的数据共享采集跳过规则。
func (s *DataSharingService) GetCaptureSkipRules(ctx context.Context) ([]DataShareCaptureSkipRule, error) {
	rules, err := s.loadCaptureSkipRules(ctx)
	if err != nil {
		return nil, err
	}
	return cloneDataShareCaptureSkipRules(rules), nil
}

// UpdateCaptureSkipRules 保存管理端维护的数据共享采集跳过规则。
func (s *DataSharingService) UpdateCaptureSkipRules(ctx context.Context, rules []DataShareCaptureSkipRule) ([]DataShareCaptureSkipRule, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	normalized, err := normalizeDataShareCaptureSkipRules(rules)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingCaptureSkipRules, string(data)); err != nil {
		return nil, err
	}
	s.clearCaptureSkipRulesCache()
	return cloneDataShareCaptureSkipRules(normalized), nil
}

// GetStorageLimit 返回数据共享采集空间阈值和当前压缩后占用。
func (s *DataSharingService) GetStorageLimit(ctx context.Context) (*DataShareStorageLimit, error) {
	limitBytes, err := s.loadStorageLimitBytes(ctx)
	if err != nil {
		return nil, err
	}
	currentBytes := int64(0)
	if s != nil && s.repo != nil {
		currentBytes, err = s.repo.TotalStorageBytes(ctx)
		if err != nil {
			return nil, err
		}
	}
	return buildDataShareStorageLimit(limitBytes, currentBytes), nil
}

// UpdateStorageLimit 保存数据共享采集空间阈值；0 表示关闭容量限制。
func (s *DataSharingService) UpdateStorageLimit(ctx context.Context, limitBytes int64) (*DataShareStorageLimit, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	if limitBytes < 0 {
		return nil, ErrDataShareStorageLimitInvalid
	}
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingStorageLimit, strconv.FormatInt(limitBytes, 10)); err != nil {
		return nil, err
	}
	return s.GetStorageLimit(ctx)
}

// GetCaptureRuntimeSettings 返回数据共享采集运行时配置。
func (s *DataSharingService) GetCaptureRuntimeSettings(ctx context.Context) (*DataShareCaptureRuntimeSettings, error) {
	settings, err := s.loadCaptureRuntimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	return settings, nil
}

// UpdateCaptureRuntimeSettings 保存数据共享采集运行时配置，并立即更新当前进程 worker。
func (s *DataSharingService) UpdateCaptureRuntimeSettings(ctx context.Context, settings DataShareCaptureRuntimeSettings) (*DataShareCaptureRuntimeSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	if settings.WorkerCount <= 0 || settings.QueueSize <= 0 || settings.TaskTimeoutSeconds <= 0 {
		return nil, ErrDataShareCaptureRuntimeInvalid
	}
	if settings.BufferIdleFlushSeconds <= 0 {
		settings.BufferEnabled = defaultDataSharingCaptureBufferEnabled
		settings.BufferIdleFlushSeconds = defaultDataSharingCaptureBufferIdleSeconds
		settings.BufferMaxSessions = defaultDataSharingCaptureBufferMaxSessions
		settings.BufferMaxPendingEvents = defaultDataSharingCaptureBufferMaxEvents
	}
	settings = normalizeDataShareCaptureRuntimeSettings(settings)
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingCaptureRuntime, dataShareCaptureRuntimeSettingsJSON(settings)); err != nil {
		return nil, err
	}
	s.applyCaptureRuntimeSettings(&settings)
	return &settings, nil
}

func (s *DataSharingService) loadCaptureRuntimeSettings(ctx context.Context) (*DataShareCaptureRuntimeSettings, error) {
	if s == nil {
		return defaultDataShareCaptureRuntimeSettings(), nil
	}
	defaultSettingsValue := normalizeDataShareCaptureRuntimeSettings(s.defaultRuntimeSettings)
	defaultSettings := &defaultSettingsValue
	if s.captureWorker != nil {
		stats := s.captureWorker.Stats()
		if stats.WorkerCount > 0 {
			defaultSettings.WorkerCount = stats.WorkerCount
		}
		if stats.QueueCapacity > 0 {
			defaultSettings.QueueSize = stats.QueueCapacity
		}
		if stats.FlushQueueCapacity > 0 {
			defaultSettings.FlushQueueSize = stats.FlushQueueCapacity
		}
		if stats.TaskTimeoutSeconds > 0 {
			defaultSettings.TaskTimeoutSeconds = stats.TaskTimeoutSeconds
		}
		if stats.CompressionLevel != "" {
			defaultSettings.CompressionLevel = stats.CompressionLevel
		}
	}
	if s.captureBuffer != nil {
		bufferStats := s.captureBuffer.Stats()
		defaultSettings.BufferEnabled = bufferStats.Enabled
		if bufferStats.IdleFlushSeconds > 0 {
			defaultSettings.BufferIdleFlushSeconds = bufferStats.IdleFlushSeconds
		}
		if bufferStats.MaxSessions > 0 {
			defaultSettings.BufferMaxSessions = bufferStats.MaxSessions
		}
		if bufferStats.MaxPendingEvents > 0 {
			defaultSettings.BufferMaxPendingEvents = bufferStats.MaxPendingEvents
		}
	}
	if s.captureDurations != nil {
		defaultSettings.DurationWindowSize = s.captureDurations.Snapshot().WindowSize
	}
	if s.settingRepo == nil {
		return defaultSettings, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingCaptureRuntime)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaultSettings, nil
		}
		return nil, err
	}
	settings := *defaultSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, ErrDataShareCaptureRuntimeInvalid
	}
	if !gjson.Get(raw, "flush_queue_size").Exists() {
		settings.FlushQueueSize = settings.QueueSize
	}
	if !gjson.Get(raw, "duration_window_size").Exists() {
		settings.DurationWindowSize = defaultSettings.DurationWindowSize
	}
	if settings.WorkerCount <= 0 || settings.QueueSize <= 0 || settings.TaskTimeoutSeconds <= 0 ||
		settings.BufferIdleFlushSeconds <= 0 || settings.BufferMaxSessions <= 0 || settings.BufferMaxPendingEvents <= 0 ||
		settings.DurationWindowSize <= 0 {
		return nil, ErrDataShareCaptureRuntimeInvalid
	}
	if strings.TrimSpace(settings.CompressionLevel) == "" {
		settings.CompressionLevel = defaultSettings.CompressionLevel
	}
	settings = normalizeDataShareCaptureRuntimeSettings(settings)
	return &settings, nil
}

func (s *DataSharingService) applyCaptureRuntimeSettings(settings *DataShareCaptureRuntimeSettings) {
	if s == nil || settings == nil || settings.WorkerCount <= 0 || settings.QueueSize <= 0 || settings.TaskTimeoutSeconds <= 0 ||
		settings.BufferIdleFlushSeconds <= 0 || settings.BufferMaxSessions <= 0 || settings.BufferMaxPendingEvents <= 0 ||
		settings.DurationWindowSize <= 0 {
		return
	}
	SetDataShareCompressionLevel(settings.CompressionLevel)
	if s.captureWorker != nil {
		s.captureWorker.UpdateRuntimeSettings(
			settings.WorkerCount,
			settings.QueueSize,
			settings.FlushQueueSize,
			time.Duration(settings.TaskTimeoutSeconds)*time.Second,
		)
	}
	if s.captureBuffer != nil {
		s.captureBuffer.UpdateRuntimeSettings(*settings)
	}
	if s.captureDurations != nil {
		s.captureDurations.SetWindowSize(settings.DurationWindowSize)
	}
}

func defaultDataShareCaptureRuntimeSettings() *DataShareCaptureRuntimeSettings {
	settings := normalizeDataShareCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            defaultDataSharingCaptureWorkerCount,
		QueueSize:              defaultDataSharingCaptureQueueSize,
		FlushQueueSize:         defaultDataSharingCaptureQueueSize,
		TaskTimeoutSeconds:     defaultDataSharingCaptureTaskTimeoutSeconds,
		CompressionLevel:       string(defaultDataSharingCaptureCompressionLevel),
		BufferEnabled:          defaultDataSharingCaptureBufferEnabled,
		BufferIdleFlushSeconds: defaultDataSharingCaptureBufferIdleSeconds,
		BufferMaxSessions:      defaultDataSharingCaptureBufferMaxSessions,
		BufferMaxPendingEvents: defaultDataSharingCaptureBufferMaxEvents,
		DurationWindowSize:     defaultDataSharingCaptureDurationWindowSize,
	})
	return &settings
}

func normalizeDataShareCaptureRuntimeSettings(settings DataShareCaptureRuntimeSettings) DataShareCaptureRuntimeSettings {
	opts := normalizeDataSharingCapturePoolOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount:    settings.WorkerCount,
		QueueSize:      settings.QueueSize,
		FlushQueueSize: settings.FlushQueueSize,
		TaskTimeout:    time.Duration(settings.TaskTimeoutSeconds) * time.Second,
	})
	bufferIdleSeconds := settings.BufferIdleFlushSeconds
	if bufferIdleSeconds <= 0 {
		bufferIdleSeconds = defaultDataSharingCaptureBufferIdleSeconds
	}
	if bufferIdleSeconds > maxDataSharingCaptureBufferIdleSeconds {
		bufferIdleSeconds = maxDataSharingCaptureBufferIdleSeconds
	}
	bufferMaxSessions := settings.BufferMaxSessions
	if bufferMaxSessions <= 0 {
		bufferMaxSessions = defaultDataSharingCaptureBufferMaxSessions
	}
	if bufferMaxSessions > maxDataSharingCaptureBufferMaxSessions {
		bufferMaxSessions = maxDataSharingCaptureBufferMaxSessions
	}
	bufferMaxPendingEvents := settings.BufferMaxPendingEvents
	if bufferMaxPendingEvents <= 0 {
		bufferMaxPendingEvents = defaultDataSharingCaptureBufferMaxEvents
	}
	if bufferMaxPendingEvents > maxDataSharingCaptureBufferMaxEvents {
		bufferMaxPendingEvents = maxDataSharingCaptureBufferMaxEvents
	}
	durationWindowSize := normalizeDataShareCaptureDurationWindowSize(settings.DurationWindowSize)
	return DataShareCaptureRuntimeSettings{
		WorkerCount:            opts.WorkerCount,
		QueueSize:              opts.QueueSize,
		FlushQueueSize:         opts.FlushQueueSize,
		TaskTimeoutSeconds:     durationSecondsCeil(opts.TaskTimeout),
		CompressionLevel:       NormalizeDataShareCompressionLevel(settings.CompressionLevel),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: bufferIdleSeconds,
		BufferMaxSessions:      bufferMaxSessions,
		BufferMaxPendingEvents: bufferMaxPendingEvents,
		DurationWindowSize:     durationWindowSize,
	}
}

// NormalizeDataShareCompressionLevel 归一化管理端可配置的 zstd 压缩等级。
func NormalizeDataShareCompressionLevel(level string) string {
	switch DataShareCompressionLevel(strings.ToLower(strings.TrimSpace(level))) {
	case DataShareCompressionLevelFastest:
		return string(DataShareCompressionLevelFastest)
	case DataShareCompressionLevelDefault:
		return string(DataShareCompressionLevelDefault)
	case DataShareCompressionLevelBetter:
		return string(DataShareCompressionLevelBetter)
	case DataShareCompressionLevelBest:
		return string(DataShareCompressionLevelBest)
	default:
		return string(defaultDataSharingCaptureCompressionLevel)
	}
}

// SetDataShareCompressionLevel 在线更新后续采集 payload 使用的 zstd 压缩等级。
func SetDataShareCompressionLevel(level string) string {
	normalized := NormalizeDataShareCompressionLevel(level)
	dataShareCompressionLevel.Store(normalized)
	return normalized
}

// CurrentDataShareCompressionLevel 返回当前采集 payload 使用的 zstd 压缩等级。
func CurrentDataShareCompressionLevel() string {
	level, _ := dataShareCompressionLevel.Load().(string)
	if strings.TrimSpace(level) == "" {
		return string(defaultDataSharingCaptureCompressionLevel)
	}
	return NormalizeDataShareCompressionLevel(level)
}

func dataShareCaptureRuntimeSettingsJSON(settings DataShareCaptureRuntimeSettings) string {
	data, err := json.Marshal(settings)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (s *DataSharingService) loadStorageLimitBytes(ctx context.Context) (int64, error) {
	if s == nil || s.settingRepo == nil {
		return 0, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingStorageLimit)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return 0, nil
		}
		return 0, err
	}
	limitBytes, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || limitBytes < 0 {
		return 0, ErrDataShareStorageLimitInvalid
	}
	return limitBytes, nil
}

func buildDataShareStorageLimit(limitBytes, currentBytes int64) *DataShareStorageLimit {
	if limitBytes < 0 {
		limitBytes = 0
	}
	if currentBytes < 0 {
		currentBytes = 0
	}
	out := &DataShareStorageLimit{
		LimitBytes:          limitBytes,
		CurrentStorageBytes: currentBytes,
		Enabled:             limitBytes > 0,
		Exceeded:            limitBytes > 0 && currentBytes >= limitBytes,
	}
	if limitBytes > 0 {
		out.UsageRatio = float64(currentBytes) / float64(limitBytes)
	}
	return out
}

func (s *DataSharingService) captureStorageLimitOption(ctx context.Context) DataShareUpsertOptions {
	limitBytes, err := s.loadStorageLimitBytes(ctx)
	if err != nil {
		slog.Warn("data sharing: failed to load storage limit, capture continues without limit", "error", err)
		return DataShareUpsertOptions{DurationRecorder: s.captureDurations}
	}
	return DataShareUpsertOptions{StorageLimitBytes: limitBytes, DurationRecorder: s.captureDurations}
}

func (s *DataSharingService) shouldSkipDataShareCapture(ctx context.Context, input DataShareCaptureInput) bool {
	rules, err := s.loadCaptureSkipRules(ctx)
	if err != nil {
		slog.Warn("data sharing: failed to load capture skip rules", "error", err)
		return false
	}
	return dataShareCaptureSkipRulesMatch(input, rules)
}

func (s *DataSharingService) loadCaptureSkipRules(ctx context.Context) ([]DataShareCaptureSkipRule, error) {
	if s == nil || s.settingRepo == nil {
		return defaultDataShareCaptureSkipRules(), nil
	}
	now := time.Now()
	s.skipRulesMu.RLock()
	if now.Before(s.skipRulesCacheExpiresAt) && s.skipRulesCache != nil {
		cached := cloneDataShareCaptureSkipRules(s.skipRulesCache)
		s.skipRulesMu.RUnlock()
		return cached, nil
	}
	s.skipRulesMu.RUnlock()

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingCaptureSkipRules)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			rules := defaultDataShareCaptureSkipRules()
			s.storeCaptureSkipRulesCache(rules)
			return rules, nil
		}
		return nil, err
	}
	var rules []DataShareCaptureSkipRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		slog.Warn("data sharing: invalid capture skip rules json, fallback to defaults", "error", err)
		rules = defaultDataShareCaptureSkipRules()
		s.storeCaptureSkipRulesCache(rules)
		return rules, nil
	}
	normalized, err := normalizeDataShareCaptureSkipRules(rules)
	if err != nil {
		slog.Warn("data sharing: invalid capture skip rules config, fallback to defaults", "error", err)
		normalized = defaultDataShareCaptureSkipRules()
	}
	s.storeCaptureSkipRulesCache(normalized)
	return cloneDataShareCaptureSkipRules(normalized), nil
}

func (s *DataSharingService) storeCaptureSkipRulesCache(rules []DataShareCaptureSkipRule) {
	if s == nil {
		return
	}
	s.skipRulesMu.Lock()
	defer s.skipRulesMu.Unlock()
	s.skipRulesCache = cloneDataShareCaptureSkipRules(rules)
	s.skipRulesCacheExpiresAt = time.Now().Add(dataShareSkipRulesCacheTTL)
}

func (s *DataSharingService) clearCaptureSkipRulesCache() {
	if s == nil {
		return
	}
	s.skipRulesMu.Lock()
	defer s.skipRulesMu.Unlock()
	s.skipRulesCache = nil
	s.skipRulesCacheExpiresAt = time.Time{}
}

func defaultDataShareCaptureSkipRules() []DataShareCaptureSkipRule {
	return []DataShareCaptureSkipRule{
		{
			ID:             "claude_code_title",
			Name:           "Claude Code 标题生成",
			Enabled:        true,
			ClientFamilies: []string{"claude-cli"},
			RequestPaths:   []string{"/v1/messages"},
			FieldScopes:    []string{"system"},
			Patterns:       []string{"Generate a concise, sentence-case title"},
			MatchMode:      dataShareSkipRuleMatchContains,
		},
		{
			ID:             "opencode_title_system",
			Name:           "opencode 标题生成系统提示",
			Enabled:        true,
			ClientFamilies: []string{"opencode"},
			RequestPaths:   []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:    []string{"system"},
			Patterns: []string{
				"You are a title generator. You output ONLY a thread title. Nothing else.",
				"Generate a brief title that would help the user find this conversation later.",
				"NEVER respond to questions, just generate a title for the conversation",
			},
			MatchMode: dataShareSkipRuleMatchContains,
		},
		{
			ID:             "opencode_title_user_prompt",
			Name:           "opencode 标题生成用户提示",
			Enabled:        true,
			ClientFamilies: []string{"opencode"},
			RequestPaths:   []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:    []string{"messages", "input"},
			Patterns:       []string{"Generate a title for this conversation:"},
			MatchMode:      dataShareSkipRuleMatchContains,
		},
		{
			ID:           "agent_title_from_messages",
			Name:         "Agent 会话标题生成",
			Enabled:      true,
			RequestPaths: []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:  []string{"messages", "input"},
			Patterns:     []string{"Please write a 5-10 word title for the following conversation:"},
			MatchMode:    dataShareSkipRuleMatchContains,
		},
		{
			ID:           "agent_topic_title",
			Name:         "Agent 主题标题提取",
			Enabled:      true,
			RequestPaths: []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:  []string{"system", "instructions"},
			Patterns:     []string{"extract a 2-3 word title"},
			MatchMode:    dataShareSkipRuleMatchContains,
		},
		{
			ID:           "agent_warmup",
			Name:         "Agent 预热请求",
			Enabled:      true,
			RequestPaths: []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:  []string{"messages", "input"},
			Patterns:     []string{"Warmup"},
			MatchMode:    dataShareSkipRuleMatchEquals,
		},
		{
			ID:        "excluded_models",
			Name:      "默认排除模型",
			Enabled:   true,
			Models:    []string{"gpt-5.4-mini", "codex-auto-review"},
			MatchMode: dataShareSkipRuleMatchEquals,
		},
	}
}

func normalizeDataShareCaptureSkipRules(rules []DataShareCaptureSkipRule) ([]DataShareCaptureSkipRule, error) {
	out := make([]DataShareCaptureSkipRule, 0, len(rules))
	seenIDs := map[string]struct{}{}
	for _, rule := range rules {
		normalized, err := normalizeDataShareCaptureSkipRule(rule)
		if err != nil {
			return nil, err
		}
		if _, ok := seenIDs[normalized.ID]; ok {
			return nil, ErrDataShareSkipRulesInvalid
		}
		seenIDs[normalized.ID] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeDataShareCaptureSkipRule(rule DataShareCaptureSkipRule) (DataShareCaptureSkipRule, error) {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.Name = strings.TrimSpace(rule.Name)
	rule.MatchMode = strings.ToLower(strings.TrimSpace(rule.MatchMode))
	if rule.MatchMode == "" {
		rule.MatchMode = dataShareSkipRuleMatchContains
	}
	if rule.ID == "" || rule.Name == "" {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	if rule.MatchMode != dataShareSkipRuleMatchContains && rule.MatchMode != dataShareSkipRuleMatchEquals {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	rule.ClientFamilies = uniqueTrimmedStrings(rule.ClientFamilies, func(v string) string {
		return strings.ToLower(normalizeDataShareUserAgent(v))
	})
	rule.RequestPaths = uniqueTrimmedStrings(rule.RequestPaths, func(v string) string {
		return strings.ToLower(normalizeDataShareRequestPath(v))
	})
	rule.Models = uniqueTrimmedStrings(rule.Models, func(v string) string {
		return strings.ToLower(strings.TrimSpace(v))
	})
	rule.FieldScopes = uniqueTrimmedStrings(rule.FieldScopes, func(v string) string {
		return strings.ToLower(strings.TrimSpace(v))
	})
	rule.Patterns = uniqueTrimmedStrings(rule.Patterns, strings.TrimSpace)
	if len(rule.Models) == 0 && len(rule.Patterns) == 0 {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	if len(rule.Patterns) > 0 && len(rule.FieldScopes) == 0 {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	for _, scope := range rule.FieldScopes {
		if !isDataShareSkipScope(scope) {
			return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
		}
	}
	return rule, nil
}

func uniqueTrimmedStrings(values []string, normalize func(string) string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalize(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isDataShareSkipScope(scope string) bool {
	switch scope {
	case "system", "messages", "input", "instructions":
		return true
	default:
		return false
	}
}

func cloneDataShareCaptureSkipRules(rules []DataShareCaptureSkipRule) []DataShareCaptureSkipRule {
	out := make([]DataShareCaptureSkipRule, 0, len(rules))
	for _, rule := range rules {
		cloned := rule
		cloned.ClientFamilies = append([]string(nil), rule.ClientFamilies...)
		cloned.RequestPaths = append([]string(nil), rule.RequestPaths...)
		cloned.Models = append([]string(nil), rule.Models...)
		cloned.FieldScopes = append([]string(nil), rule.FieldScopes...)
		cloned.Patterns = append([]string(nil), rule.Patterns...)
		out = append(out, cloned)
	}
	return out
}

func dataShareCaptureSkipRulesMatch(input DataShareCaptureInput, rules []DataShareCaptureSkipRule) bool {
	texts := dataShareSkipCandidateTexts(input.RequestBody)
	clientFamily := strings.ToLower(normalizeDataShareUserAgent(input.UserAgent))
	requestPath := strings.ToLower(normalizeDataShareRequestPath(input.InboundEndpoint))
	models := dataShareSkipCandidateModels(input)
	for _, rule := range rules {
		if !rule.Enabled ||
			!dataShareSkipRuleApplies(rule.ClientFamilies, clientFamily) ||
			!dataShareSkipRuleApplies(rule.RequestPaths, requestPath) ||
			!dataShareSkipRuleModelsApply(rule.Models, models) {
			continue
		}
		if len(rule.Patterns) == 0 && len(rule.Models) > 0 {
			return true
		}
		for _, scope := range rule.FieldScopes {
			for _, text := range texts[scope] {
				if dataShareSkipRuleTextMatches(rule, text) {
					return true
				}
			}
		}
	}
	return false
}

func dataShareSkipCandidateModels(input DataShareCaptureInput) []string {
	candidates := []string{
		input.UpstreamModel,
		input.Model,
		gjson.GetBytes(input.RequestBody, "model").String(),
	}
	return uniqueTrimmedStrings(candidates, func(v string) string {
		return strings.ToLower(strings.TrimSpace(v))
	})
}

func dataShareSkipRuleApplies(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func dataShareSkipRuleModelsApply(allowed []string, models []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, allowedModel := range allowed {
		for _, model := range models {
			if strings.EqualFold(strings.TrimSpace(allowedModel), strings.TrimSpace(model)) {
				return true
			}
		}
	}
	return false
}

func dataShareSkipRuleTextMatches(rule DataShareCaptureSkipRule, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, pattern := range rule.Patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		left, right := text, pattern
		if !rule.CaseSensitive {
			left = strings.ToLower(left)
			right = strings.ToLower(right)
		}
		switch rule.MatchMode {
		case dataShareSkipRuleMatchEquals:
			if left == right {
				return true
			}
		default:
			if strings.Contains(left, right) {
				return true
			}
		}
	}
	return false
}

func dataShareSkipCandidateTexts(body []byte) map[string][]string {
	out := map[string][]string{
		"system":       {},
		"messages":     {},
		"input":        {},
		"instructions": {},
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return out
	}
	add := func(scope string, value any) {
		if text := strings.TrimSpace(dataShareContentText(value)); text != "" {
			out[scope] = append(out[scope], text)
		}
	}
	add("system", payload["system"])
	add("system", payload["system_instruction"])
	add("instructions", payload["instructions"])
	add("instructions", payload["system_instruction"])
	add("input", payload["input"])
	appendDataShareSkipResponsesInput(out, payload["input"])
	appendDataShareSkipMessages(out, payload["messages"])
	appendDataShareSkipContents(out, payload["contents"])
	return out
}

func appendDataShareSkipResponsesInput(out map[string][]string, raw any) {
	for _, item := range anySlice(raw) {
		msg, ok := mapFromAny(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(msg["role"])))
		if role == "system" || role == "developer" {
			out["system"] = append(out["system"], text)
		}
	}
}

func appendDataShareSkipMessages(out map[string][]string, raw any) {
	for _, item := range anySlice(raw) {
		msg, ok := mapFromAny(item)
		if !ok {
			if text := strings.TrimSpace(dataShareContentText(item)); text != "" {
				out["messages"] = append(out["messages"], text)
			}
			continue
		}
		text := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(msg["role"])))
		if role == "system" || role == "developer" {
			out["system"] = append(out["system"], text)
			continue
		}
		out["messages"] = append(out["messages"], text)
	}
}

func appendDataShareSkipContents(out map[string][]string, raw any) {
	for _, item := range anySlice(raw) {
		msg, ok := mapFromAny(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["parts"], msg["content"], msg["text"])))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(msg["role"])))
		if role == "system" || role == "developer" {
			out["system"] = append(out["system"], text)
			continue
		}
		out["messages"] = append(out["messages"], text)
	}
}

// CaptureClaudeRequest 采集 Claude/Gemini 兼容协议成功请求。
func (s *DataSharingService) CaptureClaudeRequest(ctx context.Context, input DataShareCaptureInput) error {
	session := s.buildBufferedCaptureSession(ctx, input)
	if session == nil {
		return nil
	}
	return s.submitCaptureSessionToBuffer(ctx, session)
}

// CaptureClaudeRequestAsync 异步提交 Claude/Gemini 兼容协议数据共享采集。
func (s *DataSharingService) CaptureClaudeRequestAsync(input DataShareCaptureInput) DataSharingCaptureSubmitMode {
	return s.submitCaptureJob(DataSharingCaptureProtocolClaude, input)
}

// CaptureOpenAIRequest 采集 OpenAI 协议成功请求。
func (s *DataSharingService) CaptureOpenAIRequest(ctx context.Context, input DataShareCaptureInput) error {
	session := s.buildBufferedCaptureSession(ctx, input)
	if session == nil {
		return nil
	}
	return s.submitCaptureSessionToBuffer(ctx, session)
}

func (s *DataSharingService) buildBufferedCaptureSession(ctx context.Context, input DataShareCaptureInput) *DataShareSession {
	// 缓冲池最终只落库合并后的快照，入缓冲时跳过质量评估和 payload marshal，降低热点 session CPU 消耗。
	start := time.Now()
	defer func() {
		if s != nil && s.captureDurations != nil {
			s.captureDurations.Observe(DataShareCaptureDurationPartCaptureBuild, time.Since(start))
		}
	}()
	return s.buildCaptureSessionWithOptions(ctx, input, dataShareBuildSessionOptions{FinalizeQuality: false})
}

func (s *DataSharingService) buildCaptureSessionWithOptions(ctx context.Context, input DataShareCaptureInput, opts dataShareBuildSessionOptions) *DataShareSession {
	if input.APIKey == nil || input.APIKey.Group == nil || !input.APIKey.Group.DataSharingEnabled {
		return nil
	}
	if s == nil || s.repo == nil {
		return nil
	}
	if s.shouldSkipDataShareCapture(ctx, input) {
		return nil
	}
	if input.Model == "" && input.UpstreamModel != "" {
		input.Model = input.UpstreamModel
	}
	if dataShareCaptureInputIsOpenAIResponses(input) && !opts.FinalizeQuality {
		return s.buildOpenAIResponsesRawCaptureSession(input)
	}
	return s.buildSessionWithOptions(input, opts)
}

// CaptureOpenAIRequestAsync 异步提交 OpenAI 协议数据共享采集。
func (s *DataSharingService) CaptureOpenAIRequestAsync(input DataShareCaptureInput) DataSharingCaptureSubmitMode {
	return s.submitCaptureJob(DataSharingCaptureProtocolOpenAI, input)
}

func (s *DataSharingService) submitCaptureJob(protocol DataSharingCaptureProtocol, input DataShareCaptureInput) DataSharingCaptureSubmitMode {
	if s == nil || s.repo == nil {
		return DataSharingCaptureSubmitModeDropped
	}
	if input.APIKey == nil || input.APIKey.Group == nil || !input.APIKey.Group.DataSharingEnabled {
		return DataSharingCaptureSubmitModeDropped
	}
	if input.Model == "" && input.UpstreamModel != "" {
		input.Model = input.UpstreamModel
	}
	metadata := dataShareCaptureMetadata(input)
	if s.captureWorker == nil {
		s.recordMissingCaptureWorkerDrop(metadata)
		return DataSharingCaptureSubmitModeDropped
	}
	return s.captureWorker.Submit(DataSharingCaptureJob{
		Protocol: protocol,
		Input:    input,
		Metadata: metadata,
	})
}

func (s *DataSharingService) handleCaptureJob(ctx context.Context, job DataSharingCaptureJob) error {
	if s == nil {
		return nil
	}
	return s.captureRequestFromJob(ctx, job)
}

func (s *DataSharingService) captureRequestFromJob(ctx context.Context, job DataSharingCaptureJob) error {
	session := s.buildBufferedCaptureSession(ctx, job.Input)
	if session == nil {
		return nil
	}
	// 只把解析后的轻量增量放入进程内缓冲，容量检查和压缩落库延后到 flush 阶段执行。
	return s.submitCaptureSessionToBuffer(ctx, session)
}

func (s *DataSharingService) flushBufferedCaptureSession(ctx context.Context, session *DataShareSession) error {
	if s == nil || s.repo == nil {
		return nil
	}
	return s.withDataSharePersistenceRetry(ctx, "flush", func(attemptCtx context.Context) error {
		return s.repo.SaveCaptureSnapshot(attemptCtx, session, s.captureStorageLimitOption(attemptCtx))
	})
}

func (s *DataSharingService) hydrateBufferedCaptureSession(ctx context.Context, key string) (*DataShareSession, error) {
	if s == nil || s.repo == nil {
		return nil, ErrDataShareSessionNotFound
	}
	var session *DataShareSession
	err := s.withDataSharePersistenceRetry(ctx, "hydrate", func(attemptCtx context.Context) error {
		var err error
		session, err = s.repo.GetCaptureByTrajectoryIDWithPayload(attemptCtx, key)
		return err
	})
	return session, err
}

func (s *DataSharingService) scheduleBufferedCaptureFlush(job DataSharingCaptureJob) DataSharingCaptureSubmitMode {
	if s == nil || s.captureWorker == nil {
		return DataSharingCaptureSubmitModeDropped
	}
	return s.captureWorker.SubmitFlush(job)
}

func (s *DataSharingService) submitCaptureSessionToBuffer(ctx context.Context, session *DataShareSession) error {
	if s == nil {
		return nil
	}
	if s.captureBuffer == nil {
		return s.flushBufferedCaptureSession(ctx, finalizeBufferedDataShareSession(session))
	}
	return s.captureBuffer.Submit(ctx, session)
}

func (s *DataSharingService) withDataSharePersistenceRetry(ctx context.Context, operation string, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	delay := dataSharePersistenceRetryInitialDelay
	for attempt := 1; attempt <= dataSharePersistenceRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if attempt == dataSharePersistenceRetryAttempts || !isDataShareTransientPersistenceError(err) {
			return err
		}
		// 数据共享采集是后台 fail-open 链路，数据库旧连接被重置时短暂退避后重试完整幂等 upsert。
		slog.Warn("data sharing: retry transient persistence error",
			"operation", operation,
			"attempt", attempt,
			"error", err,
		)
		if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
			return sleepErr
		}
		delay *= 2
	}
	return nil
}

func isDataShareTransientPersistenceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range []string{
		"connection reset by peer",
		"broken pipe",
		"driver: bad connection",
		"bad connection",
		"server closed the connection unexpectedly",
		"terminating connection due to administrator command",
		"connection refused",
		"use of closed network connection",
		"unexpected eof",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// Stop 停止数据共享采集缓冲池，正常退出时尽量把内存中的增量落库。
func (s *DataSharingService) Stop(ctx context.Context) {
	if s == nil {
		return
	}
	if s.captureWorker != nil {
		s.captureWorker.Stop()
	}
	if s.captureBuffer != nil {
		s.captureBuffer.Stop(ctx)
	}
}

func (s *DataSharingService) recordMissingCaptureWorkerDrop(metadata DataSharingCaptureJobMetadata) {
	if s == nil {
		return
	}
	s.captureWorkerNilDropped.Add(1)
	now := time.Now().UnixNano()
	last := s.captureWorkerNilLogNanos.Load()
	if now-last < int64(dataSharingCaptureDropLogInterval) {
		return
	}
	if !s.captureWorkerNilLogNanos.CompareAndSwap(last, now) {
		return
	}
	slog.Warn(
		"data_sharing.capture_dropped",
		"reason", "missing_worker",
		"provider", metadata.Provider,
		"model", metadata.Model,
		"request_id", metadata.RequestID,
		"api_key_id", metadata.APIKeyID,
		"account_id", metadata.AccountID,
		"group_id", metadata.GroupID,
		"dropped_total", s.captureWorkerNilDropped.Load(),
	)
}

func dataShareCaptureMetadata(input DataShareCaptureInput) DataSharingCaptureJobMetadata {
	metadata := DataSharingCaptureJobMetadata{
		Provider:  input.Provider,
		Model:     firstNonBlank(input.UpstreamModel, input.Model),
		RequestID: input.RequestID,
	}
	if input.APIKey != nil {
		metadata.APIKeyID = input.APIKey.ID
		if input.APIKey.GroupID != nil {
			metadata.GroupID = *input.APIKey.GroupID
		} else if input.APIKey.Group != nil {
			metadata.GroupID = input.APIKey.Group.ID
		}
	}
	if input.Account != nil {
		metadata.AccountID = input.Account.ID
	}
	return metadata
}

// ListSessions 查询数据共享 session。
func (s *DataSharingService) ListSessions(ctx context.Context, params pagination.PaginationParams, filters DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error) {
	return s.repo.List(ctx, params, filters)
}

// GetSession 查询单条 session，并可选限制 userID。
func (s *DataSharingService) GetSession(ctx context.Context, id int64, userID int64) (*DataShareSession, error) {
	session, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if userID > 0 && session.UserID != userID {
		return nil, ErrDataShareSessionNotFound
	}
	return session, nil
}

func (s *DataSharingService) DeleteSession(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *DataSharingService) BatchDeleteSessions(ctx context.Context, ids []int64, filters DataShareSessionFilters) (int64, error) {
	return s.repo.BatchDelete(ctx, ids, filters)
}

func (s *DataSharingService) Stats(ctx context.Context, filters DataShareSessionFilters) (*DataShareStats, error) {
	stats, err := s.repo.Stats(ctx, filters)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		stats = &DataShareStats{}
	}
	stats.CaptureWorker = s.CaptureWorkerStats()
	stats.CaptureBuffer = s.CaptureBufferStats()
	stats.CaptureDurations = s.CaptureDurationStats()
	return stats, nil
}

// CaptureWorkerStats 返回数据共享采集池运行时统计。
func (s *DataSharingService) CaptureWorkerStats() DataSharingCaptureWorkerPoolStats {
	if s == nil {
		return DataSharingCaptureWorkerPoolStats{}
	}
	if s.captureWorker == nil {
		return DataSharingCaptureWorkerPoolStats{
			DroppedTotal: s.captureWorkerNilDropped.Load(),
		}
	}
	return s.captureWorker.Stats()
}

// CaptureBufferStats 返回数据共享采集缓冲池运行时统计。
func (s *DataSharingService) CaptureBufferStats() DataSharingCaptureBufferStats {
	if s == nil || s.captureBuffer == nil {
		return DataSharingCaptureBufferStats{}
	}
	return s.captureBuffer.Stats()
}

// CaptureDurationStats 返回数据共享采集链路的进程内耗时统计。
func (s *DataSharingService) CaptureDurationStats() DataShareCaptureDurationStats {
	if s == nil || s.captureDurations == nil {
		return DataShareCaptureDurationStats{WindowSize: defaultDataSharingCaptureDurationWindowSize}
	}
	return s.captureDurations.Snapshot()
}

func (s *DataSharingService) FilterOptions(ctx context.Context, filters DataShareSessionFilters) (*DataShareSessionFilterOptions, error) {
	return s.repo.FilterOptions(ctx, filters)
}

// CreateExportTicket 为大文件下载签发短期票据，避免浏览器用 Blob 缓存完整导出文件。
func (s *DataSharingService) CreateExportTicket(ctx context.Context, req DataShareExportTicketRequest) (*DataShareExportTicket, error) {
	if err := validateDataShareExportTicketRequest(req); err != nil {
		return nil, err
	}
	key, err := s.exportTicketSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(dataShareExportTicketTTL)
	encoding := normalizeDataShareExportEncoding(req.Encoding)
	claims := DataShareExportTicketClaims{
		Scope:     req.Scope,
		UserID:    req.UserID,
		Filters:   req.Filters,
		Filename:  normalizeDataShareExportFilename(req.Filename, encoding),
		Encoding:  encoding,
		ExpiresAt: expiresAt.Unix(),
	}
	token, err := signDataShareExportTicket(claims, key)
	if err != nil {
		return nil, err
	}
	return &DataShareExportTicket{
		Token:       token,
		DownloadURL: dataShareExportDownloadURL(req.Scope, token),
		Filename:    claims.Filename,
		Encoding:    string(claims.Encoding),
		ExpiresAt:   expiresAt,
	}, nil
}

// ParseExportTicket 校验短期下载票据并返回导出上下文。
func (s *DataSharingService) ParseExportTicket(ctx context.Context, scope DataShareExportScope, token string) (*DataShareExportTicketClaims, error) {
	key, err := s.exportTicketSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	claims, err := parseDataShareExportTicket(token, key)
	if err != nil {
		return nil, err
	}
	if claims.Scope != scope {
		return nil, ErrDataShareExportTicketForbidden
	}
	if claims.ExpiresAt <= 0 || time.Now().Unix() > claims.ExpiresAt {
		return nil, ErrDataShareExportTicketInvalid
	}
	if err := validateDataShareExportTicketRequest(DataShareExportTicketRequest{
		Scope:    claims.Scope,
		UserID:   claims.UserID,
		Filters:  claims.Filters,
		Filename: claims.Filename,
		Encoding: claims.Encoding,
	}); err != nil {
		return nil, err
	}
	claims.Encoding = normalizeDataShareExportEncoding(claims.Encoding)
	claims.Filename = normalizeDataShareExportFilename(claims.Filename, claims.Encoding)
	return claims, nil
}

// ExportJSONL 导出选中的数据共享 session；显式选中的记录保留原始快照，不再因质量状态跳过。
func (s *DataSharingService) ExportJSONL(ctx context.Context, w io.Writer, filters DataShareSessionFilters, includeNonExportable bool) error {
	_ = includeNonExportable
	params := pagination.PaginationParams{Page: 1, PageSize: 1000, SortBy: "created_at", SortOrder: pagination.SortOrderAsc}
	for {
		items, result, err := s.repo.ListWithPayload(ctx, params, filters)
		if err != nil {
			return err
		}
		for i := range items {
			payload, err := exportDownloadPayloadFromSession(&items[i])
			if err != nil {
				if errors.Is(err, ErrDataShareExportPayloadInvalid) && (filters.SelectAll || len(filters.IDs) != 1) {
					slog.Warn("data sharing: skip session failed export recheck",
						"trajectory_id", items[i].TrajectoryID,
						"session_id", items[i].SessionID,
						"quality_status", items[i].QualityStatus,
						"error", err,
					)
					continue
				}
				return err
			}
			line, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if _, err := w.Write(append(line, '\n')); err != nil {
				return err
			}
		}
		if result == nil || params.Page >= result.Pages || len(items) == 0 {
			return nil
		}
		params.Page++
	}
}

func validateDataShareExportTicketRequest(req DataShareExportTicketRequest) error {
	switch req.Scope {
	case DataShareExportScopeUser:
		if req.UserID <= 0 {
			return ErrDataShareExportTicketInvalid
		}
		if req.Filters.UserID != 0 && req.Filters.UserID != req.UserID {
			return ErrDataShareExportTicketForbidden
		}
	case DataShareExportScopeAdmin:
	default:
		return ErrDataShareExportTicketInvalid
	}
	if req.Filters.SelectAll {
		return nil
	}
	if len(req.Filters.IDs) == 0 {
		return ErrDataShareExportTicketInvalid
	}
	return nil
}

func (s *DataSharingService) exportTicketSigningKey(ctx context.Context) ([]byte, error) {
	if s == nil || s.settingRepo == nil {
		return []byte("tokenrouter-data-sharing-export-ticket-dev-key"), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingExportTicketKey)
	if err == nil && strings.TrimSpace(raw) != "" {
		return []byte(strings.TrimSpace(raw)), nil
	}
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	secret := base64.RawURLEncoding.EncodeToString(buf)
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingExportTicketKey, secret); err != nil {
		return nil, err
	}
	return []byte(secret), nil
}

func signDataShareExportTicket(claims DataShareExportTicketClaims, key []byte) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	return encodedPayload + "." + signDataShareExportTicketPayload(encodedPayload, key), nil
}

func parseDataShareExportTicket(token string, key []byte) (*DataShareExportTicketClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrDataShareExportTicketInvalid
	}
	expected := signDataShareExportTicketPayload(parts[0], key)
	if !hmac.Equal([]byte(parts[1]), []byte(expected)) {
		return nil, ErrDataShareExportTicketInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrDataShareExportTicketInvalid
	}
	var claims DataShareExportTicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrDataShareExportTicketInvalid
	}
	return &claims, nil
}

func signDataShareExportTicketPayload(payload string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func dataShareExportDownloadURL(scope DataShareExportScope, token string) string {
	if scope == DataShareExportScopeAdmin {
		return "/api/v1/admin/data-sharing/export/download?ticket=" + token
	}
	return "/api/v1/data-sharing/export/download?ticket=" + token
}

func normalizeDataShareExportEncoding(encoding DataShareExportEncoding) DataShareExportEncoding {
	switch encoding {
	case DataShareExportEncodingJSON:
		return DataShareExportEncodingJSON
	case DataShareExportEncodingJSONL:
		return DataShareExportEncodingJSONL
	default:
		return DataShareExportEncodingZstd
	}
}

func normalizeDataShareExportFilename(filename string, encoding DataShareExportEncoding) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "data-sharing-" + time.Now().Format("20060102-150405")
	}
	filename = strings.TrimSuffix(filename, ".jsonl.zst")
	filename = strings.TrimSuffix(filename, ".jsonl")
	filename = strings.TrimSuffix(filename, ".json")
	filename = strings.TrimSuffix(filename, ".zst")
	filename = strings.NewReplacer("/", "-", "\\", "-", "\x00", "").Replace(filename)
	switch normalizeDataShareExportEncoding(encoding) {
	case DataShareExportEncodingJSON:
		return filename + ".json"
	case DataShareExportEncodingJSONL:
		return filename + ".jsonl"
	default:
		return filename + ".jsonl.zst"
	}
}

func defaultDataSharingNotice(ctx context.Context, repo SettingRepository) (*DataShareNotice, error) {
	if repo == nil {
		return &DataShareNotice{Content: defaultDataSharingNoticeContent, Version: 1, UpdatedAt: time.Now()}, nil
	}
	settings, err := repo.GetMultiple(ctx, []string{SettingKeyDataSharingNoticeContent, SettingKeyDataSharingNoticeVersion})
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(settings[SettingKeyDataSharingNoticeContent])
	if content == "" {
		content = defaultDataSharingNoticeContent
	}
	version, _ := strconv.Atoi(strings.TrimSpace(settings[SettingKeyDataSharingNoticeVersion]))
	if version <= 0 {
		version = 1
	}
	return &DataShareNotice{Content: content, Version: version, UpdatedAt: time.Now()}, nil
}

type dataShareBuildSessionOptions struct {
	FinalizeQuality bool
}

func (s *DataSharingService) buildSession(input DataShareCaptureInput) *DataShareSession {
	return s.buildSessionWithOptions(input, dataShareBuildSessionOptions{FinalizeQuality: true})
}

func (s *DataSharingService) buildOpenAIResponsesRawCaptureSession(input DataShareCaptureInput) *DataShareSession {
	now := time.Now()
	groupID := int64(0)
	if input.APIKey != nil && input.APIKey.GroupID != nil {
		groupID = *input.APIKey.GroupID
	} else if input.APIKey != nil && input.APIKey.Group != nil {
		groupID = input.APIKey.Group.ID
	}
	userID := int64(0)
	if input.User != nil {
		userID = input.User.ID
	} else if input.APIKey != nil {
		userID = input.APIKey.UserID
	}
	apiKeyID := int64(0)
	if input.APIKey != nil {
		apiKeyID = input.APIKey.ID
	}
	provider := normalizeDataShareProvider(input.Provider, input.APIKey)
	model := resolveDataShareActualModel(input)
	requestPath := normalizeDataShareRequestPath(input.InboundEndpoint)
	userAgent := normalizeDataShareUserAgent(input.UserAgent)
	sessionID := normalizeDataShareSessionID(input.SessionID, input.RequestID, input.RequestBody, apiKeyID)
	trajectoryID := buildTrajectoryID(provider, sessionID, apiKeyID, groupID)
	usage := buildCaptureUsage(input)
	meta := buildCaptureMeta(input)
	if input.Turn > 0 {
		meta["turn"] = input.Turn
	}
	if input.CaptureIncomplete {
		meta["capture_incomplete"] = true
	}
	systemPrompt := strings.TrimSpace(input.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = extractSystemPromptFromRequest(input.RequestBody)
	}
	inputCopy := cloneDataShareCaptureInput(input)
	requestItems := normalizeOpenAIResponsesRequestInputItems(input.RequestBody)
	responseItems := appendAssistantMessageFromResponse(nil, input.ResponseBody)
	return &DataShareSession{
		TrajectoryID:         trajectoryID,
		SessionID:            sessionID,
		Dataset:              defaultDataShareDataset,
		Provider:             provider,
		Model:                model,
		RequestPath:          requestPath,
		UserAgent:            userAgent,
		Status:               DataShareStatusTerminated,
		IsFinalSnapshot:      false,
		SourceRequestCount:   1,
		SystemPrompt:         optionalDataShareString(systemPrompt),
		Tools:                normalizeCaptureTools(input),
		Usage:                usage,
		Meta:                 meta,
		QualityStatus:        DataShareQualityInvalid,
		Exportable:           false,
		InputTokens:          int64(input.InputTokens + input.CacheReadTokens + input.CacheCreateTokens),
		OutputTokens:         int64(input.OutputTokens),
		TotalTokens:          int64(input.InputTokens + input.CacheReadTokens + input.CacheCreateTokens + input.OutputTokens),
		ActualCost:           cloneFloat64Ptr(input.ActualCost),
		UserID:               userID,
		APIKeyID:             apiKeyID,
		GroupID:              groupID,
		CreatedAt:            now,
		EndedAt:              &now,
		UpdatedAt:            now,
		captureMode:          dataShareCaptureModeOpenAIResponsesRaw,
		captureInput:         &inputCopy,
		captureRequestItems:  cloneDataShareResponsesInputItems(requestItems),
		captureResponseItems: cloneBufferedDataShareMaps(responseItems),
	}
}

func dataShareCaptureInputIsOpenAIResponses(input DataShareCaptureInput) bool {
	if input.CaptureMode == dataShareCaptureModeOpenAIResponsesRaw {
		return true
	}
	if normalizeDataShareRequestPath(input.InboundEndpoint) != "/v1/responses" {
		return false
	}
	if len(input.RequestBody) == 0 {
		return false
	}
	return gjson.GetBytes(input.RequestBody, "input").Exists()
}

func optionalDataShareString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	out := value
	return &out
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneDataShareCaptureInput(input DataShareCaptureInput) DataShareCaptureInput {
	clone := input
	clone.RequestBody = cloneDataSharingRequestBody(input.RequestBody)
	clone.ResponseBody = cloneDataSharingRequestBody(input.ResponseBody)
	if input.ActualCost != nil {
		actualCost := *input.ActualCost
		clone.ActualCost = &actualCost
	}
	if len(input.Messages) > 0 {
		clone.Messages = append([]any(nil), input.Messages...)
	}
	clone.Tools = cloneBufferedDataShareMaps(input.Tools)
	return clone
}

func (s *DataSharingService) buildSessionWithOptions(input DataShareCaptureInput, opts dataShareBuildSessionOptions) *DataShareSession {
	now := time.Now()
	groupID := int64(0)
	if input.APIKey != nil && input.APIKey.GroupID != nil {
		groupID = *input.APIKey.GroupID
	} else if input.APIKey != nil && input.APIKey.Group != nil {
		groupID = input.APIKey.Group.ID
	}
	userID := int64(0)
	if input.User != nil {
		userID = input.User.ID
	} else if input.APIKey != nil {
		userID = input.APIKey.UserID
	}
	apiKeyID := int64(0)
	if input.APIKey != nil {
		apiKeyID = input.APIKey.ID
	}
	provider := normalizeDataShareProvider(input.Provider, input.APIKey)
	model := resolveDataShareActualModel(input)
	requestPath := normalizeDataShareRequestPath(input.InboundEndpoint)
	userAgent := normalizeDataShareUserAgent(input.UserAgent)
	sessionID := normalizeDataShareSessionID(input.SessionID, input.RequestID, input.RequestBody, apiKeyID)
	trajectoryID := buildTrajectoryID(provider, sessionID, apiKeyID, groupID)
	messages := normalizeCaptureMessages(input)
	tools := normalizeCaptureTools(input)
	usage := buildCaptureUsage(input)
	meta := buildCaptureMeta(input)
	systemPrompt := strings.TrimSpace(input.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = extractSystemPromptFromRequest(input.RequestBody)
	}
	if systemPrompt == "" {
		systemPrompt = extractSystemPromptFromMessages(messages)
	}
	qualityStatus := DataShareQualityInvalid
	qualityErrors := []string(nil)
	status := DataShareStatusTerminated
	finalSnapshot := false
	if opts.FinalizeQuality {
		qualityReport := evaluateDataShareSessionQuality(model, systemPrompt, messages, tools, usage)
		qualityErrors = qualityReport.Errors
		qualityStatus = qualityReport.Status
		status, finalSnapshot = dataShareCompletionState(qualityStatus)
	}
	sessionJSON := map[string]any{
		"trajectory_id":        trajectoryID,
		"session_id":           sessionID,
		"dataset":              defaultDataShareDataset,
		"provider":             provider,
		"model":                model,
		"request_path":         requestPath,
		"user_agent":           userAgent,
		"created_at":           now.Format(time.RFC3339Nano),
		"ended_at":             now.Format(time.RFC3339Nano),
		"status":               status,
		"is_final_snapshot":    finalSnapshot,
		"source_request_count": 1,
		"quality_status":       qualityStatus,
		"system_prompt":        systemPrompt,
		"tools":                tools,
		"messages":             messages,
		"usage":                usage,
		"meta":                 meta,
	}
	storageBytes := int64(0)
	if opts.FinalizeQuality {
		storageBytes = int64(len(mustJSON(sessionJSON)))
	} else {
		sessionJSON = nil
	}
	var sysPtr *string
	if systemPrompt != "" {
		sysPtr = &systemPrompt
	}
	inputTokens := int64(input.InputTokens + input.CacheReadTokens + input.CacheCreateTokens)
	outputTokens := int64(input.OutputTokens)
	var actualCost *float64
	if input.ActualCost != nil {
		cost := *input.ActualCost
		actualCost = &cost
	}
	return &DataShareSession{
		TrajectoryID:       trajectoryID,
		SessionID:          sessionID,
		Dataset:            defaultDataShareDataset,
		Provider:           provider,
		Model:              model,
		RequestPath:        requestPath,
		UserAgent:          userAgent,
		Status:             status,
		IsFinalSnapshot:    finalSnapshot,
		SourceRequestCount: 1,
		SystemPrompt:       sysPtr,
		Tools:              tools,
		Messages:           messages,
		Usage:              usage,
		Meta:               meta,
		SessionJSON:        sessionJSON,
		Exportable:         DataShareQualityExportable(qualityStatus),
		QualityStatus:      qualityStatus,
		QualityErrors:      qualityErrors,
		StorageBytes:       storageBytes,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		TotalTokens:        inputTokens + outputTokens,
		ActualCost:         actualCost,
		UserID:             userID,
		APIKeyID:           apiKeyID,
		GroupID:            groupID,
		CreatedAt:          now,
		EndedAt:            &now,
		UpdatedAt:          now,
	}
}

func normalizeCaptureMessages(input DataShareCaptureInput) []map[string]any {
	var out []map[string]any
	if len(input.Messages) > 0 {
		out = appendAnyMessages(out, input.Messages)
	}
	if len(out) == 0 && len(input.RequestBody) > 0 {
		out = appendRequestMessages(out, input.RequestBody)
	}
	if len(input.ResponseBody) > 0 {
		out = appendAssistantMessageFromResponse(out, input.ResponseBody)
	}
	return normalizeDataShareMessages(out)
}

func appendAnyMessages(out []map[string]any, messages []any) []map[string]any {
	for _, msg := range messages {
		switch v := msg.(type) {
		case map[string]any:
			out = append(out, v)
		default:
			out = append(out, map[string]any{"role": "unknown", "content": v})
		}
	}
	return out
}

func appendRequestMessages(out []map[string]any, body []byte) []map[string]any {
	startLen := len(out)
	if arr := gjson.GetBytes(body, "messages"); arr.IsArray() {
		for _, item := range arr.Array() {
			out = append(out, rawJSONToMap(item.Raw))
		}
	}
	if arr := gjson.GetBytes(body, "contents"); arr.IsArray() {
		for _, item := range arr.Array() {
			msg := rawJSONToMap(item.Raw)
			if role, ok := msg["role"].(string); ok && role == "model" {
				msg["role"] = "assistant"
			}
			out = append(out, msg)
		}
	}
	if len(out) == startLen {
		// OpenAI Responses 使用 input 承载对话上下文，Codex CLI 会走这条协议。
		out = appendResponsesInputMessages(out, gjson.GetBytes(body, "input"))
	}
	return out
}

func appendResponsesInputMessages(out []map[string]any, input gjson.Result) []map[string]any {
	if !input.Exists() {
		return out
	}
	if input.Type == gjson.String {
		return append(out, map[string]any{"role": "user", "content": input.String()})
	}
	if input.IsObject() {
		return append(out, normalizeResponsesInputItem(input))
	}
	if !input.IsArray() {
		return out
	}
	for _, item := range input.Array() {
		if item.Type == gjson.String {
			out = append(out, map[string]any{"role": "user", "content": item.String()})
			continue
		}
		if item.IsObject() {
			out = append(out, normalizeResponsesInputItem(item))
		}
	}
	return out
}

func normalizeResponsesInputItem(item gjson.Result) map[string]any {
	msg := rawJSONToMap(item.Raw)
	role := normalizeResponsesInputRole(item.Get("role").String(), item.Get("type").String())
	if role != "" {
		msg["role"] = role
	}
	itemType := strings.TrimSpace(item.Get("type").String())
	switch itemType {
	case "function_call":
		// 工具调用在对话中等价于 assistant 发起的 tool_call。
		return normalizeResponsesFunctionCallMessage(msg)
	case "function_call_output":
		// 工具执行结果按 tool 消息保存，便于后续训练流水线识别。
		return normalizeToolResultMessage(msg)
	case "input_text", "text":
		msg["role"] = "user"
		if text := item.Get("text"); text.Exists() {
			msg["content"] = text.String()
		}
	}
	if _, ok := msg["content"]; !ok {
		if content := item.Get("content"); content.Exists() {
			msg["content"] = responseInputContentValue(content)
		} else if text := item.Get("text"); text.Exists() {
			msg["content"] = text.String()
		}
	}
	return msg
}

func normalizeResponsesInputRole(role string, itemType string) string {
	role = strings.TrimSpace(role)
	switch role {
	case "developer":
		return "system"
	case "model":
		return "assistant"
	case "":
		switch strings.TrimSpace(itemType) {
		case "function_call":
			return "assistant"
		case "function_call_output":
			return "tool"
		default:
			return "user"
		}
	default:
		return role
	}
}

func responseInputContentValue(value gjson.Result) any {
	if value.Type == gjson.String {
		return value.String()
	}
	return normalizeDataShareContentValue(rawJSONToAny(value.Raw))
}

func appendAssistantMessageFromResponse(out []map[string]any, body []byte) []map[string]any {
	if msg := gjson.GetBytes(body, "choices.0.message"); msg.Exists() {
		out = append(out, rawJSONToMap(msg.Raw))
	}
	if output := gjson.GetBytes(body, "output"); output.IsArray() {
		for _, item := range output.Array() {
			if item.IsObject() {
				out = append(out, normalizeResponsesOutputItem(item))
			}
		}
	}
	if content := gjson.GetBytes(body, "content"); content.IsArray() {
		out = append(out, map[string]any{"role": "assistant", "content": responseInputContentValue(content)})
	}
	if candidates := gjson.GetBytes(body, "candidates.0.content"); candidates.Exists() {
		msg := rawJSONToMap(candidates.Raw)
		msg["role"] = "assistant"
		out = append(out, msg)
	}
	return out
}

type dataShareResponsesCaptureState struct {
	StableIDs        map[string]struct{} `json:"stable_ids,omitempty"`
	ReplayIdentities []string            `json:"replay_identities,omitempty"`
	ResponseKeys     map[string]struct{} `json:"response_keys,omitempty"`
	LastTurn         int                 `json:"last_turn,omitempty"`
	OrderUncertain   bool                `json:"order_uncertain,omitempty"`
}

func (s *DataSharingService) buildOpenAIResponsesIncrementalSession(existing *DataShareSession, raw *DataShareSession) *DataShareSession {
	if raw == nil || raw.captureInput == nil {
		return raw
	}
	out := cloneBufferedDataShareSession(raw)
	out.captureMode = dataShareCaptureModeIncremental
	input := *raw.captureInput
	state := cloneDataShareResponsesCaptureState(captureStateFromDataShareSession(existing))
	if state == nil {
		state = &dataShareResponsesCaptureState{}
	}
	if input.Turn > 0 && state.LastTurn > 0 && input.Turn <= state.LastTurn {
		state.OrderUncertain = true
	}
	requestItems := cloneDataShareResponsesInputItems(raw.captureRequestItems)
	if len(requestItems) == 0 {
		requestItems = normalizeOpenAIResponsesRequestInputItems(input.RequestBody)
	}
	replayPlan, orderUncertain := dataShareBuildResponsesReplayPlan(state, requestItems)
	if orderUncertain {
		state.OrderUncertain = true
	}
	messages := make([]map[string]any, 0, dataShareResponsesReplayPlanKeepCount(replayPlan)+2)
	for index, item := range requestItems {
		if !replayPlan.Keep[index] {
			continue
		}
		messages = append(messages, cloneDataShareMap(item.Message))
	}
	responseStart := len(messages)
	responseItems := cloneBufferedDataShareMaps(raw.captureResponseItems)
	if len(responseItems) == 0 {
		responseItems = appendAssistantMessageFromResponse(nil, input.ResponseBody)
	}
	messages = append(messages, responseItems...)
	messages = dataShareResponsesFilterKnownResponseMessages(state, requestItems, messages, responseStart)
	if input.CaptureIncomplete && len(messages) == responseStart {
		out.Meta["capture_incomplete"] = true
	}
	out.Messages = normalizeDataShareMessages(messages)
	out.captureState = updateDataShareResponsesCaptureState(state, requestItems, replayPlan.Keep, messages[responseStart:], input.Turn)
	out.Meta = withDataShareInternalCaptureState(out.Meta, out.captureState)
	return out
}

type dataShareResponsesInputItem struct {
	Message     map[string]any
	StableID    string
	Identity    string
	IdentityKey string
}

type dataShareResponsesReplayPlan struct {
	Keep []bool
}

func normalizeOpenAIResponsesRequestInputItems(body []byte) []dataShareResponsesInputItem {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return nil
	}
	var out []dataShareResponsesInputItem
	appendItem := func(item gjson.Result) {
		msg := map[string]any(nil)
		if item.Type == gjson.String {
			msg = map[string]any{"role": "user", "content": item.String()}
		} else if item.IsObject() {
			msg = normalizeResponsesInputItem(item)
		}
		if len(msg) == 0 {
			return
		}
		msg = normalizeDataShareMessage(msg)
		identity := dataShareMessageIdentity(msg)
		out = append(out, dataShareResponsesInputItem{
			Message:     msg,
			StableID:    dataShareResponsesStableItemID(item, msg),
			Identity:    identity,
			IdentityKey: dataShareResponsesIdentityKey(identity),
		})
	}
	if input.Type == gjson.String || input.IsObject() {
		appendItem(input)
		return out
	}
	if !input.IsArray() {
		return nil
	}
	for _, item := range input.Array() {
		appendItem(item)
	}
	return out
}

func dataShareResponsesStableItemID(item gjson.Result, msg map[string]any) string {
	itemType := strings.TrimSpace(item.Get("type").String())
	for _, candidate := range []string{
		item.Get("call_id").String(),
		item.Get("tool_call_id").String(),
		item.Get("tool_use_id").String(),
		stringFromAny(msg["tool_call_id"]),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return itemType + ":" + candidate
		}
	}
	if itemType != "message" {
		if id := strings.TrimSpace(item.Get("id").String()); id != "" {
			return itemType + ":" + id
		}
	}
	if calls := anySlice(msg["tool_calls"]); len(calls) > 0 {
		call, _ := mapFromAny(calls[0])
		id := firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"]))
		if id != "" {
			return "function_call:" + id
		}
	}
	return ""
}

func dataShareBuildResponsesReplayPlan(state *dataShareResponsesCaptureState, incoming []dataShareResponsesInputItem) (dataShareResponsesReplayPlan, bool) {
	plan := dataShareResponsesReplayPlan{Keep: make([]bool, len(incoming))}
	for i := range plan.Keep {
		plan.Keep[i] = true
	}
	if state == nil || len(incoming) == 0 {
		return plan, false
	}
	prefixReplay := 0
	for prefixReplay < len(incoming) {
		item := incoming[prefixReplay]
		if item.StableID != "" {
			if _, ok := state.StableIDs[item.StableID]; ok {
				prefixReplay++
				continue
			}
			break
		}
		if prefixReplay < len(state.ReplayIdentities) && state.ReplayIdentities[prefixReplay] == item.IdentityKey {
			prefixReplay++
			continue
		}
		break
	}
	// 没有稳定 id 的部分前缀命中可能是分叉；保留这段前缀，但仍继续扫描后续明确的长窗口 replay。
	prefixOrderUncertain := prefixReplay > 0 && prefixReplay < len(incoming) && !dataShareResponsesPrefixHasStableAnchor(incoming[:prefixReplay])
	if !prefixOrderUncertain {
		for i := 0; i < prefixReplay; i++ {
			plan.Keep[i] = false
		}
	}
	if state == nil || len(state.ReplayIdentities) < dataShareLongReplayMinMessages || len(incoming) < dataShareLongReplayMinMessages {
		return plan, prefixOrderUncertain
	}
	incomingKeys := dataShareResponsesInputIdentityKeys(incoming)
	index := dataShareReplayWindowIndex(state.ReplayIdentities)
	for i := prefixReplay; i < len(incoming); {
		match := dataShareBestIndexedReplayMatch(state.ReplayIdentities, index, incomingKeys, i)
		if match.length < dataShareLongReplayMinMessages {
			i++
			continue
		}
		for end := i + match.length; i < end; i++ {
			plan.Keep[i] = false
		}
	}
	return plan, prefixOrderUncertain
}

func dataShareResponsesReplayPlanKeepCount(plan dataShareResponsesReplayPlan) int {
	count := 0
	for _, keep := range plan.Keep {
		if keep {
			count++
		}
	}
	return count
}

func dataShareResponsesInputIdentityKeys(items []dataShareResponsesInputItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.IdentityKey
	}
	return out
}

func dataShareResponsesPrefixHasStableAnchor(items []dataShareResponsesInputItem) bool {
	for _, item := range items {
		if item.StableID != "" {
			return true
		}
	}
	return false
}

func dataShareResponsesFilterKnownResponseMessages(state *dataShareResponsesCaptureState, requestItems []dataShareResponsesInputItem, messages []map[string]any, responseStart int) []map[string]any {
	if state == nil || len(state.ResponseKeys) == 0 || responseStart >= len(messages) {
		return messages
	}
	out := cloneBufferedDataShareMaps(messages[:responseStart])
	context := dataShareResponsesContextSeed()
	for _, item := range requestItems {
		context = dataShareResponsesAdvanceContext(context, item.IdentityKey)
	}
	for _, msg := range messages[responseStart:] {
		identity := dataShareMessageIdentity(msg)
		identityKey := dataShareResponsesIdentityKey(identity)
		seen := false
		if identity != "" {
			key := dataShareResponsesScopedResponseKey(context, identityKey)
			if _, ok := state.ResponseKeys[key]; ok {
				seen = true
			}
		}
		context = dataShareResponsesAdvanceContext(context, identityKey)
		if seen {
			continue
		}
		out = append(out, cloneDataShareMap(msg))
	}
	return out
}

func updateDataShareResponsesCaptureState(state *dataShareResponsesCaptureState, requestItems []dataShareResponsesInputItem, keepRequestItems []bool, responseMessages []map[string]any, turn int) *dataShareResponsesCaptureState {
	if state == nil {
		state = &dataShareResponsesCaptureState{}
	}
	if state.StableIDs == nil {
		state.StableIDs = map[string]struct{}{}
	}
	if state.ResponseKeys == nil {
		state.ResponseKeys = map[string]struct{}{}
	}
	if len(keepRequestItems) != len(requestItems) {
		keepRequestItems = make([]bool, len(requestItems))
		for i := range keepRequestItems {
			keepRequestItems[i] = true
		}
	}
	context := dataShareResponsesContextSeed()
	for index, item := range requestItems {
		if dataShareResponsesInputItemLooksAssistantOutput(item) {
			// 只记录“前文 + assistant 输出”的固定长度作用域 key，避免误删合法重复回答，也避免 meta 随文本长度膨胀。
			state.ResponseKeys[dataShareResponsesScopedResponseKey(context, item.IdentityKey)] = struct{}{}
		}
		if keepRequestItems[index] {
			if item.StableID != "" {
				state.StableIDs[item.StableID] = struct{}{}
			}
			state.ReplayIdentities = append(state.ReplayIdentities, item.IdentityKey)
		}
		context = dataShareResponsesAdvanceContext(context, item.IdentityKey)
	}
	for _, msg := range responseMessages {
		for _, stableID := range dataShareResponsesStableIDsFromMessage(msg) {
			state.StableIDs[stableID] = struct{}{}
		}
		identity := dataShareMessageIdentity(msg)
		identityKey := dataShareResponsesIdentityKey(identity)
		if identity != "" {
			state.ResponseKeys[dataShareResponsesScopedResponseKey(context, identityKey)] = struct{}{}
		}
		state.ReplayIdentities = append(state.ReplayIdentities, identityKey)
		context = dataShareResponsesAdvanceContext(context, identityKey)
	}
	if turn > state.LastTurn {
		state.LastTurn = turn
	}
	return state
}

func dataShareResponsesIdentityKey(identity string) string {
	if identity == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:16])
}

func cloneDataShareResponsesInputItems(items []dataShareResponsesInputItem) []dataShareResponsesInputItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]dataShareResponsesInputItem, 0, len(items))
	for _, item := range items {
		out = append(out, dataShareResponsesInputItem{
			Message:     cloneDataShareMap(item.Message),
			StableID:    item.StableID,
			Identity:    item.Identity,
			IdentityKey: item.IdentityKey,
		})
	}
	return out
}

func dataShareResponsesContextSeed() string {
	return strings.Repeat("0", 32)
}

func dataShareResponsesAdvanceContext(context string, identityKey string) string {
	sum := sha256.Sum256([]byte(context + "\x00" + identityKey))
	return hex.EncodeToString(sum[:16])
}

func dataShareResponsesScopedResponseKey(context string, responseIdentityKey string) string {
	if responseIdentityKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(context + "\x01" + responseIdentityKey))
	return hex.EncodeToString(sum[:16])
}

func dataShareResponsesInputItemLooksAssistantOutput(item dataShareResponsesInputItem) bool {
	role := strings.TrimSpace(stringFromAny(item.Message["role"]))
	return role == "assistant"
}

func dataShareResponsesStableIDsFromMessage(msg map[string]any) []string {
	if len(msg) == 0 {
		return nil
	}
	var out []string
	role := strings.TrimSpace(stringFromAny(msg["role"]))
	if role == "assistant" {
		for _, raw := range anySlice(msg["tool_calls"]) {
			call, ok := mapFromAny(raw)
			if !ok {
				continue
			}
			id := firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"]))
			if id != "" {
				// Responses 下一轮 input 会把上一轮 output.function_call 用 call_id 回放，必须写入 replay 锚点。
				out = append(out, "function_call:"+id)
			}
		}
	}
	if role == "tool" {
		id := firstNonBlank(stringFromAny(msg["tool_call_id"]), stringFromAny(msg["call_id"]), stringFromAny(msg["tool_use_id"]))
		if id != "" {
			out = append(out, "function_call_output:"+id)
		}
	}
	return out
}

func captureStateFromDataShareSession(session *DataShareSession) *dataShareResponsesCaptureState {
	if session == nil {
		return nil
	}
	if session.captureState != nil {
		return cloneDataShareResponsesCaptureState(session.captureState)
	}
	if meta := mapAnyFromAny(session.Meta[dataShareInternalCaptureMetaKey]); len(meta) > 0 {
		return dataShareResponsesCaptureStateFromMap(meta)
	}
	if meta := mapAnyFromAny(session.SessionJSON["meta"]); len(meta) > 0 {
		if captureMeta := mapAnyFromAny(meta[dataShareInternalCaptureMetaKey]); len(captureMeta) > 0 {
			return dataShareResponsesCaptureStateFromMap(captureMeta)
		}
	}
	return nil
}

func dataShareResponsesCaptureStateFromMap(meta map[string]any) *dataShareResponsesCaptureState {
	if len(meta) == 0 {
		return nil
	}
	state := &dataShareResponsesCaptureState{
		StableIDs:        map[string]struct{}{},
		ReplayIdentities: stringsFromAny(meta["replay_identities"]),
		ResponseKeys:     map[string]struct{}{},
		LastTurn:         intFromAny(meta["last_turn"]),
		OrderUncertain:   boolFromAny(meta["order_uncertain"]),
	}
	for _, id := range stringsFromAny(meta["stable_ids"]) {
		if id != "" {
			state.StableIDs[id] = struct{}{}
		}
	}
	for _, key := range stringsFromAny(meta["response_keys"]) {
		if key != "" {
			state.ResponseKeys[key] = struct{}{}
		}
	}
	return state
}

func cloneDataShareResponsesCaptureState(state *dataShareResponsesCaptureState) *dataShareResponsesCaptureState {
	if state == nil {
		return nil
	}
	out := &dataShareResponsesCaptureState{
		StableIDs:        map[string]struct{}{},
		ReplayIdentities: append([]string(nil), state.ReplayIdentities...),
		ResponseKeys:     map[string]struct{}{},
		LastTurn:         state.LastTurn,
		OrderUncertain:   state.OrderUncertain,
	}
	for id := range state.StableIDs {
		out.StableIDs[id] = struct{}{}
	}
	for key := range state.ResponseKeys {
		out.ResponseKeys[key] = struct{}{}
	}
	return out
}

func withDataShareInternalCaptureState(meta map[string]any, state *dataShareResponsesCaptureState) map[string]any {
	out := cloneDataShareMap(meta)
	if state == nil {
		delete(out, dataShareInternalCaptureMetaKey)
		return out
	}
	stableIDs := make([]string, 0, len(state.StableIDs))
	for id := range state.StableIDs {
		stableIDs = append(stableIDs, id)
	}
	sort.Strings(stableIDs)
	responseKeys := make([]string, 0, len(state.ResponseKeys))
	for key := range state.ResponseKeys {
		responseKeys = append(responseKeys, key)
	}
	sort.Strings(responseKeys)
	out[dataShareInternalCaptureMetaKey] = map[string]any{
		"stable_ids":        stableIDs,
		"replay_identities": append([]string(nil), state.ReplayIdentities...),
		"response_keys":     responseKeys,
		"last_turn":         state.LastTurn,
		"order_uncertain":   state.OrderUncertain,
		"schema":            "openai_responses_v1",
	}
	if state.OrderUncertain {
		out["capture_order_uncertain"] = true
	}
	return out
}

func stripDataShareInternalCaptureStateFromMeta(meta map[string]any) map[string]any {
	out := cloneDataShareMap(meta)
	delete(out, dataShareInternalCaptureMetaKey)
	return out
}

func normalizeResponsesOutputItem(item gjson.Result) map[string]any {
	msg := rawJSONToMap(item.Raw)
	switch strings.TrimSpace(item.Get("type").String()) {
	case "function_call":
		// Responses API 的 output 也可能直接携带工具调用，需要转成统一 tool_calls。
		return normalizeResponsesFunctionCallMessage(msg)
	case "function_call_output":
		return normalizeToolResultMessage(msg)
	case "message":
		role := normalizeResponsesInputRole(item.Get("role").String(), item.Get("type").String())
		if strings.TrimSpace(item.Get("role").String()) == "" {
			role = "assistant"
		}
		out := map[string]any{"role": role}
		if content := item.Get("content"); content.Exists() {
			out["content"] = responseInputContentValue(content)
		}
		if phase := strings.TrimSpace(item.Get("phase").String()); phase != "" {
			// Codex Responses 会用 phase 标记 commentary 等可见中间输出，保留后才能和下一轮 input 回放稳定对齐。
			out["phase"] = phase
		}
		return out
	default:
		return normalizeDataShareMessage(msg)
	}
}

func normalizeCaptureTools(input DataShareCaptureInput) []map[string]any {
	if len(input.Tools) > 0 {
		return normalizeDataShareTools(input.Tools)
	}
	body := input.RequestBody
	var out []map[string]any
	for _, path := range []string{"tools", "functions"} {
		if arr := gjson.GetBytes(body, path); arr.IsArray() {
			for _, item := range arr.Array() {
				out = append(out, rawJSONToMap(item.Raw))
			}
		}
	}
	return normalizeDataShareTools(out)
}

func buildCaptureUsage(input DataShareCaptureInput) map[string]any {
	totalInput := input.InputTokens + input.CacheReadTokens + input.CacheCreateTokens
	total := totalInput + input.OutputTokens
	return map[string]any{
		"input_tokens":                input.InputTokens,
		"output_tokens":               input.OutputTokens,
		"cache_read_input_tokens":     input.CacheReadTokens,
		"cache_creation_input_tokens": input.CacheCreateTokens,
		"total_tokens":                total,
	}
}

func buildCaptureMeta(input DataShareCaptureInput) map[string]any {
	requestID := resolveDataShareRequestID(input)
	requestPath := normalizeDataShareRequestPath(input.InboundEndpoint)
	sourceRequestIDs := []string{}
	if requestID != "" {
		sourceRequestIDs = append(sourceRequestIDs, requestID)
	}
	meta := map[string]any{
		"api_key_id":         int64(0),
		"group_id":           int64(0),
		"account_id":         int64(0),
		"request_id":         requestID,
		"source_request_ids": sourceRequestIDs,
		"requested_model":    firstNonBlank(input.Model, gjson.GetBytes(input.RequestBody, "model").String()),
		"inbound_endpoint":   requestPath,
		"request_path":       requestPath,
		"upstream_endpoint":  input.UpstreamEndpoint,
		"user_agent":         input.UserAgent,
		"user_agent_family":  normalizeDataShareUserAgent(input.UserAgent),
		"ip_address":         input.IPAddress,
	}
	if input.APIKey != nil {
		meta["user_id"] = input.APIKey.UserID
		meta["api_key_id"] = input.APIKey.ID
		meta["api_key_name"] = input.APIKey.Name
		if input.APIKey.GroupID != nil {
			meta["group_id"] = *input.APIKey.GroupID
		}
		if input.APIKey.User != nil {
			meta["user_name"] = input.APIKey.User.Username
			meta["user_email"] = input.APIKey.User.Email
		}
		if input.APIKey.Group != nil {
			meta["group_id"] = input.APIKey.Group.ID
			meta["group_name"] = input.APIKey.Group.Name
		}
	}
	if input.User != nil {
		meta["user_id"] = input.User.ID
		meta["user_name"] = input.User.Username
		meta["user_email"] = input.User.Email
	}
	if input.Account != nil {
		meta["account_id"] = input.Account.ID
	}
	return meta
}

func resolveDataShareRequestID(input DataShareCaptureInput) string {
	return firstNonBlank(
		input.RequestID,
		gjson.GetBytes(input.ResponseBody, "id").String(),
		gjson.GetBytes(input.RequestBody, "request_id").String(),
		gjson.GetBytes(input.RequestBody, "metadata.request_id").String(),
	)
}

func resolveDataShareActualModel(input DataShareCaptureInput) string {
	// 正式交付要求 model 等于实际生成模型；映射后的上游模型优先，客户端请求模型只放入 meta。
	return firstNonBlank(input.UpstreamModel, input.Model, gjson.GetBytes(input.RequestBody, "model").String())
}

type dataShareQualityReport struct {
	Errors []string
	Status string
}

// ValidateDataShareSessionQuality 按附件交付规则检查 session 是否可进入正式导出。
func ValidateDataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) []string {
	compact := CompactDataShareMessages(messages)
	errs := validateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
	if dataShareHasReplayDuplicateBlock(compact) {
		errs = appendDataShareQualityError(errs, dataShareQualityErrorReplayDuplicateBlock)
	}
	return errs
}

// DataShareSessionQuality 一次性返回质量状态和错误列表，避免采集热路径重复扫描消息。
func DataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) (string, []string) {
	report := evaluateDataShareSessionQuality(model, systemPrompt, messages, tools, usage)
	if report.Status != DataShareQualityInvalid {
		return report.Status, report.Errors
	}
	if !dataShareErrorsAllowNormalizeFallback(report.Errors) {
		return report.Status, report.Errors
	}
	if !dataShareMessagesNeedNormalizeFallback(messages) {
		return report.Status, report.Errors
	}
	normalized := normalizeDataShareMessages(messages)
	if len(normalized) == 0 {
		return report.Status, report.Errors
	}
	// 兼容历史/异构 payload：状态允许走规范化恢复，错误列表仍保留原始快照的具体缺口。
	if normalizedReport := evaluateDataShareSessionQuality(model, systemPrompt, normalized, tools, usage); normalizedReport.Status != DataShareQualityInvalid {
		return DataShareQualityPartial, report.Errors
	}
	return report.Status, report.Errors
}

func evaluateDataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) dataShareQualityReport {
	compact := CompactDataShareMessages(messages)
	return evaluateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
}

// evaluateCompactDataShareSessionQuality 只接收已 compact 的消息，避免最终化阶段重复压缩同一份大快照。
func evaluateCompactDataShareSessionQuality(model string, systemPrompt string, compact []map[string]any, tools []map[string]any, usage map[string]any) dataShareQualityReport {
	errs := validateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
	if dataShareHasReplayDuplicateBlock(compact) {
		errs = appendDataShareQualityError(errs, dataShareQualityErrorReplayDuplicateBlock)
	}
	status := DataShareQualityInvalid
	if len(errs) == 0 {
		status = DataShareQualityComplete
	} else if dataShareErrorsAllowTailTrim(errs) && dataShareCanTrimTailToComplete(model, systemPrompt, compact, tools, usage) {
		status = DataShareQualityPartial
	}
	return dataShareQualityReport{Errors: errs, Status: status}
}

func validateCompactDataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) []string {
	var errs []string
	seenErrs := map[string]struct{}{}
	addErr := func(code string) {
		if _, ok := seenErrs[code]; ok {
			return
		}
		seenErrs[code] = struct{}{}
		errs = append(errs, code)
	}
	systemPrompt = firstNonBlank(systemPrompt, extractSystemPromptFromMessages(messages))
	if strings.TrimSpace(systemPrompt) == "" {
		addErr("missing_system_prompt")
	}
	if len(messages) < 2 {
		addErr("effective_turns_lt_2")
	}
	toolDefs, invalidToolCount := collectDataShareToolDefinitions(tools)
	if len(toolDefs) == 0 {
		addErr("missing_tool_definitions")
	}
	if invalidToolCount > 0 {
		addErr("invalid_tool_definition")
	}
	toolCalls := collectDataShareToolCalls(messages)
	toolResults := collectDataShareToolResults(messages)
	if len(toolCalls) == 0 {
		addErr("missing_structured_tool_call")
	}
	for _, call := range toolCalls {
		if call.id == "" || call.name == "" {
			addErr("invalid_tool_call")
			continue
		}
		if _, ok := toolDefs[call.name]; !ok {
			addErr("tool_definition_missing")
		}
		if toolResults[call.id] != 1 {
			addErr("tool_call_result_unpaired")
		}
	}
	for id, count := range toolResults {
		if id == "" || count != 1 {
			addErr("tool_result_unpaired")
		}
	}
	if len(toolCalls) > 0 && !hasFinalAssistantMessage(messages) {
		addErr("missing_final_assistant")
	}
	if !dataShareModelAllowed(model) {
		addErr("model_not_allowed")
	}
	// 交付文档允许 token 用量无法聚合时为空或保留在 meta，因此 usage 不能作为 session 可用性的硬门槛。
	_ = usage
	return errs
}

func dataShareCanTrimTailToComplete(model string, systemPrompt string, compact []map[string]any, tools []map[string]any, usage map[string]any) bool {
	return dataShareCompleteTrimPrefixLen(model, systemPrompt, compact, tools, usage) > 0
}

// dataShareCompleteTrimPrefixLen 用单次前缀扫描寻找可导出的完整前缀，替代逐候选完整校验的平方级路径。
func dataShareCompleteTrimPrefixLen(model string, systemPrompt string, compact []map[string]any, tools []map[string]any, usage map[string]any) int {
	// usage 当前不是质量硬门槛；保留参数是为了让调用点和完整校验的签名保持一致。
	_ = usage
	state := newDataSharePrefixQualityState(model, systemPrompt, tools)
	completeLen := 0
	for i, msg := range compact {
		state.observe(msg)
		// 尾部裁剪必须至少去掉一条消息，避免把完整快照误当成 partial 修复。
		if i < len(compact)-1 && state.complete() {
			completeLen = i + 1
		}
	}
	return completeLen
}

// dataSharePrefixQualityState 增量维护 validateCompactDataShareSessionQuality 关心的质量条件。
type dataSharePrefixQualityState struct {
	modelAllowed               bool
	toolDefinitionsReady       bool
	hasSystemPrompt            bool
	messageCount               int
	toolCallCount              int
	invalidToolCallCount       int
	toolDefinitionMissingCount int
	callIDs                    map[string]struct{}
	resultCounts               map[string]int
	callIDsWithBadResultCount  int
	resultIDsWithBadCount      int
	emptyToolResultCount       int
	hasFinalAssistant          bool
	toolDefs                   map[string]struct{}
}

func newDataSharePrefixQualityState(model string, systemPrompt string, tools []map[string]any) *dataSharePrefixQualityState {
	toolDefs, invalidToolCount := collectDataShareToolDefinitions(tools)
	return &dataSharePrefixQualityState{
		modelAllowed:         dataShareModelAllowed(model),
		toolDefinitionsReady: len(toolDefs) > 0 && invalidToolCount == 0,
		hasSystemPrompt:      strings.TrimSpace(systemPrompt) != "",
		callIDs:              map[string]struct{}{},
		resultCounts:         map[string]int{},
		toolDefs:             toolDefs,
	}
}

func (s *dataSharePrefixQualityState) observe(msg map[string]any) {
	if s == nil {
		return
	}
	s.messageCount++
	role := strings.TrimSpace(stringFromAny(msg["role"]))
	if (role == "system" || role == "developer") && strings.TrimSpace(dataShareContentText(msg["content"])) != "" {
		s.hasSystemPrompt = true
	}
	s.observeToolCalls(msg)
	if role == "tool" {
		s.observeToolResult(strings.TrimSpace(stringFromAny(msg["tool_call_id"])))
	}
	if role != "" {
		s.hasFinalAssistant = role == "assistant" && len(anySlice(msg["tool_calls"])) == 0 && strings.TrimSpace(dataShareContentText(msg["content"])) != ""
	}
}

func (s *dataSharePrefixQualityState) observeToolCalls(msg map[string]any) {
	for _, raw := range anySlice(msg["tool_calls"]) {
		call, ok := mapFromAny(raw)
		if !ok {
			continue
		}
		s.toolCallCount++
		id := strings.TrimSpace(stringFromAny(call["id"]))
		name := strings.TrimSpace(stringFromAny(call["name"]))
		if id == "" || name == "" {
			s.invalidToolCallCount++
			continue
		}
		if _, ok := s.toolDefs[name]; !ok {
			s.toolDefinitionMissingCount++
		}
		s.observeToolCallID(id)
	}
}

func (s *dataSharePrefixQualityState) observeToolCallID(id string) {
	if _, exists := s.callIDs[id]; exists {
		return
	}
	s.callIDs[id] = struct{}{}
	if s.resultCounts[id] != 1 {
		s.callIDsWithBadResultCount++
	}
}

func (s *dataSharePrefixQualityState) observeToolResult(id string) {
	if id == "" {
		s.emptyToolResultCount++
		return
	}
	oldCount := s.resultCounts[id]
	oldBadResult := oldCount > 0 && oldCount != 1
	oldBadCall := s.callIDNeedsResult(id, oldCount)
	newCount := oldCount + 1
	s.resultCounts[id] = newCount
	newBadResult := newCount != 1
	newBadCall := s.callIDNeedsResult(id, newCount)
	if oldBadResult && !newBadResult {
		s.resultIDsWithBadCount--
	} else if !oldBadResult && newBadResult {
		s.resultIDsWithBadCount++
	}
	if oldBadCall && !newBadCall {
		s.callIDsWithBadResultCount--
	} else if !oldBadCall && newBadCall {
		s.callIDsWithBadResultCount++
	}
}

func (s *dataSharePrefixQualityState) callIDNeedsResult(id string, resultCount int) bool {
	if _, ok := s.callIDs[id]; !ok {
		return false
	}
	return resultCount != 1
}

func (s *dataSharePrefixQualityState) complete() bool {
	return s != nil &&
		s.modelAllowed &&
		s.toolDefinitionsReady &&
		s.hasSystemPrompt &&
		s.messageCount >= 2 &&
		s.toolCallCount > 0 &&
		s.invalidToolCallCount == 0 &&
		s.toolDefinitionMissingCount == 0 &&
		s.callIDsWithBadResultCount == 0 &&
		s.resultIDsWithBadCount == 0 &&
		s.emptyToolResultCount == 0 &&
		s.hasFinalAssistant
}

func dataShareErrorsAllowTailTrim(errs []string) bool {
	if len(errs) == 0 {
		return false
	}
	for _, errCode := range errs {
		switch errCode {
		case "invalid_tool_call", "tool_definition_missing", "tool_call_result_unpaired", "tool_result_unpaired", "missing_final_assistant":
			continue
		default:
			return false
		}
	}
	return true
}

func dataShareErrorsAllowNormalizeFallback(errs []string) bool {
	if len(errs) == 0 {
		return false
	}
	for _, errCode := range errs {
		switch errCode {
		case "missing_structured_tool_call", "invalid_tool_call", "tool_call_result_unpaired", "tool_result_unpaired", "missing_final_assistant":
			continue
		default:
			return false
		}
	}
	return true
}

func dataShareMessagesNeedNormalizeFallback(messages []map[string]any) bool {
	for _, msg := range messages {
		for _, raw := range anySlice(msg["content"]) {
			block, ok := mapFromAny(raw)
			if !ok {
				continue
			}
			switch strings.TrimSpace(stringFromAny(block["type"])) {
			case "tool_use", "tool_result":
				return true
			}
		}
	}
	return false
}

func dataShareModelAllowed(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	return strings.Contains(model, "gpt-5") ||
		strings.Contains(model, "claude") && (strings.Contains(model, "4.5") || strings.Contains(model, "4-5")) ||
		strings.Contains(model, "gemini-3")
}

type dataShareToolCall struct {
	id   string
	name string
}

func collectDataShareToolDefinitions(tools []map[string]any) (map[string]struct{}, int) {
	defs := make(map[string]struct{}, len(tools))
	invalid := 0
	for _, tool := range tools {
		name := strings.TrimSpace(stringFromAny(tool["name"]))
		description := strings.TrimSpace(stringFromAny(tool["description"]))
		parameters, ok := mapFromAny(tool["parameters"])
		if name == "" || description == "" || !ok || len(parameters) == 0 {
			invalid++
			continue
		}
		defs[name] = struct{}{}
	}
	return defs, invalid
}

func collectDataShareToolCalls(messages []map[string]any) []dataShareToolCall {
	var out []dataShareToolCall
	for _, msg := range messages {
		for _, call := range anySlice(msg["tool_calls"]) {
			m, ok := mapFromAny(call)
			if !ok {
				continue
			}
			out = append(out, dataShareToolCall{
				id:   strings.TrimSpace(stringFromAny(m["id"])),
				name: strings.TrimSpace(stringFromAny(m["name"])),
			})
		}
	}
	return out
}

func collectDataShareToolResults(messages []map[string]any) map[string]int {
	out := map[string]int{}
	for _, msg := range messages {
		if strings.TrimSpace(stringFromAny(msg["role"])) != "tool" {
			continue
		}
		id := strings.TrimSpace(stringFromAny(msg["tool_call_id"]))
		out[id]++
	}
	return out
}

func hasFinalAssistantMessage(messages []map[string]any) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		role := strings.TrimSpace(stringFromAny(msg["role"]))
		if role == "" {
			continue
		}
		if role != "assistant" {
			return false
		}
		if len(anySlice(msg["tool_calls"])) > 0 {
			return false
		}
		return strings.TrimSpace(dataShareContentText(msg["content"])) != ""
	}
	return false
}

func normalizeDataShareMessages(messages []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		for _, expanded := range expandAnthropicDataShareMessage(msg) {
			normalized := normalizeDataShareMessage(expanded)
			if len(normalized) == 0 {
				continue
			}
			out = append(out, normalized)
		}
	}
	return out
}

// expandAnthropicDataShareMessage 将 Anthropic content block 展开成统一的 message/tool 结构。
func expandAnthropicDataShareMessage(msg map[string]any) []map[string]any {
	if msg == nil {
		return nil
	}
	content := anySlice(msg["content"])
	if len(content) == 0 {
		return []map[string]any{msg}
	}
	role := normalizeResponsesInputRole(stringFromAny(msg["role"]), stringFromAny(msg["type"]))
	switch role {
	case "assistant":
		return expandAnthropicAssistantMessage(msg, content)
	case "user":
		return expandAnthropicUserMessage(msg, content)
	default:
		return []map[string]any{msg}
	}
}

// expandAnthropicAssistantMessage 把 assistant 的 tool_use block 转成标准 tool_calls。
func expandAnthropicAssistantMessage(msg map[string]any, content []any) []map[string]any {
	calls := make([]map[string]any, 0)
	textBlocks := make([]any, 0, len(content))
	for _, raw := range content {
		block, ok := mapFromAny(raw)
		if !ok || strings.TrimSpace(stringFromAny(block["type"])) != "tool_use" {
			textBlocks = append(textBlocks, raw)
			continue
		}
		calls = append(calls, map[string]any{
			"id":        firstNonBlank(stringFromAny(block["id"]), stringFromAny(block["tool_use_id"])),
			"name":      stringFromAny(block["name"]),
			"arguments": firstPresentAny(block["input"], block["arguments"]),
		})
	}
	if len(calls) == 0 {
		return []map[string]any{msg}
	}
	out := cloneDataShareMap(msg)
	out["role"] = "assistant"
	out["tool_calls"] = calls
	out["finish_reason"] = "tool_calls"
	out["content"] = contentValueFromAnthropicBlocks(textBlocks)
	return []map[string]any{out}
}

// expandAnthropicUserMessage 把 user 消息里的 tool_result block 转成标准 tool 消息。
func expandAnthropicUserMessage(msg map[string]any, content []any) []map[string]any {
	out := make([]map[string]any, 0, len(content))
	textBlocks := make([]any, 0, len(content))
	sawToolResult := false
	flushText := func() {
		if len(textBlocks) == 0 {
			return
		}
		textMsg := cloneDataShareMap(msg)
		textMsg["role"] = "user"
		textMsg["content"] = contentValueFromAnthropicBlocks(textBlocks)
		out = append(out, textMsg)
		textBlocks = nil
	}
	for _, raw := range content {
		block, ok := mapFromAny(raw)
		if !ok || strings.TrimSpace(stringFromAny(block["type"])) != "tool_result" {
			textBlocks = append(textBlocks, raw)
			continue
		}
		sawToolResult = true
		flushText()
		out = append(out, map[string]any{
			"role":         "tool",
			"tool_call_id": firstNonBlank(stringFromAny(block["tool_use_id"]), stringFromAny(block["tool_call_id"]), stringFromAny(block["id"])),
			"content":      firstPresentAny(block["content"], block["output"]),
			"is_error":     firstPresentAny(block["is_error"], block["error"]),
			"status":       stringFromAny(block["status"]),
		})
	}
	if !sawToolResult {
		return []map[string]any{msg}
	}
	flushText()
	return out
}

// contentValueFromAnthropicBlocks 提取 Anthropic 文本块中的可读内容。
func contentValueFromAnthropicBlocks(blocks []any) any {
	if len(blocks) == 0 {
		return ""
	}
	return normalizeDataShareContentValue(blocks)
}

// CompactDataShareMessages 压缩 Responses/Codex 每轮请求重复携带的历史消息。
func CompactDataShareMessages(messages []map[string]any) []map[string]any {
	out := messages
	for pass := 0; pass < dataShareCompactFixedPointMaxPasses; pass++ {
		next := compactDataShareMessagesOnce(out)
		if len(next) == len(out) {
			return next
		}
		out = next
		if len(out) < dataShareLongReplayMinMessages*2 {
			return out
		}
	}
	return out
}

// compactDataShareMessagesOnce 执行一轮压缩；公开入口会在固定上限内重复调用直到长度稳定。
func compactDataShareMessagesOnce(messages []map[string]any) []map[string]any {
	messages = dataShareCompactTrailingReplayBlock(messages)
	out := make([]map[string]any, 0, len(messages))
	outIdentities := make([]string, 0, len(messages))
	outIdentityPositions := map[string][]int{}
	outIdentityAt := func(index int) string {
		if outIdentities[index] == "" {
			outIdentities[index] = dataShareMessageIdentity(out[index])
		}
		return outIdentities[index]
	}
	messageIdentities := make([]string, len(messages))
	messageIdentityAt := func(index int) string {
		if messageIdentities[index] == "" {
			messageIdentities[index] = dataShareMessageIdentity(messages[index])
		}
		return messageIdentities[index]
	}
	seenToolCalls := map[string]struct{}{}
	seenToolResults := map[string]struct{}{}
	seenAssistantText := map[string]int{}
	assistantTextEpoch := 0
	for i := 0; i < len(messages); {
		if len(out) > 0 {
			if replay := dataShareReplaySkipLen(
				out,
				len(messages),
				i,
				outIdentityAt,
				messageIdentityAt,
				outIdentityPositions,
				seenToolCalls,
				seenToolResults,
			); replay >= dataShareReplayOverlapMinMessages {
				i += replay
				continue
			}
		}
		msg := messages[i]
		if dataShareMessageAlreadySeen(msg, seenToolCalls, seenToolResults) {
			i++
			continue
		}
		if dataShareAssistantTextEchoWindowReset(msg) {
			assistantTextEpoch++
		}
		if dataShareCommentaryEchoAlreadySeen(msg, seenAssistantText, assistantTextEpoch) {
			i++
			continue
		}
		rememberDataShareMessage(msg, seenToolCalls, seenToolResults)
		rememberDataShareAssistantTextMessage(msg, seenAssistantText, assistantTextEpoch)
		out = append(out, msg)
		identity := messageIdentityAt(i)
		outIdentities = append(outIdentities, identity)
		outIdentityPositions[identity] = append(outIdentityPositions[identity], len(out)-1)
		i++
	}
	return dataShareCompactGlobalReplayWindows(dataShareCompactTrailingReplayBlock(dataShareCompactAdjacentReplayBlocks(out)))
}

func dataShareCompactAdjacentReplayBlocks(messages []map[string]any) []map[string]any {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return messages
	}
	// 相邻长重复块更符合 replay 污染形态，默认压成一份；非相邻纯文本重复由全局窗口逻辑保守处理。
	out := cloneBufferedDataShareMaps(messages)
	for pass := 0; pass < dataShareAdjacentReplayCompactMaxPasses; pass++ {
		keys := dataShareMessageIdentityKeys(out)
		keyHash := dataShareNewReplayRangeHash(keys)
		index := dataShareReplayWindowIndex(keys)
		compact := make([]map[string]any, 0, len(out))
		changed := false
		for i := 0; i < len(out); {
			if matchLen := dataShareAdjacentReplayBlockLen(keys, keyHash, index, i); matchLen >= dataShareLongReplayMinMessages {
				runEnd := i + matchLen
				for runEnd+matchLen <= len(out) && dataShareKeysEqualHashed(keys, keyHash, runEnd-matchLen, runEnd, matchLen) {
					runEnd += matchLen
				}
				if runEnd > i+matchLen && dataShareHasEarlierReplayBlock(keys, keyHash, index, i, matchLen) {
					// replay run 自身相邻重复且更早历史已有同一块时，整段 run 都是污染副本。
					i = runEnd
					changed = true
					continue
				}
				if runEnd > i+matchLen {
					compact = append(compact, cloneBufferedDataShareMaps(out[i:i+matchLen])...)
					i = runEnd
					changed = true
					continue
				}
				compact = append(compact, cloneBufferedDataShareMaps(out[i:runEnd])...)
				i = runEnd
				continue
			}
			compact = append(compact, cloneDataShareMap(out[i]))
			i++
		}
		out = compact
		if !changed || len(out) < dataShareLongReplayMinMessages*2 {
			return out
		}
	}
	return out
}

func dataShareAdjacentReplayBlockLen(keys []string, keyHash dataShareReplayRangeHash, index map[string][]int, start int) int {
	candidates := index[dataShareReplayWindowKey(keys, start)]
	if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
		return 0
	}
	best := 0
	for _, other := range candidates {
		if other <= start {
			continue
		}
		blockLen := other - start
		if blockLen < dataShareLongReplayMinMessages || start+blockLen*2 > len(keys) {
			continue
		}
		if dataShareKeysEqualHashed(keys, keyHash, start, other, blockLen) && blockLen > best {
			best = blockLen
		}
	}
	return best
}

func dataShareHasEarlierReplayBlock(keys []string, keyHash dataShareReplayRangeHash, index map[string][]int, start int, length int) bool {
	if start <= 0 || length < dataShareLongReplayMinMessages {
		return false
	}
	candidates := index[dataShareReplayWindowKey(keys, start)]
	if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
		return false
	}
	for _, other := range candidates {
		if other >= start {
			continue
		}
		if dataShareKeysEqualHashed(keys, keyHash, other, start, length) {
			return true
		}
	}
	return false
}

func dataShareCompactTrailingReplayBlock(messages []map[string]any) []map[string]any {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return messages
	}
	keys := dataShareMessageIdentityKeys(messages)
	keyHash := dataShareNewReplayRangeHash(keys)
	index := dataShareReplayWindowIndex(keys)
	for suffixStart := dataShareLongReplayMinMessages; suffixStart <= len(keys)-dataShareLongReplayMinMessages; suffixStart++ {
		suffixLen := len(keys) - suffixStart
		candidates := index[dataShareReplayWindowKey(keys, suffixStart)]
		if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
			continue
		}
		for _, pos := range candidates {
			if pos >= suffixStart {
				continue
			}
			if pos+suffixLen > suffixStart {
				continue
			}
			if !dataShareKeysEqualHashed(keys, keyHash, pos, suffixStart, suffixLen) {
				continue
			}
			if !dataShareReplayWindowSafe(messages[suffixStart:]) {
				return messages
			}
			// 尾部窗口完整重复早前历史且带强 replay 信号时，删除尾部污染副本，保留更早的真实上下文。
			return cloneBufferedDataShareMaps(messages[:suffixStart])
		}
	}
	return messages
}

func dataShareKeysEqual(keys []string, left int, right int, length int) bool {
	if left < 0 || right < 0 || length <= 0 || left+length > len(keys) || right+length > len(keys) {
		return false
	}
	for i := 0; i < length; i++ {
		if keys[left+i] == "" || keys[left+i] != keys[right+i] {
			return false
		}
	}
	return true
}

type dataShareReplayRangeHash struct {
	prefix []uint64
	power  []uint64
}

func dataShareNewReplayRangeHash(keys []string) dataShareReplayRangeHash {
	h := dataShareReplayRangeHash{
		prefix: []uint64{0},
		power:  []uint64{1},
	}
	for _, key := range keys {
		h.append(key)
	}
	return h
}

func (h *dataShareReplayRangeHash) append(key string) {
	value := dataShareReplayKeyHash(key)
	h.prefix = append(h.prefix, h.prefix[len(h.prefix)-1]*dataShareReplayRangeHashBase+value)
	h.power = append(h.power, h.power[len(h.power)-1]*dataShareReplayRangeHashBase)
}

func (h dataShareReplayRangeHash) rangeHash(start int, length int) (uint64, bool) {
	if start < 0 || length < 0 || start+length >= len(h.prefix) || length >= len(h.power) {
		return 0, false
	}
	return h.prefix[start+length] - h.prefix[start]*h.power[length], true
}

func dataShareReplayKeyHash(key string) uint64 {
	hash := uint64(1469598103934665603)
	for i := 0; i < len(key); i++ {
		hash ^= uint64(key[i])
		hash *= 1099511628211
	}
	if hash == 0 {
		return 1
	}
	return hash
}

func dataShareKeysEqualHashed(keys []string, keyHash dataShareReplayRangeHash, left int, right int, length int) bool {
	leftHash, ok := keyHash.rangeHash(left, length)
	if !ok {
		return false
	}
	rightHash, ok := keyHash.rangeHash(right, length)
	if !ok || leftHash != rightHash {
		return false
	}
	return dataShareKeysEqual(keys, left, right, length)
}

func dataShareCrossKeysEqual(leftKeys []string, leftStart int, rightKeys []string, rightStart int, length int) bool {
	if leftStart < 0 || rightStart < 0 || length <= 0 || leftStart+length > len(leftKeys) || rightStart+length > len(rightKeys) {
		return false
	}
	for i := 0; i < length; i++ {
		if leftKeys[leftStart+i] == "" || leftKeys[leftStart+i] != rightKeys[rightStart+i] {
			return false
		}
	}
	return true
}

func dataShareContiguousKeyMatchLenHashed(leftKeys []string, leftHash dataShareReplayRangeHash, leftStart int, rightKeys []string, rightHash dataShareReplayRangeHash, rightStart int) int {
	limit := len(leftKeys) - leftStart
	if remaining := len(rightKeys) - rightStart; remaining < limit {
		limit = remaining
	}
	if limit <= 0 || leftStart < 0 || rightStart < 0 {
		return 0
	}
	low, high := 0, limit
	for low < high {
		mid := (low + high + 1) / 2
		leftValue, leftOK := leftHash.rangeHash(leftStart, mid)
		rightValue, rightOK := rightHash.rangeHash(rightStart, mid)
		if leftOK && rightOK && leftValue == rightValue {
			low = mid
			continue
		}
		high = mid - 1
	}
	// hash 只做候选长度过滤；调用方在删除或报错前必须再做真实 identity 连续比较。
	return low
}

func dataShareHasReplayDuplicateBlock(messages []map[string]any) bool {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return false
	}
	keys := dataShareMessageIdentityKeys(messages)
	return dataShareHasUnsafeReplayDuplicateBlock(messages, keys)
}

func dataShareCompactGlobalReplayWindows(messages []map[string]any) []map[string]any {
	if len(messages) < dataShareLongReplayMinMessages*2 {
		return messages
	}
	// 非相邻窗口只在强信号下删除，避免把用户真实重复执行的一段长任务误判为 replay。
	keys := dataShareMessageIdentityKeys(messages)
	keyHash := dataShareNewReplayRangeHash(keys)
	safePrefix := dataShareReplaySafePrefix(messages)
	out := make([]map[string]any, 0, len(messages))
	outKeys := make([]string, 0, len(messages))
	outKeyHash := dataShareNewReplayRangeHash(nil)
	index := map[string][]int{}
	for i := 0; i < len(messages); {
		if len(outKeys) >= dataShareLongReplayMinMessages && len(keys)-i >= dataShareLongReplayMinMessages {
			windowKey := dataShareReplayWindowKey(keys, i)
			candidates := index[windowKey]
			if len(candidates) > 0 && len(candidates) <= dataShareReplayWindowCandidateLimit {
				best := 0
				bestPos := 0
				for _, pos := range candidates {
					length := dataShareContiguousKeyMatchLenHashed(outKeys, outKeyHash, pos, keys, keyHash, i)
					if length > best {
						best = length
						bestPos = pos
					}
				}
				if best >= dataShareLongReplayMinMessages &&
					dataShareReplayWindowSafeRange(safePrefix, i, i+best) &&
					dataShareCrossKeysEqual(outKeys, bestPos, keys, i, best) {
					i += best
					continue
				}
			}
		}
		out = append(out, cloneDataShareMap(messages[i]))
		outKeys = append(outKeys, keys[i])
		outKeyHash.append(keys[i])
		dataShareAddReplayWindowIndex(index, outKeys, len(outKeys)-1)
		i++
	}
	return out
}

func dataShareAddReplayWindowIndex(index map[string][]int, keys []string, appended int) {
	start := appended - dataShareReplayWindowWidth + 1
	if start < 0 {
		return
	}
	key := dataShareReplayWindowKey(keys, start)
	if key == "" {
		return
	}
	index[key] = append(index[key], start)
}

func dataShareReplayWindowSafe(messages []map[string]any) bool {
	for _, msg := range messages {
		if dataShareReplayMessageSafe(msg) {
			return true
		}
	}
	return false
}

func dataShareReplayMessageSafe(msg map[string]any) bool {
	if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" || len(anySlice(msg["tool_calls"])) > 0 {
		return true
	}
	return dataShareSyntheticUserContextText(dataShareMessageTextForReplay(msg))
}

func dataShareReplaySafePrefix(messages []map[string]any) []int {
	prefix := make([]int, len(messages)+1)
	for i, msg := range messages {
		prefix[i+1] = prefix[i]
		if dataShareReplayMessageSafe(msg) {
			prefix[i+1]++
		}
	}
	return prefix
}

func dataShareReplayWindowSafeRange(prefix []int, start int, end int) bool {
	if start < 0 {
		start = 0
	}
	if end > len(prefix)-1 {
		end = len(prefix) - 1
	}
	if start >= end || len(prefix) == 0 {
		return false
	}
	return prefix[end] > prefix[start]
}

func dataShareHasUnsafeReplayDuplicateBlock(messages []map[string]any, keys []string) bool {
	if len(messages) < dataShareLongReplayMinMessages*2 || len(keys) != len(messages) {
		return false
	}
	keyHash := dataShareNewReplayRangeHash(keys)
	safePrefix := dataShareReplaySafePrefix(messages)
	index := dataShareReplayWindowIndex(keys)
	for start := 0; start+dataShareLongReplayMinMessages <= len(keys); start++ {
		candidates := index[dataShareReplayWindowKey(keys, start)]
		if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
			continue
		}
		for _, other := range candidates {
			if other <= start || other-start < dataShareLongReplayMinMessages {
				continue
			}
			length := dataShareContiguousKeyMatchLenHashed(keys, keyHash, start, keys, keyHash, other)
			if length < dataShareLongReplayMinMessages {
				continue
			}
			if dataShareReplayWindowSafeRange(safePrefix, start, start+length) ||
				dataShareReplayWindowSafeRange(safePrefix, other, other+length) {
				if dataShareKeysEqual(keys, start, other, length) {
					return true
				}
				continue
			}
			if other == start+length &&
				dataShareHasEarlierReplayBlock(keys, keyHash, index, start, length) &&
				dataShareKeysEqual(keys, start, other, length) {
				return true
			}
		}
	}
	return false
}

func appendDataShareQualityError(errs []string, code string) []string {
	code = strings.TrimSpace(code)
	if code == "" {
		return errs
	}
	for _, existing := range errs {
		if existing == code {
			return errs
		}
	}
	return append(errs, code)
}

func dataShareReplaySkipLenForMessages(existing, incoming []map[string]any, incomingStart int) int {
	if len(existing) == 0 || len(incoming) == 0 || incomingStart >= len(incoming) {
		return 0
	}
	existingIdentities := make([]string, len(existing))
	existingIdentityPositions := make(map[string][]int, len(existing))
	existingIdentityAt := func(index int) string {
		if existingIdentities[index] == "" {
			existingIdentities[index] = dataShareMessageIdentity(existing[index])
		}
		return existingIdentities[index]
	}
	for i := range existing {
		identity := existingIdentityAt(i)
		existingIdentityPositions[identity] = append(existingIdentityPositions[identity], i)
	}
	incomingIdentities := make([]string, len(incoming))
	incomingIdentityAt := func(index int) string {
		if incomingIdentities[index] == "" {
			incomingIdentities[index] = dataShareMessageIdentity(incoming[index])
		}
		return incomingIdentities[index]
	}
	seenToolCalls := map[string]struct{}{}
	seenToolResults := map[string]struct{}{}
	for _, msg := range existing {
		rememberDataShareMessage(msg, seenToolCalls, seenToolResults)
	}
	return dataShareReplaySkipLen(
		existing,
		len(incoming),
		incomingStart,
		existingIdentityAt,
		incomingIdentityAt,
		existingIdentityPositions,
		seenToolCalls,
		seenToolResults,
	)
}

func dataShareReplaySkipLen(
	existing []map[string]any,
	incomingLen int,
	incomingStart int,
	existingIdentityAt func(int) string,
	incomingIdentityAt func(int) string,
	existingIdentityPositions map[string][]int,
	seenToolCalls map[string]struct{},
	seenToolResults map[string]struct{},
) int {
	if len(existing) == 0 || incomingStart >= incomingLen {
		return 0
	}
	prefix := dataShareCommonPrefixLen(len(existing), existingIdentityAt, incomingStart, incomingLen, incomingIdentityAt)
	if prefix < dataShareReplayOverlapMinMessages {
		return 0
	}
	ordered := dataShareOrderedReplaySkipLen(existing, incomingLen, incomingStart, existingIdentityAt, incomingIdentityAt, existingIdentityPositions)
	if dataShareReplayPrefixSafe(existing[:prefix], prefix, seenToolCalls, seenToolResults) {
		if ordered > prefix {
			return ordered
		}
		return prefix
	}
	if dataShareOrderedReplayPrefixSafe(prefix, ordered, incomingStart, incomingIdentityAt) {
		return ordered
	}
	return 0
}

func dataShareReplayPrefixSafe(messages []map[string]any, prefix int, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) bool {
	if prefix >= 5 {
		if prefix >= dataShareLongReplayMinMessages && !dataShareReplayWindowSafe(messages[:prefix]) {
			return false
		}
		return true
	}
	if prefix >= dataShareReplayOverlapMinMessages {
		role := strings.TrimSpace(stringFromAny(messages[0]["role"]))
		if role == "system" || role == "developer" {
			if prefix >= 3 {
				return true
			}
			return len(messages) > 1 && dataShareStrongSyntheticUserContextText(dataShareMessageTextForReplay(messages[1]))
		}
		if dataShareReplayPrefixStartsWithSyntheticUserContext(messages[:prefix]) {
			return true
		}
		if prefix >= 3 && dataShareReplayPrefixHasSyntheticContext(messages[:prefix]) {
			return true
		}
		return dataShareReplayPrefixHasSeenToolEcho(messages[:prefix], seenToolCalls, seenToolResults)
	}
	return false
}

func dataShareReplayPrefixStartsWithSyntheticUserContext(messages []map[string]any) bool {
	if len(messages) < dataShareReplayOverlapMinMessages {
		return false
	}
	if strings.TrimSpace(stringFromAny(messages[0]["role"])) != "user" {
		return false
	}
	first := dataShareMessageTextForReplay(messages[0])
	if !dataShareSyntheticUserContextText(first) {
		return false
	}
	secondRole := strings.TrimSpace(stringFromAny(messages[1]["role"]))
	if secondRole == "system" || secondRole == "developer" {
		return true
	}
	return dataShareSyntheticUserContextText(dataShareMessageTextForReplay(messages[1]))
}

func dataShareReplayPrefixHasSyntheticContext(messages []map[string]any) bool {
	for _, msg := range messages {
		if dataShareSyntheticUserContextText(dataShareMessageTextForReplay(msg)) {
			return true
		}
	}
	return false
}

func dataShareMessageTextForReplay(msg map[string]any) string {
	return strings.ToLower(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
}

func dataShareSyntheticUserContextText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if dataShareStrongSyntheticUserContextText(text) {
		return true
	}
	weakMatches := 0
	for _, marker := range []string{
		"agents.md instructions",
		"current_date",
		"filesystem sandboxing",
		"<cwd>",
		"<shell>",
	} {
		if strings.Contains(text, marker) {
			weakMatches++
		}
	}
	return weakMatches >= 2
}

func dataShareStrongSyntheticUserContextText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.HasPrefix(text, "<system-reminder") || strings.HasPrefix(text, "<system_reminder") {
		return true
	}
	for _, marker := range []string{
		"<command-message>",
		"<environment_context>",
		"<permissions instructions>",
		"base directory for this skill",
		"mcp server instructions",
		"deferred tools are now available",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if strings.Contains(text, "subagent context") && strings.Contains(text, "you are running as a subagent") {
		return true
	}
	if strings.Contains(text, "[important:") && strings.Contains(text, "the user has invoked the") && strings.Contains(text, "skill") {
		return true
	}
	if strings.Contains(text, "todo list is currently empty") && strings.Contains(text, "do not mention this to the user") {
		return true
	}
	return false
}

func dataShareOrderedReplaySkipLen(existing []map[string]any, incomingLen int, incomingStart int, existingIdentityAt func(int) string, incomingIdentityAt func(int) string, existingIdentityPositions map[string][]int) int {
	if len(existing) == 0 || incomingStart >= incomingLen {
		return 0
	}
	incomingIndex := incomingStart
	existingCursor := 0
	var lastIncomingIndex int
	matched := 0
	for incomingIndex < incomingLen {
		position := dataShareNextIdentityPosition(existingIdentityPositions[incomingIdentityAt(incomingIndex)], existingCursor)
		if position < 0 {
			break
		}
		lastIncomingIndex = incomingIndex
		matched++
		incomingIndex++
		existingCursor = position + 1
	}
	if matched < dataShareReplayOverlapMinMessages {
		return 0
	}
	end := incomingIndex
	if dataShareShouldKeepPotentialReplayTailUser(incomingLen, incomingIndex, lastIncomingIndex, incomingIdentityAt) {
		end = lastIncomingIndex
	}
	if end <= incomingStart {
		return 0
	}
	return end - incomingStart
}

func dataShareOrderedReplayPrefixSafe(prefix int, ordered int, incomingStart int, incomingIdentityAt func(int) string) bool {
	if prefix < dataShareReplayOverlapMinMessages || ordered < 5 {
		return false
	}
	firstRole := dataShareIdentityRole(incomingIdentityAt(incomingStart))
	if firstRole != "system" && firstRole != "developer" {
		return false
	}
	for i := incomingStart; i < incomingStart+ordered; i++ {
		switch dataShareIdentityRole(incomingIdentityAt(i)) {
		case "assistant", "tool":
			return true
		}
	}
	return false
}

func dataShareNextIdentityPosition(positions []int, cursor int) int {
	if len(positions) == 0 {
		return -1
	}
	index := sort.SearchInts(positions, cursor)
	if index >= len(positions) {
		return -1
	}
	return positions[index]
}

func dataShareShouldKeepPotentialReplayTailUser(incomingLen int, incomingIndex int, lastIncomingIndex int, incomingIdentityAt func(int) string) bool {
	if lastIncomingIndex < 0 || dataShareIdentityRole(incomingIdentityAt(lastIncomingIndex)) != "user" {
		return false
	}
	if incomingIndex >= incomingLen {
		return true
	}
	return dataShareIdentityRole(incomingIdentityAt(incomingIndex)) == "assistant" || dataShareIdentityRole(incomingIdentityAt(incomingIndex)) == "tool"
}

func dataShareIdentityRole(identity string) string {
	if strings.HasPrefix(identity, "assistant_tool_calls:") {
		return "assistant"
	}
	if before, _, ok := strings.Cut(identity, ":"); ok {
		return before
	}
	return ""
}

// dataShareReplayPrefixHasSeenToolEcho 用已出现过的工具调用/result id 作为无边界 compact 的强 replay 信号，避免误删普通文本重复。
func dataShareReplayPrefixHasSeenToolEcho(messages []map[string]any, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) bool {
	for _, msg := range messages {
		if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" {
			if id := strings.TrimSpace(stringFromAny(msg["tool_call_id"])); id != "" {
				if _, ok := seenToolResults[id]; ok {
					return true
				}
			}
			continue
		}
		for _, id := range dataShareToolCallIDs(msg) {
			if _, ok := seenToolCalls[id]; ok {
				return true
			}
		}
	}
	return false
}

// dataShareMessagesAreExistingPrefix 判断 incoming 是否只是已聚合快照的前缀重放。
func dataShareMessagesAreExistingPrefix(existing, incoming []map[string]any) bool {
	if len(existing) == 0 || len(incoming) < dataShareReplayOverlapMinMessages || len(incoming) > len(existing) {
		return false
	}
	existingIdentities := dataShareMessageIdentities(existing)
	incomingIdentities := dataShareMessageIdentities(incoming)
	for i := range incoming {
		if existingIdentities[i] != incomingIdentities[i] {
			return false
		}
	}
	return true
}

func dataShareMessageIdentities(messages []map[string]any) []string {
	out := make([]string, len(messages))
	for i, msg := range messages {
		out[i] = dataShareMessageIdentity(msg)
	}
	return out
}

func dataShareMessageIdentityKeys(messages []map[string]any) []string {
	out := make([]string, len(messages))
	for i, msg := range messages {
		out[i] = dataShareResponsesIdentityKey(dataShareMessageIdentity(msg))
	}
	return out
}

type dataShareReplayMatch struct {
	existingStart int
	incomingStart int
	length        int
}

func dataShareBestIndexedReplayMatch(existingKeys []string, index map[string][]int, incomingKeys []string, incomingStart int) dataShareReplayMatch {
	if len(existingKeys) < dataShareLongReplayMinMessages || len(incomingKeys)-incomingStart < dataShareLongReplayMinMessages {
		return dataShareReplayMatch{}
	}
	// 使用三元组 hash 锁定当前位置候选，再用完整 identity 连续比较确认，避免随 incoming 尾部反复扫描。
	if incomingStart < 0 {
		incomingStart = 0
	}
	best := dataShareReplayMatch{}
	candidates := index[dataShareReplayWindowKey(incomingKeys, incomingStart)]
	if len(candidates) == 0 || len(candidates) > dataShareReplayWindowCandidateLimit {
		return dataShareReplayMatch{}
	}
	for _, pos := range candidates {
		length := dataShareContiguousKeyMatchLen(existingKeys, pos, incomingKeys, incomingStart)
		if length > best.length {
			best = dataShareReplayMatch{existingStart: pos, incomingStart: incomingStart, length: length}
		}
	}
	if best.length < dataShareLongReplayMinMessages {
		return dataShareReplayMatch{}
	}
	return best
}

func dataShareReplayWindowIndex(keys []string) map[string][]int {
	index := map[string][]int{}
	limit := len(keys) - dataShareReplayWindowWidth
	for i := 0; i <= limit; i++ {
		key := dataShareReplayWindowKey(keys, i)
		if key == "" {
			continue
		}
		index[key] = append(index[key], i)
	}
	return index
}

func dataShareReplayWindowKey(keys []string, start int) string {
	if start < 0 || start+dataShareReplayWindowWidth > len(keys) {
		return ""
	}
	for i := 0; i < dataShareReplayWindowWidth; i++ {
		if keys[start+i] == "" {
			return ""
		}
	}
	return strings.Join(keys[start:start+dataShareReplayWindowWidth], "\x00")
}

func dataShareContiguousKeyMatchLen(existingKeys []string, existingStart int, incomingKeys []string, incomingStart int) int {
	length := 0
	for existingStart+length < len(existingKeys) && incomingStart+length < len(incomingKeys) {
		if existingKeys[existingStart+length] == "" || existingKeys[existingStart+length] != incomingKeys[incomingStart+length] {
			break
		}
		length++
	}
	return length
}

func dataShareMessageAlreadySeen(msg map[string]any, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) bool {
	if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" {
		id := strings.TrimSpace(stringFromAny(msg["tool_call_id"]))
		if id == "" {
			return false
		}
		_, ok := seenToolResults[id]
		return ok
	}
	callIDs := dataShareToolCallIDs(msg)
	if len(callIDs) == 0 {
		return false
	}
	for _, id := range callIDs {
		if _, ok := seenToolCalls[id]; !ok {
			return false
		}
	}
	return true
}

func rememberDataShareMessage(msg map[string]any, seenToolCalls map[string]struct{}, seenToolResults map[string]struct{}) {
	if strings.TrimSpace(stringFromAny(msg["role"])) == "tool" {
		if id := strings.TrimSpace(stringFromAny(msg["tool_call_id"])); id != "" {
			seenToolResults[id] = struct{}{}
		}
		return
	}
	for _, id := range dataShareToolCallIDs(msg) {
		seenToolCalls[id] = struct{}{}
	}
}

func dataShareCommentaryEchoAlreadySeen(msg map[string]any, seenAssistantText map[string]int, currentEpoch int) bool {
	if strings.TrimSpace(stringFromAny(msg["phase"])) != "commentary" {
		return false
	}
	key := dataShareAssistantTextKey(msg)
	if key == "" {
		return false
	}
	seenEpoch, ok := seenAssistantText[key]
	return ok && seenEpoch == currentEpoch
}

func rememberDataShareAssistantTextMessage(msg map[string]any, seenAssistantText map[string]int, currentEpoch int) {
	key := dataShareAssistantTextKey(msg)
	if key == "" {
		return
	}
	seenAssistantText[key] = currentEpoch
}

func dataShareAssistantTextKey(msg map[string]any) string {
	if strings.TrimSpace(stringFromAny(msg["role"])) != "assistant" {
		return ""
	}
	if len(anySlice(msg["tool_calls"])) > 0 {
		return ""
	}
	contentValue := firstPresentAny(msg["content"], msg["text"])
	content := strings.TrimSpace(dataShareContentText(contentValue))
	if content == "" || !dataShareContentIdentityCanUseText(contentValue) {
		return ""
	}
	return content
}

func dataShareAssistantTextEchoWindowReset(msg map[string]any) bool {
	if strings.TrimSpace(stringFromAny(msg["role"])) != "user" {
		return false
	}
	content := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
	if content == "" {
		return false
	}
	// 真实用户新输入开启新的去重窗口；AGENTS/环境等合成上下文不应打断 Responses input 回放识别。
	return !dataShareSyntheticUserContextText(strings.ToLower(content))
}

func dataShareToolCallIDs(msg map[string]any) []string {
	calls := anySlice(msg["tool_calls"])
	out := make([]string, 0, len(calls))
	for _, raw := range calls {
		call, ok := mapFromAny(raw)
		if !ok {
			continue
		}
		if id := strings.TrimSpace(stringFromAny(call["id"])); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// dataShareMessageIdentity 生成稳定身份，忽略 Responses message 的 id/status/type 等易变字段。
func dataShareMessageIdentity(msg map[string]any) string {
	role := strings.TrimSpace(stringFromAny(msg["role"]))
	if role == "" {
		return string(mustJSON(msg))
	}
	if role == "tool" {
		if id := strings.TrimSpace(stringFromAny(msg["tool_call_id"])); id != "" {
			return "tool:" + id
		}
	}
	if role == "assistant" {
		if calls := anySlice(msg["tool_calls"]); len(calls) > 0 {
			return "assistant_tool_calls:" + dataShareToolCallsIdentity(calls)
		}
	}
	contentValue := firstPresentAny(msg["content"], msg["text"])
	content := strings.TrimSpace(dataShareContentText(contentValue))
	if content != "" && dataShareContentIdentityCanUseText(contentValue) {
		return role + ":content:" + content
	}
	return role + ":structured:" + string(mustJSON(dataShareMessageIdentityPayload(msg, role)))
}

// dataShareMessageIdentityPayload 只清理 Responses 外层易变字段；其它字段可能承载 reasoning/refusal 等语义，必须参与身份。
func dataShareMessageIdentityPayload(msg map[string]any, role string) map[string]any {
	out := cloneDataShareMap(msg)
	delete(out, "id")
	delete(out, "status")
	delete(out, "type")
	if role != "" {
		out["role"] = role
	}
	if contentValue, ok := out["content"]; ok {
		out["content"] = normalizeDataShareContentValue(contentValue)
	}
	if textValue, ok := out["text"]; ok {
		out["text"] = normalizeDataShareContentValue(textValue)
	}
	return out
}

func dataShareContentIdentityCanUseText(value any) bool {
	switch v := value.(type) {
	case nil, string:
		return true
	case []any:
		if len(v) == 0 {
			return true
		}
		for _, item := range v {
			block, ok := mapFromAny(item)
			if !ok {
				return false
			}
			if !dataShareContentBlockIdentityCanUseText(block) {
				return false
			}
		}
		return true
	case []map[string]any:
		if len(v) == 0 {
			return true
		}
		for _, block := range v {
			if !dataShareContentBlockIdentityCanUseText(block) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func dataShareContentBlockIdentityCanUseText(block map[string]any) bool {
	blockType := strings.TrimSpace(stringFromAny(block["type"]))
	if blockType != "" {
		switch blockType {
		case "input_text", "output_text", "text":
		default:
			return false
		}
	}
	for key := range block {
		switch key {
		case "type", "text", "content":
			continue
		default:
			return false
		}
	}
	return true
}

// dataShareToolCallsIdentity 使用工具调用的业务字段生成身份，避免上游包装字段影响重放识别。
func dataShareToolCallsIdentity(calls []any) string {
	normalized := make([]map[string]any, 0, len(calls))
	for _, raw := range calls {
		call, ok := mapFromAny(raw)
		if !ok {
			normalized = append(normalized, map[string]any{"raw": raw})
			continue
		}
		functionMap, _ := mapFromAny(call["function"])
		normalized = append(normalized, map[string]any{
			"id":        firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"])),
			"name":      firstNonBlank(stringFromAny(call["name"]), stringFromAny(functionMap["name"]), stringFromAny(call["type"])),
			"arguments": normalizeToolArguments(firstPresentAny(call["arguments"], functionMap["arguments"], call["input"])),
		})
	}
	return string(mustJSON(normalized))
}

func dataShareCommonPrefixLen(leftLen int, leftIdentityAt func(int) string, rightStart int, rightLen int, rightIdentityAt func(int) string) int {
	limit := leftLen
	if remaining := rightLen - rightStart; remaining < limit {
		limit = remaining
	}
	for i := 0; i < limit; i++ {
		if leftIdentityAt(i) != rightIdentityAt(rightStart+i) {
			return i
		}
	}
	return limit
}

func normalizeDataShareMessage(msg map[string]any) map[string]any {
	if msg == nil {
		return nil
	}
	msgType := strings.TrimSpace(stringFromAny(msg["type"]))
	switch msgType {
	case "function_call":
		return normalizeResponsesFunctionCallMessage(msg)
	case "function_call_output":
		return normalizeToolResultMessage(msg)
	}
	out := cloneDataShareMap(msg)
	role := normalizeResponsesInputRole(stringFromAny(out["role"]), msgType)
	if role != "" {
		out["role"] = role
	}
	if role == "tool" {
		return normalizeToolResultMessage(out)
	}
	if role == "assistant" {
		if calls := normalizeToolCalls(out["tool_calls"]); len(calls) > 0 {
			out["tool_calls"] = calls
			if _, ok := out["finish_reason"]; !ok {
				out["finish_reason"] = "tool_calls"
			}
		} else {
			delete(out, "tool_calls")
		}
	}
	if content, ok := out["content"]; ok {
		out["content"] = normalizeDataShareContentValue(content)
	} else if text := strings.TrimSpace(stringFromAny(out["text"])); text != "" {
		out["content"] = text
	}
	delete(out, "type")
	return out
}

func normalizeResponsesFunctionCallMessage(msg map[string]any) map[string]any {
	functionMap, _ := mapFromAny(msg["function"])
	call := map[string]any{
		"id":        firstNonBlank(stringFromAny(msg["call_id"]), stringFromAny(msg["id"]), stringFromAny(msg["tool_call_id"])),
		"name":      firstNonBlank(stringFromAny(msg["name"]), stringFromAny(functionMap["name"])),
		"arguments": normalizeToolArguments(firstPresentAny(msg["arguments"], functionMap["arguments"], msg["input"])),
	}
	return map[string]any{
		"role":          "assistant",
		"content":       normalizeDataShareContentValue(msg["content"]),
		"tool_calls":    []map[string]any{call},
		"finish_reason": "tool_calls",
	}
}

func normalizeToolResultMessage(msg map[string]any) map[string]any {
	callID := firstNonBlank(
		stringFromAny(msg["tool_call_id"]),
		stringFromAny(msg["call_id"]),
		stringFromAny(msg["tool_use_id"]),
		stringFromAny(msg["id"]),
	)
	content := normalizeDataShareContentValue(firstPresentAny(msg["content"], msg["output"], msg["result"], msg["error"]))
	isError := boolFromAny(msg["is_error"]) || dataShareStatusIsError(stringFromAny(msg["status"])) || dataShareToolContentLooksError(content) || msg["error"] != nil
	status := strings.TrimSpace(stringFromAny(msg["status"]))
	if status == "" {
		if isError {
			status = "error"
		} else {
			status = "success"
		}
	}
	out := map[string]any{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      content,
		"status":       status,
		"is_error":     isError,
	}
	if errMsg := strings.TrimSpace(stringFromAny(msg["error_message"])); errMsg != "" {
		out["error_message"] = errMsg
	}
	return out
}

func normalizeToolCalls(value any) []map[string]any {
	rawCalls := anySlice(value)
	out := make([]map[string]any, 0, len(rawCalls))
	for _, raw := range rawCalls {
		call, ok := mapFromAny(raw)
		if !ok {
			continue
		}
		functionMap, _ := mapFromAny(call["function"])
		arguments := firstPresentAny(call["arguments"], functionMap["arguments"], call["input"])
		if arguments == nil && len(functionMap) > 0 {
			arguments = functionMap
		}
		out = append(out, map[string]any{
			"id":        firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"])),
			"name":      firstNonBlank(stringFromAny(call["name"]), stringFromAny(functionMap["name"]), stringFromAny(call["type"])),
			"arguments": normalizeToolArguments(arguments),
		})
	}
	return out
}

func normalizeToolArguments(value any) any {
	value = firstPresentAny(value)
	if value == nil {
		return map[string]any{}
	}
	if raw, ok := value.(string); ok {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return map[string]any{}
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			return parsed
		}
		return raw
	}
	return value
}

func dataShareStatusIsError(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "error", "failed", "failure":
		return true
	default:
		return false
	}
}

func dataShareToolContentLooksError(content any) bool {
	text := dataShareContentText(content)
	if !strings.Contains(text, "Process exited with code ") {
		return false
	}
	return !strings.Contains(text, "Process exited with code 0")
}

func normalizeDataShareTools(tools []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	seen := map[string]struct{}{}
	var visit func(map[string]any)
	visit = func(tool map[string]any) {
		if nested := mapsFromAny(tool["tools"]); len(nested) > 0 {
			for _, item := range nested {
				visit(item)
			}
		}
		normalized, ok := normalizeDataShareTool(tool)
		if !ok {
			return
		}
		name := stringFromAny(normalized["name"])
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
		out = append(out, normalized)
	}
	for _, tool := range tools {
		visit(tool)
	}
	return out
}

func normalizeDataShareTool(tool map[string]any) (map[string]any, bool) {
	if tool == nil {
		return nil, false
	}
	functionMap, _ := mapFromAny(tool["function"])
	name := firstNonBlank(stringFromAny(tool["name"]), stringFromAny(functionMap["name"]), dataShareToolNameFromType(stringFromAny(tool["type"])))
	description := firstNonBlank(stringFromAny(tool["description"]), stringFromAny(functionMap["description"]), defaultDataShareToolDescription(name, stringFromAny(tool["type"])))
	parameters := firstPresentAny(tool["parameters"], functionMap["parameters"], tool["input_schema"], defaultDataShareToolParameters(name, stringFromAny(tool["type"])))
	parameterMap, ok := mapFromAny(parameters)
	if strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "" || !ok || len(parameterMap) == 0 {
		return nil, false
	}
	out := map[string]any{
		"name":        strings.TrimSpace(name),
		"description": strings.TrimSpace(description),
		"parameters":  parameterMap,
	}
	if toolType := normalizeDataShareToolType(stringFromAny(tool["type"])); toolType != "" {
		out["type"] = toolType
	}
	if strict, ok := tool["strict"]; ok {
		out["strict"] = strict
	}
	return out, true
}

func dataShareToolNameFromType(toolType string) string {
	switch strings.TrimSpace(toolType) {
	case "tool_search":
		return "tool_search"
	case "web_search", "web_search_preview", "web_search_20250305":
		return "web_search"
	default:
		return ""
	}
}

func defaultDataShareToolDescription(name string, toolType string) string {
	switch firstNonBlank(name, toolType) {
	case "apply_patch":
		return "Apply a structured patch to files in the workspace."
	case "tool_search":
		return "Search available deferred tools by text query."
	case "web_search", "web_search_preview", "web_search_20250305":
		return "Search the web for relevant information."
	default:
		return ""
	}
}

func defaultDataShareToolParameters(name string, toolType string) map[string]any {
	switch firstNonBlank(name, dataShareToolNameFromType(toolType)) {
	case "apply_patch":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch": map[string]any{"type": "string", "description": "符合 apply_patch 语法的补丁内容。"},
			},
			"required": []string{"patch"},
		}
	case "web_search":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "搜索关键词。"},
			},
			"required": []string{"query"},
		}
	default:
		return nil
	}
}

func normalizeDataShareToolType(toolType string) string {
	switch strings.TrimSpace(toolType) {
	case "function", "custom", "namespace":
		return strings.TrimSpace(toolType)
	case "tool_search", "web_search", "web_search_preview", "web_search_20250305":
		return "function"
	default:
		return ""
	}
}

func normalizeDataShareContentValue(value any) any {
	if value == nil {
		return ""
	}
	if text := dataShareContentText(value); text != "" && dataShareContentIdentityCanUseText(value) {
		return text
	}
	return value
}

func dataShareContentText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := dataShareContentText(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case []map[string]any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := dataShareContentText(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content", "parts", "output", "summary"} {
			if text := dataShareContentText(v[key]); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func extractSystemPromptFromMessages(messages []map[string]any) string {
	for _, msg := range messages {
		role := strings.TrimSpace(stringFromAny(msg["role"]))
		if role == "system" || role == "developer" {
			if text := strings.TrimSpace(dataShareContentText(msg["content"])); text != "" {
				return text
			}
		}
	}
	return ""
}

func normalizeDataShareUsage(usage map[string]any) map[string]any {
	out := cloneDataShareMap(usage)
	inputTokens := intFromAny(out["input_tokens"])
	outputTokens := intFromAny(out["output_tokens"])
	cacheReadTokens := intFromAny(firstPresentAny(out["cache_read_input_tokens"], out["cache_read_tokens"]))
	cacheCreateTokens := intFromAny(firstPresentAny(out["cache_creation_input_tokens"], out["cache_creation_tokens"]))
	totalTokens := intFromAny(out["total_tokens"])
	if totalTokens <= 0 {
		totalTokens = inputTokens + outputTokens + cacheReadTokens + cacheCreateTokens
	}
	return map[string]any{
		"input_tokens":                inputTokens,
		"output_tokens":               outputTokens,
		"total_tokens":                totalTokens,
		"cache_creation_input_tokens": cacheCreateTokens,
		"cache_read_input_tokens":     cacheReadTokens,
	}
}

func normalizeDataShareMeta(meta map[string]any) map[string]any {
	out := cloneDataShareMap(meta)
	sourceIDs := appendStringValues(nil, stringsFromAny(out["source_request_ids"])...)
	sourceIDs = appendStringValues(sourceIDs, stringsFromAny(out["request_ids"])...)
	sourceIDs = appendStringValues(sourceIDs, stringFromAny(out["request_id"]))
	out["source_request_ids"] = sourceIDs
	delete(out, "request_ids")
	return out
}

func validateDataSharePayloadQuality(payload map[string]any) []string {
	return ValidateDataShareSessionQuality(
		stringFromAny(payload["model"]),
		stringFromAny(payload["system_prompt"]),
		mapsFromAny(payload["messages"]),
		mapsFromAny(payload["tools"]),
		normalizeDataShareUsage(mapAnyFromAny(payload["usage"])),
	)
}

// DataSharePayloadQualityStatus 把附件质量规则归纳成完整、部分完整、无效三态。
func DataSharePayloadQualityStatus(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) string {
	status, _ := DataShareSessionQuality(model, systemPrompt, messages, tools, usage)
	return status
}

func dataShareCompletionState(qualityStatus string) (string, bool) {
	if qualityStatus == DataShareQualityComplete {
		return DataShareStatusCompleted, true
	}
	return DataShareStatusTerminated, false
}

// DataShareQualityExportable 表示默认导出是否应包含该质量状态。
func DataShareQualityExportable(qualityStatus string) bool {
	return qualityStatus == DataShareQualityComplete || qualityStatus == DataShareQualityPartial
}

// exportableDataShareMessages 仅裁掉尾部未闭合工具链，裁切后仍需完整通过同一套交付校验。
func exportableDataShareMessages(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) ([]map[string]any, []string) {
	compact := CompactDataShareMessages(normalizeDataShareMessages(messages))
	if report := evaluateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage); report.Status == DataShareQualityComplete {
		return append([]map[string]any{}, compact...), nil
	} else if report.Status != DataShareQualityPartial {
		return nil, report.Errors
	}
	if end := dataShareCompleteTrimPrefixLen(model, systemPrompt, compact, tools, usage); end > 0 {
		return append([]map[string]any{}, compact[:end]...), nil
	}
	return nil, validateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
}

func exportPayloadFromSession(session *DataShareSession) map[string]any {
	return exportPayloadFromSessionWithOptions(session, false)
}

func exportPayloadFromFinalizedSession(session *DataShareSession) map[string]any {
	return exportPayloadFromSessionWithOptions(session, true)
}

func exportPayloadFromSessionWithOptions(session *DataShareSession, finalized bool) map[string]any {
	if session == nil {
		return map[string]any{}
	}
	payload := cloneDataShareMap(session.SessionJSON)
	payload["trajectory_id"] = session.TrajectoryID
	payload["session_id"] = session.SessionID
	payload["dataset"] = session.Dataset
	payload["provider"] = session.Provider
	payload["model"] = session.Model
	payload["request_path"] = firstNonBlank(session.RequestPath, stringFromAny(payload["request_path"]), stringFromAny(session.Meta["request_path"]), stringFromAny(session.Meta["inbound_endpoint"]))
	payload["user_agent"] = firstNonBlank(session.UserAgent, stringFromAny(payload["user_agent"]), stringFromAny(session.Meta["user_agent"]))
	payload["created_at"] = session.CreatedAt.Format(time.RFC3339Nano)
	if session.EndedAt != nil {
		payload["ended_at"] = session.EndedAt.Format(time.RFC3339Nano)
	}
	payload["status"] = session.Status
	payload["is_final_snapshot"] = session.IsFinalSnapshot
	payload["source_request_count"] = session.SourceRequestCount
	messages := firstNonEmptyMaps(session.Messages, mapsFromAny(payload["messages"]))
	tools := firstNonEmptyMaps(session.Tools, mapsFromAny(payload["tools"]))
	usage := firstNonEmptyMap(session.Usage, mapAnyFromAny(payload["usage"]))
	meta := firstNonEmptyMap(session.Meta, mapAnyFromAny(payload["meta"]))
	if !finalized {
		messages = CompactDataShareMessages(normalizeDataShareMessages(messages))
		tools = normalizeDataShareTools(tools)
		usage = normalizeDataShareUsage(usage)
		meta = normalizeDataShareMeta(meta)
	}
	systemPrompt := firstNonBlank(optionalStringValue(session.SystemPrompt), stringFromAny(payload["system_prompt"]), extractSystemPromptFromMessages(messages))
	payload["system_prompt"] = systemPrompt
	payload["tools"] = tools
	payload["messages"] = messages
	payload["usage"] = usage
	payload["meta"] = meta
	delete(payload, "quality_status")
	return payload
}

// PublicDataShareSessionPayload 返回对外可见 payload，剥离采集内部状态。
func PublicDataShareSessionPayload(payload map[string]any) map[string]any {
	out := cloneDataShareMap(payload)
	if meta := mapAnyFromAny(out["meta"]); meta != nil {
		out["meta"] = stripDataShareInternalCaptureStateFromMeta(meta)
	}
	return out
}

// PublicDataShareSessionMeta 返回对外可见 meta，剥离采集内部状态。
func PublicDataShareSessionMeta(meta map[string]any) map[string]any {
	return stripDataShareInternalCaptureStateFromMeta(meta)
}

// exportDownloadPayloadFromSession 仅用于下载导出，在保留库内原始采集数据的同时剔除敏感字段。
func exportDownloadPayloadFromSession(session *DataShareSession) (map[string]any, error) {
	payload, ok := redactDataShareExportFields(PublicDataShareSessionPayload(exportPayloadFromSession(session))).(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	if err := recheckDataShareExportPayload(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func recheckDataShareExportPayload(payload map[string]any) error {
	messages := mapsFromAny(payload["messages"])
	if len(messages) == 0 {
		return nil
	}
	if dataShareHasReplayDuplicateBlock(messages) {
		return fmt.Errorf("%w: %s", ErrDataShareExportPayloadInvalid, dataShareQualityErrorReplayDuplicateBlock)
	}
	// 导出前再做一轮幂等性复核，兜住达到轮数上限后仍可继续收缩的阶梯 replay。
	if len(compactDataShareMessagesOnce(messages)) != len(messages) {
		return fmt.Errorf("%w: %s", ErrDataShareExportPayloadInvalid, dataShareQualityErrorReplayDuplicateBlock)
	}
	return nil
}

// BuildDataShareSessionPayload 生成可导出、可压缩持久化的规范 session payload。
func BuildDataShareSessionPayload(session *DataShareSession) map[string]any {
	return exportPayloadFromSession(session)
}

// BuildFinalizedDataShareSessionPayload 复用最终化阶段已规范化的字段，避免超大快照重复 compact。
func BuildFinalizedDataShareSessionPayload(session *DataShareSession) map[string]any {
	return exportPayloadFromFinalizedSession(session)
}

func normalizeDataShareProvider(provider string, apiKey *APIKey) string {
	provider = strings.TrimSpace(provider)
	if provider != "" {
		return provider
	}
	if apiKey != nil && apiKey.Group != nil {
		return apiKey.Group.Platform
	}
	return "unknown"
}

func normalizeDataShareRequestPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func normalizeDataShareUserAgent(userAgent string) string {
	userAgent = strings.TrimSpace(userAgent)
	// 统计维度只保留客户端产品名，避免版本号、系统架构把同一客户端打散成大量分组。
	if idx := strings.Index(userAgent, "/"); idx > 0 {
		userAgent = strings.TrimSpace(userAgent[:idx])
	}
	if len(userAgent) > 512 {
		return userAgent[:512]
	}
	return userAgent
}

func normalizeDataShareSessionID(sessionID string, requestID string, body []byte, apiKeyID int64) string {
	for _, candidate := range []string{
		sessionID,
		gjson.GetBytes(body, "session_id").String(),
		gjson.GetBytes(body, "conversation_id").String(),
		gjson.GetBytes(body, "metadata.session_id").String(),
		gjson.GetBytes(body, "metadata.conversation_id").String(),
		gjson.GetBytes(body, "metadata.prompt_cache_key").String(),
		gjson.GetBytes(body, "prompt_cache_key").String(),
		gjson.GetBytes(body, "metadata.user_id").String(),
		requestID,
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}
	sum := sha256.Sum256(append(body, []byte(strconv.FormatInt(apiKeyID, 10))...))
	return hex.EncodeToString(sum[:16])
}

func buildTrajectoryID(provider string, sessionID string, apiKeyID int64, groupID int64) string {
	seed := fmt.Sprintf("%s:%s:%d:%d", provider, sessionID, apiKeyID, groupID)
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

func extractSystemPromptFromRequest(body []byte) string {
	sys := gjson.GetBytes(body, "system")
	if sys.Exists() {
		if sys.Type == gjson.String {
			return sys.String()
		}
		return sys.Raw
	}
	if sys = gjson.GetBytes(body, "system_instruction"); sys.Exists() {
		return sys.Raw
	}
	return ""
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func optionalStringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func firstPresentAny(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}

func boolFromAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true")
	default:
		return false
	}
}

func mapFromAny(v any) (map[string]any, bool) {
	switch x := v.(type) {
	case map[string]any:
		return x, true
	default:
		return nil, false
	}
}

func mapAnyFromAny(v any) map[string]any {
	if m, ok := mapFromAny(v); ok {
		return m
	}
	return nil
}

func mapsFromAny(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return x
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			if m, ok := mapFromAny(item); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func anySlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func stringsFromAny(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(x) == "" {
			return nil
		}
		return []string{x}
	default:
		return nil
	}
}

func appendStringValues(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(values))
	out := make([]string, 0, len(existing)+len(values))
	for _, item := range existing {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func cloneDataShareMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// redactDataShareExportFields 递归清理导出 payload 中的敏感字段，保留其它业务字段。
func redactDataShareExportFields(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if _, excluded := dataShareExportExcludedFields[key]; excluded {
				continue
			}
			out[key] = redactDataShareExportFields(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			redacted, _ := redactDataShareExportFields(item).(map[string]any)
			out = append(out, redacted)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, redactDataShareExportFields(item))
		}
		return out
	default:
		return value
	}
}

func firstNonEmptyMaps(values ...[]map[string]any) []map[string]any {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func firstNonEmptyMap(values ...map[string]any) map[string]any {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func rawJSONToMap(raw string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]any{"raw": raw}
	}
	return out
}

func rawJSONToAny(raw string) any {
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return raw
	}
	return out
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	default:
		return 0
	}
}

// WriteSingleSessionJSONL 输出单条 session 的下载 JSONL，并剔除不允许外发的身份字段。
func WriteSingleSessionJSONL(w io.Writer, session *DataShareSession) error {
	if session == nil {
		return ErrDataShareSessionNotFound
	}
	var buf bytes.Buffer
	payload, err := exportDownloadPayloadFromSession(session)
	if err != nil {
		return err
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := buf.Write(line); err != nil {
		return err
	}
	if err := buf.WriteByte('\n'); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}

func IsDataShareNotFound(err error) bool {
	return errors.Is(err, ErrDataShareSessionNotFound)
}

func cloneDataSharingRequestBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	return append([]byte(nil), body...)
}
