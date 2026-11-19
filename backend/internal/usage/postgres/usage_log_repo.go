package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo.go constructs the usage store, coordinates batcher shutdown,
// and supplies compatibility filters and public rankings.
// Usage records preserve the acting user, financial identity, execution
// platform, and model evidence needed by distinct reports. Report filters keep
// those meanings separate: ordinary user scopes select actors, public rankings
// merge the payer's activity, and media/request-type helpers interpret
// historical rows that lack newer dimensions.
//
// Store retains an Ent client and a caller-selected SQL executor; a concrete
// database pool enables queued writes and independent dashboard queries. Two
// lazily started batchers share shutdown coordination, and a recent-write cache
// suppresses repeated best-effort submissions. Calendar supplies server dates,
// while aggregate failures are logged at most once per operation per minute
// before eligible callers read raw records.

const (
	// rawUsageLogModelColumn selects the stored model value for direct filters.
	// Historical rows can contain upstream or billing models; current rows contain
	// the internal request model. Use resolveModelDimensionExpression for
	// requested, upstream, or mapping analytics.
	rawUsageLogModelColumn = "model"
	// usageAnalyticsFallbackLogInterval bounds repeated process warnings for each
	// failed report operation before complete raw-query recovery is attempted.
	usageAnalyticsFallbackLogInterval = time.Minute

	// usageLogSuccessFilterUL selects charged rows for queries using the ul alias.
	// The cost predicate excludes zero-cost failure placeholders and free requests.
	//
	// Checking cost also includes per-request and image charges whose text token
	// counts are zero.
	usageLogSuccessFilterUL = "ul.actual_cost > 0"

	// usageLogEffectivePlatformExpr uses the stored platform so configuration
	// changes do not reassign historical usage.
	usageLogEffectivePlatformExpr = "ul.platform"
)

var (
	// usageAnalyticsFallbackLogState shares warning timestamps across stores so one
	// failing operation cannot flood process logs from concurrent queries.
	usageAnalyticsFallbackLogState = struct {
		// Mutex protects lastByOperation during rate checks, claims, and test cleanup.
		// These operation timestamps are process-wide and require the same lock for
		// every reader and writer that inspects or changes them.
		sync.Mutex                           // protects operation timestamps
		lastByOperation map[string]time.Time // last warning claim per operation
	}{lastByOperation: make(map[string]time.Time)}

	// dateFormatWhitelist maps allowed granularities to fixed PostgreSQL formats,
	// keeping report SQL interpolation separate from user-supplied text.
	dateFormatWhitelist = map[string]string{
		"hour":  "YYYY-MM-DD HH24:00",
		"day":   "YYYY-MM-DD",
		"week":  "IYYY-IW",
		"month": "YYYY-MM",
	}
)

// Store persists analytical usage and reads reports with caller-owned SQL and
// Ent resources. Pool-backed stores lazily start two write workers, which
// StopUsageBatchers drains.
type Store struct {
	// batchLifecycleMu, batchStopped, and batchWG coordinate lazy worker creation,
	// queue admission, and shutdown. Queued writers hold the read lock through
	// their wait; StopUsageBatchers takes the write lock before closing queues and
	// then waits.
	batchLifecycleMu sync.RWMutex   // admission and close lock
	batchStopped     bool           // terminal queue shutdown flag
	batchWG          sync.WaitGroup // initialized batch workers
	// client supplies current display associations using the Ent client selected
	// at construction. Callers pass a transaction client for transaction-scoped
	// hydration; cleanup methods can select another client from context.
	client *dbent.Client
	// sql runs on the caller-selected connection or transaction. Its owner controls
	// transaction completion and database resource shutdown.
	sql sqlExecutor
	// db is set only for a concrete pool executor, enabling queued writes and
	// independently connected report queries. Nil selects direct writes, while
	// runDashboardQueries uses its serial callback path.
	db *sql.DB
	// preAggregation decides multidimensional query eligibility when bound. Legacy
	// dashboard paths retain their own behavior when this service is nil.
	preAggregation *preaggregation.PreAggregationSettingsService
	calendar       timezone.Calendar // server reporting date rules

	// createBatchOnce and createBatchCh publish the result-aware queue once;
	// bestEffortBatchOnce and bestEffortBatchCh do the same for
	// acknowledgement-only writes. All queue creation and closing participates in
	// batchLifecycleMu.
	createBatchOnce     sync.Once                      // create worker initialization guard
	createBatchCh       chan usageLogCreateRequest     // result-aware write admissions
	bestEffortBatchOnce sync.Once                      // best-effort initialization guard
	bestEffortBatchCh   chan usageLogBestEffortRequest // acknowledgement-only write admissions
	// bestEffortRecent records successful request/key submissions for a short
	// suppression interval; ExpireRuntimeCaches performs its scheduled cleanup.
	bestEffortRecent *querycache.TTLMap[struct{}]
}

// UsageRankingItem is a public billing-user ranking entry with display data.
type UsageRankingItem = usage.UsageRankingItem

// UsageRankingResponse combines limited public entries with all eligible payer
// totals.
type UsageRankingResponse = usage.UsageRankingResponse

// NewUsageLogRepository constructs a usage store with the supplied shared
// database and Calendar. It binds preAggregation for query selection; batch
// workers start lazily during writes, and the caller owns database shutdown.
func NewUsageLogRepository(client *dbent.Client, sqlDB *sql.DB, preAggregation *preaggregation.PreAggregationSettingsService, calendar timezone.Calendar) *Store {
	repo := NewUsageLogRepositoryWithSQL(client, sqlDB, calendar)
	repo.preAggregation = preAggregation
	return repo
}

// NewUsageLogRepositoryWithSQL constructs a store for a caller-selected SQL
// executor and Calendar. A concrete *sql.DB enables queued writes and pool
// report dispatch; other executors use direct writes. The caller owns the Ent
// client, SQL resource, and transaction completion.
func NewUsageLogRepositoryWithSQL(client *dbent.Client, sqlq sqlExecutor, calendar timezone.Calendar) *Store {
	// The infra row scanner accepts QueryContext executors such as Ent
	// transactions, allowing report reads to use the caller's chosen transaction.
	repo := &Store{client: client, sql: sqlq, calendar: calendar}
	if db, ok := sqlq.(*sql.DB); ok {
		repo.db = db
	}
	repo.bestEffortRecent = querycache.NewTTLMap[struct{}](usageLogBestEffortRecentTTL)
	return repo
}

// logUsageAnalyticsFallback warns when a nonnil aggregate error qualifies for
// the shared operation limit. It changes no query data; callers execute their
// raw-query recovery after deciding the aggregate result is unusable.
func (r *Store) logUsageAnalyticsFallback(operation string, err error) {
	if err == nil || !shouldLogUsageAnalyticsFallback(operation, time.Now()) {
		return
	}
	slog.Warn("Pre-aggregation query failed, querying raw usage logs", "operation", operation, "error", err)
}

// shouldLogUsageAnalyticsFallback claims the next warning slot for an operation.
// A shared mutex protects the timestamp check and update, so concurrent failures
// cannot publish separate warnings inside one permitted interval.
//
// The first timestamp and later differences are evaluated against the supplied
// now value. Calls less than one minute after the previous claim return false
// without updating it; an eligible claim stores now before releasing the lock.
// The claim records permission, so logging later remains the caller's
// responsibility.
func shouldLogUsageAnalyticsFallback(operation string, now time.Time) bool {
	// Keep the timestamp read and claim together; releasing the lock between them
	// would allow concurrent failures to claim the same warning interval.
	usageAnalyticsFallbackLogState.Lock()
	defer usageAnalyticsFallbackLogState.Unlock()
	last := usageAnalyticsFallbackLogState.lastByOperation[operation]
	if now.Sub(last) < usageAnalyticsFallbackLogInterval {
		return false
	}
	usageAnalyticsFallbackLogState.lastByOperation[operation] = now
	return true
}

// safeDateFormat returns the fixed PostgreSQL format for an allowed granularity.
// Unknown labels use daily formatting, allowing callers to interpolate a format
// whose text comes from the whitelist.
func safeDateFormat(granularity string) string {
	if f, ok := dateFormatWhitelist[granularity]; ok {
		return f
	}
	return "YYYY-MM-DD"
}

// appendRawUsageLogModelWhereCondition appends equality on the stored model
// column. Blank model text adds nothing; otherwise the original string becomes
// the next bound parameter, and appending may reuse the supplied slice storage.
func appendRawUsageLogModelWhereCondition(conditions []string, args []any, model string) ([]string, []any) {
	if strings.TrimSpace(model) == "" {
		return conditions, args
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", rawUsageLogModelColumn, len(args)+1))
	args = append(args, model)
	return conditions, args
}

// appendUsageLogBillingModeWhereCondition appends the unqualified historical
// billing-mode predicate. Blank input leaves the slices unchanged; other input
// is trimmed and bound by the alias-aware helper using its media compatibility
// rules.
func appendUsageLogBillingModeWhereCondition(conditions []string, args []any, billingMode string) ([]string, []any) {
	return appendUsageLogBillingModeWhereConditionWithAlias(conditions, args, billingMode, "")
}

// appendUsageLogBillingModeWhereConditionWithAlias adds a mode filter to a
// trusted SQL column alias. Trimmed empty input leaves the query unchanged, and
// returned slices can retain the input backing arrays.
//
// Explicit stored modes match directly. Rows with null or empty modes infer
// video from positive duration, image from positive image count with nonpositive
// video duration, and token from neither media marker. Other mode labels use
// exact equality. That ordering keeps legacy video evidence ahead of image
// evidence without altering rows carrying a stored mode.
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
	// Legacy image matching excludes positive video duration so mixed media evidence
	// belongs to the video filter when the stored billing mode is absent.
	switch pricing.BillingMode(mode) {
	case pricing.BillingModeImage:
		conditions = append(conditions, fmt.Sprintf("(%s = %s OR ((%s IS NULL OR %s = '') AND COALESCE(%s, 0) <= 0 AND COALESCE(%s, 0) > 0))", column("billing_mode"), placeholder, column("billing_mode"), column("billing_mode"), column("video_duration_seconds"), column("image_count")))
	case pricing.BillingModeVideo:
		conditions = append(conditions, fmt.Sprintf("(%s = %s OR ((%s IS NULL OR %s = '') AND COALESCE(%s, 0) > 0))", column("billing_mode"), placeholder, column("billing_mode"), column("billing_mode"), column("video_duration_seconds")))
	case pricing.BillingModeToken:
		conditions = append(conditions, fmt.Sprintf("(%s = %s OR ((%s IS NULL OR %s = '') AND COALESCE(%s, 0) <= 0 AND COALESCE(%s, 0) <= 0))", column("billing_mode"), placeholder, column("billing_mode"), column("billing_mode"), column("video_duration_seconds"), column("image_count")))
	default:
		conditions = append(conditions, fmt.Sprintf("%s = %s", column("billing_mode"), placeholder))
	}
	args = append(args, mode)
	return conditions, args
}

// appendUsageLogBillingModeQueryFilter adds the mode predicate to SQL with an
// existing WHERE clause. Blank input leaves SQL and arguments unchanged; callers
// supply a trusted alias for the compatibility helper.
func appendUsageLogBillingModeQueryFilter(query string, args []any, billingMode string, alias string) (string, []any) {
	conditions, args := appendUsageLogBillingModeWhereConditionWithAlias(nil, args, billingMode, alias)
	if len(conditions) == 0 {
		return query, args
	}
	return query + " AND " + conditions[0], args
}

// appendUsageLogModelWhereCondition adds equality using the selected model
// dimension. Empty source selects raw model storage; blank model input adds
// nothing, while a supplied source goes through the fixed dimension-expression
// helper.
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

// appendRawUsageLogModelQueryFilter appends stored-model equality to SQL that
// already has WHERE. Blank model input returns the original query and arguments;
// nonblank text is bound without trimming its value.
func appendRawUsageLogModelQueryFilter(query string, args []any, model string) (string, []any) {
	if strings.TrimSpace(model) == "" {
		return query, args
	}
	query += fmt.Sprintf(" AND %s = $%d", rawUsageLogModelColumn, len(args)+1)
	args = append(args, model)
	return query, args
}

// appendUsageLogModelQueryFilter appends model equality to a query with WHERE.
// Empty source uses the raw stored column, a supplied source selects its
// normalized dimension, and blank models leave the query unchanged.
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

// buildWhere returns the WHERE clause for the supplied predicates joined by AND.
// Empty predicates produce empty text, allowing callers to append the result to
// either a filtered or unrestricted SQL source.
func buildWhere(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conditions, " AND ")
}

// appendRequestTypeOrStreamWhereCondition adds one transport selector to the
// predicates. A nonnil requestType takes precedence over stream and uses
// legacy-compatible matching; nil selectors leave the supplied predicates and
// arguments unchanged.
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

// appendRequestTypeOrStreamQueryFilter adds one transport selector after an
// existing WHERE. Request type takes precedence over stream, while absent
// selectors leave SQL unchanged; compatibility handling belongs to the
// request-type helper.
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

// appendNativeCompactionV2WhereCondition adds compaction equality when value is
// nonnil. False remains a requested filter, nil adds nothing, and alias names
// come from trusted callers before the value is bound.
func appendNativeCompactionV2WhereCondition(conditions []string, args []any, value *bool, alias string) ([]string, []any) {
	if value == nil {
		return conditions, args
	}
	column := "native_compaction_v2"
	if alias != "" {
		column = alias + "." + column
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", column, len(args)+1))
	args = append(args, *value)
	return conditions, args
}

// appendNativeCompactionV2QueryFilter appends compaction equality after an
// existing WHERE. Nil leaves SQL and arguments unchanged; the trusted alias
// qualifies the column while the boolean remains a bound value.
func appendNativeCompactionV2QueryFilter(query string, args []any, value *bool, alias string) (string, []any) {
	conditions, args := appendNativeCompactionV2WhereCondition(nil, args, value, alias)
	if len(conditions) == 0 {
		return query, args
	}
	return query + " AND " + conditions[0], args
}

// buildRequestTypeFilterCondition returns a legacy-compatible unqualified
// transport predicate and its argument. startArgIndex identifies the caller's
// next SQL parameter, and unsupported request types normalize to the unknown
// category.
func buildRequestTypeFilterCondition(startArgIndex int, requestType int16) (string, []any) {
	return buildRequestTypeFilterConditionWithAlias(startArgIndex, requestType, "")
}

// buildRequestTypeFilterConditionWithAlias returns transport equality for a
// trusted alias and normalized type. Callers supply the next parameter position
// and use the returned argument in that slot. Unsupported category codes
// normalize to unknown before a predicate is selected.
//
// For sync, stream, and WebSocket types, rows storing unknown type can also
// match their legacy flags. WebSocket evidence takes precedence over stream,
// while sync requires both flags false. Other categories match the stored type
// alone, so special execution categories retain their own recorded meaning
// rather than inheriting transport labels.
func buildRequestTypeFilterConditionWithAlias(startArgIndex int, requestType int16, alias string) (string, []any) {
	normalized := usage.RequestTypeFromInt16(requestType)
	requestTypeArg := int16(normalized)
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	// Infer legacy transport only for unknown stored types, keeping rows with an
	// explicit execution category from inheriting another category's boolean match.
	switch normalized {
	case usage.RequestTypeSync:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sstream = FALSE AND %sopenai_ws_mode = FALSE))", prefix, startArgIndex, prefix, int16(usage.RequestTypeUnknown), prefix, prefix), []any{requestTypeArg}
	case usage.RequestTypeStream:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sstream = TRUE AND %sopenai_ws_mode = FALSE))", prefix, startArgIndex, prefix, int16(usage.RequestTypeUnknown), prefix, prefix), []any{requestTypeArg}
	case usage.RequestTypeWSV2:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sopenai_ws_mode = TRUE))", prefix, startArgIndex, prefix, int16(usage.RequestTypeUnknown), prefix), []any{requestTypeArg}
	default:
		return fmt.Sprintf("%srequest_type = $%d", prefix, startArgIndex), []any{requestTypeArg}
	}
}

// GetDashboardPublicStats returns current nondeleted-user count with public
// token totals. useAggregates reads retained dashboard days regardless of
// start/end; raw mode applies that supplied range to TotalTokens and Calendar's
// midnight plus twenty-four hours to TodayTokens.
func (r *Store) GetDashboardPublicStats(ctx context.Context, start, end time.Time, useAggregates bool) (*usage.DashboardPublicStats, error) {
	stats := &usage.DashboardPublicStats{}
	if err := r.fillDashboardPublicUserStats(ctx, stats); err != nil {
		return nil, err
	}
	if useAggregates {
		// The caller selected retained daily buckets; propagate query failure because
		// this public path does not retry raw token totals itself.
		if err := r.fillDashboardPublicTokenStatsAggregated(ctx, stats, r.calendar.Today()); err != nil {
			return nil, err
		}
		return stats, nil
	}
	if err := r.fillDashboardPublicTokenStatsFromUsageLogs(ctx, stats, start, end, r.calendar.Today()); err != nil {
		return nil, err
	}
	return stats, nil
}

// usageRankingQueryParts chooses fixed eligibility and ordering SQL for public
// rankings. Invalid metrics normalize to total tokens; each ordering uses other
// counters as tie breakers and ends with ascending user ID.
func usageRankingQueryParts(sortBy usage.UsageRankingSortBy) (eligibility, orderBy string) {
	switch usage.NormalizeUsageRankingSortBy(string(sortBy)) {
	case usage.UsageRankingSortByRequests:
		return "COUNT(*) > 0", "requests DESC, total_tokens DESC, actual_cost DESC, user_id ASC"
	case usage.UsageRankingSortByActualCost:
		return "COALESCE(SUM(u.actual_cost), 0) > 0", "actual_cost DESC, total_tokens DESC, requests DESC, user_id ASC"
	default:
		return "COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) > 0", "total_tokens DESC, requests DESC, actual_cost DESC, user_id ASC"
	}
}

// GetUsageRanking returns eligible billing-user rankings for the supplied
// half-open range. Nonpositive limit uses the public default, and unavailable or
// failed multidimensional analytics causes a complete raw query, with failures
// warning through the shared limiter.
//
// Raw rows group by billing_user_id with historical user_id recovery. Positive
// eligibility follows the selected metric, and window sums compute totals before
// LIMIT, so totals cover every eligible payer. Current identity supplies
// username or masked-email display, while only ranked entries are returned to
// the caller.
func (r *Store) GetUsageRanking(ctx context.Context, startTime, endTime time.Time, limit int, sortBy usage.UsageRankingSortBy) (result *UsageRankingResponse, err error) {
	if limit <= 0 {
		limit = usage.DefaultUsageRankingLimit
	}
	// Unavailable coverage or an aggregate failure sends the whole ranking through
	// raw SQL, so partial aggregate totals never enter the response.
	if aggregated, ok, aggregateErr := r.getUsageRankingFromAnalytics(ctx, startTime, endTime, limit, sortBy); aggregateErr == nil && ok {
		return aggregated, nil
	} else if aggregateErr != nil {
		r.logUsageAnalyticsFallback("usage_ranking", aggregateErr)
	}

	eligibility, orderBy := usageRankingQueryParts(sortBy)
	query := fmt.Sprintf(`
		WITH user_usage AS (
			SELECT
				COALESCE(u.billing_user_id, u.user_id) as user_id,
				COUNT(*) as requests,
				COALESCE(SUM(u.input_tokens), 0) as input_tokens,
				COALESCE(SUM(u.output_tokens), 0) as output_tokens,
				COALESCE(SUM(u.cache_creation_tokens), 0) as cache_creation_tokens,
				COALESCE(SUM(u.cache_read_tokens), 0) as cache_read_tokens,
				COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as total_tokens,
				COALESCE(SUM(u.actual_cost), 0) as actual_cost
			FROM usage_logs u
			WHERE u.created_at >= $1 AND u.created_at < $2
			-- 排行按付款主体归属，团队成员使用团队 Key 时计入团队 Owner。
			GROUP BY COALESCE(u.billing_user_id, u.user_id)
			HAVING %s
		),
		ranked AS (
			SELECT
				ROW_NUMBER() OVER (ORDER BY %s) as rank,
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
			ORDER BY %s
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
	`, eligibility, orderBy, orderBy)

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	// Close the ranking rows on every exit. An otherwise successful cleanup failure
	// clears the response and becomes the returned error.
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

// fillDashboardPublicTokenStatsAggregated writes today's and retained-total
// tokens into stats from daily dashboard buckets. todayStart is interpreted as a
// SQL date, and empty retained buckets yield zero sums.
func (r *Store) fillDashboardPublicTokenStatsAggregated(ctx context.Context, stats *usage.DashboardPublicStats, todayStart time.Time) error {
	query := `
		SELECT
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) FILTER (WHERE bucket_date = $1::date), 0) AS today_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS total_tokens
		FROM usage_dashboard_daily
	`
	return scanSingleRow(ctx, r.sql, query, []any{todayStart}, &stats.TodayTokens, &stats.TotalTokens)
}

// fillDashboardPublicTokenStatsFromUsageLogs fills range and today token totals
// from raw customer records. start/end determine TotalTokens, while todayStart determines
// a separate fixed twenty-four-hour TodayTokens window.
//
// The query scans the union of both time spans and applies distinct FILTER
// predicates to each sum. This lets today's usage remain available even when the
// requested retained range ends elsewhere. UTC conversion keeps parameter
// instants intact; adding twenty-four hours describes elapsed time and can
// differ from a local calendar day's end around offset transitions.
func (r *Store) fillDashboardPublicTokenStatsFromUsageLogs(ctx context.Context, stats *usage.DashboardPublicStats, start, end, todayStart time.Time) error {
	startUTC := start.UTC()
	endUTC := end.UTC()
	todayStartUTC := todayStart.UTC()
	// Today's raw token window uses twenty-four elapsed hours from Calendar
	// midnight.
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
				AND request_source = 'user'
		)
		SELECT
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_tokens
		FROM scoped
	`
	return scanSingleRow(ctx, r.sql, query, []any{startUTC, endUTC, todayStartUTC, todayEndUTC}, &stats.TodayTokens, &stats.TotalTokens)
}

// fillDashboardPublicUserStats fills the count of current nondeleted users in
// stats. User status and usage activity do not restrict this count, and query
// errors leave the caller to discard the response.
func (r *Store) fillDashboardPublicUserStats(ctx context.Context, stats *usage.DashboardPublicStats) error {
	query := `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`
	return scanSingleRow(ctx, r.sql, query, nil, &stats.TotalUsers)
}

// rankingDisplayName chooses a public label from trimmed username, masked email,
// then userID. A nonblank username wins; only when it is absent is the trimmed
// email passed to identity.MaskEmail.
func rankingDisplayName(username, email string, userID int64) string {
	if name := strings.TrimSpace(username); name != "" {
		return name
	}
	if email = strings.TrimSpace(email); email != "" {
		return identity.MaskEmail(email)
	}
	return fmt.Sprintf("User #%d", userID)
}

// ExpireRuntimeCaches removes expired best-effort deduplication entries when the
// cache exists. The application schedules this call because the cache
// constructor starts no cleanup janitor, and queued writes retain their own
// lifecycle.
func (r *Store) ExpireRuntimeCaches() {
	if r.bestEffortRecent != nil {
		r.bestEffortRecent.DeleteExpired()
	}
}

// StopUsageBatchers stops admission to both initialized queues and waits for
// worker drainage. Repeated calls keep the stopped state and wait on the same
// workers; the method has no cancellation budget.
//
// The exclusive lifecycle lock waits for writers holding read locks before
// closing channels, preventing sends from racing with close. The stopped flag
// also prevents later lazy starts. Worker waiting occurs after releasing that
// lock so drainage can finish, and callers stop completion writers before
// invoking this method to let queued submissions settle.
func (r *Store) StopUsageBatchers() {
	// Exclude active admissions before channel close, then publish the terminal
	// flag so subsequent writers cannot start or send to either queue.
	r.batchLifecycleMu.Lock()
	if !r.batchStopped {
		r.batchStopped = true
		if r.createBatchCh != nil {
			close(r.createBatchCh)
		}
		if r.bestEffortBatchCh != nil {
			close(r.bestEffortBatchCh)
		}
	}
	// Release the lifecycle lock before waiting for both workers to flush their
	// closed queues and complete accepted callers' acknowledgement channels.
	r.batchLifecycleMu.Unlock()
	r.batchWG.Wait()
}

// appendRequestSourceCondition adds a parameterized source restriction to SQL.
// Empty source retains all records; system selects every internal source without
// mixing that classification with the transport-oriented request_type column.
// Specific internal sources include the partial-index predicate explicitly so
// PostgreSQL generic prepared plans can use the system source/time index.
func appendRequestSourceCondition(conditions []string, args []any, source, prefix string) ([]string, []any) {
	if source == "" || source == "all" {
		return conditions, args
	}
	if source == "system" {
		return append(conditions, prefix+"request_source <> 'user'"), args
	}
	if source != usage.SourceUser {
		conditions = append(conditions, prefix+"request_source <> 'user'")
	}
	conditions = append(conditions, fmt.Sprintf("%srequest_source = $%d", prefix, len(args)+1))
	return conditions, append(args, source)
}
