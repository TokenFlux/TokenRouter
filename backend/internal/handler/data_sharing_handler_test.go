package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

type dataShareHandlerRepoStub struct {
	items []service.DataShareSession
}

type dataShareHandlerSettingRepoStub struct {
	values map[string]string
}

func (s *dataShareHandlerSettingRepoStub) Get(context.Context, string) (*service.Setting, error) {
	panic("unexpected Get call")
}

func (s *dataShareHandlerSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}

func (s *dataShareHandlerSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}

func (s *dataShareHandlerSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *dataShareHandlerSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *dataShareHandlerSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *dataShareHandlerSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func (r *dataShareHandlerRepoStub) GetCaptureByTrajectoryIDWithPayload(context.Context, string) (*service.DataShareSession, error) {
	panic("unexpected GetCaptureByTrajectoryIDWithPayload call")
}

func (r *dataShareHandlerRepoStub) SaveCaptureSnapshot(context.Context, *service.DataShareSession, ...service.DataShareUpsertOptions) error {
	panic("unexpected SaveCaptureSnapshot call")
}

func (r *dataShareHandlerRepoStub) Count(ctx context.Context, filters service.DataShareSessionFilters) (int64, error) {
	_, result, err := r.ListWithPayload(ctx, pagination.PaginationParams{Page: 1, PageSize: len(r.items)}, filters)
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}
	return result.Total, nil
}

func (r *dataShareHandlerRepoStub) List(context.Context, pagination.PaginationParams, service.DataShareSessionFilters) ([]service.DataShareSession, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (r *dataShareHandlerRepoStub) ListWithPayload(_ context.Context, _ pagination.PaginationParams, filters service.DataShareSessionFilters) ([]service.DataShareSession, *pagination.PaginationResult, error) {
	out := make([]service.DataShareSession, 0, len(r.items))
	for _, item := range r.items {
		if filters.UserID > 0 && item.UserID != filters.UserID {
			continue
		}
		if len(filters.IDs) > 0 {
			matched := false
			for _, id := range filters.IDs {
				if item.ID == id {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, item)
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: 1, PageSize: 1000, Pages: 1}, nil
}

func (r *dataShareHandlerRepoStub) ListWithPayloadPage(ctx context.Context, params pagination.PaginationParams, filters service.DataShareSessionFilters) ([]service.DataShareSession, error) {
	items, _, err := r.ListWithPayload(ctx, params, filters)
	return items, err
}

func (r *dataShareHandlerRepoStub) ListExportPayloadPage(ctx context.Context, filters service.DataShareSessionFilters, _ *service.DataShareSessionExportCursor, _ int, _ int, _ service.DataShareExportDurationRecorder) ([]service.DataShareSession, *service.DataShareSessionExportCursor, error) {
	items, _, err := r.ListWithPayload(ctx, pagination.PaginationParams{Page: 1, PageSize: len(r.items)}, filters)
	if err != nil || len(items) == 0 {
		return items, nil, err
	}
	last := items[len(items)-1]
	return items, &service.DataShareSessionExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (r *dataShareHandlerRepoStub) GetByID(context.Context, int64) (*service.DataShareSession, error) {
	panic("unexpected GetByID call")
}

func (r *dataShareHandlerRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (r *dataShareHandlerRepoStub) BatchDelete(context.Context, []int64, service.DataShareSessionFilters) (int64, error) {
	panic("unexpected BatchDelete call")
}

func (r *dataShareHandlerRepoStub) Stats(context.Context, service.DataShareSessionFilters) (*service.DataShareStats, error) {
	panic("unexpected Stats call")
}

func (r *dataShareHandlerRepoStub) FilterOptions(context.Context, service.DataShareSessionFilters) (*service.DataShareSessionFilterOptions, error) {
	panic("unexpected FilterOptions call")
}

func (r *dataShareHandlerRepoStub) TotalStorageBytes(context.Context) (int64, error) {
	return 0, nil
}

func TestParseDataShareSessionFiltersIncludesRequestPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/data-sharing?request_path=/v1/messages&user_agent=codex-cli&model=gpt-5.5&quality_status=non_invalid&search=/v1&api_key_name=测试Key&group_name=共享分组", nil)

	filters, ok := parseDataShareSessionFilters(c)
	require.True(t, ok)
	require.Equal(t, "/v1/messages", filters.RequestPath)
	require.Equal(t, "codex-cli", filters.UserAgent)
	require.Equal(t, "gpt-5.5", filters.Model)
	require.Equal(t, service.DataShareQualityFilterNonInvalid, filters.QualityStatus)
	require.Equal(t, "/v1", filters.Search)
	require.Equal(t, "测试Key", filters.APIKeyName)
	require.Equal(t, "共享分组", filters.GroupName)
}

func TestDataShareSessionToResponseIncludesRequestPathAndUserAgent(t *testing.T) {
	resp := dataShareSessionToResponse(&service.DataShareSession{
		ID:              1,
		SessionID:       "sess_1",
		RequestPath:     "/v1/chat/completions",
		UserAgent:       "codex-cli/1.0",
		PayloadEncoding: "zstd",
		PayloadBytes:    1234,
	}, false)

	require.Equal(t, "/v1/chat/completions", resp.RequestPath)
	require.Equal(t, "codex-cli/1.0", resp.UserAgent)
	require.Equal(t, "zstd", resp.PayloadEncoding)
	require.Equal(t, int64(1234), resp.PayloadBytes)
}

func TestDownloadExportReturnsZstdJSONL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingRepo := &dataShareHandlerSettingRepoStub{values: map[string]string{}}
	svc := service.NewDataSharingService(&dataShareHandlerRepoStub{items: []service.DataShareSession{{
		ID:                 1,
		TrajectoryID:       "traj",
		SessionID:          "sess",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             service.DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "hi"}},
		Usage:              map[string]any{"total_tokens": 1},
		UserID:             42,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}}}, settingRepo)
	h := NewDataSharingHandler(svc)
	ticket, err := svc.CreateExportTicket(context.Background(), service.DataShareExportTicketRequest{
		Scope:    service.DataShareExportScopeUser,
		UserID:   42,
		Filters:  service.DataShareSessionFilters{IDs: []int64{1}, UserID: 42},
		Filename: "data-sharing-session-1",
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/data-sharing/export/download?ticket="+ticket.Token, nil)
	h.DownloadExport(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/zstd", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Header().Get("Content-Disposition"), ".jsonl.zst")
	dec, err := zstd.NewReader(bytes.NewReader(recorder.Body.Bytes()))
	require.NoError(t, err)
	defer dec.Close()
	data, err := io.ReadAll(dec)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(data), &payload))
	require.Equal(t, "sess", payload["session_id"])
}

func TestDownloadExportReturnsPlainJSONForSingleSessionTicket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingRepo := &dataShareHandlerSettingRepoStub{values: map[string]string{}}
	svc := service.NewDataSharingService(&dataShareHandlerRepoStub{items: []service.DataShareSession{{
		ID:                 1,
		TrajectoryID:       "traj",
		SessionID:          "sess",
		Dataset:            "tokenrouter-agent",
		Provider:           service.PlatformOpenAI,
		Model:              "gpt-5.5",
		Status:             service.DataShareStatusCompleted,
		IsFinalSnapshot:    true,
		SourceRequestCount: 1,
		Messages:           []map[string]any{{"role": "user", "content": "hi"}},
		Usage:              map[string]any{"total_tokens": 1},
		UserID:             42,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}}}, settingRepo)
	h := NewDataSharingHandler(svc)
	ticket, err := svc.CreateExportTicket(context.Background(), service.DataShareExportTicketRequest{
		Scope:    service.DataShareExportScopeUser,
		UserID:   42,
		Filters:  service.DataShareSessionFilters{IDs: []int64{1}, UserID: 42},
		Filename: "data-sharing-session-1",
		Encoding: service.DataShareExportEncodingJSON,
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/data-sharing/export/download?ticket="+ticket.Token, nil)
	h.DownloadExport(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Header().Get("Content-Disposition"), "data-sharing-session-1.json")
	require.NotContains(t, recorder.Header().Get("Content-Disposition"), ".jsonl")
	require.NotContains(t, recorder.Header().Get("Content-Disposition"), ".zst")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(recorder.Body.Bytes()), &payload))
	require.Equal(t, "sess", payload["session_id"])
}
