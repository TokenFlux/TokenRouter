package openaiforward

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// raw_fallback.go forwards Messages and Responses requests through Chat-only
// upstream endpoints and restores the client output format.
// Messages and Responses clients can use a Chat-only upstream through different
// conversion routes. Responses conversion saves client-tool names, tool-search
// presence, and namespace mappings before lowering them to function tools,
// allowing output readers to reconstruct the caller's tool representation after
// the Chat exchange.
//
// Messages conversion carries its converted effort and optionally applies group
// policy; Responses conversion refreshes plaintext reasoning history before
// resolving encrypted-only copies. Both paths retain a supplied service tier
// before fast-policy preparation and consult the final body only when that tier
// was absent. The exchange and error presenter share the selected provider,
// while client output and any partial observations use the corresponding
// conversion reader.

// MessagesViaRawChat converts Messages input to one Chat exchange and restores
// Messages output. It requires a model, validates incoming effort, maps billing
// and vendor identities, and requests stream usage when the client streams. The
// runner closes the response after reading or final failure presentation.
//
// Converted effort is retained as a pointer, including empty text; adopted
// nonempty group-policy effort can replace it. A present input service tier
// remains the reader's tier hint, while an absent one is derived after fast
// preparation. Reader observations are converted back to Result even when
// accompanied by an error, leaving completion eligibility to the caller.
func MessagesViaRawChat(ctx context.Context, body []byte, defaultMappedModel string, p RawFallbackPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	var anthropicReq protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	originalModel := anthropicReq.Model
	if strings.TrimSpace(originalModel) == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	if err := requeststate.ValidateOpenAIReasoningEffort(body, originalModel); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	clientStream := anthropicReq.Stream
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	anthropicReq.Model = upstreamModel

	// Convert Messages directly to Chat so the selected Chat-only endpoint
	// receives its expected history and tool-call representation before model and
	// effort controls are applied.
	chatReq, err := p.AnthropicToChat(&anthropicReq)
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert anthropic to chat completions: %w", err)
	}

	chatReq.Model = upstreamModel
	chatReq.ReasoningEffort = p.MessagesEffort(&anthropicReq, upstreamModel, chatReq.ReasoningEffort)
	chatReq.Stream = clientStream
	// Request Chat stream usage for streaming clients so conversion readers can
	// retain final token evidence while restoring the caller's output format.
	if clientStream {
		chatReq.StreamOptions = &protocolopenai.ChatStreamOptions{IncludeUsage: true}
	}

	convertedEffort := chatReq.ReasoningEffort
	reasoningEffort := &convertedEffort
	reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)
	serviceTier := requeststate.ExtractOpenAIServiceTierFromBody(body)

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions request: %w", err)
	}
	chatBody, err = p.PrepareModel(ctx, chatBody)
	if err != nil {
		return nil, err
	}
	// Apply admitted Messages effort policy after conversion, and replace the
	// recorded effort only when an adopted body supplies a nonempty effective
	// control.
	if profile.OpenAI {
		policyBody, changed, policyErr := p.ApplyEffort(ctx, chatBody)
		if policyErr != nil {
			return nil, policyErr
		}
		if changed {
			chatBody = policyBody
			if effectiveEffort := strings.TrimSpace(gjson.GetBytes(chatBody, "reasoning_effort").String()); effectiveEffort != "" {
				reasoningEffort = &effectiveEffort
			}
		}
	}
	chatBody, err = p.FastFallback(ctx, upstreamModel, chatBody)
	if err != nil {
		return nil, err
	}
	if serviceTier == nil {
		serviceTier = requeststate.ExtractOpenAIServiceTierFromBody(chatBody)
	}

	p.Debug("openai messages: forwarding via raw chat completions",
		zap.Int64("provider_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// Use the shared Chat transport to build and send the request.
	apiKey, targetURL, err := p.Target(ctx)
	if err != nil {
		return nil, err
	}
	p.Endpoint("/v1/chat/completions")
	resp, err := p.SendCC(ctx, targetURL, chatBody, clientStream, apiKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// The shared Anthropic error handler applies passthrough rules, Ops observations,
		// and cyber-policy detection after failover checks.
		return p.AnthropicError(resp, billingModel)
	}

	output := upstream.NewDeferredOutputContext(p.Sink())
	options := p.RawOptions(resp, billingModel, upstreamModel, serviceTier)
	var result *openai.CompatResponseResult
	if clientStream {
		result, err = openai.ReadCCAsMessagesStreaming(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime)
	} else {
		result, err = openai.ReadCCAsMessagesBuffered(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime)
	}
	return FromCompatResult(result, billingModel), err
}

// ResponsesViaRawChat converts Responses input to one Chat exchange and
// restores Responses output. It saves custom, function, tool-search, and
// namespace identities before lowering tools, then refreshes plaintext
// reasoning history for conversion lookups. Missing models and conversion
// errors are rejected before exchange.
//
// Client streaming requests upstream usage and selects the matching response
// converter. Recorded effort comes from the final outgoing body, while a
// supplied service tier remains the reader's hint unless it was absent. The
// runner closes the response after processing and returns any converted
// observations beside their reader error.
func ResponsesViaRawChat(ctx context.Context, body []byte, p RawFallbackPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	var responsesReq protocolopenai.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := strings.TrimSpace(responsesReq.Model)
	if originalModel == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	clientStream := responsesReq.Stream
	serviceTier := requeststate.ExtractOpenAIServiceTierFromBody(body)
	// Cache plaintext summaries before resolving the converted history so
	// encrypted-only copies with the same item identity can obtain the
	// corresponding reasoning content.
	p.RecacheInput(responsesReq.Input)
	chatReq, mapping, err := bridge.ResponsesToChatCompletionsRequestWithMapping(&responsesReq, &bridge.ResponsesToChatOptions{
		ReasoningContentByID: p.ReasoningContent,
	})
	if err != nil {
		p.Error(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}
	// Response readers receive the exact mapping produced with this Chat request,
	// keeping discovery aliases and default namespaces consistent across the round trip.
	customTools := mapping.CustomTools
	functionTools := mapping.FunctionTools
	toolSearch := mapping.ToolSearch
	namespaceTools := mapping.NamespaceTools

	billingModel := p.BillingModel(originalModel, "")
	upstreamModel := p.UpstreamModel(billingModel)
	chatReq.Model = upstreamModel
	// Request Chat stream usage for streaming clients so conversion readers can
	// retain final token evidence while restoring the caller's output format.
	if clientStream {
		chatReq.StreamOptions = &protocolopenai.ChatStreamOptions{IncludeUsage: true}
	}

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions fallback request: %w", err)
	}
	chatBody, err = p.FastFallback(ctx, upstreamModel, chatBody)
	if err != nil {
		return nil, err
	}
	// Usage records use effort from the final Chat request after conversion and policy processing.
	reasoningEffort := requeststate.ExtractOpenAIReasoningEffortFromBody(chatBody)
	// Record a default display effort for supported Kimi, Zhipu, and DeepSeek
	// models with thinking enabled and no explicit effort.
	reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(reasoningEffort, chatBody, billingModel)
	if serviceTier == nil {
		serviceTier = requeststate.ExtractOpenAIServiceTierFromBody(chatBody)
	}

	p.Debug("openai responses: forwarding via raw chat completions",
		zap.Int64("provider_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)
	p.ObserveModel(upstreamModel)

	// Use the shared Chat transport to build and send the request.
	apiKey, targetURL, err := p.Target(ctx)
	if err != nil {
		return nil, err
	}
	p.Endpoint("/v1/chat/completions")
	resp, err := p.SendCC(ctx, targetURL, chatBody, clientStream, apiKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return p.ResponsesError(ctx, resp, chatBody, billingModel)
	}

	output := upstream.NewDeferredOutputContext(p.Sink())
	options := p.RawOptions(resp, billingModel, upstreamModel, serviceTier)
	var result *openai.CompatResponseResult
	if clientStream {
		result, err = openai.ReadCCAsResponsesStreaming(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime, customTools, functionTools, toolSearch, namespaceTools)
	} else {
		result, err = openai.ReadCCAsResponsesBuffered(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime, customTools, functionTools, toolSearch, namespaceTools)
	}
	return FromCompatResult(result, billingModel), err
}
