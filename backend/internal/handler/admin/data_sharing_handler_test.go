package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminDataShareSettingRepoStub struct {
	values map[string]string
}

func (s *adminDataShareSettingRepoStub) Get(context.Context, string) (*service.Setting, error) {
	panic("unexpected Get call")
}

func (s *adminDataShareSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}

func (s *adminDataShareSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}

func (s *adminDataShareSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (s *adminDataShareSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	for key, value := range settings {
		s.values[key] = value
	}
	return nil
}

func (s *adminDataShareSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *adminDataShareSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

type adminDataShareExportArtifactRepoStub struct {
	mu     sync.Mutex
	nextID int64
	items  map[int64]*service.DataShareExportArtifact
}

func (r *adminDataShareExportArtifactRepoStub) Create(_ context.Context, artifact *service.DataShareExportArtifact) (*service.DataShareExportArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = map[int64]*service.DataShareExportArtifact{}
	}
	r.nextID++
	item := *artifact
	item.ID = r.nextID
	item.CreatedAt = time.Now()
	item.UpdatedAt = item.CreatedAt
	r.items[item.ID] = &item
	return &item, nil
}

func (r *adminDataShareExportArtifactRepoStub) Get(_ context.Context, id int64) (*service.DataShareExportArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return nil, service.ErrDataShareExportArtifactNotFound
	}
	out := *item
	return &out, nil
}

func (r *adminDataShareExportArtifactRepoStub) List(context.Context, pagination.PaginationParams) ([]service.DataShareExportArtifact, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

func (r *adminDataShareExportArtifactRepoStub) MarkRunning(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item := r.items[id]; item != nil {
		item.Status = service.DataShareExportArtifactStatusRunning
		return nil
	}
	return service.ErrDataShareExportArtifactNotFound
}

func (r *adminDataShareExportArtifactRepoStub) MarkCompleted(context.Context, int64, string, int64, int64, string) error {
	return nil
}

func (r *adminDataShareExportArtifactRepoStub) MarkFailed(_ context.Context, id int64, errorMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item := r.items[id]; item != nil {
		item.Status = service.DataShareExportArtifactStatusFailed
		item.ErrorMessage = errorMessage
		return nil
	}
	return service.ErrDataShareExportArtifactNotFound
}

func (r *adminDataShareExportArtifactRepoStub) MarkRemoteUploading(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item := r.items[id]; item != nil {
		item.RemoteStatus = service.DataShareExportArtifactRemoteStatusUploading
		item.RemoteErrorMessage = ""
		return nil
	}
	return service.ErrDataShareExportArtifactNotFound
}

func (r *adminDataShareExportArtifactRepoStub) MarkRemoteUploaded(_ context.Context, id int64, bucket string, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item := r.items[id]; item != nil {
		now := time.Now()
		item.RemoteStatus = service.DataShareExportArtifactRemoteStatusUploaded
		item.RemoteBucket = bucket
		item.RemoteKey = key
		item.RemoteUploadedAt = &now
		item.RemoteErrorMessage = ""
		return nil
	}
	return service.ErrDataShareExportArtifactNotFound
}

func (r *adminDataShareExportArtifactRepoStub) MarkRemoteUploadFailed(_ context.Context, id int64, errorMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item := r.items[id]; item != nil {
		if item.RemoteKey != "" {
			item.RemoteStatus = service.DataShareExportArtifactRemoteStatusUploaded
		} else {
			item.RemoteStatus = service.DataShareExportArtifactRemoteStatusFailed
		}
		item.RemoteErrorMessage = errorMessage
		return nil
	}
	return service.ErrDataShareExportArtifactNotFound
}

func (r *adminDataShareExportArtifactRepoStub) MarkInterruptedFailed(context.Context, string) (int64, error) {
	return 0, nil
}

func (r *adminDataShareExportArtifactRepoStub) MarkInterruptedRemoteUploads(context.Context, string) (int64, error) {
	return 0, nil
}

func (r *adminDataShareExportArtifactRepoStub) MarkDeleted(context.Context, int64) (string, error) {
	return "", nil
}

func TestParseAdminDataShareFiltersIncludesRequestPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/data-sharing?request_path=/v1/responses&user_agent=claude-code&model=claude-sonnet-4-5&quality_status=non_invalid&search=/v1&user_name=alice&api_key_name=主Key&group_name=共享分组", nil)

	filters, ok := parseAdminDataShareFilters(c)
	require.True(t, ok)
	require.Equal(t, "/v1/responses", filters.RequestPath)
	require.Equal(t, "claude-code", filters.UserAgent)
	require.Equal(t, "claude-sonnet-4-5", filters.Model)
	require.Equal(t, service.DataShareQualityFilterNonInvalid, filters.QualityStatus)
	require.Equal(t, "/v1", filters.Search)
	require.Equal(t, "alice", filters.UserName)
	require.Equal(t, "主Key", filters.APIKeyName)
	require.Equal(t, "共享分组", filters.GroupName)
}

func TestAdminDataShareSessionToResponseIncludesRequestPathAndUserAgent(t *testing.T) {
	resp := adminDataShareSessionToResponse(&service.DataShareSession{
		ID:              1,
		SessionID:       "sess_1",
		RequestPath:     "/v1/messages",
		UserAgent:       "claude-code/2.0",
		PayloadEncoding: "zstd",
		PayloadBytes:    5678,
	}, false)

	require.Equal(t, "/v1/messages", resp.RequestPath)
	require.Equal(t, "claude-code/2.0", resp.UserAgent)
	require.Equal(t, "zstd", resp.PayloadEncoding)
	require.Equal(t, int64(5678), resp.PayloadBytes)
}

func TestDataShareSkipRulesHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &adminDataShareSettingRepoStub{values: map[string]string{}}
	h := NewDataSharingHandler(service.NewDataSharingService(nil, repo))

	getRecorder := httptest.NewRecorder()
	getCtx, _ := gin.CreateTestContext(getRecorder)
	getCtx.Request = httptest.NewRequest(http.MethodGet, "/admin/data-sharing/skip-rules", nil)
	h.GetSkipRules(getCtx)
	require.Equal(t, http.StatusOK, getRecorder.Code)

	var getEnvelope struct {
		Code int                                `json:"code"`
		Data []service.DataShareCaptureSkipRule `json:"data"`
	}
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &getEnvelope))
	require.NotEmpty(t, getEnvelope.Data)

	body := bytes.NewBufferString(`{"rules":[{"id":"custom","name":"自定义","enabled":true,"request_paths":["v1/responses"],"field_scopes":["input"],"patterns":["Warmup"],"match_mode":"equals"}]}`)
	putRecorder := httptest.NewRecorder()
	putCtx, _ := gin.CreateTestContext(putRecorder)
	putCtx.Request = httptest.NewRequest(http.MethodPut, "/admin/data-sharing/skip-rules", body)
	putCtx.Request.Header.Set("Content-Type", "application/json")
	h.UpdateSkipRules(putCtx)
	require.Equal(t, http.StatusOK, putRecorder.Code)
	require.NotEmpty(t, repo.values[service.SettingKeyDataSharingCaptureSkipRules])
}

func TestCreateSessionExportArtifactReturnsJSONFilename(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &adminDataShareSettingRepoStub{values: map[string]string{}}
	svc := service.NewDataSharingService(nil, repo)
	artifactRepo := &adminDataShareExportArtifactRepoStub{}
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	h := NewDataSharingHandler(svc)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/data-sharing/sessions/7/export-artifacts", nil)
	h.CreateSessionExportArtifact(c)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Filename string `json:"filename"`
			Encoding string `json:"encoding"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, "admin-data-sharing-session-7.json", envelope.Data.Filename)
	require.Equal(t, string(service.DataShareExportEncodingJSON), envelope.Data.Encoding)
	require.Eventually(t, func() bool {
		item, err := artifactRepo.Get(context.Background(), 1)
		return err == nil && item.Status == service.DataShareExportArtifactStatusFailed
	}, time.Second, 10*time.Millisecond)
}

func TestDataShareStorageLimitHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &adminDataShareSettingRepoStub{values: map[string]string{}}
	h := NewDataSharingHandler(service.NewDataSharingService(nil, repo))

	getRecorder := httptest.NewRecorder()
	getCtx, _ := gin.CreateTestContext(getRecorder)
	getCtx.Request = httptest.NewRequest(http.MethodGet, "/admin/data-sharing/storage-limit", nil)
	h.GetStorageLimit(getCtx)
	require.Equal(t, http.StatusOK, getRecorder.Code)

	var getEnvelope struct {
		Code int                           `json:"code"`
		Data service.DataShareStorageLimit `json:"data"`
	}
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &getEnvelope))
	require.False(t, getEnvelope.Data.Enabled)

	body := bytes.NewBufferString(`{"limit_bytes":1048576}`)
	putRecorder := httptest.NewRecorder()
	putCtx, _ := gin.CreateTestContext(putRecorder)
	putCtx.Request = httptest.NewRequest(http.MethodPut, "/admin/data-sharing/storage-limit", body)
	putCtx.Request.Header.Set("Content-Type", "application/json")
	h.UpdateStorageLimit(putCtx)
	require.Equal(t, http.StatusOK, putRecorder.Code)
	require.Equal(t, "1048576", repo.values[service.SettingKeyDataSharingStorageLimit])
}

func TestDataShareCaptureRuntimeSettingsHandlers(t *testing.T) {
	service.SetDataShareCompressionLevel(string(service.DataShareCompressionLevelFastest))
	t.Cleanup(func() {
		service.SetDataShareCompressionLevel(string(service.DataShareCompressionLevelFastest))
	})
	gin.SetMode(gin.TestMode)
	repo := &adminDataShareSettingRepoStub{values: map[string]string{}}
	pool := service.NewDataSharingCaptureWorkerPoolWithOptions(service.DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	h := NewDataSharingHandler(service.NewDataSharingService(nil, repo, pool))

	getRecorder := httptest.NewRecorder()
	getCtx, _ := gin.CreateTestContext(getRecorder)
	getCtx.Request = httptest.NewRequest(http.MethodGet, "/admin/data-sharing/runtime-settings", nil)
	h.GetCaptureRuntimeSettings(getCtx)
	require.Equal(t, http.StatusOK, getRecorder.Code)

	var getEnvelope struct {
		Code int                                     `json:"code"`
		Data service.DataShareCaptureRuntimeSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &getEnvelope))
	require.Equal(t, 15, getEnvelope.Data.TaskTimeoutSeconds)
	require.Equal(t, string(service.DataShareCompressionLevelFastest), getEnvelope.Data.CompressionLevel)
	require.True(t, getEnvelope.Data.BufferEnabled)
	require.Equal(t, 30, getEnvelope.Data.BufferIdleFlushSeconds)

	body := bytes.NewBufferString(`{"worker_count":3,"queue_size":8,"flush_queue_size":9,"task_timeout_seconds":60,"compression_level":"default","buffer_enabled":true,"buffer_idle_flush_seconds":7,"buffer_max_sessions":123,"buffer_max_pending_events":456,"duration_window_size":256}`)
	putRecorder := httptest.NewRecorder()
	putCtx, _ := gin.CreateTestContext(putRecorder)
	putCtx.Request = httptest.NewRequest(http.MethodPut, "/admin/data-sharing/runtime-settings", body)
	putCtx.Request.Header.Set("Content-Type", "application/json")
	h.UpdateCaptureRuntimeSettings(putCtx)
	require.Equal(t, http.StatusOK, putRecorder.Code)
	require.JSONEq(t, fmt.Sprintf(`{"worker_count":3,"queue_size":8,"flush_queue_size":9,"task_timeout_seconds":60,"compression_level":"default","buffer_enabled":true,"buffer_idle_flush_seconds":7,"buffer_max_sessions":123,"buffer_max_pending_events":456,"duration_window_size":256,"export_batch_size":500,"export_worker_count":%d}`, service.NormalizeDataShareExportWorkerCount(0)), repo.values[service.SettingKeyDataSharingCaptureRuntime])
	require.Equal(t, 3, pool.Stats().WorkerCount)
	require.Equal(t, 8, pool.Stats().QueueCapacity)
	require.Equal(t, 9, pool.Stats().FlushQueueCapacity)
	require.Equal(t, 60, pool.Stats().TaskTimeoutSeconds)
	require.Equal(t, string(service.DataShareCompressionLevelDefault), pool.Stats().CompressionLevel)
}

func TestDataShareCaptureRuntimeSettingsHandlerBackfillsLegacyPayload(t *testing.T) {
	service.SetDataShareCompressionLevel(string(service.DataShareCompressionLevelFastest))
	t.Cleanup(func() {
		service.SetDataShareCompressionLevel(string(service.DataShareCompressionLevelFastest))
	})
	gin.SetMode(gin.TestMode)
	repo := &adminDataShareSettingRepoStub{values: map[string]string{}}
	pool := service.NewDataSharingCaptureWorkerPoolWithOptions(service.DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	h := NewDataSharingHandler(service.NewDataSharingService(nil, repo, pool))

	body := bytes.NewBufferString(`{"worker_count":3,"queue_size":8,"task_timeout_seconds":60,"compression_level":"default"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/admin/data-sharing/runtime-settings", body)
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateCaptureRuntimeSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, fmt.Sprintf(`{"worker_count":3,"queue_size":8,"flush_queue_size":8,"task_timeout_seconds":60,"compression_level":"default","buffer_enabled":true,"buffer_idle_flush_seconds":30,"buffer_max_sessions":4096,"buffer_max_pending_events":65536,"duration_window_size":512,"export_batch_size":500,"export_worker_count":%d}`, service.NormalizeDataShareExportWorkerCount(0)), repo.values[service.SettingKeyDataSharingCaptureRuntime])
}
