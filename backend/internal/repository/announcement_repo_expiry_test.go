package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 批量归档必须同时限制展示状态和结束时间，避免改变草稿或未到期公告。
func TestAnnouncementRepositoryArchiveExpired(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(sql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "announcements" SET "status" = $1, "updated_at" = $2 WHERE ("announcements"."status" = $3 AND "announcements"."ends_at" IS NOT NULL) AND "announcements"."ends_at" <= $4`)).
		WithArgs(service.AnnouncementStatusArchived, sqlmock.AnyArg(), service.AnnouncementStatusActive, now).
		WillReturnResult(sqlmock.NewResult(0, 2))

	repo := NewAnnouncementRepository(client)
	updated, err := repo.ArchiveExpired(context.Background(), now)

	require.NoError(t, err)
	require.EqualValues(t, 2, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}
