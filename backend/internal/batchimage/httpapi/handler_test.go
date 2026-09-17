package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type contractBatchUseCases struct {
	UseCases
	owner  batchimage.BatchImageOwner
	input  batchimage.BatchImageSubmitRequest
	key    string
	marked atomic.Int64
}

func (s *contractBatchUseCases) Submit(_ context.Context, owner batchimage.BatchImageOwner, input batchimage.BatchImageSubmitRequest, key string) (*batchimage.BatchImagePublicBatch, error) {
	s.owner = owner
	s.input = input
	s.key = key
	return &batchimage.BatchImagePublicBatch{}, nil
}
func (s *contractBatchUseCases) MarkDownloaded(context.Context, batchimage.BatchImageOwner, string) error {
	s.marked.Add(1)
	return nil
}

type contractDownload struct {
	DownloadUseCases
	closed atomic.Int64
}
type contractBody struct {
	io.Reader
	closed *atomic.Int64
}

func (b contractBody) Close() error { b.closed.Add(1); return nil }
func (d *contractDownload) OpenItemContent(context.Context, batchimage.BatchImageOwner, string, string, int) (*batchimage.BatchImageContentStream, error) {
	return &batchimage.BatchImageContentStream{Reader: contractBody{Reader: strings.NewReader("image-data"), closed: &d.closed}, ContentType: "image/png", Filename: "output.png"}, nil
}

// HTTP 层保留付款主体、会话和幂等输入，不能让任务核心再次解析凭据。
func TestBatchHTTPSubmitProjectionAndParseOrder(t *testing.T) {
	usecases := &contractBatchUseCases{}
	var authCalls atomic.Int64
	access := AccessPorts{Key: func(*gin.Context) (*apikey.APIKey, bool) {
		authCalls.Add(1)
		return &apikey.APIKey{ID: 17, UserID: 21, BillingMode: apikey.APIKeyBillingModeBalance}, true
	}, SessionID: func(*gin.Context) string { return "session-1" }}
	h := NewBatchImageHandler(usecases, nil, nil, access)
	router := gin.New()
	router.POST("/batches", h.Submit)
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, httptest.NewRequest("POST", "/batches", strings.NewReader("{")))
	require.Equal(t, 400, bad.Code)
	require.Zero(t, authCalls.Load())
	req := httptest.NewRequest("POST", "/batches", strings.NewReader(`{"model":"image","items":[{"prompt":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "same-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	require.Equal(t, 200, response.Code)
	require.Equal(t, int64(17), usecases.owner.APIKeyID)
	require.Equal(t, int64(21), usecases.owner.BillingUserID)
	require.Equal(t, "same-key", usecases.key)
	require.Equal(t, "session-1", *usecases.input.SessionID)
}

// 下载响应写完后关闭唯一受控流，并按原时机记下载状态。
func TestBatchHTTPDownloadClosesStream(t *testing.T) {
	usecases := &contractBatchUseCases{}
	download := &contractDownload{}
	h := NewBatchImageHandler(usecases, download, nil, AccessPorts{Key: func(*gin.Context) (*apikey.APIKey, bool) { return &apikey.APIKey{ID: 1, UserID: 2}, true }})
	router := gin.New()
	router.GET("/batches/:id/items/:custom_id/content", h.ItemContent)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/batches/job/items/item/content", nil))
	require.Equal(t, 200, response.Code)
	require.Equal(t, "image-data", response.Body.String())
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	require.Contains(t, response.Header().Get("Content-Disposition"), "output.png")
	require.Equal(t, int64(1), download.closed.Load())
	require.Equal(t, int64(1), usecases.marked.Load())
}
