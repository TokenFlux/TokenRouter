package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// openai_native_anthropic_ports.go binds native Anthropic execution to
// client-specific conversions and HTTP output.
// Native Anthropic execution uses the selected provider's protocol key, model
// policy and proxy while the client may speak Messages, Chat or Responses. The
// adapter's kind selects client errors and output conversion, and shared
// attempt state supplies credentials, request observations and synchronous
// output until the runner closes the upstream response.

// openAINativeAnthropicAdapter binds native Anthropic exchange to shared text
// attempt services.
type openAINativeAnthropicAdapter struct {
	// openAIMessagesExecutionAdapter shares this attempt's provider, services and
	// HTTP state; the native runner borrows them until response handling ends.
	*openAIMessagesExecutionAdapter
	// kind selects the client error and conversion format. The native entry
	// constructor supplies Messages, Chat or Responses for the current call.
	kind forward.NativeAnthropicKind
}

// errorWriter selects the client rejection writer from the native execution
// kind. NativeResponses uses Responses terminal handling, NativeChat uses Chat
// JSON, and other kinds use Anthropic output; the caller supplies the current
// live context.
func (p *openAINativeAnthropicAdapter) errorWriter() func(*gin.Context, int, string, string) {
	switch p.kind {
	case forward.NativeResponses:
		return writeTextResponsesError
	case forward.NativeChat:
		return WriteForwardChatError
	default:
		return WriteForwardAnthropicError
	}
}

// Profile describes the selected provider for the native Anthropic runner.
func (p *openAINativeAnthropicAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.provider), ID: p.provider.Record.ID}
}

// Error writes the rejection through the current native client protocol.
func (p *openAINativeAnthropicAdapter) Error(status int, kind, message string) {
	p.errorWriter()(p.c, status, kind, message)
}

// PrepareModel delegates attribute-sensitive body preparation for the
// Anthropic Messages protocol. The selected model and provider guide the
// shared attribute service; preparation errors return before the native runner
// constructs or sends its request.
func (p *openAINativeAnthropicAdapter) PrepareModel(ctx context.Context, body []byte, model string) ([]byte, error) {
	return gatewayprovider.PrepareModelAttributeParameters(ctx, p.provider, model, body, p.s.modelAttributes(), "anthropic_messages")
}

// ThinkingFallback returns supplied effort or the model's enabled-thinking
// display default. The provider helper retains a present pointer, including
// empty text; a derived value affects usage attribution rather than rewriting
// this body.
func (p *openAINativeAnthropicAdapter) ThinkingFallback(effort *string, body []byte, model string) *string {
	return gatewayprovider.ApplyThinkingEnabledFallback(effort, body, model)
}

// StripEmpty delegates removal of empty Anthropic text blocks from request
// bytes.
func (p *openAINativeAnthropicAdapter) StripEmpty(body []byte) []byte {
	return protocolanthropic.StripEmptyTextBlocks(body)
}

// FilterSearch delegates search-history cleanup using the mapped model's
// passback requirement. Passback-required models select stripAll for supported
// upstream and emulated search blocks; other models select the filter's
// synthetic-block-only rule, with failures handled by that helper.
func (p *openAINativeAnthropicAdapter) FilterSearch(body []byte, model string) []byte {
	return searchtools.FilterWebSearchHistoryBlocks(body, modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired)
}

// CacheLimit delegates Anthropic cache-control cleanup and breakpoint limiting
// for the body. The upstream helper removes invalid thinking breakpoints and
// excess tool, message and system anchors in its defined order before native
// execution.
func (p *openAINativeAnthropicAdapter) CacheLimit(body []byte) []byte {
	return anthropic.EnforceCacheControlLimit(body)
}

// CacheTTL rewrites existing ephemeral cache markers to a 1h lifetime when the
// authenticated API Key enables forced Claude 1h caching for the selected
// upstream model. Ineligible platforms and passback models keep their input bytes.
func (p *openAINativeAnthropicAdapter) CacheTTL(ctx context.Context, body []byte, model string) []byte {
	if !gatewayprovider.ShouldForceClaude1hCache(ctx, p.provider, model) {
		return body
	}
	return anthropic.InjectAnthropicCacheControlTTL1h(body)
}

// Log writes native forwarding diagnostics under the gateway logging
// component.
func (p *openAINativeAnthropicAdapter) Log(format string, args ...any) {
	logging.LegacyPrintf("service.gateway", format, args...)
}

// ProtocolAPIKey reads the provider's configured Anthropic-compatible protocol
// credential for execution.
func (p *openAINativeAnthropicAdapter) ProtocolAPIKey() string {
	return p.provider.View().GetOpenAIProtocolAPIKey()
}

// TargetURL resolves the native Anthropic endpoint through the configured
// request builder.
func (p *openAINativeAnthropicAdapter) TargetURL() (string, error) {
	return p.s.Requests.AnthropicURL(p.provider)
}

// StreamContext removes client cancellation and deadlines for streaming
// requests while retaining values and any server-abort signal. Nonstreaming
// input stays attached, and nil input becomes Background. The returned
// callback is a no-op that the runner invokes after request construction.
func (p *openAINativeAnthropicAdapter) StreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc) {
	return gatewayprovider.DetachStreamUpstreamContext(ctx, stream)
}

// BuildNative constructs the native Anthropic HTTP request using this
// attempt's request headers and provider. It returns the request or
// construction error; the runner supplies the derived upstream context and
// retains response cleanup after sending.
func (p *openAINativeAnthropicAdapter) BuildNative(ctx context.Context, body []byte, key, url string) (*http.Request, error) {
	r, _, err := p.s.Requests.BuildAnthropic(ctx, p.c, p.provider, body, key, url)
	return r, err
}

// SendNative clears request model observation before sending the prepared
// native request through the selected proxy. A successful response belongs to
// the runner, which closes its body after native reading or HTTP-error
// handling finishes.
func (p *openAINativeAnthropicAdapter) SendNative(r *http.Request) (*http.Response, error) {
	resetResponseModel(p.c)
	return p.s.Requests.Transport.Do(r, p.proxyURL, p.provider.Record.ID, p.provider.Record.Concurrency)
}

// TransportErrorNative delegates native exchange failure observation with the
// passthrough Ops marker. The shared handler preserves client cancellation and
// otherwise returns a sanitized provider failover signal; the outer text loop
// decides whether another attempt is allowed.
func (p *openAINativeAnthropicAdapter) TransportErrorNative(ctx context.Context, err error) error {
	return p.s.Requests.Failure.Handle(ctx, p.c, p.provider, err, true)
}

// DirectOptions binds native Messages output to this request and selected
// provider.
func (p *openAINativeAnthropicAdapter) DirectOptions() forward.NativeAnthropicOptions {
	return p.s.Output.AnthropicDirectOptions(p.c, p.provider)
}

// AdaptResponsesTools delegates Responses client-tool lowering for Anthropic
// request conversion. It returns the converted bytes and restoration mapping,
// or an error that prevents the native runner from forwarding an incompatible
// tool representation.
func (p *openAINativeAnthropicAdapter) AdaptResponsesTools(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error) {
	return protocolforward.AdaptResponsesClientToolsForAnthropic(body)
}

// ResponsesToAnthropic delegates pure Responses-to-Anthropic request
// conversion for native execution. Conversion errors reach the runner before
// request preparation, and subsequent model and protocol policies remain with
// that runner and its adapter.
func (p *openAINativeAnthropicAdapter) ResponsesToAnthropic(r *protocolopenai.ResponsesRequest) (*protocolanthropic.AnthropicRequest, error) {
	return bridge.ResponsesToAnthropicRequest(r)
}

// ChatToResponses delegates Chat conversion using model-derived conversion
// options for this request. The intermediate Responses representation then
// feeds native Anthropic conversion; an error ends that conversion path before
// the upstream exchange.
func (p *openAINativeAnthropicAdapter) ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error) {
	return bridge.ChatCompletionsToResponses(r, protocolforward.ConversionOptionsForModel(r.Model))
}

// ResponsesEffort extracts and normalizes recorded effort from a
// Responses-shaped body.
func (p *openAINativeAnthropicAdapter) ResponsesEffort(body []byte, models ...string) *string {
	return protocolforward.ExtractEffort(body, false, capability.NormalizeRecordedOpenAIEffortForModel, models...)
}

// ChatEffort extracts and normalizes recorded effort from a Chat-shaped body.
func (p *openAINativeAnthropicAdapter) ChatEffort(body []byte, models ...string) *string {
	return protocolforward.ExtractEffort(body, true, capability.NormalizeRecordedOpenAIEffortForModel, models...)
}

// MapStatus delegates upstream-to-client status mapping for native protocol
// conversion output.
func (p *openAINativeAnthropicAdapter) MapStatus(status int) int {
	return protocolforward.MapStatus(status)
}

// OutputOptions binds Anthropic conversion output using the client-kind
// rejection writer.
func (p *openAINativeAnthropicAdapter) OutputOptions() forward.AnthropicOutputOptions {
	return p.s.Output.AnthropicOptions(p.c, p.errorWriter())
}

// writeTextResponsesError writes a Responses conversion error and marks
// terminal output committed. It delegates formatting to
// ForwardConversionOutput with Responses enabled, so outer attempt handling
// sees the dedicated protocol rejection before further fallback decisions.
func writeTextResponsesError(c *gin.Context, status int, code, message string) {
	output := ForwardConversionOutput{Context: c, Responses: true, Commit: func() { MarkResponseCommitted(c) }}
	output.Error(status, code, message)
}
