package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildContentModerationLogWhere_BlockedIncludesAllBlockActions(t *testing.T) {
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{Result: "blocked"})

	require.Empty(t, args)
	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.action IN ('block', 'keyword_block', 'hash_block')")
	require.NotContains(t, sql, "l.action = 'block'")
}

func TestContentModerationRepositoryCreateLog_PersistsMatchedKeyword(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewContentModerationRepository(db)
	userID := int64(1001)
	apiKeyID := int64(2001)
	groupID := int64(3001)
	latencyMS := 42
	queueDelayMS := 7
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	log := &service.ContentModerationLog{
		RequestID:         "req-keyword",
		UserID:            &userID,
		UserEmail:         "user@example.com",
		APIKeyID:          &apiKeyID,
		APIKeyName:        "default-key",
		GroupID:           &groupID,
		GroupName:         "default",
		Endpoint:          "/v1/messages",
		Provider:          "anthropic",
		Model:             "claude-sonnet-4",
		Mode:              service.ContentModerationModePreBlock,
		Action:            service.ContentModerationActionKeywordBlock,
		Flagged:           true,
		HighestCategory:   "keyword",
		HighestScore:      1,
		CategoryScores:    map[string]float64{"keyword": 1},
		ThresholdSnapshot: map[string]float64{"keyword": 1},
		InputExcerpt:      "blocked prompt",
		UpstreamLatencyMS: &latencyMS,
		ViolationCount:    1,
		EmailSent:         true,
		QueueDelayMS:      &queueDelayMS,
		MatchedKeyword:    "secret-token",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO content_moderation_logs").
		WithArgs(
			log.RequestID,
			userID,
			log.UserEmail,
			apiKeyID,
			log.APIKeyName,
			groupID,
			log.GroupName,
			log.Endpoint,
			log.Provider,
			log.Model,
			log.Mode,
			log.Action,
			log.Flagged,
			log.HighestCategory,
			log.HighestScore,
			sqlmock.AnyArg(), // 分类分数快照
			sqlmock.AnyArg(), // 阈值快照
			log.InputExcerpt,
			latencyMS,
			log.Error,
			log.ViolationCount,
			log.AutoBanned,
			log.EmailSent,
			queueDelayMS,
			log.MatchedKeyword,
			log.Source,
			sqlmock.AnyArg(), // 完整输入单元
			log.ContentComplete,
			log.AuditComplete,
			log.TextUnitCount,
			log.ImageUnitCount,
			log.FailedUnitCount,
			sqlmock.AnyArg(), // 失败单元
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(99), createdAt))
	mock.ExpectCommit()

	err := repo.CreateLog(context.Background(), log)

	require.NoError(t, err)
	require.Equal(t, int64(99), log.ID)
	require.Equal(t, createdAt, log.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCreateLogPersistsReviewMedia(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &contentModerationRepository{db: db}
	createdAt := time.Now()
	log := &service.ContentModerationLog{
		Source:          service.ContentModerationSourceTool,
		ContentComplete: true,
		AuditComplete:   true,
		InputItems: []service.ContentModerationInputItem{{
			Index: 0, Source: service.ContentModerationSourceTool, Type: service.ContentModerationItemTypeImage, ImageRef: "data:image/png;base64,aW1hZ2U=",
		}},
		Media: []service.ContentModerationMedia{{
			SourceIndex: 0, Source: service.ContentModerationSourceTool, MIMEType: "image/png", SHA256: strings.Repeat("a", 64), ByteSize: 5,
			OriginalRef: "data:image/png;base64,aW1hZ2U=", SnapshotStatus: "ready", Content: []byte("image"),
		}},
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO content_moderation_logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(101), createdAt))
	mock.ExpectQuery("INSERT INTO content_moderation_media").
		WithArgs(int64(101), nil, 0, service.ContentModerationSourceTool, "image/png", strings.Repeat("a", 64), int64(5), log.Media[0].OriginalRef, "ready", "", []byte("image")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(201), createdAt))
	mock.ExpectCommit()

	require.NoError(t, repo.CreateLog(context.Background(), log))
	require.Equal(t, int64(201), log.Media[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCreateLogRollsBackWhenMediaInsertFails(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &contentModerationRepository{db: db}
	log := &service.ContentModerationLog{Media: []service.ContentModerationMedia{{OriginalRef: "data:image/png;base64,aW1hZ2U="}}}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO content_moderation_logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(101), time.Now()))
	mock.ExpectQuery("INSERT INTO content_moderation_media").
		WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()

	err := repo.CreateLog(context.Background(), log)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetLogReturnsFullReviewPayload(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &contentModerationRepository{db: db}
	createdAt := time.Now()
	inputItems := `[{
		"index":0,"source":"tool","type":"text","text":"complete output"
	}]`
	failedUnits := `[{"type":"image","index":1,"source_index":2,"error":"timeout"}]`
	mock.ExpectQuery("FROM content_moderation_logs l").WithArgs(int64(9)).WillReturnRows(
		sqlmock.NewRows([]string{
			"id", "request_id", "user_id", "user_email", "api_key_id", "api_key_name", "group_id", "group_name",
			"endpoint", "provider", "model", "mode", "action", "flagged", "highest_category", "highest_score",
			"category_scores", "threshold_snapshot", "input_excerpt", "upstream_latency_ms", "error",
			"violation_count", "auto_banned", "email_sent", "user_status", "queue_delay_ms", "matched_keyword",
			"source", "input_items", "content_complete", "audit_complete", "text_unit_count", "image_unit_count",
			"failed_unit_count", "failed_units", "created_at",
		}).AddRow(
			int64(9), "req", nil, "user@example.com", nil, "", nil, "", "/v1/responses", "openai", "gpt-5", "pre_block", "allow", false, "sexual", 0.1,
			`{"sexual":0.1}`, `{"sexual":0.65}`, "excerpt", 12, "timeout", 0, false, false, "active", 3, "",
			"tool", inputItems, true, false, 1, 1, 1, failedUnits, createdAt,
		),
	)
	mock.ExpectQuery("FROM content_moderation_media").WithArgs(int64(9)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "source_index", "source", "mime_type", "sha256", "byte_size", "original_ref", "snapshot_status", "snapshot_error", "created_at"}).
			AddRow(int64(88), 2, "tool", "image/png", strings.Repeat("b", 64), int64(5), "data:image/png;base64,aW1hZ2U=", "ready", "", createdAt),
	)

	item, err := repo.GetLog(context.Background(), 9)

	require.NoError(t, err)
	require.True(t, item.ContentComplete)
	require.False(t, item.AuditComplete)
	require.Equal(t, "complete output", item.InputItems[0].Text)
	require.Equal(t, "timeout", item.FailedUnits[0].Error)
	require.Len(t, item.Media, 1)
	require.Empty(t, item.Media[0].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetMediaContentReturnsBinary(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &contentModerationRepository{db: db}
	createdAt := time.Now()
	mock.ExpectQuery("FROM content_moderation_media").WithArgs(int64(88)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "source_index", "source", "mime_type", "sha256", "byte_size", "original_ref", "snapshot_status", "snapshot_error", "content", "created_at"}).
			AddRow(int64(88), 2, "tool", "image/png", strings.Repeat("b", 64), int64(5), "ref", "ready", "", []byte("image"), createdAt),
	)

	item, err := repo.GetMediaContent(context.Background(), 88)

	require.NoError(t, err)
	require.Equal(t, []byte("image"), item.Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesHashBlock(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND action <> 'hash_block'")).
		WithArgs(int64(1001), since).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since)

	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCreateCyberWarningAndApplyUserBan_DisablesUserAtomically(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &contentModerationRepository{db: db}
	userID := int64(1001)
	accountID := int64(2001)
	createdAt := time.Now()
	warning := &service.ContentModerationCyberWarning{
		RequestID:      "req_1",
		UserID:         &userID,
		UserEmail:      "user@example.com",
		AccountID:      &accountID,
		AccountName:    "openai-1",
		Endpoint:       "/v1/responses",
		Model:          "gpt-5.1",
		UpstreamStatus: 400,
		WarningText:    "This request may pose a cybersecurity risk.",
		PromptExcerpt:  "bad cyber prompt",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status FROM users WHERE id = $1 FOR UPDATE")).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(service.StatusActive))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO content_moderation_cyber_warnings")).
		WithArgs(
			warning.RequestID,
			userID,
			warning.UserEmail,
			nil,
			warning.APIKeyName,
			nil,
			warning.GroupName,
			accountID,
			warning.AccountName,
			warning.Endpoint,
			warning.Model,
			warning.UpstreamStatus,
			warning.WarningText,
			warning.PromptExcerpt,
			1,
			false,
			false,
			warning.Source,
			sqlmock.AnyArg(),
			warning.ContentComplete,
			warning.AuditComplete,
			warning.TextUnitCount,
			warning.ImageUnitCount,
			warning.FailedUnitCount,
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(99), createdAt))
	mock.ExpectQuery(regexp.QuoteMeta("WITH last_auto_ban AS")).
		WithArgs(userID, sqlmock.AnyArg(), int64(99)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE users")).
		WithArgs(userID, service.StatusDisabled).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE content_moderation_cyber_warnings")).
		WithArgs(int64(99), 3, true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	autoBanned, err := repo.CreateCyberWarningAndApplyUserBan(context.Background(), warning, service.ContentModerationCyberWarningPolicy{
		AutoBanEnabled: true,
		BanThreshold:   3,
		WindowHours:    720,
	})

	require.NoError(t, err)
	require.True(t, autoBanned)
	require.Equal(t, int64(99), warning.ID)
	require.Equal(t, createdAt, warning.CreatedAt)
	require.Equal(t, 3, warning.ViolationCount)
	require.True(t, warning.AutoBanned)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCreateCyberWarningAndApplyUserBan_KeepsBelowThreshold(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &contentModerationRepository{db: db}
	userID := int64(1001)
	warning := &service.ContentModerationCyberWarning{
		RequestID:      "req_1",
		UserID:         &userID,
		WarningText:    "flagged by OpenAI cyber policy",
		ViolationCount: 1,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status FROM users WHERE id = $1 FOR UPDATE")).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(service.StatusActive))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO content_moderation_cyber_warnings")).
		WithArgs(
			warning.RequestID,
			userID,
			warning.UserEmail,
			nil,
			warning.APIKeyName,
			nil,
			warning.GroupName,
			nil,
			warning.AccountName,
			warning.Endpoint,
			warning.Model,
			warning.UpstreamStatus,
			warning.WarningText,
			warning.PromptExcerpt,
			1,
			false,
			false,
			warning.Source,
			sqlmock.AnyArg(),
			warning.ContentComplete,
			warning.AuditComplete,
			warning.TextUnitCount,
			warning.ImageUnitCount,
			warning.FailedUnitCount,
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(100), time.Now()))
	mock.ExpectQuery(regexp.QuoteMeta("WITH last_auto_ban AS")).
		WithArgs(userID, sqlmock.AnyArg(), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE content_moderation_cyber_warnings")).
		WithArgs(int64(100), 2, false).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	autoBanned, err := repo.CreateCyberWarningAndApplyUserBan(context.Background(), warning, service.ContentModerationCyberWarningPolicy{
		AutoBanEnabled: true,
		BanThreshold:   3,
		WindowHours:    720,
	})

	require.NoError(t, err)
	require.False(t, autoBanned)
	require.Equal(t, 2, warning.ViolationCount)
	require.False(t, warning.AutoBanned)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryMarkCyberWarningEmailSent(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &contentModerationRepository{db: db}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE content_moderation_cyber_warnings")).
		WithArgs(int64(99)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.MarkCyberWarningEmailSent(context.Background(), 99))
	require.NoError(t, mock.ExpectationsWereMet())
}
