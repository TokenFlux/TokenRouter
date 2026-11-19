package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ops_capture.go captures bounded HTTP and SSE failure evidence through a
// generation-stamped ResponseWriter pool and submits attributed Ops error logs.
// An upstream stream can fail after HTTP 200 commitment, and terminal SSE
// payloads can span multiple writes or exceed body capture limits. Separate
// line and frame probes preserve terminal failure recognition even when the
// captured body buffer truncates.
//
// Each opsCaptureWriter handle binds to one generation of pooled state. Release
// advances that generation and clears request references under state.mu before
// waiting on inFlight outside the mutex, preventing stale handles from reading
// reused buffers while allowing active network writes to finish without
// deadlock.
//
// Final middleware parses captured bytes once, selects terminal, in-band
// stream, or recovered-upstream logging, and builds entries through a shared
// request/identity constructor before handing ownership to OpsErrorLogQueue.

// Capture byte budgets bound pooled response buffers and split-write SSE
// terminal probes.
const (
	opsCaptureWriterLimit                   = opscore.OpsErrorLogQueueBodyMaxBytes // maximum captured response body bytes
	opsTerminalSSEFrameProbeLimit           = 16 * 1024                            // maximum buffered terminal SSE frame probe bytes
	opsCaptureWriterPoolMaxRetainedCapacity = opscore.OpsErrorLogQueueBodyMaxBytes // maximum buffer capacity returned to sync.Pool
)

// opsCaptureWriterPool reuses bounded capture state across requests, and the
// interface check verifies Gin ResponseWriter compatibility at compile time.
var (
	opsCaptureWriterPool opsCaptureWriterStatePool = &sync.Pool{
		New: func() any {
			return &opsCaptureWriterState{limit: opsCaptureWriterLimit}
		},
	}

	_ gin.ResponseWriter = (*opsCaptureWriter)(nil)
)

// OpsErrorLogQueue accepts completed request error observations for
// asynchronous persistence.
type OpsErrorLogQueue interface {
	// Enqueue takes ownership of the mutable input entry for asynchronous
	// sanitization and storage; callers must not mutate it after submission.
	Enqueue(*opscore.OpsService, *opscore.OpsInsertErrorLogInput)
}

// OpsObservationAccess supplies optional API Key lookup and ingress-rejection
// checks to OpsErrorLoggerMiddleware.
type OpsObservationAccess struct {
	// APIKey returns the authenticated API Key for attribution when available.
	APIKey func(*gin.Context) *apikey.APIKey
	// Rejected reports whether ingress middleware already rejected and accounted
	// for the request; called under capture state locks so it must not reenter c.Writer.
	Rejected func(*gin.Context) bool
}

// opsCaptureWriter binds one HTTP middleware invocation to a generation-stamped
// opsCaptureWriterState lease.
type opsCaptureWriter struct {
	// state, generation, and pool identify one active lease so release can
	// invalidate older handles before returning bounded storage to pool.
	state      *opsCaptureWriterState    // borrowed leased capture storage
	generation uint64                    // handle lease generation stamp
	pool       opsCaptureWriterStatePool // destination pool on release
}

// opsCaptureWriterState retains bounded response/SSE probes and in-flight
// writer call tracking for one pooled lease.
type opsCaptureWriterState struct {
	// mu guards generation, responseWriter, and parser buffers while inFlight
	// tracks delegated network calls that run after unlocking mu.
	mu             sync.RWMutex       // capture lease and parser mutex
	inFlight       sync.WaitGroup     // active delegated writer operations
	generation     uint64             // monotonic lease generation counter
	responseWriter gin.ResponseWriter // underlying Gin response writer
	limit          int                // captured response byte cap
	buf            bytes.Buffer       // bounded attempted response bytes

	// probe, lineProbe, frameLineLen, frameTruncated, lineTruncated, and skipLF
	// track split SSE lines and frames across chunked writes within fixed caps.
	probe          []byte // bounded terminal frame probe bytes
	lineProbe      []byte // bounded current SSE line bytes
	frameLineLen   int    // current SSE line byte count
	frameTruncated bool   // whether probe exceeded its 16 KiB cap
	lineTruncated  bool   // whether lineProbe exceeded its 256-byte cap
	skipLF         bool   // whether the next LF completes a split CRLF

	// sseCapturing, terminalError, and terminalFound latch recognized SSE failure
	// evidence independently of body truncation.
	sseCapturing  bool                   // whether terminal SSE body capture is active
	terminalError opscore.ParsedOpsError // parsed terminal SSE error metadata
	terminalFound bool                   // whether terminal SSE failure was observed

	ctx      *gin.Context            // active request context cleared on release
	rejected func(*gin.Context) bool // read-only ingress rejection predicate
}

// opsCaptureWriterStatePool abstracts sync.Pool for deterministic lease-reuse
// testing.
type opsCaptureWriterStatePool interface {
	// Get returns an unowned state candidate or nil.
	Get() any
	// Put returns a reset, bounded state after all in-flight writer calls finish.
	Put(any)
}

// key returns the authenticated API Key from a.APIKey when configured.
func (a OpsObservationAccess) key(c *gin.Context) *apikey.APIKey {
	if a.APIKey == nil {
		return nil
	}
	return a.APIKey(c)
}

// rejected reports whether a.Rejected suppresses capture and logging for c.
func (a OpsObservationAccess) rejected(c *gin.Context) bool {
	return a.Rejected != nil && a.Rejected(c)
}

// keyPrefix returns at most n leading credential bytes for persisted prefix
// identification after a Key may have been deleted.
func keyPrefix(key string, n int) string {
	if len(key) <= n {
		return key
	}
	return key[:n]
}

// SetOpsRequestContext records client model attribution and stream intent on c,
// preferring telemetry ClientModel over composite display names and raw inputs.
//
// Preserving the client-facing model name before provider mapping keeps error
// attribution aligned with what the caller requested while also publishing
// non-blank models to request telemetry and stage timing.
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
	if c.Request != nil {
		if stages := timing.StagesFromContext(c.Request.Context()); stages != nil {
			stages.SetStream(stream)
		}
	}
	if c.Request != nil && model != "" {
		ctx := context.WithValue(c.Request.Context(), telemetry.Model, model)
		c.Request = c.Request.WithContext(ctx)
	}
}

// SetOpsEndpointContext records a non-blank upstream model and the persisted
// int16 request type on c.
func SetOpsEndpointContext(c *gin.Context, upstreamModel string, requestType int16) {
	if c == nil {
		return
	}
	if upstreamModel = strings.TrimSpace(upstreamModel); upstreamModel != "" {
		c.Set(OpsUpstreamModelKey, upstreamModel)
	}
	c.Set(opsRequestTypeKey, requestType)
}

// SetOpsSelectedProvider clears prior upstream model state, records the
// selected provider ID on c, and binds provider identity and request-ID header
// configuration into the request telemetry context.
func SetOpsSelectedProvider(c *gin.Context, value provider.ProviderSnapshot) {
	if c == nil || value.ID <= 0 {
		return
	}
	ClearOpsUpstreamModel(c)
	c.Set(OpsProviderIDKey, value.ID)
	if c.Request != nil {
		ctx := context.WithValue(c.Request.Context(), telemetry.ProviderID, value.ID)
		if platform := strings.TrimSpace(value.Platform); platform != "" {
			ctx = context.WithValue(ctx, telemetry.Platform, platform)
		}
		ctx = telemetry.WithRequestProvider(ctx, value.ID, value.RequestIDHeader)
		c.Request = c.Request.WithContext(ctx)
	}
}

// acquireOpsCaptureWriter leases a generation-stamped capture writer from the
// shared state pool.
func acquireOpsCaptureWriter(rw gin.ResponseWriter) *opsCaptureWriter {
	return acquireOpsCaptureWriterFromPool(opsCaptureWriterPool, rw)
}

// acquireOpsCaptureWriterFromPool resets a pooled or newly allocated state
// under state.mu and advances its generation before returning a fresh handle.
//
// Advancing generation at both acquisition and release ensures any retained
// handle from an earlier request is rejected on lockActive or beginDelegatedCall.
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
	state.terminalError = opscore.ParsedOpsError{}
	state.terminalFound = false
	state.ctx = nil
	state.rejected = nil
	generation := state.generation
	state.mu.Unlock()
	return &opsCaptureWriter{state: state, generation: generation, pool: pool}
}

// releaseOpsCaptureWriter invalidates w's generation, waits outside state.mu
// for in-flight delegated writer calls to finish, resets buffers, and returns
// bounded state to w.pool.
//
// Waiting with state.mu unlocked avoids deadlocking against active network
// writes while the generation increment prevents new calls from starting on w.
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
	// Advance generation and clear live references before unlocking so no new
	// writer calls can register while inFlight drains.
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
	state.terminalError = opscore.ParsedOpsError{}
	state.terminalFound = false
	poolable := shouldPoolOpsCaptureWriterState(state)
	state.buf.Reset()
	state.mu.Unlock()
	if poolable && w.pool != nil {
		w.pool.Put(state)
	}
}

// shouldPoolOpsCaptureWriterState reports whether state's buffer capacities fit
// within pool retention limits.
func shouldPoolOpsCaptureWriterState(state *opsCaptureWriterState) bool {
	return state != nil && state.buf.Cap() <= opsCaptureWriterPoolMaxRetainedCapacity &&
		cap(state.probe) <= opsTerminalSSEFrameProbeLimit && cap(state.lineProbe) <= 256
}

// lockActive acquires state.mu.RLock when w's generation remains active, or
// returns nil with no lock held when w is nil or released.
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

// lockActiveWrite acquires state.mu.Lock when w's generation remains active, or
// returns nil with no lock held when w is nil or released.
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

// beginDelegatedCall validates w's generation and increments state.inFlight
// under state.mu, returning with state.mu held so callers can capture before
// unlocking for network I/O.
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

// finishDelegatedCall decrements state.inFlight once a delegated writer
// operation completes.
func finishDelegatedCall(state *opsCaptureWriterState) {
	if state != nil {
		state.inFlight.Done()
	}
}

// setContext attaches c and an optional read-only ingress rejection predicate
// to an active capture lease.
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

// capturedBytes returns an independent copy of the bounded response bytes
// captured under an active lease.
func (w *opsCaptureWriter) capturedBytes() []byte {
	state, _ := w.lockActive()
	if state == nil {
		return nil
	}
	defer state.mu.RUnlock()
	return append([]byte(nil), state.buf.Bytes()...)
}

// capturedTerminalError returns any terminal SSE error metadata captured on an
// active lease.
func (w *opsCaptureWriter) capturedTerminalError() (opscore.ParsedOpsError, bool) {
	state, _ := w.lockActive()
	if state == nil {
		return opscore.ParsedOpsError{}, false
	}
	defer state.mu.RUnlock()
	return state.terminalError, state.terminalFound
}

// finalizeCapture flushes any trailing terminal SSE probe on an active lease
// before middleware error attribution runs.
func (w *opsCaptureWriter) finalizeCapture() {
	state, _ := w.lockActiveWrite()
	if state == nil {
		return
	}
	defer state.mu.Unlock()
	state.finalizeResponseCapture()
}

// Header returns the underlying writer's HTTP header map when w remains active,
// or an empty map after release.
func (w *opsCaptureWriter) Header() http.Header {
	state, rw := w.lockActive()
	if state == nil {
		return http.Header{}
	}
	defer state.mu.RUnlock()
	return rw.Header()
}

// WriteHeader delegates HTTP status selection outside state.mu while keeping
// the call registered in state.inFlight.
func (w *opsCaptureWriter) WriteHeader(code int) {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	rw.WriteHeader(code)
}

// WriteHeaderNow flushes HTTP headers immediately through a registered writer
// call outside state.mu.
func (w *opsCaptureWriter) WriteHeaderNow() {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	rw.WriteHeaderNow()
}

// Status returns the active writer's HTTP status, or zero after release.
func (w *opsCaptureWriter) Status() int {
	state, rw := w.lockActive()
	if state == nil {
		return 0
	}
	defer state.mu.RUnlock()
	return rw.Status()
}

// Size returns the active writer's written byte count, or -1 after release.
func (w *opsCaptureWriter) Size() int {
	state, rw := w.lockActive()
	if state == nil {
		return -1
	}
	defer state.mu.RUnlock()
	return rw.Size()
}

// Written reports whether the active writer has committed output, or false
// after release.
func (w *opsCaptureWriter) Written() bool {
	state, rw := w.lockActive()
	if state == nil {
		return false
	}
	defer state.mu.RUnlock()
	return rw.Written()
}

// Flush flushes the underlying writer outside state.mu under inFlight tracking.
func (w *opsCaptureWriter) Flush() {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	rw.Flush()
}

// Hijack takes over the underlying network connection outside state.mu when w
// remains active, returning an error if the writer lease was already released.
func (w *opsCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	state, rw := w.beginDelegatedCall()
	if state == nil {
		return nil, nil, errors.New("response writer released")
	}
	state.mu.Unlock()
	defer finishDelegatedCall(state)
	return rw.Hijack()
}

// CloseNotify returns the active writer's close channel, or an already closed
// channel if the lease was released.
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

// Pusher returns the active writer's HTTP/2 Pusher, or nil after release.
func (w *opsCaptureWriter) Pusher() http.Pusher {
	state, rw := w.lockActive()
	if state == nil {
		return nil
	}
	defer state.mu.RUnlock()
	return rw.Pusher()
}

// Write captures bounded diagnostic bytes under state.mu before delegating the
// network write outside the lock under state.inFlight protection.
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

// WriteString captures bounded diagnostic bytes under state.mu before
// delegating WriteString outside the lock under state.inFlight protection.
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

// captureResponseChunk records HTTP>=400 bodies directly or scans HTTP<400
// chunks for terminal SSE frames within bounded line and frame probes.
//
// Complete non-terminal SSE chunks bypass byte-by-byte scanning on the hot
// path, while split chunks carry partial lines across writes so CRLF splits or
// truncated JSON payloads cannot hide a terminal failure frame.
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
	// Bypass byte-wise scanning when chunk ends on a frame boundary and contains
	// no terminal SSE marker bytes.
	if len(state.probe) == 0 && len(state.lineProbe) == 0 &&
		opscore.EndsAtOpsSSEFrameBoundary(chunk) &&
		!opscore.MayContainOpsTerminalSSE(chunk) {
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
		if !state.lineTruncated && opscore.IsOpsTerminalSSEEventLine(state.lineProbe) {
			state.sseCapturing = true
			state.terminalError = opscore.ParsedOpsError{ErrorType: "upstream_error", StreamFailure: true}
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
		if !state.frameTruncated && opscore.IsOpsTerminalSSEFrame(state.probe) {
			state.sseCapturing = true
			state.terminalError, state.terminalFound = opscore.ParseOpsSSEFailure(state.probe)
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

// appendTerminalProbe appends up to the remaining 16 KiB terminal SSE probe
// budget, setting frameTruncated on overflow.
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

// finalizeResponseCapture enriches already latched terminal SSE metadata or
// inspects a trailing unseparated SSE probe at stream end.
func (state *opsCaptureWriterState) finalizeResponseCapture() {
	if state == nil {
		return
	}
	if state.terminalFound {
		if parsed, ok := opscore.ParseOpsSSEFailure(state.probe); ok {
			state.terminalError = parsed
		}
		return
	}
	if state.frameTruncated || len(state.probe) == 0 || !opscore.IsOpsTerminalSSEFrame(state.probe) {
		return
	}
	state.sseCapturing = true
	state.appendCapturedResponse(state.probe)
	state.terminalError, state.terminalFound = opscore.ParseOpsSSEFailure(state.probe)
	if !state.terminalFound {
		state.terminalError = opscore.ParsedOpsError{ErrorType: "upstream_error", StreamFailure: true}
		state.terminalFound = true
	}
}

// appendCapturedResponse appends up to the remaining body-capture limit.
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

// shouldCapture reports whether ingress rejection allows response capture.
func (state *opsCaptureWriterState) shouldCapture() bool {
	if state.ctx == nil {
		return true
	}
	return state.rejected == nil || !state.rejected(state.ctx)
}

// OpsErrorLoggerMiddleware wraps c.Writer with a pooled capture writer, parses
// captured bytes once after handler execution, and enqueues terminal, in-band
// stream, or recovered-upstream Ops observations.
//
// Request failure telemetry is marked before ingress-rejection and monitoring
// skip gates so filtered Ops entries still leave accurate request telemetry.
func OpsErrorLoggerMiddleware(ops *opscore.OpsService, queue OpsErrorLogQueue, access OpsObservationAccess) gin.HandlerFunc {
	return func(c *gin.Context) {
		originalWriter := c.Writer
		w := acquireOpsCaptureWriter(originalWriter)
		w.setContext(c, access.Rejected)
		defer func() {
			if c.Writer == w {
				c.Writer = originalWriter
			}
			releaseOpsCaptureWriter(w)
		}()
		c.Writer = w
		c.Next()
		w.finalizeCapture()

		body := w.capturedBytes()
		parsed := opscore.ParseOpsErrorResponse(body)
		if !parsed.StreamFailure {
			if terminal, ok := w.capturedTerminalError(); ok {
				parsed = terminal
			}
		}
		// Record stream failure on request telemetry before monitoring gates run so
		// suppressed Ops entries do not mask failed request outcomes.
		if parsed.StreamFailure {
			telemetry.UpdateRequest(c.Request.Context(), func(record *telemetry.RequestRecord) {
				record.State = "failed"
				record.ErrorCode = "stream_error"
			})
		}
		if access.rejected(c) || ops == nil || !ops.IsMonitoringEnabled(c.Request.Context()) || IsOpsDedicatedErrorRecorded(c) {
			return
		}

		status := c.Writer.Status()
		if status < 400 {
			if parsed.StreamFailure {
				status = opscore.InferStreamFailureStatus(parsed)
			} else {
				if len(GetOpsStreamErrors(c)) > 0 {
					logOpsStreamError(c, ops, status, queue, access)
				} else {
					logOpsRecoveredUpstream(c, ops, status, queue, access)
				}
				return
			}
		}

		if shouldSkipFinalOpsFailure(c) {
			return
		}
		requestPath := ""
		if c.Request != nil && c.Request.URL != nil {
			requestPath = c.Request.URL.Path
		}
		if shouldSkipOpsErrorLog(c.Request.Context(), ops, parsed.Message, string(body), requestPath) {
			return
		}

		normalizedType := opscore.NormalizeErrorType(parsed.ErrorType, parsed.Code, parsed.Message)
		phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, normalizedType, parsed.Message, parsed.Code, status)

		entry := buildBaseOpsErrorLogInput(c, access, false)
		entry.ErrorPhase = phase
		entry.ErrorType = normalizedType
		entry.Severity = opscore.ClassifySeverity(normalizedType, status)
		entry.StatusCode = status
		entry.IsBusinessLimited = isBusinessLimited
		entry.ErrorMessage = parsed.Message
		entry.ErrorBody = opscore.SanitizeOpsSSEDataForPersistence(body)
		entry.ErrorSource = errorSource
		entry.ErrorOwner = errorOwner

		// Override stale attempt fields when terminal SSE failure supplies the
		// authoritative client-visible message and inferred HTTP status.
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
		queue.Enqueue(ops, entry)
	}
}

// buildBaseOpsErrorLogInput populates common request, endpoint, model, API Key,
// client IP, latency, and upstream attribution fields shared by final,
// recovered-upstream, and in-band stream Ops log builders. When forceStream is
// true, Stream is forced true regardless of the request context flag.
func buildBaseOpsErrorLogInput(c *gin.Context, access OpsObservationAccess, forceStream bool) *opscore.OpsInsertErrorLogInput {
	apiKey := access.key(c)
	platform := resolveOpsPlatform(apiKey, selectedOpsPlatform(c))
	modelName := GetOpsModel(c)
	stream := true
	if !forceStream {
		stream = GetOpsStream(c)
	}

	var providerID *int64
	if id, ok := GetOpsProviderID(c); ok {
		providerID = &id
	}

	var clientRequestID, requestID, requestPath, userAgent string
	if c != nil && c.Request != nil {
		userAgent = c.GetHeader("User-Agent")
		if c.Request.URL != nil {
			requestPath = c.Request.URL.Path
		}
		if c.Request.Context() != nil {
			clientRequestID, _ = c.Request.Context().Value(telemetry.ClientRequestID).(string)
			requestID, _ = c.Request.Context().Value(telemetry.RequestID).(string)
		}
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" && c != nil && c.Writer != nil {
		if h := c.Writer.Header(); h != nil {
			requestID = h.Get("X-Request-Id")
		}
	}

	entry := &opscore.OpsInsertErrorLogInput{
		RequestID:        requestID,
		ClientRequestID:  clientRequestID,
		ProviderID:       providerID,
		Platform:         platform,
		Model:            modelName,
		RequestedModel:   modelName,
		RequestPath:      requestPath,
		Stream:           stream,
		InboundEndpoint:  GetInboundEndpoint(c),
		UpstreamEndpoint: GetUpstreamEndpoint(c, platform),
		UpstreamModel:    GetOpsUpstreamModel(c),
		RequestType:      opsRequestTypeFromContext(c),
		UserAgent:        userAgent,
		IsCountTokens:    isCountTokensRequest(c),
		CreatedAt:        time.Now(),
	}
	applyOpsLatencyFieldsFromContext(c, entry)
	applyOpsUpstreamFieldsFromContext(c, entry)

	if apiKey != nil {
		entry.APIKeyID = &apiKey.ID
		entry.APIKeyPrefix = keyPrefix(apiKey.Key, 8)
		if apiKey.User != nil {
			entry.UserID = &apiKey.User.ID
		}
		if apiKey.GroupID != nil {
			entry.GroupID = apiKey.GroupID
		}
	}
	if c != nil {
		if ip := strings.TrimSpace(clientip.GetClientIP(c)); ip != "" {
			entry.ClientIP = &ip
		}
	}
	return entry
}

// logOpsRecoveredUpstream queues visible upstream attempt diagnostics when the
// final HTTP status succeeded (<400) after failover or retry recovery.
func logOpsRecoveredUpstream(c *gin.Context, ops *opscore.OpsService, finalStatus int, queue OpsErrorLogQueue, access OpsObservationAccess) {
	if c == nil || ops == nil || finalStatus >= 400 {
		return
	}

	probe := &opscore.OpsInsertErrorLogInput{}
	applyOpsUpstreamFieldsFromContext(c, probe)
	if len(probe.UpstreamErrors) > 0 {
		visibleEvents := make([]*opscore.OpsUpstreamErrorEvent, 0, len(probe.UpstreamErrors))
		for _, event := range probe.UpstreamErrors {
			if event != nil && !event.SkipMonitoring {
				visibleEvents = append(visibleEvents, event)
			}
		}
		if len(visibleEvents) == 0 {
			return
		}
		applyOpsUpstreamErrorEvents(probe, visibleEvents)
	}
	if probe.UpstreamStatusCode == nil && probe.UpstreamErrorMessage == nil &&
		probe.UpstreamErrorDetail == nil && len(probe.UpstreamErrors) == 0 {
		return
	}

	entry := buildBaseOpsErrorLogInput(c, access, false)
	entry.StatusCode = finalStatus
	entry.UpstreamStatusCode = probe.UpstreamStatusCode
	entry.UpstreamErrorMessage = probe.UpstreamErrorMessage
	entry.UpstreamErrorDetail = probe.UpstreamErrorDetail
	entry.UpstreamErrors = probe.UpstreamErrors

	lastStatus := 0
	if entry.UpstreamStatusCode != nil {
		lastStatus = *entry.UpstreamStatusCode
	}
	lastStage := ""
	lastKind := ""
	for _, event := range slices.Backward(entry.UpstreamErrors) {
		if event != nil {
			lastStage = event.Stage
			lastKind = event.Kind
			if event.ProviderID > 0 {
				providerID := event.ProviderID
				entry.ProviderID = &providerID
			}
			break
		}
	}

	entry.ErrorPhase = "upstream"
	entry.ErrorType = "upstream_error"
	entry.ErrorSource = "upstream_http"
	entry.ErrorOwner = "provider"
	entry.Severity = opscore.ClassifySeverity(entry.ErrorType, lastStatus)
	entry.ErrorMessage = "Recovered upstream error"
	if lastKind == "usage_invalid" {
		entry.ErrorMessage = "SystemOne usage is missing or invalid; billing skipped"
	}
	if lastKind != "usage_invalid" && lastStage == opscore.ErrorPhaseProviderAuth {
		entry.ErrorPhase = opscore.ErrorPhaseProviderAuth
		entry.ErrorMessage = "Recovered provider authentication failure"
	} else if lastKind != "usage_invalid" && lastStatus > 0 {
		entry.ErrorMessage += " " + strconv.Itoa(lastStatus)
	}
	if lastKind != "usage_invalid" && entry.UpstreamErrorMessage != nil && strings.TrimSpace(*entry.UpstreamErrorMessage) != "" {
		entry.ErrorMessage += ": " + strings.TrimSpace(*entry.UpstreamErrorMessage)
	}
	entry.ErrorMessage = logredact.TruncateUTF8(entry.ErrorMessage, 2048)
	queue.Enqueue(ops, entry)
}

// opsRequestTypeFromContext returns a copied *int16 request type from int16 or
// int context values, or nil when absent.
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

// logOpsStreamError submits each retained HTTP or WebSocket turn stream error
// to logOpsStreamErrorValue.
func logOpsStreamError(c *gin.Context, ops *opscore.OpsService, wireStatus int, queue OpsErrorLogQueue, access OpsObservationAccess) {
	for _, streamErr := range GetOpsStreamErrors(c) {
		logOpsStreamErrorValue(c, ops, wireStatus, streamErr, queue, access)
	}
}

// logOpsStreamErrorValue evaluates monitoring skip rules, classifies severity
// and SLA status, applies any WebSocket turn snapshot, and enqueues streamErr.
func logOpsStreamErrorValue(c *gin.Context, ops *opscore.OpsService, wireStatus int, streamErr OpsStreamError, queue OpsErrorLogQueue, access OpsObservationAccess) {
	if streamErr.SkipMonitoring || (streamErr.Turn == 0 && shouldSkipFinalOpsFailure(c)) {
		return
	}
	requestPath := ""
	if c.Request != nil && c.Request.URL != nil {
		requestPath = c.Request.URL.Path
	}
	if shouldSkipOpsErrorLog(c.Request.Context(), ops, streamErr.Message, streamErr.Message, requestPath) {
		return
	}

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

	entry := buildBaseOpsErrorLogInput(c, access, true)
	entry.ErrorPhase = phase
	entry.ErrorType = normalizedType
	entry.Severity = opscore.ClassifySeverity(normalizedType, classifyStatus)
	entry.StatusCode = recordedStatus
	entry.IsBusinessLimited = isBusinessLimited
	entry.ErrorMessage = streamErr.Message
	entry.ErrorBody = errorBody
	entry.ErrorSource = errorSource
	entry.ErrorOwner = errorOwner
	if streamErr.Turn > 0 {
		applyOpsStreamErrorSnapshot(entry, streamErr)
	}
	queue.Enqueue(ops, entry)
}

// applyOpsStreamErrorSnapshot replaces entry's live upstream fields with a
// captured WebSocket turn snapshot so later turns cannot alter earlier turn
// attribution.
func applyOpsStreamErrorSnapshot(entry *opscore.OpsInsertErrorLogInput, streamErr OpsStreamError) {
	if entry == nil {
		return
	}
	if streamErr.ProviderID > 0 {
		providerID := streamErr.ProviderID
		entry.ProviderID = &providerID
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
	for _, v := range slices.Backward(streamErr.UpstreamErrors) {
		if v != nil {
			lastStage = v.Stage
			break
		}
	}
	if lastStage == opscore.ErrorPhaseProviderAuth {
		entry.ErrorPhase = opscore.ErrorPhaseProviderAuth
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

// isCountTokensRequest reports whether c targets a count_tokens endpoint or
// OpenAI Responses input_tokens preflight path.
func isCountTokensRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	return strings.Contains(c.Request.URL.Path, "/count_tokens") ||
		IsOpenAIResponsesInputTokensRequestPath(c)
}

// applyOpsLatencyFieldsFromContext copies stage latency observations from c
// into entry.
func applyOpsLatencyFieldsFromContext(c *gin.Context, entry *opscore.OpsInsertErrorLogInput) {
	if c == nil || entry == nil {
		return
	}
	entry.AuthLatencyMs = GetOpsLatencyMs(c, OpsAuthLatencyMsKey)
	entry.RoutingLatencyMs = GetOpsLatencyMs(c, OpsRoutingLatencyMsKey)
	entry.UpstreamLatencyMs = GetOpsLatencyMs(c, OpsUpstreamLatencyMsKey)
	entry.ResponseLatencyMs = GetOpsLatencyMs(c, OpsResponseLatencyMsKey)
}

// applyOpsUpstreamFieldsFromContext copies upstream status, message, detail,
// and attempt-event summaries from c into entry.
func applyOpsUpstreamFieldsFromContext(c *gin.Context, entry *opscore.OpsInsertErrorLogInput) {
	if c == nil || entry == nil {
		return
	}
	if code, ok := GetOpsUpstreamStatusCode(c); ok && code > 0 {
		entry.UpstreamStatusCode = &code
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
	if events := GetOpsUpstreamErrors(c); len(events) > 0 {
		applyOpsUpstreamErrorEvents(entry, events)
	}
}

// applyOpsUpstreamErrorEvents attaches events to entry and overwrites top-level
// upstream status, message, and detail from the last non-nil event.
func applyOpsUpstreamErrorEvents(entry *opscore.OpsInsertErrorLogInput, events []*opscore.OpsUpstreamErrorEvent) {
	entry.UpstreamErrors = events
	var last *opscore.OpsUpstreamErrorEvent
	for _, event := range slices.Backward(events) {
		if event != nil {
			last = event
			break
		}
	}
	if last == nil {
		return
	}

	entry.UpstreamStatusCode = nil
	entry.UpstreamErrorMessage = nil
	entry.UpstreamErrorDetail = nil
	if last.Stage == opscore.ErrorPhaseProviderAuth {
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

// suppressOpsUpstreamAttributionForLocalModelConfiguration clears provider and
// upstream fields on entry when local model configuration rejection caused the error.
func suppressOpsUpstreamAttributionForLocalModelConfiguration(c *gin.Context, entry *opscore.OpsInsertErrorLogInput) {
	if entry == nil || !HasOpsClientBusinessLimited(c) ||
		OpsClientBusinessLimitedReason(c) != OpsClientBusinessLimitedReasonLocalModelConfiguration {
		return
	}
	entry.ProviderID = nil
	entry.UpstreamEndpoint = ""
	entry.UpstreamModel = ""
	entry.UpstreamStatusCode = nil
	entry.UpstreamErrorMessage = nil
	entry.UpstreamErrorDetail = nil
	entry.UpstreamErrors = nil
}

// selectedOpsPlatform returns the non-blank telemetry platform on c or "unknown".
func selectedOpsPlatform(c *gin.Context) string {
	if c != nil && c.Request != nil {
		if platform, ok := c.Request.Context().Value(telemetry.Platform).(string); ok && strings.TrimSpace(platform) != "" {
			return platform
		}
	}
	return "unknown"
}

// resolveOpsPlatform returns actual when non-blank, otherwise "unknown".
func resolveOpsPlatform(_ *apikey.APIKey, actual string) string {
	if strings.TrimSpace(actual) == "" {
		return "unknown"
	}
	return actual
}

// classifyOpsErrorLog delegates phase, business-limit, owner, and source
// selection to opscore.ClassifyRequestError using context observations on c.
func classifyOpsErrorLog(c *gin.Context, errType, message, code string, status int) (phase string, isBusinessLimited bool, errorOwner string, errorSource string) {
	return opscore.ClassifyRequestError(opscore.ErrorClassificationInput{
		Type:                         errType,
		Message:                      message,
		Code:                         code,
		Status:                       status,
		RoutingCapacityLimited:       isOpsRoutingCapacityLimited(c),
		ClientBusinessLimited:        HasOpsClientBusinessLimited(c),
		LocalModelConfiguration:      OpsClientBusinessLimitedReason(c) == OpsClientBusinessLimitedReasonLocalModelConfiguration,
		UpstreamError:                hasOpsUpstreamErrorContext(c),
		UpstreamClientInvalidRequest: isOpsUpstreamClientInvalidRequest(c, errType, code, status),
		ProviderAuthFailure:          hasOpsProviderAuthFailure(c),
	})
}

// isOpsUpstreamClientInvalidRequest reports whether an upstream HTTP 400
// invalid_request_error carries OpenAI's property-name-length client code.
func isOpsUpstreamClientInvalidRequest(c *gin.Context, errType, code string, status int) bool {
	if c == nil || status != http.StatusBadRequest || errType != "invalid_request_error" ||
		!strings.EqualFold(strings.TrimSpace(code), openai.OpenAIPropertyNameAboveMaxLengthCode) {
		return false
	}
	upstreamStatus, ok := GetOpsUpstreamStatusCode(c)
	return ok && upstreamStatus == http.StatusBadRequest
}

// hasOpsUpstreamErrorContext reports whether c holds a positive upstream HTTP
// status or at least one recorded upstream attempt event.
func hasOpsUpstreamErrorContext(c *gin.Context) bool {
	if code, ok := GetOpsUpstreamStatusCode(c); ok && code > 0 {
		return true
	}
	return len(GetOpsUpstreamErrors(c)) > 0
}

// hasOpsProviderAuthFailure reports whether the last non-nil upstream error
// event on c occurred in the provider_auth stage.
func hasOpsProviderAuthFailure(c *gin.Context) bool {
	for _, event := range slices.Backward(GetOpsUpstreamErrors(c)) {
		if event != nil {
			return event.Stage == opscore.ErrorPhaseProviderAuth
		}
	}
	return false
}

// shouldSkipOpsErrorLog evaluates the advanced error-log filter snapshot on ops.
func shouldSkipOpsErrorLog(_ context.Context, ops *opscore.OpsService, message, body, requestPath string) bool {
	if ops == nil {
		return false
	}
	return opscore.ShouldSkipOpsErrorLog(ops.OpsAdvancedSettingsSnapshot(), message, body, requestPath)
}
