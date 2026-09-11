package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChatReasoningAliasNonStreaming 验证非流响应的 reasoning 别名进入两种桥接协议。
func TestChatReasoningAliasNonStreaming(t *testing.T) {
	var response ChatCompletionsResponse
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"chatcmpl-alias","model":"reasoning-model",
		"choices":[{"index":0,"message":{"role":"assistant","content":"final answer","reasoning":"fallback reasoning"},"finish_reason":"stop"}]
	}`), &response))

	anthropic := ChatCompletionsResponseToAnthropic(testRuntime(), &response, "claude-sonnet")
	require.Len(t, anthropic.Content, 2)
	require.Equal(t, "thinking", anthropic.Content[0].Type)
	require.Equal(t, "fallback reasoning", anthropic.Content[0].Thinking)

	responses := ChatCompletionsResponseToResponses(testRuntime(), &response, "reasoning-model", nil, nil, false, nil)
	require.Len(t, responses.Output, 2)
	require.Equal(t, "reasoning", responses.Output[0].Type)
	require.Equal(t, "fallback reasoning", responses.Output[0].Summary[0].Text)
}

// TestChatReasoningAliasStreaming 验证流式别名和正式字段的优先级。
func TestChatReasoningAliasStreaming(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "alias fallback",
			payload: `{"id":"chatcmpl-alias","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning":"streamed fallback"},"finish_reason":null}]}`,
			want:    "streamed fallback",
		},
		{
			name:    "reasoning content precedence",
			payload: `{"id":"chatcmpl-alias","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning_content":"preferred reasoning","reasoning":"fallback reasoning"},"finish_reason":null}]}`,
			want:    "preferred reasoning",
		},
		{
			name:    "explicit empty reasoning content precedence",
			payload: `{"id":"chatcmpl-alias","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning_content":"","reasoning":"must not leak"},"finish_reason":null}]}`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk ChatCompletionsChunk
			require.NoError(t, json.Unmarshal([]byte(tt.payload), &chunk))

			anthropicEvents := ChatCompletionsChunkToAnthropicEvents(testRuntime(), &chunk, NewChatCompletionsToAnthropicStreamState(testRuntime(), "reasoning-model"))
			var anthropicReasoning string
			for _, event := range anthropicEvents {
				if event.Delta != nil && event.Delta.Type == "thinking_delta" {
					anthropicReasoning += event.Delta.Thinking
				}
			}
			require.Equal(t, tt.want, anthropicReasoning)

			responseEvents := ChatCompletionsChunkToResponsesEvents(testRuntime(), &chunk, NewChatCompletionsToResponsesStreamState(testRuntime(), "reasoning-model"))
			var responsesReasoning string
			for _, event := range responseEvents {
				if event.Type == "response.reasoning_summary_text.delta" {
					responsesReasoning += event.Delta
				}
			}
			require.Equal(t, tt.want, responsesReasoning)
		})
	}
}
