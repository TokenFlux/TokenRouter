package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// TestGetUserDashboardStatsUsesIndexableOwnedTeamScope 锁定先解析团队再分支扫描的查询形状。
func TestGetUserDashboardStatsUsesIndexableOwnedTeamScope(t *testing.T) {
	db, mock := newSQLMock(t)
	// 团队解析完成后，Key、用量和实时性能查询会并行执行。
	mock.MatchExpectationsInOrder(false)
	repo := &usageLogRepository{sql: db}

	mock.ExpectQuery("(?s)SELECT \\(.*FROM team_memberships.*tm.user_id = \\$1.*tm.role = 'owner'.*tm.left_at IS NULL").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(42)))
	mock.ExpectQuery("(?s)WITH scoped AS.*FROM api_keys.*user_id = \\$1.*UNION ALL.*team_id = \\$2 AND user_id <> \\$1.*COUNT\\(\\*\\) FILTER").
		WithArgs(int64(7), int64(42), service.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"total", "active"}).AddRow(int64(3), int64(2)))
	mock.ExpectQuery("(?s)WITH scoped AS.*FROM usage_logs WHERE user_id = \\$1.*UNION ALL.*team_id = \\$2 AND user_id <> \\$1.*COUNT\\(\\*\\) FILTER").
		WithArgs(int64(7), int64(42), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_creation_tokens",
			"total_cache_read_tokens", "total_cost", "total_actual_cost", "total_duration_ms", "duration_count",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens",
			"today_cache_read_tokens", "today_cost", "today_actual_cost",
		}).AddRow(
			int64(3), int64(10), int64(20), int64(2), int64(4), 0.4, 0.3, int64(300), int64(3),
			int64(1), int64(5), int64(6), int64(1), int64(2), 0.2, 0.15,
		))
	mock.ExpectQuery("(?s)WITH scoped AS.*FROM usage_logs.*user_id = \\$1 AND created_at >= \\$3.*UNION ALL.*team_id = \\$2 AND user_id <> \\$1").
		WithArgs(int64(7), int64(42), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count"}).AddRow(int64(10), int64(100)))

	stats, err := repo.GetUserDashboardStats(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, int64(3), stats.TotalAPIKeys)
	require.Equal(t, int64(3), stats.TotalRequests)
	require.Equal(t, int64(36), stats.TotalTokens)
	require.Equal(t, int64(2), stats.Rpm)
	require.Equal(t, int64(20), stats.Tpm)
	require.NoError(t, mock.ExpectationsWereMet())
}
