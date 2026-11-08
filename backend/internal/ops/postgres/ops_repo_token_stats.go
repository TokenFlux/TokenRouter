package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// ops_repo_token_stats.go queries model token statistics with ranked or
// paginated results and nullable latency measurements.
// Model rankings count saved usage rows by execution model while token-rate and
// TTFT measurements use different sample sets. TPS requires complete streaming
// usage and a positive interval from attempt start to last useful content.
// Rows without an estimate still contribute to request and output-token totals.
// TTFT uses each row's captured event mode and includes measured zero latency.
//
// The grouped statistics CTE supplies both the ranked selection and the model
// total in one statement. A left join keeps that total available when
// pagination selects no model, with has_item distinguishing the synthetic row
// from actual items. Nullable rate and TTFT averages remain absent in
// the response, so callers can distinguish missing measurements from recorded
// zero. Repository calls expect pagination already normalized by OpsService.

// GetTokenStats returns raw usage statistics grouped by saved execution model,
// ordered by descending request count and then ascending model. Nonzero ordered
// endpoints are required, equality is accepted, and platform is trimmed and
// lowercased. OpsService supplies positive pagination values or a positive
// TopN; direct repository calls receive no limit normalization. GroupID is
// retained in the response, while only a positive value adds a SQL predicate.
// Callers treat that pointed-to identity as read-only while sharing the result.
//
// The total counts all grouped models before limiting. A synthetic row
// preserves that total for an empty page and is excluded from Items, whose
// empty slice remains available. Token rate averages the usage-derived TPS
// estimates, latency averages nonnull TTFT values, and nullable averages become
// nil pointers. Query and scan failures reject the
// response; the supplied filter is read without mutation.
func (r *Store) GetTokenStats(ctx context.Context, filter *ops.OpsTokenStatsFilter) (*ops.OpsTokenStatsResponse, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		return nil, fmt.Errorf("nil filter")
	}
	if filter.StartTime.IsZero() || filter.EndTime.IsZero() {
		return nil, fmt.Errorf("start_time/end_time required")
	}
	// An empty time range is valid, matching service validation.
	if filter.StartTime.After(filter.EndTime) {
		return nil, fmt.Errorf("start_time must be <= end_time")
	}

	dashboardFilter := &ops.OpsDashboardFilter{
		StartTime: filter.StartTime.UTC(),
		EndTime:   filter.EndTime.UTC(),
		Platform:  strings.TrimSpace(strings.ToLower(filter.Platform)),
		GroupID:   filter.GroupID,
	}

	join, where, baseArgs, next := buildUsageWhere(dashboardFilter, dashboardFilter.StartTime, dashboardFilter.EndTime, 1)

	// Shared expressions apply the same average-speed and captured-mode rules as
	// usage DTOs. SQL averages their eligible samples independently, rounding rate
	// and TTFT means to two decimals and duration means to whole milliseconds.
	baseCTE := `
WITH stats AS (
  SELECT
    ul.model AS model,
    COUNT(*)::bigint AS request_count,
    ROUND(
      AVG(
        ` + usageTPSSQL("ul") + `
      )::numeric,
      2
    )::float8 AS avg_tokens_per_sec,
    ROUND(AVG(` + usageTTFTSQL("ul") + `)::numeric, 2)::float8 AS avg_ttft_ms,
    COALESCE(SUM(ul.output_tokens), 0)::bigint AS total_output_tokens,
    COALESCE(ROUND(AVG(ul.duration_ms)::numeric, 0), 0)::bigint AS avg_duration_ms,
    COUNT(CASE WHEN ` + usageTTFTSQL("ul") + ` IS NOT NULL THEN 1 END)::bigint AS requests_with_first_token
  FROM (SELECT * FROM usage_logs WHERE request_source = 'user') ul
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
  avg_ttft_ms,
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

	// Read the complete model count from the same statistics as the page. LEFT
	// JOIN supplies one sentinel row when the page contains no model.
	querySQL += `
)
SELECT
  p.model,
  p.request_count,
  p.avg_tokens_per_sec,
  p.avg_ttft_ms,
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
	// Close the cursor after scanning or an early failure; cleanup errors are
	// suppressed, while query, scan, and iteration errors determine the response.
	defer func() { _ = rows.Close() }()

	items := make([]*ops.OpsTokenStatsItem, 0, 32)
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
		// The left-join sentinel carries the total for an empty page. Its nullable
		// item columns are discarded before constructing a model entry.
		if !hasItem {
			continue
		}

		item := &ops.OpsTokenStatsItem{
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
			item.AvgTTFTMs = &v
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	resp := &ops.OpsTokenStatsResponse{
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
