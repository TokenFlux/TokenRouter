package ws

import (
	"context"
	"encoding/json"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// stream_contract.go defines observations and provider operations required to
// relay one pooled WebSocket turn.
// A pooled WS turn observes upstream model, tier, usage, risk, and image data
// before preparing client output. Stream interfaces separate those observations
// and provider-failure actions from client metadata restoration, letting
// RelayTurn decide when a failure can escape before output or must finish the
// active stream.

// StreamLease performs bounded request and event I/O on an acquired upstream
// connection.
type StreamLease interface {
	// ConnLease supplies pool identity, health marking, idle checks, and release ownership.
	ConnLease
	// WriteRequest sends one normalized request with the supplied timeout and
	// control context. RelayTurn wraps failures as write_upstream before any
	// downstream output; the session retains ownership of this lease for cleanup.
	WriteRequest(context.Context, []byte, time.Duration) error
	// ReadEvent waits for one upstream event within the supplied timeout and
	// control context. A failure makes RelayTurn mark the connection broken, then
	// returns read_upstream with its current downstream-write state.
	ReadEvent(context.Context, time.Duration) ([]byte, error)
	// Headers returns handshake response headers associated with the acquired
	// connection. RelayTurn supplies them to error policies and completion
	// results; consumers treat the shared map and nested values as immutable.
	Headers() map[string][]string
}

// ErrorPolicy supplies client presentation and retry choices selected from
// provider failure rules.
type ErrorPolicy struct {
	// Generic, Failover, and RetrySame describe separate decisions. RelayTurn
	// checks Generic before Failover for first-turn pre-output errors; RetrySame
	// is attached to an emitted provider failure marker.
	Generic   bool // generic client failure presentation
	Failover  bool // replacement provider failure permitted
	RetrySame bool // same provider retry permitted
}

// TerminalPolicy combines the normalized terminal label with status and
// provider action choices.
type TerminalPolicy struct {
	TerminalEvent string      // normalized terminal event label
	StatusCode    int         // classified terminal HTTP status
	Decision      ErrorPolicy // selected provider error choices
}

// StreamOptions supplies the current provider identity and bounded single-turn
// I/O settings.
type StreamOptions struct {
	ProviderID   int64         // selected upstream provider identifier
	WriteTimeout time.Duration // request write operation budget
	ReadTimeout  time.Duration // per-event read operation budget
	// PreviousRecovery permits suppressing an eligible previous_response_not_found
	// error before output. The payload needs a continuation and no tool outputs;
	// IngressSession applies the final replay budget and retained-history checks.
	PreviousRecovery bool // missing continuation recovery permitted
	Debug            bool // detailed turn diagnostics enabled
}

// StreamHooks observes upstream warnings while RelayTurn retains current
// request and usage data.
type StreamHooks struct {
	// OnUpstreamError is optional and receives the turn number, client model,
	// status, response bytes, and message before client rewriting alters that
	// warning payload.
	OnUpstreamError func(int, string, int, []byte, string) // upstream risk warning observer
}

// ImageCounter collects generated image counts and sizes from upstream response
// events.
type ImageCounter interface {
	// AddSSEData observes one response event for generated image output
	// accounting.
	AddSSEData([]byte)
	// Count returns the generated image count observed for the current turn.
	Count() int
	// FrameCount returns observed frame units, or zero for exclusively static outputs.
	FrameCount() int
	// Sizes returns the observed output image size labels for turn accounting.
	Sizes() []string
}

// ReplayCollector retains output items that ingress can append to future replay
// history.
type ReplayCollector interface {
	// AddEvent observes a response label and payload for retained replay output
	// items.
	AddEvent(string, []byte)
	// Items returns the collected output items for future conversation replay.
	// Ingress may share their bytes with prior request history, so callers retain
	// immutable item storage while that history is in use.
	Items() []json.RawMessage
}

// StreamPort supplies protocol observation, provider failure handling, and
// client output for one turn.
type StreamPort interface {
	// BeginObservation initializes request-local upstream model and service-tier
	// observation for this turn.
	BeginObservation()
	// ObserveModel records upstream model and tier declarations before client
	// metadata restoration.
	ObserveModel([]byte, string)
	// ResponseTier returns the selected upstream-declared tier observed for this
	// turn.
	ResponseTier() string
	// ResolvedTier combines response observation with the accepted request tier
	// for accounting. The HTTP adapter uses its per-turn observer and shared
	// request extractor, returning an optional resolved tier for completion
	// capture.
	ResolvedTier([]byte) *string
	// Reasoning resolves recorded effort from accepted bytes and provider model
	// information. The result can be nil; the requested client effort remains
	// separate in ClientPayload for completion to inspect before-policy intent.
	Reasoning([]byte, string, string) *string
	// ImageCounter constructs an independent image-output collector for the
	// current turn.
	ImageCounter() ImageCounter
	// ReplayCollector constructs an independent response-output history collector
	// for this turn.
	ReplayCollector() ReplayCollector
	// Streaming reports the client stream preference encoded in the request bytes.
	// The OpenAI adapter defaults an absent field to true; this value is recorded
	// even though the transport remains WebSocket.
	Streaming([]byte) bool
	// StoreDisabled resolves the current provider and request store-disabled
	// continuation mode.
	StoreDisabled([]byte) bool
	// ClassifyPrevious identifies continuation ID spelling for diagnostics and
	// recovery checks.
	ClassifyPrevious(string) string
	// HasToolOutput reports whether the normalized request contains protocol
	// tool-output items.
	HasToolOutput([]byte) bool
	// MappedModel resolves the normalized provider model used for client
	// restoration and result metadata.
	MappedModel(string) string
	// NormalizeCompleted repairs compatible terminal event forms and reports
	// whether payload bytes changed. RelayTurn uses the repaired envelope for
	// subsequent observation, policy, output, and completion; unchanged bytes can
	// remain shared.
	NormalizeCompleted([]byte) ([]byte, bool)
	// Envelope extracts event type and response identity for the upstream frame.
	// Its auxiliary boolean is adapter-specific and unused by RelayTurn; callers
	// do not treat envelope extraction alone as full document validation.
	Envelope([]byte) (string, string, bool)
	// ShouldParseUsage identifies labels whose frames can update the current usage
	// observation.
	ShouldParseUsage(string) bool
	// ParseUsage updates the supplied turn usage from a selected response frame.
	// The protocol adapter can replace terminal counts or merge positive interim
	// buckets, so RelayTurn retains one mutable observation across all turn
	// events.
	ParseUsage([]byte, *wire.ForwardUsage)
	// MarkCyber records upstream risk evidence using the current payload and
	// observed turn usage.
	MarkCyber([]byte, *wire.ForwardUsage)
	// SchedulingModel derives the canonical model used by provider health and
	// retry policies.
	SchedulingModel(string) string
	// ErrorDecision classifies a bare error event and applies its selected
	// provider failure handling. It returns generic presentation, failover, and
	// same-provider retry choices; RelayTurn separately restricts escape to
	// first-turn pre-output conditions.
	ErrorDecision(context.Context, string, map[string][]string, []byte) ErrorPolicy
	// TerminalDecision classifies a terminal event with provider status and action
	// choices. RelayTurn uses the normalized terminal label for scheduling
	// completion, and separately chooses pre-output failure escape or an in-stream
	// generic error presentation.
	TerminalDecision(context.Context, string, map[string][]string, []byte) TerminalPolicy
	// ErrorFields extracts the raw upstream error code, type, and message strings.
	ErrorFields([]byte) (string, string, string)
	// ErrorStatus translates the current protocol error event into an HTTP-style
	// status.
	ErrorStatus([]byte) int
	// ClassifyError selects a recovery reason from upstream code, type, and
	// message strings.
	ClassifyError(string, string, string) (string, bool)
	// EncryptedDigests identifies ciphertext lineage present in the current
	// request payload.
	EncryptedDigests([]byte) []string
	// MarkEncrypted retains rejected ciphertext digests for the scoped session
	// before later-turn normalization.
	MarkEncrypted([]string)
	// SummarizeError normalizes error fields for bounded ingress diagnostic
	// labels.
	SummarizeError(string, string, string) (string, string, string)
	// GenericError constructs a provider-rule generic rejection retaining its
	// internal upstream status.
	GenericError(int) error
	// RawFailure constructs a replacement-provider marker from raw error status,
	// headers, body, and retry choice. The HTTP adapter copies response data;
	// RelayTurn marks the current lease broken before returning this failure.
	RawFailure(int, map[string][]string, []byte, bool) error
	// Failure constructs the selected upstream failure marker with its message and
	// retry choice. RelayTurn returns it only for first-turn pre-output failover;
	// later events remain inside the current output handling path.
	Failure(int, map[string][]string, []byte, string, bool) error
	// Warning extracts optional risk evidence from error or failed events. Nil
	// suppresses the warning callback; events that reach this observation stage
	// report evidence before client rewriting, while earlier policy returns can
	// skip this callback.
	Warning(string, []byte) *forwardcore.UpstreamWarning
	// IsToken classifies an event label for pooled-turn TTFT observation. The
	// OpenAI adapter uses IsWSTokenEvent, including empty delta labels; RelayTurn
	// performs no further content check before recording elapsed milliseconds.
	IsToken(string) bool
	// IsTerminal reports whether the event label ends the current pooled response
	// turn.
	IsTerminal(string) bool
	// NormalizeTerminal maps a terminal event label into the completion
	// observation vocabulary.
	NormalizeTerminal(string) string
	// Message extracts the upstream error text used by a terminal failure marker.
	Message([]byte) string
	// GenericEvent renders the fixed client error event for in-stream generic
	// failure presentation.
	GenericEvent() []byte
	// MayContainModel identifies event labels eligible for response model metadata
	// restoration.
	MayContainModel(string) bool
	// ReplaceModel restores upstream model metadata to the client requested
	// identifier in response bytes. The protocol adapter limits replacement to
	// supported metadata fields; ordinary output text remains under the payload
	// representation rules.
	ReplaceModel([]byte, string, string) []byte
	// MayContainTools identifies event labels that may need client tool-name
	// correction.
	MayContainTools(string) bool
	// LikelyTools cheaply screens response bytes before invoking tool-name
	// correction.
	LikelyTools([]byte) bool
	// CorrectTools applies captured tool-name restoration to the client-facing
	// response bytes. Its changed flag controls replacing the payload; RelayTurn
	// also supplies the corrected bytes to its output replay collector.
	CorrectTools([]byte) ([]byte, bool)
	// CapacityShed rewrites recognized overload codes in the client copy to permit
	// client backoff. Provider classification retains the upstream event bytes and
	// selected decision, so presentation does not erase the original failure
	// evidence.
	CapacityShed([]byte) ([]byte, bool)
	// WriteClient synchronously sends the prepared response event under
	// adapter-owned downstream controls. A nil error records downstream output;
	// recognized disconnection switches RelayTurn to draining, while other
	// failures return a nonretryable client-write stage.
	WriteClient([]byte) error
	// IsDisconnect recognizes downstream write failure that permits draining
	// upstream to terminal completion.
	IsDisconnect(error) bool
	// SummarizeClose returns diagnostic close status and reason labels for client
	// disconnection.
	SummarizeClose(error) (string, string)
	// Truncate bounds a diagnostic value to the supplied display limit.
	Truncate(string, int) string
	// NormalizeLog produces an adapter-safe label for ingress diagnostic values.
	NormalizeLog(string) string
	// Log records turn diagnostics through the adapter-provided informational
	// logging path.
	Log(string)
	// Debug records detailed turn diagnostics when RelayTurn has enabled them.
	Debug(string)
}
