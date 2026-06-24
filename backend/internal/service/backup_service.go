package service

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/TokenFlux/TokenRouter/internal/config"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

const (
	settingKeyBackupS3Config      = "backup_s3_config"
	settingKeyBackupStorageConfig = "backup_storage_config"
	settingKeyBackupContentConfig = "backup_content_config"
	settingKeyBackupSchedule      = "backup_schedule"
	settingKeyBackupRecords       = "backup_records"

	BackupStorageTypeLocal = "local"
	BackupStorageTypeS3    = "s3"

	maxBackupRecords = 100
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
	ID            string `json:"id"`
	Status        string `json:"status"`      // pending, running, completed, failed
	BackupType    string `json:"backup_type"` // postgres
	FileName      string `json:"file_name"`
	StorageType   string `json:"storage_type,omitempty"`
	StorageKey    string `json:"storage_key,omitempty"`
	S3Key         string `json:"s3_key"` // 兼容旧版本远程备份记录
	SizeBytes     int64  `json:"size_bytes"`
	TriggeredBy   string `json:"triggered_by"` // manual, scheduled
	ErrorMsg      string `json:"error_message,omitempty"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`     // 过期时间
	Progress      string `json:"progress,omitempty"`       // "dumping", "uploading", ""
	RestoreStatus string `json:"restore_status,omitempty"` // "", "running", "completed", "failed"
	RestoreError  string `json:"restore_error,omitempty"`
	RestoredAt    string `json:"restored_at,omitempty"`
}

// BackupService 数据库备份恢复服务
type BackupService struct {
	settingRepo  SettingRepository
	dbCfg        *config.DatabaseConfig
	encryptor    SecretEncryptor
	storeFactory BackupObjectStoreFactory
	dumper       DBDumper
	localStore   BackupObjectStore

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

func NewBackupService(
	settingRepo SettingRepository,
	cfg *config.Config,
	encryptor SecretEncryptor,
	storeFactory BackupObjectStoreFactory,
	dumper DBDumper,
) *BackupService {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	return &BackupService{
		settingRepo:  settingRepo,
		dbCfg:        &cfg.Database,
		encryptor:    encryptor,
		storeFactory: storeFactory,
		dumper:       dumper,
		localStore:   NewLocalBackupStore(defaultBackupLocalPath()),
		bgCtx:        bgCtx,
		bgCancel:     bgCancel,
	}
}

// Start 启动定时备份调度器并清理孤立记录
func (s *BackupService) Start() {
	s.cronSched = cron.New()
	s.cronSched.Start()

	// 清理重启后孤立的 running 记录
	s.recoverStaleRecords()

	// 加载已有的定时配置
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	schedule, err := s.GetSchedule(ctx)
	if err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 加载定时备份配置失败: %v", err)
		return
	}
	if schedule.Enabled && schedule.CronExpr != "" {
		if err := s.applyCronSchedule(schedule); err != nil {
			logger.LegacyPrintf("service.backup", "[Backup] 应用定时备份配置失败: %v", err)
		}
	}
}

// recoverStaleRecords 启动时将孤立的 running 记录标记为 failed
func (s *BackupService) recoverStaleRecords() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	records, err := s.loadRecords(ctx)
	if err != nil {
		return
	}
	for i := range records {
		if records[i].Status == "running" {
			records[i].Status = "failed"
			records[i].ErrorMsg = "interrupted by server restart"
			records[i].Progress = ""
			records[i].FinishedAt = time.Now().Format(time.RFC3339)
			_ = s.saveRecord(ctx, &records[i])
			logger.LegacyPrintf("service.backup", "[Backup] recovered stale running record: %s", records[i].ID)
		}
		if records[i].RestoreStatus == "running" {
			records[i].RestoreStatus = "failed"
			records[i].RestoreError = "interrupted by server restart"
			_ = s.saveRecord(ctx, &records[i])
			logger.LegacyPrintf("service.backup", "[Backup] recovered stale restoring record: %s", records[i].ID)
		}
	}
}

// Stop 停止定时备份并等待活跃操作完成
func (s *BackupService) Stop() {
	s.shuttingDown.Store(true)

	s.cronMu.Lock()
	if s.cronSched != nil {
		s.cronSched.Stop()
	}
	s.cronMu.Unlock()

	// 等待活跃备份/恢复完成（最多 5 分钟）
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.LegacyPrintf("service.backup", "[Backup] all active operations finished")
	case <-time.After(5 * time.Minute):
		logger.LegacyPrintf("service.backup", "[Backup] shutdown timeout after 5min, cancelling active operations")
		if s.bgCancel != nil {
			s.bgCancel() // 取消所有后台操作
		}
		// 给 goroutine 时间响应取消并完成清理
		select {
		case <-done:
			logger.LegacyPrintf("service.backup", "[Backup] active operations cancelled and cleaned up")
		case <-time.After(10 * time.Second):
			logger.LegacyPrintf("service.backup", "[Backup] goroutine cleanup timed out")
		}
	}
}

// ─── 存储配置管理 ───

func (s *BackupService) GetStorageConfig(ctx context.Context) (*BackupStorageConfig, error) {
	cfg, err := s.loadStorageConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &BackupStorageConfig{Type: BackupStorageTypeLocal}
	}
	cfg.LocalPath = defaultBackupLocalPath()
	cfg.S3.SecretAccessKey = ""
	return cfg, nil
}

func (s *BackupService) UpdateStorageConfig(ctx context.Context, cfg BackupStorageConfig) (*BackupStorageConfig, error) {
	normalizedType := normalizeBackupStorageType(cfg.Type)
	if normalizedType == "" {
		return nil, infraerrors.BadRequest("INVALID_BACKUP_STORAGE_TYPE", "backup storage type must be local or s3")
	}
	cfg.Type = normalizedType
	cfg.LocalPath = defaultBackupLocalPath()

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
		return &BackupS3Config{}, nil
	}
	// 脱敏返回
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
	logger.LegacyPrintf("service.backup", "[Backup] 定时备份已启用: %s", cfg.CronExpr)
	return nil
}

func (s *BackupService) removeCronSchedule() {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if s.cronSched != nil && s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
		logger.LegacyPrintf("service.backup", "[Backup] 定时备份已停用")
	}
}

func (s *BackupService) runScheduledBackup() {
	s.wg.Add(1)
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	// 读取定时备份配置中的过期天数
	schedule, _ := s.GetSchedule(ctx)
	expireDays := 14 // 默认14天过期
	if schedule != nil && schedule.RetainDays > 0 {
		expireDays = schedule.RetainDays
	}

	logger.LegacyPrintf("service.backup", "[Backup] 开始执行定时备份, 过期天数: %d", expireDays)
	record, err := s.CreateBackup(ctx, "scheduled", expireDays)
	if err != nil {
		if errors.Is(err, ErrBackupInProgress) {
			logger.LegacyPrintf("service.backup", "[Backup] 定时备份跳过: 已有备份正在进行中")
		} else {
			logger.LegacyPrintf("service.backup", "[Backup] 定时备份失败: %v", err)
		}
		return
	}
	logger.LegacyPrintf("service.backup", "[Backup] 定时备份完成: id=%s size=%d", record.ID, record.SizeBytes)

	// 清理过期备份（复用已加载的 schedule）
	if schedule == nil {
		return
	}
	if err := s.cleanupOldBackups(ctx, schedule); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 清理过期备份失败: %v", err)
	}
}

// ─── 备份/恢复核心 ───

// CreateBackup 创建全量数据库备份并写入当前配置的存储后端（流式处理）
// expireDays: 备份过期天数，0=永不过期，默认14天
func (s *BackupService) CreateBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

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

	now := time.Now()
	backupID := uuid.New().String()[:8]
	fileName := fmt.Sprintf("%s_%s.sql.gz", s.dbCfg.DBName, now.Format("20060102_150405"))
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
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, err
	}

	// 流式执行: pg_dump -> gzip -> S3 upload
	dumpReader, err := s.dumper.Dump(ctx, dumpOptions)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = fmt.Sprintf("pg_dump failed: %v", err)
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, fmt.Errorf("pg_dump: %w", err)
	}

	// 使用 io.Pipe 将 gzip 压缩数据流式传递给 S3 上传
	pr, pw := io.Pipe()
	gzipDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pw.CloseWithError(fmt.Errorf("gzip goroutine panic: %v", r)) //nolint:errcheck
				gzipDone <- fmt.Errorf("gzip goroutine panic: %v", r)
			}
		}()
		gzWriter := gzip.NewWriter(pw)
		var gzErr error
		_, gzErr = io.Copy(gzWriter, dumpReader)
		if closeErr := gzWriter.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if closeErr := dumpReader.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if gzErr != nil {
			_ = pw.CloseWithError(gzErr)
		} else {
			_ = pw.Close()
		}
		gzipDone <- gzErr
	}()

	contentType := "application/gzip"
	sizeBytes, err := objectStore.Upload(ctx, storageKey, pr, contentType)
	if err != nil {
		_ = pr.CloseWithError(err) // 确保 gzip goroutine 不会悬挂
		gzErr := <-gzipDone        // 安全等待 gzip goroutine 完成
		record.Status = "failed"
		errMsg := fmt.Sprintf("backup write failed: %v", err)
		if gzErr != nil {
			errMsg = fmt.Sprintf("gzip/dump failed: %v", gzErr)
		}
		record.ErrorMsg = errMsg
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, fmt.Errorf("backup upload: %w", err)
	}
	<-gzipDone // 确保 gzip goroutine 已退出

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.FinishedAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(ctx, record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}

	return record, nil
}

// StartBackup 异步创建备份，立即返回 running 状态的记录
func (s *BackupService) StartBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
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

	now := time.Now()
	backupID := uuid.New().String()[:8]
	fileName := fmt.Sprintf("%s_%s.sql.gz", s.dbCfg.DBName, now.Format("20060102_150405"))
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
		defer func() {
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				logger.LegacyPrintf("service.backup", "[Backup] panic recovered: %v", r)
				record.Status = "failed"
				record.ErrorMsg = fmt.Sprintf("internal panic: %v", r)
				record.Progress = ""
				record.FinishedAt = time.Now().Format(time.RFC3339)
				_ = s.saveRecord(context.Background(), record)
			}
		}()
		s.executeBackup(record, objectStore, dumpOptions)
	}()

	return &result, nil
}

// executeBackup 后台执行备份（独立于 HTTP context）
func (s *BackupService) executeBackup(record *BackupRecord, objectStore BackupObjectStore, dumpOptions BackupDumpOptions) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	// 阶段1: pg_dump
	record.Progress = "dumping"
	_ = s.saveRecord(ctx, record)

	dumpReader, err := s.dumper.Dump(ctx, dumpOptions)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = fmt.Sprintf("pg_dump failed: %v", err)
		record.Progress = ""
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(context.Background(), record)
		return
	}

	// 阶段2: gzip + upload
	record.Progress = "uploading"
	_ = s.saveRecord(ctx, record)

	pr, pw := io.Pipe()
	gzipDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pw.CloseWithError(fmt.Errorf("gzip goroutine panic: %v", r)) //nolint:errcheck
				gzipDone <- fmt.Errorf("gzip goroutine panic: %v", r)
			}
		}()
		gzWriter := gzip.NewWriter(pw)
		var gzErr error
		_, gzErr = io.Copy(gzWriter, dumpReader)
		if closeErr := gzWriter.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if closeErr := dumpReader.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if gzErr != nil {
			_ = pw.CloseWithError(gzErr)
		} else {
			_ = pw.Close()
		}
		gzipDone <- gzErr
	}()

	contentType := "application/gzip"
	storageKey := recordEffectiveStorageKey(record)
	sizeBytes, err := objectStore.Upload(ctx, storageKey, pr, contentType)
	if err != nil {
		_ = pr.CloseWithError(err) // 确保 gzip goroutine 不会悬挂
		gzErr := <-gzipDone        // 安全等待 gzip goroutine 完成
		record.Status = "failed"
		errMsg := fmt.Sprintf("backup write failed: %v", err)
		if gzErr != nil {
			errMsg = fmt.Sprintf("gzip/dump failed: %v", gzErr)
		}
		record.ErrorMsg = errMsg
		record.Progress = ""
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	<-gzipDone // 确保 gzip goroutine 已退出

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.Progress = ""
	record.FinishedAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(context.Background(), record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}
}

// RestoreBackup 从记录对应存储后端下载备份并流式恢复到数据库
func (s *BackupService) RestoreBackup(ctx context.Context, backupID string) error {
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

	// 从记录对应存储后端流式下载
	body, err := objectStore.Download(ctx, recordEffectiveStorageKey(record))
	if err != nil {
		return fmt.Errorf("backup download failed: %w", err)
	}
	defer func() { _ = body.Close() }()

	// 流式解压 gzip -> psql（不将全部数据加载到内存）
	gzReader, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	// 流式恢复
	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		return fmt.Errorf("pg restore: %w", err)
	}

	return nil
}

// StartRestore 异步恢复备份，立即返回
func (s *BackupService) StartRestore(ctx context.Context, backupID string) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		return nil, ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
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
	_ = s.saveRecord(ctx, record)

	launched = true
	result := *record

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				logger.LegacyPrintf("service.backup", "[Backup] restore panic recovered: %v", r)
				record.RestoreStatus = "failed"
				record.RestoreError = fmt.Sprintf("internal panic: %v", r)
				_ = s.saveRecord(context.Background(), record)
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

	body, err := objectStore.Download(ctx, recordEffectiveStorageKey(record))
	if err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("backup download failed: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	defer func() { _ = body.Close() }()

	gzReader, err := gzip.NewReader(body)
	if err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("gzip reader: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	defer func() { _ = gzReader.Close() }()

	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("pg restore: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}

	record.RestoreStatus = "completed"
	record.RestoredAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(context.Background(), record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存恢复记录失败: %v", err)
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

	// 从记录对应的存储后端删除文件。
	if recordEffectiveStorageKey(found) != "" && found.Status == "completed" {
		if objectStore, err := s.getStoreForRecord(ctx, found); err == nil {
			_ = objectStore.Delete(ctx, recordEffectiveStorageKey(found))
		}
	}

	return s.saveRecordsLocked(ctx, remaining)
}

// GetBackupDownloadURL 获取备份文件预签名下载 URL
func (s *BackupService) GetBackupDownloadURL(ctx context.Context, backupID string) (string, error) {
	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return "", err
	}
	if record.Status != "completed" {
		return "", infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "backup is not completed")
	}

	objectStore, err := s.getStoreForRecord(ctx, record)
	if err != nil {
		return "", err
	}

	url, err := objectStore.PresignURL(ctx, recordEffectiveStorageKey(record), 1*time.Hour)
	if err != nil {
		return "", fmt.Errorf("presign url: %w", err)
	}
	return url, nil
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

	objectStore, err := s.getStoreForRecord(ctx, record)
	if err != nil {
		return nil, nil, err
	}
	body, err := objectStore.Download(ctx, recordEffectiveStorageKey(record))
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
				LocalPath: defaultBackupLocalPath(),
				S3:        *oldS3,
			}, nil
		}
		return &BackupStorageConfig{
			Type:      BackupStorageTypeLocal,
			LocalPath: defaultBackupLocalPath(),
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
	cfg.LocalPath = defaultBackupLocalPath()
	if !backupS3ConfigHasValue(cfg.S3) {
		oldS3, err := s.loadS3Config(ctx)
		if err != nil {
			return nil, err
		}
		if oldS3 != nil {
			cfg.S3 = *oldS3
		}
	}
	if cfg.S3.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.S3.SecretAccessKey)
		if err != nil {
			logger.LegacyPrintf("service.backup", "[Backup] 存储配置 S3 SecretAccessKey 解密失败（可能是旧的未加密数据）: %v", err)
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
	// 如果没提供 secret，优先保留统一配置中的旧值，其次保留旧 S3 配置。
	if cfg.SecretAccessKey == "" {
		cfg.SecretAccessKey = s.loadStoredEncryptedS3Secret(ctx)
	} else {
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
			logger.LegacyPrintf("service.backup", "[Backup] S3 SecretAccessKey 解密失败（可能是旧的未加密数据）: %v", err)
		} else {
			cfg.SecretAccessKey = decrypted
		}
	}
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
	switch recordEffectiveStorageType(record) {
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
	return fmt.Sprintf("%s/%s/%s", prefix, time.Now().Format("2006/01/02"), fileName)
}

func (s *BackupService) buildStorageKey(storageType string, s3Cfg *BackupS3Config, fileName string) string {
	if storageType == BackupStorageTypeLocal {
		return fmt.Sprintf("%s/%s", time.Now().Format("2006/01/02"), fileName)
	}
	if s3Cfg == nil {
		return fmt.Sprintf("backups/%s/%s", time.Now().Format("2006/01/02"), fileName)
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

	var toDelete []BackupRecord
	var toKeep []BackupRecord

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

		if shouldDelete && r.Status == "completed" {
			toDelete = append(toDelete, r)
		} else {
			toKeep = append(toKeep, r)
		}
	}

	// 删除存储后端上的文件
	for _, r := range toDelete {
		if key := recordEffectiveStorageKey(&r); key != "" {
			if store, err := s.getStoreForRecord(ctx, &r); err == nil {
				_ = store.Delete(ctx, key)
			}
		}
	}

	if len(toDelete) > 0 {
		logger.LegacyPrintf("service.backup", "[Backup] 自动清理了 %d 个过期备份", len(toDelete))
		return s.saveRecordsLocked(ctx, toKeep)
	}
	return nil
}

func defaultBackupLocalPath() string {
	return filepath.Join(resolveBackupDataDir(), "backups")
}

func resolveBackupDataDir() string {
	if dir := os.Getenv("DATA_DIR"); strings.TrimSpace(dir) != "" {
		return dir
	}

	dockerDataDir := "/app/data"
	if info, err := os.Stat(dockerDataDir); err == nil && info.IsDir() {
		testFile := filepath.Join(dockerDataDir, ".write_test")
		if f, err := os.Create(testFile); err == nil {
			_ = f.Close()
			_ = os.Remove(testFile)
			return dockerDataDir
		}
	}
	return "."
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
		cfg.ForcePathStyle
}

func recordEffectiveStorageType(record *BackupRecord) string {
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
	return BackupStorageTypeLocal
}

func recordEffectiveStorageKey(record *BackupRecord) string {
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

// LocalBackupStore 将备份文件保存到应用数据目录下，key 只能是相对路径。
type LocalBackupStore struct {
	basePath string
}

func NewLocalBackupStore(basePath string) *LocalBackupStore {
	return &LocalBackupStore{basePath: basePath}
}

func (s *LocalBackupStore) Upload(ctx context.Context, key string, body io.Reader, _ string) (int64, error) {
	path, err := s.safePath(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, fmt.Errorf("create backup directory: %w", err)
	}
	out, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("create backup file: %w", err)
	}
	defer func() { _ = out.Close() }()

	written, err := copyWithContext(ctx, out, body)
	if err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	return written, nil
}

func (s *LocalBackupStore) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	return s.Upload(ctx, key, body, contentType)
}

func (s *LocalBackupStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := s.safePath(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open backup file: %w", err)
	}
	return file, nil
}

func (s *LocalBackupStore) Delete(_ context.Context, key string) error {
	path, err := s.safePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete backup file: %w", err)
	}
	return nil
}

func (s *LocalBackupStore) PresignURL(_ context.Context, key string, _ time.Duration) (string, error) {
	_, err := s.safePath(key)
	if err != nil {
		return "", err
	}
	return "", infraerrors.BadRequest("BACKUP_LOCAL_DOWNLOAD_REQUIRES_API", "local backup must be downloaded through the application")
}

func (s *LocalBackupStore) HeadBucket(_ context.Context) error {
	if err := os.MkdirAll(s.basePath, 0755); err != nil {
		return fmt.Errorf("create local backup directory: %w", err)
	}
	testFile, err := os.CreateTemp(s.basePath, ".write-test-*")
	if err != nil {
		return fmt.Errorf("local backup directory is not writable: %w", err)
	}
	name := testFile.Name()
	_ = testFile.Close()
	_ = os.Remove(name)
	return nil
}

func (s *LocalBackupStore) safePath(key string) (string, error) {
	cleanKey := filepath.Clean(strings.TrimSpace(key))
	if cleanKey == "." || strings.HasPrefix(cleanKey, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanKey) {
		return "", infraerrors.BadRequest("INVALID_BACKUP_PATH", "invalid backup path")
	}
	baseAbs, err := filepath.Abs(s.basePath)
	if err != nil {
		return "", err
	}
	fullPath := filepath.Join(baseAbs, cleanKey)
	rel, err := filepath.Rel(baseAbs, fullPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", infraerrors.BadRequest("INVALID_BACKUP_PATH", "invalid backup path")
	}
	return fullPath, nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				break
			}
			return written, er
		}
	}
	return written, nil
}
