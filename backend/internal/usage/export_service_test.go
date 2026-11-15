package usage

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// inMemoryExportRepo implements UsageExportRepository in memory for deterministic
// service lifecycle, quota, cancellation, and expiry tests.
type inMemoryExportRepo struct {
	mu     sync.Mutex
	nextID int64
	tasks  map[int64]*UsageExportTask

	// onBeforeMarkSucceeded runs inside MarkTaskSucceeded before status check so
	// tests can deterministically inject a concurrent user cancellation race.
	onBeforeMarkSucceeded func(taskID int64)
}

// inMemoryArtifactStore stores committed CSV files and staging buffers in memory.
type inMemoryArtifactStore struct {
	mu    sync.Mutex
	files map[string][]byte
}

// memArtifactWriter buffers staging bytes until Commit publishes them into store.
type memArtifactWriter struct {
	store     *inMemoryArtifactStore // target artifact map owner
	key       string                 // destination artifact key
	buf       bytes.Buffer           // staged CSV bytes
	committed bool                   // whether Commit already ran
	aborted   bool                   // whether Abort already ran
}

// sliceExportRowReader streams preloaded UsageLog rows in keyset order for tests.
type sliceExportRowReader struct {
	rows []UsageLog // descending (CreatedAt, ID) ordered test rows
}

// staticBalanceUnitReader supplies a fixed currency unit label in service tests.
type staticBalanceUnitReader string

// scriptedExportRowReader delegates per-task batch reads to a caller-supplied
// function so tests can block specific tasks until timeout or service shutdown.
type scriptedExportRowReader struct {
	countFn func(ctx context.Context, filters UsageExportFilters) (int64, error)                                 // custom CountExportLogs callback
	batchFn func(ctx context.Context, filters UsageExportFilters, cursor *UsageExportCursor) ([]UsageLog, error) // custom NextBatch callback
}

// newInMemoryExportRepo constructs an empty in-memory export repository fixture.
func newInMemoryExportRepo() *inMemoryExportRepo {
	return &inMemoryExportRepo{
		tasks: make(map[int64]*UsageExportTask),
	}
}

// CreateTask atomically checks maxActive under r.mu before storing a cloned task.
func (r *inMemoryExportRepo) CreateTask(_ context.Context, task *UsageExportTask, maxActive int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if maxActive > 0 {
		var active int
		for _, existing := range r.tasks {
			if existing.Scope != task.Scope {
				continue
			}
			if task.CreatedBy > 0 && existing.CreatedBy != task.CreatedBy {
				continue
			}
			if existing.Status == UsageExportStatusPending || existing.Status == UsageExportStatusRunning {
				active++
			}
		}
		if active >= maxActive {
			return ErrExportActiveLimitExceeded
		}
	}
	r.nextID++
	now := time.Now().UTC()
	task.ID = r.nextID
	task.CreatedAt = now
	task.UpdatedAt = now
	cloned := *task
	r.tasks[task.ID] = &cloned
	return nil
}

// GetTask returns a value copy of taskID or ErrExportTaskNotFound.
func (r *inMemoryExportRepo) GetTask(_ context.Context, taskID int64) (*UsageExportTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[taskID]
	if !ok {
		return nil, ErrExportTaskNotFound
	}
	cp := *t
	return &cp, nil
}

// ListTaskPage reads one consistent snapshot of paginated tasks, active IDs, and
// off-page watched tasks under a single mutex lock.
func (r *inMemoryExportRepo) ListTaskPage(
	_ context.Context,
	filter UsageExportListFilter,
	params pagination.PaginationParams,
	watchIDs []int64,
) (*UsageExportTaskListResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matched []UsageExportTask
	for _, t := range r.tasks {
		if t.Scope != filter.Scope {
			continue
		}
		if filter.CreatedBy != nil && t.CreatedBy != *filter.CreatedBy {
			continue
		}
		matched = append(matched, *t)
	}
	slices.SortFunc(matched, func(a, b UsageExportTask) int {
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		return 0
	})
	total := int64(len(matched))
	offset := min(params.Offset(), len(matched))
	end := min(offset+params.Limit(), len(matched))
	pageItems := matched[offset:end]

	onPage := make(map[int64]struct{}, len(pageItems))
	for i := range pageItems {
		onPage[pageItems[i].ID] = struct{}{}
	}
	wanted := make(map[int64]struct{}, len(watchIDs))
	for _, id := range watchIDs {
		if _, present := onPage[id]; !present && id > 0 {
			wanted[id] = struct{}{}
		}
	}

	activeIDs := make([]int64, 0)
	var watchedItems []UsageExportTask
	for i := range matched {
		if matched[i].Status == UsageExportStatusPending || matched[i].Status == UsageExportStatusRunning {
			activeIDs = append(activeIDs, matched[i].ID)
		}
		if _, ok := wanted[matched[i].ID]; ok {
			watchedItems = append(watchedItems, matched[i])
		}
	}

	return &UsageExportTaskListResult{
		Items:        pageItems,
		Pagination:   pagination.ResultFromTotal(total, params),
		ActiveCount:  int64(len(activeIDs)),
		ActiveIDs:    activeIDs,
		WatchedItems: watchedItems,
	}, nil
}

// CountActiveTasks counts pending and running tasks in scope for createdBy.
func (r *inMemoryExportRepo) CountActiveTasks(_ context.Context, scope string, createdBy int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for _, t := range r.tasks {
		if t.Scope != scope {
			continue
		}
		if createdBy > 0 && t.CreatedBy != createdBy {
			continue
		}
		if t.Status == UsageExportStatusPending || t.Status == UsageExportStatusRunning {
			count++
		}
	}
	return count, nil
}

// ClaimNextPendingTask claims the lowest-ID pending or stale-running task and
// transitions it to running with zeroed progress.
func (r *inMemoryExportRepo) ClaimNextPendingTask(_ context.Context, staleAfterSeconds int64) (*UsageExportTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if staleAfterSeconds <= 0 {
		staleAfterSeconds = 1800
	}
	staleCutoff := time.Now().UTC().Add(-time.Duration(staleAfterSeconds) * time.Second)
	var candidate *UsageExportTask
	for _, t := range r.tasks {
		eligible := t.Status == UsageExportStatusPending ||
			(t.Status == UsageExportStatusRunning && t.StartedAt != nil && t.StartedAt.Before(staleCutoff))
		if !eligible {
			continue
		}
		if candidate == nil || t.ID < candidate.ID {
			candidate = t
		}
	}
	if candidate == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	candidate.Status = UsageExportStatusRunning
	candidate.ExportedRows = 0
	candidate.StartedAt = &now
	candidate.UpdatedAt = now
	cp := *candidate
	return &cp, nil
}

// RequeueTask resets a running task back to pending with cleared claim fields.
func (r *inMemoryExportRepo) RequeueTask(_ context.Context, taskID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tasks[taskID]; ok && t.Status == UsageExportStatusRunning {
		t.Status = UsageExportStatusPending
		t.ExportedRows = 0
		t.StartedAt = nil
		t.UpdatedAt = time.Now().UTC()
	}
	return nil
}

// GetTaskStatus returns persisted status or ErrExportTaskNotFound.
func (r *inMemoryExportRepo) GetTaskStatus(_ context.Context, taskID int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[taskID]
	if !ok {
		return "", ErrExportTaskNotFound
	}
	return t.Status, nil
}

// UpdateTaskProgress updates row counters while taskID remains running.
func (r *inMemoryExportRepo) UpdateTaskProgress(_ context.Context, taskID, totalRows, exportedRows int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tasks[taskID]; ok && t.Status == UsageExportStatusRunning {
		t.TotalRows = totalRows
		t.ExportedRows = exportedRows
	}
	return nil
}

// CancelTask transitions a pending or running task to canceled.
func (r *inMemoryExportRepo) CancelTask(_ context.Context, taskID, canceledBy int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[taskID]
	if !ok || (t.Status != UsageExportStatusPending && t.Status != UsageExportStatusRunning) {
		return false, nil
	}
	now := time.Now().UTC()
	t.Status = UsageExportStatusCanceled
	t.CanceledBy = &canceledBy
	t.CanceledAt = &now
	t.FinishedAt = &now
	return true, nil
}

// MarkTaskSucceeded transitions non-canceled tasks to succeeded with file metadata.
func (r *inMemoryExportRepo) MarkTaskSucceeded(
	_ context.Context,
	taskID, exportedRows int64,
	fileName, filePath string,
	fileSizeBytes int64,
	expiresAt time.Time,
) (bool, error) {
	if r.onBeforeMarkSucceeded != nil {
		r.onBeforeMarkSucceeded(taskID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[taskID]
	if !ok || t.Status == UsageExportStatusCanceled {
		return false, nil
	}
	now := time.Now().UTC()
	t.Status = UsageExportStatusSucceeded
	t.ExportedRows = exportedRows
	t.FileName = &fileName
	t.FilePath = &filePath
	t.FileSizeBytes = fileSizeBytes
	t.ExpiresAt = &expiresAt
	t.FinishedAt = &now
	return true, nil
}

// MarkTaskFailed transitions non-canceled tasks to failed.
func (r *inMemoryExportRepo) MarkTaskFailed(_ context.Context, taskID, exportedRows int64, errorMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[taskID]
	if !ok || t.Status == UsageExportStatusCanceled {
		return nil
	}
	now := time.Now().UTC()
	t.Status = UsageExportStatusFailed
	t.ExportedRows = exportedRows
	t.ErrorMsg = &errorMsg
	t.FinishedAt = &now
	return nil
}

// ListExpiredSucceededTasks returns succeeded tasks whose ExpiresAt <= now.
func (r *inMemoryExportRepo) ListExpiredSucceededTasks(_ context.Context, now time.Time, limit int) ([]UsageExportTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []UsageExportTask
	for _, t := range r.tasks {
		if t.Status == UsageExportStatusSucceeded && t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
			out = append(out, *t)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// MarkTaskExpired marks a succeeded task expired and clears FilePath.
func (r *inMemoryExportRepo) MarkTaskExpired(_ context.Context, taskID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tasks[taskID]; ok && t.Status == UsageExportStatusSucceeded {
		t.Status = UsageExportStatusExpired
		t.FilePath = nil
	}
	return nil
}

// newInMemoryArtifactStore constructs an empty in-memory CSV artifact store.
func newInMemoryArtifactStore() *inMemoryArtifactStore {
	return &inMemoryArtifactStore{files: make(map[string][]byte)}
}

// Write appends p to the staged buffer.
func (w *memArtifactWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

// Commit publishes buffered bytes into w.store.files under w.key.
func (w *memArtifactWriter) Commit() (int64, error) {
	w.committed = true
	data := slices.Clone(w.buf.Bytes())
	w.store.mu.Lock()
	w.store.files[w.key] = data
	w.store.mu.Unlock()
	return int64(len(data)), nil
}

// Abort marks the staging writer aborted without publishing staged bytes.
func (w *memArtifactWriter) Abort() error {
	w.aborted = true
	return nil
}

// CreateWriter allocates a staging buffer for key.
func (s *inMemoryArtifactStore) CreateWriter(key string) (UsageExportArtifactWriter, error) {
	return &memArtifactWriter{store: s, key: key}, nil
}

// Open returns an io.ReadCloser over key's bytes or ErrExportArtifactNotFound.
func (s *inMemoryArtifactStore) Open(key string) (io.ReadCloser, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[key]
	if !ok {
		return nil, 0, ErrExportArtifactNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

// Delete removes key from the in-memory store.
func (s *inMemoryArtifactStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, key)
	return nil
}

// SweepStalePartFiles returns zero in the in-memory store.
func (s *inMemoryArtifactStore) SweepStalePartFiles(_ time.Duration) (int, error) { return 0, nil }

// CountExportLogs returns len(r.rows).
func (r *sliceExportRowReader) CountExportLogs(_ context.Context, _ UsageExportFilters) (int64, error) {
	return int64(len(r.rows)), nil
}

// NewExportBatchReader returns r as a stateless UsageExportBatchReader.
func (r *sliceExportRowReader) NewExportBatchReader(_ string) UsageExportBatchReader { return r }

// NextBatch returns up to limit rows strictly older than cursor.
func (r *sliceExportRowReader) NextBatch(
	_ context.Context,
	_ UsageExportFilters,
	cursor *UsageExportCursor,
	limit int,
) ([]UsageLog, error) {
	var out []UsageLog
	for _, row := range r.rows {
		if cursor != nil {
			if row.CreatedAt.After(cursor.CreatedAt) || (row.CreatedAt.Equal(cursor.CreatedAt) && row.ID >= cursor.ID) {
				continue
			}
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// GetBalanceUnitName returns the static unit string.
func (u staticBalanceUnitReader) GetBalanceUnitName(_ context.Context) string { return string(u) }

// CountExportLogs invokes r.countFn when set or defaults to one matching row.
func (r *scriptedExportRowReader) CountExportLogs(ctx context.Context, filters UsageExportFilters) (int64, error) {
	if r.countFn != nil {
		return r.countFn(ctx, filters)
	}
	return 1, nil
}

// NewExportBatchReader returns r as an isolated UsageExportBatchReader.
func (r *scriptedExportRowReader) NewExportBatchReader(_ string) UsageExportBatchReader {
	return r
}

// NextBatch invokes r.batchFn for the first page and ends once cursor is nonnil.
func (r *scriptedExportRowReader) NextBatch(
	ctx context.Context,
	filters UsageExportFilters,
	cursor *UsageExportCursor,
	_ int,
) ([]UsageLog, error) {
	if cursor != nil {
		return nil, nil
	}
	return r.batchFn(ctx, filters, cursor)
}

// TestUsageExportServiceQuotasStreamingTicketAndExpiry exercises end-to-end user
// task creation, active-task quota enforcement, multi-batch keyset CSV writing,
// and cross-user isolation. Signed download tickets stream UTF-8 BOM CSV bytes
// before retention elapses and return HTTP 410 USAGE_EXPORT_EXPIRED afterward.
func TestUsageExportServiceQuotasStreamingTicketAndExpiry(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryArtifactStore()
	t0 := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	reader := &sliceExportRowReader{
		rows: []UsageLog{
			{ID: 3, CreatedAt: t0.Add(-1 * time.Minute), Model: "gpt-5.4", ActualCost: 0.1},
			{ID: 2, CreatedAt: t0.Add(-2 * time.Minute), Model: "gpt-5.4-mini", ActualCost: 0.05},
			{ID: 1, CreatedAt: t0.Add(-2 * time.Minute), Model: "claude-sonnet-4-6", ActualCost: 0.2},
		},
	}

	opts := &Options{
		UsageExport: UsageExportConfig{
			Enabled:           true,
			RetentionHours:    24,
			MaxActivePerUser:  2,
			MaxActivePerAdmin: 5,
			BatchSize:         2,
			WorkerConcurrency: 1,
		},
	}
	svc := NewUsageExportService(repo, reader, store, staticBalanceUnitReader("Credits"), nil, opts)
	now := t0
	svc.now = func() time.Time { return now }

	filters := UsageExportFilters{
		StartTime: t0.Add(-1 * time.Hour),
		EndTime:   t0,
		Timezone:  "Asia/Shanghai",
		Locale:    "en",
	}

	task1, err := svc.CreateUserTask(context.Background(), 101, filters)
	require.NoError(t, err)
	task2, err := svc.CreateUserTask(context.Background(), 101, filters)
	require.NoError(t, err)

	_, quotaErr := svc.CreateUserTask(context.Background(), 101, filters)
	require.Error(t, quotaErr)
	require.Equal(t, "USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED", infraerrors.FromError(quotaErr).Reason)

	canceledTask2, err := svc.CancelUserTask(context.Background(), 101, task2.ID)
	require.NoError(t, err)
	require.Equal(t, UsageExportStatusCanceled, canceledTask2.Status)

	// Another user attempting to cancel or download task1 receives 404 rather
	// than leaking task existence across tenant boundaries.
	_, _, foreignErr := svc.IssueUserDownloadTicket(context.Background(), 202, task1.ID)
	require.Error(t, foreignErr)
	require.Equal(t, infraerrors.CategoryNotFound, infraerrors.CategoryOf(foreignErr))

	svc.RunOnce()

	ticket, ticketExp, err := svc.IssueUserDownloadTicket(context.Background(), 101, task1.ID)
	require.NoError(t, err)
	require.Equal(t, now.Add(60*time.Second), ticketExp)

	stream, err := svc.OpenDownloadByTicket(context.Background(), ticket)
	require.NoError(t, err)
	csvBytes, err := io.ReadAll(stream.Reader)
	require.NoError(t, err)
	require.NoError(t, stream.Reader.Close())
	csvText := string(csvBytes)
	require.True(t, strings.HasPrefix(csvText, utf8BOM))
	require.Contains(t, csvText, "User billed (Credits)")
	require.Contains(t, csvText, "claude-sonnet-4-6")

	// Advance clock past 24h retention and confirm ticket exchange returns 410
	// while cleaning up the expired CSV artifact from storage.
	now = now.Add(25 * time.Hour)
	_, _, expiredErr := svc.IssueUserDownloadTicket(context.Background(), 101, task1.ID)
	require.Error(t, expiredErr)
	require.Equal(t, infraerrors.Category(410), infraerrors.CategoryOf(expiredErr))
	require.Equal(t, "USAGE_EXPORT_EXPIRED", infraerrors.FromError(expiredErr).Reason)
	require.Empty(t, store.files)
}

// TestUsageExportServiceDeletesArtifactOnConcurrentCancelRace reproduces a user
// cancellation winning immediately after the local CSV writer commits. Because
// MarkTaskSucceeded refuses to overwrite the canceled row, the worker deletes
// the newly committed CSV artifact so no orphan file remains on disk.
func TestUsageExportServiceDeletesArtifactOnConcurrentCancelRace(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryArtifactStore()
	t0 := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	reader := &sliceExportRowReader{
		rows: []UsageLog{{ID: 1, CreatedAt: t0.Add(-time.Minute), Model: "gpt-5.4"}},
	}
	opts := &Options{UsageExport: UsageExportConfig{Enabled: true, BatchSize: 10}}
	svc := NewUsageExportService(repo, reader, store, nil, nil, opts)

	repo.onBeforeMarkSucceeded = func(taskID int64) {
		_, _ = repo.CancelTask(context.Background(), taskID, 55)
	}

	task, err := svc.CreateUserTask(context.Background(), 55, UsageExportFilters{
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Timezone:  "UTC",
	})
	require.NoError(t, err)

	svc.RunOnce()

	persisted, err := repo.GetTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, UsageExportStatusCanceled, persisted.Status)
	require.Empty(t, store.files)
}

// TestUsageExportServiceTimedOutTasksFailTerminallyWithoutStarvingQueue queues
// two persistently slow tasks ahead of a healthy task across two workers. Both
// slow tasks transition to terminal failed status on deadline expiry instead of
// staying running and starving the queue, allowing the healthy task to succeed.
func TestUsageExportServiceTimedOutTasksFailTerminallyWithoutStarvingQueue(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryArtifactStore()
	t0 := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	var slow1Runs, slow2Runs int32
	reader := &scriptedExportRowReader{
		batchFn: func(ctx context.Context, filters UsageExportFilters, _ *UsageExportCursor) ([]UsageLog, error) {
			if filters.RequestID == "slow-1" {
				atomic.AddInt32(&slow1Runs, 1)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			if filters.RequestID == "slow-2" {
				atomic.AddInt32(&slow2Runs, 1)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return []UsageLog{{ID: 99, CreatedAt: t0.Add(-time.Minute), Model: "gpt-5.4", ActualCost: 0.05}}, nil
		},
	}

	opts := &Options{
		UsageExport: UsageExportConfig{
			Enabled:               true,
			RetentionHours:        24,
			MaxActivePerAdmin:     5,
			BatchSize:             10,
			WorkerConcurrency:     2,
			WorkerIntervalSeconds: 1,
			TaskTimeoutSeconds:    1,
		},
	}
	svc := NewUsageExportService(repo, reader, store, nil, nil, opts)

	slow1, err := svc.CreateAdminTask(context.Background(), 1, UsageExportFilters{
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Timezone:  "UTC",
		RequestID: "slow-1",
	})
	require.NoError(t, err)
	slow2, err := svc.CreateAdminTask(context.Background(), 1, UsageExportFilters{
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Timezone:  "UTC",
		RequestID: "slow-2",
	})
	require.NoError(t, err)
	fast3, err := svc.CreateAdminTask(context.Background(), 1, UsageExportFilters{
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Timezone:  "UTC",
		RequestID: "fast-3",
	})
	require.NoError(t, err)

	svc.Start()
	t.Cleanup(svc.Stop)

	require.Eventually(t, func() bool {
		gotFast, errFast := repo.GetTask(context.Background(), fast3.ID)
		gotS1, errS1 := repo.GetTask(context.Background(), slow1.ID)
		gotS2, errS2 := repo.GetTask(context.Background(), slow2.ID)
		return errFast == nil && errS1 == nil && errS2 == nil &&
			gotFast.Status == UsageExportStatusSucceeded &&
			gotS1.Status == UsageExportStatusFailed &&
			gotS2.Status == UsageExportStatusFailed
	}, 4*time.Second, 25*time.Millisecond)

	gotSlow1, err := repo.GetTask(context.Background(), slow1.ID)
	require.NoError(t, err)
	require.Equal(t, UsageExportStatusFailed, gotSlow1.Status)
	require.NotNil(t, gotSlow1.ErrorMsg)
	require.Contains(t, *gotSlow1.ErrorMsg, "timed out")

	gotSlow2, err := repo.GetTask(context.Background(), slow2.ID)
	require.NoError(t, err)
	require.Equal(t, UsageExportStatusFailed, gotSlow2.Status)
	require.NotNil(t, gotSlow2.ErrorMsg)
	require.Contains(t, *gotSlow2.ErrorMsg, "timed out")

	// Trigger another drain pass after timeout to confirm timed-out tasks never re-enter execution.
	svc.RunOnce()
	require.Equal(t, int32(1), atomic.LoadInt32(&slow1Runs))
	require.Equal(t, int32(1), atomic.LoadInt32(&slow2Runs))
}

// TestUsageExportServiceGracefulShutdownRequeuesInterruptedTask interrupts an
// in-flight worker batch via Stop before the per-task timeout elapses. Shutdown
// requeues the running task back to pending rather than marking it failed so a
// newly started service instance completes the CSV cleanly.
func TestUsageExportServiceGracefulShutdownRequeuesInterruptedTask(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryArtifactStore()
	t0 := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	enteredBatch := make(chan struct{})
	var blockFirstRun sync.Once
	reader := &scriptedExportRowReader{
		batchFn: func(ctx context.Context, _ UsageExportFilters, _ *UsageExportCursor) ([]UsageLog, error) {
			blocked := false
			blockFirstRun.Do(func() {
				blocked = true
				close(enteredBatch)
				<-ctx.Done()
			})
			if blocked {
				return nil, ctx.Err()
			}
			return []UsageLog{{ID: 7, CreatedAt: t0.Add(-time.Minute), Model: "gpt-5.4"}}, nil
		},
	}

	opts := &Options{
		UsageExport: UsageExportConfig{
			Enabled:            true,
			BatchSize:          10,
			WorkerConcurrency:  1,
			TaskTimeoutSeconds: 60,
		},
	}
	svc1 := NewUsageExportService(repo, reader, store, nil, nil, opts)
	task, err := svc1.CreateUserTask(context.Background(), 42, UsageExportFilters{
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Timezone:  "UTC",
	})
	require.NoError(t, err)

	svc1.Start()
	<-enteredBatch
	svc1.Stop()

	afterStop, err := repo.GetTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, UsageExportStatusPending, afterStop.Status)
	require.Nil(t, afterStop.ErrorMsg)

	svc2 := NewUsageExportService(repo, reader, store, nil, nil, opts)
	svc2.RunOnce()

	completed, err := repo.GetTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, UsageExportStatusSucceeded, completed.Status)
}

// TestUsageExportServiceConcurrentCreateEnforcesActiveQuotaAtomically launches
// concurrent CreateUserTask calls when one active task already exists under a
// limit of two. Exactly one concurrent caller succeeds while the remaining
// callers receive USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED, leaving two active tasks.
func TestUsageExportServiceConcurrentCreateEnforcesActiveQuotaAtomically(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryArtifactStore()
	t0 := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	reader := &sliceExportRowReader{}

	opts := &Options{
		UsageExport: UsageExportConfig{
			Enabled:          true,
			MaxActivePerUser: 2,
		},
	}
	svc := NewUsageExportService(repo, reader, store, nil, nil, opts)
	filters := UsageExportFilters{
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Timezone:  "UTC",
	}

	_, err := svc.CreateUserTask(context.Background(), 77, filters)
	require.NoError(t, err)

	const contenders = 6
	startBarrier := make(chan struct{})
	errs := make([]error, contenders)
	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startBarrier
			_, errs[idx] = svc.CreateUserTask(context.Background(), 77, filters)
		}(i)
	}
	close(startBarrier)
	wg.Wait()

	var succeeded, quotaRejected int
	for _, callErr := range errs {
		if callErr == nil {
			succeeded++
			continue
		}
		if infraerrors.FromError(callErr).Reason == "USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED" {
			quotaRejected++
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, contenders-1, quotaRejected)

	activeCount, err := repo.CountActiveTasks(context.Background(), UsageExportScopeUser, 77)
	require.NoError(t, err)
	require.Equal(t, int64(2), activeCount)
}

// TestUsageExportServiceDisabledWithNilStoreReturnsDisabledError constructs
// UsageExportService with Enabled=false and a nil artifact store matching app
// wiring. Every task and download method returns 503 USAGE_EXPORT_DISABLED
// instead of failing early on store readiness with an internal error.
func TestUsageExportServiceDisabledWithNilStoreReturnsDisabledError(t *testing.T) {
	repo := newInMemoryExportRepo()
	reader := &sliceExportRowReader{}
	svc := NewUsageExportService(repo, reader, nil, nil, nil, &Options{
		UsageExport: UsageExportConfig{Enabled: false},
	})
	ctx := context.Background()
	t0 := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	filters := UsageExportFilters{
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Timezone:  "UTC",
	}

	assertDisabled := func(err error) {
		t.Helper()
		require.Error(t, err)
		require.Equal(t, infraerrors.CategoryServiceUnavailable, infraerrors.CategoryOf(err))
		require.Equal(t, "USAGE_EXPORT_DISABLED", infraerrors.FromError(err).Reason)
	}

	_, err := svc.ListUserTasks(ctx, 1, pagination.PaginationParams{Page: 1, PageSize: 10})
	assertDisabled(err)

	_, err = svc.ListAdminTasks(ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	assertDisabled(err)

	_, err = svc.CreateUserTask(ctx, 1, filters)
	assertDisabled(err)

	_, err = svc.CreateAdminTask(ctx, 1, filters)
	assertDisabled(err)

	_, err = svc.CancelUserTask(ctx, 1, 10)
	assertDisabled(err)

	_, _, err = svc.IssueUserDownloadTicket(ctx, 1, 10)
	assertDisabled(err)

	_, err = svc.OpenUserDownload(ctx, 1, 10)
	assertDisabled(err)

	_, err = svc.OpenDownloadByTicket(ctx, "any.ticket")
	assertDisabled(err)
}
