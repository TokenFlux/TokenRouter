package ws

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// normalize.go normalizes response.create frames while retaining client model
// and replay identities.
// A WebSocket request can omit model after the session has accepted an earlier
// client identifier, but policy and provider execution still need distinct
// model forms. Normalization retains the client model for response restoration,
// resolves routing eligibility before the actual upstream model, and applies
// image permission using the routing form.
//
// The request also carries identity metadata that must be scoped to its
// selected provider. RequestNormalizer saves pre-scope bytes for later
// replacement-provider replay, then applies provider scoping, Lite fields,
// image transformations, model mutation, and Fast policy. Requested effort is
// captured before reasoning rules rewrite it. The retained client model changes
// only after this sequence succeeds, so a rejected normalization does not
// replace the session default used by another frame.

// Normalize validates a turn and returns its forwarded payload plus routing,
// replay, and accounting metadata. State and Port are required; the caller
// serializes this mutable normalizer within one ingress session.
//
// The prompt-replacement flag is true for later frames and false when entry has
// already processed the initial prompt. Missing type is promoted to
// response.create, unsupported types close locally, and missing model uses the
// prior accepted client model. Model and image checks retain client and routing
// identities separately from the actual upstream model. Pre-scope bytes and
// pre-policy requested effort survive later mutations, while blocked Fast
// policy attempts an error event before returning its close instruction. Only
// full normalization success updates State.OriginalModel.
func (s *RequestNormalizer) Normalize(ctx context.Context, raw []byte, applyUserPromptReplacement bool, turn int) (ClientPayload, error) {
	p, o := s.Port, s.Options
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ClientPayload{}, p.CloseError(1008, "empty websocket request payload", nil)
	}
	if err := wirejson.ValidateRequestObject(trimmed); err != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", err)
	}
	if applyUserPromptReplacement {
		// Later client frames need the same user prompt rules applied at entry for
		// the first frame, before moderation and execution consumers read their
		// normalized content.
		trimmed = p.PromptReplace(ctx, trimmed)
	}

	values := gjson.GetManyBytes(trimmed, "type", "model", "prompt_cache_key", "previous_response_id")
	eventType := strings.TrimSpace(values[0].String())
	normalized := trimmed
	switch eventType {
	case "":
		// An omitted type represents the supported request form; publish its
		// response.create label so subsequent provider compatibility and tier policy
		// see an explicit event type.
		eventType = "response.create"
		next, setErr := p.Mutate(normalized, "type", eventType)
		if setErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", setErr)
		}
		normalized = next
	case "response.create":
	case "response.append":
		return ClientPayload{}, p.CloseError(
			1008,
			"response.append is not supported in ws v2; use response.create with previous_response_id",
			nil,
		)
	default:
		return ClientPayload{}, p.CloseError(
			1008,
			fmt.Sprintf("unsupported websocket request type: %s", eventType),
			nil,
		)
	}
	policyRequestModel := strings.TrimSpace(values[1].String())
	if policyRequestModel == "" {
		policyRequestModel = s.State.OriginalModel
	}
	// Capture requested effort before group caps or mappings rewrite it, keeping
	// client intent available separately from the accepted effort used for usage
	// recording.
	requestedReasoningEffort := p.RequestedEffort(normalized, strings.TrimSpace(values[1].String()))
	next, policyErr := p.Reasoning(normalized, policyRequestModel)
	if policyErr != nil {
		return ClientPayload{}, p.CloseError(1008, policyErr.Error(), policyErr)
	}
	normalized = next
	responsesLite := p.IsLite(normalized)
	// Normalize provider-specific Responses fields before OAuth tool aliasing and
	// later Lite enforcement, so protocol compatibility is established before
	// provider identity and image handling.
	if compatibilityBody, compatibilityChanged, compatibilityErr := p.Compatibility(normalized, responsesLite); compatibilityErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", compatibilityErr)
	} else if compatibilityChanged {
		normalized = compatibilityBody
	}
	// OAuth WebSocket forwarding uses reserved-name aliases with response reverse
	// mappings; forced HTTP bridge delegates that provider conversion to its HTTP
	// execution path.
	if o.OAuth && !o.ForceHTTPBridge {
		aliasedBody, aliasErr := p.AliasTools(normalized)
		if aliasErr != nil {
			return ClientPayload{}, p.CloseError(1008, aliasErr.Error(), aliasErr)
		}
		normalized = aliasedBody
	}

	originalModel := strings.TrimSpace(values[1].String())
	modelMissing := originalModel == ""
	if originalModel == "" {
		// An omitted model inherits the last normalized client identifier. Resolve
		// that identifier through current mappings before model-specific policy and
		// upstream serialization use their respective model forms.
		originalModel = s.State.OriginalModel
		if originalModel == "" {
			return ClientPayload{}, p.CloseError(
				1008,
				"model is required in response.create payload",
				nil,
			)
		}
	}
	// Resolve group eligibility and then provider model while retaining the client
	// identifier, so image permission and response restoration use their own model
	// forms.
	routingModel, upstreamModel, resolveModelErr := p.Models(turn, originalModel, normalized)
	if resolveModelErr != nil {
		return ClientPayload{}, resolveModelErr
	}
	normalized, resolveModelErr = p.ModelAttributes(ctx, turn, upstreamModel, normalized)
	if resolveModelErr != nil {
		return ClientPayload{}, resolveModelErr
	}
	promptCacheKey := strings.TrimSpace(values[2].String())
	previousResponseID := strings.TrimSpace(values[3].String())
	previousResponseIDKind := p.ClassifyPrevious(previousResponseID)
	if previousResponseID != "" && previousResponseIDKind == "message_id" {
		return ClientPayload{}, p.CloseError(
			1008,
			"previous_response_id must be a response.id (resp_*), not a message id",
			nil,
		)
	}
	// Copy the inbound turn header into the payload metadata expected by WS
	// forwarding, so each normalized request carries the same client turn
	// association.
	if turnMetadata := p.TurnMetadata(); turnMetadata != "" {
		next, setErr := p.Mutate(normalized, "client_metadata.x-codex-turn-metadata", turnMetadata)
		if setErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", setErr)
		}
		normalized = next
	}
	// Retain separate bytes before provider identity scoping, giving current-turn
	// failover construction metadata that can be scoped for the replacement
	// target.
	providerIdentitySourceRaw := append([]byte(nil), normalized...)
	providerScopedPayload, providerScoped, scopeErr := p.ScopeIdentity(normalized)
	if scopeErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket identity metadata", scopeErr)
	}
	if providerScoped {
		normalized = providerScopedPayload
	}
	// Apply required Lite fields after compatibility and identity conversion, so
	// earlier field removal does not leave this marked request missing its Lite
	// parameters.
	if p.IsLite(normalized) {
		litePayload, liteErr := p.NormalizeLite(normalized)
		if liteErr != nil {
			return ClientPayload{}, p.CloseError(
				1008,
				liteErr.Error(),
				liteErr,
			)
		}
		normalized = litePayload
	}
	imagePolicy := p.ImagePolicy(ctx, normalized)
	imageGenerationAllowed := imagePolicy.Allowed
	codexImageGenerationExplicitToolPolicy := imagePolicy.Explicit
	codexBridgeEnabled := imagePolicy.Bridge
	// Bridge image intent only when the adapter has combined group permission,
	// explicit-tool policy, client identity, and bridge availability into an
	// enabled decision.
	if codexBridgeEnabled {
		next, err := p.BridgeImages(normalized)
		if err != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", err)
		}
		normalized = next
	}

	// Write the resolved upstream model when inheritance or mapping changed the
	// wire value, while retaining originalModel for client-visible metadata and
	// turn capture.
	if modelMissing || upstreamModel != originalModel {
		next, setErr := p.Mutate(normalized, "model", upstreamModel)
		if setErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", setErr)
		}
		normalized = next
	}
	// Strip explicit image tools before inspecting final intent, so a provider or
	// group strip action removes those declarations from the body being
	// authorized.
	if codexImageGenerationExplicitToolPolicy == "strip" {
		if stripped, changed, stripErr := p.StripImages(normalized); stripErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", stripErr)
		} else if changed {
			normalized = stripped
			p.Log(fmt.Sprintf("ingress_ws_codex_image_tool_stripped_by_policy provider_id=%d", o.ProviderID))
		}
	}
	// Use the resolved provider model for Spark image compatibility, preventing
	// client aliases from hiding tool declarations that its selected upstream
	// model cannot accept.
	if stripped, changed, stripErr := p.StripSparkImages(normalized, upstreamModel); stripErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", stripErr)
	} else if changed {
		normalized = stripped
		p.Log(fmt.Sprintf("ingress_ws_codex_spark_image_tool_stripped provider_id=%d", o.ProviderID))
	}
	// Inspect group-model image intent while returning actual upstream-model
	// bytes, so group permission and image billing do not borrow a provider alias
	// as their eligibility identifier.
	imageIntentBody, imageIntent, explicitImageIntent := p.ImageIntent(routingModel, upstreamModel, normalized)
	if explicitImageIntent && !imageGenerationAllowed {
		p.FeatureDenied()
		return ClientPayload{}, p.CloseError(1008, p.ImageDeniedMessage(), nil)
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfgErr error
		imageCfg, imageCfgErr := p.ImageBilling(imageIntentBody, routingModel)
		if imageCfgErr != nil {
			return ClientPayload{}, p.CloseError(1008, imageCfgErr.Error(), imageCfgErr)
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	// Apply the HTTP Fast policy rules to each WebSocket turn.
	// The resolved upstream model drives whitelist matching, including when the frame omits model.
	policyCtx := ctx
	policyApplied, blocked, policyErr := p.FastPolicy(policyCtx, turn, upstreamModel, normalized, true)
	if policyErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", policyErr)
	}
	if blocked != nil {
		p.PolicyDenied()
		// Attempt the policy error event before returning the close instruction.
		// A successful synchronous write orders the event before the close frame.
		// The blocked request is not forwarded upstream.
		eventBytes := p.BlockedEvent(blocked)
		if eventBytes != nil {
			p.WriteBlocked(ctx, eventBytes)
		}
		return ClientPayload{}, p.CloseError(
			1008,
			blocked.Message,
			blocked,
		)
	}
	normalized = policyApplied
	// Publish the session model only after every normalization and policy step
	// succeeds; a rejected frame therefore leaves the previously usable default
	// intact.
	s.State.OriginalModel = originalModel

	return ClientPayload{
		PayloadRaw:                normalized,
		ProviderIdentitySourceRaw: providerIdentitySourceRaw,
		RawForHash:                trimmed,
		PromptCacheKey:            promptCacheKey,
		PreviousResponseID:        previousResponseID,
		OriginalModel:             originalModel,
		RoutingModel:              routingModel,
		ImageBillingModel:         imageBillingModel,
		ImageSizeTier:             imageSizeTier,
		ImageInputSize:            imageInputSize,
		PayloadBytes:              len(normalized),
		RequestedReasoningEffort:  requestedReasoningEffort,
	}, nil
}

// ApplyReasoningEffortPolicy applies inherited group caps and mappings to the
// frame using requestModel. Nil hooks or absent caps and mappings return the
// supplied bytes; callers provide the retained client model when a later frame
// omits it.
func ApplyReasoningEffortPolicy(payload []byte, hooks *OpenAIIngressHooks, requestModel string) ([]byte, error) {
	if hooks == nil || (hooks.MaxReasoningEffort == "" && len(hooks.ReasoningEffortMappings) == 0) {
		return payload, nil
	}
	updated, changed, err := requeststate.ApplyOpenAIReasoningEffortPolicyForModel(
		payload,
		hooks.MaxReasoningEffort,
		hooks.ReasoningEffortMappings,
		hooks.MaxReasoningEffortOverLimit,
		strings.TrimSpace(requestModel),
	)
	if err != nil {
		return payload, err
	}
	if changed {
		return updated, nil
	}
	return payload, nil
}
