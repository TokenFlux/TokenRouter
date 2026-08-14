package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryGetTokenStats_PaginationMode(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &opsRepository{db: db}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	groupID := int64(9)

	filter := &service.OpsTokenStatsFilter{
		TimeRange: "1d",
		StartTime: start,
		EndTime:   end,
		Platform:  " Anthropic ",
		GroupID:   &groupID,
		Page:      2,
		PageSize:  10,
	}

	rows := sqlmock.NewRows([]string{
		"model",
		"request_count",
		"avg_tokens_per_sec",
		"avg_first_token_ms",
		"total_output_tokens",
		"avg_duration_ms",
		"requests_with_first_token",
		"has_item",
		"total",
	}).
		AddRow("claude-sonnet-4", int64(20), 21.56, 120.34, int64(3000), int64(850), int64(18), true, int64(12)).
		AddRow("claude-opus-4", int64(20), 10.2, 240.0, int64(2500), int64(900), int64(20), true, int64(12))

	mock.ExpectQuery(`ORDER BY request_count DESC, model ASC\s+LIMIT \$5 OFFSET \$6[\s\S]+LEFT JOIN paged p ON TRUE`).
		WithArgs(start, end, groupID, "anthropic", 10, 10).
		WillReturnRows(rows)

	resp, err := repo.GetTokenStats(context.Background(), filter)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, int64(12), resp.Total)
	require.Equal(t, 2, resp.Page)
	require.Equal(t, 10, resp.PageSize)
	require.Nil(t, resp.TopN)
	require.Equal(t, "anthropic", resp.Platform)
	require.NotNil(t, resp.GroupID)
	require.Equal(t, groupID, *resp.GroupID)
	require.Len(t, resp.Items, 2)
	require.Equal(t, "claude-sonnet-4", resp.Items[0].Model)
	require.NotNil(t, resp.Items[0].AvgTokensPerSec)
	require.InDelta(t, 21.56, *resp.Items[0].AvgTokensPerSec, 0.0001)
	require.NotNil(t, resp.Items[0].AvgFirstTokenMs)
	require.InDelta(t, 120.34, *resp.Items[0].AvgFirstTokenMs, 0.0001)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsRepositoryGetTokenStats_TopNMode(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &opsRepository{db: db}

	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	filter := &service.OpsTokenStatsFilter{
		TimeRange: "1h",
		StartTime: start,
		EndTime:   end,
		TopN:      5,
	}

	rows := sqlmock.NewRows([]string{
		"model",
		"request_count",
		"avg_tokens_per_sec",
		"avg_first_token_ms",
		"total_output_tokens",
		"avg_duration_ms",
		"requests_with_first_token",
		"has_item",
		"total",
	}).
		AddRow("gpt-4o", int64(5), nil, nil, int64(0), int64(0), int64(0), true, int64(1))

	mock.ExpectQuery(`ORDER BY request_count DESC, model ASC\s+LIMIT \$3[\s\S]+LEFT JOIN paged p ON TRUE`).
		WithArgs(start, end, 5).
		WillReturnRows(rows)

	resp, err := repo.GetTokenStats(context.Background(), filter)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.TopN)
	require.Equal(t, 5, *resp.TopN)
	require.Equal(t, 0, resp.Page)
	require.Equal(t, 0, resp.PageSize)
	require.Len(t, resp.Items, 1)
	require.Nil(t, resp.Items[0].AvgTokensPerSec)
	require.Nil(t, resp.Items[0].AvgFirstTokenMs)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsRepositoryGetTokenStats_EmptyResult(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &opsRepository{db: db}

	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	filter := &service.OpsTokenStatsFilter{
		TimeRange: "30m",
		StartTime: start,
		EndTime:   end,
		Page:      1,
		PageSize:  20,
	}

	mock.ExpectQuery(`ORDER BY request_count DESC, model ASC\s+LIMIT \$3 OFFSET \$4[\s\S]+LEFT JOIN paged p ON TRUE`).
		WithArgs(start, end, 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"model",
			"request_count",
			"avg_tokens_per_sec",
			"avg_first_token_ms",
			"total_output_tokens",
			"avg_duration_ms",
			"requests_with_first_token",
			"has_item",
			"total",
		}).AddRow(nil, nil, nil, nil, nil, nil, nil, false, int64(0)))

	resp, err := repo.GetTokenStats(context.Background(), filter)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, int64(0), resp.Total)
	require.Len(t, resp.Items, 0)
	require.Equal(t, 1, resp.Page)
	require.Equal(t, 20, resp.PageSize)

	require.NoError(t, mock.ExpectationsWereMet())
}
