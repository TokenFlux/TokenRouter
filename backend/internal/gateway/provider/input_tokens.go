package provider

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// input_tokens.go prepares counting requests while retaining client, billing,
// and upstream model names.
// Input-token queries can count a provider-mapped model while the caller still
// needs the client's requested identity. Preparation retains both names before
// replacing the counting request's model, so diagnostics and later request
// handling can distinguish the input from the route submitted for counting.
//
// Anthropic content is converted to Responses counting fields using the
// execution bridge. Native Responses requests keep their decoded counting
// fields and receive the mapped, normalized model. Provider type and endpoint
// then determine whether counting stays local, including malformed custom
// URLs that cannot establish the official OpenAI counting host.

// InputTokensPrepared combines counting input with the model names used during
// preparation.
type InputTokensPrepared struct {
	Request tokenestimate.Request // converted upstream counting input
	// OriginalModel and NormalizedModel describe ingress, while BillingModel and
	// UpstreamModel record provider mapping and wire normalization. The counting
	// request uses UpstreamModel, leaving ingress identity available to its caller.
	OriginalModel   string // client model before mapping
	NormalizedModel string // preparation input model spelling
	BillingModel    string // provider-mapped billing model name
	UpstreamModel   string // final counting request model
}

// PrepareAnthropicInputTokens decodes an Anthropic count request and converts
// its content to Responses counting fields. Decode or bridge conversion
// failures return errors before a prepared value is available.
//
// Provider mapping uses a nonblank dispatch model before the request model,
// then normalizes the counting identity. OriginalModel and NormalizedModel
// retain the untrimmed Anthropic spelling, while BillingModel and UpstreamModel
// record the later stages. Request.Model uses UpstreamModel, so callers can
// submit the converted input while retaining the client's original name.
func PrepareAnthropicInputTokens(
	body []byte,
	provider *ExecutionProvider,
	defaultMappedModel string,
) (*InputTokensPrepared, error) {
	var anthropicReq protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic count_tokens request: %w", err)
	}

	originalModel := anthropicReq.Model
	normalizedModel := anthropicReq.Model
	effort := requeststate.ExtractRoutingReasoningEffort(body, protocol.ProtocolAnthropicMessages)
	billingModel := ExecutionModelPolicy(provider).WithEffort(effort).ForwardModel(normalizedModel, strings.TrimSpace(defaultMappedModel))
	upstreamModel := ExecutionModelPolicy(provider).NormalizeOpenAI(billingModel)

	// Count the converted Responses input, using the same Anthropic content and
	// tool conversion as execution while keeping billing and client model names separate.
	responsesReq, err := protocolbridge.AnthropicToResponses(&anthropicReq, protocolforward.ConversionOptionsForModel(anthropicReq.Model))
	if err != nil {
		return nil, fmt.Errorf("convert anthropic request to responses: %w", err)
	}

	return &InputTokensPrepared{
		Request: tokenestimate.Request{
			Model:        upstreamModel,
			Instructions: responsesReq.Instructions,
			Input:        responsesReq.Input,
			Tools:        responsesReq.Tools,
			ToolChoice:   responsesReq.ToolChoice,
		},
		OriginalModel:   originalModel,
		NormalizedModel: normalizedModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
	}, nil
}

// PrepareNativeInputTokens decodes a Responses count request and requires a
// nonblank model after trimming. Decode failure or a missing model returns an
// error before provider mapping begins.
//
// The trimmed client name is saved before Request.Model receives the
// provider-mapped and normalized identity. OriginalModel and NormalizedModel
// retain that client name, BillingModel records mapping, and UpstreamModel
// records normalization. Callers submit Request for counting and use the
// retained stages when reporting the model that entered preparation.
func PrepareNativeInputTokens(body []byte, provider *ExecutionProvider) (*InputTokensPrepared, error) {
	var req tokenestimate.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse responses input_tokens request: %w", err)
	}
	originalModel := strings.TrimSpace(req.Model)
	if originalModel == "" {
		return nil, fmt.Errorf("parse responses input_tokens request: model is required")
	}
	effort := requeststate.ExtractRoutingReasoningEffort(body, protocol.ProtocolOpenAIResponses)
	billingModel := ExecutionModelPolicy(provider).WithEffort(effort).ForwardModel(originalModel, "")
	upstreamModel := ExecutionModelPolicy(provider).NormalizeOpenAI(billingModel)
	req.Model = upstreamModel
	return &InputTokensPrepared{
		Request:         req,
		OriginalModel:   originalModel,
		NormalizedModel: originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
	}, nil
}

// EstimateInputTokensLocally selects local counting for absent targets,
// compatible platforms other than OpenAI, upstream-type targets, or custom API
// Key targets. Non-API-Key targets and API Keys
// with blank or official api.openai.com hosts use the upstream path; malformed
// custom URLs choose local counting.
func EstimateInputTokensLocally(provider *ExecutionProvider) bool {
	if provider == nil || provider.View().IsOpenAICompatible() && !provider.View().IsOpenAI() || provider.Record.Type == capability.ProviderTypeUpstream {
		return true
	}
	if provider.Record.Type != capability.ProviderTypeAPIKey {
		return false
	}
	baseURL := strings.TrimSpace(provider.View().GetCredential("base_url"))
	if baseURL == "" {
		return false
	}
	// A malformed custom URL cannot establish the official counting endpoint
	// identity, so parse failure selects local estimation along with other custom hosts.
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	return !strings.EqualFold(parsed.Hostname(), "api.openai.com")
}
