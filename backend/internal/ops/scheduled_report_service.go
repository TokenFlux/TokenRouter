package ops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
)

// scheduled_report_service.go schedules operational email reports and builds
// summary or detailed content from monitoring observations.
// Scheduled reports use local cron times to decide when to summarize an elapsed
// UTC window. A Redis marker records an attempt before content generation, and
// a leader lease gates the scheduler when Redis is configured. Missing Redis
// permits execution with neither mechanism, while Redis claim failures skip
// that cycle.
//
// Summary HTML and editable metric placeholders share one queried overview.
// Detailed reports supply escaped HTML and missing summary values. Recipient
// attempts can complete despite suppressed mail errors, so successful job
// heartbeats describe scheduler completion rather than confirmed delivery to
// every address. Start and Stop require caller ordering because their
// independent once guards do not serialize publication of the cancellation
// handle.

// opsScheduledReportJobName and the scheduling constants identify heartbeat
// storage, Redis leadership, per-kind attempt markers, and the scheduler's
// minute polling cadence.
const (
	opsScheduledReportJobName = "ops_scheduled_reports" // report heartbeat identity

	// shared report lease.
	opsScheduledReportLeaderLockKeyDefault = "ops:scheduled_reports:leader"
	// unrenewed lease lifetime.
	opsScheduledReportLeaderLockTTLDefault = 5 * time.Minute

	// report-kind marker prefix.
	opsScheduledReportLastRunKeyPrefix = "ops:scheduled_reports:last_run:"

	opsScheduledReportTickInterval = 1 * time.Minute // report eligibility cadence
)

// opsScheduledReportCronParser accepts five schedule fields, while
// notificationEmailOpsSummaryPlaceholders names the notification renderer's
// shared summary variables filled from actual report data.
var (
	opsScheduledReportCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	// notificationEmailOpsSummaryPlaceholders follows notification's
	// summary field names, including the display flag that determines
	// whether structured metrics appear in a template.
	notificationEmailOpsSummaryPlaceholders = contract.SummaryPlaceholders()
)

// OpsScheduledReportService schedules summaries and delivers report attempts
// through the configured mail callbacks.
type OpsScheduledReportService struct {
	opsService *OpsService // settings and report queries
	// userService supplies a fallback recipient when RunReport receives an
	// empty recipient slice; lookup failures produce no fallback address.
	userService AdminReader // first administrator lookup
	// emailService is bound before startup. TemplatesEnabled requires
	// template, locale, name, and fallback callbacks; plain sending
	// requires SendEmail.
	emailService *EmailDelivery // report delivery callbacks
	// redisClient can be absent, allowing unlocked scheduling and disabling
	// last-run tracking. A configured client failing Claim instead rejects
	// that cycle.
	redisClient RuntimeCache // lease and attempt storage
	// cfg is retained by reference and read at startup. Bind deployment
	// options before starting and leave them unchanged while the scheduler
	// uses them.
	cfg *Options // deployment report options

	instanceID string // Redis lease owner
	// loc comes from a successfully loaded configured zone or time.Local.
	// Reading attempt markers converts their instants into this location
	// for cron.Next.
	loc *time.Location // cron evaluation location

	// warnNoRedisOnce limits the missing-client warning per instance; it
	// does not suppress configured Redis claim failures in later cycles.
	warnNoRedisOnce sync.Once // missing Redis diagnostic guard

	// startOnce and stopOnce independently guard loop registration and
	// cancellation through stopCtx and stop. wg joins registered work;
	// callers serialize startup before shutdown because these fields have
	// no shared lifecycle mutex.
	startOnce sync.Once          // initial loop registration
	stopOnce  sync.Once          // initial cancellation attempt
	stopCtx   context.Context    // scheduler cancellation context
	stop      context.CancelFunc // scheduler cancellation handle
	wg        sync.WaitGroup     // registered loop completion
}

// opsScheduledReport holds one configured report with its cron eligibility and
// delivery inputs.
type opsScheduledReport struct {
	// Name, ReportType, and Schedule identify notification delivery sources
	// after trimming and colon joining. Name controls display, ReportType
	// chooses content, and Schedule controls cron selection.
	Name       string // report display name
	ReportType string // report query selector
	Schedule   string // five-field cron expression
	// Enabled is checked by runOnce and filled true for selected
	// definitions; direct RunReport calls do not inspect this flag.
	Enabled bool // scheduler eligibility flag

	// TimeRange is elapsed time ending at the attempted run instant. Daily
	// and weekly definitions use twenty-four and one hundred sixty-eight
	// hours, independent of local calendar-day length.
	TimeRange time.Duration // elapsed report query window

	// Recipients from listScheduledReports share one normalized slice
	// across reports. Treat it as read-only; direct RunReport callers can
	// supply unnormalized addresses.
	Recipients []string // selected recipient addresses

	// ErrorDigestMinCount suppresses a digest when its nonnil query result
	// has fewer total errors; nonpositive values leave that selection
	// disabled.
	ErrorDigestMinCount int // digest minimum error count
	// ProviderHealthErrorRateThreshold is copied from preferences but
	// generateReportContent does not apply it to current provider
	// availability or suppress delivery.
	ProviderHealthErrorRateThreshold float64

	// LastRunAt is a copied per-kind Redis attempt time, while NextRunAt is
	// recomputed from that time or the recent startup window. Missing
	// LastRunAt leaves the pointer nil.
	LastRunAt *time.Time // previous recorded attempt
	NextRunAt time.Time  // next cron occurrence
}

// opsScheduledReportContent carries generated HTML together with the overview
// used to render summary metrics.
type opsScheduledReportContent struct {
	// html is generated by the report's escaped renderer and passed as
	// authorized raw report_html. A blank string suppresses RunReport
	// delivery.
	html string // generated report HTML
	// overview retains the queried dashboard pointer so structured
	// placeholders use the same result as summary HTML. Detailed reports
	// leave it nil and consumers keep it read-only.
	overview *OpsDashboardOverview // queried summary metric pointer
}

// NewOpsScheduledReportService binds report queries, recipient lookup, mail,
// and optional Redis storage without starting a loop. A valid configured
// timezone selects cron's base location; blank or invalid zones retain
// time.Local, and dependency pointers remain shared.
func NewOpsScheduledReportService(
	opsService *OpsService,
	userService AdminReader,
	emailService *EmailDelivery,
	redisClient RuntimeCache,
	cfg *Options,
) *OpsScheduledReportService {
	loc := time.Local
	if cfg != nil && strings.TrimSpace(cfg.Timezone) != "" {
		// A bad configured zone keeps process-local cron evaluation;
		// loading failure does not prevent report-service construction
		// or later delivery attempts.
		if parsed, err := time.LoadLocation(strings.TrimSpace(cfg.Timezone)); err == nil && parsed != nil {
			loc = parsed
		}
	}
	return &OpsScheduledReportService{
		opsService:   opsService,
		userService:  userService,
		emailService: emailService,
		redisClient:  redisClient,
		cfg:          cfg,

		instanceID:      uuid.NewString(),
		loc:             loc,
		warnNoRedisOnce: sync.Once{},
		startOnce:       sync.Once{},
		stopOnce:        sync.Once{},
		stopCtx:         nil,
		stop:            nil,
		wg:              sync.WaitGroup{},
	}
}

// Start registers the report loop using a background parent context through
// StartWithContext. Call it before Stop under serialized lifecycle ownership;
// successful registration is permanent and repeated starts cannot restart a
// cancelled loop.
func (s *OpsScheduledReportService) Start() {
	s.StartWithContext(context.Background())
}

// StartWithContext registers one report loop when Ops is enabled and query and
// mail dependencies are present. Nil context selects Background; the derived
// context cancels scheduled queries when its parent or Stop is cancelled.
//
// Callers serialize registration before shutdown. Separate start and stop once
// guards do not protect cancellation-handle publication or the first WaitGroup
// increment against Stop. A Stop preceding registration consumes its
// cancellation guard and cannot cancel a loop registered afterward.
func (s *OpsScheduledReportService) StartWithContext(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return
	}
	if s.opsService == nil || s.emailService == nil {
		return
	}

	// Publish the cycle context and register its wait count before
	// launching; the once guard combines repeated starts but does not
	// serialize Stop.
	s.startOnce.Do(func() {
		s.stopCtx, s.stop = context.WithCancel(ctx)
		s.wg.Add(1)
		go s.run()
	})
}

// Stop requests cancellation once and waits for registered scheduler work to
// finish. It accepts a nil receiver; callers finish StartWithContext before
// invoking it and keep lifecycle calls serialized.
//
// Cancellation reaches the cycle's queries and delivery context, while
// heartbeat writes use detached budgets. The loop can select an already-ready
// tick before observing cancellation. Calling Stop before registration consumes
// stopOnce with no handle, so a later start remains outside that cancellation
// attempt.
func (s *OpsScheduledReportService) Stop() {
	if s == nil {
		return
	}
	// Consume the cancellation attempt even when no handle exists; joining
	// the current wait count cannot register a future loop started after
	// this call.
	s.stopOnce.Do(func() {
		if s.stop != nil {
			s.stop()
		}
	})
	s.wg.Wait()
}

// run owns the registered scheduler completion and its minute ticker.
// StartWithContext establishes stopCtx and increments wg before launching this
// method; direct invocation requires the same setup.
//
// The first cycle runs immediately, including when cancellation is already
// visible, because it precedes the select. Later ticks and cancellation compete
// through select, so a ready tick can admit another cycle. Each cycle uses
// stopCtx for cancellation, and returning stops the ticker and releases the
// registered wait count.
func (s *OpsScheduledReportService) run() {
	// Own both the wait-count release and ticker cleanup in this goroutine,
	// so Stop can join it once the select exits.
	defer s.wg.Done()

	ticker := time.NewTicker(opsScheduledReportTickInterval)
	defer ticker.Stop()

	s.runOnce()
	for {
		select {
		case <-ticker.C:
			s.runOnce()
		case <-s.stopCtx.Done():
			return
		}
	}
}

// runOnce checks due reports under one sixty-second budget derived from the
// started scheduler's stopCtx. Call it after StartWithContext has established
// that context. Monitoring refusal, lost leadership, and an empty report list
// return without a heartbeat.
//
// Eligible reports run in definition order over windows ending at the current
// instant, expressed in the cron location for scheduling. A content-generation
// error records failure and stops later reports. Otherwise the success
// heartbeat includes recipient attempts, including failed mail sends and cycles
// with no due report; its wall-clock duration starts before gating and
// leadership.
func (s *OpsScheduledReportService) runOnce() {
	if s == nil || s.opsService == nil || s.emailService == nil {
		return
	}

	startedAt := time.Now().UTC()
	runAt := startedAt

	// Use the published scheduler context so Stop cancels this cycle's
	// query and mail work; heartbeat writers later receive independent
	// budgets.
	ctx, cancel := context.WithTimeout(s.stopCtx, 60*time.Second)
	defer cancel()

	// Monitoring refusal exits before job status is written, so a retained
	// heartbeat can describe an older admitted report cycle.
	if !s.opsService.IsMonitoringEnabled(ctx) {
		return
	}

	release, ok := s.tryAcquireLeaderLock(ctx)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}

	now := time.Now()
	if s.loc != nil {
		now = now.In(s.loc)
	}

	reports := s.listScheduledReports(ctx, now)
	if len(reports) == 0 {
		return
	}

	reportsTotal := len(reports)
	reportsDue := 0
	sentAttempts := 0

	for _, report := range reports {
		if report == nil || !report.Enabled {
			continue
		}
		if report.NextRunAt.After(now) {
			continue
		}
		reportsDue++

		// Process due definitions sequentially; a generation error ends
		// the cycle after earlier report markers and recipient attempts
		// have already occurred.
		attempts, err := s.RunReport(ctx, report, now)
		if err != nil {
			s.recordHeartbeatError(runAt, time.Since(startedAt), err)
			return
		}
		sentAttempts += attempts
	}

	// Bound the scheduler status with the job's UTF-8 byte cap; its
	// attempts count can include suppressed delivery failures.
	result := truncateString(fmt.Sprintf("reports=%d due=%d send_attempts=%d", reportsTotal, reportsDue, sentAttempts), 2048)
	s.recordHeartbeatSuccess(runAt, time.Since(startedAt), result)
}

// listScheduledReports builds enabled report definitions and their next cron
// occurrences from stored preferences. Nil context selects Background;
// unavailable preferences or disabled reports return nil. Malformed individual
// schedules are logged and skipped.
//
// Each report reads one attempt marker keyed by type. A missing or invalid
// marker uses now minus one minute, admitting a matching current-minute
// occurrence without replaying older history. Existing marker instants retain
// the configured cron location. Returned reports share a normalized recipient
// slice, hold copied LastRunAt values, and use fixed elapsed windows rather
// than local calendar days.
func (s *OpsScheduledReportService) listScheduledReports(ctx context.Context, now time.Time) []*opsScheduledReport {
	if s == nil || s.opsService == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Preference-read failure makes the schedule list unavailable for this
	// cycle; runOnce sees an empty list and leaves previous heartbeat data
	// intact.
	emailCfg, err := s.opsService.GetEmailNotificationConfig(ctx)
	if err != nil || emailCfg == nil {
		return nil
	}
	if !emailCfg.Report.Enabled {
		return nil
	}

	recipients := normalizeEmails(emailCfg.Report.Recipients)

	// reportDef binds one preference toggle and cron string to its content
	// kind, display name, and fixed elapsed query duration.
	type reportDef struct {
		enabled   bool          // configured report toggle
		name      string        // default display name
		kind      string        // content query kind
		timeRange time.Duration // elapsed query window
		schedule  string        // configured cron expression
	}

	defs := []reportDef{
		{enabled: emailCfg.Report.DailySummaryEnabled, name: "日报", kind: "daily_summary", timeRange: 24 * time.Hour, schedule: emailCfg.Report.DailySummarySchedule},
		{enabled: emailCfg.Report.WeeklySummaryEnabled, name: "周报", kind: "weekly_summary", timeRange: 7 * 24 * time.Hour, schedule: emailCfg.Report.WeeklySummarySchedule},
		{enabled: emailCfg.Report.ErrorDigestEnabled, name: "错误摘要", kind: "error_digest", timeRange: 24 * time.Hour, schedule: emailCfg.Report.ErrorDigestSchedule},
		{enabled: emailCfg.Report.ProviderHealthEnabled, name: "提供商健康", kind: "provider_health", timeRange: 24 * time.Hour, schedule: emailCfg.Report.ProviderHealthSchedule},
	}

	out := make([]*opsScheduledReport, 0, len(defs))
	for _, d := range defs {
		if !d.enabled {
			continue
		}
		spec := strings.TrimSpace(d.schedule)
		if spec == "" {
			continue
		}
		// An invalid individual expression is logged and excluded,
		// allowing other configured report kinds to remain eligible for
		// this cycle.
		sched, err := opsScheduledReportCronParser.Parse(spec)
		if err != nil {
			logging.LegacyPrintf("service.ops_scheduled_report", "[OpsScheduledReport] invalid cron spec=%q for report=%s: %v", spec, d.kind, err)
			continue
		}

		lastRun := s.getLastRunAt(ctx, d.kind)
		base := lastRun
		if base.IsZero() {
			// Use a one-minute lookback when no usable marker
			// exists, so startup can admit the current matching
			// minute while older missed occurrences stay outside
			// this selection.
			base = now.Add(-1 * time.Minute)
		}
		// cron.Next returns zero when its search finds no future match;
		// omit that definition instead of treating zero as an overdue
		// report.
		next := sched.Next(base)
		if next.IsZero() {
			continue
		}

		var lastRunPtr *time.Time
		if !lastRun.IsZero() {
			lastCopy := lastRun
			lastRunPtr = &lastCopy
		}

		out = append(out, &opsScheduledReport{
			Name:       d.name,
			ReportType: d.kind,
			Schedule:   spec,
			Enabled:    true,

			TimeRange: d.timeRange,

			Recipients: recipients,

			ErrorDigestMinCount:              emailCfg.Report.ErrorDigestMinCount,
			ProviderHealthErrorRateThreshold: emailCfg.Report.ProviderHealthErrorRateThreshold,

			LastRunAt: lastRunPtr,
			NextRunAt: next,
		})
	}

	return out
}

// RunReport generates one report and returns the number of nonblank recipient
// attempts. Missing service dependencies or a nil report return zero without
// error; nil context selects Background. Direct callers supply the desired
// report and instant because this method does not enforce Enabled, cron
// eligibility, or leadership.
//
// The per-kind attempt marker is written before generation, content
// suppression, recipient lookup, and sending. It therefore advances even when
// generation fails or nobody receives mail, provided Redis accepts that write.
// Only content-generation errors are returned. An empty recipient slice can use
// the first administrator; a nonempty slice containing only blanks has no such
// fallback.
//
// Template delivery receives the same queried summary data and authorized HTML,
// with a UTC minute reminder key. Only callback-approved template errors try
// plain mail. All recipient delivery errors are suppressed, and attempts
// include these failures. Configure the applicable EmailDelivery callbacks
// before invoking this method.
func (s *OpsScheduledReportService) RunReport(ctx context.Context, report *opsScheduledReport, now time.Time) (int, error) {
	if s == nil || s.opsService == nil || s.emailService == nil || report == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Record the attempted instant before querying and sending, so a stored
	// marker advances past repeated minute checks even if subsequent work
	// fails.
	s.setLastRunAt(ctx, report.ReportType, now)

	content, err := s.generateReportContent(ctx, report, now)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(content.html) == "" {
		// Content selection can deliberately return blank HTML; keep
		// its advanced attempt marker and skip recipient work for that
		// occurrence.
		return 0, nil
	}

	recipients := report.Recipients
	// An empty supplied list permits administrator lookup, but lookup
	// failure is suppressed and a nonempty all-blank list never enters this
	// fallback.
	if len(recipients) == 0 && s.userService != nil {
		admin, err := s.userService.GetFirstAdmin(ctx)
		if err == nil && admin != nil && strings.TrimSpace(admin.Email) != "" {
			recipients = []string{strings.TrimSpace(admin.Email)}
		}
	}
	if len(recipients) == 0 {
		return 0, nil
	}

	attempts := 0
	for _, to := range recipients {
		addr := strings.TrimSpace(to)
		if addr == "" {
			continue
		}
		attempts++
		locale := ""
		// Only a callback-approved template failure proceeds to plain
		// HTML mail; other template outcomes consume this recipient
		// attempt and advance to the next address.
		if s.emailService.TemplatesEnabled {
			locale = s.emailService.ResolveRecipientLocale(ctx, 0, addr)
			templateVariables := opsScheduledReportLocalizedEmailVariables(report, now, locale)
			// Supply generated report HTML through the authorized
			// raw variable map, while structured text variables
			// retain the renderer's normal escaping path.
			rawHTMLVariables := map[string]string{"report_html": content.html}
			if isOpsSummaryReport(report) {
				templateVariables = opsSummaryReportEmailVariables(report, now, content.overview, locale)
			}
			if err := s.emailService.SendTemplate(ctx, Notification{
				Event:            "ops.scheduled_report",
				Locale:           locale,
				RecipientEmail:   addr,
				RecipientName:    s.emailService.RecipientName(addr),
				SourceType:       "ops_scheduled_report",
				SourceID:         opsScheduledReportDeliverySourceID(report),
				ReminderKey:      now.UTC().Format("2006-01-02T15:04"),
				Variables:        templateVariables,
				RawHTMLVariables: rawHTMLVariables,
			}); err == nil {
				continue
			} else if !s.emailService.ShouldFallback(err) {
				continue
			}
		}
		subjectName := strings.TrimSpace(report.Name)
		if locale != "" {
			subjectName = opsScheduledReportLocalizedName(report, locale)
		}
		subjectPrefix := "[Ops Report]"
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			subjectPrefix = "[运维报表]"
		}
		subject := fmt.Sprintf("%s %s", subjectPrefix, subjectName)
		if err := s.emailService.SendEmail(ctx, addr, subject, content.html); err != nil {
			// Continue with later recipients after plain-mail
			// failure; the returned attempt count still includes
			// this address and does not expose its error.
			continue
		}
	}
	return attempts, nil
}

// opsScheduledReportDeliverySourceID joins the trimmed report type, display
// name, and cron expression for notification deduplication. A nil report or an
// all-empty joined value uses scheduled_report.
//
// Trimming removes outer colons but leaves colons within components unescaped,
// so this string is not a collision-free encoding of arbitrary report
// definitions. RunReport supplies the recipient and UTC minute separately in
// the notification request; changing a component changes ordinary source
// identities used by that delivery layer.
func opsScheduledReportDeliverySourceID(report *opsScheduledReport) string {
	if report == nil {
		return "scheduled_report"
	}
	parts := []string{
		strings.TrimSpace(report.ReportType),
		strings.TrimSpace(report.Name),
		strings.TrimSpace(report.Schedule),
	}
	joined := strings.Trim(strings.Join(parts, ":"), ":")
	if joined == "" {
		return "scheduled_report"
	}
	return joined
}

// opsScheduledReportEmailVariables formats report identity and its elapsed
// interval as UTC RFC3339 strings. A nil report uses fallback identity, while
// absent or nonpositive TimeRange leaves start equal to end; formatted times
// omit fractional seconds.
func opsScheduledReportEmailVariables(report *opsScheduledReport, now time.Time) map[string]string {
	end := now.UTC()
	start := end
	name := "Ops report"
	reportType := "scheduled_report"
	if report != nil {
		if strings.TrimSpace(report.Name) != "" {
			name = strings.TrimSpace(report.Name)
		}
		if strings.TrimSpace(report.ReportType) != "" {
			reportType = strings.TrimSpace(report.ReportType)
		}
		if report.TimeRange > 0 {
			start = end.Add(-report.TimeRange)
		}
	}
	// Format UTC instants to second precision for notification fields, so
	// displayed report times do not retain fractional-second precision from
	// now.
	return map[string]string{
		"report_name":       name,
		"report_type":       reportType,
		"report_start_time": start.Format(time.RFC3339),
		"report_end_time":   end.Format(time.RFC3339),
	}
}

// opsScheduledReportLocalizedEmailVariables builds the detailed-report display
// variables using a localized name and the supplied interval. It allocates a
// new map and initializes every shared summary placeholder from notification's
// field list.
//
// Detailed reports display their HTML section and hide the summary section,
// assigning dashes to metric fields rather than preview values. report_html
// stays empty in this escaped-variable map because RunReport sends generated
// HTML separately through the renderer's authorized raw-HTML map.
func opsScheduledReportLocalizedEmailVariables(report *opsScheduledReport, now time.Time, locale string) map[string]string {
	variables := opsScheduledReportEmailVariables(report, now)
	// Keep generated HTML on the separate authorized raw path and fill
	// summary fields with missing markers so detail templates cannot
	// inherit preview measurements.
	variables["report_html"] = ""
	variables["report_detail_display"] = "block"
	for _, placeholder := range notificationEmailOpsSummaryPlaceholders {
		variables[placeholder] = "-"
	}
	variables["report_summary_display"] = "none"
	if name := opsScheduledReportLocalizedName(report, locale); name != "" {
		variables["report_name"] = name
	}
	return variables
}

// opsScheduledReportLocalizedName selects Chinese names for locales with a
// trimmed zh prefix and English names for other locales. Unknown report kinds
// use the trimmed configured name, while a nil report yields Ops report.
func opsScheduledReportLocalizedName(report *opsScheduledReport, locale string) string {
	if report == nil {
		return "Ops report"
	}
	chinese := strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "zh")
	switch strings.TrimSpace(report.ReportType) {
	case "daily_summary":
		if chinese {
			return "日报"
		}
		return "Daily summary"
	case "weekly_summary":
		if chinese {
			return "周报"
		}
		return "Weekly summary"
	case "error_digest":
		if chinese {
			return "错误摘要"
		}
		return "Error digest"
	case "provider_health":
		if chinese {
			return "提供商健康"
		}
		return "Provider health"
	default:
		return strings.TrimSpace(report.Name)
	}
}

// isOpsSummaryReport reports whether a nonnil report selects daily_summary or
// weekly_summary after trimming. Nil reports and other kinds return false,
// leaving RunReport on the detailed-report variable path.
func isOpsSummaryReport(report *opsScheduledReport) bool {
	if report == nil {
		return false
	}
	switch strings.TrimSpace(report.ReportType) {
	case "daily_summary", "weekly_summary":
		return true
	default:
		return false
	}
}

// opsSummaryReportEmailVariables formats one queried overview for the summary
// section and hides the detailed section. The returned map is newly allocated;
// a nil overview keeps missing metrics as dashes while still displaying the
// summary section.
//
// Counts use grouped decimal integers, latency pointers use milliseconds with
// dashes for absence, and rates use one decimal place. SLA and error fractions
// become two-decimal percentages. The escaped report_html value remains empty
// because legacy HTML is supplied separately from the same overview, preventing
// a second query from changing one representation's measurements.
func opsSummaryReportEmailVariables(report *opsScheduledReport, now time.Time, overview *OpsDashboardOverview, locale string) map[string]string {
	variables := opsScheduledReportLocalizedEmailVariables(report, now, locale)
	variables["report_detail_display"] = "none"
	if overview == nil {
		for _, placeholder := range notificationEmailOpsSummaryPlaceholders {
			if placeholder == "report_summary_display" {
				continue
			}
			variables[placeholder] = "-"
		}
		variables["report_summary_display"] = "block"
		return variables
	}
	variables["report_summary_display"] = "block"

	// Use the supplied overview for every displayed metric, scaling
	// fractions into percentages while preserving missing latency pointers
	// as dashes.
	variables["report_total_requests"] = formatOpsReportInteger(overview.RequestCountTotal)
	variables["report_success_count"] = formatOpsReportInteger(overview.SuccessCount)
	variables["report_sla_error_count"] = formatOpsReportInteger(overview.ErrorCountSLA)
	variables["report_business_limited_count"] = formatOpsReportInteger(overview.BusinessLimitedCount)
	variables["report_sla"] = fmt.Sprintf("%.2f%%", overview.SLA*100)
	variables["report_error_rate"] = fmt.Sprintf("%.2f%%", overview.ErrorRate*100)
	variables["report_upstream_error_rate"] = fmt.Sprintf("%.2f%%", overview.UpstreamErrorRate*100)
	variables["report_upstream_error_count_excl_429_529"] = formatOpsReportInteger(overview.UpstreamErrorCountExcl429529)
	variables["report_upstream_429_count"] = formatOpsReportInteger(overview.Upstream429Count)
	variables["report_upstream_529_count"] = formatOpsReportInteger(overview.Upstream529Count)
	variables["report_latency_p50"] = formatOpsReportMilliseconds(overview.Duration.P50)
	variables["report_latency_p99"] = formatOpsReportMilliseconds(overview.Duration.P99)
	variables["report_ttft_p50"] = formatOpsReportMilliseconds(overview.TTFT.P50)
	variables["report_ttft_p99"] = formatOpsReportMilliseconds(overview.TTFT.P99)
	variables["report_tokens"] = formatOpsReportInteger(overview.TokenConsumed)
	variables["report_qps_current"] = fmt.Sprintf("%.1f", overview.QPS.Current)
	variables["report_qps_peak"] = fmt.Sprintf("%.1f", overview.QPS.Peak)
	variables["report_qps_avg"] = fmt.Sprintf("%.1f", overview.QPS.Avg)
	variables["report_tps_current"] = fmt.Sprintf("%.1f", overview.TPS.Current)
	variables["report_tps_peak"] = fmt.Sprintf("%.1f", overview.TPS.Peak)
	variables["report_tps_avg"] = fmt.Sprintf("%.1f", overview.TPS.Avg)
	return variables
}

// formatOpsReportInteger inserts comma separators into a signed decimal count
// while preserving its sign. It first formats the complete int64 value and
// groups the digit substring, so it never negates the minimum int64 value.
//
// Counts of at most three digits pass through unchanged. Larger counts allocate
// a builder and place separators from the right, leaving the minus sign outside
// the grouping width. Builder writes cannot fail and their error results are
// discarded.
func formatOpsReportInteger(value int64) string {
	raw := strconv.FormatInt(value, 10)
	start := 0
	if strings.HasPrefix(raw, "-") {
		start = 1
	}
	if len(raw)-start <= 3 {
		return raw
	}

	var builder strings.Builder
	builder.Grow(len(raw) + (len(raw)-start-1)/3)
	// strings.Builder writes return nil errors; retain the
	// already-formatted sign outside digit grouping to handle minimum int64
	// without arithmetic negation.
	_, _ = builder.WriteString(raw[:start])
	digitLen := len(raw) - start
	for offset := range digitLen {
		if offset > 0 && (digitLen-offset)%3 == 0 {
			_ = builder.WriteByte(',')
		}
		_ = builder.WriteByte(raw[start+offset])
	}
	return builder.String()
}

// formatOpsReportMilliseconds formats a present integer measurement with
// grouped digits and an ms suffix. A nil pointer renders a dash; a present zero
// remains zero milliseconds and negative inputs retain their sign.
func formatOpsReportMilliseconds(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%s ms", formatOpsReportInteger(int64(*value)))
}

// generateReportContent queries the selected report kind over a positive
// elapsed window ending at now in UTC. Missing dependencies, nonpositive
// duration, and unknown kinds return errors. The caller supplies a usable query
// context.
//
// Daily and weekly summaries first request auto dashboard data, then retry raw
// data for any returned error. Their HTML and structured variables retain the
// same overview pointer. Error digests use the first error page and can
// suppress content below the configured total-count threshold. Provider health
// renders current availability, even though the display includes the elapsed
// interval; its copied error-rate threshold is unused.
func (s *OpsScheduledReportService) generateReportContent(ctx context.Context, report *opsScheduledReport, now time.Time) (opsScheduledReportContent, error) {
	if s == nil || s.opsService == nil || report == nil {
		return opsScheduledReportContent{}, fmt.Errorf("service not initialized")
	}
	if report.TimeRange <= 0 {
		return opsScheduledReportContent{}, fmt.Errorf("invalid time range")
	}

	end := now.UTC()
	start := end.Add(-report.TimeRange)

	switch strings.TrimSpace(report.ReportType) {
	case "daily_summary", "weekly_summary":
		overview, err := s.opsService.GetDashboardOverview(ctx, &OpsDashboardFilter{
			StartTime: start,
			EndTime:   end,
			Platform:  "",
			GroupID:   nil,
			QueryMode: OpsQueryModeAuto,
		})
		if err != nil {
			// Retry raw for every auto-query error, including
			// errors unrelated to preaggregation coverage; an
			// already-degraded service call can therefore be
			// queried again.
			if strings.TrimSpace(report.ReportType) == "daily_summary" || strings.TrimSpace(report.ReportType) == "weekly_summary" {
				overview, err = s.opsService.GetDashboardOverview(ctx, &OpsDashboardFilter{
					StartTime: start,
					EndTime:   end,
					Platform:  "",
					GroupID:   nil,
					QueryMode: OpsQueryModeRaw,
				})
			}
			if err != nil {
				return opsScheduledReportContent{}, err
			}
		}
		return opsScheduledReportContent{
			html:     buildOpsSummaryEmailHTML(report.Name, start, end, overview),
			overview: overview,
		}, nil
	case "error_digest":
		// Use the first error page under GetErrorLogs' ordinary view
		// selection; the HTML displays recent rows and the full
		// filtered count.
		startTime := start
		endTime := end
		filter := &OpsErrorLogFilter{
			StartTime: &startTime,
			EndTime:   &endTime,
			Page:      1,
			PageSize:  100,
		}
		out, err := s.opsService.GetErrorLogs(ctx, filter)
		if err != nil {
			return opsScheduledReportContent{}, err
		}
		// Suppress the digest using the full query total rather than
		// its first page; a nil query result still reaches the
		// empty-list renderer.
		if report.ErrorDigestMinCount > 0 && out != nil && out.Total < report.ErrorDigestMinCount {
			return opsScheduledReportContent{}, nil
		}
		return opsScheduledReportContent{html: buildOpsErrorDigestEmailHTML(report.Name, start, end, out)}, nil
	case "provider_health":
		// Read current provider availability for this report; the
		// elapsed start and end are display labels and do not constrain
		// that status query.
		avail, err := s.opsService.GetProviderAvailability(ctx, "", nil)
		if err != nil {
			return opsScheduledReportContent{}, err
		}
		_ = report.ProviderHealthErrorRateThreshold // stored threshold is unused
		return opsScheduledReportContent{html: buildOpsProviderHealthEmailHTML(report.Name, start, end, avail)}, nil
	default:
		return opsScheduledReportContent{}, fmt.Errorf("unknown report type: %s", report.ReportType)
	}
}

// buildOpsSummaryEmailHTML renders dashboard measurements with an escaped title
// and UTC interval. Nil overviews produce a No data message; missing latency
// pointers display dashes, fractions become two-decimal percentages, and rates
// use one decimal place.
func buildOpsSummaryEmailHTML(title string, start, end time.Time, overview *OpsDashboardOverview) string {
	if overview == nil {
		return fmt.Sprintf("<h2>%s</h2><p>No data.</p>", htmlEscape(title))
	}

	latP50 := "-"
	latP99 := "-"
	if overview.Duration.P50 != nil {
		latP50 = fmt.Sprintf("%dms", *overview.Duration.P50)
	}
	if overview.Duration.P99 != nil {
		latP99 = fmt.Sprintf("%dms", *overview.Duration.P99)
	}

	ttftP50 := "-"
	ttftP99 := "-"
	if overview.TTFT.P50 != nil {
		ttftP50 = fmt.Sprintf("%dms", *overview.TTFT.P50)
	}
	if overview.TTFT.P99 != nil {
		ttftP99 = fmt.Sprintf("%dms", *overview.TTFT.P99)
	}

	// Escape interpolated text before it joins the HTML template; ratios
	// are rendered as percentages and nullable latency fields retain
	// missing markers.
	return fmt.Sprintf(`
<h2>%s</h2>
<p><b>Period</b>: %s ~ %s (UTC)</p>
<ul>
  <li><b>Total Requests</b>: %d</li>
  <li><b>Success</b>: %d</li>
  <li><b>Errors (SLA)</b>: %d</li>
  <li><b>Business Limited</b>: %d</li>
  <li><b>SLA</b>: %.2f%%</li>
  <li><b>Error Rate</b>: %.2f%%</li>
  <li><b>Upstream Error Rate (excl 429/529)</b>: %.2f%%</li>
  <li><b>Upstream Errors</b>: excl429/529=%d, 429=%d, 529=%d</li>
  <li><b>Latency</b>: p50=%s, p99=%s</li>
  <li><b>TTFT</b>: p50=%s, p99=%s</li>
  <li><b>Tokens</b>: %d</li>
  <li><b>QPS</b>: current=%.1f, peak=%.1f, avg=%.1f</li>
  <li><b>TPS</b>: current=%.1f, peak=%.1f, avg=%.1f</li>
</ul>
`,
		htmlEscape(strings.TrimSpace(title)),
		htmlEscape(start.UTC().Format(time.RFC3339)),
		htmlEscape(end.UTC().Format(time.RFC3339)),
		overview.RequestCountTotal,
		overview.SuccessCount,
		overview.ErrorCountSLA,
		overview.BusinessLimitedCount,
		overview.SLA*100,
		overview.ErrorRate*100,
		overview.UpstreamErrorRate*100,
		overview.UpstreamErrorCountExcl429529,
		overview.Upstream429Count,
		overview.Upstream529Count,
		htmlEscape(latP50),
		htmlEscape(latP99),
		htmlEscape(ttftP50),
		htmlEscape(ttftP99),
		overview.TokenConsumed,
		overview.QPS.Current,
		overview.QPS.Peak,
		overview.QPS.Avg,
		overview.TPS.Current,
		overview.TPS.Peak,
		overview.TPS.Avg,
	)
}

// buildOpsErrorDigestEmailHTML renders the query's total error count and up to
// ten supplied slice positions. Nil lists retain zero total, nil entries are
// skipped, and the first-ten cap is applied before that skipping.
//
// Each retained message is truncated to a UTF-8 byte limit before HTML
// escaping. Title, platform, messages, and UTC timestamps are escaped; an empty
// rendered row set receives a No recent errors row. The total remains the
// query's full count rather than the number of displayed rows.
func buildOpsErrorDigestEmailHTML(title string, start, end time.Time, list *OpsErrorLogList) string {
	total := 0
	recent := []*OpsErrorLog{}
	if list != nil {
		total = list.Total
		recent = list.Errors
	}
	// Cap slice positions before skipping nil entries, so a sparse first
	// ten can display fewer rows despite later populated entries.
	if len(recent) > 10 {
		recent = recent[:10]
	}

	rows := ""
	for _, item := range recent {
		if item == nil {
			continue
		}
		// Truncate diagnostic messages to a UTF-8 byte limit before
		// escaping, keeping oversized source text bounded while
		// protecting the surrounding table markup.
		rows += fmt.Sprintf(
			"<tr><td>%s</td><td>%s</td><td>%d</td><td>%s</td></tr>",
			htmlEscape(item.CreatedAt.UTC().Format(time.RFC3339)),
			htmlEscape(item.Platform),
			item.StatusCode,
			htmlEscape(truncateString(item.Message, 180)),
		)
	}
	if rows == "" {
		rows = "<tr><td colspan=\"4\">No recent errors.</td></tr>"
	}

	return fmt.Sprintf(`
<h2>%s</h2>
<p><b>Period</b>: %s ~ %s (UTC)</p>
<p><b>Total Errors</b>: %d</p>
<h3>Recent</h3>
<table border="1" cellpadding="6" cellspacing="0" style="border-collapse:collapse;">
  <thead><tr><th>Time</th><th>Platform</th><th>Status</th><th>Message</th></tr></thead>
  <tbody>%s</tbody>
</table>
`,
		htmlEscape(strings.TrimSpace(title)),
		htmlEscape(start.UTC().Format(time.RFC3339)),
		htmlEscape(end.UTC().Format(time.RFC3339)),
		total,
		rows,
	)
}

// buildOpsProviderHealthEmailHTML counts nonnil current provider entries and
// renders their availability flags with an escaped title and UTC interval
// label. Nil availability yields zero counts.
//
// Available, rate-limited, and error flags are counted independently for each
// entry, so those counts can overlap and do not partition the provider total.
// The interval labels the report request; these supplied flags describe current
// status rather than historical observations collected across that interval.
func buildOpsProviderHealthEmailHTML(title string, start, end time.Time, avail *OpsProviderAvailability) string {
	total := 0
	available := 0
	rateLimited := 0
	hasError := 0

	// Count each flag independently from current nonnil entries;
	// simultaneous availability and error flags contribute to multiple
	// displayed categories for one provider.
	if avail != nil && avail.Providers != nil {
		for _, a := range avail.Providers {
			if a == nil {
				continue
			}
			total++
			if a.IsAvailable {
				available++
			}
			if a.IsRateLimited {
				rateLimited++
			}
			if a.HasError {
				hasError++
			}
		}
	}

	return fmt.Sprintf(`
<h2>%s</h2>
<p><b>Period</b>: %s ~ %s (UTC)</p>
<ul>
  <li><b>Total Providers</b>: %d</li>
  <li><b>Available</b>: %d</li>
  <li><b>Rate Limited</b>: %d</li>
  <li><b>Error</b>: %d</li>
</ul>
<p>Note: This report currently reflects provider availability status only.</p>
`,
		htmlEscape(strings.TrimSpace(title)),
		htmlEscape(start.UTC().Format(time.RFC3339)),
		htmlEscape(end.UTC().Format(time.RFC3339)),
		total,
		available,
		rateLimited,
		hasError,
	)
}

// tryAcquireLeaderLock claims the report lease and returns its owner-checked
// release callback. A nil service or absent Redis permits execution; configured
// Redis errors and contention refuse it. Nil context selects Background, and
// the unrenewed lease's release uses that same context, ignoring release
// errors.
func (s *OpsScheduledReportService) tryAcquireLeaderLock(ctx context.Context) (func(), bool) {
	if s == nil {
		return nil, true
	}
	// Without Redis, permit this instance and warn once; the per-kind
	// marker functions also become no-ops, leaving scheduling coordination
	// local to each loop.
	if s.redisClient == nil {
		s.warnNoRedisOnce.Do(func() {
			logging.LegacyPrintf("service.ops_scheduled_report", "[OpsScheduledReport] redis not configured; running without distributed lock")
		})
		return nil, true
	}
	if ctx == nil {
		ctx = context.Background()
	}

	key := opsScheduledReportLeaderLockKeyDefault
	ttl := opsScheduledReportLeaderLockTTLDefault
	if strings.TrimSpace(key) == "" {
		key = "ops:scheduled_reports:leader"
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	ok, err := s.redisClient.Claim(ctx, key, s.instanceID, ttl)
	if err != nil {
		// A configured Redis claim error rejects this cycle because
		// ownership was not established; contention likewise skips all
		// report work.
		logging.LegacyPrintf("service.ops_scheduled_report", "[OpsScheduledReport] leader lock SetNX failed; skipping this cycle: %v", err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	// Reuse the admitted cycle context for owner-checked release; timeout
	// or cancellation can prevent release, leaving expiry to clear the
	// unrenewed lease.
	return func() {
		_ = s.redisClient.Release(ctx, key, s.instanceID)
	}, true
}

// getLastRunAt reads the per-kind attempt marker as positive decimal Unix
// seconds. Missing Redis, blank kind, read failure, blank data, or invalid and
// nonpositive seconds return a zero time.
//
// The decoded instant is converted into loc, or UTC when loc is nil, before
// cron.Next receives it. Cron expressions without their own timezone use the
// base time's location, so retaining this location keeps later calculations
// aligned with the startup calculation rather than interpreting the stored
// instant in another zone.
func (s *OpsScheduledReportService) getLastRunAt(ctx context.Context, reportType string) time.Time {
	if s == nil || s.redisClient == nil {
		return time.Time{}
	}
	kind := strings.TrimSpace(reportType)
	if kind == "" {
		return time.Time{}
	}
	key := opsScheduledReportLastRunKeyPrefix + kind

	// Treat unavailable marker reads and unusable decimal seconds as an
	// absent attempt, which lets listScheduledReports choose its recent
	// startup lookback.
	raw, err := s.redisClient.Get(ctx, key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	last := time.Unix(sec, 0)
	// Restore the cron base's location after Unix decoding; robfig/cron
	// uses that base location when an expression supplies no timezone of
	// its own.
	if s.loc != nil {
		return last.In(s.loc)
	}
	return last.UTC()
}

// setLastRunAt stores decimal Unix seconds under the trimmed report kind for
// fourteen days. Missing Redis or blank kind is a no-op; zero time selects
// current UTC time, and storage failures are ignored.
func (s *OpsScheduledReportService) setLastRunAt(ctx context.Context, reportType string, t time.Time) {
	if s == nil || s.redisClient == nil {
		return
	}
	kind := strings.TrimSpace(reportType)
	if kind == "" {
		return
	}
	if t.IsZero() {
		t = time.Now().UTC()
	}
	key := opsScheduledReportLastRunKeyPrefix + kind
	// Store only whole seconds with a fourteen-day lifetime; ignoring write
	// failure allows later scheduler checks to reuse an older or absent
	// marker.
	_ = s.redisClient.Put(ctx, key, strconv.FormatInt(t.UTC().Unix(), 10), 14*24*time.Hour)
}

// recordHeartbeatSuccess publishes scheduler completion through the Ops
// repository with a detached two-second budget. Missing dependencies skip
// persistence, blank results become ok, and UTF-8 diagnostics are capped;
// supplied duration is truncated to whole milliseconds and write errors are
// ignored.
func (s *OpsScheduledReportService) recordHeartbeatSuccess(runAt time.Time, duration time.Duration, result string) {
	if s == nil || s.opsService == nil || s.opsService.opsRepo == nil {
		return
	}
	now := time.Now().UTC()
	// Convert supplied elapsed time to whole milliseconds for the heartbeat
	// column; submillisecond information is discarded.
	durMs := duration.Milliseconds()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg := strings.TrimSpace(result)
	if msg == "" {
		msg = "ok"
	}
	// Keep job diagnostics within the scheduler's byte cap.
	msg = truncateString(msg, 2048)
	// Write job status under the detached timeout and ignore storage
	// failure, so heartbeat persistence cannot undo prior report markers or
	// mail attempts.
	_ = s.opsService.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsScheduledReportJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &now,
		LastDurationMs: &durMs,
		LastResult:     &msg,
	})
}

// recordHeartbeatError publishes a generation failure and elapsed milliseconds
// through the Ops repository using a detached two-second budget. Missing
// dependencies or a nil error skip persistence; diagnostics are UTF-8 bounded
// and write failures are ignored.
func (s *OpsScheduledReportService) recordHeartbeatError(runAt time.Time, duration time.Duration, err error) {
	if s == nil || s.opsService == nil || s.opsService.opsRepo == nil || err == nil {
		return
	}
	now := time.Now().UTC()
	// Convert supplied elapsed time to whole milliseconds for the heartbeat
	// column; submillisecond information is discarded.
	durMs := duration.Milliseconds()
	// Keep job diagnostics within the scheduler's byte cap.
	msg := truncateString(err.Error(), 2048)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Write job status under the detached timeout and ignore storage
	// failure, so heartbeat persistence cannot undo prior report markers or
	// mail attempts.
	_ = s.opsService.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsScheduledReportJobName,
		LastRunAt:      &runAt,
		LastErrorAt:    &now,
		LastError:      &msg,
		LastDurationMs: &durMs,
	})
}

// normalizeEmails trims, lowercases, and deduplicates nonblank addresses in
// first-occurrence order using a new slice. Empty input returns nil, all-blank
// nonempty input returns an allocated empty slice, and address syntax is not
// validated.
func normalizeEmails(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		// Normalize before checking seen so case and surrounding-space
		// variants share one entry; the output retains the first
		// occurrence of each normalized address.
		addr := strings.ToLower(strings.TrimSpace(raw))
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}
