package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// ops_repo_dashboard.go builds dashboard overviews from raw observations or
// covered hourly aggregates and raw fragments.
// Dashboard counts and latency samples are separate observations, so a latency
// query can exhaust its own budget while the request remains usable. That
// deadline leaves latency unavailable and can replace nonpositive current or
// peak rates with whole-window estimates in raw overviews. Caller cancellation
// and other latency errors still reject the query. Raw statements execute
// independently and do not provide one shared snapshot.
//
// Automatic selection reads covered stable UTC hours with raw fragments around
// them, then combines counts additively and latency summaries approximately.
// Duration low percentiles and means use success counts, TTFT summaries
// use measured sample counts, and high percentiles use maxima. Global-hour
// coverage relies on aligned rows produced by aggregation; dimension absence
// can mean no matching traffic. Any automatic-path error retries the complete
// raw range, while current and peak rates still read raw observations even
// after successful aggregation.

// opsRawLatencyQueryTimeout and opsRawPeakQueryTimeout limit optional expensive
// subqueries so recognized deadlines can leave counts and estimated rates
// available.
const (
	opsRawLatencyQueryTimeout = 2 * time.Second         // raw latency child budget
	opsRawPeakQueryTimeout    = 1500 * time.Millisecond // raw peak child budget
)

// errOpsRawLatencyTimeout allows both dashboard paths to omit latency when its
// own budget expires while the parent request is still active.
var (
	errOpsRawLatencyTimeout = errors.New("raw dashboard latency query exceeded its budget")
)

// opsDashboardPartial holds additive counts and approximate latency summaries
// for one selected source segment.
type opsDashboardPartial struct {
	// successCount counts all usage observations and errorCountTotal counts final
	// client failures; businessLimitedCount and errorCountSLA divide failures
	// under the supplied SLA policy. ttftSampleCount independently counts nonnull
	// TTFT measurements.
	successCount         int64 // saved usage observation count
	ttftSampleCount      int64 // nonnull TTFT sample count
	errorCountTotal      int64 // final client failure count
	businessLimitedCount int64 // SLA-excluded client failure count
	errorCountSLA        int64 // SLA-countable client failure count

	// upstreamErrorCountExcl429529, upstream429Count, and upstream529Count
	// classify SLA-countable provider observations by effective status,
	// independently of final-client totals so eligible recovered attempts can
	// contribute.
	upstreamErrorCountExcl429529 int64 // countable other provider observations
	upstream429Count             int64 // countable provider 429 observations
	upstream529Count             int64 // countable provider 529 observations

	tokenConsumed int64 // input/output/cache token total

	// duration and ttft hold pointer-valued millisecond measurements. Copying this
	// partial value shares those pointers, so segment readers treat the pointed-to
	// statistics as immutable.
	duration ops.OpsPercentiles // request duration percentile values
	ttft     ops.OpsPercentiles // TTFT latency percentile values
}

// opsHourlyMetricsRow preserves SQL measurement presence and counts while
// scanning one saved hourly dimension.
type opsHourlyMetricsRow struct {
	bucketStart time.Time // saved UTC hour start

	// successCount counts usage observations, ttftSampleCount counts measured
	// TTFT values, and errorCountTotal, businessLimitedCount, and
	// errorCountSLA retain the saved final-client classification. Their counters
	// add across selected rows.
	successCount         int64 // saved usage observation count
	ttftSampleCount      int64 // nonnull TTFT sample count
	errorCountTotal      int64 // final client failure count
	businessLimitedCount int64 // SLA-excluded client failure count
	errorCountSLA        int64 // SLA-countable client failure count

	// upstreamErrorCountExcl429529, upstream429Count, and upstream529Count
	// classify saved countable provider observations by effective status; their
	// eligibility can include recovery independently of client-failure counters.
	upstreamErrorCountExcl429529 int64 // countable other provider observations
	upstream429Count             int64 // countable provider 429 observations
	upstream529Count             int64 // countable provider 529 observations

	tokenConsumed int64 // input/output/cache token total

	// durationP50, durationP90, and durationAvg use positive successCount as
	// weight when their SQL values are valid. durationP95, durationP99, and
	// durationMax contribute maxima independently of that weight.
	durationP50 sql.NullInt64   // duration median in milliseconds
	durationP90 sql.NullInt64   // duration 90th percentile milliseconds
	durationP95 sql.NullInt64   // duration 95th percentile milliseconds
	durationP99 sql.NullInt64   // duration 99th percentile milliseconds
	durationAvg sql.NullFloat64 // mean duration in milliseconds
	durationMax sql.NullInt64   // maximum duration in milliseconds

	// ttftP50, ttftP90, and ttftAvg use positive ttftSampleCount for valid
	// measured values. ttftP95, ttftP99, and ttftMax retain valid maxima even when
	// sample weight is nonpositive.
	ttftP50 sql.NullInt64   // TTFT median in milliseconds
	ttftP90 sql.NullInt64   // TTFT 90th percentile milliseconds
	ttftP95 sql.NullInt64   // TTFT 95th percentile milliseconds
	ttftP99 sql.NullInt64   // TTFT 99th percentile milliseconds
	ttftAvg sql.NullFloat64 // mean TTFT latency milliseconds
	ttftMax sql.NullInt64   // maximum TTFT latency milliseconds
}

// opsPercentileSegment pairs a caller-selected sample weight with one
// approximate percentile summary.
type opsPercentileSegment struct {
	// weight controls p's median, 90th percentile, and mean contributions only
	// when positive. High percentiles and maximum use available values regardless
	// of weight, and readers treat p's pointers as immutable.
	weight int64              // selected measurement sample weight
	p      ops.OpsPercentiles // pointer-valued percentile summary
}

// GetDashboardOverview returns a raw or automatic Ops summary for nonzero
// filter endpoints. Invalid modes select raw; automatic mode tries covered
// hourly summaries and transparently retries the entire raw range on any error.
// OpsService validates endpoint ordering and resolves runtime source policy
// before repository calls. Returned GroupID retains the filter pointer, so
// callers keep that identity read-only while sharing either summary.
func (r *Store) GetDashboardOverview(ctx context.Context, filter *ops.OpsDashboardFilter) (*ops.OpsDashboardOverview, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		return nil, fmt.Errorf("nil filter")
	}
	if filter.StartTime.IsZero() || filter.EndTime.IsZero() {
		return nil, fmt.Errorf("start_time/end_time required")
	}

	mode := filter.QueryMode
	if !mode.IsValid() {
		mode = ops.OpsQueryModeRaw
	}

	switch mode {
	case ops.OpsQueryModeAuto:
		out, err := r.getDashboardOverviewPreaggregated(ctx, filter)
		// Retry the complete raw range after any automatic-path error, discarding its
		// incomplete work rather than adding it to the raw result.
		if err != nil {
			return r.getDashboardOverviewRaw(ctx, filter)
		}
		return out, err
	default:
		return r.getDashboardOverviewRaw(ctx, filter)
	}
}

// getDashboardOverviewRaw combines raw counts, latency, error categories, and
// rates for the supplied filter and half-open range. It expects a usable Store
// and nonnil filter. Usage and error counts execute separately, and
// zero-or-negative window seconds select one second for average-rate division.
//
// Latency and peak queries have child budgets. Exhausting the latency budget
// leaves measurements absent while the parent request is still active. Any
// recognized optional timeout can replace nonpositive current rates with
// averages and peaks with the greater of current and average, including an
// actual observed zero. Current uses the minute before end independently of
// start. Ratios divide by success plus SLA errors and round to four decimals,
// with zero denominators yielding zero and no upper clamp.
func (r *Store) getDashboardOverviewRaw(ctx context.Context, filter *ops.OpsDashboardFilter) (*ops.OpsDashboardOverview, error) {
	start := filter.StartTime.UTC()
	end := filter.EndTime.UTC()
	degraded := false

	successCount, tokenConsumed, err := r.queryUsageCounts(ctx, filter, start, end)
	if err != nil {
		return nil, err
	}

	duration, ttft, _, err := r.queryUsageLatency(ctx, filter, start, end)
	if err != nil {
		if err != errOpsRawLatencyTimeout {
			return nil, err
		}
		// Keep counts and continue building the overview. The missing measurements
		// stay empty, and the rate calculations can use the existing estimates.
		degraded = true
	}

	errorTotal, businessLimited, errorCountSLA, upstreamExcl, upstream429, upstream529, err := r.queryErrorCounts(ctx, filter, start, end)
	if err != nil {
		return nil, err
	}

	// Average rates use the entire selected duration, including idle time. A
	// nonpositive duration selects one second rather than changing the queried
	// observation range.
	windowSeconds := end.Sub(start).Seconds()
	if windowSeconds <= 0 {
		windowSeconds = 1
	}

	requestCountTotal := successCount + errorTotal
	requestCountSLA := successCount + errorCountSLA

	sla := safeDivideFloat64(float64(successCount), float64(requestCountSLA))
	errorRate := safeDivideFloat64(float64(errorCountSLA), float64(requestCountSLA))
	upstreamErrorRate := safeDivideFloat64(float64(upstreamExcl), float64(requestCountSLA))

	qpsCurrent, tpsCurrent, err := r.queryCurrentRates(ctx, filter, end)
	if err != nil {
		if isQueryTimeoutErr(err) {
			degraded = true
		} else {
			return nil, err
		}
	}

	// Budget the raw peak subquery separately, then cancel that child. Recognized
	// deadline failure enables estimates while other errors reject the dashboard
	// response.
	peakCtx, cancelPeak := context.WithTimeout(ctx, opsRawPeakQueryTimeout)
	qpsPeak, tpsPeak, err := r.queryPeakRates(peakCtx, filter, start, end)
	cancelPeak()
	if err != nil {
		if isQueryTimeoutErr(err) {
			degraded = true
		} else {
			return nil, err
		}
	}

	qpsAvg := roundTo1DP(float64(requestCountTotal) / windowSeconds)
	tpsAvg := roundTo1DP(float64(tokenConsumed) / windowSeconds)
	// A recognized optional-query deadline enables replacements for every
	// nonpositive current or peak value, even one successfully read as zero, using
	// window averages.
	if degraded {
		if qpsCurrent <= 0 {
			qpsCurrent = qpsAvg
		}
		if tpsCurrent <= 0 {
			tpsCurrent = tpsAvg
		}
		if qpsPeak <= 0 {
			qpsPeak = roundTo1DP(math.Max(qpsCurrent, qpsAvg))
		}
		if tpsPeak <= 0 {
			tpsPeak = roundTo1DP(math.Max(tpsCurrent, tpsAvg))
		}
	}

	return &ops.OpsDashboardOverview{
		StartTime: start,
		EndTime:   end,
		Platform:  strings.TrimSpace(filter.Platform),
		GroupID:   filter.GroupID,

		SuccessCount:         successCount,
		ErrorCountTotal:      errorTotal,
		BusinessLimitedCount: businessLimited,
		ErrorCountSLA:        errorCountSLA,
		RequestCountTotal:    requestCountTotal,
		RequestCountSLA:      requestCountSLA,
		TokenConsumed:        tokenConsumed,

		SLA:                          roundTo4DP(sla),
		ErrorRate:                    roundTo4DP(errorRate),
		UpstreamErrorRate:            roundTo4DP(upstreamErrorRate),
		UpstreamErrorCountExcl429529: upstreamExcl,
		Upstream429Count:             upstream429,
		Upstream529Count:             upstream529,

		QPS: ops.OpsRateSummary{
			Current: qpsCurrent,
			Peak:    qpsPeak,
			Avg:     qpsAvg,
		},
		TPS: ops.OpsRateSummary{
			Current: tpsCurrent,
			Peak:    tpsPeak,
			Avg:     tpsAvg,
		},

		Duration: duration,
		TTFT:     ttft,
	}, nil
}

// getDashboardOverviewPreaggregated combines stable full UTC hours with raw
// head and tail fragments. The stable end is capped at five minutes before the
// current time and then floored; start is ceiled to the next hour. If no full
// segment remains, the method directly uses raw. Global coverage failure
// returns an error for the outer automatic selector to retry raw.
//
// Filtered hourly absence is checked against raw sources; positive existence
// triggers raw retry, while existence-check errors are ignored and empty
// aggregate counts remain possible. Counts from nonoverlapping fragments add,
// duration summaries use success weights, and TTFT summaries use
// measured weights. Weighted medians/90th percentiles are approximations and
// high percentiles use maxima. Current and peak still query raw; recognized
// rate timeouts substitute nonpositive values from averages. Saved aggregate
// classification is used as stored, so OpsService selects raw for custom
// ignored-status policy.
func (r *Store) getDashboardOverviewPreaggregated(ctx context.Context, filter *ops.OpsDashboardFilter) (*ops.OpsDashboardOverview, error) {
	if filter == nil {
		return nil, fmt.Errorf("nil filter")
	}

	start := filter.StartTime.UTC()
	end := filter.EndTime.UTC()

	// Cap at a delayed current end, ceil the selected start, and floor the stable
	// end so raw fragments and whole-hour rows cover separate intervals.
	aggSafeEnd := preaggSafeEnd(end)
	aggFullStart := utcCeilToHour(start)
	aggFullEnd := utcFloorToHour(aggSafeEnd)

	// Use raw directly when the selected range contains no stable full hour. The
	// dashboard then applies the raw method's optional-query degradation rules.
	if !aggFullStart.Before(aggFullEnd) {
		return r.getDashboardOverviewRaw(ctx, filter)
	}
	covered, err := r.hasCompleteHourlyMetricsCoverage(ctx, aggFullStart, aggFullEnd)
	if err != nil {
		return nil, err
	}
	if !covered {
		return nil, ops.ErrOpsPreaggregatedNotPopulated
	}

	// Read selected hourly dimensions after global coverage succeeds. Filtered
	// absence can mean no traffic, so probe raw sources before treating those
	// absent dimensions as zero.
	preaggRows, err := r.listHourlyMetricsRows(ctx, filter, aggFullStart, aggFullEnd)
	if err != nil {
		return nil, err
	}
	if len(preaggRows) == 0 {
		// A matching raw observation makes empty hourly dimensions incomplete. Probe
		// errors are suppressed, allowing the empty aggregate to remain when
		// existence cannot be established.
		if exists, err := r.rawOpsDataExists(ctx, filter, aggFullStart, aggFullEnd); err == nil && exists {
			return nil, ops.ErrOpsPreaggregatedNotPopulated
		}
	}
	preagg := aggregateHourlyRows(preaggRows)

	// Keep raw observations outside the stable full-hour interval in separate head
	// and tail segments. The delayed recent tail can exceed one hour.
	head := opsDashboardPartial{}
	tail := opsDashboardPartial{}

	if start.Before(aggFullStart) {
		part, err := r.queryRawPartial(ctx, filter, start, minTime(end, aggFullStart))
		if err != nil {
			return nil, err
		}
		head = *part
	}
	if aggFullEnd.Before(end) {
		part, err := r.queryRawPartial(ctx, filter, maxTime(start, aggFullEnd), end)
		if err != nil {
			return nil, err
		}
		tail = *part
	}

	// Add counts from nonoverlapping stable, head, and tail intervals. Each was
	// read independently, so concurrent writes need not produce a common snapshot.
	successCount := preagg.successCount + head.successCount + tail.successCount
	errorTotal := preagg.errorCountTotal + head.errorCountTotal + tail.errorCountTotal
	businessLimited := preagg.businessLimitedCount + head.businessLimitedCount + tail.businessLimitedCount
	errorCountSLA := preagg.errorCountSLA + head.errorCountSLA + tail.errorCountSLA

	upstreamExcl := preagg.upstreamErrorCountExcl429529 + head.upstreamErrorCountExcl429529 + tail.upstreamErrorCountExcl429529
	upstream429 := preagg.upstream429Count + head.upstream429Count + tail.upstream429Count
	upstream529 := preagg.upstream529Count + head.upstream529Count + tail.upstream529Count

	tokenConsumed := preagg.tokenConsumed + head.tokenConsumed + tail.tokenConsumed

	// Weight low duration percentiles and mean by successes in each segment,
	// excluding missing component values. High percentile maxima approximate
	// combined tails rather than reconstructing request-level quantiles.
	duration := combineApproxPercentiles([]opsPercentileSegment{
		{weight: preagg.successCount, p: preagg.duration},
		{weight: head.successCount, p: head.duration},
		{weight: tail.successCount, p: tail.duration},
	})
	// Use nonnull TTFT sample counts separately from successes, so segments
	// containing unmeasured requests do not inflate this latency summary's
	// denominator.
	ttft := combineApproxPercentiles([]opsPercentileSegment{
		{weight: preagg.ttftSampleCount, p: preagg.ttft},
		{weight: head.ttftSampleCount, p: head.ttft},
		{weight: tail.ttftSampleCount, p: tail.ttft},
	})

	// Average rates use the entire selected duration, including idle time. A
	// nonpositive duration selects one second rather than changing the queried
	// observation range.
	windowSeconds := end.Sub(start).Seconds()
	if windowSeconds <= 0 {
		windowSeconds = 1
	}

	requestCountTotal := successCount + errorTotal
	requestCountSLA := successCount + errorCountSLA

	sla := safeDivideFloat64(float64(successCount), float64(requestCountSLA))
	errorRate := safeDivideFloat64(float64(errorCountSLA), float64(requestCountSLA))
	upstreamErrorRate := safeDivideFloat64(float64(upstreamExcl), float64(requestCountSLA))
	degraded := false

	// Read current from the raw minute before end even when historical counts use
	// saved hours. This rate observes fresh data independently of the aggregation
	// segment.
	qpsCurrent, tpsCurrent, err := r.queryCurrentRates(ctx, filter, end)
	if err != nil {
		if isQueryTimeoutErr(err) {
			degraded = true
		} else {
			return nil, err
		}
	}

	// Budget the raw peak subquery separately, then cancel that child. Recognized
	// deadline failure enables estimates while other errors reject the dashboard
	// response.
	peakCtx, cancelPeak := context.WithTimeout(ctx, opsRawPeakQueryTimeout)
	qpsPeak, tpsPeak, err := r.queryPeakRates(peakCtx, filter, start, end)
	cancelPeak()
	if err != nil {
		if isQueryTimeoutErr(err) {
			degraded = true
		} else {
			return nil, err
		}
	}

	qpsAvg := roundTo1DP(float64(requestCountTotal) / windowSeconds)
	tpsAvg := roundTo1DP(float64(tokenConsumed) / windowSeconds)
	// A recognized optional-query deadline enables replacements for every
	// nonpositive current or peak value, even one successfully read as zero, using
	// window averages.
	if degraded {
		if qpsCurrent <= 0 {
			qpsCurrent = qpsAvg
		}
		if tpsCurrent <= 0 {
			tpsCurrent = tpsAvg
		}
		if qpsPeak <= 0 {
			qpsPeak = roundTo1DP(math.Max(qpsCurrent, qpsAvg))
		}
		if tpsPeak <= 0 {
			tpsPeak = roundTo1DP(math.Max(tpsCurrent, tpsAvg))
		}
	}

	return &ops.OpsDashboardOverview{
		StartTime: start,
		EndTime:   end,
		Platform:  strings.TrimSpace(filter.Platform),
		GroupID:   filter.GroupID,

		SuccessCount:         successCount,
		ErrorCountTotal:      errorTotal,
		BusinessLimitedCount: businessLimited,
		ErrorCountSLA:        errorCountSLA,
		RequestCountTotal:    requestCountTotal,
		RequestCountSLA:      requestCountSLA,
		TokenConsumed:        tokenConsumed,

		SLA:                          roundTo4DP(sla),
		ErrorRate:                    roundTo4DP(errorRate),
		UpstreamErrorRate:            roundTo4DP(upstreamErrorRate),
		UpstreamErrorCountExcl429529: upstreamExcl,
		Upstream429Count:             upstream429,
		Upstream529Count:             upstream529,

		QPS: ops.OpsRateSummary{
			Current: qpsCurrent,
			Peak:    qpsPeak,
			Avg:     qpsAvg,
		},
		TPS: ops.OpsRateSummary{
			Current: tpsCurrent,
			Peak:    tpsPeak,
			Avg:     tpsAvg,
		},

		Duration: duration,
		TTFT:     ttft,
	}, nil
}

// hasCompleteHourlyMetricsCoverage compares global-row count with the number of
// whole UTC hours in the floored half-open range. Empty or unordered floored
// ranges return true. The check requires aligned global buckets produced by
// aggregation and their unique dimension index; it does not inspect every
// individual timestamp or freshness.
//
// Filtered platform/group dimensions can legitimately have no traffic rows, so
// only null platform and null group establish this marker. Equal row count is
// the acceptance condition, and irregular timestamps could satisfy that count
// if written outside the aligned aggregation path. Database errors return false
// with the error.
func (r *Store) hasCompleteHourlyMetricsCoverage(ctx context.Context, start, end time.Time) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("nil ops repository")
	}
	start = utcFloorToHour(start.UTC())
	end = utcFloorToHour(end.UTC())
	if !start.Before(end) {
		return true, nil
	}
	// Count expected whole hours after flooring both endpoints. Aligned global
	// buckets and their unique dimension index let row count stand in for
	// coverage.
	expected := int64(end.Sub(start) / time.Hour)
	var actual int64
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM ops_metrics_hourly
		WHERE bucket_start >= $1 AND bucket_start < $2
		  AND platform IS NULL AND group_id IS NULL
	`, start, end).Scan(&actual)
	if err != nil {
		return false, err
	}
	return actual == expected, nil
}

// listHourlyMetricsRows reads ordered saved hourly dimensions for valid nonzero
// increasing endpoints, returning an empty slice for other ranges. Positive
// GroupID selects that group across platforms unless platform is supplied;
// platform alone selects platform summary rows with null group; no positive
// dimension selects global rows.
//
// Platform is trimmed and lowercased, times become UTC, and stored
// classification/nullable latency values are retained for aggregateHourlyRows.
// Group selection uses saved identities rather than current membership. This
// read follows a separate global coverage statement, so writes between them can
// change what is available. Query, scan, and cursor errors reject the row set.
func (r *Store) listHourlyMetricsRows(ctx context.Context, filter *ops.OpsDashboardFilter, start, end time.Time) ([]opsHourlyMetricsRow, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return []opsHourlyMetricsRow{}, nil
	}

	where := "bucket_start >= $1 AND bucket_start < $2"
	args := []any{start.UTC(), end.UTC()}
	idx := 3

	platform := ""
	groupID := (*int64)(nil)
	if filter != nil {
		platform = strings.TrimSpace(strings.ToLower(filter.Platform))
		groupID = filter.GroupID
	}

	// Select one stored dimension level to avoid counting both global and platform
	// summaries. Group filtering can sum that group's rows across saved platforms.
	switch {
	case groupID != nil && *groupID > 0:
		where += fmt.Sprintf(" AND group_id = $%d", idx)
		args = append(args, *groupID)
		idx++
		if platform != "" {
			where += fmt.Sprintf(" AND platform = $%d", idx)
			args = append(args, platform)
		}
	case platform != "":
		where += fmt.Sprintf(" AND platform = $%d AND group_id IS NULL", idx)
		args = append(args, platform)
	default:
		where += " AND platform IS NULL AND group_id IS NULL"
	}

	q := `
SELECT
  bucket_start,
  success_count,
  error_count_total,
  business_limited_count,
  error_count_sla,
  upstream_error_count_excl_429_529,
  upstream_429_count,
  upstream_529_count,
  token_consumed,
  duration_p50_ms,
  duration_p90_ms,
  duration_p95_ms,
  duration_p99_ms,
  duration_avg_ms,
  duration_max_ms,
  ttft_p50_ms,
  ttft_p90_ms,
  ttft_p95_ms,
  ttft_p99_ms,
  ttft_avg_ms,
  ttft_max_ms,
  ttft_sample_count
FROM ops_metrics_hourly
WHERE ` + where + `
ORDER BY bucket_start ASC`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	// Close the row cursor after scanning or early failure. Cleanup errors are
	// suppressed, while rows.Err still rejects an incomplete hourly row set.
	defer func() { _ = rows.Close() }()

	out := make([]opsHourlyMetricsRow, 0, 64)
	for rows.Next() {
		var row opsHourlyMetricsRow
		if err := rows.Scan(
			&row.bucketStart,
			&row.successCount,
			&row.errorCountTotal,
			&row.businessLimitedCount,
			&row.errorCountSLA,
			&row.upstreamErrorCountExcl429529,
			&row.upstream429Count,
			&row.upstream529Count,
			&row.tokenConsumed,
			&row.durationP50,
			&row.durationP90,
			&row.durationP95,
			&row.durationP99,
			&row.durationAvg,
			&row.durationMax,
			&row.ttftP50,
			&row.ttftP90,
			&row.ttftP95,
			&row.ttftP99,
			&row.ttftAvg,
			&row.ttftMax,
			&row.ttftSampleCount,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// aggregateHourlyRows adds selected counts and combines nullable latency
// summaries into one partial result. Empty input yields zero counts and absent
// measurements. Median, 90th percentile, and mean require both valid SQL values
// and positive weight; duration uses all successes of each contributing row
// while TTFT values use measured samples.
//
// Each component has its own denominator so missing values do not contribute to
// that component. High percentiles and maxima select available largest values
// regardless of weights, which can retain a measurement even when its sample
// count is nonpositive. Weighted values round to integer milliseconds, and SQL
// integer values narrow to native int unchecked. Counter and weight sums have
// no overflow validation, and resulting percentiles approximate the original
// request distribution.
func aggregateHourlyRows(rows []opsHourlyMetricsRow) opsDashboardPartial {
	out := opsDashboardPartial{}
	if len(rows) == 0 {
		return out
	}

	var (
		p50Sum float64
		p50W   int64
		p90Sum float64
		p90W   int64
		avgSum float64
		avgW   int64
	)
	var (
		ttftP50Sum float64
		ttftP50W   int64
		ttftP90Sum float64
		ttftP90W   int64
		ttftAvgSum float64
		ttftAvgW   int64
	)

	var (
		p95Max *int
		p99Max *int
		maxMax *int

		ttftP95Max *int
		ttftP99Max *int
		ttftMaxMax *int
	)

	for _, row := range rows {
		out.successCount += row.successCount
		out.ttftSampleCount += row.ttftSampleCount
		out.errorCountTotal += row.errorCountTotal
		out.businessLimitedCount += row.businessLimitedCount
		out.errorCountSLA += row.errorCountSLA

		out.upstreamErrorCountExcl429529 += row.upstreamErrorCountExcl429529
		out.upstream429Count += row.upstream429Count
		out.upstream529Count += row.upstream529Count

		out.tokenConsumed += row.tokenConsumed

		// Use each available duration component's own positive-weight denominator;
		// all successes of a contributing hour supply weight even when some durations
		// were missing.
		if row.successCount > 0 {
			if row.durationP50.Valid {
				p50Sum += float64(row.durationP50.Int64) * float64(row.successCount)
				p50W += row.successCount
			}
			if row.durationP90.Valid {
				p90Sum += float64(row.durationP90.Int64) * float64(row.successCount)
				p90W += row.successCount
			}
			if row.durationAvg.Valid {
				avgSum += row.durationAvg.Float64 * float64(row.successCount)
				avgW += row.successCount
			}
		}

		// TTFT weight counts measured values rather than every usage success.
		// A missing component is excluded from its own denominator even when other
		// TTFT values are present.
		if row.ttftSampleCount > 0 {
			if row.ttftP50.Valid {
				ttftP50Sum += float64(row.ttftP50.Int64) * float64(row.ttftSampleCount)
				ttftP50W += row.ttftSampleCount
			}
			if row.ttftP90.Valid {
				ttftP90Sum += float64(row.ttftP90.Int64) * float64(row.ttftSampleCount)
				ttftP90W += row.ttftSampleCount
			}
			if row.ttftAvg.Valid {
				ttftAvgSum += row.ttftAvg.Float64 * float64(row.ttftSampleCount)
				ttftAvgW += row.ttftSampleCount
			}
		}

		// Select valid high percentile and maximum values independently of weight,
		// keeping their greatest observed hourly value while narrowing SQL integers
		// unchecked.
		if row.durationP95.Valid {
			v := int(row.durationP95.Int64)
			if p95Max == nil || v > *p95Max {
				p95Max = &v
			}
		}
		if row.durationP99.Valid {
			v := int(row.durationP99.Int64)
			if p99Max == nil || v > *p99Max {
				p99Max = &v
			}
		}
		if row.durationMax.Valid {
			v := int(row.durationMax.Int64)
			if maxMax == nil || v > *maxMax {
				maxMax = &v
			}
		}

		if row.ttftP95.Valid {
			v := int(row.ttftP95.Int64)
			if ttftP95Max == nil || v > *ttftP95Max {
				ttftP95Max = &v
			}
		}
		if row.ttftP99.Valid {
			v := int(row.ttftP99.Int64)
			if ttftP99Max == nil || v > *ttftP99Max {
				ttftP99Max = &v
			}
		}
		if row.ttftMax.Valid {
			v := int(row.ttftMax.Int64)
			if ttftMaxMax == nil || v > *ttftMaxMax {
				ttftMaxMax = &v
			}
		}
	}

	// Round weighted duration estimates to integer milliseconds; independent
	// denominators keep wholly missing components nil while maxima retain their
	// selected measurements.
	if p50W > 0 {
		v := int(math.Round(p50Sum / float64(p50W)))
		out.duration.P50 = &v
	}
	if p90W > 0 {
		v := int(math.Round(p90Sum / float64(p90W)))
		out.duration.P90 = &v
	}
	out.duration.P95 = p95Max
	out.duration.P99 = p99Max
	if avgW > 0 {
		v := int(math.Round(avgSum / float64(avgW)))
		out.duration.Avg = &v
	}
	out.duration.Max = maxMax

	// Round weighted TTFT estimates using measured-sample denominators.
	// High percentile and maximum pointers already represent the greatest
	// available values, independently of weights.
	if ttftP50W > 0 {
		v := int(math.Round(ttftP50Sum / float64(ttftP50W)))
		out.ttft.P50 = &v
	}
	if ttftP90W > 0 {
		v := int(math.Round(ttftP90Sum / float64(ttftP90W)))
		out.ttft.P90 = &v
	}
	out.ttft.P95 = ttftP95Max
	out.ttft.P99 = ttftP99Max
	if ttftAvgW > 0 {
		v := int(math.Round(ttftAvgSum / float64(ttftAvgW)))
		out.ttft.Avg = &v
	}
	out.ttft.Max = ttftMaxMax

	return out
}

// queryRawPartial combines raw usage counts, latency measurements, and error
// categories for one segment using separate statements. A usable Store and
// nonnil filter are required. Exhausting the latency budget leaves counts
// available while returning absent duration/TTFT measurements and zero
// TTFT sample weight.
//
// Other latency errors and either count failure reject the segment. The child
// latency budget inherits caller cancellation, while counts use the supplied
// context. Zero TTFT weight prevents an unavailable latency segment from
// diluting combined TTFT averages; duration still retains its success
// count but absent component values are excluded by the combination helper.
func (r *Store) queryRawPartial(ctx context.Context, filter *ops.OpsDashboardFilter, start, end time.Time) (*opsDashboardPartial, error) {
	successCount, tokenConsumed, err := r.queryUsageCounts(ctx, filter, start, end)
	if err != nil {
		return nil, err
	}

	// A segment that exhausts the latency budget still contributes its counts.
	// The empty measurements and zero TTFT weight leave other segments' summaries usable.
	duration, ttft, ttftSampleCount, err := r.queryUsageLatency(ctx, filter, start, end)
	if err != nil && err != errOpsRawLatencyTimeout {
		return nil, err
	}

	errorTotal, businessLimited, errorCountSLA, upstreamExcl, upstream429, upstream529, err := r.queryErrorCounts(ctx, filter, start, end)
	if err != nil {
		return nil, err
	}

	return &opsDashboardPartial{
		successCount:                 successCount,
		ttftSampleCount:              ttftSampleCount,
		errorCountTotal:              errorTotal,
		businessLimitedCount:         businessLimited,
		errorCountSLA:                errorCountSLA,
		upstreamErrorCountExcl429529: upstreamExcl,
		upstream429Count:             upstream429,
		upstream529Count:             upstream529,
		tokenConsumed:                tokenConsumed,
		duration:                     duration,
		ttft:                         ttft,
	}, nil
}

// rawOpsDataExists checks usage first and then non-probe error observations in
// the supplied half-open range. It expects a usable Store and delegates
// saved-dimension filters to the shared builders; any matching error
// phase/status can establish existence, and query failures return an error.
func (r *Store) rawOpsDataExists(ctx context.Context, filter *ops.OpsDashboardFilter, start, end time.Time) (bool, error) {
	{
		join, where, args, _ := buildUsageWhere(filter, start, end, 1)
		q := `SELECT EXISTS(SELECT 1 FROM (SELECT * FROM usage_logs WHERE request_source = 'user') ul ` + join + ` ` + where + ` LIMIT 1)`
		var exists bool
		if err := r.db.QueryRowContext(ctx, q, args...).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}

	{
		where, args, _ := buildErrorWhere(filter, start, end, 1)
		q := `SELECT EXISTS(SELECT 1 FROM ops_error_logs ` + where + ` LIMIT 1)`
		var exists bool
		if err := r.db.QueryRowContext(ctx, q, args...).Scan(&exists); err != nil {
			return false, err
		}
		return exists, nil
	}
}

// combineApproxPercentiles combines supplied measurement segments into
// integer-millisecond estimates. Median, 90th percentile, and mean use positive
// segment weights and present values, with a separate denominator for each
// component. No contributing weight yields nil for that weighted component.
//
// High percentiles and maximum select the greatest present value regardless of
// segment weight, including zero or negative weight. New scalar pointers are
// allocated for selected values and rounded weighted results, so callers can
// read the result independently of input pointer storage. Summation and
// conversion have no finite/range validation, and weighting saved percentiles
// cannot recover exact combined quantiles.
func combineApproxPercentiles(segments []opsPercentileSegment) ops.OpsPercentiles {
	// Compute each weighted component separately, skipping missing values and
	// nonpositive weights. Positive contributions determine its denominator, while
	// the maximum path below ignores weights.
	weightedInt := func(get func(ops.OpsPercentiles) *int) *int {
		var sum float64
		var w int64
		for _, seg := range segments {
			if seg.weight <= 0 {
				continue
			}
			v := get(seg.p)
			if v == nil {
				continue
			}
			sum += float64(*v) * float64(seg.weight)
			w += seg.weight
		}
		if w <= 0 {
			return nil
		}
		// OpsPercentiles exposes integer milliseconds, so round each weighted
		// component after division and allocate its scalar rather than sharing an
		// input pointer.
		out := int(math.Round(sum / float64(w)))
		return &out
	}

	// Copy the greatest present scalar for each high percentile or maximum.
	// Ignoring sample weight permits these available tails to survive otherwise
	// excluded weighted segments.
	maxInt := func(get func(ops.OpsPercentiles) *int) *int {
		var max *int
		for _, seg := range segments {
			v := get(seg.p)
			if v == nil {
				continue
			}
			if max == nil || *v > *max {
				c := *v
				max = &c
			}
		}
		return max
	}

	return ops.OpsPercentiles{
		P50: weightedInt(func(p ops.OpsPercentiles) *int { return p.P50 }),
		P90: weightedInt(func(p ops.OpsPercentiles) *int { return p.P90 }),
		P95: maxInt(func(p ops.OpsPercentiles) *int { return p.P95 }),
		P99: maxInt(func(p ops.OpsPercentiles) *int { return p.P99 }),
		Avg: weightedInt(func(p ops.OpsPercentiles) *int { return p.Avg }),
		Max: maxInt(func(p ops.OpsPercentiles) *int { return p.Max }),
	}
}

// preaggSafeEnd caps a requested end at five minutes before the current UTC
// time. Ends at or before that cutoff remain supplied; the caller floors the
// result to whole hours, leaving recent traffic in raw fragments.
func preaggSafeEnd(endTime time.Time) time.Time {
	now := time.Now().UTC()
	// Delay current aggregation coverage by five minutes before hour flooring.
	cutoff := now.Add(-5 * time.Minute)
	if endTime.After(cutoff) {
		return cutoff
	}
	return endTime
}

// utcCeilToHour returns the first UTC hour at or after t.
func utcCeilToHour(t time.Time) time.Time {
	u := t.UTC()
	f := u.Truncate(time.Hour)
	if f.Equal(u) {
		return f
	}
	return f.Add(time.Hour)
}

// utcFloorToHour returns the whole UTC hour at or before t.
func utcFloorToHour(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// minTime returns the earlier supplied timestamp, choosing b when equal.
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// maxTime returns the later supplied timestamp, choosing b when equal.
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// queryUsageCounts returns raw usage-row count and summed input/output/cache
// tokens in a half-open range. Shared filtering uses saved platform and
// positive group identity; nil filter leaves both dimensions unrestricted, and
// an absent token sum becomes zero.
func (r *Store) queryUsageCounts(ctx context.Context, filter *ops.OpsDashboardFilter, start, end time.Time) (successCount int64, tokenConsumed int64, err error) {
	join, where, args, _ := buildUsageWhere(filter, start, end, 1)

	q := `
SELECT
  COALESCE(COUNT(*), 0) AS success_count,
  COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS token_consumed
FROM (SELECT * FROM usage_logs WHERE request_source = 'user') ul
` + join + `
` + where

	var tokens sql.NullInt64
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(&successCount, &tokens); err != nil {
		return 0, 0, err
	}
	if tokens.Valid {
		tokenConsumed = tokens.Int64
	}
	return successCount, tokenConsumed, nil
}

// queryUsageLatency returns duration and TTFT percentile measurements
// from raw usage rows plus the count of nonnull TTFT samples. Shared
// filtering uses the supplied half-open range and saved dimensions. SQL
// excludes missing measurement values from each statistic, while recorded zero
// still contributes.
//
// Percentiles and means are rounded into newly allocated native-int pointers,
// preserving absence as nil. The returned sample count weights TTFT summaries
// when combining raw fragments with saved hours. If the query reaches its own
// deadline while the parent remains active, errOpsRawLatencyTimeout accompanies
// empty summaries and a zero sample count. Other query failures are returned
// to the caller.
func (r *Store) queryUsageLatency(ctx context.Context, filter *ops.OpsDashboardFilter, start, end time.Time) (duration ops.OpsPercentiles, ttft ops.OpsPercentiles, ttftSampleCount int64, err error) {
	join, where, args, _ := buildUsageWhere(filter, start, end, 1)
	// Materialize the two measurements after filtering. PostgreSQL can reuse the
	// computed TTFT across aggregates without decoding the same timing JSON for each one.
	q := `
WITH latency_samples AS MATERIALIZED (
  SELECT duration_ms, ` + usageTTFTSQL("ul") + ` AS ttft_ms
  FROM (SELECT * FROM usage_logs WHERE request_source = 'user') ul
  ` + join + `
  ` + where + `
)
SELECT
  percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS duration_p50,
  percentile_cont(0.90) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS duration_p90,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS duration_p95,
  percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS duration_p99,
  AVG(duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS duration_avg,
  MAX(duration_ms) AS duration_max,
  percentile_cont(0.50) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL) AS ttft_p50,
  percentile_cont(0.90) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL) AS ttft_p90,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL) AS ttft_p95,
  percentile_cont(0.99) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL) AS ttft_p99,
  AVG(ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL) AS ttft_avg,
  MAX(ttft_ms) AS ttft_max,
  COUNT(ttft_ms) AS ttft_sample_count
FROM latency_samples`

	var dP50, dP90, dP95, dP99 sql.NullFloat64
	var dAvg sql.NullFloat64
	var dMax sql.NullInt64
	var tP50, tP90, tP95, tP99 sql.NullFloat64
	var tAvg sql.NullFloat64
	var tMax sql.NullInt64
	var tCount int64
	// Reserve the child deadline for latency so later counts and rates can use
	// the parent context. Release the timer after the query and its error classification.
	latencyCtx, cancel := context.WithTimeout(ctx, opsRawLatencyQueryTimeout)
	defer cancel()
	if err := r.db.QueryRowContext(latencyCtx, q, args...).Scan(
		&dP50, &dP90, &dP95, &dP99, &dAvg, &dMax,
		&tP50, &tP90, &tP95, &tP99, &tAvg, &tMax, &tCount,
	); err != nil {
		// lib/pq can surface cancellation as SQLSTATE 57014. Check the child's
		// deadline and the parent's status before treating that code as recoverable.
		if latencyCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil &&
			(isQueryTimeoutErr(err) || infra.SQLState(err) == "57014") {
			return ops.OpsPercentiles{}, ops.OpsPercentiles{}, 0, errOpsRawLatencyTimeout
		}
		return ops.OpsPercentiles{}, ops.OpsPercentiles{}, 0, err
	}

	// Round available SQL percentiles and means into fresh integer pointers,
	// preserving missing measurements as nil; maximum values narrow SQL integers
	// without range validation.
	duration.P50 = floatToIntPtr(dP50)
	duration.P90 = floatToIntPtr(dP90)
	duration.P95 = floatToIntPtr(dP95)
	duration.P99 = floatToIntPtr(dP99)
	duration.Avg = floatToIntPtr(dAvg)
	if dMax.Valid {
		v := int(dMax.Int64)
		duration.Max = &v
	}

	ttft.P50 = floatToIntPtr(tP50)
	ttft.P90 = floatToIntPtr(tP90)
	ttft.P95 = floatToIntPtr(tP95)
	ttft.P99 = floatToIntPtr(tP99)
	ttft.Avg = floatToIntPtr(tAvg)
	if tMax.Valid {
		v := int(tMax.Int64)
		ttft.Max = &v
	}

	return duration, ttft, tCount, nil
}

// queryErrorCounts returns raw client-failure and provider-status categories
// for a nonnil filter. buildErrorWhere excludes count-token probes and applies
// saved dimensions; queryOpsErrorCounts applies the supplied ignored-status
// policy to its own shared classification expressions.
func (r *Store) queryErrorCounts(ctx context.Context, filter *ops.OpsDashboardFilter, start, end time.Time) (
	errorTotal int64,
	businessLimited int64,
	errorCountSLA int64,
	upstreamExcl429529 int64,
	upstream429 int64,
	upstream529 int64,
	err error,
) {
	where, args, _ := buildErrorWhere(filter, start, end, 1)
	return queryOpsErrorCounts(ctx, r.db, where, args, filter.IgnoredStatusCodes)
}

// queryCurrentRates returns one-decimal request and token rates over the minute
// ending at end. It expects a usable Store and nonnil filter. Usage and error
// counts are independent reads, and this interval can precede the selected
// start for a window shorter than a minute.
//
// Requests add usage and final-client failures, while tokens use usage alone.
// Both rates divide by sixty seconds, including idle time; their queries use
// the caller context with no separate local timeout. A count error rejects the
// two rates, allowing the owning dashboard method to apply its recognized
// deadline policy.
func (r *Store) queryCurrentRates(ctx context.Context, filter *ops.OpsDashboardFilter, end time.Time) (qpsCurrent float64, tpsCurrent float64, err error) {
	// Read the entire minute before end independently of selected start; divide
	// later by sixty seconds so idle intervals remain part of the rate.
	windowStart := end.Add(-1 * time.Minute)

	successCount1m, token1m, err := r.queryUsageCounts(ctx, filter, windowStart, end)
	if err != nil {
		return 0, 0, err
	}
	errorCount1m, _, _, _, _, _, err := r.queryErrorCounts(ctx, filter, windowStart, end)
	if err != nil {
		return 0, 0, err
	}

	qpsCurrent = roundTo1DP(float64(successCount1m+errorCount1m) / 60.0)
	tpsCurrent = roundTo1DP(float64(token1m) / 60.0)
	return qpsCurrent, tpsCurrent, nil
}

// queryPeakRates finds the greatest raw request and token counts in minute
// buckets within the supplied half-open range. It expects a usable Store and
// nonnil filter. Usage and final-client failures combine by bucket, while
// count-token errors are excluded and tokens remain from usage only.
//
// One statement reads both sources, and minute grouping follows the SQL session
// timezone. Positive maxima divide by sixty seconds and round to one decimal
// even for partial endpoint minutes; null, zero, or negative maxima leave zero
// rates. The caller supplies any child budget and decides whether a recognized
// deadline permits a degraded summary.
func (r *Store) queryPeakRates(ctx context.Context, filter *ops.OpsDashboardFilter, start, end time.Time) (qpsPeak float64, tpsPeak float64, err error) {
	usageJoin, usageWhere, usageArgs, next := buildUsageWhere(filter, start, end, 1)
	errorWhere, errorArgs, _ := buildErrorWhere(filter, start, end, next)

	q := `
WITH usage_buckets AS (
  SELECT
    date_trunc('minute', ul.created_at) AS bucket,
    COUNT(*) AS req_cnt,
    COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS token_cnt
  FROM (SELECT * FROM usage_logs WHERE request_source = 'user') ul
  ` + usageJoin + `
  ` + usageWhere + `
  GROUP BY 1
),
error_buckets AS (
  SELECT date_trunc('minute', created_at) AS bucket, COUNT(*) AS err_cnt
  FROM ops_error_logs
  ` + errorWhere + `
    AND COALESCE(status_code, 0) >= 400
  GROUP BY 1
),
combined AS (
  SELECT COALESCE(u.bucket, e.bucket) AS bucket,
         COALESCE(u.req_cnt, 0) + COALESCE(e.err_cnt, 0) AS total_req,
         COALESCE(u.token_cnt, 0) AS total_tokens
  FROM usage_buckets u
  FULL OUTER JOIN error_buckets e ON u.bucket = e.bucket
)
SELECT
  COALESCE(MAX(total_req), 0) AS max_req_per_min,
  COALESCE(MAX(total_tokens), 0) AS max_tokens_per_min
FROM combined`

	args := append(usageArgs, errorArgs...)

	var maxReqPerMinute, maxTokensPerMinute sql.NullInt64
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(&maxReqPerMinute, &maxTokensPerMinute); err != nil {
		return 0, 0, err
	}
	// Convert positive bucket maxima to per-second rates using a full minute,
	// including partial endpoint minutes; absent or nonpositive maxima leave the
	// zero result.
	if maxReqPerMinute.Valid && maxReqPerMinute.Int64 > 0 {
		qpsPeak = roundTo1DP(float64(maxReqPerMinute.Int64) / 60.0)
	}
	if maxTokensPerMinute.Valid && maxTokensPerMinute.Int64 > 0 {
		tpsPeak = roundTo1DP(float64(maxTokensPerMinute.Int64) / 60.0)
	}
	return qpsPeak, tpsPeak, nil
}

// isQueryTimeoutErr recognizes an error in the context.DeadlineExceeded chain.
// Optional rate queries use this check directly. The latency query also checks
// the owning context and accepts a typed driver cancellation when its budget expires.
func isQueryTimeoutErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// buildUsageWhere returns a half-open raw-usage predicate and ordered arguments
// starting at a positive caller-supplied placeholder index. Nil filter leaves
// saved dimensions unrestricted; only positive GroupID and nonblank folded
// platform add conditions, and the returned join remains empty.
func buildUsageWhere(filter *ops.OpsDashboardFilter, start, end time.Time, startIndex int) (join string, where string, args []any, nextIndex int) {
	platform := ""
	groupID := (*int64)(nil)
	if filter != nil {
		platform = strings.TrimSpace(strings.ToLower(filter.Platform))
		groupID = filter.GroupID
	}

	idx := startIndex
	clauses := make([]string, 0, 4)
	args = make([]any, 0, 4)

	args = append(args, start)
	clauses = append(clauses, fmt.Sprintf("ul.created_at >= $%d", idx))
	idx++
	args = append(args, end)
	clauses = append(clauses, fmt.Sprintf("ul.created_at < $%d", idx))
	idx++

	if groupID != nil && *groupID > 0 {
		args = append(args, *groupID)
		clauses = append(clauses, fmt.Sprintf("ul.group_id = $%d", idx))
		idx++
	}
	if platform != "" {
		// Use the saved execution platform so historical selection follows the
		// observed request even after provider settings or group membership change.
		args = append(args, platform)
		clauses = append(clauses, fmt.Sprintf("ul.platform = $%d", idx))
		idx++
	}

	where = "WHERE " + strings.Join(clauses, " AND ")
	return join, where, args, idx
}

// buildErrorWhere returns half-open raw-error predicates and arguments from a
// positive supplied placeholder index. Nil filter leaves dimensions
// unrestricted; count-token eligibility requires is_count_tokens FALSE, while
// positive GroupID and folded nonblank platform add saved-dimension
// restrictions.
func buildErrorWhere(filter *ops.OpsDashboardFilter, start, end time.Time, startIndex int) (where string, args []any, nextIndex int) {
	platform := ""
	groupID := (*int64)(nil)
	if filter != nil {
		platform = strings.TrimSpace(strings.ToLower(filter.Platform))
		groupID = filter.GroupID
	}

	idx := startIndex
	clauses := make([]string, 0, 5)
	args = make([]any, 0, 5)

	args = append(args, start)
	clauses = append(clauses, fmt.Sprintf("created_at >= $%d", idx))
	idx++
	args = append(args, end)
	clauses = append(clauses, fmt.Sprintf("created_at < $%d", idx))
	idx++

	clauses = append(clauses, "is_count_tokens = FALSE")

	if groupID != nil && *groupID > 0 {
		args = append(args, *groupID)
		clauses = append(clauses, fmt.Sprintf("group_id = $%d", idx))
		idx++
	}
	if platform != "" {
		args = append(args, platform)
		clauses = append(clauses, fmt.Sprintf("platform = $%d", idx))
		idx++
	}

	where = "WHERE " + strings.Join(clauses, " AND ")
	return where, args, idx
}

// floatToIntPtr rounds a valid SQL float to a new native-int pointer, retaining
// missing SQL data as nil. Conversion is unchecked, so callers rely on
// supported finite measurement values and native integer range.
func floatToIntPtr(v sql.NullFloat64) *int {
	if !v.Valid {
		return nil
	}
	// Latency aggregates become whole milliseconds, with halfway values rounded
	// away from zero.
	n := int(math.Round(v.Float64))
	return &n
}

// safeDivideFloat64 divides supplied values with zero returned for a zero
// denominator. Other denominators, including negative values, pass through
// ordinary floating-point division with no finite or range validation.
func safeDivideFloat64(numerator float64, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

// roundTo1DP rounds a supplied value to one decimal place.
func roundTo1DP(v float64) float64 {
	// Display rates round halfway values away from zero at this decimal precision.
	return math.Round(v*10) / 10
}

// roundTo4DP rounds a supplied ratio to four decimal places.
func roundTo4DP(v float64) float64 {
	// Dashboard ratios retain four decimals, rounding halfway values away from zero.
	return math.Round(v*10000) / 10000
}
