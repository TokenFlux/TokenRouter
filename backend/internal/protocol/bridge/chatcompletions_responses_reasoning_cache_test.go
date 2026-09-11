package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesToChatReasoningCacheLookupRestoresEncryptedOnlyItem(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-reasoner",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"item_enc1","summary":[],"encrypted_content":"opaque"},
			{"type":"function_call","call_id":"call_1","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}
		]`),
	}

	out, err := ResponsesToChatCompletionsRequestWithOptions(req, &ResponsesToChatOptions{
		ReasoningContentByID: func(itemID string) string {
			if itemID == "item_enc1" {
				return "cached thinking"
			}
			return ""
		},
	})
	require.NoError(t, err)
	require.Len(t, out.Messages, 3)
	require.Equal(t, "cached thinking", out.Messages[0].ReasoningContent)
	require.Len(t, out.Messages[0].ToolCalls, 1)
}

func TestResponsesToChatChainedToolCallsReplayTurnReasoning(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-reasoner",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"item_r1","summary":[{"type":"summary_text","text":"turn thinking"}]},
			{"type":"function_call","call_id":"call_a","name":"exec_command","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_a","output":"ok"},
			{"type":"function_call","call_id":"call_b","name":"exec_command","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_b","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]},
			{"type":"reasoning","id":"item_r2","summary":[{"type":"summary_text","text":"second turn"}]},
			{"type":"function_call","call_id":"call_c","name":"exec_command","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_c","output":"ok"}
		]`),
	}

	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	byCallID := map[string]ChatMessage{}
	for _, message := range out.Messages {
		for _, call := range message.ToolCalls {
			byCallID[call.ID] = message
		}
	}
	require.Equal(t, "turn thinking", byCallID["call_a"].ReasoningContent)
	require.Equal(t, "turn thinking", byCallID["call_b"].ReasoningContent)
	require.Equal(t, "second turn", byCallID["call_c"].ReasoningContent)
}

func TestExtractResponsesReasoningItem(t *testing.T) {
	id, text, ok := ExtractResponsesReasoningItem(json.RawMessage(
		`{"type":"reasoning","id":"item_a","summary":[{"type":"summary_text","text":"think"}]}`))
	require.True(t, ok)
	require.Equal(t, "item_a", id)
	require.Equal(t, "think", text)

	_, _, ok = ExtractResponsesReasoningItem(json.RawMessage(
		`{"type":"message","role":"user","content":"hi"}`))
	require.False(t, ok)
}
