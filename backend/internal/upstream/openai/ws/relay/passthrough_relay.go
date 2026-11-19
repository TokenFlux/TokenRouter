package openai_ws_v2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// passthrough_relay.go relays WebSocket frames bidirectionally and settles
// response usage, identity, and timing across disconnects.
// A Responses connection can lose its client reader while upstream usage and
// terminal identity are still arriving. Graceful client closure switches the
// upstream worker into a bounded drain that observes text events and drops
// downstream frames, keeping late accounting evidence available to the gateway.
// Policy rejection occurs before observation. Accepted text frames are observed
// before client writes, while a bare error's settlement can be deferred beyond
// its write.
//
// The forwarding workers and optional idle watchdog report independent exits.
// Relay selects drain or cancellation from the first exit, closes upstream, and
// joins its observer before final pending-error settlement and aggregate reads.
// Timing, model, and tier evidence use response-ID records; usage is held in
// one current snapshot and added into session totals at settlement. Bare error
// waits for a possible response.failed so paired terminal frames can share one
// usage settlement.

// passthroughUsageParseFailureTotal counts rejected usage snapshots across
// relay sessions for diagnostic reads and failure reporting.
var passthroughUsageParseFailureTotal atomic.Int64

// FrameConn is the synchronous frame transport used by both relay directions.
// Implementations support one reader and one writer concurrently and let
// cancellation or Close release the upstream reader before Relay settles its
// result.
type FrameConn interface {
	// ReadFrame reads one frame under ctx. Relay keeps one reader per direction and
	// relies on cancellation or Close to let the upstream reader return before
	// final result settlement.
	ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error)
	// WriteFrame writes the supplied frame under ctx. Relay serializes writes
	// within each direction; payload access finishes before returning so received
	// buffers can be reused by the transport reader.
	WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error
	// Close releases the frame transport and allows its pending read to finish.
	// Relay can call it repeatedly and discards cleanup errors; returned relay
	// failures leave client closure to the surrounding caller.
	Close() error
}

// Usage holds the token counters accumulated from observed upstream snapshots.
type Usage struct {
	CacheWriteDetails        *openai.CacheWriteTokensDetails // validated lifetime details
	InputTokens              int                             // upstream input tokens
	OutputTokens             int                             // upstream output tokens
	CacheCreationInputTokens int                             // reported cache creation tokens
	CacheReadInputTokens     int                             // reported cache read tokens
	ImageOutputTokens        int                             // reported image output tokens
}

// RelayResult combines settled session usage with the latest retained upstream
// response metadata.
type RelayResult struct {
	// RequestModel is trimmed from the initial client frame and remains that model
	// across subsequent requests in this relay.
	RequestModel string
	// ResponseServiceTier retains the tier from the last retired response timing
	// record; an ID-less terminal can leave earlier metadata present.
	ResponseServiceTier string
	// UpstreamResponseModel retains the model from the last retired timing record,
	// with terminal declarations preferred by its model observer.
	UpstreamResponseModel string
	// Usage sums finalized turn snapshots; held usage from an unfinished turn
	// contributes only when terminal or pending-error settlement finalizes it.
	Usage Usage
	// RequestID is the last nonblank settled upstream response ID, used for
	// response correlation rather than an HTTP request identifier.
	RequestID string
	// TerminalEventType is the latest observed terminal name, including a bare
	// error whose pending settlement may retain earlier response identity.
	TerminalEventType string
	// Timings retains the latest settled response observation. Its cloned maps
	// belong to this result, while aggregate session usage is reported separately.
	Timings *timing.Performance
	// Duration measures elapsed time from the relay clock start; a custom clock
	// moving backward can produce a negative session duration.
	Duration time.Duration
	// ClientToUpstreamFrames, UpstreamToClientFrames, and DroppedDownstreamFrames
	// are sampled from separate worker atomics during settlement. The initial
	// client frame counts even when FirstMessageSent skips its write, and binary
	// frames participate in forwarding counts.
	ClientToUpstreamFrames  int64 // successful upstream frame count
	UpstreamToClientFrames  int64 // successful client frame count
	DroppedDownstreamFrames int64 // drained frame drop count
}

// RelayTurnResult reports usage and timing from one terminal settlement to
// gateway completion hooks.
type RelayTurnResult struct {
	// RequestModel comes from the relay's initial client frame; gateway completion
	// consumers can use their separately captured model for later turns.
	RequestModel string
	// ResponseServiceTier comes from this turn's terminal timing record and remains
	// empty when no tier was retained for that record.
	ResponseServiceTier string
	// UpstreamResponseModel comes from this response ID's model observer; a fresh
	// turn with no declaration reports an empty model.
	UpstreamResponseModel string
	// Usage is the finalized held snapshot for this completion, after terminal
	// replacement or retained progress usage has been selected.
	Usage Usage
	// RequestID identifies the settled response; a bare error completion is
	// permitted to report an empty ID.
	RequestID         string
	TerminalEventType string // settled terminal event name
	// StartedAt, Duration, and Timings are captured from the retired response
	// timing. Missing timing leaves zero start/duration and no performance snapshot.
	// Duration describes the accepted turn, while Timings derives the selected
	// upstream exchange interval and owns separate callback storage.
	StartedAt time.Time           // captured turn start time
	Duration  time.Duration       // elapsed settled turn duration
	Timings   *timing.Performance // exchange performance observations
}

// RelayExit identifies a relay interruption and its successful downstream write
// evidence.
type RelayExit struct {
	Stage string // selected relay exit stage
	Err   error  // selected relay exit error
	// Graceful carries the worker classification to BeforeRelayCancel;
	// interruptions returned by Relay use the default false classification.
	Graceful bool
	// WroteDownstream is true after a successful client frame write, including
	// preamble or binary delivery; failed partial writes do not set it.
	WroteDownstream bool
}

// RelayOptions configures relay deadlines, timing sources, and synchronous
// observation hooks. Clock and diagnostic callbacks can run concurrently across
// workers, and BeforeRelayCancel can overlap upstream observation; callers
// synchronize shared state used by those callbacks.
type RelayOptions struct {
	// BeginPerformance binds a dispatched request to its logical turn and provider attempt.
	BeginPerformance func(int, time.Time) *timing.Performance

	// WriteTimeout supplies each write's base deadline duration; Relay uses two
	// minutes when this value is nonpositive.
	WriteTimeout time.Duration
	// WriteTimeoutForTurn is queried for each write and overrides the base duration
	// only when its returned value is positive.
	WriteTimeoutForTurn func() time.Duration
	// IdleTimeout enables the activity watchdog when positive; nonpositive values
	// let the gateway's per-turn idle policy govern its sessions.
	IdleTimeout time.Duration
	// UpstreamDrainTimeout bounds exit-signal waiting after graceful client
	// closure; a nonpositive value selects twelve hundred milliseconds before
	// upstream cancellation.
	UpstreamDrainTimeout time.Duration
	// FirstTurnStartedAt supplies timing for an initial response.create frame; zero
	// uses relay start, and other initial event types leave it unused.
	FirstTurnStartedAt time.Time
	// TakeNextTurnStartedAt supplies the accepted start for later response.create
	// frames; a missing hook or zero result uses the relay clock.
	TakeNextTurnStartedAt func() time.Time
	// FirstMessageType keeps binary when supplied; every other value selects text
	// for the prepared initial upstream frame.
	FirstMessageType coderws.MessageType
	// FirstMessageSent skips the first write but counts that frame and lets
	// graceful client-read closure return nil before later exit-error selection.
	FirstMessageSent bool
	// StartClientAfterFirstDownstream delays client reading until the first
	// successful downstream frame, including structural preamble or binary
	// delivery.
	StartClientAfterFirstDownstream bool
	// OnUsageParseFailure synchronously receives an eligible rejected usage
	// snapshot after its shared failure counter increments; nil leaves diagnostic
	// counting enabled.
	OnUsageParseFailure func(eventType string, usageRaw string)
	// OnUpstreamEvent receives copied bytes for text frames with a nonblank
	// extracted event type; callers may retain or modify that copy.
	OnUpstreamEvent func(eventType string, payload []byte)
	// OnTurnComplete receives settled terminals after their delivery observation,
	// while the paired write hook still holds admission. Dropped frames and deferred
	// bare errors complete when accounting settles, independently of delivery.
	OnTurnComplete func(turn RelayTurnResult)
	// BeforeWriteClient, BeforeClientWrite, and AfterClientWrite run in upstream
	// frame order. Rejection precedes observation, while the paired write callbacks
	// surround attempted delivery; rejected or dropped frames skip that pair.
	//
	// BeforeWriteClient may reject a frame before usage or timing observation; its
	// boolean describes earlier successful downstream frames in this worker.
	BeforeWriteClient func(msgType coderws.MessageType, payload []byte, wroteDownstream bool) error
	// BeforeClientWrite follows event observation and precedes delivery. The
	// terminal completion callback runs before AfterClientWrite releases admission;
	// dropped frames skip the paired hooks.
	BeforeClientWrite func(msgType coderws.MessageType, payload []byte)
	// AfterClientWrite runs after each attempted downstream write with its error,
	// even on failure; the gateway uses it to finish terminal admission.
	AfterClientWrite func(msgType coderws.MessageType, payload []byte, writeErr error)
	// BeforeRelayCancel observes the first exit before relay cancellation or drain
	// selection, letting the gateway prepare the client's close status.
	BeforeRelayCancel func(exit RelayExit)
	// ReadClientFrame optionally replaces clientConn.ReadFrame in the client
	// worker; it respects the supplied relay context and can implement
	// accepted-request pacing.
	ReadClientFrame func(ctx context.Context, clientConn FrameConn) (coderws.MessageType, []byte, error)
	// OnTrace synchronously receives lifecycle and forwarding diagnostics from
	// several workers; callers synchronize any shared collection used by this
	// callback.
	OnTrace func(event RelayTraceEvent)
	// Now supplies elapsed-time and activity observations, defaulting to time.Now;
	// it may be called concurrently by workers and the controller.
	Now func() time.Time
}

// RelayTraceEvent describes one forwarding or shutdown observation for
// diagnostic consumers.
type RelayTraceEvent struct {
	Stage string // trace lifecycle stage name
	// Direction identifies forwarding or watchdog activity; lifecycle stages with
	// no recognized direction can leave this diagnostic string empty.
	Direction string
	// MessageType labels text, binary, or numeric unknown types for frame
	// observations; lifecycle events can leave the attribute empty.
	MessageType  string
	PayloadBytes int  // observed frame payload bytes
	Graceful     bool // reported graceful exit classification
	// WroteDownstream carries successful client-frame evidence at stages that
	// report it; other lifecycle observations may leave the attribute false.
	WroteDownstream bool
	// Error contains the traced failure's text, with an empty string used when the
	// observation has no error.
	Error string
}

// relayState holds usage and response observations owned by the upstream
// reader. Relay joins that reader before final settlement; pendingTurnStart
// separately transfers one client-side start through an atomic pointer.
type relayState struct {
	beginPerformance func(int, time.Time) *timing.Performance // accepted-turn observation factory
	dispatchedTurns  int                                      // client-worker dispatch sequence

	// usage and turnUsage keep session totals separate from the current snapshot.
	// Terminal finalization adds turnUsage into usage and clears it before later
	// progress observations can populate another turn.
	usage Usage // finalized session token counters
	// turnUsage holds positive progress-field updates or a selected terminal
	// snapshot until finalization moves its counters into session totals.
	turnUsage Usage // held turn token counters
	// requestModel is the trimmed initial client model; subsequent client frames
	// update pending timing but do not replace this value.
	requestModel string
	// pendingTurnStart transfers one copied start from the client worker to the
	// upstream observer; later stores replace an unconsumed value.
	pendingTurnStart atomic.Pointer[relayPerformanceStart]
	consumedStart    *relayPerformanceStart // observer-owned accepted start evidence
	modeReader       func() string          // runtime mode at turn dispatch
	clock            func() time.Time       // relay monotonic observation clock
	// lastResponseID changes on a nonblank settled terminal ID; an ID-less error
	// can retain the previously completed response identity.
	lastResponseID string
	// lastResponseServiceTier changes when a terminal retires a timing record,
	// including clearing an earlier tier when that record has none.
	lastResponseServiceTier string
	// lastResponseModel changes when a terminal retires its timing record, using
	// that record's response-specific preferred model declaration.
	lastResponseModel string
	// terminalEventType changes on recognized terminal observation, before a bare
	// error's final usage or timing settlement can occur.
	terminalEventType string
	performance       *timing.Performance
	// turnTimingByID and activeTurn share timing pointers: new timing creation
	// installs both, existing lookups leave activeTurn unchanged, and identified
	// retirement clears activeTurn only when it references that same record.
	turnTimingByID map[string]*relayTurnTiming // response ID timing lookup
	activeTurn     *relayTurnTiming            // current active timing pointer
	// pendingBareError holds the latest bare error until paired failure, later-turn
	// evidence, or read termination selects its terminal settlement.
	pendingBareError *observedUpstreamEvent
}

// relayExitSignal transfers one worker's exit cause to the relay controller.
type relayExitSignal struct {
	// stage, err, graceful, and wroteDownstream describe one worker's stopping
	// point. Relay combines successful write evidence across observed signals while
	// selecting an exit cause through its first-exit and drain rules.
	stage           string // worker exit stage name
	err             error  // worker exit cause error
	graceful        bool   // worker disconnect classification flag
	wroteDownstream bool   // successful client frame evidence
}

// observedUpstreamEvent carries event identity and the values available after
// terminal settlement.
type observedUpstreamEvent struct {
	// terminal becomes true after usage settlement; a newly observed bare error
	// retains false while waiting for a following failed terminal.
	terminal  bool
	eventType string // trimmed upstream event name
	// responseID prefers nested response.id, then response_id; terminal top-level
	// IDs and an active bare-error turn can supply missing identity.
	responseID string
	// responseServiceTier comes from the retired turn timing and remains empty when
	// terminal settlement has no matching record.
	responseServiceTier string
	// responseModel is the retired timing's preferred declaration, retaining each
	// response ID's model evidence across interleaved upstream events.
	responseModel string
	// usage holds parsed terminal counters before settlement and the selected
	// finalized snapshot afterward; nonterminal parsing returns an empty value.
	usage Usage
	// startedAt, duration, and performance come from the retired turn record.
	// duration is clamped at zero; performance remains live until terminal delivery
	// is observed, then completion receives a clone. Missing records leave zero or nil.
	startedAt   time.Time           // retired turn start time
	duration    time.Duration       // nonnegative turn elapsed duration
	performance *timing.Performance // retired exchange performance observer
}

// relayTurnTiming retains start, performance, model, and terminal tier evidence for
// one response ID.
type relayTurnTiming struct {
	// startAt records a consumed client start, or the observation time when the
	// pending start slot is empty.
	startAt     time.Time
	performance *timing.Performance
	// terminalResponseServiceTier retains a nonblank terminal tier; empty or
	// nonterminal observations leave this record's retained tier unchanged.
	terminalResponseServiceTier string
	// modelObserver retains this response's normalized model evidence, preferring
	// recognized Responses terminal declarations over the first usable nonterminal
	// declaration.
	modelObserver protocol.ResponseModelObserver
}

// relayPerformanceStart publishes accepted-turn identity and dispatch policy together.
// The client worker writes one immutable value before forwarding response.create;
// the upstream observer consumes it when binding the response identifier.
type relayPerformanceStart struct {
	performance *timing.Performance // dispatched turn observations

	at       time.Time // accepted turn start
	upstream time.Time // generation dispatch start
	mode     string    // captured latency mode
}

// recordUsageParseFailure increments the shared diagnostic count for rejected
// usage snapshots.
func recordUsageParseFailure() {
	// Count rejected snapshots even when no reporting callback is installed, so
	// diagnostic readers still see parsing failures across sessions.
	passthroughUsageParseFailureTotal.Add(1)
}

// Relay forwards the prepared first frame and runs both forwarding directions
// with optional idle detection. Nil context selects Background; nil connections
// return an initialization exit, and a failed first write returns before worker
// startup. Connection implementations must release the upstream reader on
// cancellation or Close because final result settlement waits for that reader
// without a separate join deadline.
//
// A graceful client-read exit starts a bounded drain: upstream events can still
// settle usage while downstream frames are dropped. Other first exits cancel
// relay work and close upstream. The upstream observer is joined before pending
// errors and aggregate results are settled; the client reader and watchdog have
// separate cancellation lifetimes. Parent cancellation makes the selected first
// exit a failure so the caller can choose the client close status.
//
// FirstMessageSent counts the initial frame as already forwarded and lets a
// graceful client-read exit return nil even when a second exit was observed.
// Otherwise a client disconnect is reported, the first failure takes
// precedence, and a later observed failure can replace a graceful exit. The
// pre-sent graceful client-read path keeps client closure with the caller.
// Other selected normal completions close the client, while reported failures
// leave closure to the caller. Callbacks follow RelayOptions ordering and
// concurrency rules.
func Relay(
	ctx context.Context,
	clientConn FrameConn,
	upstreamConn FrameConn,
	firstClientMessage []byte,
	options RelayOptions,
) (RelayResult, *RelayExit) {
	result := RelayResult{RequestModel: strings.TrimSpace(gjson.GetBytes(firstClientMessage, "model").String())}
	if clientConn == nil || upstreamConn == nil {
		return result, &RelayExit{Stage: "relay_init", Err: errors.New("relay connection is nil")}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	nowFn := options.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	writeTimeout := options.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 2 * time.Minute
	}
	getWriteTimeout := func() time.Duration {
		if options.WriteTimeoutForTurn != nil {
			if v := options.WriteTimeoutForTurn(); v > 0 {
				return v
			}
		}
		return writeTimeout
	}
	drainTimeout := options.UpstreamDrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 1200 * time.Millisecond
	}
	firstMessageType := options.FirstMessageType
	if firstMessageType != coderws.MessageBinary {
		firstMessageType = coderws.MessageText
	}
	startAt := nowFn()
	state := &relayState{requestModel: result.RequestModel, clock: nowFn, beginPerformance: options.BeginPerformance, modeReader: func() string { return timing.ModeFromContext(ctx) }}
	if isClientResponseCreateFrame(firstMessageType, firstClientMessage) {
		firstTurnStartedAt := options.FirstTurnStartedAt
		if firstTurnStartedAt.IsZero() {
			firstTurnStartedAt = startAt
		}
		state.setPendingTurnStartedAt(firstTurnStartedAt)
	}
	onTrace := options.OnTrace

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	// Both forwarding directions publish elapsed nanoseconds from startAt while
	// the watchdog samples it independently; storing elapsed offsets from startAt
	// preserves Go's monotonic clock readings across Time.Sub calls.
	lastActivity := atomic.Int64{}
	markActivity := func() {
		lastActivity.Store(int64(nowFn().Sub(startAt)))
	}

	writeUpstream := func(msgType coderws.MessageType, payload []byte) error {
		writeCtx, cancel := context.WithTimeout(relayCtx, getWriteTimeout())
		defer cancel()
		return upstreamConn.WriteFrame(writeCtx, msgType, payload)
	}
	writeClient := func(msgType coderws.MessageType, payload []byte) error {
		// Keep downstream writes independent of relay cancellation. coder/websocket can hard-close
		// the connection when a write context is canceled, even after a successful write while
		// its cancellation callback is being disarmed. context.AfterFunc's stop does not join
		// a running callback, so cancellation can discard the pending close frame and expose EOF.
		// As with background-context reads, exit handlers reclaim the connection with
		// Close or CloseNow so the adapter can send the intended close code.
		writeCtx, cancel := context.WithTimeout(context.Background(), getWriteTimeout())
		defer cancel()
		return clientConn.WriteFrame(writeCtx, msgType, payload)
	}

	clientToUpstreamFrames := &atomic.Int64{}
	upstreamToClientFrames := &atomic.Int64{}
	droppedDownstreamFrames := &atomic.Int64{}
	emitRelayTrace(onTrace, RelayTraceEvent{
		Stage:        "relay_start",
		PayloadBytes: len(firstClientMessage),
		MessageType:  relayMessageTypeString(firstMessageType),
	})

	// The gateway may have already written the prepared request; skip duplicate
	// transmission while retaining the initial frame in the relay's accounting.
	if options.FirstMessageSent {
		emitRelayTrace(onTrace, RelayTraceEvent{
			Stage:        "write_first_message_skipped",
			Direction:    "client_to_upstream",
			MessageType:  relayMessageTypeString(firstMessageType),
			PayloadBytes: len(firstClientMessage),
		})
	} else {
		if err := writeUpstream(firstMessageType, firstClientMessage); err != nil {
			result.Duration = nowFn().Sub(startAt)
			emitRelayTrace(onTrace, RelayTraceEvent{
				Stage:        "write_first_message_failed",
				Direction:    "client_to_upstream",
				MessageType:  relayMessageTypeString(firstMessageType),
				PayloadBytes: len(firstClientMessage),
				Error:        err.Error(),
			})
			return result, &RelayExit{Stage: "write_upstream", Err: err}
		}
		emitRelayTrace(onTrace, RelayTraceEvent{
			Stage:        "write_first_message_ok",
			Direction:    "client_to_upstream",
			MessageType:  relayMessageTypeString(firstMessageType),
			PayloadBytes: len(firstClientMessage),
		})
	}
	// Include the prepared request in frame accounting for either actual or prior
	// transmission; first-write failure returns before this counter changes.
	clientToUpstreamFrames.Add(1)
	markActivity()

	// Reserve one final signal per forwarding direction and watchdog, so workers
	// can finish after the controller stops receiving and waits for the upstream
	// join.
	exitCh := make(chan relayExitSignal, 3)
	dropDownstreamWrites := atomic.Bool{}
	clientReaderStarted := atomic.Bool{}
	writeClientFrameUpstream := func(msgType coderws.MessageType, payload []byte) error {
		if isClientResponseCreateFrame(msgType, payload) {
			turnStartedAt := time.Time{}
			if options.TakeNextTurnStartedAt != nil {
				turnStartedAt = options.TakeNextTurnStartedAt()
			}
			if turnStartedAt.IsZero() {
				turnStartedAt = nowFn()
			}
			// Publish the accepted client start before its upstream write, letting an
			// immediately arriving response bind timing to that request instead of its
			// arrival.
			state.setPendingTurnStartedAt(turnStartedAt)
		}
		return writeUpstream(msgType, payload)
	}
	startClientReader := func() {
		// Immediate startup and repeated downstream-delivery hooks share this latch,
		// preventing them from launching concurrent readers on the same client
		// transport.
		if !clientReaderStarted.CompareAndSwap(false, true) {
			return
		}
		go runClientToUpstream(relayCtx, clientConn, options.ReadClientFrame, writeClientFrameUpstream, markActivity, clientToUpstreamFrames, onTrace, exitCh)
	}
	if !options.StartClientAfterFirstDownstream {
		startClientReader()
	}
	// Signal completion after the upstream worker stops all state observation;
	// Relay uses this separate join to settle usage safely after competing exit
	// signals.
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		runUpstreamToClient(
			relayCtx,
			upstreamConn,
			writeClient,
			startAt,
			nowFn,
			state,
			options.OnUsageParseFailure,
			options.OnUpstreamEvent,
			options.OnTurnComplete,
			options.BeforeWriteClient,
			options.BeforeClientWrite,
			options.AfterClientWrite,
			func(msgType coderws.MessageType, payload []byte) {
				if options.StartClientAfterFirstDownstream {
					startClientReader()
				}
			},
			&dropDownstreamWrites,
			upstreamToClientFrames,
			droppedDownstreamFrames,
			markActivity,
			onTrace,
			exitCh,
		)
	}()
	// The optional watchdog shares the workers' exit channel and cancellation
	// context, allowing inactivity to select shutdown while either frame reader
	// remains blocked.
	go runIdleWatchdog(relayCtx, startAt, nowFn, options.IdleTimeout, &lastActivity, onTrace, exitCh)

	// The first signal selects drain or cancellation while other workers can still
	// progress; close preparation runs before internal cancellation affects
	// transport contexts.
	firstExit := <-exitCh
	// Parent cancellation is a control-plane shutdown. Keep the client connection for the
	// adapter to send its lease or request close code; relayCancel does not cancel the parent.
	if ctx.Err() != nil {
		firstExit.graceful = false
	}
	emitRelayTrace(onTrace, RelayTraceEvent{
		Stage:           "first_exit",
		Direction:       relayDirectionFromStage(firstExit.stage),
		Graceful:        firstExit.graceful,
		WroteDownstream: firstExit.wroteDownstream,
		Error:           relayErrorString(firstExit.err),
	})
	// Let the gateway send its selected client close status before relay
	// cancellation; dependency write cancellation can otherwise close the transport
	// before that frame.
	if options.BeforeRelayCancel != nil {
		options.BeforeRelayCancel(RelayExit{
			Stage:           firstExit.stage,
			Err:             firstExit.err,
			Graceful:        firstExit.graceful,
			WroteDownstream: firstExit.wroteDownstream,
		})
	}
	combinedWroteDownstream := firstExit.wroteDownstream
	secondExit := relayExitSignal{graceful: true}
	hasSecondExit := false

	// Keep upstream observation running after graceful client closure, but publish
	// the drop flag before waiting so late terminal usage can settle without
	// another client delivery attempt.
	if firstExit.stage == "read_client" && firstExit.graceful {
		dropDownstreamWrites.Store(true)
		secondExit, hasSecondExit = waitRelayExit(exitCh, drainTimeout)
	} else {
		// Stop the other direction before transport closure and bounded sibling
		// waiting; discard cleanup close errors because the selected forwarding exit
		// supplies the cause.
		relayCancel()
		_ = upstreamConn.Close()
		if clientReaderStarted.Load() {
			secondExit, hasSecondExit = waitRelayExit(exitCh, 200*time.Millisecond)
		}
	}
	if hasSecondExit {
		combinedWroteDownstream = combinedWroteDownstream || secondExit.wroteDownstream
		emitRelayTrace(onTrace, RelayTraceEvent{
			Stage:           "second_exit",
			Direction:       relayDirectionFromStage(secondExit.stage),
			Graceful:        secondExit.graceful,
			WroteDownstream: secondExit.wroteDownstream,
			Error:           relayErrorString(secondExit.err),
		})
	}

	// Cancel forwarding and close upstream before joining its observer, so no late
	// observation can race final relayState reads or turn callbacks. Cancellation
	// or Close must let ReadFrame return because this join has no separate timeout;
	// cleanup close errors leave the selected exit cause intact.
	relayCancel()
	_ = upstreamConn.Close()
	<-upstreamDone

	// The upstream observer has finished; settle any remaining bare error before
	// copying aggregate results so late failure usage remains available to the
	// gateway.
	emitTurnComplete(options.OnTurnComplete, state, finalizePendingBareError(state, nowFn()))
	enrichResult(&result, state, nowFn().Sub(startAt))
	result.ClientToUpstreamFrames = clientToUpstreamFrames.Load()
	result.UpstreamToClientFrames = upstreamToClientFrames.Load()
	result.DroppedDownstreamFrames = droppedDownstreamFrames.Load()
	// The pre-sent session's client reader has ended; return its drained accounting
	// result through normal completion even when an observed later signal carried
	// an error.
	if options.FirstMessageSent && firstExit.stage == "read_client" && firstExit.graceful {
		emitRelayTrace(onTrace, RelayTraceEvent{
			Stage:           "relay_client_closed",
			Graceful:        true,
			WroteDownstream: combinedWroteDownstream,
		})
		return result, nil
	}
	if firstExit.stage == "read_client" && firstExit.graceful {
		stage := "client_disconnected"
		exitErr := firstExit.err
		if hasSecondExit && !secondExit.graceful {
			stage = secondExit.stage
			exitErr = secondExit.err
		}
		// Use EOF when the selected signal supplies no error, giving the gateway an
		// observable disconnect cause together with the already settled accounting
		// result.
		if exitErr == nil {
			exitErr = io.EOF
		}
		emitRelayTrace(onTrace, RelayTraceEvent{
			Stage:           "relay_exit",
			Direction:       relayDirectionFromStage(stage),
			Graceful:        false,
			WroteDownstream: combinedWroteDownstream,
			Error:           relayErrorString(exitErr),
		})
		return result, &RelayExit{
			Stage:           stage,
			Err:             exitErr,
			WroteDownstream: combinedWroteDownstream,
		}
	}
	if firstExit.graceful && (!hasSecondExit || secondExit.graceful) {
		emitRelayTrace(onTrace, RelayTraceEvent{
			Stage:           "relay_complete",
			Graceful:        true,
			WroteDownstream: combinedWroteDownstream,
		})
		// Release the client after aggregate observations are settled; a cleanup close
		// error does not replace the selected successful relay completion.
		_ = clientConn.Close()
		return result, nil
	}
	if !firstExit.graceful {
		emitRelayTrace(onTrace, RelayTraceEvent{
			Stage:           "relay_exit",
			Direction:       relayDirectionFromStage(firstExit.stage),
			Graceful:        false,
			WroteDownstream: combinedWroteDownstream,
			Error:           relayErrorString(firstExit.err),
		})
		return result, &RelayExit{
			Stage:           firstExit.stage,
			Err:             firstExit.err,
			WroteDownstream: combinedWroteDownstream,
		}
	}
	emitRelayTrace(onTrace, RelayTraceEvent{
		Stage:           "relay_exit",
		Direction:       relayDirectionFromStage(secondExit.stage),
		Graceful:        false,
		WroteDownstream: combinedWroteDownstream,
		Error:           relayErrorString(secondExit.err),
	})
	return result, &RelayExit{
		Stage:           secondExit.stage,
		Err:             secondExit.err,
		WroteDownstream: combinedWroteDownstream,
	}
}

// runClientToUpstream forwards frames in read order and sends one exit signal
// after a read or write failure. A nil reader hook uses clientConn.ReadFrame;
// writeUpstream and markActivity are required. Successful writes update the
// optional counter. The exit channel needs capacity for this worker's final
// signal even after the controller stops receiving.
func runClientToUpstream(
	ctx context.Context,
	clientConn FrameConn,
	readClientFrame func(context.Context, FrameConn) (coderws.MessageType, []byte, error),
	writeUpstream func(msgType coderws.MessageType, payload []byte) error,
	markActivity func(),
	forwardedFrames *atomic.Int64,
	onTrace func(event RelayTraceEvent),
	exitCh chan<- relayExitSignal,
) {
	if readClientFrame == nil {
		readClientFrame = func(ctx context.Context, conn FrameConn) (coderws.MessageType, []byte, error) {
			return conn.ReadFrame(ctx)
		}
	}
	for {
		msgType, payload, err := readClientFrame(ctx, clientConn)
		if err != nil {
			emitRelayTrace(onTrace, RelayTraceEvent{
				Stage:     "read_client_failed",
				Direction: "client_to_upstream",
				Error:     err.Error(),
				Graceful:  isDisconnectError(err),
			})
			// Publish client disconnect classification before returning, allowing the
			// controller to keep upstream observation active during its graceful accounting
			// drain.
			exitCh <- relayExitSignal{stage: "read_client", err: err, graceful: isDisconnectError(err)}
			return
		}
		markActivity()
		if err := writeUpstream(msgType, payload); err != nil {
			emitRelayTrace(onTrace, RelayTraceEvent{
				Stage:        "write_upstream_failed",
				Direction:    "client_to_upstream",
				MessageType:  relayMessageTypeString(msgType),
				PayloadBytes: len(payload),
				Error:        err.Error(),
			})
			// Stop this direction with the failed frame uncounted; the controller can
			// cancel the peer direction using the original upstream write cause.
			exitCh <- relayExitSignal{stage: "write_upstream", err: err}
			return
		}
		// Advance frame accounting only after successful delivery; activity publication
		// then reflects a completed upstream write, not just arrival of its client
		// frame.
		if forwardedFrames != nil {
			forwardedFrames.Add(1)
		}
		markActivity()
	}
}

// runUpstreamToClient observes upstream text frames, settles turns, and
// forwards permitted frames in read order. It owns relayState until Relay joins
// it, and requires read, write, clock, activity, and exit-channel dependencies.
// BeforeWriteClient can reject a frame before any usage or identity
// observation. Settled terminals reach OnTurnComplete after delivery observation
// and before AfterClientWrite releases the gateway's terminal admission lock.
//
// During client-disconnect drain, observation continues while writes are
// dropped, and a settled terminal stops the worker. A bare error remains
// pending across auxiliary frames so a following response.failed can provide
// the turn's final usage. Read termination settles any pending bare error; a
// graceful transport close with an identified active turn becomes a failure.
// WroteDownstream records successful whole-frame writes, including preambles
// and binary frames.
func runUpstreamToClient(
	ctx context.Context,
	upstreamConn FrameConn,
	writeClient func(msgType coderws.MessageType, payload []byte) error,
	startAt time.Time,
	nowFn func() time.Time,
	state *relayState,
	onUsageParseFailure func(eventType string, usageRaw string),
	onUpstreamEvent func(eventType string, payload []byte),
	onTurnComplete func(turn RelayTurnResult),
	beforeWriteClient func(msgType coderws.MessageType, payload []byte, wroteDownstream bool) error,
	beforeClientWrite func(msgType coderws.MessageType, payload []byte),
	afterClientWrite func(msgType coderws.MessageType, payload []byte, writeErr error),
	afterWriteClient func(msgType coderws.MessageType, payload []byte),
	dropDownstreamWrites *atomic.Bool,
	forwardedFrames *atomic.Int64,
	droppedFrames *atomic.Int64,
	markActivity func(),
	onTrace func(event RelayTraceEvent),
	exitCh chan<- relayExitSignal,
) {
	wroteDownstream := false
	for {
		msgType, payload, err := upstreamConn.ReadFrame(ctx)
		if err != nil {
			// Settle a deferred bare error before incomplete-close detection, allowing its
			// matching active timing to retire when the stream ends without a paired failed
			// frame.
			emitTurnComplete(onTurnComplete, state, finalizePendingBareError(state, nowFn()))
			graceful := isDisconnectError(err)
			// A normal WebSocket close completes the transport handshake, not an active Responses turn.
			// A turn with an observed response ID needs a terminal event; otherwise report the
			// disconnect as a relay failure so the adapter can report the unfinished turn.
			if graceful && openAIWSRelayActiveTurnID(state) != "" {
				graceful = false
				err = errors.New("upstream websocket closed before terminal event: " + err.Error())
			}
			emitRelayTrace(onTrace, RelayTraceEvent{
				Stage:           "read_upstream_failed",
				Direction:       "upstream_to_client",
				Error:           err.Error(),
				Graceful:        graceful,
				WroteDownstream: wroteDownstream,
			})
			// Carry the worker's successful delivery evidence after final read settlement,
			// so the controller can retain that evidence together with the upstream
			// failure.
			exitCh <- relayExitSignal{
				stage:           "read_upstream",
				err:             err,
				graceful:        graceful,
				wroteDownstream: wroteDownstream,
			}
			return
		}
		markActivity()
		// Run policy rejection before feeding usage and timing state; rejected bytes
		// cannot create a billed observation or terminal callback in this worker.
		if beforeWriteClient != nil {
			if err := beforeWriteClient(msgType, payload, wroteDownstream); err != nil {
				emitRelayTrace(onTrace, RelayTraceEvent{
					Stage:           "upstream_message_rejected",
					Direction:       "upstream_to_client",
					MessageType:     relayMessageTypeString(msgType),
					PayloadBytes:    len(payload),
					WroteDownstream: wroteDownstream,
					Error:           err.Error(),
				})
				// Reject this frame with prior delivery evidence intact; its bytes have not
				// entered usage or timing observation when the controller receives this policy
				// exit.
				exitCh <- relayExitSignal{
					stage:           "upstream_message",
					err:             err,
					wroteDownstream: wroteDownstream,
				}
				return
			}
		}
		observedEvent := observedUpstreamEvent{}
		switch msgType {
		case coderws.MessageText:
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			// Settle the earlier bare error before parsing a later turn's snapshot,
			// preventing that new usage from becoming the pending error's held counters.
			if shouldFinalizePendingBareError(state, payload, eventType) {
				emitTurnComplete(onTurnComplete, state, finalizePendingBareError(state, nowFn()))
			}
			observedEvent = observeUpstreamMessage(state, payload, startAt, nowFn, onUsageParseFailure, onUpstreamEvent)
		case coderws.MessageBinary:
			// The message type controls observation: binary frames keep their raw transport
			// representation even when their bytes resemble JSON, so response identity and
			// usage remain untouched.
		}
		// Keep drain accounting and terminal callbacks even when client delivery is
		// suppressed; a settled terminal ends draining without waiting for another
		// upstream read.
		if dropDownstreamWrites != nil && dropDownstreamWrites.Load() {
			if observedEvent.performance != nil {
				observedEvent.performance.Disconnected(nowFn())
			}
			emitTurnComplete(onTurnComplete, state, observedEvent)
			if droppedFrames != nil {
				droppedFrames.Add(1)
			}
			emitRelayTrace(onTrace, RelayTraceEvent{
				Stage:           "drop_downstream_frame",
				Direction:       "upstream_to_client",
				MessageType:     relayMessageTypeString(msgType),
				PayloadBytes:    len(payload),
				WroteDownstream: wroteDownstream,
			})
			if observedEvent.terminal {
				// Report a settled terminal as the drain stopping point, allowing the
				// controller to end its wait while retaining dropped-frame accounting and
				// observed usage.
				exitCh <- relayExitSignal{
					stage:           "drain_terminal",
					graceful:        true,
					wroteDownstream: wroteDownstream,
				}
				return
			}
			markActivity()
			continue
		}
		// The gateway brackets terminal delivery with its admission mutex; run the
		// paired after hook on errors too so that write phase can release its lock.
		if beforeClientWrite != nil {
			beforeClientWrite(msgType, payload)
		}
		writeErr := writeClient(msgType, payload)
		// Publish completion while terminal admission remains locked. The next turn
		// cannot overtake its predecessor, and a panicking callback still releases the paired hook.
		func() {
			if afterClientWrite != nil {
				defer afterClientWrite(msgType, payload, writeErr)
			}
			delivery := observedEvent.performance
			if delivery == nil && state.activeTurn != nil {
				delivery = state.activeTurn.performance
			}
			if writeErr == nil {
				delivery.Delivered(string(payload), nowFn())
			} else {
				delivery.Disconnected(nowFn())
			}
			if observedEvent.terminal {
				state.performance = observedEvent.performance.Clone()
			}
			emitTurnComplete(onTurnComplete, state, observedEvent)
		}()
		if writeErr != nil {
			emitRelayTrace(onTrace, RelayTraceEvent{
				Stage:           "write_client_failed",
				Direction:       "upstream_to_client",
				MessageType:     relayMessageTypeString(msgType),
				PayloadBytes:    len(payload),
				WroteDownstream: wroteDownstream,
				Error:           writeErr.Error(),
			})
			// Usage and terminal observation have already occurred when delivery fails;
			// report that failure with evidence from earlier successful frames for gateway
			// presentation.
			exitCh <- relayExitSignal{stage: "write_client", err: writeErr, wroteDownstream: wroteDownstream}
			return
		}
		// Successful delivery of even a structural preamble enables delayed client
		// reading; failed writes never reach the post-write hook or forwarded-frame
		// increment.
		wroteDownstream = true
		if afterWriteClient != nil {
			afterWriteClient(msgType, payload)
		}
		if forwardedFrames != nil {
			forwardedFrames.Add(1)
		}
		markActivity()
	}
}

// runIdleWatchdog sends an idle-timeout exit when elapsed duration since its
// sampled activity offset exceeds the configured budget. Nonpositive budgets
// disable the worker. Checks run at one quarter of the budget, bounded between
// one and five seconds, so short budgets can be detected after their nominal
// deadline.
//
// Selecting ctx.Done ends the worker without a signal. A selected tick can race
// with new activity or cancellation, and the caller supplies startAt, a
// concurrency-safe clock, and enough exit-channel capacity for that decision.
func runIdleWatchdog(
	ctx context.Context,
	startAt time.Time,
	nowFn func() time.Time,
	idleTimeout time.Duration,
	lastActivity *atomic.Int64,
	onTrace func(event RelayTraceEvent),
	exitCh chan<- relayExitSignal,
) {
	if idleTimeout <= 0 {
		return
	}
	// Bound polling between one and five seconds; the lower bound means a short
	// configured idle budget can be detected after its nominal deadline.
	checkInterval := max(minDuration(idleTimeout/4, 5*time.Second), time.Second)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		// The cancellation arm publishes no exit because Relay obtains forwarding exits
		// before joining the upstream observer; a selected tick can still report
		// independently.
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Compare elapsed offsets relative to startAt so Time.Sub retains monotonic
			// clock readings instead of converting through wall-clock Unix nanoseconds.
			elapsedSinceActivity := nowFn().Sub(startAt) - time.Duration(lastActivity.Load())
			if elapsedSinceActivity < idleTimeout {
				continue
			}
			emitRelayTrace(onTrace, RelayTraceEvent{
				Stage:     "idle_timeout_triggered",
				Direction: "watchdog",
				Error:     context.DeadlineExceeded.Error(),
			})
			// Send the sampled timeout even if a concurrent frame just refreshed activity;
			// Relay resolves this signal against the other workers' stopping points.
			exitCh <- relayExitSignal{stage: "idle_timeout", err: context.DeadlineExceeded}
			return
		}
	}
}

// emitRelayTrace synchronously invokes the optional diagnostic callback with
// event. A nil callback is ignored, and callers handle synchronization when
// forwarding workers or the controller emit traces concurrently.
func emitRelayTrace(onTrace func(event RelayTraceEvent), event RelayTraceEvent) {
	if onTrace == nil {
		return
	}
	onTrace(event)
}

// relayMessageTypeString formats text, binary, and numeric unknown frame types
// for traces.
func relayMessageTypeString(msgType coderws.MessageType) string {
	switch msgType {
	case coderws.MessageText:
		return "text"
	case coderws.MessageBinary:
		return "binary"
	default:
		return "unknown(" + strconv.Itoa(int(msgType)) + ")"
	}
}

// relayDirectionFromStage maps forwarding or watchdog exit stages to their
// diagnostic direction. Unrecognized stages return an empty string so lifecycle
// observations can retain a stage without claiming a transport direction.
func relayDirectionFromStage(stage string) string {
	switch stage {
	case "read_client", "write_upstream":
		return "client_to_upstream"
	case "read_upstream", "write_client", "drain_terminal":
		return "upstream_to_client"
	case "idle_timeout":
		return "watchdog"
	default:
		return ""
	}
}

// relayErrorString returns an error's text for diagnostic events. Nil errors
// return an empty string, allowing successful lifecycle observations to use the
// same trace representation as failures.
func relayErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// firstRelayResponseServiceTier returns the first nonblank string tier from
// response.service_tier, then service_tier. Missing, nonstring, and blank
// values are skipped; retained spelling is trimmed but not normalized for
// gateway billing.
func firstRelayResponseServiceTier(message []byte) string {
	if len(message) == 0 {
		return ""
	}
	// Select nested terminal metadata before a usable top-level string; the gateway
	// later normalizes the retained tier for its completion accounting.
	values := gjson.GetManyBytes(message, "response.service_tier", "service_tier")
	for _, value := range values {
		if value.Type != gjson.String {
			continue
		}
		if tier := strings.TrimSpace(value.String()); tier != "" {
			return tier
		}
	}
	return ""
}

// observeRelayTurnResponseServiceTier updates a turn with a trimmed nonblank
// terminal tier. Nil turns and blank tiers leave prior evidence unchanged; the
// upstream observer serializes writes to this timing record.
func observeRelayTurnResponseServiceTier(turn *relayTurnTiming, tier string) {
	if turn == nil {
		return
	}
	if tier = strings.TrimSpace(tier); tier != "" {
		turn.terminalResponseServiceTier = tier
	}
}

// observeUpstreamMessage records an event-bearing text frame and returns its
// available response or terminal observation. Nil state, empty bytes, or a
// blank extracted event type returns an empty observation. The caller
// serializes state access and supplies the clock. The optional event callback
// receives copied bytes before observation; nested response.id and response_id
// identify responses, while a top-level id is accepted only for terminal events
// so ordinary event IDs do not create turns.
//
// Response-ID timing records retain model evidence separately for interleaved
// responses, and terminal service tiers are recorded on the selected timing.
// Token timing uses isTokenEvent's event-name predicate and nonnegative elapsed
// milliseconds. The initial session token branch can also fill the active turn;
// later events carrying response IDs fill their own timing records through
// lookup.
//
// Usage is held for the current turn until a terminal settles it. Bare error
// retains a pending observation and can bind to the active response ID,
// allowing response.failed to replace its provisional usage. Other terminals
// clear that pending error, add the selected turn usage to session totals, and
// retire the corresponding timing record. The request model stays the initial
// frame's model.
func observeUpstreamMessage(
	state *relayState,
	message []byte,
	startAt time.Time,
	nowFn func() time.Time,
	onUsageParseFailure func(eventType string, usageRaw string),
	onUpstreamEvent func(eventType string, payload []byte),
) observedUpstreamEvent {
	if state == nil || len(message) == 0 {
		return observedUpstreamEvent{}
	}
	values := gjson.GetManyBytes(message, "type", "response.id", "response_id", "id")
	eventType := strings.TrimSpace(values[0].String())
	if eventType == "" {
		return observedUpstreamEvent{}
	}
	if onUpstreamEvent != nil {
		// Give the observer its own bytes so retained payloads or callback mutations
		// cannot alter the message used for this worker's identity and usage parsing.
		bodyCopy := append([]byte(nil), message...)
		onUpstreamEvent(eventType, bodyCopy)
	}
	responseID := strings.TrimSpace(values[1].String())
	if responseID == "" {
		responseID = strings.TrimSpace(values[2].String())
	}
	// A top-level ID can identify an ordinary event. Use it only at a terminal, so
	// nonterminal event IDs cannot allocate an unrelated response timing record.
	if responseID == "" && isTerminalEvent(eventType) {
		responseID = strings.TrimSpace(values[3].String())
	}
	now := nowFn()

	parsedUsage := parseUsageAndAccumulate(state, message, eventType, onUsageParseFailure)
	observed := observedUpstreamEvent{
		eventType:  eventType,
		responseID: responseID,
		usage:      parsedUsage,
	}
	var turnTiming *relayTurnTiming
	// Select an identified timing before model observation; ID-less events use the
	// active pointer, while older identified lookups keep that pointer's existing
	// association.
	if responseID != "" {
		turnTiming = openAIWSRelayGetOrInitTurnTiming(state, responseID, now)
	}
	if turnTiming == nil {
		turnTiming = state.activeTurn
		if turnTiming == nil {
			turnTiming = openAIWSRelayGetOrInitTurnTiming(state, "", now)
		}
	}
	if turnTiming != nil {
		turnTiming.modelObserver.ObserveOpenAI(message, eventType)
		turnTiming.performance.ObserveAt(string(message), eventType, now)
	}
	if isTerminalEvent(eventType) {
		observeRelayTurnResponseServiceTier(turnTiming, firstRelayResponseServiceTier(message))
	}
	if !isTerminalEvent(eventType) {
		return observed
	}
	state.terminalEventType = eventType
	if eventType == "error" {
		// A bare error can precede response.failed for the same turn. Hold its
		// settlement while the failed frame can replace provisional usage, keeping the
		// paired terminal observations in one accounting decision.
		if observed.responseID == "" {
			observed.responseID = openAIWSRelayActiveTurnID(state)
		}
		pending := observed
		state.pendingBareError = &pending
		return observed
	}
	// Clear pending error settlement before finalizing this terminal, so EOF or
	// Relay's final join phase cannot later bill that pending observation again.
	state.pendingBareError = nil
	return finalizeObservedRelayTerminal(state, observed, now)
}

// shouldFinalizePendingBareError decides whether a new text event ends the
// pending bare error before that event is observed. The caller serializes state
// and supplies the same payload and event type. A new response.created or
// another terminal settles the error, while error and response.failed leave it
// available for paired failure settlement.
//
// For other events, both the pending response ID and the new nested response.id
// must be present and differ. Auxiliary frames without that identity keep the
// error pending, preventing an intervening rate-limit update from creating a
// second billed completion.
func shouldFinalizePendingBareError(state *relayState, payload []byte, eventType string) bool {
	if state == nil || state.pendingBareError == nil {
		return false
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || eventType == "error" || eventType == "response.failed" {
		return false
	}
	// A new turn or another terminal closes the old error before current usage is
	// parsed; response.failed stays in the paired-error path above.
	if isTerminalEvent(eventType) || eventType == "response.created" {
		return true
	}
	// Auxiliary traffic can arrive between an error and its paired failed terminal.
	// This path requires a different nested response.id and ignores top-level
	// response_id, so identity-free auxiliary frames keep settlement pending.
	responseID := strings.TrimSpace(gjson.GetBytes(payload, "response.id").String())
	if responseID == "" || state.pendingBareError.responseID == "" {
		return false
	}
	return responseID != state.pendingBareError.responseID
}

// finalizePendingBareError removes the pending error and delegates its terminal
// settlement at now. Nil state or an absent error returns an empty observation;
// callers serialize it with upstream observation or invoke it after joining
// that reader.
func finalizePendingBareError(state *relayState, now time.Time) observedUpstreamEvent {
	if state == nil || state.pendingBareError == nil {
		return observedUpstreamEvent{}
	}
	// Consume the pending pointer before delegation, so reader EOF and Relay's
	// later joined settlement cannot finalize the same held observation twice.
	observed := *state.pendingBareError
	state.pendingBareError = nil
	return finalizeObservedRelayTerminal(state, observed, now)
}

// finalizeObservedRelayTerminal settles the held turn usage and associates
// timing metadata with the terminal response ID. Nil state or a blank event
// type returns an empty observation. Identified terminals update the retained
// response ID and retire its timing record, capturing that record's model,
// tier, start, duration, and performance observer for final delivery capture.
//
// A terminal with no response ID consumes the pending start and discards the
// active timing while retaining previously stored aggregate identity metadata.
// Timing durations are clamped at zero for backward clock observations. The
// caller owns serial state access, and terminal settlement resets turn usage
// before another turn can accumulate snapshots.
func finalizeObservedRelayTerminal(state *relayState, observed observedUpstreamEvent, now time.Time) observedUpstreamEvent {
	if state == nil || strings.TrimSpace(observed.eventType) == "" {
		return observedUpstreamEvent{}
	}
	// Move the selected snapshot into session totals before retiring timing,
	// leaving subsequent progress observation an empty held usage value for the
	// next settlement.
	observed.usage = finalizeRelayTurnUsage(state)
	observed.terminal = true
	responseID := strings.TrimSpace(observed.responseID)
	if responseID != "" {
		state.lastResponseID = responseID
		if turnTiming, ok := openAIWSRelayDeleteTurnTiming(state, responseID); ok {
			observed.responseModel = turnTiming.modelObserver.Model()
			state.lastResponseModel = observed.responseModel
			observed.responseServiceTier = turnTiming.terminalResponseServiceTier
			state.lastResponseServiceTier = observed.responseServiceTier
			observed.startedAt = turnTiming.startAt
			// Clamp backward-clock turn duration to zero elapsed time.
			duration := max(now.Sub(turnTiming.startAt), 0)
			observed.duration = duration
			if turnTiming.performance != nil {
				turnTiming.performance.FinishAt(now)
				observed.performance = turnTiming.performance
				state.performance = observed.performance.Clone()
			}
		}
	} else {
		// Without a terminal ID, discard pending timing association and the active
		// record together; previously retained aggregate identity remains available in
		// session results.
		state.consumePendingTurnStartedAt()
		openAIWSRelayDiscardActiveTurnTiming(state)
	}
	return observed
}

// emitTurnComplete delivers a settled terminal's usage and timing to the
// optional callback. Nonterminal observations and non-error terminals with no
// response ID are ignored; bare error can complete with an empty ID so its
// usage remains observable before a later successful turn.
//
// The request model comes from the initial relay state, with an empty model
// when state is nil; response model and tier come from the settled observation.
// Performance storage is copied for the callback after terminal delivery
// observation, before the paired hook releases turn admission. Deferred
// errors can complete before a later event, at read termination, or after
// reader join, when the original error frame may already have been written,
// dropped, or failed during a write attempt.
func emitTurnComplete(
	onTurnComplete func(turn RelayTurnResult),
	state *relayState,
	observed observedUpstreamEvent,
) {
	if onTurnComplete == nil || !observed.terminal {
		return
	}
	responseID := strings.TrimSpace(observed.responseID)
	// An ID-less bare error still needs completion accounting; other terminal
	// frames require a response ID to associate their callback result with that
	// response.
	if responseID == "" && strings.TrimSpace(observed.eventType) != "error" {
		return
	}
	requestModel := ""
	if state != nil {
		requestModel = state.requestModel
	}
	onTurnComplete(RelayTurnResult{
		RequestModel:          requestModel,
		ResponseServiceTier:   observed.responseServiceTier,
		UpstreamResponseModel: observed.responseModel,
		Usage:                 observed.usage,
		RequestID:             responseID,
		TerminalEventType:     observed.eventType,
		StartedAt:             observed.startedAt,
		Duration:              observed.duration,
		Timings:               observed.performance.Clone(),
	})
}

// isClientResponseCreateFrame recognizes response.create in text or binary
// frame payloads using a trimmed type field. Other frame types return false,
// and absent or different types leave pending turn-start transfer unchanged.
func isClientResponseCreateFrame(msgType coderws.MessageType, payload []byte) bool {
	if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
		return false
	}
	return strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.create"
}

// openAIWSRelayGetOrInitTurnTiming returns the timing record for responseID,
// creating it when absent or missing a start. Nil state returns nil. Creation
// consumes the one pending client start, uses now when that start is zero, and
// publishes the same pointer as the map entry and active turn.
//
// An existing usable record retains its initial start and does not replace the
// active pointer. This keeps interleaved response lookups from moving the
// current active turn merely because an older response appears again. The
// upstream observer serializes map access.
func openAIWSRelayGetOrInitTurnTiming(state *relayState, responseID string, now time.Time) *relayTurnTiming {
	if state == nil {
		return nil
	}
	if state.turnTimingByID == nil {
		// Eight entries are initial capacity; observed response IDs can grow this map.
		state.turnTimingByID = make(map[string]*relayTurnTiming, 8)
	}
	if responseID != "" && state.turnTimingByID[responseID] == nil && state.turnTimingByID[""] != nil {
		// An ID-less delta belongs to the pending turn. Bind its existing observations
		// when the first response identifier arrives, rather than discarding that content.
		state.turnTimingByID[responseID] = state.turnTimingByID[""]
		delete(state.turnTimingByID, "")
	}
	entry, ok := state.turnTimingByID[responseID]
	// Rebuild a missing or zero-start record before making it active; this can
	// replace earlier incomplete record metadata with the newly selected client
	// start.
	if !ok || entry == nil || entry.startAt.IsZero() {
		startAt := state.consumePendingTurnStartedAt()
		if startAt.IsZero() {
			startAt = now
		}
		entry = &relayTurnTiming{startAt: startAt}
		mode, upstreamStart := "first_output", int64(0)
		if state.consumedStart != nil {
			mode = state.consumedStart.mode
			upstreamStart = max(state.consumedStart.upstream.Sub(startAt).Milliseconds(), 0)
		}
		entry.performance = timing.NewPerformanceAt(mode, startAt, startAt.Add(time.Duration(upstreamStart)*time.Millisecond))
		if state.consumedStart == nil {
			delete(entry.performance.Values, "upstream_started_ms")
		}
		if state.consumedStart != nil && state.consumedStart.performance != nil {
			entry.performance = state.consumedStart.performance
		}
		state.turnTimingByID[responseID] = entry
		state.activeTurn = entry
		return entry
	}
	return entry
}

// setPendingTurnStartedAt publishes a copied nonzero start time for the next
// timing-record creation. Nil state and zero times are ignored; later stores
// replace the single pending value, so request pacing supplies the intended
// association.
func (s *relayState) setPendingTurnStartedAt(startedAt time.Time) {
	if s == nil || startedAt.IsZero() {
		return
	}
	// Copy the supplied time before atomic publication, leaving readers stable
	// value storage while later client requests can replace the single pending
	// pointer.
	value := &relayPerformanceStart{at: startedAt, upstream: startedAt, mode: "first_output"}
	if s.clock != nil {
		value.upstream = s.clock()
	}
	if s.modeReader != nil {
		value.mode = s.modeReader()
	}
	s.dispatchedTurns++
	if s.beginPerformance != nil {
		value.performance = s.beginPerformance(s.dispatchedTurns, value.upstream)
	}
	if value.performance == nil {
		value.performance = timing.NewPerformanceAt(value.mode, value.at, value.upstream)
	}
	s.pendingTurnStart.Store(value)
}

// consumePendingTurnStartedAt atomically takes and clears the pending client
// start time. Nil state or an empty slot returns zero, allowing timing-record
// creation to select its observation time as the start.
func (s *relayState) consumePendingTurnStartedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	// Take and clear the timestamp in one operation so one consumer obtains it; a
	// later publication remains available for a subsequent timing creation.
	startedAt := s.pendingTurnStart.Swap(nil)
	if startedAt == nil {
		return time.Time{}
	}
	s.consumedStart = startedAt
	return startedAt.at
}

// openAIWSRelayDeleteTurnTiming removes responseID and returns its timing value
// when a nonnil record exists. Missing state, map, or record returns false. The
// upstream observer serializes this mutation, and the returned model observer
// and performance pointer retain the record's observations for terminal settlement.
//
// The active pointer is cleared only when it identifies the removed record.
// Retiring an earlier interleaved response therefore leaves another active
// response available for ID-less events and incomplete-close detection.
func openAIWSRelayDeleteTurnTiming(state *relayState, responseID string) (relayTurnTiming, bool) {
	if state == nil || state.turnTimingByID == nil {
		return relayTurnTiming{}, false
	}
	timing, ok := state.turnTimingByID[responseID]
	if !ok || timing == nil {
		return relayTurnTiming{}, false
	}
	// Retire the key before clearing an equal active pointer; another active
	// response stays available for ID-less event handling and unfinished-close
	// detection.
	delete(state.turnTimingByID, responseID)
	if state.activeTurn == timing {
		state.activeTurn = nil
	}
	return *timing, true
}

// openAIWSRelayDiscardActiveTurnTiming retires every map entry pointing to the
// active timing and then clears that pointer. Nil state or an absent active
// record is ignored. The caller serializes this operation with event
// observation.
//
// A terminal lacking a response ID uses this cleanup because direct ID lookup
// cannot select the active record. Other timing records survive, preserving
// their response-specific model and start evidence until their own identified
// terminals arrive.
func openAIWSRelayDiscardActiveTurnTiming(state *relayState) {
	if state == nil || state.activeTurn == nil {
		return
	}
	// Retire entries by pointer because this terminal has no response key;
	// unrelated response timings stay indexed for their later identified
	// settlement.
	active := state.activeTurn
	for responseID, timing := range state.turnTimingByID {
		if timing == active {
			delete(state.turnTimingByID, responseID)
		}
	}
	state.activeTurn = nil
}

// openAIWSRelayActiveTurnID finds the map key whose timing pointer is active.
// Missing state, active timing, or matching entry returns empty; callers
// serialize lookup with map mutation and use the result for unfinished-turn
// close handling.
func openAIWSRelayActiveTurnID(state *relayState) string {
	if state == nil || state.activeTurn == nil {
		return ""
	}
	for responseID, timing := range state.turnTimingByID {
		if timing == state.activeTurn {
			return responseID
		}
	}
	return ""
}

// parseUsageAndAccumulate parses eligible usage snapshots into the held turn
// state and returns parsed terminal usage. The caller serializes state. Nested
// response.usage takes precedence whenever present, and missing native input,
// output, or cached fields can use Chat aliases. Input and output are required
// at terminals; those fields and cached tokens require numeric JSON types,
// while optional image, reasoning, and cache-creation counters use their
// separate coercion rules.
//
// Rejected snapshots report a diagnostic failure and leave the held turn value
// unchanged. Nonterminal snapshots overwrite only positive fields, retaining
// earlier evidence for missing or zero counters. A terminal with any positive
// counter replaces the held snapshot as a whole, including its zero fields; a
// terminal with no positive counter retains a previously positive snapshot. The
// returned parsed terminal value can therefore differ from the usage later
// settled for that turn.
//
// Positive reasoning is added only within the total-token gap through
// IncludeIndependentReasoningTokens. This parser checks numeric type rather
// than nonnegative integral bounds; parseUsageIntField performs gjson's integer
// conversion and then converts to Go int. Session totals change later through
// finalizeRelayTurnUsage.
func parseUsageAndAccumulate(
	state *relayState,
	message []byte,
	eventType string,
	onParseFailure func(eventType string, usageRaw string),
) Usage {
	if state == nil || len(message) == 0 || !shouldParseUsage(eventType) || !bytes.Contains(message, []byte(`"usage"`)) {
		return Usage{}
	}
	// Prefer an existing nested usage snapshot even when malformed; top-level usage
	// is consulted only when absent, keeping snapshot selection independent of
	// validation.
	usageResult := gjson.GetBytes(message, "response.usage")
	if !usageResult.Exists() {
		usageResult = gjson.GetBytes(message, "usage")
	}
	if !usageResult.Exists() {
		return Usage{}
	}
	usageRaw := strings.TrimSpace(usageResult.Raw)
	// A non-object usage value leaves the held snapshot intact; report parsing
	// failure while the frame-forwarding path can still deliver the terminal event.
	if usageRaw == "" || !strings.HasPrefix(usageRaw, "{") {
		recordUsageParseFailure()
		if onParseFailure != nil {
			onParseFailure(eventType, usageRaw)
		}
		return Usage{}
	}

	// Use Chat aliases when native counters are absent; present native zeros or
	// invalid types retain precedence over their alias within this snapshot.
	inputResult := usageResult.Get("input_tokens")
	if !inputResult.Exists() {
		inputResult = usageResult.Get("prompt_tokens")
	}
	outputResult := usageResult.Get("output_tokens")
	if !outputResult.Exists() {
		outputResult = usageResult.Get("completion_tokens")
	}
	cachedResult := usageResult.Get("input_tokens_details.cached_tokens")
	if !cachedResult.Exists() {
		cachedResult = usageResult.Get("prompt_tokens_details.cached_tokens")
	}
	// Image details use zero as alias fallback, so a present zero or a conversion
	// yielding zero can expose Chat's image counter.
	imageTokens := usageResult.Get("output_tokens_details.image_tokens").Int()
	if imageTokens == 0 {
		imageTokens = usageResult.Get("completion_tokens_details.image_tokens").Int()
	}

	requireTotals := isTerminalEvent(strings.TrimSpace(eventType)) && state.turnUsage.CacheWriteDetails == nil
	inputTokens, inputOK := parseUsageIntField(inputResult, requireTotals)
	outputTokens, outputOK := parseUsageIntField(outputResult, requireTotals)
	cachedTokens, cachedOK := parseUsageIntField(cachedResult, false)
	// Reject the snapshot as a unit when a validated field fails; previously held
	// usage survives and partially parsed counters cannot reach its accounting
	// state.
	if !inputOK || !outputOK || !cachedOK {
		recordUsageParseFailure()
		if onParseFailure != nil {
			onParseFailure(eventType, usageRaw)
		}
		return Usage{}
	}
	// Classify complete zero observations before filling omitted counters. The
	// WebSocket relay keeps its positive prior usage intact for terminal placeholders.
	previousDetails := state.turnUsage.CacheWriteDetails
	if previousDetails != nil && inputTokens == 0 && outputTokens == 0 {
		rawUsage, _ := openai.OpenAIUsageFromGJSON(usageResult)
		if openai.IsExplicitZeroOpenAIUsage(usageResult, rawUsage) {
			previousDetails = nil
		}
	}
	// A timed cache split can precede the terminal usage frame. Keep the earlier
	// same-turn totals where the terminal frame only supplies a subset of counters.
	if previousDetails != nil {
		if !inputResult.Exists() {
			inputTokens = state.turnUsage.InputTokens
		}
		if !outputResult.Exists() {
			outputTokens = state.turnUsage.OutputTokens
		}
		if !cachedResult.Exists() {
			cachedTokens = state.turnUsage.CacheReadInputTokens
		}
	}
	// Select reasoning through the zero-valued details alias rule, then let the
	// shared total-gap helper cap its addition so inclusive output does not receive
	// those tokens again.
	reasoningTokens := usageResult.Get("output_tokens_details.reasoning_tokens").Int()
	if reasoningTokens == 0 {
		reasoningTokens = usageResult.Get("completion_tokens_details.reasoning_tokens").Int()
	}
	if reasoningTokens > 0 {
		outputTokens = int(protocol.IncludeIndependentReasoningTokens(
			int64(inputTokens), int64(outputTokens), usageResult.Get("total_tokens").Int(), reasoningTokens,
		))
	}
	cacheWrites, cacheWriteDetails := openai.ObserveCacheWriteDetails(usageResult, state.turnUsage.CacheCreationInputTokens, previousDetails)
	parsedUsage := Usage{
		CacheWriteDetails:        cacheWriteDetails,
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheWrites,
		CacheReadInputTokens:     cachedTokens,
		ImageOutputTokens:        int(imageTokens),
	}

	// Terminal selection uses one whole snapshot so zero counterpart counters can
	// replace earlier progress values; a snapshot with no positive counter retains
	// prior positive evidence.
	if isTerminalEvent(strings.TrimSpace(eventType)) {
		if relayUsageHasTokens(parsedUsage) || !relayUsageHasTokens(state.turnUsage) {
			state.turnUsage = parsedUsage
		}
	} else {
		if previousDetails != nil && cacheWrites == 0 && cacheWriteDetails == nil {
			// An explicit zero aggregate invalidates an earlier classified write
			// even though the ordinary progress merge only accepts positive counts.
			state.turnUsage.CacheCreationInputTokens = 0
			state.turnUsage.CacheWriteDetails = nil
		}
		mergeRelayUsageNonZero(&state.turnUsage, parsedUsage)
		return Usage{}
	}
	return parsedUsage
}

// relayUsageHasTokens reports whether any of the five usage counters or nested
// cache-write lifetime details is positive.
func relayUsageHasTokens(usage Usage) bool {
	return usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheWriteDetails.HasTokens() ||
		usage.CacheReadInputTokens > 0 || usage.ImageOutputTokens > 0
}

// mergeRelayUsageNonZero merges positive source counters into the destination
// snapshot, overwriting those fields rather than adding them. Nil destinations
// are ignored. Zero and negative source fields retain destination evidence, so
// the result can combine counters observed in different progress events.
//
// The caller serializes updates to this held snapshot. Terminal selection
// happens separately in parseUsageAndAccumulate, where a terminal with a
// positive counter can replace all fields, including zeros that this helper
// would retain.
func mergeRelayUsageNonZero(dst *Usage, src Usage) {
	if dst == nil {
		return
	}
	// Progress fields can arrive in separate events; positive replacements assemble
	// their latest available evidence without treating individual snapshots as
	// additive token deltas.
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens > 0 || src.CacheWriteDetails != nil {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
		dst.CacheWriteDetails = src.CacheWriteDetails
	}
	if src.CacheReadInputTokens > 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.ImageOutputTokens > 0 {
		dst.ImageOutputTokens = src.ImageOutputTokens
	}
}

// finalizeRelayTurnUsage moves the held snapshot into cumulative session totals
// and returns that turn value. Nil state returns zero usage. The upstream
// observer or joined controller performs this operation serially, adding each
// counter and clearing the held value before later observations.
//
// A repeated call with no intervening snapshot returns zero and adds no tokens.
// Bare error settlement uses this reset to leave the following turn's usage
// separate; identified terminal events remain responsible for selecting which
// snapshot is held before finalization.
func finalizeRelayTurnUsage(state *relayState) Usage {
	if state == nil {
		return Usage{}
	}
	// Keep the held snapshot as a value while all five totals change and then reset
	// it, letting later progress begin a separate accounting settlement.
	turnUsage := state.turnUsage
	state.usage.InputTokens += turnUsage.InputTokens
	state.usage.OutputTokens += turnUsage.OutputTokens
	priorTotal := state.usage.CacheCreationInputTokens
	priorDetails := state.usage.CacheWriteDetails
	state.usage.CacheWriteDetails = openai.AddCacheWriteDetails(priorTotal, priorDetails, turnUsage.CacheCreationInputTokens, turnUsage.CacheWriteDetails)
	if (priorTotal != 0 || priorDetails != nil) && (turnUsage.CacheCreationInputTokens != 0 || turnUsage.CacheWriteDetails != nil) {
		state.usage.CacheCreationInputTokens = openai.EffectiveCacheWriteTokens(priorTotal, priorDetails) +
			openai.EffectiveCacheWriteTokens(turnUsage.CacheCreationInputTokens, turnUsage.CacheWriteDetails)
	} else {
		state.usage.CacheCreationInputTokens += turnUsage.CacheCreationInputTokens
	}
	state.usage.CacheReadInputTokens += turnUsage.CacheReadInputTokens
	state.usage.ImageOutputTokens += turnUsage.ImageOutputTokens
	state.turnUsage = Usage{}
	return turnUsage
}

// parseUsageIntField accepts a numeric JSON value and converts gjson's integer
// result to Go int. An absent optional field returns zero with success; absent
// required fields or nonnumeric values return failure. Sign, fraction, and
// Go-int range are not validated here.
func parseUsageIntField(value gjson.Result, required bool) (int, bool) {
	if !value.Exists() {
		return 0, !required
	}
	if value.Type != gjson.Number {
		return 0, false
	}
	// gjson truncates an ordinary numeric fraction during integer conversion; raw
	// large integer parsing and the following Go-int conversion have their own
	// range behavior.
	return int(value.Int()), true
}

// enrichResult fills aggregate duration and the state values retained after
// settlement. Nil results are ignored; nil state still updates duration.
// Timings receives a cloned performance snapshot, separating its maps from the
// relay state before the caller retains the aggregate result.
func enrichResult(result *RelayResult, state *relayState, duration time.Duration) {
	if result == nil {
		return
	}
	result.Duration = duration
	if state == nil {
		return
	}
	result.RequestModel = state.requestModel
	result.ResponseServiceTier = state.lastResponseServiceTier
	result.UpstreamResponseModel = state.lastResponseModel
	result.Usage = state.usage
	result.RequestID = state.lastResponseID
	result.TerminalEventType = state.terminalEventType
	// Copy the latest settled exchange so aggregate consumers can retain its maps
	// independently of relay state and previously delivered turn callbacks.
	result.Timings = state.performance.Clone()
}

// isDisconnectError recognizes EOF, closed transports, cancellation, selected
// WebSocket close statuses, and common disconnect text. It returns false for
// nil or unrelated errors; Relay separately treats parent cancellation and an
// identified unfinished upstream turn as failures.
func isDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return true
	}
	// These transport close statuses qualify for disconnect handling; the upstream
	// worker separately rejects closure when an identified response still lacks a
	// settled terminal.
	switch coderws.CloseStatus(err) {
	case coderws.StatusNormalClosure, coderws.StatusGoingAway, coderws.StatusNoStatusRcvd, coderws.StatusAbnormalClosure:
		return true
	}
	// Recognize common transport wrappers by their text when structured error
	// identity is unavailable, letting the relay apply disconnect handling to those
	// read failures.
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	return strings.Contains(message, "failed to read frame header: eof") ||
		strings.Contains(message, "unexpected eof") ||
		strings.Contains(message, "use of closed network connection") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "broken pipe")
}

// isTerminalEvent recognizes trimmed completion, failure, incomplete,
// cancellation, and bare error event names. Bare error classification permits
// terminal usage parsing, while its actual turn settlement can be deferred.
func isTerminalEvent(eventType string) bool {
	// Treat both cancellation spellings as terminal alongside failure and
	// completion, so their observed usage follows the same terminal selection path.
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
		return true
	default:
		return false
	}
}

// shouldParseUsage accepts trimmed terminals and other response events except
// names ending in .delta. Empty and unrelated names return false, so
// usage-bearing progress frames can contribute the held turn snapshot.
func shouldParseUsage(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "error" || isTerminalEvent(eventType) {
		return true
	}
	// Usage and timing use separate event predicates; progress snapshots can hold
	// accounting counters while token-delta events stay outside usage parsing.
	return strings.HasPrefix(eventType, "response.") && !strings.HasSuffix(eventType, ".delta")
}

// isTokenEvent recognizes any trimmed event name ending in .delta plus
// response.output_text.done and response.function_call_arguments.done. The
// predicate examines the event name alone; payload content and token counts are
// not validated for timing here.
func isTokenEvent(eventType string) bool {
	// Event-name timing admits audio, reasoning, and argument deltas through their
	// suffix; selected done names also record output timing when no earlier delta
	// was observed.
	eventType = strings.TrimSpace(eventType)
	return strings.HasSuffix(eventType, ".delta") ||
		eventType == "response.output_text.done" ||
		eventType == "response.function_call_arguments.done"
}

// minDuration returns the smaller positive candidate when both durations are
// positive. A nonpositive first duration selects the second, and a nonpositive
// second selects the first, including the supplied value when both are
// nonpositive.
func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// waitRelayExit waits for one exit signal within timeout, using two hundred
// milliseconds for a nonpositive budget. Timeout returns false; receiving from
// a closed channel returns a zero signal with true, so Relay keeps its exit
// channel open.
func waitRelayExit(exitCh <-chan relayExitSignal, timeout time.Duration) (relayExitSignal, bool) {
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	// Expiry lets Relay advance to cancellation and upstream joining; this bounded
	// signal wait alone does not authorize reading the observer's mutable state.
	select {
	case sig := <-exitCh:
		return sig, true
	case <-time.After(timeout):
		return relayExitSignal{}, false
	}
}
