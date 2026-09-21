package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	idempotencytest "github.com/TokenFlux/TokenRouter/internal/idempotency/testkit"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type storeUnavailableRepoStub struct{}

func (storeUnavailableRepoStub) CreateProcessing(context.Context, *idempotency.IdempotencyRecord) (bool, error) {
	return false, errors.New("store unavailable")
}
func (storeUnavailableRepoStub) GetByScopeAndKeyHash(context.Context, string, string) (*idempotency.IdempotencyRecord, error) {
	return nil, errors.New("store unavailable")
}
func (storeUnavailableRepoStub) TryReclaim(context.Context, int64, string, time.Time, time.Time, time.Time) (bool, error) {
	return false, errors.New("store unavailable")
}
func (storeUnavailableRepoStub) ExtendProcessingLock(context.Context, int64, string, time.Time, time.Time) (bool, error) {
	return false, errors.New("store unavailable")
}
func (storeUnavailableRepoStub) MarkSucceeded(context.Context, int64, int, string, time.Time) error {
	return errors.New("store unavailable")
}
func (storeUnavailableRepoStub) MarkFailedRetryable(context.Context, int64, string, time.Time, time.Time) error {
	return errors.New("store unavailable")
}
func (storeUnavailableRepoStub) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, errors.New("store unavailable")
}

func TestExecuteAdminIdempotentJSONFailCloseOnStoreUnavailable(t *testing.T) {

	idempotency.SetDefaultIdempotencyCoordinator(idempotency.NewIdempotencyCoordinator(storeUnavailableRepoStub{}, idempotency.DefaultIdempotencyConfig()))
	t.Cleanup(func() {
		idempotency.SetDefaultIdempotencyCoordinator(nil)
	})

	var executed int
	router := gin.New()
	router.POST("/idempotent", func(c *gin.Context) {
		ExecuteAdminIdempotentJSON(c, "admin.test.high", map[string]any{"a": 1}, time.Minute, func(ctx context.Context) (any, error) {
			executed++
			return gin.H{"ok": true}, nil
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/idempotent", bytes.NewBufferString(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-key-1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, 0, executed, "fail-close should block business execution when idempotency store is unavailable")
}

func TestExecuteAdminIdempotentJSONFailOpenOnStoreUnavailable(t *testing.T) {

	idempotency.SetDefaultIdempotencyCoordinator(idempotency.NewIdempotencyCoordinator(storeUnavailableRepoStub{}, idempotency.DefaultIdempotencyConfig()))
	t.Cleanup(func() {
		idempotency.SetDefaultIdempotencyCoordinator(nil)
	})

	var executed int
	router := gin.New()
	router.POST("/idempotent", func(c *gin.Context) {
		ExecuteAdminIdempotentJSONFailOpenOnStoreUnavailable(c, "admin.test.medium", map[string]any{"a": 1}, time.Minute, func(ctx context.Context) (any, error) {
			executed++
			return gin.H{"ok": true}, nil
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/idempotent", bytes.NewBufferString(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-key-2")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "store-unavailable", rec.Header().Get("X-Idempotency-Degraded"))
	require.Equal(t, 1, executed, "fail-open strategy should allow semantic idempotent path to continue")
}

func TestExecuteAdminIdempotentJSONConcurrentRetryOnlyOneSideEffect(t *testing.T) {

	repo := idempotencytest.NewMemoryStore()
	cfg := idempotency.DefaultIdempotencyConfig()
	cfg.ProcessingTimeout = 2 * time.Second
	idempotency.SetDefaultIdempotencyCoordinator(idempotency.NewIdempotencyCoordinator(repo, cfg))
	t.Cleanup(func() {
		idempotency.SetDefaultIdempotencyCoordinator(nil)
	})

	var executed atomic.Int32
	router := gin.New()
	router.POST("/idempotent", func(c *gin.Context) {
		ExecuteAdminIdempotentJSON(c, "admin.test.concurrent", map[string]any{"a": 1}, time.Minute, func(ctx context.Context) (any, error) {
			executed.Add(1)
			time.Sleep(120 * time.Millisecond)
			return gin.H{"ok": true}, nil
		})
	})

	call := func() (int, http.Header) {
		req := httptest.NewRequest(http.MethodPost, "/idempotent", bytes.NewBufferString(`{"a":1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "same-key")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code, rec.Header()
	}

	var status1, status2 int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		status1, _ = call()
	}()
	go func() {
		defer wg.Done()
		status2, _ = call()
	}()
	wg.Wait()

	require.Contains(t, []int{http.StatusOK, http.StatusConflict}, status1)
	require.Contains(t, []int{http.StatusOK, http.StatusConflict}, status2)
	require.Equal(t, int32(1), executed.Load(), "same idempotency key should execute side-effect only once")

	status3, headers3 := call()
	require.Equal(t, http.StatusOK, status3)
	require.Equal(t, "true", headers3.Get("X-Idempotency-Replayed"))
	require.Equal(t, int32(1), executed.Load())
}
