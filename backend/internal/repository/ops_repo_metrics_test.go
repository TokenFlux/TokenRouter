package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryInsertSystemMetricsPreservesZeroDBPoolCounts(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &opsRepository{db: db}
	createdAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	active := 0
	idle := 10

	args := make([]driver.Value, 43)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	// 参数索引 38、39、40 分别对应活跃、空闲和等待连接数，等待连接缺失时仍应写入 NULL。
	args[38] = int64(0)
	args[39] = int64(10)
	args[40] = nil

	mock.ExpectExec("INSERT INTO ops_system_metrics").
		WithArgs(args...).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.InsertSystemMetrics(context.Background(), &service.OpsInsertSystemMetricsInput{
		CreatedAt:     createdAt,
		WindowMinutes: 1,
		DBConnActive:  &active,
		DBConnIdle:    &idle,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
