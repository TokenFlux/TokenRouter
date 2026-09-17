package service

import (
	"context"
	"database/sql"
	"time"

	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/backup"
	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

type DBDumper = backup.DBDumper
type BackupDumpOptions = backup.BackupDumpOptions
type BackupObjectStore = backup.BackupObjectStore
type BackupObjectStoreProgressUploader = backup.BackupObjectStoreProgressUploader
type BackupObjectStoreSizedUploader = backup.BackupObjectStoreSizedUploader
type BackupObjectStoreFactory = backup.BackupObjectStoreFactory
type BackupStorageConfig = backup.BackupStorageConfig
type BackupContentConfig = backup.BackupContentConfig
type BackupS3Config = backup.BackupS3Config
type BackupScheduleConfig = backup.BackupScheduleConfig
type BackupRecord = backup.BackupRecord
type BackupDownloadPart = backup.BackupDownloadPart
type BackupDownloadResponse = backup.BackupDownloadResponse
type BackupPart = backup.BackupPart
type LocalBackupStore = bp.LocalBackupStore

var BackupStorageTypeLocal = backup.BackupStorageTypeLocal
var BackupStorageTypeS3 = backup.BackupStorageTypeS3
var BackupS3UploadModeMultipart = backup.BackupS3UploadModeMultipart
var BackupS3UploadModeSpooledPut = backup.BackupS3UploadModeSpooledPut
var ErrBackupS3NotConfigured = backup.ErrBackupS3NotConfigured
var ErrBackupStorageNotConfigured = backup.ErrBackupStorageNotConfigured
var ErrBackupNotFound = backup.ErrBackupNotFound
var ErrBackupInProgress = backup.ErrBackupInProgress
var ErrRestoreInProgress = backup.ErrRestoreInProgress
var ErrBackupRecordsCorrupt = backup.ErrBackupRecordsCorrupt
var ErrBackupS3ConfigCorrupt = backup.ErrBackupS3ConfigCorrupt
var ErrBackupStorageConfigCorrupt = backup.ErrBackupStorageConfigCorrupt
var ErrBackupContentConfigCorrupt = backup.ErrBackupContentConfigCorrupt
var ErrDatabaseMaintenanceBusy = backup.ErrDatabaseMaintenanceBusy
var ErrSecretEncryptionKeyNotConfigured = backup.ErrSecretEncryptionKeyNotConfigured

// BackupService 仅保留旧装配签名，生产状态由唯一核心持有。
type BackupService struct{ *backup.BackupService }

func NewBackupService(repo SettingRepository, cfg *config.Config, cipher SecretEncryptor, factory BackupObjectStoreFactory, dumper DBDumper) *BackupService {
	path := bp.DefaultBackupLocalPath()
	return &BackupService{backup.New(repo, backup.Options{Log: logging.LegacyPrintf, DatabaseName: cfg.Database.DBName, LocalPath: path, EncryptionKeyConfigured: cfg.Totp.EncryptionKeyConfigured, Now: time.Now}, cipher, factory, bp.NewLocalBackupStore(path), bp.NewArchive(dumper, 0), nil)}
}
func (s *BackupService) SetMaintenanceDB(db *sql.DB) {
	s.SetMaintenanceLock(func(ctx context.Context) (func(), bool, error) {
		return tryAcquireDatabaseHeavyMaintenanceLock(ctx, db)
	})
}
func NewLocalBackupStore(path string) *LocalBackupStore { return bp.NewLocalBackupStore(path) }
func NormalizeBackupS3UploadMode(value string) string {
	return backup.NormalizeBackupS3UploadMode(value)
}
