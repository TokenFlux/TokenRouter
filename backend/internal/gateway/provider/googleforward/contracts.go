package googleforward

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// contracts.go defines Google forwarding dependencies and tracks attempt-local
// tool and image observations.
// Google forwarding can observe the same images in several cumulative response
// payloads. The attempt counter retains the largest single-payload count so
// repeated cumulative chunks do not become additional images in completion
// metadata. Missing image observations remain zero, regardless of the request
// or upstream model name.
//
// The request-local counter and tool restoration map travel with one attempt
// and synchronous HTTP output. Injected Gemini and Antigravity services remain
// application-owned, while startup options control body limits and URL policy.
// Model names, usage, image observations, and client output are separate
// values; the result wrapper copies the outer forwarding value while retaining
// its reference-valued fields.

// Options supplies startup limits, diagnostics, and URL policy for Google
// forwarding.
type Options struct {
	// Configured gates diagnostic logging and native header diagnostics;
	// URL validation also uses it when selecting the disabled-allowlist
	// format path.
	Configured           bool  // startup configuration availability flag
	LogErrorBody         bool  // error body logging permission
	LogErrorBodyMaxBytes int   // diagnostic UTF-8 byte cap
	DebugHeaders         bool  // native response header diagnostics
	ResponseReadLimit    int64 // response body byte cap
	MaxLineSize          int   // SSE line byte limit
	StreamInterval       int   // upstream idle interval seconds
	URLAllowlistEnabled  bool  // upstream host allowlist permission
	AllowInsecureHTTP    bool  // format-validator HTTP scheme permission
	// AllowedHosts retains the supplied slice for URL validation; callers
	// keep its contents stable while forwarding can read the startup
	// policy.
	AllowedHosts      []string // permitted upstream host patterns
	AllowPrivateHosts bool     // private upstream host permission
}

// Output supplies synchronous HTTP presentation and request-local Google
// forwarding observations.
type Output interface {
	// RequestContext returns the active HTTP request context used by
	// forwarding.
	RequestContext() context.Context
	// APIKeyID returns the authenticated Key used to scope Gemini tool replay.
	// Zero means the caller has no authenticated Key available for replay.
	APIKeyID() int64
	// GetHeader reads one inbound header from the current HTTP request.
	GetHeader(string) string
	// Header sets one outbound header through the active HTTP presentation
	// adapter.
	Header(string, string)
	// WriteHeaders copies upstream headers through the supplied compiled
	// filter. Its delegated policy handles nil maps and filter defaults,
	// and callers provide the active destination before presenting response
	// bytes.
	WriteHeaders(http.Header, http.Header, *egress.CompiledHeaderFilter)
	// Raw presents status, content type, and response bytes through HTTP
	// output. The interface has no delivery error return; callers use Sink
	// when write failures affect attempt recovery or completion.
	Raw(int, string, []byte)
	// Sink returns synchronous output backed by the active HTTP response
	// writer.
	Sink() upstream.OutputSink
	// Commit records semantic response commitment for the outer request
	// handler. The marker changes request state separately from physical
	// output, and callers use it to suppress another terminal presentation.
	Commit()
	// Count writes the supplied local token count in native Google JSON.
	Count(int)
	// GeminiErrorBody commits and presents supplied native error bytes.
	// Callers provide their selected content type and status; the commit
	// marker suppresses an additional response if the underlying write
	// fails.
	GeminiErrorBody(int, string, []byte)
	// ReadBody reads bounded upstream bytes and reports size or reading
	// failures. Its reader remains caller-owned, and the HTTP adapter may
	// present an oversize error before returning that failure.
	ReadBody(io.Reader, int64) ([]byte, error)
	// Observe appends one upstream failure event to the active request
	// diagnostics.
	Observe(ops.OpsUpstreamErrorEvent)
	// SetError records upstream failure status and text for active request
	// diagnostics.
	SetError(int, string, string)
	// FeatureDenied records local feature admission failure in active
	// request diagnostics.
	FeatureDenied()
	// ClaudeError commits and presents a Messages error using the selected
	// platform adapter. It returns the presentation error to forwarding,
	// which decides whether later request handling may continue.
	ClaudeError(int, string, string) error
	// GoogleError presents a native Google error using the selected
	// platform adapter. Status-name mapping follows that adapter, and the
	// returned error is propagated to the forwarding caller.
	GoogleError(int, string) error
	// ChatError presents the selected Chat error status, kind, and message
	// through Gemini HTTP output. Callers retain the returned error for the
	// attempt result and terminal handler decision.
	ChatError(int, string, string) error
	// GeminiCustomCodeSkippedError presents a custom-code miss or uses the
	// supplied default error callback. Its platform policy decides
	// presentation and observation, and callers propagate the returned
	// terminal error.
	GeminiCustomCodeSkippedError(*gatewayadapter.ExecutionProvider, int, string, []byte, func()) error
	// GeminiNativeUpstreamError presents native upstream failure using
	// provider, response, and captured bytes. OAuth controls body
	// unwrapping, and the caller retains response-body cleanup after
	// synchronous presentation.
	GeminiNativeUpstreamError(*gatewayadapter.ExecutionProvider, *http.Response, []byte, string, bool) error
	// GeminiMappedError maps upstream status and bytes into a Messages
	// error with provider diagnostics. The HTTP adapter applies bound
	// presentation rules and commitment before returning its terminal
	// error.
	GeminiMappedError(*gatewayadapter.ExecutionProvider, int, string, []byte) error
	// GeminiOpenAICompatMappedError presents captured Gemini failure in the
	// selected OpenAI protocol. The provider and request identity support
	// diagnostics and rule matching, while callers retain cleanup of the
	// inspected response.
	GeminiOpenAICompatMappedError(*gatewayadapter.ExecutionProvider, int, string, []byte, gemini.OpenAICompatProtocol) error
	// GeminiOpenAICompatError presents a safe error in the selected OpenAI
	// client protocol. Status and kind are supplied by the attempt, and its
	// returned error remains part of forwarding failure handling.
	GeminiOpenAICompatError(gemini.OpenAICompatProtocol, int, string, string) error
	// AntigravityCompatError presents safe compatible error output through
	// the Antigravity adapter. The method selects that platform
	// independently of generic output selection and returns its committed
	// presentation error.
	AntigravityCompatError(int, string, string) error
	// MappedAntigravityCompatError maps captured upstream failure through
	// Antigravity compatible output and rule matching. Provider and request
	// identity inform diagnostics, and callers propagate the returned
	// presentation error to orchestration.
	MappedAntigravityCompatError(*gatewayadapter.ExecutionProvider, int, string, []byte) error
	// MapAntigravityCollectionError classifies errors from buffered
	// Antigravity stream collection. Cancellation and failover can pass
	// through; other failures may present an error response, so callers
	// propagate the returned classification.
	MapAntigravityCollectionError(error) error
	// MappedClaudeError maps captured Antigravity failure into Messages
	// output and bound presentation rules. The supplied provider and
	// request identity inform diagnostics, and its returned error ends this
	// response path.
	MappedClaudeError(*gatewayadapter.ExecutionProvider, int, string, []byte) error
}

// Gemini retains the services used to prepare and execute selected-provider
// attempts.
type Gemini struct {
	// Attributes, Tokens, Health, Errors, Transport, HeaderFilter, and
	// Enter retain application services or callbacks. Forwarding borrows
	// them through response reading, and their lifetime is managed by the
	// application.
	//
	// final model attribute reader
	Attributes *routing.ModelAttributeService
	Options    Options // startup platform forwarding values
	// selected Gemini credential source
	Tokens *provider.GeminiTokenSource
	// explicit provider policy observer
	Health *provideradapter.UpstreamHealth
	// Gemini default error observer
	Errors *provideradapter.GeminiErrorObserver
	// upstream HTTP exchange transport
	Transport httpclient.UpstreamTransport
	// response header filtering policy
	HeaderFilter *egress.CompiledHeaderFilter
	// toolCalls binds client call IDs to Gemini metadata across requests.
	// The zero value is ready to use for the lifetime of this runtime.
	toolCalls session.GeminiToolCalls
	// Enter is optional; a successful admission returns the release
	// callback that the platform executor holds through response-body
	// cleanup.
	//
	// application attempt admission callback
	Enter func() (func(), error)
}

// Antigravity retains shared services and builds platform inputs for each
// selected provider.
type Antigravity struct {
	// Attributes, Tokens, Retry, Errors, Store, Transport, Gateway,
	// Routing, Sticky, and Enter retain application-owned services or
	// callbacks. Each forwarding call borrows them while it builds and
	// executes attempt inputs.
	//
	// final model attribute reader
	Attributes *routing.ModelAttributeService
	Options    Options // startup platform forwarding values
	// selected Antigravity credential source
	Tokens *provider.AntigravityTokenSource
	// shared provider retry service
	Retry *provideradapter.AntigravityRetry
	// Antigravity upstream error observer
	Errors *provideradapter.AntigravityErrorObserver
	// provider model cooldown store
	Store gatewayadapter.ExecutionProviderStore
	// upstream HTTP exchange transport
	Transport httpclient.UpstreamTransport
	// request-time gateway policy reader
	Gateway *gateway.RuntimeSettings
	Routing *routing.RuntimeSettings // request-time model route settings
	Sticky  session.GatewayCache     // session provider binding cache
	// Enter is optional; a successful admission returns the release
	// callback that the platform executor holds through response-body
	// cleanup.
	//
	// application attempt admission callback
	Enter func() (func(), error)
}

// attempt retains request-local tool restoration and image observations for
// platform forwarding.
type attempt struct {
	// Output retains the active request presentation adapter for
	// synchronous forwarding; callers keep its context and writer usable
	// through all response callbacks.
	Output // borrowed HTTP presentation adapter
	// ToolNames is replaced during compatibility preparation and borrowed
	// by response callbacks, which restore names for this same synchronous
	// attempt.
	//
	// response tool restoration mapping
	ToolNames *anthropic.ToolNameRewrite
	// Images starts at zero for each attempt; observeImages retains the
	// maximum payload count, and imageCount retains zero when no output was
	// observed for this attempt.
	Images int // largest observed image count
}

// LogConfig returns enabled diagnostic logging and its UTF-8 byte cap. Logging
// requires Configured and LogErrorBody; the cap is 2048 unless the supplied
// positive option selects another value.
func (o Options) LogConfig() (bool, int) {
	n := 2048
	if o.LogErrorBodyMaxBytes > 0 {
		n = o.LogErrorBodyMaxBytes
	}
	return o.Configured && o.LogErrorBody, n
}

// ErrorDetail returns a UTF-8 error-body prefix when diagnostic logging is
// enabled. Disabled logging returns empty, and LogConfig supplies the positive
// default cap for body detail.
func (o Options) ErrorDetail(body []byte) string {
	enabled, n := o.LogConfig()
	if !enabled {
		return ""
	}
	return logredact.TruncateUTF8(string(body), n)
}

// reverseTools delegates response tool-name restoration using this attempt
// mapping.
func (a *attempt) reverseTools(body []byte) []byte {
	return anthropic.RestoreToolNamesInBytes(body, a.ToolNames)
}

// observeImages updates the attempt image observation from one response
// payload. It stores the maximum count across payloads, and smaller later
// counts leave the prior observation available.
//
// Cumulative SSE can repeat an image already counted by an earlier payload.
// Summing those payload counts would treat repetition as additional output,
// while the maximum retains the largest observed complete payload count. Sparse
// trailing usage or text payloads therefore keep the existing image observation
// for imageCount.
func (a *attempt) observeImages(body []byte) {
	body = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(body), []byte("data:")))
	if response := gjson.GetBytes(body, "response"); response.IsObject() {
		body = []byte(response.Raw)
	}
	if n := gemini.CountGeminiInlineImageOutputs(body); n > a.Images {
		a.Images = n
	}
}

// imageCount returns the largest observed image count for this attempt.
// Zero observations remain zero for every model identity, so model capability
// and request names cannot fabricate billable output when a response contains none.
func (a *attempt) imageCount(_, _ string) int {
	return a.Images
}

// messagesResult returns a new outer MessagesResult when forwarding
// observations exist. Reference-valued fields remain shared with the original
// result, and nil input returns nil for absent completion observations.
func messagesResult(value *forward.Result) *forward.MessagesResult {
	if value == nil {
		return nil
	}
	copy := forward.MessagesResult(*value)
	return &copy
}
