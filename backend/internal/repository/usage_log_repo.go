package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	"github.com/TokenFlux/TokenRouter/internal/service"
	gocache "github.com/patrickmn/go-cache"
)

const rawUsageLogModelColumn = "model"

// rawUsageLogModelColumn preserves the exact stored usage_logs.model semantics for direct filters.
// Historical rows may contain upstream/billing model values, while newer rows store requested_model.
// Requested/upstream/mapping analytics must use resolveModelDimensionExpression instead.

// usageLogSuccessFilterUL 用于把"失败请求 usage log"（tokens=0、cost=0、不计费的占位记录）
// 从统计性聚合中排除，避免污染 Dashboard / 用量拆分等指标。
//
// 表结构中没有 success bool 列；新增列要做迁移，风险大；这里用 actual_cost > 0 作为代理：
// 任何成功落账的请求都会产生 actual_cost（包括 token 计费、纯图片 token 计费、按次/按图计费），
// 反之失败请求占位 usage log 的 actual_cost 为 0。
// 早期版本用 4 项 token 和 > 0 判定会把"按次/按图计费"与"image_output_tokens 独立计费"的纯图片
// 请求误判为失败，导致这部分请求从用量统计里消失，故改用 actual_cost。
// 配合 `FROM usage_logs ul` JOIN 查询使用。
const usageLogSuccessFilterUL = "ul.actual_cost > 0"

// usageLogEffectivePlatformExpr 用于按"有效平台"维度聚合 usage_logs：
// 优先取请求实际走的分组 platform，若分组未设置 platform 再 fallback 到 account.platform。
// 配套要求查询里 LEFT JOIN groups g ON g.id = ul.group_id 与 LEFT JOIN accounts a ON a.id = ul.account_id。
const usageLogEffectivePlatformExpr = "COALESCE(NULLIF(g.platform,''), a.platform)"

// dateFormatWhitelist 将 granularity 参数映射为 PostgreSQL TO_CHAR 格式字符串，防止外部输入直接拼入 SQL
var dateFormatWhitelist = map[string]string{
	"hour":  "YYYY-MM-DD HH24:00",
	"day":   "YYYY-MM-DD",
	"week":  "IYYY-IW",
	"month": "YYYY-MM",
}

// safeDateFormat 根据白名单获取 dateFormat，未匹配时返回默认值
func safeDateFormat(granularity string) string {
	if f, ok := dateFormatWhitelist[granularity]; ok {
		return f
	}
	return "YYYY-MM-DD"
}

// appendRawUsageLogModelWhereCondition 对历史数据兼容使用原始 model 列过滤。
// requested/upstream 维度分析必须改用 resolveModelDimensionExpression。
func appendRawUsageLogModelWhereCondition(conditions []string, args []any, model string) ([]string, []any) {
	if strings.TrimSpace(model) == "" {
		return conditions, args
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", rawUsageLogModelColumn, len(args)+1))
	args = append(args, model)
	return conditions, args
}

func appendUsageLogBillingModeWhereCondition(conditions []string, args []any, billingMode string) ([]string, []any) {
	return appendUsageLogBillingModeWhereConditionWithAlias(conditions, args, billingMode, "")
}

func appendUsageLogBillingModeWhereConditionWithAlias(conditions []string, args []any, billingMode string, alias string) ([]string, []any) {
	mode := strings.TrimSpace(billingMode)
	if mode == "" {
		return conditions, args
	}
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	placeholder := fmt.Sprintf("$%d", len(args)+1)
	switch service.BillingMode(mode) {
	case service.BillingModeImage:
		conditions = append(conditions, fmt.Sprintf("(%s = %s OR ((%s IS NULL OR %s = '') AND COALESCE(%s, 0) > 0))", column("billing_mode"), placeholder, column("billing_mode"), column("billing_mode"), column("image_count")))
	case service.BillingModeVideo:
		conditions = append(conditions, fmt.Sprintf("%s = %s", column("billing_mode"), placeholder))
	case service.BillingModeToken:
		conditions = append(conditions, fmt.Sprintf("(%s = %s OR ((%s IS NULL OR %s = '') AND COALESCE(%s, 0) <= 0))", column("billing_mode"), placeholder, column("billing_mode"), column("billing_mode"), column("image_count")))
	default:
		conditions = append(conditions, fmt.Sprintf("%s = %s", column("billing_mode"), placeholder))
	}
	args = append(args, mode)
	return conditions, args
}

func appendUsageLogBillingModeQueryFilter(query string, args []any, billingMode string, alias string) (string, []any) {
	conditions, args := appendUsageLogBillingModeWhereConditionWithAlias(nil, args, billingMode, alias)
	if len(conditions) == 0 {
		return query, args
	}
	return query + " AND " + conditions[0], args
}

func appendUsageLogModelWhereCondition(conditions []string, args []any, model string, source string) ([]string, []any) {
	if strings.TrimSpace(source) == "" {
		return appendRawUsageLogModelWhereCondition(conditions, args, model)
	}
	if strings.TrimSpace(model) == "" {
		return conditions, args
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", resolveModelDimensionExpression(source), len(args)+1))
	args = append(args, model)
	return conditions, args
}

// appendRawUsageLogModelQueryFilter 对历史数据兼容使用原始 model 列过滤。
// requested/upstream 维度分析必须改用 resolveModelDimensionExpression。
func appendRawUsageLogModelQueryFilter(query string, args []any, model string) (string, []any) {
	if strings.TrimSpace(model) == "" {
		return query, args
	}
	query += fmt.Sprintf(" AND %s = $%d", rawUsageLogModelColumn, len(args)+1)
	args = append(args, model)
	return query, args
}

func appendUsageLogModelQueryFilter(query string, args []any, model string, source string) (string, []any) {
	if strings.TrimSpace(source) == "" {
		return appendRawUsageLogModelQueryFilter(query, args, model)
	}
	if strings.TrimSpace(model) == "" {
		return query, args
	}
	query += fmt.Sprintf(" AND %s = $%d", resolveModelDimensionExpression(source), len(args)+1)
	args = append(args, model)
	return query, args
}

type usageLogRepository struct {
	client *dbent.Client
	sql    sqlExecutor
	db     *sql.DB

	createBatchOnce     sync.Once
	createBatchCh       chan usageLogCreateRequest
	bestEffortBatchOnce sync.Once
	bestEffortBatchCh   chan usageLogBestEffortRequest
	bestEffortRecent    *gocache.Cache
}

func NewUsageLogRepository(client *dbent.Client, sqlDB *sql.DB) service.UsageLogRepository {
	return newUsageLogRepositoryWithSQL(client, sqlDB)
}

func newUsageLogRepositoryWithSQL(client *dbent.Client, sqlq sqlExecutor) *usageLogRepository {
	// 使用 scanSingleRow 替代 QueryRowContext，保证 ent.Tx 作为 sqlExecutor 可用。
	repo := &usageLogRepository{client: client, sql: sqlq}
	if db, ok := sqlq.(*sql.DB); ok {
		repo.db = db
	}
	repo.bestEffortRecent = gocache.New(usageLogBestEffortRecentTTL, time.Minute)
	return repo
}

func buildWhere(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conditions, " AND ")
}

func appendRequestTypeOrStreamWhereCondition(conditions []string, args []any, requestType *int16, stream *bool) ([]string, []any) {
	if requestType != nil {
		condition, conditionArgs := buildRequestTypeFilterCondition(len(args)+1, *requestType)
		conditions = append(conditions, condition)
		args = append(args, conditionArgs...)
		return conditions, args
	}
	if stream != nil {
		conditions = append(conditions, fmt.Sprintf("stream = $%d", len(args)+1))
		args = append(args, *stream)
	}
	return conditions, args
}

func appendRequestTypeOrStreamQueryFilter(query string, args []any, requestType *int16, stream *bool) (string, []any) {
	if requestType != nil {
		condition, conditionArgs := buildRequestTypeFilterCondition(len(args)+1, *requestType)
		query += " AND " + condition
		args = append(args, conditionArgs...)
		return query, args
	}
	if stream != nil {
		query += fmt.Sprintf(" AND stream = $%d", len(args)+1)
		args = append(args, *stream)
	}
	return query, args
}

// buildRequestTypeFilterCondition 在 request_type 过滤时兼容 legacy 字段，避免历史数据漏查。
func buildRequestTypeFilterCondition(startArgIndex int, requestType int16) (string, []any) {
	return buildRequestTypeFilterConditionWithAlias(startArgIndex, requestType, "")
}

// buildRequestTypeFilterConditionWithAlias 为带表别名的统计查询复用历史请求类型兼容条件。
func buildRequestTypeFilterConditionWithAlias(startArgIndex int, requestType int16, alias string) (string, []any) {
	normalized := service.RequestTypeFromInt16(requestType)
	requestTypeArg := int16(normalized)
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	switch normalized {
	case service.RequestTypeSync:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sstream = FALSE AND %sopenai_ws_mode = FALSE))", prefix, startArgIndex, prefix, int16(service.RequestTypeUnknown), prefix, prefix), []any{requestTypeArg}
	case service.RequestTypeStream:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sstream = TRUE AND %sopenai_ws_mode = FALSE))", prefix, startArgIndex, prefix, int16(service.RequestTypeUnknown), prefix, prefix), []any{requestTypeArg}
	case service.RequestTypeWSV2:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sopenai_ws_mode = TRUE))", prefix, startArgIndex, prefix, int16(service.RequestTypeUnknown), prefix), []any{requestTypeArg}
	default:
		return fmt.Sprintf("%srequest_type = $%d", prefix, startArgIndex), []any{requestTypeArg}
	}
}

// GetDashboardPublicStats 只读取首页公开统计所需字段，避免公开接口复用完整仪表盘查询。
func (r *usageLogRepository) GetDashboardPublicStats(ctx context.Context, start, end time.Time, useAggregates bool) (*service.DashboardPublicStats, error) {
	stats := &service.DashboardPublicStats{}
	if err := r.fillDashboardPublicUserStats(ctx, stats); err != nil {
		return nil, err
	}
	if useAggregates {
		if err := r.fillDashboardPublicTokenStatsAggregated(ctx, stats, timezone.Today()); err != nil {
			return nil, err
		}
		return stats, nil
	}
	if err := r.fillDashboardPublicTokenStatsFromUsageLogs(ctx, stats, start, end, timezone.Today()); err != nil {
		return nil, err
	}
	return stats, nil
}

// GetUsageRanking 返回用户侧按 Token 总量排序的用量排行。
func (r *usageLogRepository) GetUsageRanking(ctx context.Context, startTime, endTime time.Time, limit int) (result *UsageRankingResponse, err error) {
	if limit <= 0 {
		limit = service.DefaultUsageRankingLimit
	}

	query := `
		WITH user_usage AS (
			SELECT
				u.user_id,
				COUNT(*) as requests,
				COALESCE(SUM(u.input_tokens), 0) as input_tokens,
				COALESCE(SUM(u.output_tokens), 0) as output_tokens,
				COALESCE(SUM(u.cache_creation_tokens), 0) as cache_creation_tokens,
				COALESCE(SUM(u.cache_read_tokens), 0) as cache_read_tokens,
				COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as total_tokens,
				COALESCE(SUM(u.actual_cost), 0) as actual_cost
			FROM usage_logs u
			WHERE u.created_at >= $1 AND u.created_at < $2
			GROUP BY u.user_id
			HAVING COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) > 0
		),
		ranked AS (
			SELECT
				ROW_NUMBER() OVER (ORDER BY total_tokens DESC, requests DESC, user_id ASC) as rank,
				user_id,
				requests,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				total_tokens,
				actual_cost,
				COALESCE(SUM(requests) OVER (), 0) as total_requests,
				COALESCE(SUM(total_tokens) OVER (), 0) as ranking_total_tokens,
				COALESCE(SUM(actual_cost) OVER (), 0) as total_actual_cost
			FROM user_usage
			ORDER BY total_tokens DESC, requests DESC, user_id ASC
			LIMIT $3
		)
		SELECT
			r.rank,
			r.user_id,
			COALESCE(us.email, '') as email,
			COALESCE(us.username, '') as username,
			COALESCE(ua.url, '') as avatar_url,
			r.requests,
			r.input_tokens,
			r.output_tokens,
			r.cache_creation_tokens,
			r.cache_read_tokens,
			r.total_tokens,
			r.actual_cost,
			r.total_requests,
			r.ranking_total_tokens,
			r.total_actual_cost
		FROM ranked r
		LEFT JOIN users us ON r.user_id = us.id
		LEFT JOIN user_avatars ua ON ua.user_id = r.user_id
		ORDER BY r.rank ASC
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

	ranking := make([]UsageRankingItem, 0)
	totalRequests := int64(0)
	totalTokens := int64(0)
	totalActualCost := 0.0
	for rows.Next() {
		var row UsageRankingItem
		var email string
		var username string
		if err = rows.Scan(
			&row.Rank,
			&row.UserID,
			&email,
			&username,
			&row.AvatarURL,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.TotalTokens,
			&row.ActualCost,
			&totalRequests,
			&totalTokens,
			&totalActualCost,
		); err != nil {
			return nil, err
		}
		row.DisplayName = rankingDisplayName(username, email, row.UserID)
		ranking = append(ranking, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return &UsageRankingResponse{
		Ranking:         ranking,
		TotalRequests:   totalRequests,
		TotalTokens:     totalTokens,
		TotalActualCost: totalActualCost,
	}, nil
}

func (r *usageLogRepository) fillDashboardPublicTokenStatsAggregated(ctx context.Context, stats *service.DashboardPublicStats, todayStart time.Time) error {
	query := `
		SELECT
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) FILTER (WHERE bucket_date = $1::date), 0) AS today_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS total_tokens
		FROM usage_dashboard_daily
	`
	return scanSingleRow(ctx, r.sql, query, []any{todayStart}, &stats.TodayTokens, &stats.TotalTokens)
}

func (r *usageLogRepository) fillDashboardPublicTokenStatsFromUsageLogs(ctx context.Context, stats *service.DashboardPublicStats, start, end, todayStart time.Time) error {
	startUTC := start.UTC()
	endUTC := end.UTC()
	todayStartUTC := todayStart.UTC()
	todayEndUTC := todayStartUTC.Add(24 * time.Hour)
	query := `
		WITH scoped AS (
			SELECT
				created_at,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens
			FROM usage_logs
			WHERE created_at >= LEAST($1::timestamptz, $3::timestamptz)
				AND created_at < GREATEST($2::timestamptz, $4::timestamptz)
		)
		SELECT
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_tokens
		FROM scoped
	`
	return scanSingleRow(ctx, r.sql, query, []any{startUTC, endUTC, todayStartUTC, todayEndUTC}, &stats.TodayTokens, &stats.TotalTokens)
}

func (r *usageLogRepository) fillDashboardPublicUserStats(ctx context.Context, stats *service.DashboardPublicStats) error {
	query := `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`
	return scanSingleRow(ctx, r.sql, query, nil, &stats.TotalUsers)
}

// rankingDisplayName 只返回用户名或脱敏邮箱，避免排行接口泄露完整邮箱。
func rankingDisplayName(username, email string, userID int64) string {
	if name := strings.TrimSpace(username); name != "" {
		return name
	}
	if email = strings.TrimSpace(email); email != "" {
		return service.MaskEmail(email)
	}
	return fmt.Sprintf("User #%d", userID)
}

type UsageRankingItem = usagestats.UsageRankingItem

type UsageRankingResponse = usagestats.UsageRankingResponse
