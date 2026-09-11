package apicompat

import (
	json "encoding/json"
	require "github.com/stretchr/testify/require"
	testing "testing"
)

// 验证两种 OpenAI 入站协议不会把 xhigh 提升到会额外计费的 max。
func TestAnthropicReasoningBridgePreservesEffort(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		t.Run(effort, func(t *testing.T) {
			var responses ResponsesRequest
			require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-fable-5-1","input":"hi","reasoning":{"effort":"`+effort+`"}}`), &responses))
			converted, err := ResponsesToAnthropicRequest(&responses)
			require.NoError(t, err)
			require.Equal(t, effort, converted.OutputConfig.Effort)
			var chat ChatCompletionsRequest
			require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"`+effort+`"}`), &chat))
			bridge, err := ChatCompletionsToResponses(&chat)
			require.NoError(t, err)
			converted, err = ResponsesToAnthropicRequest(bridge)
			require.NoError(t, err)
			require.Equal(t, effort, converted.OutputConfig.Effort)
		})
	}
}
