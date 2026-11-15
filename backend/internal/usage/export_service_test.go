package usage

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"sync"
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

// newInMemoryExportRepo constructs an empty in-memory export repository fixture.
func newInMemoryExportRepo() *inMemoryExportRepo {
	return &inMemoryExportRepo{
		tasks: make(map[int64]*UsageExportTask),
	}
}

// CreateTask clones and stores task with a monotonically increasing ID.
func (r *inMemoryExportRepo) CreateTask(_ context.Context, task *UsageExportTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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

// ListTasks filters by scope and optional creator ordered newest ID first.
func (r *inMemoryExportRepo) ListTasks(
	_ context.Context,
	filter UsageExportListFilter,
	params pagination.PaginationParams,
) ([]UsageExportTask, *pagination.PaginationResult, error) {
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
	return matched, pagination.ResultFromTotal(total, params), nil
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

// ClaimNextPendingTask claims the lowest-ID pending task and transitions it to running.
func (r *inMemoryExportRepo) ClaimNextPendingTask(_ context.Context, _ int64) (*UsageExportTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var candidate *UsageExportTask
	for _, t := range r.tasks {
		if t.Status != UsageExportStatusPending {
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
