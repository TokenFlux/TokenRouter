package backup

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"io"

	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	settingKeyBackupS3Config      = "backup_s3_config"
	settingKeyBackupStorageConfig = "backup_storage_config"
	settingKeyBackupContentConfig = "backup_content_config"
	settingKeyBackupSchedule      = "backup_schedule"
	settingKeyBackupRecords       = "backup_records"

	BackupStorageTypeLocal = "local"
	BackupStorageTypeS3    = "s3"

	BackupS3UploadModeMultipart  = "multipart"
	BackupS3UploadModeSpooledPut = "spooled_put"

	maxBackupRecords           = 100
	backupObjectCleanupTimeout = 2 * time.Minute
)

var (
	ErrBackupS3NotConfigured      = infraerrors.BadRequest("BACKUP_S3_NOT_CONFIGURED", "backup S3 storage is not configured")
	ErrBackupStorageNotConfigured = infraerrors.BadRequest("BACKUP_STORAGE_NOT_CONFIGURED", "backup storage is not configured")
	ErrBackupNotFound             = infraerrors.NotFound("BACKUP_NOT_FOUND", "backup record not found")
	ErrBackupInProgress           = infraerrors.Conflict("BACKUP_IN_PROGRESS", "a backup is already in progress")
	ErrRestoreInProgress          = infraerrors.Conflict("RESTORE_IN_PROGRESS", "a restore is already in progress")
	ErrBackupRecordsCorrupt       = infraerrors.InternalServer("BACKUP_RECORDS_CORRUPT", "backup records data is corrupted")
	ErrBackupS3ConfigCorrupt      = infraerrors.InternalServer("BACKUP_S3_CONFIG_CORRUPT", "backup S3 config data is corrupted")
	ErrBackupStorageConfigCorrupt = infraerrors.InternalServer("BACKUP_STORAGE_CONFIG_CORRUPT", "backup storage config data is corrupted")
	ErrBackupContentConfigCorrupt = infraerrors.InternalServer("BACKUP_CONTENT_CONFIG_CORRUPT", "backup content config data is corrupted")
	ErrDatabaseMaintenanceBusy    = infraerrors.Conflict("MAINTENANCE_BUSY", "another database maintenance task is running")
	// ErrSecretEncryptionKeyNotConfigured 表示当前使用的是重启后会变化的临时密钥，不能持久化新的 S3 密钥。
	ErrSecretEncryptionKeyNotConfigured = infraerrors.BadRequest(
		"SECRET_ENCRYPTION_KEY_NOT_CONFIGURED",
		"cannot store the S3 secret access key: no fixed secret encryption key is configured, so the auto-generated key would change on every restart and make the stored secret undecryptable after a restart or upgrade. Set a fixed TOTP_ENCRYPTION_KEY (e.g. generate one with `openssl rand -hex 32`) and try again",
	)
)

var backupContentTableDataGroups = map[string][]string{
	"usage_records": {
		"public.usage_logs",
		"public.usage_logs_*",
		"public.billing_usage_entries",
		"public.usage_billing_dedup",
		"public.usage_billing_dedup_archive",
		"public.usage_dashboard_hourly",
		"public.usage_dashboard_daily",
		"public.usage_dashboard_hourly_users",
		"public.usage_dashboard_daily_users",
		"public.usage_dashboard_aggregation_watermark",
		"public.usage_analytics_hourly",
		"public.usage_analytics_daily",
		"public.usage_analytics_aggregation_state",
	},
	"ops_logs": {
		"public.ops_system_logs",
		"public.ops_error_logs",
		"public.ops_retry_attempts",
		"public.ops_system_metrics",
		"public.ops_metrics_hourly",
		"public.ops_metrics_daily",
		"public.ops_alert_events",
		"public.ops_job_heartbeats",
		"public.ops_system_log_cleanup_audits",
	},
	"audit_logs": {
		"public.payment_audit_logs",
		"public.content_moderation_logs",
		"public.announcement_reads",
		"public.orphan_allowed_groups_audit",
		"public.auth_identity_migration_reports",
	},
	"runtime_data": {
		"public.idempotency_records",
		"public.scheduler_outbox",
		"public.pending_auth_sessions",
		"public.identity_adoption_decisions",
		"public.usage_cleanup_tasks",
		"public.scheduled_test_results",
	},
}

// ─── 接口定义 ───

// DBDumper 抽象数据库导出和恢复操作。
type DBDumper interface {
	Dump(ctx context.Context, opts BackupDumpOptions) (io.ReadCloser, error)
	Restore(ctx context.Context, data io.Reader) error
}

// BackupDumpOptions 控制 pg_dump 的导出范围。
type BackupDumpOptions struct {
	ExcludeTableData []string
}

// BackupObjectStore 抽象备份文件存储后端。
type BackupObjectStore interface {
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error)
	UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	HeadBucket(ctx context.Context) error
}

// BackupObjectStoreProgressUploader 是支持上报上传进度的对象存储扩展接口。
type BackupObjectStoreProgressUploader interface {
	UploadFileWithProgress(ctx context.Context, key string, body io.Reader, contentType string, onProgress func(uploadedBytes int64)) (sizeBytes int64, err error)
}

// BackupObjectStoreSizedUploader 可直接上传已知长度的分卷，避免兼容模式再次落盘计算大小。
type BackupObjectStoreSizedUploader interface {
	UploadSized(ctx context.Context, key string, body io.Reader, contentType string, sizeBytes int64) (int64, error)
}

// BackupObjectStoreFactory 根据 S3 配置创建对象存储客户端。
type BackupObjectStoreFactory func(ctx context.Context, cfg *BackupS3Config) (BackupObjectStore, error)

// ─── 数据模型 ───

// BackupStorageConfig 备份存储配置，决定后续新备份写入本地还是远程对象存储。
type BackupStorageConfig struct {
	Type      string         `json:"type"`
	LocalPath string         `json:"local_path"`
	S3        BackupS3Config `json:"s3"`
}

// BackupContentConfig 控制备份文件中包含哪些非核心历史数据。
type BackupContentConfig struct {
	IncludeUsageRecords bool     `json:"include_usage_records"`
	IncludeOpsLogs      bool     `json:"include_ops_logs"`
	IncludeAuditLogs    bool     `json:"include_audit_logs"`
	IncludeRuntimeData  bool     `json:"include_runtime_data"`
	ExcludedTableData   []string `json:"excluded_table_data,omitempty"`
}

// BackupS3Config S3 兼容存储配置（支持 Cloudflare R2）
type BackupS3Config struct {
	Endpoint          string `json:"endpoint"` // 例如 https://<account_id>.r2.cloudflarestorage.com
	Region            string `json:"region"`   // R2 用 "auto"
	Bucket            string `json:"bucket"`
	AccessKeyID       string `json:"access_key_id"`
	SecretAccessKey   string `json:"secret_access_key,omitempty"` //nolint:revive // 字段名沿用 AWS 约定
	Prefix            string `json:"prefix"`                      // S3 key 前缀，如 "backups/"
	ForcePathStyle    bool   `json:"force_path_style"`
	UploadConcurrency int    `json:"upload_concurrency"`
	UploadPartSizeMB  int    `json:"upload_part_size_mb"`
	UploadMode        string `json:"upload_mode"`
}

// IsConfigured 检查必要字段是否已配置
func (c *BackupS3Config) IsConfigured() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// BackupScheduleConfig 定时备份配置
type BackupScheduleConfig struct {
	Enabled     bool   `json:"enabled"`
	CronExpr    string `json:"cron_expr"`    // cron 表达式，如 "0 2 * * *" 每天凌晨2点
	RetainDays  int    `json:"retain_days"`  // 备份文件过期天数，默认14，0=不自动清理
	RetainCount int    `json:"retain_count"` // 最多保留份数，0=不限制
}

// BackupRecord 备份记录
type BackupRecord struct {
	ID            string       `json:"id"`
	Status        string       `json:"status"`      // pending, running, completed, failed
	BackupType    string       `json:"backup_type"` // postgres
	FileName      string       `json:"file_name"`
	StorageType   string       `json:"storage_type,omitempty"`
	StorageKey    string       `json:"storage_key,omitempty"`
	S3Key         string       `json:"s3_key"` // 兼容旧版本远程备份记录
	Parts         []BackupPart `json:"parts,omitempty"`
	SizeBytes     int64        `json:"size_bytes"`
	TriggeredBy   string       `json:"triggered_by"` // manual, scheduled
	ErrorMsg      string       `json:"error_message,omitempty"`
	StartedAt     string       `json:"started_at"`
	FinishedAt    string       `json:"finished_at,omitempty"`
	ExpiresAt     string       `json:"expires_at,omitempty"`     // 过期时间
	Progress      string       `json:"progress,omitempty"`       // "pending", "dumping", "uploading", ""
	RestoreStatus string       `json:"restore_status,omitempty"` // "", "running", "completed", "failed"
	RestoreError  string       `json:"restore_error,omitempty"`
	RestoredAt    string       `json:"restored_at,omitempty"`
}

// BackupDownloadPart 描述一个可下载的备份分卷。
type BackupDownloadPart struct {
	Index     int    `json:"index"`
	SizeBytes int64  `json:"size_bytes"`
	URL       string `json:"url"`
}

// BackupDownloadResponse 兼容单文件 URL 与分卷 URL 列表。
type BackupDownloadResponse struct {
	URL   string               `json:"url,omitempty"`
	Parts []BackupDownloadPart `json:"parts,omitempty"`
}

// BackupService 数据库备份恢复服务
type BackupService struct {
	cleanupCancels  map[uint64]context.CancelFunc
	cleanupSequence uint64

	log func(string, string, ...any)

	started             bool
	startDone, stopDone chan struct{}
	stopOnce            sync.Once
	stopErr             error
	shutdownContext     context.Context

	operationLifecycleMu sync.RWMutex
	settingRepo          SettingRepository
	databaseName         string
	localPath            string
	now                  func() time.Time
	archive              ArchiveExecutor
	maintenance          MaintenanceLock
	encryptor            SecretEncryptor
	// encryptionKeyConfigured 标记加密密钥是否由部署显式配置；临时密钥不能用于持久化新凭证。
	encryptionKeyConfigured bool
	storeFactory            BackupObjectStoreFactory

	localStore BackupObjectStore

	opMu      sync.Mutex // 保护 backingUp/restoring 标志
	backingUp bool
	restoring bool

	storeMu sync.Mutex // 保护 store/s3Cfg 缓存
	store   BackupObjectStore
	s3Cfg   *BackupS3Config

	recordsMu sync.Mutex // 保护 records 的 load/save 操作

	cronMu      sync.Mutex
	cronSched   *cron.Cron
	cronEntryID cron.EntryID

	wg           sync.WaitGroup     // 追踪活跃的备份/恢复 goroutine
	shuttingDown atomic.Bool        // 阻止新备份启动
	bgCtx        context.Context    // 所有后台操作的 parent context
	bgCancel     context.CancelFunc // 取消所有活跃后台操作
}

// SetMaintenanceDB 注入数据库连接，使备份与其他数据库重任务共享互斥锁。

func (s *BackupService) recoverStaleRecords() {
	loadCtx, loadCancel := context.WithTimeout(s.bgCtx, 10*time.Second)
	defer loadCancel()

	records, err := s.loadRecords(loadCtx)
	if err != nil {
		return
	}
	for i := range records {
		if records[i].Status == "running" {
			staleRecord := records[i]
			records[i].Status = "failed"
			records[i].ErrorMsg = "interrupted by server restart"
			records[i].Progress = ""
			records[i].FinishedAt = s.now().Format(time.RFC3339)
			s.saveRecoveredRecord(&records[i])
			if cleanupErr := s.cleanupStaleBackupObjects(&staleRecord); cleanupErr != nil {
				records[i].ErrorMsg = fmt.Sprintf("interrupted by server restart; cleanup failed, manual deletion may be required: %v", cleanupErr)
				s.saveRecoveredRecord(&records[i])
				s.log("service.backup", "[Backup] failed to clean stale backup objects for %s: %v", records[i].ID, cleanupErr)
			}
			s.log("service.backup", "[Backup] recovered stale running record: %s", records[i].ID)
		}
		if records[i].RestoreStatus == "running" {
			records[i].RestoreStatus = "failed"
			records[i].RestoreError = "interrupted by server restart"
			s.saveRecoveredRecord(&records[i])
			s.log("service.backup", "[Backup] recovered stale restoring record: %s", records[i].ID)
		}
	}
}

func (s *BackupService) saveRecoveredRecord(record *BackupRecord) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 10*time.Second)
	defer cancel()
	if err := s.saveRecord(ctx, record); err != nil {
		s.log("service.backup", "[Backup] 保存恢复后的备份记录失败 %s: %v", record.ID, err)
	}
}

func (s *BackupService) cleanupStaleBackupObjects(record *BackupRecord) error {
	if len(BackupObjectKeys(record)) == 0 {
		return nil
	}
	ctx, cancel := s.cleanupContext(backupObjectCleanupTimeout)
	defer cancel()
	return s.deleteBackupObjects(ctx, record)
}

// Stop 停止定时备份并等待活跃操作完成
func (s *BackupService) EncryptionKeyConfigured() bool {
	return s != nil && s.encryptionKeyConfigured
}

func (s *BackupService) GetStorageConfig(ctx context.Context) (*BackupStorageConfig, error) {
	cfg, err := s.loadStorageConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &BackupStorageConfig{Type: BackupStorageTypeLocal}
	}
	cfg.LocalPath = s.localPath
	cfg.S3.UploadMode = NormalizeBackupS3UploadMode(cfg.S3.UploadMode)
	cfg.S3.SecretAccessKey = ""
	return cfg, nil
}

func (s *BackupService) UpdateStorageConfig(ctx context.Context, cfg BackupStorageConfig) (*BackupStorageConfig, error) {
	normalizedType := normalizeBackupStorageType(cfg.Type)
	if normalizedType == "" {
		return nil, infraerrors.BadRequest("INVALID_BACKUP_STORAGE_TYPE", "backup storage type must be local or s3")
	}
	cfg.Type = normalizedType
	cfg.LocalPath = s.localPath

	if cfg.Type == BackupStorageTypeS3 {
		s3Cfg, err := s.prepareS3ConfigForSave(ctx, cfg.S3)
		if err != nil {
			return nil, err
		}
		if !s3Cfg.IsConfigured() {
			return nil, ErrBackupS3NotConfigured
		}
		cfg.S3 = *s3Cfg
	} else {
		// 本地模式只切换写入目标，保留已保存的远程配置，方便用户后续切回远程备份。
		cfg.S3 = s.loadStoredEncryptedS3Config(ctx)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal storage config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupStorageConfig, string(data)); err != nil {
		return nil, fmt.Errorf("save storage config: %w", err)
	}

	if cfg.Type == BackupStorageTypeS3 {
		// 继续写旧 key，兼容旧接口和旧版本回滚。
		s3Data, err := json.Marshal(cfg.S3)
		if err != nil {
			return nil, fmt.Errorf("marshal s3 config: %w", err)
		}
		if err := s.settingRepo.Set(ctx, settingKeyBackupS3Config, string(s3Data)); err != nil {
			return nil, fmt.Errorf("save s3 config: %w", err)
		}
	}

	s.resetCachedS3Store()

	cfg.S3.SecretAccessKey = ""
	return &cfg, nil
}

func (s *BackupService) TestStorageConnection(ctx context.Context, cfg BackupStorageConfig) error {
	switch normalizeBackupStorageType(cfg.Type) {
	case BackupStorageTypeLocal, "":
		return s.localStore.HeadBucket(ctx)
	case BackupStorageTypeS3:
		return s.TestS3Connection(ctx, cfg.S3)
	default:
		return infraerrors.BadRequest("INVALID_BACKUP_STORAGE_TYPE", "backup storage type must be local or s3")
	}
}

// ─── 备份内容配置管理 ───

func (s *BackupService) GetContentConfig(ctx context.Context) (*BackupContentConfig, error) {
	cfg, err := s.loadContentConfig(ctx)
	if err != nil {
		return nil, err
	}
	cfg.ExcludedTableData = s.buildExcludedTableData(cfg)
	return cfg, nil
}

func (s *BackupService) UpdateContentConfig(ctx context.Context, cfg BackupContentConfig) (*BackupContentConfig, error) {
	normalized := normalizeBackupContentConfig(cfg)
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal content config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupContentConfig, string(data)); err != nil {
		return nil, fmt.Errorf("save content config: %w", err)
	}
	normalized.ExcludedTableData = s.buildExcludedTableData(&normalized)
	return &normalized, nil
}

// ─── S3 配置管理（兼容旧接口） ───

func (s *BackupService) GetS3Config(ctx context.Context) (*BackupS3Config, error) {
	cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		if storageCfg, err := s.loadStorageConfig(ctx); err == nil && storageCfg != nil && storageCfg.S3.IsConfigured() {
			cfg = &storageCfg.S3
		}
	}
	if cfg == nil {
		return &BackupS3Config{UploadMode: BackupS3UploadModeSpooledPut}, nil
	}
	// 脱敏返回
	cfg.UploadMode = NormalizeBackupS3UploadMode(cfg.UploadMode)
	cfg.SecretAccessKey = ""
	return cfg, nil
}

func (s *BackupService) UpdateS3Config(ctx context.Context, cfg BackupS3Config) (*BackupS3Config, error) {
	prepared, err := s.prepareS3ConfigForSave(ctx, cfg)
	if err != nil {
		return nil, err
	}
	cfg = *prepared

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal s3 config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupS3Config, string(data)); err != nil {
		return nil, fmt.Errorf("save s3 config: %w", err)
	}

	storageCfg, _ := s.loadStorageConfig(ctx)
	if storageCfg != nil {
		storageCfg.S3 = cfg
		if storageData, err := json.Marshal(storageCfg); err == nil {
			_ = s.settingRepo.Set(ctx, settingKeyBackupStorageConfig, string(storageData))
		}
	}

	s.resetCachedS3Store()

	cfg.SecretAccessKey = ""
	return &cfg, nil
}

func (s *BackupService) TestS3Connection(ctx context.Context, cfg BackupS3Config) error {
	// 如果没提供 secret，用已保存的
	if cfg.SecretAccessKey == "" {
		old, _ := s.loadS3Config(ctx)
		if old != nil {
			cfg.SecretAccessKey = old.SecretAccessKey
		}
	}

	if cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return fmt.Errorf("incomplete S3 config: bucket, access_key_id, secret_access_key are required")
	}

	store, err := s.storeFactory(ctx, &cfg)
	if err != nil {
		return err
	}
	return store.HeadBucket(ctx)
}

// ─── 定时备份管理 ───

func (s *BackupService) GetSchedule(ctx context.Context) (*BackupScheduleConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupSchedule)
	if err != nil || raw == "" {
		return &BackupScheduleConfig{}, nil
	}
	var cfg BackupScheduleConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return &BackupScheduleConfig{}, nil
	}
	return &cfg, nil
}

func (s *BackupService) UpdateSchedule(ctx context.Context, cfg BackupScheduleConfig) (*BackupScheduleConfig, error) {
	if cfg.Enabled && cfg.CronExpr == "" {
		return nil, infraerrors.BadRequest("INVALID_CRON", "cron expression is required when schedule is enabled")
	}
	// 验证 cron 表达式
	if cfg.CronExpr != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(cfg.CronExpr); err != nil {
			return nil, infraerrors.BadRequest("INVALID_CRON", fmt.Sprintf("invalid cron expression: %v", err))
		}
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal schedule config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupSchedule, string(data)); err != nil {
		return nil, fmt.Errorf("save schedule config: %w", err)
	}

	// 应用或停止定时任务
	if cfg.Enabled {
		if err := s.applyCronSchedule(&cfg); err != nil {
			return nil, err
		}
	} else {
		s.removeCronSchedule()
	}

	return &cfg, nil
}

func (s *BackupService) applyCronSchedule(cfg *BackupScheduleConfig) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()

	if s.cronSched == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}

	// 移除旧任务
	if s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
	}

	entryID, err := s.cronSched.AddFunc(cfg.CronExpr, func() {
		s.runScheduledBackup()
	})
	if err != nil {
		return infraerrors.BadRequest("INVALID_CRON", fmt.Sprintf("failed to schedule: %v", err))
	}
	s.cronEntryID = entryID
	s.log("service.backup", "[Backup] 定时备份已启用: %s", cfg.CronExpr)
	return nil
}

func (s *BackupService) removeCronSchedule() {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if s.cronSched != nil && s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
		s.log("service.backup", "[Backup] 定时备份已停用")
	}
}

func (s *BackupService) runScheduledBackup() {
	ctx0, done, err := s.begin(s.bgCtx)
	if err != nil {
		return
	}
	defer done()

	ctx, cancel := context.WithTimeout(ctx0, 30*time.Minute)
	defer cancel()

	// 读取定时备份配置中的过期天数
	schedule, _ := s.GetSchedule(ctx)
	expireDays := 14 // 默认14天过期
	if schedule != nil && schedule.RetainDays > 0 {
		expireDays = schedule.RetainDays
	}

	s.log("service.backup", "[Backup] 开始执行定时备份, 过期天数: %d", expireDays)
	record, err := s.createBackup(ctx, "scheduled", expireDays)
	if err != nil {
		if errors.Is(err, ErrBackupInProgress) {
			s.log("service.backup", "[Backup] 定时备份跳过: 已有备份正在进行中")
		} else {
			s.log("service.backup", "[Backup] 定时备份失败: %v", err)
		}
		return
	}
	s.log("service.backup", "[Backup] 定时备份完成: id=%s size=%d", record.ID, record.SizeBytes)

	// 清理过期备份（复用已加载的 schedule）
	if schedule == nil {
		return
	}
	if err := s.cleanupOldBackups(ctx, schedule); err != nil {
		s.log("service.backup", "[Backup] 清理过期备份失败: %v", err)
	}
}

// ─── 备份/恢复核心 ───

// CreateBackup 创建全量数据库备份并写入当前配置的存储后端（流式处理）
// expireDays: 备份过期天数，0=永不过期，默认14天
func (s *BackupService) createBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	releaseMaintenance, acquired, err := s.maintenance(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire database maintenance lock: %w", err)
	}
	if !acquired {
		return nil, ErrDatabaseMaintenanceBusy
	}
	defer releaseMaintenance()

	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()
	defer func() {
		s.opMu.Lock()
		s.backingUp = false
		s.opMu.Unlock()
	}()

	storageType, objectStore, s3Cfg, err := s.getCurrentBackupStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	now := s.now()
	backupID := uuid.New().String()[:8]
	fileName := fmt.Sprintf("%s_%s.sql.gz", s.databaseName, now.Format("20060102_150405"))
	storageKey := s.buildStorageKey(storageType, s3Cfg, fileName)

	var expiresAt string
	if expireDays > 0 {
		expiresAt = now.AddDate(0, 0, expireDays).Format(time.RFC3339)
	}

	record := &BackupRecord{
		ID:          backupID,
		Status:      "running",
		BackupType:  "postgres",
		FileName:    fileName,
		StorageType: storageType,
		StorageKey:  storageKey,
		S3Key:       legacyS3Key(storageType, storageKey),
		TriggeredBy: triggeredBy,
		StartedAt:   now.Format(time.RFC3339),
		ExpiresAt:   expiresAt,
	}

	dumpOptions, err := s.buildDumpOptions(ctx)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = fmt.Sprintf("load backup content config failed: %v", err)
		record.FinishedAt = s.now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, err
	}

	record.Progress = "dumping"
	_ = s.saveRecord(ctx, record)
	sizeBytes, err := s.archive.Write(ctx, record, objectStore, s3Cfg, dumpOptions, s.saveRecord, s.cleanupContext)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = err.Error()
		record.Progress = ""
		record.FinishedAt = s.now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, err
	}

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.Progress = ""
	record.FinishedAt = s.now().Format(time.RFC3339)
	if err := s.saveRecord(ctx, record); err != nil {
		s.log("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}

	return record, nil
}

// StartBackup 异步创建备份，立即返回 running 状态的记录
func (s *BackupService) StartBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	ctx, done, beginErr := s.begin(ctx)
	if beginErr != nil {
		return nil, beginErr
	}
	defer done()

	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	releaseMaintenance, acquired, err := s.maintenance(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire database maintenance lock: %w", err)
	}
	if !acquired {
		return nil, ErrDatabaseMaintenanceBusy
	}

	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		releaseMaintenance()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
			releaseMaintenance()
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}
	}()

	// 在返回前加载存储配置和创建 store，避免 goroutine 中配置被修改
	storageType, objectStore, s3Cfg, err := s.getCurrentBackupStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	now := s.now()
	backupID := uuid.New().String()[:8]
	fileName := fmt.Sprintf("%s_%s.sql.gz", s.databaseName, now.Format("20060102_150405"))
	storageKey := s.buildStorageKey(storageType, s3Cfg, fileName)

	var expiresAt string
	if expireDays > 0 {
		expiresAt = now.AddDate(0, 0, expireDays).Format(time.RFC3339)
	}

	dumpOptions, err := s.buildDumpOptions(ctx)
	if err != nil {
		return nil, err
	}

	record := &BackupRecord{
		ID:          backupID,
		Status:      "running",
		BackupType:  "postgres",
		FileName:    fileName,
		StorageType: storageType,
		StorageKey:  storageKey,
		S3Key:       legacyS3Key(storageType, storageKey),
		TriggeredBy: triggeredBy,
		StartedAt:   now.Format(time.RFC3339),
		ExpiresAt:   expiresAt,
		Progress:    "pending",
	}

	if err := s.saveRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("save initial record: %w", err)
	}

	launched = true
	// 在启动 goroutine 前完成拷贝，避免数据竞争
	result := *record

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer releaseMaintenance()
		defer func() {
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				s.log("service.backup", "[Backup] panic recovered: %v", r)
				record.Status = "failed"
				record.ErrorMsg = fmt.Sprintf("internal panic: %v", r)
				record.Progress = ""
				record.FinishedAt = s.now().Format(time.RFC3339)
				_ = s.saveFinal(record)
			}
		}()
		s.executeBackup(record, objectStore, s3Cfg, dumpOptions)
	}()

	return &result, nil
}

// executeBackup 后台执行备份（独立于 HTTP context）
func (s *BackupService) executeBackup(record *BackupRecord, objectStore BackupObjectStore, s3Cfg *BackupS3Config, dumpOptions BackupDumpOptions) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	record.Progress = "dumping"
	_ = s.saveRecord(ctx, record)
	sizeBytes, err := s.archive.Write(ctx, record, objectStore, s3Cfg, dumpOptions, s.saveRecord, s.cleanupContext)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = err.Error()
		record.Progress = ""
		record.FinishedAt = s.now().Format(time.RFC3339)
		_ = s.saveFinal(record)
		return
	}

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.Progress = ""
	record.FinishedAt = s.now().Format(time.RFC3339)
	if err := s.saveFinal(record); err != nil {
		s.log("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}
}

// writeBackupPayload 统一执行 pg_dump、gzip 与对象写入，并在任何失败后清理已登记对象。

func (s *BackupService) restoreBackup(ctx context.Context, backupID string) error {
	releaseMaintenance, acquired, err := s.maintenance(ctx)
	if err != nil {
		return fmt.Errorf("acquire database maintenance lock: %w", err)
	}
	if !acquired {
		return ErrDatabaseMaintenanceBusy
	}
	defer releaseMaintenance()

	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		return ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()
	defer func() {
		s.opMu.Lock()
		s.restoring = false
		s.opMu.Unlock()
	}()

	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return err
	}
	if record.Status != "completed" {
		return infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "can only restore from a completed backup")
	}

	objectStore, err := s.getStoreForRecord(ctx, record)
	if err != nil {
		return fmt.Errorf("init object store: %w", err)
	}
	return s.archive.Restore(ctx, record, objectStore)
}
func (s *BackupService) StartRestore(ctx context.Context, backupID string) (*BackupRecord, error) {
	ctx, done, beginErr := s.begin(ctx)
	if beginErr != nil {
		return nil, beginErr
	}
	defer done()

	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	releaseMaintenance, acquired, err := s.maintenance(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire database maintenance lock: %w", err)
	}
	if !acquired {
		return nil, ErrDatabaseMaintenanceBusy
	}

	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		releaseMaintenance()
		return nil, ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
			releaseMaintenance()
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}
	}()

	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return nil, err
	}
	if record.Status != "completed" {
		return nil, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "can only restore from a completed backup")
	}

	objectStore, err := s.getStoreForRecord(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	record.RestoreStatus = "running"
	if err := s.saveRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("save restore record: %w", err)
	}

	launched = true
	result := *record

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer releaseMaintenance()
		defer func() {
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				s.log("service.backup", "[Backup] restore panic recovered: %v", r)
				record.RestoreStatus = "failed"
				record.RestoreError = fmt.Sprintf("internal panic: %v", r)
				_ = s.saveFinal(record)
			}
		}()
		s.executeRestore(record, objectStore)
	}()

	return &result, nil
}

// executeRestore 后台执行恢复
func (s *BackupService) executeRestore(record *BackupRecord, objectStore BackupObjectStore) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()
	if err := s.archive.Restore(ctx, record, objectStore); err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = err.Error()
		_ = s.saveFinal(record)
		return
	}
	s.completeRestore(record)
}

func (s *BackupService) completeRestore(record *BackupRecord) {
	record.RestoreStatus = "completed"
	record.RestoreError = ""
	record.RestoredAt = s.now().Format(time.RFC3339)
	if err := s.saveFinal(record); err != nil {
		s.log("service.backup", "[Backup] 保存恢复记录失败: %v", err)
	}
}

// ─── 备份记录管理 ───

func (s *BackupService) ListBackups(ctx context.Context) ([]BackupRecord, error) {
	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	// 倒序返回（最新在前）
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})
	return records, nil
}

func (s *BackupService) GetBackupRecord(ctx context.Context, backupID string) (*BackupRecord, error) {
	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	for i := range records {
		if records[i].ID == backupID {
			return &records[i], nil
		}
	}
	return nil, ErrBackupNotFound
}

func (s *BackupService) DeleteBackup(ctx context.Context, backupID string) error {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, err := s.loadRecordsLocked(ctx)
	if err != nil {
		return err
	}

	var found *BackupRecord
	var remaining []BackupRecord
	for i := range records {
		if records[i].ID == backupID {
			found = &records[i]
		} else {
			remaining = append(remaining, records[i])
		}
	}
	if found == nil {
		return ErrBackupNotFound
	}
	if found.Status == "running" {
		// 后台上传可能仍在使用已登记分卷；删除会让完成后的记录引用缺失对象。
		return ErrBackupInProgress
	}

	// 删除不完整时保留记录，便于管理员重试并定位残留对象。
	if err := s.deleteBackupObjects(ctx, found); err != nil {
		return err
	}

	return s.saveRecordsLocked(ctx, remaining)
}

// GetBackupDownloadURL 获取单文件或分卷备份的预签名下载地址。
func (s *BackupService) GetBackupDownloadURL(ctx context.Context, backupID string) (BackupDownloadResponse, error) {
	var download BackupDownloadResponse
	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return download, err
	}
	if record.Status != "completed" {
		return download, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "backup is not completed")
	}

	objectStore, err := s.getStoreForRecord(ctx, record)
	if err != nil {
		return download, err
	}
	if len(record.Parts) > 0 {
		parts, err := OrderedBackupParts(record.Parts)
		if err != nil {
			return download, err
		}
		for _, part := range parts {
			url, err := objectStore.PresignURL(ctx, BackupPartStorageKey(part), time.Hour)
			if err != nil {
				return download, fmt.Errorf("presign backup part %d: %w", part.Index, err)
			}
			download.Parts = append(download.Parts, BackupDownloadPart{
				Index:     part.Index,
				SizeBytes: part.SizeBytes,
				URL:       url,
			})
		}
		return download, nil
	}

	url, err := objectStore.PresignURL(ctx, RecordEffectiveStorageKey(record), 1*time.Hour)
	if err != nil {
		return download, fmt.Errorf("presign url: %w", err)
	}
	download.URL = url
	return download, nil
}

// OpenBackupDownload 打开备份文件读取流，供本地模式走同源鉴权下载。
func (s *BackupService) OpenBackupDownload(ctx context.Context, backupID string) (io.ReadCloser, *BackupRecord, error) {
	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return nil, nil, err
	}
	if record.Status != "completed" {
		return nil, nil, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "backup is not completed")
	}
	if len(record.Parts) > 0 {
		return nil, nil, infraerrors.BadRequest("BACKUP_PART_DOWNLOAD_REQUIRED", "split backup parts must be downloaded individually")
	}

	objectStore, err := s.getStoreForRecord(ctx, record)
	if err != nil {
		return nil, nil, err
	}
	body, err := objectStore.Download(ctx, RecordEffectiveStorageKey(record))
	if err != nil {
		return nil, nil, fmt.Errorf("backup download failed: %w", err)
	}
	return body, record, nil
}

// ─── 内部方法 ───

func (s *BackupService) loadStorageConfig(ctx context.Context) (*BackupStorageConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupStorageConfig)
	if err != nil || raw == "" {
		oldS3, err := s.loadS3Config(ctx)
		if err != nil {
			return nil, err
		}
		if oldS3 != nil && oldS3.IsConfigured() {
			return &BackupStorageConfig{
				Type:      BackupStorageTypeS3,
				LocalPath: s.localPath,
				S3:        *oldS3,
			}, nil
		}
		return &BackupStorageConfig{
			Type:      BackupStorageTypeLocal,
			LocalPath: s.localPath,
		}, nil
	}
	var cfg BackupStorageConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrBackupStorageConfigCorrupt
	}
	cfg.Type = normalizeBackupStorageType(cfg.Type)
	if cfg.Type == "" {
		cfg.Type = BackupStorageTypeLocal
	}
	cfg.LocalPath = s.localPath
	if !backupS3ConfigHasValue(cfg.S3) {
		oldS3, err := s.loadS3Config(ctx)
		if err != nil {
			return nil, err
		}
		if oldS3 != nil {
			cfg.S3 = *oldS3
		}
	}
	cfg.S3.UploadMode = NormalizeBackupS3UploadMode(cfg.S3.UploadMode)
	if cfg.S3.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.S3.SecretAccessKey)
		if err != nil {
			s.log("service.backup", "[Backup] 存储配置 S3 SecretAccessKey 解密失败（可能是旧的未加密数据）: %v", err)
		} else {
			cfg.S3.SecretAccessKey = decrypted
		}
	}
	return &cfg, nil
}

func (s *BackupService) loadContentConfig(ctx context.Context) (*BackupContentConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupContentConfig)
	if err != nil || raw == "" {
		cfg := defaultBackupContentConfig()
		return &cfg, nil
	}
	var cfg BackupContentConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrBackupContentConfigCorrupt
	}
	normalized := normalizeBackupContentConfig(cfg)
	return &normalized, nil
}

func (s *BackupService) buildDumpOptions(ctx context.Context) (BackupDumpOptions, error) {
	cfg, err := s.loadContentConfig(ctx)
	if err != nil {
		return BackupDumpOptions{}, err
	}
	return BackupDumpOptions{
		ExcludeTableData: s.buildExcludedTableData(cfg),
	}, nil
}

func (s *BackupService) buildExcludedTableData(cfg *BackupContentConfig) []string {
	if cfg == nil {
		defaultCfg := defaultBackupContentConfig()
		cfg = &defaultCfg
	}

	var excluded []string
	if !cfg.IncludeUsageRecords {
		excluded = append(excluded, backupContentTableDataGroups["usage_records"]...)
	}
	if !cfg.IncludeOpsLogs {
		excluded = append(excluded, backupContentTableDataGroups["ops_logs"]...)
	}
	if !cfg.IncludeAuditLogs {
		excluded = append(excluded, backupContentTableDataGroups["audit_logs"]...)
	}
	if !cfg.IncludeRuntimeData {
		excluded = append(excluded, backupContentTableDataGroups["runtime_data"]...)
	}
	return uniqueSortedStrings(excluded)
}

func (s *BackupService) prepareS3ConfigForSave(ctx context.Context, cfg BackupS3Config) (*BackupS3Config, error) {
	uploadMode := NormalizeBackupS3UploadMode(cfg.UploadMode)
	if uploadMode == "" {
		return nil, infraerrors.BadRequest("INVALID_BACKUP_UPLOAD_MODE", "backup upload mode must be multipart or spooled_put")
	}
	cfg.UploadMode = uploadMode

	// 如果没提供 secret，优先保留统一配置中的旧值，其次保留旧 S3 配置。
	if cfg.SecretAccessKey == "" {
		cfg.SecretAccessKey = s.loadStoredEncryptedS3Secret(ctx)
	} else {
		// 自动生成的临时密钥会在重启后变化，使用它落库会让新密文永久无法解密。
		if !s.encryptionKeyConfigured {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		encrypted, err := s.encryptor.Encrypt(cfg.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
	}
	return &cfg, nil
}

func (s *BackupService) loadStoredEncryptedS3Config(ctx context.Context) BackupS3Config {
	// 直接读取原始配置，避免把已解密的 SecretAccessKey 再写回数据库。
	if raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupStorageConfig); err == nil && raw != "" {
		var storageCfg BackupStorageConfig
		if json.Unmarshal([]byte(raw), &storageCfg) == nil && backupS3ConfigHasValue(storageCfg.S3) {
			return storageCfg.S3
		}
	}
	if raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupS3Config); err == nil && raw != "" {
		var s3Cfg BackupS3Config
		if json.Unmarshal([]byte(raw), &s3Cfg) == nil {
			return s3Cfg
		}
	}
	return BackupS3Config{}
}

func (s *BackupService) loadStoredEncryptedS3Secret(ctx context.Context) string {
	if raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupStorageConfig); err == nil && raw != "" {
		var storageCfg BackupStorageConfig
		if json.Unmarshal([]byte(raw), &storageCfg) == nil && storageCfg.S3.SecretAccessKey != "" {
			return storageCfg.S3.SecretAccessKey
		}
	}
	if raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupS3Config); err == nil && raw != "" {
		var s3Cfg BackupS3Config
		if json.Unmarshal([]byte(raw), &s3Cfg) == nil {
			return s3Cfg.SecretAccessKey
		}
	}
	return ""
}

func (s *BackupService) loadS3Config(ctx context.Context) (*BackupS3Config, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupS3Config)
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no config is a valid state
	}
	var cfg BackupS3Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrBackupS3ConfigCorrupt
	}
	// 解密 SecretAccessKey
	if cfg.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.SecretAccessKey)
		if err != nil {
			// 兼容未加密的旧数据：如果解密失败，保持原值
			s.log("service.backup", "[Backup] S3 SecretAccessKey 解密失败（可能是旧的未加密数据）: %v", err)
		} else {
			cfg.SecretAccessKey = decrypted
		}
	}
	cfg.UploadMode = NormalizeBackupS3UploadMode(cfg.UploadMode)
	return &cfg, nil
}

func (s *BackupService) getCurrentBackupStore(ctx context.Context) (string, BackupObjectStore, *BackupS3Config, error) {
	cfg, err := s.loadStorageConfig(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	storageType := BackupStorageTypeLocal
	if cfg != nil && cfg.Type != "" {
		storageType = cfg.Type
	}
	switch storageType {
	case BackupStorageTypeLocal:
		return BackupStorageTypeLocal, s.localStore, nil, nil
	case BackupStorageTypeS3:
		if cfg == nil || !cfg.S3.IsConfigured() {
			return "", nil, nil, ErrBackupS3NotConfigured
		}
		store, err := s.getOrCreateStore(ctx, &cfg.S3)
		return BackupStorageTypeS3, store, &cfg.S3, err
	default:
		return "", nil, nil, ErrBackupStorageNotConfigured
	}
}

func (s *BackupService) getStoreForRecord(ctx context.Context, record *BackupRecord) (BackupObjectStore, error) {
	switch RecordEffectiveStorageType(record) {
	case BackupStorageTypeLocal:
		return s.localStore, nil
	case BackupStorageTypeS3:
		storageCfg, err := s.loadStorageConfig(ctx)
		if err != nil {
			return nil, err
		}
		var s3Cfg *BackupS3Config
		if storageCfg != nil && storageCfg.S3.IsConfigured() {
			s3Cfg = &storageCfg.S3
		} else {
			s3Cfg, err = s.loadS3Config(ctx)
			if err != nil {
				return nil, err
			}
		}
		if s3Cfg == nil || !s3Cfg.IsConfigured() {
			return nil, ErrBackupS3NotConfigured
		}
		return s.getOrCreateStore(ctx, s3Cfg)
	default:
		return nil, ErrBackupStorageNotConfigured
	}
}

func (s *BackupService) getOrCreateStore(ctx context.Context, cfg *BackupS3Config) (BackupObjectStore, error) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()

	if s.store != nil && s.s3Cfg != nil {
		return s.store, nil
	}

	if cfg == nil {
		return nil, ErrBackupS3NotConfigured
	}

	store, err := s.storeFactory(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s.store = store
	s.s3Cfg = cfg
	return store, nil
}

func (s *BackupService) resetCachedS3Store() {
	s.storeMu.Lock()
	s.store = nil
	s.s3Cfg = nil
	s.storeMu.Unlock()
}

func (s *BackupService) buildS3Key(cfg *BackupS3Config, fileName string) string {
	prefix := strings.TrimRight(cfg.Prefix, "/")
	if prefix == "" {
		prefix = "backups"
	}
	return fmt.Sprintf("%s/%s/%s", prefix, s.now().Format("2006/01/02"), fileName)
}

func (s *BackupService) buildStorageKey(storageType string, s3Cfg *BackupS3Config, fileName string) string {
	if storageType == BackupStorageTypeLocal {
		return fmt.Sprintf("%s/%s", s.now().Format("2006/01/02"), fileName)
	}
	if s3Cfg == nil {
		return fmt.Sprintf("backups/%s/%s", s.now().Format("2006/01/02"), fileName)
	}
	return s.buildS3Key(s3Cfg, fileName)
}

// loadRecords 加载备份记录，区分"无数据"和"数据损坏"
func (s *BackupService) loadRecords(ctx context.Context) ([]BackupRecord, error) {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()
	return s.loadRecordsLocked(ctx)
}

// loadRecordsLocked 在已持有 recordsMu 锁的情况下加载记录
func (s *BackupService) loadRecordsLocked(ctx context.Context) ([]BackupRecord, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupRecords)
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no records is a valid state
	}
	var records []BackupRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, ErrBackupRecordsCorrupt
	}
	return records, nil
}

// saveRecordsLocked 在已持有 recordsMu 锁的情况下保存记录
func (s *BackupService) saveRecordsLocked(ctx context.Context, records []BackupRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, settingKeyBackupRecords, string(data))
}

// saveRecord 保存单条记录（带互斥锁保护）
func (s *BackupService) saveRecord(ctx context.Context, record *BackupRecord) error {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, _ := s.loadRecordsLocked(ctx)

	// 更新已有记录或追加
	found := false
	for i := range records {
		if records[i].ID == record.ID {
			records[i] = *record
			found = true
			break
		}
	}
	if !found {
		records = append(records, *record)
	}

	// 限制记录数量
	if len(records) > maxBackupRecords {
		records = records[len(records)-maxBackupRecords:]
	}

	return s.saveRecordsLocked(ctx, records)
}

func (s *BackupService) cleanupOldBackups(ctx context.Context, schedule *BackupScheduleConfig) error {
	if schedule == nil {
		return nil
	}

	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, err := s.loadRecordsLocked(ctx)
	if err != nil {
		return err
	}

	// 按时间倒序
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})

	var toKeep []BackupRecord
	deletedCount := 0
	var cleanupErrs []error

	for i, r := range records {
		shouldDelete := false

		// 按保留份数清理
		if schedule.RetainCount > 0 && i >= schedule.RetainCount {
			shouldDelete = true
		}

		// 按保留天数清理
		if schedule.RetainDays > 0 && r.StartedAt != "" {
			startedAt, err := time.Parse(time.RFC3339, r.StartedAt)
			if err == nil && time.Since(startedAt) > time.Duration(schedule.RetainDays)*24*time.Hour {
				shouldDelete = true
			}
		}

		if !shouldDelete || r.Status != "completed" {
			toKeep = append(toKeep, r)
			continue
		}
		if err := s.deleteBackupObjects(ctx, &r); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete backup %s: %w", r.ID, err))
			toKeep = append(toKeep, r)
			continue
		}
		deletedCount++
	}

	if deletedCount > 0 {
		if err := s.saveRecordsLocked(ctx, toKeep); err != nil {
			return errors.Join(append(cleanupErrs, err)...)
		}
		s.log("service.backup", "[Backup] 自动清理了 %d 个过期备份", deletedCount)
	}
	return errors.Join(cleanupErrs...)
}

func normalizeBackupStorageType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", BackupStorageTypeLocal:
		return BackupStorageTypeLocal
	case BackupStorageTypeS3:
		return BackupStorageTypeS3
	default:
		return ""
	}
}

// NormalizeBackupS3UploadMode 归一化备份上传模式；旧配置默认走磁盘暂存的兼容路径。
func NormalizeBackupS3UploadMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", BackupS3UploadModeSpooledPut:
		return BackupS3UploadModeSpooledPut
	case BackupS3UploadModeMultipart:
		return BackupS3UploadModeMultipart
	default:
		return ""
	}
}

func defaultBackupContentConfig() BackupContentConfig {
	return BackupContentConfig{}
}

func normalizeBackupContentConfig(cfg BackupContentConfig) BackupContentConfig {
	return BackupContentConfig{
		IncludeUsageRecords: cfg.IncludeUsageRecords,
		IncludeOpsLogs:      cfg.IncludeOpsLogs,
		IncludeAuditLogs:    cfg.IncludeAuditLogs,
		IncludeRuntimeData:  cfg.IncludeRuntimeData,
	}
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func backupS3ConfigHasValue(cfg BackupS3Config) bool {
	return cfg.Endpoint != "" ||
		cfg.Region != "" ||
		cfg.Bucket != "" ||
		cfg.AccessKeyID != "" ||
		cfg.SecretAccessKey != "" ||
		cfg.Prefix != "" ||
		cfg.ForcePathStyle ||
		cfg.UploadMode != "" ||
		cfg.UploadConcurrency != 0 ||
		cfg.UploadPartSizeMB != 0
}

func RecordEffectiveStorageType(record *BackupRecord) string {
	if record == nil {
		return BackupStorageTypeLocal
	}
	if record.StorageType != "" {
		if normalized := normalizeBackupStorageType(record.StorageType); normalized != "" {
			return normalized
		}
	}
	if record.S3Key != "" {
		return BackupStorageTypeS3
	}
	// 上游分卷记录可能没有 fork 的 storage_type，只保留 parts[].s3_key。
	for _, part := range record.Parts {
		if BackupPartStorageKey(part) != "" {
			return BackupStorageTypeS3
		}
	}
	return BackupStorageTypeLocal
}

func RecordEffectiveStorageKey(record *BackupRecord) string {
	if record == nil {
		return ""
	}
	if record.StorageKey != "" {
		return record.StorageKey
	}
	return record.S3Key
}

func legacyS3Key(storageType, storageKey string) string {
	if storageType == BackupStorageTypeS3 {
		return storageKey
	}
	return ""
}

// BackupObjectKeys 返回记录关联的全部单文件或分卷对象键，并兼容旧字段。
func BackupObjectKeys(record *BackupRecord) []string {
	if record == nil {
		return nil
	}
	keys := make([]string, 0, len(record.Parts)+2)
	seen := make(map[string]struct{}, len(record.Parts)+2)
	appendKey := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	appendKey(record.StorageKey)
	appendKey(record.S3Key)
	parts := append([]BackupPart(nil), record.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
	for _, part := range parts {
		appendKey(BackupPartStorageKey(part))
	}
	return keys
}

// deleteBackupObjects 使用记录绑定的存储后端删除全部对象；任何失败都会返回并保留调用方元数据。
func (s *BackupService) deleteBackupObjects(ctx context.Context, record *BackupRecord) error {
	if len(BackupObjectKeys(record)) == 0 {
		return nil
	}
	objectStore, err := s.getStoreForRecord(ctx, record)
	if err != nil {
		return err
	}
	return DeleteBackupObjectKeys(ctx, objectStore, record)
}

func DeleteBackupObjectKeys(ctx context.Context, objectStore BackupObjectStore, record *BackupRecord) error {
	if objectStore == nil {
		return errors.New("backup object store is unavailable")
	}
	var errs []error
	for _, key := range BackupObjectKeys(record) {
		if err := objectStore.Delete(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("delete backup object %q: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

// LocalBackupStore 将备份文件保存到应用数据目录下，key 只能是相对路径。
