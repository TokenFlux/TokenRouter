package forward

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// messages_contract.go defines Messages adapter operations and constructs
// partial usage results for eligible stream failures. Anthropic forwarding
// distinguishes the provider's HTTP decision from a later stream failure.
// Ordinary eligible HTTP statuses request failover unless health selects
// generic output; stream events additionally require an uncommitted writer
// before replacement output or a provider switch can be selected. The adapter
// supplies response evidence and executes hooks in the attempt's order.
//
// PartialUsage joins usage observed by the reader with HTTP metadata, the
// logical and mapped models, and a requested speed hint. Positive observations
// survive ordinary stream errors, while a failover result suppresses the partial
// billing result. Header maps and captured performance remain shared, so the
// holder expresses which observations belong together without cloning them.

// MessageInput captures provider eligibility and static options for one Messages attempt.
type MessageInput struct {
	// ProviderPresent guards alternate provider paths; HTTPPresent enables
	// HTTP beta/debug handling. OAuth enables mimicry and 403 retry eligibility,
	// while Passthrough and Bedrock select their ordered early delegation paths.
	ProviderPresent, HTTPPresent, OAuth, Passthrough, Bedrock bool   // selected provider presence; HTTP context presence; OAuth provider eligibility; API-key passthrough eligibility; Bedrock provider selection
	ProviderID                                                int64  // selected upstream provider
	ProviderName, ProviderType, Platform                      string // provider display name; provider authentication type; provider platform identifier
	// LogErrorBody controls extra body detail independently of summary logging.
	// FailoverOn400 permits the separate body-classification path for HTTP 400
	// after ordinary status-based recovery has declined that status.
	LogErrorBody, FailoverOn400 bool // detailed body logging; classified HTTP-400 recovery
	// LogErrorBodyMaxBytes bounds detailed excerpts. The classified-400 and
	// stream paths select 2048 for nonpositive limits; other calls use Truncate's
	// empty result or TruncateBytes' own default directly.
	LogErrorBodyMaxBytes int
}

// PassthroughInput supplies a selected body and entry metadata to API-key forwarding.
type PassthroughInput struct {
	// Body contains the locally mapped passthrough bytes supplied to the
	// delegate; this holder retains their storage and does not publish them.
	Body []byte
	// Parsed retains the entrypoint request state. The passthrough implementation
	// decides when filtered preparation bytes replace this shared parsed body.
	Parsed *requeststate.ParsedRequest
	// RequestModel names the model selected for the passthrough body, while
	// OriginalModel retains the entry input for result metadata after mapping.
	RequestModel, OriginalModel string    // selected passthrough model; original entrypoint model
	Stream                      bool      // requested downstream streaming
	StartedAt                   time.Time // entrypoint forwarding start time
}

// NormalizeOptions selects system-cache stripping and metadata injection during OAuth preparation.
type NormalizeOptions struct {
	// StripSystemCacheControl follows whether the system rewrite path was
	// invoked. InjectMetadata is enabled when Metadata supplies a nonempty
	// identity, independently of whether the system bytes actually changed.
	StripSystemCacheControl, InjectMetadata bool // strip system markers; inject metadata identity
	// MetadataUserID supplies the selected OAuth identity when InjectMetadata
	// is enabled; the normalizer decides how that identity updates request JSON.
	MetadataUserID string
}

// Notice describes a source failure submitted to the request observation adapter.
type Notice struct {
	UpstreamURL string // diagnostic upstream URL
	Passthrough bool   // API-key passthrough mode
	// upstream platform identifier; provider display name; source request identifier;
	// observation event kind; extracted error summary; optional body detail
	Platform, ProviderName, UpstreamRequestID, Kind, Message, Detail string
	ProviderID                                                       int64 // observed upstream provider
	UpstreamStatusCode                                               int   // observed source status
}

// ExchangeResponse retains HTTP metadata supplied before successful response processing begins.
type ExchangeResponse struct {
	StatusCode int // upstream HTTP status
	// Headers retains the adapter's map and value slices. Result construction
	// carries these references forward, so later mutation is visible to consumers.
	Headers   map[string][]string
	RequestID string // upstream request identifier
}

// StreamOutcome retains observations reported by the reader after its stream attempt.
type StreamOutcome struct {
	// Usage points to reader observations, including partial usage on error.
	// A nil pointer or no positive token observation cannot form PartialUsage.
	Usage *upstream.TokenUsage
	// Timings retains the captured performance snapshot for this exchange. A missing
	// observation stays nil, and PartialUsage carries the snapshot without cloning it.
	Timings          *timing.Performance
	ClientDisconnect bool // observed client disconnection
}

// MessageHooks receives serial exchange and reader milestones within one execution.
type MessageHooks struct {
	// Before receives nonnil response metadata and selected wire bytes before
	// acceptance. A true result or error stops normal output; captured readers
	// can be consumed again by the health/error callbacks this hook invokes.
	Before func(context.Context, *ExchangeResponse, []byte) (bool, error)
	// Wire records the exchange's selected bytes, including on exchange failure.
	// The supplied slice stays shared and is later used for requested-speed evidence.
	Wire func([]byte)
	// Accepted follows a passing Before hook. BeforeStream runs afterward for
	// streaming output and captures the writer size before the reader starts.
	Accepted, BeforeStream func() // accepted upstream milestone; stream reader milestone
	// Stream receives available reader observations after streaming, including
	// an outcome accompanying error. Callbacks finish before Execute returns
	// and should retain only observations belonging to this attempt.
	Stream func(*StreamOutcome)
}

// MessageExecution holds prepared request bytes and ordered callbacks for the Anthropic executor.
type MessageExecution struct {
	// Model names the prepared upstream request, while OriginalModel identifies
	// the client's logical input for output and result observations after mapping.
	Model, OriginalModel string // prepared upstream model; original client model
	// Stream selects downstream output. Mimic identifies the OAuth rewrite path
	// chosen by Messages and controls exchange/response adaptation for that attempt.
	Stream, Mimic bool      // downstream streaming mode; OAuth mimic selection
	StartedAt     time.Time // forwarding attempt start
	// Body retains the prepared byte storage passed to the exchange. Selected
	// wire bytes can later be published through ReplaceBody after successful HTTP handling.
	Body []byte
	// ReplaceBody reparses the shared request after a rewrite. The concrete
	// ParsedRequest replacement can change body state before reporting a parse
	// error, so this callback does not promise rollback of failed replacement.
	ReplaceBody func([]byte) error
	// Hooks belongs to this execution and receives ordered, serial callbacks.
	// Messages keeps mutable observations in those callbacks until Execute returns.
	Hooks MessageHooks
}

// MessagePorts supplies settings, provider operations, and transport steps used by Messages.
type MessagePorts interface {
	// ShouldEmulate reports whether the selected provider/group and request body
	// require the configured emulation path. Messages checks it before passthrough,
	// Bedrock, or ordinary preparation so its result chooses the complete early delegate.
	ShouldEmulate(context.Context, *int64, []byte) bool
	// Emulate handles the shared parsed request through the selected emulation
	// implementation. Messages returns that result directly, and ordinary beta,
	// credential, transport, and response-hook preparation does not follow this path.
	Emulate(context.Context, *requeststate.ParsedRequest) (*Result, error)
	// MappedModel resolves the provider's mapping for a passthrough model.
	// Messages rewrites the local body when the returned string differs, then
	// passes both selected and original models to the passthrough delegate.
	MappedModel(string) string
	// ReplaceModel writes the selected model into request JSON for later
	// preparation. Ordinary Messages republishes the returned bytes through its
	// replacement callback, while the early passthrough branch retains its local
	// body until delegation.
	ReplaceModel([]byte, string) []byte
	// Passthrough executes API-key forwarding with the supplied locally mapped
	// body and original request metadata. Messages returns its outcome directly;
	// that implementation owns subsequent parsed-body publication, response
	// reading, and error handling.
	Passthrough(context.Context, PassthroughInput) (*Result, error)
	// Bedrock executes the selected provider's parsed Messages request using
	// the supplied start time. This delegate runs before ordinary Anthropic beta
	// and mimic preparation and owns the platform-specific exchange outcome.
	Bedrock(context.Context, *requeststate.ParsedRequest, time.Time) (*Result, error)
	// Begin enters ordinary forwarding and returns its release callback.
	// Messages calls the nonnil callback once on every later return after successful
	// entry; alternate emulation, passthrough, and Bedrock delegates run before it.
	Begin() (func(), error)
	// Beta evaluates the selected Anthropic provider's beta-header policy for
	// the entry model. Messages invokes it when HTTP context is present, before
	// model rewriting or credentials, and returns any rejection immediately.
	Beta(context.Context, string) error
	// DebugOriginal records entry request evidence before ordinary model rewriting begins.
	DebugOriginal([]byte, string, bool)
	// ProviderMappedModel resolves the provider mapping used before platform
	// normalization. Messages republishes changed bytes and selected model state
	// before mimic/cache preparation, retaining the logical entry model for its
	// returned result.
	ProviderMappedModel(string) string
	// IsClaudeCode reports whether request context, body, or metadata identify
	// a Claude Code client. Messages combines this evidence with OAuth eligibility;
	// a recognized client skips the system, metadata, cache, and tool mimic phase.
	IsClaudeCode(context.Context, []byte, string) bool
	// SystemSettings returns injection eligibility, system text, and encoded
	// prompt blocks for OAuth mimicry. The enabled path marks system rewriting as
	// invoked even when returned bytes are unchanged, affecting subsequent cache
	// marker stripping.
	SystemSettings(context.Context) (bool, string, string)
	// RewriteSystem produces system-prompt bytes from the parsed request and
	// configured text/blocks. Messages reparses its return before recording the
	// rewrite path, and leaves allocation or unchanged-body behavior with the
	// implementation.
	RewriteSystem([]byte, *requeststate.ParsedRequest, string, string) []byte
	// Metadata resolves an OAuth metadata identity for this parsed request.
	// An empty string leaves injection disabled; a nonempty identity is passed
	// together with InjectMetadata to the normalizer after the system path.
	Metadata(context.Context, *requeststate.ParsedRequest) string
	// NormalizeOAuth applies system-cache and metadata choices to OAuth request
	// JSON. Messages republishes its returned bytes before cache and tool handling;
	// storage sharing and optional rewrite behavior belong to the implementation.
	NormalizeOAuth([]byte, NormalizeOptions) []byte
	// RewriteCache applies configured OAuth cache-control preparation to the
	// current normalized body. Messages reparses that result before tool adaptation
	// and the later CacheLimit pass, so downstream steps see the selected request
	// structure.
	RewriteCache(context.Context, []byte) []byte
	// RewriteTools returns adapted tool bytes and whether a tool-name rewrite
	// was found. A true result is published before BindTools; false selects the
	// ToolsLast path and does not bind new response-name state.
	RewriteTools([]byte) ([]byte, bool)
	// BindTools records the tool-name rewrite selected by RewriteTools for
	// response restoration. Messages calls it after successful body replacement;
	// a replacement error returns before the response state is bound.
	BindTools()
	// ToolsLast applies the tool-side cache breakpoint when no tool-name
	// rewrite is selected. Messages reparses its returned bytes before dateline
	// normalization and the total cache-marker limit; it does not invoke BindTools
	// on this path.
	ToolsLast([]byte) []byte
	// NormalizeDateline returns optional normalized dateline bytes and whether
	// to publish them. False leaves the current body in place; true requires
	// successful parsed-body replacement before later cache and model preparation
	// continues.
	NormalizeDateline(context.Context, []byte) ([]byte, bool)
	// CacheLimit enforces the Anthropic request's total cache-control marker
	// limit after optional mimic/dateline handling. Messages republishes its return
	// before platform mapping and TTL injection, independently of which system
	// rewrite path ran.
	CacheLimit([]byte) []byte
	// PlatformModel resolves the Anthropic upstream name after provider mapping.
	// Messages replaces both body model and parsed model when it changes, while
	// retaining the original client name for completion metadata.
	PlatformModel(string) string
	// InjectTTL reports whether selected-provider or per-Key settings enable
	// cache TTL injection for the resolved upstream model. A true result applies
	// CacheTTL after model normalization and before credentials, with parsed
	// replacement required for continued execution.
	InjectTTL(context.Context, string) bool
	// CacheTTL applies the selected one-hour cache-control TTL request rewrite.
	// Messages calls it after TTL eligibility and republishes its return before
	// credential/transport preparation; callback ownership remains with the
	// implementation.
	CacheTTL([]byte) []byte
	// Credential resolves and retains credentials for the prepared provider
	// request. An error stops before transport and compatibility filters; success
	// makes that retained credential available to the later executor exchange.
	Credential(context.Context) error
	// Transport resolves the selected provider proxy and TLS fingerprint for execution.
	Transport()
	// FilterSearchHistory adapts web-search history using the prepared model's
	// thinking protocol. Messages reparses the selected body before thinking-block
	// filtering so both compatibility steps operate on the same model and request
	// state.
	FilterSearchHistory([]byte, string) []byte
	// FilterThinking selects compatible thinking blocks for the prepared model.
	// Messages republishes the returned bytes, then optionally normalizes thinking
	// mode for models requiring passback; an invalid replacement ends preparation
	// before exchange.
	FilterThinking([]byte, string) []byte
	// PassbackThinking reports whether the prepared model requires thinking
	// passback. Its true result gates NormalizeThinking after history/block
	// filtering, and only an applied normalization is published and recorded in
	// the forwarding log.
	PassbackThinking(string) bool
	// NormalizeThinking returns a model-compatible thinking-mode body and an
	// applied flag. False retains current bytes; true requires parsed replacement
	// before logging the rewrite, leaving transport execution to the later phase.
	NormalizeThinking([]byte, string) ([]byte, bool)
	// ReadErrorBody consumes the retained upstream response body and returns
	// captured bytes with any read failure. Some status branches retain partial
	// evidence despite error; classified-400 handling delegates immediately when
	// reading fails.
	ReadErrorBody() ([]byte, error)
	// ResetErrorBody replaces the consumed response reader with the captured
	// bytes before HTTP health classification. Health may consume that replacement
	// reader again; callers cannot assume it remains unread for later error
	// handling.
	ResetErrorBody([]byte)
	// Health selects provider error treatment for retry, failover, synthetic,
	// volatile, or persisted observations. HTTP status branches use Generic and
	// RetrySameProvider; stream branches additionally inspect Failover and writer
	// commitment before selecting recovery.
	Health(context.Context, string, int, map[string][]string, []byte, string) ErrorDecision
	// HandleError delegates retained HTTP error handling and optional exhausted
	// retry treatment. Messages uses its result as an early outcome, so the ordinary
	// accepted/response-reader hooks do not follow that handled branch.
	HandleError(context.Context, string, bool) (*Result, error)
	// Observe submits source-error metadata through the current request observation adapter.
	Observe(Notice)
	// Failover400 reports whether captured HTTP 400 evidence matches the
	// configured recoverable error classification. Messages calls it only when
	// FailoverOn400 is enabled, then consults health before selecting its failover
	// signal.
	Failover400([]byte) bool
	// FailoverError constructs a gateway error retaining status, body evidence,
	// and same-provider retry permission. The surrounding request loop decides
	// whether its remaining attempt budget and output state permit another provider
	// exchange.
	FailoverError(int, []byte, bool) error
	// IsFailover reports whether an error carries the gateway failover signal.
	// Messages uses it to suppress PartialUsage on stream failure, preventing that
	// selected recovery outcome from also producing a partial billing result here.
	IsFailover(error) bool
	// Truncate bounds diagnostic text by bytes while retaining complete UTF-8
	// characters for valid input. Nonpositive limits return empty, so callers choose
	// any desired body-detail default before invoking this helper.
	Truncate(string, int) string
	// TruncateBytes limits captured input bytes and escapes line breaks for
	// routine logs. Nonpositive limits select the byte logger's default, and
	// escaping can expand output length beyond the chosen input-byte budget.
	TruncateBytes([]byte, int) string
	// Sanitize masks recognized credential query values in diagnostic summaries.
	// The adapter recognizes key, client_secret, access_token, and refresh_token
	// names; raw body detail follows separate logging choices and is not made safe
	// by sanitizing a summary.
	Sanitize(string) string
	// Log writes the prepared forwarding diagnostic through the configured platform logger.
	Log(string)
	// Execute exchanges the prepared request and owns response reading and
	// body closure. Hooks run serially before return, with nonnil metadata before
	// normal handling and stream observations available on reader failure when
	// reported by the implementation.
	Execute(context.Context, MessageExecution) (upstream.AttemptResult, error)
	// StreamError returns raw Anthropic error-event data and whether the error
	// has that classified form. A recognized event enters status/health handling;
	// other stream failures can return PartialUsage from already observed token
	// counts.
	StreamError(error) (string, bool)
	// Size reports accepted body bytes after excluding recorded heartbeats.
	// Implementations return the unwritten sentinel for heartbeat-only output,
	// preserving provider recovery after queue or header-wait commitment.
	Size() int
	// Written reports whether the HTTP response writer has committed its response.
	Written() bool
	// GenericError writes the generic failure envelope for a selected stream
	// policy rejection. Messages calls it only while Written is false, then returns
	// the corresponding handling error before a provider switch is considered.
	GenericError()
	// ServiceTier returns response-tier evidence collected by the current response adapter.
	ServiceTier() string
}

// ShouldRetry reports whether caller-supplied retry eligibility permits HTTP 403
// treatment. The boolean comes from the selected adapter's provider view; this
// helper does not infer credential type or decide the remaining retry budget.
func ShouldRetry(oauth bool, status int) bool { return oauth && status == 403 }

// ShouldFailover reports status eligibility for 401, 403, 429, 529, and all
// statuses at least 500. The request loop still decides whether its provider
// policy, output state, and remaining budget allow the indicated switch.
func ShouldFailover(status int) bool {
	switch status {
	case 401, 403, 429, 529:
		return true
	default:
		return status >= 500
	}
}

// RetryDelay returns exponential retry pacing from a 300 ms starting delay.
// Nonpositive attempts use that starting delay, and ordinary bounded attempt
// indices cap at three seconds; callers must bound indices because the shift
// and duration multiplication are unchecked for integer overflow.
func RetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 300 * time.Millisecond
	}
	// Doubling paces successive attempts; the cap bounds ordinary retry waits.
	delay := 300 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 3*time.Second {
		return 3 * time.Second
	}
	return delay
}

// PartialUsage returns positive observed token usage after an ordinary stream
// failure, or nil when usage is absent or the error requests failover. A result
// requires nonnil response metadata because its headers are retained even when
// the request identifier is empty.
//
// Usage values are copied before a blank speed is filled from a case-insensitive
// fast request hint. Logical/mapped model identity and elapsed forwarding time
// accompany those counts, while headers and performance observations retain shared
// references. The caller may attach the separately observed response model.
func PartialUsage(resp *ExchangeResponse, stream *StreamOutcome, model, upstreamModel string, start time.Time, speed string, failover bool) *Result {
	if stream == nil || !stream.Usage.HasObservedTokens() || failover {
		return nil
	}
	usage := *stream.Usage
	// An omitted response speed can use the retained request hint; an observed
	// speed wins so partial accounting does not replace provider evidence.
	if strings.TrimSpace(usage.Speed) == "" && strings.EqualFold(strings.TrimSpace(speed), "fast") {
		usage.Speed = "fast"
	}
	requestID := ""
	if resp != nil {
		requestID = resp.RequestID
	}
	return &Result{
		RequestID:        requestID,
		UpstreamHeaders:  resp.Headers,
		Usage:            usage,
		Model:            model,
		UpstreamModel:    upstreamModel,
		Stream:           true,
		Duration:         time.Since(start),
		Timings:          stream.Timings,
		ClientDisconnect: stream.ClientDisconnect,
	}
}
