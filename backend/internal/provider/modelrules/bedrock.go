package modelrules

import "strings"

// bedrock.go selects execution rules for registered Bedrock Claude identities.
// Bedrock Claude identifiers can include a catalog provider and an inference
// region before the model name. Encoding rules are selected from complete
// registered identities after removing those routing prefixes, so unregistered
// versions retain unknown cache, tool search, and thinking settings.

// BedrockExecutionRules returns registered cache, tool search, and thinking
// encodings for a Bedrock model. Unknown identities return empty rules; add
// matching request fixtures when registering a supported encoding.
func BedrockExecutionRules(model string) ExecutionRules {
	model = strings.TrimPrefix(normalize(model), "amazon-bedrock/")
	// Regional and global inference IDs address the same registered encoding.
	// Strip one recognized routing prefix while keeping model version suffixes for exact selection.
	for _, prefix := range []string{"us.", "eu.", "apac.", "jp.", "au.", "us-gov.", "global."} {
		if name, found := strings.CutPrefix(model, prefix); found {
			model = name
			break
		}
	}
	model = strings.TrimPrefix(model, "anthropic.")
	result := ExecutionRules{}
	switch model {
	case "claude-opus-4-5", "claude-opus-4-5-20251101-v1:0", "claude-sonnet-4-5", "claude-sonnet-4-5-20250929-v1:0":
		result.ThinkingMode = "budget"
		result.ToolSearch = new(true)
	case "claude-opus-4-6", "claude-opus-4-6-v1", "claude-sonnet-4-6":
		result.ThinkingMode = "adaptive_or_budget"
		result.ToolSearch = new(true)
	case "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5", "claude-sonnet-5":
		result.ThinkingMode = "adaptive"
		result.ToolSearch = new(true)
	case "claude-sonnet-5-5":
		result.ThinkingMode = "adaptive_between_tools"
		result.ToolSearch = new(true)
	case "claude-haiku-4-5", "claude-haiku-4-5-20251001-v1:0":
		result.ThinkingMode = "budget"
		result.ToolSearch = new(false)
	case "claude-haiku-5-5":
		result.ThinkingMode = "adaptive"
		result.ToolSearch = new(false)
	case "claude-fable-5", "claude-fable-5-1":
		result.ThinkingMode = "adaptive"
		result.ToolSearch = new(false)
	default:
		return result
	}
	result.CacheTTL = []string{"5m", "1h"}
	return result
}
