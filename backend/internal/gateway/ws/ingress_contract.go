package ws

import (
	"context"
	"encoding/json"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// ingress_contract.go defines turn payloads, execution results, and
// dependencies for sequential Responses ingress.
// A normalized WebSocket turn has several identities: client model, eligible
// routing model, provider-scoped payload, and pre-scope input used for retry
// construction. Ingress records keep these forms separate from observed result
// metadata and replay history, while dependency interfaces let the session
// choose connection affinity and recovery order.

// ClientPayload retains normalized turn bytes and the model and retry inputs
// that produced them.
type ClientPayload struct {
	// PayloadRaw is forwarded after normalization. ProviderIdentitySourceRaw
	// retains a separate copy from before provider scoping; RawForHash can share
	// the supplied trimmed client bytes and remains immutable during hashing or
	// replay.
	PayloadRaw                []byte // normalized provider request bytes
	ProviderIdentitySourceRaw []byte // request before identity scoping
	RawForHash                []byte // prompt-replaced client request bytes
	PromptCacheKey            string // client prompt cache key
	PreviousResponseID        string // client supplied continuation identifier
	// OriginalModel, RoutingModel, and image billing metadata describe different
	// consumers. Eligibility uses RoutingModel, response restoration uses
	// OriginalModel, and image usage uses ImageBillingModel when generated output
	// is counted.
	OriginalModel            string  // client requested model identifier
	RoutingModel             string  // group eligible model identifier
	ImageBillingModel        string  // resolved image pricing model
	ImageSizeTier            string  // image billing size tier
	ImageInputSize           string  // image request size label
	PayloadBytes             int     // normalized payload byte count
	RequestedReasoningEffort *string // client effort before policy
}

// ForwardResult supplies one turn's accounting observations, output metadata,
// and replay material.
type ForwardResult struct {
	RequestAttempt telemetry.RequestAttemptRef `json:"-"` // originating diagnostic dispatch reference
	// LocalWarmup marks a locally completed generate=false request. RunEntry skips
	// upstream health, quota refresh, and normal usage submission for this result.
	LocalWarmup           bool                         `json:"-"` // gateway handled warmup locally
	UpstreamResponseModel string                       // observed upstream response model
	VideoCount            int                          // observed generated video count
	VideoResolution       string                       // observed video resolution label
	VideoDurationSeconds  int                          // seconds
	WebSearchCalls        int                          // observed upstream web-search calls
	SearchCount           int                          // observed billable search count
	AudioUsage            *protocol.AudioUsage         // observed audio usage breakdown
	UpstreamWarning       *forwardcore.UpstreamWarning // observed upstream risk warning

	// RequestID and ResponseID retain the execution adapter's result identifiers.
	// Pooled relay uses the observed response ID as RequestID; HTTP bridge can
	// retain both protocol and correlation values.
	RequestID  string // result correlation request identifier
	ResponseID string // protocol response object identifier
	// UpstreamHeaders and ResponseHeaders can be shared through provider result
	// conversion. Completion consumers treat the returned maps and nested slices
	// as immutable after capture.
	UpstreamHeaders             map[string][]string // upstream exchange header values
	Usage                       wire.ForwardUsage   // observed turn token usage
	Model                       string              // client requested model identifier
	BillingModel                string              // selected billing model identifier
	UpstreamModel               string              // actual provider request model
	UpstreamResponseServiceTier string              // observed upstream service tier
	UpstreamEndpoint            string              // actual upstream endpoint label
	// ServiceTier and ReasoningEffort reflect accepted request metadata;
	// RequestedReasoningEffort captures the client choice before rewriting.
	// UpstreamResponseServiceTier separately reports the observed response tier
	// for completion resolution.
	ServiceTier              *string             // accepted request service tier
	ReasoningEffort          *string             // accepted recorded reasoning effort
	RequestedReasoningEffort *string             // client effort before policy
	Stream                   bool                // client requested streaming behavior
	OpenAIWSMode             bool                // WS execution result marker
	UpstreamTerminalEvent    string              // normalized terminal event label
	ResponseHeaders          map[string][]string // completion response header values
	ResponseTurnState        string              // returned opaque turn state
	Duration                 time.Duration       // elapsed turn execution duration
	// Timings records upstream content independently of downstream commitment.
	// Completion clones the observations before a later turn can reuse mutable request state.
	Timings            *timing.Performance // frozen exchange performance observations
	ClientDisconnect   bool                // downstream client disconnected marker
	ImageCount         int                 // observed generated image count
	ImageFrameCount    int                 // encoded output frame billing units
	ImageSize          string              // image billing size tier
	ImageInputSize     string              // input image size label
	ImageOutputSize    string              // selected output image size
	ImageOutputSizes   []string            // observed output image sizes
	ImageSizeSource    string              // image size observation source
	ImageSizeBreakdown map[string]int      // output counts by image size
	// WSReplayInput and WSReplayInputExists retain normalized ordinary history,
	// including explicit empty input. WSProviderFailoverReplayInput keeps the
	// bridge's separate output history used to build a self-contained
	// replacement-provider request.
	WSReplayInput                 []json.RawMessage // ordinary continuation replay items
	WSReplayInputExists           bool              // ordinary replay input presence
	WSProviderFailoverReplayInput []json.RawMessage // replacement-provider replay output items
}

// TurnCapture associates a turn outcome with its accepted request and logical
// completion timing.
type TurnCapture struct {
	Turn      int       // attempt-local turn sequence number
	StartedAt time.Time // turn pricing reference time
	// RequestBody is copied by sequential ingress before AfterTurn. Passthrough
	// supplies its queue-owned snapshot instead; callback consumers treat either
	// captured payload as immutable.
	RequestBody        []byte // captured forwarded request bytes
	OriginalModel      string // client requested model identifier
	PreviousResponseID string // forwarded continuation response identifier
	// Result can be nil on a failed or missing turn. Err and image observations
	// together determine whether entry handles partial output and whether
	// completion can be submitted.
	Result        *ForwardResult // available turn execution observations
	Err           error          // final turn execution failure
	PayloadSource string         // request capture source label
}

// IngressHooks supplies admission and completion callbacks called by the
// sequential ingress loop.
type IngressHooks struct {
	// TurnStarted, BeforeTurn, BeforeRequest, and AfterTurn are optional
	// synchronous hooks. Run ignores absent callbacks, while each execution path
	// chooses its timing for observed, admitted, and completed turns.
	TurnStarted func(int, time.Time) // turn timing observation callback
	// BeforeTurn and BeforeRequest are skipped on a local pooled retry after
	// successful admission. A new client turn invokes them again; AfterTurn
	// reports only the final attempt for that local turn.
	BeforeTurn    func(int) error                                   // turn admission validation callback
	BeforeRequest func(int, []byte, string, string) ([]byte, error) // later payload validation callback
	AfterTurn     func(TurnCapture)                                 // final turn observation callback
}

// IngressState retains the current session model, continuation headers, and
// connection affinity.
type IngressState struct {
	// OriginalModel is updated only after normalization succeeds. Later frames
	// that omit model reuse it before resolving group and provider mappings.
	OriginalModel string // last accepted client model
	TurnState     string // current opaque continuation header
	SessionHash   string // derived session affinity hash
	// PreferredConnID starts from response or session affinity and then tracks the
	// held connection. Strict store-disabled continuation can force that
	// connection rather than allowing ordinary pool selection.
	PreferredConnID string // current preferred pooled connection
	StoreDisabled   bool   // current request store disabled
}

// ConnLease exposes an acquired upstream connection's identity, health, and
// release operations.
type ConnLease interface {
	// ConnID returns the identifier used by response and session connection
	// affinity.
	ConnID() string
	// MarkBroken makes the acquired transport unavailable for reuse after a
	// failed turn. The OpenAI binding evicts and closes it during this call;
	// ingress still releases its lease ownership afterward.
	MarkBroken()
	// Release returns this acquired lease's capacity and applies the pool's
	// retirement policy. Ingress owns release for its held session lease and
	// stops using that lease after cleanup or recovery resets the reference.
	Release()
	// SupportsIdlePingWithoutReader reports whether an idle connection can be
	// pinged safely between turns.
	SupportsIdlePingWithoutReader() bool
	// PingWithTimeout tests the held connection with the supplied health-check
	// budget. Ingress invokes it only when idle ping is supported; failure can
	// force replay-safe recovery or a continuation close. The OpenAI binding uses
	// its pool health-check default when the supplied budget is nonpositive.
	PingWithTimeout(time.Duration) error
}

// PreviousTurn compares retained protocol context with a proposed continuation
// request.
type PreviousTurn interface {
	// Keep decides whether current request bytes may retain the last response ID
	// using saved comparable protocol state. The tool-output flag gives required
	// call context precedence over ordinary comparison.
	//
	// The OpenAI adapter checks current ID against the last completed response and
	// compares normalized non-input fields when tool context does not force
	// retention. A false result includes a reason for full-input reconstruction;
	// an error makes ingress retain the current continuation, because comparison
	// failure alone does not establish that removing it is safe.
	Keep([]byte, string, bool) (bool, string, error)
}

// ReplayCodec provides normalized replay algorithms used by ingress recovery
// and HTTP bridging.
type ReplayCodec interface {
	// Extract parses normalized input items and separately reports input presence.
	// Returned items may share request bytes, so ingress keeps those bytes
	// immutable while normal and replacement-provider histories refer to them.
	Extract([]byte) ([]json.RawMessage, bool, error)
	// BuildFromItems selects a current full input or combines it with retained
	// history when continuation requires replay. Presence booleans distinguish
	// absent input from an explicitly empty sequence.
	//
	// The OpenAI adapter sanitizes historical tool calls, retains current input
	// that already has the historical prefix, and otherwise joins history with new
	// items. It may return either input slice directly and shares each item's
	// bytes. Callers keep those arrays and bytes immutable and use Combine when
	// appending output to separately retained replay histories.
	BuildFromItems([]json.RawMessage, bool, []json.RawMessage, bool, bool) ([]json.RawMessage, bool)
	// Build extracts current input and derives a full replay sequence with
	// optional retained history. Its result includes input presence separately
	// from sequence length.
	//
	// The OpenAI adapter applies the same prefix and historical tool-call rules as
	// BuildFromItems. Extraction errors return before producing usable current
	// replay; IngressSession then clears that turn's optional replay state while
	// retaining the current request for direct forwarding. Successful results may
	// share request bytes and history storage, which remain immutable while
	// retained across turns.
	Build([]json.RawMessage, bool, []byte, bool) ([]json.RawMessage, bool, error)
	// SetInput rewrites the request input when the presence flag is true. False
	// returns supplied bytes unchanged; a present empty sequence is encoded as an
	// array, preserving its distinction from absent input.
	SetInput([]byte, []json.RawMessage, bool) ([]byte, error)
	// RetryPayload builds a self-contained current request for a replacement
	// provider from pre-scope bytes, retained input, and the client model. Missing
	// full-input presence returns an unsafe result.
	//
	// The OpenAI adapter writes full input, removes previous_response_id, and
	// restores a nonblank client model before checking tool-output call coverage.
	// An uncovered tool output also returns false, because another provider cannot
	// resolve its prior call chain. Ingress wraps that empty unsafe result as a
	// recognized current-turn failure, and RunEntry closes instead of repeating an
	// earlier turn.
	RetryPayload([]byte, []json.RawMessage, bool, string) ([]byte, bool, error)
	// Combine appends output item references to history while sharing item bytes.
	// Nonempty output allocates a fresh slice array; empty output can return
	// history directly, so callers keep retained item bodies immutable.
	Combine([]json.RawMessage, []json.RawMessage) []json.RawMessage
	// HasOutput reports whether the supplied request contains protocol tool-output
	// items.
	HasOutput([]byte) bool
	// ItemsHaveOutput reports whether retained replay items include protocol tool
	// outputs.
	ItemsHaveOutput([]json.RawMessage) bool
	// ItemsCoverOutput reports whether replay contains matching call context for
	// every tool output.
	ItemsCoverOutput([]json.RawMessage) bool
	// DropPrevious attempts to remove previous_response_id and reports whether
	// removal occurred. Callers publish those bytes only after any required
	// replay-input rewrite succeeds; errors or no removal retain the original
	// continuation request.
	DropPrevious([]byte) ([]byte, bool, error)
	// SetPrevious writes the supplied continuation response ID into request bytes.
	// Ingress retains the current payload when mutation fails, and updates its
	// derived ID and byte count only after success.
	SetPrevious([]byte, string) ([]byte, error)
	// BuildStrict retains comparable non-input request state for the next
	// continuation check. An error makes IngressSession keep full payload bytes
	// for raw comparison; success clears those bytes even when the returned
	// state is nil. The OpenAI adapter returns nil without error for empty request
	// bytes, which leaves no saved comparison input on that success path.
	BuildStrict([]byte) (PreviousTurn, error)
	// KeepPrevious compares previous and current request bytes when no saved
	// strict state is available. Tool outputs require retaining their continuation
	// before the ordinary ID and non-input checks.
	//
	// The OpenAI adapter requires the current ID to match the last completed
	// response, then normalizes both payloads for non-input comparison. Changed
	// fields select full-input reconstruction and provide a reason. A comparison
	// error keeps the supplied continuation in ingress, which does not infer
	// replay safety from a parsing failure.
	KeepPrevious([]byte, []byte, string, bool) (bool, string, error)
	// StripItems removes ciphertext-bearing replay items whose digests were
	// previously rejected for this session. Its count reports removed items;
	// callers strip current payload and retained histories together to keep prefix
	// comparisons aligned.
	StripItems([]json.RawMessage, map[string]struct{}) ([]json.RawMessage, int)
	// ShouldInfer selects missing continuation inference from store-disabled state
	// and tool-call observations. The OpenAI rule requires a later turn, tool
	// outputs with call IDs, no supplied continuation, no actual call context, and
	// a known last response ID.
	//
	// An item reference alone is not the tool-call context needed by an output.
	// Inference therefore gives such outputs the last response anchor, while a
	// self-contained call/output request keeps its existing unanchored form.
	// Ingress applies the returned decision through SetPrevious and logs mutation
	// failure while retaining the original bytes.
	ShouldInfer(bool, int, wire.ToolContinuationSignals, string, string) bool
	// ClassifyPrevious labels continuation ID spellings for validation and session
	// diagnostics.
	ClassifyPrevious(string) string
}

// IngressPort supplies adapter operations while IngressSession owns turn
// iteration and recovery.
type IngressPort interface {
	// Parse validates and normalizes one request for the supplied local turn
	// number. The replacement flag controls user prompt rewriting; successful
	// results retain client, routing, and provider-scoped forms needed by later
	// execution and replay.
	Parse([]byte, bool, int) (ClientPayload, error)
	// ShouldBridge selects HTTP bridge eligibility from the first normalized
	// payload and provider capabilities.
	ShouldBridge(ClientPayload) bool
	// ReadClient waits for the next client turn under adapter-owned control and
	// idle policy. Ingress treats recognized disconnection as a normal session
	// exit and returns other read failures for close presentation.
	ReadClient() ([]byte, error)
	// GenerateHash derives the session affinity key from the normalized hashing
	// input.
	GenerateHash([]byte) string
	// StoreDisabled reports whether the current request uses store-disabled
	// connection affinity rules.
	StoreDisabled([]byte) bool
	// InvalidDigests returns ciphertext digests previously rejected for this group
	// and session.
	InvalidDigests(int64, string) map[string]struct{}
	// StripInvalid removes previously rejected ciphertext from the current request
	// and returns removed count. Ingress changes payload bytes and size only after
	// positive removal, then applies the same digest set to retained replay items.
	StripInvalid([]byte, map[string]struct{}, string, int64, int) ([]byte, int)
	// BridgeIdentity derives the Grok cache identity using the session seed and
	// current routing model. A failed derivation stops the turn before Bridge,
	// preventing a request from using unrelated cached history.
	BridgeIdentity([]byte, string, int) (string, error)
	// Bridge executes one normalized turn through HTTP with the supplied
	// replay-expanded body and cache identity. The result includes ordinary and
	// replacement-provider output histories; errors can be wrapped with
	// current-turn replay for entry failover.
	Bridge(context.Context, ClientPayload, []byte, string, int) (*ForwardResult, error)
	// SetRequestState publishes the current turn-state header and session hash to
	// the execution adapter.
	SetRequestState(string, string)
	// OpenPool prepares the provider pool from the first normalized request and
	// connection configuration.
	OpenPool(ClientPayload) error
	// Acquire obtains a provider connection for this turn and optional preferred
	// ID. Force requires continuation affinity, while allowRecovery permits
	// returning AcquireRecoveryError for the session's single credential recovery
	// path.
	Acquire(int, string, bool, bool) (ConnLease, error)
	// RecoverAcquire repairs provider identity after a marked dial failure under
	// the control context. Ingress sets its one-recovery flag before invoking this
	// method and retries acquisition only when recovery succeeds.
	RecoverAcquire(context.Context) error
	// Relay forwards one pooled turn on the held lease and returns result or
	// classified failure. Ingress owns lease release; pre-output transport
	// failures and eligible missing-response errors may be retried before
	// AfterTurn receives a final outcome.
	Relay(int, ConnLease, ClientPayload) (*ForwardResult, error)
	// PinConn reserves the supplied provider connection for store-disabled session
	// reuse and reports success. Ingress tracks only a successful pin and unpins
	// that exact connection before release or changing its session reservation.
	PinConn(int64, string) bool
	// UnpinConn releases the stored reservation for the supplied provider
	// connection identifier.
	UnpinConn(int64, string)
	// Header returns the current handshake header value used by continuation
	// diagnostics.
	Header(string) string
	// UpdateHeaders applies normalized turn identity and retained turn-state
	// values to future acquisition headers.
	UpdateHeaders(ClientPayload, string)
	// BindOwner records response ownership after a successful pooled turn supplies
	// a response ID.
	BindOwner(context.Context, string)
	// IsDisconnect classifies a client read exit handled as normal session
	// closure.
	IsDisconnect(error) bool
	// IsFailover recognizes a provider failure requiring entry-level
	// replacement-provider selection.
	IsFailover(error) bool
	// CloseError constructs the client close instruction around a session failure
	// cause.
	CloseError(int, string, error) error
	// Log records an ingress session diagnostic through the adapter's configured
	// logger.
	Log(string)
	// Debug records detailed ingress session diagnostics when its caller enables
	// them.
	Debug(string)
	// NormalizeLog produces a safe display label for an ingress diagnostic value.
	NormalizeLog(string) string
	// TruncateLog bounds one diagnostic value using the supplied display limit.
	TruncateLog(string, int) string
	// SummarizeClose extracts close status and reason labels for session exit
	// diagnostics.
	SummarizeClose(error) (string, string)
	// BindWarning observes a failed response-to-provider affinity write with its
	// group and response identity.
	BindWarning(int64, int64, string, error)
}

// IngressOptions specifies provider identity, bridge selection, and session
// affinity budgets.
type IngressOptions struct {
	ProviderType    string // selected provider credential type
	BridgeThreshold int64  // first payload bridge threshold bytes
	ProviderID      int64  // selected upstream provider identifier
	Platform        string // selected upstream platform label
	GroupID         int64  // authenticated request group identifier
	UseBridge       bool   // adapter selected bridge mode
	Debug           bool   // detailed ingress diagnostics enabled
	// PreviousRecovery, ResponseStickyTTL, and SessionStickyTTL are refreshed
	// before each loop iteration when Refresh is configured. Other option values
	// remain the session's initial copy.
	PreviousRecovery  bool   // missing continuation recovery permitted
	StoreDisabledMode string // store-disabled affinity mode label
	// PreflightPingIdle suppresses ping while the prior clean turn is recent.
	// Nonpositive values permit the later-turn ping immediately when the held
	// lease supports idle checks.
	PreflightPingIdle  time.Duration // idle health check threshold
	HealthCheckTimeout time.Duration // idle ping operation budget
	ResponseStickyTTL  time.Duration // response affinity record lifetime
	SessionStickyTTL   time.Duration // session affinity record lifetime
}

// IngressSession owns one sequential client conversation and its bridge or pool
// recovery state.
type IngressSession struct {
	// Refresh is optional and reads a value snapshot before each turn or local
	// retry. Recovery permission and sticky TTL values update from that snapshot.
	Refresh func() Parameters // current turn parameter snapshot reader
	// State, Codec, and Port are required and owned by one sequential Run
	// invocation. Session fields are mutated across turns; callers do not run the
	// same instance concurrently.
	State   *IngressState              // mutable session affinity state
	Store   session.OpenAIWSStateStore // optional response session cache
	Options IngressOptions             // initial provider session options
	Hooks   *IngressHooks              // optional turn lifecycle callbacks
	Codec   ReplayCodec                // protocol replay algorithm dependency
	Port    IngressPort                // execution adapter operation dependency
}

// AcquireRecoveryError marks an acquire failure eligible for one
// identity-recovery attempt.
type AcquireRecoveryError struct {
	// Err is the marked acquisition cause and is required when Error formats the
	// wrapper. Ingress uses the wrapper type to select its single identity repair.
	Err error // underlying acquire failure cause
}

// Error exposes the underlying acquire failure message for identity-recovery
// diagnostics. Err must be nonnil on a usable wrapper; the constructor is
// supplied by acquisition adapters when a recoverable dial failure occurs.
func (e *AcquireRecoveryError) Error() string { return e.Err.Error() }

// Unwrap exposes the acquire failure cause to standard identity-recovery
// error-chain inspection.
func (e *AcquireRecoveryError) Unwrap() error { return e.Err }
