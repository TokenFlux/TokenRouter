package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// 聚合开启事务后仍须使用注入的时区，日桶不能退化为固定 24 小时。
func TestAggregationCalendarSurvivesTransaction(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		month time.Month
		day   int
		hours int
	}{
		{name: "spring", month: time.March, day: 8, hours: 23},
		{name: "autumn", month: time.November, day: 1, hours: 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			calendar := timezone.NewCalendar(loc)
			repo := NewAggregationStoreWithSQL(db, calendar)
			start := time.Date(2026, tc.month, tc.day, 0, 0, 0, 0, loc)
			end := start.AddDate(0, 0, 1)
			require.Equal(t, time.Duration(tc.hours)*time.Hour, end.Sub(start))

			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO usage_dashboard_hourly_users").
				WithArgs(start, end, "America/New_York").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("INSERT INTO usage_dashboard_daily_users").
				WithArgs(start, end, "America/New_York").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("WITH hourly AS").
				WithArgs(start, end, "America/New_York").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("WITH daily AS").
				WithArgs(start, end, start, end, "America/New_York").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()

			require.NoError(t, repo.AggregateRange(context.Background(), start.UTC(), end.UTC()))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
