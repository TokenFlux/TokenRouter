package provider

import (
	"strings"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/stretchr/testify/require"
)

func TestSanitizeOpenAIResponsesOrphanToolOutputs(t *testing.T) {
	t.Run("preserves matches regardless of item order", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "tool_search_output", "call_id": "search_1", "output": "first"},
			map[string]any{"type": "tool_search_call", "id": "search_1", "query": "docs"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "custom_1", "output": "second"},
			map[string]any{"type": "item_reference", "id": "custom_1"},
		}
		reqBody := map[string]any{"input": input}

		require.False(t, SanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
		require.Equal(t, input, reqBody["input"])
	})

	t.Run("outputs do not legitimize each other", func(t *testing.T) {
		input := []any{
			map[string]any{"type": "function_call_output", "call_id": "missing", "output": "one"},
			map[string]any{"type": "tool_search_output", "call_id": "missing", "output": "two"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "missing", "output": "three"},
			map[string]any{"type": "mcp_tool_call_output", "call_id": "missing", "output": "four"},
		}
		reqBody := map[string]any{"input": input}

		require.True(t, SanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
		got, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Empty(t, got)
	})

	t.Run("preserves all output variants with matching calls", func(t *testing.T) {
		pairs := []struct {
			callType   string
			outputType string
		}{
			{callType: "function_call", outputType: "function_call_output"},
			{callType: "tool_search_call", outputType: "tool_search_output"},
			{callType: "custom_tool_call", outputType: "custom_tool_call_output"},
			{callType: "mcp_tool_call", outputType: "mcp_tool_call_output"},
		}
		input := make([]any, 0, len(pairs)*2)
		for index, pair := range pairs {
			callID := string(rune('a' + index))
			input = append(input,
				map[string]any{"type": pair.callType, "call_id": callID},
				map[string]any{"type": pair.outputType, "call_id": callID, "output": "ok"},
			)
		}
		reqBody := map[string]any{"input": input}

		require.False(t, SanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, false))
	})

	t.Run("previous response may contain the missing call", func(t *testing.T) {
		input := []any{map[string]any{"type": "function_call_output", "call_id": "remote", "output": "ok"}}
		reqBody := map[string]any{"input": input, "previous_response_id": "resp_1"}

		require.False(t, SanitizeOpenAIResponsesOrphanToolOutputs(reqBody, input, true))
		require.Equal(t, input, reqBody["input"])
	})
}

func TestOpenAIResponsesInputTextIsNeverSilentlyTruncated(t *testing.T) {
	atLimit := strings.Repeat("z", openAIResponsesInputTextMaxChars)
	oversized := strings.Repeat("a", openAIResponsesInputTextMaxChars) + "中"
	input := []any{
		map[string]any{"type": "function_call_output", "call_id": "limit", "output": atLimit},
		map[string]any{"type": "function_call_output", "call_id": "a", "output": oversized},
		map[string]any{"type": "tool_search_output", "call_id": "b", "output": oversized},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c", "output": oversized},
		map[string]any{"type": "mcp_tool_call_output", "call_id": "d", "output": oversized},
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "short"},
				map[string]any{"type": "input_text", "text": oversized},
			},
		},
	}
	reqBody := map[string]any{"input": input}

	encoded, err := wirejson.Marshal(reqBody)
	require.NoError(t, err)
	preserved, changed, err := NormalizeOpenAIResponsesWebSocketCompatibilityBody(encoded, &accountcore.Record{Platform: accountcore.PlatformOpenAI, Type: accountcore.AccountTypeAPIKey}, false)
	require.NoError(t, err)
	require.False(t, changed)
	var decoded map[string]any
	require.NoError(t, wirejson.DecodeUseNumber(preserved, &decoded))
	var inputOK bool
	input, inputOK = decoded["input"].([]any)
	require.True(t, inputOK)
	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, atLimit, first["output"])
	for _, rawItem := range input[1:5] {
		item, ok := rawItem.(map[string]any)
		require.True(t, ok)
		require.Equal(t, oversized, item["output"])
	}
	last, ok := input[5].(map[string]any)
	require.True(t, ok)
	content, ok := last["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	shortPart, ok := content[0].(map[string]any)
	require.True(t, ok)
	oversizedPart, ok := content[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "short", shortPart["text"])
	require.Equal(t, oversized, oversizedPart["text"])
}

func TestOpenAIResponsesInputNeverRequestsPreemptiveTruncation(t *testing.T) {
	short := []byte(`{"input":[{"type":"function_call_output","output":"ok"}]}`)
	largeUnrelated := []byte(`{"input":"` + strings.Repeat("x", openAIResponsesInputTextMaxChars+1) + `"}`)
	largeOutput := []byte(`{"input":[{"type":"function_call_output","output":"` + strings.Repeat("x", openAIResponsesInputTextMaxChars+1) + `"}]}`)

	{
		preserved, changed, err := NormalizeOpenAIResponsesWebSocketCompatibilityBody(short, &accountcore.Record{Platform: accountcore.PlatformOpenAI, Type: accountcore.AccountTypeAPIKey}, false)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, short, preserved)
	}
	{
		preserved, changed, err := NormalizeOpenAIResponsesWebSocketCompatibilityBody(largeUnrelated, &accountcore.Record{Platform: accountcore.PlatformOpenAI, Type: accountcore.AccountTypeAPIKey}, false)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, largeUnrelated, preserved)
	}
	{
		preserved, changed, err := NormalizeOpenAIResponsesWebSocketCompatibilityBody(largeOutput, &accountcore.Record{Platform: accountcore.PlatformOpenAI, Type: accountcore.AccountTypeAPIKey}, false)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, largeOutput, preserved)
	}
}

// 原边界长度仅作回归数据，生产链不静默截断。
const openAIResponsesInputTextMaxChars = 10000000
