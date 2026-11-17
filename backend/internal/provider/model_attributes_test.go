package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// An Ollama Chat route supplies its provider output cap to catalogue execution
// rules, including nonpositive values that disable clamping. The test compares
// those route attributes and verifies that the same provider's Responses route
// does not acquire the Chat-specific Ollama interpretation.

// TestModelAttributeTargetOllamaLimits resolves positive, zero, and negative
// caps on an Ollama Chat target. It checks the copied cap, positive-limit
// clamping and output attributes, and Ollama exclusion on a Responses target.
func TestModelAttributeTargetOllamaLimits(t *testing.T) {
	for _, limit := range []int{-1, 0, 32000, 90000} {
		value := &Record{Platform: "openai", Type: "apikey", Credentials: map[string]any{"base_url": "https://ollama.com"}, Extra: map[string]any{"ollama_max_tokens_cap": limit}}
		target := value.ModelAttributeTarget("custom", "openai_chat_completions")
		require.True(t, target.Ollama)
		require.Equal(t, limit, *target.OllamaOutputCap)
		result := (modelrules.View{}).Resolve(target, modelcatalog.Attributes{})
		require.Equal(t, limit > 0, result.Rules.ClampOutput)
		if limit > 0 {
			require.Equal(t, limit, *result.Attributes.OutputLimit)
		}
		require.False(t, value.ModelAttributeTarget("custom", "openai_responses").Ollama)
	}
}
