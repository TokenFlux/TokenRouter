package openaiforward

import (
	"context"
	"encoding/json"
	"net/http"

	"go.uber.org/zap"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// raw_fallback_ports.go defines conversion, model policy, transport, and
// output callbacks for Chat-only upstream attempts.
// Chat-only targets need Messages or Responses conversion before exchange and
// matching conversion after response reading. The fallback bindings retain
// mapped models, outgoing reasoning controls, saved plaintext reasoning, and
// protocol-specific error presentation while sharing the selected attempt's
// transport and output services.

// RawFallbackPorts supplies the selected attempt's Chat fallback and output operations.
type RawFallbackPorts interface {
	// AnthropicToChat resolves model capabilities and converts the supplied
	// Messages request to Chat. Capability publication can precede a later
	// conversion failure, and callers stop before exchange when conversion returns
	// an error.
	AnthropicToChat(*protocolanthropic.AnthropicRequest) (*protocolopenai.ChatCompletionsRequest, error)
	// Profile returns the selected provider's platform, credential, and protocol
	// capability snapshot.
	Profile() MessagesProfile
	// Error writes the selected client protocol's local request failure response.
	Error(int, string, string)
	// BillingModel selects the charge model using provider mapping and a caller
	// default.
	BillingModel(string, string) string
	// UpstreamModel normalizes the charge model for the selected upstream
	// provider.
	UpstreamModel(string) string
	// MessagesEffort resolves converted reasoning effort from Anthropic controls
	// and model attributes.
	MessagesEffort(*protocolanthropic.AnthropicRequest, string, string) string
	// PrepareModel applies selected-provider model attributes to the outgoing Chat
	// request bytes.
	PrepareModel(context.Context, []byte) ([]byte, error)
	// ApplyEffort applies group reasoning limits to converted outgoing request
	// bytes. Its changed result identifies adopted controls; a policy error writes
	// the bound client rejection and stops forwarding.
	ApplyEffort(context.Context, []byte) ([]byte, bool, error)
	// FastFallback applies provider fast-tier policy to the outgoing Chat request
	// bytes.
	FastFallback(context.Context, string, []byte) ([]byte, error)
	// Debug writes the supplied structured diagnostic fields through the execution
	// logger.
	Debug(string, ...zap.Field)
	// RecacheInput records plaintext reasoning from Responses history before Chat
	// conversion runs.
	RecacheInput(json.RawMessage)
	// ReasoningContent looks up saved reasoning text for an opaque response item
	// identifier.
	ReasoningContent(string) string
	// ObserveModel records the mapped upstream model for this request attempt.
	ObserveModel(string)
	// Target returns the Chat-compatible credential and endpoint for the selected
	// provider.
	Target(context.Context) (string, string, error)
	// Endpoint records the actual selected upstream request path in request
	// observations.
	Endpoint(string)
	// SendCC exchanges a converted Chat request using the provider and TLS state.
	// The runner closes a successful response body after client output or final
	// HTTP error handling finishes.
	SendCC(context.Context, string, []byte, bool, string) (*http.Response, error)
	// ReadUpstreamError captures bounded upstream error bytes and closes the
	// consumed network body. The installed adapter replaces the body for later
	// presentation and returns a query-sanitized explanation alongside captured
	// bytes.
	ReadUpstreamError(*http.Response) ([]byte, string)
	// FailoverHTTP applies provider failure policy before final client error
	// presentation. A nonnil result returns recovery guidance to the outer
	// provider loop; nil keeps the current response for its protocol presenter.
	FailoverHTTP(context.Context, *http.Response, []byte, string, string) error
	// AnthropicError presents HTTP failure through the Messages-compatible error
	// writer and returns its observation.
	AnthropicError(*http.Response, string) (*Result, error)
	// ResponsesError presents HTTP failure through Responses output using the
	// prepared body and model.
	ResponsesError(context.Context, *http.Response, []byte, string) (*Result, error)
	// Sink returns the synchronous client output destination for this provider
	// attempt.
	Sink() upstream.OutputSink
	// RawOptions binds response reading to this request and its client protocol.
	// The installed fallback adapter supplies no provider to the reader options,
	// so provider-specific raw transforms use that absent input.
	RawOptions(*http.Response, string, string, *string) openai.RawResponseOptions
}
