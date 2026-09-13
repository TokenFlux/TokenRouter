//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/stretchr/testify/require"
)

// 干跑只报告原分类，执行时按原截止时间/认证过滤删除分析记录。
func TestHistoricalIngressCleanupDryRunAndExecute(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { _, err := integrationDB.ExecContext(ctx, "TRUNCATE ops_error_logs"); require.NoError(t, err) })
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	// 独立提交的测试表状态只在此隔离容器内使用，事务确保数据写入一致。
	_, err = tx.ExecContext(ctx, "TRUNCATE ops_error_logs")
	require.NoError(t, err)
	before := time.Now().UTC()
	for _, v := range []struct {
		code, phase string
		at          time.Time
	}{{"INVALID_API_KEY", "auth", before.Add(-time.Minute)}, {"API_KEY_REQUIRED", "auth", before.Add(-time.Minute)}, {"API_KEY_QUOTA_EXHAUSTED", "auth", before.Add(-time.Minute)}, {"INVALID_API_KEY", "upstream", before.Add(-time.Minute)}, {"INVALID_API_KEY", "auth", before.Add(time.Minute)}} {
		_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs(error_phase,error_type,severity,status_code,error_message,error_body,created_at) VALUES($1,'authentication_error','warn',401,'',$2,$3)`, v.phase, `{"code":"`+v.code+`"}`, v.at)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	store := NewHistoricalIngressCleanup(integrationDB)
	counts, scanned, matched, deleted, err := ops.CleanupHistoricalIngress(ctx, store, before, 1, false)
	require.NoError(t, err)
	require.EqualValues(t, 3, scanned)
	require.EqualValues(t, 2, matched)
	require.Zero(t, deleted)
	require.Equal(t, map[string]int64{"invalid_key": 1, "missing_key": 1}, counts)
	var actual int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_error_logs").Scan(&actual))
	require.Equal(t, 5, actual)
	_, scanned, matched, deleted, err = ops.CleanupHistoricalIngress(ctx, store, before, 1, true)
	require.NoError(t, err)
	require.EqualValues(t, 3, scanned)
	require.EqualValues(t, 2, matched)
	require.EqualValues(t, 2, deleted)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_error_logs").Scan(&actual))
	require.Equal(t, 3, actual)
}
