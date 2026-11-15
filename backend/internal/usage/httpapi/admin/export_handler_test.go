package admin

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

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
)

// export_handler_test.go verifies administrator usage-export HTTP routes, MCP
// operation catalog metadata, selector validation, active-task quotas, and
// streaming 38-column CSV downloads. In-memory repository, row-reader, and
// artifact-store doubles isolate HTTP and catalog behavior from PostgreSQL.

// Compile-time interface assertions ensure the administrator export test
// doubles satisfy UsageExportService contracts.
var (
	_ usage.UsageExportRepository    = (*adminMemoryExportRepo)(nil)
	_ usage.UsageExportRowReader     = (*adminMemoryExportRowReader)(nil)
	_ usage.UsageExportArtifactStore = (*adminMemoryExportStore)(nil)
)

// adminMemoryExportRepo stores persisted export tasks in memory for admin HTTP tests.
type adminMemoryExportRepo struct {
	mu     sync.Mutex                       // guards tasks and nextID
	nextID int64                            // monotonic primary-key counter
	tasks  map[int64]*usage.UsageExportTask // stored task clones keyed by task ID
}

// adminMemoryExportRowReader supplies configured usage rows to admin CSV export runs.
type adminMemoryExportRowReader struct {
	rows []usage.UsageLog // configured matching usage rows returned in one batch
}

// adminMemoryExportBatchReader streams a single slice of configured rows once.
type adminMemoryExportBatchReader struct {
	rows []usage.UsageLog // configured usage rows for one export execution
}

// adminMemoryExportStore keeps committed CSV artifacts in memory for download tests.
type adminMemoryExportStore struct {
	mu    sync.Mutex        // guards files map
	files map[string][]byte // committed CSV payloads keyed by store-relative path
}

// adminMemoryExportWriter buffers CSV bytes until Commit publishes them into store.
type adminMemoryExportWriter struct {
	store     *adminMemoryExportStore // owning in-memory artifact store
	key       string                  // target store-relative artifact key
	buf       bytes.Buffer            // buffered uncommitted CSV bytes
	committed bool                    // whether Commit already published buf
}

// adminExportTaskEnvelope decodes a single UsageExportTask response envelope.
type adminExportTaskEnvelope struct {
	Code int                 `json:"code"` // HTTP application status code
	Data dto.UsageExportTask `json:"data"` // decoded export task DTO
}

// adminExportTicketEnvelope decodes a signed browser download ticket envelope.
type adminExportTicketEnvelope struct {
	Code int                           `json:"code"` // HTTP application status code
	Data dto.UsageExportDownloadTicket `json:"data"` // decoded signed ticket payload
}

// newAdminMemoryExportRepo constructs an empty in-memory export repository fixture.
func newAdminMemoryExportRepo() *adminMemoryExportRepo {
	return &adminMemoryExportRepo{
		tasks: make(map[int64]*usage.UsageExportTask),
	}
}

// CreateTask atomically enforces maxActive under r.mu before storing a task copy.
func (r *adminMemoryExportRepo) CreateTask(_ context.Context, task *usage.UsageExportTask, maxActive int) error {
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
func (r *adminMemoryExportRepo) GetTask(_ context.Context, taskID int64) (*usage.UsageExportTask, error) {
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
func (r *adminMemoryExportRepo) ListTaskPage(
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
func (r *adminMemoryExportRepo) CountActiveTasks(_ context.Context, scope string, createdBy int64) (int64, error) {
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
func (r *adminMemoryExportRepo) ClaimNextPendingTask(_ context.Context, _ int64) (*usage.UsageExportTask, error) {
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
func (r *adminMemoryExportRepo) RequeueTask(_ context.Context, taskID int64) error {
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
func (r *adminMemoryExportRepo) GetTaskStatus(_ context.Context, taskID int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[taskID]
	if !ok {
		return "", usage.ErrExportTaskNotFound
	}
	return task.Status, nil
}

// UpdateTaskProgress updates totalRows and exportedRows on a running task.
func (r *adminMemoryExportRepo) UpdateTaskProgress(_ context.Context, taskID, totalRows, exportedRows int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[taskID]; ok && task.Status == usage.UsageExportStatusRunning {
		task.TotalRows = totalRows
		task.ExportedRows = exportedRows
	}
	return nil
}

// CancelTask transitions a pending or running task to canceled and records actor metadata.
func (r *adminMemoryExportRepo) CancelTask(_ context.Context, taskID, canceledBy int64) (bool, error) {
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
func (r *adminMemoryExportRepo) MarkTaskSucceeded(
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
func (r *adminMemoryExportRepo) MarkTaskFailed(_ context.Context, taskID, exportedRows int64, errorMsg string) error {
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
func (r *adminMemoryExportRepo) ListExpiredSucceededTasks(_ context.Context, now time.Time, limit int) ([]usage.UsageExportTask, error) {
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
func (r *adminMemoryExportRepo) MarkTaskExpired(_ context.Context, taskID int64) error {
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
func (r *adminMemoryExportRowReader) CountExportLogs(_ context.Context, _ usage.UsageExportFilters) (int64, error) {
	return int64(len(r.rows)), nil
}

// NewExportBatchReader returns a batch reader backed by the configured fixture slice.
func (r *adminMemoryExportRowReader) NewExportBatchReader(_ string) usage.UsageExportBatchReader {
	return &adminMemoryExportBatchReader{rows: r.rows}
}

// NextBatch returns all configured rows on the initial call and nil once cursor is set.
func (b *adminMemoryExportBatchReader) NextBatch(
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

// newAdminMemoryExportStore constructs an empty in-memory CSV artifact store.
func newAdminMemoryExportStore() *adminMemoryExportStore {
	return &adminMemoryExportStore{
		files: make(map[string][]byte),
	}
}

// CreateWriter opens a buffered writer targeting key inside the in-memory store.
func (s *adminMemoryExportStore) CreateWriter(key string) (usage.UsageExportArtifactWriter, error) {
	return &adminMemoryExportWriter{store: s, key: key}, nil
}

// Open returns a reader and byte length for key or ErrExportArtifactNotFound.
func (s *adminMemoryExportStore) Open(key string) (io.ReadCloser, int64, error) {
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
func (s *adminMemoryExportStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, key)
	return nil
}

// SweepStalePartFiles reports zero removed temporary files for the in-memory store.
func (s *adminMemoryExportStore) SweepStalePartFiles(_ time.Duration) (int, error) {
	return 0, nil
}

// Write appends p into the buffered uncommitted CSV payload.
func (w *adminMemoryExportWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

// Commit publishes the buffered CSV bytes into the parent store and returns their length.
func (w *adminMemoryExportWriter) Commit() (int64, error) {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	cp := append([]byte(nil), w.buf.Bytes()...)
	w.store.files[w.key] = cp
	w.committed = true
	return int64(len(cp)), nil
}

// Abort discards buffered bytes when Commit has not run.
func (w *adminMemoryExportWriter) Abort() error {
	if !w.committed {
		w.buf.Reset()
	}
	return nil
}

// setupAdminExportTestRouter wires administrator usage routes and public ticket
// download routes against exportSvc for end-to-end HTTP verification.
func setupAdminExportTestRouter(exportSvc *usage.UsageExportService, adminUserID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")

	userHandler := usagehttp.NewUsageHandler(nil, nil, nil, nil, timezone.NewCalendar(time.UTC))
	userHandler.SetExportService(exportSvc)
	usagehttp.RegisterPublicExportDownloadRoutes(v1, userHandler, nil)

	adminGroup := v1.Group("/admin")
	if adminUserID > 0 {
		adminGroup.Use(func(c *gin.Context) {
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: adminUserID})
			c.Next()
		})
	}
	adminHandler := NewUsageHandler(nil, nil, nil, nil, timezone.NewCalendar(time.UTC))
	adminHandler.SetExportService(exportSvc)
	RegisterUsageRoutes(adminGroup, adminHandler)
	return router
}

// TestAdminExportOperationsMountCleanlyInManagementCatalog mounts administrator
// usage routes and builds an adminapi Catalog from the live router. It verifies
// that all five export operations register valid schemas, idempotency, and file modes.
func TestAdminExportOperationsMountCleanlyInManagementCatalog(t *testing.T) {
	repo := newAdminMemoryExportRepo()
	store := newAdminMemoryExportStore()
	rowReader := &adminMemoryExportRowReader{}
	exportSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: true, MaxActivePerAdmin: 5},
	})
	router := setupAdminExportTestRouter(exportSvc, 1)

	catalog, err := adminapi.FromRoutes(router.Routes())
	require.NoError(t, err)

	listOp, ok := catalog.Get("usage.export_tasks.list")
	require.True(t, ok)
	require.True(t, listOp.ReadOnly)
	require.Equal(t, adminapi.ResponseJSON, listOp.Response)

	createOp, ok := catalog.Get("usage.export_tasks.create")
	require.True(t, ok)
	require.False(t, createOp.ReadOnly)
	require.True(t, createOp.Idempotent)

	cancelOp, ok := catalog.Get("usage.export_tasks.cancel.create")
	require.True(t, ok)
	require.False(t, cancelOp.ReadOnly)

	ticketOp, ok := catalog.Get("usage.export_tasks.download_ticket.create")
	require.True(t, ok)
	require.False(t, ticketOp.ReadOnly)

	downloadOp, ok := catalog.Get("usage.export_tasks.download.get")
	require.True(t, ok)
	require.True(t, downloadOp.ReadOnly)
	require.Equal(t, adminapi.ResponseFile, downloadOp.Response)
}

// TestAdminCreateExportTaskValidatesSelectorsAndEnforcesAdminActiveQuota submits
// administrator export requests with system request sources, model filter sources,
// invalid inputs, and active task quota exhaustion. It verifies 400 rejections,
// preserved admin-only filter fields, and 429 enforcement at MaxActivePerAdmin.
func TestAdminCreateExportTaskValidatesSelectorsAndEnforcesAdminActiveQuota(t *testing.T) {
	repo := newAdminMemoryExportRepo()
	store := newAdminMemoryExportStore()
	rowReader := &adminMemoryExportRowReader{}
	exportSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: true, MaxActivePerAdmin: 2},
	})
	router := setupAdminExportTestRouter(exportSvc, 1)

	// Reject invalid request_source.
	badSource := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks", strings.NewReader(`{
		"start_time":"2026-06-15T00:00:00Z",
		"end_time":"2026-06-15T01:00:00Z",
		"timezone":"UTC",
		"request_source":"bogus"
	}`))
	badSource.Header.Set("Content-Type", "application/json")
	badSourceRec := httptest.NewRecorder()
	router.ServeHTTP(badSourceRec, badSource)
	require.Equal(t, http.StatusBadRequest, badSourceRec.Code)

	// Reject invalid model_filter_source.
	badModelSource := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks", strings.NewReader(`{
		"start_time":"2026-06-15T00:00:00Z",
		"end_time":"2026-06-15T01:00:00Z",
		"timezone":"UTC",
		"model_filter_source":"bogus"
	}`))
	badModelSource.Header.Set("Content-Type", "application/json")
	badModelSourceRec := httptest.NewRecorder()
	router.ServeHTTP(badModelSourceRec, badModelSource)
	require.Equal(t, http.StatusBadRequest, badModelSourceRec.Code)

	// Valid administrator task preserves user_id, provider_id, request_source, and model_filter_source.
	validBody := `{
		"start_time":"2026-06-15T09:00:00",
		"end_time":"2026-06-15T10:00:00",
		"timezone":"Asia/Tokyo",
		"locale":"zh-CN",
		"request_source":"system",
		"user_id":42,
		"provider_id":8,
		"model":"claude-sonnet-4-5",
		"model_filter_source":"upstream"
	}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks", strings.NewReader(validBody))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)

	var env1 adminExportTaskEnvelope
	require.NoError(t, json.Unmarshal(rec1.Body.Bytes(), &env1))
	require.Equal(t, usage.UsageExportScopeAdmin, env1.Data.Scope)
	require.Equal(t, "Asia/Tokyo", env1.Data.Filters.Timezone)
	require.Equal(t, "zh", env1.Data.Filters.Locale)
	require.Equal(t, "system", env1.Data.Filters.RequestSource)
	require.Equal(t, "upstream", env1.Data.Filters.ModelFilterSource)
	require.NotNil(t, env1.Data.Filters.UserID)
	require.Equal(t, int64(42), *env1.Data.Filters.UserID)
	require.NotNil(t, env1.Data.Filters.ProviderID)
	require.Equal(t, int64(8), *env1.Data.Filters.ProviderID)
	require.Equal(t, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), env1.Data.Filters.StartTime.UTC())
	require.Equal(t, time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC), env1.Data.Filters.EndTime.UTC())

	// Second active admin task succeeds; third hits MaxActivePerAdmin (429).
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks", strings.NewReader(validBody))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks", strings.NewReader(validBody))
	req3.Header.Set("Content-Type", "application/json")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusTooManyRequests, rec3.Code)
	require.Contains(t, rec3.Body.String(), "USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED")
}

// TestAdminCancelTicketAndDirectDownloadStreamDeliverAdminCSV creates both a
// user-scoped task and an admin-scoped task before exercising cancel and CSV
// delivery routes. Cross-scope cancellation returns 404, while completed admin
// exports stream all 38 columns through signed-ticket and bearer endpoints.
func TestAdminCancelTicketAndDirectDownloadStreamDeliverAdminCSV(t *testing.T) {
	repo := newAdminMemoryExportRepo()
	store := newAdminMemoryExportStore()
	upstreamModel := "claude-sonnet-4-5-20250929"
	rowReader := &adminMemoryExportRowReader{
		rows: []usage.UsageLog{
			{
				ID:             501,
				UserID:         42,
				ProviderID:     8,
				RequestID:      "req-admin-501",
				Model:          "claude-sonnet-4-5",
				RequestedModel: "claude-sonnet-4-5",
				UpstreamModel:  &upstreamModel,
				InputTokens:    500,
				OutputTokens:   200,
				TotalCost:      0.05,
				ActualCost:     0.04,
				CreatedAt:      time.Date(2026, 6, 15, 2, 0, 0, 0, time.UTC),
			},
		},
	}
	exportSvc := usage.NewUsageExportService(repo, rowReader, store, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{
			Enabled:           true,
			RetentionHours:    24,
			MaxActivePerUser:  2,
			MaxActivePerAdmin: 5,
		},
	})
	router := setupAdminExportTestRouter(exportSvc, 1)

	// Seed a user-scope task (ID 1) and verify admin cancel returns 404 due to scope isolation.
	_, err := exportSvc.CreateUserTask(context.Background(), 42, usage.UsageExportFilters{
		StartTime: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC),
		Timezone:  "UTC",
	})
	require.NoError(t, err)

	crossCancel := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks/1/cancel", nil)
	crossCancelRec := httptest.NewRecorder()
	router.ServeHTTP(crossCancelRec, crossCancel)
	require.Equal(t, http.StatusNotFound, crossCancelRec.Code)

	// Create admin task (ID 2) and run background worker round synchronously.
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks", strings.NewReader(`{
		"start_time":"2026-06-15T00:00:00Z",
		"end_time":"2026-06-15T04:00:00Z",
		"timezone":"UTC",
		"locale":"en"
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusOK, createRec.Code)

	exportSvc.RunOnce()

	// Exchange admin download ticket and stream via public signed-ticket endpoint.
	ticketReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/export-tasks/2/download-ticket", nil)
	ticketRec := httptest.NewRecorder()
	router.ServeHTTP(ticketRec, ticketReq)
	require.Equal(t, http.StatusOK, ticketRec.Code)
	var ticketEnv adminExportTicketEnvelope
	require.NoError(t, json.Unmarshal(ticketRec.Body.Bytes(), &ticketEnv))

	pubReq := httptest.NewRequest(http.MethodGet, ticketEnv.Data.DownloadURL, nil)
	pubRec := httptest.NewRecorder()
	router.ServeHTTP(pubRec, pubReq)
	require.Equal(t, http.StatusOK, pubRec.Code)
	csvBody := pubRec.Body.String()
	require.True(t, strings.HasPrefix(csvBody, "\xEF\xBB\xBF"))
	require.Contains(t, csvBody, "Provider billed (USD)")
	require.Contains(t, csvBody, "claude-sonnet-4-5-20250929")

	// Stream directly via the admin download endpoint used by API and MCP callers.
	directReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/export-tasks/2/download", nil)
	directRec := httptest.NewRecorder()
	router.ServeHTTP(directRec, directReq)
	require.Equal(t, http.StatusOK, directRec.Code)
	require.Equal(t, csvBody, directRec.Body.String())
}

// TestAdminExportEndpointsReturnDisabledErrorWithNilStore mounts administrator
// export routes with Enabled=false and a nil local artifact store. Every admin
// export route returns 503 USAGE_EXPORT_DISABLED instead of 500.
func TestAdminExportEndpointsReturnDisabledErrorWithNilStore(t *testing.T) {
	repo := newAdminMemoryExportRepo()
	rowReader := &adminMemoryExportRowReader{}
	disabledSvc := usage.NewUsageExportService(repo, rowReader, nil, nil, nil, &usage.Options{
		UsageExport: usage.UsageExportConfig{Enabled: false},
	})
	router := setupAdminExportTestRouter(disabledSvc, 1)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/admin/usage/export-tasks", ""},
		{http.MethodPost, "/api/v1/admin/usage/export-tasks", `{"start_time":"2026-06-15T00:00:00Z","end_time":"2026-06-15T01:00:00Z","timezone":"UTC"}`},
		{http.MethodPost, "/api/v1/admin/usage/export-tasks/1/cancel", ""},
		{http.MethodPost, "/api/v1/admin/usage/export-tasks/1/download-ticket", ""},
		{http.MethodGet, "/api/v1/admin/usage/export-tasks/1/download", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, tc.path)
		require.Contains(t, rec.Body.String(), "USAGE_EXPORT_DISABLED", tc.path)
	}
}
