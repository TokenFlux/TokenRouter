package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// collector_queries.go queries raw request counts, error classifications,
// provider switches, and latency samples for metrics collection.
// Minute metric collection reads usage records and error observations directly
// from their business tables. Usage-row counts and consumed tokens supply
// rates, while error totals distinguish final client failures, business
// exclusions, SLA eligibility, and provider status classes. Provider-switch
// counts expand stored attempt arrays, so several switch events can contribute
// to one final request record.
//
// Latency sampling reads duration and TTFT values in separate statements
// and retains SQL absence as nil pointers. Those observations can describe
// different database snapshots from each other and from the count queries.
// Continuous percentiles are rounded to integer milliseconds, averages to one
// decimal place, and maximums use native integers. NewMetricsQueries borrows
// the SQL pool and shares it with its advisory-lock adapter; these methods
// allocate no collector result cache.

// MetricsQueries reads request observations and supplies advisory leadership
// locking to metric collection.
type MetricsQueries struct {
	// NewMetricsQueries binds Advisory and db to the same pool. Direct query-only
	// fixtures can leave Advisory nil, while production leadership uses the
	// constructed adapter.
	*Advisory // database leadership lock adapter
	// db is required for query methods and remains owned by its caller. Keep it
	// open until collector reads and advisory releases finish.
	db *sql.DB // borrowed metric query pool
}

// QueryProviderSwitchCount counts recognized switch-event prefixes in stored
// upstream arrays over the supplied half-open range, excluding count-token
// rows. Several events can count for one row; SQL or JSON array-expansion
// failures are returned.
func (c *MetricsQueries) QueryProviderSwitchCount(ctx context.Context, start, end time.Time) (int64, error) {
	// Expand each retained attempt array and recognize the kind prefix before its
	// colon, counting events rather than one switch flag per request row.
	q := `
SELECT
  COALESCE(SUM(CASE
    WHEN split_part(ev->>'kind', ':', 1) IN ('failover', 'retry_exhausted_failover', 'failover_on_400') THEN 1
    ELSE 0
  END), 0) AS switch_count
FROM ops_error_logs o
CROSS JOIN LATERAL jsonb_array_elements(
  COALESCE(NULLIF(o.upstream_errors, 'null'::jsonb), '[]'::jsonb)
) AS ev
WHERE o.created_at >= $1 AND o.created_at < $2
  AND o.is_count_tokens = FALSE`

	var count int64
	if err := c.db.QueryRowContext(ctx, q, start, end).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// QueryErrorCounts returns global final-client, business, SLA, and
// provider-status counts for the supplied half-open range. It excludes rows not
// marked is_count_tokens=false and delegates status-policy normalization to the
// shared SQL builders.
func (c *MetricsQueries) QueryErrorCounts(ctx context.Context, start, end time.Time, ignoredStatusCodes []int) (
	errorTotal int64,
	businessLimited int64,
	errorSLA int64,
	upstreamExcl429529 int64,
	upstream429 int64,
	upstream529 int64,
	err error,
) {
	const where = "WHERE created_at >= $1 AND created_at < $2 AND is_count_tokens = FALSE"
	return queryOpsErrorCounts(ctx, c.db, where, []any{start, end}, ignoredStatusCodes)
}

// queryOpsErrorCounts reads six observation counts in one statement using a
// code-owned WHERE clause and its existing arguments. Final-client totals
// require status at least 400; business and SLA counts apply their respective
// predicates within that same cutoff.
//
// Provider counts instead require the exact stored owner provider and SLA
// eligibility, then select the effective upstream status or final status into
// 429, 529, or the remaining group. They can therefore include recovered rows
// below the final-client cutoff. Nil ignored codes select defaults, while an
// allocated empty policy removes configured client-status exclusions. Query
// failures return their error before any counts are accepted.
func queryOpsErrorCounts(ctx context.Context, db *sql.DB, where string, args []any, ignoredStatusCodes []int) (
	errorTotal int64,
	businessLimited int64,
	errorCountSLA int64,
	upstreamExcl429529 int64,
	upstream429 int64,
	upstream529 int64,
	err error,
) {
	businessLimitedSQL := opsBusinessLimitedSQL("status_code", "upstream_status_code", "upstream_errors", "error_owner", "is_business_limited", ignoredStatusCodes)
	slaCountableSQL := opsSLACountableSQL("status_code", "upstream_status_code", "upstream_errors", "error_owner", "is_business_limited", ignoredStatusCodes)

	q := `
SELECT
  COALESCE(COUNT(*) FILTER (WHERE COALESCE(status_code, 0) >= 400), 0) AS error_total,
  COALESCE(COUNT(*) FILTER (WHERE COALESCE(status_code, 0) >= 400 AND ` + businessLimitedSQL + `), 0) AS business_limited,
  COALESCE(COUNT(*) FILTER (WHERE COALESCE(status_code, 0) >= 400 AND ` + slaCountableSQL + `), 0) AS error_sla,
  COALESCE(COUNT(*) FILTER (WHERE error_owner = 'provider' AND ` + slaCountableSQL + ` AND COALESCE(upstream_status_code, status_code, 0) NOT IN (429, 529)), 0) AS upstream_excl,
  COALESCE(COUNT(*) FILTER (WHERE error_owner = 'provider' AND ` + slaCountableSQL + ` AND COALESCE(upstream_status_code, status_code, 0) = 429), 0) AS upstream_429,
  COALESCE(COUNT(*) FILTER (WHERE error_owner = 'provider' AND ` + slaCountableSQL + ` AND COALESCE(upstream_status_code, status_code, 0) = 529), 0) AS upstream_529
FROM ops_error_logs
` + where

	if err := db.QueryRowContext(ctx, q, args...).Scan(
		&errorTotal,
		&businessLimited,
		&errorCountSLA,
		&upstreamExcl429529,
		&upstream429,
		&upstream529,
	); err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}
	return errorTotal, businessLimited, errorCountSLA, upstreamExcl429529, upstream429, upstream529, nil
}

// QueryUsageLatency returns duration and TTFT percentiles from nonnull
// usage observations in the supplied half-open range. The independent duration
// query includes recorded elapsed values; TTFT requires a streaming snapshot
// with ordered offsets for its captured mode. Both retain measured zero.
//
// Continuous quantiles are rounded to native integer milliseconds, averages to
// one decimal place, and available maxima narrowed to native int. Missing SQL
// aggregates leave nil pointers. If either statement fails, the method returns
// an error without its earlier observations, so callers do not persist a
// partial pair. The method performs no time validation or finite/range check
// before those conversions.
func (c *MetricsQueries) QueryUsageLatency(ctx context.Context, start, end time.Time) (duration ops.CollectedPercentiles, ttft ops.CollectedPercentiles, err error) {
	// Read duration and TTFT aggregates sequentially with the same supplied
	// window; separate statements can observe different committed database states.
	{
		q := `
SELECT
  percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms) AS p50,
  percentile_cont(0.90) WITHIN GROUP (ORDER BY duration_ms) AS p90,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) AS p95,
  percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms) AS p99,
  AVG(duration_ms) AS avg_ms,
  MAX(duration_ms) AS max_ms
FROM (SELECT * FROM usage_logs WHERE request_source = 'user') usage_logs
WHERE created_at >= $1 AND created_at < $2
  AND duration_ms IS NOT NULL`

		var p50, p90, p95, p99 sql.NullFloat64
		var avg sql.NullFloat64
		var max sql.NullInt64
		if err := c.db.QueryRowContext(ctx, q, start, end).Scan(&p50, &p90, &p95, &p99, &avg, &max); err != nil {
			return ops.CollectedPercentiles{}, ops.CollectedPercentiles{}, err
		}
		// Convert continuous quantiles to integer milliseconds, average to one
		// decimal place, and max to native int while leaving absent aggregates nil.
		duration.P50 = floatToIntPtr(p50)
		duration.P90 = floatToIntPtr(p90)
		duration.P95 = floatToIntPtr(p95)
		duration.P99 = floatToIntPtr(p99)
		if avg.Valid {
			v := ops.RoundTo1DP(avg.Float64)
			duration.Avg = &v
		}
		if max.Valid {
			v := int(max.Int64)
			duration.Max = &v
		}
	}

	{
		q := `
SELECT
  percentile_cont(0.50) WITHIN GROUP (ORDER BY ` + usageTTFTSQL("") + `) AS p50,
  percentile_cont(0.90) WITHIN GROUP (ORDER BY ` + usageTTFTSQL("") + `) AS p90,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY ` + usageTTFTSQL("") + `) AS p95,
  percentile_cont(0.99) WITHIN GROUP (ORDER BY ` + usageTTFTSQL("") + `) AS p99,
  AVG(` + usageTTFTSQL("") + `) AS avg_ms,
  MAX(` + usageTTFTSQL("") + `) AS max_ms
FROM (SELECT * FROM usage_logs WHERE request_source = 'user') usage_logs
WHERE created_at >= $1 AND created_at < $2
  AND ` + usageTTFTSQL("") + ` IS NOT NULL`

		var p50, p90, p95, p99 sql.NullFloat64
		var avg sql.NullFloat64
		var max sql.NullInt64
		if err := c.db.QueryRowContext(ctx, q, start, end).Scan(&p50, &p90, &p95, &p99, &avg, &max); err != nil {
			return ops.CollectedPercentiles{}, ops.CollectedPercentiles{}, err
		}
		// Apply the same nullable conversions to TTFT observations, whose
		// nonnull rows can differ from the duration sample population.
		ttft.P50 = floatToIntPtr(p50)
		ttft.P90 = floatToIntPtr(p90)
		ttft.P95 = floatToIntPtr(p95)
		ttft.P99 = floatToIntPtr(p99)
		if avg.Valid {
			v := ops.RoundTo1DP(avg.Float64)
			ttft.Avg = &v
		}
		if max.Valid {
			v := int(max.Int64)
			ttft.Max = &v
		}
	}

	return duration, ttft, nil
}

// QueryUsageCounts counts all matching usage rows and sums the SQL expression
// for input, output, cache-creation, and cache-read tokens. It applies the
// supplied half-open range without platform or group filters, returning zero
// aggregates for no matches.
func (c *MetricsQueries) QueryUsageCounts(ctx context.Context, start, end time.Time) (successCount int64, tokenConsumed int64, err error) {
	q := `
SELECT
  COALESCE(COUNT(*), 0) AS success_count,
  COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS token_consumed
FROM (SELECT * FROM usage_logs WHERE request_source = 'user') usage_logs
WHERE created_at >= $1 AND created_at < $2`

	var tokens sql.NullInt64
	if err := c.db.QueryRowContext(ctx, q, start, end).Scan(&successCount, &tokens); err != nil {
		return 0, 0, err
	}
	if tokens.Valid {
		tokenConsumed = tokens.Int64
	}
	return successCount, tokenConsumed, nil
}

// NewMetricsQueries returns a metric source borrowing the supplied SQL pool for
// queries and advisory locks. A nil database returns a nil interface; callers
// keep the pool alive through reads and leadership release.
func NewMetricsQueries(db *sql.DB) ops.MetricsSource {
	if db == nil {
		return nil
	}
	return &MetricsQueries{&Advisory{db}, db}
}

// usageTTFTSQL derives the same nullable delay as timing.Performance.TTFTMs.
// Alias is an internal table alias, never user input. Both ordered offsets must
// belong to the persisted exchange; absent historical JSON contributes no sample.
func usageTTFTSQL(alias string) string {
	column := "timings"
	if alias != "" {
		column = alias + ".timings"
	}
	start := "(" + column + "->'values'->>'upstream_started_ms')::bigint"
	end := "(" + column + "->'values'->>(CASE WHEN " + column + "->>'ttft_mode'='first_event' THEN 'upstream_first_event_ms' ELSE 'upstream_first_output_ms' END))::bigint"
	return "(CASE WHEN " + column + "->>'upstream_stream'='true' AND " + end + ">=" + start + " THEN " + end + "-" + start + " END)"
}

// usageTPSSQL derives average output speed from attempt start to last useful content.
// Alias is a trusted internal table alias, never user input. Eligibility and rounding
// match timing.Performance.TPS so model averages use the same estimates as usage DTOs.
//
// Single-chunk output qualifies when the attempt has a positive observed interval.
// Missing endpoints, nonstreaming exchanges, incomplete usage or nonpositive output
// tokens produce SQL NULL, keeping unavailable estimates out of averages rather than
// counting them as zero speed. First-event and first-output timestamps do not select
// this interval, so changing the TTFT mode cannot alter throughput.
func usageTPSSQL(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	column := prefix + "timings"
	elapsed := "((" + column + "->'values'->>'upstream_last_output_ms')::bigint-" +
		"(" + column + "->'values'->>'upstream_started_ms')::bigint)"
	// SQL preserves absent estimates while converting milliseconds and matching
	// the API's one-decimal rounding for eligible streams.
	return "(CASE WHEN " + column + "->>'upstream_stream'='true'" +
		" AND " + column + "->>'upstream_complete'='true'" +
		" AND " + column + "->>'usage_complete'='true'" +
		" AND " + prefix + "output_tokens>0 AND " + elapsed + ">0" +
		" THEN ROUND(" + prefix + "output_tokens*1000.0/" + elapsed + ",1) END)"
}
