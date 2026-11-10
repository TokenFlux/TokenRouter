package openaiforward

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// transform.go applies Responses model, image, OAuth, and billing policy while
// synchronizing request bytes and decoded state.
// Responses preparation can update simple fields as byte patches while image,
// schema, and OAuth transformations need a decoded object. Pending patches are
// adopted before a decoded map is exposed, and an object mutation disables
// further queued patching so both representations describe the same request
// when serialization finishes. The installed decoder and marshaler retain JSON
// number values through object rewriting.
//
// Model resolution keeps requested, billing, and vendor identities separate.
// Canonical image intent is checked before later tool policy changes, and
// synthesized Codex instructions follow the resolved vendor identity.
// OAuth preparation saves the client cache seed before provider namespace
// changes and derives fingerprint values for the later header stage. Final
// serialization rebuilds the view and then moves the compaction trigger after
// historical input.

// TransformResult holds final request bytes, model identities, and header-stage values.
type TransformResult struct {
	// final outgoing request bytes
	Body []byte
	// final request scalar view
	View requeststate.OpenAIRequestView
	// Decoded retains the writable object when serialization used it; successful
	// byte-patch adoption and final trigger normalization clear that cached
	// object.
	Decoded map[string]any
	// Model is the working billing identity; RequestedModel is the incoming model;
	// BillingModel is the charge identity; UpstreamModel is the final vendor
	// model; PromptCacheKey is the selected header/session seed;
	// ClientPromptCacheKey is the retained client seed.
	Model, RequestedModel, BillingModel, UpstreamModel, PromptCacheKey, ClientPromptCacheKey string
	// resolved broad image intent
	ImageIntent bool
	// ImageMainModelAdapted distinguishes a gateway-selected image driver from an
	// explicit client text model, allowing rejection policy to preserve client routing.
	ImageMainModelAdapted bool
	// Fingerprint carries this attempt's derived IDs for header construction; the
	// caller stages them with the same selected provider.
	Fingerprint *openai.FingerprintIDs
	// DefaultInstructions retains the exact synthesized prompt for Compact
	// recovery. An empty value prevents replacement of client-supplied content.
	DefaultInstructions string
}

// FastDecision describes fast-tier edits or a policy rejection before request serialization.
type FastDecision struct {
	// Blocked stops preparation before DeleteField or Tier is applied; DeleteField
	// takes precedence over Tier when an accepted decision supplies both edit forms.
	DeleteField bool   // remove outgoing tier field
	Tier        string // selected outgoing tier value
	Blocked     error  // optional policy rejection error
}

// TransformRequest prepares an ordinary Responses body and returns its final
// view, model identities, and header inputs. The Prelude supplies matching
// bytes and decoded scalar observations, and decoded request maps are borrowed
// for synchronous mutation. Simple edits stay queued until adopted before
// object-level preparation.
//
// Object mutation disables patch queuing, while successful byte patching
// rebuilds the view and clears the decoded cache. Image admission separates
// explicit declarations from broad billing intent, and selected-model
// attributes precede OAuth compatibility transformation. Default Codex
// instructions use the resolved vendor model before image guidance is added.
// Client cache identity is retained before provider-scoped metadata changes so
// header isolation can use the caller seed. Final serialization and trigger
// ordering rebuild the returned view; patch or encoding errors stop preparation
// before an exchange can use inconsistent bytes.
func TransformRequest(ctx context.Context, prepared *Prelude, profile Profile, p TransformPorts) (*TransformResult, error) {
	body := prepared.Body
	requestView := prepared.View
	reqModel, promptCacheKey := requestView.Model, requestView.PromptCacheKey
	canonicalImageIntentBody := prepared.CanonicalImageIntentBody
	codexImageGenerationExplicitToolPolicy := prepared.ImageToolPolicy
	isCodexCLI, compactPath, compatMessagesBridge, statelessResponses := prepared.CodexCLI, prepared.Compact, prepared.MessagesBridge, profile.StatelessResponses
	wsDecision := prepared.Transport
	bodyModified := false
	imageMainModelAdapted := false
	clientPromptCacheKey := promptCacheKey
	defaultInstructions := ""
	var reqBody map[string]any
	// Apply and adopt queued byte edits before exposing a decoded map; otherwise
	// later object transformations would read fields from the earlier body and
	// overwrite those edits.
	ensureReqBody := func() (map[string]any, error) {
		if requestView.HasPatches() {
			patchedBody, patchErr := requestView.ApplyPatches()
			if patchErr != nil {
				return nil, patchErr
			}
			body = patchedBody
			requestView = requeststate.NewOpenAIRequestView(body)
			reqBody = nil
			bodyModified = false
		}
		if reqBody != nil {
			return reqBody, nil
		}
		decoded, decodeErr := p.Decode(requestView.Bytes())
		if decodeErr != nil {
			return nil, decodeErr
		}
		reqBody = decoded
		return reqBody, nil
	}
	// markPatchSet and markPatchDelete target the decoded map once object edits
	// disable patch queuing. Sending those later edits back to the old byte view
	// would discard the object changes at serialization.
	markPatchSet := func(path string, value any) {
		bodyModified = true
		if requestView.PatchesDisabled() {
			if reqBody != nil {
				openai.SetOpenAIRequestMapPath(reqBody, path, value)
			}
			return
		}
		requestView.MarkPatchSet(path, value)
	}
	markPatchDelete := func(path string) {
		bodyModified = true
		if requestView.PatchesDisabled() {
			if reqBody != nil {
				openai.DeleteOpenAIRequestMapPath(reqBody, path)
			}
			return
		}
		requestView.MarkPatchDelete(path)
	}
	disablePatch := func() {
		requestView.DisablePatches()
	}
	// Decoded-object changes become authoritative for final serialization, so
	// disable queued byte patching after those edits to prevent a later patch
	// adoption from discarding them.
	markDecodedModified := func() {
		bodyModified = true
		disablePatch()
	}

	codexImageGenerationExplicitToolPolicy = p.GroupImagePolicy(codexImageGenerationExplicitToolPolicy)
	imageGenerationAllowed := p.ImageAllowed()
	liteHeader := p.LiteHeader()
	codexImageGenerationBridgeEnabled := isCodexCLI &&
		!liteHeader &&
		imageGenerationAllowed &&
		codexImageGenerationExplicitToolPolicy != "strip" &&
		p.BridgeEnabled(ctx)
	var imageIntent bool
	canonicalImageIntent := p.ImageIntentHint(reqModel, canonicalImageIntentBody)
	// Explicit declarations decide image admission, while broad intent also
	// controls bridge handling and billing; retain both observations when provider
	// policy strips image tools.
	explicitImageIntent := p.IsExplicitImageGenerationIntent("/v1/responses", reqModel, canonicalImageIntentBody)
	// Strip provider-blocked image declarations on the decoded representation,
	// then reevaluate both intent kinds against that adopted body before checking
	// group admission.
	if codexImageGenerationExplicitToolPolicy == "strip" {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if openai.StripOpenAIImageGenerationTools(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Stripped /responses image_generation tool for Codex client by provider policy")
		}
		imageIntent = p.IsImageGenerationIntentMap("/v1/responses", reqModel, decoded)
		explicitImageIntent = p.IsExplicitImageGenerationIntentMap("/v1/responses", reqModel, decoded)
	} else {
		imageIntent = canonicalImageIntent
	}
	if explicitImageIntent && !imageGenerationAllowed {
		p.Reject(Rejection{Status: 403, Type: "permission_error", Message: p.ImagePermissionMessage(), FeatureDenied: true})
		return nil, errors.New("image generation disabled for group")
	}

	isCompactRequest := compactPath
	requestedModel := reqModel
	billingModel, upstreamModel := p.Models(requestedModel, isCompactRequest)
	// An explicit Compact model overrides the mapped vendor model for this
	// endpoint; billing continues using the separately selected charge identity.
	if isCompactRequest {
		if compactModel := p.CompactModel(requestedModel); compactModel != "" {
			upstreamModel = compactModel
		}
	}
	attributes, attributesErr := p.ModelAttributes(ctx, upstreamModel, body, isCompactRequest)
	if attributesErr != nil {
		return nil, attributesErr
	}
	// Apply aliases from the selected model resolution before OAuth conversion,
	// retaining each supported effort field spelling until subsequent protocol
	// preparation chooses its representation.
	for _, field := range []string{"reasoning.effort", "reasoning_effort"} {
		if effort := gjson.GetBytes(body, field); effort.Exists() {
			if mapped := attributes.Rules.EffortAliases[effort.String()]; mapped != "" {
				markPatchSet(field, mapped)
			}
		}
	}
	// Known unsupported sampling controls are removed using resolved model
	// attributes; absent attribute pointers keep the caller's supplied controls
	// available for upstream validation.
	if attributes.Attributes.Temperature != nil && !*attributes.Attributes.Temperature {
		markPatchDelete("temperature")
	}
	if attributes.Attributes.TopP != nil && !*attributes.Attributes.TopP {
		markPatchDelete("top_p")
	}
	if billingModel != requestedModel {
		p.Log("[OpenAI] Model mapping applied: %s -> %s (provider: %s, isCodexCLI: %v)", requestedModel, billingModel, profile.Name, isCodexCLI)
	}
	reqModel = billingModel
	if upstreamModel != requestedModel {
		markPatchSet("model", upstreamModel)
	}
	if upstreamModel != billingModel {
		if isCompactRequest {
			p.Log("[OpenAI] Compact model mapping applied: %s -> %s (provider: %s, isCodexCLI: %v)", requestedModel, upstreamModel, profile.Name, isCodexCLI)
		} else {
			p.Log("[OpenAI] Upstream model resolved: %s -> %s (provider: %s, type: %s, isCodexCLI: %v)", billingModel, upstreamModel, profile.Name, profile.Type, isCodexCLI)
		}
	}
	// Structured output schemas need object-level normalization, so adopt pending
	// patches before inspecting and editing the schema map for Responses
	// compatibility.
	if strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String()) == "json_schema" ||
		strings.TrimSpace(gjson.GetBytes(body, "response_format.type").String()) == "json_schema" {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if openai.NormalizeOpenAIResponseFormatSchemas(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Normalized Responses JSON schema compatibility")
		}
	}

	imageIntent = imageIntent || p.IsImageGenerationIntent("/v1/responses", reqModel, nil) || attributes.Attributes.OutputOnly("image")
	explicitImageIntent = explicitImageIntent || p.IsExplicitImageGenerationIntent("/v1/responses", reqModel, nil) || attributes.Attributes.OutputOnly("image")
	if explicitImageIntent && !imageGenerationAllowed {
		p.Reject(Rejection{Status: 403, Type: "permission_error", Message: p.ImagePermissionMessage(), FeatureDenied: true})
		return nil, errors.New("image generation disabled for group")
	}

	// Bridge insertion and image-only model migration share the decoded path,
	// allowing model validation and instruction preparation to inspect the same
	// normalized image tool declaration.
	if imageGenerationAllowed && !isCompactRequest && (codexImageGenerationBridgeEnabled || attributes.Attributes.OutputOnly("image") || p.OpenAIRequestBodyImageGenerationToolNeedsNormalization(body)) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if codexImageGenerationBridgeEnabled && p.EnsureOpenAIResponsesImageGenerationTool(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Injected /responses image_generation tool for Codex client")
		}
		if codexImageGenerationBridgeEnabled && p.EnsureOpenAIResponsesImageGenerationToolChoiceAuto(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Set /responses image_generation tool_choice=auto for Codex client")
		}
		if openai.NormalizeOpenAIResponsesImageGenerationTools(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Normalized /responses image_generation tool payload")
		}
		if openai.NormalizeOpenAIResponsesImageOnlyModel(decoded, attributes.Attributes.OutputOnly("image"), profile.ImageResponsesModel) {
			imageMainModelAdapted = true
			markDecodedModified()
			if model, ok := decoded["model"].(string); ok {
				upstreamModel = strings.TrimSpace(model)
			}
			p.Log("[OpenAI] Normalized /responses image-only model request inbound_model=%s image_model=%s upstream_model=%s", requestView.Model, billingModel, upstreamModel)
		}
		if err := openai.ValidateOpenAIResponsesImageModel(decoded, upstreamModel, upstreamModel == attributes.Target.Model && attributes.Attributes.OutputOnly("image")); err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "model", PolicyDenied: true})
			return nil, err
		}
		if openai.HasOpenAIImageGenerationTool(decoded) {
			imageIntent = true
			p.Log("[OpenAI] /responses image_generation request inbound_model=%s mapped_model=%s provider_type=%s", requestView.Model, upstreamModel, profile.Type)
		}
	} else if imageGenerationAllowed && imageIntent && p.OpenAIRequestBodyHasImageGenerationDeclaration(body) {
		// A complete image tool declaration can be read from raw JSON; validation, bridging, or legacy migration requires decoding input.
		p.Log("[OpenAI] /responses image_generation request inbound_model=%s mapped_model=%s provider_type=%s", requestView.Model, upstreamModel, profile.Type)
	}

	// Wait until provider, Compact, and image driver mapping have resolved the
	// outgoing model. The image bridge appends its guidance after the default
	// prompt so it cannot suppress synthesis by filling an empty field first.
	instructions := gjson.GetBytes(body, "instructions")
	instructionsEmpty := !instructions.Exists() || instructions.Type != gjson.String || strings.TrimSpace(instructions.String()) == ""
	// Lite clients may omit the top-level field because developer input already
	// carries their base instructions. Both synthesis steps honor that choice.
	liteDeveloperInstructions := profile.OpenAI && liteHeader && !instructions.Exists() &&
		openai.HasResponsesLiteDeveloperInstructions(body)
	if instructionsEmpty && profile.UsesCodex && !compatMessagesBridge && !statelessResponses && !liteDeveloperInstructions {
		defaultInstructions = openai.DefaultCodexSynthInstructions(upstreamModel)
		markPatchSet("instructions", defaultInstructions)
	}
	if codexImageGenerationBridgeEnabled && !isCompactRequest {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if p.ApplyCodexImageGenerationBridgeInstructions(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Added Codex image_generation bridge instructions")
		}
	}

	imageToolsDisabled := attributes.Rules.ImageGenerationTool != nil && !*attributes.Rules.ImageGenerationTool
	// When resolved model rules disable image tools and exclude image input,
	// validate the prepared Spark request before tool stripping can hide its
	// declarations.
	if imageToolsDisabled && attributes.Attributes.InputModalities != nil && !slices.Contains(*attributes.Attributes.InputModalities, "image") && openai.OpenAIRequestBodyMayContainImageInput(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err := p.ValidateCodexSparkInput(decoded, upstreamModel); err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "input", ObserveUpstream: true})
			return nil, err
		}
	}

	// Resolved platform rules can disallow image tools after group admission and
	// bridge preparation; remove declarations from the current map before OAuth
	// serialization.
	if imageToolsDisabled && p.OpenAIRequestBodyHasImageGenerationDeclaration(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if openai.StripCodexSparkImageGenerationTools(decoded) {
			markDecodedModified()
		}
	}

	var fingerprintIDs *openai.FingerprintIDs
	if profile.OAuth {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		// Promoted text system messages can be removed from input. JSON object mode keeps
		// its JSON instruction in input for upstream compatibility validation.
		omitPromotedSystemMessages := !strings.EqualFold(
			strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String()),
			"json_object",
		)
		codexResult := openai.CodexTransformResult{}
		if compatMessagesBridge {
			codexResult = p.CodexTransform(decoded, openai.CodexOAuthTransformOptions{
				IsCodexCLI:                          isCodexCLI,
				IsCompact:                           isCompactRequest,
				SkipDefaultInstructions:             true,
				PreserveToolCallIDs:                 true,
				OmitPromotedSystemMessagesFromInput: omitPromotedSystemMessages,
			})
			p.EnsureCodexOAuthInstructionsField(decoded)
			markDecodedModified()
		} else {
			codexResult = p.CodexTransform(decoded, openai.CodexOAuthTransformOptions{
				IsCodexCLI:                          isCodexCLI,
				IsCompact:                           isCompactRequest,
				SkipDefaultInstructions:             liteDeveloperInstructions,
				OmitPromotedSystemMessagesFromInput: omitPromotedSystemMessages,
			})
		}
		if codexResult.Error != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: codexResult.Error.Error()})
			return nil, codexResult.Error
		}
		p.ToolNameReverse(codexResult.ToolNameReverse)
		if codexResult.Modified {
			markDecodedModified()
		}
		// Fill supported installation metadata for non-Compact OAuth requests before
		// saving the client cache seed and deriving provider-scoped identity for
		// later headers.
		if !isCompactRequest && p.ClientMetadata(decoded) {
			markDecodedModified()
		}
		if currentClientPromptCacheKey, ok := decoded["prompt_cache_key"].(string); ok {
			clientPromptCacheKey = currentClientPromptCacheKey
		}
		// Capture the client cache seed before provider namespace rewriting so header
		// construction can isolate the caller identity without applying that
		// namespace twice.
		if !isCompactRequest && p.ProviderIdentity(decoded) {
			markDecodedModified()
		}
		// Clear earlier staged fingerprint data before deriving this provider
		// attempt's IDs; Compact leaves it absent because its request skips
		// fingerprint metadata preparation.
		p.ClearFingerprint()
		if !isCompactRequest {
			var changed bool
			var resolveErr error
			fingerprintIDs, changed, resolveErr = p.Fingerprint(ctx, decoded)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if changed {
				markDecodedModified()
			}
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if strings.TrimSpace(clientPromptCacheKey) != "" {
			// The body already has a provider-scoped key; header construction needs the saved client key to derive its namespace.
			promptCacheKey = clientPromptCacheKey
		} else if currentPromptCacheKey, ok := decoded["prompt_cache_key"].(string); ok && currentPromptCacheKey != "" {
			// When no nonblank client seed survived, use the fingerprint-prepared body
			// key before the transform's synthesized key so headers follow the selected
			// attempt identity.
			promptCacheKey = currentPromptCacheKey
		} else if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		}
	}

	// A resolved verbosity prohibition removes the supplied text control before
	// final serialization, while absent policy information leaves that control for
	// upstream handling.
	if attributes.Rules.Verbosity != nil && !*attributes.Rules.Verbosity && gjson.GetBytes(body, "text.verbosity").Exists() {
		markPatchDelete("text.verbosity")
	}

	if !isCodexCLI {
		maxOutputTokens := gjson.GetBytes(body, "max_output_tokens")
		// Translate the Responses output cap for Anthropic, retain it for OpenAI,
		// DeepSeek, and OpenCode, and remove it on other platform routes before final
		// serialization.
		if maxOutputTokens.Exists() {
			switch profile.Platform {
			case "openai", "deepseek", "opencode":
				// Send the Responses output limit first; bounded HTTP retries remove it if the upstream rejects it.
			case "anthropic":
				decoded, decodeErr := ensureReqBody()
				if decodeErr != nil {
					return nil, decodeErr
				}
				delete(decoded, "max_output_tokens")
				if _, hasMaxTokens := decoded["max_tokens"]; !hasMaxTokens {
					decoded["max_tokens"] = maxOutputTokens.Value()
				}
				markDecodedModified()
			case "gemini":
				markPatchDelete("max_output_tokens")
			default:
				markPatchDelete("max_output_tokens")
			}
		}
		// OpenAI Responses uses max_output_tokens for the Chat-style max_tokens
		// limit. Keep a supplied Responses limit when both forms occur, then remove
		// the Chat field.
		if profile.Platform == "openai" {
			if maxTokens := gjson.GetBytes(body, "max_tokens"); maxTokens.Exists() {
				if !gjson.GetBytes(body, "max_output_tokens").Exists() {
					markPatchSet("max_output_tokens", maxTokens.Value())
				}
				markPatchDelete("max_tokens")
			}
		}
		// Remove this Chat-specific completion cap for API Key or non-OpenAI routes;
		// this branch discards it rather than translating its value into a Responses
		// output limit.
		if gjson.GetBytes(body, "max_completion_tokens").Exists() && (profile.Type == "apikey" || profile.Platform != "openai") {
			markPatchDelete("max_completion_tokens")
		}
		// Remove the compatibility fields handled by this non-Codex route after
		// output-limit translation, keeping later request construction from
		// forwarding unsupported retention and identity controls.
		for _, unsupportedField := range []string{"prompt_cache_retention", "safety_identifier", "prompt_cache_options"} {
			if gjson.GetBytes(body, unsupportedField).Exists() {
				markPatchDelete(unsupportedField)
			}
		}
	}
	// Native HTTP continuation and WSv2 retain their upstream response reference.
	// Converted and stateless compatibility routes rely on the supplied input history.
	if wsDecision.Transport != "responses_websockets_v2" && !profile.HTTPContinuation && gjson.GetBytes(body, "previous_response_id").Exists() {
		markPatchDelete("previous_response_id")
	}
	// Remove empty image placeholders on the decoded path before tier resolution;
	// adopting that change also prevents later queued edits from restoring the
	// earlier malformed input.
	if openai.OpenAIRequestBodyMayContainEmptyBase64InputImage(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if openai.SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(decoded) {
			markDecodedModified()
		}
	}

	// Resolve tier policy after model and image preparation. A denial returns
	// before serialization, while accepted deletion takes precedence over a
	// replacement tier for the final outgoing request.
	decision := p.FastDecision(ctx, upstreamModel, requestView.ServiceTier, requestView.HasServiceTier)
	if decision.Blocked != nil {
		p.FastBlocked(decision.Blocked)
		return nil, decision.Blocked
	}
	if decision.DeleteField {
		markPatchDelete("service_tier")
	} else if decision.Tier != "" && decision.Tier != requestView.ServiceTier {
		markPatchSet("service_tier", decision.Tier)
	}

	// OAuth input cleanup removes unmatched tool outputs when continuation cannot
	// supply their call context, after earlier model, identity, and tier
	// preparation has been adopted.
	if profile.OAuth {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if input, ok := decoded["input"].([]any); ok && p.SanitizeOpenAIResponsesOrphanToolOutputs(
			decoded,
			input,
			strings.TrimSpace(p.FirstNonEmptyString(decoded["previous_response_id"])) != "",
		) {
			markDecodedModified()
		}
	}
	if reqBody != nil {
		// Synchronize any remaining queued edits before serialization; ensureReqBody
		// rebuilds the decoded cache from adopted bytes so the returned object
		// matches final preparation.
		if _, decodeErr := ensureReqBody(); decodeErr != nil {
			return nil, decodeErr
		}
	}

	if bodyModified {
		// Successful byte-patch adoption keeps unrelated payload representation and
		// clears the decoded cache; a failed attempt leaves its queue for
		// ensureReqBody, which can return the patch error.
		if requestView.HasPatches() {
			if patchedBody, patchErr := requestView.ApplyPatches(); patchErr == nil {
				body = patchedBody
				requestView = requeststate.NewOpenAIRequestView(body)
				reqBody = nil
				bodyModified = false
			}
		}
		if bodyModified {
			decoded, decodeErr := ensureReqBody()
			if decodeErr != nil {
				return nil, decodeErr
			}
			var marshalErr error
			body, marshalErr = p.Marshal(decoded)
			if marshalErr != nil {
				return nil, fmt.Errorf("serialize request body: %w", marshalErr)
			}
			requestView = requeststate.NewOpenAIRequestView(body)
		}
	}
	// Place the compaction trigger after historical input only after all body
	// edits are serialized; adopting reordered bytes rebuilds the view and clears
	// cached decoding.
	if normalizedBody, changed, normalizeErr := p.NormalizeTrigger(body); normalizeErr != nil {
		return nil, fmt.Errorf("normalize compaction trigger order: %w", normalizeErr)
	} else if changed {
		body = normalizedBody
		requestView = requeststate.NewOpenAIRequestView(body)
		reqBody = nil
	}

	return &TransformResult{
		Body: body, View: requestView, Decoded: reqBody,
		Model: reqModel, RequestedModel: requestedModel,
		BillingModel: billingModel, UpstreamModel: upstreamModel,
		PromptCacheKey: promptCacheKey, ClientPromptCacheKey: clientPromptCacheKey,
		ImageIntent: imageIntent, ImageMainModelAdapted: imageMainModelAdapted,
		Fingerprint: fingerprintIDs, DefaultInstructions: defaultInstructions,
	}, nil
}
