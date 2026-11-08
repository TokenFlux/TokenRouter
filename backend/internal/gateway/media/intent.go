package media

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// intent.go classifies image-generation intent and resolves image billing
// model and size evidence from request payloads.
// Image intent comes from image endpoints, tool declarations, and tool-choice
// evidence. Ordinary classification accepts passive image namespaces, while
// explicit classification accepts native declarations or selected functions.
// Final route attributes determine model output separately after provider
// mapping, so payload inspection cannot infer capabilities from an identity.
//
// Root JSON traversal keeps the first occurrence of each relevant member so
// duplicate fields agree with gjson lookup. Map inputs instead delegate
// their ordinary tool qualification to injected platform rules. Billing
// configuration follows its own precedence: the first native image tool, a
// confirmed converted image identity, then the registered native tool default.
// Those billing choices retain raw size evidence alongside a normalized
// tier and do not validate execution permission.

const (
	// OpenAIResponsesEndpoint names the canonical Responses route used by
	// gateway image policy callers and endpoint comparisons.
	OpenAIResponsesEndpoint = "/v1/responses" // canonical OpenAI Responses route

	// ResponsesLiteHeader, ResponsesLiteHeaderKey and
	// ResponsesLiteWSMetadataKey name the same opt-in signal in HTTP headers,
	// normalized header lookup and tunneled WebSocket request metadata.
	ResponsesLiteHeader        = "X-OpenAI-Internal-Codex-Responses-Lite"                   // HTTP Responses Lite header name
	ResponsesLiteHeaderKey     = "x-openai-internal-codex-responses-lite"                   // normalized Responses Lite header key
	ResponsesLiteWSMetadataKey = "ws_request_header_x_openai_internal_codex_responses_lite" // WebSocket tunneled Lite metadata key
	// ImageGenerationPermissionMessage is the client-facing denial text used
	// when the selected group disables image generation permission.
	ImageGenerationPermissionMessage = "Image generation is not enabled for this group" // group image permission denial message
)

// ResponsesImageSelection carries the tool identities used for provider eligibility.
// Models is immutable after context publication; empty identities retain an
// unspecified tool model until the route chooses conversion or the hosted default.
// Codex enables provider tool stripping.
type ResponsesImageSelection struct {
	Models    []string // declared image tool models
	GIFModels []string // tool identities requesting GIF output
	Codex     bool     // client uses Codex preparation
}

// responsesImageSelectionKey identifies one request or turn's image tool requirements.
type responsesImageSelectionKey struct{}

// ImageToolRules supplies platform predicates and string extraction for
// image intent decisions.
type ImageToolRules struct {
	// IsImageType, IsNamespaceName, HasTool, ToolChoice and FirstString are
	// retained function bindings. Callers supply each binding used by their
	// chosen path and keep captured state valid for policy calls.
	IsImageType     func(string) bool // image-generation type qualification callback
	IsNamespaceName func(string) bool // image namespace name qualification callback
	// HasTool is used by ordinary map classification after endpoint and model
	// checks. Explicit map classification examines native tools directly,
	// leaving this callback out of that path.
	HasTool func(map[string]any) bool // ordinary map tool qualification callback
	// ToolChoice handles ordinary any-valued tool choice and is also consulted
	// before explicit function-reference checks. The callback determines which
	// decoded platform shapes qualify.
	ToolChoice func(any) bool // ordinary arbitrary tool-choice qualifier
	// FirstString extracts model, type and size hints from map values.
	// JSON-result methods separately require string-typed values, so custom
	// callback coercion can make the representations differ.
	FirstString func(...any) string // injected nonempty-string extraction rule
}

// ImageIntentPolicy applies configured platform tool rules to gateway image
// classifications.
type ImageIntentPolicy struct {
	// rules retains its function bindings and their captured references by
	// value. NewImageIntentPolicy performs no callback validation, so callers
	// configure the paths they invoke.
	rules ImageToolRules // retained platform image tool rules
}

// OpenAIResponsesImageBillingConfig carries selected image model and
// separate billing size evidence.
type OpenAIResponsesImageBillingConfig struct {
	// Model follows the detailed resolver's tool/confirmed/default
	// precedence and may be empty. These resolvers select billing evidence
	// without validating that model for execution.
	Model     string // selected image billing model
	SizeTier  string // normalized image billing size tier
	InputSize string // trimmed selected raw size evidence
}

// imageGIFRequirementKey isolates native image intent from Responses tool choices.
type imageGIFRequirementKey struct{}

// WithResponsesImageSelection captures image tool identities for candidate checks.
// Each call replaces the previous turn's requirements, including an empty list;
// callers supply the client classification used by their forwarding preparation.
func WithResponsesImageSelection(ctx context.Context, body []byte, codex bool) context.Context {
	selection := ResponsesImageSelection{Models: ResponsesImageModels(body), Codex: codex}
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if tool.Get("type").String() == "image_generation" && strings.EqualFold(tool.Get("output_format").String(), "gif") {
			selection.GIFModels = append(selection.GIFModels, strings.TrimSpace(tool.Get("model").String()))
		}
	}
	return context.WithValue(ctx, responsesImageSelectionKey{}, selection)
}

// ResponsesImageSelectionFromContext returns the request's immutable image requirements.
// Missing contexts or values return an empty selection. Consumers may read the
// Models slice during selection but must leave its contents unchanged.
func ResponsesImageSelectionFromContext(ctx context.Context) ResponsesImageSelection {
	if ctx == nil {
		return ResponsesImageSelection{}
	}
	value, _ := ctx.Value(responsesImageSelectionKey{}).(ResponsesImageSelection)
	return value
}

// ResponsesImageModels reads every native image tool's selected model.
// An omitted model stays empty until route preparation selects the hosted default
// or moves an image-only request into its tool. The surrounding model name is
// never used as evidence of output capability.
func ResponsesImageModels(body []byte) []string {
	var models []string
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if tool.Get("type").String() != "image_generation" {
			continue
		}
		model := strings.TrimSpace(tool.Get("model").String())
		models = append(models, model)
	}
	return models
}

// NewImageIntentPolicy retains the supplied platform rule functions in a
// value policy. It performs no callback validation; callers provide the
// bindings reached by their JSON, map or billing-configuration paths.
func NewImageIntentPolicy(rules ImageToolRules) ImageIntentPolicy {
	return ImageIntentPolicy{rules: rules}
}

// GroupImagePermission permits requests when no group is present, otherwise
// returning the supplied permission flag. Callers resolve group presence
// and authorization beforehand; this helper reads no group configuration
// itself.
func GroupImagePermission(groupPresent, allowed bool) bool { return !groupPresent || allowed }

// IsOpenAIResponsesLiteHeader recognizes the trimmed, case-insensitive
// string true as the opt-in value. Other values, including empty, return
// false; callers locate the header before passing its contents.
func (p ImageIntentPolicy) IsOpenAIResponsesLiteHeader(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

// IsOpenAIResponsesLiteWebSocketPayload checks the tunneled Lite metadata
// key in a valid JSON payload. Empty or invalid body returns false; the
// extracted value uses the same trimmed, case-insensitive true recognition
// as headers.
func (p ImageIntentPolicy) IsOpenAIResponsesLiteWebSocketPayload(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	return p.IsOpenAIResponsesLiteHeader(gjson.GetBytes(body, "client_metadata."+ResponsesLiteWSMetadataKey).String())
}

// IsImageGenerationIntent recognizes image endpoints and body tool declarations.
// Invalid bodies provide no tool evidence; model identifiers are deliberately
// left for final-route capability resolution after provider mapping.
//
// Ordinary declarations include native image tools, namespaces, and additional
// input tools. Each relevant root member is considered once, retaining the
// first duplicate member consistently with gjson lookups. Positive evidence
// ends scanning immediately, while absence of evidence keeps the request on
// its ordinary entry protocol until route attributes are available.
func (p ImageIntentPolicy) IsImageGenerationIntent(endpoint string, requestedModel string, body []byte) bool {
	if p.IsImageGenerationEndpoint(endpoint) {
		return true
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}

	var toolsSeen, inputSeen, toolChoiceSeen bool
	imageIntent := false
	wirejson.ParseView(body).ForEach(func(key, value gjson.Result) bool {
		// Track the first root member for each recognized key, matching GetBytes
		// so later duplicates cannot replace the evidence already examined.
		switch key.Str {
		case "tools":
			if !toolsSeen {
				toolsSeen = true
				imageIntent = p.OpenAIJSONToolsContainImageGeneration(value)
			}
		case "input":
			if !inputSeen {
				inputSeen = true
				imageIntent = p.OpenAIJSONInputContainsImageGenTool(value)
			}
		case "tool_choice":
			if !toolChoiceSeen {
				toolChoiceSeen = true
				imageIntent = p.OpenAIJSONToolChoiceSelectsImageGeneration(value)
			}
		}
		return !imageIntent && (!toolsSeen || !inputSeen || !toolChoiceSeen)
	})
	return imageIntent
}

// IsExplicitImageGenerationIntent recognizes image endpoints, native tools,
// and explicitly selected image functions. Passive namespaces and additional
// input tools do not establish explicit intent; model capability is resolved
// independently for the final provider route.
//
// Each root tool member retains its first occurrence, so a later duplicate
// cannot change the meaning already examined. Tool choice may select a
// namespace or nested image function reference. The scan stops once positive
// evidence is available and leaves invalid JSON to ingress validation.
func (p ImageIntentPolicy) IsExplicitImageGenerationIntent(endpoint string, requestedModel string, body []byte) bool {
	if p.IsImageGenerationEndpoint(endpoint) {
		return true
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	// Track explicit-mode root members independently, so a later duplicate
	// cannot introduce intent after the first value for that key supplied
	// none.
	var toolsSeen, toolChoiceSeen bool
	imageIntent := false
	wirejson.ParseView(body).ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "tools":
			if !toolsSeen {
				toolsSeen = true
				imageIntent = p.OpenAIJSONToolsContainNativeImageGeneration(value)
			}
		case "tool_choice":
			if !toolChoiceSeen {
				toolChoiceSeen = true
				imageIntent = p.OpenAIJSONToolChoiceSelectsExplicitImageGeneration(value)
			}
		}
		return !imageIntent && (!toolsSeen || !toolChoiceSeen)
	})
	return imageIntent
}

// IsImageGenerationIntentForPlatform chooses ordinary or explicit
// classification using explicitOnly. Callers decide that platform flag;
// ordinary mode accepts passive declarations, while explicit mode uses
// endpoint/native-tool or selected-function evidence.
func (p ImageIntentPolicy) IsImageGenerationIntentForPlatform(endpoint string, requestedModel string, body []byte, explicitOnly bool) bool {
	if !explicitOnly {
		return p.IsImageGenerationIntent(endpoint, requestedModel, body)
	}
	return p.IsExplicitImageGenerationIntent(endpoint, requestedModel, body)
}

// IsImageGenerationIntentMap recognizes endpoint evidence before
// consulting a decoded request map. Nil map returns false after those
// checks; ordinary map tool and choice qualification delegate to the
// required injected HasTool and ToolChoice rules.
func (p ImageIntentPolicy) IsImageGenerationIntentMap(endpoint string, requestedModel string, reqBody map[string]any) bool {
	if p.IsImageGenerationEndpoint(endpoint) {
		return true
	}
	if reqBody == nil {
		return false
	}
	if p.rules.HasTool(reqBody) {
		return true
	}
	return p.OpenAIAnyToolChoiceSelectsImageGeneration(reqBody["tool_choice"])
}

// IsExplicitImageGenerationIntentMap checks image endpoints, native
// tools and explicit tool choice. Nil map supplies no body
// evidence; passive declaration qualification through HasTool is skipped on
// this path.
func (p ImageIntentPolicy) IsExplicitImageGenerationIntentMap(endpoint string, requestedModel string, reqBody map[string]any) bool {
	if p.IsImageGenerationEndpoint(endpoint) {
		return true
	}
	if reqBody == nil {
		return false
	}
	if p.OpenAIAnyToolsContainNativeImageGeneration(reqBody["tools"]) {
		return true
	}
	return p.OpenAIAnyToolChoiceSelectsExplicitImageGeneration(reqBody["tool_choice"])
}

// IsImageGenerationEndpoint recognizes generation/edit routes with or
// without the v1 prefix after normalization. The normalizer accepts the
// OpenAI HTTPS origin spelling, optional query and trailing slashes; other
// normalized routes return false.
func (p ImageIntentPolicy) IsImageGenerationEndpoint(endpoint string) bool {
	switch p.NormalizeImageGenerationEndpoint(endpoint) {
	case "/v1/images/generations", "/v1/images/edits", "/images/generations", "/images/edits":
		return true
	default:
		return false
	}
}

// NormalizeImageGenerationEndpoint trims and lowercases endpoint text,
// removes the OpenAI HTTPS origin prefix and query, then drops trailing
// slashes. Empty input returns empty; it does not generally parse or
// validate arbitrary URLs.
func (p ImageIntentPolicy) NormalizeImageGenerationEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(strings.ToLower(endpoint))
	if endpoint == "" {
		return ""
	}
	endpoint = strings.TrimPrefix(endpoint, "https://api.openai.com")
	if idx := strings.IndexByte(endpoint, '?'); idx >= 0 {
		endpoint = endpoint[:idx]
	}
	return strings.TrimRight(endpoint, "/")
}

// OpenAIJSONToolsContainImageGeneration checks an array for qualified image
// types or image namespace declarations. Nonarrays return false, and the
// first qualifying entry ends traversal through the injected type/name
// rules.
func (p ImageIntentPolicy) OpenAIJSONToolsContainImageGeneration(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, item gjson.Result) bool {
		if p.IsOpenAIImageGenerationType(p.OpenAIJSONString(item.Get("type"))) {
			found = true
			return false
		}
		if p.IsImageGenNamespaceTool(item) {
			found = true
			return false
		}
		return true
	})
	return found
}

// OpenAIJSONToolsContainNativeImageGeneration checks array entries for
// qualified native image-generation types. Nonarrays return false;
// namespace declarations alone do not qualify, and the first matching type
// ends traversal.
func (p ImageIntentPolicy) OpenAIJSONToolsContainNativeImageGeneration(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, item gjson.Result) bool {
		found = p.IsOpenAIImageGenerationType(p.OpenAIJSONString(item.Get("type")))
		return !found
	})
	return found
}

// OpenAIAnyToolsContainNativeImageGeneration accepts a decoded []any
// containing map[string]any tools with qualified type strings. Other
// container/entry shapes are skipped, and the required
// FirstString/IsImageType callbacks determine qualification during
// scanning.
func (p ImageIntentPolicy) OpenAIAnyToolsContainNativeImageGeneration(rawTools any) bool {
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if ok && p.IsOpenAIImageGenerationType(p.rules.FirstString(tool["type"])) {
			return true
		}
	}
	return false
}

// IsOpenAIImageGenerationType delegates type qualification to the
// configured platform predicate.
func (p ImageIntentPolicy) IsOpenAIImageGenerationType(value string) bool {
	return p.rules.IsImageType(value)
}

// IsOpenAIImageGenNamespaceName delegates namespace-name qualification to
// the configured platform predicate.
func (p ImageIntentPolicy) IsOpenAIImageGenNamespaceName(value string) bool {
	return p.rules.IsNamespaceName(value)
}

// IsImageGenNamespaceTool recognizes exact namespace type with a qualified
// image namespace name. JSON values must be strings after trimming, while
// name recognition follows the configured platform rule.
func (p ImageIntentPolicy) IsImageGenNamespaceTool(tool gjson.Result) bool {
	return p.OpenAIJSONString(tool.Get("type")) == "namespace" &&
		p.IsOpenAIImageGenNamespaceName(p.OpenAIJSONString(tool.Get("name")))
}

// OpenAIJSONInputContainsImageGenTool checks array entries whose string
// type is additional_tools for ordinary image declarations. Nonarrays or
// other item types contribute no evidence, and the first matching nested
// tools array ends scanning.
func (p ImageIntentPolicy) OpenAIJSONInputContainsImageGenTool(input gjson.Result) bool {
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if p.OpenAIJSONString(item.Get("type")) != "additional_tools" {
			return true
		}
		found = p.OpenAIJSONToolsContainImageGeneration(item.Get("tools"))
		return !found
	})
	return found
}

// OpenAIRequestBodyHasImageGenerationDeclaration checks tools,
// additional_tools input and tool_choice in valid JSON. Empty or invalid
// bytes return false; endpoint and model evidence are handled by other
// intent entry points.
func (p ImageIntentPolicy) OpenAIRequestBodyHasImageGenerationDeclaration(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	return p.OpenAIJSONToolsContainImageGeneration(gjson.GetBytes(body, "tools")) ||
		p.OpenAIJSONInputContainsImageGenTool(gjson.GetBytes(body, "input")) ||
		p.OpenAIJSONToolChoiceSelectsImageGeneration(gjson.GetBytes(body, "tool_choice"))
}

// OpenAIRequestBodyImageGenerationToolNeedsNormalization finds exact
// image_generation tool declarations carrying format or compression
// members. Empty, invalid or nonarray tools input returns false; member
// existence qualifies even when its value is null.
func (p ImageIntentPolicy) OpenAIRequestBodyImageGenerationToolNeedsNormalization(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	needsNormalization := false
	tools.ForEach(func(_, item gjson.Result) bool {
		if p.OpenAIJSONString(item.Get("type")) != "image_generation" {
			return true
		}
		// Existing format or compression members require map-based normalization
		// before forwarding, including null values whose presence still marks the
		// legacy tool shape.
		if item.Get("format").Exists() || item.Get("compression").Exists() {
			needsNormalization = true
			return false
		}

		return true
	})
	return needsNormalization
}

// OpenAIJSONToolChoiceSelectsImageGeneration recognizes qualified
// string/type, namespace and function-name choices, including a nested tool
// object. Missing or incompatible shapes return false; injected type/name
// rules determine accepted spellings after JSON string extraction.
func (p ImageIntentPolicy) OpenAIJSONToolChoiceSelectsImageGeneration(choice gjson.Result) bool {
	if !choice.Exists() {
		return false
	}
	if choice.Type == gjson.String {
		return p.IsOpenAIImageGenerationType(choice.String())
	}
	if !choice.IsObject() {
		return false
	}
	choiceType := p.OpenAIJSONString(choice.Get("type"))
	if p.IsOpenAIImageGenerationType(choiceType) {
		return true
	}
	if choiceType == "namespace" &&
		(p.IsOpenAIImageGenNamespaceName(p.OpenAIJSONString(choice.Get("name"))) ||
			p.IsOpenAIImageGenNamespaceName(p.OpenAIJSONString(choice.Get("namespace")))) {
		return true
	}
	if tool := choice.Get("tool"); tool.IsObject() && p.OpenAIJSONToolChoiceSelectsImageGeneration(tool) {
		return true
	}
	if p.IsOpenAIImageGenerationType(p.OpenAIJSONString(choice.Get("function.name"))) {
		return true
	}
	return false
}

// OpenAIJSONToolChoiceSelectsExplicitImageGeneration accepts ordinary
// selected image choices, then additional imagegen function-reference
// forms. Object choices can nest tool or function objects; incompatible
// shapes return false after ordinary qualification.
func (p ImageIntentPolicy) OpenAIJSONToolChoiceSelectsExplicitImageGeneration(choice gjson.Result) bool {
	if p.OpenAIJSONToolChoiceSelectsImageGeneration(choice) {
		return true
	}
	if !choice.IsObject() {
		return false
	}
	if tool := choice.Get("tool"); tool.IsObject() && p.OpenAIJSONToolChoiceSelectsExplicitImageGeneration(tool) {
		return true
	}
	if p.IsOpenAIImageGenFunctionReference(
		p.OpenAIJSONString(choice.Get("namespace")),
		p.OpenAIJSONString(choice.Get("name")),
	) {
		return true
	}
	if fn := choice.Get("function"); fn.IsObject() {
		return p.IsOpenAIImageGenFunctionReference(
			p.OpenAIJSONString(fn.Get("namespace")),
			p.OpenAIJSONString(fn.Get("name")),
		)
	}
	return false
}

// IsOpenAIImageGenFunctionReference recognizes trimmed namespace/name
// image_gen and imagegen, or the qualified names image_gen.imagegen and
// image_gen__imagegen. Matching uses those case-sensitive literal
// spellings; other namespace/name pairs and function names return false.
func (p ImageIntentPolicy) IsOpenAIImageGenFunctionReference(namespace string, name string) bool {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if namespace == "image_gen" && name == "imagegen" {
		return true
	}
	switch name {
	case "image_gen.imagegen", "image_gen__imagegen":
		return true
	default:
		return false
	}
}

// OpenAIAnyToolChoiceSelectsImageGeneration delegates decoded choice
// qualification to the configured platform rule.
func (p ImageIntentPolicy) OpenAIAnyToolChoiceSelectsImageGeneration(choice any) bool {
	return p.rules.ToolChoice(choice)
}

// OpenAIAnyToolChoiceSelectsExplicitImageGeneration first delegates
// ordinary qualification, then checks map-based nested tool/function
// references. Nonmaps return false after that check; callers supply acyclic
// decoded request maps for recursive tool traversal.
func (p ImageIntentPolicy) OpenAIAnyToolChoiceSelectsExplicitImageGeneration(choice any) bool {
	if p.OpenAIAnyToolChoiceSelectsImageGeneration(choice) {
		return true
	}
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return false
	}
	if tool, ok := choiceMap["tool"].(map[string]any); ok && p.OpenAIAnyToolChoiceSelectsExplicitImageGeneration(tool) {
		return true
	}
	if p.IsOpenAIImageGenFunctionReference(
		p.rules.FirstString(choiceMap["namespace"]),
		p.rules.FirstString(choiceMap["name"]),
	) {
		return true
	}
	if fn, ok := choiceMap["function"].(map[string]any); ok {
		return p.IsOpenAIImageGenFunctionReference(
			p.rules.FirstString(fn["namespace"]),
			p.rules.FirstString(fn["name"]),
		)
	}
	return false
}

// ResolveOpenAIResponsesImageBillingConfigDetailed chooses image model and
// raw size from a decoded request map, returning a normalized size tier.
// The first exact image_generation tool supplies model/size; missing tool
// size falls back to the body size.
//
// A missing tool model uses confirmedImageModel supplied by route conversion.
// If that identity is also absent, a native tool uses gpt-image-2. With neither
// evidence source the result stays empty. Callers validate the selected model's
// capabilities separately; unknown sizes retain their raw spelling while tier
// normalization uses the existing billing default.
func (p ImageIntentPolicy) ResolveOpenAIResponsesImageBillingConfigDetailed(reqBody map[string]any, confirmedImageModel string) (OpenAIResponsesImageBillingConfig, error) {
	imageModel := ""
	imageSize := ""
	hasImageTool := false
	if reqBody != nil {
		rawTools, _ := reqBody["tools"].([]any)
		for _, rawTool := range rawTools {
			toolMap, ok := rawTool.(map[string]any)
			if !ok || strings.TrimSpace(p.rules.FirstString(toolMap["type"])) != "image_generation" {
				continue
			}
			hasImageTool = true
			imageModel = strings.TrimSpace(p.rules.FirstString(toolMap["model"]))
			imageSize = strings.TrimSpace(p.rules.FirstString(toolMap["size"]))
			break
		}
		if imageSize == "" {
			imageSize = strings.TrimSpace(p.rules.FirstString(reqBody["size"]))
		}
	}

	// Conversion can retain a confirmed image identity alongside its text driver.
	// Prefer that identity before the registered default for an unspecified native tool.
	if imageModel == "" {
		imageModel = strings.TrimSpace(confirmedImageModel)
	}
	if imageModel == "" && hasImageTool {
		imageModel = "gpt-image-2"
	}
	sizeTier := NormalizeImageSizeTier(imageSize)
	return OpenAIResponsesImageBillingConfig{
		Model:     imageModel,
		SizeTier:  sizeTier,
		InputSize: imageSize,
	}, nil
}

// ResolveOpenAIResponsesImageBillingConfigFromBody returns the selected
// model and normalized size tier from the detailed byte resolver. Raw
// selected size evidence is omitted from this pair; confirmedImageModel
// participates when body evidence supplies no model.
func (p ImageIntentPolicy) ResolveOpenAIResponsesImageBillingConfigFromBody(body []byte, confirmedImageModel string) (string, string, error) {
	cfg, err := p.ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, confirmedImageModel)
	if err != nil {
		return "", "", err
	}
	return cfg.Model, cfg.SizeTier, nil
}

// ResolveOpenAIResponsesImageBillingConfigDetailedFromBody selects image
// billing evidence from valid JSON, returning model, normalized tier and
// raw size. The first native image tool supplies model/size; absent tool
// size uses body size, and the first root members follow gjson lookup.
//
// A missing tool model uses the caller's confirmed converted image identity,
// then the registered gpt-image-2 default when a native tool exists. A text
// driver's model field supplies no image identity. Invalid or empty bodies
// retain only confirmedImageModel and the default size tier; callers perform
// final route capability validation before sending the selected operation.
func (p ImageIntentPolicy) ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body []byte, confirmedImageModel string) (OpenAIResponsesImageBillingConfig, error) {
	imageModel := ""
	imageSize := ""
	hasImageTool := false
	if len(body) > 0 && gjson.ValidBytes(body) {
		tools := gjson.GetBytes(body, "tools")
		if tools.IsArray() {
			tools.ForEach(func(_, item gjson.Result) bool {
				if p.OpenAIJSONString(item.Get("type")) != "image_generation" {
					return true
				}
				hasImageTool = true
				imageModel = p.OpenAIJSONString(item.Get("model"))
				imageSize = p.OpenAIJSONString(item.Get("size"))
				return false
			})
		}
		if imageSize == "" {
			imageSize = p.OpenAIJSONString(gjson.GetBytes(body, "size"))
		}
	}
	// Conversion can retain a confirmed image identity alongside its text driver.
	// Prefer that identity before the registered default for an unspecified native tool.
	if imageModel == "" {
		imageModel = strings.TrimSpace(confirmedImageModel)
	}
	if imageModel == "" && hasImageTool {
		imageModel = "gpt-image-2"
	}
	return OpenAIResponsesImageBillingConfig{
		Model:     imageModel,
		SizeTier:  NormalizeImageSizeTier(imageSize),
		InputSize: imageSize,
	}, nil
}

// OpenAIJSONString returns trimmed content only for a JSON string result.
// Missing, null and nonstring values return empty, keeping byte-based
// policy extraction distinct from any coercion supplied by map callbacks.
func (p ImageIntentPolicy) OpenAIJSONString(value gjson.Result) string {
	if value.Type != gjson.String {
		return ""
	}
	return strings.TrimSpace(value.String())
}

// WithImageGIFRequirement marks a native image request for candidate filtering.
// The flag belongs to this request context and survives retries; ordinary image
// requests explicitly carry false so reused contexts cannot retain earlier intent.
func WithImageGIFRequirement(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, imageGIFRequirementKey{}, enabled)
}

// ImageGIFRequired reports whether native image execution requires GIF output.
func ImageGIFRequired(ctx context.Context) bool {
	enabled, _ := ctx.Value(imageGIFRequirementKey{}).(bool)
	return enabled
}
