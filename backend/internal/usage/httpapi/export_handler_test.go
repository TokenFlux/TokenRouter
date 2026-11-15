package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/ports"
)

// export_handler_test.go verifies user usage-export HTTP routes, timestamp and
// timezone validation, active-task admission limits, cancellation, and signed
// CSV download delivery. In-memory repository, batch-reader, and artifact-store
// fixtures exercise UsageExportService end to end without external databases.

// Compile-time interface assertions ensure the in-memory export test doubles
// satisfy UsageExportService contracts.
var (
	_ usage.UsageExportRepository    = (*inMemoryExportRepo)(nil)
	_ usage.UsageExportRowReader     = (*inMemoryExportRowReader)(nil)
	_ usage.UsageExportArtifactStore = (*inMemoryExportStore)(nil)
)

// inMemoryExportRepo stores persisted export tasks in memory for HTTP handler tests.
type inMemoryExportRepo struct {
	mu     sync.Mutex                       // guards tasks and nextID across synchronous rounds
	nextID int64                            // monotonic primary-key counter
	tasks  map[int64]*usage.UsageExportTask // stored task clones keyed by task ID
}

// inMemoryExportRowReader supplies configured usage rows to background CSV export runs.
type inMemoryExportRowReader struct {
	rows []usage.UsageLog // configured matching usage rows returned in one batch
}

// inMemoryExportBatchReader streams a single slice of configured usage rows and
// stops on subsequent cursor calls.
type inMemoryExportBatchReader struct {
	rows []usage.UsageLog // configured usage rows for one export execution
}

// inMemoryExportStore keeps committed CSV bytes in memory for streaming download tests.
type inMemoryExportStore struct {
	mu    sync.Mutex        // guards files map
	files map[string][]byte // committed CSV payloads keyed by store-relative path
}

// inMemoryExportWriter buffers CSV bytes until Commit publishes them into store.
type inMemoryExportWriter struct {
	store     *inMemoryExportStore // owning in-memory artifact store
	key       string               // target store-relative artifact key
	buf       bytes.Buffer         // buffered uncommitted CSV bytes
	committed bool                 // whether Commit already published buf
}

// exportTaskEnvelope decodes a single UsageExportTask response envelope.
type exportTaskEnvelope struct {
	Code int                 `json:"code"` // HTTP application status code
	Data dto.UsageExportTask `json:"data"` // decoded export task DTO
}

// exportTaskListEnvelope decodes a paginated UsageExportTask list response envelope.
type exportTaskListEnvelope struct {
	Code int `json:"code"` // HTTP application status code
	Data struct {
		Items        []dto.UsageExportTask `json:"items"`         // decoded page of export tasks
		Total        int64                 `json:"total"`         // total matching task count
		ActiveCount  int64                 `json:"active_count"`  // scope-wide pending/running task count
		ActiveIDs    []int64               `json:"active_ids"`    // scope-wide pending/running task IDs
		WatchedItems []dto.UsageExportTask `json:"watched_items"` // off-page watched task snapshots
	} `json:"data"` // paginated payload wrapper
}

// exportTicketEnvelope decodes a signed browser download ticket response envelope.
type exportTicketEnvelope struct {
	Code int                           `json:"code"` // HTTP application status code
	Data dto.UsageExportDownloadTicket `json:"data"` // decoded signed ticket payload
}

// newInMemoryExportRepo constructs an empty in-memory export repository fixture.
func newInMemoryExportRepo() *inMemoryExportRepo {
	return &inMemoryExportRepo{
		tasks: make(map[int64]*usage.UsageExportTask),
	}
}

// CreateTask atomically enforces maxActive under r.mu before storing a task copy.
func (r *inMemoryExportRepo) CreateTask(_ context.Context, task *usage.UsageExportTask, maxActive int) error {
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
			if existing.Status == usage.UsageExportStatusPending || existing.Status == usage.UsageExportStatusRunning {
				active++
			}
		}
		if active >= maxActive {
			return usage.ErrExportActiveLimitExceeded
		}
	}
	r.nextID++
	now := time.Now().UTC()
	task.ID = r.nextID
	task.CreatedAt = now
	task.UpdatedAt = now
	cp := *task
	r.tasks[cp.ID] = &cp
	return nil
}

// GetTask returns a shallow copy of taskID or ErrExportTaskNotFound when missing.
func (r *inMemoryExportRepo) GetTask(_ context.Context, taskID int64) (*usage.UsageExportTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[taskID]
	if !ok {
		return nil, usage.ErrExportTaskNotFound
	}
	cp := *task
	return &cp, nil
}

// ListTaskPage reads paginated tasks, active IDs, and off-page watched tasks
// atomically under r.mu in descending ID order.
func (r *inMemoryExportRepo) ListTaskPage(
	_ context.Context,
	filter usage.UsageExportListFilter,
	params pagination.PaginationParams,
	watchIDs []int64,
) (*usage.UsageExportTaskListResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matched []usage.UsageExportTask
	for id := r.nextID; id >= 1; id-- {
		task, ok := r.tasks[id]
		if !ok || task.Scope != filter.Scope {
			continue
		}
		if filter.CreatedBy != nil && task.CreatedBy != *filter.CreatedBy {
			continue
		}
		matched = append(matched, *task)
	}
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
	var watchedItems []usage.UsageExportTask
	for i := range matched {
		if matched[i].Status == usage.UsageExportStatusPending || matched[i].Status == usage.UsageExportStatusRunning {
			activeIDs = append(activeIDs, matched[i].ID)
		}
		if _, ok := wanted[matched[i].ID]; ok {
			watchedItems = append(watchedItems, matched[i])
		}
	}
	return &usage.UsageExportTaskListResult{
		Items: pageItems,
		Pagination: &pagination.PaginationResult{
			Total:    total,
			Page:     params.Page,
			PageSize: params.PageSize,
		},
		ActiveCount:  int64(len(activeIDs)),
		ActiveIDs:    activeIDs,
		WatchedItems: watchedItems,
	}, nil
}

// CountActiveTasks counts pending and running tasks matching scope and createdBy.
func (r *inMemoryExportRepo) CountActiveTasks(_ context.Context, scope string, createdBy int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for _, task := range r.tasks {
		if task.Scope != scope {
			continue
		}
		if createdBy > 0 && task.CreatedBy != createdBy {
			continue
		}
		if task.Status == usage.UsageExportStatusPending || task.Status == usage.UsageExportStatusRunning {
			count++
		}
	}
	return count, nil
}

// ClaimNextPendingTask claims the oldest pending task and transitions it to running.
func (r *inMemoryExportRepo) ClaimNextPendingTask(_ context.Context, _ int64) (*usage.UsageExportTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := int64(1); id <= r.nextID; id++ {
		task, ok := r.tasks[id]
		if !ok || task.Status != usage.UsageExportStatusPending {
			continue
		}
		now := time.Now().UTC()
		task.Status = usage.UsageExportStatusRunning
		task.ExportedRows = 0
		task.StartedAt = &now
		task.UpdatedAt = now
		cp := *task
		return &cp, nil
	}
	return nil, nil
}

// RequeueTask resets an interrupted running task back to pending.
func (r *inMemoryExportRepo) RequeueTask(_ context.Context, taskID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[taskID]; ok && task.Status == usage.UsageExportStatusRunning {
		task.Status = usage.UsageExportStatusPending
		task.ExportedRows = 0
		task.StartedAt = nil
	}
	return nil
}

// GetTaskStatus returns the persisted status string or ErrExportTaskNotFound.
func (r *inMemoryExportRepo) GetTaskStatus(_ context.Context, taskID int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[taskID]
	if !ok {
		return "", usage.ErrExportTaskNotFound
	}
	return task.Status, nil
}

// UpdateTaskProgress updates totalRows and exportedRows on a running task.
func (r *inMemoryExportRepo) UpdateTaskProgress(_ context.Context, taskID, totalRows, exportedRows int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[taskID]; ok && task.Status == usage.UsageExportStatusRunning {
		task.TotalRows = totalRows
		task.ExportedRows = exportedRows
	}
	return nil
}

// CancelTask transitions a pending or running task to canceled and records actor metadata.
func (r *inMemoryExportRepo) CancelTask(_ context.Context, taskID, canceledBy int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[taskID]
	if !ok || (task.Status != usage.UsageExportStatusPending && task.Status != usage.UsageExportStatusRunning) {
		return false, nil
	}
	now := time.Now().UTC()
	actor := canceledBy
	task.Status = usage.UsageExportStatusCanceled
	task.CanceledBy = &actor
	task.CanceledAt = &now
	task.FinishedAt = &now
	task.UpdatedAt = now
	return true, nil
}

// MarkTaskSucceeded transitions a non-canceled task to succeeded and stores artifact metadata.
func (r *inMemoryExportRepo) MarkTaskSucceeded(
	_ context.Context,
	taskID, exportedRows int64,
	fileName, filePath string,
	fileSizeBytes int64,
	expiresAt time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[taskID]
	if !ok || task.Status == usage.UsageExportStatusCanceled {
		return false, nil
	}
	now := time.Now().UTC()
	fn := fileName
	fp := filePath
	exp := expiresAt
	task.Status = usage.UsageExportStatusSucceeded
	task.ExportedRows = exportedRows
	task.FileName = &fn
	task.FilePath = &fp
	task.FileSizeBytes = fileSizeBytes
	task.FinishedAt = &now
	task.ExpiresAt = &exp
	task.UpdatedAt = now
	return true, nil
}

// MarkTaskFailed transitions a non-canceled task to failed with diagnostic text.
func (r *inMemoryExportRepo) MarkTaskFailed(_ context.Context, taskID, exportedRows int64, errorMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[taskID]; ok && task.Status != usage.UsageExportStatusCanceled {
		msg := errorMsg
		now := time.Now().UTC()
		task.Status = usage.UsageExportStatusFailed
		task.ExportedRows = exportedRows
		task.ErrorMsg = &msg
		task.FinishedAt = &now
		task.UpdatedAt = now
	}
	return nil
}

// ListExpiredSucceededTasks returns succeeded tasks whose ExpiresAt is at or before now.
func (r *inMemoryExportRepo) ListExpiredSucceededTasks(_ context.Context, now time.Time, limit int) ([]usage.UsageExportTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []usage.UsageExportTask
	for _, task := range r.tasks {
		if task.Status == usage.UsageExportStatusSucceeded && task.ExpiresAt != nil && !now.Before(*task.ExpiresAt) {
			out = append(out, *task)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// MarkTaskExpired marks a succeeded task as expired and clears its FilePath.
func (r *inMemoryExportRepo) MarkTaskExpired(_ context.Context, taskID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[taskID]; ok && task.Status == usage.UsageExportStatusSucceeded {
		task.Status = usage.UsageExportStatusExpired
		task.FilePath = nil
		task.UpdatedAt = time.Now().UTC()
	}
	return nil
}

// CountExportLogs returns the number of configured usage rows in the fixture.
func (r *inMemoryExportRowReader) CountExportLogs(_ context.Context, _ usage.UsageExportFilters) (int64, error) {
	return int64(len(r.rows)), nil
}

// NewExportBatchReader returns a batch reader backed by the configured fixture slice.
func (r *inMemoryExportRowReader) NewExportBatchReader(_ string) usage.UsageExportBatchReader {
	return &inMemoryExportBatchReader{rows: r.rows}
}

// NextBatch returns all configured rows on the initial call and nil once cursor is set.
func (b *inMemoryExportBatchReader) NextBatch(
	_ context.Context,
	_ usage.UsageExportFilters,
	cursor *usage.UsageExportCursor,
	_ int,
) ([]usage.UsageLog, error) {
	if cursor != nil {
		return nil, nil
	}
	return b.rows, nil
}

// newInMemoryExportStore constructs an empty in-memory CSV artifact store.
func newInMemoryExportStore() *inMemoryExportStore {
	return &inMemoryExportStore{
		files: make(map[string][]byte),
	}
}

// CreateWriter opens a buffered writer targeting key inside the in-memory store.
func (s *inMemoryExportStore) CreateWriter(key string) (usage.UsageExportArtifactWriter, error) {
	return &inMemoryExportWriter{store: s, key: key}, nil
}

// Open returns a reader and byte length for key or ErrExportArtifactNotFound.
func (s *inMemoryExportStore) Open(key string) (io.ReadCloser, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[key]
	if !ok {
		return nil, 0, usage.ErrExportArtifactNotFound
	}
	cp := append([]byte(nil), data...)
	return io.NopCloser(bytes.NewReader(cp)), int64(len(cp)), nil
}

// Delete removes any stored CSV bytes for key.
func (s *inMemoryExportStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, key)
	return nil
}

// SweepStalePartFiles reports zero removed temporary files for the in-memory store.
func (s *inMemoryExportStore) SweepStalePartFiles(_ time.Duration) (int, error) {
	return 0, nil
}

// Write appends p into the buffered uncommitted CSV payload.
func (w *inMemoryExportWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

// Commit publishes the buffered CSV bytes into the parent store and returns their length.
func (w *inMemoryExportWriter) Commit() (int64, error) {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	cp := append([]byte(nil), w.buf.Bytes()...)
	w.store.files[w.key] = cp
	w.committed = true
	return int64(len(cp)), nil
}

// Abort discards buffered bytes when Commit has not run.
func (w *inMemoryExportWriter) Abort() error {
	if !w.committed {
		w.buf.Reset()
	}
	return nil
}

// newUserExportTestRouter wires UsageHandler, UsageExportService, authenticated
// user routes, and public signed-ticket download routes for HTTP tests.
func newUserExportTestRouter(
	exportSvc *usage.UsageExportService,
	usageRepo usage.UsageLogRepository,
	apiKeyRepo ports.KeyReader,
	subjectHeader string,
	heavy gin.HandlerFunc,
	publicLimiter gin.HandlerFunc,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	var usageSvc *usage.UsageService
	if usageRepo != nil {
		usageSvc = usage.NewUsageService(usageRepo)
	}
	handler := NewUsageHandler(usageSvc, apiKeyRepo, nil, nil, timezone.NewCalendar(time.UTC))
	handler.SetExportService(exportSvc)

	if heavy == nil {
		heavy = func(c *gin.Context) { c.Next() }
	}
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterPublicExportDownloadRoutes(v1, handler, publicLimiter)

	authenticated := v1.Group("")
	authenticated.Use(func(c *gin.Context) {
		rawUser := strings.TrimSpace(c.GetHeader(subjectHeader))
		if rawUser != "" {
			var uid int64
			for _, ch := range rawUser {
				if ch >= '0' && ch <= '9' {
					uid = uid*10 + int64(ch-'0')
				}
			}
			if uid > 0 {
				c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: uid})
			}
		}
		c.Next()
	})
	RegisterUserRoutes(authenticated, handler, heavy)
	return router
}

// TestExportRoutesBypassHeavyRateLimiterWhileUsageListUsesHeavy mounts both
// user usage and export routes behind separate middleware counters. It verifies
// that polling export tasks never triggers the heavy-query limiter while listing
// usage logs triggers heavy and ticket downloads trigger the public IP limiter.
func TestExportRoutesBypassHeavyRateLimiterWhileUsageListUsesHeavy(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryExportStore()
	rowReader := &inMemoryExportRowReader{}
	exportSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: true, MaxActivePerUser: 2},
	})
	usageRepo := &userUsageRepoCapture{listRows: []usage.UsageLog{}}

	heavyCalls := 0
	heavy := func(c *gin.Context) {
		heavyCalls++
		c.Next()
	}
	publicCalls := 0
	publicLimiter := func(c *gin.Context) {
		publicCalls++
		c.Next()
	}
	router := newUserExportTestRouter(exportSvc, usageRepo, nil, "X-Test-User", heavy, publicLimiter)

	listTasksReq := httptest.NewRequest(http.MethodGet, "/api/v1/usage/export-tasks", nil)
	listTasksReq.Header.Set("X-Test-User", "42")
	listTasksRec := httptest.NewRecorder()
	router.ServeHTTP(listTasksRec, listTasksReq)
	require.Equal(t, http.StatusOK, listTasksRec.Code)
	require.Equal(t, 0, heavyCalls)

	listUsageReq := httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil)
	listUsageReq.Header.Set("X-Test-User", "42")
	listUsageRec := httptest.NewRecorder()
	router.ServeHTTP(listUsageRec, listUsageReq)
	require.Equal(t, http.StatusOK, listUsageRec.Code)
	require.Equal(t, 1, heavyCalls)

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/v1/usage/export-downloads/invalid.ticket", nil)
	downloadRec := httptest.NewRecorder()
	router.ServeHTTP(downloadRec, downloadReq)
	require.Equal(t, http.StatusUnauthorized, downloadRec.Code)
	require.Equal(t, 1, publicCalls)
	require.Equal(t, 1, heavyCalls)
}

// TestCreateExportTaskParsesLocalTimestampWithTimezoneAndEnforcesActiveQuota
// submits user export requests with local DST timestamps, API key selectors,
// invalid timezones, and active quota exhaustion. It verifies exact UTC bound
// conversion, key existence validation, and HTTP 429 enforcement at two tasks.
func TestCreateExportTaskParsesLocalTimestampWithTimezoneAndEnforcesActiveQuota(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryExportStore()
	rowReader := &inMemoryExportRowReader{}
	exportSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: true, MaxActivePerUser: 2},
	})
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{
		keys: map[int64]*ports.KeyReference{
			7: {ID: 7, UserID: 42},
		},
	}
	router := newUserExportTestRouter(exportSvc, nil, apiKeyRepo, "X-Test-User", nil, nil)

	// Invalid IANA timezone must be rejected with 400.
	badTZReq := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(`{
		"start_time":"2026-06-15T00:00:00",
		"end_time":"2026-06-15T01:00:00",
		"timezone":"Mars/Olympus"
	}`))
	badTZReq.Header.Set("Content-Type", "application/json")
	badTZReq.Header.Set("X-Test-User", "42")
	badTZRec := httptest.NewRecorder()
	router.ServeHTTP(badTZRec, badTZReq)
	require.Equal(t, http.StatusBadRequest, badTZRec.Code)

	// Nonexistent API key must return 404 from apiKeyService.GetByID.
	missingKeyReq := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(`{
		"start_time":"2026-06-15T00:00:00",
		"end_time":"2026-06-15T01:00:00",
		"timezone":"America/New_York",
		"api_key_id":999
	}`))
	missingKeyReq.Header.Set("Content-Type", "application/json")
	missingKeyReq.Header.Set("X-Test-User", "42")
	missingKeyRec := httptest.NewRecorder()
	router.ServeHTTP(missingKeyRec, missingKeyReq)
	require.ErrorIs(t, apikey.ErrAPIKeyNotFound, apikey.ErrAPIKeyNotFound)
	require.Equal(t, http.StatusNotFound, missingKeyRec.Code)

	// Valid local timestamp in America/New_York during EDT (UTC-4) converts 00:00:00 -> 04:00:00Z.
	body := `{
		"start_time":"2026-06-15T00:00:00",
		"end_time":"2026-06-15T01:30:45",
		"timezone":"America/New_York",
		"locale":"en-US",
		"api_key_id":7,
		"request_type":"ws_v2",
		"stream":true,
		"billing_mode":"token"
	}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Test-User", "42")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)

	var env1 exportTaskEnvelope
	require.NoError(t, json.Unmarshal(rec1.Body.Bytes(), &env1))
	require.Equal(t, usage.UsageExportScopeUser, env1.Data.Scope)
	require.Equal(t, usage.UsageExportStatusPending, env1.Data.Status)
	require.Equal(t, "America/New_York", env1.Data.Filters.Timezone)
	require.Equal(t, "en", env1.Data.Filters.Locale)
	require.Equal(t, time.Date(2026, 6, 15, 4, 0, 0, 0, time.UTC), env1.Data.Filters.StartTime.UTC())
	require.Equal(t, time.Date(2026, 6, 15, 5, 30, 45, 0, time.UTC), env1.Data.Filters.EndTime.UTC())
	require.Nil(t, env1.Data.Filters.UserID)
	require.Nil(t, env1.Data.Filters.Stream)
	require.NotNil(t, env1.Data.Filters.RequestType)
	require.Equal(t, "ws_v2", *env1.Data.Filters.RequestType)

	// Second active task for user 42 succeeds.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Test-User", "42")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	// Third active task for user 42 hits the per-user active quota (429).
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(body))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Test-User", "42")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusTooManyRequests, rec3.Code)
	require.Contains(t, rec3.Body.String(), "USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED")
}

// TestCancelExportTaskEnforcesOwnershipAndTerminalConflict creates a user export
// task and exercises foreign-user cancellation, owner cancellation, and terminal
// conflict after completion. It verifies 404 isolation, 200 cancellation, and 409 on succeeded tasks.
func TestCancelExportTaskEnforcesOwnershipAndTerminalConflict(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryExportStore()
	rowReader := &inMemoryExportRowReader{}
	exportSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: true, MaxActivePerUser: 5},
	})
	router := newUserExportTestRouter(exportSvc, nil, nil, "X-Test-User", nil, nil)

	createBody := `{"start_time":"2026-06-15T00:00:00Z","end_time":"2026-06-15T02:00:00Z","timezone":"UTC"}`
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(createBody))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("X-Test-User", "42")
	recCreate := httptest.NewRecorder()
	router.ServeHTTP(recCreate, reqCreate)
	require.Equal(t, http.StatusOK, recCreate.Code)

	// Foreign user 99 cannot see or cancel user 42's task -> 404.
	foreignCancel := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks/1/cancel", nil)
	foreignCancel.Header.Set("X-Test-User", "99")
	foreignRec := httptest.NewRecorder()
	router.ServeHTTP(foreignRec, foreignCancel)
	require.Equal(t, http.StatusNotFound, foreignRec.Code)

	// Owner user 42 cancels task 1 -> 200 with status=canceled.
	ownerCancel := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks/1/cancel", nil)
	ownerCancel.Header.Set("X-Test-User", "42")
	ownerRec := httptest.NewRecorder()
	router.ServeHTTP(ownerRec, ownerCancel)
	require.Equal(t, http.StatusOK, ownerRec.Code)
	var canceledEnv exportTaskEnvelope
	require.NoError(t, json.Unmarshal(ownerRec.Body.Bytes(), &canceledEnv))
	require.Equal(t, usage.UsageExportStatusCanceled, canceledEnv.Data.Status)

	// Create a second task, run it to completion, then verify canceling returns 409.
	reqCreate2 := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(createBody))
	reqCreate2.Header.Set("Content-Type", "application/json")
	reqCreate2.Header.Set("X-Test-User", "42")
	recCreate2 := httptest.NewRecorder()
	router.ServeHTTP(recCreate2, reqCreate2)
	require.Equal(t, http.StatusOK, recCreate2.Code)

	exportSvc.RunOnce()

	terminalCancel := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks/2/cancel", nil)
	terminalCancel.Header.Set("X-Test-User", "42")
	terminalRec := httptest.NewRecorder()
	router.ServeHTTP(terminalRec, terminalCancel)
	require.Equal(t, http.StatusConflict, terminalRec.Code)
}

// TestExportDownloadTicketAndDirectBearerStreamDeliverCSVAndHandleExpiry runs a
// user export task to completion and exercises both signed-ticket and direct
// bearer CSV downloads. Both endpoints stream formula-safe UTF-8 BOM CSV bytes
// before retention expires and return HTTP 410 afterward.
func TestExportDownloadTicketAndDirectBearerStreamDeliverCSVAndHandleExpiry(t *testing.T) {
	repo := newInMemoryExportRepo()
	store := newInMemoryExportStore()
	rowReader := &inMemoryExportRowReader{
		rows: []usage.UsageLog{
			{
				ID:             101,
				UserID:         42,
				RequestID:      "req-csv-101",
				Model:          "gpt-5",
				RequestedModel: "=cmd|' /C calc'!A0",
				InputTokens:    120,
				OutputTokens:   80,
				ActualCost:     0.0125,
				CreatedAt:      time.Date(2026, 6, 15, 1, 2, 3, 0, time.UTC),
			},
		},
	}
	exportSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{
			Enabled:          true,
			RetentionHours:   24,
			MaxActivePerUser: 2,
		},
	})
	router := newUserExportTestRouter(exportSvc, nil, nil, "X-Test-User", nil, nil)

	createBody := `{"start_time":"2026-06-15T08:00:00","end_time":"2026-06-15T10:00:00","timezone":"Asia/Shanghai","locale":"en"}`
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks", strings.NewReader(createBody))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("X-Test-User", "42")
	recCreate := httptest.NewRecorder()
	router.ServeHTTP(recCreate, reqCreate)
	require.Equal(t, http.StatusOK, recCreate.Code)

	exportSvc.RunOnce()

	// Verify list endpoint surfaces succeeded status, row progress, and filename.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/usage/export-tasks", nil)
	listReq.Header.Set("X-Test-User", "42")
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	var listEnv exportTaskListEnvelope
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &listEnv))
	require.Len(t, listEnv.Data.Items, 1)
	require.Equal(t, usage.UsageExportStatusSucceeded, listEnv.Data.Items[0].Status)
	require.Equal(t, int64(1), listEnv.Data.Items[0].ExportedRows)
	require.NotNil(t, listEnv.Data.Items[0].FileName)

	// Exchange authenticated request for a signed browser download ticket.
	ticketReq := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks/1/download-ticket", nil)
	ticketReq.Header.Set("X-Test-User", "42")
	ticketRec := httptest.NewRecorder()
	router.ServeHTTP(ticketRec, ticketReq)
	require.Equal(t, http.StatusOK, ticketRec.Code)
	var ticketEnv exportTicketEnvelope
	require.NoError(t, json.Unmarshal(ticketRec.Body.Bytes(), &ticketEnv))
	require.NotEmpty(t, ticketEnv.Data.Ticket)
	require.True(t, strings.HasPrefix(ticketEnv.Data.DownloadURL, "/api/v1/usage/export-downloads/"))

	// Stream CSV via the public signed-ticket URL without authentication headers.
	pubReq := httptest.NewRequest(http.MethodGet, ticketEnv.Data.DownloadURL, nil)
	pubRec := httptest.NewRecorder()
	router.ServeHTTP(pubRec, pubReq)
	require.Equal(t, http.StatusOK, pubRec.Code)
	require.Equal(t, "text/csv; charset=utf-8", pubRec.Header().Get("Content-Type"))
	require.Equal(t, "no-store", pubRec.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", pubRec.Header().Get("X-Content-Type-Options"))
	require.Contains(t, pubRec.Header().Get("Content-Disposition"), "attachment; filename=")
	csvBody := pubRec.Body.String()
	require.True(t, strings.HasPrefix(csvBody, "\xEF\xBB\xBF"))
	require.Contains(t, csvBody, "2026-06-15T09:02:03+08:00")
	require.Contains(t, csvBody, "'=cmd|' /C calc'!A0")

	// Stream CSV directly via the bearer-authenticated download endpoint.
	directReq := httptest.NewRequest(http.MethodGet, "/api/v1/usage/export-tasks/1/download", nil)
	directReq.Header.Set("X-Test-User", "42")
	directRec := httptest.NewRecorder()
	router.ServeHTTP(directRec, directReq)
	require.Equal(t, http.StatusOK, directRec.Code)
	require.Equal(t, csvBody, directRec.Body.String())

	// Remove artifact from store to simulate retention expiration and verify HTTP 410 Gone.
	repo.mu.Lock()
	past := time.Now().UTC().Add(-time.Minute)
	repo.tasks[1].ExpiresAt = &past
	repo.mu.Unlock()

	expiredTicketReq := httptest.NewRequest(http.MethodPost, "/api/v1/usage/export-tasks/1/download-ticket", nil)
	expiredTicketReq.Header.Set("X-Test-User", "42")
	expiredTicketRec := httptest.NewRecorder()
	router.ServeHTTP(expiredTicketRec, expiredTicketReq)
	require.Equal(t, http.StatusGone, expiredTicketRec.Code)
	require.Contains(t, expiredTicketRec.Body.String(), "USAGE_EXPORT_EXPIRED")

	expiredStreamReq := httptest.NewRequest(http.MethodGet, ticketEnv.Data.DownloadURL, nil)
	expiredStreamRec := httptest.NewRecorder()
	router.ServeHTTP(expiredStreamRec, expiredStreamReq)
	require.Equal(t, http.StatusGone, expiredStreamRec.Code)
	require.Contains(t, expiredStreamRec.Body.String(), "USAGE_EXPORT_EXPIRED")
}

// TestUserExportEndpointsReturnDisabledErrorWithNilStoreAndTrackOffPageTasks
// verifies both disabled export routing (with nil artifact store) and off-page
// active task tracking via active_ids and watch_ids on page one queries.
func TestUserExportEndpointsReturnDisabledErrorWithNilStoreAndTrackOffPageTasks(t *testing.T) {
	repo := newInMemoryExportRepo()
	rowReader := &inMemoryExportRowReader{}
	usageRepo := &userUsageRepoCapture{listRows: []usage.UsageLog{}}

	disabledSvc := usage.NewUsageExportService(repo, rowReader, nil, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: false},
	})
	disabledRouter := newUserExportTestRouter(disabledSvc, usageRepo, nil, "X-Test-User", nil, nil)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/usage/export-tasks", ""},
		{http.MethodPost, "/api/v1/usage/export-tasks", `{"start_time":"2026-06-15T00:00:00Z","end_time":"2026-06-15T01:00:00Z","timezone":"UTC"}`},
		{http.MethodPost, "/api/v1/usage/export-tasks/1/cancel", ""},
		{http.MethodPost, "/api/v1/usage/export-tasks/1/download-ticket", ""},
		{http.MethodGet, "/api/v1/usage/export-tasks/1/download", ""},
		{http.MethodGet, "/api/v1/usage/export-downloads/any.ticket", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("X-Test-User", "42")
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		disabledRouter.ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, tc.path)
		require.Contains(t, rec.Body.String(), "USAGE_EXPORT_DISABLED", tc.path)
	}

	// Enable exports and seed task #1 (running) pushed to page 2 by tasks #2 and #3 (succeeded).
	store := newInMemoryExportStore()
	enabledSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: true, MaxActivePerUser: 5},
	})
	enabledRouter := newUserExportTestRouter(enabledSvc, usageRepo, nil, "X-Test-User", nil, nil)

	start := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	filters := usage.UsageExportFilters{StartTime: start, EndTime: start.Add(time.Hour), Timezone: "UTC"}
	require.NoError(t, repo.CreateTask(context.Background(), &usage.UsageExportTask{
		Scope: usage.UsageExportScopeUser, Status: usage.UsageExportStatusRunning, CreatedBy: 42, Filters: filters,
	}, 0))
	require.NoError(t, repo.CreateTask(context.Background(), &usage.UsageExportTask{
		Scope: usage.UsageExportScopeUser, Status: usage.UsageExportStatusSucceeded, CreatedBy: 42, Filters: filters,
	}, 0))
	require.NoError(t, repo.CreateTask(context.Background(), &usage.UsageExportTask{
		Scope: usage.UsageExportScopeUser, Status: usage.UsageExportStatusSucceeded, CreatedBy: 42, Filters: filters,
	}, 0))

	page1Req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/export-tasks?page=1&page_size=2", nil)
	page1Req.Header.Set("X-Test-User", "42")
	page1Rec := httptest.NewRecorder()
	enabledRouter.ServeHTTP(page1Rec, page1Req)
	require.Equal(t, http.StatusOK, page1Rec.Code)

	var page1Env exportTaskListEnvelope
	require.NoError(t, json.Unmarshal(page1Rec.Body.Bytes(), &page1Env))
	require.Len(t, page1Env.Data.Items, 2)
	require.Equal(t, int64(1), page1Env.Data.ActiveCount)
	require.Equal(t, []int64{1}, page1Env.Data.ActiveIDs)

	// Transition off-page task #1 to succeeded and verify watch_ids=1 returns its settled row.
	_, err := repo.MarkTaskSucceeded(context.Background(), 1, 50, "out.csv", "user/out.csv", 128, start.Add(24*time.Hour))
	require.NoError(t, err)

	watchReq := httptest.NewRequest(http.MethodGet, "/api/v1/usage/export-tasks?page=1&page_size=2&watch_ids=1", nil)
	watchReq.Header.Set("X-Test-User", "42")
	watchRec := httptest.NewRecorder()
	enabledRouter.ServeHTTP(watchRec, watchReq)
	require.Equal(t, http.StatusOK, watchRec.Code)

	var watchEnv exportTaskListEnvelope
	require.NoError(t, json.Unmarshal(watchRec.Body.Bytes(), &watchEnv))
	require.Zero(t, watchEnv.Data.ActiveCount)
	require.Empty(t, watchEnv.Data.ActiveIDs)
	require.Len(t, watchEnv.Data.WatchedItems, 1)
	require.Equal(t, int64(1), watchEnv.Data.WatchedItems[0].ID)
	require.Equal(t, usage.UsageExportStatusSucceeded, watchEnv.Data.WatchedItems[0].Status)
}
