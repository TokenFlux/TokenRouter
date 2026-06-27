package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type dataShareSettingRepoStub struct {
	values map[string]string
	err    error
}

func (s *dataShareSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *dataShareSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}

func (s *dataShareSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.err != nil {
		return s.err
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}

func (s *dataShareSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (s *dataShareSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if s.err != nil {
		return s.err
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	for key, value := range settings {
		s.values[key] = value
	}
	return nil
}

func (s *dataShareSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *dataShareSettingRepoStub) Delete(_ context.Context, key string) error {
	if s.err != nil {
		return s.err
	}
	delete(s.values, key)
	return nil
}

func resetDataShareCompressionLevel(t *testing.T) {
	t.Helper()
	previous := CurrentDataShareCompressionLevel()
	t.Cleanup(func() {
		SetDataShareCompressionLevel(previous)
	})
	SetDataShareCompressionLevel(string(defaultDataSharingCaptureCompressionLevel))
}

type dataShareCaptureRepoStub struct {
	mu          sync.Mutex
	saves       int
	sessions    []*DataShareSession
	hydrated    map[string]*DataShareSession
	hydrateErrs []error
	stats       *DataShareStats
	err         error
	saveErrs    []error
	seenOpts    []DataShareUpsertOptions
}

func (r *dataShareCaptureRepoStub) GetCaptureByTrajectoryIDWithPayload(_ context.Context, trajectoryID string) (*DataShareSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.hydrateErrs) > 0 {
		err := r.hydrateErrs[0]
		r.hydrateErrs = r.hydrateErrs[1:]
		return nil, err
	}
	if r.hydrated == nil || r.hydrated[trajectoryID] == nil {
		return nil, ErrDataShareSessionNotFound
	}
	return cloneBufferedDataShareSession(r.hydrated[trajectoryID]), nil
}

func (r *dataShareCaptureRepoStub) SaveCaptureSnapshot(ctx context.Context, session *DataShareSession, opts ...DataShareUpsertOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saves++
	r.sessions = append(r.sessions, cloneBufferedDataShareSession(session))
	if len(opts) > 0 {
		r.seenOpts = append(r.seenOpts, opts[0])
	}
	if len(r.saveErrs) > 0 {
		err := r.saveErrs[0]
		r.saveErrs = r.saveErrs[1:]
		return err
	}
	if r.err != nil {
		return r.err
	}
	return ctx.Err()
}

func (r *dataShareCaptureRepoStub) Count(context.Context, DataShareSessionFilters) (int64, error) {
	panic("unexpected Count call")
}

func (r *dataShareCaptureRepoStub) List(context.Context, pagination.PaginationParams, DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (r *dataShareCaptureRepoStub) ListWithPayload(context.Context, pagination.PaginationParams, DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error) {
	panic("unexpected ListWithPayload call")
}

func (r *dataShareCaptureRepoStub) ListWithPayloadPage(context.Context, pagination.PaginationParams, DataShareSessionFilters) ([]DataShareSession, error) {
	panic("unexpected ListWithPayloadPage call")
}

func (r *dataShareCaptureRepoStub) ListExportPayloadPage(context.Context, DataShareSessionFilters, *DataShareSessionExportCursor, int, int, DataShareExportDurationRecorder) ([]DataShareSession, *DataShareSessionExportCursor, error) {
	panic("unexpected ListExportPayloadPage call")
}

func (r *dataShareCaptureRepoStub) GetByID(context.Context, int64) (*DataShareSession, error) {
	panic("unexpected GetByID call")
}

func (r *dataShareCaptureRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (r *dataShareCaptureRepoStub) BatchDelete(context.Context, []int64, DataShareSessionFilters) (int64, error) {
	panic("unexpected BatchDelete call")
}

func (r *dataShareCaptureRepoStub) Stats(context.Context, DataShareSessionFilters) (*DataShareStats, error) {
	if r.stats != nil {
		return r.stats, r.err
	}
	return &DataShareStats{SessionCount: 2}, r.err
}

func (r *dataShareCaptureRepoStub) FilterOptions(context.Context, DataShareSessionFilters) (*DataShareSessionFilterOptions, error) {
	panic("unexpected FilterOptions call")
}

func (r *dataShareCaptureRepoStub) TotalStorageBytes(context.Context) (int64, error) {
	return 0, nil
}

func (r *dataShareCaptureRepoStub) upsertCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saves
}

func (r *dataShareCaptureRepoStub) lastSession() *DataShareSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) == 0 {
		return nil
	}
	return r.sessions[len(r.sessions)-1]
}

type dataShareExportRepoStub struct {
	items       []DataShareSession
	countCalls  int
	pageLimits  []int
	pageWorkers []int
}

type dataShareExportObjectStoreStub struct {
	mu            sync.Mutex
	objects       map[string][]byte
	presignedKeys []string
	uploadErr     error
}

type dataShareBlockingExportObjectStoreStub struct {
	started chan struct{}
}

func newDataShareExportObjectStoreStub() *dataShareExportObjectStoreStub {
	return &dataShareExportObjectStoreStub{objects: map[string][]byte{}}
}

func (s *dataShareExportObjectStoreStub) Upload(_ context.Context, key string, body io.Reader, _ string) (int64, error) {
	if s.uploadErr != nil {
		return 0, s.uploadErr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.objects[key] = data
	s.mu.Unlock()
	return int64(len(data)), nil
}

func (s *dataShareExportObjectStoreStub) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	return s.Upload(ctx, key, body, contentType)
}

func (s *dataShareExportObjectStoreStub) UploadFileWithProgress(ctx context.Context, key string, body io.Reader, contentType string, onProgress func(uploadedBytes int64)) (int64, error) {
	size, err := s.Upload(ctx, key, body, contentType)
	if err == nil && onProgress != nil {
		onProgress(size)
	}
	return size, err
}

func newDataShareBlockingExportObjectStoreStub() *dataShareBlockingExportObjectStoreStub {
	return &dataShareBlockingExportObjectStoreStub{started: make(chan struct{})}
}

func (s *dataShareBlockingExportObjectStoreStub) Upload(context.Context, string, io.Reader, string) (int64, error) {
	panic("unexpected Upload call")
}

func (s *dataShareBlockingExportObjectStoreStub) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	return s.UploadFileWithProgress(ctx, key, body, contentType, nil)
}

func (s *dataShareBlockingExportObjectStoreStub) UploadFileWithProgress(ctx context.Context, _ string, _ io.Reader, _ string, onProgress func(uploadedBytes int64)) (int64, error) {
	close(s.started)
	if onProgress != nil {
		onProgress(1)
	}
	<-ctx.Done()
	return 1, ctx.Err()
}

func (s *dataShareBlockingExportObjectStoreStub) Download(context.Context, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not found")
}

func (s *dataShareBlockingExportObjectStoreStub) Delete(context.Context, string) error {
	return nil
}

func (s *dataShareBlockingExportObjectStoreStub) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (s *dataShareBlockingExportObjectStoreStub) HeadBucket(context.Context) error {
	return nil
}

func (s *dataShareExportObjectStoreStub) Download(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *dataShareExportObjectStoreStub) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

func (s *dataShareExportObjectStoreStub) PresignURL(_ context.Context, key string, _ time.Duration) (string, error) {
	s.mu.Lock()
	s.presignedKeys = append(s.presignedKeys, key)
	s.mu.Unlock()
	return "https://download.example.test/" + key, nil
}

func (s *dataShareExportObjectStoreStub) HeadBucket(context.Context) error {
	return nil
}

type dataSharePlainEncryptor struct{}

func (dataSharePlainEncryptor) Encrypt(value string) (string, error) {
	return "ENC:" + value, nil
}

func (dataSharePlainEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "ENC:"), nil
}

func (r *dataShareExportRepoStub) GetCaptureByTrajectoryIDWithPayload(context.Context, string) (*DataShareSession, error) {
	panic("unexpected GetCaptureByTrajectoryIDWithPayload call")
}

func (r *dataShareExportRepoStub) SaveCaptureSnapshot(context.Context, *DataShareSession, ...DataShareUpsertOptions) error {
	panic("unexpected SaveCaptureSnapshot call")
}

func (r *dataShareExportRepoStub) Count(_ context.Context, filters DataShareSessionFilters) (int64, error) {
	r.countCalls++
	_, result, err := r.ListWithPayload(context.Background(), pagination.PaginationParams{Page: 1, PageSize: len(r.items)}, filters)
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}
	return result.Total, nil
}

func (r *dataShareExportRepoStub) List(context.Context, pagination.PaginationParams, DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (r *dataShareExportRepoStub) ListWithPayload(_ context.Context, params pagination.PaginationParams, _ DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error) {
	pageSize := params.PageSize
	if pageSize <= 0 {
		pageSize = len(r.items)
	}
	start := params.Offset()
	if start >= len(r.items) {
		return nil, &pagination.PaginationResult{Total: int64(len(r.items)), Page: params.Page, PageSize: pageSize, Pages: 1}, nil
	}
	end := start + pageSize
	if end > len(r.items) {
		end = len(r.items)
	}
	pages := 1
	if pageSize > 0 {
		pages = (len(r.items) + pageSize - 1) / pageSize
	}
	return r.items[start:end], &pagination.PaginationResult{Total: int64(len(r.items)), Page: params.Page, PageSize: pageSize, Pages: pages}, nil
}

func (r *dataShareExportRepoStub) ListWithPayloadPage(ctx context.Context, params pagination.PaginationParams, filters DataShareSessionFilters) ([]DataShareSession, error) {
	items, _, err := r.ListWithPayload(ctx, params, filters)
	return items, err
}

func (r *dataShareExportRepoStub) ListExportPayloadPage(_ context.Context, _ DataShareSessionFilters, cursor *DataShareSessionExportCursor, limit int, workerCount int, recorder DataShareExportDurationRecorder) ([]DataShareSession, *DataShareSessionExportCursor, error) {
	r.pageLimits = append(r.pageLimits, limit)
	r.pageWorkers = append(r.pageWorkers, workerCount)
	start := 0
	if cursor != nil {
		for i := range r.items {
			item := r.items[i]
			if item.CreatedAt.After(cursor.CreatedAt) || (item.CreatedAt.Equal(cursor.CreatedAt) && item.ID > cursor.ID) {
				start = i
				break
			}
			start = i + 1
		}
	}
	if limit <= 0 {
		limit = len(r.items)
	}
	if start >= len(r.items) {
		return nil, nil, nil
	}
	end := start + limit
	if end > len(r.items) {
		end = len(r.items)
	}
	if recorder != nil {
		recorder.Observe(DataShareExportDurationPartDBPage, 0)
		recorder.Observe(DataShareExportDurationPartPayloadDecode, 0)
	}
	out := r.items[start:end]
	if len(out) == 0 {
		return out, nil, nil
	}
	last := out[len(out)-1]
	return out, &DataShareSessionExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (r *dataShareExportRepoStub) GetByID(context.Context, int64) (*DataShareSession, error) {
	panic("unexpected GetByID call")
}

func (r *dataShareExportRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (r *dataShareExportRepoStub) BatchDelete(context.Context, []int64, DataShareSessionFilters) (int64, error) {
	panic("unexpected BatchDelete call")
}

func (r *dataShareExportRepoStub) Stats(context.Context, DataShareSessionFilters) (*DataShareStats, error) {
	panic("unexpected Stats call")
}

func (r *dataShareExportRepoStub) FilterOptions(context.Context, DataShareSessionFilters) (*DataShareSessionFilterOptions, error) {
	panic("unexpected FilterOptions call")
}

func (r *dataShareExportRepoStub) TotalStorageBytes(context.Context) (int64, error) {
	return 0, nil
}

type dataShareExportArtifactRepoStub struct {
	mu                sync.Mutex
	nextID            int64
	items             map[int64]*DataShareExportArtifact
	markCompletedErr  error
	recoveredMessages []string
}

func newDataShareExportArtifactRepoStub() *dataShareExportArtifactRepoStub {
	return &dataShareExportArtifactRepoStub{nextID: 1, items: map[int64]*DataShareExportArtifact{}}
}

func (r *dataShareExportArtifactRepoStub) Create(_ context.Context, artifact *DataShareExportArtifact) (*DataShareExportArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := *artifact
	item.ID = r.nextID
	r.nextID++
	now := time.Now()
	item.CreatedAt = now
	item.UpdatedAt = now
	r.items[item.ID] = &item
	return cloneDataShareExportArtifact(&item), nil
}

func (r *dataShareExportArtifactRepoStub) Get(_ context.Context, id int64) (*DataShareExportArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return nil, ErrDataShareExportArtifactNotFound
	}
	return cloneDataShareExportArtifact(item), nil
}

func (r *dataShareExportArtifactRepoStub) List(_ context.Context, params pagination.PaginationParams) ([]DataShareExportArtifact, *pagination.PaginationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]DataShareExportArtifact, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, *cloneDataShareExportArtifact(item))
	}
	return items, &pagination.PaginationResult{Total: int64(len(items)), Page: params.Page, PageSize: params.PageSize, Pages: 1}, nil
}

func (r *dataShareExportArtifactRepoStub) MarkRunning(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return ErrDataShareExportArtifactNotFound
	}
	now := time.Now()
	item.Status = DataShareExportArtifactStatusRunning
	item.StartedAt = &now
	item.UpdatedAt = now
	return nil
}

func (r *dataShareExportArtifactRepoStub) MarkCompleted(_ context.Context, id int64, storagePath string, sessionCount int64, fileSize int64, sha256 string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.markCompletedErr != nil {
		return r.markCompletedErr
	}
	item := r.items[id]
	if item == nil {
		return ErrDataShareExportArtifactNotFound
	}
	now := time.Now()
	item.Status = DataShareExportArtifactStatusCompleted
	item.StoragePath = storagePath
	item.SessionCount = sessionCount
	item.FileSize = fileSize
	item.SHA256 = sha256
	item.CompletedAt = &now
	item.UpdatedAt = now
	return nil
}

func (r *dataShareExportArtifactRepoStub) MarkFailed(_ context.Context, id int64, errorMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return ErrDataShareExportArtifactNotFound
	}
	now := time.Now()
	item.Status = DataShareExportArtifactStatusFailed
	item.ErrorMessage = errorMessage
	item.CompletedAt = &now
	item.UpdatedAt = now
	return nil
}

func (r *dataShareExportArtifactRepoStub) MarkRemoteUploading(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return ErrDataShareExportArtifactNotFound
	}
	item.RemoteStatus = DataShareExportArtifactRemoteStatusUploading
	item.RemoteErrorMessage = ""
	item.UpdatedAt = time.Now()
	return nil
}

func (r *dataShareExportArtifactRepoStub) MarkRemoteUploaded(_ context.Context, id int64, bucket string, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return ErrDataShareExportArtifactNotFound
	}
	now := time.Now()
	item.RemoteStatus = DataShareExportArtifactRemoteStatusUploaded
	item.RemoteBucket = bucket
	item.RemoteKey = key
	item.RemoteErrorMessage = ""
	item.RemoteUploadedAt = &now
	item.UpdatedAt = now
	return nil
}

func (r *dataShareExportArtifactRepoStub) MarkRemoteUploadFailed(_ context.Context, id int64, errorMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return ErrDataShareExportArtifactNotFound
	}
	if item.RemoteKey != "" {
		item.RemoteStatus = DataShareExportArtifactRemoteStatusUploaded
	} else {
		item.RemoteStatus = DataShareExportArtifactRemoteStatusFailed
	}
	item.RemoteErrorMessage = errorMessage
	item.UpdatedAt = time.Now()
	return nil
}

func (r *dataShareExportArtifactRepoStub) MarkInterruptedFailed(_ context.Context, errorMessage string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoveredMessages = append(r.recoveredMessages, errorMessage)
	now := time.Now()
	var affected int64
	for _, item := range r.items {
		if item.Status != DataShareExportArtifactStatusPending && item.Status != DataShareExportArtifactStatusRunning {
			continue
		}
		item.Status = DataShareExportArtifactStatusFailed
		item.ErrorMessage = errorMessage
		item.CompletedAt = &now
		item.UpdatedAt = now
		affected++
	}
	return affected, nil
}

func (r *dataShareExportArtifactRepoStub) MarkInterruptedRemoteUploads(_ context.Context, errorMessage string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var affected int64
	for _, item := range r.items {
		if item.RemoteStatus != DataShareExportArtifactRemoteStatusUploading {
			continue
		}
		if item.RemoteKey != "" {
			item.RemoteStatus = DataShareExportArtifactRemoteStatusUploaded
		} else {
			item.RemoteStatus = DataShareExportArtifactRemoteStatusFailed
		}
		item.RemoteErrorMessage = errorMessage
		item.UpdatedAt = time.Now()
		affected++
	}
	return affected, nil
}

func (r *dataShareExportArtifactRepoStub) MarkDeleted(_ context.Context, id int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[id]
	if item == nil {
		return "", ErrDataShareExportArtifactNotFound
	}
	if item.RemoteStatus == DataShareExportArtifactRemoteStatusUploading {
		return "", ErrDataShareExportArtifactRemoteUploadInProgress
	}
	now := time.Now()
	storagePath := item.StoragePath
	item.Status = DataShareExportArtifactStatusDeleted
	item.StoragePath = ""
	item.DeletedAt = &now
	item.UpdatedAt = now
	return storagePath, nil
}

func cloneDataShareExportArtifact(item *DataShareExportArtifact) *DataShareExportArtifact {
	if item == nil {
		return nil
	}
	out := *item
	out.Filters.IDs = append([]int64(nil), item.Filters.IDs...)
	out.Filters.ExcludeIDs = append([]int64(nil), item.Filters.ExcludeIDs...)
	return &out
}

func TestDataSharingService_CaptureAsyncUsesWorkerContext(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   4,
		TaskTimeout: time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(repo, nil, pool)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          false,
		BufferIdleFlushSeconds: 5,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	mode := svc.CaptureOpenAIRequestAsync(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group:   &Group{ID: gid, DataSharingEnabled: true},
		},
		Provider:        PlatformOpenAI,
		Model:           "gpt-5-alias",
		UpstreamModel:   "gpt-5-2026-05-01",
		SessionID:       "session-async",
		RequestID:       "request-async",
		RequestBody:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		ResponseBody:    []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`),
		InboundEndpoint: "/v1/chat/completions",
	})
	require.Equal(t, DataSharingCaptureSubmitModeEnqueued, mode)

	require.Eventually(t, func() bool {
		return svc.CaptureWorkerStats().CompletedTotal == 1 && svc.CaptureBufferStats().PendingEvents == 1
	}, time.Second, 10*time.Millisecond)
	require.Greater(t, svc.CaptureDurationStats().SampleCount, 0)
	require.Equal(t, 0, repo.upsertCount())
	svc.captureBuffer.FlushAll(context.Background())
	require.Equal(t, 1, repo.upsertCount())
	require.Greater(t, findDataShareCaptureDurationPart(t, svc.CaptureDurationStats(), DataShareCaptureDurationPartFlushTotal).SampleCount, 0)
}

func TestDataSharingService_CaptureFlushRetriesTransientPersistenceError(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{
		saveErrs: []error{fmt.Errorf("write tcp 172.18.0.4:45932->172.18.0.3:5432: write: connection reset by peer")},
	}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   4,
		TaskTimeout: time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(repo, &dataShareSettingRepoStub{values: map[string]string{}}, pool)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	mode := svc.CaptureOpenAIRequestAsync(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group:   &Group{ID: gid, DataSharingEnabled: true},
		},
		Provider:        PlatformOpenAI,
		Model:           "gpt-5",
		SessionID:       "session-transient-db",
		RequestID:       "request-transient-db",
		RequestBody:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		ResponseBody:    []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`),
		InboundEndpoint: "/v1/chat/completions",
	})
	require.Equal(t, DataSharingCaptureSubmitModeEnqueued, mode)
	require.Eventually(t, func() bool {
		return svc.CaptureBufferStats().PendingEvents == 1
	}, time.Second, 10*time.Millisecond)

	svc.captureBuffer.FlushAll(context.Background())

	require.Equal(t, 2, repo.upsertCount())
	require.Equal(t, uint64(0), svc.CaptureWorkerStats().FailedTotal)
	require.Empty(t, svc.CaptureWorkerStats().LastError)
	require.Equal(t, uint64(1), svc.CaptureBufferStats().FlushSuccessTotal)
	require.Equal(t, uint64(0), svc.CaptureBufferStats().FlushFailedTotal)
	require.Empty(t, svc.CaptureBufferStats().LastError)
	require.Equal(t, 0, svc.CaptureBufferStats().PendingEvents)
}

func TestDataSharingService_CaptureAsyncDisabledGroupDoesNotSubmit(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   4,
		TaskTimeout: time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(repo, nil, pool)

	mode := svc.CaptureClaudeRequestAsync(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group:   &Group{ID: gid, DataSharingEnabled: false},
		},
	})

	require.Equal(t, DataSharingCaptureSubmitModeDropped, mode)
	require.Equal(t, uint64(0), svc.CaptureWorkerStats().SubmittedTotal)
	require.Equal(t, 0, repo.upsertCount())
}

func TestDataSharingService_CaptureAsyncMissingPoolDoesNotSyncFallback(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)

	mode := svc.CaptureOpenAIRequestAsync(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group:   &Group{ID: gid, DataSharingEnabled: true},
		},
		Provider: PlatformOpenAI,
		Model:    "gpt-5",
	})

	require.Equal(t, DataSharingCaptureSubmitModeDropped, mode)
	require.Equal(t, 0, repo.upsertCount())
	require.Equal(t, uint64(1), svc.CaptureWorkerStats().DroppedTotal)
}

func TestDataSharingService_StatsIncludesCaptureWorker(t *testing.T) {
	repo := &dataShareCaptureRepoStub{stats: &DataShareStats{SessionCount: 3}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   7,
		TaskTimeout: time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(repo, nil, pool)

	stats, err := svc.Stats(context.Background(), DataShareSessionFilters{})
	require.NoError(t, err)
	require.Equal(t, int64(3), stats.SessionCount)
	require.Equal(t, 7, stats.CaptureWorker.QueueCapacity)
	require.True(t, stats.CaptureBuffer.Enabled)
	require.Equal(t, defaultDataSharingCaptureBufferIdleSeconds, stats.CaptureBuffer.IdleFlushSeconds)
}

func TestDataSharingService_UpdateCaptureRuntimeSettingsAppliesWorkerTimeout(t *testing.T) {
	resetDataShareCompressionLevel(t)
	repo := &dataShareSettingRepoStub{values: map[string]string{}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(nil, repo, pool)

	settings, err := svc.UpdateCaptureRuntimeSettings(context.Background(), DataShareCaptureRuntimeSettings{
		WorkerCount:            2,
		QueueSize:              9,
		FlushQueueSize:         12,
		TaskTimeoutSeconds:     45,
		CompressionLevel:       string(DataShareCompressionLevelDefault),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 7,
		BufferMaxSessions:      123,
		BufferMaxPendingEvents: 456,
	})
	require.NoError(t, err)
	require.Equal(t, 2, settings.WorkerCount)
	require.Equal(t, 9, settings.QueueSize)
	require.Equal(t, 12, settings.FlushQueueSize)
	require.Equal(t, 45, settings.TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelDefault), settings.CompressionLevel)
	require.True(t, settings.BufferEnabled)
	require.Equal(t, 7, settings.BufferIdleFlushSeconds)
	require.Equal(t, 123, settings.BufferMaxSessions)
	require.Equal(t, 456, settings.BufferMaxPendingEvents)
	require.Equal(t, defaultDataShareExportBatchSize, settings.ExportBatchSize)
	require.Equal(t, defaultDataShareExportWorkerCount(), settings.ExportWorkerCount)
	require.JSONEq(t, fmt.Sprintf(`{"worker_count":2,"queue_size":9,"flush_queue_size":12,"task_timeout_seconds":45,"compression_level":"default","buffer_enabled":true,"buffer_idle_flush_seconds":7,"buffer_max_sessions":123,"buffer_max_pending_events":456,"duration_window_size":512,"export_batch_size":500,"export_worker_count":%d}`, defaultDataShareExportWorkerCount()), repo.values[SettingKeyDataSharingCaptureRuntime])
	require.Equal(t, 2, svc.CaptureWorkerStats().WorkerCount)
	require.Equal(t, 9, svc.CaptureWorkerStats().QueueCapacity)
	require.Equal(t, 12, svc.CaptureWorkerStats().FlushQueueCapacity)
	require.Equal(t, 45, svc.CaptureWorkerStats().TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelDefault), svc.CaptureWorkerStats().CompressionLevel)
}

func TestDataSharingService_UpdateCaptureRuntimeSettingsClampsUpperBounds(t *testing.T) {
	resetDataShareCompressionLevel(t)
	repo := &dataShareSettingRepoStub{values: map[string]string{}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(nil, repo, pool)

	settings, err := svc.UpdateCaptureRuntimeSettings(context.Background(), DataShareCaptureRuntimeSettings{
		WorkerCount:            maxDataSharingCaptureWorkerCount + 100,
		QueueSize:              maxDataSharingCaptureQueueSize + 100,
		FlushQueueSize:         maxDataSharingCaptureQueueSize + 200,
		TaskTimeoutSeconds:     maxDataSharingCaptureTaskTimeoutSeconds + 100,
		BufferIdleFlushSeconds: maxDataSharingCaptureBufferIdleSeconds + 100,
		BufferMaxSessions:      maxDataSharingCaptureBufferMaxSessions + 100,
		BufferMaxPendingEvents: maxDataSharingCaptureBufferMaxEvents + 100,
		ExportBatchSize:        maxDataShareExportBatchSize + 100,
		ExportWorkerCount:      maxDataShareExportWorkerCount + 100,
	})
	require.NoError(t, err)
	require.Equal(t, maxDataSharingCaptureWorkerCount, settings.WorkerCount)
	require.Equal(t, maxDataSharingCaptureQueueSize, settings.QueueSize)
	require.Equal(t, maxDataSharingCaptureQueueSize, settings.FlushQueueSize)
	require.Equal(t, maxDataSharingCaptureTaskTimeoutSeconds, settings.TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelFastest), settings.CompressionLevel)
	require.Equal(t, maxDataSharingCaptureBufferIdleSeconds, settings.BufferIdleFlushSeconds)
	require.Equal(t, maxDataSharingCaptureBufferMaxSessions, settings.BufferMaxSessions)
	require.Equal(t, maxDataSharingCaptureBufferMaxEvents, settings.BufferMaxPendingEvents)
	require.Equal(t, maxDataShareExportBatchSize, settings.ExportBatchSize)
	require.Equal(t, maxDataShareExportWorkerCount, settings.ExportWorkerCount)
	require.JSONEq(t, `{"worker_count":1024,"queue_size":100000,"flush_queue_size":100000,"task_timeout_seconds":1800,"compression_level":"fastest","buffer_enabled":true,"buffer_idle_flush_seconds":1800,"buffer_max_sessions":100000,"buffer_max_pending_events":1000000,"duration_window_size":512,"export_batch_size":2000,"export_worker_count":8}`, repo.values[SettingKeyDataSharingCaptureRuntime])
	require.Equal(t, maxDataSharingCaptureWorkerCount, pool.Stats().WorkerCount)
	require.Equal(t, maxDataSharingCaptureQueueSize, pool.Stats().QueueCapacity)
	require.Equal(t, maxDataSharingCaptureQueueSize, pool.Stats().FlushQueueCapacity)
	require.Equal(t, maxDataSharingCaptureTaskTimeoutSeconds, pool.Stats().TaskTimeoutSeconds)
}

func TestDataSharingService_LoadRuntimeSettingsAppliesStoredTimeout(t *testing.T) {
	resetDataShareCompressionLevel(t)
	repo := &dataShareSettingRepoStub{values: map[string]string{SettingKeyDataSharingCaptureRuntime: `{"worker_count":4,"queue_size":10,"flush_queue_size":11,"task_timeout_seconds":90,"compression_level":"better","buffer_enabled":true,"buffer_idle_flush_seconds":9,"buffer_max_sessions":100,"buffer_max_pending_events":200}`}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(nil, repo, pool)

	settings, err := svc.LoadRuntimeSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4, settings.WorkerCount)
	require.Equal(t, 10, settings.QueueSize)
	require.Equal(t, 11, settings.FlushQueueSize)
	require.Equal(t, 90, settings.TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelBetter), settings.CompressionLevel)
	require.True(t, settings.BufferEnabled)
	require.Equal(t, 9, settings.BufferIdleFlushSeconds)
	require.Equal(t, 100, settings.BufferMaxSessions)
	require.Equal(t, 200, settings.BufferMaxPendingEvents)
	require.Equal(t, 4, pool.Stats().WorkerCount)
	require.Equal(t, 10, pool.Stats().QueueCapacity)
	require.Equal(t, 11, pool.Stats().FlushQueueCapacity)
	require.Equal(t, 90, pool.Stats().TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelBetter), pool.Stats().CompressionLevel)
}

func TestDataSharingService_LoadRuntimeSettingsUsesCurrentCompressionDefault(t *testing.T) {
	resetDataShareCompressionLevel(t)
	SetDataShareCompressionLevel(string(DataShareCompressionLevelBest))
	repo := &dataShareSettingRepoStub{values: map[string]string{}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(nil, repo, pool)

	settings, err := svc.LoadRuntimeSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, string(DataShareCompressionLevelBest), settings.CompressionLevel)
	require.Equal(t, string(DataShareCompressionLevelBest), pool.Stats().CompressionLevel)
}

func TestDataSharingService_LoadRuntimeSettingsClampsStoredUpperBounds(t *testing.T) {
	resetDataShareCompressionLevel(t)
	repo := &dataShareSettingRepoStub{values: map[string]string{SettingKeyDataSharingCaptureRuntime: `{"worker_count":2048,"queue_size":999999,"task_timeout_seconds":9999,"buffer_enabled":true,"buffer_idle_flush_seconds":9999,"buffer_max_sessions":999999,"buffer_max_pending_events":9999999}`}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(nil, repo, pool)

	settings, err := svc.LoadRuntimeSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, maxDataSharingCaptureWorkerCount, settings.WorkerCount)
	require.Equal(t, maxDataSharingCaptureQueueSize, settings.QueueSize)
	require.Equal(t, maxDataSharingCaptureQueueSize, settings.FlushQueueSize)
	require.Equal(t, maxDataSharingCaptureTaskTimeoutSeconds, settings.TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelFastest), settings.CompressionLevel)
	require.Equal(t, maxDataSharingCaptureBufferIdleSeconds, settings.BufferIdleFlushSeconds)
	require.Equal(t, maxDataSharingCaptureBufferMaxSessions, settings.BufferMaxSessions)
	require.Equal(t, maxDataSharingCaptureBufferMaxEvents, settings.BufferMaxPendingEvents)
	require.Equal(t, defaultDataShareExportWorkerCount(), settings.ExportWorkerCount)
	require.Equal(t, maxDataSharingCaptureWorkerCount, pool.Stats().WorkerCount)
	require.Equal(t, maxDataSharingCaptureQueueSize, pool.Stats().QueueCapacity)
	require.Equal(t, maxDataSharingCaptureQueueSize, pool.Stats().FlushQueueCapacity)
	require.Equal(t, maxDataSharingCaptureTaskTimeoutSeconds, pool.Stats().TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelFastest), pool.Stats().CompressionLevel)
}

func TestDataSharingService_LoadRuntimeSettingsBackfillsLegacyBufferDefaults(t *testing.T) {
	resetDataShareCompressionLevel(t)
	repo := &dataShareSettingRepoStub{values: map[string]string{SettingKeyDataSharingCaptureRuntime: `{"worker_count":4,"queue_size":10,"task_timeout_seconds":90,"compression_level":"better"}`}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(nil, repo, pool)

	settings, err := svc.LoadRuntimeSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4, settings.WorkerCount)
	require.Equal(t, 10, settings.QueueSize)
	require.Equal(t, 10, settings.FlushQueueSize)
	require.Equal(t, 90, settings.TaskTimeoutSeconds)
	require.Equal(t, string(DataShareCompressionLevelBetter), settings.CompressionLevel)
	require.True(t, settings.BufferEnabled)
	require.Equal(t, defaultDataSharingCaptureBufferIdleSeconds, settings.BufferIdleFlushSeconds)
	require.Equal(t, defaultDataSharingCaptureBufferMaxSessions, settings.BufferMaxSessions)
	require.Equal(t, defaultDataSharingCaptureBufferMaxEvents, settings.BufferMaxPendingEvents)
	require.Equal(t, defaultDataSharingCaptureDurationWindowSize, settings.DurationWindowSize)
}

func TestDataSharingService_UpdateRuntimeSettingsBackfillsLegacyRequest(t *testing.T) {
	resetDataShareCompressionLevel(t)
	repo := &dataShareSettingRepoStub{values: map[string]string{}}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   1,
		TaskTimeout: 15 * time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(nil, repo, pool)

	settings, err := svc.UpdateCaptureRuntimeSettings(context.Background(), DataShareCaptureRuntimeSettings{
		WorkerCount:        3,
		QueueSize:          8,
		TaskTimeoutSeconds: 60,
		CompressionLevel:   string(DataShareCompressionLevelDefault),
	})
	require.NoError(t, err)
	require.Equal(t, 8, settings.FlushQueueSize)
	require.True(t, settings.BufferEnabled)
	require.Equal(t, defaultDataSharingCaptureBufferIdleSeconds, settings.BufferIdleFlushSeconds)
	require.Equal(t, defaultDataSharingCaptureBufferMaxSessions, settings.BufferMaxSessions)
	require.Equal(t, defaultDataSharingCaptureBufferMaxEvents, settings.BufferMaxPendingEvents)
	require.Equal(t, defaultDataShareExportBatchSize, settings.ExportBatchSize)
	require.Equal(t, defaultDataShareExportWorkerCount(), settings.ExportWorkerCount)
	require.JSONEq(t, fmt.Sprintf(`{"worker_count":3,"queue_size":8,"flush_queue_size":8,"task_timeout_seconds":60,"compression_level":"default","buffer_enabled":true,"buffer_idle_flush_seconds":30,"buffer_max_sessions":4096,"buffer_max_pending_events":65536,"duration_window_size":512,"export_batch_size":500,"export_worker_count":%d}`, defaultDataShareExportWorkerCount()), repo.values[SettingKeyDataSharingCaptureRuntime])
}

func TestDataSharingService_CaptureAsyncBuffersUntilFlush(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	pool := NewDataSharingCaptureWorkerPoolWithOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount: 1,
		QueueSize:   4,
		TaskTimeout: time.Second,
	})
	t.Cleanup(pool.Stop)
	svc := NewDataSharingService(repo, nil, pool)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	mode := svc.CaptureOpenAIRequestAsync(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group:   &Group{ID: gid, DataSharingEnabled: true},
		},
		Provider:        PlatformOpenAI,
		Model:           "gpt-5",
		SessionID:       "session-buffer",
		RequestID:       "request-buffer-1",
		RequestBody:     []byte(`{"messages":[{"role":"system","content":"你是编码助手"},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"exec_command"}}]}`),
		ResponseBody:    []byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{}"}}]}}]}`),
		InboundEndpoint: "/v1/chat/completions",
	})
	require.Equal(t, DataSharingCaptureSubmitModeEnqueued, mode)

	require.Eventually(t, func() bool {
		return svc.CaptureBufferStats().PendingEvents == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 0, repo.upsertCount())

	svc.captureBuffer.FlushAll(context.Background())
	require.Equal(t, 1, repo.upsertCount())
	require.Equal(t, uint64(1), svc.CaptureBufferStats().FlushSuccessTotal)
}

func TestDataSharingService_CaptureBufferMergesSameTrajectory(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	input := func(requestID string, messages string, inputTokens int) DataShareCaptureInput {
		return DataShareCaptureInput{
			APIKey: &APIKey{
				ID:      34,
				UserID:  56,
				GroupID: &gid,
				Group:   &Group{ID: gid, DataSharingEnabled: true},
			},
			Provider:        PlatformOpenAI,
			Model:           "gpt-5",
			SessionID:       "session-buffer-merge",
			RequestID:       requestID,
			RequestBody:     []byte(`{"messages":` + messages + `,"tools":[{"type":"function","function":{"name":"exec_command"}}]}`),
			ResponseBody:    []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`),
			InboundEndpoint: "/v1/chat/completions",
			InputTokens:     inputTokens,
			ActualCost:      float64Ptr(float64(inputTokens) / 100),
		}
	}
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-1", `[{"role":"system","content":"你是编码助手"},{"role":"user","content":"hi"}]`, 10)}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-2", `[{"role":"system","content":"你是编码助手"},{"role":"user","content":"hi"},{"role":"assistant","content":"ok"},{"role":"user","content":"again"}]`, 20)}))

	require.Equal(t, 0, repo.upsertCount())
	require.Equal(t, 2, svc.CaptureBufferStats().PendingEvents)
	svc.captureBuffer.FlushAll(context.Background())

	require.Equal(t, 1, repo.upsertCount())
	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Equal(t, int64(30), session.InputTokens)
	require.NotNil(t, session.ActualCost)
	require.InDelta(t, 0.3, *session.ActualCost, 1e-12)
	require.Equal(t, []string{"request-1", "request-2"}, stringsFromAny(session.Meta["source_request_ids"]))
	require.Len(t, session.Messages, 5)
}

func TestDataSharingService_CaptureBufferDedupesResponsesStatelessReplay(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	input := func(requestID string, inputItems string, outputText string, inputTokens int) DataShareCaptureInput {
		return DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-replay",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `,"tools":[{"type":"function","name":"exec_command","description":"运行命令","parameters":{"type":"object"}}]}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, requestID, outputText)),
			InboundEndpoint: "/v1/responses",
			InputTokens:     inputTokens,
			OutputTokens:    5,
			ActualCost:      float64Ptr(float64(inputTokens) / 100),
		}
	}

	firstInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"请列目录"}]},
		{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"README.md"}
	]`
	secondInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"请列目录"}]},
		{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"README.md"},
		{"type":"message","role":"assistant","id":"msg_old","status":"completed","content":[{"type":"output_text","text":"看到了 README.md"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"再检查 rustc 来源"}]},
		{"type":"function_call","call_id":"call_2","name":"exec_command","arguments":"{\"cmd\":\"which rustc && which cargo\"}"},
		{"type":"function_call_output","call_id":"call_2","output":"/opt/homebrew/bin/rustc\n/opt/homebrew/bin/cargo"}
	]`
	thirdInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"请列目录"}]},
		{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"README.md"},
		{"type":"message","role":"assistant","id":"msg_old","status":"completed","content":[{"type":"output_text","text":"看到了 README.md"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"再检查 rustc 来源"}]},
		{"type":"function_call","call_id":"call_2","name":"exec_command","arguments":"{\"cmd\":\"which rustc && which cargo\"}"},
		{"type":"function_call_output","call_id":"call_2","output":"/opt/homebrew/bin/rustc\n/opt/homebrew/bin/cargo"},
		{"type":"message","role":"assistant","id":"msg_2","status":"completed","content":[{"type":"output_text","text":"初步结果显示 rustc 和 cargo 都来自 /opt/homebrew/bin"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"我再补一层检查"}]}
	]`

	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-1", firstInput, "看到了 README.md", 10)}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-2", secondInput, "初步结果显示 rustc 和 cargo 都来自 /opt/homebrew/bin", 20)}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-3", thirdInput, "下一步可以检查 PATH 优先级", 30)}))

	svc.captureBuffer.FlushAll(context.Background())

	require.Equal(t, 1, repo.upsertCount())
	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 3, session.SourceRequestCount)
	require.Equal(t, int64(75), session.TotalTokens)
	require.Equal(t, []string{"request-1", "request-2", "request-3"}, stringsFromAny(session.Meta["source_request_ids"]))
	require.Len(t, session.Messages, 11)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "看到了 README.md"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "初步结果显示 rustc 和 cargo 都来自 /opt/homebrew/bin"))
	require.Equal(t, "下一步可以检查 PATH 优先级", dataShareContentText(session.Messages[len(session.Messages)-1]["content"]))
}

func TestDataSharingService_CaptureBufferDedupesMultimodalReplayWithVolatileWrapper(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	input := func(requestID string, inputItems string, outputText string) DataShareCaptureInput {
		return DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-multimodal-replay",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `,"tools":[{"type":"function","name":"exec_command","description":"运行命令","parameters":{"type":"object"}}]}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, requestID, outputText)),
			InboundEndpoint: "/v1/responses",
			InputTokens:     10,
			OutputTokens:    5,
		}
	}
	firstInput := `[
		{"type":"message","role":"system","id":"sys_req_1","status":"completed","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","id":"user_req_1","status":"completed","content":[{"type":"input_text","text":"看图并读文件"},{"type":"input_image","image_url":"data:image/png;base64,aGVsbG8="},{"type":"input_file","file_id":"file_abc","filename":"notes.txt"}]},
		{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"README.md"}
	]`
	secondInput := `[
		{"type":"message","role":"system","id":"sys_req_2","status":"in_progress","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","id":"user_req_2","status":"in_progress","content":[{"type":"input_text","text":"看图并读文件"},{"type":"input_image","image_url":"data:image/png;base64,aGVsbG8="},{"type":"input_file","file_id":"file_abc","filename":"notes.txt"}]},
		{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"README.md"},
		{"type":"message","role":"assistant","id":"msg_old_changed","status":"completed","content":[{"type":"output_text","text":"图片和文件已保留"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"继续检查"}]},
		{"type":"function_call","call_id":"call_2","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"},
		{"type":"function_call_output","call_id":"call_2","output":"/tmp/workspace"}
	]`

	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-multi-1", firstInput, "图片和文件已保留")}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-multi-2", secondInput, "第二步完成")}))
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Len(t, session.Messages, 9)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "看图并读文件"))
	content := anySlice(session.Messages[1]["content"])
	require.Len(t, content, 3)
	imageBlock, ok := mapFromAny(content[1])
	require.True(t, ok)
	require.Equal(t, "data:image/png;base64,aGVsbG8=", imageBlock["image_url"])
	fileBlock, ok := mapFromAny(content[2])
	require.True(t, ok)
	require.Equal(t, "file_abc", fileBlock["file_id"])
}

func TestDataSharingService_CaptureBufferDedupesResponseToolCallReplay(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	input := func(requestID string, inputItems string, responseBody string) DataShareCaptureInput {
		return DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-tool-replay",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `,"tools":[{"type":"function","name":"exec_command","description":"运行命令","parameters":{"type":"object"}}]}`),
			ResponseBody:    []byte(responseBody),
			InboundEndpoint: "/v1/responses",
			InputTokens:     10,
			OutputTokens:    5,
		}
	}

	firstInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"查一下目录"}]}
	]`
	firstResponse := `{"id":"resp-tool-1","output":[{"type":"function_call","call_id":"call_replay_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}]}`
	secondInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"查一下目录"}]},
		{"type":"function_call","call_id":"call_replay_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_replay_1","output":"README.md"},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"总结一下"}]}
	]`
	secondResponse := `{"id":"resp-tool-2","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"目录里有 README.md"}]}]}`

	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-tool-1", firstInput, firstResponse)}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-tool-2", secondInput, secondResponse)}))
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Len(t, session.Messages, 6)
	require.Equal(t, 1, countDataShareToolCallID(session.Messages, "call_replay_1"))
	require.Equal(t, 1, countDataShareMessagesWithToolCallID(session.Messages, "call_replay_1"))
	require.Equal(t, "总结一下", dataShareContentText(session.Messages[4]["content"]))
	require.Equal(t, "目录里有 README.md", dataShareContentText(session.Messages[5]["content"]))
}

func TestDataSharingService_CaptureBufferDedupesResponsesOutOfOrderReplay(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	input := func(requestID string, turn int, inputItems string, outputText string) DataShareCaptureInput {
		return DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-out-of-order",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, requestID, outputText)),
			InboundEndpoint: "/v1/responses",
			Turn:            turn,
			InputTokens:     10,
			OutputTokens:    5,
		}
	}

	firstInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第一步"}]}
	]`
	secondInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第一步"}]},
		{"type":"message","role":"assistant","id":"msg_turn_1","status":"completed","content":[{"type":"output_text","text":"第一步完成"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第二步"}]}
	]`

	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-2", 2, secondInput, "第二步完成")}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-1", 1, firstInput, "第一步完成")}))
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Len(t, session.Messages, 5)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第一步完成"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第二步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第二步完成"))
	require.Equal(t, true, session.Meta["capture_order_uncertain"])
}

func TestDataSharingService_CaptureBufferDedupesOutOfOrderMultiOutputResponse(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	input := func(requestID string, turn int, inputItems string, responseBody string) DataShareCaptureInput {
		return DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-out-of-order-multi-output",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `}`),
			ResponseBody:    []byte(responseBody),
			InboundEndpoint: "/v1/responses",
			Turn:            turn,
			InputTokens:     10,
			OutputTokens:    5,
		}
	}

	firstInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第一步"}]}
	]`
	firstResponse := `{"id":"request-multi-1","output":[
		{"type":"function_call","call_id":"call_multi_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第一步完成"}]}
	]}`
	secondInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第一步"}]},
		{"type":"function_call","call_id":"call_multi_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第一步完成"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第二步"}]}
	]`
	secondResponse := `{"id":"request-multi-2","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第二步完成"}]}]}`

	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-multi-2", 2, secondInput, secondResponse)}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-multi-1", 1, firstInput, firstResponse)}))
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Len(t, session.Messages, 6)
	require.Equal(t, 1, countDataShareToolCallID(session.Messages, "call_multi_1"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第一步完成"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第二步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第二步完成"))
}

func TestDataSharingService_CaptureBufferKeepsRepeatedResponseTextAcrossTurns(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	input := func(requestID string, inputItems string) DataShareCaptureInput {
		return DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-repeated-text",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"好的"}]}]}`, requestID)),
			InboundEndpoint: "/v1/responses",
			InputTokens:     10,
			OutputTokens:    5,
		}
	}

	firstInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"确认第一步"}]}
	]`
	secondInput := `[
		{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"确认第一步"}]},
		{"type":"message","role":"assistant","id":"msg_turn_1","status":"completed","content":[{"type":"output_text","text":"好的"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"确认第二步"}]}
	]`

	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-repeat-1", firstInput)}))
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: input("request-repeat-2", secondInput)}))
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Len(t, session.Messages, 5)
	require.Equal(t, 2, countDataShareMessagesWithContent(session.Messages, "好的"))
	require.Equal(t, "确认第二步", dataShareContentText(session.Messages[3]["content"]))
	require.Equal(t, "好的", dataShareContentText(session.Messages[4]["content"]))
}

func TestDataSharingService_CaptureStateUsesBoundedIdentityKeys(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	longText := strings.Repeat("很长的历史消息", 128)
	inputItems := fmt.Sprintf(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}
	]`, longText)
	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
		APIKey:          apiKey,
		Provider:        PlatformOpenAI,
		Model:           "gpt-5.5",
		SessionID:       "session-responses-bounded-state",
		RequestID:       "request-bounded-state",
		RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `}`),
		ResponseBody:    []byte(`{"id":"request-bounded-state","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"收到"}]}]}`),
		InboundEndpoint: "/v1/responses",
		InputTokens:     10,
		OutputTokens:    5,
	}}))
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	capture := mapAnyFromAny(session.Meta[dataShareInternalCaptureMetaKey])
	require.NotEmpty(t, capture)
	for _, identity := range stringsFromAny(capture["replay_identities"]) {
		require.Len(t, identity, 32)
		require.NotContains(t, identity, longText)
	}
	for _, key := range stringsFromAny(capture["response_keys"]) {
		require.Len(t, key, 32)
	}
}

func TestDataSharingCaptureBufferKeepsRealRepeatedSingleMessage(t *testing.T) {
	var got *DataShareSession
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Flush: func(_ context.Context, session *DataShareSession) error {
			got = cloneBufferedDataShareSession(session)
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	repeated := map[string]any{"role": "user", "content": "继续"}
	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-real-repeat",
		SessionID:          "sess-real-repeat",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{repeated},
	}))
	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-real-repeat",
		SessionID:          "sess-real-repeat",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{repeated},
	}))

	buffer.FlushAll(context.Background())

	require.NotNil(t, got)
	require.Equal(t, 2, got.SourceRequestCount)
	require.Len(t, got.Messages, 2)
	require.Equal(t, 2, countDataShareMessagesWithContent(got.Messages, "继续"))
}

func TestDataSharingCaptureBufferDoesNotDiffDivergedPrefix(t *testing.T) {
	merged := CompactDataShareMessages(mergeBufferedDataShareMessages(
		[]map[string]any{
			{"role": "system", "content": "你是编码助手"},
			{"role": "user", "content": "第一步"},
			{"role": "assistant", "content": "旧分支"},
			{"role": "user", "content": "旧分支继续"},
		},
		[]map[string]any{
			{"role": "system", "content": "你是编码助手"},
			{"role": "user", "content": "第一步"},
			{"role": "assistant", "content": "新分支"},
			{"role": "user", "content": "新分支继续"},
		},
	))

	require.Len(t, merged, 8)
	require.Equal(t, "旧分支", dataShareContentText(merged[2]["content"]))
	require.Equal(t, "新分支", dataShareContentText(merged[6]["content"]))
}

func TestDataSharingCaptureBufferDedupesPartialReplayWithNewTail(t *testing.T) {
	existing := []map[string]any{
		{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
		{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd><shell>bash</shell></environment_context>"},
		{"role": "user", "content": "修复按钮"},
		{"role": "assistant", "content": "我先检查代码"},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "rg button"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "Button.vue"},
		{"role": "assistant", "content": "找到入口"},
	}
	incoming := []map[string]any{
		{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
		{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd><shell>bash</shell></environment_context>"},
		{"role": "user", "content": "修复按钮"},
		{"role": "assistant", "content": "我先检查代码"},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "rg button"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "Button.vue"},
		{"role": "assistant", "content": "找到入口"},
		{"role": "user", "content": "继续修"},
		{"role": "assistant", "content": "开始修改"},
	}

	merged := mergeBufferedDataShareMessages(existing, incoming)

	require.Len(t, merged, 9)
	require.Equal(t, 1, countDataShareMessagesWithContent(merged, "修复按钮"))
	require.Equal(t, "继续修", dataShareContentText(merged[7]["content"]))
	require.Equal(t, "开始修改", dataShareContentText(merged[8]["content"]))
}

func TestDataSharingCaptureBufferDedupesReplayWithoutLosingCounters(t *testing.T) {
	costA := 0.12
	costB := 0.34
	existing := &DataShareSession{
		TrajectoryID:       "traj-replay-counters",
		SessionID:          "sess-replay-counters",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 2,
		InputTokens:        10,
		OutputTokens:       4,
		TotalTokens:        14,
		ActualCost:         &costA,
		Messages: []map[string]any{
			{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
			{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd></environment_context>"},
			{"role": "user", "content": "修复按钮"},
			{"role": "assistant", "content": "我先检查代码"},
		},
		Usage: map[string]any{"input_tokens": 10, "output_tokens": 4, "total_tokens": 14},
		Meta:  map[string]any{"source_request_ids": []string{"req-1", "req-2"}},
	}
	incoming := &DataShareSession{
		TrajectoryID:       "traj-replay-counters",
		SessionID:          "sess-replay-counters",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		InputTokens:        20,
		OutputTokens:       6,
		TotalTokens:        26,
		ActualCost:         &costB,
		Messages: []map[string]any{
			{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
			{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd></environment_context>"},
			{"role": "user", "content": "修复按钮"},
			{"role": "assistant", "content": "我先检查代码"},
			{"role": "user", "content": "继续修"},
			{"role": "assistant", "content": "开始修改"},
		},
		Usage: map[string]any{"input_tokens": 20, "output_tokens": 6, "total_tokens": 26},
		Meta:  map[string]any{"source_request_ids": []string{"req-3"}},
	}

	merged := mergeBufferedDataShareSession(existing, incoming)

	require.Equal(t, 3, merged.SourceRequestCount)
	require.Equal(t, int64(30), merged.InputTokens)
	require.Equal(t, int64(10), merged.OutputTokens)
	require.Equal(t, int64(40), merged.TotalTokens)
	require.NotNil(t, merged.ActualCost)
	require.InDelta(t, 0.46, *merged.ActualCost, 1e-12)
	require.Equal(t, 30, intFromAny(merged.Usage["input_tokens"]))
	require.Equal(t, 10, intFromAny(merged.Usage["output_tokens"]))
	require.Equal(t, 40, intFromAny(merged.Usage["total_tokens"]))
	require.Equal(t, []string{"req-1", "req-2", "req-3"}, stringsFromAny(merged.Meta["source_request_ids"]))
	require.Len(t, merged.Messages, 6)
	require.Equal(t, 1, countDataShareMessagesWithContent(merged.Messages, "修复按钮"))
	require.Equal(t, "开始修改", dataShareContentText(merged.Messages[len(merged.Messages)-1]["content"]))
}

func TestDataSharingService_MessagesRawCaptureDedupesStatelessReplay(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})
	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformAnthropic, DataSharingEnabled: true},
	}
	capture := func(requestID string, messages string, response string) {
		require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformAnthropic,
			Model:           "claude-opus-4-8",
			SessionID:       "session-messages-stateless-replay",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"claude-opus-4-8","messages":` + messages + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"content":[{"type":"text","text":%q}]}`, requestID, response)),
			InboundEndpoint: "/v1/messages",
			InputTokens:     10,
			OutputTokens:    5,
		}}))
	}

	capture("messages-replay-1", `[
		{"role":"user","content":"<system-reminder>current_date and cwd</system-reminder>"},
		{"role":"user","content":"第一步"}
	]`, "完成第一步")
	capture("messages-replay-2", `[
		{"role":"user","content":"<system-reminder>current_date and cwd</system-reminder>"},
		{"role":"user","content":"第一步"},
		{"role":"assistant","content":"完成第一步"},
		{"role":"user","content":"第二步"}
	]`, "完成第二步")
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Equal(t, int64(30), session.TotalTokens)
	require.Len(t, session.Messages, 5)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "<system-reminder>current_date and cwd</system-reminder>"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "完成第一步"))
	require.Equal(t, "第二步", dataShareContentText(session.Messages[3]["content"]))
	require.Equal(t, "完成第二步", dataShareContentText(session.Messages[4]["content"]))
}

func TestDataSharingService_MessagesRawCaptureKeepsHistoryBeforeCompaction(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})
	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformAnthropic, DataSharingEnabled: true},
	}
	capture := func(requestID string, messages string, response string) {
		require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformAnthropic,
			Model:           "claude-opus-4-8",
			SessionID:       "session-messages-compaction",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"claude-opus-4-8","messages":` + messages + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"content":[{"type":"text","text":%q}]}`, requestID, response)),
			InboundEndpoint: "/v1/messages",
			InputTokens:     10,
			OutputTokens:    5,
		}}))
	}

	capture("messages-compact-1", `[
		{"role":"user","content":"第一步"},
		{"role":"assistant","content":"完成第一步"},
		{"role":"user","content":"第二步"}
	]`, "完成第二步")
	capture("messages-compact-2", `[
		{"role":"assistant","content":[{"type":"compaction","content":"摘要：已经完成第一步和第二步。"}]},
		{"role":"user","content":"第三步"}
	]`, "完成第三步")
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Len(t, session.Messages, 7)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "完成第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第二步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "完成第二步"))
	require.Contains(t, dataShareContentText(session.Messages[4]["content"]), "摘要：已经完成第一步和第二步。")
	require.Equal(t, "第三步", dataShareContentText(session.Messages[5]["content"]))
	require.Equal(t, "完成第三步", dataShareContentText(session.Messages[6]["content"]))
}

func TestDataSharingService_MessagesRawCaptureDedupesCompactionPreservedRecentMessages(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})
	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformAnthropic, DataSharingEnabled: true},
	}
	capture := func(requestID string, messages string, response string) {
		require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformAnthropic,
			Model:           "claude-opus-4-8",
			SessionID:       "session-messages-compaction-recent",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"claude-opus-4-8","messages":` + messages + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"content":[{"type":"text","text":%q}]}`, requestID, response)),
			InboundEndpoint: "/v1/messages",
			InputTokens:     10,
			OutputTokens:    5,
		}}))
	}

	capture("messages-preserved-recent-1", `[
		{"role":"user","content":"第一步"}
	]`, "完成第一步")
	capture("messages-preserved-recent-2", `[
		{"role":"user","content":"第一步"},
		{"role":"assistant","content":"完成第一步"},
		{"role":"user","content":"第二步"}
	]`, "完成第二步")
	capture("messages-preserved-recent-3", `[
		{"role":"assistant","content":[{"type":"compaction","content":"摘要：已经完成第一步和第二步。"}]},
		{"role":"user","content":"第二步"},
		{"role":"assistant","content":"完成第二步"},
		{"role":"user","content":"第三步"}
	]`, "完成第三步")
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 3, session.SourceRequestCount)
	require.Len(t, session.Messages, 7)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "完成第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第二步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "完成第二步"))
	require.Contains(t, dataShareContentText(session.Messages[4]["content"]), "摘要：已经完成第一步和第二步。")
	require.Equal(t, "第三步", dataShareContentText(session.Messages[5]["content"]))
	require.Equal(t, "完成第三步", dataShareContentText(session.Messages[6]["content"]))
}

func TestDataSharingService_MessagesRawCaptureDedupesCompactionSinglePreservedRecentMessage(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})
	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformAnthropic, DataSharingEnabled: true},
	}
	capture := func(requestID string, messages string, response string) {
		require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformAnthropic,
			Model:           "claude-opus-4-8",
			SessionID:       "session-messages-compaction-single-recent",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"claude-opus-4-8","messages":` + messages + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"content":[{"type":"text","text":%q}]}`, requestID, response)),
			InboundEndpoint: "/v1/messages",
			InputTokens:     10,
			OutputTokens:    5,
		}}))
	}

	capture("messages-single-recent-1", `[
		{"role":"user","content":"第一步"}
	]`, "完成第一步")
	capture("messages-single-recent-2", `[
		{"role":"user","content":"第一步"},
		{"role":"assistant","content":"完成第一步"},
		{"role":"user","content":"第二步"}
	]`, "完成第二步")
	capture("messages-single-recent-3", `[
		{"role":"assistant","content":[{"type":"compaction","content":"摘要：已经完成第一步和第二步。"}]},
		{"role":"assistant","content":"完成第二步"},
		{"role":"user","content":"第三步"}
	]`, "完成第三步")
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 3, session.SourceRequestCount)
	require.Len(t, session.Messages, 7)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "完成第一步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "第二步"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "完成第二步"))
	require.Contains(t, dataShareContentText(session.Messages[4]["content"]), "摘要：已经完成第一步和第二步。")
	require.Equal(t, "第三步", dataShareContentText(session.Messages[5]["content"]))
	require.Equal(t, "完成第三步", dataShareContentText(session.Messages[6]["content"]))
}

func TestDataShareMessagesRequestDeltaUsesLongWindowIndexWhenIdentityIsCommon(t *testing.T) {
	existing := make([]map[string]any, 0, 6000)
	for i := 0; i < dataShareReplayWindowCandidateLimit+1; i++ {
		existing = append(existing, map[string]any{
			"role":    "user",
			"content": "重复锚点",
		})
	}
	existing = append(existing, map[string]any{"role": "user", "content": "重复锚点"})
	for i := 0; i < dataShareLongReplayMinMessages; i++ {
		existing = append(existing, map[string]any{
			"role":    "assistant",
			"content": fmt.Sprintf("历史尾部-%04d", i),
		})
	}
	incoming := []map[string]any{
		{"role": "assistant", "content": []any{map[string]any{"type": "compaction", "content": "摘要：保留最后一段历史。"}}},
	}
	incoming = append(incoming, cloneBufferedDataShareMaps(existing[len(existing)-dataShareLongReplayMinMessages-1:])...)
	incoming = append(incoming, map[string]any{"role": "user", "content": "新的问题"})

	delta := dataShareMessagesRequestDelta(existing, incoming)

	require.Len(t, delta, 2)
	require.Contains(t, dataShareContentText(delta[0]["content"]), "摘要：保留最后一段历史。")
	require.Equal(t, "新的问题", dataShareContentText(delta[1]["content"]))
}

func TestDataShareMessageIdentityKeepsStructuredContentDistinct(t *testing.T) {
	first := map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": "看这张图"},
			map[string]any{"type": "input_image", "image_url": "https://example.com/a.png"},
		},
	}
	second := map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": "看这张图"},
			map[string]any{"type": "input_image", "image_url": "https://example.com/b.png"},
		},
	}
	normalized := normalizeDataShareMessages([]map[string]any{first, second})

	require.Len(t, normalized, 2)
	require.NotEqual(t, dataShareMessageIdentity(normalized[0]), dataShareMessageIdentity(normalized[1]))
	require.IsType(t, []any{}, normalized[0]["content"])
}

func TestDataShareMessageIdentityIgnoresVolatileWrapperForStructuredContent(t *testing.T) {
	first := map[string]any{
		"type":   "message",
		"id":     "msg_first",
		"status": "completed",
		"role":   "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": "看这张图"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aGVsbG8="},
			map[string]any{"type": "input_file", "file_id": "file_abc", "filename": "notes.txt"},
		},
	}
	second := map[string]any{
		"type":   "message",
		"id":     "msg_second",
		"status": "in_progress",
		"role":   "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": "看这张图"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aGVsbG8="},
			map[string]any{"type": "input_file", "file_id": "file_abc", "filename": "notes.txt"},
		},
	}
	normalized := normalizeDataShareMessages([]map[string]any{first, second})

	require.Len(t, normalized, 2)
	require.Equal(t, dataShareMessageIdentity(normalized[0]), dataShareMessageIdentity(normalized[1]))
	require.IsType(t, []any{}, normalized[0]["content"])
}

func TestDataShareMessageIdentityKeepsNonContentSemanticFieldsDistinct(t *testing.T) {
	first := normalizeDataShareMessage(map[string]any{
		"type":              "message",
		"id":                "msg_first",
		"status":            "completed",
		"role":              "assistant",
		"content":           []any{map[string]any{"type": "output_text", "text": ""}},
		"reasoning_content": "先检查文件",
	})
	second := normalizeDataShareMessage(map[string]any{
		"type":              "message",
		"id":                "msg_second",
		"status":            "completed",
		"role":              "assistant",
		"content":           []any{map[string]any{"type": "output_text", "text": ""}},
		"reasoning_content": "改查日志",
	})

	require.NotEqual(t, dataShareMessageIdentity(first), dataShareMessageIdentity(second))
}

func TestDataSharingService_CaptureBufferIdleHotUpdateFlushesSooner(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, &dataShareSettingRepoStub{values: map[string]string{}})
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})

	require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group:   &Group{ID: gid, DataSharingEnabled: true},
		},
		Provider:        PlatformOpenAI,
		Model:           "gpt-5",
		SessionID:       "session-buffer-hot-update",
		RequestID:       "request-hot-update",
		RequestBody:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		ResponseBody:    []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`),
		InboundEndpoint: "/v1/chat/completions",
	}}))
	require.Equal(t, 0, repo.upsertCount())

	_, err := svc.UpdateCaptureRuntimeSettings(context.Background(), DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 1,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return repo.upsertCount() == 1
	}, 2*time.Second, 20*time.Millisecond)
}

func TestDataSharingCaptureBuffer_ForcedEnabledIgnoresDisabledSetting(t *testing.T) {
	var got *DataShareSession
	systemPrompt := "你是编码助手"
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Flush: func(_ context.Context, session *DataShareSession) error {
			got = session
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          false,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-bypass",
		SessionID:          "sess-bypass",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		SystemPrompt:       &systemPrompt,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages: []map[string]any{
			{"role": "system", "content": "你是编码助手"},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command"}}},
			{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
			{"role": "assistant", "content": "ok"},
		},
		Usage: map[string]any{"total_tokens": 3},
		Meta:  map[string]any{"request_id": "req-bypass"},
	}))
	require.Nil(t, got)
	require.True(t, buffer.Stats().Enabled)

	buffer.FlushAll(context.Background())
	require.NotNil(t, got)
	require.Equal(t, DataShareQualityComplete, got.QualityStatus)
	require.True(t, got.Exportable)
	require.NotEmpty(t, got.SessionJSON)
	require.Zero(t, got.StorageBytes)
}

func TestDataSharingCaptureBuffer_RetainsSessionWhenFlushFails(t *testing.T) {
	wantErr := errors.New("boom")
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Flush: func(context.Context, *DataShareSession) error {
			return wantErr
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-failed",
		SessionID:          "sess-failed",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "hi"}},
		Meta:               map[string]any{"request_id": "req-failed"},
	}))
	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-failed",
		SessionID:          "sess-failed",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "assistant", "content": "ok"}},
		Meta:               map[string]any{"request_id": "req-failed-2"},
	}))

	buffer.FlushAll(context.Background())
	stats := buffer.Stats()
	require.Equal(t, uint64(1), stats.FlushFailedTotal)
	require.Equal(t, 1, stats.BufferedSessions)
	require.Equal(t, 2, stats.PendingEvents)
	require.Contains(t, stats.LastError, "boom")
}

func TestDataSharingCaptureBuffer_RecordsLastErrorAtWhenHydrateFails(t *testing.T) {
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Hydrate: func(context.Context, string) (*DataShareSession, error) {
			return nil, errors.New("hydrate timeout")
		},
		Flush: func(context.Context, *DataShareSession) error {
			return nil
		},
	})

	err := buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-hydrate-failed",
		SessionID:          "sess-hydrate-failed",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "hi"}},
	})
	require.ErrorContains(t, err, "hydrate timeout")

	stats := buffer.Stats()
	require.Equal(t, uint64(1), stats.FlushFailedTotal)
	require.Contains(t, stats.LastError, "hydrate timeout")
	require.NotNil(t, stats.LastErrorAt)
	require.Nil(t, stats.LastSuccessAt)
}

func TestDataSharingCaptureBuffer_RecordsLastErrorAtWhenFlushQueueFull(t *testing.T) {
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		ScheduleFlush: func(DataSharingCaptureJob) DataSharingCaptureSubmitMode {
			return DataSharingCaptureSubmitModeDropped
		},
		Flush: func(context.Context, *DataShareSession) error {
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 1,
	})

	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-flush-queue-full",
		SessionID:          "sess-flush-queue-full",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "hi"}},
	}))
	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-after-flush-queue-full",
		SessionID:          "sess-after-flush-queue-full",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "again"}},
	}))

	stats := buffer.Stats()
	require.Equal(t, uint64(1), stats.FlushFailedTotal)
	require.Contains(t, stats.LastError, "flush queue is full")
	require.NotNil(t, stats.LastErrorAt)
	require.Nil(t, stats.LastSuccessAt)
}

func TestDataSharingCaptureBuffer_KeepsLastErrorAfterRecoveredFlush(t *testing.T) {
	var flushes int
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Flush: func(context.Context, *DataShareSession) error {
			flushes++
			if flushes == 1 {
				return errors.New("db reset")
			}
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-recovered",
		SessionID:          "sess-recovered",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "hi"}},
		Meta:               map[string]any{"request_id": "req-recovered"},
	}))

	buffer.FlushAll(context.Background())
	failedStats := buffer.Stats()
	require.Equal(t, uint64(1), failedStats.FlushFailedTotal)
	require.Contains(t, failedStats.LastError, "db reset")
	require.NotNil(t, failedStats.LastErrorAt)
	require.Nil(t, failedStats.LastSuccessAt)

	buffer.FlushAll(context.Background())
	recoveredStats := buffer.Stats()
	require.Equal(t, uint64(1), recoveredStats.FlushFailedTotal)
	require.Equal(t, uint64(1), recoveredStats.FlushSuccessTotal)
	require.Contains(t, recoveredStats.LastError, "db reset")
	require.NotNil(t, recoveredStats.LastErrorAt)
	require.NotNil(t, recoveredStats.LastSuccessAt)
	require.False(t, recoveredStats.LastSuccessAt.Before(*recoveredStats.LastErrorAt))
	require.Equal(t, 0, recoveredStats.BufferedSessions)
}

func TestDataSharingCaptureBuffer_HydratesColdSessionBeforeMerge(t *testing.T) {
	systemPrompt := "你是编码助手"
	var got *DataShareSession
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Hydrate: func(_ context.Context, key string) (*DataShareSession, error) {
			require.Equal(t, "traj-cold", key)
			return &DataShareSession{
				TrajectoryID:       "traj-cold",
				ID:                 99,
				SessionID:          "sess-cold",
				Dataset:            defaultDataShareDataset,
				Provider:           PlatformOpenAI,
				Model:              "gpt-5",
				SourceRequestCount: 1,
				SystemPrompt:       &systemPrompt,
				Messages:           []map[string]any{{"role": "user", "content": "old"}},
				Usage:              map[string]any{"input_tokens": 3},
				InputTokens:        3,
				ActualCost:         float64Ptr(1.25),
			}, nil
		},
		Flush: func(_ context.Context, session *DataShareSession) error {
			got = cloneBufferedDataShareSession(session)
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-cold",
		SessionID:          "sess-cold",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "assistant", "content": "new"}},
		Usage:              map[string]any{"input_tokens": 7},
		InputTokens:        7,
		ActualCost:         float64Ptr(0),
	}))
	buffer.FlushAll(context.Background())

	require.NotNil(t, got)
	require.Equal(t, 2, got.SourceRequestCount)
	require.Equal(t, int64(10), got.InputTokens)
	require.NotNil(t, got.ActualCost)
	require.InDelta(t, 1.25, *got.ActualCost, 1e-12)
	require.Len(t, got.Messages, 2)
}

func TestDataSharingCaptureBuffer_KeepsLegacyUnknownActualCostNull(t *testing.T) {
	var got *DataShareSession
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Hydrate: func(_ context.Context, key string) (*DataShareSession, error) {
			require.Equal(t, "traj-legacy-cost", key)
			return &DataShareSession{
				ID:                 100,
				TrajectoryID:       "traj-legacy-cost",
				SessionID:          "sess-legacy-cost",
				Dataset:            defaultDataShareDataset,
				Provider:           PlatformOpenAI,
				Model:              "gpt-5",
				SourceRequestCount: 1,
				Messages:           []map[string]any{{"role": "user", "content": "old"}},
				InputTokens:        3,
				ActualCost:         nil,
			}, nil
		},
		Flush: func(_ context.Context, session *DataShareSession) error {
			got = cloneBufferedDataShareSession(session)
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-legacy-cost",
		SessionID:          "sess-legacy-cost",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "assistant", "content": "new"}},
		InputTokens:        7,
		ActualCost:         float64Ptr(0.75),
	}))
	buffer.FlushAll(context.Background())

	require.NotNil(t, got)
	require.Nil(t, got.ActualCost)
	require.Equal(t, int64(10), got.InputTokens)
}

func TestDataSharingCaptureBuffer_FlushAllWaitsForHydrate(t *testing.T) {
	hydrateStarted := make(chan struct{})
	releaseHydrate := make(chan struct{})
	flushDone := make(chan struct{})
	var flushed *DataShareSession
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Hydrate: func(_ context.Context, key string) (*DataShareSession, error) {
			require.Equal(t, "traj-wait-hydrate", key)
			close(hydrateStarted)
			<-releaseHydrate
			return nil, ErrDataShareSessionNotFound
		},
		Flush: func(_ context.Context, session *DataShareSession) error {
			flushed = cloneBufferedDataShareSession(session)
			close(flushDone)
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	submitDone := make(chan error, 1)
	go func() {
		submitDone <- buffer.Submit(context.Background(), &DataShareSession{
			TrajectoryID:       "traj-wait-hydrate",
			SessionID:          "sess-wait-hydrate",
			Dataset:            defaultDataShareDataset,
			Provider:           PlatformOpenAI,
			Model:              "gpt-5",
			SourceRequestCount: 1,
			Messages:           []map[string]any{{"role": "user", "content": "after hydrate"}},
		})
	}()
	<-hydrateStarted

	flushAllDone := make(chan struct{})
	go func() {
		buffer.FlushAll(context.Background())
		close(flushAllDone)
	}()

	select {
	case <-flushAllDone:
		t.Fatal("FlushAll returned before hydrate completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseHydrate)
	require.NoError(t, <-submitDone)
	select {
	case <-flushDone:
	case <-time.After(time.Second):
		t.Fatal("FlushAll did not flush after hydrate completed")
	}
	select {
	case <-flushAllDone:
	case <-time.After(time.Second):
		t.Fatal("FlushAll did not return after flush")
	}
	require.NotNil(t, flushed)
	require.Equal(t, "after hydrate", flushed.Messages[0]["content"])
}

func TestDataSharingCaptureBuffer_MergesNewEventsWhileFlushInFlight(t *testing.T) {
	flushStarted := make(chan struct{})
	releaseFlush := make(chan struct{})
	var flushed []*DataShareSession
	buffer := NewDataSharingCaptureBuffer(DataSharingCaptureBufferOptions{
		Flush: func(_ context.Context, session *DataShareSession) error {
			flushed = append(flushed, cloneBufferedDataShareSession(session))
			if len(flushed) == 1 {
				close(flushStarted)
				<-releaseFlush
			}
			return nil
		},
	})
	buffer.UpdateRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      16,
		BufferMaxPendingEvents: 16,
	})

	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-inflight",
		SessionID:          "sess-inflight",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "first"}},
		Meta:               map[string]any{"request_id": "first"},
	}))
	buffer.mu.Lock()
	buffer.startFlushLocked(buffer.entries["traj-inflight"], context.Background(), true)
	buffer.mu.Unlock()

	select {
	case <-flushStarted:
	case <-time.After(time.Second):
		t.Fatal("flush did not start")
	}
	require.NoError(t, buffer.Submit(context.Background(), &DataShareSession{
		TrajectoryID:       "traj-inflight",
		SessionID:          "sess-inflight",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5",
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "assistant", "content": "second"}},
		Meta:               map[string]any{"request_id": "second"},
	}))
	close(releaseFlush)
	buffer.flushWG.Wait()

	stats := buffer.Stats()
	require.Equal(t, 1, stats.BufferedSessions)
	require.Equal(t, 1, stats.PendingEvents)
	buffer.FlushAll(context.Background())
	require.Len(t, flushed, 2)
	require.Equal(t, 2, flushed[1].SourceRequestCount)
	require.Len(t, flushed[1].Messages, 2)
	require.Equal(t, "first", flushed[1].Messages[0]["content"])
	require.Equal(t, "second", flushed[1].Messages[1]["content"])
}

func TestBuildSessionUsesActualUpstreamModel(t *testing.T) {
	gid := int64(12)
	svc := NewDataSharingService(nil, nil)

	session := svc.buildSession(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group: &Group{
				ID:                 gid,
				Platform:           PlatformOpenAI,
				DataSharingEnabled: true,
			},
		},
		Provider:        PlatformOpenAI,
		Model:           "gpt-5-alias",
		UpstreamModel:   "gpt-5-2026-05-01",
		SessionID:       "session-1",
		RequestID:       "request-1",
		RequestBody:     []byte(`{"model":"gpt-5-alias","messages":[{"role":"system","content":"你是编码助手"},{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"README.md"}],"tools":[{"type":"function","function":{"name":"exec_command","description":"运行命令","parameters":{"type":"object"}}}]}`),
		ResponseBody:    []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"id":"resp_1"}`),
		InboundEndpoint: "v1/chat/completions",
		UserAgent:       "codex-cli/1.0",
		InputTokens:     10,
		OutputTokens:    5,
	})

	if session.Model != "gpt-5-2026-05-01" {
		t.Fatalf("model = %q, want actual upstream model", session.Model)
	}
	if got := session.SessionJSON["model"]; got != "gpt-5-2026-05-01" {
		t.Fatalf("session_json.model = %v, want actual upstream model", got)
	}
	if got := session.Meta["requested_model"]; got != "gpt-5-alias" {
		t.Fatalf("meta.requested_model = %v, want client requested model", got)
	}
	if got := session.RequestPath; got != "/v1/chat/completions" {
		t.Fatalf("request_path = %q, want normalized inbound path", got)
	}
	if got := session.SessionJSON["request_path"]; got != "/v1/chat/completions" {
		t.Fatalf("session_json.request_path = %v, want normalized inbound path", got)
	}
	if got := session.Meta["inbound_endpoint"]; got != "/v1/chat/completions" {
		t.Fatalf("meta.inbound_endpoint = %v, want normalized inbound path", got)
	}
	if got := session.UserAgent; got != "codex-cli" {
		t.Fatalf("user_agent = %q, want captured client user agent", got)
	}
	if got := session.SessionJSON["user_agent"]; got != "codex-cli" {
		t.Fatalf("session_json.user_agent = %v, want captured client user agent", got)
	}
	if got := session.Meta["user_agent"]; got != "codex-cli/1.0" {
		t.Fatalf("meta.user_agent = %v, want captured client user agent", got)
	}
	if got := session.Meta["user_agent_family"]; got != "codex-cli" {
		t.Fatalf("meta.user_agent_family = %v, want normalized client family", got)
	}
	if session.Exportable != true {
		t.Fatalf("exportable = false, quality_errors = %v", session.QualityErrors)
	}
}

func TestBuildSessionCapturesOpenAIResponsesInputAndOutput(t *testing.T) {
	gid := int64(12)
	svc := NewDataSharingService(nil, nil)

	session := svc.buildSession(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group: &Group{
				ID:                 gid,
				Platform:           PlatformOpenAI,
				DataSharingEnabled: true,
			},
		},
		Provider: PlatformOpenAI,
		Model:    "gpt-5.5",
		RequestBody: []byte(`{
			"model":"gpt-5.5",
				"input":[
					{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
					{"type":"message","role":"user","content":[{"type":"input_text","text":"请列目录"}]},
					{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
					{"type":"function_call_output","call_id":"call_1","output":"README.md"}
				],
			"tools":[{"type":"function","name":"exec_command","description":"运行命令","parameters":{"type":"object"}}]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_1",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"看到了 README.md"}]}],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`),
		InputTokens:  10,
		OutputTokens: 5,
	})

	if len(session.Messages) != 5 {
		t.Fatalf("message count = %d, want 5: %#v", len(session.Messages), session.Messages)
	}
	if got := session.Messages[0]["role"]; got != "system" {
		t.Fatalf("first role = %v, want system", got)
	}
	if got := session.Messages[2]["role"]; got != "assistant" {
		t.Fatalf("function_call role = %v, want assistant", got)
	}
	if calls, ok := session.Messages[2]["tool_calls"].([]map[string]any); !ok || len(calls) != 1 || calls[0]["id"] != "call_1" || calls[0]["name"] != "exec_command" {
		t.Fatalf("tool_calls not normalized: %#v", session.Messages[2]["tool_calls"])
	}
	if got := session.Messages[3]["role"]; got != "tool" {
		t.Fatalf("function_call_output role = %v, want tool", got)
	}
	if got := session.Messages[3]["status"]; got != "success" {
		t.Fatalf("tool status = %v, want success", got)
	}
	if got := session.Messages[3]["is_error"]; got != false {
		t.Fatalf("tool is_error = %v, want false", got)
	}
	if got := session.Messages[4]["role"]; got != "assistant" {
		t.Fatalf("response role = %v, want assistant", got)
	}
	if session.SystemPrompt == nil || *session.SystemPrompt == "" {
		t.Fatalf("system_prompt missing")
	}
	if got := session.Meta["source_request_ids"]; got == nil {
		t.Fatalf("source_request_ids missing")
	}
	if !session.Exportable {
		t.Fatalf("exportable = false, quality_errors = %v", session.QualityErrors)
	}
	if session.QualityStatus != DataShareQualityComplete {
		t.Fatalf("quality_status = %q, want complete", session.QualityStatus)
	}
}

func TestBuildSessionCapturesAnthropicResponseBody(t *testing.T) {
	gid := int64(12)
	svc := NewDataSharingService(nil, nil)

	session := svc.buildSession(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group: &Group{
				ID:                 gid,
				Platform:           PlatformAnthropic,
				DataSharingEnabled: true,
			},
		},
		Provider: PlatformAnthropic,
		Model:    "claude-sonnet-4-5-20250929",
		RequestBody: []byte(`{
			"model":"claude-sonnet-4-5-20250929",
			"system":"你是编码助手",
			"messages":[
				{"role":"user","content":"列目录"},
				{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"exec_command","input":{"cmd":"ls"}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"README.md"}]}
			],
			"tools":[{"name":"exec_command","description":"运行命令","input_schema":{"type":"object"}}]
		}`),
		ResponseBody: []byte(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"content":[{"type":"text","text":"看到了 README.md"}],
			"usage":{"input_tokens":10,"output_tokens":5}
		}`),
		InboundEndpoint: "/v1/messages",
		InputTokens:     10,
		OutputTokens:    5,
	})

	if got := session.RequestPath; got != "/v1/messages" {
		t.Fatalf("request_path = %q, want /v1/messages", got)
	}
	if len(session.Messages) != 4 {
		t.Fatalf("message count = %d, want 4: %#v", len(session.Messages), session.Messages)
	}
	if got := session.Messages[1]["role"]; got != "assistant" {
		t.Fatalf("tool_use role = %v, want assistant", got)
	}
	calls, ok := session.Messages[1]["tool_calls"].([]map[string]any)
	if !ok || len(calls) != 1 || calls[0]["id"] != "toolu_1" || calls[0]["name"] != "exec_command" {
		t.Fatalf("tool_calls not normalized: %#v", session.Messages[1]["tool_calls"])
	}
	if got := session.Messages[2]["role"]; got != "tool" {
		t.Fatalf("tool_result role = %v, want tool", got)
	}
	if got := session.Messages[2]["tool_call_id"]; got != "toolu_1" {
		t.Fatalf("tool_call_id = %v, want toolu_1", got)
	}
	if got := session.Messages[3]["role"]; got != "assistant" {
		t.Fatalf("response role = %v, want assistant", got)
	}
	if got := dataShareContentText(session.Messages[3]["content"]); got != "看到了 README.md" {
		t.Fatalf("response content = %q, want assistant text", got)
	}
	if !session.Exportable {
		t.Fatalf("exportable = false, quality_errors = %v", session.QualityErrors)
	}
}

func TestAnthropicStreamAccumulatorBuildsFinalMessage(t *testing.T) {
	acc := &anthropicStreamResponseAccumulator{}
	acc.ObserveData("", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":10}}}`)
	acc.ObserveData("", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"exec_command","input":{}}}`)
	acc.ObserveData("", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\""}}`)
	acc.ObserveData("", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"ls\"}"}}`)
	acc.ObserveData("", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
	acc.ObserveData("", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"看到了 README.md"}}`)
	body := acc.ObserveData("", `{"type":"message_delta","usage":{"output_tokens":5},"delta":{"stop_reason":"end_turn"}}`)

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid accumulated body: %v", err)
	}
	messages := normalizeDataShareMessages([]map[string]any{got})
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1: %#v", len(messages), messages)
	}
	calls, ok := messages[0]["tool_calls"].([]map[string]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls not normalized: %#v", messages[0]["tool_calls"])
	}
	if calls[0]["id"] != "toolu_1" || calls[0]["name"] != "exec_command" {
		t.Fatalf("tool call mismatch: %#v", calls[0])
	}
	args, ok := calls[0]["arguments"].(map[string]any)
	if !ok || args["cmd"] != "ls" {
		t.Fatalf("tool arguments mismatch: %#v", calls[0]["arguments"])
	}
	if got := dataShareContentText(got["content"]); got != "看到了 README.md" {
		t.Fatalf("content text = %q, want assistant text", got)
	}
}

func TestBuildSessionFiltersOrdinaryResponsesChat(t *testing.T) {
	gid := int64(12)
	svc := NewDataSharingService(nil, nil)

	session := svc.buildSession(DataShareCaptureInput{
		APIKey: &APIKey{
			ID:      34,
			UserID:  56,
			GroupID: &gid,
			Group: &Group{
				ID:                 gid,
				Platform:           PlatformOpenAI,
				DataSharingEnabled: true,
			},
		},
		Provider: PlatformOpenAI,
		Model:    "gpt-5.5",
		RequestBody: []byte(`{
			"model":"gpt-5.5",
			"input":[
				{"type":"message","role":"system","content":[{"type":"input_text","text":"你是编码助手"}]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
			],
			"tools":[{"type":"function","name":"exec_command","description":"运行命令","parameters":{"type":"object"}}]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_ordinary",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`),
		InputTokens:  10,
		OutputTokens: 5,
	})

	if session.Exportable {
		t.Fatalf("ordinary no-tool session should not be exportable")
	}
	if session.QualityStatus != DataShareQualityInvalid {
		t.Fatalf("quality_status = %q, want invalid", session.QualityStatus)
	}
	if !containsString(session.QualityErrors, "missing_structured_tool_call") {
		t.Fatalf("quality_errors = %v, want missing_structured_tool_call", session.QualityErrors)
	}
}

func TestCaptureSkipRulesDefaultMatching(t *testing.T) {
	ctx := context.Background()
	svc := NewDataSharingService(nil, &dataShareSettingRepoStub{values: map[string]string{}})

	cases := []struct {
		name  string
		input DataShareCaptureInput
		want  bool
	}{
		{
			name: "Claude Code title generator",
			input: DataShareCaptureInput{
				UserAgent:       "claude-cli/2.1.142 (external, cli)",
				InboundEndpoint: "/v1/messages",
				RequestBody: []byte(`{
					"model":"gpt-5.5",
					"system":[
						{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."},
						{"type":"text","text":"Generate a concise, sentence-case title (3-7 words) that captures the main topic."}
					],
					"messages":[{"role":"user","content":"<session>看看 Documents 里面有什么</session>"}]
				}`),
			},
			want: true,
		},
		{
			name: "opencode chat completions title generator",
			input: DataShareCaptureInput{
				UserAgent:       "opencode/0.7.0",
				InboundEndpoint: "/v1/chat/completions",
				RequestBody: []byte(`{
					"model":"claude-sonnet-4-5",
					"messages":[
						{"role":"system","content":"You are a title generator. You output ONLY a thread title. Nothing else.\nGenerate a brief title that would help the user find this conversation later.\nNEVER respond to questions, just generate a title for the conversation"},
						{"role":"user","content":"Generate a title for this conversation:"},
						{"role":"user","content":"hi"}
					]
				}`),
			},
			want: true,
		},
		{
			name: "opencode messages title generator",
			input: DataShareCaptureInput{
				UserAgent:       "opencode/0.7.0",
				InboundEndpoint: "/v1/messages",
				RequestBody: []byte(`{
					"model":"claude-sonnet-4-5",
					"system":"You are a title generator. You output ONLY a thread title. Nothing else.",
					"messages":[{"role":"user","content":"Generate a title for this conversation:\n\nhi"}]
				}`),
			},
			want: true,
		},
		{
			name: "opencode responses title generator",
			input: DataShareCaptureInput{
				UserAgent:       "opencode/1.15.11 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14",
				InboundEndpoint: "/v1/responses",
				RequestBody: []byte(`{
					"model":"gpt-5.5",
					"input":[
						{"role":"system","content":"You are a title generator. You output ONLY a thread title. Nothing else.\n\n<task>\nGenerate a brief title that would help the user find this conversation later.\n</task>\n\n<rules>\n- NEVER respond to questions, just generate a title for the conversation\n</rules>"},
						{"role":"user","content":"Generate a title for this conversation:\n"},
						{"role":"user","content":"hi"}
					]
				}`),
			},
			want: true,
		},
		{
			name: "opencode normal task",
			input: DataShareCaptureInput{
				UserAgent:       "opencode/0.7.0",
				InboundEndpoint: "/v1/messages",
				RequestBody:     []byte(`{"model":"claude-sonnet-4-5","system":"你是编码助手","messages":[{"role":"user","content":"帮我检查这个函数"}]}`),
			},
			want: false,
		},
		{
			name: "default excluded requested model",
			input: DataShareCaptureInput{
				UserAgent:       "codex-cli/1.0",
				InboundEndpoint: "/v1/responses",
				Model:           "gpt-5.4-mini",
				UpstreamModel:   "gpt-5.4-mini-openai-compact",
				RequestBody:     []byte(`{"model":"gpt-5.4-mini","input":"帮我检查这个函数"}`),
			},
			want: true,
		},
		{
			name: "default excluded model from request body",
			input: DataShareCaptureInput{
				UserAgent:       "codex-cli/1.0",
				InboundEndpoint: "/v1/responses",
				RequestBody:     []byte(`{"model":"codex-auto-review","input":"review this change"}`),
			},
			want: true,
		},
		{
			name: "ordinary title request",
			input: DataShareCaptureInput{
				UserAgent:       "curl/8.0",
				InboundEndpoint: "/v1/chat/completions",
				RequestBody:     []byte(`{"model":"gpt-5.5","messages":[{"role":"system","content":"你是写作助手"},{"role":"user","content":"帮我写一个标题"}]}`),
			},
			want: false,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := svc.shouldSkipDataShareCapture(ctx, tt.input); got != tt.want {
				t.Fatalf("shouldSkipDataShareCapture = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCaptureSkipRulesFallbackAndUpdate(t *testing.T) {
	ctx := context.Background()
	repo := &dataShareSettingRepoStub{values: map[string]string{
		SettingKeyDataSharingCaptureSkipRules: "{not-json",
	}}
	svc := NewDataSharingService(nil, repo)

	rules, err := svc.GetCaptureSkipRules(ctx)
	if err != nil {
		t.Fatalf("GetCaptureSkipRules error = %v", err)
	}
	if len(rules) == 0 || rules[0].ID != "claude_code_title" {
		t.Fatalf("rules fallback mismatch: %#v", rules)
	}

	custom := []DataShareCaptureSkipRule{{
		ID:           "custom_warmup",
		Name:         "自定义预热",
		Enabled:      true,
		RequestPaths: []string{"v1/responses"},
		FieldScopes:  []string{"input"},
		Patterns:     []string{"Warmup"},
		MatchMode:    "equals",
	}}
	updated, err := svc.UpdateCaptureSkipRules(ctx, custom)
	if err != nil {
		t.Fatalf("UpdateCaptureSkipRules error = %v", err)
	}
	if len(updated) != 1 || updated[0].RequestPaths[0] != "/v1/responses" {
		t.Fatalf("updated rules mismatch: %#v", updated)
	}
	if repo.values[SettingKeyDataSharingCaptureSkipRules] == "" {
		t.Fatalf("rules were not persisted")
	}
	if !svc.shouldSkipDataShareCapture(ctx, DataShareCaptureInput{
		UserAgent:       "custom-client/1.0",
		InboundEndpoint: "/v1/responses",
		RequestBody:     []byte(`{"model":"gpt-5.5","input":"Warmup"}`),
	}) {
		t.Fatalf("custom warmup rule should skip matching request")
	}

	modelOnly := []DataShareCaptureSkipRule{{
		ID:      "custom_model",
		Name:    "自定义模型",
		Enabled: true,
		Models:  []string{"codex-auto-review"},
	}}
	updated, err = svc.UpdateCaptureSkipRules(ctx, modelOnly)
	if err != nil {
		t.Fatalf("UpdateCaptureSkipRules model-only error = %v", err)
	}
	if len(updated) != 1 || len(updated[0].Models) != 1 || updated[0].Models[0] != "codex-auto-review" {
		t.Fatalf("model-only rule mismatch: %#v", updated)
	}
	if !svc.shouldSkipDataShareCapture(ctx, DataShareCaptureInput{
		UserAgent:       "custom-client/1.0",
		InboundEndpoint: "/v1/responses",
		RequestBody:     []byte(`{"model":"codex-auto-review","input":"real task"}`),
	}) {
		t.Fatalf("custom model-only rule should skip matching request")
	}
}

func TestDataShareExportTicketRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := &dataShareSettingRepoStub{values: map[string]string{}}
	svc := NewDataSharingService(nil, repo)

	ticket, err := svc.CreateExportTicket(ctx, DataShareExportTicketRequest{
		Scope:    DataShareExportScopeUser,
		UserID:   42,
		Filters:  DataShareSessionFilters{UserID: 42, IDs: []int64{1, 2}},
		Filename: "my-export",
	})
	if err != nil {
		t.Fatalf("CreateExportTicket error = %v", err)
	}
	if !strings.HasSuffix(ticket.Filename, ".jsonl.zst") {
		t.Fatalf("filename = %q, want .jsonl.zst suffix", ticket.Filename)
	}
	if !strings.Contains(ticket.DownloadURL, "/data-sharing/export/download?ticket=") {
		t.Fatalf("download_url = %q", ticket.DownloadURL)
	}
	if repo.values[SettingKeyDataSharingExportTicketKey] == "" {
		t.Fatalf("ticket signing key was not persisted")
	}

	claims, err := svc.ParseExportTicket(ctx, DataShareExportScopeUser, ticket.Token)
	if err != nil {
		t.Fatalf("ParseExportTicket error = %v", err)
	}
	if claims.UserID != 42 || len(claims.Filters.IDs) != 2 {
		t.Fatalf("claims mismatch: %#v", claims)
	}
}

func TestDataShareExportTicketFilenameEncoding(t *testing.T) {
	ctx := context.Background()
	svc := NewDataSharingService(nil, &dataShareSettingRepoStub{values: map[string]string{}})

	zstdTicket, err := svc.CreateExportTicket(ctx, DataShareExportTicketRequest{
		Scope:    DataShareExportScopeAdmin,
		Filters:  DataShareSessionFilters{IDs: []int64{1}},
		Filename: "admin-data-sharing.jsonl",
	})
	if err != nil {
		t.Fatalf("CreateExportTicket zstd error = %v", err)
	}
	if zstdTicket.Filename != "admin-data-sharing.jsonl.zst" {
		t.Fatalf("zstd filename = %q, want admin-data-sharing.jsonl.zst", zstdTicket.Filename)
	}
	if zstdTicket.Encoding != string(DataShareExportEncodingZstd) {
		t.Fatalf("zstd encoding = %q", zstdTicket.Encoding)
	}

	plainTicket, err := svc.CreateExportTicket(ctx, DataShareExportTicketRequest{
		Scope:    DataShareExportScopeAdmin,
		Filters:  DataShareSessionFilters{IDs: []int64{1}},
		Filename: "admin-data-sharing-session-1.jsonl.zst",
		Encoding: DataShareExportEncodingJSONL,
	})
	if err != nil {
		t.Fatalf("CreateExportTicket jsonl error = %v", err)
	}
	if plainTicket.Filename != "admin-data-sharing-session-1.jsonl" {
		t.Fatalf("plain filename = %q, want admin-data-sharing-session-1.jsonl", plainTicket.Filename)
	}
	if plainTicket.Encoding != string(DataShareExportEncodingJSONL) {
		t.Fatalf("plain encoding = %q", plainTicket.Encoding)
	}

	jsonTicket, err := svc.CreateExportTicket(ctx, DataShareExportTicketRequest{
		Scope:    DataShareExportScopeAdmin,
		Filters:  DataShareSessionFilters{IDs: []int64{1}},
		Filename: "admin-data-sharing-session-1.jsonl.zst",
		Encoding: DataShareExportEncodingJSON,
	})
	if err != nil {
		t.Fatalf("CreateExportTicket json error = %v", err)
	}
	if jsonTicket.Filename != "admin-data-sharing-session-1.json" {
		t.Fatalf("json filename = %q, want admin-data-sharing-session-1.json", jsonTicket.Filename)
	}
	if jsonTicket.Encoding != string(DataShareExportEncodingJSON) {
		t.Fatalf("json encoding = %q", jsonTicket.Encoding)
	}
}

func TestDataShareExportTicketRejectsScopeMismatch(t *testing.T) {
	ctx := context.Background()
	svc := NewDataSharingService(nil, &dataShareSettingRepoStub{values: map[string]string{}})

	ticket, err := svc.CreateExportTicket(ctx, DataShareExportTicketRequest{
		Scope:   DataShareExportScopeAdmin,
		Filters: DataShareSessionFilters{IDs: []int64{1}},
	})
	if err != nil {
		t.Fatalf("CreateExportTicket error = %v", err)
	}
	if _, err := svc.ParseExportTicket(ctx, DataShareExportScopeUser, ticket.Token); err == nil {
		t.Fatalf("ParseExportTicket should reject scope mismatch")
	}
}

func TestDataShareExportTicketRejectsUserFilterMismatch(t *testing.T) {
	ctx := context.Background()
	svc := NewDataSharingService(nil, &dataShareSettingRepoStub{values: map[string]string{}})

	_, err := svc.CreateExportTicket(ctx, DataShareExportTicketRequest{
		Scope:   DataShareExportScopeUser,
		UserID:  42,
		Filters: DataShareSessionFilters{UserID: 7, IDs: []int64{1}},
	})
	if err == nil {
		t.Fatalf("CreateExportTicket should reject mismatched user filter")
	}
}

func TestExportPayloadNormalizesLegacyRecord(t *testing.T) {
	sys := ""
	session := &DataShareSession{
		TrajectoryID:       "traj",
		SessionID:          "sess",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		SystemPrompt:       &sys,
		Tools: []map[string]any{
			{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}, "type": "function"},
			{"name": "apply_patch", "description": "Use the apply_patch tool", "type": "custom"},
			{"name": "mcp__node_repl__", "description": "Node namespace", "type": "namespace", "tools": []any{
				map[string]any{"name": "js", "description": "运行 JavaScript", "parameters": map[string]any{"type": "object"}, "type": "function"},
			}},
			{"type": "web_search"},
		},
		Messages: []map[string]any{
			{"role": "system", "content": "你是编码助手"},
			{"role": "user", "content": "列目录"},
			{"role": "assistant", "type": "function_call", "call_id": "call_1", "name": "exec_command", "arguments": `{"cmd":"ls"}`},
			{"role": "tool", "type": "function_call_output", "call_id": "call_1", "output": "README.md\nProcess exited with code 0"},
			{"role": "assistant", "content": "看到了 README.md"},
		},
		Usage: map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_read_tokens": 2, "total_tokens": 17},
		Meta:  map[string]any{"request_id": "req_1"},
	}

	payload := exportPayloadFromSession(session)
	if got := payload["system_prompt"]; got != "你是编码助手" {
		t.Fatalf("system_prompt = %v", got)
	}
	tools, ok := payload["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("tools type = %T", payload["tools"])
	}
	for _, tool := range tools {
		if tool["name"] == "" || tool["description"] == "" || tool["parameters"] == nil {
			t.Fatalf("invalid tool after normalize: %#v", tool)
		}
	}
	messages, ok := payload["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages type = %T", payload["messages"])
	}
	calls, ok := messages[2]["tool_calls"].([]map[string]any)
	if !ok {
		t.Fatalf("tool_calls type = %T", messages[2]["tool_calls"])
	}
	if calls[0]["id"] != "call_1" || calls[0]["name"] != "exec_command" {
		t.Fatalf("tool call not normalized: %#v", calls[0])
	}
	if messages[3]["status"] != "success" || messages[3]["is_error"] != false {
		t.Fatalf("tool result not normalized: %#v", messages[3])
	}
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta type = %T", payload["meta"])
	}
	sourceIDs, ok := meta["source_request_ids"].([]string)
	if !ok {
		t.Fatalf("source_request_ids type = %T", meta["source_request_ids"])
	}
	if len(sourceIDs) != 1 || sourceIDs[0] != "req_1" {
		t.Fatalf("source_request_ids = %#v", sourceIDs)
	}
	if errs := validateDataSharePayloadQuality(payload); len(errs) != 0 {
		t.Fatalf("payload quality errors = %v", errs)
	}
}

func TestExportPayloadDedupesRepeatedResponsesHistory(t *testing.T) {
	sys := "你是编码助手"
	session := &DataShareSession{
		TrajectoryID:       "traj",
		SessionID:          "sess",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 7,
		SystemPrompt:       &sys,
		Tools: []map[string]any{
			{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}, "type": "function"},
		},
		Messages: []map[string]any{
			{"role": "system", "content": sys},
			{"role": "user", "content": "列目录"},
			{"role": "assistant", "content": "", "finish_reason": "tool_calls", "tool_calls": []map[string]any{{"id": "call_dup", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
			{"role": "tool", "tool_call_id": "call_dup", "content": "README.md", "status": "success", "is_error": false},
			{"role": "assistant", "content": "", "finish_reason": "tool_calls", "tool_calls": []map[string]any{{"id": "call_dup", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
			{"role": "tool", "tool_call_id": "call_dup", "content": "README.md", "status": "success", "is_error": false},
			{"role": "assistant", "content": "看到了 README.md"},
		},
		Usage: map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		Meta:  map[string]any{"source_request_ids": []any{"req_1", "req_2"}},
	}

	payload := exportPayloadFromSession(session)
	messages, ok := payload["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages type = %T", payload["messages"])
	}
	callCount := 0
	resultCount := 0
	for _, msg := range messages {
		callCount += len(anySlice(msg["tool_calls"]))
		if msg["role"] == "tool" {
			resultCount++
		}
	}
	if callCount != 1 || resultCount != 1 {
		t.Fatalf("dedupe failed, callCount=%d resultCount=%d messages=%#v", callCount, resultCount, messages)
	}
	if errs := validateDataSharePayloadQuality(payload); len(errs) != 0 {
		t.Fatalf("payload quality errors = %v", errs)
	}
}

func TestCompactDataShareMessagesKeepsUnfinishedTail(t *testing.T) {
	sys := map[string]any{"role": "system", "content": "你是编码助手"}
	user := map[string]any{"role": "user", "content": "列目录"}
	firstCall := map[string]any{"role": "assistant", "content": "", "finish_reason": "tool_calls", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}}
	firstResult := map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "README.md", "status": "success", "is_error": false}
	final := map[string]any{"role": "assistant", "content": "看到了 README.md"}
	tailCall := map[string]any{"role": "assistant", "content": "", "finish_reason": "tool_calls", "tool_calls": []map[string]any{{"id": "call_2", "name": "exec_command", "arguments": map[string]any{"cmd": "pwd"}}}}
	messages := []map[string]any{sys, user, firstCall, firstResult, sys, user, firstCall, firstResult, final, tailCall}

	compact := CompactDataShareMessages(messages)
	if len(compact) != 6 {
		t.Fatalf("compact len = %d, want 6: %#v", len(compact), compact)
	}
	if compact[5]["tool_calls"] == nil {
		t.Fatalf("unfinished tail call should be retained")
	}
	errs := ValidateDataShareSessionQuality("gpt-5.5", "你是编码助手", compact, []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}}, map[string]any{"total_tokens": 1})
	if !containsString(errs, "tool_call_result_unpaired") {
		t.Fatalf("quality_errors = %v, want unfinished tail to remain unpaired", errs)
	}
	if status := DataSharePayloadQualityStatus("gpt-5.5", "你是编码助手", compact, []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}}, map[string]any{"total_tokens": 1}); status != DataShareQualityPartial {
		t.Fatalf("quality_status = %q, want partial", status)
	}
}

func TestCompactDataShareMessagesKeepsRealRepeatedPrefix(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "ok"},
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "ok"},
	}

	compact := CompactDataShareMessages(messages)
	require.Len(t, compact, 4)
	require.Equal(t, "hi", dataShareContentText(compact[2]["content"]))
	require.Equal(t, "ok", dataShareContentText(compact[3]["content"]))
}

func TestCompactDataShareMessagesDedupesSystemEnvironmentReplay(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
		{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd><shell>bash</shell></environment_context>"},
		{"role": "user", "content": "修复按钮"},
		{"role": "assistant", "content": "我先检查代码"},
		{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
		{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd><shell>bash</shell></environment_context>"},
		{"role": "user", "content": "修复按钮"},
		{"role": "assistant", "content": "我先检查代码"},
		{"role": "user", "content": "继续修"},
		{"role": "assistant", "content": "开始修改"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 6)
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "修复按钮"))
	require.Equal(t, "继续修", dataShareContentText(compact[4]["content"]))
	require.Equal(t, "开始修改", dataShareContentText(compact[5]["content"]))
}

func TestCompactDataShareMessagesDedupesSystemReplayPrefixesAtSafeLengths(t *testing.T) {
	base := []map[string]any{
		{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
		{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd></environment_context>"},
		{"role": "user", "content": "修复按钮"},
		{"role": "assistant", "content": "我先检查代码"},
		{"role": "user", "content": "继续修"},
	}
	for _, prefixLen := range []int{3, 4, 5} {
		t.Run(fmt.Sprintf("prefix_%d", prefixLen), func(t *testing.T) {
			messages := append(cloneBufferedDataShareMaps(base[:prefixLen]), cloneBufferedDataShareMaps(base[:prefixLen])...)
			messages = append(messages, map[string]any{"role": "assistant", "content": "开始修改"})

			compact := CompactDataShareMessages(messages)

			require.Len(t, compact, prefixLen+1)
			require.Equal(t, 1, countDataShareMessagesWithContent(compact, "修复按钮"))
			require.Equal(t, "开始修改", dataShareContentText(compact[len(compact)-1]["content"]))
		})
	}
}

func TestCompactDataShareMessagesDedupesUserHistoryReplay(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "第一步"},
		{"role": "user", "content": "第二步"},
		{"role": "user", "content": "第三步"},
		{"role": "user", "content": "第四步"},
		{"role": "user", "content": "第五步"},
		{"role": "user", "content": "第一步"},
		{"role": "user", "content": "第二步"},
		{"role": "user", "content": "第三步"},
		{"role": "user", "content": "第四步"},
		{"role": "user", "content": "第五步"},
		{"role": "assistant", "content": "我来处理"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 6)
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "第一步"))
	require.Equal(t, "我来处理", dataShareContentText(compact[5]["content"]))
}

func TestCompactDataShareMessagesKeepsShortUserHistoryReplay(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "第一步"},
		{"role": "user", "content": "第二步"},
		{"role": "user", "content": "第三步"},
		{"role": "user", "content": "第四步"},
		{"role": "user", "content": "第一步"},
		{"role": "user", "content": "第二步"},
		{"role": "user", "content": "第三步"},
		{"role": "user", "content": "第四步"},
		{"role": "assistant", "content": "我来处理"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 9)
	require.Equal(t, 2, countDataShareMessagesWithContent(compact, "第一步"))
	require.Equal(t, "我来处理", dataShareContentText(compact[8]["content"]))
}

func TestCompactDataShareMessagesDedupesMessagesSyntheticReplay(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "<system-reminder> current date and tool context </system-reminder>"},
		{"role": "system", "content": "The following skills are available for use with the Skill tool"},
		{"role": "assistant", "content": "我会调用 skill"},
		{"role": "tool", "tool_call_id": "skill_1", "content": "Skill loaded"},
		{"role": "user", "content": "<system-reminder> current date and tool context </system-reminder>"},
		{"role": "system", "content": "The following skills are available for use with the Skill tool"},
		{"role": "assistant", "content": "我会调用 skill"},
		{"role": "tool", "tool_call_id": "skill_1", "content": "Skill loaded"},
		{"role": "user", "content": "继续执行"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 5)
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "The following skills are available for use with the Skill tool"))
	require.Equal(t, "继续执行", dataShareContentText(compact[4]["content"]))
}

func TestCompactDataShareMessagesDedupesSyntheticReplayMarkers(t *testing.T) {
	for _, marker := range []string{
		`<system-reminder data-role="team-context">Agent Team Communication</system-reminder>`,
		"<system_reminder>todo list is currently empty</system_reminder>",
		"[Subagent Context] You are running as a subagent (depth 1/1).",
		`[important: the user has invoked the "personal-health-router" skill]`,
	} {
		t.Run(marker, func(t *testing.T) {
			messages := []map[string]any{
				{"role": "system", "content": "You are a terminal assistant."},
				{"role": "user", "content": marker},
				{"role": "system", "content": "You are a terminal assistant."},
				{"role": "user", "content": marker},
				{"role": "assistant", "content": "继续执行"},
			}

			compact := CompactDataShareMessages(messages)

			require.Len(t, compact, 3)
			require.Equal(t, 1, countDataShareMessagesWithContent(compact, marker))
			require.Equal(t, "继续执行", dataShareContentText(compact[2]["content"]))
		})
	}
}

func TestCompactDataShareMessagesDedupesOrderedReplayAfterShortSystemPrefix(t *testing.T) {
	base := []map[string]any{
		{"role": "system", "content": "You are Hermes Agent."},
		{"role": "user", "content": "can u combine pdf for me?"},
		{"role": "assistant", "content": "Please upload the PDFs."},
		{"role": "user", "content": "[The user sent a document: exam_paper.zip]"},
		{"role": "assistant", "content": "Done, I combined the PDFs."},
		{"role": "user", "content": "how much credits left"},
		{"role": "assistant", "content": "I cannot see your credits."},
	}
	messages := append(cloneBufferedDataShareMaps(base), cloneBufferedDataShareMaps(base[:5])...)
	messages = append(messages, map[string]any{"role": "user", "content": "thanks"})
	messages = append(messages, map[string]any{"role": "assistant", "content": "You're welcome."})

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, len(base)+2)
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "can u combine pdf for me?"))
	require.Equal(t, "thanks", dataShareContentText(compact[len(compact)-2]["content"]))
	require.Equal(t, "You're welcome.", dataShareContentText(compact[len(compact)-1]["content"]))
}

func TestCompactDataShareMessagesDedupesUserFirstSyntheticPrefixReplay(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "分析活动营销统计数量\n<image></image>"},
		{"role": "system", "content": "<permissions instructions>workspace-write</permissions instructions>"},
		{"role": "user", "content": "# AGENTS.md instructions for /tmp/app\n<INSTRUCTIONS>"},
		{"role": "user", "content": "为什么预计人数提示重复\n<image></image>"},
		{"role": "assistant", "content": "我先检查代码"},
		{"role": "user", "content": "分析活动营销统计数量\n<image></image>"},
		{"role": "system", "content": "<permissions instructions>workspace-write</permissions instructions>"},
		{"role": "user", "content": "# AGENTS.md instructions for /tmp/app\n<INSTRUCTIONS>"},
		{"role": "user", "content": "为什么预计人数提示重复\n<image></image>"},
		{"role": "assistant", "content": "我继续处理"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 6)
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "分析活动营销统计数量\n<image></image>"))
	require.Equal(t, "我继续处理", dataShareContentText(compact[len(compact)-1]["content"]))
}

func TestCompactDataShareMessagesKeepsOrdinaryShortRepeatedUserAssistant(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "count"},
		{"role": "assistant", "content": "What would you like me to count?"},
		{"role": "user", "content": "count"},
		{"role": "assistant", "content": "What would you like me to count?"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 4)
	require.Equal(t, 2, countDataShareMessagesWithContent(compact, "count"))
}

func TestCompactDataShareMessagesKeepsOrdinaryRepeatedUserCommands(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "继续"},
		{"role": "user", "content": "好的"},
		{"role": "user", "content": "count"},
		{"role": "user", "content": "resume"},
		{"role": "user", "content": "继续"},
		{"role": "user", "content": "好的"},
		{"role": "user", "content": "count"},
		{"role": "user", "content": "resume"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 8)
	for _, text := range []string{"继续", "好的", "count", "resume"} {
		require.Equal(t, 2, countDataShareMessagesWithContent(compact, text))
	}
}

func TestCompactDataShareMessagesKeepsMarkerLikeRealUserRepeat(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "You are a terminal assistant."},
		{"role": "user", "content": "what does <shell> mean?"},
		{"role": "system", "content": "You are a terminal assistant."},
		{"role": "user", "content": "what does <shell> mean?"},
		{"role": "assistant", "content": "It usually refers to a command interpreter."},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 5)
	require.Equal(t, 2, countDataShareMessagesWithContent(compact, "what does <shell> mean?"))
	require.Equal(t, "It usually refers to a command interpreter.", dataShareContentText(compact[4]["content"]))
}

func TestCompactDataShareMessagesKeepsShortDivergedSystemPrefix(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "你是编码助手"},
		{"role": "user", "content": "第一步"},
		{"role": "assistant", "content": "旧分支"},
		{"role": "system", "content": "你是编码助手"},
		{"role": "user", "content": "第一步"},
		{"role": "assistant", "content": "新分支"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 6)
	require.Equal(t, "旧分支", dataShareContentText(compact[2]["content"]))
	require.Equal(t, "新分支", dataShareContentText(compact[5]["content"]))
}

func TestCompactDataShareMessagesDedupesToolEchoReplay(t *testing.T) {
	messages := []map[string]any{
		{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md"},
		{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md"},
		{"role": "assistant", "content": "看到了 README.md"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 3)
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "README.md"))
	require.Equal(t, "看到了 README.md", dataShareContentText(compact[2]["content"]))
}

func TestCompactDataShareMessagesDedupesCodexCommentaryEcho(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "<permissions instructions> sandbox</permissions instructions>"},
		{"role": "user", "content": "查一下配置"},
		{"role": "assistant", "content": "我先检查相关文件", "phase": "commentary"},
		{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "rg 配置"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "config.go"},
		{"role": "assistant", "content": "我先检查相关文件", "phase": "commentary"},
		{"role": "assistant", "content": "配置入口还在"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 6)
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "我先检查相关文件"))
	require.Equal(t, "配置入口还在", dataShareContentText(compact[len(compact)-1]["content"]))
}

func TestCompactDataShareMessagesKeepsRepeatedNonCommentaryAssistantText(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "重复一次"},
		{"role": "assistant", "content": "好的"},
		{"role": "user", "content": "再说一次"},
		{"role": "assistant", "content": "好的"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 4)
	require.Equal(t, 2, countDataShareMessagesWithContent(compact, "好的"))
}

func TestCompactDataShareMessagesKeepsCommentaryAfterNewUserTurn(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "先查配置"},
		{"role": "assistant", "content": "我先检查相关文件", "phase": "commentary"},
		{"role": "user", "content": "再查一次"},
		{"role": "assistant", "content": "我先检查相关文件", "phase": "commentary"},
	}

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, 4)
	require.Equal(t, 2, countDataShareMessagesWithContent(compact, "我先检查相关文件"))
}

func TestDataShareQualityAllowsMissingUsageTokens(t *testing.T) {
	sys := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": sys},
		{"role": "user", "content": "列目录"},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md", "status": "success", "is_error": false},
		{"role": "assistant", "content": "看到了 README.md"},
	}
	tools := []map[string]any{
		{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
	}
	usage := map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}

	// 交付说明允许 token 用量无法聚合时为空/为 0，不能因此误判完整工具 session 无效。
	if errs := ValidateDataShareSessionQuality("gpt-5.5", sys, messages, tools, usage); len(errs) != 0 {
		t.Fatalf("quality_errors = %v, want none", errs)
	}
	if status := DataSharePayloadQualityStatus("gpt-5.5", sys, messages, tools, usage); status != DataShareQualityComplete {
		t.Fatalf("quality_status = %q, want complete", status)
	}
}

func TestDataShareQualityDoesNotApplyHardcodedModelScope(t *testing.T) {
	sys := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": sys},
		{"role": "user", "content": "列目录"},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md", "status": "success", "is_error": false},
		{"role": "assistant", "content": "看到了 README.md"},
	}
	tools := []map[string]any{
		{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
	}

	for _, model := range []string{"claude-opus-4-6", "claude-sonnet-4-20250514", "gpt-4.1"} {
		// 模型是否进入数据共享由采集跳过规则决定，质量校验不能再用硬编码模型范围判无效。
		if errs := ValidateDataShareSessionQuality(model, sys, messages, tools, map[string]any{"total_tokens": 15}); len(errs) != 0 {
			t.Fatalf("model %q quality_errors = %v, want none", model, errs)
		}
	}
}

func TestDataShareQualityStatusNormalizesLegacyPartialPayload(t *testing.T) {
	sys := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": sys},
		{"role": "user", "content": "列目录"},
		{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "exec_command", "input": map[string]any{"cmd": "ls"}},
			},
		},
		{
			"role": "user",
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "README.md"},
			},
		},
		{"role": "assistant", "content": "看到了 README.md"},
		{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_tail", "name": "exec_command", "input": map[string]any{"cmd": "pwd"}},
			},
		},
	}
	tools := []map[string]any{
		{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
	}

	if status := DataSharePayloadQualityStatus("gpt-5.5", sys, messages, tools, map[string]any{"total_tokens": 15}); status != DataShareQualityPartial {
		t.Fatalf("quality_status = %q, want partial", status)
	}
}

func TestDataShareQualityStatusDoesNotTrimUnfixableErrors(t *testing.T) {
	sys := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": sys},
		{"role": "user", "content": "列目录"},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md", "status": "success", "is_error": false},
		{"role": "assistant", "content": "看到了 README.md"},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_tail", "name": "exec_command", "arguments": map[string]any{"cmd": "pwd"}}}},
	}

	if status := DataSharePayloadQualityStatus("gpt-5.5", sys, messages, nil, map[string]any{"total_tokens": 15}); status != DataShareQualityInvalid {
		t.Fatalf("quality_status = %q, want invalid", status)
	}
}

func TestDataShareQualityStatusLargePartialTail(t *testing.T) {
	sys := "你是编码助手"
	tools := []map[string]any{
		{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
	}
	messages := buildLargeDataShareMessages(sys, 2000, true)

	status, errs := DataShareSessionQuality("gpt-5.5", sys, messages, tools, map[string]any{"total_tokens": 15})
	if status != DataShareQualityPartial {
		t.Fatalf("quality_status = %q, want partial, errors=%v", status, errs)
	}
	if !containsString(errs, "tool_call_result_unpaired") {
		t.Fatalf("quality_errors = %v, want unpaired tail", errs)
	}
}

func TestDataShareQualityStatusDoesNotNormalizeUnfixableErrors(t *testing.T) {
	sys := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": sys},
		{"role": "user", "content": "列目录"},
		{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "exec_command", "input": map[string]any{"cmd": "ls"}},
			},
		},
		{
			"role": "user",
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "README.md"},
			},
		},
		{"role": "assistant", "content": "看到了 README.md"},
	}
	tools := []map[string]any{
		{"name": "other_tool", "description": "其他工具", "parameters": map[string]any{"type": "object"}},
	}

	if status := DataSharePayloadQualityStatus("gpt-5.5", sys, messages, tools, map[string]any{"total_tokens": 15}); status != DataShareQualityInvalid {
		t.Fatalf("quality_status = %q, want invalid", status)
	}
}

func TestExportableDataShareMessagesAllowsCompleteSnapshot(t *testing.T) {
	sys := "你是编码助手"
	messages := []map[string]any{
		{"role": "system", "content": sys},
		{"role": "user", "content": "列目录"},
		{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md", "status": "success", "is_error": false},
		{"role": "assistant", "content": "看到了 README.md"},
	}
	tools := []map[string]any{
		{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
	}

	exported, errs := exportableDataShareMessages("gpt-5.5", sys, messages, tools, map[string]any{"total_tokens": 15})
	if len(errs) != 0 {
		t.Fatalf("quality_errors = %v, want none", errs)
	}
	if len(exported) != len(messages) {
		t.Fatalf("exported len = %d, want %d", len(exported), len(messages))
	}
}

func TestPartialSessionExportsRawSnapshot(t *testing.T) {
	sys := "你是编码助手"
	session := &DataShareSession{
		TrajectoryID:       "traj",
		SessionID:          "sess",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             DataShareStatusTerminated,
		IsFinalSnapshot:    false,
		SourceRequestCount: 1,
		SystemPrompt:       &sys,
		Tools: []map[string]any{
			{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
		},
		Messages: []map[string]any{
			{"role": "system", "content": sys},
			{"role": "user", "content": "列目录"},
			{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
			{"role": "tool", "tool_call_id": "call_1", "content": "README.md", "status": "success", "is_error": false},
			{"role": "assistant", "content": "看到了 README.md"},
			{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_tail", "name": "exec_command", "arguments": map[string]any{"cmd": "pwd"}}}},
		},
		Usage: map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		Meta:  map[string]any{"source_request_ids": []string{"req_1"}},
	}
	status := DataSharePayloadQualityStatus(session.Model, sys, session.Messages, session.Tools, session.Usage)
	if status != DataShareQualityPartial {
		t.Fatalf("quality_status = %q, want partial", status)
	}

	var buf bytes.Buffer
	if err := WriteSingleSessionJSONL(&buf, session); err != nil {
		t.Fatalf("WriteSingleSessionJSONL returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload); err != nil {
		t.Fatalf("invalid jsonl: %v", err)
	}
	messages := mapsFromAny(payload["messages"])
	if len(messages) != 6 {
		t.Fatalf("exported messages len = %d, want raw len 6: %#v", len(messages), messages)
	}
	if got := payload["status"]; got != DataShareStatusTerminated {
		t.Fatalf("exported status = %v, want terminated", got)
	}
	if got := payload["is_final_snapshot"]; got != false {
		t.Fatalf("exported is_final_snapshot = %v, want false", got)
	}
	if _, ok := payload["quality_status"]; ok {
		t.Fatalf("quality_status should not be included in JSONL payload")
	}
	if errs := validateDataSharePayloadQuality(payload); !containsString(errs, "tool_call_result_unpaired") {
		t.Fatalf("payload quality errors = %v, want unpaired tail retained", errs)
	}
}

func TestInvalidSessionCanExportWhenSelected(t *testing.T) {
	sys := "你是编码助手"
	session := &DataShareSession{
		TrajectoryID:       "traj",
		SessionID:          "sess",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             DataShareStatusTerminated,
		IsFinalSnapshot:    false,
		SourceRequestCount: 1,
		SystemPrompt:       &sys,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages: []map[string]any{
			{"role": "system", "content": sys},
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hello"},
		},
		Usage: map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
	}
	if status := DataSharePayloadQualityStatus(session.Model, sys, session.Messages, session.Tools, session.Usage); status != DataShareQualityInvalid {
		t.Fatalf("quality_status = %q, want invalid", status)
	}
	var buf bytes.Buffer
	if err := WriteSingleSessionJSONL(&buf, session); err != nil {
		t.Fatalf("WriteSingleSessionJSONL returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload); err != nil {
		t.Fatalf("invalid jsonl: %v", err)
	}
	if got := payload["session_id"]; got != "sess" {
		t.Fatalf("session_id = %v, want sess", got)
	}
}

func buildLargeDataShareMessages(systemPrompt string, rounds int, includeUnpairedTail bool) []map[string]any {
	messages := make([]map[string]any, 0, rounds*4+3)
	messages = append(messages,
		map[string]any{"role": "system", "content": systemPrompt},
		map[string]any{"role": "user", "content": "开始执行任务"},
	)
	for i := 0; i < rounds; i++ {
		callID := fmt.Sprintf("call_%05d", i)
		messages = append(messages,
			map[string]any{
				"role":          "assistant",
				"content":       "",
				"finish_reason": "tool_calls",
				"tool_calls": []map[string]any{{
					"id":        callID,
					"name":      "exec_command",
					"arguments": map[string]any{"cmd": fmt.Sprintf("echo %d", i)},
				}},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      strings.Repeat("ok ", 8),
				"status":       "success",
				"is_error":     false,
			},
			map[string]any{"role": "assistant", "content": fmt.Sprintf("完成第 %d 步", i)},
			map[string]any{"role": "user", "content": fmt.Sprintf("继续第 %d 步", i+1)},
		)
	}
	messages = append(messages, map[string]any{"role": "assistant", "content": "所有步骤完成"})
	if includeUnpairedTail {
		messages = append(messages, map[string]any{
			"role":          "assistant",
			"content":       "",
			"finish_reason": "tool_calls",
			"tool_calls": []map[string]any{{
				"id":        "call_tail",
				"name":      "exec_command",
				"arguments": map[string]any{"cmd": "pwd"},
			}},
		})
	}
	return messages
}

func buildSequentialDataShareMessages(prefix string, count int) []map[string]any {
	messages := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, map[string]any{"role": role, "content": fmt.Sprintf("%s-%03d", prefix, i)})
	}
	return messages
}

func buildLayeredReplayDataShareMessages() []map[string]any {
	prefix := buildSequentialDataShareMessages("固定点前置", 20)
	window := buildSequentialDataShareMessages("固定点窗口", 24)
	window[0] = map[string]any{"role": "user", "content": "<environment_context><cwd>/tmp/fixed-point</cwd><shell>bash</shell></environment_context>"}
	divider := []map[string]any{{"role": "assistant", "content": "固定点分隔"}}
	block := buildSequentialDataShareMessages("固定点重复块", 24)
	messages := cloneBufferedDataShareMaps(prefix)
	messages = append(messages, cloneBufferedDataShareMaps(window)...)
	messages = append(messages, cloneBufferedDataShareMaps(divider)...)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)
	messages = append(messages, cloneBufferedDataShareMaps(window)...)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)
	return messages
}

func buildSequentialResponsesInputJSON(prefix string, start int, end int) string {
	items := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		items = append(items, fmt.Sprintf(`{"type":"message","role":%q,"content":[{"type":"input_text","text":%q}]}`, role, fmt.Sprintf("%s-%03d", prefix, i)))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestCompactDataShareMessagesDedupesLargeOrderedReplay(t *testing.T) {
	sys := "<permissions instructions> sandbox</permissions instructions>"
	base := buildLargeDataShareMessages(sys, 150, false)
	replayed := append(cloneBufferedDataShareMaps(base), cloneBufferedDataShareMaps(base)...)
	replayed = append(replayed, map[string]any{"role": "user", "content": "新的尾巴"})

	compact := CompactDataShareMessages(replayed)

	require.Len(t, compact, len(base)+1)
	require.Len(t, CompactDataShareMessages(compact), len(compact))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "开始执行任务"))
	require.Equal(t, "新的尾巴", dataShareContentText(compact[len(compact)-1]["content"]))
}

func TestCompactDataShareMessagesDedupesAdjacentWindowReplay(t *testing.T) {
	base := buildSequentialDataShareMessages("历史", 80)
	base[20] = map[string]any{"role": "user", "content": "<environment_context><cwd>/tmp/app</cwd><shell>bash</shell></environment_context>"}
	replayed := append(cloneBufferedDataShareMaps(base[:40]), cloneBufferedDataShareMaps(base[20:55])...)
	replayed = append(replayed, cloneBufferedDataShareMaps(base[20:55])...)
	replayed = append(replayed, map[string]any{"role": "user", "content": "新的尾巴"})

	compact := CompactDataShareMessages(replayed)

	require.Len(t, compact, 56)
	require.Len(t, CompactDataShareMessages(compact), len(compact))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "<environment_context><cwd>/tmp/app</cwd><shell>bash</shell></environment_context>"))
	require.Equal(t, "新的尾巴", dataShareContentText(compact[len(compact)-1]["content"]))
}

func TestCompactDataShareMessagesDedupesAdjacentRepeatedLongTextWorkflow(t *testing.T) {
	block := buildSequentialDataShareMessages("连续重复任务", 60)
	messages := append(cloneBufferedDataShareMaps(block), cloneBufferedDataShareMaps(block)...)

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, len(block))
	require.Len(t, CompactDataShareMessages(compact), len(compact))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "连续重复任务-000"))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "连续重复任务-059"))
}

func TestCompactDataShareMessagesDedupesSystemFirstAdjacentRepeatedLongTextWorkflow(t *testing.T) {
	block := append(
		[]map[string]any{{"role": "system", "content": "你是编码助手"}},
		buildSequentialDataShareMessages("系统开头重复任务", 60)...,
	)
	messages := append(cloneBufferedDataShareMaps(block), cloneBufferedDataShareMaps(block)...)

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, len(block))
	require.Len(t, CompactDataShareMessages(compact), len(compact))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "你是编码助手"))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "系统开头重复任务-059"))
}

func TestCompactDataShareMessagesReachesFixedPointAfterGlobalReplayRemoval(t *testing.T) {
	messages := buildLayeredReplayDataShareMessages()
	once := compactDataShareMessagesOnce(messages)

	compact := CompactDataShareMessages(messages)

	require.Less(t, len(once), len(messages))
	require.Less(t, len(compact), len(once))
	require.Len(t, CompactDataShareMessages(compact), len(compact))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "固定点重复块-000"))
	require.Equal(t, 1, countDataShareMessagesWithContent(compact, "<environment_context><cwd>/tmp/fixed-point</cwd><shell>bash</shell></environment_context>"))
}

func TestMergeBufferedDataShareMessagesKeepsRepeatedLongWorkflow(t *testing.T) {
	base := buildSequentialDataShareMessages("历史", 120)
	existing := cloneBufferedDataShareMaps(base[:100])
	incoming := cloneBufferedDataShareMaps(base[50:120])
	incoming = append(incoming, map[string]any{"role": "assistant", "content": "新的响应"})

	merged := mergeBufferedDataShareMessages(existing, incoming)

	require.Len(t, merged, len(existing)+len(incoming))
	require.Equal(t, 2, countDataShareMessagesWithContent(merged, "历史-050"))
	require.Equal(t, 2, countDataShareMessagesWithContent(merged, "历史-099"))
	require.Equal(t, "新的响应", dataShareContentText(merged[len(merged)-1]["content"]))
}

func TestResponsesReplayPlanDedupesMixedPrefixAndSlidingWindow(t *testing.T) {
	base := buildSequentialDataShareMessages("历史", 120)
	existingItems := make([]dataShareResponsesInputItem, 0, 100)
	for _, msg := range base[:100] {
		identity := dataShareMessageIdentity(msg)
		existingItems = append(existingItems, dataShareResponsesInputItem{
			Message:     msg,
			Identity:    identity,
			IdentityKey: dataShareResponsesIdentityKey(identity),
		})
	}
	state := updateDataShareResponsesCaptureState(&dataShareResponsesCaptureState{}, existingItems, nil, nil, 1)
	incomingMessages := append(cloneBufferedDataShareMaps(base[:3]), cloneBufferedDataShareMaps(base[50:120])...)
	incomingMessages = append(incomingMessages, map[string]any{"role": "user", "content": "新的用户问题"})
	incoming := make([]dataShareResponsesInputItem, 0, len(incomingMessages))
	for _, msg := range incomingMessages {
		identity := dataShareMessageIdentity(msg)
		incoming = append(incoming, dataShareResponsesInputItem{
			Message:     msg,
			Identity:    identity,
			IdentityKey: dataShareResponsesIdentityKey(identity),
		})
	}

	plan, orderUncertain := dataShareBuildResponsesReplayPlan(state, incoming)

	require.True(t, orderUncertain)
	require.True(t, plan.Keep[0])
	require.True(t, plan.Keep[1])
	require.True(t, plan.Keep[2])
	for i := 3; i < 53; i++ {
		require.False(t, plan.Keep[i], "replayed window item %d should be dropped", i)
	}
	for i := 53; i < len(plan.Keep); i++ {
		require.True(t, plan.Keep[i], "new tail item %d should be kept", i)
	}
}

func TestCompactDataShareMessagesKeepsNonAdjacentRepeatedLongTextWorkflow(t *testing.T) {
	block := buildSequentialDataShareMessages("重复任务", 60)
	messages := buildSequentialDataShareMessages("前置", 20)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)
	messages = append(messages, buildSequentialDataShareMessages("间隔", 5)...)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)

	compact := CompactDataShareMessages(messages)

	require.Len(t, compact, len(messages))
	require.Equal(t, 2, countDataShareMessagesWithContent(compact, "重复任务-000"))
	require.Equal(t, 2, countDataShareMessagesWithContent(compact, "重复任务-059"))
}

func TestExportDownloadPayloadKeepsSafeRepeatedLongTextWorkflow(t *testing.T) {
	sys := "你是编码助手"
	block := buildSequentialDataShareMessages("重复任务", 60)
	messages := buildSequentialDataShareMessages("前置", 20)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)
	messages = append(messages, buildSequentialDataShareMessages("间隔", 5)...)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)
	session := &DataShareSession{
		TrajectoryID:       "traj-safe-repeat",
		SessionID:          "sess-safe-repeat",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 2,
		SystemPrompt:       &sys,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages:           messages,
		Usage:              map[string]any{"total_tokens": 15},
		QualityStatus:      DataShareQualityComplete,
		Exportable:         true,
		CreatedAt:          time.Now(),
	}

	payload, err := exportDownloadPayloadFromSession(session)

	require.NoError(t, err)
	exported := mapsFromAny(payload["messages"])
	require.Len(t, exported, len(messages))
	require.Equal(t, 2, countDataShareMessagesWithContent(exported, "重复任务-000"))
}

func TestExportDownloadPayloadRepairsStoredTrailingToolReplayWithoutMutatingSession(t *testing.T) {
	sys := "你是编码助手"
	base := buildLargeDataShareMessages(sys, 30, false)
	replay := cloneBufferedDataShareMaps(base[10:70])
	duplicated := append(cloneBufferedDataShareMaps(base), replay...)
	session := &DataShareSession{
		TrajectoryID:       "traj-trailing-tool-replay",
		SessionID:          "sess-trailing-tool-replay",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 3,
		SystemPrompt:       &sys,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages:           duplicated,
		Usage:              map[string]any{"total_tokens": 15},
		QualityStatus:      DataShareQualityComplete,
		Exportable:         true,
		CreatedAt:          time.Now(),
	}

	payload, err := exportDownloadPayloadFromSession(session)

	require.NoError(t, err)
	exported := mapsFromAny(payload["messages"])
	require.Len(t, exported, len(base))
	require.Len(t, CompactDataShareMessages(exported), len(exported))
	require.False(t, dataShareHasReplayDuplicateBlock(exported))
	require.Len(t, session.Messages, len(base)+len(replay))
}

func TestRecheckDataShareExportPayloadRejectsNonIdempotentCompact(t *testing.T) {
	messages := []map[string]any{
		{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md"},
		{"role": "user", "content": "继续"},
		{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call_1", "name": "exec_command", "arguments": map[string]any{"cmd": "ls"}}}},
		{"role": "tool", "tool_call_id": "call_1", "content": "README.md"},
	}
	payload := map[string]any{"messages": messages}

	err := recheckDataShareExportPayload(payload)

	require.ErrorIs(t, err, ErrDataShareExportPayloadInvalid)
	require.False(t, dataShareHasReplayDuplicateBlock(messages))
	require.Less(t, len(compactDataShareMessagesOnce(messages)), len(messages))
}

func TestResponsesReplayPlanHandlesLargeNoMatchInputLinearly(t *testing.T) {
	existing := buildSequentialDataShareMessages("已有", 50000)
	incoming := buildSequentialDataShareMessages("新增", 4000)
	existingItems := make([]dataShareResponsesInputItem, 0, len(existing))
	for _, msg := range existing {
		identity := dataShareMessageIdentity(msg)
		existingItems = append(existingItems, dataShareResponsesInputItem{
			Message:     msg,
			Identity:    identity,
			IdentityKey: dataShareResponsesIdentityKey(identity),
		})
	}
	incomingItems := make([]dataShareResponsesInputItem, 0, len(incoming))
	for _, msg := range incoming {
		identity := dataShareMessageIdentity(msg)
		incomingItems = append(incomingItems, dataShareResponsesInputItem{
			Message:     msg,
			Identity:    identity,
			IdentityKey: dataShareResponsesIdentityKey(identity),
		})
	}
	state := updateDataShareResponsesCaptureState(&dataShareResponsesCaptureState{}, existingItems, nil, nil, 1)

	start := time.Now()
	plan, orderUncertain := dataShareBuildResponsesReplayPlan(state, incomingItems)
	elapsed := time.Since(start)

	require.False(t, orderUncertain)
	require.Equal(t, len(incomingItems), dataShareResponsesReplayPlanKeepCount(plan))
	require.Less(t, elapsed, 2*time.Second)
}

func TestCompactDataShareMessagesHandlesLargeTrailingReplayLinearly(t *testing.T) {
	sys := "你是编码助手"
	base := buildLargeDataShareMessages(sys, 12500, false)
	replayed := append(cloneBufferedDataShareMaps(base), cloneBufferedDataShareMaps(base[10000:14000])...)

	compact := CompactDataShareMessages(replayed)

	require.Len(t, compact, len(base))
}

func TestExportDownloadPayloadKeepsLargeSafeRepeatedTextLinearly(t *testing.T) {
	sys := "你是编码助手"
	block := buildSequentialDataShareMessages("重复导出任务", 12000)
	messages := cloneBufferedDataShareMaps(block)
	messages = append(messages, buildSequentialDataShareMessages("间隔", 20)...)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)
	session := &DataShareSession{
		TrajectoryID:       "traj-large-safe-repeat",
		SessionID:          "sess-large-safe-repeat",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 2,
		SystemPrompt:       &sys,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages:           messages,
		Usage:              map[string]any{"total_tokens": 15},
		QualityStatus:      DataShareQualityComplete,
		Exportable:         true,
		CreatedAt:          time.Now(),
	}

	payload, err := exportDownloadPayloadFromSession(session)

	require.NoError(t, err)
	exported := mapsFromAny(payload["messages"])
	require.Len(t, exported, len(messages))
	require.Equal(t, 2, countDataShareMessagesWithContent(exported, "重复导出任务-000"))
}

func TestOpenAIResponsesRawCaptureDedupesSlidingWindowRequestInput(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})
	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	capture := func(requestID string, start, end int, response string) {
		require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-sliding-window",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + buildSequentialResponsesInputJSON("历史", start, end) + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, requestID, response)),
			InboundEndpoint: "/v1/responses",
			InputTokens:     10,
			OutputTokens:    5,
		}}))
	}

	capture("request-window-1", 0, 100, "响应-100")
	capture("request-window-2", 50, 120, "响应-120")
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "历史-050"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "历史-099"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "历史-119"))
	require.Equal(t, "响应-120", dataShareContentText(session.Messages[len(session.Messages)-1]["content"]))
}

func TestOpenAIResponsesRawCaptureDedupesMixedPrefixSlidingWindowRequestInput(t *testing.T) {
	gid := int64(12)
	repo := &dataShareCaptureRepoStub{}
	svc := NewDataSharingService(repo, nil)
	svc.SetDefaultCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              4,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 30,
		BufferMaxSessions:      4096,
		BufferMaxPendingEvents: 65536,
	})
	apiKey := &APIKey{
		ID:      34,
		UserID:  56,
		GroupID: &gid,
		Group:   &Group{ID: gid, Platform: PlatformOpenAI, DataSharingEnabled: true},
	}
	capture := func(requestID string, inputItems string, response string) {
		require.NoError(t, svc.captureRequestFromJob(context.Background(), DataSharingCaptureJob{Input: DataShareCaptureInput{
			APIKey:          apiKey,
			Provider:        PlatformOpenAI,
			Model:           "gpt-5.5",
			SessionID:       "session-responses-mixed-window",
			RequestID:       requestID,
			RequestBody:     []byte(`{"model":"gpt-5.5","input":` + inputItems + `}`),
			ResponseBody:    []byte(fmt.Sprintf(`{"id":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, requestID, response)),
			InboundEndpoint: "/v1/responses",
			InputTokens:     10,
			OutputTokens:    5,
		}}))
	}
	mixedInput := "[" + strings.TrimPrefix(strings.TrimSuffix(buildSequentialResponsesInputJSON("历史", 0, 3), "]"), "[") + "," +
		strings.TrimPrefix(strings.TrimSuffix(buildSequentialResponsesInputJSON("历史", 50, 120), "]"), "[") + "]"

	capture("request-mixed-1", buildSequentialResponsesInputJSON("历史", 0, 100), "响应-100")
	capture("request-mixed-2", mixedInput, "响应-120")
	svc.captureBuffer.FlushAll(context.Background())

	session := repo.lastSession()
	require.NotNil(t, session)
	require.Equal(t, 2, session.SourceRequestCount)
	require.LessOrEqual(t, countDataShareMessagesWithContent(session.Messages, "历史-000"), 2)
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "历史-050"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "历史-099"))
	require.Equal(t, 1, countDataShareMessagesWithContent(session.Messages, "历史-119"))
	require.Equal(t, "响应-120", dataShareContentText(session.Messages[len(session.Messages)-1]["content"]))
}

func TestExportDownloadPayloadRepairsStoredAdjacentReplayWithoutMutatingSession(t *testing.T) {
	sys := "你是编码助手"
	base := buildSequentialDataShareMessages("历史", 80)
	duplicated := append(cloneBufferedDataShareMaps(base), cloneBufferedDataShareMaps(base[20:70])...)
	duplicated = append(duplicated, cloneBufferedDataShareMaps(base[20:70])...)
	session := &DataShareSession{
		TrajectoryID:       "traj-stored-duplicate",
		SessionID:          "sess-stored-duplicate",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 3,
		SystemPrompt:       &sys,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages:           duplicated,
		Usage:              map[string]any{"total_tokens": 15},
		QualityStatus:      DataShareQualityComplete,
		Exportable:         true,
		CreatedAt:          time.Now(),
	}

	payload, err := exportDownloadPayloadFromSession(session)

	require.NoError(t, err)
	exported := mapsFromAny(payload["messages"])
	require.Len(t, exported, len(base))
	require.Len(t, session.Messages, len(base)+100)
}

func TestWriteSingleSessionJSONLAllowsSafeRepeatedLongTextBlock(t *testing.T) {
	messages := buildSafeRepeatedTextMessages()
	session := &DataShareSession{
		TrajectoryID:       "traj-safe-text-repeat",
		SessionID:          "sess-safe-text-repeat",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 2,
		SystemPrompt:       optionalDataShareString("你是编码助手"),
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages:           messages,
		Usage:              map[string]any{"total_tokens": 15},
		QualityStatus:      DataShareQualityComplete,
		Exportable:         true,
		CreatedAt:          time.Now(),
	}

	var buf bytes.Buffer
	err := WriteSingleSessionJSONL(&buf, session)

	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload))
	exported := mapsFromAny(payload["messages"])
	require.Len(t, exported, len(messages))
	require.Equal(t, 2, countDataShareMessagesWithContent(exported, "重复块-000"))
}

func TestExportJSONLIncludesSafeRepeatedLongTextBlockInBatch(t *testing.T) {
	sys := "你是编码助手"
	good := &DataShareSession{
		TrajectoryID:       "traj-good-export",
		SessionID:          "sess-good-export",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 1,
		SystemPrompt:       &sys,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages:           buildLargeDataShareMessages(sys, 5, false),
		Usage:              map[string]any{"total_tokens": 15},
		QualityStatus:      DataShareQualityComplete,
		Exportable:         true,
		CreatedAt:          time.Now(),
	}
	repeated := cloneBufferedDataShareSession(good)
	repeated.TrajectoryID = "traj-safe-repeat-export"
	repeated.SessionID = "sess-safe-repeat-export"
	repeated.Messages = buildSafeRepeatedTextMessages()
	repo := &dataShareExportRepoStub{items: []DataShareSession{*repeated, *good}}
	svc := NewDataSharingService(repo, nil)

	var buf bytes.Buffer
	err := svc.ExportJSONL(context.Background(), &buf, DataShareSessionFilters{SelectAll: true}, false)

	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, buf.String(), "sess-safe-repeat-export")
	require.Contains(t, buf.String(), "sess-good-export")
}

func TestExportJSONLParallelKeepsCursorOrder(t *testing.T) {
	now := time.Now()
	items := make([]DataShareSession, 0, 8)
	for i := 0; i < 8; i++ {
		items = append(items, DataShareSession{
			ID:            int64(i + 1),
			TrajectoryID:  fmt.Sprintf("traj-parallel-%02d", i),
			SessionID:     fmt.Sprintf("sess-parallel-%02d", i),
			Dataset:       defaultDataShareDataset,
			Provider:      PlatformOpenAI,
			Model:         "gpt-5.5",
			Messages:      []map[string]any{{"role": "user", "content": fmt.Sprintf("hello-%02d", i)}},
			SessionJSON:   map[string]any{"messages": []any{map[string]any{"role": "user", "content": fmt.Sprintf("hello-%02d", i)}}},
			Usage:         map[string]any{"total_tokens": i + 1},
			QualityStatus: DataShareQualityComplete,
			Exportable:    true,
			CreatedAt:     now.Add(time.Duration(i) * time.Millisecond),
			UpdatedAt:     now.Add(time.Duration(i) * time.Millisecond),
		})
	}
	repo := &dataShareExportRepoStub{items: items}
	svc := NewDataSharingService(repo, nil)

	var buf bytes.Buffer
	err := svc.exportJSONL(context.Background(), &buf, DataShareSessionFilters{SelectAll: true}, false, int64(len(items)), 4, 4, nil, nil)

	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, len(items))
	for i, line := range lines {
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &payload))
		require.Equal(t, fmt.Sprintf("sess-parallel-%02d", i), payload["session_id"])
	}
	require.NotEmpty(t, repo.pageWorkers)
	require.Equal(t, 4, repo.pageWorkers[0])
}

func buildSafeRepeatedTextMessages() []map[string]any {
	out := buildSequentialDataShareMessages("前置", 20)
	block := buildSequentialDataShareMessages("重复块", dataShareLongReplayMinMessages+4)
	out = append(out, cloneBufferedDataShareMaps(block)...)
	out = append(out, buildSequentialDataShareMessages("间隔", 5)...)
	out = append(out, cloneBufferedDataShareMaps(block)...)
	return out
}

func BenchmarkCompactDataShareMessages_LargeTrailingReplay(b *testing.B) {
	sys := "你是编码助手"
	base := buildLargeDataShareMessages(sys, 12500, false)
	replayed := append(cloneBufferedDataShareMaps(base), cloneBufferedDataShareMaps(base[10000:14000])...)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompactDataShareMessages(replayed)
	}
}

func BenchmarkExportDownloadPayload_LargeSafeRepeatedText(b *testing.B) {
	sys := "你是编码助手"
	block := buildSequentialDataShareMessages("重复导出任务", 12000)
	messages := cloneBufferedDataShareMaps(block)
	messages = append(messages, buildSequentialDataShareMessages("间隔", 20)...)
	messages = append(messages, cloneBufferedDataShareMaps(block)...)
	session := &DataShareSession{
		TrajectoryID:       "traj-large-safe-repeat",
		SessionID:          "sess-large-safe-repeat",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 2,
		SystemPrompt:       &sys,
		Tools:              []map[string]any{{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}}},
		Messages:           messages,
		Usage:              map[string]any{"total_tokens": 15},
		QualityStatus:      DataShareQualityComplete,
		Exportable:         true,
		CreatedAt:          time.Now(),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := exportDownloadPayloadFromSession(session); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkFinalizeBufferedDataShareSession(b *testing.B, rounds int, includeUnpairedTail bool) {
	sys := "你是编码助手"
	base := &DataShareSession{
		TrajectoryID:       "traj-bench",
		SessionID:          "sess-bench",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: rounds,
		SystemPrompt:       &sys,
		Tools: []map[string]any{
			{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
		},
		Messages: buildLargeDataShareMessages(sys, rounds, includeUnpairedTail),
		Usage:    map[string]any{"input_tokens": rounds * 10, "output_tokens": rounds * 5, "total_tokens": rounds * 15},
		Meta:     map[string]any{"source_request_ids": []string{"bench"}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := cloneBufferedDataShareSession(base)
		finalizeBufferedDataShareSession(session)
	}
}

func BenchmarkBuildDataShareSessionPayload_FromFinalizedLargeSession(b *testing.B) {
	sys := "你是编码助手"
	base := finalizeBufferedDataShareSession(&DataShareSession{
		TrajectoryID:       "traj-bench",
		SessionID:          "sess-bench",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		SourceRequestCount: 1000,
		SystemPrompt:       &sys,
		Tools: []map[string]any{
			{"name": "exec_command", "description": "运行命令", "parameters": map[string]any{"type": "object"}},
		},
		Messages: buildLargeDataShareMessages(sys, 1000, true),
		Usage:    map[string]any{"input_tokens": 10000, "output_tokens": 5000, "total_tokens": 15000},
		Meta:     map[string]any{"source_request_ids": []string{"bench"}},
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := cloneBufferedDataShareSession(base)
		session.SessionJSON = nil
		BuildFinalizedDataShareSessionPayload(session)
	}
}

func BenchmarkFinalizeBufferedDataShareSession_LargeComplete(b *testing.B) {
	benchmarkFinalizeBufferedDataShareSession(b, 1000, false)
}

func BenchmarkFinalizeBufferedDataShareSession_LargePartialTail(b *testing.B) {
	benchmarkFinalizeBufferedDataShareSession(b, 1000, true)
}

func BenchmarkCompactDataShareMessages_LargeOrderedReplay(b *testing.B) {
	sys := "<permissions instructions> sandbox</permissions instructions>"
	base := buildLargeDataShareMessages(sys, 1000, false)
	replayed := append(cloneBufferedDataShareMaps(base), cloneBufferedDataShareMaps(base)...)
	replayed = append(replayed, map[string]any{"role": "user", "content": "新的尾巴"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompactDataShareMessages(replayed)
	}
}

func TestDataShareExportRedactsSensitiveFields(t *testing.T) {
	sys := "你是编码助手"
	session := DataShareSession{
		TrajectoryID:       "traj-redact",
		SessionID:          "sess-redact",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		SystemPrompt:       &sys,
		Messages: []map[string]any{
			{"role": "user", "content": "hello", "metadata": map[string]any{"user_id": "client-user", "trace_id": "trace-1"}},
			{"role": "assistant", "content": "hi"},
		},
		Usage: map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		Meta: map[string]any{
			"request_id":   "req-redact",
			"user_email":   "alice@example.com",
			"ip_address":   "127.0.0.1",
			"api_key_id":   int64(10),
			"api_key_name": "main-key",
			"account_id":   int64(20),
			"user_id":      int64(30),
			"user_name":    "alice",
			"group_id":     int64(40),
			"group_name":   "共享分组",
			"nested":       map[string]any{"api_key_name": "nested-key", "kept": true},
		},
		SessionJSON: map[string]any{
			"user_id": "legacy-user",
			"metadata": map[string]any{
				"user_email": "legacy@example.com",
				"trace_id":   "legacy-trace",
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	var buf bytes.Buffer
	err := NewDataSharingService(&dataShareExportRepoStub{items: []DataShareSession{session}}, nil).
		ExportJSONL(context.Background(), &buf, DataShareSessionFilters{}, false)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload))
	requireNoDataShareExportSensitiveFields(t, payload)

	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, messages)
	firstMessage, ok := messages[0].(map[string]any)
	require.True(t, ok)
	messageMetadata, ok := firstMessage["metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "trace-1", messageMetadata["trace_id"])
	metadata, ok := payload["metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "legacy-trace", metadata["trace_id"])
	meta, ok := payload["meta"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "req-redact", meta["request_id"])
	require.Equal(t, "共享分组", meta["group_name"])
	nestedMeta, ok := meta["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, nestedMeta["kept"])
}

func TestDataSharingService_CreateExportArtifactGeneratesDownloadableFile(t *testing.T) {
	session := DataShareSession{
		TrajectoryID:  "traj-artifact",
		SessionID:     "sess-artifact",
		Dataset:       defaultDataShareDataset,
		Provider:      PlatformOpenAI,
		Model:         "gpt-5.5",
		Messages:      []map[string]any{{"role": "user", "content": "hello"}},
		SessionJSON:   map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}},
		Exportable:    true,
		QualityStatus: DataShareQualityComplete,
		UserID:        1,
		APIKeyID:      2,
		GroupID:       3,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{SettingKeyDataSharingExportTicketKey: "test-secret"}}
	svc := NewDataSharingService(&dataShareExportRepoStub{items: []DataShareSession{session}}, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())

	artifact, err := svc.CreateExportArtifact(context.Background(), DataShareExportArtifactCreateInput{
		Filters:  DataShareSessionFilters{IDs: []int64{1}},
		Filename: "artifact-test",
		Encoding: DataShareExportEncodingJSONL,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		got, err := svc.GetExportArtifact(context.Background(), artifact.ID)
		return err == nil && got.Status == DataShareExportArtifactStatusCompleted
	}, time.Second, 10*time.Millisecond)

	completed, err := svc.GetExportArtifact(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), completed.SessionCount)
	require.Greater(t, completed.FileSize, int64(0))
	require.FileExists(t, completed.StoragePath)

	ticket, err := svc.CreateExportArtifactDownloadTicket(context.Background(), artifact.ID)
	require.NoError(t, err)
	body, opened, err := svc.OpenExportArtifactDownload(context.Background(), ticket.Token)
	require.NoError(t, err)
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, completed.ID, opened.ID)
	require.Contains(t, string(raw), "sess-artifact")
}

func TestDataSharingService_CreateExportArtifactUsesRuntimeBatchAndRecordsDurations(t *testing.T) {
	now := time.Now()
	sessions := make([]DataShareSession, 0, 120)
	for i := 1; i <= 120; i++ {
		sessions = append(sessions, DataShareSession{
			ID:            int64(i),
			TrajectoryID:  fmt.Sprintf("traj-artifact-batch-%d", i),
			SessionID:     fmt.Sprintf("sess-artifact-batch-%d", i),
			Dataset:       defaultDataShareDataset,
			Provider:      PlatformOpenAI,
			Model:         "gpt-5.5",
			Messages:      []map[string]any{{"role": "user", "content": "hello"}},
			SessionJSON:   map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}},
			Exportable:    true,
			QualityStatus: DataShareQualityComplete,
			UserID:        1,
			APIKeyID:      2,
			GroupID:       3,
			CreatedAt:     now.Add(time.Duration(i) * time.Millisecond),
			UpdatedAt:     now.Add(time.Duration(i) * time.Millisecond),
		})
	}
	exportRepo := &dataShareExportRepoStub{items: sessions}
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{SettingKeyDataSharingExportTicketKey: "test-secret"}}
	svc := NewDataSharingService(exportRepo, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	_, err := svc.UpdateCaptureRuntimeSettings(context.Background(), DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		FlushQueueSize:         1,
		TaskTimeoutSeconds:     1,
		CompressionLevel:       string(DataShareCompressionLevelFastest),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: 1,
		BufferMaxSessions:      1,
		BufferMaxPendingEvents: 1,
		DurationWindowSize:     64,
		ExportBatchSize:        50,
		ExportWorkerCount:      3,
	})
	require.NoError(t, err)

	artifact, err := svc.CreateExportArtifact(context.Background(), DataShareExportArtifactCreateInput{
		Filters:  DataShareSessionFilters{SelectAll: true},
		Filename: "artifact-batch-test",
		Encoding: DataShareExportEncodingJSONL,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		got, err := svc.GetExportArtifact(context.Background(), artifact.ID)
		return err == nil && got.Status == DataShareExportArtifactStatusCompleted
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, 1, exportRepo.countCalls)
	require.NotEmpty(t, exportRepo.pageLimits)
	require.Equal(t, 50, exportRepo.pageLimits[0])
	require.NotEmpty(t, exportRepo.pageWorkers)
	require.Equal(t, 3, exportRepo.pageWorkers[0])
	stats := svc.ExportDurationStats()
	require.Greater(t, stats.SampleCount, 0)
	require.Greater(t, findDataShareExportDurationPart(t, stats, DataShareExportDurationPartCount).SampleCount, 0)
	require.Greater(t, findDataShareExportDurationPart(t, stats, DataShareExportDurationPartGenerateTotal).SampleCount, 0)
	require.Greater(t, findDataShareExportDurationPart(t, stats, DataShareExportDurationPartWriteCompress).SampleCount, 0)
}

func TestDataSharingService_ExportArtifactRemoteConfigDefaultsAndSavesConfig(t *testing.T) {
	repo := &dataShareSettingRepoStub{values: map[string]string{}}
	svc := NewDataSharingService(nil, repo)
	svc.SetExportObjectStoreDeps(nil, dataSharePlainEncryptor{})

	cfg, err := svc.GetExportRemoteConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, defaultDataShareExportArtifactRemotePrefix, cfg.Prefix)
	require.Equal(t, "auto", cfg.Region)

	cfg, err = svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Endpoint:        " https://r2.example.test ",
		Region:          "",
		Bucket:          "shared-bucket",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Prefix:          "/team/../data-sharing//exports/",
		ForcePathStyle:  true,
	})
	require.NoError(t, err)
	require.Equal(t, "https://r2.example.test", cfg.Endpoint)
	require.Equal(t, "auto", cfg.Region)
	require.Equal(t, "shared-bucket", cfg.Bucket)
	require.Equal(t, "ak", cfg.AccessKeyID)
	require.Empty(t, cfg.SecretAccessKey)
	require.Equal(t, "team/data-sharing/exports", cfg.Prefix)
	require.True(t, cfg.ForcePathStyle)
	require.Equal(t, defaultDataShareExportRemoteUploadConcurrency, cfg.UploadConcurrency)
	require.Equal(t, defaultDataShareExportRemoteUploadPartSizeMB, cfg.UploadPartSizeMB)
	require.JSONEq(t, `{"endpoint":"https://r2.example.test","region":"auto","bucket":"shared-bucket","access_key_id":"ak","secret_access_key":"ENC:sk","prefix":"team/data-sharing/exports","force_path_style":true,"upload_concurrency":4,"upload_part_size_mb":64}`, repo.values[SettingKeyDataSharingExportRemoteConfig])
	cfg, err = svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Endpoint:          "https://r2.example.test",
		Bucket:            "shared-bucket",
		AccessKeyID:       "ak",
		SecretAccessKey:   "sk",
		UploadConcurrency: 99,
		UploadPartSizeMB:  512,
	})
	require.NoError(t, err)
	require.Equal(t, maxDataShareExportRemoteUploadConcurrency, cfg.UploadConcurrency)
	require.Equal(t, maxDataShareExportRemoteUploadPartSizeMB, cfg.UploadPartSizeMB)
}

func TestDataSharingService_UploadExportArtifactToRemoteAndPresignURL(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{}}
	store := newDataShareExportObjectStoreStub()
	factory := func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}
	svc := NewDataSharingService(nil, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	svc.SetExportObjectStoreDeps(factory, dataSharePlainEncryptor{})
	_, err := svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Region:          "auto",
		Prefix:          "exports/share",
	})
	require.NoError(t, err)

	path := filepath.Join(svc.exportStorageDir, "artifact.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(`{"ok":true}`+"\n"), 0644))
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:      DataShareExportArtifactStatusCompleted,
		Filename:    "artifact.jsonl",
		StoragePath: path,
		Encoding:    string(DataShareExportEncodingJSONL),
		FileSize:    12,
	})
	require.NoError(t, err)

	uploaded, err := svc.UploadExportArtifactToRemote(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactRemoteStatusUploading, uploaded.RemoteStatus)
	require.Eventually(t, func() bool {
		got, err := svc.GetExportArtifact(context.Background(), artifact.ID)
		if err != nil || got.RemoteStatus != DataShareExportArtifactRemoteStatusUploaded {
			return false
		}
		uploaded = got
		return true
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, DataShareExportArtifactRemoteStatusUploaded, uploaded.RemoteStatus)
	require.Equal(t, "bucket-a", uploaded.RemoteBucket)
	require.Contains(t, uploaded.RemoteKey, "exports/share/")
	require.Contains(t, uploaded.RemoteKey, "artifact.jsonl")
	require.NotNil(t, uploaded.RemoteUploadedAt)
	store.mu.Lock()
	_, exists := store.objects[uploaded.RemoteKey]
	store.mu.Unlock()
	require.True(t, exists)

	url, err := svc.CreateExportArtifactRemoteDownloadURL(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, "https://download.example.test/"+uploaded.RemoteKey, url)
}

func TestDataSharingService_UploadExportArtifactToRemoteUsesDedicatedConfig(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{}}
	store := newDataShareExportObjectStoreStub()
	var usedCfg BackupS3Config
	factory := func(_ context.Context, cfg *BackupS3Config) (BackupObjectStore, error) {
		usedCfg = *cfg
		return store, nil
	}
	svc := NewDataSharingService(nil, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	svc.SetExportObjectStoreDeps(factory, dataSharePlainEncryptor{})
	_, err := svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Endpoint:          "https://data-share.example.test",
		Region:            "auto",
		Bucket:            "data-share-bucket",
		AccessKeyID:       "data-share-ak",
		SecretAccessKey:   "data-share-secret",
		Prefix:            "share",
		UploadConcurrency: 7,
		UploadPartSizeMB:  128,
	})
	require.NoError(t, err)
	backupData, err := json.Marshal(BackupStorageConfig{
		Type: BackupStorageTypeS3,
		S3: BackupS3Config{
			Bucket:          "backup-bucket",
			AccessKeyID:     "backup-ak",
			SecretAccessKey: "ENC:backup-secret",
		},
	})
	require.NoError(t, err)
	require.NoError(t, settingRepo.Set(context.Background(), settingKeyBackupStorageConfig, string(backupData)))
	path := filepath.Join(svc.exportStorageDir, "artifact.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:      DataShareExportArtifactStatusCompleted,
		Filename:    "artifact.jsonl",
		StoragePath: path,
		Encoding:    string(DataShareExportEncodingJSONL),
	})
	require.NoError(t, err)

	uploaded, err := svc.UploadExportArtifactToRemote(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		got, err := svc.GetExportArtifact(context.Background(), artifact.ID)
		if err != nil || got.RemoteStatus != DataShareExportArtifactRemoteStatusUploaded {
			return false
		}
		uploaded = got
		return true
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, DataShareExportArtifactRemoteStatusUploaded, uploaded.RemoteStatus)
	require.Equal(t, "data-share-bucket", uploaded.RemoteBucket)
	require.Equal(t, "data-share-bucket", usedCfg.Bucket)
	require.Equal(t, "data-share-ak", usedCfg.AccessKeyID)
	require.Equal(t, "data-share-secret", usedCfg.SecretAccessKey)
	require.Equal(t, 7, usedCfg.UploadConcurrency)
	require.Equal(t, 128, usedCfg.UploadPartSizeMB)
}

func TestDataSharingService_UploadExportArtifactToRemoteRecordsFailure(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{}}
	store := newDataShareExportObjectStoreStub()
	store.uploadErr = errors.New("upload failed")
	factory := func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}
	svc := NewDataSharingService(nil, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	svc.SetExportObjectStoreDeps(factory, dataSharePlainEncryptor{})
	_, err := svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Prefix:          "share",
	})
	require.NoError(t, err)
	path := filepath.Join(svc.exportStorageDir, "artifact.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:      DataShareExportArtifactStatusCompleted,
		Filename:    "artifact.jsonl",
		StoragePath: path,
		Encoding:    string(DataShareExportEncodingJSONL),
	})
	require.NoError(t, err)

	_, err = svc.UploadExportArtifactToRemote(context.Background(), artifact.ID)
	require.NoError(t, err)
	var got *DataShareExportArtifact
	require.Eventually(t, func() bool {
		got, err = svc.GetExportArtifact(context.Background(), artifact.ID)
		return err == nil && got.RemoteStatus == DataShareExportArtifactRemoteStatusFailed
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, DataShareExportArtifactRemoteStatusFailed, got.RemoteStatus)
	require.Contains(t, got.RemoteErrorMessage, "upload failed")
}

func TestDataSharingService_UploadExportArtifactToRemoteKeepsOldRemoteFileOnRetryFailure(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{}}
	store := newDataShareExportObjectStoreStub()
	factory := func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}
	svc := NewDataSharingService(nil, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	svc.SetExportObjectStoreDeps(factory, dataSharePlainEncryptor{})
	_, err := svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Prefix:          "share",
	})
	require.NoError(t, err)
	path := filepath.Join(svc.exportStorageDir, "artifact.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:       DataShareExportArtifactStatusCompleted,
		Filename:     "artifact.jsonl",
		StoragePath:  path,
		Encoding:     string(DataShareExportEncodingJSONL),
		RemoteStatus: DataShareExportArtifactRemoteStatusUploaded,
		RemoteBucket: "bucket-a",
		RemoteKey:    "share/2026/06/18/1-artifact.jsonl",
	})
	require.NoError(t, err)
	store.uploadErr = errors.New("retry upload failed")

	_, err = svc.UploadExportArtifactToRemote(context.Background(), artifact.ID)
	require.NoError(t, err)
	var got *DataShareExportArtifact
	require.Eventually(t, func() bool {
		got, err = svc.GetExportArtifact(context.Background(), artifact.ID)
		return err == nil && strings.Contains(got.RemoteErrorMessage, "retry upload failed")
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, DataShareExportArtifactRemoteStatusUploaded, got.RemoteStatus)
	require.Equal(t, "share/2026/06/18/1-artifact.jsonl", got.RemoteKey)

	url, err := svc.CreateExportArtifactRemoteDownloadURL(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, "https://download.example.test/share/2026/06/18/1-artifact.jsonl", url)
}

func TestDataSharingService_CreateExportArtifactRemoteDownloadURLAllowsUploadingWithOldKey(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{}}
	store := newDataShareExportObjectStoreStub()
	factory := func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}
	svc := NewDataSharingService(nil, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportObjectStoreDeps(factory, dataSharePlainEncryptor{})
	_, err := svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Prefix:          "share",
	})
	require.NoError(t, err)
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:       DataShareExportArtifactStatusCompleted,
		Filename:     "artifact.jsonl",
		Encoding:     string(DataShareExportEncodingJSONL),
		RemoteStatus: DataShareExportArtifactRemoteStatusUploading,
		RemoteBucket: "bucket-a",
		RemoteKey:    "share/old-artifact.jsonl",
	})
	require.NoError(t, err)

	url, err := svc.CreateExportArtifactRemoteDownloadURL(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, "https://download.example.test/share/old-artifact.jsonl", url)
}

func TestDataSharingService_CancelExportArtifactRemoteUploadStopsRunningTask(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{}}
	store := newDataShareBlockingExportObjectStoreStub()
	factory := func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}
	svc := NewDataSharingService(nil, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	svc.SetExportObjectStoreDeps(factory, dataSharePlainEncryptor{})
	_, err := svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Prefix:          "share",
	})
	require.NoError(t, err)
	path := filepath.Join(svc.exportStorageDir, "cancel-upload.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(`{"ok":true}`+"\n"), 0644))
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:      DataShareExportArtifactStatusCompleted,
		Filename:    "cancel-upload.jsonl",
		StoragePath: path,
		Encoding:    string(DataShareExportEncodingJSONL),
		FileSize:    12,
	})
	require.NoError(t, err)

	uploaded, err := svc.UploadExportArtifactToRemote(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactRemoteStatusUploading, uploaded.RemoteStatus)
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("upload task did not start")
	}

	cancelled, err := svc.CancelExportArtifactRemoteUpload(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactRemoteStatusFailed, cancelled.RemoteStatus)
	require.Equal(t, dataShareExportArtifactRemoteCancelMessage, cancelled.RemoteErrorMessage)
	require.Eventually(t, func() bool {
		got, err := svc.GetExportArtifact(context.Background(), artifact.ID)
		return err == nil && got.RemoteStatus == DataShareExportArtifactRemoteStatusFailed && got.RemoteUploadBytes == 0
	}, time.Second, 10*time.Millisecond)

	_, err = svc.CancelExportArtifactRemoteUpload(context.Background(), artifact.ID)
	require.ErrorIs(t, err, ErrDataShareExportArtifactRemoteUploadNotRunning)
}

func TestDataSharingService_UploadExportArtifactToRemoteRejectsConcurrentTask(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	settingRepo := &dataShareSettingRepoStub{values: map[string]string{}}
	store := newDataShareBlockingExportObjectStoreStub()
	factory := func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}
	svc := NewDataSharingService(nil, settingRepo)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(t.TempDir())
	svc.SetExportObjectStoreDeps(factory, dataSharePlainEncryptor{})
	_, err := svc.UpdateExportRemoteConfig(context.Background(), DataShareExportRemoteConfig{
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Prefix:          "share",
	})
	require.NoError(t, err)

	pathA := filepath.Join(svc.exportStorageDir, "upload-a.jsonl")
	pathB := filepath.Join(svc.exportStorageDir, "upload-b.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(pathA), 0755))
	require.NoError(t, os.WriteFile(pathA, []byte(`{"a":true}`+"\n"), 0644))
	require.NoError(t, os.WriteFile(pathB, []byte(`{"b":true}`+"\n"), 0644))
	artifactA, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:      DataShareExportArtifactStatusCompleted,
		Filename:    "upload-a.jsonl",
		StoragePath: pathA,
		Encoding:    string(DataShareExportEncodingJSONL),
		FileSize:    11,
	})
	require.NoError(t, err)
	artifactB, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:       DataShareExportArtifactStatusCompleted,
		Filename:     "upload-b.jsonl",
		StoragePath:  pathB,
		Encoding:     string(DataShareExportEncodingJSONL),
		FileSize:     11,
		RemoteStatus: DataShareExportArtifactRemoteStatusNotUploaded,
	})
	require.NoError(t, err)

	_, err = svc.UploadExportArtifactToRemote(context.Background(), artifactA.ID)
	require.NoError(t, err)
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("upload task did not start")
	}

	_, err = svc.UploadExportArtifactToRemote(context.Background(), artifactB.ID)
	require.ErrorIs(t, err, ErrDataShareExportArtifactRemoteUploadInProgress)
	gotB, err := svc.GetExportArtifact(context.Background(), artifactB.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactRemoteStatusNotUploaded, gotB.RemoteStatus)

	_, err = svc.CancelExportArtifactRemoteUpload(context.Background(), artifactA.ID)
	require.NoError(t, err)
}

func TestDataSharingService_ListExportArtifactsMergesUploadProgress(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	svc := NewDataSharingService(nil, nil)
	svc.SetExportArtifactRepository(artifactRepo)
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:       DataShareExportArtifactStatusCompleted,
		Filename:     "artifact.jsonl",
		Encoding:     string(DataShareExportEncodingJSONL),
		FileSize:     1000,
		RemoteStatus: DataShareExportArtifactRemoteStatusUploading,
	})
	require.NoError(t, err)

	svc.startExportArtifactUploadProgress(artifact.ID, artifact.FileSize)
	svc.updateExportArtifactUploadProgress(artifact.ID, 250)
	items, _, err := svc.ListExportArtifacts(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(250), items[0].RemoteUploadBytes)
	require.Greater(t, items[0].RemoteUploadSpeed, 0.0)
}

func TestDataSharingService_ListExportArtifactsMergesGenerateProgress(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	svc := NewDataSharingService(nil, nil)
	svc.SetExportArtifactRepository(artifactRepo)
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:   DataShareExportArtifactStatusPending,
		Filename: "artifact.jsonl",
		Encoding: string(DataShareExportEncodingJSONL),
	})
	require.NoError(t, err)
	require.NoError(t, artifactRepo.MarkRunning(context.Background(), artifact.ID))

	svc.startExportArtifactGenerateProgress(artifact.ID, 100)
	svc.updateExportArtifactGenerateProgress(artifact.ID, 25, 100)
	items, _, err := svc.ListExportArtifacts(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(25), items[0].GenerateProgressDone)
	require.Equal(t, int64(100), items[0].GenerateProgressTotal)
	require.Equal(t, 25.0, items[0].GenerateProgressPercent)
}

func TestDataSharingService_GenerateExportArtifactCleansFinalFileWhenMarkCompletedFails(t *testing.T) {
	session := DataShareSession{
		TrajectoryID:  "traj-artifact-cleanup",
		SessionID:     "sess-artifact-cleanup",
		Dataset:       defaultDataShareDataset,
		Provider:      PlatformOpenAI,
		Model:         "gpt-5.5",
		Messages:      []map[string]any{{"role": "user", "content": "hello"}},
		SessionJSON:   map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}},
		Exportable:    true,
		QualityStatus: DataShareQualityComplete,
		UserID:        1,
		APIKeyID:      2,
		GroupID:       3,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	artifactRepo := newDataShareExportArtifactRepoStub()
	artifactRepo.markCompletedErr = errors.New("mark completed failed")
	storageDir := t.TempDir()
	svc := NewDataSharingService(&dataShareExportRepoStub{items: []DataShareSession{session}}, nil)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(storageDir)
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:   DataShareExportArtifactStatusPending,
		Filename: normalizeDataShareExportFilename("artifact-cleanup", DataShareExportEncodingJSONL),
		Encoding: string(DataShareExportEncodingJSONL),
		Filters:  DataShareSessionFilters{IDs: []int64{1}},
	})
	require.NoError(t, err)
	finalPath := filepath.Join(storageDir, dataShareExportArtifactStorageFilename(artifact.ID, artifact.Filename))

	err = svc.generateExportArtifactWithError(context.Background(), artifact.ID)
	require.ErrorContains(t, err, "mark completed failed")
	require.NoFileExists(t, finalPath)

	got, err := svc.GetExportArtifact(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactStatusRunning, got.Status)
	require.Empty(t, got.StoragePath)
}

func TestDataSharingService_DeleteExportArtifactRejectsRunning(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	svc := NewDataSharingService(nil, nil)
	svc.SetExportArtifactRepository(artifactRepo)
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:   DataShareExportArtifactStatusRunning,
		Filename: "running.jsonl",
		Encoding: string(DataShareExportEncodingJSONL),
	})
	require.NoError(t, err)

	err = svc.DeleteExportArtifact(context.Background(), artifact.ID)
	require.ErrorIs(t, err, ErrDataShareExportArtifactNotReady)
}

func TestDataSharingService_DeleteExportArtifactRejectsRemoteUploading(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	storageDir := t.TempDir()
	svc := NewDataSharingService(nil, nil)
	svc.SetExportArtifactRepository(artifactRepo)
	svc.SetExportStorageDir(storageDir)
	path := filepath.Join(storageDir, "uploading.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))
	artifact, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:       DataShareExportArtifactStatusCompleted,
		Filename:     "uploading.jsonl",
		StoragePath:  path,
		Encoding:     string(DataShareExportEncodingJSONL),
		RemoteStatus: DataShareExportArtifactRemoteStatusUploading,
	})
	require.NoError(t, err)

	err = svc.DeleteExportArtifact(context.Background(), artifact.ID)
	require.ErrorIs(t, err, ErrDataShareExportArtifactRemoteUploadInProgress)
	require.FileExists(t, path)

	got, err := svc.GetExportArtifact(context.Background(), artifact.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactStatusCompleted, got.Status)
	require.Equal(t, path, got.StoragePath)
}

func TestDataSharingService_RecoverInterruptedExportArtifactsMarksActiveTasksFailed(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	svc := NewDataSharingService(nil, nil)
	svc.SetExportArtifactRepository(artifactRepo)
	pending, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:   DataShareExportArtifactStatusPending,
		Filename: "pending.jsonl",
		Encoding: string(DataShareExportEncodingJSONL),
	})
	require.NoError(t, err)
	running, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:   DataShareExportArtifactStatusRunning,
		Filename: "running.jsonl",
		Encoding: string(DataShareExportEncodingJSONL),
	})
	require.NoError(t, err)
	completed, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:   DataShareExportArtifactStatusCompleted,
		Filename: "completed.jsonl",
		Encoding: string(DataShareExportEncodingJSONL),
	})
	require.NoError(t, err)

	affected, err := svc.RecoverInterruptedExportArtifacts(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), affected)

	gotPending, err := svc.GetExportArtifact(context.Background(), pending.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactStatusFailed, gotPending.Status)
	require.Equal(t, dataShareExportArtifactInterruptedMessage, gotPending.ErrorMessage)

	gotRunning, err := svc.GetExportArtifact(context.Background(), running.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactStatusFailed, gotRunning.Status)
	require.Equal(t, dataShareExportArtifactInterruptedMessage, gotRunning.ErrorMessage)

	gotCompleted, err := svc.GetExportArtifact(context.Background(), completed.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactStatusCompleted, gotCompleted.Status)
	require.Empty(t, gotCompleted.ErrorMessage)
}

func TestDataSharingService_RecoverInterruptedExportArtifactRemoteUploadsMarksUploadingFailed(t *testing.T) {
	artifactRepo := newDataShareExportArtifactRepoStub()
	svc := NewDataSharingService(nil, nil)
	svc.SetExportArtifactRepository(artifactRepo)
	freshUpload, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:       DataShareExportArtifactStatusCompleted,
		Filename:     "fresh.jsonl",
		Encoding:     string(DataShareExportEncodingJSONL),
		RemoteStatus: DataShareExportArtifactRemoteStatusUploading,
	})
	require.NoError(t, err)
	retryUpload, err := artifactRepo.Create(context.Background(), &DataShareExportArtifact{
		Status:       DataShareExportArtifactStatusCompleted,
		Filename:     "retry.jsonl",
		Encoding:     string(DataShareExportEncodingJSONL),
		RemoteStatus: DataShareExportArtifactRemoteStatusUploading,
		RemoteKey:    "share/retry.jsonl",
	})
	require.NoError(t, err)

	affected, err := svc.RecoverInterruptedExportArtifactRemoteUploads(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), affected)

	gotFresh, err := svc.GetExportArtifact(context.Background(), freshUpload.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactRemoteStatusFailed, gotFresh.RemoteStatus)
	require.Equal(t, dataShareExportArtifactRemoteInterruptedMessage, gotFresh.RemoteErrorMessage)

	gotRetry, err := svc.GetExportArtifact(context.Background(), retryUpload.ID)
	require.NoError(t, err)
	require.Equal(t, DataShareExportArtifactRemoteStatusUploaded, gotRetry.RemoteStatus)
	require.Equal(t, "share/retry.jsonl", gotRetry.RemoteKey)
	require.Equal(t, dataShareExportArtifactRemoteInterruptedMessage, gotRetry.RemoteErrorMessage)
}

func TestWriteSingleSessionJSONLRedactsSensitiveFields(t *testing.T) {
	session := &DataShareSession{
		TrajectoryID:       "traj-single-redact",
		SessionID:          "sess-single-redact",
		Dataset:            defaultDataShareDataset,
		Provider:           PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "hello"}},
		Usage:              map[string]any{"total_tokens": 1},
		Meta:               map[string]any{"api_key_id": int64(10), "request_id": "req-single"},
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	var buf bytes.Buffer
	require.NoError(t, WriteSingleSessionJSONL(&buf, session))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload))
	requireNoDataShareExportSensitiveFields(t, payload)
	meta, ok := payload["meta"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "req-single", meta["request_id"])
}

func requireNoDataShareExportSensitiveFields(t *testing.T, value any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if _, excluded := dataShareExportExcludedFields[key]; excluded {
				t.Fatalf("export payload contains sensitive field %q in %#v", key, v)
			}
			requireNoDataShareExportSensitiveFields(t, item)
		}
	case []any:
		for _, item := range v {
			requireNoDataShareExportSensitiveFields(t, item)
		}
	}
}

func countDataShareMessagesWithContent(messages []map[string]any, content string) int {
	count := 0
	for _, msg := range messages {
		if dataShareContentText(msg["content"]) == content {
			count++
		}
	}
	return count
}

func countDataShareToolCallID(messages []map[string]any, callID string) int {
	count := 0
	for _, msg := range messages {
		for _, raw := range anySlice(msg["tool_calls"]) {
			call, ok := mapFromAny(raw)
			if ok && stringFromAny(call["id"]) == callID {
				count++
			}
		}
	}
	return count
}

func countDataShareMessagesWithToolCallID(messages []map[string]any, callID string) int {
	count := 0
	for _, msg := range messages {
		if stringFromAny(msg["tool_call_id"]) == callID {
			count++
		}
	}
	return count
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
