package usage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// export_service.go coordinates background usage-export task admission, worker
// execution, cancellation, retention sweeps, and signed download delivery.
// Regular users and administrators submit export tasks that snapshot explicit
// half-open time bounds, IANA timezone, display locale, currency unit, and
// authorized query filters while enforcing per-actor active quotas atomically at
// insertion. Dedicated background workers claim pending or crash-abandoned tasks
// with SKIP LOCKED, stream keyset batches into temporary .part CSV files, and
// atomically publish finished files.
//
// Task execution separates service shutdown from per-task timeout so timed-out
// exports transition immediately to failed status instead of remaining running
// and starving worker slots across retries, whereas graceful shutdown requeues
// interrupted running tasks back to pending. Workers also check persisted status
// between batches and use a conditional terminal update after committing the
// file, deleting the published CSV whenever a concurrent cancellation wins.

// Worker scheduling, default limits, and HTTP status constants govern background
// export execution, active-task admission quotas, and expired-artifact responses.
const (
	usageExportWorkerName = "usage_export_worker" // recurring timing-wheel callback name

	defaultExportRetentionHours     = 24               // post-completion artifact retention hours
	defaultExportMaxActivePerUser   = 2                // per-user pending/running export limit
	defaultExportMaxActivePerAdmin  = 5                // per-admin pending/running export limit
	defaultExportBatchSize          = 2000             // keyset scan rows per worker batch
	defaultExportWorkerConcurrency  = 2                // background export worker goroutine count
	defaultExportWorkerInterval     = 5 * time.Second  // recurring poll and sweep interval
	defaultExportTaskTimeout        = 30 * time.Minute // per-task execution timeout
	defaultExportStaleClaimGrace    = 1 * time.Minute  // grace after task timeout before reclaiming crash-abandoned tasks
	defaultExportSweepBatchLimit    = 100              // maximum expired tasks processed per sweep
	defaultExportStalePartGraceTime = 5 * time.Minute  // extra grace beyond task timeout before deleting .part files
	maxExportErrorMsgBytes          = 500              // maximum persisted failure diagnostic length
	maxWatchedExportTaskIDs         = 64               // maximum off-page watched task IDs inspected per list poll

	exportCategoryGone infraerrors.Category = 410 // HTTP 410 Gone status category for expired artifacts
)

// UsageExportService manages persisted usage-export tasks, background CSV worker
// goroutines, artifact retention sweeps, and download authorization.
type UsageExportService struct {
	repo         UsageExportRepository    // export task metadata repository
	rowReader    UsageExportRowReader     // keyset usage-log batch reader factory
	store        UsageExportArtifactStore // confined local CSV artifact store
	balanceUnits UsageBalanceUnitReader   // optional currency unit label reader
	timingWheel  TimingWheel              // optional recurring sweep scheduler
	cfg          *Options                 // deployment limits and logging callback

	signer *usageExportTicketSigner // short-lived browser download ticket signer
	now    func() time.Time         // clock function replaceable in unit tests

	// wakeupCh notifies idle worker goroutines when a new task is created or a
	// recurring tick arrives without spawning unbounded per-request goroutines.
	wakeupCh  chan struct{} // buffered worker wakeup signal channel
	sweeping  int32         // atomic guard serializing expiry sweep rounds
	startOnce sync.Once     // worker pool and scheduler registration guard
	stopOnce  sync.Once     // graceful shutdown execution guard
	// lifecycleMu protects stopped, wheelScheduled, and workWG registration so
	// Stop can close new worker or RunOnce admission before waiting for active work.
	lifecycleMu    sync.Mutex     // worker lifecycle synchronization lock
	stopped        bool           // permanent service shutdown flag
	wheelScheduled bool           // whether timingWheel recurring sweep was registered
	workWG         sync.WaitGroup // active worker goroutines and synchronous rounds

	// workerCtx and workerCancel bound background task execution and unblock
	// waiting workers immediately when Stop is invoked.
	workerCtx    context.Context    // background worker cancellation context
	workerCancel context.CancelFunc // background worker cancellation trigger
}

// NewUsageExportService constructs an idle usage-export service with a fresh
// per-process HMAC ticket secret and cancellable worker context. Callers invoke
// Start to launch background workers and Stop during shutdown after HTTP drain.
func NewUsageExportService(
	repo UsageExportRepository,
	rowReader UsageExportRowReader,
	store UsageExportArtifactStore,
	balanceUnits UsageBalanceUnitReader,
	timingWheel TimingWheel,
	cfg *Options,
) *UsageExportService {
	workerCtx, workerCancel := context.WithCancel(context.Background())
	svc := &UsageExportService{
		repo:         repo,
		rowReader:    rowReader,
		store:        store,
		balanceUnits: balanceUnits,
		timingWheel:  timingWheel,
		cfg:          cfg,
		now:          time.Now,
		wakeupCh:     make(chan struct{}, 1),
		workerCtx:    workerCtx,
		workerCancel: workerCancel,
	}
	svc.signer = newUsageExportTicketSigner(nil, svc.currentTime)
	return svc
}

// Start launches the configured background worker pool and registers recurring
// retention sweeps when dependencies and deployment settings allow execution.
// Repeated calls share startOnce, and a stopped service stays stopped.
func (s *UsageExportService) Start() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopped {
		return
	}
	if !s.isEnabled() {
		s.logMessage("service.usage_export", "[UsageExport] not started (disabled)")
		return
	}
	if s.repo == nil || s.rowReader == nil || s.store == nil {
		s.logMessage("service.usage_export", "[UsageExport] not started (missing deps)")
		return
	}

	s.startOnce.Do(func() {
		workers := s.workerConcurrency()
		for range workers {
			// Register each worker under lifecycleMu before launching its goroutine so
			// a concurrent Stop call waits for every spawned worker to exit cleanly.
			s.workWG.Add(1)
			go s.workerLoop()
		}
		interval := s.workerInterval()
		if s.timingWheel != nil {
			s.timingWheel.ScheduleRecurring(usageExportWorkerName, interval, s.onRecurringTick)
			s.wheelScheduled = true
		}
		// Trigger an immediate wakeup so pending or stale-running tasks persisted
		// before process restart are claimed without waiting for the first tick.
		s.wakeWorkers()
		s.logMessage(
			"service.usage_export",
			"[UsageExport] started (workers=%d interval=%s batch_size=%d retention=%s task_timeout=%s)",
			workers,
			interval,
			s.batchSize(),
			s.retentionDuration(),
			s.taskTimeout(),
		)
	})
}

// Stop closes worker admission, cancels active export contexts, cancels the
// recurring sweep registration, and waits for all accepted work to finish.
func (s *UsageExportService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		// Flip stopped and cancel workerCtx under lifecycleMu so neither Start nor
		// RunOnce can register additional work after WaitGroup waiting begins.
		s.lifecycleMu.Lock()
		s.stopped = true
		cancelWheel := s.wheelScheduled && s.timingWheel != nil
		s.wheelScheduled = false
		if s.workerCancel != nil {
			s.workerCancel()
		}
		s.lifecycleMu.Unlock()
		if cancelWheel {
			s.timingWheel.CancelAndWait(usageExportWorkerName)
		}
		s.workWG.Wait()
		s.logMessage("service.usage_export", "[UsageExport] stopped")
	})
}

// RunOnce executes one synchronous expiry sweep and drains all currently
// claimable export tasks. Unit tests and manual callers use this entry point
// without starting background goroutines or timers.
func (s *UsageExportService) RunOnce() {
	if s == nil || !s.isEnabled() || s.repo == nil || s.rowReader == nil || s.store == nil {
		return
	}
	s.lifecycleMu.Lock()
	if s.stopped {
		s.lifecycleMu.Unlock()
		return
	}
	// Track synchronous rounds in workWG under lifecycleMu so Stop waits for an
	// active RunOnce drain before closing underlying repositories or temp roots.
	s.workWG.Add(1)
	s.lifecycleMu.Unlock()
	defer s.workWG.Done()

	s.sweepExpiredAndStaleParts()
	s.drainClaimableTasks()
}

// CreateUserTask validates filters, enforces the per-user active task quota,
// snapshots the current display currency unit, and persists a pending user export.
func (s *UsageExportService) CreateUserTask(ctx context.Context, actorUserID int64, filters UsageExportFilters) (*UsageExportTask, error) {
	return s.createTask(ctx, UsageExportScopeUser, actorUserID, filters)
}

// CreateAdminTask validates filters, enforces the per-admin active task quota,
// snapshots the current display currency unit, and persists a pending admin export.
func (s *UsageExportService) CreateAdminTask(ctx context.Context, actorUserID int64, filters UsageExportFilters) (*UsageExportTask, error) {
	return s.createTask(ctx, UsageExportScopeAdmin, actorUserID, filters)
}

// IsEnabled reports whether background CSV export endpoints and workers are
// enabled by deployment configuration.
func (s *UsageExportService) IsEnabled() bool {
	return s.isEnabled()
}

// ListUserTasks returns paginated user-scope export tasks owned by actorUserID
// along with scope-wide active task IDs and optional watched task records.
func (s *UsageExportService) ListUserTasks(
	ctx context.Context,
	actorUserID int64,
	params pagination.PaginationParams,
	watchIDs ...int64,
) (*UsageExportTaskListResult, error) {
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	if actorUserID <= 0 {
		return nil, infraerrors.BadRequest("USAGE_EXPORT_INVALID_CREATOR", "invalid export task creator")
	}
	uid := actorUserID
	return s.listScopedTasks(ctx, UsageExportListFilter{
		Scope:     UsageExportScopeUser,
		CreatedBy: &uid,
	}, params, watchIDs)
}

// ListAdminTasks returns paginated administrator-scope export tasks across admins
// along with scope-wide active task IDs and optional watched task records.
func (s *UsageExportService) ListAdminTasks(
	ctx context.Context,
	params pagination.PaginationParams,
	watchIDs ...int64,
) (*UsageExportTaskListResult, error) {
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	return s.listScopedTasks(ctx, UsageExportListFilter{
		Scope: UsageExportScopeAdmin,
	}, params, watchIDs)
}

// listScopedTasks normalizes watchIDs and delegates to a single snapshot-consistent
// repository read so returned task statuses and active_ids cannot diverge when a
// worker finishes a task concurrently with polling.
func (s *UsageExportService) listScopedTasks(
	ctx context.Context,
	filter UsageExportListFilter,
	params pagination.PaginationParams,
	watchIDs []int64,
) (*UsageExportTaskListResult, error) {
	return s.repo.ListTaskPage(ctx, filter, params, normalizeWatchedTaskIDs(watchIDs))
}

// normalizeWatchedTaskIDs deduplicates positive watchIDs and caps the slice at
// maxWatchedExportTaskIDs.
func normalizeWatchedTaskIDs(watchIDs []int64) []int64 {
	if len(watchIDs) == 0 {
		return nil
	}
	out := make([]int64, 0, min(len(watchIDs), maxWatchedExportTaskIDs))
	seen := make(map[int64]struct{}, len(out))
	for _, id := range watchIDs {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= maxWatchedExportTaskIDs {
			break
		}
	}
	return out
}

// CancelUserTask cancels a pending or running user export task owned by
// actorUserID, returning 404 when missing or owned by another user and 409
// when already succeeded, failed, or expired. Already-canceled tasks succeed.
func (s *UsageExportService) CancelUserTask(ctx context.Context, actorUserID, taskID int64) (*UsageExportTask, error) {
	return s.cancelTask(ctx, UsageExportScopeUser, actorUserID, taskID)
}

// CancelAdminTask cancels a pending or running administrator export task,
// returning 404 when missing or scoped to a regular user and 409 when terminal.
func (s *UsageExportService) CancelAdminTask(ctx context.Context, actorUserID, taskID int64) (*UsageExportTask, error) {
	return s.cancelTask(ctx, UsageExportScopeAdmin, actorUserID, taskID)
}

// IssueUserDownloadTicket verifies that actorUserID owns a succeeded, unexpired
// user export task and returns a 60-second signed ticket for native browser download.
func (s *UsageExportService) IssueUserDownloadTicket(ctx context.Context, actorUserID, taskID int64) (string, time.Time, error) {
	return s.issueDownloadTicket(ctx, UsageExportScopeUser, actorUserID, taskID)
}

// IssueAdminDownloadTicket verifies that taskID is a succeeded, unexpired admin
// export task and returns a 60-second signed ticket for native browser download.
func (s *UsageExportService) IssueAdminDownloadTicket(ctx context.Context, actorUserID, taskID int64) (string, time.Time, error) {
	return s.issueDownloadTicket(ctx, UsageExportScopeAdmin, actorUserID, taskID)
}

// OpenDownloadByTicket verifies a short-lived signed browser download ticket and
// opens the corresponding CSV artifact stream, returning 401 for invalid/expired
// tickets and 410 when the export artifact itself has expired.
func (s *UsageExportService) OpenDownloadByTicket(ctx context.Context, ticket string) (*UsageExportDownloadStream, error) {
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	claims, err := s.signer.Verify(ticket)
	if err != nil {
		return nil, err
	}
	return s.openAuthorizedDownload(ctx, claims.Scope, claims.ActorUserID, claims.TaskID)
}

// OpenUserDownload authorizes actorUserID directly and opens the CSV stream for
// API clients using bearer authentication. Callers must close Stream.Reader.
func (s *UsageExportService) OpenUserDownload(ctx context.Context, actorUserID, taskID int64) (*UsageExportDownloadStream, error) {
	return s.openAuthorizedDownload(ctx, UsageExportScopeUser, actorUserID, taskID)
}

// OpenAdminDownload authorizes an administrator request directly and opens the
// CSV stream for API or MCP callers. Callers must close Stream.Reader.
func (s *UsageExportService) OpenAdminDownload(ctx context.Context, actorUserID, taskID int64) (*UsageExportDownloadStream, error) {
	return s.openAuthorizedDownload(ctx, UsageExportScopeAdmin, actorUserID, taskID)
}

// createTask validates filters, snapshots the currency unit label, atomically
// enforces the caller's active-task quota during persistence, and wakes workers.
func (s *UsageExportService) createTask(
	ctx context.Context,
	scope string,
	actorUserID int64,
	filters UsageExportFilters,
) (*UsageExportTask, error) {
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(filters.BalanceUnitName) == "" && s.balanceUnits != nil {
		filters.BalanceUnitName = strings.TrimSpace(s.balanceUnits.GetBalanceUnitName(ctx))
	}
	if _, err := SanitizeAndValidateUsageExportFilters(scope, actorUserID, &filters); err != nil {
		return nil, err
	}

	limit := s.maxActiveLimit(scope)
	task := &UsageExportTask{
		Scope:     scope,
		Status:    UsageExportStatusPending,
		Filters:   filters,
		CreatedBy: actorUserID,
	}
	if err := s.repo.CreateTask(ctx, task, limit); err != nil {
		if errors.Is(err, ErrExportActiveLimitExceeded) {
			return nil, infraerrors.TooManyRequests(
				"USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED",
				fmt.Sprintf("active export task limit (%d) reached; wait for a running task to finish or cancel it", limit),
			)
		}
		return nil, fmt.Errorf("create export task: %w", err)
	}
	s.logMessage(
		"service.usage_export",
		"[UsageExport] task created: task=%d scope=%s operator=%d start=%s end=%s tz=%s",
		task.ID,
		scope,
		actorUserID,
		filters.StartTime.Format(time.RFC3339),
		filters.EndTime.Format(time.RFC3339),
		filters.Timezone,
	)
	s.wakeWorkers()
	return task, nil
}

// cancelTask enforces scope and creator ownership before conditionally marking
// a pending or running export task as canceled.
func (s *UsageExportService) cancelTask(ctx context.Context, scope string, actorUserID, taskID int64) (*UsageExportTask, error) {
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	if actorUserID <= 0 {
		return nil, infraerrors.BadRequest("USAGE_EXPORT_INVALID_CANCELLER", "invalid export task canceller")
	}

	task, err := s.getAuthorizedTask(ctx, scope, actorUserID, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status == UsageExportStatusCanceled {
		return task, nil
	}
	if task.Status != UsageExportStatusPending && task.Status != UsageExportStatusRunning {
		return nil, infraerrors.Conflict("USAGE_EXPORT_CANCEL_CONFLICT", "export task cannot be canceled in current status")
	}

	ok, err := s.repo.CancelTask(ctx, taskID, actorUserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Another actor or worker may have transitioned the task after the initial
		// read; reload so concurrent cancel requests remain idempotent on canceled state.
		latest, getErr := s.getAuthorizedTask(ctx, scope, actorUserID, taskID)
		if getErr == nil && latest.Status == UsageExportStatusCanceled {
			return latest, nil
		}
		return nil, infraerrors.Conflict("USAGE_EXPORT_CANCEL_CONFLICT", "export task cannot be canceled in current status")
	}

	updated, err := s.getAuthorizedTask(ctx, scope, actorUserID, taskID)
	if err != nil {
		return task, nil
	}
	return updated, nil
}

// issueDownloadTicket authorizes the task, confirms its artifact is ready and
// unexpired, and signs a 60-second browser download ticket.
func (s *UsageExportService) issueDownloadTicket(
	ctx context.Context,
	scope string,
	actorUserID, taskID int64,
) (string, time.Time, error) {
	if err := s.ensureAvailable(); err != nil {
		return "", time.Time{}, err
	}
	if actorUserID <= 0 {
		return "", time.Time{}, infraerrors.Unauthorized("UNAUTHORIZED", "authentication required")
	}

	task, err := s.getAuthorizedTask(ctx, scope, actorUserID, taskID)
	if err != nil {
		return "", time.Time{}, err
	}
	if err := s.ensureTaskDownloadable(task); err != nil {
		return "", time.Time{}, err
	}

	expiresAt := s.currentTime().UTC().Add(usageExportTicketTTL)
	ticket, err := s.signer.Sign(UsageExportDownloadTicketClaims{
		TaskID:        task.ID,
		Scope:         scope,
		ActorUserID:   actorUserID,
		ExpiresAtUnix: expiresAt.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return ticket, expiresAt, nil
}

// openAuthorizedDownload loads and authorizes taskID, enforces artifact readiness
// and retention TTL, and opens the underlying CSV stream from store.
func (s *UsageExportService) openAuthorizedDownload(
	ctx context.Context,
	scope string,
	actorUserID, taskID int64,
) (*UsageExportDownloadStream, error) {
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	if actorUserID <= 0 {
		return nil, infraerrors.Unauthorized("UNAUTHORIZED", "authentication required")
	}

	task, err := s.getAuthorizedTask(ctx, scope, actorUserID, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskDownloadable(task); err != nil {
		return nil, err
	}

	rc, sizeBytes, err := s.store.Open(*task.FilePath)
	if err != nil {
		if errors.Is(err, ErrExportArtifactNotFound) {
			// Reconcile persisted metadata when a local CSV file was already removed
			// externally so subsequent task listings reflect expired status immediately.
			s.expireTaskBestEffort(task)
			return nil, newExportExpiredError()
		}
		return nil, fmt.Errorf("open export artifact: %w", err)
	}

	fileName := ""
	if task.FileName != nil {
		fileName = strings.TrimSpace(*task.FileName)
	}
	if fileName == "" {
		loc, _, locErr := LoadUsageExportLocation(task.Filters.Timezone)
		if locErr != nil {
			loc = time.UTC
		}
		fileName = buildUsageExportFileName(task, loc)
	}
	if sizeBytes <= 0 && task.FileSizeBytes > 0 {
		sizeBytes = task.FileSizeBytes
	}

	return &UsageExportDownloadStream{
		Reader:        rc,
		FileName:      fileName,
		FileSizeBytes: sizeBytes,
	}, nil
}

// getAuthorizedTask loads taskID and hides cross-scope or cross-user tasks behind
// a uniform 404 error so callers cannot enumerate foreign export identifiers.
func (s *UsageExportService) getAuthorizedTask(
	ctx context.Context,
	scope string,
	actorUserID, taskID int64,
) (*UsageExportTask, error) {
	if taskID <= 0 {
		return nil, infraerrors.NotFound("USAGE_EXPORT_TASK_NOT_FOUND", "export task not found")
	}
	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, ErrExportTaskNotFound) {
			return nil, infraerrors.NotFound("USAGE_EXPORT_TASK_NOT_FOUND", "export task not found")
		}
		return nil, err
	}
	if task.Scope != scope {
		return nil, infraerrors.NotFound("USAGE_EXPORT_TASK_NOT_FOUND", "export task not found")
	}
	if scope == UsageExportScopeUser && task.CreatedBy != actorUserID {
		return nil, infraerrors.NotFound("USAGE_EXPORT_TASK_NOT_FOUND", "export task not found")
	}
	return task, nil
}

// ensureTaskDownloadable checks whether task holds an unexpired committed CSV
// artifact, returning 410 when expired and 409 when still running or failed.
func (s *UsageExportService) ensureTaskDownloadable(task *UsageExportTask) error {
	if task.Status == UsageExportStatusExpired {
		return newExportExpiredError()
	}
	if task.Status == UsageExportStatusSucceeded && task.ExpiresAt != nil && !s.currentTime().UTC().Before(task.ExpiresAt.UTC()) {
		// Enforce retention expiry immediately on read even before the periodic
		// sweep visits this row, deleting the artifact and updating task status.
		s.expireTaskBestEffort(task)
		return newExportExpiredError()
	}
	if task.Status != UsageExportStatusSucceeded || task.FilePath == nil || strings.TrimSpace(*task.FilePath) == "" {
		return infraerrors.Conflict("USAGE_EXPORT_NOT_READY", "export task is not ready for download")
	}
	return nil
}

// newExportExpiredError builds the structured HTTP 410 Gone error returned when
// an export artifact has aged past its retention window.
func newExportExpiredError() *infraerrors.ApplicationError {
	return infraerrors.New(exportCategoryGone, "USAGE_EXPORT_EXPIRED", "export file has expired; create a new export task")
}

// onRecurringTick sweeps expired artifacts and signals worker goroutines to
// claim any newly eligible pending or stale-running tasks.
func (s *UsageExportService) onRecurringTick() {
	if s == nil || s.workerCtx.Err() != nil {
		return
	}
	s.sweepExpiredAndStaleParts()
	s.wakeWorkers()
}

// wakeWorkers performs a nonblocking send on wakeupCh so waiting workers claim
// newly created or stale-reclaimable tasks promptly.
func (s *UsageExportService) wakeWorkers() {
	if s == nil || s.wakeupCh == nil {
		return
	}
	// Coalesce concurrent wakeups in the single-slot buffer so task creation never
	// blocks when workers are already awake or Start has not been called.
	select {
	case s.wakeupCh <- struct{}{}:
	default:
	}
}

// workerLoop drains claimable export tasks whenever woken until workerCtx is canceled.
func (s *UsageExportService) workerLoop() {
	defer s.workWG.Done()
	for {
		s.drainClaimableTasks()
		select {
		case <-s.workerCtx.Done():
			return
		case <-s.wakeupCh:
		}
	}
}

// drainClaimableTasks repeatedly claims and executes pending or stale-running
// tasks until the queue is empty or workerCtx is canceled.
func (s *UsageExportService) drainClaimableTasks() {
	// Add a post-timeout grace window so a live worker whose execCtx deadline just
	// expired can finish MarkTaskFailed before peer workers treat the row as abandoned.
	staleSeconds := int64((s.taskTimeout() + defaultExportStaleClaimGrace).Seconds())
	for {
		if s.workerCtx != nil && s.workerCtx.Err() != nil {
			return
		}
		claimCtx, claimCancel := context.WithTimeout(s.parentContext(), 10*time.Second)
		task, err := s.repo.ClaimNextPendingTask(claimCtx, staleSeconds)
		claimCancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				s.logMessage("service.usage_export", "[UsageExport] claim pending task failed: %v", err)
			}
			return
		}
		if task == nil {
			return
		}

		execCtx, execCancel := context.WithTimeout(s.parentContext(), s.taskTimeout())
		s.executeTask(execCtx, task)
		execCancel()
	}
}

// executeTask counts matching rows, streams keyset batches into a temporary CSV
// writer, checks cancellation between batches, and atomically publishes the file.
//
// Execution timeouts and service shutdown take distinct recovery paths because CSV
// generation streams into an uncommitted .part file from row zero. Exceeding the
// per-task deadline while workerCtx remains active transitions the task to terminal
// failed status immediately so slow tasks cannot repeatedly reclaim worker slots,
// whereas graceful shutdown aborts partial output and requeues the running task
// back to pending. When MarkTaskSucceeded reports that a concurrent cancel won
// after Commit, the newly published CSV file is deleted immediately.
func (s *UsageExportService) executeTask(ctx context.Context, task *UsageExportTask) {
	if task == nil {
		return
	}
	start := s.currentTime()
	loc, _, err := LoadUsageExportLocation(task.Filters.Timezone)
	if err != nil {
		s.markTaskFailed(task.ID, 0, err)
		return
	}

	totalRows, err := s.rowReader.CountExportLogs(ctx, task.Filters)
	if err != nil {
		if isContextInterruption(err) {
			s.handleTaskInterruption(ctx, task.ID, 0, err, "count")
			return
		}
		s.markTaskFailed(task.ID, 0, err)
		return
	}
	s.updateProgressBestEffort(task.ID, totalRows, 0)

	artifactKey := buildUsageExportArtifactKey(task.Scope, task.ID)
	writer, err := s.store.CreateWriter(artifactKey)
	if err != nil {
		s.markTaskFailed(task.ID, 0, err)
		return
	}
	// Abort removes any uncommitted .part file on early return and becomes a no-op
	// once Commit renames the completed CSV to its final path.
	defer func() { _ = writer.Abort() }()

	csvWriter, err := newUsageExportCSVWriter(
		writer,
		task.Scope,
		task.Filters.Locale,
		task.Filters.BalanceUnitName,
		loc,
	)
	if err != nil {
		s.markTaskFailed(task.ID, 0, err)
		return
	}

	batchSize := s.batchSize()
	batchReader := s.rowReader.NewExportBatchReader(task.Scope)
	var (
		cursor        *UsageExportCursor
		exportedTotal int64
	)

	for {
		if ctx != nil && ctx.Err() != nil {
			_ = writer.Abort()
			_ = s.store.Delete(artifactKey)
			s.handleTaskInterruption(ctx, task.ID, exportedTotal, ctx.Err(), "batch loop")
			return
		}
		canceled, err := s.isTaskCanceled(task.ID)
		if err != nil {
			s.markTaskFailed(task.ID, exportedTotal, err)
			return
		}
		if canceled {
			_ = writer.Abort()
			_ = s.store.Delete(artifactKey)
			s.logMessage("service.usage_export", "[UsageExport] task canceled mid-stream: task=%d exported=%d", task.ID, exportedTotal)
			return
		}

		rows, err := batchReader.NextBatch(ctx, task.Filters, cursor, batchSize)
		if err != nil {
			if isContextInterruption(err) {
				_ = writer.Abort()
				_ = s.store.Delete(artifactKey)
				s.handleTaskInterruption(ctx, task.ID, exportedTotal, err, "batch read")
				return
			}
			s.markTaskFailed(task.ID, exportedTotal, err)
			return
		}
		if len(rows) == 0 {
			break
		}

		if err := csvWriter.WriteBatch(rows); err != nil {
			s.markTaskFailed(task.ID, exportedTotal, err)
			return
		}

		exportedTotal += int64(len(rows))
		last := rows[len(rows)-1]
		cursor = &UsageExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		// Keep total_rows at least exported_rows when rows arrive near end_time
		// after the initial count so UI progress never exceeds 100 percent.
		s.updateProgressBestEffort(task.ID, max(totalRows, exportedTotal), exportedTotal)

		if len(rows) < batchSize {
			break
		}
	}

	canceled, err := s.isTaskCanceled(task.ID)
	if err != nil {
		s.markTaskFailed(task.ID, exportedTotal, err)
		return
	}
	if canceled {
		_ = writer.Abort()
		_ = s.store.Delete(artifactKey)
		return
	}

	sizeBytes, err := writer.Commit()
	if err != nil {
		s.markTaskFailed(task.ID, exportedTotal, err)
		return
	}

	fileName := buildUsageExportFileName(task, loc)
	expiresAt := s.currentTime().UTC().Add(s.retentionDuration())
	updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	committed, err := s.repo.MarkTaskSucceeded(updateCtx, task.ID, exportedTotal, fileName, artifactKey, sizeBytes, expiresAt)
	if err != nil {
		// Remove the newly committed file when database publication fails so stale
		// task reclamation does not leave an unreferenced artifact on disk.
		_ = s.store.Delete(artifactKey)
		s.logMessage("service.usage_export", "[UsageExport] mark task succeeded failed: task=%d err=%v", task.ID, err)
		return
	}
	if !committed {
		// A concurrent cancellation won after Commit published the CSV; delete the
		// committed artifact immediately so canceled tasks retain zero disk files.
		_ = s.store.Delete(artifactKey)
		s.logMessage("service.usage_export", "[UsageExport] task canceled before finalize: task=%d", task.ID)
		return
	}

	s.logMessage(
		"service.usage_export",
		"[UsageExport] task succeeded: task=%d scope=%s rows=%d bytes=%d duration=%s",
		task.ID,
		task.Scope,
		exportedTotal,
		sizeBytes,
		s.currentTime().Sub(start),
	)
}

// sweepExpiredAndStaleParts deletes artifacts for expired succeeded tasks before
// marking them expired and removes abandoned .part files older than task timeout.
func (s *UsageExportService) sweepExpiredAndStaleParts() {
	if s == nil || s.repo == nil || s.store == nil {
		return
	}
	if !atomic.CompareAndSwapInt32(&s.sweeping, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&s.sweeping, 0)

	sweepCtx, cancel := context.WithTimeout(s.parentContext(), 15*time.Second)
	defer cancel()

	expiredTasks, err := s.repo.ListExpiredSucceededTasks(sweepCtx, s.currentTime().UTC(), defaultExportSweepBatchLimit)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.logMessage("service.usage_export", "[UsageExport] list expired tasks failed: %v", err)
		}
	} else {
		for i := range expiredTasks {
			task := &expiredTasks[i]
			if task.FilePath != nil && strings.TrimSpace(*task.FilePath) != "" {
				// Delete the local CSV artifact before clearing persisted FilePath so a
				// transient filesystem error leaves the path recorded for the next sweep.
				if delErr := s.store.Delete(*task.FilePath); delErr != nil {
					s.logMessage("service.usage_export", "[UsageExport] delete expired file failed: task=%d err=%v", task.ID, delErr)
					continue
				}
			}
			if markErr := s.repo.MarkTaskExpired(sweepCtx, task.ID); markErr != nil {
				s.logMessage("service.usage_export", "[UsageExport] mark task expired failed: task=%d err=%v", task.ID, markErr)
			}
		}
	}

	stalePartAge := s.taskTimeout() + defaultExportStalePartGraceTime
	if _, sweepErr := s.store.SweepStalePartFiles(stalePartAge); sweepErr != nil {
		s.logMessage("service.usage_export", "[UsageExport] sweep stale part files failed: %v", sweepErr)
	}
}

// expireTaskBestEffort removes any persisted artifact and marks task expired
// using a short background timeout when read-time TTL enforcement triggers.
func (s *UsageExportService) expireTaskBestEffort(task *UsageExportTask) {
	if s == nil || task == nil {
		return
	}
	if s.store != nil && task.FilePath != nil && strings.TrimSpace(*task.FilePath) != "" {
		if err := s.store.Delete(*task.FilePath); err != nil {
			s.logMessage("service.usage_export", "[UsageExport] delete expired artifact failed: task=%d err=%v", task.ID, err)
			return
		}
	}
	if s.repo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.repo.MarkTaskExpired(ctx, task.ID); err != nil {
			s.logMessage("service.usage_export", "[UsageExport] mark expired best-effort failed: task=%d err=%v", task.ID, err)
		}
	}
}

// updateProgressBestEffort writes cumulative row counters without aborting an
// otherwise healthy export stream when a transient progress update fails.
func (s *UsageExportService) updateProgressBestEffort(taskID, totalRows, exportedRows int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Progress updates are advisory UI telemetry; log transient write errors and
	// keep streaming so large exports survive brief metadata contention.
	if err := s.repo.UpdateTaskProgress(ctx, taskID, totalRows, exportedRows); err != nil {
		s.logMessage("service.usage_export", "[UsageExport] update progress failed: task=%d err=%v", taskID, err)
	}
}

// isTaskCanceled checks persisted status using an independent short timeout,
// treating a missing task row as uncanceled so repository updates decide state.
func (s *UsageExportService) isTaskCanceled(taskID int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := s.repo.GetTaskStatus(ctx, taskID)
	if err != nil {
		if errors.Is(err, ErrExportTaskNotFound) {
			return false, nil
		}
		return false, err
	}
	return status == UsageExportStatusCanceled, nil
}

// handleTaskInterruption separates graceful service shutdown from per-task
// execution timeouts so timed-out tasks fail terminally while shutdown-interrupted
// tasks return to pending for the next process startup.
func (s *UsageExportService) handleTaskInterruption(
	ctx context.Context,
	taskID, exportedRows int64,
	cause error,
	stage string,
) {
	timedOut := errors.Is(cause, context.DeadlineExceeded) ||
		(ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
	if !timedOut && s.parentContext().Err() != nil {
		s.logMessage(
			"service.usage_export",
			"[UsageExport] %s interrupted by shutdown: task=%d exported=%d err=%v",
			stage,
			taskID,
			exportedRows,
			cause,
		)
		s.requeueInterruptedTask(taskID)
		return
	}

	failErr := fmt.Errorf("export task interrupted during %s: %w", stage, cause)
	if timedOut {
		failErr = fmt.Errorf("export task timed out after %s", s.taskTimeout())
	}
	s.markTaskFailed(taskID, exportedRows, failErr)
}

// requeueInterruptedTask resets a running task back to pending on a short
// background timeout during Stop so restarted workers resume promptly without
// waiting for the stale-running timeout window.
func (s *UsageExportService) requeueInterruptedTask(taskID int64) {
	if s == nil || s.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.repo.RequeueTask(ctx, taskID); err != nil {
		s.logMessage("service.usage_export", "[UsageExport] requeue interrupted task failed: task=%d err=%v", taskID, err)
	}
}

// markTaskFailed truncates err to 500 UTF-8-safe bytes and records failed status,
// retrying once on transient database errors so timed-out tasks do not linger in running.
func (s *UsageExportService) markTaskFailed(taskID, exportedRows int64, err error) {
	msg := "export failed"
	if err != nil {
		msg = truncateUTF8Bytes(strings.TrimSpace(err.Error()), maxExportErrorMsgBytes)
	}
	s.logMessage("service.usage_export", "[UsageExport] task failed: task=%d exported=%d err=%s", taskID, exportedRows, msg)
	for attempt := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		updateErr := s.repo.MarkTaskFailed(ctx, taskID, exportedRows, msg)
		cancel()
		if updateErr == nil {
			return
		}
		if attempt == 0 {
			// Retry once after a brief pause so a transient connection reset right as a
			// slow query times out still persists terminal failed status.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		s.logMessage("service.usage_export", "[UsageExport] mark task failed write error: task=%d err=%v", taskID, updateErr)
	}
}

// buildUsageExportArtifactKey returns the store-relative key under user/ or admin/.
func buildUsageExportArtifactKey(scope string, taskID int64) string {
	prefix := UsageExportScopeUser
	if scope == UsageExportScopeAdmin {
		prefix = UsageExportScopeAdmin
	}
	return fmt.Sprintf("%s/usage-export-%d.csv", prefix, taskID)
}

// buildUsageExportFileName formats the browser attachment filename using the
// task's snapshotted timezone bounds and identifier.
func buildUsageExportFileName(task *UsageExportTask, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	prefix := "usage_export"
	if task.Scope == UsageExportScopeAdmin {
		prefix = "admin_usage_export"
	}
	startStr := task.Filters.StartTime.In(loc).Format("20060102_150405")
	endStr := task.Filters.EndTime.In(loc).Format("20060102_150405")
	return fmt.Sprintf("%s_%s_%s_%d.csv", prefix, startStr, endStr, task.ID)
}

// truncateUTF8Bytes trims s to at most maxBytes without splitting a multibyte rune.
func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Step backward across UTF-8 continuation bytes so persisted failure text
	// remains valid UTF-8 for PostgreSQL text columns and JSON responses.
	end := maxBytes
	for end > 0 && (s[end]&0xC0) == 0x80 {
		end--
	}
	return s[:end]
}

// isContextInterruption reports whether err stems from context cancellation or deadline expiry.
func isContextInterruption(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// ensureAvailable checks the feature gate before verifying storage dependencies
// so disabled deployments where local artifact storage was skipped at startup
// return 503 USAGE_EXPORT_DISABLED rather than a 500 readiness error.
func (s *UsageExportService) ensureAvailable() error {
	if s == nil {
		return fmt.Errorf("usage export service not ready")
	}
	if !s.isEnabled() {
		return infraerrors.ServiceUnavailable("USAGE_EXPORT_DISABLED", "usage export is disabled")
	}
	return s.requireReady()
}

// requireReady verifies that core storage and reader dependencies are bound.
func (s *UsageExportService) requireReady() error {
	if s == nil || s.repo == nil || s.rowReader == nil || s.store == nil {
		return fmt.Errorf("usage export service not ready")
	}
	return nil
}

// isEnabled reports whether background exports are enabled by configuration.
func (s *UsageExportService) isEnabled() bool {
	if s == nil {
		return false
	}
	if s.cfg == nil {
		return true
	}
	return s.cfg.UsageExport.Enabled
}

// currentTime returns the service clock instant, defaulting to time.Now.
func (s *UsageExportService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

// parentContext returns workerCtx when initialized or context.Background.
func (s *UsageExportService) parentContext() context.Context {
	if s != nil && s.workerCtx != nil {
		return s.workerCtx
	}
	return context.Background()
}

// maxActiveLimit returns the configured per-actor active task ceiling for scope.
func (s *UsageExportService) maxActiveLimit(scope string) int {
	if scope == UsageExportScopeAdmin {
		if s != nil && s.cfg != nil && s.cfg.UsageExport.MaxActivePerAdmin > 0 {
			return s.cfg.UsageExport.MaxActivePerAdmin
		}
		return defaultExportMaxActivePerAdmin
	}
	if s != nil && s.cfg != nil && s.cfg.UsageExport.MaxActivePerUser > 0 {
		return s.cfg.UsageExport.MaxActivePerUser
	}
	return defaultExportMaxActivePerUser
}

// retentionDuration returns the post-completion artifact retention duration.
func (s *UsageExportService) retentionDuration() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.UsageExport.RetentionHours > 0 {
		return time.Duration(s.cfg.UsageExport.RetentionHours) * time.Hour
	}
	return defaultExportRetentionHours * time.Hour
}

// batchSize returns the configured keyset scan batch limit or 2000 rows.
func (s *UsageExportService) batchSize() int {
	if s != nil && s.cfg != nil && s.cfg.UsageExport.BatchSize > 0 {
		return s.cfg.UsageExport.BatchSize
	}
	return defaultExportBatchSize
}

// workerConcurrency returns the configured worker goroutine count or 2.
func (s *UsageExportService) workerConcurrency() int {
	if s != nil && s.cfg != nil && s.cfg.UsageExport.WorkerConcurrency > 0 {
		return s.cfg.UsageExport.WorkerConcurrency
	}
	return defaultExportWorkerConcurrency
}

// workerInterval returns the recurring sweep and poll interval or 5 seconds.
func (s *UsageExportService) workerInterval() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.UsageExport.WorkerIntervalSeconds > 0 {
		return time.Duration(s.cfg.UsageExport.WorkerIntervalSeconds) * time.Second
	}
	return defaultExportWorkerInterval
}

// taskTimeout returns the per-task execution timeout and stale-running threshold.
func (s *UsageExportService) taskTimeout() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.UsageExport.TaskTimeoutSeconds > 0 {
		return time.Duration(s.cfg.UsageExport.TaskTimeoutSeconds) * time.Second
	}
	return defaultExportTaskTimeout
}

// logMessage forwards export diagnostics through Options.Logf or logUsage.
func (s *UsageExportService) logMessage(component, format string, args ...any) {
	if s != nil && s.cfg != nil && s.cfg.Logf != nil {
		s.cfg.Logf(component, format, args...)
		return
	}
	logUsage(component, format, args...)
}
