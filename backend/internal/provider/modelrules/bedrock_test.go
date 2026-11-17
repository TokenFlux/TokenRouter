package modelrules

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Bedrock encoding registration distinguishes exact Claude versions from
// lookalike names and future suffixes. Region prefixed fixtures exercise the
// same thinking and cache choices as their underlying model while unknown
// identities remain empty.

// TestBedrockExecutionRules checks registered regional Claude identities and
// unsupported variants. Known identities must retain their thinking modes,
// cache lifetimes, and tool search flags; unknown identities return empty rules.
func TestBedrockExecutionRules(t *testing.T) {
	for _, tc := range []struct {
		// model: complete Bedrock request ID; mode: expected thinking protocol family.
		model, mode string
		toolSearch  bool // expected tool search support
	}{
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", "budget", true},
		{"eu.anthropic.claude-opus-4-6-v1", "adaptive_or_budget", true},
		{"global.anthropic.claude-opus-4-7", "adaptive", true},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "budget", false},
		{"anthropic.claude-sonnet-5-5", "adaptive_between_tools", true},
		{"anthropic.claude-fable-5-1", "adaptive", false},
	} {
		rules := BedrockExecutionRules(tc.model)
		require.Equal(t, tc.mode, rules.ThinkingMode)
		require.Equal(t, []string{"5m", "1h"}, rules.CacheTTL)
		require.Equal(t, new(tc.toolSearch), rules.ToolSearch)
	}
	for _, unknown := range []string{"custom/anthropic.claude-opus-4-7", "anthropic.claude-opus-4-7-future", "anthropic.claude-opus-9", "anthropic.claude-fable-50"} {
		require.Equal(t, ExecutionRules{}, BedrockExecutionRules(unknown))
	}
}
