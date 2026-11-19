package usage

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	p "github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

// options.go defines usage-service configuration and application-supplied
// scheduling, calendar, and reporting dependencies.
// Application assembly supplies calendars, background dispatch, and deployment
// settings separately to usage services. Constructors and workers interpret
// zero-valued durations and limits at their own call sites, while callbacks
// keep scheduling, singleton acquisition, and reporting with their application
// owners.

// Usage task and artifact sentinels distinguish missing persisted rows or
// removed export files from ordinary storage failures during status and download checks.
var (
	// ErrCleanupTaskNotFound marks a missing persisted cleanup task so service
	// cancellation and worker status checks can distinguish absence from storage failure.
	ErrCleanupTaskNotFound = errors.New("usage cleanup task not found")
	// ErrExportTaskNotFound marks a missing persisted export task so cancellation,
	// worker polling, and download checks can distinguish absence from storage failure.
	ErrExportTaskNotFound = errors.New("usage export task not found")
	// ErrExportArtifactNotFound marks a missing local CSV artifact so download
	// handlers and expiry sweeps can distinguish removed files from read errors.
	ErrExportArtifactNotFound = errors.New("usage export artifact not found")
	// ErrExportActiveLimitExceeded marks an admission rejection when creating a
	// pending export task would exceed the caller's active-task quota.
	ErrExportActiveLimitExceeded = errors.New("usage export active task limit exceeded")
)

// Options carries application-owned dependencies and deployment settings for
// usage services.
type Options struct {
	Calendar timezone.Calendar // server report calendar dependency
	// RunBackground receives a diagnostic name and callback; false means dispatch
	// was rejected. App retains background-task ownership, and DashboardService
	// uses its own runner when this field is nil.
	RunBackground func(string, func()) bool  // tracked background dispatch callback
	DashboardAgg  DashboardAggregationConfig // dashboard rollup deployment settings
	UsageCleanup  UsageCleanupConfig         // administrator cleanup deployment settings
	UsageExport   UsageExportConfig          // background CSV export deployment settings
	Dashboard     DashboardConfig            // dashboard snapshot deployment settings
	// Logf receives component, format, and arguments. Nil selects logUsage, which
	// forwards the component, format, and arguments to logging.LegacyPrintf.
	Logf func(string, string, ...any) // usage component reporting callback
}

// DashboardAggregationConfig controls scheduled rollups, historical work, and
// retention limits.
type DashboardAggregationConfig struct {
	Enabled bool // scheduled aggregation deployment gate
	// IntervalSeconds and LookbackSeconds govern cadence and recent rescans.
	// DashboardAggregationService replaces cadence with resolved runtime settings
	// when bound and uses LookbackSeconds directly; DashboardService applies its
	// own constructor defaults.
	IntervalSeconds, LookbackSeconds int  // scheduled interval and live lookback seconds
	BackfillEnabled                  bool // manual backfill deployment permission
	// BackfillMaxDays bounds elapsed manual ranges using twenty-four-hour days
	// when positive. Nonpositive values leave TriggerBackfill's duration cap unset.
	BackfillMaxDays int                                 // maximum manual backfill duration days
	Retention       DashboardAggregationRetentionConfig // raw and rollup retention settings
	// RecomputeDays schedules startup range work when positive and no
	// multidimensional analytics repository is bound. The scheduler uses this
	// deployment value only for that legacy startup path.
	RecomputeDays int // legacy startup reverse-range days
}
type (
	// DashboardAggregationRetentionConfig supplies retention ages for raw records
	// and both rollup sizes.
	DashboardAggregationRetentionConfig struct {
		// UsageLogsDays, UsageBillingDedupDays, HourlyDays, and DailyDays are
		// subtracted from one cleanup instant with AddDate.
		// DashboardAggregationService retains these ages and uses the resulting
		// cutoffs directly for its retention work.
		UsageLogsDays, UsageBillingDedupDays, HourlyDays, DailyDays int /* days */
	}
	// UsageCleanupConfig controls administrator deletion admission and worker
	// batch execution.
	UsageCleanupConfig struct {
		// Enabled gates task creation, cancellation, and Start's registration of
		// worker polling when an Options value is supplied to the cleanup service.
		Enabled bool // cleanup task admission gate
		// MaxRangeDays bounds elapsed range days, BatchSize bounds each deletion,
		// WorkerIntervalSeconds sets polling, and TaskTimeoutSeconds bounds one
		// claimed task. The cleanup service selects defaults for nonpositive values.
		MaxRangeDays, BatchSize, WorkerIntervalSeconds, TaskTimeoutSeconds int // days rows seconds seconds
	}
	// UsageExportConfig controls background CSV export admission, worker pool
	// concurrency, keyset batch sizes, and artifact retention sweeps.
	UsageExportConfig struct {
		// Enabled gates export task creation, cancellation, download ticket exchange,
		// and Start's registration of worker goroutines and sweep callbacks.
		Enabled bool // export task admission gate
		// RetentionHours sets post-completion CSV retention, MaxActivePerUser bounds
		// concurrent pending/running user exports, and MaxActivePerAdmin bounds
		// concurrent pending/running administrator exports. Nonpositive values select
		// service defaults.
		RetentionHours, MaxActivePerUser, MaxActivePerAdmin int // hours tasks tasks
		// BatchSize bounds each keyset scan batch, WorkerConcurrency sets worker count,
		// WorkerIntervalSeconds sets recurring poll/sweep cadence, and TaskTimeoutSeconds
		// bounds one claimed export task and stale-running reclamation. Nonpositive
		// values select service defaults.
		BatchSize, WorkerConcurrency, WorkerIntervalSeconds, TaskTimeoutSeconds int // rows workers seconds seconds
	}
)

// DashboardConfig controls the serialized dashboard snapshot cache and refresh
// lifetimes.
type DashboardConfig struct {
	// Enabled retains the supplied shared DashboardStatsCache when true. False
	// drops that dependency at construction while per-query caches remain available.
	Enabled bool // shared dashboard cache gate
	// StatsFreshTTLSeconds selects when a cached value triggers refresh;
	// StatsTTLSeconds selects Redis expiration, and StatsRefreshTimeoutSeconds
	// bounds refresh and cache operations. Positive values override the
	// constructor defaults.
	StatsFreshTTLSeconds, StatsTTLSeconds, StatsRefreshTimeoutSeconds int // freshness expiration and refresh-timeout seconds
}

// TimingWheel schedules named recurring work and waits for cancellation of its
// callbacks.
type TimingWheel interface {
	// ScheduleRecurring registers named work to recur after each callback
	// finishes and the interval elapses. Services reserve that name for
	// cancellation; replacing a name lets an already-running callback finish.
	ScheduleRecurring(string, time.Duration, func())
	// CancelAndWait cancels the current registration for name and waits for its
	// active callbacks. Separately dispatched work keeps its own lifecycle, so
	// usage services also wait for their background workers. Calling from that
	// registration's callback waits for the caller itself.
	CancelAndWait(string)
}

// AggregationSettings supplies runtime aggregation decisions and change
// notifications to usage services.
type AggregationSettings interface {
	// UsageEnabled combines deployment availability with resolved Usage.Enabled
	// for query and scheduling decisions. PreAggregationSettingsService uses its
	// last cached settings after a read failure, or deployment defaults when no
	// cache exists.
	UsageEnabled(context.Context) bool
	// Resolve returns runtime aggregation controls including the scheduled usage
	// interval. PreAggregationSettingsService recovers read failures using its
	// last cached snapshot, including expired data, and selects deployment
	// defaults when no cached snapshot exists.
	Resolve(context.Context) p.PreAggregationSettings
	// RegisterListener retains an application-lifetime observer receiving
	// previous and next settings. PreAggregationSettingsService invokes listeners
	// synchronously after successful writes, in registration order; a listener
	// must not call that service's Update reentrantly. Nil callbacks are ignored
	// by PreAggregationSettingsService.
	RegisterListener(func(p.PreAggregationSettings, p.PreAggregationSettings))
}
type (
	// PreAggregationSettings is the shared runtime settings value consumed by
	// usage aggregation.
	PreAggregationSettings = p.PreAggregationSettings
	// SingletonLocker attempts a lease for key and owner for ttl, returning a
	// release callback and an acquired flag. Success requires a nonnil callback,
	// which the aggregation service defers until protected work returns; false
	// skips that work.
	SingletonLocker func(context.Context, string, string, time.Duration) (func(), bool)
)

// logUsage forwards component-tagged usage service diagnostics to logging.LegacyPrintf when no custom reporter is configured.
func logUsage(component, format string, args ...any) {
	logging.LegacyPrintf(component, format, args...)
}

// usageReporter returns the supplied component-aware logging callback for
// service construction. Nil options or nil Logf leave the result nil so each
// service can select logUsage when reporting.
func usageReporter(options *Options) func(string, string, ...any) {
	if options == nil {
		return nil
	}
	return options.Logf
}
