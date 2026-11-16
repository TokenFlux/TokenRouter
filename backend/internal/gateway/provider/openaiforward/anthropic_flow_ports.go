package openaiforward

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// anthropic_flow_ports.go defines the request and output operations used for
// Anthropic-compatible forwarding.
// Anthropic-compatible providers accept one Messages exchange while clients may
// request Messages, Responses, or Chat output. The request adapter supplies
// protocol credentials and preparation rules, and the chosen output options
// bind header handling, tool restoration, bounded reading, and timeout
// observations to the same attempt.

// NativeAnthropicPorts supplies request preparation and output bindings for one native exchange.
type NativeAnthropicPorts interface {
	// BillingModel selects the charge model using provider mapping and a caller
	// default.
	BillingModel(model, fallback string) string
	// Debug writes the supplied structured diagnostic fields through the execution
	// logger.
	Debug(msg string, fields ...zap.Field)
	// ErrorResponse applies shared Messages failure rules and returns any
	// accompanying observation. It can produce client output or a recovery error;
	// the execution caller retains final response-body cleanup.
	ErrorResponse(r *http.Response, model string) (*Result, error)
	// FailoverHTTP applies provider failure policy before final client error
	// presentation. A nonnil result returns recovery guidance to the outer
	// provider loop; nil keeps the current response for its protocol presenter.
	FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error
	// PrepareTransport resolves the current provider proxy before its next request
	// exchange.
	PrepareTransport()
	// ReadUpstreamError captures bounded upstream error bytes and closes the
	// consumed network body. The installed adapter replaces the body for later
	// presentation and returns a query-sanitized explanation alongside captured
	// bytes.
	ReadUpstreamError(r *http.Response) ([]byte, string)
	// Sink returns the synchronous client output destination for this provider
	// attempt.
	Sink() upstream.OutputSink
	// UpstreamModel normalizes the charge model for the selected upstream
	// provider.
	UpstreamModel(model string) string
	// Profile returns the selected provider's platform, credential, and protocol
	// capability snapshot.
	Profile() MessagesProfile
	// Error writes the selected client protocol's local request failure response.
	Error(status int, kind, message string)
	// PrepareModel applies selected-model attributes to prepared Anthropic
	// Messages request bytes.
	PrepareModel(ctx context.Context, body []byte, model string) ([]byte, error)
	// ThinkingFallback retains a present effort pointer, including an empty
	// string. Otherwise it derives a display effort from enabled thinking and the
	// supplied model for result attribution after protocol preparation.
	ThinkingFallback(effort *string, body []byte, model string) *string
	// StripEmpty removes empty text blocks from the prepared Anthropic request
	// bytes.
	StripEmpty(body []byte) []byte
	// FilterSearch removes search-history blocks incompatible with the mapped
	// model's thinking policy.
	FilterSearch(body []byte, model string) []byte
	// CacheLimit applies the Anthropic cache-control validity and breakpoint
	// limiting rules.
	CacheLimit(body []byte) []byte
	// CacheTTL rewrites existing ephemeral cache-control markers to one hour when
	// the authenticated API Key and target upstream model qualify for Claude 1h
	// caching. Ineligible targets or passback models return the supplied body unchanged.
	CacheTTL(ctx context.Context, body []byte, model string) []byte
	// Log writes the supplied formatting message through the bound gateway logger.
	Log(format string, args ...any)
	// ProtocolAPIKey reads the configured Anthropic-compatible credential for the
	// selected provider.
	ProtocolAPIKey() string
	// TargetURL returns the selected provider's validated Anthropic Messages
	// endpoint address.
	TargetURL() (string, error)
	// StreamContext chooses upstream cancellation behavior from the requested
	// streaming mode. Call its release after construction; stream requests detach
	// client cancellation while nonstream requests retain their supplied context
	// under the shared adapter.
	StreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc)
	// BuildNative creates an authenticated Anthropic request from the prepared
	// body and target. Construction may sanitize beta-dependent fields; the runner
	// borrows the resulting request until exchange and owns response cleanup.
	BuildNative(ctx context.Context, body []byte, key, url string) (*http.Request, error)
	// SendNative exchanges through the selected provider and proxy after
	// PrepareTransport. A successful response remains open for native or
	// conversion output; the runner closes its body after reading or failure
	// presentation.
	SendNative(r *http.Request) (*http.Response, error)
	// TransportErrorNative presents exchange failure using the native passthrough
	// diagnostic marker.
	TransportErrorNative(ctx context.Context, err error) error
	// DirectOptions returns request-bound reading and observation callbacks for
	// native Messages output.
	DirectOptions() NativeAnthropicOptions
	// AdaptResponsesTools lowers client tools before Responses-to-Anthropic
	// conversion and returns their restoration mapping. An error stops request
	// conversion, and the mapping is retained through successful response
	// processing.
	AdaptResponsesTools(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error)
	// ResponsesToAnthropic converts the supplied Responses request for native
	// Anthropic execution.
	ResponsesToAnthropic(r *protocolopenai.ResponsesRequest) (*protocolanthropic.AnthropicRequest, error)
	// ChatToResponses resolves model capabilities and converts Chat to Responses
	// using their options. Capability publication can precede a later conversion
	// failure; callers return errors before constructing the upstream request.
	ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error)
	// ResponsesEffort extracts recorded Responses effort using the supplied model
	// normalization hints.
	ResponsesEffort(body []byte, models ...string) *string
	// ChatEffort extracts recorded Chat effort using the supplied model
	// normalization hints.
	ChatEffort(body []byte, models ...string) *string
	// MapStatus selects the client status for a failed native conversion request.
	MapStatus(status int) int
	// OutputOptions returns request-bound conversion callbacks selected for the
	// client error format.
	OutputOptions() AnthropicOutputOptions
}
