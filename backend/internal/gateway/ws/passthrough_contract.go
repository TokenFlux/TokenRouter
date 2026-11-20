package ws

import (
	"context"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// passthrough_contract.go defines provider operations and session inputs for
// dedicated WebSocket forwarding.
// A passthrough session filters each client turn before using one dedicated
// upstream socket, then observes completion through a bidirectional relay. The
// adapter supplies provider credentials, protocol rewriting, handshake failure
// recovery, and client presentation while PassthroughSession owns accepted-turn
// metadata and admission ordering.

// ClientSocket adds synchronous client frame delivery to close-aware connection
// reading.
type ClientSocket interface {
	ClientConn
	// Write synchronously sends the message type and bytes under the supplied
	// context. Policy rejection uses best-effort writes before closure, while
	// normal relay delivery reports errors to terminal admission and drain
	// handling.
	Write(context.Context, int, []byte) error
}

// PolicyBlocked carries the client reason and retained cause for a local policy
// rejection.
type PolicyBlocked struct {
	Message string // client policy rejection reason
	Cause   error  // underlying local policy error
}

// DialResult retains one upstream handshake outcome and its response evidence.
type DialResult struct {
	// PreparationError returns the associated error immediately without recovery
	// classification. Otherwise dial failures may use the session's one
	// provider-identity recovery attempt before final presentation.
	PreparationError bool // dial preparation failed marker
	// Conn is required after a successful dial. PassthroughSession retains and
	// closes it after relay; adapters keep returned handshake evidence stable
	// through failure and completion handling.
	Conn    FrameConn           // successful upstream frame connection
	Status  int                 // handshake HTTP response status
	Headers map[string][]string // handshake response header values
	Body    []byte              // handshake response body bytes
}

// PassthroughHooks supplies inherited turn callbacks and initial session
// observation values.
type PassthroughHooks struct {
	// ClientDisconnected freezes the active logical request before upstream usage drain.
	ClientDisconnected func(int, time.Time)

	// BeginPerformance records an exchange after this turn passes request preparation.
	BeginPerformance func(int, time.Time) *timing.Performance

	// IngressHooks is read through embedding when PassthroughHooks is present.
	// Callers supplying a hooks wrapper also provide that embedded pointer so
	// promoted turn callbacks can be accessed safely.
	*IngressHooks // shared turn lifecycle callbacks
	// InitialRequestModel supplies a model when the first payload omits it. A
	// supplied nonblank value also initializes UsageMeta's shared client model
	// default.
	InitialRequestModel string // initial entry model default
	// InitialTurnStartedAt supplies the first accepted-turn time when nonzero.
	// Otherwise Run captures its current time before first-frame policy
	// processing.
	InitialTurnStartedAt time.Time // first frame acceptance timestamp
	// OnUpstreamError is optional and observes recognized warning events with the
	// current queue's client model before client response rewriting.
	OnUpstreamError func(int, string, int, []byte, string) // upstream risk warning callback
}

// outboundEffortStripper optionally strips wire reasoning effort fields right
// before a WebSocket frame is sent upstream, after usage metadata and failover
// snapshots have captured the intact pre-strip payload.
type outboundEffortStripper interface {
	// StripOutboundEffort returns a wire-ready copy of body with reasoning effort
	// fields removed when the provider enabled effort_model_mapping_strip_effort
	// and an effort-scoped model override matched routingModel.
	StripOutboundEffort(ctx context.Context, routingModel string, body []byte) []byte
}

// PassthroughPort provides selected-provider protocol and transport operations
// for a dedicated relay session.
type PassthroughPort interface {
	// ModelAttributes applies provider request parameters for the resolved
	// upstream model and turn context. First and accepted later response.create
	// frames run this before queue publication, so failures prevent capturing an
	// unforwardable request.
	ModelAttributes(context.Context, int, string, []byte) ([]byte, error)
	// UsageDecoder supplies the accepted and requested metadata extractors used by UsageMeta.
	UsageDecoder
	// IsLite identifies the client metadata marker selecting Responses Lite
	// request handling.
	IsLite([]byte) bool
	// NormalizeLite enforces selected-provider fields required by a Lite request.
	// Passthrough reapplies it after compatibility normalization when needed,
	// because compatibility can remove fields that Lite processing must retain.
	NormalizeLite([]byte) ([]byte, error)
	// Reasoning applies group effort rules against the current client model. It
	// runs before model rewriting and Fast policy; a returned error becomes a
	// local close instruction and prevents forwarding the request.
	Reasoning([]byte, string) ([]byte, error)
	// Models resolves routing and provider models for a client identifier and
	// current turn. Later omitted-model requests use the retained client default,
	// while session.update can supply a new default for subsequent frames.
	Models(int, string, []byte) (string, string, error)
	// AliasTools rewrites OAuth-reserved tool names and records response reverse
	// mappings. Passthrough invokes it for OAuth response.create payloads; errors
	// stop the turn before policy filtering or upstream delivery.
	AliasTools([]byte) ([]byte, error)
	// Compatibility normalizes the selected provider's Responses request fields
	// and reports whether bytes changed. Callers replace bytes only when changed
	// is true and keep Lite enforcement after this compatibility step.
	Compatibility([]byte, bool) ([]byte, bool, error)
	// ScopeIdentity rewrites identity metadata into the selected provider
	// namespace. Passthrough applies it to first and later response.create plus
	// session.update frames, using the changed flag to retain unmodified bytes
	// when possible.
	ScopeIdentity([]byte) ([]byte, bool, error)
	// FastPolicy evaluates the accepted request tier against the current upstream
	// model. It returns bytes, a separate local block, or an error; the scoped
	// flag controls per-turn API Key policy context lookup.
	FastPolicy(context.Context, int, string, []byte, bool) ([]byte, *PolicyBlocked, error)
	// PromptReplace applies user prompt substitutions to later client request
	// frames before policy checks.
	PromptReplace(context.Context, []byte) []byte
	// BlockedEvent encodes a nonnil local policy rejection for best-effort delivery
	// before its close instruction. Nil bytes let the session skip the event write
	// while still returning the selected rejection.
	BlockedEvent(*PolicyBlocked) []byte
	// PolicyDenied marks local policy rejection in the owning request's
	// observation state.
	PolicyDenied()
	// SetUpstreamModel records the first resolved provider model for dial
	// preparation and diagnostics.
	SetUpstreamModel(string)
	// PrepareDial resolves URL, proxy, headers, fingerprint, and credential
	// routing before dialing upstream. The first normalized payload and prompt
	// cache key supply handshake identity; errors stop the session before
	// DialOnce.
	PrepareDial(context.Context, []byte, string) error
	// DialOnce attempts one upstream handshake under the supplied context. Its
	// result separates preparation failure from exchange failure and retains
	// status, headers, and body needed by recovery or final client presentation.
	DialOnce(context.Context) (DialResult, error)
	// CanRecover reports whether the failed handshake permits provider-identity
	// recovery under current context.
	CanRecover(context.Context, DialResult, error) bool
	// Recover refreshes provider identity after an eligible failed handshake.
	// Passthrough marks its single recovery attempt before invoking this method,
	// then dials again only after successful recovery.
	Recover(context.Context) error
	// DialFailure converts the final failed handshake into provider failover or
	// client-close handling. It receives the retained routing model and response
	// evidence after the bounded identity-recovery opportunity has been exhausted.
	DialFailure(context.Context, string, DialResult, error) error
	// RunRelay executes concurrent client-to-upstream and upstream-to-client
	// exchange using gateway frame wrappers and callbacks. It returns aggregate
	// observations and an optional classified exit when the controller returns.
	//
	// The gateway has already sent the first request when FirstMessageSent is set,
	// and owns accepted-turn snapshots, terminal admission callbacks, and final
	// client-close presentation. Completion callbacks may run before the
	// corresponding terminal frame is delivered, so the paired write callbacks
	// retain that ordering. The adapter translates gateway frame types and
	// delegates transport coordination to the upstream relay implementation. That
	// controller joins any started upstream reader before final settlement; its
	// bounded client exit wait can end while the client worker is still finishing.
	RunRelay(RelayInput) (RelayResult, *RelayExit)
	// NormalizeCompleted repairs compatible terminal frame forms before client
	// delivery and reports whether bytes changed. Client frame filtering uses the
	// returned bytes only on change; unchanged payload storage can remain shared.
	NormalizeCompleted([]byte) ([]byte, bool)
	// RestoreTools applies retained OAuth tool-name reverse mappings to downstream
	// response bytes.
	RestoreTools([]byte) []byte
	// EventType extracts the response event label used by output and lifecycle
	// callbacks.
	EventType([]byte) string
	// MayContainModel identifies response labels eligible for client model
	// metadata restoration.
	MayContainModel(string) bool
	// ReplaceModel restores supported model metadata from the provider identifier
	// to the client identifier.
	ReplaceModel([]byte, string, string) []byte
	// IsTerminal identifies labels used for queue snapshot consumption during
	// response delivery.
	IsTerminal(string) bool
	// NormalizeTerminal maps relay terminal labels to gateway completion result
	// vocabulary.
	NormalizeTerminal(string) string
	// NormalizeTier canonicalizes the tier declared by a terminal upstream
	// response.
	NormalizeTier(string) string
	// Warning extracts optional upstream risk evidence from an event label and
	// body. Nil suppresses the warning callback; recognized evidence reaches the
	// turn hook before client model and tool restoration.
	Warning(string, []byte) *forwardcore.UpstreamWarning
	// BeforeWrite applies selected provider failure handling before a downstream
	// text frame is delivered. The routing model and wroteDownstream state select
	// pre-output failover versus current-session client presentation using
	// retained handshake headers.
	BeforeWrite(context.Context, string, []byte, bool, map[string][]string) error
	// RelayClose selects a client close instruction from relay exit and
	// completed-turn count. A false result leaves close handling to the final
	// session path; passthrough attempts a selected instruction before internal
	// relay cancellation.
	RelayClose(RelayExit, int) (int, string, bool)
	// CloseError constructs the selected client-close instruction with its
	// retained underlying cause.
	CloseError(int, string, error) error
	// FirstOutputFailure records provider timeout feedback and constructs a
	// failover error for an expired first-output budget. Passthrough calls it for
	// every such expiry before choosing client presentation. Later turns or
	// sessions with prior downstream output replace its returned error with a
	// reconnect instruction while its observation and provider effects remain
	// applied.
	FirstOutputFailure(context.Context, Deadline, map[string][]string) error
	// FirstOutputTimeout selects the request's first-output budget from recorded
	// reasoning effort. A nonpositive result makes passthrough use the current
	// turn's read-timeout parameter before arming DeadlineConn.
	FirstOutputTimeout(string) time.Duration
	// Log records dedicated-session diagnostics through the adapter-provided
	// logging path.
	Log(string)
	// Truncate bounds a diagnostic value to the supplied display limit.
	Truncate(string, int) string
	// TruncateReason bounds a close reason to the supplied control-frame text
	// limit.
	TruncateReason(string, int) string
	// ClosedError returns the selected transport's missing or closed connection
	// error.
	ClosedError() error
}

// PassthroughOptions supplies selected provider flags and initial
// dedicated-session I/O budgets.
type PassthroughOptions struct {
	ProviderID int64 // selected upstream provider identifier
	OAuth      bool  // OAuth tool aliasing enabled
	// WriteTimeout and IdleTimeout initialize budgets when no parameter reader is
	// supplied. With Parameters available, accepted-turn write and read budgets
	// come from captured snapshots; some first-frame and rejection writes still
	// use WriteTimeout.
	WriteTimeout         time.Duration // initial synchronous write budget
	IdleTimeout          time.Duration // initial upstream silence budget
	InterTurnIdleTimeout time.Duration // initial client idle budget
}

// PassthroughSession filters requests and matches observed relay turns to
// captured completion metadata.
type PassthroughSession struct {
	// Parameters is sampled for initial and accepted later-turn budgets.
	// Inter-turn idle reads consult it separately when waiting for the next frame,
	// allowing a newer idle setting then.
	Parameters func() Parameters  // optional runtime parameter reader
	Options    PassthroughOptions // initial provider and budgets
	Port       PassthroughPort    // required selected-provider operations
	// Hooks may be nil to omit turn callbacks. A present wrapper requires its
	// embedded IngressHooks pointer; callbacks are invoked synchronously by the
	// relevant relay direction or controller.
	Hooks *PassthroughHooks // optional turn lifecycle wrapper
}

// stripOutboundEffort delegates wire-effort stripping to ports implementing
// outboundEffortStripper, returning body unchanged for other ports.
func stripOutboundEffort(ctx context.Context, port any, routingModel string, body []byte) []byte {
	stripper, ok := port.(outboundEffortStripper)
	if !ok || stripper == nil {
		return body
	}
	return stripper.StripOutboundEffort(ctx, routingModel, body)
}

// Error returns the client-facing policy rejection reason retained in the
// marker.
func (e *PolicyBlocked) Error() string { return e.Message }

// Unwrap exposes the retained local policy cause for standard error-chain
// inspection.
func (e *PolicyBlocked) Unwrap() error { return e.Cause }
