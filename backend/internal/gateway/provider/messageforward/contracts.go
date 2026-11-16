package messageforward

import (
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// contracts.go defines Messages forwarding options, attempt state, and
// synchronous HTTP operations.
// Messages output distinguishes counting errors, native response errors, and
// converted Chat or Responses output. Preparation keeps beta evaluation and
// tool restoration state with each attempt, while the HTTP adapter supplies
// borrowed request data and synchronous response operations.

// MessagesBody and CountBody select the HTTP error envelope used when bounded
// upstream response reads exceed their configured limit.
const (
	MessagesBody BodyKind = iota // Messages response error format
	CountBody                    // Count Tokens error format
)

// Options supplies startup transport and presentation choices for Messages
// forwarding.
type Options struct {
	// Configured enables the gated MessageInput and URL policy choices;
	// response read and stream options are consumed independently.
	Configured           bool          // startup configuration availability flag
	InjectAPIKeyBeta     bool          // API key beta injection
	LogErrorBody         bool          // upstream diagnostic body logging
	LogErrorBodyMaxBytes int           // diagnostic UTF-8 byte cap
	FailoverOn400        bool          // compatibility 400 failover permission
	GeminiDebugHeaders   bool          // Gemini exchange header diagnostics
	MaxLineSize          int           // SSE line byte limit
	StreamInterval       time.Duration // upstream event idle duration
	ResponseReadLimit    int64         // response body byte cap
	PreserveContentType  bool          // upstream content type retention
	URLAllowlistEnabled  bool          // upstream host allowlist permission
	AllowInsecureHTTP    bool          // format-validator HTTP scheme permission
	// URLValidation supplies allowlist checks when required; NewRuntime
	// clones AllowedHosts while retaining the other supplied option values.
	//
	// upstream URL allowlist policy
	URLValidation egress.ValidationOptions
}

// AttemptState keeps beta policy and response restoration data for one attempt.
type AttemptState struct {
	// BetaEvaluated records successful evaluation even when BetaFilters is
	// empty. Builders reuse BetaFilters after that flag is set, retaining
	// one attempt policy decision during request construction.
	BetaEvaluated bool                // saved evaluation presence flag
	BetaFilters   map[string]struct{} // beta tokens to remove
	// ToolNames is retained by response callbacks for this attempt; callers
	// keep the mapping available until synchronous conversion completes.
	//
	// response tool restoration mapping
	ToolNames *anthropic.ToolNameRewrite
	// MimicDebug stores retained OAuth Capture output; ScopeDiagnostic logs
	// it when the upstream error contains both recognized Claude Code
	// authorization phrases.
	MimicDebug string // OAuth mimic diagnostic text
	// CacheTTL1hApplied records that outer API key passthrough already applied
	// one-hour ephemeral cache lifetimes so buildPassthroughRequest skips rescanning.
	CacheTTL1hApplied bool // passthrough 1h cache TTL already applied
	// PrepareStartedAt and PrepareRecorded measure post-slot provider preparation
	// once before the attempt's first transport exchange begins.
	PrepareStartedAt time.Time // attempt preparation start timestamp
	PrepareRecorded  bool      // attempt preparation duration already emitted
}

// BodyKind selects the response-body size failure presentation for an entry.
type BodyKind uint8

// HTTPBoundary supplies synchronous HTTP presentation and request-local
// observations to forwarding.
type HTTPBoundary interface {
	// Present reports whether an HTTP context is available for this
	// attempt.
	Present() bool
	// RequestPresent reports whether the current HTTP context has a usable
	// request.
	RequestPresent() bool
	// HasHeaderFilter reports whether response headers use a configured
	// filtering policy.
	HasHeaderFilter() bool
	// RequestHeaders returns the current request headers or an empty map
	// when unavailable. A live result is borrowed, so callers synchronize
	// any changes with other request consumers.
	RequestHeaders() http.Header
	// Sink returns synchronous output backed by the current HTTP response
	// writer.
	Sink() upstream.OutputSink
	// SearchOutput returns search-emulation presentation backed by the
	// current HTTP request.
	SearchOutput() searchtools.Output
	// ConversionOutput binds Chat or Responses presentation to the supplied
	// attempt state. Its reverse callback reads ToolNames during output, so
	// callers retain that state until conversion finishes.
	ConversionOutput(responses bool, state *AttemptState) forward.Output
	// ReadResponseBody reads bounded upstream bytes and presents oversize
	// errors in the selected BodyKind format. The reader remains
	// caller-owned, and callers inspect the returned error before further
	// presentation.
	ReadResponseBody(io.Reader, int64, BodyKind) ([]byte, error)
	// Size reports accepted body bytes after excluding recorded heartbeats.
	// Implementations return the unwritten sentinel for heartbeat-only output,
	// preserving provider recovery after queue or header-wait commitment.
	Size() int
	// Written reports whether the response writer has begun its HTTP
	// response.
	Written() bool
	// Commit marks semantic response commitment for outer fallback
	// handling. This marker is separate from physical writer bytes, and
	// invoking it can suppress an additional response after presentation
	// failure.
	Commit()
	// GenericError writes the generic Messages failure and marks semantic
	// commitment. Implementations coordinate the supplied writer with the
	// current response state before selecting this terminal error output.
	GenericError()
	// MessageError presents an Anthropic JSON error with the supplied
	// status and kind. Runtime orchestration owns semantic commitment and
	// chooses this path when its current output state permits it.
	MessageError(int, string, string)
	// RawError presents the supplied JSON bytes with the selected HTTP
	// status. Runtime orchestration supplies valid framing and owns
	// semantic commitment for the request before any subsequent fallback.
	RawError(int, []byte)
	// CountError presents a counting failure using the Count Tokens error
	// envelope. Its caller decides commitment and response sequencing,
	// including whether an unsupported counting route should instead be
	// returned successfully.
	CountError(int, string, string)
	// CountSuccess presents counting bytes with upstream status and
	// headers. Passthrough uses the native header policy and upstream
	// content type when present; ordinary counting uses the shared JSON
	// response path.
	CountSuccess(int, map[string][]string, []byte, bool)
	// MarkPassthrough records native Anthropic passthrough in the request
	// observation state.
	MarkPassthrough()
	// ServiceTier returns the service tier selected by the response
	// observer. Missing or conflicting nonterminal evidence returns empty
	// so completion can interpret the absence of a tier observation.
	ServiceTier() string
	// SetError records upstream status and diagnostic text for the current
	// request.
	SetError(int, string, string)
	// Observe appends one upstream failure notice to the request diagnostic
	// history.
	Observe(forward.Notice)
	// MatchRule returns the configured presentation rule matching platform,
	// status, and body. An unavailable rule service or unmatched input
	// returns nil so the caller continues default error presentation.
	MatchRule(string, int, []byte) *errorpolicy.ErrorPassthroughRule
	// SkipMonitoring records the request flag suppressing configured
	// passthrough error monitoring.
	SkipMonitoring()
	// WriteHeaders copies upstream response headers according to
	// passthrough mode and the adapter filter. Nil filtering uses different
	// native and converted defaults, so callers supply the actual output
	// mode.
	WriteHeaders(http.Header, http.Header, bool)
}
