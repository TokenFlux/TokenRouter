// 等待与串行锁的 HTTP 适配；资源所有权和等待循环复用 scheduler。
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/gin-gonic/gin"
)

const gatewayStreamHeartbeatBytesKey = "gateway_stream_heartbeat_bytes"
const maxConcurrencyWait = 30 * time.Second
const defaultPingInterval = 10 * time.Second

func RecordStreamHeartbeat(c *gin.Context, written int) {
	if c == nil || written <= 0 {
		return
	}
	total, _ := c.Get(gatewayStreamHeartbeatBytesKey)
	bytes, _ := total.(int)
	c.Set(gatewayStreamHeartbeatBytesKey, bytes+written)
}

func StreamHasOnlyHeartbeats(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	value, ok := c.Get(gatewayStreamHeartbeatBytesKey)
	if !ok {
		return false
	}
	heartbeatBytes, _ := value.(int)
	return heartbeatBytes > 0 && c.Writer.Size() == heartbeatBytes
}

// SSEPingFormat defines the format of SSE ping events for different platforms
type SSEPingFormat string

const (
	// SSEPingFormatClaude is the Claude/Anthropic SSE ping format
	SSEPingFormatClaude SSEPingFormat = "data: {\"type\": \"ping\"}\n\n"
	// SSEPingFormatNone indicates no ping should be sent (e.g., OpenAI has no ping spec)
	SSEPingFormatNone SSEPingFormat = ""
	// SSEPingFormatComment is an SSE comment ping for OpenAI/Codex CLI clients
	SSEPingFormatComment SSEPingFormat = ":\n\n"
)

// 旧 HTTP 错误入口使用相同核心类型，保留 errors.As 与字段访问。
type ConcurrencyError = scheduler.ConcurrencyError

type WaitQueueFullError = scheduler.WaitQueueFullError

// ConcurrencyHelper provides common concurrency slot management for gateway handlers
type ConcurrencyHelper struct {
	concurrencyService *scheduler.ConcurrencyService
	keyID              func(*gin.Context) int64
	pingFormat         SSEPingFormat
	pingInterval       time.Duration
}

// NewConcurrencyHelper creates a new ConcurrencyHelper
func NewConcurrencyHelper(concurrencyService *scheduler.ConcurrencyService, pingFormat SSEPingFormat, pingInterval time.Duration, keyID ...func(*gin.Context) int64) *ConcurrencyHelper {
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	resolveKey := func(c *gin.Context) int64 {
		if key, ok := EffectiveAPIKey(c); ok && key != nil {
			return key.ID
		}
		return 0
	}
	if len(keyID) > 0 && keyID[0] != nil {
		resolveKey = keyID[0]
	}
	return &ConcurrencyHelper{
		keyID:              resolveKey,
		concurrencyService: concurrencyService,
		pingFormat:         pingFormat,
		pingInterval:       pingInterval,
	}
}

// EnterUserWait 和 EnterAccountWait 仅转接有所有权的等待结果。
func (h *ConcurrencyHelper) EnterUserWait(ctx context.Context, id int64, limit int) (scheduler.WaitResult, error) {
	return h.concurrencyService.EnterUserWait(ctx, id, limit)
}

func (h *ConcurrencyHelper) EnterAccountWait(ctx context.Context, id int64, limit int) (scheduler.WaitResult, error) {
	return h.concurrencyService.EnterAccountWait(ctx, id, limit)
}

// TryAcquireUserSlot 尝试立即获取用户并发槽位。
// 返回值: (releaseFunc, acquired, error)
func (h *ConcurrencyHelper) TryAcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int) (func(), bool, error) {
	result, err := h.concurrencyService.AcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil {
		return nil, false, err
	}
	if !result.Acquired {
		return nil, false, nil
	}
	return result.ReleaseFunc, true, nil
}

// TryAcquireUserSlotForAPIKey 获取用户槽位并同步记录 API Key 统计槽位。
func (h *ConcurrencyHelper) TryAcquireUserSlotForAPIKey(ctx context.Context, userID int64, maxConcurrency int, apiKeyID int64) (func(), bool, error) {
	releaseFunc, acquired, err := h.TryAcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil || !acquired {
		return releaseFunc, acquired, err
	}
	return h.withAPIKeySlot(ctx, apiKeyID, releaseFunc), true, nil
}

// AcquireOpenAIWSIngressLease 独立于单轮用户和账号槽位，限制整个客户端 WebSocket 生命周期。
func (h *ConcurrencyHelper) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int) (*scheduler.OpenAIWSIngressLease, bool, error) {
	if h == nil || h.concurrencyService == nil {
		return nil, false, fmt.Errorf("concurrency service is unavailable")
	}
	return h.concurrencyService.AcquireOpenAIWSIngressLease(ctx, apiKeyID, maxConnections)
}

// TryAcquireAccountSlot 尝试立即获取账号并发槽位。
// 返回值: (releaseFunc, acquired, error)
func (h *ConcurrencyHelper) TryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (func(), bool, error) {
	result, err := h.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
	if err != nil {
		return nil, false, err
	}
	if !result.Acquired {
		return nil, false, nil
	}
	return result.ReleaseFunc, true, nil
}

// AcquireUserSlotWithWait 获取用户并发槽位，必要时进入等待队列。
// 流式请求等待期间会发送 ping，并在真正写出流时更新 streamStarted。
func (h *ConcurrencyHelper) AcquireUserSlotWithWait(c *gin.Context, userID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	return h.AcquireUserSlotWithWaitTimeout(c, userID, maxConcurrency, maxConcurrencyWait, isStream, streamStarted)
}

func (h *ConcurrencyHelper) AcquireUserSlotWithWaitTimeout(c *gin.Context, userID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	apiKeyID := h.keyID(c)
	lease, _, err := h.concurrencyService.AcquireUser(c.Request.Context(), scheduler.UserAcquireOptions{
		UserID: userID, APIKeyID: apiKeyID, Limit: maxConcurrency, Timeout: timeout,
		Mode: scheduler.ReleaseOnCompletion, Observer: WaitObserver(c, h.pingFormat, h.pingInterval, isStream, streamStarted, true),
	})
	if err != nil {
		return nil, err
	}
	c.Request = c.Request.WithContext(scheduler.WithRequestLease(c.Request.Context(), lease))
	return lease.Release, nil
}

func (h *ConcurrencyHelper) withAPIKeySlot(ctx context.Context, apiKeyID int64, releaseFunc func()) func() {
	if h == nil || h.concurrencyService == nil || apiKeyID <= 0 {
		return releaseFunc
	}
	apiKeyReleaseFunc := h.concurrencyService.TrackAPIKeySlot(ctx, apiKeyID)
	return func() {
		if releaseFunc != nil {
			releaseFunc()
		}
		if apiKeyReleaseFunc != nil {
			apiKeyReleaseFunc()
		}
	}
}

// AcquireAccountSlotWithWait acquires an account concurrency slot, waiting if necessary.
// For streaming requests, sends ping events during the wait.
// streamStarted is updated if streaming response has begun.
func (h *ConcurrencyHelper) AcquireAccountSlotWithWait(c *gin.Context, accountID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	ctx := c.Request.Context()

	// Try to acquire immediately
	releaseFunc, acquired, err := h.TryAcquireAccountSlot(ctx, accountID, maxConcurrency)
	if err != nil {
		return nil, err
	}

	if acquired {
		return releaseFunc, nil
	}

	// Need to wait - handle streaming ping if needed
	return h.waitForSlotWithPing(c, "account", accountID, maxConcurrency, isStream, streamStarted)
}

// waitForSlotWithPing waits for a concurrency slot, sending ping events for streaming requests.
// streamStarted pointer is updated when streaming begins (for proper error handling by caller).
func (h *ConcurrencyHelper) waitForSlotWithPing(c *gin.Context, slotType string, id int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	return h.WaitForSlotWithPingTimeout(c, slotType, id, maxConcurrency, maxConcurrencyWait, isStream, streamStarted, false)
}

// WaitForSlotWithPingTimeout waits for a concurrency slot with a custom timeout.
func (h *ConcurrencyHelper) WaitForSlotWithPingTimeout(c *gin.Context, slotType string, id int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool, tryImmediate bool) (func(), error) {
	return h.concurrencyService.WaitForSlot(c.Request.Context(), slotType, id, maxConcurrency, timeout, tryImmediate,
		WaitObserver(c, h.pingFormat, h.pingInterval, isStream, streamStarted, true))
}

// AcquireAccountSlotWithWaitTimeout acquires an account slot with a custom timeout (keeps SSE ping).
func (h *ConcurrencyHelper) AcquireAccountSlotWithWaitTimeout(c *gin.Context, accountID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	return h.WaitForSlotWithPingTimeout(c, "account", accountID, maxConcurrency, timeout, isStream, streamStarted, true)
}

// WaitObserver 只负责原有 HTTP 心跳和首次输出标记；串行队列保留无 Flusher 时不输出的降级。
func WaitObserver(c *gin.Context, format SSEPingFormat, interval time.Duration, isStream bool, started *bool, strict bool) scheduler.WaitObserver {
	if !isStream || format == "" {
		return scheduler.WaitObserver{}
	}
	var flusher http.Flusher
	return scheduler.WaitObserver{Interval: interval, Begin: func() error {
		flusher, _ = c.Writer.(http.Flusher)
		if flusher == nil && strict {
			return fmt.Errorf("streaming not supported")
		}
		return nil
	}, Heartbeat: func() error {
		if flusher == nil {
			return nil
		}
		if !*started {
			c.Header("Content-Type", "text/event-stream")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("X-Accel-Buffering", "no")
			*started = true
		}
		written, err := fmt.Fprint(c.Writer, string(format))
		if err != nil {
			return err
		}
		RecordStreamHeartbeat(c, written)
		flusher.Flush()
		return nil
	}}
}

// Service 只供兼容装配复用原来的唯一并发实例。
func (h *ConcurrencyHelper) Service() *scheduler.ConcurrencyService {
	if h == nil {
		return nil
	}
	return h.concurrencyService
}
