package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_responses_ingress_compat.go normalizes legacy Chat messages and
// prompt aliases in Responses requests.
// Legacy Responses ingress can contain Chat messages or a string prompt
// alongside native Responses input. Nonnull input remains authoritative, so
// conversion cannot silently replace its history or previous-response
// relationship; message-derived history removes previous_response_id when it
// is adopted as the complete input.
//
// Chat conversion supplies input plus scalar and tool fields, while raw
// legacy-field inspection determines which converted tool, choice, and
// reasoning values can replace existing data. String prompts are input
// aliases, whereas object/array prompts stay available for upstream
// validation. The decoder retains json.Number values in the surrounding map
// and the body is published by final serialization; conversion of typed input
// follows the bridge encoding rules.

// convertedLegacyResponsesMessages records converted Chat input and selected
// legacy configuration fields.
type convertedLegacyResponsesMessages struct {
	input           any      // converted Responses input items
	instructions    string   // converted system instruction text
	maxOutputTokens *int     // optional converted output token cap
	temperature     *float64 // optional converted sampling temperature
	topP            *float64 // optional converted nucleus sampling
	tools           any      // converted Responses tool declarations
	reasoning       any      // converted Responses reasoning configuration
	toolChoice      any      // converted Responses tool selection
	serviceTier     string   // converted service tier text
	// hasChatTools, hasChatToolChoice, and hasReasoningEffort track explicit legacy
	// inputs independently of converter output. Adoption uses those flags to replace
	// corresponding fields only when the Chat-shaped request actually supplied them.
	hasChatTools       bool // explicit legacy tool declarations
	hasChatToolChoice  bool // explicit legacy tool selection
	hasReasoningEffort bool // explicit legacy reasoning effort
}

// NormalizeOpenAIResponsesLegacyIngress converts legacy Messages or string
// prompt aliases to Responses input. Empty bodies return unchanged; invalid
// decoding or conversion returns the supplied body with false and an error.
//
// A present nonnull native input wins over converted messages, including an
// empty array. Converted message input receives compatible top-level fields
// and loses previous_response_id because that history has been supplied
// directly. The messages field is then removed; string prompt aliases are
// removed after filling only absent/null input, while object or array prompts
// remain. Serialization publishes the rebuilt body after all selected
// mutations succeed.
func NormalizeOpenAIResponsesLegacyIngress(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	// Screen valid root objects with a zero-copy view so native Responses bodies
	// lacking messages, prompt, and commands skip full-body UseNumber map decoding.
	if gjson.ValidBytes(body) && !hasLegacyResponsesIngressFields(body) {
		return body, false, nil
	}

	var request map[string]any
	if err := wirejson.DecodeUseNumber(body, &request); err != nil {
		return body, false, fmt.Errorf("normalize legacy Responses ingress: %w", err)
	}

	changed := false
	messagesValue, hasMessages := request["messages"]
	if hasMessages {
		messages, messagesAreArray := messagesValue.([]any)
		if messagesAreArray && len(messages) > 0 {
			legacy, err := convertLegacyResponsesMessages(body)
			if err != nil {
				return body, false, err
			}

			input, hasInput := request["input"]
			hasNativeInput := hasInput && input != nil
			// Message-derived history becomes complete input only when native input is
			// absent or null. Remove its continuation ID so the same history is not resolved twice.
			if !hasNativeInput {
				request["input"] = legacy.input
				applyLegacyResponsesTopLevelFields(request, legacy)
				delete(request, "previous_response_id")
			}
		}
		// Responses forwarding consumes input, so remove the legacy messages alias
		// after its adoption decision while retaining the selected authoritative history.
		delete(request, "messages")
		changed = true
	}

	if prompt, hasPrompt := request["prompt"]; hasPrompt {
		// String prompts supply legacy text input. Object prompt templates and other
		// shapes retain their own meaning, so leave them available for upstream validation.
		if promptText, isLegacyString := prompt.(string); isLegacyString {
			if input, hasInput := request["input"]; !hasInput || input == nil {
				request["input"] = promptText
			}
			delete(request, "prompt")
			changed = true
		}
	}
	if _, hasCommands := request["commands"]; hasCommands {
		delete(request, "commands")
		changed = true
	}

	if !changed {
		return body, false, nil
	}
	normalized, err := wirejson.Marshal(request)
	if err != nil {
		return body, false, fmt.Errorf("serialize legacy Responses ingress: %w", err)
	}
	return normalized, true, nil
}

// convertLegacyResponsesMessages decodes Chat input and uses the shared bridge
// to build Responses fields. It rejects invalid conversion or empty encoded
// input, and records raw legacy tool/choice presence separately so top-level
// adoption only replaces the corresponding Chat-shaped fields.
func convertLegacyResponsesMessages(body []byte) (convertedLegacyResponsesMessages, error) {
	var converted convertedLegacyResponsesMessages
	var chatRequest protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatRequest); err != nil {
		return converted, fmt.Errorf("normalize legacy Responses messages: %w", err)
	}
	responsesRequest, err := protocolbridge.ChatCompletionsToResponses(&chatRequest, protocolforward.ConversionOptionsForModel(chatRequest.Model))
	if err != nil {
		return converted, fmt.Errorf("normalize legacy Responses messages: %w", err)
	}
	if len(responsesRequest.Input) == 0 {
		return converted, fmt.Errorf("normalize legacy Responses messages: empty input")
	}
	if err := json.Unmarshal(responsesRequest.Input, &converted.input); err != nil {
		return converted, fmt.Errorf("decode normalized legacy Responses input: %w", err)
	}

	converted.instructions = strings.TrimSpace(responsesRequest.Instructions)
	converted.maxOutputTokens = responsesRequest.MaxOutputTokens
	converted.temperature = responsesRequest.Temperature
	converted.topP = responsesRequest.TopP
	converted.serviceTier = strings.TrimSpace(responsesRequest.ServiceTier)
	converted.hasReasoningEffort = strings.TrimSpace(chatRequest.ReasoningEffort) != ""
	if responsesRequest.Reasoning != nil {
		converted.reasoning = responsesRequest.Reasoning
	}

	var rawRequest map[string]any
	if err := wirejson.DecodeUseNumber(body, &rawRequest); err != nil {
		return converted, fmt.Errorf("inspect legacy Responses fields: %w", err)
	}
	converted.hasChatTools = hasChatStyleTools(rawRequest) || rawRequest["functions"] != nil
	if converted.hasChatTools && len(responsesRequest.Tools) > 0 {
		converted.tools = responsesRequest.Tools
	}
	converted.hasChatToolChoice = hasChatStyleToolChoice(rawRequest) || rawRequest["function_call"] != nil
	if converted.hasChatToolChoice && len(responsesRequest.ToolChoice) > 0 {
		choice, err := normalizeLegacyResponsesToolChoice(responsesRequest.ToolChoice)
		if err != nil {
			return converted, err
		}
		converted.toolChoice = choice
	}
	return converted, nil
}

// hasChatStyleTools recognizes nested function objects inside a decoded tools
// array. Other tool representations or non-array values return false, letting
// legacy ingress preserve flat Responses declarations when no Chat encoding is
// present.
func hasChatStyleTools(request map[string]any) bool {
	tools, ok := request["tools"].([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		if _, ok := tool["function"].(map[string]any); ok {
			return true
		}
	}
	return false
}

// hasChatStyleToolChoice recognizes a nested function object in decoded
// tool_choice. Other choice forms return false, leaving their adoption
// decision to the legacy-field presence tracked during conversion.
func hasChatStyleToolChoice(request map[string]any) bool {
	choice, _ := request["tool_choice"].(map[string]any)
	_, ok := choice["function"].(map[string]any)
	return ok
}

// normalizeLegacyResponsesToolChoice decodes converted choice and flattens a
// named nested function selection. Non-function choices, missing nested
// objects, and blank names return their decoded value; invalid JSON returns a
// contextual error.
func normalizeLegacyResponsesToolChoice(raw json.RawMessage) (any, error) {
	var choice any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil, fmt.Errorf("normalize legacy Responses tool_choice: %w", err)
	}
	object, ok := choice.(map[string]any)
	if !ok || strings.TrimSpace(openai.FirstNonEmptyString(object["type"])) != "function" {
		return choice, nil
	}
	function, ok := object["function"].(map[string]any)
	if !ok {
		return choice, nil
	}
	name := strings.TrimSpace(openai.FirstNonEmptyString(function["name"]))
	if name == "" {
		return choice, nil
	}
	// Responses function choice uses a flat name beside type; retaining the
	// nested Chat function object would give the selected endpoint another request shape.
	return map[string]any{"type": "function", "name": name}, nil
}

// applyLegacyResponsesTopLevelFields installs selected converted fields after
// legacy messages become authoritative input. The writable request retains
// nonblank instructions, receives available scalar conversions, and removes
// deprecated Chat top-level spellings.
//
// Tool and choice replacement depends on their recorded Chat-shaped presence,
// while reasoning replacement requires explicit Chat reasoning effort. This
// keeps preexisting Responses forms from being overwritten by converter
// defaults when no matching legacy input was supplied. The conversion result
// is temporary data; NormalizeOpenAIResponsesLegacyIngress serializes the
// request only after adoption.
func applyLegacyResponsesTopLevelFields(request map[string]any, legacy convertedLegacyResponsesMessages) {
	if legacy.instructions != "" && strings.TrimSpace(openai.FirstNonEmptyString(request["instructions"])) == "" {
		request["instructions"] = legacy.instructions
	}
	if legacy.maxOutputTokens != nil {
		request["max_output_tokens"] = *legacy.maxOutputTokens
	}
	if legacy.temperature != nil {
		request["temperature"] = *legacy.temperature
	}
	if legacy.topP != nil {
		request["top_p"] = *legacy.topP
	}
	if legacy.hasChatTools && legacy.tools != nil {
		request["tools"] = legacy.tools
	}
	if legacy.hasReasoningEffort && legacy.reasoning != nil {
		request["reasoning"] = legacy.reasoning
	}
	if legacy.hasChatToolChoice && legacy.toolChoice != nil {
		request["tool_choice"] = legacy.toolChoice
	}
	if legacy.serviceTier != "" {
		request["service_tier"] = legacy.serviceTier
	}
	for _, key := range []string{"max_tokens", "max_completion_tokens", "reasoning_effort", "functions", "function_call"} {
		delete(request, key)
	}
}

// hasLegacyResponsesIngressFields reports whether a validated JSON document is a
// non-object (which DecodeUseNumber rejects) or carries top-level messages,
// prompt, or commands fields requiring legacy ingress normalization.
func hasLegacyResponsesIngressFields(body []byte) bool {
	root := wirejson.ParseView(body)
	if !root.IsObject() {
		return true
	}
	found := false
	root.ForEach(func(key, _ gjson.Result) bool {
		switch key.String() {
		case "messages", "prompt", "commands":
			found = true
			return false
		default:
			return true
		}
	})
	return found
}
