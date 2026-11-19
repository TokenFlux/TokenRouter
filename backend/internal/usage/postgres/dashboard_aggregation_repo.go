package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// dashboard_aggregation_repo.go rebuilds dashboard buckets and active-user
// membership and manages usage retention and partitions.
// Dashboard active-user counts depend on distinct user membership per hour and
// per calendar day. Membership rows are inserted before numeric totals read
// their counts, so repeatedly aggregating a range can add late users. Cleanup
// recomputation removes affected membership and totals, allowing deleted usage
// to reduce those counts. Daily membership then reads full affected days so
// surviving hours remain represented.
//
// The injected Calendar supplies SQL timezone names and local dates; Go hour
// bounds use absolute time.Truncate boundaries. Database handles wrap each
// range rebuild in a transaction, while supplied executors retain their own
// transaction ownership. Raw retention uses bounded row deletes for an ordinary
// table and whole obsolete UTC months for a partitioned table, with activity
// rebuilding delegated to the partition-drop function.

// usageLogsCleanupBatchSize limits each nonpartitioned retention delete;
// smaller affected counts end the current cleanup pass.
const usageLogsCleanupBatchSize = 10000

// AggregationStore maintains dashboard and multidimensional rollups through a
// caller-selected SQL executor.
type AggregationStore struct {
	// archiveDedup is bound by app to billing archival; a nil binding makes
	// CleanupUsageBillingDedup fail before any archival call is attempted.
	archiveDedup func(context.Context, time.Time) error // billing archive callback
	// sql selects transaction ownership. Range methods begin and finish a
	// transaction for *sql.DB; other executors remain controlled by their caller.
	sql sqlExecutor // aggregation SQL execution source
	// calendar supplies dashboard dates and SQL timezone names. Transaction
	// stores retain this value, and a zero calendar uses time.Local.
	calendar timezone.Calendar // dashboard calendar date rules
}

// NewDashboardAggregationRepository binds PostgreSQL rollup storage, the
// dashboard Calendar, and billing archival. A nil database or a driver whose
// concrete type is not *pq.Driver returns nil, disabling this repository at
// assembly.
func NewDashboardAggregationRepository(sqlDB *sql.DB, calendar timezone.Calendar, archive func(context.Context, time.Time) error) *AggregationStore {
	if sqlDB == nil {
		return nil
	}
	if !isPostgresDriver(sqlDB) {
		logging.LegacyPrintf("repository.dashboard_aggregation", "[DashboardAggregation] Non-PostgreSQL driver detected, pre-aggregation disabled")
		return nil
	}
	return NewAggregationStoreWithSQL(sqlDB, calendar, archive)
}

// NewAggregationStoreWithSQL binds an executor and Calendar for aggregation and
// retention. The first optional archive callback is retained; additional
// callbacks are ignored, and callers keep ownership of executor shutdown and
// supplied transactions.
func NewAggregationStoreWithSQL(sqlq sqlExecutor, calendar timezone.Calendar, archive ...func(context.Context, time.Time) error) *AggregationStore {
	r := &AggregationStore{sql: sqlq, calendar: calendar}
	if len(archive) > 0 {
		r.archiveDedup = archive[0]
	}
	return r
}

// isPostgresDriver reports whether db exposes the concrete *pq.Driver used by
// this aggregation adapter. Nil returns false, and wrapped drivers need their
// own support even when they ultimately connect to PostgreSQL.
func isPostgresDriver(db *sql.DB) bool {
	if db == nil {
		return false
	}
	_, ok := db.Driver().(*pq.Driver)
	return ok
}

// AggregateRange refreshes dashboard counts over hours and calendar days
// touched by [start, end). Nil storage and nonincreasing ranges return
// successfully. Go truncates the first hour down and rounds a partial final
// hour up; an exact-hour end remains exclusive, while daily bounds follow the
// injected Calendar.
//
// The hourly scan includes records after a partial end through that hour's
// rounded end. With *sql.DB, membership and numeric writes commit together and
// a failed phase requests rollback. A supplied executor owns its transaction
// behavior, and watermark publication is a separate operation.
func (r *AggregationStore) AggregateRange(ctx context.Context, start, end time.Time) error {
	if r == nil || r.sql == nil {
		return nil
	}
	loc := r.calendar.Location()
	startLocal := start.In(loc)
	endLocal := end.In(loc)
	if !endLocal.After(startLocal) {
		return nil
	}

	// Truncate measures absolute elapsed hours, while date bounds below follow
	// local calendar midnight; a partial end expands the raw scan to its hour.
	hourStart := startLocal.Truncate(time.Hour)
	hourEnd := endLocal.Truncate(time.Hour)
	if endLocal.After(hourEnd) {
		hourEnd = hourEnd.Add(time.Hour)
	}

	// Calendar dates keep local midnight endpoints through DST changes; adding
	// a fixed elapsed day would shift those endpoints on a transition date.
	dayStart := truncateToDay(startLocal, r.calendar)
	dayEnd := truncateToDay(endLocal, r.calendar)
	if endLocal.After(dayEnd) {
		dayEnd = dayEnd.AddDate(0, 0, 1)
	}

	// Membership and numeric totals commit as one database transaction; supplied
	// transaction executors retain the completion and rollback chosen by their
	// owner.
	if db, ok := r.sql.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		txRepo := NewAggregationStoreWithSQL(tx, r.calendar)
		if err := txRepo.aggregateRangeInTx(ctx, hourStart, hourEnd, dayStart, dayEnd); err != nil {
			// Request rollback of the earlier membership and numeric writes, then return
			// the failing phase error even if rollback also reports a failure.
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	return r.aggregateRangeInTx(ctx, hourStart, hourEnd, dayStart, dayEnd)
}

// aggregateRangeInTx inserts active-user membership and refreshes dashboard
// totals over already-rounded ranges. Its caller provides the executor and any
// transaction containing these writes. Hourly membership precedes daily
// membership, and both precede the count queries, so the numeric rows see users
// found by this pass.
//
// Daily totals read the affected full-day range from existing hourly totals.
// Membership insertion accumulates distinct users, so callers use
// recomputeRangeInTx when deletions must remove a previously recorded user.
func (r *AggregationStore) aggregateRangeInTx(ctx context.Context, hourStart, hourEnd, dayStart, dayEnd time.Time) error {
	// Insert hourly and daily membership before numeric totals read those
	// distinct-user rows; later passes can add users from newly available raw
	// records.
	if err := r.insertHourlyActiveUsers(ctx, hourStart, hourEnd); err != nil {
		return err
	}
	if err := r.insertDailyActiveUsers(ctx, hourStart, hourEnd); err != nil {
		return err
	}
	if err := r.upsertHourlyAggregates(ctx, hourStart, hourEnd); err != nil {
		return err
	}
	return r.upsertDailyAggregates(ctx, dayStart, dayEnd)
}

// RecomputeRange rebuilds dashboard data after an inclusive [start, end]
// cleanup. Nil storage and nonincreasing ranges are successful no-ops. The end
// hour and end calendar day are included even when end lies exactly at their
// beginning, matching cleanup's created_at <= end predicate.
//
// Affected membership and totals are removed before surviving usage is scanned.
// A database handle supplies one transaction and requests rollback on rebuild
// failure; a provided executor retains caller-owned transaction behavior. The
// Calendar is carried into the transaction-derived store.
func (r *AggregationStore) RecomputeRange(ctx context.Context, start, end time.Time) error {
	if r == nil || r.sql == nil {
		return nil
	}
	loc := r.calendar.Location()
	startLocal := start.In(loc)
	endLocal := end.In(loc)
	if !endLocal.After(startLocal) {
		return nil
	}

	// Round cleanup to whole absolute hours, including the hour beginning at
	// end; rebuilding those hours keeps surviving records outside deletion limits.
	hourStart := startLocal.Truncate(time.Hour)
	hourEnd := endLocal.Truncate(time.Hour).Add(time.Hour)

	// Clear full local dates so daily membership can be reconstructed from
	// every surviving hour, including hours outside the cleanup's raw range.
	dayStart := truncateToDay(startLocal, r.calendar)
	dayEnd := truncateToDay(endLocal, r.calendar).AddDate(0, 0, 1)

	// Deletion and rebuilding share the new database transaction so a failed
	// replacement cannot commit the intermediate state with affected buckets
	// removed.
	if db, ok := r.sql.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		txRepo := NewAggregationStoreWithSQL(tx, r.calendar)
		if err := txRepo.recomputeRangeInTx(ctx, hourStart, hourEnd, dayStart, dayEnd); err != nil {
			// Request rollback of the earlier membership and numeric writes, then return
			// the failing phase error even if rollback also reports a failure.
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	return r.recomputeRangeInTx(ctx, hourStart, hourEnd, dayStart, dayEnd)
}

// recomputeRangeInTx replaces membership and totals for rounded cleanup hours
// and days. The caller supplies any transaction surrounding all deletes and
// rebuilds. Clearing both numeric rows and membership removes users whose last
// request disappeared, including buckets that become empty.
//
// Only affected hourly membership is rebuilt, while daily membership reads
// every hour in the cleared days. This retains users from unaffected hours of
// the same date. Numeric hourly totals then feed daily totals, and the first
// failed statement stops further work.
func (r *AggregationStore) recomputeRangeInTx(ctx context.Context, hourStart, hourEnd, dayStart, dayEnd time.Time) error {
	// Remove totals and their membership together before rebuilding; membership
	// insertion uses conflict suppression and would otherwise retain users whose
	// requests were deleted.
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_hourly WHERE bucket_start >= $1 AND bucket_start < $2", hourStart, hourEnd); err != nil {
		return err
	}
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_hourly_users WHERE bucket_start >= $1 AND bucket_start < $2", hourStart, hourEnd); err != nil {
		return err
	}
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_daily WHERE bucket_date >= $1::date AND bucket_date < $2::date", dayStart, dayEnd); err != nil {
		return err
	}
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_daily_users WHERE bucket_date >= $1::date AND bucket_date < $2::date", dayStart, dayEnd); err != nil {
		return err
	}

	if err := r.insertHourlyActiveUsers(ctx, hourStart, hourEnd); err != nil {
		return err
	}
	// The cleared daily membership covers full dates. Read every surviving hour in
	// those dates, including hours outside the smaller cleanup range.
	if err := r.insertDailyActiveUsers(ctx, dayStart, dayEnd); err != nil {
		return err
	}
	if err := r.upsertHourlyAggregates(ctx, hourStart, hourEnd); err != nil {
		return err
	}
	return r.upsertDailyAggregates(ctx, dayStart, dayEnd)
}

// GetAggregationWatermark reads the singleton dashboard cursor and converts it
// to UTC. An exactly matched sql.ErrNoRows returns the Unix epoch as the
// initial cursor; other scan or cleanup errors propagate.
func (r *AggregationStore) GetAggregationWatermark(ctx context.Context) (time.Time, error) {
	var ts time.Time
	query := "SELECT last_aggregated_at FROM usage_dashboard_aggregation_watermark WHERE id = 1"
	if err := scanSingleRow(ctx, r.sql, query, nil, &ts); err != nil {
		// A missing singleton starts dashboard progress at the Unix epoch, giving the
		// aggregation service an initial cursor for its next range decision.
		if err == sql.ErrNoRows {
			return time.Unix(0, 0).UTC(), nil
		}
		return time.Time{}, err
	}
	return ts.UTC(), nil
}

// UpdateAggregationWatermark replaces the singleton dashboard cursor with
// aggregatedAt in UTC. The caller publishes it after successful range work;
// this statement does not start a transaction with earlier aggregate writes.
func (r *AggregationStore) UpdateAggregationWatermark(ctx context.Context, aggregatedAt time.Time) error {
	query := `
		INSERT INTO usage_dashboard_aggregation_watermark (id, last_aggregated_at, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id)
		DO UPDATE SET last_aggregated_at = EXCLUDED.last_aggregated_at, updated_at = EXCLUDED.updated_at
	`
	_, err := r.sql.ExecContext(ctx, query, aggregatedAt.UTC())
	return err
}

// CleanupAggregates deletes dashboard totals and membership before the supplied
// retention cutoffs. It runs hourly totals, hourly users, daily totals, then
// daily users on the current executor and stops at the first error. Callers
// choose a transaction when all four deletions need to commit together.
func (r *AggregationStore) CleanupAggregates(ctx context.Context, hourlyCutoff, dailyCutoff time.Time) error {
	hourlyCutoffUTC := hourlyCutoff.UTC()
	dailyCutoffUTC := dailyCutoff.UTC()
	// These deletions stop at the first failure. On an autocommitting executor,
	// later tables can retain rows after an earlier table has been cleaned.
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_hourly WHERE bucket_start < $1", hourlyCutoffUTC); err != nil {
		return err
	}
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_hourly_users WHERE bucket_start < $1", hourlyCutoffUTC); err != nil {
		return err
	}
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_daily WHERE bucket_date < $1::date", dailyCutoffUTC); err != nil {
		return err
	}
	if _, err := r.sql.ExecContext(ctx, "DELETE FROM usage_dashboard_daily_users WHERE bucket_date < $1::date", dailyCutoffUTC); err != nil {
		return err
	}
	return nil
}

// CleanupUsageLogs removes retained raw usage by the table's storage layout.
// Ordinary tables delete records before cutoff in batches of
// usageLogsCleanupBatchSize until a batch is short. Partitioned tables delegate
// whole-month removal, retaining the cutoff month. SQL errors stop the pass,
// and the supplied executor determines whether earlier deletions remain
// committed.
func (r *AggregationStore) CleanupUsageLogs(ctx context.Context, cutoff time.Time) error {
	isPartitioned, err := r.isUsageLogsPartitioned(ctx)
	if err != nil {
		return err
	}
	if isPartitioned {
		return r.dropUsageLogsPartitions(ctx, cutoff)
	}
	for {
		res, err := r.sql.ExecContext(ctx, `
			WITH victims AS (
				SELECT ctid
				FROM usage_logs
				WHERE created_at < $1
				LIMIT $2
			)
			DELETE FROM usage_logs
			WHERE ctid IN (SELECT ctid FROM victims)
		`, cutoff.UTC(), usageLogsCleanupBatchSize)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		// A full batch can leave more victims for the next pass.
		if affected < usageLogsCleanupBatchSize {
			return nil
		}
	}
}

// CleanupUsageBillingDedup calls the billing archive callback bound by app with
// cutoff unchanged. An absent callback returns a binding error; the callback
// owns archival ordering and any database transaction.
func (r *AggregationStore) CleanupUsageBillingDedup(ctx context.Context, cutoff time.Time) error {
	if r.archiveDedup == nil {
		return fmt.Errorf("billing dedup archive is not bound")
	}
	return r.archiveDedup(ctx, cutoff)
}

// EnsureUsageLogsPartitions creates the previous, current, and next UTC month
// partitions around now. A nonpartitioned usage_logs table needs no work;
// creation proceeds in that order and returns the first failure.
func (r *AggregationStore) EnsureUsageLogsPartitions(ctx context.Context, now time.Time) error {
	isPartitioned, err := r.isUsageLogsPartitioned(ctx)
	if err != nil || !isPartitioned {
		return err
	}
	// Align partition names to UTC months even when now uses a local zone.
	monthStart := truncateToMonthUTC(now)
	prevMonth := monthStart.AddDate(0, -1, 0)
	nextMonth := monthStart.AddDate(0, 1, 0)

	for _, m := range []time.Time{prevMonth, monthStart, nextMonth} {
		if err := r.createUsageLogsPartition(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// insertHourlyActiveUsers adds distinct user/hour membership from raw [start,
// end) usage. The Calendar's named timezone determines the SQL hour label, and
// the current executor determines transaction ownership. Repeated requests by
// one user create one membership row for that hour.
//
// ON CONFLICT keeps existing membership when late usage is scanned again. It
// cannot remove a user after that user's requests disappear; cleanup
// recomputation clears affected membership before invoking this helper. Numeric
// hourly counts read these rows in the following aggregation phase.
func (r *AggregationStore) insertHourlyActiveUsers(ctx context.Context, start, end time.Time) error {
	tzName := r.calendar.Location().String()
	// Repeated scans encounter the same user/hour pairs. Keeping existing pairs
	// lets late requests add users without duplicating stored active-user
	// membership.
	query := `
		INSERT INTO usage_dashboard_hourly_users (bucket_start, user_id)
		SELECT DISTINCT
			date_trunc('hour', created_at AT TIME ZONE $3) AT TIME ZONE $3 AS bucket_start,
			user_id
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2 AND user_id IS NOT NULL
		ON CONFLICT DO NOTHING
	`
	_, err := r.sql.ExecContext(ctx, query, start, end, tzName)
	return err
}

// insertDailyActiveUsers adds distinct user/date membership from hourly user
// rows in [start, end). The Calendar converts each hourly timestamp to its
// local date. Multiple active hours by the same user become one row for that
// date, so daily user counts do not add hourly user counts.
//
// ON CONFLICT retains membership already stored by earlier passes. When
// deletion repair clears whole dates, callers pass every hour of those dates so
// users active outside the smaller deleted range remain represented in the
// rebuilt membership.
func (r *AggregationStore) insertDailyActiveUsers(ctx context.Context, start, end time.Time) error {
	tzName := r.calendar.Location().String()
	// A user may already appear through another hour of this date. Retain that
	// existing user/date pair so daily membership remains distinct.
	query := `
		INSERT INTO usage_dashboard_daily_users (bucket_date, user_id)
		SELECT DISTINCT
			(bucket_start AT TIME ZONE $3)::date AS bucket_date,
			user_id
		FROM usage_dashboard_hourly_users
		WHERE bucket_start >= $1 AND bucket_start < $2
		ON CONFLICT DO NOTHING
	`
	_, err := r.sql.ExecContext(ctx, query, start, end, tzName)
	return err
}

// upsertHourlyAggregates replaces numeric dashboard rows from raw usage and
// recorded hourly membership. Callers supply the scan bounds and establish
// membership before this query. SQL groups hours in the Calendar's timezone,
// while the executor determines the transaction containing the write.
//
// Provider cost uses provider_stats_cost when present, otherwise total_cost,
// multiplied by the recorded multiplier or one. Null durations contribute zero
// to the sum. Buckets with no raw records produce no replacement row, making
// prior deletion necessary when repairing an emptied bucket.
func (r *AggregationStore) upsertHourlyAggregates(ctx context.Context, start, end time.Time) error {
	tzName := r.calendar.Location().String()
	// Legacy cost rows can lack provider statistics cost or a multiplier. Apply
	// the recorded multiplier after choosing the available base for dashboard
	// provider-cost totals.
	query := `
		WITH hourly AS (
			SELECT
				date_trunc('hour', created_at AT TIME ZONE $3) AT TIME ZONE $3 AS bucket_start,
				COUNT(*) FILTER (WHERE request_source = 'user') AS total_requests,
				COALESCE(SUM(input_tokens) FILTER (WHERE request_source = 'user'), 0) AS input_tokens,
				COALESCE(SUM(output_tokens) FILTER (WHERE request_source = 'user'), 0) AS output_tokens,
				COALESCE(SUM(cache_creation_tokens) FILTER (WHERE request_source = 'user'), 0) AS cache_creation_tokens,
				COALESCE(SUM(cache_read_tokens) FILTER (WHERE request_source = 'user'), 0) AS cache_read_tokens,
				COALESCE(SUM(total_cost), 0) AS total_cost,
				COALESCE(SUM(actual_cost), 0) AS actual_cost,
				COALESCE(SUM(COALESCE(provider_stats_cost, total_cost) * COALESCE(provider_rate_multiplier, 1)), 0) AS provider_cost,
				COALESCE(SUM(COALESCE(duration_ms, 0)) FILTER (WHERE request_source = 'user'), 0) AS total_duration_ms
			FROM usage_logs
			WHERE created_at >= $1 AND created_at < $2
			GROUP BY 1
		),
		user_counts AS (
			SELECT bucket_start, COUNT(*) AS active_users
			FROM usage_dashboard_hourly_users
			WHERE bucket_start >= $1 AND bucket_start < $2
			GROUP BY bucket_start
		)
		INSERT INTO usage_dashboard_hourly (
			bucket_start,
			total_requests,
			input_tokens,
			output_tokens,
			cache_creation_tokens,
			cache_read_tokens,
			total_cost,
			actual_cost,
			provider_cost,
			total_duration_ms,
			active_users,
			computed_at
		)
		SELECT
			hourly.bucket_start,
			hourly.total_requests,
			hourly.input_tokens,
			hourly.output_tokens,
			hourly.cache_creation_tokens,
			hourly.cache_read_tokens,
			hourly.total_cost,
			hourly.actual_cost,
			hourly.provider_cost,
			hourly.total_duration_ms,
			COALESCE(user_counts.active_users, 0) AS active_users,
			NOW()
		FROM hourly
		LEFT JOIN user_counts ON user_counts.bucket_start = hourly.bucket_start
		ON CONFLICT (bucket_start)
		DO UPDATE SET
			total_requests = EXCLUDED.total_requests,
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			cache_creation_tokens = EXCLUDED.cache_creation_tokens,
			cache_read_tokens = EXCLUDED.cache_read_tokens,
			total_cost = EXCLUDED.total_cost,
			actual_cost = EXCLUDED.actual_cost,
			provider_cost = EXCLUDED.provider_cost,
			total_duration_ms = EXCLUDED.total_duration_ms,
			active_users = EXCLUDED.active_users,
			computed_at = EXCLUDED.computed_at
	`
	_, err := r.sql.ExecContext(ctx, query, start, end, tzName)
	return err
}

// upsertDailyAggregates replaces daily numeric totals from hourly rows and
// daily distinct-user membership. Both sets must be ready on the current
// executor before this query. The Calendar names the date grouping timezone,
// and [start, end) selects hourly timestamps plus daily date bounds.
//
// Active users are counted from daily membership instead of adding hourly
// counts, since one user can appear in multiple hours. Numeric request, token,
// cost, and duration totals remain additive across hours. A day with no hourly
// rows emits no replacement row.
func (r *AggregationStore) upsertDailyAggregates(ctx context.Context, start, end time.Time) error {
	tzName := r.calendar.Location().String()
	// Join each daily numeric row to its user/date count, keeping multiple
	// active hours by one user from inflating that date's active-user total.
	query := `
		WITH daily AS (
			SELECT
				(bucket_start AT TIME ZONE $5)::date AS bucket_date,
				COALESCE(SUM(total_requests), 0) AS total_requests,
				COALESCE(SUM(input_tokens), 0) AS input_tokens,
				COALESCE(SUM(output_tokens), 0) AS output_tokens,
				COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
				COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
				COALESCE(SUM(total_cost), 0) AS total_cost,
				COALESCE(SUM(actual_cost), 0) AS actual_cost,
				COALESCE(SUM(provider_cost), 0) AS provider_cost,
				COALESCE(SUM(total_duration_ms), 0) AS total_duration_ms
			FROM usage_dashboard_hourly
			WHERE bucket_start >= $1 AND bucket_start < $2
			GROUP BY (bucket_start AT TIME ZONE $5)::date
		),
		user_counts AS (
			SELECT bucket_date, COUNT(*) AS active_users
			FROM usage_dashboard_daily_users
			WHERE bucket_date >= $3::date AND bucket_date < $4::date
			GROUP BY bucket_date
		)
		INSERT INTO usage_dashboard_daily (
			bucket_date,
			total_requests,
			input_tokens,
			output_tokens,
			cache_creation_tokens,
			cache_read_tokens,
			total_cost,
			actual_cost,
			provider_cost,
			total_duration_ms,
			active_users,
			computed_at
		)
		SELECT
			daily.bucket_date,
			daily.total_requests,
			daily.input_tokens,
			daily.output_tokens,
			daily.cache_creation_tokens,
			daily.cache_read_tokens,
			daily.total_cost,
			daily.actual_cost,
			daily.provider_cost,
			daily.total_duration_ms,
			COALESCE(user_counts.active_users, 0) AS active_users,
			NOW()
		FROM daily
		LEFT JOIN user_counts ON user_counts.bucket_date = daily.bucket_date
		ON CONFLICT (bucket_date)
		DO UPDATE SET
			total_requests = EXCLUDED.total_requests,
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			cache_creation_tokens = EXCLUDED.cache_creation_tokens,
			cache_read_tokens = EXCLUDED.cache_read_tokens,
			total_cost = EXCLUDED.total_cost,
			actual_cost = EXCLUDED.actual_cost,
			provider_cost = EXCLUDED.provider_cost,
			total_duration_ms = EXCLUDED.total_duration_ms,
			active_users = EXCLUDED.active_users,
			computed_at = EXCLUDED.computed_at
	`
	_, err := r.sql.ExecContext(ctx, query, start, end, start, end, tzName)
	return err
}

// isUsageLogsPartitioned queries PostgreSQL's catalogs for a partitioned table
// named usage_logs. Query failures propagate so retention does not choose a
// deletion strategy from an unavailable catalog result.
func (r *AggregationStore) isUsageLogsPartitioned(ctx context.Context) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1
			FROM pg_partitioned_table pt
			JOIN pg_class c ON c.oid = pt.partrelid
			WHERE c.relname = 'usage_logs'
		)
	`
	var partitioned bool
	if err := scanSingleRow(ctx, r.sql, query, nil, &partitioned); err != nil {
		return false, err
	}
	return partitioned, nil
}

// dropUsageLogsPartitions removes direct usage_logs children whose
// usage_logs_YYYYMM names precede cutoff's UTC month. Unknown prefixes and
// unparsable months remain untouched. The cutoff month is retained as a whole,
// including records older than a midmonth cutoff.
//
// Discovery rows are exhausted and closed before mutation so a transaction can
// reuse its connection. Discovery matches parent names across schemas; each
// quoted child name resolves under the active search_path. The drop function
// validates that resolved table and rebuilds activity during removal, so
// callers need catalog discovery and name lookup to refer to the same parent.
// Their transaction scope determines whether several removals commit together.
func (r *AggregationStore) dropUsageLogsPartitions(ctx context.Context, cutoff time.Time) error {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT c.relname
		FROM pg_inherits
		JOIN pg_class c ON c.oid = pg_inherits.inhrelid
		JOIN pg_class p ON p.oid = pg_inherits.inhparent
		WHERE p.relname = 'usage_logs'
	`)
	if err != nil {
		return err
	}
	// Close discovery rows on scan or iteration failure before returning that
	// primary error; successful discovery also checks an explicit Close before
	// dropping partitions.
	defer func() {
		_ = rows.Close()
	}()

	cutoffMonth := truncateToMonthUTC(cutoff)
	var obsolete []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		// Child partitions may use names outside the monthly convention. Retain those
		// targets because their deletion age cannot be inferred from this name.
		if !strings.HasPrefix(name, "usage_logs_") {
			continue
		}
		suffix := strings.TrimPrefix(name, "usage_logs_")
		month, err := time.Parse("200601", suffix)
		// A nonmonthly suffix does not establish a retention interval. Keep that
		// partition and continue discovering targets with parseable UTC month names.
		if err != nil {
			continue
		}
		month = month.UTC()
		// Retain the cutoff month because partition deletion removes every contained
		// record.
		if month.Before(cutoffMonth) {
			obsolete = append(obsolete, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Finish the discovery result before executing on its transaction connection;
	// closing failure stops partition mutation while no drop has been attempted.
	if err := rows.Close(); err != nil {
		return err
	}
	for _, name := range obsolete {
		// Quoting retains the child's identifier spelling for search_path lookup.
		// The database function then validates the resolved table and rebuilds
		// activity because a drop bypasses row-deletion triggers.
		if _, err := r.sql.ExecContext(ctx, "SELECT usage_drop_partition_with_activity($1)", pq.QuoteIdentifier(name)); err != nil {
			return err
		}
	}
	return nil
}

// createUsageLogsPartition requests a usage_logs_YYYYMM partition for month's
// UTC calendar month. IF NOT EXISTS skips creation when the table name already
// exists; the upper date is next month's start, and identifiers and date
// literals are quoted before execution.
func (r *AggregationStore) createUsageLogsPartition(ctx context.Context, month time.Time) error {
	monthStart := truncateToMonthUTC(month)
	nextMonth := monthStart.AddDate(0, 1, 0)
	name := fmt.Sprintf("usage_logs_%s", monthStart.Format("200601"))
	query := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF usage_logs FOR VALUES FROM (%s) TO (%s)",
		pq.QuoteIdentifier(name),
		pq.QuoteLiteral(monthStart.Format("2006-01-02")),
		pq.QuoteLiteral(nextMonth.Format("2006-01-02")),
	)
	_, err := r.sql.ExecContext(ctx, query)
	return err
}

// truncateToDay returns midnight for t's date under the supplied dashboard
// Calendar. A zero Calendar uses time.Local, and its date construction accounts
// for the location's offset rules.
func truncateToDay(t time.Time, calendar timezone.Calendar) time.Time {
	return calendar.StartOfDay(t)
}

// truncateToMonthUTC returns UTC midnight on the first day of t's UTC month.
// Converting t before extracting its date makes partition naming and retention
// compare the same UTC month.
func truncateToMonthUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
