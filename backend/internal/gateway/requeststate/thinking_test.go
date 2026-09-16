package requeststate

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestThinkingPolicyPreservesExcludedPayload(t *testing.T) {
	body := []byte(`{"thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"history"}]}]}`)
	require.Equal(t, body, FilterThinkingBlocks(body, ThinkingRequestOptions{}))
	require.Equal(t, body, FilterThinkingBlocksForRetry(body, ThinkingRequestOptions{}))
	require.Equal(t, body, FilterSignatureSensitiveBlocksForRetry(body, ThinkingRequestOptions{}))
	filtered := FilterThinkingBlocks(body, ThinkingRequestOptions{PreFilter: true, DummySignature: "placeholder"})
	require.NotEqual(t, body, filtered)
}
func TestThinkingPolicyExplicitEffortAndNativeFallback(t *testing.T) {
	body := []byte(`{"thinking":{"type":"adaptive"}}`)
	effort := "max"
	require.Same(t, &effort, ApplyThinkingEnabledFallback(&effort, body, ThinkingRequestOptions{PassbackRequired: true}))
	require.Nil(t, ApplyThinkingEnabledFallback(nil, body, ThinkingRequestOptions{PassbackRequired: true, NativeReasoningEffort: true}))
	require.Equal(t, "high", *ApplyThinkingEnabledFallback(nil, body, ThinkingRequestOptions{PassbackRequired: true}))
}
func TestThinkingPolicyGLMVariants(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options ThinkingRequestOptions
		value   string
	}{
		{"legacy", ThinkingRequestOptions{GLM: true}, "high"},
		{"53", ThinkingRequestOptions{GLM: true, GLM53: true}, "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := NormalizeGLMOpenAIReasoningEffort([]byte(`{"reasoning_effort":"low"}`), tc.options)
			require.Equal(t, tc.value, gjson.GetBytes(out, "reasoning_effort").String())
		})
	}
	out, changed := NormalizeGLM53AnthropicThinking([]byte(`{"output_config":{"effort":"ultra"}}`), true)
	require.True(t, changed)
	require.Equal(t, "enabled", gjson.GetBytes(out, "thinking.type").String())
	require.Equal(t, "max", gjson.GetBytes(out, "output_config.effort").String())
	out, changed = NormalizeChineseLLMThinking([]byte(`{"thinking":{"type":"enabled"}}`), true)
	require.True(t, changed)
	require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
}
