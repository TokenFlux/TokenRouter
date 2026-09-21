package forward

import (
	json "encoding/json"
	testing "testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	require "github.com/stretchr/testify/require"
)

func TestChatCompletionsToResponsesPreservesXSearchTool(t *testing.T) {
	enabled := true
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "grok-4.5",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"latest xAI post"`)},
		},
		Tools: []protocolopenai.ChatTool{{
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

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.Len(t, resp.Tools, 1)
	require.Equal(t, "x_search", resp.Tools[0].Type)
	require.Equal(t, []string{"xai"}, resp.Tools[0].AllowedXHandles)
	require.Equal(t, []string{"spam"}, resp.Tools[0].ExcludedXHandles)
	require.Equal(t, "2026-08-01", resp.Tools[0].FromDate)
	require.Equal(t, "2026-08-10", resp.Tools[0].ToDate)
	require.NotNil(t, resp.Tools[0].EnableImageUnderstanding)
	require.True(t, *resp.Tools[0].EnableImageUnderstanding)
	require.NotNil(t, resp.Tools[0].EnableVideoUnderstanding)
	require.True(t, *resp.Tools[0].EnableVideoUnderstanding)
	require.JSONEq(t, `{"type":"x_search"}`, string(resp.ToolChoice))
}

func TestChatCompletionsToResponsesPreservesSupportedBuiltInTools(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model:    "claude-opus-4-6-thinking",
		Messages: []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
		Tools: []protocolopenai.ChatTool{
			{Type: "function", Function: &protocolopenai.ChatFunction{Name: "read_file", Parameters: json.RawMessage(`{"type":"object"}`)}},
			{Type: "web_search"},
			{Type: "code_execution"},
			{Type: "unsupported_builtin"},
			{Type: "function"},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.Len(t, resp.Tools, 3)
	require.Equal(t, "function", resp.Tools[0].Type)
	require.Equal(t, "read_file", resp.Tools[0].Name)
	require.Equal(t, "web_search", resp.Tools[1].Type)
	require.Equal(t, "code_execution", resp.Tools[2].Type)
}
