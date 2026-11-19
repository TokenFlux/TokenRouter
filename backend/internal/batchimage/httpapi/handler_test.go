package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

// HTTP fixtures capture the owner and submission metadata passed to use cases
// and count stream closure and download recording. The tests exercise malformed
// JSON ordering, successful response delivery, and alias expansion across
// multiple platform rows sharing a target model.

// contractBatchUseCases captures submission inputs and counts download
// recording for focused HTTP handler tests.
type contractBatchUseCases struct {
	// UseCases remains nil in these fixtures and satisfies unexercised method
	// requirements through embedding. Tests invoke the overridden methods; calling
	// a remaining embedded method would panic through that nil interface.
	UseCases // unused methods delegated through embedding
	// owner, input, and key retain the most recent Submit arguments for handler
	// assertions. Submit assigns the values directly, so input's nested references
	// remain shared with the passed request.
	owner  batchimage.BatchImageOwner         // last submitted owner value
	input  batchimage.BatchImageSubmitRequest // last submitted request value
	key    string                             // last submitted idempotency key
	marked atomic.Int64                       // observed download recording count
}

// contractDownload supplies an image body whose closure is observable in
// HTTP handler tests.
type contractDownload struct {
	// DownloadUseCases remains nil while its overridden OpenItemContent
	// supplies the tested stream. Tests use that method; invoking the embedded
	// StreamZip method would panic through the nil interface.
	DownloadUseCases              // unused methods delegated through embedding
	closed           atomic.Int64 // observed image body closures
}

// contractBody wraps fixture image bytes and counts Close calls for the
// download response test.
type contractBody struct {
	io.Reader // fixture image byte reader
	// closed points to the owning contractDownload counter, so closing a
	// returned body contributes to the same observation inspected after the HTTP
	// request finishes.
	closed *atomic.Int64 // shared body closure counter
}

// Submit captures the supplied owner, input, and idempotency key for assertions.
// It returns an empty public batch successfully; the captured input retains
// the reference fields of the handler's submitted value.
func (s *contractBatchUseCases) Submit(_ context.Context, owner batchimage.BatchImageOwner, input batchimage.BatchImageSubmitRequest, key string) (*batchimage.BatchImagePublicBatch, error) {
	s.owner = owner
	s.input = input
	s.key = key
	return &batchimage.BatchImagePublicBatch{}, nil
}

// MarkDownloaded increments the fixture's observed successful-delivery count
// and returns nil.
func (s *contractBatchUseCases) MarkDownloaded(context.Context, batchimage.BatchImageOwner, string) error {
	s.marked.Add(1)
	return nil
}

// Close increments the shared stream-closure count and returns success.
func (b contractBody) Close() error { b.closed.Add(1); return nil }

// OpenItemContent returns fixed PNG metadata and a close-counting image stream.
// The handler owns closing that stream after copying; each opened body uses
// the same fixture counter for assertions.
func (d *contractDownload) OpenItemContent(context.Context, batchimage.BatchImageOwner, string, string, int) (*batchimage.BatchImageContentStream, error) {
	return &batchimage.BatchImageContentStream{Reader: contractBody{Reader: strings.NewReader("image-data"), closed: &d.closed}, ContentType: "image/png", Filename: "output.png"}, nil
}

// TestBatchHTTPSubmitProjectionAndParseOrder sends malformed JSON before a
// valid authenticated submission. It checks parsing precedes owner lookup and
// that successful submission receives payer, API key, session, and idempotency
// values.
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
	router.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/batches", strings.NewReader("{")))
	require.Equal(t, 400, bad.Code)
	require.Zero(t, authCalls.Load())
	req := httptest.NewRequest(http.MethodPost, "/batches", strings.NewReader(`{"model":"image","items":[{"prompt":"hello"}]}`))
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

// TestBatchHTTPDownloadClosesStream reads one fixture image through the HTTP
// handler. It checks the body and attachment metadata, then observes one stream
// close and one successful-download recording call.
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

// TestAppendBatchImageAPIKeyModelAliasesPreservesProvider maps an exact alias
// to a model with two platform rows. Both alias rows retain their platforms,
// and the wildcard source adds no advertised model entry.
func TestAppendBatchImageAPIKeyModelAliasesPreservesProvider(t *testing.T) {
	models := AppendBatchImageAPIKeyModelAliases([]batchimage.BatchImagePublicModel{
		{ID: "gemini-image", Object: "image.batch.model", Platform: "gemini"},
		{ID: "gemini-image", Object: "image.batch.model", Platform: "antigravity"},
	}, map[string]string{
		"image-review": "gemini-image",
		"wild-*":       "gemini-image",
	})

	require.Equal(t, []batchimage.BatchImagePublicModel{
		{ID: "gemini-image", Object: "image.batch.model", Platform: "gemini"},
		{ID: "gemini-image", Object: "image.batch.model", Platform: "antigravity"},
		{ID: "image-review", Object: "image.batch.model", Platform: "gemini"},
		{ID: "image-review", Object: "image.batch.model", Platform: "antigravity"},
	}, models)
}

// TestBatchHTTPSubmitRejectsDuplicateAndCaseVariantFieldsBeforeAuth sends
// batch submissions with duplicate model keys and uppercase Model variants.
// Submit must return HTTP 400 before consulting API key authentication.
func TestBatchHTTPSubmitRejectsDuplicateAndCaseVariantFieldsBeforeAuth(t *testing.T) {
	usecases := &contractBatchUseCases{}
	var authCalls atomic.Int64
	access := AccessPorts{Key: func(*gin.Context) (*apikey.APIKey, bool) {
		authCalls.Add(1)
		return &apikey.APIKey{ID: 17, UserID: 21, BillingMode: apikey.APIKeyBillingModeBalance}, true
	}, SessionID: func(*gin.Context) string { return "session-1" }}
	h := NewBatchImageHandler(usecases, nil, nil, access)
	router := gin.New()
	router.POST("/batches", h.Submit)

	for _, body := range []string{
		`{"model":"cheap-image","items":[{"prompt":"hello"}],"model":"expensive-image"}`,
		`{"model":"cheap-image","Model":"expensive-image","items":[{"prompt":"hello"}]}`,
		`{"model":"cheap-image","items":[{"prompt":"hello","Model":"expensive-image"}]}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/batches", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		require.Zero(t, authCalls.Load(), body)
	}
}
