package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/datasharesession"
	"github.com/TokenFlux/TokenRouter/ent/predicate"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/klauspost/compress/zstd"
	"github.com/lib/pq"
	"golang.org/x/sync/errgroup"

	entsql "entgo.io/ent/dialect/sql"
)

type dataShareSessionRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

const dataSharePayloadEncodingZstd = "zstd"

var dataSharePayloadCodecCache sync.Map

func NewDataShareSessionRepository(client *dbent.Client, sqlDB *sql.DB) service.DataShareSessionRepository {
	return &dataShareSessionRepository{client: client, sql: sqlDB}
}

func (r *dataShareSessionRepository) sqlExecutorFromContext(ctx context.Context) sqlExecutor {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return r.sql
}

func (r *dataShareSessionRepository) GetCaptureByTrajectoryIDWithPayload(ctx context.Context, trajectoryID string) (*service.DataShareSession, error) {
	trajectoryID = strings.TrimSpace(trajectoryID)
	if trajectoryID == "" {
		return nil, service.ErrDataShareSessionNotFound
	}
	item, err := clientFromContext(ctx, r.client).DataShareSession.Query().
		Where(datasharesession.TrajectoryIDEQ(trajectoryID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrDataShareSessionNotFound, nil)
	}
	out := dataShareSessionEntityToService(item)
	if out == nil {
		return nil, service.ErrDataShareSessionNotFound
	}
	if err := populateDataShareSessionPayload(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *dataShareSessionRepository) SaveCaptureSnapshot(ctx context.Context, session *service.DataShareSession, opts ...service.DataShareUpsertOptions) error {
	if session == nil {
		return nil
	}
	upsertOptions := resolveDataShareUpsertOptions(opts)
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = now
	}
	if session.EndedAt == nil {
		session.EndedAt = &now
	}
	payload := dataShareSessionPayloadForPersist(session)
	encodeStart := time.Now()
	compressed, payloadBytes, err := encodeDataSharePayload(payload)
	recordDataShareCaptureDuration(upsertOptions.DurationRecorder, service.DataShareCaptureDurationPartPayloadEncode, time.Since(encodeStart))
	if err != nil {
		return err
	}
	lookupStart := time.Now()
	existing, err := client.DataShareSession.Query().
		Where(datasharesession.TrajectoryIDEQ(session.TrajectoryID)).
		Only(ctx)
	recordDataShareCaptureDuration(upsertOptions.DurationRecorder, service.DataShareCaptureDurationPartDBLookup, time.Since(lookupStart))
	if err != nil && !dbent.IsNotFound(err) {
		return err
	}
	if dbent.IsNotFound(err) || existing == nil {
		// 新 session 写入前做容量保护，避免数据共享采集把磁盘持续打满。
		if limitBytes := upsertOptions.StorageLimitBytes; limitBytes > 0 {
			limitStart := time.Now()
			currentBytes, err := r.TotalStorageBytes(ctx)
			recordDataShareCaptureDuration(upsertOptions.DurationRecorder, service.DataShareCaptureDurationPartStorageLimitCheck, time.Since(limitStart))
			if err != nil {
				return err
			}
			if currentBytes+int64(len(compressed)) > limitBytes {
				return nil
			}
		}
		builder := client.DataShareSession.Create().
			SetTrajectoryID(session.TrajectoryID).
			SetSessionID(session.SessionID).
			SetDataset(session.Dataset).
			SetProvider(session.Provider).
			SetModel(session.Model).
			SetRequestPath(session.RequestPath).
			SetUserAgent(session.UserAgent).
			SetStatus(session.Status).
			SetIsFinalSnapshot(session.IsFinalSnapshot).
			SetSourceRequestCount(session.SourceRequestCount).
			SetTools([]map[string]any{}).
			SetMessages([]map[string]any{}).
			SetUsage(map[string]any{}).
			SetMeta(map[string]any{}).
			SetSessionJSON(map[string]any{}).
			SetPayloadCompressed(compressed).
			SetPayloadEncoding(dataSharePayloadEncodingZstd).
			SetPayloadBytes(payloadBytes).
			SetExportable(session.Exportable).
			SetQualityStatus(session.QualityStatus).
			SetQualityErrors(session.QualityErrors).
			SetStorageBytes(int64(len(compressed))).
			SetInputTokens(session.InputTokens).
			SetOutputTokens(session.OutputTokens).
			SetTotalTokens(session.TotalTokens).
			SetNillableActualCost(session.ActualCost).
			SetUserID(session.UserID).
			SetAPIKeyID(session.APIKeyID).
			SetGroupID(session.GroupID).
			SetCreatedAt(session.CreatedAt).
			SetNillableEndedAt(session.EndedAt).
			SetUpdatedAt(now)
		writeStart := time.Now()
		_, err = builder.Save(ctx)
		recordDataShareCaptureDuration(upsertOptions.DurationRecorder, service.DataShareCaptureDurationPartDBWrite, time.Since(writeStart))
		return err
	}

	// 已有 session 的快照保存也需要容量保护，避免单个任务持续追加时突破磁盘阈值。
	if limitBytes := upsertOptions.StorageLimitBytes; limitBytes > 0 {
		limitStart := time.Now()
		currentBytes, err := r.TotalStorageBytes(ctx)
		recordDataShareCaptureDuration(upsertOptions.DurationRecorder, service.DataShareCaptureDurationPartStorageLimitCheck, time.Since(limitStart))
		if err != nil {
			return err
		}
		nextBytes := currentBytes - existing.StorageBytes + int64(len(compressed))
		if nextBytes > limitBytes {
			return nil
		}
	}

	writeStart := time.Now()
	_, err = client.DataShareSession.Update().
		Where(datasharesession.IDEQ(existing.ID)).
		SetSessionID(session.SessionID).
		SetDataset(session.Dataset).
		SetProvider(session.Provider).
		SetModel(session.Model).
		SetRequestPath(session.RequestPath).
		SetUserAgent(session.UserAgent).
		SetStatus(session.Status).
		SetIsFinalSnapshot(session.IsFinalSnapshot).
		SetSourceRequestCount(session.SourceRequestCount).
		ClearSystemPrompt().
		SetTools([]map[string]any{}).
		SetMessages([]map[string]any{}).
		SetUsage(map[string]any{}).
		SetMeta(map[string]any{}).
		SetSessionJSON(map[string]any{}).
		SetPayloadCompressed(compressed).
		SetPayloadEncoding(dataSharePayloadEncodingZstd).
		SetPayloadBytes(payloadBytes).
		SetExportable(session.Exportable).
		SetQualityStatus(session.QualityStatus).
		SetQualityErrors(session.QualityErrors).
		SetStorageBytes(int64(len(compressed))).
		SetInputTokens(session.InputTokens).
		SetOutputTokens(session.OutputTokens).
		SetTotalTokens(session.TotalTokens).
		SetNillableActualCost(session.ActualCost).
		SetUserID(session.UserID).
		SetAPIKeyID(session.APIKeyID).
		SetGroupID(session.GroupID).
		SetNillableEndedAt(session.EndedAt).
		SetUpdatedAt(now).
		Save(ctx)
	recordDataShareCaptureDuration(upsertOptions.DurationRecorder, service.DataShareCaptureDurationPartDBWrite, time.Since(writeStart))
	return err
}

func resolveDataShareUpsertOptions(opts []service.DataShareUpsertOptions) service.DataShareUpsertOptions {
	if len(opts) == 0 {
		return service.DataShareUpsertOptions{}
	}
	return opts[0]
}

func recordDataShareCaptureDuration(recorder service.DataShareCaptureDurationRecorder, part service.DataShareCaptureDurationPartKey, duration time.Duration) {
	if recorder == nil {
		return
	}
	recorder.Observe(part, duration)
}

func recordDataShareExportDuration(recorder service.DataShareExportDurationRecorder, part service.DataShareExportDurationPartKey, duration time.Duration) {
	if recorder == nil {
		return
	}
	recorder.Observe(part, duration)
}

func (r *dataShareSessionRepository) List(ctx context.Context, params pagination.PaginationParams, filters service.DataShareSessionFilters) ([]service.DataShareSession, *pagination.PaginationResult, error) {
	return r.listSessions(ctx, params, filters, false)
}

func (r *dataShareSessionRepository) ListWithPayload(ctx context.Context, params pagination.PaginationParams, filters service.DataShareSessionFilters) ([]service.DataShareSession, *pagination.PaginationResult, error) {
	return r.listSessions(ctx, params, filters, true)
}

func (r *dataShareSessionRepository) ListWithPayloadPage(ctx context.Context, params pagination.PaginationParams, filters service.DataShareSessionFilters) ([]service.DataShareSession, error) {
	items, err := r.listSessionPage(ctx, params, filters, true)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (r *dataShareSessionRepository) ListExportPayloadPage(ctx context.Context, filters service.DataShareSessionFilters, cursor *service.DataShareSessionExportCursor, limit int, workerCount int, recorder service.DataShareExportDurationRecorder) ([]service.DataShareSession, *service.DataShareSessionExportCursor, error) {
	limit = service.NormalizeDataShareExportBatchSize(limit)
	workerCount = service.NormalizeDataShareExportWorkerCount(workerCount)
	client := clientFromContext(ctx, r.client)
	q := applyDataShareFilters(client.DataShareSession.Query(), filters)
	if cursor != nil && !cursor.CreatedAt.IsZero() && cursor.ID > 0 {
		q = q.Where(datasharesession.Or(
			datasharesession.CreatedAtGT(cursor.CreatedAt),
			datasharesession.And(
				datasharesession.CreatedAtEQ(cursor.CreatedAt),
				datasharesession.IDGT(cursor.ID),
			),
		))
	}
	params := pagination.PaginationParams{Page: 1, PageSize: limit, SortBy: "created_at", SortOrder: pagination.SortOrderAsc}
	start := time.Now()
	query := q.Offset(0).Limit(params.Limit())
	for _, order := range dataShareListOrder(params) {
		query = query.Order(order)
	}
	var items []*dbent.DataShareSession
	err := query.Select(dataShareExportPayloadFields()...).Scan(ctx, &items)
	recordDataShareExportDuration(recorder, service.DataShareExportDurationPartDBPage, time.Since(start))
	if err != nil {
		return nil, nil, err
	}
	out := make([]service.DataShareSession, len(items))
	g, gctx := errgroup.WithContext(ctx)
	if workerCount > len(items) && len(items) > 0 {
		workerCount = len(items)
	}
	g.SetLimit(workerCount)
	for i := range items {
		i := i
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			item := dataShareSessionEntityToService(items[i])
			decodeStart := time.Now()
			if err := populateDataShareSessionPayload(item); err != nil {
				recordDataShareExportDuration(recorder, service.DataShareExportDurationPartPayloadDecode, time.Since(decodeStart))
				return err
			}
			recordDataShareExportDuration(recorder, service.DataShareExportDurationPartPayloadDecode, time.Since(decodeStart))
			out[i] = *item
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	for i := range out {
		if len(out[i].PayloadCompressed) == 0 && len(out[i].SessionJSON) > 0 {
			if err := r.persistCompressedPayload(ctx, &out[i]); err != nil {
				return nil, nil, err
			}
		}
	}
	if len(out) == 0 {
		return out, nil, nil
	}
	last := out[len(out)-1]
	return out, &service.DataShareSessionExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (r *dataShareSessionRepository) Count(ctx context.Context, filters service.DataShareSessionFilters) (int64, error) {
	client := clientFromContext(ctx, r.client)
	q := applyDataShareFilters(client.DataShareSession.Query(), filters)
	total, err := q.Count(ctx)
	return int64(total), err
}

func (r *dataShareSessionRepository) listSessions(ctx context.Context, params pagination.PaginationParams, filters service.DataShareSessionFilters, includePayload bool) ([]service.DataShareSession, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := applyDataShareFilters(client.DataShareSession.Query(), filters)
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	items, err := r.listSessionPage(ctx, params, filters, includePayload)
	if err != nil {
		return nil, nil, err
	}
	return items, paginationResultFromTotal(int64(total), params), nil
}

func (r *dataShareSessionRepository) listSessionPage(ctx context.Context, params pagination.PaginationParams, filters service.DataShareSessionFilters, includePayload bool) ([]service.DataShareSession, error) {
	client := clientFromContext(ctx, r.client)
	q := applyDataShareFilters(client.DataShareSession.Query(), filters)
	items, err := listDataShareEntities(ctx, q, params, includePayload)
	if err != nil {
		return nil, err
	}
	out := make([]service.DataShareSession, 0, len(items))
	for i := range items {
		item := dataShareSessionEntityToService(items[i])
		if includePayload {
			if err := populateDataShareSessionPayload(item); err != nil {
				return nil, err
			}
			if len(item.PayloadCompressed) == 0 && len(item.SessionJSON) > 0 {
				if err := r.persistCompressedPayload(ctx, item); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, *item)
	}
	if err := r.hydrateDisplayNames(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *dataShareSessionRepository) GetByID(ctx context.Context, id int64) (*service.DataShareSession, error) {
	item, err := clientFromContext(ctx, r.client).DataShareSession.Query().
		Where(datasharesession.IDEQ(id)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrDataShareSessionNotFound, nil)
	}
	out := dataShareSessionEntityToService(item)
	if out == nil {
		return nil, service.ErrDataShareSessionNotFound
	}
	if err := populateDataShareSessionPayload(out); err != nil {
		return nil, err
	}
	if len(out.PayloadCompressed) == 0 && len(out.SessionJSON) > 0 {
		if err := r.persistCompressedPayload(ctx, out); err != nil {
			return nil, err
		}
	}
	items := []service.DataShareSession{*out}
	if err := r.hydrateDisplayNames(ctx, items); err != nil {
		return nil, err
	}
	return &items[0], nil
}

func (r *dataShareSessionRepository) Delete(ctx context.Context, id int64) error {
	affected, err := clientFromContext(ctx, r.client).DataShareSession.Delete().
		Where(datasharesession.IDEQ(id)).
		Exec(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrDataShareSessionNotFound
	}
	return nil
}

func (r *dataShareSessionRepository) BatchDelete(ctx context.Context, ids []int64, filters service.DataShareSessionFilters) (int64, error) {
	if len(ids) == 0 && !filters.SelectAll {
		return 0, nil
	}
	if filters.SelectAll {
		filters.IDs = nil
	} else {
		filters.IDs = ids
	}
	q := clientFromContext(ctx, r.client).DataShareSession.Delete()
	if preds := dataSharePredicates(filters); len(preds) > 0 {
		q = q.Where(preds...)
	}
	affected, err := q.Exec(ctx)
	return int64(affected), err
}

func (r *dataShareSessionRepository) Stats(ctx context.Context, filters service.DataShareSessionFilters) (*service.DataShareStats, error) {
	sqlq := r.sqlExecutorFromContext(ctx)
	whereSQL, args := dataShareStatsWhere(filters)
	stats := &service.DataShareStats{}
	if err := scanSingleRow(ctx, sqlq, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE exportable = TRUE),
			COUNT(*) FILTER (WHERE exportable = FALSE),
			COUNT(*) FILTER (WHERE quality_status = 'complete'),
			COUNT(*) FILTER (WHERE quality_status = 'partial'),
			COUNT(*) FILTER (WHERE quality_status = 'invalid'),
			COALESCE(SUM(storage_bytes), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE actual_cost IS NOT NULL), 0),
			COALESCE(AVG(actual_cost) FILTER (WHERE actual_cost IS NOT NULL), 0)
		FROM data_share_sessions
		`+whereSQL,
		args,
		&stats.SessionCount,
		&stats.ExportableCount,
		&stats.NonExportableCount,
		&stats.CompleteCount,
		&stats.PartialCount,
		&stats.InvalidCount,
		&stats.TotalStorageBytes,
		&stats.TotalTokens,
		&stats.TotalActualCost,
		&stats.AvgActualCostPerSession,
	); err != nil {
		return nil, err
	}
	if stats.SessionCount > 0 {
		stats.AvgTokensPerSession = float64(stats.TotalTokens) / float64(stats.SessionCount)
	}
	trend, err := r.loadStorageTrend(ctx, sqlq, whereSQL, args)
	if err != nil {
		return nil, err
	}
	stats.StorageTrend = trend
	breakdown, err := r.loadGroupStorageBreakdown(ctx, sqlq, whereSQL, args)
	if err != nil {
		return nil, err
	}
	stats.GroupStorageBreakdown = breakdown
	pathBreakdown, err := r.loadRequestPathBreakdown(ctx, sqlq, whereSQL, args)
	if err != nil {
		return nil, err
	}
	stats.RequestPathBreakdown = pathBreakdown
	modelBreakdown, err := r.loadModelBreakdown(ctx, sqlq, whereSQL, args)
	if err != nil {
		return nil, err
	}
	stats.ModelBreakdown = modelBreakdown
	userAgentBreakdown, err := r.loadUserAgentBreakdown(ctx, sqlq, whereSQL, args)
	if err != nil {
		return nil, err
	}
	stats.UserAgentBreakdown = userAgentBreakdown
	qualityErrorBreakdown, err := r.loadQualityErrorBreakdown(ctx, sqlq, whereSQL, args)
	if err != nil {
		return nil, err
	}
	stats.QualityErrorBreakdown = qualityErrorBreakdown
	invalidUserBreakdown, err := r.loadInvalidUserBreakdown(ctx, sqlq, filters)
	if err != nil {
		return nil, err
	}
	stats.InvalidUserBreakdown = invalidUserBreakdown
	return stats, nil
}

func (r *dataShareSessionRepository) FilterOptions(ctx context.Context, filters service.DataShareSessionFilters) (*service.DataShareSessionFilterOptions, error) {
	sqlq := r.sqlExecutorFromContext(ctx)
	whereSQL, args := dataShareStatsWhere(filters)
	models, err := r.loadDistinctDataShareStrings(ctx, sqlq, "model", whereSQL, args)
	if err != nil {
		return nil, err
	}
	requestPaths, err := r.loadDistinctDataShareStrings(ctx, sqlq, "request_path", whereSQL, args)
	if err != nil {
		return nil, err
	}
	userAgents, err := r.loadDistinctDataShareStrings(ctx, sqlq, "user_agent", whereSQL, args)
	if err != nil {
		return nil, err
	}
	return &service.DataShareSessionFilterOptions{
		Models:       models,
		RequestPaths: requestPaths,
		UserAgents:   userAgents,
	}, nil
}

func (r *dataShareSessionRepository) TotalStorageBytes(ctx context.Context) (int64, error) {
	sqlq := r.sqlExecutorFromContext(ctx)
	total := int64(0)
	err := scanSingleRow(ctx, sqlq, `SELECT COALESCE(SUM(storage_bytes), 0) FROM data_share_sessions`, nil, &total)
	return total, err
}

func (r *dataShareSessionRepository) loadDistinctDataShareStrings(ctx context.Context, sqlq sqlExecutor, column string, whereSQL string, args []any) ([]string, error) {
	// 列名只来自固定调用点，避免把用户输入拼进 DISTINCT 查询。
	whereSQL = dataShareAppendWhere(whereSQL, fmt.Sprintf("NULLIF(%s, '') IS NOT NULL", column))
	rows, err := sqlq.QueryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT %s
		FROM data_share_sessions
		%s
		ORDER BY %s ASC
	`, column, whereSQL, column), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func dataShareAppendWhere(whereSQL string, clause string) string {
	if strings.TrimSpace(clause) == "" {
		return whereSQL
	}
	if strings.TrimSpace(whereSQL) == "" {
		return "WHERE " + clause
	}
	return whereSQL + " AND " + clause
}

func (r *dataShareSessionRepository) loadStorageTrend(ctx context.Context, sqlq sqlExecutor, whereSQL string, args []any) ([]service.DataShareStoragePoint, error) {
	rows, err := sqlq.QueryContext(ctx, `
		SELECT to_char(date_trunc('day', created_at), 'YYYY-MM-DD') AS day,
		       COALESCE(SUM(storage_bytes), 0),
		       COUNT(*)
		FROM data_share_sessions
		`+whereSQL+`
		GROUP BY day
		ORDER BY day ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.DataShareStoragePoint
	for rows.Next() {
		var p service.DataShareStoragePoint
		if err := rows.Scan(&p.Date, &p.StorageBytes, &p.SessionCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *dataShareSessionRepository) loadGroupStorageBreakdown(ctx context.Context, sqlq sqlExecutor, whereSQL string, args []any) ([]service.DataShareGroupStoragePoint, error) {
	whereSQL = prefixDataShareWhereAlias(whereSQL, "d")
	rows, err := sqlq.QueryContext(ctx, `
		SELECT d.group_id, COALESCE(g.name, ''), COALESCE(SUM(d.storage_bytes), 0), COUNT(*)
		FROM data_share_sessions d
		LEFT JOIN groups g ON g.id = d.group_id
		`+whereSQL+`
		GROUP BY d.group_id, g.name
		ORDER BY COALESCE(SUM(d.storage_bytes), 0) DESC
		LIMIT 20
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.DataShareGroupStoragePoint
	for rows.Next() {
		var p service.DataShareGroupStoragePoint
		if err := rows.Scan(&p.GroupID, &p.GroupName, &p.StorageBytes, &p.SessionCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *dataShareSessionRepository) loadRequestPathBreakdown(ctx context.Context, sqlq sqlExecutor, whereSQL string, args []any) ([]service.DataShareRequestPathPoint, error) {
	rows, err := sqlq.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(request_path, ''), '(unknown)') AS request_path,
		       COALESCE(SUM(storage_bytes), 0),
		       COUNT(*),
		       COALESCE(SUM(total_tokens), 0)
		FROM data_share_sessions
		`+whereSQL+`
		GROUP BY COALESCE(NULLIF(request_path, ''), '(unknown)')
		ORDER BY COUNT(*) DESC, COALESCE(SUM(storage_bytes), 0) DESC
		LIMIT 20
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.DataShareRequestPathPoint
	for rows.Next() {
		var p service.DataShareRequestPathPoint
		if err := rows.Scan(&p.RequestPath, &p.StorageBytes, &p.SessionCount, &p.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *dataShareSessionRepository) loadModelBreakdown(ctx context.Context, sqlq sqlExecutor, whereSQL string, args []any) ([]service.DataShareModelPoint, error) {
	rows, err := sqlq.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(model, ''), '(unknown)') AS model,
		       COALESCE(SUM(storage_bytes), 0),
		       COUNT(*),
		       COALESCE(SUM(total_tokens), 0)
		FROM data_share_sessions
		`+whereSQL+`
		GROUP BY COALESCE(NULLIF(model, ''), '(unknown)')
		ORDER BY COUNT(*) DESC, COALESCE(SUM(storage_bytes), 0) DESC
		LIMIT 20
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.DataShareModelPoint
	for rows.Next() {
		var p service.DataShareModelPoint
		if err := rows.Scan(&p.Model, &p.StorageBytes, &p.SessionCount, &p.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *dataShareSessionRepository) loadUserAgentBreakdown(ctx context.Context, sqlq sqlExecutor, whereSQL string, args []any) ([]service.DataShareUserAgentPoint, error) {
	rows, err := sqlq.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(user_agent, ''), '(unknown)') AS user_agent,
		       COALESCE(SUM(storage_bytes), 0),
		       COUNT(*),
		       COALESCE(SUM(total_tokens), 0)
		FROM data_share_sessions
		`+whereSQL+`
		GROUP BY COALESCE(NULLIF(user_agent, ''), '(unknown)')
		ORDER BY COUNT(*) DESC, COALESCE(SUM(storage_bytes), 0) DESC
		LIMIT 20
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.DataShareUserAgentPoint
	for rows.Next() {
		var p service.DataShareUserAgentPoint
		if err := rows.Scan(&p.UserAgent, &p.StorageBytes, &p.SessionCount, &p.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *dataShareSessionRepository) loadQualityErrorBreakdown(ctx context.Context, sqlq sqlExecutor, whereSQL string, args []any) ([]service.DataShareQualityErrorPoint, error) {
	whereSQL = prefixDataShareWhereAlias(whereSQL, "d")
	rows, err := sqlq.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(err.error_code, ''), '(unknown)') AS error_code,
		       COUNT(DISTINCT d.id) AS session_count
		FROM data_share_sessions d
		CROSS JOIN LATERAL jsonb_array_elements_text(
			CASE
				-- 历史脏数据可能把 quality_errors 写成标量，统计时只展开可识别的数组或字符串。
				WHEN jsonb_typeof(d.quality_errors) = 'array' THEN d.quality_errors
				WHEN jsonb_typeof(d.quality_errors) = 'string' THEN jsonb_build_array(d.quality_errors #>> '{}')
				ELSE '[]'::jsonb
			END
		) AS err(error_code)
		`+whereSQL+`
		GROUP BY COALESCE(NULLIF(err.error_code, ''), '(unknown)')
		ORDER BY COUNT(DISTINCT d.id) DESC, error_code ASC
		LIMIT 20
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.DataShareQualityErrorPoint
	for rows.Next() {
		var p service.DataShareQualityErrorPoint
		if err := rows.Scan(&p.ErrorCode, &p.SessionCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *dataShareSessionRepository) loadInvalidUserBreakdown(ctx context.Context, sqlq sqlExecutor, filters service.DataShareSessionFilters) ([]service.DataShareInvalidUserPoint, error) {
	// 用户排行需要固定统计无效会话贡献，不能被页面的质量筛选切走。
	filters.QualityStatus = ""
	whereSQL, args := dataShareStatsWhere(filters)
	whereSQL = prefixDataShareWhereAlias(whereSQL, "d")
	rows, err := sqlq.QueryContext(ctx, `
		SELECT d.user_id,
		       COALESCE(u.username, ''),
		       COALESCE(u.email, ''),
		       COUNT(*) AS session_count,
		       COUNT(*) FILTER (WHERE d.quality_status = 'invalid') AS invalid_count,
		       COALESCE(SUM(d.storage_bytes) FILTER (WHERE d.quality_status = 'invalid'), 0) AS storage_bytes,
		       COALESCE(SUM(d.total_tokens) FILTER (WHERE d.quality_status = 'invalid'), 0) AS total_tokens
		FROM data_share_sessions d
		LEFT JOIN users u ON u.id = d.user_id
		`+whereSQL+`
		GROUP BY d.user_id, u.username, u.email
		HAVING COUNT(*) FILTER (WHERE d.quality_status = 'invalid') > 0
		ORDER BY invalid_count DESC, storage_bytes DESC, d.user_id ASC
		LIMIT 20
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.DataShareInvalidUserPoint
	for rows.Next() {
		var p service.DataShareInvalidUserPoint
		if err := rows.Scan(&p.UserID, &p.UserName, &p.UserEmail, &p.SessionCount, &p.InvalidCount, &p.StorageBytes, &p.TotalTokens); err != nil {
			return nil, err
		}
		if p.SessionCount > 0 {
			p.InvalidRatio = float64(p.InvalidCount) / float64(p.SessionCount)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func prefixDataShareWhereAlias(whereSQL, alias string) string {
	if strings.TrimSpace(whereSQL) == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"user_id", alias+".user_id",
		"api_key_id", alias+".api_key_id",
		"group_id", alias+".group_id",
		"provider", alias+".provider",
		"model", alias+".model",
		"request_path", alias+".request_path",
		"user_agent", alias+".user_agent",
		"exportable", alias+".exportable",
		"quality_status", alias+".quality_status",
		"created_at", alias+".created_at",
		"trajectory_id", alias+".trajectory_id",
		"session_id", alias+".session_id",
	)
	return replacer.Replace(whereSQL)
}

func applyDataShareFilters(q *dbent.DataShareSessionQuery, filters service.DataShareSessionFilters) *dbent.DataShareSessionQuery {
	preds := dataSharePredicates(filters)
	if len(preds) > 0 {
		q = q.Where(preds...)
	}
	return q
}

func dataSharePredicates(filters service.DataShareSessionFilters) []predicate.DataShareSession {
	var preds []predicate.DataShareSession
	if len(filters.IDs) > 0 {
		preds = append(preds, datasharesession.IDIn(filters.IDs...))
	}
	if len(filters.ExcludeIDs) > 0 {
		preds = append(preds, datasharesession.IDNotIn(filters.ExcludeIDs...))
	}
	if filters.UserID > 0 {
		preds = append(preds, datasharesession.UserIDEQ(filters.UserID))
	}
	if filters.UserName != "" {
		preds = append(preds, dataShareRelatedNamePredicate("users", "user_id", []string{"username", "email"}, filters.UserName))
	}
	if filters.APIKeyID > 0 {
		preds = append(preds, datasharesession.APIKeyIDEQ(filters.APIKeyID))
	}
	if filters.APIKeyName != "" {
		preds = append(preds, dataShareRelatedNamePredicate("api_keys", "api_key_id", []string{"name"}, filters.APIKeyName))
	}
	if filters.GroupID > 0 {
		preds = append(preds, datasharesession.GroupIDEQ(filters.GroupID))
	}
	if filters.GroupName != "" {
		preds = append(preds, dataShareRelatedNamePredicate("groups", "group_id", []string{"name"}, filters.GroupName))
	}
	if filters.Provider != "" {
		preds = append(preds, datasharesession.ProviderEQ(filters.Provider))
	}
	if filters.Model != "" {
		preds = append(preds, datasharesession.ModelEQ(filters.Model))
	}
	if filters.RequestPath != "" {
		preds = append(preds, datasharesession.RequestPathEQ(filters.RequestPath))
	}
	if filters.UserAgent != "" {
		preds = append(preds, datasharesession.UserAgentEQ(filters.UserAgent))
	}
	if filters.Exportable != nil {
		preds = append(preds, datasharesession.ExportableEQ(*filters.Exportable))
	}
	if statuses := service.DataShareQualityFilterStatuses(filters.QualityStatus); len(statuses) == 1 {
		preds = append(preds, datasharesession.QualityStatusEQ(statuses[0]))
	} else if len(statuses) > 1 {
		preds = append(preds, datasharesession.QualityStatusIn(statuses...))
	}
	if filters.StartTime != nil {
		preds = append(preds, datasharesession.CreatedAtGTE(*filters.StartTime))
	}
	if filters.EndTime != nil {
		preds = append(preds, datasharesession.CreatedAtLT(*filters.EndTime))
	}
	if filters.Search != "" {
		preds = append(preds, datasharesession.Or(
			datasharesession.TrajectoryIDContainsFold(filters.Search),
			datasharesession.SessionIDContainsFold(filters.Search),
			datasharesession.ModelContainsFold(filters.Search),
			datasharesession.RequestPathContainsFold(filters.Search),
			datasharesession.UserAgentContainsFold(filters.Search),
			dataShareRelatedNamePredicate("users", "user_id", []string{"username", "email"}, filters.Search),
			dataShareRelatedNamePredicate("api_keys", "api_key_id", []string{"name"}, filters.Search),
			dataShareRelatedNamePredicate("groups", "group_id", []string{"name"}, filters.Search),
		))
	}
	return preds
}

func dataShareRelatedNamePredicate(table string, localField string, columns []string, keyword string) predicate.DataShareSession {
	return predicate.DataShareSession(func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			b.WriteString("EXISTS (SELECT 1 FROM ").
				WriteString(table).
				WriteString(" rel WHERE rel.id = ").
				WriteString(s.C(localField)).
				WriteString(" AND (")
			for i, column := range columns {
				if i > 0 {
					b.WriteString(" OR ")
				}
				b.WriteString("LOWER(").
					WriteString("rel.").
					WriteString(column).
					WriteString(") LIKE '%' || LOWER(").
					Arg(keyword).
					WriteString(") || '%'")
			}
			b.WriteString("))")
		}))
	})
}

func dataShareStatsWhere(filters service.DataShareSessionFilters) (string, []any) {
	clauses := make([]string, 0, 8)
	args := make([]any, 0, 8)
	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if filters.UserID > 0 {
		add("user_id = $%d", filters.UserID)
	}
	if filters.UserName != "" {
		addDataShareRelatedNameStatsClause(&clauses, &args, "users", "user_id", []string{"username", "email"}, filters.UserName)
	}
	if filters.APIKeyID > 0 {
		add("api_key_id = $%d", filters.APIKeyID)
	}
	if filters.APIKeyName != "" {
		addDataShareRelatedNameStatsClause(&clauses, &args, "api_keys", "api_key_id", []string{"name"}, filters.APIKeyName)
	}
	if filters.GroupID > 0 {
		add("group_id = $%d", filters.GroupID)
	}
	if filters.GroupName != "" {
		addDataShareRelatedNameStatsClause(&clauses, &args, "groups", "group_id", []string{"name"}, filters.GroupName)
	}
	if filters.Provider != "" {
		add("provider = $%d", filters.Provider)
	}
	if filters.Model != "" {
		add("model = $%d", filters.Model)
	}
	if filters.RequestPath != "" {
		add("request_path = $%d", filters.RequestPath)
	}
	if filters.UserAgent != "" {
		add("user_agent = $%d", filters.UserAgent)
	}
	if filters.Exportable != nil {
		add("exportable = $%d", *filters.Exportable)
	}
	if statuses := service.DataShareQualityFilterStatuses(filters.QualityStatus); len(statuses) == 1 {
		add("quality_status = $%d", statuses[0])
	} else if len(statuses) > 1 {
		// 非无效筛选需要同时命中完整和部分完整两种实际入库状态。
		placeholders := make([]string, 0, len(statuses))
		for _, status := range statuses {
			args = append(args, status)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		clauses = append(clauses, "quality_status IN ("+strings.Join(placeholders, ", ")+")")
	}
	if filters.StartTime != nil {
		add("created_at >= $%d", *filters.StartTime)
	}
	if filters.EndTime != nil {
		add("created_at < $%d", *filters.EndTime)
	}
	if filters.Search != "" {
		// 搜索条件复用同一个参数，避免手工补多个占位符时出现编号错位。
		args = append(args, filters.Search)
		idx := len(args)
		clauses = append(clauses, fmt.Sprintf(`(
			trajectory_id ILIKE '%%' || $%d || '%%'
			OR session_id ILIKE '%%' || $%d || '%%'
			OR model ILIKE '%%' || $%d || '%%'
			OR request_path ILIKE '%%' || $%d || '%%'
			OR user_agent ILIKE '%%' || $%d || '%%'
			OR EXISTS (SELECT 1 FROM users u WHERE u.id = user_id AND (u.username ILIKE '%%' || $%d || '%%' OR u.email ILIKE '%%' || $%d || '%%'))
			OR EXISTS (SELECT 1 FROM api_keys ak WHERE ak.id = api_key_id AND ak.name ILIKE '%%' || $%d || '%%')
			OR EXISTS (SELECT 1 FROM groups g WHERE g.id = group_id AND g.name ILIKE '%%' || $%d || '%%')
		)`, idx, idx, idx, idx, idx, idx, idx, idx, idx))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func addDataShareRelatedNameStatsClause(clauses *[]string, args *[]any, table string, localField string, columns []string, keyword string) {
	*args = append(*args, keyword)
	idx := len(*args)
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, fmt.Sprintf("LOWER(rel.%s) LIKE '%%' || LOWER($%d) || '%%'", column, idx))
	}
	*clauses = append(*clauses, fmt.Sprintf("EXISTS (SELECT 1 FROM %s rel WHERE rel.id = %s AND (%s))", table, localField, strings.Join(parts, " OR ")))
}

func dataShareListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)
	field := datasharesession.FieldCreatedAt
	switch sortBy {
	case "storage_bytes":
		field = datasharesession.FieldStorageBytes
	case "total_tokens":
		field = datasharesession.FieldTotalTokens
	case "updated_at":
		field = datasharesession.FieldUpdatedAt
	case "model":
		field = datasharesession.FieldModel
	case "request_path":
		field = datasharesession.FieldRequestPath
	case "user_agent":
		field = datasharesession.FieldUserAgent
	case "quality_status":
		field = datasharesession.FieldQualityStatus
	case "provider":
		field = datasharesession.FieldProvider
	case "id":
		field = datasharesession.FieldID
	}
	if sortOrder == pagination.SortOrderAsc {
		return []func(*entsql.Selector){dbent.Asc(field), dbent.Asc(datasharesession.FieldID)}
	}
	return []func(*entsql.Selector){dbent.Desc(field), dbent.Desc(datasharesession.FieldID)}
}

func dataShareMetadataFields() []string {
	return []string{
		datasharesession.FieldID,
		datasharesession.FieldTrajectoryID,
		datasharesession.FieldSessionID,
		datasharesession.FieldDataset,
		datasharesession.FieldProvider,
		datasharesession.FieldModel,
		datasharesession.FieldRequestPath,
		datasharesession.FieldUserAgent,
		datasharesession.FieldStatus,
		datasharesession.FieldIsFinalSnapshot,
		datasharesession.FieldSourceRequestCount,
		datasharesession.FieldPayloadEncoding,
		datasharesession.FieldPayloadBytes,
		datasharesession.FieldExportable,
		datasharesession.FieldQualityStatus,
		datasharesession.FieldQualityErrors,
		datasharesession.FieldStorageBytes,
		datasharesession.FieldInputTokens,
		datasharesession.FieldOutputTokens,
		datasharesession.FieldTotalTokens,
		datasharesession.FieldActualCost,
		datasharesession.FieldUserID,
		datasharesession.FieldAPIKeyID,
		datasharesession.FieldGroupID,
		datasharesession.FieldEndedAt,
		datasharesession.FieldCreatedAt,
		datasharesession.FieldUpdatedAt,
	}
}

func dataShareExportPayloadFields() []string {
	fields := dataShareMetadataFields()
	fields = append(fields,
		datasharesession.FieldSystemPrompt,
		datasharesession.FieldTools,
		datasharesession.FieldMessages,
		datasharesession.FieldUsage,
		datasharesession.FieldMeta,
		datasharesession.FieldSessionJSON,
		datasharesession.FieldPayloadCompressed,
	)
	return fields
}

func listDataShareEntities(ctx context.Context, q *dbent.DataShareSessionQuery, params pagination.PaginationParams, includePayload bool) ([]*dbent.DataShareSession, error) {
	query := q.Offset(params.Offset()).Limit(params.Limit())
	for _, order := range dataShareListOrder(params) {
		query = query.Order(order)
	}
	if includePayload {
		return query.All(ctx)
	}
	var rows []*dbent.DataShareSession
	err := query.Select(dataShareMetadataFields()...).Scan(ctx, &rows)
	return rows, err
}

func dataShareSessionEntityToService(m *dbent.DataShareSession) *service.DataShareSession {
	if m == nil {
		return nil
	}
	return &service.DataShareSession{
		ID:                 m.ID,
		TrajectoryID:       m.TrajectoryID,
		SessionID:          m.SessionID,
		Dataset:            m.Dataset,
		Provider:           m.Provider,
		Model:              m.Model,
		RequestPath:        m.RequestPath,
		UserAgent:          m.UserAgent,
		Status:             m.Status,
		IsFinalSnapshot:    m.IsFinalSnapshot,
		SourceRequestCount: m.SourceRequestCount,
		SystemPrompt:       m.SystemPrompt,
		Tools:              m.Tools,
		Messages:           m.Messages,
		Usage:              m.Usage,
		Meta:               m.Meta,
		SessionJSON:        m.SessionJSON,
		PayloadCompressed:  dataSharePayloadCompressedValue(m.PayloadCompressed),
		PayloadEncoding:    m.PayloadEncoding,
		PayloadBytes:       m.PayloadBytes,
		Exportable:         m.Exportable,
		QualityStatus:      m.QualityStatus,
		QualityErrors:      m.QualityErrors,
		StorageBytes:       m.StorageBytes,
		InputTokens:        m.InputTokens,
		OutputTokens:       m.OutputTokens,
		TotalTokens:        m.TotalTokens,
		ActualCost:         m.ActualCost,
		UserID:             m.UserID,
		UserName:           stringFromRepositoryAny(m.Meta["user_name"]),
		UserEmail:          stringFromRepositoryAny(m.Meta["user_email"]),
		APIKeyID:           m.APIKeyID,
		APIKeyName:         stringFromRepositoryAny(m.Meta["api_key_name"]),
		GroupID:            m.GroupID,
		GroupName:          stringFromRepositoryAny(m.Meta["group_name"]),
		CreatedAt:          m.CreatedAt,
		EndedAt:            m.EndedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}

func dataSharePayloadCompressedValue(value *[]byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), (*value)...)
}

func (r *dataShareSessionRepository) hydrateDisplayNames(ctx context.Context, items []service.DataShareSession) error {
	if len(items) == 0 {
		return nil
	}
	sqlq := r.sqlExecutorFromContext(ctx)
	userIDs := make([]int64, 0, len(items))
	apiKeyIDs := make([]int64, 0, len(items))
	groupIDs := make([]int64, 0, len(items))
	for i := range items {
		if items[i].UserID > 0 {
			userIDs = append(userIDs, items[i].UserID)
		}
		if items[i].APIKeyID > 0 {
			apiKeyIDs = append(apiKeyIDs, items[i].APIKeyID)
		}
		if items[i].GroupID > 0 {
			groupIDs = append(groupIDs, items[i].GroupID)
		}
	}
	users, err := dataShareLoadUserNames(ctx, sqlq, uniqueInt64s(userIDs))
	if err != nil {
		return err
	}
	keys, err := dataShareLoadIDNames(ctx, sqlq, "api_keys", "name", uniqueInt64s(apiKeyIDs))
	if err != nil {
		return err
	}
	groups, err := dataShareLoadIDNames(ctx, sqlq, "groups", "name", uniqueInt64s(groupIDs))
	if err != nil {
		return err
	}
	for i := range items {
		if info, ok := users[items[i].UserID]; ok {
			if strings.TrimSpace(items[i].UserName) == "" {
				items[i].UserName = info.Name
			}
			if strings.TrimSpace(items[i].UserEmail) == "" {
				items[i].UserEmail = info.Email
			}
		}
		if name := keys[items[i].APIKeyID]; strings.TrimSpace(items[i].APIKeyName) == "" && name != "" {
			items[i].APIKeyName = name
		}
		if name := groups[items[i].GroupID]; strings.TrimSpace(items[i].GroupName) == "" && name != "" {
			items[i].GroupName = name
		}
		if items[i].Meta == nil {
			items[i].Meta = map[string]any{}
		}
		items[i].Meta["user_name"] = items[i].UserName
		items[i].Meta["user_email"] = items[i].UserEmail
		items[i].Meta["api_key_name"] = items[i].APIKeyName
		items[i].Meta["group_name"] = items[i].GroupName
		if items[i].SessionJSON != nil {
			meta, _ := items[i].SessionJSON["meta"].(map[string]any)
			if meta == nil {
				meta = map[string]any{}
			}
			meta["user_name"] = items[i].UserName
			meta["user_email"] = items[i].UserEmail
			meta["api_key_name"] = items[i].APIKeyName
			meta["group_name"] = items[i].GroupName
			items[i].SessionJSON["meta"] = meta
		}
	}
	return nil
}

type dataShareUserDisplay struct {
	Name  string
	Email string
}

func dataShareLoadUserNames(ctx context.Context, sqlq sqlExecutor, ids []int64) (map[int64]dataShareUserDisplay, error) {
	out := make(map[int64]dataShareUserDisplay)
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := sqlq.QueryContext(ctx, `
		SELECT id, COALESCE(NULLIF(username, ''), email, ''), COALESCE(email, '')
		FROM users
		WHERE id = ANY($1)
	`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var item dataShareUserDisplay
		if err := rows.Scan(&id, &item.Name, &item.Email); err != nil {
			return nil, err
		}
		out[id] = item
	}
	return out, rows.Err()
}

func dataShareLoadIDNames(ctx context.Context, sqlq sqlExecutor, table string, nameColumn string, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string)
	if len(ids) == 0 {
		return out, nil
	}
	query := fmt.Sprintf("SELECT id, COALESCE(%s, '') FROM %s WHERE id = ANY($1)", nameColumn, table)
	rows, err := sqlq.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func uniqueInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func encodeDataSharePayload(payload map[string]any) ([]byte, int64, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	codec, err := currentDataSharePayloadCodec()
	if err != nil {
		return nil, 0, err
	}
	compressed := codec.encoder.EncodeAll(data, nil)
	return compressed, int64(len(data)), nil
}

func decodeDataSharePayload(compressed []byte, encoding string) (map[string]any, error) {
	if len(compressed) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(encoding) != dataSharePayloadEncodingZstd {
		return nil, fmt.Errorf("unsupported data share payload encoding: %s", encoding)
	}
	codec, err := currentDataSharePayloadCodec()
	if err != nil {
		return nil, err
	}
	data, err := codec.decoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

type dataSharePayloadCodec struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

func currentDataSharePayloadCodec() (*dataSharePayloadCodec, error) {
	level := service.CurrentDataShareCompressionLevel()
	if cached, ok := dataSharePayloadCodecCache.Load(level); ok {
		codec, ok := cached.(*dataSharePayloadCodec)
		if !ok {
			return nil, fmt.Errorf("invalid cached data share payload codec for level %s", level)
		}
		return codec, nil
	}
	codec, err := newDataSharePayloadCodec(level)
	if err != nil {
		return nil, err
	}
	actual, _ := dataSharePayloadCodecCache.LoadOrStore(level, codec)
	if actual != codec {
		// 并发场景下丢弃未入缓存的临时 codec，避免后台资源残留。
		if err := codec.close(); err != nil {
			return nil, err
		}
	}
	cached, ok := actual.(*dataSharePayloadCodec)
	if !ok {
		return nil, fmt.Errorf("invalid cached data share payload codec for level %s", level)
	}
	return cached, nil
}

func newDataSharePayloadCodec(level string) (*dataSharePayloadCodec, error) {
	encoderLevel, err := dataShareZstdEncoderLevel(level)
	if err != nil {
		return nil, err
	}
	encoder, err := zstd.NewWriter(
		nil,
		zstd.WithEncoderLevel(encoderLevel),
	)
	if err != nil {
		return nil, err
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		// 构造 decoder 失败时清理已创建的 encoder，保留两个错误便于排查。
		return nil, errors.Join(err, encoder.Close())
	}
	return &dataSharePayloadCodec{encoder: encoder, decoder: decoder}, nil
}

func (c *dataSharePayloadCodec) close() error {
	if c == nil {
		return nil
	}
	if c.encoder != nil {
		if err := c.encoder.Close(); err != nil {
			return err
		}
	}
	if c.decoder != nil {
		c.decoder.Close()
	}
	return nil
}

func dataShareZstdEncoderLevel(level string) (zstd.EncoderLevel, error) {
	switch service.NormalizeDataShareCompressionLevel(level) {
	case string(service.DataShareCompressionLevelFastest):
		return zstd.SpeedFastest, nil
	case string(service.DataShareCompressionLevelDefault):
		return zstd.SpeedDefault, nil
	case string(service.DataShareCompressionLevelBetter):
		return zstd.SpeedBetterCompression, nil
	case string(service.DataShareCompressionLevelBest):
		return zstd.SpeedBestCompression, nil
	default:
		return zstd.SpeedFastest, nil
	}
}

func populateDataShareSessionPayload(session *service.DataShareSession) error {
	if session == nil {
		return nil
	}
	payload, err := decodeDataSharePayload(session.PayloadCompressed, session.PayloadEncoding)
	if err != nil {
		return err
	}
	if payload == nil {
		payload = dataShareSessionPayloadForPersist(session)
	}
	applyDataSharePayloadToSession(session, payload)
	return nil
}

func dataShareSessionPayloadForPersist(session *service.DataShareSession) map[string]any {
	if session == nil {
		return map[string]any{}
	}
	if session.SessionJSONFinalized && len(session.SessionJSON) > 0 {
		// 采集最终化已经构建了规范 payload，持久化阶段直接复用以避免大快照重复 compact。
		return session.SessionJSON
	}
	return service.BuildDataShareSessionPayload(session)
}

func applyDataSharePayloadToSession(session *service.DataShareSession, payload map[string]any) {
	if session == nil || payload == nil {
		return
	}
	session.SessionJSON = payload
	if session.SystemPrompt == nil {
		if prompt := strings.TrimSpace(stringFromRepositoryAny(payload["system_prompt"])); prompt != "" {
			session.SystemPrompt = &prompt
		}
	}
	if messages := mapsFromRepositoryAny(payload["messages"]); len(messages) > 0 {
		session.Messages = messages
	}
	if tools := mapsFromRepositoryAny(payload["tools"]); len(tools) > 0 {
		session.Tools = tools
	}
	if usage := mapFromRepositoryAny(payload["usage"]); len(usage) > 0 {
		session.Usage = usage
	}
	if meta := mapFromRepositoryAny(payload["meta"]); len(meta) > 0 {
		session.Meta = meta
	}
}

func (r *dataShareSessionRepository) persistCompressedPayload(ctx context.Context, session *service.DataShareSession) error {
	if session == nil || session.ID <= 0 {
		return nil
	}
	payload := dataShareSessionPayloadForPersist(session)
	compressed, payloadBytes, err := encodeDataSharePayload(payload)
	if err != nil {
		return err
	}
	_, err = clientFromContext(ctx, r.client).DataShareSession.Update().
		Where(datasharesession.IDEQ(session.ID)).
		ClearSystemPrompt().
		SetTools([]map[string]any{}).
		SetMessages([]map[string]any{}).
		SetUsage(map[string]any{}).
		SetMeta(map[string]any{}).
		SetSessionJSON(map[string]any{}).
		SetPayloadCompressed(compressed).
		SetPayloadEncoding(dataSharePayloadEncodingZstd).
		SetPayloadBytes(payloadBytes).
		SetStorageBytes(int64(len(compressed))).
		Save(ctx)
	if err != nil {
		return err
	}
	session.PayloadCompressed = compressed
	session.PayloadEncoding = dataSharePayloadEncodingZstd
	session.PayloadBytes = payloadBytes
	session.StorageBytes = int64(len(compressed))
	return nil
}

func mapFromRepositoryAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func mapsFromRepositoryAny(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return x
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func stringFromRepositoryAny(v any) string {
	if text, ok := v.(string); ok {
		return text
	}
	return ""
}

func mustRepositoryJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}
