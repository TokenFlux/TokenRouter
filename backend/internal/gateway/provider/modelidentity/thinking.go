package modelidentity

import (
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// thinking.go resolves model signature policy and gates Anthropic thinking
// repair filters.
// Historical thinking blocks require the rules of the model receiving the
// Anthropic body. Catalogue signature modes distinguish strict Anthropic
// validation from upstreams that need unchanged passback, and unknown modes
// leave the gateway's repair guards disabled.

// Thinking protocol values tell request filters which signature handling the
// resolved model family permits before forwarding historical blocks or retrying
// rejected requests.
const (
	// ThinkingProtocolUnknown leaves thinking blocks intact when the protocol family is unknown.
	ThinkingProtocolUnknown ThinkingProtocol = iota

	// ThinkingProtocolAnthropicStrict removes thinking blocks with missing or invalid signatures.
	ThinkingProtocolAnthropicStrict

	// ThinkingProtocolPassbackRequired replays thinking blocks unchanged for compatible upstreams.
	ThinkingProtocolPassbackRequired
)

// ThinkingProtocol describes how the upstream handles historical thinking blocks.
// Anthropic requires valid signatures; compatible providers with passback rules
// require the historical blocks to be replayed unchanged.
type ThinkingProtocol int

// ResolveThinkingProtocol converts the catalogue's Anthropic-message signature
// mode into a thinking policy. Anthropic forwarding supplies the mapped
// upstream model, while Gemini Messages retry callers use the model parsed
// from the Anthropic body they filter. Unrecognized signature modes return
// ThinkingProtocolUnknown.
func ResolveThinkingProtocol(modelID string) ThinkingProtocol {
	rules := (modelrules.View{}).Resolve(modelrules.Target{Model: modelID, UpstreamProtocol: "anthropic_messages"}, modelcatalog.Attributes{}).Rules
	switch rules.SignatureMode {
	case "passback":
		return ThinkingProtocolPassbackRequired
	case "anthropic":
		return ThinkingProtocolAnthropicStrict
	default:
		// Unrecognized signature modes retain historical blocks because the gateway
		// has no catalogue rule authorizing strict Anthropic filtering for this model.
		return ThinkingProtocolUnknown
	}
}

// ShouldPreFilterThinkingBlocks reports whether the resolved family uses strict
// Anthropic signature validation.
func ShouldPreFilterThinkingBlocks(modelID string) bool {
	return ResolveThinkingProtocol(modelID) == ThinkingProtocolAnthropicStrict
}

// ShouldRectifyThinkingSignatureError reports whether strict Anthropic rules
// permit signature-error repair on retry.
func ShouldRectifyThinkingSignatureError(modelID string) bool {
	return ResolveThinkingProtocol(modelID) == ThinkingProtocolAnthropicStrict
}

// ShouldApplyRetryFilters reports whether strict Anthropic rules permit
// rewriting retry-sensitive thinking and tool blocks.
func ShouldApplyRetryFilters(modelID string) bool {
	return ResolveThinkingProtocol(modelID) == ThinkingProtocolAnthropicStrict
}
