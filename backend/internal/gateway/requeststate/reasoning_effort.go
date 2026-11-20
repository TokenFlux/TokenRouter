package requeststate

import (
	"errors"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// reasoning_effort.go extracts requested and recorded reasoning effort from
// request bytes and decoded objects. Capturing the client choice before policy
// preserves a recognized none value, while effective reporting keeps the
// supported nonempty effort labels after transformations.
//
// Byte readers and decoded-object readers select their own field evidence. The
// byte reporting path falls through a blank nested value to the flat spelling;
// the decoded reporting dependency treats any nested string as supplied, even
// when its reporting label becomes empty. Canonical readers additionally
// consider Anthropic output_config.effort. These helpers do not infer effort
// from model names, and the ultra validator checks selected request fields
// separately from general JSON or provider-capability validation.

// ExtractOpenAIReasoningEffortFromBody returns the canonical reporting effort
// from nested reasoning.effort or the flat compatibility spelling. A blank
// nested value permits the flat field; missing, none, minimal or unrecognized
// reporting values return nil after normalization.
func ExtractOpenAIReasoningEffortFromBody(body []byte) *string {
	reasoningEffort := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	// A blank nested choice leaves the Chat-compatible flat spelling available.
	// Reporting still normalizes that supplied spelling before deciding whether to retain it.
	if reasoningEffort == "" {
		reasoningEffort = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if reasoningEffort != "" {
		normalized := openai.NormalizeRecordedReasoningEffort(reasoningEffort)
		if normalized == "" {
			return nil
		}
		return &normalized
	}

	return nil
}

// CanonicalRequestedReasoningEffort captures a recognized pre-policy client
// choice, including none, from request bytes. It prefers the first nonblank
// nested, flat, or output_config effort; missing or unrecognized values return
// nil, and model suffixes supply no value.
func CanonicalRequestedReasoningEffort(body []byte) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	// The flat Chat spelling can provide a pre-policy choice when nested
	// Responses evidence is blank, retaining one ordered client value for capture.
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if raw == "" {
		// Anthropic clients express the same pre-policy choice in output_config.
		// Read it after both OpenAI spellings so existing precedence stays visible.
		raw = strings.TrimSpace(gjson.GetBytes(body, "output_config.effort").String())
	}
	if raw != "" {
		canonical := routing.NormalizeRequestedOpenAIReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}

	return nil
}

// ExtractOpenAIServiceTier normalizes a string service_tier from a decoded
// request, mapping fast to priority. Nil maps, nonstring values, blanks and
// unknown labels return nil; recognized tier evidence does not establish
// provider capability.
func ExtractOpenAIServiceTier(reqBody map[string]any) *string {
	if reqBody == nil {
		return nil
	}
	raw, ok := reqBody["service_tier"].(string)
	if !ok {
		return nil
	}
	return openai.NormalizeServiceTier(raw)
}

// ExtractOpenAIServiceTierFromBody normalizes service_tier read from request
// bytes, mapping fast to priority. Empty bodies and unrecognized or blank
// spellings return nil; full request validation remains with the owning parser.
func ExtractOpenAIServiceTierFromBody(body []byte) *string {
	if len(body) == 0 {
		return nil
	}
	return openai.NormalizeServiceTier(gjson.GetBytes(body, "service_tier").String())
}

// ExtractOpenAIReasoningEffort returns the normalized reporting value selected
// by the decoded-object dependency. A supplied nested string takes precedence
// even when blank, none, minimal or unknown normalization yields nil; only
// missing or nonstring nested evidence permits the flat field.
func ExtractOpenAIReasoningEffort(reqBody map[string]any) *string {
	// The decoded dependency retains nested string presence even when reporting
	// drops its label; do not substitute a conflicting flat effort after that decision.
	if value, present := openai.GetOpenAIReasoningEffortFromReqBody(reqBody); present {
		if value == "" {
			return nil
		}
		return &value
	}

	return nil
}

// CanonicalRequestedReasoningEffortFromReqBody captures a recognized pre-policy
// choice from a decoded request, including none. The first nonblank string
// among nested, flat and output_config effort wins; missing and unrecognized
// choices return nil, and request-model spelling does not supply a choice.
func CanonicalRequestedReasoningEffortFromReqBody(reqBody map[string]any) *string {
	if reqBody == nil {
		return nil
	}
	raw := ""
	if reasoning, ok := reqBody["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			raw = strings.TrimSpace(effort)
		}
	}
	// A missing or blank nested string leaves the flat client spelling available
	// for requested-choice capture, whose precedence differs from effective map reporting.
	if raw == "" {
		if effort, ok := reqBody["reasoning_effort"].(string); ok {
			raw = strings.TrimSpace(effort)
		}
	}
	// Anthropic output effort supplies the requested choice after both OpenAI
	// spellings are empty, allowing the same capture path across entry protocols.
	if raw == "" {
		if outputConfig, ok := reqBody["output_config"].(map[string]any); ok {
			if effort, ok := outputConfig["effort"].(string); ok {
				raw = strings.TrimSpace(effort)
			}
		}
	}
	if raw != "" {
		canonical := routing.NormalizeRequestedOpenAIReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}
	return nil
}

// ValidateOpenAIReasoningEffort rejects case-insensitive trimmed ultra values
// at its known HTTP, Realtime response and session effort paths. Other values
// pass this narrow check, requestedModel is unused, and general
// JSON/type/capability validation belongs to callers.
func ValidateOpenAIReasoningEffort(body []byte, requestedModel string) error {
	efforts := []string{
		gjson.GetBytes(body, "reasoning.effort").String(),
		gjson.GetBytes(body, "reasoning_effort").String(),
		gjson.GetBytes(body, "output_config.effort").String(),
		gjson.GetBytes(body, "response.reasoning.effort").String(),
		gjson.GetBytes(body, "response.reasoning_effort").String(),
		gjson.GetBytes(body, "session.reasoning.effort").String(),
		gjson.GetBytes(body, "session.reasoning_effort").String(),
	}
	for _, effort := range efforts {
		if strings.EqualFold(strings.TrimSpace(effort), "ultra") {
			return errors.New(`reasoning effort "ultra" is not supported; use "max"`)
		}
	}

	return nil
}

// ExtractRoutingReasoningEffort reads the explicit protocol effort field from
// request bytes and returns its canonical label for provider model mapping.
// Each entry protocol inspects its primary wire location first before falling
// back to compatible spellings; unrecognized or absent fields return empty,
// and neither model names nor thinking budgets are inferred.
func ExtractRoutingReasoningEffort(body []byte, clientProtocol protocol.ProtocolID) string {
	if len(body) == 0 {
		return ""
	}
	var paths []string
	switch clientProtocol {
	case protocol.ProtocolAnthropicMessages:
		paths = []string{"output_config.effort", "reasoning.effort"}
	case protocol.ProtocolOpenAIChatCompletions:
		paths = []string{"reasoning_effort", "reasoning.effort"}
	case protocol.ProtocolOpenAIResponses, protocol.ProtocolResponsesWebSocket, protocol.ProtocolResponsesCompact:
		paths = []string{"reasoning.effort", "reasoning_effort"}
	case protocol.ProtocolGeminiGenerateContent:
		paths = []string{
			"generationConfig.thinkingConfig.thinkingLevel",
			"generation_config.thinking_config.thinking_level",
		}
	default:
		paths = []string{
			"reasoning.effort",
			"reasoning_effort",
			"output_config.effort",
			"generationConfig.thinkingConfig.thinkingLevel",
			"generation_config.thinking_config.thinking_level",
		}
	}
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if !value.Exists() || value.Type != gjson.String {
			continue
		}
		if normalized := protocol.NormalizeReasoningEffort(value.String()); normalized != "" {
			return normalized
		}
	}
	return ""
}

// StripWireReasoningEffort removes wire effort fields for wireProtocol from a
// JSON request body after an effort-scoped model mapping has encoded the level
// into the upstream model ID. Empty parent objects left behind by removal are
// also deleted so strict upstreams do not reject empty containers; empty input
// or bodies without matching fields are returned unchanged.
func StripWireReasoningEffort(body []byte, wireProtocol protocol.ProtocolID) []byte {
	if len(body) == 0 {
		return body
	}
	deletePath := func(current []byte, path string) ([]byte, bool) {
		if !gjson.GetBytes(current, path).Exists() {
			return current, false
		}
		updated, err := sjson.DeleteBytes(current, path)
		if err != nil {
			// Skip byte mutation if sjson rejects the payload rather than corrupting
			// the outgoing request; the caller still forwards the intact body.
			return current, false
		}
		return updated, true
	}
	deleteIfEmptyObject := func(current []byte, path string) []byte {
		node := gjson.GetBytes(current, path)
		if !node.Exists() || !node.IsObject() || len(node.Map()) != 0 {
			return current
		}
		updated, err := sjson.DeleteBytes(current, path)
		if err != nil {
			return current
		}
		return updated
	}

	out := body
	switch wireProtocol {
	case protocol.ProtocolAnthropicMessages:
		if next, changed := deletePath(out, "output_config.effort"); changed {
			out = deleteIfEmptyObject(next, "output_config")
		}
	case protocol.ProtocolOpenAIChatCompletions:
		out, _ = deletePath(out, "reasoning_effort")
		if next, changed := deletePath(out, "reasoning.effort"); changed {
			out = deleteIfEmptyObject(next, "reasoning")
		}
	case protocol.ProtocolOpenAIResponses, protocol.ProtocolResponsesWebSocket, protocol.ProtocolResponsesCompact:
		if next, changed := deletePath(out, "reasoning.effort"); changed {
			out = deleteIfEmptyObject(next, "reasoning")
		}
		out, _ = deletePath(out, "reasoning_effort")
	case protocol.ProtocolGeminiGenerateContent:
		for _, spec := range [][3]string{
			{"generationConfig.thinkingConfig.thinkingLevel", "generationConfig.thinkingConfig", "generationConfig"},
			{"generation_config.thinking_config.thinking_level", "generation_config.thinking_config", "generation_config"},
			// Antigravity v1internal wraps the Gemini payload under a top-level request object.
			{"request.generationConfig.thinkingConfig.thinkingLevel", "request.generationConfig.thinkingConfig", "request.generationConfig"},
			{"request.generation_config.thinking_config.thinking_level", "request.generation_config.thinking_config", "request.generation_config"},
		} {
			if next, changed := deletePath(out, spec[0]); changed {
				next = deleteIfEmptyObject(next, spec[1])
				out = deleteIfEmptyObject(next, spec[2])
			}
		}
	}
	return out
}
