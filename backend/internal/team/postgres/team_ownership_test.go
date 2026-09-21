//go:build unit

package postgres

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestTransferTeamOwnershipUsesTwoOrderedUpdates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		// 关闭由测试自身负责，单独登记预期，避免掩盖前面的事务顺序断言。
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}()

	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE team_memberships SET role = 'member'")).
		WithArgs(int64(7), int64(11), int64(22), now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE team_memberships SET role = 'owner'")).
		WithArgs(int64(7), int64(11), int64(22), now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, transferTeamOwnership(context.Background(), tx, 7, 11, 22, now))
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
