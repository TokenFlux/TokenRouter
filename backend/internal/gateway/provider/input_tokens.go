package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"net/url"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

type InputTokensPrepared struct {
	Request         tokenestimate.Request
	OriginalModel   string
	NormalizedModel string
	BillingModel    string
	UpstreamModel   string
}

func PrepareAnthropicInputTokens(
	body []byte,
	account *ExecutionAccount,
	defaultMappedModel string,
) (*InputTokensPrepared, error) {
	var anthropicReq protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic count_tokens request: %w", err)
	}

	originalModel := anthropicReq.Model
	ApplyOpenAICompatModelNormalization(&anthropicReq)
	normalizedModel := anthropicReq.Model
	billingModel := ExecutionModelPolicy(account).ForwardModel(normalizedModel, strings.TrimSpace(defaultMappedModel))
	upstreamModel := ExecutionModelPolicy(account).NormalizeOpenAI(billingModel)

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
func PrepareNativeInputTokens(body []byte, account *ExecutionAccount) (*InputTokensPrepared, error) {
	var req tokenestimate.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse responses input_tokens request: %w", err)
	}
	originalModel := strings.TrimSpace(req.Model)
	if originalModel == "" {
		return nil, fmt.Errorf("parse responses input_tokens request: model is required")
	}
	billingModel := ExecutionModelPolicy(account).ForwardModel(originalModel, "")
	upstreamModel := ExecutionModelPolicy(account).NormalizeOpenAI(billingModel)
	req.Model = upstreamModel
	return &InputTokensPrepared{
		Request:         req,
		OriginalModel:   originalModel,
		NormalizedModel: originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
	}, nil
}
func EstimateInputTokensLocally(account *ExecutionAccount) bool {
	if account == nil || account.View().IsGrok() || account.View().IsCNProvider() || account.Record.Type == capability.AccountTypeUpstream {
		return true
	}
	if account.Record.Type != capability.AccountTypeAPIKey {
		return false
	}
	baseURL := strings.TrimSpace(account.View().GetCredential("base_url"))
	if baseURL == "" {
		return false
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	return !strings.EqualFold(parsed.Hostname(), "api.openai.com")
}
