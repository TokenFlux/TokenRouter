package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/usagestats"
)

type usageAnalyticsQuery struct {
	cte   string
	where string
	args  []any
}

// buildUsageAnalyticsQuery 生成“日表主体、小时边界、原始首尾”的无重叠查询源。
func (r *usageLogRepository) buildUsageAnalyticsQuery(ctx context.Context, filters UsageLogFilters, start, end time.Time, useDaily bool) (usageAnalyticsQuery, bool, error) {
	if filters.AccountID > 0 || strings.TrimSpace(filters.RequestID) != "" {
		return usageAnalyticsQuery{}, false, nil
	}
	modelSource := strings.TrimSpace(filters.ModelFilterSource)
	if strings.TrimSpace(filters.Model) != "" && modelSource != usagestats.ModelSourceRequested {
		return usageAnalyticsQuery{}, false, nil
	}
	window, ok, err := r.resolveUsageAnalyticsWindow(ctx, start, end)
	if err != nil || !ok {
		return usageAnalyticsQuery{}, false, err
	}

	args := []any{
		window.start, window.end, window.aggregateStart, window.aggregateEnd,
		window.rawTailStart,
	}
	if useDaily {
		// 小时边界与日期边界必须使用不同参数，防止日期类型污染小时范围比较。
		args = append(args, window.dailySplit().args()...)
	}
	rawUsageSource := "usage_logs ul"
	conditions := make([]string, 0, 10)
	addCondition := func(format string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(format, len(args)))
	}

	if filters.UserID > 0 {
		if filters.IncludeOwnedTeam {
			teamID, teamErr := r.getOwnedTeamID(ctx, filters.UserID)
			if teamErr != nil {
				return usageAnalyticsQuery{}, false, teamErr
			}
			args = append(args, filters.UserID)
			userPosition := len(args)
			if teamID > 0 {
				args = append(args, teamID)
				teamPosition := len(args)
				rawUsageSource = fmt.Sprintf(`(
						SELECT * FROM usage_logs WHERE user_id = $%d
						UNION ALL
						SELECT * FROM usage_logs WHERE team_id = $%d AND user_id <> $%d
					) ul`, userPosition, teamPosition, userPosition)
				conditions = append(conditions, fmt.Sprintf(
					"(user_id = $%d OR (team_id = $%d AND user_id <> $%d))",
					userPosition, teamPosition, userPosition,
				))
			} else {
				conditions = append(conditions, fmt.Sprintf("user_id = $%d", userPosition))
			}
		} else {
			addCondition("user_id = $%d", filters.UserID)
		}
	}
	if filters.APIKeyID > 0 {
		addCondition("api_key_id = $%d", filters.APIKeyID)
	}
	if filters.GroupID > 0 {
		addCondition("group_id = $%d", filters.GroupID)
	}
	if filters.TeamID > 0 {
		addCondition("team_id = $%d", filters.TeamID)
	}
	if filters.PersonalOnly {
		conditions = append(conditions, "team_id = 0")
	}
	if model := strings.TrimSpace(filters.Model); model != "" {
		addCondition("requested_model = $%d", model)
	}
	if filters.RequestType != nil {
		addCondition("request_type = $%d", *filters.RequestType)
	}
	if filters.Stream != nil {
		addCondition("stream = $%d", *filters.Stream)
	}
	if filters.BillingType != nil {
		addCondition("billing_type = $%d", int16(*filters.BillingType))
	}
	if mode := strings.TrimSpace(filters.BillingMode); mode != "" {
		addCondition("billing_mode = $%d", mode)
	}

	metricColumns := `
		user_id, billing_user_id, team_id, api_key_id, group_id, requested_model,
		request_type, stream, billing_type, billing_mode, platform, inbound_endpoint,
		total_requests, input_tokens, output_tokens, cache_creation_tokens,
		cache_read_tokens, total_cost, actual_cost, account_cost,
		total_duration_ms, duration_count`
	parts := make([]string, 0, 4)
	if useDaily {
		parts = append(parts, `
			SELECT bucket_date::timestamp AT TIME ZONE 'UTC' AS occurred_at, `+metricColumns+`
			FROM usage_analytics_daily
			WHERE bucket_date >= $8::date AND bucket_date < $9::date`)
	}
	hourlyRange := "bucket_start >= $3 AND bucket_start < $4"
	if useDaily {
		hourlyRange = "((bucket_start >= $3 AND bucket_start < $6) OR (bucket_start >= $7 AND bucket_start < $4))"
	}
	parts = append(parts, `
		SELECT bucket_start AS occurred_at, `+metricColumns+`
		FROM usage_analytics_hourly
		WHERE `+hourlyRange)
	parts = append(parts, `
		SELECT
			ul.created_at AS occurred_at,
			ul.user_id,
			COALESCE(ul.billing_user_id, ul.user_id),
			COALESCE(ul.team_id, 0),
			ul.api_key_id,
			COALESCE(ul.group_id, 0),
			-- 原始首尾区间必须与小时、日聚合使用同一内部请求模型口径。
			COALESCE(NULLIF(TRIM(ul.model), ''), NULLIF(TRIM(ul.requested_model), ''), ''),
			CASE
				WHEN COALESCE(ul.request_type, 0) <> 0 THEN ul.request_type
				WHEN COALESCE(ul.openai_ws_mode, FALSE) THEN 3
				WHEN COALESCE(ul.stream, FALSE) THEN 2
				ELSE 1
			END,
			COALESCE(ul.stream, FALSE),
			COALESCE(ul.billing_type, 0),
			COALESCE(NULLIF(ul.billing_mode, ''), CASE
				WHEN COALESCE(ul.video_duration_seconds, 0) > 0 THEN 'video'
				WHEN COALESCE(ul.image_count, 0) > 0 THEN 'image'
				ELSE 'token'
			END),
			COALESCE(NULLIF(g.platform, ''), a.platform, ''),
			COALESCE(ul.inbound_endpoint, ''),
			1, ul.input_tokens, ul.output_tokens, ul.cache_creation_tokens,
			ul.cache_read_tokens, ul.total_cost, ul.actual_cost,
			COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1),
			COALESCE(ul.duration_ms, 0), CASE WHEN ul.duration_ms IS NULL THEN 0 ELSE 1 END
		FROM `+rawUsageSource+`
		LEFT JOIN groups g ON g.id = ul.group_id
		LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE (ul.created_at >= $1 AND ul.created_at < $3)
		   OR (ul.created_at >= $5 AND ul.created_at < $2)`)

	return usageAnalyticsQuery{
		cte:   "WITH combined AS NOT MATERIALIZED (" + strings.Join(parts, " UNION ALL ") + ")",
		where: buildWhere(conditions),
		args:  args,
	}, true, nil
}

// getUsageStatsFromAnalytics 从组合聚合源计算摘要。
func (r *usageLogRepository) getUsageStatsFromAnalytics(ctx context.Context, filters UsageLogFilters) (*UsageStats, bool, error) {
	if filters.StartTime == nil || filters.EndTime == nil || !filters.EndTime.After(*filters.StartTime) {
		return nil, false, nil
	}
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, filters, *filters.StartTime, *filters.EndTime, true)
	if err != nil || !ok {
		return nil, false, err
	}
	stats := &UsageStats{}
	var totalDuration, durationCount int64
	var totalAccountCost float64
	err = scanSingleRow(ctx, r.sql, query.cte+`
		SELECT
			COALESCE(SUM(total_requests), 0), COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_tokens + cache_read_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0), COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0), COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(account_cost), 0), COALESCE(SUM(total_duration_ms), 0),
			COALESCE(SUM(duration_count), 0)
		FROM combined `+query.where, query.args,
		&stats.TotalRequests, &stats.TotalInputTokens, &stats.TotalOutputTokens,
		&stats.TotalCacheTokens, &stats.TotalCacheCreationTokens, &stats.TotalCacheReadTokens,
		&stats.TotalCost, &stats.TotalActualCost, &totalAccountCost,
		&totalDuration, &durationCount,
	)
	if err != nil {
		return nil, false, err
	}
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheTokens
	stats.TotalAccountCost = &totalAccountCost
	if durationCount > 0 {
		stats.AverageDurationMs = float64(totalDuration) / float64(durationCount)
	}
	return stats, true, nil
}

func (r *usageLogRepository) getUsageTrendFromAnalytics(ctx context.Context, start, end time.Time, granularity string, filters UsageLogFilters) ([]TrendDataPoint, bool, error) {
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, filters, start, end, false)
	if err != nil || !ok {
		return nil, false, err
	}
	query.args = append(query.args, resolveUsageStatsTimezone())
	timezonePosition := len(query.args)
	dateFormat := safeDateFormat(granularity)
	rows, err := r.sql.QueryContext(ctx, query.cte+fmt.Sprintf(`
		SELECT
			TO_CHAR(occurred_at AT TIME ZONE $%d, '%s'),
			COALESCE(SUM(total_requests), 0), COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0), COALESCE(SUM(actual_cost), 0)
		FROM combined %s
		GROUP BY 1 ORDER BY 1`, timezonePosition, dateFormat, query.where), query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	results, err := scanTrendRows(rows)
	return results, true, err
}

func (r *usageLogRepository) getModelStatsFromAnalytics(ctx context.Context, start, end time.Time, filters UsageLogFilters) ([]ModelStat, bool, error) {
	if usagestats.NormalizeModelSource(filters.ModelFilterSource) != usagestats.ModelSourceRequested {
		// 聚合表只有请求模型维度，上游模型和映射模型必须保留原始查询语义。
		return nil, false, nil
	}
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, filters, start, end, true)
	if err != nil || !ok {
		return nil, false, err
	}
	rows, err := r.sql.QueryContext(ctx, query.cte+`
		SELECT requested_model, COALESCE(SUM(total_requests), 0),
		       COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_creation_tokens), 0), COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0),
		       COALESCE(SUM(total_cost), 0), COALESCE(SUM(actual_cost), 0),
		       COALESCE(SUM(account_cost), 0)
		FROM combined `+query.where+`
		GROUP BY requested_model ORDER BY 7 DESC`, query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	results, err := scanModelStatsRows(rows)
	return results, true, err
}

func (r *usageLogRepository) getGroupStatsFromAnalytics(ctx context.Context, start, end time.Time, filters UsageLogFilters) ([]usagestats.GroupStat, bool, error) {
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, filters, start, end, true)
	if err != nil || !ok {
		return nil, false, err
	}
	rows, err := r.sql.QueryContext(ctx, query.cte+`
		SELECT c.group_id, COALESCE(g.name, ''), COALESCE(SUM(c.total_requests), 0),
		       COALESCE(SUM(c.input_tokens + c.output_tokens + c.cache_creation_tokens + c.cache_read_tokens), 0),
		       COALESCE(SUM(c.total_cost), 0), COALESCE(SUM(c.actual_cost), 0),
		       COALESCE(SUM(c.account_cost), 0)
		FROM combined c
		LEFT JOIN groups g ON g.id = NULLIF(c.group_id, 0) `+query.where+`
		GROUP BY c.group_id, g.name ORDER BY 4 DESC`, query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]usagestats.GroupStat, 0)
	for rows.Next() {
		var row usagestats.GroupStat
		if err := rows.Scan(&row.GroupID, &row.GroupName, &row.Requests, &row.TotalTokens, &row.Cost, &row.ActualCost, &row.AccountCost); err != nil {
			return nil, false, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return results, true, nil
}

func (r *usageLogRepository) getInboundEndpointStatsFromAnalytics(ctx context.Context, start, end time.Time, filters UsageLogFilters) ([]EndpointStat, bool, error) {
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, filters, start, end, true)
	if err != nil || !ok {
		return nil, false, err
	}
	rows, err := r.sql.QueryContext(ctx, query.cte+`
		SELECT COALESCE(NULLIF(TRIM(inbound_endpoint), ''), 'unknown'),
		       COALESCE(SUM(total_requests), 0),
		       COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0),
		       COALESCE(SUM(total_cost), 0), COALESCE(SUM(actual_cost), 0)
		FROM combined `+query.where+`
		GROUP BY 1 ORDER BY 2 DESC`, query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]EndpointStat, 0)
	for rows.Next() {
		var row EndpointStat
		if err := rows.Scan(&row.Endpoint, &row.Requests, &row.TotalTokens, &row.Cost, &row.ActualCost); err != nil {
			return nil, false, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return results, true, nil
}

// getAPIKeyUsageTrendFromAnalytics 从组合聚合源计算最活跃 API Key 趋势。
func (r *usageLogRepository) getAPIKeyUsageTrendFromAnalytics(ctx context.Context, start, end time.Time, granularity string, limit int) ([]APIKeyUsageTrendPoint, bool, error) {
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, start, end, false)
	if err != nil || !ok {
		return nil, false, err
	}
	if limit <= 0 {
		limit = 12
	}
	query.args = append(query.args, resolveUsageStatsTimezone(), limit)
	timezonePosition := len(query.args) - 1
	limitPosition := len(query.args)
	rows, err := r.sql.QueryContext(ctx, query.cte+fmt.Sprintf(`,
		top_keys AS (
			SELECT api_key_id
			FROM combined
			GROUP BY api_key_id
			ORDER BY SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) DESC
			LIMIT $%d
		)
		SELECT
			TO_CHAR(c.occurred_at AT TIME ZONE $%d, '%s'),
			c.api_key_id,
			COALESCE(k.name, ''),
			COALESCE(SUM(c.total_requests), 0),
			COALESCE(SUM(c.input_tokens + c.output_tokens + c.cache_creation_tokens + c.cache_read_tokens), 0)
		FROM combined c
		LEFT JOIN api_keys k ON k.id = c.api_key_id
		WHERE c.api_key_id IN (SELECT api_key_id FROM top_keys)
		GROUP BY 1, c.api_key_id, k.name
		ORDER BY 1 ASC, 5 DESC`, limitPosition, timezonePosition, safeDateFormat(granularity)), query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]APIKeyUsageTrendPoint, 0)
	for rows.Next() {
		var row APIKeyUsageTrendPoint
		if err := rows.Scan(&row.Date, &row.APIKeyID, &row.KeyName, &row.Requests, &row.Tokens); err != nil {
			return nil, false, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return results, true, nil
}

// getUserUsageTrendFromAnalytics 从组合聚合源计算最活跃用户趋势。
func (r *usageLogRepository) getUserUsageTrendFromAnalytics(ctx context.Context, start, end time.Time, granularity string, limit int) ([]UserUsageTrendPoint, bool, error) {
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, start, end, false)
	if err != nil || !ok {
		return nil, false, err
	}
	if limit <= 0 {
		limit = 12
	}
	query.args = append(query.args, resolveUsageStatsTimezone(), limit)
	timezonePosition := len(query.args) - 1
	limitPosition := len(query.args)
	rows, err := r.sql.QueryContext(ctx, query.cte+fmt.Sprintf(`,
		top_users AS (
			SELECT user_id
			FROM combined
			GROUP BY user_id
			ORDER BY SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) DESC
			LIMIT $%d
		)
		SELECT
			TO_CHAR(c.occurred_at AT TIME ZONE $%d, '%s'),
			c.user_id,
			COALESCE(u.email, ''),
			COALESCE(u.username, ''),
			COALESCE(SUM(c.total_requests), 0),
			COALESCE(SUM(c.input_tokens + c.output_tokens + c.cache_creation_tokens + c.cache_read_tokens), 0),
			COALESCE(SUM(c.total_cost), 0),
			COALESCE(SUM(c.actual_cost), 0)
		FROM combined c
		LEFT JOIN users u ON u.id = c.user_id
		WHERE c.user_id IN (SELECT user_id FROM top_users)
		GROUP BY 1, c.user_id, u.email, u.username
		ORDER BY 1 ASC, 6 DESC`, limitPosition, timezonePosition, safeDateFormat(granularity)), query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]UserUsageTrendPoint, 0)
	for rows.Next() {
		var row UserUsageTrendPoint
		if err := rows.Scan(&row.Date, &row.UserID, &row.Email, &row.Username, &row.Requests, &row.Tokens, &row.Cost, &row.ActualCost); err != nil {
			return nil, false, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return results, true, nil
}

// getUserSpendingRankingFromAnalytics 从组合聚合源计算用户消费排行。
func (r *usageLogRepository) getUserSpendingRankingFromAnalytics(ctx context.Context, start, end time.Time, limit int) (*UserSpendingRankingResponse, bool, error) {
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, start, end, true)
	if err != nil || !ok {
		return nil, false, err
	}
	query.args = append(query.args, limit)
	limitPosition := len(query.args)
	rows, err := r.sql.QueryContext(ctx, query.cte+fmt.Sprintf(`,
		user_spend AS (
			SELECT user_id,
			       COALESCE(SUM(actual_cost), 0) AS actual_cost,
			       COALESCE(SUM(total_requests), 0) AS requests,
			       COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS tokens
			FROM combined
			GROUP BY user_id
		),
		ranked AS (
			SELECT user_id, actual_cost, requests, tokens,
			       COALESCE(SUM(actual_cost) OVER (), 0) AS total_actual_cost,
			       COALESCE(SUM(requests) OVER (), 0) AS total_requests,
			       COALESCE(SUM(tokens) OVER (), 0) AS total_tokens
			FROM user_spend
			ORDER BY actual_cost DESC, tokens DESC, user_id ASC
			LIMIT $%d
		)
		SELECT r.user_id, COALESCE(u.email, ''), COALESCE(u.username, ''),
		       r.actual_cost, r.requests, r.tokens,
		       r.total_actual_cost, r.total_requests, r.total_tokens
		FROM ranked r
		LEFT JOIN users u ON u.id = r.user_id
		ORDER BY r.actual_cost DESC, r.tokens DESC, r.user_id ASC`, limitPosition), query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	result := &UserSpendingRankingResponse{Ranking: make([]UserSpendingRankingItem, 0)}
	for rows.Next() {
		var row UserSpendingRankingItem
		if err := rows.Scan(&row.UserID, &row.Email, &row.Username, &row.ActualCost, &row.Requests, &row.Tokens,
			&result.TotalActualCost, &result.TotalRequests, &result.TotalTokens); err != nil {
			return nil, false, err
		}
		result.Ranking = append(result.Ranking, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return result, true, nil
}

// getUsageRankingFromAnalytics 从组合聚合源计算公开用量排行。
func (r *usageLogRepository) getUsageRankingFromAnalytics(ctx context.Context, start, end time.Time, limit int) (*UsageRankingResponse, bool, error) {
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, start, end, true)
	if err != nil || !ok {
		return nil, false, err
	}
	query.args = append(query.args, limit)
	limitPosition := len(query.args)
	rows, err := r.sql.QueryContext(ctx, query.cte+fmt.Sprintf(`,
		user_usage AS (
			SELECT user_id,
			       COALESCE(SUM(total_requests), 0) AS requests,
			       COALESCE(SUM(input_tokens), 0) AS input_tokens,
			       COALESCE(SUM(output_tokens), 0) AS output_tokens,
			       COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
			       COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
			       COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS total_tokens,
			       COALESCE(SUM(actual_cost), 0) AS actual_cost
			FROM combined
			GROUP BY user_id
		),
		ranked AS (
			SELECT ROW_NUMBER() OVER (ORDER BY actual_cost DESC, total_tokens DESC, requests DESC, user_id ASC) AS rank,
			       user_id, requests, input_tokens, output_tokens, cache_creation_tokens,
			       cache_read_tokens, total_tokens, actual_cost,
			       COALESCE(SUM(requests) OVER (), 0) AS total_requests,
			       COALESCE(SUM(total_tokens) OVER (), 0) AS ranking_total_tokens,
			       COALESCE(SUM(actual_cost) OVER (), 0) AS total_actual_cost
			FROM user_usage
			ORDER BY actual_cost DESC, total_tokens DESC, requests DESC, user_id ASC
			LIMIT $%d
		)
		SELECT r.rank, r.user_id, COALESCE(u.email, ''), COALESCE(u.username, ''),
		       COALESCE(a.url, ''), r.requests, r.input_tokens, r.output_tokens,
		       r.cache_creation_tokens, r.cache_read_tokens, r.total_tokens, r.actual_cost,
		       r.total_requests, r.ranking_total_tokens, r.total_actual_cost
		FROM ranked r
		LEFT JOIN users u ON u.id = r.user_id
		LEFT JOIN user_avatars a ON a.user_id = r.user_id
		ORDER BY r.rank ASC`, limitPosition), query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	result := &UsageRankingResponse{Ranking: make([]UsageRankingItem, 0)}
	for rows.Next() {
		var row UsageRankingItem
		var email, username string
		if err := rows.Scan(
			&row.Rank, &row.UserID, &email, &username, &row.AvatarURL,
			&row.Requests, &row.InputTokens, &row.OutputTokens,
			&row.CacheCreationTokens, &row.CacheReadTokens, &row.TotalTokens,
			&row.ActualCost, &result.TotalRequests, &result.TotalTokens,
			&result.TotalActualCost,
		); err != nil {
			return nil, false, err
		}
		row.DisplayName = rankingDisplayName(username, email, row.UserID)
		result.Ranking = append(result.Ranking, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return result, true, nil
}

// getUserBreakdownStatsFromAnalytics 从组合聚合源计算可支持维度下的用户明细。
func (r *usageLogRepository) getUserBreakdownStatsFromAnalytics(ctx context.Context, start, end time.Time, dim usagestats.UserBreakdownDimension, limit int) ([]usagestats.UserBreakdownItem, bool, error) {
	modelSource := usagestats.NormalizeModelSource(dim.ModelType)
	if dim.AccountID > 0 || (dim.Model != "" && modelSource != usagestats.ModelSourceRequested) {
		return nil, false, nil
	}
	if dim.Endpoint != "" && dim.EndpointType != "" && dim.EndpointType != "inbound" {
		return nil, false, nil
	}
	filters := UsageLogFilters{
		UserID: dim.UserID, APIKeyID: dim.APIKeyID, GroupID: dim.GroupID,
		Model: dim.Model, ModelFilterSource: modelSource,
		RequestType: dim.RequestType, Stream: dim.Stream, BillingType: dim.BillingType,
	}
	query, ok, err := r.buildUsageAnalyticsQuery(ctx, filters, start, end, true)
	if err != nil || !ok {
		return nil, false, err
	}
	if dim.Endpoint != "" {
		query.args = append(query.args, dim.Endpoint)
		query.where = appendUsageAnalyticsCondition(query.where, fmt.Sprintf("inbound_endpoint = $%d", len(query.args)))
	}

	// 排序字段仅从固定列表中选择，不拼接外部任意输入。
	orderBy := "actual_cost"
	switch dim.SortBy {
	case "total_tokens", "input_tokens", "output_tokens", "cache_tokens", "requests", "cost", "actual_cost":
		orderBy = dim.SortBy
	}
	limitClause := ""
	if limit > 0 {
		query.args = append(query.args, limit)
		limitClause = fmt.Sprintf(" LIMIT $%d", len(query.args))
	}
	rows, err := r.sql.QueryContext(ctx, query.cte+`
		SELECT c.user_id, COALESCE(u.email, ''),
		       COALESCE(SUM(c.total_requests), 0) AS requests,
		       COALESCE(SUM(c.input_tokens), 0) AS input_tokens,
		       COALESCE(SUM(c.output_tokens), 0) AS output_tokens,
		       COALESCE(SUM(c.cache_creation_tokens + c.cache_read_tokens), 0) AS cache_tokens,
		       COALESCE(SUM(c.input_tokens + c.output_tokens + c.cache_creation_tokens + c.cache_read_tokens), 0) AS total_tokens,
		       COALESCE(SUM(c.total_cost), 0) AS cost,
		       COALESCE(SUM(c.actual_cost), 0) AS actual_cost,
		       COALESCE(SUM(c.account_cost), 0) AS account_cost
		FROM combined c
		LEFT JOIN users u ON u.id = c.user_id `+query.where+`
		GROUP BY c.user_id, u.email
		ORDER BY `+orderBy+` DESC`+limitClause, query.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]usagestats.UserBreakdownItem, 0)
	for rows.Next() {
		var row usagestats.UserBreakdownItem
		if err := rows.Scan(
			&row.UserID, &row.Email, &row.Requests, &row.InputTokens,
			&row.OutputTokens, &row.CacheTokens, &row.TotalTokens,
			&row.Cost, &row.ActualCost, &row.AccountCost,
		); err != nil {
			return nil, false, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return results, true, nil
}

// getAllGroupUsageSummaryFromAnalytics 分别使用日表主体和小时表计算累计与今日分组用量。
func (r *usageLogRepository) getAllGroupUsageSummaryFromAnalytics(ctx context.Context, todayStart time.Time) ([]usagestats.GroupUsageSummary, bool, error) {
	var sourceOldest sql.NullTime
	if err := scanSingleRow(ctx, r.sql, `
		SELECT source_oldest_at
		FROM usage_analytics_aggregation_state
		WHERE id = 1
	`, nil, &sourceOldest); err != nil {
		return nil, false, err
	}
	if !sourceOldest.Valid {
		return nil, false, nil
	}
	now := time.Now().UTC()
	totalQuery, ok, err := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, sourceOldest.Time, now, true)
	if err != nil || !ok {
		return nil, false, err
	}
	todayQuery, ok, err := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, todayStart, now, false)
	if err != nil || !ok {
		return nil, false, err
	}

	rows, err := r.sql.QueryContext(ctx, totalQuery.cte+`
		SELECT g.id, COALESCE(SUM(c.actual_cost), 0)
		FROM groups g
		LEFT JOIN combined c ON c.group_id = g.id
		GROUP BY g.id
	`, totalQuery.args...)
	if err != nil {
		return nil, false, err
	}
	results := make([]usagestats.GroupUsageSummary, 0)
	byID := make(map[int64]int)
	for rows.Next() {
		var row usagestats.GroupUsageSummary
		if err := rows.Scan(&row.GroupID, &row.TotalCost); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		byID[row.GroupID] = len(results)
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}

	todayRows, err := r.sql.QueryContext(ctx, todayQuery.cte+`
		SELECT group_id, COALESCE(SUM(actual_cost), 0)
		FROM combined
		GROUP BY group_id
	`, todayQuery.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = todayRows.Close() }()
	for todayRows.Next() {
		var groupID int64
		var todayCost float64
		if err := todayRows.Scan(&groupID, &todayCost); err != nil {
			return nil, false, err
		}
		if position, exists := byID[groupID]; exists {
			results[position].TodayCost = todayCost
		}
	}
	if err := todayRows.Err(); err != nil {
		return nil, false, err
	}
	return results, true, nil
}

func appendUsageAnalyticsCondition(where, condition string) string {
	if where == "" {
		return "WHERE " + condition
	}
	return where + " AND " + condition
}
