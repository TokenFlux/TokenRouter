package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TrendDataPoint represents a single point in trend data
type TrendDataPoint = usage.TrendDataPoint

// ModelStat represents usage statistics for a single model
type ModelStat = usage.ModelStat

// UserUsageTrendPoint represents user usage trend data point
type UserUsageTrendPoint = usage.UserUsageTrendPoint

// UserSpendingRankingItem represents a user spending ranking row.
type UserSpendingRankingItem = usage.UserSpendingRankingItem
type UserSpendingRankingResponse = usage.UserSpendingRankingResponse

// APIKeyUsageTrendPoint represents API key usage trend data point
type APIKeyUsageTrendPoint = usage.APIKeyUsageTrendPoint

// GetAPIKeyUsageTrend returns usage trend data grouped by API key and date
func (r *Store) GetAPIKeyUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) (results []APIKeyUsageTrendPoint, err error) {
	if aggregated, ok, aggregateErr := r.getAPIKeyUsageTrendFromAnalytics(ctx, startTime, endTime, granularity, limit); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("api_key_usage_trend", aggregateErr)
	}
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		WITH top_keys AS (
			SELECT api_key_id
			FROM usage_logs
			WHERE created_at >= $1 AND created_at < $2
			GROUP BY api_key_id
			ORDER BY SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) DESC
			LIMIT $3
		)
		SELECT
			TO_CHAR(u.created_at, '%s') as date,
			u.api_key_id,
			COALESCE(k.name, '') as key_name,
			COUNT(*) as requests,
			COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as tokens
		FROM usage_logs u
		LEFT JOIN api_keys k ON u.api_key_id = k.id
		WHERE u.api_key_id IN (SELECT api_key_id FROM top_keys)
		  AND u.created_at >= $4 AND u.created_at < $5
		GROUP BY date, u.api_key_id, k.name
		ORDER BY date ASC, tokens DESC
	`, dateFormat)

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]APIKeyUsageTrendPoint, 0)
	for rows.Next() {
		var row APIKeyUsageTrendPoint
		if err = rows.Scan(&row.Date, &row.APIKeyID, &row.KeyName, &row.Requests, &row.Tokens); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// GetUserUsageTrend 返回按付款主体和日期聚合的最活跃用户趋势。
func (r *Store) GetUserUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) (results []UserUsageTrendPoint, err error) {
	if aggregated, ok, aggregateErr := r.getUserUsageTrendFromAnalytics(ctx, startTime, endTime, granularity, limit); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("user_usage_trend", aggregateErr)
	}
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		WITH top_users AS (
			SELECT COALESCE(billing_user_id, user_id) AS user_id
			FROM usage_logs
			WHERE created_at >= $1 AND created_at < $2
			-- 团队成员使用团队 Key 时，Top 用户应按付款主体合并用量。
			GROUP BY COALESCE(billing_user_id, user_id)
			ORDER BY SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) DESC
			LIMIT $3
		)
		SELECT
			TO_CHAR(u.created_at, '%s') as date,
			COALESCE(u.billing_user_id, u.user_id) AS user_id,
			COALESCE(us.email, '') as email,
			COALESCE(us.username, '') as username,
			COUNT(*) as requests,
			COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as tokens,
			COALESCE(SUM(u.total_cost), 0) as cost,
			COALESCE(SUM(u.actual_cost), 0) as actual_cost
		FROM usage_logs u
		LEFT JOIN users us ON COALESCE(u.billing_user_id, u.user_id) = us.id
		WHERE COALESCE(u.billing_user_id, u.user_id) IN (SELECT user_id FROM top_users)
		  AND u.created_at >= $4 AND u.created_at < $5
		GROUP BY date, COALESCE(u.billing_user_id, u.user_id), us.email, us.username
		ORDER BY date ASC, tokens DESC
	`, dateFormat)

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]UserUsageTrendPoint, 0)
	for rows.Next() {
		var row UserUsageTrendPoint
		if err = rows.Scan(&row.Date, &row.UserID, &row.Email, &row.Username, &row.Requests, &row.Tokens, &row.Cost, &row.ActualCost); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// GetUserSpendingRanking 返回按付款主体聚合的消费排行；团队成员用团队 Key 的用量归到 Owner。
func (r *Store) GetUserSpendingRanking(ctx context.Context, startTime, endTime time.Time, limit int) (result *UserSpendingRankingResponse, err error) {
	if limit <= 0 {
		limit = 12
	}
	if aggregated, ok, aggregateErr := r.getUserSpendingRankingFromAnalytics(ctx, startTime, endTime, limit); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("user_spending_ranking", aggregateErr)
	}

	query := `
		WITH user_spend AS (
			SELECT
				COALESCE(u.billing_user_id, u.user_id) as user_id,
				COALESCE(us.email, '') as email,
				COALESCE(us.username, '') as username,
				COALESCE(SUM(u.actual_cost), 0) as actual_cost,
				COUNT(*) as requests,
				COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as tokens
			FROM usage_logs u
			LEFT JOIN users us ON COALESCE(u.billing_user_id, u.user_id) = us.id
			WHERE u.created_at >= $1 AND u.created_at < $2
			-- 排行按付款主体归属，团队成员使用团队 Key 时计入团队 Owner。
			GROUP BY COALESCE(u.billing_user_id, u.user_id), us.email, us.username
		),
		ranked AS (
			SELECT
				user_id,
				email,
				username,
				actual_cost,
				requests,
				tokens,
				COALESCE(SUM(actual_cost) OVER (), 0) as total_actual_cost,
				COALESCE(SUM(requests) OVER (), 0) as total_requests,
				COALESCE(SUM(tokens) OVER (), 0) as total_tokens
			FROM user_spend
			ORDER BY actual_cost DESC, tokens DESC, user_id ASC
			LIMIT $3
		)
		SELECT
			user_id,
			email,
			username,
			actual_cost,
			requests,
			tokens,
			total_actual_cost,
			total_requests,
			total_tokens
		FROM ranked
		ORDER BY actual_cost DESC, tokens DESC, user_id ASC
	`

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			result = nil
		}
	}()

	ranking := make([]UserSpendingRankingItem, 0)
	totalActualCost := 0.0
	totalRequests := int64(0)
	totalTokens := int64(0)
	for rows.Next() {
		var row UserSpendingRankingItem
		if err = rows.Scan(&row.UserID, &row.Email, &row.Username, &row.ActualCost, &row.Requests, &row.Tokens, &totalActualCost, &totalRequests, &totalTokens); err != nil {
			return nil, err
		}
		ranking = append(ranking, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return &UserSpendingRankingResponse{
		Ranking:         ranking,
		TotalActualCost: totalActualCost,
		TotalRequests:   totalRequests,
		TotalTokens:     totalTokens,
	}, nil
}

// GetUserUsageTrendByUserID 获取指定用户的使用趋势
func (r *Store) GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) (results []TrendDataPoint, err error) {
	return r.getUsageTrendWithFilters(ctx, startTime, endTime, granularity, userID, 0, 0, 0, 0, "", "", nil, nil, nil, "", false, true, nil)
}

// GetUserModelStats 获取指定用户的模型统计
func (r *Store) GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, 0, 0, 0, 0, "", nil, nil, nil, usage.ModelSourceRequested, "", false, true, nil)
}

// GetUsageTrendWithFilters returns usage trend data with optional filters
func (r *Store) GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) (results []TrendDataPoint, err error) {
	return r.getUsageTrendWithFilters(ctx, startTime, endTime, granularity, userID, apiKeyID, accountID, groupID, 0, model, "", requestType, stream, billingType, "", false, false, nil)
}

func (r *Store) GetUsageTrendWithUsageFilters(ctx context.Context, startTime, endTime time.Time, granularity string, filters UsageLogFilters) (results []TrendDataPoint, err error) {
	return r.getUsageTrendWithFilters(ctx, startTime, endTime, granularity, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.TeamID, filters.Model, filters.ModelFilterSource, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.PersonalOnly, filters.IncludeOwnedTeam, filters.NativeCompactionV2)
}

func (r *Store) getUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID, teamID int64, model string, modelSource string, requestType *int16, stream *bool, billingType *int8, billingMode string, personalOnly bool, includeOwnedTeam bool, nativeCompactionV2 *bool) (results []TrendDataPoint, err error) {
	analyticsFilters := UsageLogFilters{
		UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID,
		TeamID: teamID, Model: model, ModelFilterSource: modelSource,
		RequestType: requestType, Stream: stream, BillingType: billingType,
		BillingMode: billingMode, PersonalOnly: personalOnly, IncludeOwnedTeam: includeOwnedTeam,
		NativeCompactionV2: nativeCompactionV2,
	}
	if aggregated, ok, aggregateErr := r.getUsageTrendFromAnalytics(ctx, startTime, endTime, granularity, analyticsFilters); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("usage_trend", aggregateErr)
	}
	if teamID == 0 && shouldUsePreaggregatedTrend(granularity, userID, apiKeyID, accountID, groupID, model, requestType, stream, billingType, billingMode, personalOnly, includeOwnedTeam, nativeCompactionV2) {
		aggregated, aggregatedErr := r.getUsageTrendFromAggregates(ctx, startTime, endTime, granularity)
		if aggregatedErr == nil && len(aggregated) > 0 {
			return aggregated, nil
		}
	}

	dateFormat := safeDateFormat(granularity)
	args := []any{startTime, endTime}
	source, scopeCondition, args, scopeErr := r.buildUsageLogScopeSource(ctx, args, userID, includeOwnedTeam, "")
	if scopeErr != nil {
		return nil, scopeErr
	}

	query := fmt.Sprintf(`
		SELECT
			TO_CHAR(created_at, '%s') as date,
			COUNT(*) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(total_cost), 0) as cost,
			COALESCE(SUM(actual_cost), 0) as actual_cost
		FROM %s
		WHERE created_at >= $1 AND created_at < $2
	`, dateFormat, source)

	if scopeCondition != "" {
		query += " AND " + scopeCondition
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND api_key_id = $%d", len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND group_id = $%d", len(args)+1)
		args = append(args, groupID)
	}
	if teamID > 0 {
		query += fmt.Sprintf(" AND team_id = $%d", len(args)+1)
		args = append(args, teamID)
	}
	if personalOnly {
		query += " AND team_id IS NULL"
	}
	query, args = appendUsageLogModelQueryFilter(query, args, model, modelSource)
	query, args = appendRequestTypeOrStreamQueryFilter(query, args, requestType, stream)
	query, args = appendNativeCompactionV2QueryFilter(query, args, nativeCompactionV2, "")
	if billingType != nil {
		query += fmt.Sprintf(" AND billing_type = $%d", len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, "")
	query += " GROUP BY date ORDER BY date ASC"

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func shouldUsePreaggregatedTrend(granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8, billingMode string, personalOnly bool, includeOwnedTeam bool, nativeCompactionV2 *bool) bool {
	if granularity != "day" && granularity != "hour" {
		return false
	}
	return !personalOnly &&
		!includeOwnedTeam &&
		userID == 0 &&
		apiKeyID == 0 &&
		accountID == 0 &&
		groupID == 0 &&
		model == "" &&
		requestType == nil &&
		stream == nil &&
		billingType == nil &&
		billingMode == "" &&
		nativeCompactionV2 == nil
}

func (r *Store) getUsageTrendFromAggregates(ctx context.Context, startTime, endTime time.Time, granularity string) (results []TrendDataPoint, err error) {
	if r.preAggregation != nil && !r.preAggregation.UsageEnabled(ctx) {
		return nil, nil
	}
	dateFormat := safeDateFormat(granularity)
	query := ""
	args := []any{startTime, endTime}

	switch granularity {
	case "hour":
		query = fmt.Sprintf(`
			SELECT
				TO_CHAR(bucket_start, '%s') as date,
				total_requests as requests,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) as total_tokens,
				total_cost as cost,
				actual_cost
			FROM usage_dashboard_hourly
			WHERE bucket_start >= $1 AND bucket_start < $2
			ORDER BY bucket_start ASC
		`, dateFormat)
	case "day":
		query = fmt.Sprintf(`
			SELECT
				TO_CHAR(bucket_date::timestamp, '%s') as date,
				total_requests as requests,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) as total_tokens,
				total_cost as cost,
				actual_cost
			FROM usage_dashboard_daily
			WHERE bucket_date >= $1::date AND bucket_date < $2::date
			ORDER BY bucket_date ASC
		`, dateFormat)
	default:
		return nil, nil
	}

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetModelStatsWithFilters returns model statistics with optional filters
func (r *Store) GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, 0, "", requestType, stream, billingType, usage.ModelSourceRequested, "", false, false, nil)
}

// GetModelStatsWithFiltersBySource returns model statistics with optional filters and model source dimension.
// source: requested | upstream | mapping.
func (r *Store) GetModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8, source string) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, 0, "", requestType, stream, billingType, source, "", false, false, nil)
}

func (r *Store) GetModelStatsWithUsageFiltersBySource(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters, source string) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.TeamID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType, source, filters.BillingMode, filters.PersonalOnly, filters.IncludeOwnedTeam, filters.NativeCompactionV2)
}

func (r *Store) getModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID, teamID int64, model string, requestType *int16, stream *bool, billingType *int8, source string, billingMode string, personalOnly bool, includeOwnedTeam bool, nativeCompactionV2 *bool) (results []ModelStat, err error) {
	analyticsFilters := UsageLogFilters{
		UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID,
		TeamID: teamID, Model: model, ModelFilterSource: source,
		RequestType: requestType, Stream: stream, BillingType: billingType,
		BillingMode: billingMode, PersonalOnly: personalOnly, IncludeOwnedTeam: includeOwnedTeam,
		NativeCompactionV2: nativeCompactionV2,
	}
	if aggregated, ok, aggregateErr := r.getModelStatsFromAnalytics(ctx, startTime, endTime, analyticsFilters); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("model_stats", aggregateErr)
	}
	// “实际”始终表示用户实际扣费，账号成本由独立的 account_cost 字段承载。
	accountCostExpr := "COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0) as account_cost"
	modelExpr := resolveModelDimensionExpression(source)
	args := []any{startTime, endTime}
	usageSource, scopeCondition, args, scopeErr := r.buildUsageLogScopeSource(ctx, args, userID, includeOwnedTeam, "")
	if scopeErr != nil {
		return nil, scopeErr
	}

	query := fmt.Sprintf(`
		SELECT
			%s as model,
			COUNT(*) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(total_cost), 0) as cost,
			COALESCE(SUM(actual_cost), 0) as actual_cost,
			%s
		FROM %s
		WHERE created_at >= $1 AND created_at < $2
	`, modelExpr, accountCostExpr, usageSource)

	if scopeCondition != "" {
		query += " AND " + scopeCondition
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND api_key_id = $%d", len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND group_id = $%d", len(args)+1)
		args = append(args, groupID)
	}
	if teamID > 0 {
		query += fmt.Sprintf(" AND team_id = $%d", len(args)+1)
		args = append(args, teamID)
	}
	if personalOnly {
		query += " AND team_id IS NULL"
	}
	if strings.TrimSpace(model) != "" {
		query += fmt.Sprintf(" AND %s = $%d", modelExpr, len(args)+1)
		args = append(args, model)
	}
	query, args = appendRequestTypeOrStreamQueryFilter(query, args, requestType, stream)
	query, args = appendNativeCompactionV2QueryFilter(query, args, nativeCompactionV2, "")
	if billingType != nil {
		query += fmt.Sprintf(" AND billing_type = $%d", len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, "")
	query += fmt.Sprintf(" GROUP BY %s ORDER BY total_tokens DESC", modelExpr)

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanModelStatsRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetGroupStatsWithFilters returns group usage statistics with optional filters
func (r *Store) GetGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) (results []usage.GroupStat, err error) {
	return r.getGroupStatsWithFilters(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, 0, "", requestType, stream, billingType, "", false, false, nil)
}

func (r *Store) GetGroupStatsWithUsageFilters(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters) (results []usage.GroupStat, err error) {
	return r.getGroupStatsWithFilters(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.TeamID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.PersonalOnly, filters.IncludeOwnedTeam, filters.NativeCompactionV2)
}

func (r *Store) getGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID, teamID int64, model string, requestType *int16, stream *bool, billingType *int8, billingMode string, personalOnly bool, includeOwnedTeam bool, nativeCompactionV2 *bool) (results []usage.GroupStat, err error) {
	analyticsFilters := UsageLogFilters{
		UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID,
		TeamID: teamID, Model: model, ModelFilterSource: usage.ModelSourceRequested,
		RequestType: requestType, Stream: stream, BillingType: billingType,
		BillingMode: billingMode, PersonalOnly: personalOnly, IncludeOwnedTeam: includeOwnedTeam,
		NativeCompactionV2: nativeCompactionV2,
	}
	if aggregated, ok, aggregateErr := r.getGroupStatsFromAnalytics(ctx, startTime, endTime, analyticsFilters); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("group_stats", aggregateErr)
	}
	args := []any{startTime, endTime}
	usageSource, scopeCondition, args, scopeErr := r.buildUsageLogScopeSource(ctx, args, userID, includeOwnedTeam, "ul")
	if scopeErr != nil {
		return nil, scopeErr
	}
	query := fmt.Sprintf(`
		SELECT
			COALESCE(ul.group_id, 0) as group_id,
			COALESCE(g.name, '') as group_name,
			COUNT(*) as requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.total_cost), 0) as cost,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost,
			COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1)), 0) as account_cost
		FROM %s
		LEFT JOIN groups g ON g.id = ul.group_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
	`, usageSource)

	if scopeCondition != "" {
		query += " AND " + scopeCondition
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND ul.api_key_id = $%d", len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND ul.account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND ul.group_id = $%d", len(args)+1)
		args = append(args, groupID)
	}
	if teamID > 0 {
		query += fmt.Sprintf(" AND ul.team_id = $%d", len(args)+1)
		args = append(args, teamID)
	}
	if personalOnly {
		query += " AND ul.team_id IS NULL"
	}
	if strings.TrimSpace(model) != "" {
		modelExpr := resolveModelDimensionExpressionWithAlias(usage.ModelSourceRequested, "ul")
		query += fmt.Sprintf(" AND %s = $%d", modelExpr, len(args)+1)
		args = append(args, model)
	}
	query, args = appendRequestTypeOrStreamQueryFilter(query, args, requestType, stream)
	query, args = appendNativeCompactionV2QueryFilter(query, args, nativeCompactionV2, "ul")
	if billingType != nil {
		query += fmt.Sprintf(" AND ul.billing_type = $%d", len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, "ul")
	query += " GROUP BY ul.group_id, g.name ORDER BY total_tokens DESC"

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usage.GroupStat, 0)
	for rows.Next() {
		var row usage.GroupStat
		if err := rows.Scan(
			&row.GroupID,
			&row.GroupName,
			&row.Requests,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetUserBreakdownStats 返回按付款主体聚合的管理员用量排行明细。
func (r *Store) GetUserBreakdownStats(ctx context.Context, startTime, endTime time.Time, dim usage.UserBreakdownDimension, limit int) (results []usage.UserBreakdownItem, err error) {
	if aggregated, ok, aggregateErr := r.getUserBreakdownStatsFromAnalytics(ctx, startTime, endTime, dim, limit); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("user_breakdown_stats", aggregateErr)
	}
	query := `
		SELECT
			COALESCE(ul.billing_user_id, ul.user_id, 0) as user_id,
			COALESCE(u.email, '') as email,
			COUNT(*) as requests,
			COALESCE(SUM(ul.input_tokens), 0) as input_tokens,
			COALESCE(SUM(ul.output_tokens), 0) as output_tokens,
			COALESCE(SUM(ul.cache_creation_tokens + ul.cache_read_tokens), 0) as cache_tokens,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.total_cost), 0) as cost,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost,
			COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1)), 0) as account_cost
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = COALESCE(ul.billing_user_id, ul.user_id)
		WHERE ul.created_at >= $1 AND ul.created_at < $2
	`
	args := []any{startTime, endTime}

	if dim.GroupID > 0 {
		query += fmt.Sprintf(" AND ul.group_id = $%d", len(args)+1)
		args = append(args, dim.GroupID)
	}
	if dim.Model != "" {
		query += fmt.Sprintf(" AND %s = $%d", resolveModelDimensionExpression(dim.ModelType), len(args)+1)
		args = append(args, dim.Model)
	}
	if dim.Endpoint != "" {
		col := resolveEndpointColumn(dim.EndpointType)
		query += fmt.Sprintf(" AND %s = $%d", col, len(args)+1)
		args = append(args, dim.Endpoint)
	}
	if dim.UserID > 0 {
		query += fmt.Sprintf(" AND ul.user_id = $%d", len(args)+1)
		args = append(args, dim.UserID)
	}
	if dim.APIKeyID > 0 {
		query += fmt.Sprintf(" AND ul.api_key_id = $%d", len(args)+1)
		args = append(args, dim.APIKeyID)
	}
	if dim.AccountID > 0 {
		query += fmt.Sprintf(" AND ul.account_id = $%d", len(args)+1)
		args = append(args, dim.AccountID)
	}
	if dim.RequestType != nil {
		condition, conditionArgs := buildRequestTypeFilterConditionWithAlias(len(args)+1, *dim.RequestType, "ul")
		query += " AND " + condition
		args = append(args, conditionArgs...)
	}
	if dim.Stream != nil {
		query += fmt.Sprintf(" AND ul.stream = $%d", len(args)+1)
		args = append(args, *dim.Stream)
	}
	query, args = appendNativeCompactionV2QueryFilter(query, args, dim.NativeCompactionV2, "ul")
	if dim.BillingType != nil {
		query += fmt.Sprintf(" AND ul.billing_type = $%d", len(args)+1)
		args = append(args, *dim.BillingType)
	}

	// ORDER BY 列来自固定 allowlist(非用户原样字符串),避免 SQL 注入。
	orderBy := "actual_cost"
	switch dim.SortBy {
	case "total_tokens", "input_tokens", "output_tokens", "cache_tokens", "requests", "cost", "actual_cost":
		orderBy = dim.SortBy
	}
	// 排行按付款主体归属，团队成员使用团队 Key 时计入团队 Owner。
	query += " GROUP BY COALESCE(ul.billing_user_id, ul.user_id, 0), u.email ORDER BY " + orderBy + " DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usage.UserBreakdownItem, 0)
	for rows.Next() {
		var row usage.UserBreakdownItem
		if err := rows.Scan(
			&row.UserID,
			&row.Email,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetAllGroupUsageSummary 返回每个分组在服务端时区内的今日、昨日和累计实际扣费。
func (r *Store) GetAllGroupUsageSummary(ctx context.Context, todayStart time.Time) ([]usage.GroupUsageSummary, error) {
	if aggregated, ok, aggregateErr := r.getAllGroupUsageSummaryFromAnalytics(ctx, todayStart); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("all_group_usage_summary", aggregateErr)
	}
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	query := `
		SELECT
			g.id AS group_id,
			COALESCE(SUM(ul.actual_cost), 0) AS total_cost,
			COALESCE(SUM(CASE WHEN ul.created_at >= $1 THEN ul.actual_cost ELSE 0 END), 0) AS today_cost,
			COALESCE(SUM(CASE WHEN ul.created_at >= $2 AND ul.created_at < $1 THEN ul.actual_cost ELSE 0 END), 0) AS yesterday_cost
		FROM groups g
		LEFT JOIN usage_logs ul ON ul.group_id = g.id
		GROUP BY g.id
	`

	rows, err := r.sql.QueryContext(ctx, query, todayStart, yesterdayStart)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var results []usage.GroupUsageSummary
	for rows.Next() {
		var row usage.GroupUsageSummary
		if err := rows.Scan(&row.GroupID, &row.TotalCost, &row.TodayCost, &row.YesterdayCost); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// resolveModelDimensionExpression maps model source type to a safe SQL expression.
func resolveModelDimensionExpression(modelType string) string {
	return resolveModelDimensionExpressionWithAlias(modelType, "")
}

func resolveModelDimensionExpressionWithAlias(modelType, alias string) string {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	// 模型分布使用内部请求模型，避免复合 Key 的展示前缀把同一模型拆成多项；
	// requested_model 仍保留在明细中，用于展示客户端实际提交的模型名。
	requestedExpr := fmt.Sprintf(
		"COALESCE(NULLIF(TRIM(%s), ''), NULLIF(TRIM(%s), ''), '')",
		column("model"),
		column("requested_model"),
	)
	upstreamExpr := fmt.Sprintf("COALESCE(NULLIF(TRIM(%s), ''), %s)", column("upstream_model"), column("model"))
	switch usage.NormalizeModelSource(modelType) {
	case usage.ModelSourceUpstream:
		return upstreamExpr
	case usage.ModelSourceMapping:
		return fmt.Sprintf("(%s || ' -> ' || %s)", requestedExpr, upstreamExpr)
	default:
		return requestedExpr
	}
}

func scanTrendRows(rows *sql.Rows) ([]TrendDataPoint, error) {
	results := make([]TrendDataPoint, 0)
	for rows.Next() {
		var row TrendDataPoint
		if err := rows.Scan(
			&row.Date,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func scanModelStatsRows(rows *sql.Rows) ([]ModelStat, error) {
	results := make([]ModelStat, 0)
	for rows.Next() {
		var row ModelStat
		if err := rows.Scan(
			&row.Model,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
