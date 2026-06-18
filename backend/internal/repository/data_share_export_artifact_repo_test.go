package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDataShareExportArtifactRepository_MarkDeletedReturnsOldStoragePath(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewDataShareExportArtifactRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT storage_path, remote_status
		FROM data_share_export_artifacts
		WHERE id = $1
		FOR UPDATE
	`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"storage_path", "remote_status"}).AddRow("/tmp/export.jsonl", "uploaded"))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE data_share_export_artifacts
		SET status = 'deleted', storage_path = '', deleted_at = COALESCE(deleted_at, NOW()), updated_at = NOW()
		WHERE id = $1
	`)).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	path, err := repo.MarkDeleted(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "/tmp/export.jsonl", path)
	require.NoError(t, mock.ExpectationsWereMet())
}
