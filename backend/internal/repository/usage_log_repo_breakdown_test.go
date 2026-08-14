//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/pkg/usagestats"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestResolveEndpointColumn(t *testing.T) {
	tests := []struct {
		endpointType string
		want         string
	}{
		{"inbound", "ul.inbound_endpoint"},
		{"upstream", "ul.upstream_endpoint"},
		{"path", "ul.inbound_endpoint || ' -> ' || ul.upstream_endpoint"},
		{"", "ul.inbound_endpoint"},        // default
		{"unknown", "ul.inbound_endpoint"}, // fallback
	}

	for _, tc := range tests {
		t.Run(tc.endpointType, func(t *testing.T) {
			got := resolveEndpointColumn(tc.endpointType)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveModelDimensionExpression(t *testing.T) {
	tests := []struct {
		modelType string
		want      string
	}{
		{usagestats.ModelSourceRequested, "COALESCE(NULLIF(TRIM(model), ''), NULLIF(TRIM(requested_model), ''), '')"},
		{usagestats.ModelSourceUpstream, "COALESCE(NULLIF(TRIM(upstream_model), ''), model)"},
		{usagestats.ModelSourceMapping, "(COALESCE(NULLIF(TRIM(model), ''), NULLIF(TRIM(requested_model), ''), '') || ' -> ' || COALESCE(NULLIF(TRIM(upstream_model), ''), model))"},
		{"", "COALESCE(NULLIF(TRIM(model), ''), NULLIF(TRIM(requested_model), ''), '')"},
		{"invalid", "COALESCE(NULLIF(TRIM(model), ''), NULLIF(TRIM(requested_model), ''), '')"},
	}

	for _, tc := range tests {
		t.Run(tc.modelType, func(t *testing.T) {
			got := resolveModelDimensionExpression(tc.modelType)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAccountFilteredModelStatsKeepUserActualCost(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	accountID := int64(3667)

	// 账号筛选不能把用户实际扣费替换为账号成本，两种口径必须分别返回。
	actualExpr := regexp.QuoteMeta("COALESCE(SUM(actual_cost), 0) as actual_cost")
	accountExpr := regexp.QuoteMeta("COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0) as account_cost")
	mock.ExpectQuery("(?s)"+actualExpr+".*"+accountExpr+".*account_id = \\$3").
		WithArgs(start, end, accountID).
		WillReturnRows(sqlmock.NewRows([]string{
			"model", "requests", "input_tokens", "output_tokens",
			"cache_creation_tokens", "cache_read_tokens", "total_tokens",
			"cost", "actual_cost", "account_cost",
		}).AddRow("glm-5.2", int64(1247), int64(100), int64(200), int64(0), int64(0), int64(300), 10.603, 530.797, 10.603))

	rows, err := repo.GetModelStatsWithFilters(context.Background(), start, end, 0, 0, accountID, 0, nil, nil, nil)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.InDelta(t, 530.797, rows[0].ActualCost, 0.000001)
	require.InDelta(t, 10.603, rows[0].AccountCost, 0.000001)
	require.InDelta(t, 10.603, rows[0].Cost, 0.000001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountFilteredEndpointStatsKeepUserActualCost(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	accountID := int64(3667)
	actualExpr := regexp.QuoteMeta("COALESCE(SUM(actual_cost), 0) as actual_cost")

	// 入站、上游和完整路径三个聚合入口必须保持同一用户扣费口径。
	for _, endpoint := range []string{"/v1/chat/completions", "/api/v1/coding/chat", "/v1/chat/completions -> /api/v1/coding/chat"} {
		mock.ExpectQuery("(?s)SELECT.*"+actualExpr+".*account_id = \\$3").
			WithArgs(start, end, accountID).
			WillReturnRows(sqlmock.NewRows([]string{"endpoint", "requests", "total_tokens", "cost", "actual_cost"}).
				AddRow(endpoint, int64(1247), int64(300), 10.603, 530.797))
	}

	inbound, err := repo.GetEndpointStatsWithFilters(context.Background(), start, end, 0, 0, accountID, 0, "", nil, nil, nil)
	require.NoError(t, err)
	upstream, err := repo.GetUpstreamEndpointStatsWithFilters(context.Background(), start, end, 0, 0, accountID, 0, "", nil, nil, nil)
	require.NoError(t, err)
	paths, err := repo.getEndpointPathStatsWithFilters(context.Background(), start, end, 0, 0, accountID, 0, 0, "", "", nil, nil, nil, "", false, false)
	require.NoError(t, err)

	for _, rows := range [][]EndpointStat{inbound, upstream, paths} {
		require.Len(t, rows, 1)
		require.InDelta(t, 530.797, rows[0].ActualCost, 0.000001)
		require.InDelta(t, 10.603, rows[0].Cost, 0.000001)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGeminiUsageTotalsBatchUsesAccountCost(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	accountID := int64(3667)
	accountCostExpr := regexp.QuoteMeta("COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)")

	// 批量路径必须与单账号模型聚合一样使用账号成本，不能回退到用户实际扣费。
	mock.ExpectQuery("(?s)"+accountCostExpr+".*"+accountCostExpr+".*FROM usage_logs").
		WithArgs(sqlmock.AnyArg(), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"account_id", "flash_requests", "pro_requests", "flash_tokens", "pro_tokens", "flash_cost", "pro_cost",
		}).AddRow(accountID, int64(3), int64(2), int64(400), int64(300), 2.0, 10.0))

	totals, err := repo.GetGeminiUsageTotalsBatch(context.Background(), []int64{accountID}, start, end)

	require.NoError(t, err)
	require.Equal(t, int64(3), totals[accountID].FlashRequests)
	require.Equal(t, int64(2), totals[accountID].ProRequests)
	require.InDelta(t, 2, totals[accountID].FlashCost, 0.000001)
	require.InDelta(t, 10, totals[accountID].ProCost, 0.000001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserBreakdownStatsRequestTypeIncludesLegacyFallback(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	requestType := int16(service.RequestTypeStream)

	legacyFilter := `(ul.request_type = $3 OR (ul.request_type = 0 AND ul.stream = TRUE AND ul.openai_ws_mode = FALSE))`
	mock.ExpectQuery(regexp.QuoteMeta(legacyFilter)).
		WithArgs(start, end, requestType).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "email", "requests", "input_tokens", "output_tokens",
			"cache_tokens", "total_tokens", "cost", "actual_cost", "account_cost",
		}))

	rows, err := repo.GetUserBreakdownStats(context.Background(), start, end, usagestats.UserBreakdownDimension{
		RequestType: &requestType,
	}, 0)

	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
