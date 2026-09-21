package backup_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/backup"
	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/stretchr/testify/require"
)

func TestDataManagementService_DeprecatedRPCMethods(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "datamanagement.sock")
	svc := backup.NewDataManagementServiceWithOptions(socketPath, 0)

	_, err := svc.GetConfig(context.Background())
	assertDeprecatedDataManagementError(t, err, socketPath)

	_, err = svc.CreateBackupJob(context.Background(), backup.DataManagementCreateBackupJobInput{BackupType: "full"})
	assertDeprecatedDataManagementError(t, err, socketPath)

	err = svc.DeleteS3Profile(context.Background(), "s3-default")
	assertDeprecatedDataManagementError(t, err, socketPath)
}

func assertDeprecatedDataManagementError(t *testing.T, err error, socketPath string) {
	t.Helper()

	require.Error(t, err)
	statusCode, status := s15httpx.ToHTTP(err)
	require.Equal(t, 503, statusCode)
	require.Equal(t, backup.DataManagementDeprecatedReason, status.Reason)
	require.Equal(t, socketPath, status.Metadata["socket_path"])
}
