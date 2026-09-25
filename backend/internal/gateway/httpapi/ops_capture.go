package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"

	"github.com/gin-gonic/gin"
)

const (
	OpsModelKey                  = "ops_model"
	OpsStreamKey                 = "ops_stream"
	OpsAccountIDKey              = "ops_account_id"
	opsRoutingCapacityLimitedKey = "ops_routing_capacity_limited"
	opsDedicatedErrorRecordedKey = "ops_dedicated_error_recorded"

	opsUpstreamModelKey = OpsUpstreamModelKey
	opsRequestTypeKey   = "ops_request_type"

	// 错误过滤匹配常量 — shouldSkipOpsErrorLog 和错误分类共用
	opsErrContextCanceled            = "context canceled"
	opsErrNoAvailableAccounts        = "no available accounts"
	opsErrInvalidAPIKey              = "invalid_api_key"
	opsErrAPIKeyRequired             = "api_key_required"
	opsErrInsufficientBalance        = "insufficient balance"
	opsErrInsufficientAccountBalance = "insufficient account balance"
	opsErrInsufficientQuota          = "insufficient_quota"
)

// keyPrefix 返回脱敏前缀(前 n 个字符);不足 n 则原样返回。
func keyPrefix(key string, n int) string {
	if len(key) <= n {
		return key
	}
	return key[:n]
}

func SetOpsRequestContext(c *gin.Context, model string, stream bool) {
	if c == nil {
		return
	}
	model = strings.TrimSpace(model)
	hasClientModel := false
	if c.Request != nil {
		if clientModel, ok := c.Request.Context().Value(telemetry.ClientModel).(string); ok && strings.TrimSpace(clientModel) != "" {
			model = strings.TrimSpace(clientModel)
			hasClientModel = true
		}
	}
	if clientModel, _, ok := GetCompositeModelFromContext(c); ok && !hasClientModel {
		model = clientModel
	}
	c.Set(OpsModelKey, model)
	c.Set(OpsStreamKey, stream)
	if c.Request != nil && model != "" {
		ctx := context.WithValue(c.Request.Context(), telemetry.Model, model)
		c.Request = c.Request.WithContext(ctx)
	}
}

// SetOpsEndpointContext stores upstream model and request type for ops error logging.
// Called by handlers after model mapping and request type determination.
func SetOpsEndpointContext(c *gin.Context, upstreamModel string, requestType int16) {
	if c == nil {
		return
	}
	if upstreamModel = strings.TrimSpace(upstreamModel); upstreamModel != "" {
		c.Set(opsUpstreamModelKey, upstreamModel)
	}
	c.Set(opsRequestTypeKey, requestType)
}

func SetOpsSelectedAccount(c *gin.Context, accountID int64, platform ...string) {
	if c == nil || accountID <= 0 {
		return
	}
	ClearOpsUpstreamModel(c)
	c.Set(OpsAccountIDKey, accountID)
	if c.Request != nil {
		ctx := context.WithValue(c.Request.Context(), telemetry.AccountID, accountID)
		if len(platform) > 0 {
			p := strings.TrimSpace(platform[0])
			if p != "" {
				ctx = context.WithValue(ctx, telemetry.Platform, p)
			}
		}
		c.Request = c.Request.WithContext(ctx)
	}
}

// MarkOpsRoutingCapacityLimited 标记本次失败来自本地路由容量不足，用于 SLA 口径排除。
func MarkOpsRoutingCapacityLimited(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(opsRoutingCapacityLimitedKey, true)
}

// MarkOpsRoutingCapacityLimitedIfNoAvailable 只把无可用账号类错误归为本地容量限制。
func MarkOpsRoutingCapacityLimitedIfNoAvailable(c *gin.Context, err error) {
	if !IsOpsNoAvailableAccountError(err) {
		return
	}
	MarkOpsRoutingCapacityLimited(c)
}

func isOpsRoutingCapacityLimited(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(opsRoutingCapacityLimitedKey)
	if !ok {
		return false
	}
	marked, _ := v.(bool)
	return marked
}

func IsOpsNoAvailableAccountError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, scheduler.ErrNoAvailableAccounts) || errors.Is(err, scheduler.ErrNoAvailableCompactAccounts) {
		return true
	}
	return opscore.IsNoAvailableAccountMessage(err.Error())
}

type opsCaptureWriter struct {
	// Handles are never pooled. A generation binds each handle to exactly one
	// pooled state lease, so a stale handle cannot reach a later request.
	state      *opsCaptureWriterState
	generation uint64
	pool       opsCaptureWriterStatePool
}

type opsCaptureWriterState struct {
	mu             sync.RWMutex
	inFlight       sync.WaitGroup
	generation     uint64
	responseWriter gin.ResponseWriter
	limit          int
	buf            bytes.Buffer
	probe          []byte
	lineProbe      []byte
	frameLineLen   int
	frameTruncated bool
	lineTruncated  bool
	skipLF         bool
	sseCapturing   bool
	terminalError  parsedOpsError
	terminalFound  bool
	ctx            *gin.Context
	rejected       func(*gin.Context) bool
}

const (
	opsCaptureWriterLimit         = opscore.OpsErrorLogQueueBodyMaxBytes
	opsTerminalSSEFrameProbeLimit = 16 * 1024
)

const opsCaptureWriterPoolMaxRetainedCapacity = opscore.OpsErrorLogQueueBodyMaxBytes

type opsCaptureWriterStatePool interface {
	Get() any
	Put(any)
}

var opsCaptureWriterPool opsCaptureWriterStatePool = &sync.Pool{
	New: func() any {
		return &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	},
}

func acquireOpsCaptureWriter(rw gin.ResponseWriter) *opsCaptureWriter {
	return acquireOpsCaptureWriterFromPool(opsCaptureWriterPool, rw)
}

func acquireOpsCaptureWriterFromPool(pool opsCaptureWriterStatePool, rw gin.ResponseWriter) *opsCaptureWriter {
	var pooled any
	if pool != nil {
		pooled = pool.Get()
	}
	state, ok := pooled.(*opsCaptureWriterState)
	if !ok || state == nil {
		state = &opsCaptureWriterState{}
	}
	state.mu.Lock()
	state.generation++
	state.responseWriter = rw
	state.limit = opsCaptureWriterLimit
	state.buf.Reset()
	state.probe = state.probe[:0]
	state.lineProbe = state.lineProbe[:0]
	state.frameLineLen = 0
	state.frameTruncated = false
	state.lineTruncated = false
	state.skipLF = false
	state.sseCapturing = false
	state.terminalError = parsedOpsError{}
	state.terminalFound = false
	state.ctx = nil
	state.rejected = nil
	generation := state.generation
	state.mu.Unlock()
	return &opsCaptureWriter{state: state, generation: generation, pool: pool}
}

func releaseOpsCaptureWriter(w *opsCaptureWriter) {
	if w == nil || w.state == nil {
		return
	}
	state := w.state
	state.mu.Lock()
	if state.generation != w.generation {
		state.mu.Unlock()
		return
	}
	// Invalidate the lease before waiting. No new delegated calls can start for
	// this handle, while calls that already copied the writer keep it alive via
	// inFlight until their network operation returns.
	state.generation++
	state.responseWriter = nil
	state.ctx = nil
	state.rejected = nil
	state.mu.Unlock()
	state.inFlight.Wait()
	state.mu.Lock()
	state.limit = opsCaptureWriterLimit
	state.probe = state.probe[:0]
	state.lineProbe = state.lineProbe[:0]
	state.frameLineLen = 0
	state.frameTruncated = false
	state.lineTruncated = false
	state.skipLF = false
	state.sseCapturing = false
	state.terminalError = parsedOpsError{}
	state.terminalFound = false
	poolable := shouldPoolOpsCaptureWriterState(state)
	state.buf.Reset()
	state.mu.Unlock()
	if poolable && w.pool != nil {
		w.pool.Put(state)
	}
}

func shouldPoolOpsCaptureWriterState(state *opsCaptureWriterState) bool {
	return state != nil && state.buf.Cap() <= opsCaptureWriterPoolMaxRetainedCapacity &&
		cap(state.probe) <= opsTerminalSSEFrameProbeLimit && cap(state.lineProbe) <= 256
}

func (w *opsCaptureWriter) lockActive() (*opsCaptureWriterState, gin.ResponseWriter) {
	if w == nil || w.state == nil {
		return nil, nil
	}
	state := w.state
	state.mu.RLock()
	if state.generation != w.generation || state.responseWriter == nil {
		state.mu.RUnlock()
		return nil, nil
	}
	return state, state.responseWriter
}

func (w *opsCaptureWriter) lockActiveWrite() (*opsCaptureWriterState, gin.ResponseWriter) {
	if w == nil || w.state == nil {
		return nil, nil
	}
	state := w.state
	state.mu.Lock()
	if state.generation != w.generation || state.responseWriter == nil {
		state.mu.Unlock()
		return nil, nil
	}
	return state, state.responseWriter
}

func (w *opsCaptureWriter) beginDelegatedCall() (*opsCaptureWriterState, gin.ResponseWriter) {
	if w == nil || w.state == nil {
		return nil, nil
	}
	state := w.state
	state.mu.Lock()
	if state.generation != w.generation || state.responseWriter == nil {
		state.mu.Unlock()
		return nil, nil
	}
	rw := state.responseWriter
	state.inFlight.Add(1)
	return state, rw
}

func finishDelegatedCall(state *opsCaptureWriterState) {
	if state != nil {
		state.inFlight.Done()
	}
}

func (w *opsCaptureWriter) setContext(ctx *gin.Context, rejected ...func(*gin.Context) bool) {
	state, _ := w.lockActiveWrite()
	if state == nil {
		return
	}
	state.ctx = ctx
	if len(rejected) > 0 {
		state.rejected = rejected[0]
	}
	state.mu.Unlock()
}

func (w *opsCaptureWriter) capturedBytes() []byte {
	state, _ := w.lockActive()
	if state == nil {
		return nil
	}
	defer state.mu.RUnlock()
	return append([]byte(nil), state.buf.Bytes()...)
}

func (w *opsCaptureWriter) capturedTerminalError() (parsedOpsError, bool) {
	state, _ := w.lockActive()
	if state == nil {
		return parsedOpsError{}, false
	}
	defer state.mu.RUnlock()
	return state.terminalError, state.terminalFound
}

func (w *opsCaptureWriter) finalizeCapture() {
	state, _ := w.lockActiveWrite()
	if state == nil {
		return
	}
	defer state.mu.Unlock()
	state.finalizeResponseCapture()
}

func (w *opsCaptureWriter) Header() http.Header {
	state, rw := w.lockActive()
	if state == nil {
		return http.Header{}
	}
	defer state.mu.RUnlock()
	return rw.Header()
}
func (w *opsCaptureWriter) WriteHeader(code int) {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	rw.WriteHeader(code)
}
func (w *opsCaptureWriter) WriteHeaderNow() {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	rw.WriteHeaderNow()
}
func (w *opsCaptureWriter) Status() int {
	state, rw := w.lockActive()
	if state == nil {
		return 0
	}
	defer state.mu.RUnlock()
	return rw.Status()
}
func (w *opsCaptureWriter) Size() int {
	state, rw := w.lockActive()
	if state == nil {
		return -1
	}
	defer state.mu.RUnlock()
	return rw.Size()
}
func (w *opsCaptureWriter) Written() bool {
	state, rw := w.lockActive()
	if state == nil {
		return false
	}
	defer state.mu.RUnlock()
	return rw.Written()
}
func (w *opsCaptureWriter) Flush() {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	rw.Flush()
}
func (w *opsCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return nil, nil, errors.New("response writer released")
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	return rw.Hijack()
}
func (w *opsCaptureWriter) CloseNotify() <-chan bool {
	state, rw := w.lockActive()
	if state == nil {
		ch := make(chan bool)
		close(ch)
		return ch
	}
	defer state.mu.RUnlock()
	return rw.CloseNotify()
}
func (w *opsCaptureWriter) Pusher() http.Pusher {
	state, rw := w.lockActive()
	if state == nil {
		return nil
	}
	defer state.mu.RUnlock()
	return rw.Pusher()
}

func (w *opsCaptureWriter) Write(b []byte) (int, error) {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return 0, nil
	}
	if state.shouldCapture() {
		state.captureResponseChunk(b, rw.Status())
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	return rw.Write(b)
}

func (w *opsCaptureWriter) WriteString(s string) (int, error) {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return 0, nil
	}
	if state.shouldCapture() {
		state.captureResponseChunk([]byte(s), rw.Status())
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	return rw.WriteString(s)
}

var _ gin.ResponseWriter = (*opsCaptureWriter)(nil)

func isOpsTerminalSSEFrame(frame []byte) bool {
	eventType, payload := parseOpsSSEFrameEnvelope(frame)
	if bytes.Equal(eventType, []byte("response.failed")) || bytes.Equal(eventType, []byte("error")) {
		return true
	}
	if len(payload) == 0 {
		return false
	}
	// Most successful frames cannot be terminal. Avoid JSON decoding on this
	// hot path while still validating any plausible terminal payload below.
	if !bytes.Contains(payload, []byte("response.failed")) && !bytes.Contains(payload, []byte(`"error"`)) {
		return false
	}
	var event struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(payload, &event) == nil &&
		(event.Type == "response.failed" || event.Type == "error")
}

func parseOpsSSEFrameEnvelope(frame []byte) ([]byte, []byte) {
	var eventType []byte
	var data []byte
	dataOwned := false
	dataSeen := false
	for len(frame) > 0 {
		line := frame
		lf := bytes.IndexByte(frame, '\n')
		cr := bytes.IndexByte(frame, '\r')
		idx := lf
		if idx < 0 || (cr >= 0 && cr < idx) {
			idx = cr
		}
		if idx >= 0 {
			line = frame[:idx]
			consume := idx + 1
			if frame[idx] == '\r' && consume < len(frame) && frame[consume] == '\n' {
				consume++
			}
			frame = frame[consume:]
		} else {
			frame = nil
		}
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found {
			value = nil
		}
		field = bytes.TrimSpace(field)
		value = bytes.TrimSpace(value)
		switch {
		case bytes.Equal(field, []byte("event")):
			eventType = value
		case bytes.Equal(field, []byte("data")):
			if !dataSeen {
				data = value
				dataSeen = true
				continue
			}
			if !dataOwned {
				data = append([]byte(nil), data...)
				dataOwned = true
			}
			data = append(data, '\n')
			data = append(data, value...)
		}
	}
	return bytes.TrimSpace(eventType), data
}

func (state *opsCaptureWriterState) captureResponseChunk(chunk []byte, status int) {
	if state == nil || state.limit <= 0 || len(chunk) == 0 {
		return
	}
	if status >= 400 {
		state.appendCapturedResponse(chunk)
		return
	}
	if state.sseCapturing {
		state.appendTerminalProbe(chunk)
		state.appendCapturedResponse(chunk)
		return
	}
	// Most stream writes contain one or more complete successful SSE frames.
	// Skip the byte-wise frame parser when the chunk cannot contain a terminal
	// event and leaves no split frame to carry into the next write.
	if len(state.probe) == 0 && len(state.lineProbe) == 0 && endsAtOpsSSEFrameBoundary(chunk) &&
		!mayContainOpsTerminalSSE(chunk) {
		return
	}
	for i, b := range chunk {
		if state.skipLF {
			state.skipLF = false
			if b == '\n' {
				continue
			}
		}
		if !state.lineTruncated {
			if len(state.lineProbe) < 256 {
				state.lineProbe = append(state.lineProbe, b)
			} else {
				state.lineTruncated = true
			}
		}
		if !state.frameTruncated {
			if len(state.probe) < opsTerminalSSEFrameProbeLimit {
				state.probe = append(state.probe, b)
			} else {
				state.frameTruncated = true
			}
		}
		if b != '\n' && b != '\r' {
			state.frameLineLen++
			continue
		}
		if b == '\r' {
			state.skipLF = true
		}
		if !state.lineTruncated && isOpsTerminalSSEEventLine(state.lineProbe) {
			state.sseCapturing = true
			state.terminalError = parsedOpsError{ErrorType: "upstream_error", StreamFailure: true}
			state.terminalFound = true
			state.appendCapturedResponse(state.lineProbe)
			state.probe = state.probe[:0]
			state.appendTerminalProbe(state.lineProbe)
			state.lineProbe = state.lineProbe[:0]
			state.frameLineLen = 0
			state.frameTruncated = false
			state.lineTruncated = false
			state.skipLF = false
			state.appendTerminalProbe(chunk[i+1:])
			state.appendCapturedResponse(chunk[i+1:])
			return
		}
		if state.frameLineLen != 0 {
			state.frameLineLen = 0
			state.lineProbe = state.lineProbe[:0]
			state.lineTruncated = false
			continue
		}
		if !state.frameTruncated && isOpsTerminalSSEFrame(state.probe) {
			state.sseCapturing = true
			state.terminalError, state.terminalFound = parseOpsSSEFailure(state.probe)
			state.appendCapturedResponse(state.probe)
			state.probe = state.probe[:0]
			state.appendCapturedResponse(chunk[i+1:])
			return
		}
		state.probe = state.probe[:0]
		state.lineProbe = state.lineProbe[:0]
		state.frameTruncated = false
		state.lineTruncated = false
	}
}

func endsAtOpsSSEFrameBoundary(chunk []byte) bool {
	return bytes.HasSuffix(chunk, []byte("\n\n")) ||
		bytes.HasSuffix(chunk, []byte("\r\n\r\n")) ||
		bytes.HasSuffix(chunk, []byte("\r\r"))
}

func mayContainOpsTerminalSSE(chunk []byte) bool {
	if bytes.Contains(chunk, []byte("response.failed")) {
		return true
	}
	return bytes.Contains(chunk, []byte("error")) &&
		(bytes.Contains(chunk, []byte("event")) || bytes.Contains(chunk, []byte(`"type"`)))
}

func isOpsTerminalSSEEventLine(line []byte) bool {
	line = bytes.TrimSpace(line)
	field, value, found := bytes.Cut(line, []byte{':'})
	return found && bytes.Equal(bytes.TrimSpace(field), []byte("event")) &&
		(bytes.Equal(bytes.TrimSpace(value), []byte("response.failed")) ||
			bytes.Equal(bytes.TrimSpace(value), []byte("error")))
}

func (state *opsCaptureWriterState) appendTerminalProbe(chunk []byte) {
	remaining := opsTerminalSSEFrameProbeLimit - len(state.probe)
	if remaining <= 0 {
		state.frameTruncated = true
		return
	}
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
		state.frameTruncated = true
	}
	state.probe = append(state.probe, chunk...)
}

func (state *opsCaptureWriterState) finalizeResponseCapture() {
	if state == nil {
		return
	}
	if state.terminalFound {
		if parsed, ok := parseOpsSSEFailure(state.probe); ok {
			state.terminalError = parsed
		}
		return
	}
	if state.frameTruncated || len(state.probe) == 0 || !isOpsTerminalSSEFrame(state.probe) {
		return
	}
	state.sseCapturing = true
	state.appendCapturedResponse(state.probe)
	state.terminalError, state.terminalFound = parseOpsSSEFailure(state.probe)
	if !state.terminalFound {
		state.terminalError = parsedOpsError{ErrorType: "upstream_error", StreamFailure: true}
		state.terminalFound = true
	}
}

func (state *opsCaptureWriterState) appendCapturedResponse(chunk []byte) {
	remaining := state.limit - state.buf.Len()
	if remaining <= 0 {
		return
	}
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	_, _ = state.buf.Write(chunk)
}

func (state *opsCaptureWriterState) shouldCapture() bool {
	if state.ctx == nil {
		return true
	}
	return state.rejected == nil || !state.rejected(state.ctx)
}

// OpsErrorLoggerMiddleware records error responses (status >= 400) into ops_error_logs.
//
// Notes:
// - It buffers response bodies only for status >= 400 or terminal SSE frames.
// - Streaming errors after the response has started (SSE) may still need explicit logging.
func OpsErrorLoggerMiddleware(ops *opscore.OpsService, queue OpsErrorLogQueue, access OpsObservationAccess) gin.HandlerFunc {
	return func(c *gin.Context) {
		originalWriter := c.Writer
		w := acquireOpsCaptureWriter(originalWriter)
		w.setContext(c, access.Rejected)
		defer func() {
			// Restore the original writer before returning so outer middlewares
			// don't observe a pooled wrapper that has been released.
			if c.Writer == w {
				c.Writer = originalWriter
			}
			releaseOpsCaptureWriter(w)
		}()
		c.Writer = w
		c.Next()
		w.finalizeCapture()

		if access.rejected(c) {
			return
		}

		if ops == nil {
			return
		}
		if !ops.IsMonitoringEnabled(c.Request.Context()) {
			return
		}
		if c.GetBool(opsDedicatedErrorRecordedKey) {
			return
		}

		status := c.Writer.Status()
		body := w.capturedBytes()
		parsed := parseOpsErrorResponse(body)
		if !parsed.StreamFailure {
			if terminal, ok := w.capturedTerminalError(); ok {
				parsed = terminal
			}
		}
		if status < 400 {
			if parsed.StreamFailure {
				status = inferStreamFailureStatus(c, parsed)
			} else {
				// 已固化为 200 的流内错误按请求失败记录；其余重试或切号事件保留
				// 2xx 状态，只用于展示被恢复的上游健康异常。
				if len(GetOpsStreamErrors(c)) > 0 {
					logOpsStreamError(c, ops, status, queue, access)
				} else {
					logOpsRecoveredUpstream(c, ops, status, queue, access)
				}
				return
			}
		}

		// Skip logging if a passthrough rule with skip_monitoring=true matched.
		if shouldSkipFinalOpsFailure(c) {
			return
		}

		// 按设置过滤无需记录的错误。
		if shouldSkipOpsErrorLog(c.Request.Context(), ops, parsed.Message, string(body), c.Request.URL.Path) {
			return
		}

		apiKey := access.key(c)

		clientRequestID, _ := c.Request.Context().Value(telemetry.ClientRequestID).(string)

		model, _ := c.Get(OpsModelKey)
		streamV, _ := c.Get(OpsStreamKey)
		accountIDV, _ := c.Get(OpsAccountIDKey)

		var modelName string
		if s, ok := model.(string); ok {
			modelName = s
		}
		stream := false
		if b, ok := streamV.(bool); ok {
			stream = b
		}
		var accountID *int64
		if v, ok := accountIDV.(int64); ok && v > 0 {
			accountID = &v
		}

		fallbackPlatform := guessPlatformFromPath(c.Request.URL.Path)
		platform := resolveOpsPlatform(apiKey, fallbackPlatform)

		requestID, _ := c.Request.Context().Value(telemetry.RequestID).(string)
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			requestID = c.Writer.Header().Get("X-Request-Id")
			if requestID == "" {
				requestID = c.Writer.Header().Get("x-request-id")
			}
		}

		normalizedType := opscore.NormalizeErrorType(parsed.ErrorType, parsed.Code, parsed.Message)

		phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, normalizedType, parsed.Message, parsed.Code, status)

		entry := &opscore.OpsInsertErrorLogInput{
			RequestID:       requestID,
			ClientRequestID: clientRequestID,

			AccountID: accountID,
			Platform:  platform,
			Model:     modelName,
			RequestPath: func() string {
				if c.Request != nil && c.Request.URL != nil {
					return c.Request.URL.Path
				}
				return ""
			}(),
			Stream:           stream,
			InboundEndpoint:  GetInboundEndpoint(c),
			UpstreamEndpoint: GetUpstreamEndpoint(c, platform),
			RequestedModel:   modelName,
			UpstreamModel: func() string {
				if v, ok := c.Get(opsUpstreamModelKey); ok {
					if s, ok := v.(string); ok {
						return strings.TrimSpace(s)
					}
				}
				return ""
			}(),
			RequestType: func() *int16 {
				if v, ok := c.Get(opsRequestTypeKey); ok {
					switch t := v.(type) {
					case int16:
						return &t
					case int:
						v16 := int16(t)
						return &v16
					}
				}
				return nil
			}(),
			UserAgent: c.GetHeader("User-Agent"),

			ErrorPhase:        phase,
			ErrorType:         normalizedType,
			Severity:          opscore.ClassifySeverity(normalizedType, status),
			StatusCode:        status,
			IsBusinessLimited: isBusinessLimited,
			IsCountTokens:     isCountTokensRequest(c),

			ErrorMessage: parsed.Message,
			// Sanitize each SSE data payload before the body enters the async queue.
			ErrorBody:   sanitizeOpsSSEDataForPersistence(body),
			ErrorSource: errorSource,
			ErrorOwner:  errorOwner,

			CreatedAt: time.Now(),
		}
		applyOpsLatencyFieldsFromContext(c, entry)
		applyOpsUpstreamFieldsFromContext(c, entry)
		if parsed.StreamFailure {
			if message := strings.TrimSpace(parsed.Message); message != "" {
				entry.UpstreamErrorMessage = &message
			}
			if status >= 400 {
				finalStatus := status
				entry.UpstreamStatusCode = &finalStatus
			}
		}
		suppressOpsUpstreamAttributionForLocalModelConfiguration(c, entry)

		if apiKey != nil {
			entry.APIKeyID = &apiKey.ID
			// 有效 key 报错时快照前缀，key 之后被删也保留。
			entry.APIKeyPrefix = keyPrefix(apiKey.Key, 8)
			if apiKey.User != nil {
				entry.UserID = &apiKey.User.ID
			}
			if apiKey.GroupID != nil {
				entry.GroupID = apiKey.GroupID
			}
			// 优先使用分组平台，比从路径推断更稳定。
			if apiKey.Group != nil && apiKey.Group.Platform != "" {
				entry.Platform = apiKey.Group.Platform
			}
		}

		var clientIP string
		if ip := strings.TrimSpace(clientip.GetClientIP(c)); ip != "" {
			clientIP = ip
			entry.ClientIP = &clientIP
		}

		queue.Enqueue(ops, entry)
	}
}

func logOpsRecoveredUpstream(c *gin.Context, ops *opscore.OpsService, finalStatus int, queue OpsErrorLogQueue, access OpsObservationAccess) {
	if c == nil || ops == nil || finalStatus >= 400 {
		return
	}

	entry := &opscore.OpsInsertErrorLogInput{StatusCode: finalStatus}
	applyOpsUpstreamFieldsFromContext(c, entry)
	if len(entry.UpstreamErrors) > 0 {
		visibleEvents := make([]*opscore.OpsUpstreamErrorEvent, 0, len(entry.UpstreamErrors))
		for _, event := range entry.UpstreamErrors {
			if event != nil && !event.SkipMonitoring {
				visibleEvents = append(visibleEvents, event)
			}
		}
		if len(visibleEvents) == 0 {
			return
		}
		applyOpsUpstreamErrorEvents(entry, visibleEvents)
	}
	if entry.UpstreamStatusCode == nil && entry.UpstreamErrorMessage == nil &&
		entry.UpstreamErrorDetail == nil && len(entry.UpstreamErrors) == 0 {
		return
	}

	lastStatus := 0
	if entry.UpstreamStatusCode != nil {
		lastStatus = *entry.UpstreamStatusCode
	}
	lastStage := ""
	for i := len(entry.UpstreamErrors) - 1; i >= 0; i-- {
		if event := entry.UpstreamErrors[i]; event != nil {
			lastStage = event.Stage
			if event.AccountID > 0 {
				accountID := event.AccountID
				entry.AccountID = &accountID
			}
			break
		}
	}
	if entry.AccountID == nil {
		if accountID, ok := c.Get(OpsAccountIDKey); ok {
			if value, ok := accountID.(int64); ok && value > 0 {
				entry.AccountID = &value
			}
		}
	}

	entry.ErrorPhase = "upstream"
	entry.ErrorType = "upstream_error"
	entry.ErrorSource = "upstream_http"
	entry.ErrorOwner = "provider"
	entry.Severity = opscore.ClassifySeverity(entry.ErrorType, lastStatus)
	entry.IsCountTokens = isCountTokensRequest(c)
	entry.CreatedAt = time.Now()
	entry.ErrorMessage = "Recovered upstream error"
	if lastStage == opscore.ErrorPhaseAccountAuth {
		entry.ErrorPhase = opscore.ErrorPhaseAccountAuth
		entry.ErrorMessage = "Recovered account authentication failure"
	} else if lastStatus > 0 {
		entry.ErrorMessage += " " + strconv.Itoa(lastStatus)
	}
	if entry.UpstreamErrorMessage != nil && strings.TrimSpace(*entry.UpstreamErrorMessage) != "" {
		entry.ErrorMessage += ": " + strings.TrimSpace(*entry.UpstreamErrorMessage)
	}
	entry.ErrorMessage = truncateString(entry.ErrorMessage, 2048)

	if c.Request != nil {
		entry.UserAgent = c.GetHeader("User-Agent")
		if c.Request.URL != nil {
			entry.RequestPath = c.Request.URL.Path
		}
		if c.Request.Context() != nil {
			entry.ClientRequestID, _ = c.Request.Context().Value(telemetry.ClientRequestID).(string)
			entry.RequestID, _ = c.Request.Context().Value(telemetry.RequestID).(string)
		}
	}
	entry.RequestID = strings.TrimSpace(entry.RequestID)
	if entry.RequestID == "" {
		entry.RequestID = c.Writer.Header().Get("X-Request-Id")
	}
	entry.Model = c.GetString(OpsModelKey)
	entry.RequestedModel = entry.Model
	entry.Stream = c.GetBool(OpsStreamKey)
	entry.InboundEndpoint = GetInboundEndpoint(c)
	entry.UpstreamModel = c.GetString(opsUpstreamModelKey)
	entry.RequestType = opsRequestTypeFromContext(c)

	apiKey := access.key(c)
	fallbackPlatform := guessPlatformFromPath(entry.RequestPath)
	entry.Platform = resolveOpsPlatform(apiKey, fallbackPlatform)
	entry.UpstreamEndpoint = GetUpstreamEndpoint(c, entry.Platform)
	if apiKey != nil {
		entry.APIKeyID = &apiKey.ID
		entry.APIKeyPrefix = keyPrefix(apiKey.Key, 8)
		if apiKey.User != nil {
			entry.UserID = &apiKey.User.ID
		}
		if apiKey.GroupID != nil {
			entry.GroupID = apiKey.GroupID
		}
		if apiKey.Group != nil && apiKey.Group.Platform != "" {
			entry.Platform = apiKey.Group.Platform
		}
	}
	if clientIP := strings.TrimSpace(clientip.GetClientIP(c)); clientIP != "" {
		entry.ClientIP = &clientIP
	}
	applyOpsLatencyFieldsFromContext(c, entry)
	queue.Enqueue(ops, entry)
}

func opsRequestTypeFromContext(c *gin.Context) *int16 {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(opsRequestTypeKey); ok {
		switch typed := value.(type) {
		case int16:
			result := typed
			return &result
		case int:
			result := int16(typed)
			return &result
		}
	}
	return nil
}

// logOpsStreamError 记录一次挂在已固化 HTTP 200 SSE 流上的就地错误。
// 由于实际状态码停留在 200，常规的 status>=400 捕获路径永远不会触发；
// handleStreamingAwareError 通过 service.MarkOpsStreamError 标记这类错误，
// 此函数据此补记一条错误日志，让并发限流/流内失败在错误看板里可见。
//
// 仅在 status<400 且不存在上游错误上下文时调用：上游透传错误已由中间件的
// upstream-context 分支落库，无需在此重复记录。
func logOpsStreamError(c *gin.Context, ops *opscore.OpsService, wireStatus int, queue OpsErrorLogQueue, access OpsObservationAccess) {
	for _, streamErr := range GetOpsStreamErrors(c) {
		logOpsStreamErrorValue(c, ops, wireStatus, streamErr, queue, access)
	}
}

func logOpsStreamErrorValue(c *gin.Context, ops *opscore.OpsService, wireStatus int, streamErr OpsStreamError, queue OpsErrorLogQueue, access OpsObservationAccess) {
	// 命中 skip_monitoring=true 透传规则的请求跳过落库，与其它分支一致。
	if streamErr.SkipMonitoring || (streamErr.Turn == 0 && shouldSkipFinalOpsFailure(c)) {
		return
	}

	// 复用与 status>=400 分支相同的设置过滤（context canceled / 无可用账号等）。
	if shouldSkipOpsErrorLog(c.Request.Context(), ops, streamErr.Message, streamErr.Message, c.Request.URL.Path) {
		return
	}

	// 分级用「本应返回的状态码」（如并发限流 429），缺省时回退到实际状态码。
	classifyStatus := streamErr.IntendedStatus
	if classifyStatus <= 0 {
		classifyStatus = wireStatus
	}
	normalizedType := opscore.NormalizeErrorType(streamErr.ErrType, streamErr.Code, streamErr.Message)
	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, normalizedType, streamErr.Message, streamErr.Code, classifyStatus)
	recordedStatus := wireStatus
	if streamErr.CountTowardsSLA && streamErr.IntendedStatus >= 400 {
		recordedStatus = streamErr.IntendedStatus
	}
	errorBody := ""
	if streamErr.Code != "" {
		if payload, err := json.Marshal(gin.H{"error": gin.H{
			"type": normalizedType, "code": streamErr.Code, "message": streamErr.Message,
		}}); err == nil {
			errorBody = string(payload)
		}
	}

	apiKey := access.key(c)
	clientRequestID, _ := c.Request.Context().Value(telemetry.ClientRequestID).(string)

	model, _ := c.Get(OpsModelKey)
	var modelName string
	if s, ok := model.(string); ok {
		modelName = s
	}
	accountIDV, _ := c.Get(OpsAccountIDKey)
	var accountID *int64
	if v, ok := accountIDV.(int64); ok && v > 0 {
		accountID = &v
	}

	fallbackPlatform := guessPlatformFromPath(c.Request.URL.Path)
	platform := resolveOpsPlatform(apiKey, fallbackPlatform)

	requestID, _ := c.Request.Context().Value(telemetry.RequestID).(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID = c.Writer.Header().Get("X-Request-Id")
		if requestID == "" {
			requestID = c.Writer.Header().Get("x-request-id")
		}
	}

	entry := &opscore.OpsInsertErrorLogInput{
		RequestID:       requestID,
		ClientRequestID: clientRequestID,

		AccountID: accountID,
		Platform:  platform,
		Model:     modelName,
		RequestPath: func() string {
			if c.Request != nil && c.Request.URL != nil {
				return c.Request.URL.Path
			}
			return ""
		}(),
		// 就地 SSE 错误只出现在流式请求上。
		Stream:           true,
		InboundEndpoint:  GetInboundEndpoint(c),
		UpstreamEndpoint: GetUpstreamEndpoint(c, platform),
		RequestedModel:   modelName,
		UpstreamModel: func() string {
			if v, ok := c.Get(opsUpstreamModelKey); ok {
				if s, ok := v.(string); ok {
					return strings.TrimSpace(s)
				}
			}
			return ""
		}(),
		RequestType: func() *int16 {
			if v, ok := c.Get(opsRequestTypeKey); ok {
				switch t := v.(type) {
				case int16:
					return &t
				case int:
					v16 := int16(t)
					return &v16
				}
			}
			return nil
		}(),
		UserAgent: c.GetHeader("User-Agent"),

		ErrorPhase:        phase,
		ErrorType:         normalizedType,
		Severity:          opscore.ClassifySeverity(normalizedType, classifyStatus),
		StatusCode:        recordedStatus,
		IsBusinessLimited: isBusinessLimited,
		IsCountTokens:     isCountTokensRequest(c),

		ErrorMessage: streamErr.Message,
		ErrorBody:    errorBody,
		ErrorSource:  errorSource,
		ErrorOwner:   errorOwner,

		CreatedAt: time.Now(),
	}
	applyOpsLatencyFieldsFromContext(c, entry)
	applyOpsUpstreamFieldsFromContext(c, entry)
	if streamErr.Turn > 0 {
		applyOpsStreamErrorSnapshot(entry, streamErr)
	}

	if apiKey != nil {
		entry.APIKeyID = &apiKey.ID
		entry.APIKeyPrefix = keyPrefix(apiKey.Key, 8)
		if apiKey.User != nil {
			entry.UserID = &apiKey.User.ID
		}
		if apiKey.GroupID != nil {
			entry.GroupID = apiKey.GroupID
		}
		if apiKey.Group != nil && apiKey.Group.Platform != "" {
			entry.Platform = apiKey.Group.Platform
		}
	}

	if clientIP := strings.TrimSpace(clientip.GetClientIP(c)); clientIP != "" {
		entry.ClientIP = &clientIP
	}

	queue.Enqueue(ops, entry)
}

func applyOpsStreamErrorSnapshot(entry *opscore.OpsInsertErrorLogInput, streamErr OpsStreamError) {
	if entry == nil {
		return
	}
	if streamErr.AccountID > 0 {
		accountID := streamErr.AccountID
		entry.AccountID = &accountID
	}
	entry.UpstreamModel = strings.TrimSpace(streamErr.UpstreamModel)
	entry.UpstreamStatusCode = nil
	if streamErr.UpstreamStatus > 0 {
		status := streamErr.UpstreamStatus
		entry.UpstreamStatusCode = &status
	}
	entry.UpstreamErrorMessage = nil
	if message := strings.TrimSpace(streamErr.UpstreamMessage); message != "" {
		entry.UpstreamErrorMessage = &message
	}
	entry.UpstreamErrorDetail = nil
	if detail := strings.TrimSpace(streamErr.UpstreamDetail); detail != "" {
		entry.UpstreamErrorDetail = &detail
	}
	entry.UpstreamErrors = streamErr.UpstreamErrors
	lastStage := ""
	for i := len(streamErr.UpstreamErrors) - 1; i >= 0; i-- {
		if streamErr.UpstreamErrors[i] != nil {
			lastStage = streamErr.UpstreamErrors[i].Stage
			break
		}
	}
	if lastStage == opscore.ErrorPhaseAccountAuth {
		entry.ErrorPhase = opscore.ErrorPhaseAccountAuth
		entry.ErrorOwner = "provider"
		entry.ErrorSource = "gateway"
		entry.IsBusinessLimited = false
	} else if streamErr.UpstreamStatus > 0 || len(streamErr.UpstreamErrors) > 0 {
		entry.ErrorPhase = "upstream"
		entry.ErrorOwner = "provider"
		entry.ErrorSource = "upstream_http"
		entry.IsBusinessLimited = false
	}
}

func shouldSkipFinalOpsFailure(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(OpsSkipPassthroughKey); ok {
		if skip, _ := v.(bool); skip {
			return true
		}
	}
	if v, ok := c.Get(OpsUpstreamErrorsKey); ok {
		if events, ok := v.([]*opscore.OpsUpstreamErrorEvent); ok {
			for i := len(events) - 1; i >= 0; i-- {
				if events[i] != nil {
					return events[i].SkipMonitoring
				}
			}
		}
	}
	return false
}

// isCountTokensRequest checks if the request is a count_tokens request
func isCountTokensRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	return strings.Contains(c.Request.URL.Path, "/count_tokens") ||
		IsOpenAIResponsesInputTokensRequestPath(c)
}

func applyOpsLatencyFieldsFromContext(c *gin.Context, entry *opscore.OpsInsertErrorLogInput) {
	if c == nil || entry == nil {
		return
	}
	entry.AuthLatencyMs = getContextLatencyMs(c, OpsAuthLatencyMsKey)
	entry.RoutingLatencyMs = getContextLatencyMs(c, OpsRoutingLatencyMsKey)
	entry.UpstreamLatencyMs = getContextLatencyMs(c, OpsUpstreamLatencyMsKey)
	entry.ResponseLatencyMs = getContextLatencyMs(c, OpsResponseLatencyMsKey)
	entry.TimeToFirstTokenMs = getContextLatencyMs(c, OpsTimeToFirstTokenMsKey)
}

// applyOpsUpstreamFieldsFromContext 捕获每次尝试的上游上下文。
// 最后的 account_auth 事件接管顶层状态并将其置零，之前的推理状态仍保留在 UpstreamErrors 中。
func applyOpsUpstreamFieldsFromContext(c *gin.Context, entry *opscore.OpsInsertErrorLogInput) {
	if c == nil || entry == nil {
		return
	}
	if v, ok := c.Get(OpsUpstreamStatusCodeKey); ok {
		switch t := v.(type) {
		case int:
			if t > 0 {
				code := t
				entry.UpstreamStatusCode = &code
			}
		case int64:
			if t > 0 {
				code := int(t)
				entry.UpstreamStatusCode = &code
			}
		}
	}
	if v, ok := c.Get(OpsUpstreamErrorMessageKey); ok {
		if value, ok := v.(string); ok {
			if message := strings.TrimSpace(value); message != "" {
				entry.UpstreamErrorMessage = &message
			}
		}
	}
	if v, ok := c.Get(OpsUpstreamErrorDetailKey); ok {
		if value, ok := v.(string); ok {
			if detail := strings.TrimSpace(value); detail != "" {
				entry.UpstreamErrorDetail = &detail
			}
		}
	}
	if v, ok := c.Get(OpsUpstreamErrorsKey); ok {
		if events, ok := v.([]*opscore.OpsUpstreamErrorEvent); ok && len(events) > 0 {
			applyOpsUpstreamErrorEvents(entry, events)
		}
	}
}

func applyOpsUpstreamErrorEvents(entry *opscore.OpsInsertErrorLogInput, events []*opscore.OpsUpstreamErrorEvent) {
	entry.UpstreamErrors = events
	var last *opscore.OpsUpstreamErrorEvent
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil {
			last = events[i]
			break
		}
	}
	if last == nil {
		return
	}

	entry.UpstreamStatusCode = nil
	entry.UpstreamErrorMessage = nil
	entry.UpstreamErrorDetail = nil
	if last.Stage == opscore.ErrorPhaseAccountAuth {
		code := 0
		entry.UpstreamStatusCode = &code
	} else if last.UpstreamStatusCode > 0 {
		code := last.UpstreamStatusCode
		entry.UpstreamStatusCode = &code
	}
	if message := strings.TrimSpace(last.Message); message != "" {
		entry.UpstreamErrorMessage = &message
	}
	if detail := strings.TrimSpace(last.Detail); detail != "" {
		entry.UpstreamErrorDetail = &detail
	}
}

func suppressOpsUpstreamAttributionForLocalModelConfiguration(c *gin.Context, entry *opscore.OpsInsertErrorLogInput) {
	if entry == nil || !HasOpsClientBusinessLimited(c) || OpsClientBusinessLimitedReason(c) != OpsClientBusinessLimitedReasonLocalModelConfiguration {
		return
	}
	entry.AccountID = nil
	entry.UpstreamEndpoint = ""
	entry.UpstreamModel = ""
	entry.UpstreamStatusCode = nil
	entry.UpstreamErrorMessage = nil
	entry.UpstreamErrorDetail = nil
	entry.UpstreamErrors = nil
}

func getContextLatencyMs(c *gin.Context, key string) *int64 {
	if c == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	v, ok := c.Get(key)
	if !ok {
		return nil
	}
	var ms int64
	switch t := v.(type) {
	case int:
		ms = int64(t)
	case int32:
		ms = int64(t)
	case int64:
		ms = t
	case float64:
		ms = int64(t)
	default:
		return nil
	}
	if ms < 0 {
		return nil
	}
	return &ms
}

type parsedOpsError struct {
	ErrorType     string
	Message       string
	Code          string
	StatusCode    int
	StreamFailure bool
}

func parseOpsErrorResponse(body []byte) parsedOpsError {
	if len(body) == 0 {
		return parsedOpsError{}
	}
	if parsed, ok := parseOpsSSEFailure(body); ok {
		return parsed
	}

	// Fast path: attempt to decode into a generic map.
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return parsedOpsError{Message: truncateString(string(body), 1024)}
	}

	// Claude/OpenAI-style gateway error: { type:"error", error:{ type, message } }
	if errObj, ok := m["error"].(map[string]any); ok {
		t, _ := errObj["type"].(string)
		msg, _ := errObj["message"].(string)
		if t == "" {
			t = "api_error"
		}
		code := opsJSONScalarString(errObj["code"])
		return parsedOpsError{ErrorType: t, Message: msg, Code: code}
	}
	if errMessage, ok := m["error"].(string); ok && strings.TrimSpace(errMessage) != "" {
		t, _ := m["type"].(string)
		if t == "" || t == "error" {
			t = "api_error"
		}
		return parsedOpsError{ErrorType: t, Message: strings.TrimSpace(errMessage), Code: opsJSONScalarString(m["code"])}
	}

	// APIKeyAuth-style: { code:"INSUFFICIENT_BALANCE", message:"..." }
	code := opsJSONScalarString(m["code"])
	msg, _ := m["message"].(string)
	if code != "" || msg != "" {
		t, _ := m["type"].(string)
		if t == "" || t == "error" {
			t = "api_error"
		}
		return parsedOpsError{ErrorType: t, Message: msg, Code: code}
	}

	return parsedOpsError{Message: truncateString(string(body), 1024)}
}

func opsJSONScalarString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.Itoa(int(value))
	case json.Number:
		return strings.TrimSpace(value.String())
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	default:
		return ""
	}
}

func opsJSONInt(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	case int:
		return value
	case int64:
		return int(value)
	default:
		return 0
	}
}

func parseOpsSSEFailure(body []byte) (parsedOpsError, bool) {
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	var errorCandidate *parsedOpsError
	for _, frame := range strings.Split(normalized, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		eventTypeBytes, payloadBytes := parseOpsSSEFrameEnvelope([]byte(frame))
		eventType := string(eventTypeBytes)
		if eventType != "response.failed" && eventType != "error" && len(payloadBytes) == 0 {
			continue
		}

		payload := string(payloadBytes)
		var event map[string]any
		if err := json.Unmarshal(payloadBytes, &event); err == nil {
			if eventType == "" {
				eventType, _ = event["type"].(string)
			}
		}
		if eventType != "response.failed" && eventType != "error" {
			continue
		}

		parsed := parsedOpsError{ErrorType: "upstream_error", StreamFailure: true}
		if eventType == "error" {
			parsed.ErrorType = "api_error"
		}
		errObj := opsSSEErrorObject(event)
		if errObj == nil && event != nil && (event["message"] != nil || event["code"] != nil) {
			errObj = event
		}
		if errObj != nil {
			parsed.ErrorType, _ = errObj["type"].(string)
			if parsed.ErrorType == "error" || parsed.ErrorType == "response.failed" {
				parsed.ErrorType = ""
			}
			parsed.Message, _ = errObj["message"].(string)
			switch code := errObj["code"].(type) {
			case string:
				parsed.Code = strings.TrimSpace(code)
			case float64:
				parsed.Code = strconv.Itoa(int(code))
			}
			parsed.StatusCode = opsJSONInt(errObj["status_code"])
			if parsed.StatusCode == 0 {
				parsed.StatusCode = opsJSONInt(errObj["status"])
			}
			if parsed.StatusCode == 0 {
				parsed.StatusCode = opsJSONInt(event["status_code"])
			}
			if parsed.StatusCode == 0 {
				parsed.StatusCode = opsJSONInt(event["status"])
			}
			if parsed.ErrorType == "" {
				parsed.ErrorType = inferResponsesFailedOpsErrorType(parsed.Code)
			}
			if parsed.ErrorType == "" {
				if eventType == "error" {
					parsed.ErrorType = "api_error"
				} else {
					parsed.ErrorType = "upstream_error"
				}
			}
		}
		if strings.TrimSpace(parsed.Message) == "" && payload != "" {
			trimmedPayload := strings.TrimSpace(payload)
			if strings.HasPrefix(trimmedPayload, "{") || strings.HasPrefix(trimmedPayload, "[") {
				parsed.Message = "upstream stream failed"
			} else {
				parsed.Message = truncateString(trimmedPayload, 1024)
			}
		}
		if eventType == "response.failed" {
			return parsed, true
		}
		candidate := parsed
		errorCandidate = &candidate
	}
	if errorCandidate != nil {
		return *errorCandidate, true
	}
	return parsedOpsError{}, false
}

func opsSSEErrorObject(event map[string]any) map[string]any {
	if event == nil {
		return nil
	}
	if errObj, ok := event["error"].(map[string]any); ok {
		return errObj
	}
	if response, ok := event["response"].(map[string]any); ok {
		if errObj, ok := response["error"].(map[string]any); ok {
			return errObj
		}
	}
	// Some providers flatten error fields onto the terminal event itself:
	// {"type":"error","code":"service_unavailable","message":"..."}.
	if eventType, _ := event["type"].(string); eventType == "error" {
		return event
	}
	return nil
}

func sanitizeOpsSSEDataForPersistence(body []byte) string {
	if len(body) == 0 || !bytes.Contains(body, []byte("data")) {
		return string(body)
	}
	normalized := bytes.ReplaceAll(body, []byte("\r\n"), []byte{'\n'})
	normalized = bytes.ReplaceAll(normalized, []byte{'\r'}, []byte{'\n'})
	frames := bytes.Split(normalized, []byte("\n\n"))
	var out bytes.Buffer
	out.Grow(len(body))
	for frameIndex, frame := range frames {
		if frameIndex > 0 {
			_, _ = out.WriteString("\n\n")
		}
		_, payload := parseOpsSSEFrameEnvelope(frame)
		trimmedPayload := bytes.TrimSpace(payload)
		replacement := ""
		if json.Valid(trimmedPayload) {
			replacement, _ = opscore.SanitizeOpsErrorBodyForQueue(string(trimmedPayload))
		} else if len(trimmedPayload) > 0 && (trimmedPayload[0] == '{' || trimmedPayload[0] == '[') {
			// Captured terminal frames can be truncated at the queue bound. Never
			// persist a JSON-looking fragment that could contain an unredacted key.
			replacement = `{"payload_truncated":true}`
		}
		if replacement == "" {
			_, _ = out.Write(frame)
			continue
		}
		wroteData := false
		emittedLine := false
		for _, line := range bytes.Split(frame, []byte{'\n'}) {
			field, _, found := bytes.Cut(line, []byte{':'})
			if found && bytes.Equal(bytes.TrimSpace(field), []byte("data")) {
				if wroteData {
					continue
				}
				line = append([]byte("data: "), replacement...)
				wroteData = true
			}
			if emittedLine {
				_ = out.WriteByte('\n')
			}
			_, _ = out.Write(line)
			emittedLine = true
		}
	}
	return out.String()
}

func inferResponsesFailedOpsErrorType(code string) string {
	switch strings.TrimSpace(code) {
	case "rate_limit_exceeded":
		return "rate_limit_error"
	case "permission_denied", "permission_error", "insufficient_permissions", "cyber_policy", "content_policy":
		return "permission_error"
	case "invalid_request", "context_length_exceeded":
		return "invalid_request_error"
	case "server_is_overloaded":
		return "overloaded_error"
	case "service_unavailable", "service_unavailable_error", "server_error":
		return "service_unavailable_error"
	case "authentication_failed":
		return "authentication_error"
	default:
		return ""
	}
}

func inferStreamFailureStatus(_ *gin.Context, parsed parsedOpsError) int {
	if parsed.StatusCode >= 400 && parsed.StatusCode <= 599 {
		return parsed.StatusCode
	}
	switch strings.TrimSpace(parsed.Code) {
	case "rate_limit_exceeded":
		return http.StatusTooManyRequests
	case "permission_denied", "permission_error", "insufficient_permissions", "cyber_policy", "content_policy":
		return http.StatusForbidden
	case "invalid_request", "context_length_exceeded":
		return http.StatusBadRequest
	case "server_is_overloaded":
		return http.StatusServiceUnavailable
	case "service_unavailable", "service_unavailable_error", "server_error":
		return http.StatusServiceUnavailable
	case "authentication_failed":
		return http.StatusUnauthorized
	}

	switch strings.TrimSpace(parsed.ErrorType) {
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "permission_error", "forbidden_error":
		return http.StatusForbidden
	case "authentication_error":
		return http.StatusUnauthorized
	case "invalid_request_error":
		return http.StatusBadRequest
	case "overloaded_error", "service_unavailable_error":
		return http.StatusServiceUnavailable
	}

	return http.StatusBadGateway
}

// getOpsAPIKey 返回用于 Ops 错误日志的 API Key：优先取已鉴权写入的正式 key；
// 鉴权早退（分组停用/删除、Key 停用/过期/额度、用户停用、IP 限制等）时，
// 正式 key 尚未写入，回退到 middleware 写入的 ops fallback key
// （含 User/Group/Platform），从而让日志能展示 用户/分组/平台。

func resolveOpsPlatform(apiKey *apikey.APIKey, fallback string) string {
	if apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform != "" {
		return apiKey.Group.Platform
	}
	return fallback
}

func guessPlatformFromPath(path string) string {
	p := strings.ToLower(path)
	switch {
	case strings.HasPrefix(p, "/antigravity/"):
		return capability.PlatformAntigravity
	case strings.HasPrefix(p, "/v1beta/"):
		return capability.PlatformGemini
	case strings.Contains(p, "/responses"), strings.Contains(p, "/images/"):
		return capability.PlatformOpenAI
	default:
		return ""
	}
}

// classifyOpsErrorLog 汇总上游错误上下文与本地路由标记，生成统一的 Ops 统计口径。
func classifyOpsErrorLog(c *gin.Context, errType, message, code string, status int) (phase string, isBusinessLimited bool, errorOwner string, errorSource string) {
	return opscore.ClassifyRequestError(opscore.ErrorClassificationInput{
		Type: errType, Message: message, Code: code, Status: status,
		RoutingCapacityLimited:       isOpsRoutingCapacityLimited(c),
		ClientBusinessLimited:        HasOpsClientBusinessLimited(c),
		LocalModelConfiguration:      OpsClientBusinessLimitedReason(c) == OpsClientBusinessLimitedReasonLocalModelConfiguration,
		UpstreamError:                hasOpsUpstreamErrorContext(c),
		UpstreamClientInvalidRequest: isOpsUpstreamClientInvalidRequest(c, errType, code, status),
		AccountAuthFailure:           hasOpsAccountAuthFailure(c),
	})
}

// isOpsUpstreamClientInvalidRequest 判断下游和上游均为 400 的参数型错误。
// 这类记录仍写入错误表用于排障，但不应降低服务 SLA。
func isOpsUpstreamClientInvalidRequest(c *gin.Context, errType, code string, status int) bool {
	if c == nil || status != http.StatusBadRequest || errType != "invalid_request_error" ||
		!strings.EqualFold(strings.TrimSpace(code), openai.OpenAIPropertyNameAboveMaxLengthCode) {
		return false
	}
	v, ok := c.Get(OpsUpstreamStatusCodeKey)
	if !ok {
		return false
	}
	switch upstreamStatus := v.(type) {
	case int:
		return upstreamStatus == http.StatusBadRequest
	case int64:
		return upstreamStatus == http.StatusBadRequest
	default:
		return false
	}
}

// hasOpsUpstreamErrorContext 判断当前错误是否已有上游响应上下文，避免误判为本地认证或路由错误。
func hasOpsUpstreamErrorContext(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(OpsUpstreamStatusCodeKey); ok {
		switch code := v.(type) {
		case int:
			if code > 0 {
				return true
			}
		case int64:
			if code > 0 {
				return true
			}
		}
	}
	if v, ok := c.Get(OpsUpstreamErrorsKey); ok {
		if events, ok := v.([]*opscore.OpsUpstreamErrorEvent); ok && len(events) > 0 {
			return true
		}
	}
	return false
}

func hasOpsAccountAuthFailure(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(OpsUpstreamErrorsKey); ok {
		if events, ok := v.([]*opscore.OpsUpstreamErrorEvent); ok {
			for i := len(events) - 1; i >= 0; i-- {
				if events[i] != nil {
					return events[i].Stage == opscore.ErrorPhaseAccountAuth
				}
			}
		}
	}
	return false
}

func truncateString(s string, max int) string { return logredact.TruncateUTF8(s, max) }

// shouldSkipOpsErrorLog determines if an error should be skipped from logging based on settings.
// Returns true for errors that should be filtered according to OpsAdvancedSettings.
func shouldSkipOpsErrorLog(ctx context.Context, ops *opscore.OpsService, message, body, requestPath string) bool {
	if ops == nil {
		return false
	}

	// Get advanced settings to check filter configuration
	_ = ctx
	settings := ops.OpsAdvancedSettingsSnapshot()

	msgLower := strings.ToLower(message)
	bodyLower := strings.ToLower(body)

	// Check if count_tokens errors should be ignored
	if settings.IgnoreCountTokensErrors && strings.Contains(requestPath, "/count_tokens") {
		return true
	}

	// Check if context canceled errors should be ignored (client disconnects)
	if settings.IgnoreContextCanceled {
		if strings.Contains(msgLower, opsErrContextCanceled) || strings.Contains(bodyLower, opsErrContextCanceled) {
			return true
		}
	}

	// Check if "no available accounts" errors should be ignored
	if settings.IgnoreNoAvailableAccounts {
		if strings.Contains(msgLower, opsErrNoAvailableAccounts) || strings.Contains(bodyLower, opsErrNoAvailableAccounts) {
			return true
		}
	}

	// Check if invalid/missing API key errors should be ignored (user misconfiguration)
	if settings.IgnoreInvalidApiKeyErrors {
		if strings.Contains(bodyLower, opsErrInvalidAPIKey) || strings.Contains(bodyLower, opsErrAPIKeyRequired) {
			return true
		}
	}

	// Check if insufficient balance errors should be ignored
	if settings.IgnoreInsufficientBalanceErrors {
		if strings.Contains(bodyLower, opsErrInsufficientBalance) || strings.Contains(bodyLower, opsErrInsufficientAccountBalance) ||
			strings.Contains(bodyLower, opsErrInsufficientQuota) ||
			strings.Contains(msgLower, opsErrInsufficientBalance) || strings.Contains(msgLower, opsErrInsufficientAccountBalance) {
			return true
		}
	}

	return false
}
