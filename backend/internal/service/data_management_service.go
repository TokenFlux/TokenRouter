package service

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/backup"
)

type DataManagementAgentHealth = backup.DataManagementAgentHealth
type DataManagementAgentInfo = backup.DataManagementAgentInfo
type DataManagementService = backup.DataManagementService

var DefaultDataManagementAgentSocketPath = backup.DefaultDataManagementAgentSocketPath
var LegacyBackupAgentSocketPath = backup.LegacyBackupAgentSocketPath
var DataManagementDeprecatedReason = backup.DataManagementDeprecatedReason
var DataManagementAgentSocketMissingReason = backup.DataManagementAgentSocketMissingReason
var DataManagementAgentUnavailableReason = backup.DataManagementAgentUnavailableReason
var DefaultBackupAgentSocketPath = backup.DefaultDataManagementAgentSocketPath
var BackupAgentSocketMissingReason = backup.BackupAgentSocketMissingReason
var BackupAgentUnavailableReason = backup.BackupAgentUnavailableReason
var ErrDataManagementDeprecated = backup.ErrDataManagementDeprecated
var ErrDataManagementAgentSocketMissing = backup.ErrDataManagementAgentSocketMissing
var ErrDataManagementAgentUnavailable = backup.ErrDataManagementAgentUnavailable
var ErrBackupAgentSocketMissing = backup.ErrDataManagementAgentSocketMissing
var ErrBackupAgentUnavailable = backup.ErrBackupAgentUnavailable

func NewDataManagementService() *DataManagementService { return backup.NewDataManagementService() }
func NewDataManagementServiceWithOptions(path string, timeout time.Duration) *DataManagementService {
	return backup.NewDataManagementServiceWithOptions(path, timeout)
}
