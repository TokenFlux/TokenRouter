package modelrules

import (
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// antigravity.go defines Antigravity limits for converted requests. The entry
// protocol determines output allowances, and registered models supply thinking
// budgets and native field omissions shared by preview and request conversion.

// antigravityPatch supplies limits and thinking encodings for converted
// Antigravity requests. The Gemini native client protocol leaves these
// conversion limits unset and continues with its catalog capabilities.
//
// Claude identities and Chat requests use the registered 64,000-token limit;
// other converted routes use 65,000 tokens, retaining any smaller known
// catalog limit. Budget padding reserves output room above thinking. Registered
// adaptive Claude models use their fixed thinking budget, and selected Gemini
// thinking identities also restrict sampling, tool configuration, and stop
// sequences for their request transformer.
func (r *Resolution) antigravityPatch(patch *modelcatalog.Attributes) {
	model := normalize(r.Target.Model)
	if r.Target.ClientProtocol == "gemini_generate_content" {
		return
	}
	// Converted non-Claude Messages routes use their separate output allowance.
	limit := 65000
	if strings.HasPrefix(model, "claude-") || r.Target.ClientProtocol == "openai_chat_completions" {
		limit = AntigravityDefaultOutputLimit
	}
	patch.OutputLimit = modelcatalog.MinimumLimit(r.Attributes.OutputLimit, &limit)
	// GeminiThinking subtracts this allowance when bounding positive thinking
	// budgets, retaining output space between the chosen budget and the
	// converted request's limit.
	r.Rules.BudgetPadding = 1000
	if slices.Contains([]string{"claude-opus-4-6", "claude-opus-4-6-thinking", "claude-opus-4-7", "claude-opus-4-7-thinking", "claude-opus-4-8", "claude-opus-4-8-thinking"}, model) {
		r.Rules.AdaptiveThinkingBudget = AntigravityAdaptiveBudget
	}
	if slices.Contains([]string{"gemini-2.5-flash-thinking", "gemini-3-pro-high", "gemini-3.1-pro-high", "gemini-3.6-flash-high", "gemini-3.6-flash-low", "gemini-3.6-flash-medium", "gemini-3.6-flash-tiered"}, model) {
		// The Antigravity request transformer consumes these thinking-route
		// options when preparing Gemini payloads, excluding empty tool
		// configuration and stop sequences from that representation.
		patch.Temperature, patch.TopP = new(false), new(false)
		r.Rules.DropFields = append(r.Rules.DropFields, "top_k")
		r.Rules.OmitEmptyToolConfig = true
		r.Rules.OmitStopSequences = true
	}
}
