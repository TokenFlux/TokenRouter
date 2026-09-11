package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesToChatCompletionsPreservesXSearchTool(t *testing.T) {
	enabled := true
	req := &ResponsesRequest{
		Model: "grok-4.5",
		Input: json.RawMessage(`"latest xAI post"`),
		Tools: []ResponsesTool{{
			Type:                     "x_search",
			AllowedXHandles:          []string{"xai"},
			ExcludedXHandles:         []string{"spam"},
			FromDate:                 "2026-08-01",
			ToDate:                   "2026-08-10",
			EnableImageUnderstanding: &enabled,
			EnableVideoUnderstanding: &enabled,
		}},
		ToolChoice: json.RawMessage(`{"type":"x_search"}`),
	}

	chat, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chat.Tools, 1)
	require.Equal(t, "x_search", chat.Tools[0].Type)
	require.Equal(t, []string{"xai"}, chat.Tools[0].AllowedXHandles)
	require.Equal(t, []string{"spam"}, chat.Tools[0].ExcludedXHandles)
	require.JSONEq(t, `{"type":"x_search"}`, string(chat.ToolChoice))
}

func TestResponsesToChatCompletionsXSearchToolChoiceString(t *testing.T) {
	chat, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{
		Model:      "grok-4.5",
		Input:      json.RawMessage(`"latest xAI post"`),
		Tools:      []ResponsesTool{{Type: "x_search"}},
		ToolChoice: json.RawMessage(`"x_search"`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `"x_search"`, string(chat.ToolChoice))
}
