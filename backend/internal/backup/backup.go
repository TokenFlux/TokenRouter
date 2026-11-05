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

// backup.go manages backup configuration, archive records, and synchronous or
// asynchronous backup and restore operations.
// An archive's destination is chosen before writing begins, while later reads
// use its recorded storage type and object keys. Changing the active destination
// leaves older records' kind and keys intact. Remote connection details come
// from settings and the cached store; records preserve no historical bucket or
// credentials. A completed archive and a successfully saved
// completion record are separate outcomes: final metadata failures can leave an
// already written archive or restored database with stale recorded status.
//
// Asynchronous operations reserve maintenance and local operation state before
// persisting their initial record and registering a worker. Their workers use the
// service context after the request returns. Synchronous backups tolerate some
// record-write failures, whereas asynchronous backup and restore require their
// initial save. Cleanup removes recorded objects before dropping metadata and
// keeps failed deletions available for retry. Configuration readers reconcile
// unified and legacy S3 settings, and secret writes retain stored ciphertext or
// encrypt newly supplied credentials using a deployment-stable key.

const (
	// Setting keys locate the unified destination, legacy S3 credentials, content
	// selection, cron policy, and complete JSON record list in shared settings.
	settingKeyBackupS3Config      = "backup_s3_config"
	settingKeyBackupStorageConfig = "backup_storage_config"
	settingKeyBackupContentConfig = "backup_content_config"
	settingKeyBackupSchedule      = "backup_schedule"
	settingKeyBackupRecords       = "backup_records"

	// Storage labels select an injected local store or the configured remote store
	// and remain in records so later operations can choose that destination kind.
	BackupStorageTypeLocal = "local" // local archive directory
	BackupStorageTypeS3    = "s3"    // remote S3-compatible bucket

	// Upload modes select streaming multipart or sized PutObject preparation in the
	// archive provider; spooled mode can record independently uploaded archive segments.
	BackupS3UploadModeMultipart  = "multipart"   // single streaming multipart object
	BackupS3UploadModeSpooledPut = "spooled_put" // measured archive segment uploads

	// Record retention caps the persisted metadata list, while object compensation
	// gets a separate time budget that can also observe application shutdown.
	maxBackupRecords           = 100             // metadata entries retained on save
	backupObjectCleanupTimeout = 2 * time.Minute // stale object cleanup budget
)

var (
	// Backup errors distinguish unavailable destinations, missing or active records,
	// invalid stored JSON, maintenance contention, and unsafe persistence of new secrets.
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
	// ErrSecretEncryptionKeyNotConfigured rejects newly supplied S3 secrets when
	// deployment key stability has not been configured.
	ErrSecretEncryptionKeyNotConfigured = infraerrors.BadRequest(
		"SECRET_ENCRYPTION_KEY_NOT_CONFIGURED",
		"cannot store the S3 secret access key: no fixed secret encryption key is configured, so the auto-generated key would change on every restart and make the stored secret undecryptable after a restart or upgrade. Set a fixed security.secret_encryption_key (e.g. generate one with `openssl rand -hex 32`) and try again",
	)

	// backupContentTableDataGroups maps optional categories to pg_dump table-data
	// patterns; disabling a category omits these rows while retaining table schemas.
	// Plugin installations, indexes, KV, and ledger are business data and stay
	// outside these optional exclusions so recovery retains intent and replay evidence.
	backupContentTableDataGroups = map[string][]string{
		"usage_records": {
			"public.usage_logs",
			"public.usage_user_activity",
			"public.usage_logs_*",
			"public.billing_usage_entries",
			"public.billing_system_usage_entries",
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
			"public.request_records",
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
			"public.provider_balance_alert_states",
		},
	}
)

// DBDumper supplies SQL export streams and consumes SQL for database restoration.
type DBDumper interface {
	// Dump returns an SQL stream with the selected table data excluded.
	// Callers close the reader after consumption; the PostgreSQL implementation waits
	// for pg_dump during Close, whose error can report a failed export.
	Dump(ctx context.Context, opts BackupDumpOptions) (io.ReadCloser, error)
	// Restore applies the supplied SQL under the operation context and returns its
	// outcome. The caller owns data's lifetime and reconciles database state after
	// uncertain input or subprocess failure; status persistence is a separate service step.
	Restore(ctx context.Context, data io.Reader) error
}

// BackupDumpOptions identifies table-data patterns omitted from database exports.
type BackupDumpOptions struct {
	ExcludeTableData []string // pg_dump table-data exclusion patterns
}

// BackupObjectStore supplies archive transfers, deletion, and destination probes.
type BackupObjectStore interface {
	// Upload consumes an archive stream and reports bytes with its transfer result.
	// Callers retain responsibility for closing body; concrete stores choose streaming
	// multipart or measured-file preparation according to their configuration.
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error)
	// UploadFile consumes an archive file reader and reports the transfer's byte
	// count and result. Callers keep body available until completion and close it;
	// remote file transfer uses its configured part limits and concurrency.
	UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error)
	// Download opens the named archive object and returns its readable body.
	// Callers close that body after consumption; storage errors propagate before
	// restoration or application download can begin reading it.
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete attempts removal of the named object under the storage implementation's
	// context handling. Callers retain metadata when deletion fails so residual
	// objects remain identifiable for a later cleanup attempt.
	Delete(ctx context.Context, key string) error
	// PresignURL returns a temporary download URL with the requested lifetime.
	// Local storage reports that application download is required, so callers choose
	// the authenticated stream endpoint for records stored locally.
	PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	// HeadBucket probes access to this store's configured destination and returns
	// its result. The remote implementation checks bucket access; the local implementation
	// creates the root and tests file creation before attempting probe cleanup.
	HeadBucket(ctx context.Context) error
}

// BackupObjectStoreProgressUploader adds cumulative observations to remote file uploads.
type BackupObjectStoreProgressUploader interface {
	// UploadFileWithProgress uploads the reader and optionally invokes onProgress
	// with cumulative byte observations. Remote callbacks can overlap and arrive out
	// of order, so callers synchronize captured state and keep it alive until return.
	// Callbacks return before the enclosing upload can finish, so they avoid
	// waiting for completion of that same upload from inside the callback.
	UploadFileWithProgress(ctx context.Context, key string, body io.Reader, contentType string, onProgress func(uploadedBytes int64)) (sizeBytes int64, err error)
}

// BackupObjectStoreSizedUploader accepts archive segments with previously measured lengths.
type BackupObjectStoreSizedUploader interface {
	// UploadSized uploads a caller-measured archive segment and reports its
	// result. Callers supply a reader matching sizeBytes and own its lifetime;
	// the S3 implementation uses that length directly for PutObject preparation.
	UploadSized(ctx context.Context, key string, body io.Reader, contentType string, sizeBytes int64) (int64, error)
}

// BackupObjectStoreFactory constructs a remote store from the supplied S3 settings.
// Factory callers keep retained configuration stable while using the resulting store;
// the S3 provider retains cfg.Endpoint through the client's endpoint pointer.
type BackupObjectStoreFactory func(ctx context.Context, cfg *BackupS3Config) (BackupObjectStore, error)

// BackupStorageConfig selects a destination kind and retains remote settings.
type BackupStorageConfig struct {
	// Type selects subsequent backups. Updates normalize an empty value to local,
	// while configuration loading also replaces unrecognized stored values with local.
	Type string `json:"type"`
	// LocalPath reports the service path that app binds to its injected local
	// store. Reads and updates replace submitted paths, so payloads cannot select another root.
	LocalPath string `json:"local_path"`
	// S3 retains remote settings even while Type selects local storage. Public
	// configuration responses clear its secret; persisted settings carry the stored credential representation.
	S3 BackupS3Config `json:"s3"`
}

// BackupContentConfig selects optional row groups while keeping schemas exportable.
type BackupContentConfig struct {
	// IncludeUsageRecords, IncludeOpsLogs, IncludeAuditLogs, and IncludeRuntimeData
	// control the mapped table-data groups. False excludes that group's rows; default
	// settings leave all four false, and schemas remain in the PostgreSQL dump.
	IncludeUsageRecords bool `json:"include_usage_records"` // usage and billing history rows
	IncludeOpsLogs      bool `json:"include_ops_logs"`      // request and Ops history rows
	IncludeAuditLogs    bool `json:"include_audit_logs"`    // mapped specialized audit history
	IncludeRuntimeData  bool `json:"include_runtime_data"`  // mapped transient execution rows
	// ExcludedTableData is computed from the four flags for returned settings and
	// dump options. Submitted exclusions are discarded when configuration is normalized for storage.
	ExcludedTableData []string `json:"excluded_table_data,omitempty"`
}

// BackupS3Config holds credentials, placement, and transfer settings for remote archives.
type BackupS3Config struct {
	// Endpoint names a custom object-storage API. The S3 factory retains its
	// field pointer in the client, so callers keep this configuration field stable.
	Endpoint string `json:"endpoint"`
	// Region supplies request signing configuration. The S3 factory substitutes
	// auto when empty, supporting endpoints configured without their own supplied region.
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`        // remote archive bucket name
	AccessKeyID string `json:"access_key_id"` // remote access credential identifier
	// SecretAccessKey holds connection credential text, decrypted when possible
	// during loading and encrypted when newly supplied for persistence. Empty
	// updates reuse stored text; public getters clear this field.
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	// Prefix names remote object placement. Key construction removes trailing slashes
	// and uses backups when the resulting prefix is empty, then adds date and filename.
	Prefix         string `json:"prefix"`
	ForcePathStyle bool   `json:"force_path_style"` // bucket name in request path
	// UploadConcurrency and UploadPartSizeMB configure remote file transfer. The
	// provider normalizes their bounds, while unknown-length multipart streams use
	// separate fixed limits and spooled archive segments use sized PutObject.
	UploadConcurrency int `json:"upload_concurrency"`  // parallel file transfer workers
	UploadPartSizeMB  int `json:"upload_part_size_mb"` // MiB
	// UploadMode selects multipart or spooled_put preparation. Empty legacy settings
	// choose spooled_put; configuration updates reject unknown modes before any settings write.
	UploadMode string `json:"upload_mode"`
}

// BackupScheduleConfig controls cron backups and completed-archive retention selection.
type BackupScheduleConfig struct {
	// Enabled and CronExpr select the callback applied at startup or schedule
	// update. Enabled settings require a nonempty expression; supplied expressions
	// are validated during updates even when the callback is disabled.
	Enabled  bool   `json:"enabled"`   // installs the cron backup entry
	CronExpr string `json:"cron_expr"` // five-field minute-resolution cron expression
	// RetainDays supplies positive cleanup age and scheduled record expiration days.
	// Nonpositive values disable age cleanup, while scheduled expiration still defaults to fourteen days.
	RetainDays int `json:"retain_days"`
	// RetainCount limits retention by position in descending StartedAt text order.
	// Nonpositive values disable that rule; only completed records selected beyond the count are deleted.
	RetainCount int `json:"retain_count"`
}

// BackupRecord stores archive identity, locations, and backup and restore outcomes.
type BackupRecord struct {
	ID string `json:"id"` // eight-character UUID prefix identifier
	// Status describes archive production. Restore and download require completed;
	// deletion rejects running, while best-effort metadata saves can leave stale recorded state.
	Status     string `json:"status"`
	BackupType string `json:"backup_type"` // PostgreSQL archive type
	FileName   string `json:"file_name"`   // recorded archive download filename
	// StorageType is normalized before key inference when nonempty, with a
	// whitespace value becoming local. Missing or unknown values infer S3 from
	// legacy or part keys; remaining records use local storage.
	StorageType string `json:"storage_type,omitempty"`
	// StorageKey and S3Key describe the single-object location. Single-object reads
	// prefer nonempty StorageKey verbatim; cleanup considers both fields after trimming
	// and deduplication, retaining distinct keys from mixed-version metadata.
	StorageKey string `json:"storage_key,omitempty"` // current single archive object key
	S3Key      string `json:"s3_key"`                // legacy remote archive object key
	// Parts describes planned archive segments. A nonempty plan selects multipart
	// restore and downloads; consumers sort and validate indices, keys, and lengths before restoration.
	Parts       []BackupPart `json:"parts,omitempty"`
	SizeBytes   int64        `json:"size_bytes"`              // total compressed archive bytes
	TriggeredBy string       `json:"triggered_by"`            // manual or scheduled operation source
	ErrorMsg    string       `json:"error_message,omitempty"` // backup failure diagnostic text
	StartedAt   string       `json:"started_at"`              // RFC3339
	FinishedAt  string       `json:"finished_at,omitempty"`   // RFC3339
	// ExpiresAt records a positive requested expiration age as an RFC3339 timestamp.
	// Empty means no requested expiration; retention cleanup evaluates schedule rules against StartedAt instead.
	ExpiresAt string `json:"expires_at,omitempty"`
	// Progress names the pending, dumping, or uploading stage. Backup completion,
	// archive failure, and stale-running recovery clear it while Status describes the outcome.
	Progress string `json:"progress,omitempty"`
	// RestoreStatus and RestoreError track asynchronous restoration separately
	// from archive Status. RestoredAt changes on successful restoration; an earlier
	// success timestamp remains when a later attempt starts or fails, while success
	// clears RestoreError. These fields depend on their final metadata write succeeding.
	RestoreStatus string `json:"restore_status,omitempty"` // empty, running, completed, failed
	RestoreError  string `json:"restore_error,omitempty"`  // restoration failure diagnostic text
	RestoredAt    string `json:"restored_at,omitempty"`    // RFC3339
}

// BackupDownloadPart identifies a signed archive segment and its compressed size.
type BackupDownloadPart struct {
	Index     int    `json:"index"`      // one-based archive segment number
	SizeBytes int64  `json:"size_bytes"` // recorded compressed segment bytes
	URL       string `json:"url"`        // temporary object download URL
}

// BackupDownloadResponse contains a single-object URL or an ordered segment URL list.
type BackupDownloadResponse struct {
	// URL and Parts are alternatives populated by GetBackupDownloadURL. A nonempty
	// record part plan produces Parts after validation; a single-object record produces URL.
	URL   string               `json:"url,omitempty"`   // single archive download URL
	Parts []BackupDownloadPart `json:"parts,omitempty"` // ordered archive segment URLs
}

// BackupService coordinates archives, settings compatibility, recorded outcomes,
// and service shutdown.
type BackupService struct {
	// cleanupCancels and cleanupSequence register independent compensation contexts
	// under operationLifecycleMu. Shutdown-budget adoption links cancellation to existing
	// entries; completing cleanup removes its entry while sequence assigns later identifiers.
	cleanupCancels  map[uint64]context.CancelFunc // active cleanup cancellation functions
	cleanupSequence uint64                        // next cleanup registration sequence

	// log receives synchronous events. It can run under cron or records locks;
	// callbacks avoid reentering service methods needing the same lock or waiting for their worker.
	log func(string, string, ...any)

	// started and startDone publish one startup attempt under operationLifecycleMu.
	// stopOnce selects the stop initiator; stopErr is published before stopDone closes,
	// and all StopContext callers receive that same completion or timeout result.
	started bool // startup attempt has begun
	// startDone closes after the initial startup warmup returns, including
	// failure, and wakes duplicate StartContext callers. stopDone closes after the
	// first stop result is published in stopErr, including timeout while operation
	// waiting continues.
	// startDone: initial startup completion notification. stopDone: shared
	// stop-attempt completion notification.
	startDone, stopDone chan struct{}
	stopOnce            sync.Once // shared stop initiation guard
	stopErr             error     // first stop attempt result
	// shutdownContext supplies the adopted budget for final saves and compensation.
	// BeginStopContext replaces an absent or noncancellable context and retains an already cancellable budget.
	shutdownContext context.Context

	operationLifecycleMu sync.RWMutex      // admission and cleanup registry lock
	settingRepo          SettingRepository // persisted configuration and record settings
	localPath            string            // injected local store root
	// now supplies metadata and dated object keys. It can be called by concurrent
	// workers and request setup; key construction samples it separately from initial record timestamps.
	now     func() time.Time
	archive ArchiveExecutor // archive production and restoration executor
	// maintenance claims shared database maintenance and supplies a nonnil release
	// callback after acquisition. Callers retain the claim until synchronous work or worker cleanup finishes.
	maintenance MaintenanceLock
	encryptor   SecretEncryptor // stored S3 secret conversion
	// encryptionKeyConfigured permits encrypting newly supplied credentials for
	// storage. Reusing stored credential text remains allowed when this deployment flag is false.
	encryptionKeyConfigured bool // deployment supplies persistent secret key
	// storeFactory builds the cached store while storeMu is held. Factories
	// avoid reentering cache methods; temporary connection-test construction also uses this callback.
	storeFactory BackupObjectStoreFactory // remote store construction callback

	localStore BackupObjectStore // injected local archive store

	// opMu serializes backingUp and restoring admission and release. Each flag
	// rejects another operation of its own kind; cross-kind exclusion depends on maintenance.
	opMu      sync.Mutex // local operation flag lock
	backingUp bool       // backup operation is active
	restoring bool       // restoration operation is active

	// storeMu protects store and s3Cfg publication and invalidation. A nonnil pair
	// is reused regardless of later supplied configuration until a settings update resets it.
	storeMu sync.Mutex        // remote store cache lock
	store   BackupObjectStore // reusable remote object store
	s3Cfg   *BackupS3Config   // retained cached configuration pointer

	recordsMu sync.Mutex // record read-modify-write serialization lock

	// cronMu protects cronSched and cronEntryID across startup, schedule replacement,
	// and stop. Replacement removes the current entry before installing another, while
	// cron's own shutdown context accounts for callbacks already running.
	cronMu      sync.Mutex   // cron configuration mutation lock
	cronSched   *cron.Cron   // service-lifetime cron scheduler instance
	cronEntryID cron.EntryID // installed backup callback identifier

	// wg counts admitted setup, synchronous operations, and launched workers. begin
	// registers admission before the shutdown transition can start waiting; asynchronous
	// setup remains registered until its separately counted worker has been launched.
	wg           sync.WaitGroup
	shuttingDown atomic.Bool // rejects future operation admission
	// bgCtx and bgCancel control service work from construction through shutdown.
	// Workers derive their execution deadline here, while admitted setup also observes
	// request cancellation and cleanup can use its own bounded context.
	bgCtx    context.Context    // service background execution context
	bgCancel context.CancelFunc // service work cancellation function
}

// IsConfigured reports whether bucket, access ID, and secret strings are nonempty.
// It inspects their raw values, so whitespace passes this check. Connection tests
// or actual transfers determine whether those values identify usable remote storage.
func (c *BackupS3Config) IsConfigured() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// recoverStaleRecords marks recorded running backup or restore work interrupted
// by restart and attempts to persist that failure. Loading uses a bounded service
// context; an unreadable record list stops recovery before individual changes.
//
// For a running backup, the failed status is saved before recorded object cleanup,
// then cleanup failures are appended to its diagnostic and saved again. These
// writes are best effort, so recovery logs do not establish durable metadata.
// Restore recovery changes status separately; it cannot infer the database outcome
// of the interrupted process from a running metadata record.
func (s *BackupService) recoverStaleRecords() {
	loadCtx, loadCancel := context.WithTimeout(s.bgCtx, 10*time.Second)
	defer loadCancel()

	records, err := s.loadRecords(loadCtx)
	if err != nil {
		// Malformed metadata ends recovery before object deletion or status changes,
		// leaving the stored list and its archive references available for manual reconciliation.
		return
	}
	for i := range records {
		// Save the interrupted backup status before deleting its recorded keys, so
		// cleanup failure can be attached to the failed record on a second save.
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
		// A recorded running restore has independent outcome fields. Mark its interruption
		// without using that status alone to delete the archive or infer a database outcome.
		if records[i].RestoreStatus == "running" {
			records[i].RestoreStatus = "failed"
			records[i].RestoreError = "interrupted by server restart"
			s.saveRecoveredRecord(&records[i])
			s.log("service.backup", "[Backup] recovered stale restoring record: %s", records[i].ID)
		}
	}
}

// saveRecoveredRecord attempts to persist a recovered record under a fresh
// ten-second service context. Persistence failure is logged and suppressed so
// recovery can continue checking other interrupted backup and restore records.
func (s *BackupService) saveRecoveredRecord(record *BackupRecord) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 10*time.Second)
	defer cancel()
	// Suppress this persistence error after logging so other stale records can
	// still be examined even when one recovery update cannot be stored.
	if err := s.saveRecord(ctx, record); err != nil {
		s.log("service.backup", "[INFO] [Backup] Failed to save restored backup record %s: %v", record.ID, err)
	}
}

// cleanupStaleBackupObjects removes any keys known for an interrupted backup.
// Records without object keys return immediately; otherwise cleanup uses a separate
// two-minute context following an adopted shutdown budget and returns deletion errors.
func (s *BackupService) cleanupStaleBackupObjects(record *BackupRecord) error {
	if len(BackupObjectKeys(record)) == 0 {
		return nil
	}
	// Use the independent compensation budget so service-work cancellation can
	// still leave time to remove stale artifacts, subject to the adopted shutdown deadline.
	ctx, cancel := s.cleanupContext(backupObjectCleanupTimeout)
	defer cancel()
	return s.deleteBackupObjects(ctx, record)
}

// EncryptionKeyConfigured reports whether this service was constructed with a
// persistent deployment key. A nil service reports false; callers use the result
// to explain why newly supplied S3 secrets cannot be saved.
func (s *BackupService) EncryptionKeyConfigured() bool {
	return s != nil && s.encryptionKeyConfigured
}

// GetStorageConfig returns loaded destination settings with the service's local
// root and normalized upload mode. The returned S3 secret is empty, including when
// remote credentials remain saved for later use after selecting local storage.
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

// UpdateStorageConfig saves a local or S3 destination and returns its public
// settings with the secret cleared. Empty types select local; unrecognized types
// fail validation. S3 updates prepare encrypted credentials and require the three
// connection fields, while selecting local retains stored remote configuration.
//
// The unified setting is saved before the legacy S3 setting. A legacy-write error
// therefore returns after the unified change has persisted and before cache reset.
// On full success the remote cache is invalidated for subsequent store lookup;
// already captured stores remain available to their ongoing operations.
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
		// Read the stored credential representation when selecting local, so later
		// remote selection can reuse S3 settings and this write does not substitute decrypted secret text.
		cfg.S3 = s.loadStoredEncryptedS3Config(ctx)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal storage config: %w", err)
	}
	// Persist the unified destination first because subsequent backup lookup reads
	// it before legacy settings; a later legacy-write failure leaves this primary change in place.
	if err := s.settingRepo.Set(ctx, settingKeyBackupStorageConfig, string(data)); err != nil {
		return nil, fmt.Errorf("save storage config: %w", err)
	}

	if cfg.Type == BackupStorageTypeS3 {
		// Mirror remote settings for legacy readers after the unified write. If
		// this write fails, return the error before invalidating the currently cached store.
		s3Data, err := json.Marshal(cfg.S3)
		if err != nil {
			return nil, fmt.Errorf("marshal s3 config: %w", err)
		}
		if err := s.settingRepo.Set(ctx, settingKeyBackupS3Config, string(s3Data)); err != nil {
			return nil, fmt.Errorf("save s3 config: %w", err)
		}
	}

	// Discard the cached pair only after required settings writes finish, so the
	// next remote lookup reconstructs it while already captured worker stores remain usable.
	s.resetCachedS3Store()

	cfg.S3.SecretAccessKey = ""
	return &cfg, nil
}

// TestStorageConnection probes the selected destination using its supplied
// settings. Empty or unrecognized normalized types probe the injected local store;
// S3 delegates to its bucket-access test and can reuse a saved omitted secret.
func (s *BackupService) TestStorageConnection(ctx context.Context, cfg BackupStorageConfig) error {
	// Unknown labels normalize to empty and reach the local probe here. UpdateStorageConfig
	// rejects that same result, so probe success does not establish update eligibility.
	switch normalizeBackupStorageType(cfg.Type) {
	case BackupStorageTypeLocal, "":
		return s.localStore.HeadBucket(ctx)
	case BackupStorageTypeS3:
		return s.TestS3Connection(ctx, cfg.S3)
	default:
		return infraerrors.BadRequest("INVALID_BACKUP_STORAGE_TYPE", "backup storage type must be local or s3")
	}
}

// GetContentConfig returns the selected optional data groups and their computed
// table-data exclusions. Missing or unreadable settings use all groups disabled;
// malformed nonempty JSON returns an error before exclusions are produced.
func (s *BackupService) GetContentConfig(ctx context.Context) (*BackupContentConfig, error) {
	cfg, err := s.loadContentConfig(ctx)
	if err != nil {
		return nil, err
	}
	cfg.ExcludedTableData = s.buildExcludedTableData(cfg)
	return cfg, nil
}

// UpdateContentConfig persists the four optional-group flags and returns their
// computed exclusions. Submitted ExcludedTableData is discarded during normalization;
// these exclusions omit rows through dump options while keeping schemas exportable.
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

// GetS3Config returns remote settings for the legacy API with the secret cleared
// and upload mode normalized. It first reads the legacy setting; a present malformed
// value returns its configuration error, while an absent value permits unified
// storage settings to supply a configured remote destination.
//
// Errors from that secondary storage lookup are suppressed. When neither lookup
// supplies settings, the result contains the default spooled upload mode. A present
// legacy object, even if incomplete, takes precedence over unified settings, so
// public reads can differ from the destination used by a subsequent backup.
func (s *BackupService) GetS3Config(ctx context.Context) (*BackupS3Config, error) {
	cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return nil, err
	}
	// With no legacy object, try configured unified settings. Secondary read or
	// decode failure is suppressed so this public legacy lookup can still return default settings.
	if cfg == nil {
		if storageCfg, err := s.loadStorageConfig(ctx); err == nil && storageCfg != nil && storageCfg.S3.IsConfigured() {
			cfg = &storageCfg.S3
		}
	}
	if cfg == nil {
		return &BackupS3Config{UploadMode: BackupS3UploadModeSpooledPut}, nil
	}

	cfg.UploadMode = NormalizeBackupS3UploadMode(cfg.UploadMode)
	cfg.SecretAccessKey = ""
	return cfg, nil
}

// UpdateS3Config saves prepared remote settings through the legacy API and returns
// them with the secret cleared. Omitted secrets reuse stored credential text;
// new secrets require a persistent encryption key before the legacy write.
//
// After that required write, the method tries to mirror S3 settings into the
// loaded unified configuration while retaining its selected storage type. Loading,
// encoding, or saving that mirror can fail without failing the method. Remote
// cache invalidation still follows, so a successful legacy response does not prove
// that both persisted settings contain the same remote configuration.
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

	// The legacy save already succeeded. Try mirroring into unified settings
	// while retaining its selected destination; secondary loading or saving can
	// fail without failing this method.
	storageCfg, _ := s.loadStorageConfig(ctx)
	if storageCfg != nil {
		storageCfg.S3 = cfg
		if storageData, err := json.Marshal(storageCfg); err == nil {
			_ = s.settingRepo.Set(ctx, settingKeyBackupStorageConfig, string(storageData))
		}
	}

	// Invalidate after the required legacy save even if its unified mirror failed;
	// the next lookup reloads whichever configuration source it uses for the destination.
	s.resetCachedS3Store()

	cfg.SecretAccessKey = ""
	return &cfg, nil
}

// TestS3Connection constructs a temporary remote store and probes bucket access.
// An omitted secret reuses the legacy saved value when readable; incomplete
// credentials fail before construction. Bucket-access success does not establish
// archive upload permission.
func (s *BackupService) TestS3Connection(ctx context.Context, cfg BackupS3Config) error {
	// Omitted secrets reuse legacy credentials when readable; read or decode
	// failure leaves the omission intact and the required-field check then reports incomplete configuration.
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

// GetSchedule returns the stored cron and retention settings. Missing, empty,
// unreadable, or malformed settings return a zero configuration with scheduling
// disabled and no error; JSON null also leaves that zero configuration.
func (s *BackupService) GetSchedule(ctx context.Context) (*BackupScheduleConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupSchedule)
	// Missing or unreadable schedule settings disable scheduling for this read,
	// allowing startup and public settings lookup to proceed with an empty policy.
	if err != nil || raw == "" {
		return &BackupScheduleConfig{}, nil
	}
	var cfg BackupScheduleConfig
	// Treat malformed persisted schedule text like absent settings, so no invalid
	// cron expression is applied until an administrator supplies a valid replacement policy.
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return &BackupScheduleConfig{}, nil
	}
	return &cfg, nil
}

// UpdateSchedule validates and persists cron settings, then installs or removes
// the backup entry. Enabled settings require a nonempty expression; any supplied
// expression is parsed with five cron fields, including when scheduling is disabled.
//
// Persistence precedes scheduler mutation. An enabled update can therefore return
// an installation error after its JSON has been saved, with no settings rollback.
// A disabled update removes the current entry after saving. Callers initialize
// the service scheduler before enabling an entry and account for this distinction
// between stored configuration and the currently installed callback.
func (s *BackupService) UpdateSchedule(ctx context.Context, cfg BackupScheduleConfig) (*BackupScheduleConfig, error) {
	if cfg.Enabled && cfg.CronExpr == "" {
		return nil, infraerrors.BadRequest("INVALID_CRON", "cron expression is required when schedule is enabled")
	}

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
	// Save the intended policy before changing cron; an installation failure
	// therefore leaves persisted settings available for later startup or an administrator retry.
	if err := s.settingRepo.Set(ctx, settingKeyBackupSchedule, string(data)); err != nil {
		return nil, fmt.Errorf("save schedule config: %w", err)
	}

	if cfg.Enabled {
		if err := s.applyCronSchedule(&cfg); err != nil {
			return nil, err
		}
	} else {
		s.removeCronSchedule()
	}

	return &cfg, nil
}

// applyCronSchedule replaces the installed backup entry while holding cronMu.
// Callers supply a nonnil configuration after scheduler initialization; a missing
// scheduler returns an error. Logging runs before the mutex is released, so its
// callback must avoid acquiring that lock again.
//
// Replacement removes the prior entry and clears its identifier before AddFunc
// is attempted. If installation fails, the earlier callback is already gone;
// success publishes the new identifier under the same lock. Callbacks that cron
// already started continue under their separate operation admission and contexts.
func (s *BackupService) applyCronSchedule(cfg *BackupScheduleConfig) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()

	if s.cronSched == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}

	// Remove the existing entry before adding another under the same mutex,
	// preventing two installed callbacks while leaving no entry if the new addition fails.
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
	s.log("service.backup", "[Backup] Scheduled backup enabled: %s", cfg.CronExpr)
	return nil
}

// removeCronSchedule removes an installed backup entry under cronMu and clears
// its identifier. Missing scheduler or entry is a no-op; synchronous logging runs
// inside the lock. Running cron callbacks keep their own operation lifetime.
func (s *BackupService) removeCronSchedule() {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	// Clear the installed identifier while cron configuration remains locked;
	// running callbacks keep their admitted lifetimes and are accounted for during service stop.
	if s.cronSched != nil && s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
		s.log("service.backup", "[Backup] Scheduled backup disabled")
	}
}

// runScheduledBackup admits one cron execution and performs a synchronous backup
// under a thirty-minute service-derived context. Admission rejection ends the
// callback before reading settings or claiming maintenance; successful admission
// remains tracked until backup and any subsequent retention cleanup return.
//
// Positive RetainDays sets the recorded expiration; otherwise scheduled records
// expire after fourteen days. The schedule loaded for this run also selects
// retention cleanup after a successful archive. Backup failure skips cleanup,
// and cleanup failure is logged separately from archive success. Count and age
// retention apply to completed records, using their StartedAt values.
func (s *BackupService) runScheduledBackup() {
	// Count the callback through backup and retention work, so shutdown waits
	// for an admitted cron run even when cron removes its future schedule entry.
	ctx0, done, err := s.begin(s.bgCtx)
	if err != nil {
		return
	}
	defer done()

	ctx, cancel := context.WithTimeout(ctx0, 30*time.Minute)
	defer cancel()

	// GetSchedule returns an empty policy after read or parse failure; this
	// admitted callback still creates its archive using the default expiration before cleanup.
	schedule, _ := s.GetSchedule(ctx)
	expireDays := 14 // Scheduled expiration defaults to fourteen calendar days.
	if schedule != nil && schedule.RetainDays > 0 {
		expireDays = schedule.RetainDays
	}

	s.log("service.backup", "[Backup] Starting scheduled backup, expiration days: %d", expireDays)
	record, err := s.createBackup(ctx, "scheduled", expireDays)
	if err != nil {
		if errors.Is(err, ErrBackupInProgress) {
			s.log("service.backup", "[Backup] Scheduled backup skipped: a backup is already in progress")
		} else {
			s.log("service.backup", "[INFO] [Backup] Scheduled backup failed: %v", err)
		}
		return
	}
	s.log("service.backup", "[Backup] Scheduled backup completed: id=%s size=%d", record.ID, record.SizeBytes)

	// Run retention only after the backup returned success, using the policy
	// already read for this callback rather than mixing in a later settings update.
	if schedule == nil {
		return
	}
	// Cleanup failure is reported separately because this run has already
	// completed its archive; deletion errors keep affected metadata available for another attempt.
	if err := s.cleanupOldBackups(ctx, schedule); err != nil {
		s.log("service.backup", "[INFO] [Backup] Failed to clean up expired backups: %v", err)
	}
}

// createBackup writes a synchronous archive and returns its in-memory record and
// archive result. Callers provide an admitted operation context; shutdown rejects
// work before maintenance acquisition. The maintenance claim remains held through
// archive execution, while backingUp separately rejects another local backup.
//
// Initial progress and failure-record writes are best effort. Content-loading or
// archive failure returns a failed record even when saving it also fails; successful
// archive writing returns a completed record despite final-save failure, which is
// logged. Positive expireDays records an expiration date and nonpositive values
// leave it empty. This differs from StartBackup's mandatory initial registration.
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

	// Reserve the local backup flag through synchronous execution and release it
	// on every return, complementing maintenance with same-kind admission control.
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

	record := s.newBackupRecord(storageType, s3Cfg, triggeredBy, expireDays)

	dumpOptions, err := s.buildDumpOptions(ctx)
	if err != nil {
		record.Status = "failed"
		// Content selection failed before archive execution. Return that diagnostic
		// while attempting a failure record, even if its save also fails.
		record.ErrorMsg = fmt.Sprintf("load backup content config failed: %v", err)
		record.FinishedAt = s.now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, err
	}

	// Tolerate a failed progress save here and continue archive execution, whose
	// mandatory segment-plan writes can independently reject object storage work.
	record.Progress = "dumping"
	_ = s.saveRecord(ctx, record)
	sizeBytes, err := s.archive.Write(ctx, record, objectStore, s3Cfg, dumpOptions, s.saveRecord, s.cleanupContext)
	if err != nil {
		record.Status = "failed"
		// Keep the archive error after failure-record persistence is attempted;
		// a metadata error does not establish whether partial artifacts were removed.
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
	// Archive production succeeded before this save. Log metadata failure while
	// returning the completed in-memory result, leaving the written artifact intact.
	if err := s.saveRecord(ctx, record); err != nil {
		s.log("service.backup", "[INFO] [Backup] Failed to save backup record: %v", err)
	}

	return record, nil
}

// StartBackup registers an asynchronous backup and returns its initial running
// record with pending progress. Request and service cancellation govern setup;
// content options, destination lookup, and initial persistence must succeed before
// a worker is launched. Positive expireDays records an expiration, otherwise none.
//
// The operation reserves maintenance and backingUp through worker completion,
// releasing both if setup fails. Setup remains counted by lifecycle admission until
// the separately tracked worker is registered. A shallow record copy is made
// before worker mutation; that worker uses a thirty-minute service context, so
// HTTP request completion does not end archive work. Panics become best-effort
// failed-record finalization and release the reserved operation state.
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

	// Claim the same-kind flag after maintenance admission. A local conflict
	// returns this maintenance acquisition before reporting the active backup.
	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		releaseMaintenance()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()

	// Keep release responsibility with setup until worker launch succeeds; each
	// earlier error returns maintenance and the local reservation through this branch.
	launched := false
	defer func() {
		if !launched {
			releaseMaintenance()
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}
	}()

	// Capture before launch so a later cache reset can affect subsequent lookups
	// while this archive continues with its selected store, configuration, and keys.
	storageType, objectStore, s3Cfg, err := s.getCurrentBackupStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	record := s.newBackupRecord(storageType, s3Cfg, triggeredBy, expireDays)
	dumpOptions, err := s.buildDumpOptions(ctx)
	if err != nil {
		return nil, err
	}

	record.Progress = "pending"

	// Save pending metadata before launching background work; failure leaves setup
	// responsible for releasing its claim and prevents an unregistered worker.
	if err := s.saveRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("save initial record: %w", err)
	}

	launched = true
	// Snapshot response fields before worker mutation. The initial part slice is
	// empty here; later archive plan allocation belongs to the worker-owned record.
	result := *record

	// Register while begin still counts setup, so StopContext cannot observe an
	// empty operation set between request-side initialization and archive execution.
	s.wg.Go(func() {
		// Transfer cleanup to the worker. Deferred flag clearing and maintenance
		// release cover return, archive failure, and panic after recovery finalization.
		defer releaseMaintenance()
		defer func() {
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}()
		defer func() {
			// Attempt failed metadata after worker panic before reservation cleanup;
			// saveFinal reports settings failure independently from the captured panic diagnostic.
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
	})

	return &result, nil
}

// newBackupRecord creates running PostgreSQL metadata and its current and legacy
// object keys. Positive expireDays adds calendar days to the initial clock sample;
// nonpositive values leave expiration empty. The identifier is the first eight
// characters of a fresh UUID, while the filename uses the tr prefix and timestamp.
//
// StartedAt, filename, and expiration share the initial time sample. Storage-key
// construction samples the clock again for its date directory, so midnight can
// place that filename under the next day's path. S3 records duplicate the key in
// S3Key for legacy readers; local records leave that legacy field empty.
func (s *BackupService) newBackupRecord(storageType string, s3Cfg *BackupS3Config, triggeredBy string, expireDays int) *BackupRecord {
	now := s.now()
	// Record lookup and HTTP paths use this short UUID-prefix identifier.
	backupID := uuid.New().String()[:8]
	fileName := fmt.Sprintf("tr_%s.sql.gz", now.Format("20060102_150405"))
	// Key construction samples the clock again, allowing midnight to separate
	// placement date from the filename and StartedAt sample already captured above.
	storageKey := s.buildStorageKey(storageType, s3Cfg, fileName)

	var expiresAt string
	// Calendar-day expiration follows the initial clock sample's location.
	if expireDays > 0 {
		expiresAt = now.AddDate(0, 0, expireDays).Format(time.RFC3339)
	}

	return &BackupRecord{
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
}

// executeBackup writes an already registered archive using the captured store,
// S3 settings, and content options. Its thirty-minute context derives from the
// service lifetime; its caller owns operation reservation and wait-group cleanup.
//
// Dumping-progress persistence is best effort. Archive failure updates the record
// to failed and attempts final persistence through the separate finalization budget;
// archive success records size and completion before the same final save. A failed
// final save is logged and leaves the archive outcome intact, so persisted status
// can lag the in-memory result even after object writing succeeds.
func (s *BackupService) executeBackup(record *BackupRecord, objectStore BackupObjectStore, s3Cfg *BackupS3Config, dumpOptions BackupDumpOptions) {
	// Setup completion cancels admission's context; derive execution from the
	// service lifetime so returning the HTTP response does not cancel this archive worker.
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	// A failed progress write can leave only pending metadata; continue archive
	// execution, whose segment-plan callback can still require successful settings persistence.
	record.Progress = "dumping"
	_ = s.saveRecord(ctx, record)
	sizeBytes, err := s.archive.Write(ctx, record, objectStore, s3Cfg, dumpOptions, s.saveRecord, s.cleanupContext)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = err.Error()
		record.Progress = ""
		record.FinishedAt = s.now().Format(time.RFC3339)
		// Use the separate save budget after archive failure, including execution
		// cancellation; saveFinal logs any settings error while retaining this archive diagnostic.
		_ = s.saveFinal(record)
		return
	}

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.Progress = ""
	record.FinishedAt = s.now().Format(time.RFC3339)
	// Keep the completed archive outcome if persistence fails; this log reports
	// stale recorded status after object writing rather than attempting archive compensation.
	if err := s.saveFinal(record); err != nil {
		s.log("service.backup", "[INFO] [Backup] Failed to save backup record: %v", err)
	}
}

// restoreBackup synchronously restores a completed record using its effective
// storage type and object keys. Callers provide an admitted operation context;
// the maintenance claim is released after archive restoration returns, and a local
// restoring flag rejects a second restore until the synchronous work exits.
//
// Record lookup and completed-status validation happen before database input starts.
// The selected store uses current remote connection settings when the record is
// S3-backed. This synchronous path returns the archive executor's result directly;
// it does not publish the separate restore progress or completion fields used by
// StartRestore, so those fields cannot establish this call's database outcome.
func (s *BackupService) restoreBackup(ctx context.Context, backupID string) error {
	releaseMaintenance, acquired, err := s.maintenance(ctx)
	if err != nil {
		return fmt.Errorf("acquire database maintenance lock: %w", err)
	}
	if !acquired {
		return ErrDatabaseMaintenanceBusy
	}
	defer releaseMaintenance()

	// Hold the local restoration reservation until synchronous work exits, so
	// another restore cannot reuse this service while archive input is being applied.
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

// StartRestore registers background restoration of a completed archive and
// returns its metadata with RestoreStatus running. Request and service cancellation
// govern setup; record lookup, store selection, and the running-status save must
// succeed before any worker can begin restoration.
//
// Maintenance and restoring stay reserved through the launched worker. Failed
// setup releases them, and lifecycle admission remains counted until the worker
// is registered separately. The response is a shallow copy made before worker
// mutation, so callers keep shared Parts immutable during work. Service
// cancellation and a thirty-minute deadline govern the worker; failure attempts
// failure metadata persistence separately from the database operation.
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

	// Reserve restoration after acquiring maintenance. A local conflict releases
	// this acquisition immediately, while accepted setup retains both reservations for its worker.
	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		releaseMaintenance()
		return nil, ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()

	// Release both reservations on setup failure before a worker exists; after
	// launch, deferred worker cleanup takes responsibility for the flag and maintenance claim.
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

	// Save running status before database work starts, so a settings error takes
	// setup cleanup instead of launching a restore whose operation was never registered.
	record.RestoreStatus = "running"
	if err := s.saveRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("save restore record: %w", err)
	}

	launched = true
	// Copy scalar response fields before worker mutation. Part backing storage
	// remains shared, so callers keep returned Parts immutable while restoration runs.
	result := *record

	// Register before deferred setup completion removes its count, ensuring the
	// shutdown waiter still sees admitted work after the HTTP operation returns its record.
	s.wg.Go(func() {
		// Worker cleanup releases reservations after restoration or panic handling;
		// outcome persistence remains an attempt even if its independent final-save context expires.
		defer releaseMaintenance()
		defer func() {
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}()
		defer func() {
			// Attempt failed restore metadata after panic while retaining archive
			// fields; this state alone cannot establish the interrupted database operation's outcome.
			if r := recover(); r != nil {
				s.log("service.backup", "[Backup] restore panic recovered: %v", r)
				record.RestoreStatus = "failed"
				record.RestoreError = fmt.Sprintf("internal panic: %v", r)
				_ = s.saveFinal(record)
			}
		}()
		s.executeRestore(record, objectStore)
	})

	return &result, nil
}

// executeRestore applies an already registered archive under a thirty-minute
// service context and updates its separate restore outcome. The caller owns
// maintenance release, local reservation cleanup, and worker tracking; the supplied
// store was chosen during setup from the record's destination kind.
//
// Archive error marks restoration failed and attempts a separately budgeted final
// save. Archive success calls completeRestore only after restoration returns, so
// record persistence follows the database outcome. Saving either result can fail
// independently; a recorded failure or stale running state alone cannot prove that
// an uncertain database operation left its prior contents intact.
func (s *BackupService) executeRestore(record *BackupRecord, objectStore BackupObjectStore) {
	// Let restoration outlive request-side setup while keeping service cancellation
	// and an execution deadline active until the archive executor has returned.
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()
	if err := s.archive.Restore(ctx, record, objectStore); err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = err.Error()
		// Attempt failure persistence after the executor returns, using a separate
		// budget so execution cancellation can still leave time to store its diagnostic.
		_ = s.saveFinal(record)
		return
	}
	s.completeRestore(record)
}

// completeRestore marks the record's restoration completed, clears its restore
// diagnostic, and timestamps that outcome. Final persistence uses the separate
// service finalization budget; failure is logged after restoration returns success.
func (s *BackupService) completeRestore(record *BackupRecord) {
	record.RestoreStatus = "completed"
	record.RestoreError = ""
	record.RestoredAt = s.now().Format(time.RFC3339)
	// Restoration already returned success. Report this later metadata error
	// while the database contents remain outside the final-record persistence operation's control.
	if err := s.saveFinal(record); err != nil {
		s.log("service.backup", "[INFO] [Backup] Failed to save restore record: %v", err)
	}
}

// ListBackups returns decoded records sorted by descending StartedAt text.
// Unreadable or missing settings yield no records; malformed JSON returns an error.
// Sorting compares stored strings. Chronological ordering requires callers to
// supply timestamps with consistent formatting and time zones.
func (s *BackupService) ListBackups(ctx context.Context) ([]BackupRecord, error) {
	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})
	return records, nil
}

// GetBackupRecord finds the first decoded record whose ID matches backupID,
// or returns ErrBackupNotFound. The record belongs to this loaded list; later
// mutations require an explicit save to update the persisted JSON.
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

// DeleteBackup removes the last matching record's known objects before saving
// metadata with every matching ID removed. Record loading, object deletion, and
// final persistence hold recordsMu together, serializing this update with other
// service record writers.
// Missing identifiers fail, and a running backup is rejected before object deletion.
//
// Deletion attempts every deduplicated key. Any object error keeps the metadata
// available for retry even if other objects were removed. If all object deletions
// succeed but the final settings write fails, the old metadata can remain after
// its artifacts are gone. RestoreStatus does not participate in the running-backup
// check, so callers coordinate deletion against an active restoration separately.
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
		// An active upload can still use recorded objects; rejecting deletion keeps
		// its current or future archive segments available until backup production ends.
		return ErrBackupInProgress
	}

	// Delete objects before persisting the filtered list. If any deletion fails,
	// keep metadata so administrators can locate residual keys and retry partial cleanup.
	if err := s.deleteBackupObjects(ctx, found); err != nil {
		return err
	}

	// The objects are already removed when this list write begins; failure can
	// leave their old metadata visible, so the returned error reports incomplete record cleanup.
	return s.saveRecordsLocked(ctx, remaining)
}

// GetBackupDownloadURL returns one-hour signed URLs for a completed archive.
// Storage selection follows recorded destination kind and current connection
// settings. Stores that require application downloads return their presigning error;
// record lookup and completed-status checks precede any signing attempt.
//
// A part plan is copied, sorted, and validated before URLs are generated in segment
// order with their recorded sizes. Any signing error returns the partial response
// and an error, so callers use successful responses for downloads. A single-object
// record uses its effective key. URL generation checks part metadata, while
// multipart restoration separately checks downloaded sizes and any supplied digests.
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
	// Sort and validate a copied plan before signing any part, keeping URLs
	// paired with contiguous segment indices without rearranging the record's own slice.
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

// OpenBackupDownload opens a completed single-object archive from its recorded
// store and returns its body plus metadata. Callers close the body after reading;
// records with parts require individual segment downloads and are rejected here.
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

// loadStorageConfig reads unified destination settings, using a configured legacy
// S3 setting when unified settings are missing or unreadable. Otherwise it selects
// local storage. Malformed present unified JSON fails; null decodes to zero settings
// and follows local normalization with the injected path.
//
// Any populated embedded S3 field prevents legacy configuration replacement,
// even when credentials are incomplete. An entirely empty S3 object inherits
// legacy settings, while unknown stored destination types become local. Nonempty
// secrets are decrypted for connection use; failed decryption logs and retains
// the stored value to support plaintext legacy settings. This retained value can
// also be unusable ciphertext, so connection operations still determine usability.
func (s *BackupService) loadStorageConfig(ctx context.Context) (*BackupStorageConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupStorageConfig)
	// Missing or failed unified reads allow the legacy destination to select
	// remote storage; an incomplete legacy configuration leaves the injected local destination active.
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
	// Unknown stored labels use local storage, keeping malformed destination text
	// from selecting remote credentials implicitly; present invalid JSON still fails before this normalization.
	if cfg.Type == "" {
		cfg.Type = BackupStorageTypeLocal
	}
	cfg.LocalPath = s.localPath
	// Inherit legacy S3 settings only when every embedded field is empty; even
	// partial unified remote settings take precedence rather than combining unrelated credential configurations.
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
			// Retain stored text for legacy plaintext compatibility when decryption
			// fails; unusable ciphertext can also survive here and fail a subsequent connection.
			s.log("service.backup", "[INFO] [Backup] Failed to decrypt storage configuration S3 SecretAccessKey (possibly legacy unencrypted data): %v", err)
		} else {
			cfg.S3.SecretAccessKey = decrypted
		}
	}
	return &cfg, nil
}

// loadContentConfig returns normalized optional-group settings. Missing, empty,
// or failed reads disable all optional groups; malformed nonempty JSON fails.
// Null leaves every flag false; normalization drops stored derived exclusions.
func (s *BackupService) loadContentConfig(ctx context.Context) (*BackupContentConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupContentConfig)
	// Missing or unreadable content settings use the default exclusions, so a
	// settings outage does not implicitly include optional historical and runtime row groups.
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

// buildDumpOptions reads current content settings and derives their table-data
// exclusions for archive execution. It propagates malformed configuration before
// dump work begins. The four flags determine the exclusions, including their
// default selection when optional data groups are disabled.
func (s *BackupService) buildDumpOptions(ctx context.Context) (BackupDumpOptions, error) {
	cfg, err := s.loadContentConfig(ctx)
	if err != nil {
		return BackupDumpOptions{}, err
	}
	return BackupDumpOptions{
		ExcludeTableData: s.buildExcludedTableData(cfg),
	}, nil
}

// buildExcludedTableData returns sorted unique patterns for disabled optional
// groups. Nil settings use the default with all groups disabled; the supplied
// ExcludedTableData is ignored because the four flags determine this list.
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

// prepareS3ConfigForSave validates upload mode and prepares credential text for
// persistence. Empty mode selects spooled_put; unknown modes fail. Callers receive
// a copied configuration with either the retained stored secret or freshly encrypted
// text, and perform settings writes only after preparation succeeds.
//
// An omitted secret is read directly from unified settings before the legacy key,
// so an already encrypted value is not decrypted and then written back as plaintext.
// A newly supplied secret requires deployment key stability before encryption;
// encryption failure stops preparation. This rule allows nonsecret updates to
// reuse credentials even when the current deployment cannot safely store new ones.
func (s *BackupService) prepareS3ConfigForSave(ctx context.Context, cfg BackupS3Config) (*BackupS3Config, error) {
	uploadMode := NormalizeBackupS3UploadMode(cfg.UploadMode)
	if uploadMode == "" {
		return nil, infraerrors.BadRequest("INVALID_BACKUP_UPLOAD_MODE", "backup upload mode must be multipart or spooled_put")
	}
	cfg.UploadMode = uploadMode

	// Reuse the stored secret representation on omission so a nonsecret update
	// carries existing ciphertext forward without turning a decrypted connection value into persisted plaintext.
	if cfg.SecretAccessKey == "" {
		cfg.SecretAccessKey = s.loadStoredEncryptedS3Secret(ctx)
	} else {
		// Require deployment key stability before encrypting new credentials; an
		// automatically generated restart key would leave persisted secrets unreadable after the lifetime ends.
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

// loadStoredEncryptedS3Config reads remote settings in their stored credential
// representation for a destination update. A valid unified object with any populated
// S3 field takes precedence; otherwise the legacy setting supplies the result,
// or an empty configuration is returned.
//
// Read failures and malformed JSON are skipped rather than propagated because
// this helper retains whichever existing representation can be read. It performs
// no decryption, keeping ciphertext suitable for a later settings write. Partial
// unified S3 settings still win over complete legacy credentials, which differs
// from the secret-only reader's requirement for a nonempty unified secret.
func (s *BackupService) loadStoredEncryptedS3Config(ctx context.Context) BackupS3Config {
	// Read stored representations directly for the local-selection write path.
	// Skip failed or malformed unified settings and try legacy JSON without decrypting retained secret text.
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
	// Neither source supplied decodable retained settings. Return an empty value
	// so local selection can still be saved after failed reads or malformed remote JSON.
	return BackupS3Config{}
}

// loadStoredEncryptedS3Secret returns retained credential text for an update that
// omits the secret. A readable unified setting with a nonempty secret wins;
// otherwise readable legacy settings supply their secret, and no usable setting
// produces an empty result.
//
// Failures to read or decode either source are suppressed, allowing preparation
// to proceed with the remaining source. The helper never decrypts the selected
// text, so subsequent nonsecret updates can persist the same stored representation.
// Its precedence differs from whole-configuration loading: unrelated populated
// unified S3 fields do not prevent reuse of a legacy secret here.
func (s *BackupService) loadStoredEncryptedS3Secret(ctx context.Context) string {
	// Ignore failed or malformed sources while looking for retained secret text;
	// unlike whole-S3 inheritance, unrelated populated unified fields do not suppress a legacy credential.
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

// loadS3Config reads the legacy remote settings for connection use. Missing,
// empty, or failed reads return nil without error; malformed JSON fails. Nonempty
// secrets are decrypted when possible; failed decryption retains stored text.
// Upload mode is normalized before return. JSON null returns a nonnil zero
// configuration with the default upload mode.
func (s *BackupService) loadS3Config(ctx context.Context) (*BackupS3Config, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupS3Config)
	// Absent or unreadable legacy settings return no configuration, allowing
	// public lookup or destination loading to try their own defaults and unified-setting rules.
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no config is a valid state
	}
	var cfg BackupS3Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrBackupS3ConfigCorrupt
	}

	if cfg.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.SecretAccessKey)
		if err != nil {
			// Retain the stored value when decoding fails so legacy plaintext remains
			// usable; this compatibility path can also return invalid ciphertext to later connection attempts.
			s.log("service.backup", "[INFO] [Backup] Failed to decrypt S3 SecretAccessKey (possibly legacy unencrypted data): %v", err)
		} else {
			cfg.SecretAccessKey = decrypted
		}
	}
	cfg.UploadMode = NormalizeBackupS3UploadMode(cfg.UploadMode)
	return &cfg, nil
}

// getCurrentBackupStore selects the destination for a subsequent archive and
// returns its kind, store, and applicable S3 settings. Local selection uses the
// injected store. Remote selection requires present credentials and uses the
// cached factory-created store. Returned S3 settings come from this read; a
// previously cached store can retain another configuration until invalidation,
// so its client and the returned placement settings need not describe one load.
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

// getStoreForRecord selects a store from the archive's effective destination
// kind, allowing records to remain local or remote after the selected backup
// kind changes. Local records use the injected local store; S3 records require
// configured remote connection settings.
//
// Those connection settings come from the currently loaded unified S3 object,
// with legacy settings tried when it is incomplete. Recorded object keys identify
// archive placement within that store, but records contain no historical bucket
// or credential snapshot. Changing remote buckets can therefore redirect later
// lookups of an old key. The shared remote cache is reused until invalidated.
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
			// A remote record still needs credentials when current selection is
			// local; try legacy settings if the remembered unified S3 object is incomplete.
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

// getOrCreateStore returns the cached remote store or creates and publishes
// one while holding storeMu. An existing nonnil store/configuration pair wins
// even when cfg differs; an uncached lookup requires nonnil configuration and
// propagates factory failure without publishing a store.
//
// Successful construction stores the supplied configuration pointer with its
// result under the same lock, preventing competing cache misses from publishing
// multiple pairs. Callers keep retained configuration stable for the store's
// lifetime. Settings update methods invalidate this pair after their required
// writes, while ongoing operations can keep a previously returned store.
func (s *BackupService) getOrCreateStore(ctx context.Context, cfg *BackupS3Config) (BackupObjectStore, error) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()

	// Cache presence wins over incoming configuration, making update methods'
	// invalidation necessary before a later remote lookup can construct a different store.
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
	// Publish both members before releasing storeMu, so a competing cache miss
	// sees this store/configuration pair and reuses its retained configuration pointer.
	s.store = store
	s.s3Cfg = cfg
	return store, nil
}

// resetCachedS3Store clears the store and configuration pair under storeMu so
// the next remote lookup reconstructs it. Previously returned stores remain owned
// by consumers, including archive workers with a captured destination.
func (s *BackupService) resetCachedS3Store() {
	s.storeMu.Lock()
	// Clear both cache members under the same lock; consumers already holding
	// the previous store continue independently while a later lookup constructs another pair.
	s.store = nil
	s.s3Cfg = nil
	s.storeMu.Unlock()
}

// buildS3Key returns a remote prefix, freshly sampled date directory, and filename.
// It removes trailing slashes from Prefix and uses backups when that result is
// empty; other prefix text is retained as supplied.
func (s *BackupService) buildS3Key(cfg *BackupS3Config, fileName string) string {
	prefix := strings.TrimRight(cfg.Prefix, "/")
	if prefix == "" {
		prefix = "backups"
	}
	return fmt.Sprintf("%s/%s/%s", prefix, s.now().Format("2006/01/02"), fileName)
}

// buildStorageKey returns a dated local key or remote prefixed key for fileName.
// It samples the service clock separately from record creation; a nil remote
// configuration uses the backups prefix; supplied settings control remote placement.
func (s *BackupService) buildStorageKey(storageType string, s3Cfg *BackupS3Config, fileName string) string {
	if storageType == BackupStorageTypeLocal {
		return fmt.Sprintf("%s/%s", s.now().Format("2006/01/02"), fileName)
	}
	if s3Cfg == nil {
		return fmt.Sprintf("backups/%s/%s", s.now().Format("2006/01/02"), fileName)
	}
	return s.buildS3Key(s3Cfg, fileName)
}

// loadRecords reads the persisted record list under recordsMu and returns newly
// decoded metadata. Missing, empty, or failed settings reads yield no records;
// malformed JSON returns ErrBackupRecordsCorrupt to distinguish stored-data failure.
func (s *BackupService) loadRecords(ctx context.Context) ([]BackupRecord, error) {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()
	return s.loadRecordsLocked(ctx)
}

// loadRecordsLocked decodes the complete stored record list while its caller
// holds recordsMu. Missing, empty, or failed reads return nil without error;
// malformed JSON fails and JSON null leaves the result nil.
func (s *BackupService) loadRecordsLocked(ctx context.Context) ([]BackupRecord, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupRecords)
	// Read outages and absent settings appear as an empty list to record
	// consumers; callers cannot distinguish those cases through this helper's successful result.
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no records is a valid state
	}
	var records []BackupRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, ErrBackupRecordsCorrupt
	}
	return records, nil
}

// saveRecordsLocked encodes and writes the complete supplied record list while
// its caller holds recordsMu. Encoding and settings errors are returned; a nil
// slice is stored as JSON null and later loads as no records.
func (s *BackupService) saveRecordsLocked(ctx context.Context, records []BackupRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, settingKeyBackupRecords, string(data))
}

// saveRecord replaces the first matching ID or appends metadata while holding
// recordsMu across loading and persistence. Callers pass a nonnil record and
// avoid concurrent mutation during encoding. Malformed existing JSON is ignored
// here, so the write can rebuild the list from just this record.
//
// The list retains at most its last hundred entries in stored order after the
// update. Trimming removes metadata without deleting corresponding archive objects;
// object retention is handled separately by explicit deletion and schedule cleanup.
// The final settings write is returned to callers, which decide whether its
// failure is mandatory or best effort for their operation phase.
func (s *BackupService) saveRecord(ctx context.Context, record *BackupRecord) error {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	// Suppress a malformed-list error and rebuild from the supplied record;
	// a later successful write can replace corrupt metadata, leaving earlier object references untracked.
	records, _ := s.loadRecordsLocked(ctx)

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

	// Keep the latest appended metadata entries without deleting their artifacts;
	// explicit deletion and scheduled retention separately remove objects known by retained records.
	if len(records) > maxBackupRecords {
		records = records[len(records)-maxBackupRecords:]
	}

	return s.saveRecordsLocked(ctx, records)
}

// cleanupOldBackups removes completed archives selected by count or age and
// persists the kept records after object deletion. Nil scheduling is a no-op;
// record loading and the full cleanup hold recordsMu to serialize the eventual
// read-modify-write with other record updates.
//
// Count selection uses positions in descending StartedAt text order, including
// incomplete records in that ranking. Age selection uses successfully parsed
// RFC3339 timestamps and elapsed twenty-four-hour days from the wall clock;
// invalid timestamps skip age selection. Either rule can select a completed
// record. Object failures retain its metadata and join diagnostics; successful
// deletions precede the list write, whose failure can leave stale object references.
// The day-to-duration product is unchecked; settings accept arbitrary integers,
// so exceptionally large positive retention can overflow that age comparison.
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

	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})

	var toKeep []BackupRecord
	deletedCount := 0
	var cleanupErrs []error

	for i, r := range records {
		shouldDelete := false

		if schedule.RetainCount > 0 && i >= schedule.RetainCount {
			shouldDelete = true
		}

		// Age uses elapsed twenty-four-hour days from StartedAt; invalid timestamps
		// skip age selection while count selection still applies to the same record.
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
		// Keep metadata on any object failure, even after partial deletion, so
		// the remaining keys and joined error remain available for subsequent cleanup attempts.
		if err := s.deleteBackupObjects(ctx, &r); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete backup %s: %w", r.ID, err))
			toKeep = append(toKeep, r)
			continue
		}
		deletedCount++
	}

	// Persist the kept list after all successful object deletions. A write error
	// can leave old references to removed artifacts and joins any earlier cleanup failures.
	if deletedCount > 0 {
		if err := s.saveRecordsLocked(ctx, toKeep); err != nil {
			return errors.Join(append(cleanupErrs, err)...)
		}
		s.log("service.backup", "[Backup] Automatically cleaned up %d expired backups", deletedCount)
	}
	return errors.Join(cleanupErrs...)
}

// normalizeBackupStorageType trims and lowercases destination labels, treating
// empty input as local storage. Recognized local and S3 labels are returned;
// unrecognized input yields empty for each caller's validation or probe behavior.
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

// NormalizeBackupS3UploadMode trims and lowercases upload mode, using spooled_put
// for empty legacy settings. Multipart and spooled_put are accepted; unrecognized
// input returns empty, which settings updates reject before saving remote settings.
func NormalizeBackupS3UploadMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	// Settings written without an upload mode use the measured PutObject path,
	// keeping old remote configuration usable by the archive executor's spooled preparation.
	case "", BackupS3UploadModeSpooledPut:
		return BackupS3UploadModeSpooledPut
	case BackupS3UploadModeMultipart:
		return BackupS3UploadModeMultipart
	default:
		return ""
	}
}

// defaultBackupContentConfig disables every optional historical and runtime row group.
func defaultBackupContentConfig() BackupContentConfig {
	return BackupContentConfig{}
}

// normalizeBackupContentConfig copies the four optional-group flags into stored
// settings and drops the derived exclusion list. Callers compute ExcludedTableData
// from the normalized flags for responses and archive options; submitted
// patterns do not select dump contents.
func normalizeBackupContentConfig(cfg BackupContentConfig) BackupContentConfig {
	// Exclusions are derived response data; copying them here would store an
	// independent submitted list alongside the flags that actually control buildDumpOptions.
	return BackupContentConfig{
		IncludeUsageRecords: cfg.IncludeUsageRecords,
		IncludeOpsLogs:      cfg.IncludeOpsLogs,
		IncludeAuditLogs:    cfg.IncludeAuditLogs,
		IncludeRuntimeData:  cfg.IncludeRuntimeData,
	}
}

// uniqueSortedStrings trims entries, drops empty and duplicate values, and returns
// a sorted list. Empty input returns nil; a nonempty input containing only blanks
// returns an empty allocated slice, and the input slice is unchanged.
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

// backupS3ConfigHasValue reports whether any remote field prevents whole legacy
// configuration inheritance.
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

// RecordEffectiveStorageType determines which store kind a record's later
// operations use. Nonempty StorageType is normalized first, with recognized
// results taking precedence and whitespace becoming local. Nil records use
// local; unrecognized labels proceed through compatibility inference.
//
// A raw nonempty legacy S3Key selects S3, then any nonblank effective part key
// also selects S3 regardless of whether that key came from StorageKey or S3Key.
// Otherwise the result is local. A single-object StorageKey alone does not imply
// remote storage, so current local records need no remote credentials while older
// S3 and multipart metadata can still be located through their retained fields.
func RecordEffectiveStorageType(record *BackupRecord) string {
	if record == nil {
		return BackupStorageTypeLocal
	}
	if record.StorageType != "" {
		if normalized := normalizeBackupStorageType(record.StorageType); normalized != "" {
			return normalized
		}
	}
	// A legacy single-object record can lack destination kind. Its nonempty S3Key
	// selects remote storage before part inference, retaining the locator older readers use.
	if record.S3Key != "" {
		return BackupStorageTypeS3
	}
	// Legacy multipart plans can supply either part key field while omitting
	// storage type; a nonblank effective key selects S3 even when it came from StorageKey.
	for _, part := range record.Parts {
		if BackupPartStorageKey(part) != "" {
			return BackupStorageTypeS3
		}
	}
	return BackupStorageTypeLocal
}

// RecordEffectiveStorageKey returns a nonempty StorageKey, otherwise S3Key;
// a nil record returns empty. Values are returned verbatim, including whitespace,
// so single-object precedence differs from trimmed keys collected for cleanup.
func RecordEffectiveStorageKey(record *BackupRecord) string {
	if record == nil {
		return ""
	}
	if record.StorageKey != "" {
		return record.StorageKey
	}
	// Older single-object records can contain just S3Key; reuse that raw value
	// when the current field is empty so download and restore still locate the archive.
	return record.S3Key
}

// legacyS3Key duplicates storageKey for S3 metadata and returns empty for other
// storage types. Record creation uses it to populate the legacy field alongside
// the current key, keeping remote archives addressable by older readers.
func legacyS3Key(storageType, storageKey string) string {
	// Exact S3 selection populates the compatibility field. Current record
	// creation receives its normalized destination from store selection before invoking this helper.
	if storageType == BackupStorageTypeS3 {
		return storageKey
	}
	return ""
}

// BackupObjectKeys collects deduplicated archive keys for cleanup and returns
// nil for a nil record. It trims keys, ignores blanks, considers both top-level
// StorageKey and S3Key, then visits a copied part slice sorted by segment index.
//
// Each part supplies its preferred effective key, while distinct top-level current
// and legacy keys both remain eligible. This differs from single-object downloads,
// which choose one top-level key. Collection does not validate a contiguous segment
// plan or positive sizes, allowing incomplete upload records to retain cleanup
// candidates; sorting the copy leaves stored segment order unchanged.
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
	// Include distinct current and legacy top-level keys so mixed metadata can
	// clean both artifacts, then sort a copy of parts without requiring a valid restore plan.
	appendKey(record.StorageKey)
	appendKey(record.S3Key)
	parts := append([]BackupPart(nil), record.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
	for _, part := range parts {
		appendKey(BackupPartStorageKey(part))
	}
	return keys
}

// deleteBackupObjects removes known archive keys using the record's effective
// store and returns joined deletion failures. A record with no keys succeeds
// before store lookup. Recovery or deletion callers handle metadata changes.
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

// DeleteBackupObjectKeys attempts every deduplicated key and joins deletion
// errors after the last attempt. Nil stores fail even for empty records; callers
// retain metadata on error because some objects may remain after others were removed.
func DeleteBackupObjectKeys(ctx context.Context, objectStore BackupObjectStore, record *BackupRecord) error {
	if objectStore == nil {
		return errors.New("backup object store is unavailable")
	}
	var errs []error
	for _, key := range BackupObjectKeys(record) {
		// Continue after one object error to remove other known artifacts; callers
		// keep the record when joined failures show cleanup may have left residual keys.
		if err := objectStore.Delete(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("delete backup object %q: %w", key, err))
		}
	}
	return errors.Join(errs...)
}
