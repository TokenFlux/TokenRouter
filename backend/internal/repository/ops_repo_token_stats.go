package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/service"
)

func (r *opsRepository) GetTokenStats(ctx context.Context, filter *service.OpsTokenStatsFilter) (*service.OpsTokenStatsResponse, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		return nil, fmt.Errorf("nil filter")
	}
	if filter.StartTime.IsZero() || filter.EndTime.IsZero() {
		return nil, fmt.Errorf("start_time/end_time required")
	}
	// 允许 start_time == end_time（结果为空），与 service 层校验口径保持一致。
	if filter.StartTime.After(filter.EndTime) {
		return nil, fmt.Errorf("start_time must be <= end_time")
	}

	dashboardFilter := &service.OpsDashboardFilter{
		StartTime: filter.StartTime.UTC(),
		EndTime:   filter.EndTime.UTC(),
		Platform:  strings.TrimSpace(strings.ToLower(filter.Platform)),
		GroupID:   filter.GroupID,
	}

	join, where, baseArgs, next := buildUsageWhere(dashboardFilter, dashboardFilter.StartTime, dashboardFilter.EndTime, 1)

	baseCTE := `
WITH stats AS (
  SELECT
    ul.model AS model,
    COUNT(*)::bigint AS request_count,
    ROUND(
      AVG(
        CASE
          WHEN ul.duration_ms > 0 AND ul.output_tokens > 0
          THEN ul.output_tokens * 1000.0 / ul.duration_ms
        END
      )::numeric,
      2
    )::float8 AS avg_tokens_per_sec,
    ROUND(AVG(ul.first_token_ms)::numeric, 2)::float8 AS avg_first_token_ms,
    COALESCE(SUM(ul.output_tokens), 0)::bigint AS total_output_tokens,
    COALESCE(ROUND(AVG(ul.duration_ms)::numeric, 0), 0)::bigint AS avg_duration_ms,
    COUNT(CASE WHEN ul.first_token_ms IS NOT NULL THEN 1 END)::bigint AS requests_with_first_token
  FROM usage_logs ul
  ` + join + `
  ` + where + `
  GROUP BY ul.model
)
`

	querySQL := baseCTE + `
, paged AS (
SELECT
  model,
  request_count,
  avg_tokens_per_sec,
  avg_first_token_ms,
  total_output_tokens,
  avg_duration_ms,
  requests_with_first_token
FROM stats
ORDER BY request_count DESC, model ASC`

	args := make([]any, 0, len(baseArgs)+2)
	args = append(args, baseArgs...)

	if filter.IsTopNMode() {
		querySQL += fmt.Sprintf("\nLIMIT $%d", next)
		args = append(args, filter.TopN)
	} else {
		offset := (filter.Page - 1) * filter.PageSize
		querySQL += fmt.Sprintf("\nLIMIT $%d OFFSET $%d", next, next+1)
		args = append(args, filter.PageSize, offset)
	}

	// 聚合结果只计算一次；LEFT JOIN 保证空页仍能回传模型总数。
	querySQL += `
)
SELECT
  p.model,
  p.request_count,
  p.avg_tokens_per_sec,
  p.avg_first_token_ms,
  p.total_output_tokens,
  p.avg_duration_ms,
  p.requests_with_first_token,
  (p.request_count IS NOT NULL) AS has_item,
  totals.total
FROM (SELECT COUNT(*)::bigint AS total FROM stats) totals
LEFT JOIN paged p ON TRUE
ORDER BY p.request_count DESC NULLS LAST, p.model ASC`

	rows, err := r.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]*service.OpsTokenStatsItem, 0, 32)
	var total int64
	for rows.Next() {
		var model sql.NullString
		var requestCount sql.NullInt64
		var avgTPS sql.NullFloat64
		var avgFirstToken sql.NullFloat64
		var totalOutputTokens sql.NullInt64
		var avgDurationMs sql.NullInt64
		var requestsWithFirstToken sql.NullInt64
		var hasItem bool
		var rowTotal int64
		if err := rows.Scan(
			&model,
			&requestCount,
			&avgTPS,
			&avgFirstToken,
			&totalOutputTokens,
			&avgDurationMs,
			&requestsWithFirstToken,
			&hasItem,
			&rowTotal,
		); err != nil {
			return nil, err
		}
		total = rowTotal
		if !hasItem {
			continue
		}

		item := &service.OpsTokenStatsItem{
			Model:                  model.String,
			RequestCount:           requestCount.Int64,
			TotalOutputTokens:      totalOutputTokens.Int64,
			AvgDurationMs:          avgDurationMs.Int64,
			RequestsWithFirstToken: requestsWithFirstToken.Int64,
		}
		if avgTPS.Valid {
			v := avgTPS.Float64
			item.AvgTokensPerSec = &v
		}
		if avgFirstToken.Valid {
			v := avgFirstToken.Float64
			item.AvgFirstTokenMs = &v
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	resp := &service.OpsTokenStatsResponse{
		TimeRange: strings.TrimSpace(filter.TimeRange),
		StartTime: dashboardFilter.StartTime,
		EndTime:   dashboardFilter.EndTime,
		Platform:  dashboardFilter.Platform,
		GroupID:   dashboardFilter.GroupID,
		Items:     items,
		Total:     total,
	}
	if filter.IsTopNMode() {
		topN := filter.TopN
		resp.TopN = &topN
	} else {
		resp.Page = filter.Page
		resp.PageSize = filter.PageSize
	}
	return resp, nil
}
