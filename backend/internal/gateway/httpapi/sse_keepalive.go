package httpapi

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// sse_keepalive.go keeps downstream text SSE connections active across upstream
// header waits and gaps between complete response frames. A request owns one
// writer and idle timer, so protocol readers and conversions share the same
// connection activity clock. Header waiting can commit HTTP 200 before a
// provider returns; subsequent failures must therefore use the client's stream
// error format while heartbeat-only output remains eligible for provider retry.
//
// The writer serializes handler output and timer writes. Handler header access
// closes the early-commit window before returning a mutable map; after HTTP
// commitment it returns a copy because late mutations cannot affect wire
// headers. The worker uses cached protocol state, stops at terminal frames,
// and respects upstream inactivity without reading mutable Gin request state.

const (
	// sseKeepaliveWriterKey retains the downstream writer for snapshots and byte
	// accounting after outer wrappers replace the request's current response writer.
	sseKeepaliveWriterKey = "downstream_sse_keepalive_writer"
	// localTerminalSSEWrittenKey records that emitLocalTerminalSSE attempted a
	// terminal error frame, allowing TerminalSSEDelivered to deduplicate fallback
	// output even when DownstreamSSEKeepalive is disabled or a write fails.
	localTerminalSSEWrittenKey = "gateway_local_terminal_sse_written"
	// sseKeepaliveFrameLimit bounds metadata inspection without buffering response
	// delivery; larger frames still retain their byte-level boundary tracking.
	sseKeepaliveFrameLimit = 4096
)

// sseKeepaliveWriter serializes text output with one downstream idle-heartbeat worker.
type sseKeepaliveWriter struct {
	gin.ResponseWriter // inner HTTP response writer
	// emissionMu excludes timer output across a terminal event's complete outer
	// writer chain. Handler methods still take mu independently, avoiding reentrancy.
	emissionMu sync.Mutex
	// mu protects all inner writer access and mutable stream state. bytes remains
	// atomic for retained accounting; the snapshot path also locks mu with Size.
	mu    sync.Mutex   // output serialization lock
	bytes atomic.Int64 // accepted heartbeat byte count
	// c is accessed only from handler-side methods. The timer and dispatch
	// listener use copied protocol values and the synchronized stages collector.
	c              *gin.Context
	stages         *timing.Stages      // synchronized upstream activity observations
	interval       time.Duration       // downstream idle heartbeat interval
	stall          time.Duration       // upstream inactivity cutoff
	userAgent      string              // inbound client version identity
	protocol       protocol.ProtocolID // cached client wire protocol
	streaming      bool                // cached client streaming intent
	headerWaitOpen bool                // early HTTP commitment eligibility
	explicitStatus int                 // handler-selected pending HTTP status
	// lastWrite includes local heartbeats for idle scheduling. lastHandlerWrite
	// changes only on accepted handler bytes, so timer heartbeats cannot renew
	// the committed response's inactivity allowance by observing their own output.
	lastWrite        time.Time // most recent accepted output
	lastHandlerWrite time.Time // last accepted handler write time
	atBoundary       bool      // complete SSE frame boundary
	// tail and frame track input across Write calls. tail preserves separators
	// after frame reaches its bounded prefix limit; frame stores inspection bytes.
	tail                [3]byte // previous three output bytes
	tailLen             int     // retained tail byte count
	frame               []byte  // bounded current frame prefix
	anthropicBlockIndex int     // active content block index
	anthropicDeltaType  string  // compatible empty delta kind
	terminal            bool    // terminal event already delivered
	disabled            bool    // permanent heartbeat output stop
	// started publishes one worker under mu. close closes done once and joins
	// worker completion before middleware returns or removes its dispatch listener.
	started bool           // idle worker already started
	closed  bool           // shutdown channel already closed
	wake    chan struct{}  // buffered timer reschedule signal
	done    chan struct{}  // middleware shutdown notification
	worker  sync.WaitGroup // worker lifetime join barrier
}

// CommittedSSE reports whether the active HTTP response has already committed
// a successful event-stream header, including early downstream or wait
// heartbeats. Nil contexts or unwritten writers return false without touching
// mutable response headers, so an open header-wait window stays intact.
//
// When DownstreamSSEKeepalive owns the writer, status and Content-Type are read
// under its mutex to stay race-free with concurrent timer beats without cloning
// the header map. Routes without that middleware inspect the current writer
// only after Written reports true.
func CommittedSSE(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if value, ok := c.Get(sseKeepaliveWriterKey); ok {
		if w, valid := value.(*sseKeepaliveWriter); valid && w != nil {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.committedSSELocked()
		}
	}
	if !c.Writer.Written() {
		return false
	}
	status := c.Writer.Status()
	return status >= 200 && status < 300 && strings.Contains(strings.ToLower(c.Writer.Header().Get("Content-Type")), "text/event-stream")
}

// TerminalSSEDelivered reports whether this request has already emitted a
// local terminal SSE error frame or accepted a terminal event through the
// downstream SSE keepalive writer. Missing contexts return false; fallback
// error handlers use this predicate to suppress duplicate terminal frames even
// when DownstreamSSEKeepalive is absent or a terminal client write failed.
func TerminalSSEDelivered(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if c.GetBool(localTerminalSSEWrittenKey) {
		return true
	}
	value, ok := c.Get(sseKeepaliveWriterKey)
	if !ok {
		return false
	}
	w, valid := value.(*sseKeepaliveWriter)
	if !valid || w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.terminal
}

// DownstreamSSEKeepalive installs idle heartbeats for timed HTTP text requests.
// A nonpositive interval disables it; a nonpositive stall disables the
// committed body inactivity cutoff. Pending upstream headers use transport budgets. The middleware must immediately follow TextRequestTiming.
//
// Header-wait callbacks open early commitment only for client-streamed text.
// Once committed, complete SSE boundaries determine safe heartbeat insertion,
// including converted streams whose upstream emits no heartbeat. Cleanup joins
// the worker before returning and restores the original writer only when no
// outer wrapper owns the current writer. Compact and image controllers retain
// their own protocols and never qualify for this worker's early commitment.
// @project-doc docs/architecture/gateway_request_lifecycle.md#sse_keepalive
func DownstreamSSEKeepalive(interval, stall time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		stages := timing.StagesFromContext(c.Request.Context())
		// Compact owns a separate background writer and never participates in text
		// heartbeats. Leave its writer untouched so its worker cannot enter handler-only
		// refresh methods while a Compact attempt replaces the request context.
		compact := RouteProtocol(c.Request.Method, strings.TrimSuffix(c.Request.URL.Path, "/")) == protocol.ProtocolResponsesCompact
		if interval <= 0 || stages == nil || compact {
			c.Next()
			return
		}
		original := c.Writer
		w := &sseKeepaliveWriter{
			ResponseWriter: original, c: c, stages: stages, interval: interval, stall: stall,
			userAgent: c.Request.UserAgent(), lastWrite: time.Now(), atBoundary: true,
			anthropicBlockIndex: -1, wake: make(chan struct{}, 1), done: make(chan struct{}),
		}
		c.Writer = w
		c.Set(sseKeepaliveWriterKey, w)
		stages.SetUpstreamRequestListener(w.openHeaderWait)
		// Join before removing the listener or restoring the writer so no delayed
		// heartbeat can touch response state after request middleware has returned.
		defer func() {
			w.close()
			if c.Writer == w {
				c.Writer = original
			}
			stages.SetUpstreamRequestListener(nil)
		}()
		c.Next()
	}
}

// refreshLocked copies client state during handler-side access with mu held.
// The worker must never call this method because request handlers can replace
// c.Request while upstream or timer goroutines continue independently.
func (w *sseKeepaliveWriter) refreshLocked() {
	w.protocol, _ = requeststate.ClientProtocolFromContext(w.c.Request.Context())
	w.streaming = w.stages.Streaming()
}

// Header closes early commitment before returning mutable pending response metadata.
// After commitment it returns an independent copy, so late handler mutations
// cannot race the worker's inner timing writer reading the committed map.
//
// Callers finish editing a pending map before another upstream dispatch opens
// a new header-wait window. The map is borrowed only while the handler owns
// response preparation; gateway SSE does not use trailers, so modifications
// made after commitment have no supported wire effect to preserve.
func (w *sseKeepaliveWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshLocked()
	w.headerWaitOpen = false
	if w.ResponseWriter.Written() {
		return w.ResponseWriter.Header().Clone()
	}
	return w.ResponseWriter.Header()
}

// snapshotHeader copies metadata without closing a pending upstream wait window.
// InitialOutput uses this read-only view while a codec stages its first event;
// later Begin performs the handler-side header handoff before modifying metadata.
func (w *sseKeepaliveWriter) snapshotHeader() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Header().Clone()
}

// WriteHeader records an explicit status and closes early heartbeat commitment.
// The inner writer retains its ordinary status rules, including ignoring status
// changes after HTTP commitment; callers serialize this with other handler operations.
func (w *sseKeepaliveWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshLocked()
	w.headerWaitOpen = false
	w.explicitStatus = code
	w.ResponseWriter.WriteHeader(code)
}

// Write serializes response bytes and observes accepted SSE frame boundaries.
// Partial or failed writes permanently disable heartbeat insertion; the original
// count and error remain available to the handler for disconnect handling.
func (w *sseKeepaliveWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshLocked()
	w.headerWaitOpen = false
	n, err := w.ResponseWriter.Write(p)
	w.wroteLocked(p[:n], err, n == len(p))
	return n, err
}

// WriteString preserves the inner string-write path under the heartbeat mutex.
// Accepted bytes update framing just like Write; partial or failed delivery
// disables further heartbeat activity while preserving the inner writer's result.
func (w *sseKeepaliveWriter) WriteString(s string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshLocked()
	w.headerWaitOpen = false
	n, err := w.ResponseWriter.WriteString(s)
	w.wroteLocked([]byte(s[:n]), err, n == len(s))
	return n, err
}

// wroteLocked updates delivery observations after an inner write with mu held.
// Only accepted bytes change the idle clock; short or failed writes disable
// keepalive work so the handler remains responsible for reporting disconnection.
func (w *sseKeepaliveWriter) wroteLocked(p []byte, err error, complete bool) {
	if err != nil || !complete {
		w.disabled = true
	}
	if len(p) > 0 {
		w.lastWrite = time.Now()
		w.lastHandlerWrite = w.lastWrite
		w.observeLocked(p)
	}
	if w.committedSSELocked() {
		w.ensureLoopLocked()
	}
}

// WriteHeaderNow commits pending metadata after closing the header-wait window.
// Committed SSE becomes eligible for idle heartbeats even before a payload write;
// ordinary HTTP responses retain their existing status and body behavior.
func (w *sseKeepaliveWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshLocked()
	w.headerWaitOpen = false
	w.ResponseWriter.WriteHeaderNow()
	if w.committedSSELocked() {
		w.ensureLoopLocked()
	}
}

// Flush delegates synchronous flushing after closing early heartbeat commitment.
// An established SSE response starts the idle worker; flushing alone does not
// reset its byte-activity clock or permit insertion into an unfinished frame.
func (w *sseKeepaliveWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshLocked()
	w.headerWaitOpen = false
	w.ResponseWriter.Flush()
	if w.committedSSELocked() {
		w.ensureLoopLocked()
	}
}

// Status reads the inner HTTP status under the output serialization lock.
func (w *sseKeepaliveWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Status()
}

// Size reads accepted HTTP bytes without closing a pending header-wait window.
func (w *sseKeepaliveWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Size()
}

// Written reads HTTP commitment without changing content or heartbeat eligibility.
func (w *sseKeepaliveWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Written()
}

// Hijack permanently disables heartbeats before transferring connection ownership.
// The inner writer supplies the connection and error; disabling remains in
// effect even when that writer rejects the requested ownership transfer.
func (w *sseKeepaliveWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.disabled = true
	return w.ResponseWriter.Hijack()
}

// CloseNotify delegates the inner connection's client-disconnection notification channel.
func (w *sseKeepaliveWriter) CloseNotify() <-chan bool { return w.ResponseWriter.CloseNotify() }

// Pusher exposes the inner response writer's optional HTTP push implementation.
func (w *sseKeepaliveWriter) Pusher() http.Pusher { return w.ResponseWriter.Pusher() }

// openHeaderWait opens early commitment for the next upstream HTTP dispatch.
// It reads protocol identity from the dispatch context and streaming intent
// from the synchronized collector, never from the mutable Gin request.
//
// Each dispatch resets the idle deadline while headers remain uncommitted.
// Handler-side access closes this window before response metadata can change,
// whereas read-only sink snapshots preserve it during first-output staging.
// An already committed response uses its existing idle loop, and a disabled
// writer cannot be revived by a late callback from an abandoned exchange.
func (w *sseKeepaliveWriter) openHeaderWait(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.disabled || w.ResponseWriter.Written() {
		return
	}
	w.protocol, _ = requeststate.ClientProtocolFromContext(ctx)
	w.streaming = w.stages.Streaming()
	w.headerWaitOpen = true
	now := time.Now()
	if now.After(w.lastWrite) {
		w.lastWrite = now
	}
	w.ensureLoopLocked()
}

// ensureLoopLocked starts at most one idle worker and requests a fresh deadline.
// Call with mu held; the buffered wake signal coalesces output notifications
// while the worker calculates the latest accepted-byte deadline under that lock.
//
// Startup is published before launching the goroutine, preventing simultaneous
// output operations from creating independent timers. Subsequent notifications
// never block a handler holding mu: one outstanding wake is sufficient because
// the worker always rereads lastWrite instead of treating notifications as a
// count of elapsed heartbeat intervals.
func (w *sseKeepaliveWriter) ensureLoopLocked() {
	if w.disabled || w.terminal {
		return
	}
	if !w.started {
		// Publish startup before launching so all later writes share one timer and
		// shutdown can reliably join every worker using this request's response writer.
		w.started = true
		w.worker.Go(w.loop)
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// loop schedules heartbeats from the most recent accepted downstream write.
// It exits on middleware shutdown and owns its timer for the entire lifetime.
//
// A wake can arrive after several handler writes; each iteration recomputes
// the deadline under mu instead of extending an older timer blindly. Due beats
// still pass framing, protocol and upstream-activity checks under that lock.
// Rejected beats wait another interval, avoiding a busy loop when a terminal,
// stalled or partially written response cannot accept heartbeat output.
func (w *sseKeepaliveWriter) loop() {
	timer := time.NewTimer(w.interval)
	defer timer.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-timer.C:
		case <-w.wake:
		}
		w.emissionMu.Lock()
		w.mu.Lock()
		now := time.Now()
		delay := w.lastWrite.Add(w.interval).Sub(now)
		if delay <= 0 {
			w.beatLocked(now)
			delay = w.interval
		}
		w.mu.Unlock()
		w.emissionMu.Unlock()
		timer.Reset(delay)
	}
}

// committedSSELocked reports a successful committed SSE response with mu held.
func (w *sseKeepaliveWriter) committedSSELocked() bool {
	status := w.ResponseWriter.Status()
	return w.ResponseWriter.Written() && status >= 200 && status < 300 && strings.Contains(strings.ToLower(w.ResponseWriter.Header().Get("Content-Type")), "text/event-stream")
}

// beatLocked writes one eligible downstream heartbeat with mu held.
// It never reads Gin state and permanently disables itself after failed output.
//
// Uncommitted streaming dispatches may select HTTP 200 SSE after an idle
// interval, provided handler metadata has not selected another response. Already
// committed responses require a complete frame boundary and successful SSE
// status. Pending upstream response headers remain governed by transport and
// first-output budgets even if a heartbeat already committed downstream HTTP.
// Once headers arrive, upstream activity or accepted handler bytes renew the
// inactivity allowance; local timer writes never renew that allowance.
// Accepted heartbeat bytes join the same response-size snapshot used by provider retry checks.
func (w *sseKeepaliveWriter) beatLocked(now time.Time) {
	if w.disabled || w.terminal {
		return
	}
	switch w.protocol {
	case protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolOpenAIResponses, protocol.ProtocolGeminiGenerateContent, protocol.ProtocolQoderChat:
	default:
		return
	}
	if w.ResponseWriter.Written() {
		// A heartbeat may have committed HTTP while the transport still awaits
		// headers. Only body-phase inactivity may stop an otherwise eligible beat.
		if w.stall > 0 && !w.stages.UpstreamHeadersPending() {
			activity := w.stages.LastUpstreamActivity()
			if w.lastHandlerWrite.After(activity) {
				activity = w.lastHandlerWrite
			}
			if now.Sub(activity) >= w.stall {
				return
			}
		}
		if !w.committedSSELocked() || !w.atBoundary {
			return
		}
	} else {
		if !w.headerWaitOpen || !w.streaming || (w.explicitStatus != 0 && w.explicitStatus != http.StatusOK) {
			return
		}
		header := w.ResponseWriter.Header()
		contentType := strings.ToLower(header.Get("Content-Type"))
		if contentType != "" && !strings.Contains(contentType, "text/event-stream") {
			return
		}
		// This is the accepted early-commit tradeoff: later errors retain status
		// 200 and use SSE, while provider headers arriving afterward cannot be sent.
		if contentType == "" {
			header.Set("Content-Type", "text/event-stream")
		}
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
	frame := w.frameLocked()
	n, err := io.WriteString(w.ResponseWriter, frame)
	w.bytes.Add(int64(n))
	if err != nil || n != len(frame) {
		// Retain accepted bytes even on a short write, but leave disconnect
		// reporting to the handler's output path and prohibit further timer writes.
		w.disabled = true
		return
	}
	w.ResponseWriter.Flush()
	w.lastWrite = now
}

// frameLocked selects the client's heartbeat using cached state with mu held.
// Claude Code receives an empty delta only inside a recognized open content
// block; other Messages states use ping. Gemini SDKs require empty SSE events,
// while the remaining protocols accept ordinary comment heartbeats.
func (w *sseKeepaliveWriter) frameLocked() string {
	if w.protocol == protocol.ProtocolGeminiGenerateContent {
		return "\n\n"
	}
	if w.protocol != protocol.ProtocolAnthropicMessages {
		return ": keepalive\n\n"
	}
	if w.anthropicBlockIndex >= 0 && anthropic.ShouldUseClaudeCodeNoopDeltaKeepalive(w.userAgent) {
		if frame, ok := anthropic.BuildClaudeCodeNoopDeltaKeepalive(w.anthropicBlockIndex, w.anthropicDeltaType); ok {
			return frame
		}
	}
	return "event: ping\ndata: {\"type\": \"ping\"}\n\n"
}

// emitTerminal serializes a terminal delivery against the heartbeat worker.
// The callback uses the complete HTTP writer chain, preserving capture and model
// restoration. Only a successful callback publishes this explicit terminal marker;
// existing writer failure handling may independently disable heartbeat output.
//
// emissionMu surrounds the callback while individual writer methods retain their
// ordinary mu locking. The worker always acquires these locks in that order, so
// it cannot insert a heartbeat between accepted terminal bytes and publication
// of terminal state. A failed callback leaves failure handling to the caller.
func (w *sseKeepaliveWriter) emitTerminal(write func() error) error {
	w.emissionMu.Lock()
	defer w.emissionMu.Unlock()
	if err := write(); err != nil {
		return err
	}
	w.mu.Lock()
	w.terminal = true
	w.mu.Unlock()
	return nil
}

// observeLocked tracks SSE separators and bounded metadata across accepted writes.
// Call with mu held and only for bytes the underlying writer accepted.
//
// Frame prefixes support terminal and Anthropic block recognition without
// buffering or delaying client delivery. Separators remain tracked after that
// prefix fills, including CRLF boundaries split across writes. A truncated JSON
// payload cannot supply trustworthy fields, so only its event line can classify
// that frame. Every completed frame releases its prefix before the next event,
// keeping memory independent of the lifetime and total size of the stream.
func (w *sseKeepaliveWriter) observeLocked(p []byte) {
	for _, b := range p {
		if len(w.frame) < sseKeepaliveFrameLimit {
			w.frame = append(w.frame, b)
		}
		boundary := b == '\n' && ((w.tailLen > 0 && w.tail[w.tailLen-1] == '\n') || (w.tailLen >= 2 && w.tail[w.tailLen-2] == '\n' && w.tail[w.tailLen-1] == '\r'))
		w.atBoundary = boundary
		if boundary {
			w.observeFrameLocked()
			w.frame = w.frame[:0]
		}
		if w.tailLen < len(w.tail) {
			w.tail[w.tailLen] = b
			w.tailLen++
		} else {
			copy(w.tail[:], w.tail[1:])
			w.tail[len(w.tail)-1] = b
		}
	}
}

// observeFrameLocked classifies the retained frame prefix with mu held.
// Event names remain usable when a large JSON payload was truncated; JSON
// fields participate only when the entire first data line is valid.
//
// Either the SSE name or JSON type can end output, while a content-block update
// uses the JSON type when present. A stop clears only the matching active block,
// preserving a newer block when older stop evidence arrives. Unsupported block
// kinds retain ping as their heartbeat format through the delta builder.
func (w *sseKeepaliveWriter) observeFrameLocked() {
	event, data := "", ""
	foundData := false
	for line := range strings.SplitSeq(string(w.frame), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			event = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok && !foundData {
			data = strings.TrimSpace(value)
			foundData = true
		}
	}
	kind := ""
	valid := gjson.Valid(data)
	if valid {
		kind = gjson.Get(data, "type").String()
	}
	for _, name := range []string{event, kind} {
		switch name {
		case "message_stop", "error", "response.completed", "response.failed", "response.incomplete":
			w.terminal = true
		}
	}
	if data == "[DONE]" {
		w.terminal = true
	}
	if !valid {
		return
	}
	if kind == "" {
		kind = event
	}
	index := int(gjson.Get(data, "index").Int())
	switch kind {
	case "content_block_start":
		w.anthropicBlockIndex = index
		w.anthropicDeltaType = anthropic.ClaudeCodeKeepaliveDeltaTypeForContentBlock(gjson.Get(data, "content_block.type").String())
	case "content_block_stop":
		if index == w.anthropicBlockIndex {
			w.anthropicBlockIndex = -1
			w.anthropicDeltaType = ""
		}
	}
}

// close disables writes, signals shutdown once and joins the heartbeat worker.
// It is safe after failed writes or Hijack because those operations disable
// heartbeat output without consuming this middleware's lifetime cleanup signal.
//
// Disabling under mu prevents a concurrent dispatch callback from starting a
// new worker after shutdown begins. The wait runs outside mu so an already
// scheduled worker can finish its state check and exit. Returning from close
// therefore establishes that no timer can access the inner writer while outer
// middleware restores response ownership or finishes request observations.
func (w *sseKeepaliveWriter) close() {
	w.mu.Lock()
	w.disabled = true
	if !w.closed {
		close(w.done)
		w.closed = true
	}
	w.mu.Unlock()
	w.worker.Wait()
}
