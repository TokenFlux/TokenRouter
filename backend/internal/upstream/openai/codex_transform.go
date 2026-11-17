package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// codex_transform.go normalizes Codex OAuth requests, tool history,
// instructions, and image-generation controls for Responses.
// Codex OAuth requests inline reasoning history under store=false, while tool
// calls, call results, and item references use distinct identities. Request
// transformation captures continuation intent before filtering, normalizes
// recognized call pairs, and keeps valid remote item identities when
// continuation needs them. Reserved tool aliases are collected after legacy
// protocol fields have become Responses nodes so response restoration can use
// the same names.
//
// Decoded requests are mutated across several phases, and an alias collision
// can return after earlier modifications have already occurred. Input filtering
// copies changed maps while sharing nested values; other normalization helpers
// can mutate tool and system-message maps directly. Image helpers keep the
// Responses text driver separate from the image tool's model and options.
// Callers inspect transformation results and retain exclusive access to the
// decoded request during mutation.

// These values coordinate the Responses text driver, bounded call identities,
// and fixed guidance markers used during OAuth request compatibility handling.
const (
	// ImagesResponsesMainModel is the API-key text driver for image-only request
	// adaptation. OAuth callers resolve their driver from provider configuration.
	ImagesResponsesMainModel = "gpt-5.4-mini"

	// CodexCallIDMaxLength bounds normalized identifiers, and CodexCallIDPrefix
	// names the function namespace; normalizers obtain actual prefixes from shared
	// protocol rules.
	CodexCallIDMaxLength = 64
	CodexCallIDPrefix    = "fc_"

	// CodexImageGenerationFunctionToolName identifies the client function that
	// prevents hosted-image injection from duplicating a declared image
	// capability.
	CodexImageGenerationFunctionToolName = "image_gen.imagegen"

	// These marker/text pairs support duplicate detection and fixed image
	// guidance; payload strings are shared with current and legacy compatibility
	// consumers.
	CodexImageGenerationBridgeMarker = "<tokenrouter-codex-image-generation>"
	CodexImageGenerationBridgeText   = CodexImageGenerationBridgeMarker + "\nWhen the user asks for raster image generation or editing, use the OpenAI Responses native `image_generation` tool attached to this request. The local Codex client may not expose an `image_gen` namespace, but that does not mean image generation is unavailable. Do not ask the user to switch to CLI fallback solely because `image_gen` is absent.\n</tokenrouter-codex-image-generation>"
	CodexSparkImageUnsupportedMarker = "<tokenrouter-codex-spark-image-unsupported>"
	CodexSparkImageUnsupportedText   = CodexSparkImageUnsupportedMarker + "\nThe current model is gpt-5.3-codex-spark, which does not support image generation, image editing, image input, the `image_generation` tool, or Codex `image_gen`/`$imagegen` workflows. If the user asks for image generation or image editing, clearly explain this model limitation and ask them to switch to a non-Spark Codex model such as gpt-5.3-codex or gpt-5.4. Do not claim that the local environment merely lacks image_gen tooling, and do not suggest CLI fallback as the primary fix while the model remains Spark.\n</tokenrouter-codex-spark-image-unsupported>"
)

// Endpoint field lists are shared transformation policy; request writers read
// them after shared compatibility normalization and leave their contents
// stable.
var (
	// OpenAIChatGPTInternalUnsupportedFields names internal endpoint fields
	// removed by both decoded Codex transformation and raw OAuth passthrough
	// normalization.
	OpenAIChatGPTInternalUnsupportedFields = []string{
		"chat_template_kwargs",
		"user",
		"metadata",
		"prompt_cache_retention",
		"safety_identifier",
		"stream_options",
		"truncation",
		"stop_sequences",
	}

	// OpenAICodexOAuthUnsupportedFields adds Codex sampling/output fields to the
	// internal list, with temperature/top_p exceptions controlled by supplied
	// attributes.
	OpenAICodexOAuthUnsupportedFields = append([]string{
		"max_output_tokens",
		"max_completion_tokens",
		"temperature",
		"top_p",
		"frequency_penalty",
		"presence_penalty",
	}, OpenAIChatGPTInternalUnsupportedFields...)
)

// CodexTransformResult reports applied request transformations, observed
// request identity, and response-name restoration data.
type CodexTransformResult struct {
	// Modified records applied mutation or input-array rebuilding; an equivalent
	// filtered array can still set it, so it is not a byte-diff guarantee.
	Modified        bool
	NormalizedModel string // trimmed top-level request model
	// PromptCacheKey captures the trimmed supplied cache key before a
	// Messages-bridge request removes its wire field for endpoint compatibility.
	PromptCacheKey string
	// ToolNameReverse maps generated aliases to client tool names after protocol
	// migration; response writers use it to restore tool spelling.
	ToolNameReverse map[string]string
	// Error reports alias failure after earlier request phases may have mutated
	// maps; callers inspect it before sending the partially transformed request.
	Error error
}

// CodexOAuthTransformOptions selects model policy and compatibility handling
// for one decoded OAuth request.
type CodexOAuthTransformOptions struct {
	// Attributes permits supplied temperature and top_p capability overrides during
	// unsupported-field removal; the transform reads this pointer without copying
	// it.
	Attributes *modelcatalog.Attributes
	// Rules replaces resolved model execution rules when supplied; a disabled
	// image-tool rule selects the fixed Spark guidance and declaration cleanup.
	Rules *modelrules.ExecutionRules
	// IsMessagesBridge identifies cache-key compatibility handling and is required
	// when prompt_cache_key is a string; the gateway wrapper installs its detector.
	IsMessagesBridge func(map[string]any) bool
	// IsCodexCLI is passed to ApplyInstructions as a client hint; that helper
	// currently selects guidance by model without consulting this flag.
	IsCodexCLI bool
	// IsCompact removes store and stream and bypasses ordinary encrypted-reasoning
	// include insertion; other input and tool compatibility phases still run.
	IsCompact bool
	// SkipDefaultInstructions disables synthesized guidance for missing or unusable
	// instructions; system-message promotion still runs before that
	// default-selection phase.
	SkipDefaultInstructions bool
	// PreserveToolCallIDs retains short supplied call IDs during input filtering;
	// overlong identifiers still receive normalized compact type-specific
	// identities.
	PreserveToolCallIDs bool
	// OmitPromotedSystemMessagesFromInput removes pure-text system messages after
	// promotion; mixed or malformed messages remain as developer input with shared
	// map ownership.
	OmitPromotedSystemMessagesFromInput bool
}

// CodexInputFilterOptions selects reference retention and call-ID handling for
// replayed input items.
type CodexInputFilterOptions struct {
	// PreserveReferences retains reference items and permits valid ordinary item
	// IDs; reasoning IDs are still removed for inline store=false replay.
	PreserveReferences bool
	// PreserveCallIDs retains supplied IDs within the byte limit; longer IDs still
	// use the normalization rule needed for accepted upstream call identities.
	PreserveCallIDs bool
}

// NormalizeCodexCallID applies function-call identity normalization to a
// supplied call identifier. Empty input stays empty, and overlong normalized
// IDs receive the stable compact representation. Item types with their own
// prefix use NormalizeCodexCallIDForItemType.
func NormalizeCodexCallID(id string) string {
	return NormalizeCodexCallIDForItemType("function_call", id)
}

// NormalizeCodexCallIDForItemType normalizes a call ID using the protocol
// prefix for its item type. Empty IDs stay empty. The existing-prefix check
// accepts the bare prefix spelling, while call_ and known alternate prefixes
// receive the selected prefix.
//
// A normalized candidate exceeding the sixty-four-byte limit is compacted with
// a stable digest. Callers apply the same item-type identity rule to calls,
// outputs, and mapped legacy references so replay pairs continue to agree.
// Input is not trimmed here, and caller policy decides whether short existing
// IDs should bypass normalization.
func NormalizeCodexCallIDForItemType(itemType, id string) string {
	prefix := protocolopenai.OpenAIResponsesToolCallIDPrefix(itemType) + "_"
	candidate := id
	// Normalize the selected namespace before length compaction, keeping call/result
	// candidates consistent when legacy call_ or another known type prefix is supplied.
	switch {
	case id == "":
		return ""
	case strings.HasPrefix(id, strings.TrimSuffix(prefix, "_")):
	case strings.HasPrefix(id, "call_"):
		candidate = prefix + strings.TrimPrefix(id, "call_")
	default:
		candidate = prefix + TrimOpenAIResponsesKnownCallIDPrefix(id)
	}
	if len(candidate) <= CodexCallIDMaxLength {
		return candidate
	}
	return CompactCodexCallIDForItemType(itemType, candidate)
}

// CompactCodexCallIDForItemType returns a type-prefixed digest representation
// limited to CodexCallIDMaxLength bytes. The hash includes a stable domain and
// the supplied ID. Callers normalize candidate spelling before invoking it when
// matching replay identities matters.
//
// The prefix occupies part of the fixed length and the digest supplies the
// remaining suffix. Keeping the domain and candidate input consistent lets
// independently transformed call/result copies produce matching IDs. This is a
// deterministic identifier rewrite, not a lookup of a remote item object;
// item-reference retention is handled separately.
func CompactCodexCallIDForItemType(itemType, id string) string {
	prefix := protocolopenai.OpenAIResponsesToolCallIDPrefix(itemType) + "_"
	// The stable digest domain and candidate spelling couple separately
	// transformed call/result IDs; changing either can break pairing with retained
	// replay history.
	digest := sha256.Sum256([]byte("sub2api:codex-call-id:v1:" + id))
	encoded := hex.EncodeToString(digest[:])
	return prefix + encoded[:CodexCallIDMaxLength-len(prefix)]
}

// TrimOpenAIResponsesKnownCallIDPrefix removes one recognized fc_, ctc_, or
// tsc_ prefix from an ID. Unmatched values pass through unchanged. It does not
// trim whitespace or remove the legacy call_ prefix.
func TrimOpenAIResponsesKnownCallIDPrefix(id string) string {
	for _, prefix := range []string{"fc_", "ctc_", "tsc_"} {
		if after, ok := strings.CutPrefix(id, prefix); ok {
			return after
		}
	}
	return id
}

// ApplyCodexOAuthTransformWithOptions mutates a nonnil decoded request for
// Codex OAuth and returns model, cache-key, alias, and modification
// observations. Callers serialize mutation and inspect Error before forwarding.
// IsMessagesBridge is required when prompt_cache_key is a string; the gateway
// wrapper supplies that detector.
//
// Shared compatibility runs before continuation intent is captured. Legacy
// functions and choices then become Responses tools before reserved-name
// aliases and choice validation are applied. Alias failure can leave earlier
// mutations in place. Compact removes store/stream, ordinary execution forces
// inline streaming, and input-array processing reports Modified even when
// filtering produces equivalent values. Model rules can disable image tools and
// insert the fixed Spark guidance text, including when an override supplies
// that rule for another model.
func ApplyCodexOAuthTransformWithOptions(reqBody map[string]any, opts CodexOAuthTransformOptions) CodexTransformResult {
	result := CodexTransformResult{}
	if protocolopenai.NormalizeOpenAIOAuthResponsesCompatibilityFields(reqBody) {
		result.Modified = true
	}
	// Capture continuation intent before later input cleanup, so references needed
	// to resolve tool results remain eligible for preservation during filtering.
	needsToolContinuation := protocolopenai.NeedsToolContinuation(reqBody)

	model := ""
	if v, ok := reqBody["model"].(string); ok {
		model = v
	}
	normalizedModel := strings.TrimSpace(model)
	if normalizedModel != "" {
		if model != normalizedModel {
			reqBody["model"] = normalizedModel
			result.Modified = true
		}
		result.NormalizedModel = normalizedModel
	}

	if opts.IsCompact {
		if _, ok := reqBody["store"]; ok {
			delete(reqBody, "store")
			result.Modified = true
		}
		if _, ok := reqBody["stream"]; ok {
			delete(reqBody, "stream")
			result.Modified = true
		}
	} else {
		// Ordinary internal Responses execution consumes inline history and streaming
		// data; override supplied storage/stream flags together before replay input
		// is filtered.
		if v, ok := reqBody["store"].(bool); !ok || v {
			reqBody["store"] = false
			result.Modified = true
		}
		if v, ok := reqBody["stream"].(bool); !ok || !v {
			reqBody["stream"] = true
			result.Modified = true
		}
	}

	// Apply endpoint removal after shared compatibility normalization, retaining
	// temperature/top_p when explicit model attributes allow those otherwise
	// listed fields.
	for _, key := range OpenAICodexOAuthUnsupportedFields {
		if opts.Attributes != nil {
			if key == "temperature" && opts.Attributes.Temperature != nil && *opts.Attributes.Temperature {
				continue
			}
			if key == "top_p" && opts.Attributes.TopP != nil && *opts.Attributes.TopP {
				continue
			}
		}
		if _, ok := reqBody[key]; ok {
			delete(reqBody, key)
			result.Modified = true
		}
	}

	// Inline store=false history depends on returned encrypted reasoning; Compact
	// keeps its separate request shape and skips this include-field insertion.
	if !opts.IsCompact && EnsureCodexReasoningInclude(reqBody) {
		result.Modified = true
	}

	// Legacy arrays replace tools before alias collection, and their legacy keys
	// are removed even when an unsupported value cannot produce declarations.
	if functionsRaw, ok := reqBody["functions"]; ok {
		if functions, k := functionsRaw.([]any); k {
			tools := make([]any, 0, len(functions))
			for _, f := range functions {
				tools = append(tools, map[string]any{
					"type":     "function",
					"function": f,
				})
			}
			reqBody["tools"] = tools
		}
		delete(reqBody, "functions")
		result.Modified = true
	}

	if fcRaw, ok := reqBody["function_call"]; ok {
		if fcStr, ok := fcRaw.(string); ok {
			// e.g. "auto", "none"
			reqBody["tool_choice"] = fcStr
		} else if fcObj, ok := fcRaw.(map[string]any); ok {
			// e.g. {"name": "my_func"}
			if name, ok := fcObj["name"].(string); ok && strings.TrimSpace(name) != "" {
				reqBody["tool_choice"] = map[string]any{
					"type": "function",
					"name": name,
				}
			}
		}
		// Legacy choice spelling has been considered before removal; alias collection
		// must see any replacement name on its final Responses choice node.
		delete(reqBody, "function_call")
		result.Modified = true
	}

	if NormalizeCodexTools(reqBody) {
		result.Modified = true
	}
	// Collect aliases only after prompt/functions/function_call compatibility
	// has produced the final Responses protocol nodes. Otherwise references
	// introduced by those migrations can retain the reserved name.
	toolNameReverse, toolNamesChanged, err := AliasOpenAIOAuthReservedToolNames(reqBody)
	if err != nil {
		result.Error = err
		return result
	}
	result.ToolNameReverse = toolNameReverse
	if toolNamesChanged {
		result.Modified = true
	}
	if NormalizeCodexToolChoice(reqBody) {
		result.Modified = true
	}

	if v, ok := reqBody["prompt_cache_key"].(string); ok {
		result.PromptCacheKey = strings.TrimSpace(v)
		if opts.IsMessagesBridge(reqBody) {
			delete(reqBody, "prompt_cache_key")
			result.Modified = true
		}
	}

	// Promote system text before default injection, retaining mixed content as
	// developer input and permitting the bridge to omit text-only eligible items.
	// Extra part metadata is outside that eligibility check and can be discarded.
	if ExtractSystemMessagesFromInput(reqBody, opts.OmitPromotedSystemMessagesFromInput) {
		result.Modified = true
	}

	// Preserve supplied usable instruction text; default injection fills missing
	// or unusable values only after system promotion has contributed its guidance.
	if !opts.SkipDefaultInstructions && ApplyInstructions(reqBody, opts.IsCodexCLI) {
		result.Modified = true
	}
	rules := (modelrules.View{}).Resolve(modelrules.Target{Platform: "openai", ProviderType: "oauth", Model: normalizedModel}, modelcatalog.Attributes{}).Rules
	if opts.Rules != nil {
		rules = *opts.Rules
	}
	imageToolsDisabled := rules.ImageGenerationTool != nil && !*rules.ImageGenerationTool
	if imageToolsDisabled && ApplyCodexSparkImageUnsupportedInstructions(reqBody) {
		result.Modified = true
	}
	// The resolved disabled-image rule selects hosted/namespace cleanup
	// independently of literal model spelling; the inserted guidance text remains
	// the fixed Spark wording.
	if imageToolsDisabled && StripCodexSparkImageGenerationTools(reqBody) {
		result.Modified = true
	}

	// Normalize legacy tool-role and message-text shapes before reference
	// filtering, so pairing and item-ID rules see their final Responses input
	// representations.
	if input, ok := reqBody["input"].([]any); ok {
		if normalizedInput, modified := NormalizeCodexToolRoleMessages(input); modified {
			input = normalizedInput
			result.Modified = true
		}
		if normalizedInput, modified := NormalizeCodexMessageContentText(input); modified {
			input = normalizedInput
			result.Modified = true
		}
		input = FilterCodexInputWithOptions(input, CodexInputFilterOptions{
			PreserveReferences: needsToolContinuation,
			PreserveCallIDs:    opts.PreserveToolCallIDs,
		})
		reqBody["input"] = input
		result.Modified = true
	} else if inputStr, ok := reqBody["input"].(string); ok {
		// Convert nonblank string input to a user item retaining full text; blank
		// strings become the empty item list required by this endpoint.
		trimmed := strings.TrimSpace(inputStr)
		if trimmed != "" {
			reqBody["input"] = []any{
				map[string]any{
					"type":    "message",
					"role":    "user",
					"content": inputStr,
				},
			}
		} else {
			reqBody["input"] = []any{}
		}
		result.Modified = true
	}

	return result
}

// NormalizeCodexToolChoice mutates object choices to match available
// declarations, returning whether it changed the request. Function names can
// move from nested function data to the top-level choice; absent or undeclared
// names select auto.
//
// Other nonblank choice types remain when top-level tools or Lite
// additional_tools contain that type. Unknown choices select auto rather than a
// nonexistent hosted capability. Existing non-object or typeless choices remain
// for upstream validation. The operation can mutate the shared choice map
// before replacing the top-level value.
func NormalizeCodexToolChoice(reqBody map[string]any) bool {
	choice, ok := reqBody["tool_choice"]
	if !ok || choice == nil {
		return false
	}
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return false
	}
	choiceType := strings.TrimSpace(FirstNonEmptyString(choiceMap["type"]))
	if choiceType == "" {
		return false
	}
	modified := false
	// Resolve a function choice against declarations after flattening its name;
	// missing or unavailable selections become auto so no absent function is forced.
	if choiceType == "function" {
		name := strings.TrimSpace(FirstNonEmptyString(choiceMap["name"]))
		if name == "" {
			if function, ok := choiceMap["function"].(map[string]any); ok {
				name = strings.TrimSpace(FirstNonEmptyString(function["name"]))
			}
		}
		if name == "" {
			reqBody["tool_choice"] = "auto"
			return true
		}
		if strings.TrimSpace(FirstNonEmptyString(choiceMap["name"])) != name {
			choiceMap["name"] = name
			modified = true
		}
		if _, ok := choiceMap["function"]; ok {
			delete(choiceMap, "function")
			modified = true
		}
		if !CodexToolsContainFunctionName(reqBody["tools"], name) {
			reqBody["tool_choice"] = "auto"
			return true
		}
		return modified
	}
	if CodexToolsContainType(reqBody["tools"], choiceType) || CodexInputAdditionalToolsContainType(reqBody["input"], choiceType) {
		return modified
	}
	reqBody["tool_choice"] = "auto"
	return true
}

// CodexInputAdditionalToolsContainType finds a selected tool type in []any Lite
// additional_tools input items. Unsupported input shapes and blank requested
// types return false. Actual tool type values are trimmed before exact
// comparison.
func CodexInputAdditionalToolsContainType(rawInput any, toolType string) bool {
	input, ok := rawInput.([]any)
	if !ok || strings.TrimSpace(toolType) == "" {
		return false
	}
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(FirstNonEmptyString(item["type"])) != "additional_tools" {
			continue
		}
		if CodexToolsContainType(item["tools"], toolType) {
			return true
		}
	}
	return false
}

// CodexToolsContainType reports whether a []any tool list contains the supplied
// type. A blank requested type or other list shape fails. Tool type values are
// trimmed, while the nonblank requested value is compared as supplied.
func CodexToolsContainType(rawTools any, toolType string) bool {
	tools, ok := rawTools.([]any)
	if !ok || strings.TrimSpace(toolType) == "" {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(FirstNonEmptyString(tool["type"])) == toolType {
			return true
		}
	}
	return false
}

// CodexToolsContainFunctionName finds a function name in flat or nested []any
// declarations after trimming. Other tool types and unsupported list shapes are
// skipped. A nonblank flat name wins over a nested function name.
func CodexToolsContainFunctionName(rawTools any, name string) bool {
	tools, ok := rawTools.([]any)
	if !ok || strings.TrimSpace(name) == "" {
		return false
	}
	normalizedName := strings.TrimSpace(name)
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(FirstNonEmptyString(tool["type"])) != "function" {
			continue
		}
		toolName := strings.TrimSpace(FirstNonEmptyString(tool["name"]))
		if toolName == "" {
			if function, ok := tool["function"].(map[string]any); ok {
				toolName = strings.TrimSpace(FirstNonEmptyString(function["name"]))
			}
		}
		if toolName == normalizedName {
			return true
		}
	}
	return false
}

// NormalizeCodexToolRoleMessages converts tool-role messages into Responses
// function outputs and reports whether any message changed. Unmatched items
// remain shared; a wholly unchanged sequence returns its supplied slice.
// Callers keep nested values stable during conversion.
//
// A usable call_id, tool_call_id, or id anchors the new minimal output item.
// Text content is preferred, then a string output field, then best-effort JSON
// content serialization. Without an anchor, a shallow copy becomes a user
// message and loses tool_call_id, retaining the other fields so that unpaired
// content can still reach the model.
func NormalizeCodexToolRoleMessages(input []any) ([]any, bool) {
	if len(input) == 0 {
		return input, false
	}

	modified := false
	normalized := make([]any, 0, len(input))
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			normalized = append(normalized, item)
			continue
		}
		role, _ := m["role"].(string)
		if strings.TrimSpace(role) != "tool" {
			normalized = append(normalized, item)
			continue
		}

		callID := FirstNonEmptyString(m["call_id"], m["tool_call_id"], m["id"])
		callID = strings.TrimSpace(callID)
		if callID == "" {
			// A tool reply lacking call identity cannot form a Responses result; retain
			// its fields in a user-role copy and remove the legacy tool_call_id.
			fallback := make(map[string]any, len(m))
			maps.Copy(fallback, m)
			fallback["role"] = "user"
			delete(fallback, "tool_call_id")
			normalized = append(normalized, fallback)
			modified = true
			continue
		}

		output := ExtractTextFromContent(m["content"])
		if output == "" {
			if value, ok := m["output"].(string); ok {
				output = value
			}
		}
		// Content may contain structured values after text/output extraction fails;
		// JSON serialization supplies a compatibility string when that representation
		// is supported.
		if output == "" && m["content"] != nil {
			if b, err := json.Marshal(m["content"]); err == nil {
				output = string(b)
			}
		}

		normalized = append(normalized, map[string]any{
			"type":    "function_call_output",
			"call_id": callID,
			"output":  output,
		})
		modified = true
	}
	if !modified {
		return input, false
	}
	return normalized, true
}

// NormalizeCodexMessageContentText replaces present non-string text values in
// message content arrays with StringifyCodexContentText results. Unmatched
// items and strings remain unchanged; if nothing changes, the supplied slice is
// returned.
//
// The function copies a message map and content slice only after finding a
// non-string text field, then copies each changed part map. Other nested values
// and untouched parts remain shared. This keeps caller-owned text fields
// unchanged while adapting malformed message text, and callers still treat
// shared nested data as immutable during use.
func NormalizeCodexMessageContentText(input []any) ([]any, bool) {
	if len(input) == 0 {
		return input, false
	}

	modified := false
	normalized := make([]any, 0, len(input))
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok || strings.TrimSpace(FirstNonEmptyString(m["type"])) != "message" {
			normalized = append(normalized, item)
			continue
		}
		parts, ok := m["content"].([]any)
		if !ok {
			normalized = append(normalized, item)
			continue
		}

		var newItem map[string]any
		var newParts []any
		// Copy the item and content slice only for a changed text part, retaining
		// shared unrelated values while keeping the caller's text fields intact.
		ensureItemCopy := func() {
			if newItem != nil {
				return
			}
			newItem = make(map[string]any, len(m))
			maps.Copy(newItem, m)
			newParts = make([]any, len(parts))
			copy(newParts, parts)
		}

		for i, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			text, hasText := part["text"]
			if !hasText {
				continue
			}
			if _, ok := text.(string); ok {
				continue
			}

			ensureItemCopy()
			newPart := make(map[string]any, len(part))
			maps.Copy(newPart, part)
			newPart["text"] = StringifyCodexContentText(text)
			newParts[i] = newPart
			modified = true
		}

		if newItem != nil {
			newItem["content"] = newParts
			normalized = append(normalized, newItem)
			continue
		}
		normalized = append(normalized, item)
	}
	if !modified {
		return input, false
	}
	return normalized, true
}

// StringifyCodexContentText keeps strings, turns nil into empty text, and
// marshals other values to JSON when possible. Unsupported JSON values use
// fmt.Sprint. That alternative produces display text rather than a lossless
// JSON representation of the value.
func StringifyCodexContentText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		// Unsupported JSON values still need message text; fmt.Sprint supplies a
		// display representation after JSON encoding rejects the value.
		return fmt.Sprint(v)
	}
}

// IsCodexSparkModel recognizes the exact Spark model name after trimming and
// case folding.
func IsCodexSparkModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "gpt-5.3-codex-spark")
}

// HasOpenAIImageGenerationTool finds hosted or namespace image declarations in
// top-level tools or Lite input.
func HasOpenAIImageGenerationTool(reqBody map[string]any) bool {
	if ToolsContainImageGeneration(reqBody["tools"]) {
		return true
	}
	return InputContainsImageGenerationTool(reqBody["input"])
}

// HasCodexImageGenerationFunctionTool recognizes the configured client image
// function in top-level function declarations.
func HasCodexImageGenerationFunctionTool(reqBody map[string]any) bool {
	return len(reqBody) > 0 &&
		CodexToolsContainFunctionName(reqBody["tools"], CodexImageGenerationFunctionToolName)
}

// ToolsContainImageGeneration recognizes hosted image-generation or image_gen
// namespace declarations in a []any list. Other list shapes return false.
// Client function-name recognition uses HasCodexImageGenerationFunctionTool
// separately.
func ToolsContainImageGeneration(rawTools any) bool {
	if rawTools == nil {
		return false
	}
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		toolMap, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if IsOpenAIImageGenerationToolMap(toolMap) {
			return true
		}
	}
	return false
}

// IsOpenAIImageGenerationToolMap recognizes a hosted image-generation type or
// the configured image namespace declaration.
func IsOpenAIImageGenerationToolMap(tool map[string]any) bool {
	return IsOpenAIImageGenerationType(FirstNonEmptyString(tool["type"])) ||
		IsImageGenNamespaceToolMap(tool)
}

// IsImageGenNamespaceToolMap recognizes a namespace declaration whose trimmed
// name is image_gen.
func IsImageGenNamespaceToolMap(tool map[string]any) bool {
	return strings.TrimSpace(FirstNonEmptyString(tool["type"])) == "namespace" &&
		IsOpenAIImageGenNamespaceName(FirstNonEmptyString(tool["name"]))
}

// InputContainsImageGenerationTool finds hosted or namespace image declarations
// inside []any additional_tools items. Other input representations and item
// types are ignored. It does not infer capabilities from arbitrary text or
// function names.
func InputContainsImageGenerationTool(rawInput any) bool {
	input, ok := rawInput.([]any)
	if !ok {
		return false
	}
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(FirstNonEmptyString(item["type"])) != "additional_tools" {
			continue
		}
		if ToolsContainImageGeneration(item["tools"]) {
			return true
		}
	}
	return false
}

// StripOpenAIImageGenerationTools removes recognized hosted/namespace image
// declarations and image-selecting choices from a decoded request. Nil requests
// return false. Top-level lists and Lite input item maps can be mutated
// directly, so callers serialize access to shared request data.
func StripOpenAIImageGenerationTools(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	modified := StripOpenAIImageGenerationToolList(reqBody, "tools")
	if StripOpenAIImageGenerationToolsFromInput(reqBody) {
		modified = true
	}
	if OpenAIAnyToolChoiceSelectsImageGeneration(reqBody["tool_choice"]) {
		delete(reqBody, "tool_choice")
		modified = true
	}
	return modified
}

// StripOpenAIImageGenerationToolList replaces a named []any list after removing
// hosted/namespace image declarations. Removing its final entry deletes the
// field. Unsupported and unchanged lists remain supplied; surviving declaration
// objects stay shared.
func StripOpenAIImageGenerationToolList(container map[string]any, key string) bool {
	rawTools, ok := container[key]
	if !ok || rawTools == nil {
		return false
	}
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}
	filtered := make([]any, 0, len(tools))
	removed := false
	for _, rawTool := range tools {
		if toolMap, ok := rawTool.(map[string]any); ok && IsOpenAIImageGenerationToolMap(toolMap) {
			removed = true
			continue
		}
		filtered = append(filtered, rawTool)
	}
	if !removed {
		return false
	}
	if len(filtered) == 0 {
		delete(container, key)
	} else {
		container[key] = filtered
	}
	return true
}

// StripOpenAIImageGenerationToolsFromInput filters hosted/namespace image
// declarations from Lite additional_tools input items and removes items emptied
// by that cleanup. Other input entries remain shared. Mutated additional_tools
// maps are also visible to callers holding those map references.
func StripOpenAIImageGenerationToolsFromInput(reqBody map[string]any) bool {
	input, ok := reqBody["input"].([]any)
	if !ok {
		return false
	}

	filteredInput := make([]any, 0, len(input))
	modified := false
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(FirstNonEmptyString(item["type"])) != "additional_tools" {
			filteredInput = append(filteredInput, rawItem)
			continue
		}
		if !StripOpenAIImageGenerationToolList(item, "tools") {
			filteredInput = append(filteredInput, rawItem)
			continue
		}
		modified = true
		if _, hasTools := item["tools"]; hasTools {
			filteredInput = append(filteredInput, rawItem)
		}
		// An additional_tools item whose final recognized declaration was removed has
		// no remaining tools field, so omit the emptied declaration item.
	}
	if modified {
		reqBody["input"] = filteredInput
	}
	return modified
}

// StripOpenAIImageGenerationToolsFromRawPayload removes recognized image
// declarations from raw request JSON when hasDeclaration is true. A false hint
// still validates JSON and returns unchanged bytes. Decode or rebuild failures
// return the supplied payload.
//
// When cleanup changes the request, ordinary encoding/json decoding rebuilds
// the whole map and can round large numeric values through float64. The
// caller's declaration probe selects this path; a true hint with no supported
// removal returns the raw payload. This representation differs from the
// number-preserving raw compatibility transforms elsewhere in the package.
func StripOpenAIImageGenerationToolsFromRawPayload(payload []byte, hasDeclaration bool) ([]byte, bool, error) {
	if !hasDeclaration {
		if json.Valid(payload) {
			return payload, false, nil
		}
		var invalidPayload map[string]any
		return payload, false, json.Unmarshal(payload, &invalidPayload)
	}
	// Image declaration cleanup rebuilds through float64 decoding; changes can
	// round numeric fields unrelated to the removed tools in this whole payload.
	payloadMap := make(map[string]any)
	if err := json.Unmarshal(payload, &payloadMap); err != nil {
		return payload, false, err
	}
	if !StripOpenAIImageGenerationTools(payloadMap) {
		return payload, false, nil
	}
	rebuilt, err := json.Marshal(payloadMap)
	if err != nil {
		return payload, false, err
	}
	return rebuilt, true, nil
}

// StripCodexSparkImageGenerationTools delegates removal of hosted/namespace
// image declarations and selecting choices. The caller determines whether model
// policy needs that cleanup. Client image function-name detection is a separate
// rule and is not part of the delegated declaration predicate.
func StripCodexSparkImageGenerationTools(reqBody map[string]any) bool {
	return StripOpenAIImageGenerationTools(reqBody)
}

// HasOpenAIInputImage probes decoded Responses input and Chat messages for
// recognized image shapes.
func HasOpenAIInputImage(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	return HasOpenAIInputImageValue(reqBody["input"]) || HasOpenAIInputImageValue(reqBody["messages"])
}

// HasOpenAIInputImageValue searches []any sequences and map content for
// input_image type or an image_url key. Key presence qualifies regardless of
// its value. Other nested fields and unsupported Go container shapes are
// outside this probe.
func HasOpenAIInputImageValue(value any) bool {
	switch v := value.(type) {
	case []any:
		if slices.ContainsFunc(v, HasOpenAIInputImageValue) {
			return true
		}
	case map[string]any:
		if strings.TrimSpace(FirstNonEmptyString(v["type"])) == "input_image" {
			return true
		}
		if _, ok := v["image_url"]; ok {
			return true
		}
		return HasOpenAIInputImageValue(v["content"])
	}
	return false
}

// ValidateCodexSparkInput rejects recognized image input when the supplied
// spark decision is true. Otherwise it returns nil. The model name is trimmed
// for the error message, while the caller determines model capability.
func ValidateCodexSparkInput(reqBody map[string]any, model string, spark bool) error {
	if !spark || !HasOpenAIInputImage(reqBody) {
		return nil
	}
	return fmt.Errorf("model %q does not support image input", strings.TrimSpace(model))
}

// NormalizeOpenAIResponsesImageGenerationTools migrates format/compression
// aliases in hosted image tools to their canonical fields. Existing canonical
// key presence wins, including null or blank values; aliases are deleted. The
// supplied tool maps are mutated in place and unchanged or unsupported lists
// return false.
func NormalizeOpenAIResponsesImageGenerationTools(reqBody map[string]any) bool {
	rawTools, ok := reqBody["tools"]
	if !ok || rawTools == nil {
		return false
	}
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}

	modified := false
	for _, rawTool := range tools {
		toolMap, ok := rawTool.(map[string]any)
		if !ok || strings.TrimSpace(FirstNonEmptyString(toolMap["type"])) != "image_generation" {
			continue
		}
		// Canonical key presence wins over aliases, including null/blank values;
		// delete format/compression aliases after considering their migration as one
		// phase.
		if _, ok := toolMap["output_format"]; !ok {
			if value := strings.TrimSpace(FirstNonEmptyString(toolMap["format"])); value != "" {
				toolMap["output_format"] = value
				modified = true
			}
		}
		if _, ok := toolMap["output_compression"]; !ok {
			if value, exists := toolMap["compression"]; exists && value != nil {
				toolMap["output_compression"] = value
				modified = true
			}
		}
		if _, ok := toolMap["format"]; ok {
			delete(toolMap, "format")
			modified = true
		}
		if _, ok := toolMap["compression"]; ok {
			delete(toolMap, "compression")
			modified = true
		}
	}
	return modified
}

// NormalizeOpenAIResponseFormatSchemas applies schema cleanup to text.format
// and response_format objects selecting json_schema. It accepts direct schema
// and nested json_schema.schema layouts. The supplied schema maps are mutated
// recursively, and callers retain exclusive access during normalization.
func NormalizeOpenAIResponseFormatSchemas(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	modified := false
	normalizeFormat := func(format map[string]any) {
		if format == nil || strings.TrimSpace(FirstNonEmptyString(format["type"])) != "json_schema" {
			return
		}
		if schema, ok := format["schema"].(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(schema) {
			modified = true
		}
		if jsonSchema, ok := format["json_schema"].(map[string]any); ok {
			if schema, ok := jsonSchema["schema"].(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(schema) {
				modified = true
			}
		}
	}
	if text, ok := reqBody["text"].(map[string]any); ok {
		if format, ok := text["format"].(map[string]any); ok {
			normalizeFormat(format)
		}
	}
	if responseFormat, ok := reqBody["response_format"].(map[string]any); ok {
		normalizeFormat(responseFormat)
	}
	return modified
}

// NormalizeOpenAIResponseJSONSchema mutates an acyclic decoded schema by
// removing uniqueItems/minProperties, inferring missing or null type from
// properties/items, and visiting supported nested schema locations. Nil maps
// return false. Existing nonnull type values remain supplied.
//
// Recursion includes property maps, item schemas, combinators, definitions,
// conditional schemas, and schema-valued dependencies. Unknown values and
// non-map branches remain available for upstream validation. Changes occur
// directly in shared maps, so callers serialize access and do not pass cyclic
// Go structures produced outside normal JSON decoding.
func NormalizeOpenAIResponseJSONSchema(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	modified := false
	// Normalize the schema compatibility keywords before traversing child schemas,
	// retaining unknown constraints for upstream validation instead of inventing
	// their values.
	for _, key := range []string{"uniqueItems", "minProperties"} {
		if _, exists := schema[key]; exists {
			delete(schema, key)
			modified = true
		}
	}
	if rawType, exists := schema["type"]; !exists || rawType == nil {
		switch {
		case schema["properties"] != nil:
			schema["type"] = "object"
			modified = true
		case schema["items"] != nil:
			schema["type"] = "array"
			modified = true
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, raw := range properties {
			if child, ok := raw.(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(child) {
				modified = true
			}
		}
	}
	switch items := schema["items"].(type) {
	case map[string]any:
		if NormalizeOpenAIResponseJSONSchema(items) {
			modified = true
		}
	case []any:
		for _, raw := range items {
			if child, ok := raw.(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(child) {
				modified = true
			}
		}
	}
	for _, key := range []string{
		"additionalProperties",
		"additionalItems",
		"contains",
		"not",
		"if",
		"then",
		"else",
		"propertyNames",
		"unevaluatedProperties",
		"unevaluatedItems",
	} {
		if child, ok := schema[key].(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(child) {
			modified = true
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		children, _ := schema[key].([]any)
		for _, raw := range children {
			if child, ok := raw.(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(child) {
				modified = true
			}
		}
	}
	for _, key := range []string{"$defs", "definitions", "patternProperties", "dependentSchemas"} {
		children, _ := schema[key].(map[string]any)
		for _, raw := range children {
			if child, ok := raw.(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(child) {
				modified = true
			}
		}
	}
	if dependencies, ok := schema["dependencies"].(map[string]any); ok {
		for _, raw := range dependencies {
			if child, ok := raw.(map[string]any); ok && NormalizeOpenAIResponseJSONSchema(child) {
				modified = true
			}
		}
	}
	return modified
}

// EnsureOpenAIResponsesImageGenerationTool adds one PNG hosted image
// declaration to a nonempty request when the supplied spark decision is false.
// Existing client image functions and hosted/namespace capabilities, including
// Lite input declarations, prevent insertion.
//
// Checking those wire forms before replacement keeps one available image
// capability from receiving another declaration through a different
// representation. Missing or malformed top-level tools become a new list;
// supported lists receive an append and retain their objects. Callers classify
// model policy and serialize mutation of the decoded request.
func EnsureOpenAIResponsesImageGenerationTool(reqBody map[string]any, spark bool) bool {
	if len(reqBody) == 0 {
		return false
	}
	if spark {
		return false
	}
	// Recognize client function, namespace, and hosted declarations before
	// injection; each can already provide image capability despite a different
	// wire shape.
	if HasCodexImageGenerationFunctionTool(reqBody) {
		return false
	}
	if HasOpenAIImageGenerationTool(reqBody) {
		return false
	}

	tool := map[string]any{
		"type":          "image_generation",
		"output_format": "png",
	}

	rawTools, ok := reqBody["tools"]
	if !ok || rawTools == nil {
		reqBody["tools"] = []any{tool}
		return true
	}

	tools, ok := rawTools.([]any)
	if !ok {
		reqBody["tools"] = []any{tool}
		return true
	}
	reqBody["tools"] = append(tools, tool)
	return true
}

// EnsureOpenAIResponsesImageGenerationToolChoiceAuto adds auto selection for a
// declared hosted/namespace image capability when the supplied spark decision
// is false. A client image function or a present tool_choice, including null,
// prevents insertion.
//
// Hosted capability can come from top-level declarations or Lite input.
// Existing selection stays with the client even when it cannot be validated
// here; the separate choice normalizer handles declaration availability. The
// request map is mutated on success, and callers classify the model before
// invoking this selection rule.
func EnsureOpenAIResponsesImageGenerationToolChoiceAuto(reqBody map[string]any, spark bool) bool {
	if len(reqBody) == 0 || HasCodexImageGenerationFunctionTool(reqBody) || !HasOpenAIImageGenerationTool(reqBody) {
		return false
	}
	if spark {
		return false
	}
	if _, ok := reqBody["tool_choice"]; ok {
		return false
	}
	reqBody["tool_choice"] = "auto"
	return true
}

// ApplyCodexImageGenerationBridgeInstructions appends fixed guidance when
// hosted/namespace image capability exists, a client image function is absent,
// and the supplied spark decision is false. Current or legacy marker substrings
// anywhere in instructions suppress insertion.
//
// A nonblank string keeps its text after trailing whitespace is removed, then
// receives a separated guidance block. Blank or non-string instructions receive
// the fixed text directly. This combines caller guidance with endpoint image
// execution while recognizing both marker versions; it does not authenticate
// the marker or inspect the selected model itself.
func ApplyCodexImageGenerationBridgeInstructions(reqBody map[string]any, spark bool) bool {
	if len(reqBody) == 0 || HasCodexImageGenerationFunctionTool(reqBody) || !HasOpenAIImageGenerationTool(reqBody) {
		return false
	}
	if spark {
		return false
	}

	// Recognize either literal guidance marker before appending, so a request
	// carrying the legacy spelling does not acquire a second current guidance block.
	existing, _ := reqBody["instructions"].(string)
	if strings.Contains(existing, CodexImageGenerationBridgeMarker) || strings.Contains(existing, "<sub2api-codex-image-generation>") {
		return false
	}

	existing = strings.TrimRight(existing, " \t\r\n")
	if strings.TrimSpace(existing) == "" {
		reqBody["instructions"] = CodexImageGenerationBridgeText
		return true
	}

	reqBody["instructions"] = existing + "\n\n" + CodexImageGenerationBridgeText
	return true
}

// ApplyCodexSparkImageUnsupportedInstructions appends fixed Spark
// image-limitation guidance to a nonempty decoded request. Current or legacy
// marker substrings anywhere in instructions suppress duplicate insertion.
// Existing nonblank string text is retained after trailing whitespace cleanup.
//
// Blank or non-string instructions become the fixed guidance text. The helper
// does not inspect model identity, so a disabled-image rule override can invoke
// it for a different model while the text still names Spark. Callers decide
// eligibility and serialize mutation of the instruction field.
func ApplyCodexSparkImageUnsupportedInstructions(reqBody map[string]any) bool {
	if len(reqBody) == 0 {
		return false
	}
	// Recognize either literal guidance marker before appending, so a request
	// carrying the legacy spelling does not acquire a second current guidance block.
	existing, _ := reqBody["instructions"].(string)
	if strings.Contains(existing, CodexSparkImageUnsupportedMarker) || strings.Contains(existing, "<sub2api-codex-spark-image-unsupported>") {
		return false
	}
	existing = strings.TrimRight(existing, " \t\r\n")
	if strings.TrimSpace(existing) == "" {
		reqBody["instructions"] = CodexSparkImageUnsupportedText
		return true
	}
	reqBody["instructions"] = existing + "\n\n" + CodexSparkImageUnsupportedText
	return true
}

// ValidateOpenAIResponsesImageModel rejects an image-only top-level model when
// hosted/namespace image tools are declared. Other requests pass. The caller
// supplies imageOnly and the model label, so capability detection remains
// outside this validation helper.
func ValidateOpenAIResponsesImageModel(reqBody map[string]any, model string, imageOnly bool) error {
	if !HasOpenAIImageGenerationTool(reqBody) {
		return nil
	}
	model = strings.TrimSpace(model)
	if !imageOnly {
		return nil
	}
	return fmt.Errorf("/v1/responses image_generation requests require a Responses-capable text model; image-only model %q is not allowed", model)
}

// NormalizeOpenAIResponsesImageOnlyModel moves an image-only request to a
// Responses text driver with an image_generation tool. Nonempty requests change
// only when imageOnly is true; callers supply a resolved nonblank mainModel and
// own that model classification. The first hosted image tool is used, or a new
// one is appended.
//
// Existing nonblank tool model and existing option keys win over top-level
// image fields, which are removed after consideration. A prompt populates input
// only when input is absent; a choice is added only when absent. The top-level
// model becomes mainModel from provider policy while the selected image model
// stays on the tool, preserving their separate execution roles.
func NormalizeOpenAIResponsesImageOnlyModel(reqBody map[string]any, imageOnly bool, mainModel string) bool {
	if len(reqBody) == 0 {
		return false
	}
	imageModel := strings.TrimSpace(FirstNonEmptyString(reqBody["model"]))
	if !imageOnly {
		return false
	}

	modified := false
	tools, _ := reqBody["tools"].([]any)
	imageToolIndex := -1
	for i, rawTool := range tools {
		toolMap, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(FirstNonEmptyString(toolMap["type"])) == "image_generation" {
			imageToolIndex = i
			break
		}
	}
	// Keep image selection on one hosted tool while installing the text driver at
	// the top level; reuse the first declared image tool when present.
	if imageToolIndex < 0 {
		tools = append(tools, map[string]any{
			"type":  "image_generation",
			"model": imageModel,
		})
		imageToolIndex = len(tools) - 1
		reqBody["tools"] = tools
		modified = true
	}

	if toolMap, ok := tools[imageToolIndex].(map[string]any); ok {
		if strings.TrimSpace(FirstNonEmptyString(toolMap["model"])) == "" {
			toolMap["model"] = imageModel
			modified = true
		}
		for _, key := range []string{
			"size",
			"quality",
			"background",
			"output_format",
			"output_compression",
			"moderation",
			"style",
			"partial_images",
		} {
			if value, exists := reqBody[key]; exists && value != nil {
				if _, toolHas := toolMap[key]; !toolHas {
					toolMap[key] = value
				}
				delete(reqBody, key)
				modified = true
			}
		}
	}

	if prompt := strings.TrimSpace(FirstNonEmptyString(reqBody["prompt"])); prompt != "" {
		if _, hasInput := reqBody["input"]; !hasInput {
			reqBody["input"] = prompt
		}
		delete(reqBody, "prompt")
		modified = true
	}

	if _, ok := reqBody["tool_choice"]; !ok {
		reqBody["tool_choice"] = map[string]any{"type": "image_generation"}
		modified = true
	}
	if imageModel != mainModel {
		modified = true
	}
	reqBody["model"] = mainModel
	return modified
}

// ExtractTextFromContent returns a supplied string or concatenated string text
// from recognized text-part maps. Unsupported parts and other content shapes
// contribute empty text. Non-text content is omitted, so callers use the
// lossless helper when deciding whether an entire message can be removed.
func ExtractTextFromContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, part := range v {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			switch t, _ := m["type"].(string); t {
			case "text", "input_text", "output_text":
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

// ExtractSystemMessagesFromInput promotes exact system-role text to
// instructions and changes retained system messages to developer. Unsupported
// input layouts are unchanged. Existing nonblank instructions follow the
// extracted text with a separator.
//
// With omitPromoted, string or recognized all-text content can be removed after
// its text is promoted, including empty pure-text messages. Extra part metadata
// is outside the text-only eligibility check. Mixed or malformed content
// remains in input as developer so media and unsupported parts are retained,
// while any extractable text is also promoted. Retained message maps are
// mutated directly; callers serialize access to shared decoded input during
// this phase.
func ExtractSystemMessagesFromInput(reqBody map[string]any, omitPromoted bool) bool {
	input, ok := reqBody["input"].([]any)
	if !ok || len(input) == 0 {
		return false
	}

	var systemTexts []string
	filteredInput := make([]any, 0, len(input))
	modified := false
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok || m["role"] != "system" {
			filteredInput = append(filteredInput, item)
			continue
		}

		// Remove eligible text-only items after their text is promoted; part metadata
		// is not inspected, while mixed media or malformed parts stay in developer
		// input.
		if omitPromoted {
			if losslessText, lossless := ExtractLosslessTextFromContent(m["content"]); lossless {
				if losslessText != "" {
					systemTexts = append(systemTexts, losslessText)
				}
				modified = true
				continue
			}
		}

		if text := ExtractTextFromContent(m["content"]); text != "" {
			systemTexts = append(systemTexts, text)
		}
		// Retained system content becomes developer guidance in place, so request
		// aliases observe the role change alongside its promoted instruction text.
		m["role"] = "developer"
		filteredInput = append(filteredInput, item)
		modified = true
	}
	if omitPromoted && len(filteredInput) != len(input) {
		reqBody["input"] = filteredInput
	}

	if len(systemTexts) == 0 {
		return modified
	}

	extracted := strings.Join(systemTexts, "\n\n")
	if existing, ok := reqBody["instructions"].(string); ok && strings.TrimSpace(existing) != "" {
		reqBody["instructions"] = extracted + "\n\n" + existing
	} else {
		reqBody["instructions"] = extracted
	}
	return true
}

// ExtractLosslessTextFromContent accepts a string or []any parts having
// recognized text types and string text. Mixed or malformed parts return false;
// empty arrays are accepted as empty text. It joins text values, while extra
// part metadata is outside this promotion predicate.
func ExtractLosslessTextFromContent(content any) (string, bool) {
	switch v := content.(type) {
	case string:
		return v, true
	case []any:
		var b strings.Builder
		for _, part := range v {
			m, ok := part.(map[string]any)
			if !ok {
				return "", false
			}
			typeName, ok := m["type"].(string)
			if !ok || (typeName != "text" && typeName != "input_text" && typeName != "output_text") {
				return "", false
			}
			text, ok := m["text"].(string)
			if !ok {
				return "", false
			}
			_, _ = b.WriteString(text)
		}
		return b.String(), true
	default:
		return "", false
	}
}

// ExtractPromptLikeInstructionsFromInput joins trimmed text from exact
// developer/system roles in decoded []any input. Other roles, unsupported
// items, and blank extracted text are skipped. It reads the request without
// promoting or removing any input item.
func ExtractPromptLikeInstructionsFromInput(reqBody map[string]any) string {
	input, ok := reqBody["input"].([]any)
	if !ok || len(input) == 0 {
		return ""
	}
	var texts []string
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "developer", "system":
			if text := strings.TrimSpace(ExtractTextFromContent(m["content"])); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n\n")
}

// DefaultCodexSynthInstructions selects trimmed embedded guidance for a model
// and supplies a short coding prompt when that text is blank. Model dispatch
// belongs to CodexBaseInstructionsForModel, which also defines the embedded
// model-name compatibility rules.
func DefaultCodexSynthInstructions(model string) string {
	if instructions := strings.TrimSpace(CodexBaseInstructionsForModel(model)); instructions != "" {
		return instructions
	}
	return "You are a helpful coding assistant."
}

// EnsureCodexReasoningInclude requests encrypted reasoning when reasoning is a
// nonempty decoded map. Missing/null include becomes a list, and an existing
// []any list receives the entry only when absent. Other include shapes remain
// for upstream validation.
//
// The encrypted-content entry supplies inline reasoning for later store=false
// turns. Existing include values are retained in order; appending can share the
// supplied slice's backing storage. Callers serialize decoded request mutation
// and keep Compact requests under their separate schema policy instead of
// requiring this ordinary Responses include field.
func EnsureCodexReasoningInclude(reqBody map[string]any) bool {
	reasoning, ok := reqBody["reasoning"].(map[string]any)
	if !ok || len(reasoning) == 0 {
		return false
	}
	const encrypted = "reasoning.encrypted_content"
	switch existing := reqBody["include"].(type) {
	case nil:
		reqBody["include"] = []any{encrypted}
		return true
	case []any:
		for _, v := range existing {
			if s, ok := v.(string); ok && s == encrypted {
				return false
			}
		}
		reqBody["include"] = append(existing, encrypted)
		return true
	default:
		// Unsupported include representations remain for upstream validation,
		// avoiding a local replacement that would discard caller-supplied optional
		// field data.
		return false
	}
}

// ApplyCodexClientMetadata fills installation metadata from a trimmed nonblank
// device ID when no nonblank string ID exists. Supply a nonnil request map.
// Nil metadata becomes a map;
// unsupported metadata types remain unchanged. Existing generic maps are
// mutated, while string maps are copied to generic maps.
//
// The writer retains unrelated entries and gives an existing usable
// installation ID precedence over provider fallback identity. A present
// non-string or blank generic entry is replaceable. Callers retain exclusive
// access to shared generic metadata during mutation, while the copied
// string-map representation can diverge independently from the request's
// replacement metadata.
func ApplyCodexClientMetadata(reqBody map[string]any, deviceID string) bool {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	const key = "x-codex-installation-id"
	switch existing := reqBody["client_metadata"].(type) {
	case map[string]any:
		if v, ok := existing[key].(string); ok && strings.TrimSpace(v) != "" {
			return false
		}
		existing[key] = deviceID
		reqBody["client_metadata"] = existing
		return true
	case map[string]string:
		if strings.TrimSpace(existing[key]) != "" {
			return false
		}
		next := make(map[string]any, len(existing)+1)
		for k, v := range existing {
			next[k] = v
		}
		next[key] = deviceID
		reqBody["client_metadata"] = next
		return true
	case nil:
		reqBody["client_metadata"] = map[string]any{key: deviceID}
		return true
	default:
		return false
	}
}

// ApplyInstructions fills missing, null, non-string, or blank instructions with
// model-specific Codex guidance. A nonblank string remains supplied. The
// decoded request must be nonnil and is mutated; isCodexCLI is accepted by this
// interface but does
// not change the current instruction selection.
func ApplyInstructions(reqBody map[string]any, isCodexCLI bool) bool {
	if !IsInstructionsEmpty(reqBody) {
		return false
	}
	model, _ := reqBody["model"].(string)
	reqBody["instructions"] = DefaultCodexSynthInstructions(model)
	return true
}

// IsInstructionsEmpty treats missing, null, non-string, and whitespace-only
// instruction values as empty. The decoded request is read without mutation.
// ApplyInstructions uses this decision to replace unusable instruction values
// with model-specific guidance.
func IsInstructionsEmpty(reqBody map[string]any) bool {
	val, exists := reqBody["instructions"]
	if !exists {
		return true
	}
	if val == nil {
		return true
	}
	str, ok := val.(string)
	if !ok {
		return true
	}
	return strings.TrimSpace(str) == ""
}

// NormalizeCodexFilterCallID retains an ID verbatim when preserve is true and
// its byte length fits the limit. Otherwise it applies item-type normalization
// and compaction. Callers decide whether supplied short call IDs belong to the
// upstream's accepted identity representation.
func NormalizeCodexFilterCallID(itemType, id string, preserve bool) string {
	if preserve && len(id) <= CodexCallIDMaxLength {
		return id
	}
	return NormalizeCodexCallIDForItemType(itemType, id)
}

// CodexItemReferenceIDMappings maps trimmed original call IDs to their filtered
// type-specific IDs for recognized calls/results. PreserveCallIDs selects
// short-ID retention. The input maps remain unchanged.
//
// If the same original ID maps to different normalized identities, that key is
// removed and remembered as ambiguous so later entries cannot restore a
// misleading mapping. Filtering uses these mappings only for legacy call_
// references lacking an existing item identity. This keeps reference rewriting
// from associating one ambiguous call spelling with an arbitrary tool type.
func CodexItemReferenceIDMappings(input []any, preserveCallIDs bool) map[string]string {
	mappings := make(map[string]string)
	ambiguous := make(map[string]struct{})
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := strings.TrimSpace(FirstNonEmptyString(item["type"]))
		if !IsCodexToolCallItemType(itemType) {
			continue
		}
		rawCallID := strings.TrimSpace(FirstNonEmptyString(item["call_id"]))
		if rawCallID == "" {
			continue
		}
		normalized := NormalizeCodexFilterCallID(itemType, rawCallID, preserveCallIDs)
		// A shared raw ID mapping to incompatible tool prefixes is ambiguous;
		// remember the conflict so a later entry cannot restore an arbitrary target.
		if existing, exists := mappings[rawCallID]; exists && existing != normalized {
			delete(mappings, rawCallID)
			ambiguous[rawCallID] = struct{}{}
			continue
		}
		if _, conflict := ambiguous[rawCallID]; !conflict {
			mappings[rawCallID] = normalized
		}
	}
	return mappings
}

// CodexInputItemIDs gathers trimmed nonblank IDs from non-reference items whose
// type-specific prefix permits retention. Unsupported item shapes are skipped.
// Filtering uses these original item identities before legacy call-reference
// rewriting, giving actual item references precedence.
func CodexInputItemIDs(input []any) map[string]struct{} {
	itemIDs := make(map[string]struct{})
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(FirstNonEmptyString(item["type"])) == "item_reference" {
			continue
		}
		itemType := strings.TrimSpace(FirstNonEmptyString(item["type"]))
		id := strings.TrimSpace(FirstNonEmptyString(item["id"]))
		if id != "" && !ShouldStripOpenAIResponsesInputItemID(itemType, id) {
			itemIDs[id] = struct{}{}
		}
	}
	return itemIDs
}

// CodexInputCallIDs gathers trimmed nonblank call IDs from protocol-recognized
// call and output items. Unsupported items and blank IDs are skipped. Filtering
// uses the set to distinguish same-turn ambiguous legacy references from
// unresolved earlier-turn references.
func CodexInputCallIDs(input []any) map[string]struct{} {
	callIDs := make(map[string]struct{})
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || !IsCodexToolCallItemType(strings.TrimSpace(FirstNonEmptyString(item["type"]))) {
			continue
		}
		if callID := strings.TrimSpace(FirstNonEmptyString(item["call_id"])); callID != "" {
			callIDs[callID] = struct{}{}
		}
	}
	return callIDs
}

// FilterCodexInputWithOptions rebuilds input while applying reasoning replay,
// tool pairing, and item-reference rules. Callers keep nested values stable:
// changed maps are shallow copies and untouched objects can remain shared.
// Non-map items pass through.
//
// Reasoning loses remote id/call_id and receives summary=[] when absent or
// null, retaining its ciphertext and other fields. Preserved legacy call_
// references prefer existing item identities, then unambiguous call mappings;
// an unresolved cross-turn call reference receives function normalization.
// Calls/results use type-specific call IDs, and incompatible item IDs are
// removed rather than invented. Disabling references drops reference items and
// ordinary item IDs, while missing required tool names receive a compatibility
// name.
func FilterCodexInputWithOptions(input []any, opts CodexInputFilterOptions) []any {
	filtered := make([]any, 0, len(input))
	referenceIDMappings := CodexItemReferenceIDMappings(input, opts.PreserveCallIDs)
	inputItemIDs := CodexInputItemIDs(input)
	inputCallIDs := CodexInputCallIDs(input)
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		typ, _ := m["type"].(string)

		// Reasoning is replayed inline under store=false: remove remote id/call_id
		// and repair absent/null summary while retaining its content and ciphertext.
		// Compaction uses generic reference policy below, keeping its encrypted
		// content alongside any permitted item identity.
		if typ == "reasoning" {
			newItem := make(map[string]any, len(m))
			for key, value := range m {
				if key == "id" || key == "call_id" {
					// Remote reasoning identity cannot supply inline replay content; its
					// ciphertext, summary, and other fields remain in this newly copied item.
					continue
				}
				newItem[key] = value
			}
			if summary, ok := newItem["summary"]; !ok || summary == nil {
				// Supply the inline reasoning summary shape when missing or null, retaining
				// supplied ciphertext and any other replay fields in the copied item.
				newItem["summary"] = []any{}
			}
			filtered = append(filtered, newItem)
			continue
		}

		// Call identities and remote item identities have separate rules; normalize
		// known pairs while retaining valid item objects needed by continuation
		// references.
		fixCallIDPrefix := func(id string) string {
			return NormalizeCodexFilterCallID(typ, id, opts.PreserveCallIDs)
		}

		if typ == "item_reference" {
			if !opts.PreserveReferences {
				continue
			}
			newItem := make(map[string]any, len(m))
			maps.Copy(newItem, m)
			if id, ok := newItem["id"].(string); ok && strings.HasPrefix(strings.TrimSpace(id), "call_") {
				trimmedID := strings.TrimSpace(id)
				_, referencesExistingItem := inputItemIDs[trimmedID]
				if !referencesExistingItem {
					if normalizedID, mapped := referenceIDMappings[trimmedID]; mapped {
						newItem["id"] = normalizedID
					} else if _, hasSameTurnCall := inputCallIDs[trimmedID]; !hasSameTurnCall {
						// A legacy reference with no same-turn call or item identity uses
						// function normalization, allowing a call from earlier retained history
						// to remain addressable.
						newItem["id"] = NormalizeCodexCallID(trimmedID)
					}
				}
			}
			filtered = append(filtered, newItem)
			continue
		}

		newItem := m
		copied := false
		// A shallow copy isolates changed top-level fields during filtering;
		// untouched nested values remain shared with the caller's decoded history.
		ensureCopy := func() {
			if copied {
				return
			}
			newItem = make(map[string]any, len(m))
			maps.Copy(newItem, m)
			copied = true
		}

		if IsCodexToolCallItemType(typ) {
			callID, ok := m["call_id"].(string)
			if !ok || strings.TrimSpace(callID) == "" {
				if id, ok := m["id"].(string); ok && strings.TrimSpace(id) != "" {
					callID = id
					ensureCopy()
					newItem["call_id"] = callID
				}
			}

			if callID != "" {
				fixedCallID := fixCallIDPrefix(callID)
				if fixedCallID != callID {
					ensureCopy()
					newItem["call_id"] = fixedCallID
				}
			}
		}

		if !IsCodexToolCallItemType(typ) {
			ensureCopy()
			delete(newItem, "call_id")
		}

		if CodexInputItemRequiresName(typ) {
			if strings.TrimSpace(FirstNonEmptyString(m["name"])) == "" {
				name := FirstNonEmptyString(m["tool_name"])
				if name == "" {
					if function, ok := m["function"].(map[string]any); ok {
						name = FirstNonEmptyString(function["name"])
					}
				}
				if name == "" {
					name = "tool"
				}
				ensureCopy()
				newItem["name"] = name
			}
		}

		if typ == "reasoning" {
			// Exact reasoning items already exit above; this defensive branch mirrors
			// inline ID/summary cleanup if control flow later reaches it.
			ensureCopy()
			delete(newItem, "id")
			if summary, ok := newItem["summary"]; !ok || summary == nil {
				// Missing or null inline reasoning summary receives an empty array here,
				// matching the earlier copied-reasoning path's required replay shape.
				newItem["summary"] = []any{}
			}
		}

		if !opts.PreserveReferences {
			ensureCopy()
			delete(newItem, "id")
		} else if id, ok := m["id"].(string); ok && ShouldStripOpenAIResponsesInputItemID(typ, id) {
			ensureCopy()
			delete(newItem, "id")
		}

		filtered = append(filtered, newItem)
	}
	return filtered
}

// IsCodexToolCallItemType delegates recognition of call and output items to the
// shared protocol rules.
func IsCodexToolCallItemType(typ string) bool { return protocolopenai.IsCodexToolCallItemType(typ) }

// IsCodexToolCallInputType recognizes exact tool call-input types with
// constrained item identity prefixes. Output types are handled separately by
// OpenAIResponsesInputItemIDPrefix. Callers normalize surrounding type
// whitespace before passing it when their input policy requires that spelling.
func IsCodexToolCallInputType(typ string) bool {
	switch typ {
	case "function_call",
		"tool_call",
		"local_shell_call",
		"tool_search_call",
		"custom_tool_call",
		"mcp_tool_call":
		return true
	default:
		return false
	}
}

// CodexInputItemRequiresName identifies function, custom, and MCP call inputs
// needing a compatibility name.
func CodexInputItemRequiresName(typ string) bool {
	switch strings.TrimSpace(typ) {
	case "function_call", "custom_tool_call", "mcp_tool_call":
		return true
	default:
		return false
	}
}

// NormalizeCodexTools adapts nested Chat function declarations to flat
// Responses fields and drops function declarations without usable nested
// structure when no usable flat name exists. Non-function and unknown objects
// remain for upstream validation.
//
// Existing top-level keys win, including blank or null values. Nested
// name/description are copied only when nonblank strings and their destination
// keys are absent; parameters/strict retain their supplied values. The tool
// maps are mutated directly and nested values remain shared. The list is
// replaced only after a supported migration or removal, so callers serialize
// access to decoded declarations.
func NormalizeCodexTools(reqBody map[string]any) bool {
	rawTools, ok := reqBody["tools"]
	if !ok || rawTools == nil {
		return false
	}
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}

	modified := false
	validTools := make([]any, 0, len(tools))

	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			// Unknown declaration shapes remain for upstream validation, since dropping
			// them locally would remove caller-supplied capabilities outside recognized
			// function migration.
			validTools = append(validTools, tool)
			continue
		}

		toolType, _ := toolMap["type"].(string)
		toolType = strings.TrimSpace(toolType)
		if toolType != "function" {
			validTools = append(validTools, toolMap)
			continue
		}

		// A usable flat name already selects the Responses form; retain its remaining
		// keys and skip nested Chat-style field migration.
		if name, ok := toolMap["name"].(string); ok && strings.TrimSpace(name) != "" {
			validTools = append(validTools, toolMap)
			continue
		}

		// A function without a usable flat name needs its nested declaration for
		// migration; present top-level fields remain authoritative during copying.
		functionValue, hasFunction := toolMap["function"]
		function, ok := functionValue.(map[string]any)
		if !hasFunction || functionValue == nil || !ok || function == nil {
			// With no usable flat name or nested function map, this declaration cannot
			// participate in supported migration and is omitted from the replacement
			// list.
			modified = true
			continue
		}

		if _, ok := toolMap["name"]; !ok {
			if name, ok := function["name"].(string); ok && strings.TrimSpace(name) != "" {
				toolMap["name"] = name
				modified = true
			}
		}
		if _, ok := toolMap["description"]; !ok {
			if desc, ok := function["description"].(string); ok && strings.TrimSpace(desc) != "" {
				toolMap["description"] = desc
				modified = true
			}
		}
		if _, ok := toolMap["parameters"]; !ok {
			if params, ok := function["parameters"]; ok {
				toolMap["parameters"] = params
				modified = true
			}
		}
		if _, ok := toolMap["strict"]; !ok {
			if strict, ok := function["strict"]; ok {
				toolMap["strict"] = strict
				modified = true
			}
		}

		validTools = append(validTools, toolMap)
	}

	if modified {
		reqBody["tools"] = validTools
	}

	return modified
}

// IsOpenAIImageGenerationType recognizes the trimmed exact hosted
// image-generation tool type string.
func IsOpenAIImageGenerationType(value string) bool {
	return strings.TrimSpace(value) == "image_generation"
}

// OpenAIResponsesInputItemIDPrefix selects the expected bare item-ID prefix for
// constrained message, reasoning, search, and call types. It returns
// constrained=false for other types. This is item identity policy, separate
// from the call_id used to pair outputs.
//
// Custom-call output IDs use the generic fc namespace even though their call
// inputs use ctc. Call-input types use their protocol-selected prefixes;
// tool-search output item IDs remain unconstrained here. Callers remove
// incompatible item IDs so a synthesized ID cannot accidentally name another
// upstream object.
func OpenAIResponsesInputItemIDPrefix(itemType string) (string, bool) {
	switch strings.TrimSpace(itemType) {
	case "message":
		return "msg", true
	case "reasoning":
		return "rs", true
	case "web_search_call":
		return "ws", true
	case "custom_tool_call":
		return protocolopenai.OpenAIResponsesToolCallIDPrefix(itemType), true
	case "tool_search_call":
		return protocolopenai.OpenAIResponsesToolCallIDPrefix(itemType), true
	case "custom_tool_call_output":
		// Custom output item identity follows the generic fc namespace even when its
		// paired call_id uses ctc, so validate those two fields independently.
		return "fc", true
	default:
		if IsCodexToolCallInputType(itemType) {
			return protocolopenai.OpenAIResponsesToolCallIDPrefix(itemType), true
		}
		return "", false
	}
}

// ShouldStripOpenAIResponsesInputItemID reports whether a constrained item's ID
// is empty or lacks its bare type prefix. Unconstrained types return false. It
// does not trim the supplied ID or require an underscore after the prefix;
// filtering removes rejected IDs instead of synthesizing remote identity.
func ShouldStripOpenAIResponsesInputItemID(itemType, id string) bool {
	prefix, constrained := OpenAIResponsesInputItemIDPrefix(itemType)
	if !constrained {
		return false
	}
	return id == "" || !strings.HasPrefix(id, prefix)
}

// IsOpenAIImageGenNamespaceName recognizes the trimmed exact Codex image_gen
// namespace name.
func IsOpenAIImageGenNamespaceName(value string) bool {
	return strings.TrimSpace(value) == "image_gen"
}

// OpenAIAnyToolChoiceSelectsImageGeneration recognizes hosted image choice
// strings, namespace image choices, nested tool choices, or nested function
// names selecting image_generation. Unsupported values return false. Decoded
// map recursion expects acyclic request structures and leaves actual
// declaration availability to separate validation.
func OpenAIAnyToolChoiceSelectsImageGeneration(choice any) bool {
	switch v := choice.(type) {
	case string:
		return IsOpenAIImageGenerationType(v)
	case map[string]any:
		choiceType := strings.TrimSpace(FirstNonEmptyString(v["type"]))
		if IsOpenAIImageGenerationType(choiceType) {
			return true
		}
		if choiceType == "namespace" &&
			(IsOpenAIImageGenNamespaceName(FirstNonEmptyString(v["name"])) ||
				IsOpenAIImageGenNamespaceName(FirstNonEmptyString(v["namespace"]))) {
			return true
		}
		if tool, ok := v["tool"].(map[string]any); ok && OpenAIAnyToolChoiceSelectsImageGeneration(tool) {
			return true
		}
		if fn, ok := v["function"].(map[string]any); ok && IsOpenAIImageGenerationType(FirstNonEmptyString(fn["name"])) {
			return true
		}
	}
	return false
}
