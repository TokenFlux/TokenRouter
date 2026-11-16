package openaiforward

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// anthropic_flows.go forwards Messages requests and converts OpenAI requests
// through Anthropic endpoints.
// Native Anthropic targets can serve Messages clients directly or convert
// Responses and Chat requests through Messages. Direct Messages retains the
// client stream choice, while both OpenAI conversion routes request an upstream
// stream and choose buffered or streaming conversion from the saved client
// preference.
//
// Billing and vendor models are kept separate from the client response
// identity. Responses tool lowering retains a restoration mapping until output
// conversion, while Chat passes through an intermediate Responses request. The
// selected attempt owns response cleanup and performs provider failure
// classification before client error presentation; conversion readers retain
// observed usage and the vendor-declared model in their result.

// NativeAnthropicKind values select the client error writer and output conversion
// used by adapters sharing one selected native Anthropic provider attempt.
const (
	NativeMessages  NativeAnthropicKind = iota // direct Messages output
	NativeResponses                            // Responses conversion output
	NativeChat                                 // Chat conversion output
)

// NativeAnthropicKind identifies the client protocol for a native Anthropic attempt.
type NativeAnthropicKind uint8

// ForwardNativeMessages executes a direct Messages attempt against the selected
// Anthropic-compatible endpoint. A nonblank model and protocol credential are
// required, model attributes are applied before text and search-history
// cleanup, and the client stream choice selects native output reading. The
// runner closes the successful response after output or error presentation.
// Output effort observed after model preparation is retained separately from
// its thinking-derived display value, and reader observations can accompany a
// failure.
func ForwardNativeMessages(ctx context.Context, body []byte, defaultMappedModel string, p NativeAnthropicPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := gjson.GetBytes(body, "stream").Bool()

	// Retain the caller model for output while patching the mapped vendor model
	// into Messages bytes; native preparation then evaluates controls for that
	// selected upstream identity.
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	if upstreamModel != originalModel {
		rewritten, err := sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model: %w", err)
		}
		body = rewritten
	}
	body, err := p.PrepareModel(ctx, body, upstreamModel)
	if err != nil {
		return nil, err
	}

	// Capture output_config.effort after model preparation. When no effort is
	// captured, enabled thinking can select high for passback families that
	// lack native effort; the result keeps that display default separate from
	// the captured request control.
	requestedReasoningEffort := protocol.NormalizeClaudeOutputEffort(gjson.GetBytes(body, "output_config.effort").String())
	reasoningEffort := p.ThinkingFallback(
		requestedReasoningEffort,
		body,
		billingModel,
	)

	// Apply the Messages passthrough filters: empty text and unsupported search history
	// can cause compatible upstreams to reject the request.
	body = p.StripEmpty(body)
	body = p.FilterSearch(body, upstreamModel)
	body = p.CacheTTL(ctx, body, upstreamModel)

	p.Log("[Messages passthrough] provider=%d(%s) platform=%s model=%s upstream=%s stream=%v",
		profile.ID, profile.Name, profile.Platform, originalModel, upstreamModel, clientStream)

	apiKey := strings.TrimSpace(p.ProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("provider %d missing api_key", profile.ID)
	}
	targetURL, err := p.TargetURL()
	if err != nil {
		return nil, err
	}

	p.PrepareTransport()

	upstreamCtx, releaseUpstreamCtx := p.StreamContext(ctx, clientStream)
	upstreamReq, err := p.BuildNative(upstreamCtx, body, apiKey, targetURL)
	releaseUpstreamCtx()
	if err != nil {
		return nil, err
	}

	resp, err := p.SendNative(upstreamReq)
	if err != nil {
		return nil, p.TransportErrorNative(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// Use the shared Anthropic error formatter, passthrough rules, Ops observations,
		// and cyber-policy detection when failover is not requested.
		return p.ErrorResponse(resp, billingModel)
	}

	if clientStream {
		result, err := NativeAnthropicStreaming(ctx, resp, upstream.NewDeferredOutputContext(p.Sink()), p.DirectOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
		if result != nil {
			result.RequestedReasoningEffort = requestedReasoningEffort
		}
		return result, err
	}
	result, err := NativeAnthropicBuffered(ctx, resp, upstream.NewDeferredOutputContext(p.Sink()), p.DirectOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
	if result != nil {
		result.RequestedReasoningEffort = requestedReasoningEffort
	}
	return result, err
}

// ForwardNativeResponses converts Responses input for a native Anthropic
// exchange and restores Responses output. Client tools are lowered before typed
// conversion and their mapping stays with the output reader so tool identities
// can be reconstructed. A nonblank model and protocol key are required.
//
// The vendor exchange always requests a stream, while the saved client
// preference selects streaming output or buffered reconstruction. Empty text,
// search history, and cache breakpoints are filtered before construction. The
// runner owns response closure and returns failover guidance before protocol
// error output when health handling requests it.
func ForwardNativeResponses(ctx context.Context, body []byte, defaultMappedModel string, p NativeAnthropicPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	// Lower Responses client tools before typed conversion, retaining the mapping
	// that output readers need to reconstruct the caller's custom and namespace
	// tool forms.
	adaptedBody, clientToolMapping, err := p.AdaptResponsesTools(body)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to adapt request tools")
		return nil, fmt.Errorf("adapt responses client tools: %w", err)
	}

	var responsesReq protocolopenai.ResponsesRequest
	if err := json.Unmarshal(adaptedBody, &responsesReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := responsesReq.Model
	if strings.TrimSpace(originalModel) == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := responsesReq.Stream

	anthropicReq, err := p.ResponsesToAnthropic(&responsesReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// Retain the caller model for converted output while assigning the mapped
	// vendor model to the Anthropic request; the separate billing identity
	// remains available for completion.
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	anthropicReq.Model = upstreamModel

	reasoningEffort := p.ResponsesEffort(body, upstreamModel, billingModel, originalModel)
	reasoningEffort = p.ThinkingFallback(reasoningEffort, body, billingModel)

	// Request streaming Anthropic events for both client modes so conversion can
	// reconstruct content blocks and usage before choosing buffered JSON or
	// incremental client output.
	anthropicReq.Stream = true
	reqStream := true

	p.Debug("openai responses: forwarding via native anthropic endpoint",
		zap.Int64("provider_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("client_stream", clientStream),
	)

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// Remove empty text and incompatible search history, then enforce cache
	// breakpoint limits before native construction receives the converted
	// Anthropic body.
	anthropicBody = p.StripEmpty(anthropicBody)
	anthropicBody = p.FilterSearch(anthropicBody, upstreamModel)
	anthropicBody = p.CacheLimit(anthropicBody)

	apiKey := strings.TrimSpace(p.ProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("provider %d missing api_key", profile.ID)
	}
	targetURL, err := p.TargetURL()
	if err != nil {
		return nil, err
	}

	p.PrepareTransport()

	upstreamCtx, releaseUpstreamCtx := p.StreamContext(ctx, reqStream)
	upstreamReq, err := p.BuildNative(upstreamCtx, anthropicBody, apiKey, targetURL)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	resp, err := p.SendNative(upstreamReq)
	if err != nil {
		return nil, p.TransportErrorNative(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		p.Error(p.MapStatus(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	if clientStream {
		return ResponsesFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
	}
	return ResponsesFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
}

// ForwardNativeChat converts Chat through Responses into a native Anthropic
// request and restores Chat output. It saves the caller model, stream choice,
// and optional usage request before mapping billing and vendor identities. A
// nonblank model and protocol key are required before exchange.
//
// The vendor request always streams so either client mode can be produced by
// the corresponding conversion reader. Request cleanup removes empty text,
// search-history incompatibilities, and excess cache breakpoints before
// construction. The runner closes the response after reading and returns
// converted observations beside any reader error, leaving completion
// eligibility to its caller.
func ForwardNativeChat(ctx context.Context, body []byte, defaultMappedModel string, p NativeAnthropicPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	var ccReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := ccReq.Model
	if strings.TrimSpace(originalModel) == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := ccReq.Stream
	includeUsage := ccReq.StreamOptions != nil && ccReq.StreamOptions.IncludeUsage

	// Use the existing Chat-to-Responses conversion before Anthropic lowering so
	// the request shares the bridge's model-sensitive history and tool-call
	// representation.
	responsesReq, err := p.ChatToResponses(&ccReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}
	anthropicReq, err := p.ResponsesToAnthropic(responsesReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// Retain the caller model for converted output while assigning the mapped
	// vendor model to the Anthropic request; the separate billing identity
	// remains available for completion.
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	anthropicReq.Model = upstreamModel

	// Request streaming Anthropic events for both client modes so conversion can
	// reconstruct content blocks and usage before choosing buffered JSON or
	// incremental client output.
	anthropicReq.Stream = true
	reqStream := true

	p.Debug("openai chat_completions: forwarding via native anthropic endpoint",
		zap.Int64("provider_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("client_stream", clientStream),
	)

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// Remove empty text and incompatible search history, then enforce cache
	// breakpoint limits before native construction receives the converted
	// Anthropic body.
	anthropicBody = p.StripEmpty(anthropicBody)
	anthropicBody = p.FilterSearch(anthropicBody, upstreamModel)
	anthropicBody = p.CacheLimit(anthropicBody)

	apiKey := strings.TrimSpace(p.ProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("provider %d missing api_key", profile.ID)
	}
	targetURL, err := p.TargetURL()
	if err != nil {
		return nil, err
	}

	p.PrepareTransport()

	upstreamCtx, releaseUpstreamCtx := p.StreamContext(ctx, reqStream)
	upstreamReq, err := p.BuildNative(upstreamCtx, anthropicBody, apiKey, targetURL)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	resp, err := p.SendNative(upstreamReq)
	if err != nil {
		return nil, p.TransportErrorNative(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		p.Error(p.MapStatus(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	reasoningEffort := p.ChatEffort(body, upstreamModel, billingModel, originalModel)
	reasoningEffort = p.ThinkingFallback(reasoningEffort, body, billingModel)

	if clientStream {
		return ChatFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, includeUsage)
	}
	return ChatFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(p.Sink()), p.OutputOptions(), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
}
