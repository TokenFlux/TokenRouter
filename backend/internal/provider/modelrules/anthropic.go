package modelrules

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// anthropic.go defines Claude reasoning and signature rules for direct, hosted
// and Messages-compatible routes. Effective model metadata selects budget or
// adaptive encoding, while the chosen authentication method supplies
// Bedrock-specific controls.

// applyAnthropicAccess selects thinking and signature encodings for the native Claude endpoint.
func (r *Resolution) applyAnthropicAccess(model string, conditions modelcatalog.ParameterConditions) {
	t := r.Target
	if t.ProviderType == "bedrock" {
		r.Rules = BedrockExecutionRules(model)
	}
	if strings.HasPrefix(model, "claude-") || t.ProviderType == "bedrock" || t.ProviderType == "service_account" {
		conditions["temperature"] = modelcatalog.ParameterCondition{Thinking: new(false)}
		r.Rules.SignatureMode = "anthropic"
		if mode := claudeThinkingMode(model, r.Attributes); mode != "" {
			r.Rules.ThinkingMode = mode
		}
	}
}

// claudeThinkingMode selects a registered Claude thinking encoding from
// known reasoning controls and the full model name. Unknown reasoning
// declarations return no encoding; budget controls take precedence over
// adaptive identity selection.
//
// A budget option with effort choices selects adaptive_or_budget, while a
// budget option alone selects budget. When budget controls are absent, only
// registered adaptive identities select an encoding. Vertex date suffixes are
// removed for that encoding selection after their full identities have
// already determined catalog metadata.
func claudeThinkingMode(model string, attrs modelcatalog.Attributes) string {
	if attrs.Reasoning == nil && attrs.ReasoningOptions == nil {
		return ""
	}
	if attrs.HasReasoningOption("budget_tokens") {
		if attrs.HasReasoningOption("effort") {
			return "adaptive_or_budget"
		}
		return "budget"
	}
	// Vertex date and default identities have already selected separate
	// metadata. Strip the date marker here to choose their shared message
	// encoding from the registered family.
	identity, _, _ := strings.Cut(model, "@")
	switch identity {
	case "claude-sonnet-5-5":
		return "adaptive_between_tools"
	case "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5", "claude-sonnet-5", "claude-haiku-5-5":
		return "adaptive"
	default:
		return ""
	}
}
