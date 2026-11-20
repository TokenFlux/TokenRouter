package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// text_pricing.go checks each text attempt's effective billing model before
// forwarding to the selected provider. A text request can carry different
// requested, group-mapped and provider model names. Price preflight checks the
// completion billing choice after resolving this attempt's protocol, so a
// configured upstream or requested source cannot borrow a price from another
// name in that chain. Image generation models use the separate media admission
// rule.
//
// Compact can replace the ordinary upstream name through provider or global
// compaction settings, while passthrough and WebSocket completion supply
// different billing inputs. The shared completion selector combines those
// inputs with group mapping fields before the price service is queried for the
// effective group. Each provider attempt repeats this decision before
// forwarding begins.

// CheckTextModelPricing checks the billing model this attempt would use after
// protocol and model resolution. Nil pricing skips price lookup after output
// eligibility; image-only models use media pricing on supported entries. Supply mapped as the
// current group-routed name; a route plan supplies its mapping fields and
// requested name when present.
//
// Provider policy interprets mapped to obtain the upstream name, with Compact
// mappings applied for its eligible Responses profile. WebSocket and HTTP
// passthrough choose distinct completion billing inputs, then the shared
// completion selector applies the configured requested, group-mapped or
// upstream source. The resulting name is checked in the effective Key group,
// keeping that chosen source authoritative even when another name has a known
// price.
func CheckTextModelPricing(ctx context.Context, prices *admission.ModelPricing, key *apikey.APIKey, target *gatewayprovider.ExecutionProvider, requested, mapped string, compact *CompactExecutor, source protocol.ProtocolID) error {
	// Resolve the same candidate protocol used by execution before model policy
	// lookup, so conversion-specific mappings cannot price a different forwarding route.
	target, err := gatewayprovider.ProviderForProtocolAttempt(ctx, target)
	if err != nil {
		return err
	}
	mapping := routing.GroupMappingResult{MappedModel: mapped}
	if plan, ok := requeststate.RoutePlanFromContext(ctx); ok {
		mapping = plan.Mapping()
		requested = plan.Models().RequestedModel
	}
	policy := gatewayprovider.ExecutionModelPolicy(target).WithContext(ctx)
	result := &completion.Result{Model: mapped}
	if target.View().IsOpenAICompatible() {
		result.BillingModel, result.UpstreamModel = policy.ForwardMappedModels(mapped, compact != nil)
	} else if target.Record.Platform == provider.PlatformGemini {
		result.UpstreamModel = policy.Mapped(mapped)
	} else {
		result.UpstreamModel = policy.UpstreamModel(ctx, mapped)
	}
	switch source {
	case protocol.ProtocolResponsesWebSocket:
		result.BillingModel = ws.EntryBillingModel(nil, mapping, requested, result.UpstreamModel)
	case protocol.ProtocolOpenAIResponses:
		profile := openAIForwardProfile(target)
		if compact != nil && target.View().IsOpenAICompatible() && !profile.Grok && !profile.RawChat && !profile.Anthropic {
			// Resolve eligible Compact mappings before completion chooses its billing
			// source; provider compaction overrides can replace the configured global model.
			if model := compact.ResolveModel(target, mapped); model != "" {
				result.UpstreamModel = model
			}
		}
		if profile.Passthrough && !profile.RawChat && !profile.Anthropic {
			// HTTP passthrough completion uses the group-routed name in Model. Clear
			// the preliminary billing override before applying that same source selector.
			result.BillingModel = ""
		}
	}
	attributes, err := gatewayprovider.ResolveModelAttributes(ctx, target, result.UpstreamModel, nil)
	if err != nil {
		return err
	}
	target.Capabilities = &attributes
	if attributes.Attributes.OutputOnly("image") {
		if compact != nil || source == protocol.ProtocolResponsesCompact || source == protocol.ProtocolOpenAIChatCompletions || source == protocol.ProtocolAnthropicMessages {
			return apperror.BadRequest("MODEL_CAPABILITY_UNSUPPORTED", "the selected model route produces only images")
		}
		return nil
	}
	model := completion.OpenAIUsageBillingModel(result, mapping.ToUsageFields(requested, result.UpstreamModel))
	var groupID *int64
	if key != nil {
		groupID = key.GroupID
	}
	if prices == nil {
		return nil
	}
	return prices.Check(ctx, groupID, model)
}

// checkPricing validates selected-model output and pricing before execution.
// Capability and missing-price failures receive their local protocol errors. Compact paths use the bound CompactExecutor
// when available, otherwise its default instance. Other errors retain their
// cause; only price rejection records local policy limitation and writes the
// inbound protocol error.
func (e *UnifiedTextExecutor) checkPricing(c *gin.Context, target *gatewayprovider.ExecutionProvider, body []byte, source protocol.ProtocolID) error {
	model := gjson.GetBytes(body, "model").String()
	key, _ := EffectiveAPIKey(c)
	var compact *CompactExecutor
	if source == protocol.ProtocolOpenAIResponses && IsOpenAIResponsesCompactPath(c) {
		compact = &CompactExecutor{}
		if e.OpenAI != nil && e.OpenAI.Text != nil && e.OpenAI.Text.Compact != nil {
			compact = e.OpenAI.Text.Compact
		}
	}
	err := CheckTextModelPricing(c.Request.Context(), e.Pricing, key, target, model, model, compact, source)
	if reason := apperror.Reason(err); reason == "MODEL_CAPABILITY_UNKNOWN" || reason == "MODEL_CAPABILITY_UNSUPPORTED" || reason == "MODEL_ATTRIBUTES_UNAVAILABLE" {
		writeLocalTextError(c, source, apperror.FromError(err), reason)
		return err
	}
	if !errors.Is(err, admission.ErrModelPricingRejected) {
		return err
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	if source == protocol.ProtocolAnthropicMessages {
		DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, http.StatusBadRequest, "invalid_request_error", admission.ModelPricingUnavailableMessage, c.Writer.Written())
	} else {
		DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, http.StatusBadRequest, "invalid_request_error", "model_pricing_unavailable", admission.ModelPricingUnavailableMessage, c.Writer.Written(), false)
	}
	return err
}

// writeLocalTextError reports pre-dispatch validation once in the client protocol.
// Request handlers can receive the same error after forwarding returns. The
// handled marker prevents a second terminal response, including after keepalives.
func writeLocalTextError(c *gin.Context, source protocol.ProtocolID, failure *apperror.ApplicationError, code string) {
	if IsResponseCommitted(c) {
		return
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	if source == protocol.ProtocolAnthropicMessages {
		DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, int(failure.Code), "invalid_request_error", failure.Message, c.Writer.Written())
	} else {
		DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, int(failure.Code), "invalid_request_error", code, failure.Message, c.Writer.Written(), false)
	}
	MarkResponseCommitted(c)
}
