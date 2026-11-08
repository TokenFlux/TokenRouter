package provider

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/ollama"
)

// model_attributes.go resolves final-route capabilities and applies request
// parameter policy.
// Final-route attributes decide request sampling, reasoning controls, output
// limits, and image-tool options after model mapping. Resolution reuses a
// request catalogue session and group context, keeping each attempt on the
// intended catalogue/configuration view while protocol selection and model
// identity are supplied separately.
//
// Parameter policy first interprets client controls to activate conditional
// attributes, then applies removal, clamping, aliases, and platform encoding
// rules. Unknown capability values remain different from explicit unsupported
// or empty option sets. OpenAI image tools are resolved under their own named
// model before input_fidelity is removed. Grok preparation binds the same
// resolved attributes to body patching so cache and outgoing request can use
// the same representation.

// ResolveModelAttributes resolves attributes for the final model, admitted
// group, and current client/upstream protocols. The first optional protocol
// overrides the attempt route; request sessions are reused, otherwise
// service.NewSession supplies one for the lookup.
func ResolveModelAttributes(ctx context.Context, value *ExecutionProvider, model string, service *routing.ModelAttributeService, protocols ...string) (modelcatalog.Resolution, error) {
	upstreamProtocol := ""
	if value != nil {
		upstreamProtocol = string(value.Route.Protocol())
	}
	if len(protocols) > 0 {
		upstreamProtocol = protocols[0]
	}
	target := ExecutionProtocolRecord(value).ModelAttributeTarget(model, upstreamProtocol)
	if source, ok := requeststate.ClientProtocolFromContext(ctx); ok {
		target.ClientProtocol = string(source)
	}

	var groupID int64
	if group, ok := requeststate.GroupFromContext(ctx); ok && group != nil {
		groupID = group.ID
	}
	session := routing.ModelAttributesFromContext(ctx)
	// An entry can reach this adapter without an existing request session. Create
	// one from the bound service before resolving this route and its group attributes.
	if session == nil {
		session = service.NewSession()
	}
	return session.Resolve(ctx, groupID, target)
}

// ExecutionModelAttributes returns the attached attribute resolution when its
// target model exactly matches model. Nil targets, missing capabilities, and
// mismatched names return unknown attributes, so callers resolve the intended
// route before conversion.
func ExecutionModelAttributes(value *ExecutionProvider, model string) modelcatalog.Attributes {
	if value != nil && value.Capabilities != nil && value.Capabilities.Target.Model == model {
		return value.Capabilities.Attributes
	}
	return modelcatalog.Attributes{}
}

// ModelConversionOptions supplies known sampling restrictions and max-effort
// support to protocol conversion. Unknown temperature/top_p remain allowed,
// and unknown max support also permits the converter to retain that effort.
func ModelConversionOptions(result modelcatalog.Resolution) bridge.RequestOptions {
	attrs := result.Attributes
	max := attrs.EffortSupport("max")
	return bridge.RequestOptions{
		DropTemperature:   attrs.Temperature != nil && !*attrs.Temperature,
		DropTopP:          attrs.TopP != nil && !*attrs.TopP,
		SupportsMaxEffort: max == nil || *max,
	}
}

// GeminiThinking combines final attributes and execution rules with parsed
// client thinking controls. The bridge resolves toggle, effort, budget bounds,
// output space, and Antigravity Claude force-budget behavior into its
// configuration or validation error.
func GeminiThinking(result modelcatalog.Resolution, body []byte) (*bridge.GeminiThinkingConfig, error) {
	attrs := result.Attributes
	minimum, maximum := attrs.ReasoningBudget()
	options := bridge.GeminiThinkingOptions{
		ControlsKnown: attrs.ReasoningOptions != nil,
		Reasoning:     attrs.Reasoning, Toggle: attrs.HasReasoningOption("toggle"),
		Efforts: attrs.Efforts(), Budget: attrs.HasReasoningOption("budget_tokens"),
		BudgetMin: minimum, BudgetMax: maximum,
		ForceBudget:    result.Target.Platform == "antigravity" && strings.HasPrefix(result.Target.Model, "claude-"),
		AdaptiveBudget: result.Rules.AdaptiveThinkingBudget, OutputLimit: attrs.OutputLimit, BudgetPadding: result.Rules.BudgetPadding,
	}
	return bridge.ResolveGeminiThinking(bridge.ParseGeminiThinkingInput(body), options)
}

// ApplyModelAttributeParameters applies route attributes after resolving their
// request-state conditions. It removes prohibited fields, applies output
// clamping and effort aliases, and adapts platform-specific thinking and
// reasoning controls.
//
// Known unsupported OpenAI none effort is removed when the route has no toggle
// option; GLM conversion uses the resolved allowed effort set, distinguishing
// unknown options from an explicitly empty set. Adaptive Anthropic modes
// remove explicit budgets after encoding adaptive. Grok conversion runs last
// with these same resolved attributes. Field-edit errors can return nil, while
// final adaptive edits can return partial bytes with an error; callers adopt
// the result only when successful.
func ApplyModelAttributeParameters(body []byte, result modelcatalog.Resolution) ([]byte, error) {
	result = result.ForRequest(ModelRequestState(body))
	remove := append([]string(nil), result.Rules.DropFields...)
	if value := result.Attributes.Temperature; value != nil && !*value {
		remove = append(remove, "temperature")
	}
	if value := result.Attributes.TopP; value != nil && !*value {
		remove = append(remove, "top_p")
	}
	// Output clamping belongs to the final resolved route, after conditional
	// attributes have selected their restrictions; the Ollama helper retains bytes on declined edits.
	if result.Rules.ClampOutput && result.Attributes.OutputLimit != nil {
		body, _ = ollama.ClampMaxTokens(body, int64(*result.Attributes.OutputLimit))
	}
	var err error
	for _, field := range remove {
		if gjson.GetBytes(body, field).Exists() {
			body, err = sjson.DeleteBytes(body, field)
			if err != nil {
				return nil, err
			}
		}
	}
	// Apply route-owned effort aliases to supported protocol spellings so an
	// administrator or platform correction reaches the field actually used by this body.
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
		effort := gjson.GetBytes(body, path)
		if mapped := result.Rules.EffortAliases[effort.String()]; effort.Exists() && mapped != "" {
			body, err = sjson.SetBytes(body, path, mapped)
			if err != nil {
				return nil, err
			}
		}
	}
	// A route with known unsupported none and no toggle cannot express disabled
	// reasoning through these effort fields; remove that spelling before OpenAI forwarding.
	if result.Target.Platform == "openai" && result.Target.ServiceID() == "" && !result.Attributes.HasReasoningOption("toggle") {
		if supported := result.Attributes.EffortSupport("none"); supported != nil && !*supported {
			for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
				if gjson.GetBytes(body, path).String() == "none" {
					body, err = sjson.DeleteBytes(body, path)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
	// Final GLM options can differ from family defaults. Pass a nonnil empty
	// effort set when controls are known but absent, preserving that configured distinction.
	if strings.HasPrefix(strings.ToLower(result.Target.Model), "glm-") {
		options := thinkingRequestOptions(result.Target.Model)
		if result.Attributes.ReasoningOptions != nil {
			options.Efforts = append([]string{}, result.Attributes.Efforts()...)
		}
		if result.Target.UpstreamProtocol == "anthropic_messages" && options.GLM53 {
			body, _ = requeststate.NormalizeGLM53AnthropicThinking(body, options)
		} else if result.Attributes.HasReasoningOption("effort") {
			body, _ = requeststate.NormalizeGLMOpenAIReasoningEffort(body, options)
		}
	}
	// Adaptive mode encodes control through thinking.type, so an enabled request
	// loses its explicit budget after adaptive is set; propagate either failed byte edit.
	if (result.Rules.ThinkingMode == "adaptive" || result.Rules.ThinkingMode == "adaptive_between_tools") && gjson.GetBytes(body, "thinking.type").String() == "enabled" {
		body, err = sjson.SetBytes(body, "thinking.type", "adaptive")
		if err == nil {
			body, err = sjson.DeleteBytes(body, "thinking.budget_tokens")
		}
	}
	if err == nil && (result.Target.Platform == "grok" || result.Target.ServiceID() == "xai") {
		codec := GrokBodyCodec()
		codec.Attributes = func(string) modelcatalog.Attributes { return result.Attributes }
		if result.Target.UpstreamProtocol == "openai_chat_completions" {
			return codec.NormalizeGrokChatReasoningEffort(body, result.Target.Model)
		}
		return codec.NormalizeGrokResponsesReasoningEffort(body, result.Target.Model)
	}
	return body, err
}

// ModelRequestState reads reasoning controls used to evaluate conditional
// attributes after group policy. Each top-level path retains its first
// observed value, and the first nonblank string among nested reasoning, flat
// effort, and output effort supplies initial state.
//
// Explicit recognized thinking.type then replaces the inferred toggle. Gemini
// camel and snake config forms are visited afterward in that order; later
// level or numeric budget observations override prior state, with a zero
// budget meaning disabled. Effort strings are copied away from parsed body
// storage. Parsing is lenient and does not validate the full request, so
// ingress validation remains the caller's responsibility.
func ModelRequestState(body []byte) modelcatalog.RequestState {
	var reasoning, effort, output, thinking, camelConfig, snakeConfig gjson.Result
	// Read the controls in one pass so large inputs are skipped once. Each
	// path keeps its first match, including null, as gjson.GetBytes does.
	wirejson.ParseView(body).ForEach(func(key, value gjson.Result) bool {
		var field *gjson.Result
		switch key.Str {
		case "reasoning":
			field, value = &reasoning, value.Get("effort")
		case "reasoning_effort":
			field = &effort
		case "output_config":
			field, value = &output, value.Get("effort")
		case "thinking":
			field, value = &thinking, value.Get("type")
		case "generationConfig":
			field = &camelConfig
		case "generation_config":
			field = &snakeConfig
		}
		if field != nil && !field.Exists() {
			*field = value
		}
		return true
	})
	state := modelcatalog.RequestState{}
	for _, value := range []gjson.Result{reasoning, effort, output} {
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			effort := strings.Clone(strings.ToLower(strings.TrimSpace(value.String())))
			state.Effort = &effort
			thinking := effort != "none"
			state.Thinking = &thinking
			break
		}
	}
	if thinking.Type == gjson.String {
		switch thinking.String() {
		case "enabled", "adaptive", "between_tools":
			thinking := true
			state.Thinking = &thinking
		case "disabled", "off", "none":
			thinking := false
			state.Thinking = &thinking
		}
	}
	// Gemini aliases are read after ordinary controls. Their loop order supplies
	// later-observation precedence when both camel and snake spellings occur in one body.
	for _, config := range []gjson.Result{camelConfig, snakeConfig} {
		for _, thinkingName := range []string{"thinkingConfig", "thinking_config"} {
			thinkingConfig := config.Get(thinkingName)
			for _, key := range []string{"thinkingLevel", "thinking_level"} {
				if level := thinkingConfig.Get(key); level.Type == gjson.String {
					effort := strings.Clone(strings.ToLower(level.String()))
					state.Effort = &effort
					thinking := effort != "none"
					state.Thinking = &thinking
				}
			}
			for _, key := range []string{"thinkingBudget", "thinking_budget"} {
				if budget := thinkingConfig.Get(key); budget.Type == gjson.Number {
					// Zero budget expresses disabled thinking in this state.
					thinking := budget.Int() != 0
					state.Thinking = &thinking
				}
			}
		}
	}
	return state
}

// PrepareModelAttributeParameters applies request attributes using one session
// for the final model and named image-tool models. Main parameter rewriting
// runs first; its error can return partially rewritten bytes, which callers
// adopt only on success.
//
// An image tool can name a different model from its text driver. Resolve that
// tool's own media attributes before removing input_fidelity, so the driver's
// restrictions cannot be mistaken for the image model's capabilities. Image
// lookup or field-edit failures return nil and stop preparation. The provider
// retains the main model's resolution; named and default image tool models are
// validated independently against the same request attribute session.
func PrepareModelAttributeParameters(ctx context.Context, value *ExecutionProvider, model string, body []byte, service *routing.ModelAttributeService, protocols ...string) ([]byte, error) {
	if err := upstream.ValidateGIFJSONControls(body); err != nil {
		return nil, apperror.BadRequest("IMAGE_GIF_PARAMS_INVALID", err.Error())
	}
	if routing.ModelAttributesFromContext(ctx) == nil {
		ctx = routing.WithModelAttributes(ctx, service)
	}
	result, err := ResolveModelAttributes(ctx, value, model, service, protocols...)
	if err != nil {
		return nil, err
	}
	if value != nil {
		value.Capabilities = &result
	}
	body, err = ApplyModelAttributeParameters(body, result)
	if err != nil {
		return body, err
	}
	// Hosted image tools can target a different model from the text driver.
	// Validate each tool's selected or default image model before removing a
	// fidelity field rejected by that tool route's media declaration.
	for index, tool := range gjson.GetBytes(body, "tools").Array() {
		if tool.Get("type").String() != "image_generation" {
			continue
		}
		toolModel := strings.TrimSpace(tool.Get("model").String())
		if toolModel == "" {
			toolModel = "gpt-image-2"
		}
		image, err := ResolveModelAttributes(ctx, value, toolModel, service, protocols...)
		if err != nil {
			return nil, err
		}
		if err := routing.RequireImageOutput(image.Attributes); err != nil {
			return nil, err
		}
		// Lifecycle streaming belongs to Responses, while frame controls belong
		// to its image tool. Normalize only the tool so OAuth event delivery remains intact.
		if strings.EqualFold(tool.Get("output_format").String(), "gif") || tool.Get("frame_count").Exists() || tool.Get("frame_duration_ms").Exists() {
			parsed := &upstream.ImageRequest{OutputFormat: tool.Get("output_format").String(), Size: tool.Get("size").String(), HasMask: tool.Get("input_image_mask").Exists()}
			for _, field := range []struct {
				name   string // numeric image control name
				target **int  // parsed optional integer destination
			}{{"frame_count", &parsed.FrameCount}, {"frame_duration_ms", &parsed.FrameDurationMS}, {"output_compression", &parsed.OutputCompression}} {
				raw := tool.Get(field.name)
				if raw.Exists() {
					number, parseErr := strconv.Atoi(raw.Raw)
					if raw.Type != gjson.Number || parseErr != nil {
						return nil, apperror.BadRequest("IMAGE_GIF_PARAMS_INVALID", field.name+" must be an integer")
					}
					*field.target = &number
				}
			}
			if raw := tool.Get("partial_images"); raw.Exists() && raw.Raw != "0" {
				return nil, apperror.BadRequest("IMAGE_GIF_PARAMS_INVALID", "GIF output does not support partial images")
			}
			if err := parsed.NormalizeGIF(); err != nil {
				return nil, apperror.BadRequest("IMAGE_GIF_PARAMS_INVALID", err.Error())
			}
			if err := RequireImageGIF(value, protocol.ProtocolOpenAIResponses, image.Attributes); err != nil {
				return nil, err
			}
			prefix := "tools." + strconv.Itoa(index) + "."
			body, err = sjson.SetBytes(body, prefix+"frame_count", *parsed.FrameCount)
			if err != nil {
				return nil, err
			}
			body, err = sjson.SetBytes(body, prefix+"frame_duration_ms", *parsed.FrameDurationMS)
			if err != nil {
				return nil, err
			}
		}
		media := image.Attributes.Media
		if media != nil && media.InputFidelity != nil && !*media.InputFidelity {
			body, err = sjson.DeleteBytes(body, "tools."+strconv.Itoa(index)+".input_fidelity")
			if err != nil {
				return nil, err
			}
		}
	}
	return body, nil
}

// PrepareGrokResponses resolves Responses-route attributes, applies parameter
// policy, then patches the body through a codec bound to those same
// attributes. Errors stop preparation, allowing cache identity and forwarding
// to share the returned encoded body.
func PrepareGrokResponses(ctx context.Context, provider *ExecutionProvider, model string, body []byte, service *routing.ModelAttributeService) ([]byte, error) {
	resolution, err := ResolveModelAttributes(ctx, provider, model, service, "openai_responses")
	if err != nil {
		return nil, err
	}
	body, err = ApplyModelAttributeParameters(body, resolution)
	if err != nil {
		return nil, err
	}
	codec := GrokBodyCodec()
	codec.Attributes = func(string) modelcatalog.Attributes { return resolution.Attributes }
	return codec.PatchGrokResponsesBody(body, model)
}

// ValidateResponsesImageModels checks every selected hosted image model on one route.
// Empty identities select the hosted default. Callers retain a request
// attribute session so candidate filtering and outgoing preparation share overrides.
func ValidateResponsesImageModels(ctx context.Context, value *ExecutionProvider, models []string, service *routing.ModelAttributeService, protocols ...string) error {
	if routing.ModelAttributesFromContext(ctx) == nil {
		ctx = routing.WithModelAttributes(ctx, service)
	}
	for _, model := range models {
		if model == "" {
			model = "gpt-image-2"
		}
		resolution, err := ResolveModelAttributes(ctx, value, model, service, protocols...)
		if err != nil {
			return err
		}
		if err := routing.RequireImageOutput(resolution.Attributes); err != nil {
			return err
		}
	}
	return nil
}

// ResolveResponsesImageBilling selects the hosted tool or confirmed converted
// image identity and validates its output capability. Text-driver names never
// supply a billing hint; a native tool without a model uses its registered
// default, which undergoes the same route attribute validation.
func ResolveResponsesImageBilling(ctx context.Context, value *ExecutionProvider, body []byte, service *routing.ModelAttributeService) (media.OpenAIResponsesImageBillingConfig, error) {
	if routing.ModelAttributesFromContext(ctx) == nil {
		ctx = routing.WithModelAttributes(ctx, service)
	}
	protocol := "openai_responses"
	if value != nil && value.Route.Protocol() != "" {
		protocol = string(value.Route.Protocol())
	}
	if err := ValidateResponsesImageModels(ctx, value, media.ResponsesImageModels(body), service, protocol); err != nil {
		return media.OpenAIResponsesImageBillingConfig{}, err
	}
	confirmed := ""
	if value != nil && value.Capabilities != nil && value.Capabilities.Attributes.OutputOnly("image") {
		confirmed = value.Capabilities.Target.Model
	}
	config, err := ImageIntent().ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, confirmed)
	if err != nil || config.Model == "" {
		return config, err
	}
	resolution, err := ResolveModelAttributes(ctx, value, config.Model, service, protocol)
	if err != nil {
		return config, err
	}
	return config, routing.RequireImageOutput(resolution.Attributes)
}

// ResolvedImageOutput supplies image classification for cooldown observations.
// It uses the retained request session and final provider identity. Failed
// metadata reads provide no classification evidence; request admission separately
// propagates those errors before any upstream image operation.
func ResolvedImageOutput(ctx context.Context, record *providercore.Record, model string) bool {
	if record == nil {
		return false
	}
	target, err := ProviderForProtocolAttempt(ctx, &ExecutionProvider{Record: *record})
	if err != nil {
		// Cooldown classification has no output evidence for an unresolved route.
		// The selector and forwarding preparation report that route error independently.
		return false
	}
	resolution, err := ResolveModelAttributes(ctx, target, model, nil)
	if err != nil {
		// Failed metadata cannot classify a diagnostic as an image failure. Request
		// admission separately returns the retained profile error before upstream execution.
		return false
	}
	supported := resolution.Attributes.OutputSupport("image")
	return supported != nil && *supported
}

// RequireImageGIF checks provider transport policy and final model capabilities.
// Administrators can declare formats for aliases through model attributes, but
// those declarations cannot override a provider's disabled GIF switch or native
// streaming-only endpoint. Unknown output formats do not establish GIF support.
func RequireImageGIF(value *ExecutionProvider, operation protocol.ProtocolID, attrs modelcatalog.Attributes) error {
	if value == nil || !ExecutionProtocolRecord(value).SupportsImageGIF(operation) || attrs.Media == nil || !slices.Contains(attrs.Media.OutputFormats, "gif") {
		return apperror.BadRequest("IMAGE_GIF_UNSUPPORTED", "the selected provider or model does not support GIF output")
	}
	return nil
}
