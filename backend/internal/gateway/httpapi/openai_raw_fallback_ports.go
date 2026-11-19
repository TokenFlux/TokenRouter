package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_raw_fallback_ports.go binds converted Messages and Responses requests
// to Chat-only upstream execution.
// Messages and Responses input can use a Chat-only upstream after conversion,
// with their own client error formats and reasoning history. Shared attempt
// state supplies the provider, TLS selection and request observations, while
// the raw fallback adapter delegates model preparation and response reading to
// the selected protocol runner.

// openAIRawFallbackAdapter binds Chat-only exchanges to Messages or Responses
// client output.
type openAIRawFallbackAdapter struct {
	// openAIMessagesExecutionAdapter retains the selected provider and request
	// services shared with the fallback runner until synchronous output finishes.
	*openAIMessagesExecutionAdapter
	// kind selects Messages rejection for NativeMessages; other fallback kinds
	// use the Responses error writer selected by errorWriter.
	kind openaiexecution.NativeAnthropicKind
}

// Profile describes the current provider for the raw fallback runner.
func (p *openAIRawFallbackAdapter) Profile() openaiexecution.MessagesProfile {
	return openaiexecution.MessagesProfile{Profile: openAIForwardProfile(p.provider), ID: p.provider.Record.ID}
}

// errorWriter selects Anthropic rejection for NativeMessages and Responses
// JSON for other fallback kinds. Callers use the selected writer while the
// same live Gin context owns the client response for this attempt.
func (p *openAIRawFallbackAdapter) errorWriter() func(*gin.Context, int, string, string) {
	if p.kind == openaiexecution.NativeMessages {
		return WriteForwardAnthropicError
	}
	return WriteForwardResponsesFallbackError
}

// Error writes a rejection in this raw fallback's client protocol.
func (p *openAIRawFallbackAdapter) Error(status int, kind, message string) {
	p.errorWriter()(p.c, status, kind, message)
}

// PrepareModel applies selected-provider model attributes for Chat Completions
// request bytes. The request helper extracts the body model and delegates
// preparation; errors return before the runner obtains its target or sends the
// fallback request.
func (p *openAIRawFallbackAdapter) PrepareModel(ctx context.Context, body []byte) ([]byte, error) {
	return p.s.Requests.applyModelAttributes(ctx, p.provider, body, "openai_chat_completions")
}

// FastFallback applies the current provider fast-tier policy to the outgoing
// Chat body. A BlockedError writes the dedicated heartbeat-aware policy
// rejection; callers terminate forwarding on the returned error rather than
// sending the rejected tier.
func (p *openAIRawFallbackAdapter) FastFallback(ctx context.Context, m string, b []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(b, p.s.FastPolicy.Input(ctx, p.provider, m))
	if blocked, ok := errors.AsType[*tierpolicy.BlockedError](err); ok {
		WriteFastPolicyBlockedResponse(p.c, blocked)
	}
	return updated, err
}

// RecacheInput refreshes reasoning text under recognized item IDs before
// conversion. Absent storage, invalid input arrays and cache write failures
// leave forwarding available; the history service handles its own write
// budget and logging.
func (p *openAIRawFallbackAdapter) RecacheInput(b json.RawMessage) {
	p.s.Output.Reasoning.FromInput(b)
}

// ReasoningContent looks up cached reasoning text by the supplied item
// identifier.
func (p *openAIRawFallbackAdapter) ReasoningContent(id string) string {
	return p.s.Output.Reasoning.Lookup(id)
}

// ObserveModel records the chosen upstream model in the request's Ops context.
func (p *openAIRawFallbackAdapter) ObserveModel(m string) { SetOpsUpstreamModel(p.c, m) }

// Target delegates Chat fallback endpoint and credential resolution for the
// selected provider.
func (p *openAIRawFallbackAdapter) Target(ctx context.Context) (string, string, error) {
	return p.s.Requests.ChatFallbackTarget(ctx, p.provider)
}

// Endpoint publishes the actual Chat fallback endpoint for request
// observation.
func (p *openAIRawFallbackAdapter) Endpoint(v string) {
	SetActualOpenAIUpstreamEndpoint(p.c, v)
}

// SendCC clears model observation and sends the prepared fallback Chat body
// with the provider's User-Agent and TLS matches. The runner owns a successful
// response body and closes it after output or failure handling.
func (p *openAIRawFallbackAdapter) SendCC(ctx context.Context, url string, b []byte, stream bool, key string) (*http.Response, error) {
	resetResponseModel(p.c)
	return p.s.Requests.SendChat(ctx, p.c, p.provider, url, b, stream, key, p.provider.View().GetOpenAIUserAgent(), "", p.tls...)
}

// AnthropicError delegates captured HTTP failure handling to the
// Messages-compatible writer. It converts any returned result for the fallback
// runner; the caller retains body cleanup and honors a returned failover
// signal before another provider attempt.
func (p *openAIRawFallbackAdapter) AnthropicError(r *http.Response, m string) (*openaiexecution.Result, error) {
	v, e := p.s.messagesError(r, p.c, p.provider, m)
	return openaiexecution.FromForwardResult(v), e
}

// ResponsesError delegates HTTP failure handling to Responses output using the
// captured request bytes. It converts the returned observation for the runner;
// response cleanup and actual provider-loop retry decisions remain with the
// execution caller.
func (p *openAIRawFallbackAdapter) ResponsesError(ctx context.Context, r *http.Response, b []byte, m string) (*openaiexecution.Result, error) {
	v, e := p.s.Output.ResponseError(ctx, r, p.c, p.provider, b, m)
	return openaiexecution.FromForwardResult(v), e
}

// RawOptions binds Chat reading to the current fallback client error writer.
// It passes no provider to RawOptions, so provider-specific raw transforms and
// missing-Grok-usage checks use that nil-provider input while common request
// observations remain bound.
func (p *openAIRawFallbackAdapter) RawOptions(r *http.Response, billing, model string, tier *string) openai.RawResponseOptions {
	return p.s.Output.RawOptions(p.c, r, nil, billing, model, tier, p.errorWriter())
}

// AnthropicToChat resolves model capabilities and delegates Messages
// conversion to Chat. Successful resolution installs provider capabilities
// before serializing the request; a later serialization or conversion failure
// can therefore leave that request-local provider update visible.
func (p *openAIRawFallbackAdapter) AnthropicToChat(r *protocolanthropic.AnthropicRequest) (*protocolopenai.ChatCompletionsRequest, error) {
	result, err := p.modelResolution(r.Model, r, "openai_chat_completions")
	if err != nil {
		return nil, err
	}
	return protocolbridge.AnthropicToChatCompletionsRequest(r, gatewayprovider.ModelConversionOptions(result))
}
