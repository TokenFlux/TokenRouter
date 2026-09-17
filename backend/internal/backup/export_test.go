//go:build unit

package backup

import (
	"context"
)

func AccessSaveRecord(s *BackupService, c context.Context, r *BackupRecord) error {
	return s.saveRecord(c, r)
}
func AccessLoadRecords(s *BackupService, c context.Context) ([]BackupRecord, error) {
	return s.loadRecords(c)
}
func AccessLoadS3(s *BackupService, c context.Context) (*BackupS3Config, error) {
	return s.loadS3Config(c)
}
func AccessRecover(s *BackupService)                 { s.recoverStaleRecords() }
func AccessWait(s *BackupService)                    { s.wg.Wait() }
func AccessBackingUp(s *BackupService)               { s.opMu.Lock(); s.backingUp = true; s.opMu.Unlock() }
func AccessArchive(s *BackupService) ArchiveExecutor { return s.archive }

const TestSettingKeyBackupS3Config = settingKeyBackupS3Config
const TestSettingKeyBackupStorageConfig = settingKeyBackupStorageConfig
const TestSettingKeyBackupContentConfig = settingKeyBackupContentConfig
const TestSettingKeyBackupSchedule = settingKeyBackupSchedule
const TestSettingKeyBackupRecords = settingKeyBackupRecords
const TestMaxBackupRecords = maxBackupRecords
