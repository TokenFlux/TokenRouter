package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func TestListRequestDetailsSLAOnlyUsesSLAErrorScope(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New error = %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := &opsRepository{db: db}
	start := time.Date(2026, 6, 18, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	// SLA 详情里的错误明细要排除配置的客户端侧状态码，避免和 SLA 卡片数字不一致。
	slaScopePattern := regexp.QuoteMeta("COALESCE(o.status_code, 0) IN (401, 403)") +
		`(?s).*` +
		regexp.QuoteMeta("o.upstream_status_code IS NOT NULL")

	mock.ExpectQuery(`(?s).*WITH combined AS.*`+slaScopePattern+`.*SELECT COUNT\(1\) FROM combined.*`).
		WithArgs(start, end, "error").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))

	mock.ExpectQuery(`(?s).*WITH combined AS.*`+slaScopePattern+`.*FROM combined.*LIMIT \$4 OFFSET \$5.*`).
		WithArgs(start, end, "error", 10, 0).
		WillReturnRows(emptyRequestDetailRows())

	items, total, err := repo.ListRequestDetails(context.Background(), &service.OpsRequestDetailFilter{
		StartTime: &start,
		EndTime:   &end,
		Kind:      string(service.OpsRequestKindError),
		SLAOnly:   true,
		IgnoredStatusCodes: []int{
			401,
			403,
		},
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListRequestDetails error = %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	if len(items) != 0 {
		t.Fatalf("items len = %d, want 0", len(items))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func emptyRequestDetailRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"kind",
		"created_at",
		"request_id",
		"platform",
		"model",
		"duration_ms",
		"status_code",
		"error_id",
		"phase",
		"severity",
		"message",
		"user_id",
		"api_key_id",
		"account_id",
		"group_id",
		"stream",
	})
}
