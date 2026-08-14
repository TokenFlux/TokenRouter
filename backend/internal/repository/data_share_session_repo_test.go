package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/ent/datasharesession"
	"github.com/BrandonVee/TokenRouter/ent/enttest"
	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newDataShareSessionRepoSQLite(t *testing.T) (*dataShareSessionRepository, *dbent.Client) {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	return &dataShareSessionRepository{client: client, sql: db}, client
}

func TestDataShareSessionRepository_RequestPathFilter(t *testing.T) {
	repo, _ := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
		TrajectoryID:       "traj-responses",
		SessionID:          "sess-responses",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformOpenAI,
		Model:              "gpt-5.5",
		RequestPath:        "/v1/responses",
		UserAgent:          "codex-cli/1.0",
		Status:             service.DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		Tools:              []map[string]any{},
		Messages:           []map[string]any{},
		Usage:              map[string]any{},
		Meta:               map[string]any{"request_path": "/v1/responses", "user_agent": "codex-cli/1.0"},
		SessionJSON:        map[string]any{"request_path": "/v1/responses", "user_agent": "codex-cli/1.0"},
		QualityStatus:      service.DataShareQualityInvalid,
		QualityErrors:      []string{},
		StorageBytes:       100,
		TotalTokens:        10,
		UserID:             0,
		APIKeyID:           0,
		GroupID:            0,
		CreatedAt:          now,
		EndedAt:            &now,
		UpdatedAt:          now,
	}))
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
		TrajectoryID:       "traj-chat",
		SessionID:          "sess-chat",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformOpenAI,
		Model:              "gpt-5.5",
		RequestPath:        "/v1/chat/completions",
		UserAgent:          "claude-code/2.0",
		Status:             service.DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		Tools:              []map[string]any{},
		Messages:           []map[string]any{},
		Usage:              map[string]any{},
		Meta:               map[string]any{"request_path": "/v1/chat/completions", "user_agent": "claude-code/2.0"},
		SessionJSON:        map[string]any{"request_path": "/v1/chat/completions", "user_agent": "claude-code/2.0"},
		QualityStatus:      service.DataShareQualityInvalid,
		QualityErrors:      []string{},
		StorageBytes:       200,
		TotalTokens:        20,
		UserID:             0,
		APIKeyID:           0,
		GroupID:            0,
		CreatedAt:          now,
		EndedAt:            &now,
		UpdatedAt:          now,
	}))

	q := applyDataShareFilters(repo.client.DataShareSession.Query(), service.DataShareSessionFilters{RequestPath: "/v1/chat/completions"})
	total, err := q.Clone().Count(ctx)
	require.NoError(t, err)
	items, err := q.All(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, "/v1/chat/completions", items[0].RequestPath)

	searchQ := applyDataShareFilters(repo.client.DataShareSession.Query(), service.DataShareSessionFilters{Search: "responses"})
	searchItems, err := searchQ.All(ctx)
	require.NoError(t, err)
	require.Len(t, searchItems, 1)
	require.Equal(t, "/v1/responses", searchItems[0].RequestPath)

	uaQ := applyDataShareFilters(repo.client.DataShareSession.Query(), service.DataShareSessionFilters{UserAgent: "claude-code/2.0"})
	uaItems, err := uaQ.All(ctx)
	require.NoError(t, err)
	require.Len(t, uaItems, 1)
	require.Equal(t, "claude-code/2.0", uaItems[0].UserAgent)

	modelQ := applyDataShareFilters(repo.client.DataShareSession.Query(), service.DataShareSessionFilters{Model: "gpt-5.5"})
	modelItems, err := modelQ.All(ctx)
	require.NoError(t, err)
	require.Len(t, modelItems, 2)
}

func TestDataShareSessionRepository_NonInvalidQualityFilter(t *testing.T) {
	repo, _ := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for _, item := range []struct {
		traj    string
		quality string
	}{
		{traj: "traj-complete", quality: service.DataShareQualityComplete},
		{traj: "traj-partial", quality: service.DataShareQualityPartial},
		{traj: "traj-invalid", quality: service.DataShareQualityInvalid},
	} {
		require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
			TrajectoryID:       item.traj,
			SessionID:          "sess-" + item.traj,
			Dataset:            "tokenrouter-agent",
			Provider:           service.PlatformOpenAI,
			Model:              "gpt-5.5",
			RequestPath:        "/v1/responses",
			UserAgent:          "codex-cli/1.0",
			Status:             service.DataShareStatusCompleted,
			IsFinalSnapshot:    true,
			SourceRequestCount: 1,
			Tools:              []map[string]any{},
			Messages:           []map[string]any{},
			Usage:              map[string]any{},
			Meta:               map[string]any{},
			SessionJSON:        map[string]any{},
			QualityStatus:      item.quality,
			QualityErrors:      []string{},
			StorageBytes:       100,
			TotalTokens:        10,
			CreatedAt:          now,
			EndedAt:            &now,
			UpdatedAt:          now,
		}))
	}

	items, page, err := repo.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.DataShareSessionFilters{
		QualityStatus: service.DataShareQualityFilterNonInvalid,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Len(t, items, 2)
	qualities := make([]string, 0, len(items))
	for _, item := range items {
		qualities = append(qualities, item.QualityStatus)
		require.NotEqual(t, service.DataShareQualityInvalid, item.QualityStatus)
	}
	sort.Strings(qualities)
	require.Equal(t, []string{service.DataShareQualityComplete, service.DataShareQualityPartial}, qualities)
}

func TestDataShareStatsWhereNonInvalidQualityFilter(t *testing.T) {
	whereSQL, args := dataShareStatsWhere(service.DataShareSessionFilters{
		Model:         "gpt-5.5",
		QualityStatus: service.DataShareQualityFilterNonInvalid,
	})

	require.Contains(t, whereSQL, "model = $1")
	require.Contains(t, whereSQL, "quality_status IN ($2, $3)")
	require.Equal(t, []any{"gpt-5.5", service.DataShareQualityComplete, service.DataShareQualityPartial}, args)
}

func TestDataShareSessionRepository_RequestPathStats(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &dataShareSessionRepository{sql: db}
	ctx := context.Background()

	mock.ExpectQuery(`SELECT\s+COUNT\(\*\),\s+COUNT\(\*\) FILTER \(WHERE exportable = TRUE\)`).
		WillReturnRows(sqlmock.NewRows([]string{
			"count",
			"exportable",
			"non_exportable",
			"complete",
			"partial",
			"invalid",
			"storage",
			"tokens",
			"total_actual_cost",
			"avg_actual_cost_per_session",
		}).AddRow(int64(3), int64(1), int64(2), int64(1), int64(1), int64(1), int64(300), int64(30), 2.0, 1.0))
	mock.ExpectQuery(`SELECT to_char\(date_trunc\('day', created_at\), 'YYYY-MM-DD'\) AS day`).
		WillReturnRows(sqlmock.NewRows([]string{"day", "storage_bytes", "session_count"}).
			AddRow("2026-05-27", int64(300), int64(2)))
	mock.ExpectQuery(`LEFT JOIN groups g ON g.id = d.group_id`).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "group_name", "storage_bytes", "session_count"}).
			AddRow(int64(3), "共享分组", int64(300), int64(2)))
	mock.ExpectQuery(`SELECT COALESCE\(NULLIF\(request_path, ''\), '\(unknown\)'\) AS request_path`).
		WillReturnRows(sqlmock.NewRows([]string{"request_path", "storage_bytes", "session_count", "total_tokens"}).
			AddRow("/v1/responses", int64(100), int64(1), int64(10)).
			AddRow("/v1/chat/completions", int64(200), int64(1), int64(20)))
	mock.ExpectQuery(`SELECT COALESCE\(NULLIF\(model, ''\), '\(unknown\)'\) AS model`).
		WillReturnRows(sqlmock.NewRows([]string{"model", "storage_bytes", "session_count", "total_tokens"}).
			AddRow("gpt-5.5", int64(300), int64(2), int64(30)))
	mock.ExpectQuery(`SELECT COALESCE\(NULLIF\(user_agent, ''\), '\(unknown\)'\) AS user_agent`).
		WillReturnRows(sqlmock.NewRows([]string{"user_agent", "storage_bytes", "session_count", "total_tokens"}).
			AddRow("codex-cli/1.0", int64(100), int64(1), int64(10)).
			AddRow("claude-code/2.0", int64(200), int64(1), int64(20)))
	mock.ExpectQuery(`CROSS JOIN LATERAL jsonb_array_elements_text\(.+jsonb_typeof\(d\.quality_errors\) = 'array'.+jsonb_typeof\(d\.quality_errors\) = 'string'.+jsonb_build_array`).
		WillReturnRows(sqlmock.NewRows([]string{"error_code", "session_count"}).
			AddRow("missing_structured_tool_call", int64(2)).
			AddRow("tool_call_result_unpaired", int64(1)))
	mock.ExpectQuery(`LEFT JOIN users u ON u\.id = d\.user_id`).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "user_name", "user_email", "session_count", "invalid_count", "storage_bytes", "total_tokens"}).
			AddRow(int64(7), "alice", "alice@example.com", int64(3), int64(2), int64(240), int64(24)).
			AddRow(int64(8), "bob", "bob@example.com", int64(1), int64(1), int64(60), int64(6)))

	stats, err := repo.Stats(ctx, service.DataShareSessionFilters{})
	require.NoError(t, err)
	require.Equal(t, int64(3), stats.SessionCount)
	require.Equal(t, float64(10), stats.AvgTokensPerSession)
	require.InDelta(t, 2.0, stats.TotalActualCost, 1e-12)
	require.InDelta(t, 1.0, stats.AvgActualCostPerSession, 1e-12)
	require.Len(t, stats.RequestPathBreakdown, 2)
	require.Equal(t, "/v1/responses", stats.RequestPathBreakdown[0].RequestPath)
	require.Equal(t, int64(100), stats.RequestPathBreakdown[0].StorageBytes)
	require.Equal(t, int64(10), stats.RequestPathBreakdown[0].TotalTokens)
	require.Len(t, stats.ModelBreakdown, 1)
	require.Equal(t, "gpt-5.5", stats.ModelBreakdown[0].Model)
	require.Len(t, stats.UserAgentBreakdown, 2)
	require.Equal(t, "codex-cli/1.0", stats.UserAgentBreakdown[0].UserAgent)
	require.Len(t, stats.QualityErrorBreakdown, 2)
	require.Equal(t, "missing_structured_tool_call", stats.QualityErrorBreakdown[0].ErrorCode)
	require.Equal(t, int64(2), stats.QualityErrorBreakdown[0].SessionCount)
	require.Len(t, stats.InvalidUserBreakdown, 2)
	require.Equal(t, int64(7), stats.InvalidUserBreakdown[0].UserID)
	require.Equal(t, "alice", stats.InvalidUserBreakdown[0].UserName)
	require.Equal(t, int64(2), stats.InvalidUserBreakdown[0].InvalidCount)
	require.InDelta(t, 2.0/3.0, stats.InvalidUserBreakdown[0].InvalidRatio, 1e-12)
	require.Equal(t, int64(240), stats.InvalidUserBreakdown[0].StorageBytes)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDataShareSessionRepository_InvalidUserBreakdownIgnoresQualityFilter(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &dataShareSessionRepository{sql: db}
	ctx := context.Background()
	filters := service.DataShareSessionFilters{
		Model:         "gpt-5.5",
		QualityStatus: service.DataShareQualityComplete,
	}

	mock.ExpectQuery(`SELECT\s+COUNT\(\*\),\s+COUNT\(\*\) FILTER \(WHERE exportable = TRUE\)`).
		WithArgs("gpt-5.5", service.DataShareQualityComplete).
		WillReturnRows(sqlmock.NewRows([]string{
			"count",
			"exportable",
			"non_exportable",
			"complete",
			"partial",
			"invalid",
			"storage",
			"tokens",
			"total_actual_cost",
			"avg_actual_cost_per_session",
		}).AddRow(int64(1), int64(1), int64(0), int64(1), int64(0), int64(0), int64(100), int64(10), 1.0, 1.0))
	mock.ExpectQuery(`SELECT to_char\(date_trunc\('day', created_at\), 'YYYY-MM-DD'\) AS day`).
		WithArgs("gpt-5.5", service.DataShareQualityComplete).
		WillReturnRows(sqlmock.NewRows([]string{"day", "storage_bytes", "session_count"}))
	mock.ExpectQuery(`LEFT JOIN groups g ON g.id = d.group_id`).
		WithArgs("gpt-5.5", service.DataShareQualityComplete).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "group_name", "storage_bytes", "session_count"}))
	mock.ExpectQuery(`SELECT COALESCE\(NULLIF\(request_path, ''\), '\(unknown\)'\) AS request_path`).
		WithArgs("gpt-5.5", service.DataShareQualityComplete).
		WillReturnRows(sqlmock.NewRows([]string{"request_path", "storage_bytes", "session_count", "total_tokens"}))
	mock.ExpectQuery(`SELECT COALESCE\(NULLIF\(model, ''\), '\(unknown\)'\) AS model`).
		WithArgs("gpt-5.5", service.DataShareQualityComplete).
		WillReturnRows(sqlmock.NewRows([]string{"model", "storage_bytes", "session_count", "total_tokens"}))
	mock.ExpectQuery(`SELECT COALESCE\(NULLIF\(user_agent, ''\), '\(unknown\)'\) AS user_agent`).
		WithArgs("gpt-5.5", service.DataShareQualityComplete).
		WillReturnRows(sqlmock.NewRows([]string{"user_agent", "storage_bytes", "session_count", "total_tokens"}))
	mock.ExpectQuery(`CROSS JOIN LATERAL jsonb_array_elements_text\(.+jsonb_typeof\(d\.quality_errors\) = 'array'.+jsonb_typeof\(d\.quality_errors\) = 'string'.+jsonb_build_array`).
		WithArgs("gpt-5.5", service.DataShareQualityComplete).
		WillReturnRows(sqlmock.NewRows([]string{"error_code", "session_count"}))
	mock.ExpectQuery(`LEFT JOIN users u ON u\.id = d\.user_id`).
		WithArgs("gpt-5.5").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "user_name", "user_email", "session_count", "invalid_count", "storage_bytes", "total_tokens"}).
			AddRow(int64(7), "alice", "alice@example.com", int64(4), int64(3), int64(300), int64(30)))

	stats, err := repo.Stats(ctx, filters)
	require.NoError(t, err)
	require.Len(t, stats.InvalidUserBreakdown, 1)
	require.Equal(t, int64(7), stats.InvalidUserBreakdown[0].UserID)
	require.Equal(t, int64(4), stats.InvalidUserBreakdown[0].SessionCount)
	require.Equal(t, int64(3), stats.InvalidUserBreakdown[0].InvalidCount)
	require.InDelta(t, 0.75, stats.InvalidUserBreakdown[0].InvalidRatio, 1e-12)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDataShareSessionRepository_ActualCostPersistsNullAndZeroDistinctly(t *testing.T) {
	repo, _ := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()
	zeroCost := 0.0

	baseSession := func(trajectoryID string, actualCost *float64) *service.DataShareSession {
		return &service.DataShareSession{
			TrajectoryID:       trajectoryID,
			SessionID:          trajectoryID + "-session",
			Dataset:            "tokenrouter-agent",
			Provider:           service.PlatformOpenAI,
			Model:              "gpt-5.5",
			RequestPath:        "/v1/responses",
			UserAgent:          "codex-cli/1.0",
			Status:             service.DataShareStatusCompleted,
			IsFinalSnapshot:    true,
			SourceRequestCount: 1,
			Tools:              []map[string]any{},
			Messages:           []map[string]any{},
			Usage:              map[string]any{},
			Meta:               map[string]any{},
			SessionJSON:        map[string]any{},
			QualityStatus:      service.DataShareQualityInvalid,
			QualityErrors:      []string{},
			ActualCost:         actualCost,
			CreatedAt:          now,
			EndedAt:            &now,
			UpdatedAt:          now,
		}
	}

	require.NoError(t, repo.SaveCaptureSnapshot(ctx, baseSession("traj-actual-cost-null", nil)))
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, baseSession("traj-actual-cost-zero", &zeroCost)))

	unknownCost, err := repo.GetCaptureByTrajectoryIDWithPayload(ctx, "traj-actual-cost-null")
	require.NoError(t, err)
	require.Nil(t, unknownCost.ActualCost)

	knownZeroCost, err := repo.GetCaptureByTrajectoryIDWithPayload(ctx, "traj-actual-cost-zero")
	require.NoError(t, err)
	require.NotNil(t, knownZeroCost.ActualCost)
	require.Zero(t, *knownZeroCost.ActualCost)
}

func TestDataShareSessionRepository_RequestPathBreakdownLoader(t *testing.T) {
	repo, _ := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()

	now := time.Now().UTC()
	storageByPath := map[string]int64{}
	storageByUserAgent := map[string]int64{}
	totalStorage := int64(0)
	for _, item := range []struct {
		traj    string
		session string
		path    string
		ua      string
		tokens  int64
	}{
		{traj: "traj-responses", session: "sess-responses", path: "/v1/responses", ua: "codex-cli/1.0", tokens: 10},
		{traj: "traj-chat", session: "sess-chat", path: "/v1/chat/completions", ua: "claude-code/2.0", tokens: 20},
	} {
		require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
			TrajectoryID:       item.traj,
			SessionID:          item.session,
			Dataset:            "tokenrouter-agent",
			Provider:           service.PlatformOpenAI,
			Model:              "gpt-5.5",
			RequestPath:        item.path,
			UserAgent:          item.ua,
			Status:             service.DataShareStatusCompleted,
			IsFinalSnapshot:    true,
			SourceRequestCount: 1,
			Tools:              []map[string]any{},
			Messages:           []map[string]any{},
			Usage:              map[string]any{},
			Meta:               map[string]any{"request_path": item.path, "user_agent": item.ua},
			SessionJSON:        map[string]any{"request_path": item.path, "user_agent": item.ua},
			QualityStatus:      service.DataShareQualityInvalid,
			QualityErrors:      []string{},
			TotalTokens:        item.tokens,
			UserID:             1,
			APIKeyID:           2,
			GroupID:            3,
			CreatedAt:          now,
			EndedAt:            &now,
			UpdatedAt:          now,
		}))
		stored, err := repo.client.DataShareSession.Query().Where(datasharesession.TrajectoryIDEQ(item.traj)).Only(ctx)
		require.NoError(t, err)
		storageByPath[item.path] = stored.StorageBytes
		storageByUserAgent[item.ua] = stored.StorageBytes
		totalStorage += stored.StorageBytes
	}

	points, err := repo.loadRequestPathBreakdown(ctx, repo.sql, "", nil)
	require.NoError(t, err)
	sort.Slice(points, func(i, j int) bool { return points[i].RequestPath < points[j].RequestPath })
	require.Len(t, points, 2)
	require.Equal(t, "/v1/chat/completions", points[0].RequestPath)
	require.Equal(t, storageByPath["/v1/chat/completions"], points[0].StorageBytes)
	require.Equal(t, int64(20), points[0].TotalTokens)
	require.Equal(t, "/v1/responses", points[1].RequestPath)
	require.Equal(t, storageByPath["/v1/responses"], points[1].StorageBytes)
	require.Equal(t, int64(10), points[1].TotalTokens)

	modelPoints, err := repo.loadModelBreakdown(ctx, repo.sql, "", nil)
	require.NoError(t, err)
	require.Len(t, modelPoints, 1)
	require.Equal(t, "gpt-5.5", modelPoints[0].Model)
	require.Equal(t, totalStorage, modelPoints[0].StorageBytes)
	require.Equal(t, int64(30), modelPoints[0].TotalTokens)

	uaPoints, err := repo.loadUserAgentBreakdown(ctx, repo.sql, "", nil)
	require.NoError(t, err)
	sort.Slice(uaPoints, func(i, j int) bool { return uaPoints[i].UserAgent < uaPoints[j].UserAgent })
	require.Len(t, uaPoints, 2)
	require.Equal(t, "claude-code/2.0", uaPoints[0].UserAgent)
	require.Equal(t, storageByUserAgent["claude-code/2.0"], uaPoints[0].StorageBytes)
	require.Equal(t, int64(20), uaPoints[0].TotalTokens)
	require.Equal(t, "codex-cli/1.0", uaPoints[1].UserAgent)
	require.Equal(t, storageByUserAgent["codex-cli/1.0"], uaPoints[1].StorageBytes)
	require.Equal(t, int64(10), uaPoints[1].TotalTokens)
}

func TestDataShareSessionRepository_FilterOptionsIndependentFromQuality(t *testing.T) {
	repo, _ := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sessions := []struct {
		traj    string
		path    string
		model   string
		ua      string
		quality string
		userID  int64
	}{
		{traj: "traj-invalid", path: "/v1/responses", model: "gpt-5.5", ua: "opencode", quality: service.DataShareQualityInvalid, userID: 7},
		{traj: "traj-complete", path: "/v1/messages", model: "gpt-5.4", ua: "claude-cli", quality: service.DataShareQualityComplete, userID: 7},
		{traj: "traj-other-user", path: "/v1/chat/completions", model: "other-model", ua: "other-ua", quality: service.DataShareQualityComplete, userID: 8},
	}
	for _, item := range sessions {
		require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
			TrajectoryID:       item.traj,
			SessionID:          item.traj,
			Dataset:            "tokenrouter-agent",
			Provider:           service.PlatformOpenAI,
			Model:              item.model,
			RequestPath:        item.path,
			UserAgent:          item.ua,
			Status:             service.DataShareStatusCompleted,
			IsFinalSnapshot:    true,
			SourceRequestCount: 1,
			Tools:              []map[string]any{},
			Messages:           []map[string]any{},
			Usage:              map[string]any{},
			Meta:               map[string]any{"request_path": item.path, "user_agent": item.ua},
			SessionJSON:        map[string]any{"request_path": item.path, "user_agent": item.ua},
			QualityStatus:      item.quality,
			QualityErrors:      []string{},
			TotalTokens:        1,
			UserID:             item.userID,
			CreatedAt:          now,
			EndedAt:            &now,
			UpdatedAt:          now,
		}))
	}

	options, err := repo.FilterOptions(ctx, service.DataShareSessionFilters{UserID: 7})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.4", "gpt-5.5"}, options.Models)
	require.Equal(t, []string{"/v1/messages", "/v1/responses"}, options.RequestPaths)
	require.Equal(t, []string{"claude-cli", "opencode"}, options.UserAgents)
}

func TestDataShareSessionRepository_CompressesPayloadAndOmitsListPayload(t *testing.T) {
	repo, client := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()
	systemPrompt := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": strings.Repeat("请分析这个文件。", 20)},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": strings.Repeat("README.md\n", 40), "status": "success", "is_error": false},
		{"role": "assistant", "content": "分析完成。"},
	}
	tools := []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}}
	sessionJSON := map[string]any{
		"trajectory_id":        "traj-compress",
		"session_id":           "sess-compress",
		"dataset":              "tokenrouter-agent",
		"provider":             service.PlatformOpenAI,
		"model":                "gpt-5.5",
		"request_path":         "/v1/responses",
		"user_agent":           "codex-cli",
		"created_at":           now.Format(time.RFC3339Nano),
		"ended_at":             now.Format(time.RFC3339Nano),
		"status":               service.DataShareStatusCompleted,
		"is_final_snapshot":    true,
		"source_request_count": 1,
		"system_prompt":        systemPrompt,
		"tools":                tools,
		"messages":             messages,
		"usage":                map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		"meta":                 map[string]any{"request_path": "/v1/responses", "user_agent": "codex-cli/1.0"},
	}

	require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
		TrajectoryID:       "traj-compress",
		SessionID:          "sess-compress",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformOpenAI,
		Model:              "gpt-5.5",
		RequestPath:        "/v1/responses",
		UserAgent:          "codex-cli",
		Status:             service.DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		SystemPrompt:       &systemPrompt,
		Tools:              tools,
		Messages:           messages,
		Usage:              map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		Meta:               map[string]any{"request_path": "/v1/responses", "user_agent": "codex-cli/1.0"},
		SessionJSON:        sessionJSON,
		QualityStatus:      service.DataShareQualityComplete,
		QualityErrors:      []string{},
		StorageBytes:       int64(len(mustRepositoryJSON(sessionJSON))),
		InputTokens:        10,
		OutputTokens:       5,
		TotalTokens:        15,
		UserID:             0,
		APIKeyID:           0,
		GroupID:            0,
		CreatedAt:          now,
		EndedAt:            &now,
		UpdatedAt:          now,
	}))

	stored, err := client.DataShareSession.Query().Where(datasharesession.TrajectoryIDEQ("traj-compress")).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, stored.PayloadCompressed)
	require.NotEmpty(t, *stored.PayloadCompressed)
	require.Equal(t, dataSharePayloadEncodingZstd, stored.PayloadEncoding)
	require.Greater(t, stored.PayloadBytes, int64(0))
	require.Equal(t, int64(len(*stored.PayloadCompressed)), stored.StorageBytes)
	require.Less(t, stored.StorageBytes, stored.PayloadBytes)
	require.Empty(t, stored.Messages)
	require.Empty(t, stored.Tools)
	require.Empty(t, stored.Usage)
	require.Empty(t, stored.Meta)
	require.Empty(t, stored.SessionJSON)
	require.Nil(t, stored.SystemPrompt)

	listItems, _, err := repo.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.DataShareSessionFilters{})
	require.NoError(t, err)
	require.Len(t, listItems, 1)
	require.Empty(t, listItems[0].PayloadCompressed)
	require.Empty(t, listItems[0].Messages)
	require.Empty(t, listItems[0].SessionJSON)

	detail, err := repo.GetByID(ctx, stored.ID)
	require.NoError(t, err)
	require.Len(t, detail.Messages, len(messages))
	require.Equal(t, "system", detail.Messages[0]["role"])
	require.Equal(t, "tool_calls", detail.Messages[2]["finish_reason"])
	require.Equal(t, "tool", detail.Messages[3]["role"])
	require.Equal(t, "分析完成。", detail.Messages[4]["content"])
	require.Equal(t, tools, detail.Tools)
	require.Equal(t, systemPrompt, *detail.SystemPrompt)
	require.Equal(t, "/v1/responses", detail.SessionJSON["request_path"])

	payloadItems, _, err := repo.ListWithPayload(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.DataShareSessionFilters{})
	require.NoError(t, err)
	require.Len(t, payloadItems, 1)
	require.Len(t, payloadItems[0].Messages, len(messages))
	require.Equal(t, tools, payloadItems[0].Tools)
}

func TestDataShareSessionRepository_ListExportPayloadPageUsesCreatedAtIDCursor(t *testing.T) {
	repo, _ := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 1; i <= 51; i++ {
		require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
			TrajectoryID:       fmt.Sprintf("traj-export-cursor-%d", i),
			SessionID:          fmt.Sprintf("sess-export-cursor-%d", i),
			Dataset:            "tokenrouter-agent",
			Provider:           service.PlatformOpenAI,
			Model:              "gpt-5.5",
			RequestPath:        "/v1/responses",
			UserAgent:          "codex-cli",
			Status:             service.DataShareStatusCompleted,
			IsFinalSnapshot:    true,
			SourceRequestCount: 1,
			Tools:              []map[string]any{},
			Messages:           []map[string]any{{"role": "user", "content": fmt.Sprintf("hello-%d", i)}},
			Usage:              map[string]any{},
			Meta:               map[string]any{"request_path": "/v1/responses"},
			SessionJSON:        map[string]any{"messages": []map[string]any{{"role": "user", "content": fmt.Sprintf("hello-%d", i)}}},
			QualityStatus:      service.DataShareQualityComplete,
			QualityErrors:      []string{},
			UserID:             0,
			APIKeyID:           0,
			GroupID:            0,
			CreatedAt:          now,
			EndedAt:            &now,
			UpdatedAt:          now,
		}))
	}

	first, cursor, err := repo.ListExportPayloadPage(ctx, service.DataShareSessionFilters{}, nil, 50, 1, nil)
	require.NoError(t, err)
	require.Len(t, first, 50)
	require.NotNil(t, cursor)
	require.Equal(t, "traj-export-cursor-1", first[0].TrajectoryID)
	require.Equal(t, "traj-export-cursor-50", first[49].TrajectoryID)
	require.NotEmpty(t, first[0].Messages)

	second, nextCursor, err := repo.ListExportPayloadPage(ctx, service.DataShareSessionFilters{}, cursor, 50, 1, nil)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.NotNil(t, nextCursor)
	require.Equal(t, "traj-export-cursor-51", second[0].TrajectoryID)

	empty, finalCursor, err := repo.ListExportPayloadPage(ctx, service.DataShareSessionFilters{}, nextCursor, 50, 1, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
	require.Nil(t, finalCursor)
}

func TestDataShareSessionRepository_SaveCaptureSnapshotReplacesWithoutMerging(t *testing.T) {
	repo, client := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()
	systemPrompt := "你是编码助手"
	tools := []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}}
	firstMessages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": "列目录"},
	}
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
		TrajectoryID:       "traj-legacy-partial",
		SessionID:          "sess-legacy-partial",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformAnthropic,
		Model:              "claude-sonnet-4.5",
		RequestPath:        "/v1/messages",
		UserAgent:          "claude-code",
		Status:             service.DataShareStatusTerminated,
		IsFinalSnapshot:    false,
		SourceRequestCount: 1,
		SystemPrompt:       &systemPrompt,
		Tools:              tools,
		Messages:           firstMessages,
		Usage:              map[string]any{"total_tokens": 15},
		Meta:               map[string]any{"request_path": "/v1/messages"},
		SessionJSON:        map[string]any{"messages": firstMessages},
		QualityStatus:      service.DataShareQualityInvalid,
		QualityErrors:      []string{},
		TotalTokens:        15,
		CreatedAt:          now,
		EndedAt:            &now,
		UpdatedAt:          now,
	}))
	secondMessages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": "列目录"},
		{"role": "assistant", "content": "看到了 README.md"},
	}
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, &service.DataShareSession{
		TrajectoryID:       "traj-legacy-partial",
		SessionID:          "sess-legacy-partial",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformAnthropic,
		Model:              "claude-sonnet-4.5",
		RequestPath:        "/v1/messages",
		UserAgent:          "claude-code",
		Status:             service.DataShareStatusTerminated,
		IsFinalSnapshot:    false,
		SourceRequestCount: 2,
		SystemPrompt:       &systemPrompt,
		Tools:              tools,
		Messages:           secondMessages,
		Usage:              map[string]any{"total_tokens": 30},
		Meta:               map[string]any{"request_path": "/v1/messages"},
		SessionJSON:        map[string]any{"messages": secondMessages},
		Exportable:         true,
		QualityStatus:      service.DataShareQualityPartial,
		QualityErrors:      []string{},
		TotalTokens:        30,
		CreatedAt:          now,
		EndedAt:            &now,
		UpdatedAt:          now,
	}))

	stored, err := client.DataShareSession.Query().Where(datasharesession.TrajectoryIDEQ("traj-legacy-partial")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stored.SourceRequestCount)
	require.Equal(t, int64(30), stored.TotalTokens)
	require.True(t, stored.Exportable)
	require.Equal(t, service.DataShareQualityPartial, stored.QualityStatus)
	hydrated, err := repo.GetCaptureByTrajectoryIDWithPayload(ctx, "traj-legacy-partial")
	require.NoError(t, err)
	require.Len(t, hydrated.Messages, len(secondMessages))
}

func TestDataShareSessionRepository_PersistReusesOnlyCompleteSessionJSON(t *testing.T) {
	repo, client := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()
	systemPrompt := "你是编码助手"
	tools := []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}}
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": "列目录"},
	}
	base := &service.DataShareSession{
		TrajectoryID:       "traj-payload-reuse",
		SessionID:          "sess-payload-reuse",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformOpenAI,
		Model:              "gpt-5.5",
		RequestPath:        "/v1/responses",
		UserAgent:          "codex-cli",
		Status:             service.DataShareStatusTerminated,
		IsFinalSnapshot:    false,
		SourceRequestCount: 1,
		SystemPrompt:       &systemPrompt,
		Tools:              tools,
		Messages:           messages,
		Usage:              map[string]any{"total_tokens": 1},
		Meta:               map[string]any{"request_path": "/v1/responses"},
		SessionJSON:        map[string]any{"messages": messages, "sentinel": "partial"},
		QualityStatus:      service.DataShareQualityInvalid,
		QualityErrors:      []string{},
		TotalTokens:        1,
		CreatedAt:          now,
		EndedAt:            &now,
		UpdatedAt:          now,
	}
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, base))
	stored, err := client.DataShareSession.Query().Where(datasharesession.TrajectoryIDEQ(base.TrajectoryID)).Only(ctx)
	require.NoError(t, err)
	payload, err := decodeDataSharePayload(*stored.PayloadCompressed, stored.PayloadEncoding)
	require.NoError(t, err)
	require.Equal(t, "partial", payload["sentinel"])
	require.Equal(t, base.TrajectoryID, payload["trajectory_id"])
	require.Equal(t, systemPrompt, payload["system_prompt"])
	require.Equal(t, "/v1/responses", payload["request_path"])
	require.Equal(t, tools, mapsFromRepositoryAny(payload["tools"]))

	base.SourceRequestCount = 2
	completePayload := service.BuildFinalizedDataShareSessionPayload(&service.DataShareSession{
		TrajectoryID:       base.TrajectoryID,
		SessionID:          base.SessionID,
		Dataset:            base.Dataset,
		Provider:           base.Provider,
		Model:              base.Model,
		RequestPath:        base.RequestPath,
		UserAgent:          base.UserAgent,
		Status:             base.Status,
		IsFinalSnapshot:    base.IsFinalSnapshot,
		SourceRequestCount: base.SourceRequestCount,
		SystemPrompt:       base.SystemPrompt,
		Tools:              base.Tools,
		Messages:           base.Messages,
		Usage:              base.Usage,
		Meta:               base.Meta,
		CreatedAt:          base.CreatedAt,
		EndedAt:            base.EndedAt,
		UpdatedAt:          base.UpdatedAt,
	})
	completePayload["sentinel"] = "complete"
	base.SessionJSON = completePayload
	base.SessionJSONFinalized = true
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, base))
	stored, err = client.DataShareSession.Query().Where(datasharesession.TrajectoryIDEQ(base.TrajectoryID)).Only(ctx)
	require.NoError(t, err)
	payload, err = decodeDataSharePayload(*stored.PayloadCompressed, stored.PayloadEncoding)
	require.NoError(t, err)
	require.Equal(t, "complete", payload["sentinel"])
	require.Equal(t, float64(2), payload["source_request_count"])
}

func TestDataShareSessionRepository_StorageLimitSkipsNewSessionAndOversizedSnapshot(t *testing.T) {
	repo, client := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()

	session := func(trajectoryID string, content string) *service.DataShareSession {
		return &service.DataShareSession{
			TrajectoryID:       trajectoryID,
			SessionID:          trajectoryID,
			Dataset:            "tokenrouter-agent",
			Provider:           service.PlatformOpenAI,
			Model:              "gpt-5.5",
			RequestPath:        "/v1/responses",
			UserAgent:          "codex-cli",
			Status:             service.DataShareStatusTerminated,
			IsFinalSnapshot:    false,
			SourceRequestCount: 1,
			Tools:              []map[string]any{},
			Messages:           []map[string]any{{"role": "user", "content": content}},
			Usage:              map[string]any{"total_tokens": 1},
			Meta:               map[string]any{"request_path": "/v1/responses"},
			SessionJSON:        map[string]any{"messages": []map[string]any{{"role": "user", "content": content}}},
			QualityStatus:      service.DataShareQualityInvalid,
			QualityErrors:      []string{},
			TotalTokens:        1,
			CreatedAt:          now,
			EndedAt:            &now,
			UpdatedAt:          now,
		}
	}

	require.NoError(t, repo.SaveCaptureSnapshot(ctx, session("traj-limit-1", strings.Repeat("a", 64))))
	total, err := repo.TotalStorageBytes(ctx)
	require.NoError(t, err)
	require.Greater(t, total, int64(0))

	// 新 session 超过阈值时直接跳过采集，避免继续扩大数据共享表空间。
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, session("traj-limit-2", strings.Repeat("b", 64)), service.DataShareUpsertOptions{StorageLimitBytes: total}))
	count, err := client.DataShareSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// 已有 session 的小快照在阈值仍有余量时允许，避免正常任务无法闭合。
	next := session("traj-limit-1", strings.Repeat("c", 64))
	next.SourceRequestCount = 2
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, next, service.DataShareUpsertOptions{StorageLimitBytes: total + 4096}))
	stored, err := client.DataShareSession.Query().Where(datasharesession.TrajectoryIDEQ("traj-limit-1")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stored.SourceRequestCount)

	currentTotal, err := repo.TotalStorageBytes(ctx)
	require.NoError(t, err)
	require.Greater(t, currentTotal, int64(0))

	// 已有 session 的大快照超过阈值时也会跳过，避免单 session 持续追加打爆磁盘。
	limit := currentTotal + 8
	oversized := session("traj-limit-1", deterministicDataShareTestContent(4096))
	oversized.SourceRequestCount = 3
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, oversized, service.DataShareUpsertOptions{StorageLimitBytes: limit}))
	stored, err = client.DataShareSession.Query().Where(datasharesession.TrajectoryIDEQ("traj-limit-1")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stored.SourceRequestCount)
}

func TestDataShareSessionRepository_SaveCaptureSnapshotRecordsDurations(t *testing.T) {
	repo, client := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	seen := map[service.DataShareCaptureDurationPartKey]int{}
	recorder := service.DataShareCaptureDurationObserveFunc(func(part service.DataShareCaptureDurationPartKey, duration time.Duration) {
		require.NotEmpty(t, part)
		require.GreaterOrEqual(t, duration, time.Duration(0))
		seen[part]++
	})
	now := time.Now().UTC()
	session := func(trajectoryID string, count int) *service.DataShareSession {
		return &service.DataShareSession{
			TrajectoryID:       trajectoryID,
			SessionID:          "sess-" + trajectoryID,
			Dataset:            "tokenrouter-agent",
			Provider:           service.PlatformOpenAI,
			Model:              "gpt-5",
			RequestPath:        "/v1/responses",
			UserAgent:          "codex-cli",
			Status:             service.DataShareStatusCompleted,
			IsFinalSnapshot:    true,
			SourceRequestCount: count,
			Tools:              []map[string]any{},
			Messages:           []map[string]any{{"role": "user", "content": "hi"}},
			Usage:              map[string]any{"total_tokens": count},
			Meta:               map[string]any{"request_path": "/v1/responses"},
			SessionJSON:        map[string]any{"messages": []map[string]any{{"role": "user", "content": "hi"}}},
			QualityStatus:      service.DataShareQualityComplete,
			QualityErrors:      []string{},
			TotalTokens:        int64(count),
			CreatedAt:          now,
			EndedAt:            &now,
			UpdatedAt:          now,
		}
	}

	require.NoError(t, repo.SaveCaptureSnapshot(ctx, session("traj-duration", 1), service.DataShareUpsertOptions{
		StorageLimitBytes: 1 << 30,
		DurationRecorder:  recorder,
	}))
	require.NoError(t, repo.SaveCaptureSnapshot(ctx, session("traj-duration", 2), service.DataShareUpsertOptions{
		StorageLimitBytes: 1 << 30,
		DurationRecorder:  recorder,
	}))
	count, err := client.DataShareSession.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.GreaterOrEqual(t, seen[service.DataShareCaptureDurationPartPayloadEncode], 2)
	require.GreaterOrEqual(t, seen[service.DataShareCaptureDurationPartStorageLimitCheck], 2)
	require.GreaterOrEqual(t, seen[service.DataShareCaptureDurationPartDBLookup], 2)
	require.GreaterOrEqual(t, seen[service.DataShareCaptureDurationPartDBWrite], 2)
}

func deterministicDataShareTestContent(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		// 生成不完全重复的文本，避免压缩率过高导致大增量测试失真。
		_, _ = fmt.Fprintf(&b, "line-%04d-%08x-%08x\n", i, i*2654435761, (i+17)*1103515245)
	}
	return b.String()
}

func TestDataShareSessionRepository_LegacyPayloadLazyCompression(t *testing.T) {
	repo, client := newDataShareSessionRepoSQLite(t)
	ctx := context.Background()
	now := time.Now().UTC()
	systemPrompt := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": "请列目录"},
	}
	created, err := client.DataShareSession.Create().
		SetTrajectoryID("traj-legacy").
		SetSessionID("sess-legacy").
		SetDataset("tokenrouter-agent").
		SetProvider(service.PlatformOpenAI).
		SetModel("gpt-5.5").
		SetRequestPath("/v1/chat/completions").
		SetUserAgent("codex-cli").
		SetStatus(service.DataShareStatusTerminated).
		SetIsFinalSnapshot(false).
		SetSourceRequestCount(1).
		SetNillableSystemPrompt(&systemPrompt).
		SetTools([]map[string]any{}).
		SetMessages(messages).
		SetUsage(map[string]any{"total_tokens": 2}).
		SetMeta(map[string]any{"request_path": "/v1/chat/completions"}).
		SetSessionJSON(map[string]any{
			"trajectory_id":        "traj-legacy",
			"session_id":           "sess-legacy",
			"dataset":              "tokenrouter-agent",
			"provider":             service.PlatformOpenAI,
			"model":                "gpt-5.5",
			"request_path":         "/v1/chat/completions",
			"user_agent":           "codex-cli",
			"created_at":           now.Format(time.RFC3339Nano),
			"ended_at":             now.Format(time.RFC3339Nano),
			"status":               service.DataShareStatusTerminated,
			"is_final_snapshot":    false,
			"source_request_count": 1,
			"system_prompt":        systemPrompt,
			"tools":                []map[string]any{},
			"messages":             messages,
			"usage":                map[string]any{"total_tokens": 2},
			"meta":                 map[string]any{"request_path": "/v1/chat/completions"},
		}).
		SetExportable(false).
		SetQualityStatus(service.DataShareQualityInvalid).
		SetQualityErrors([]string{}).
		SetStorageBytes(999).
		SetInputTokens(1).
		SetOutputTokens(1).
		SetTotalTokens(2).
		SetUserID(0).
		SetAPIKeyID(0).
		SetGroupID(0).
		SetCreatedAt(now).
		SetNillableEndedAt(&now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	detail, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, messages, detail.Messages)
	require.Equal(t, "/v1/chat/completions", detail.SessionJSON["request_path"])

	stored, err := client.DataShareSession.Get(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.PayloadCompressed)
	require.NotEmpty(t, *stored.PayloadCompressed)
	require.Equal(t, dataSharePayloadEncodingZstd, stored.PayloadEncoding)
	require.Greater(t, stored.PayloadBytes, int64(0))
	require.Equal(t, int64(len(*stored.PayloadCompressed)), stored.StorageBytes)
	require.Empty(t, stored.Messages)
	require.Empty(t, stored.SessionJSON)
	require.Nil(t, stored.SystemPrompt)
}
