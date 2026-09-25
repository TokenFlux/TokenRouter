package forward

import (
	"encoding/json"
	"testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionsToResponses_BasicText(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", resp.Model)
	assert.True(t, resp.Stream) // always forced true
	assert.False(t, *resp.Store)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 1)
	assert.Equal(t, "user", items[0].Role)
}

func TestChatCompletionsToResponses_SystemMessage(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "system", Content: json.RawMessage(`"You are helpful."`)},
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 2)
	assert.Equal(t, "system", items[0].Role)
	assert.Equal(t, "user", items[1].Role)
}

func TestChatCompletionsToResponses_ToolCalls(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Call the function"`)},
			{
				Role: "assistant",
				ToolCalls: []protocolopenai.ChatToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: protocolopenai.ChatFunctionCall{
							Name:      "ping",
							Arguments: `{"host":"example.com"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_1",
				Content:    json.RawMessage(`"pong"`),
			},
		},
		Tools: []protocolopenai.ChatTool{
			{
				Type: "function",
				Function: &protocolopenai.ChatFunction{
					Name:        "ping",
					Description: "Ping a host",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
			},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	// user + function_call + function_call_output = 3
	// (assistant message with empty content + tool_calls → only function_call items emitted)
	require.Len(t, items, 3)

	// Check function_call item
	assert.Equal(t, "function_call", items[1].Type)
	assert.Equal(t, "call_1", items[1].CallID)
	assert.Empty(t, items[1].ID)
	assert.Equal(t, "ping", items[1].Name)

	// Check function_call_output item
	assert.Equal(t, "function_call_output", items[2].Type)
	assert.Equal(t, "call_1", items[2].CallID)
	assert.Equal(t, "pong", items[2].Output)

	// Check tools
	require.Len(t, resp.Tools, 1)
	assert.Equal(t, "function", resp.Tools[0].Type)
	assert.Equal(t, "ping", resp.Tools[0].Name)
}

func TestChatCompletionsToResponses_ToolStrict(t *testing.T) {
	strictTrue := true
	strictFalse := false
	tests := []struct {
		name   string
		strict *bool
		want   bool
	}{
		{name: "defaults omitted strict to false", want: false},
		{name: "preserves explicit true", strict: &strictTrue, want: true},
		{name: "preserves explicit false", strict: &strictFalse, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &protocolopenai.ChatCompletionsRequest{
				Model:    "gpt-4o",
				Messages: []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
				Tools: []protocolopenai.ChatTool{{
					Type: "function",
					Function: &protocolopenai.ChatFunction{
						Name:   "lookup",
						Strict: tt.strict,
					},
				}},
			}

			resp, err := ChatCompletionsToResponses(req)
			require.NoError(t, err)
			require.Len(t, resp.Tools, 1)
			require.NotNil(t, resp.Tools[0].Strict)
			assert.Equal(t, tt.want, *resp.Tools[0].Strict)

			payload, err := json.Marshal(resp)
			require.NoError(t, err)

			var serialized struct {
				Tools []map[string]json.RawMessage `json:"tools"`
			}
			require.NoError(t, json.Unmarshal(payload, &serialized))
			require.Len(t, serialized.Tools, 1)
			strictJSON, ok := serialized.Tools[0]["strict"]
			require.True(t, ok, "strict must be present in the Responses payload")
			assert.JSONEq(t, string(mustMarshalJSON(t, tt.want)), string(strictJSON))
		})
	}
}

func TestChatCompletionsToResponses_LegacyFunctionDefaultsStrictFalse(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model:    "gpt-4o",
		Messages: []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
		Functions: []protocolopenai.ChatFunction{{
			Name: "lookup",
		}},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.Len(t, resp.Tools, 1)
	require.NotNil(t, resp.Tools[0].Strict)
	assert.False(t, *resp.Tools[0].Strict)

	payload, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"strict":false`)
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func TestChatCompletionsToResponses_MaxTokens(t *testing.T) {
	t.Run("max_tokens", func(t *testing.T) {
		maxTokens := 100
		req := &protocolopenai.ChatCompletionsRequest{
			Model:     "gpt-4o",
			MaxTokens: &maxTokens,
			Messages:  []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
		}
		resp, err := ChatCompletionsToResponses(req)
		require.NoError(t, err)
		require.NotNil(t, resp.MaxOutputTokens)
		// Below minMaxOutputTokens (128), should be clamped
		assert.Equal(t, minMaxOutputTokens, *resp.MaxOutputTokens)
	})

	t.Run("max_completion_tokens_preferred", func(t *testing.T) {
		maxTokens := 100
		maxCompletion := 500
		req := &protocolopenai.ChatCompletionsRequest{
			Model:               "gpt-4o",
			MaxTokens:           &maxTokens,
			MaxCompletionTokens: &maxCompletion,
			Messages:            []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
		}
		resp, err := ChatCompletionsToResponses(req)
		require.NoError(t, err)
		require.NotNil(t, resp.MaxOutputTokens)
		assert.Equal(t, 500, *resp.MaxOutputTokens)
	})
}

func TestChatCompletionsToResponses_ReasoningEffort(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model:           "gpt-4o",
		ReasoningEffort: "high",
		Messages:        []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.NotNil(t, resp.Reasoning)
	assert.Equal(t, "high", resp.Reasoning.Effort)
	assert.Equal(t, "auto", resp.Reasoning.Summary)
}

func TestChatCompletionsToResponses_ResponseFormatJsonObject(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model:          "gpt-4o",
		Messages:       []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Return JSON"`)}},
		ResponseFormat: json.RawMessage(`{"type":"json_object"}`),
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.NotNil(t, resp.Text)
	assert.JSONEq(t, `{"type":"json_object"}`, string(resp.Text.Format))

	payload, err := json.Marshal(resp)
	require.NoError(t, err)
	var serialized struct {
		Text protocolopenai.ResponsesText `json:"text"`
	}
	require.NoError(t, json.Unmarshal(payload, &serialized))
	assert.JSONEq(t, `{"type":"json_object"}`, string(serialized.Text.Format))
}

func TestChatCompletionsToResponses_ResponseFormatJsonSchema(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model:    "gpt-4o",
		Messages: []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Return structured JSON"`)}},
		ResponseFormat: json.RawMessage(`{
			"type":"json_schema",
			"json_schema":{
				"name":"answer",
				"schema":{
					"type":"object",
					"properties":{"ok":{"type":"boolean"}},
					"required":["ok"],
					"additionalProperties":false
				},
				"strict":true
			}
		}`),
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.NotNil(t, resp.Text)
	assert.JSONEq(t, `{
		"type":"json_schema",
		"name":"answer",
		"schema":{
			"type":"object",
			"properties":{"ok":{"type":"boolean"}},
			"required":["ok"],
			"additionalProperties":false
		},
		"strict":true
	}`, string(resp.Text.Format))
}

func TestChatCompletionsToResponses_RejectsUltraReasoningEffort(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model:           "gpt-5.6-sol",
		ReasoningEffort: "ultra",
		Messages:        []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.ErrorContains(t, err, "not supported")
	require.Nil(t, resp)
}

func TestChatCompletionsToResponses_ImageURL(t *testing.T) {
	content := `[{"type":"text","text":"Describe this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc123"}}]`
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(content)},
		},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 1)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 2)
	assert.Equal(t, "input_text", parts[0].Type)
	assert.Equal(t, "Describe this", parts[0].Text)
	assert.Equal(t, "input_image", parts[1].Type)
	assert.Equal(t, "data:image/png;base64,abc123", parts[1].ImageURL)
}

func TestChatCompletionsToResponses_EmptyBase64ImageURLSkipped(t *testing.T) {
	content := `[{"type":"text","text":"Describe this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,"}}]`
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(content)},
		},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 1)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 1)
	assert.Equal(t, "input_text", parts[0].Type)
	assert.Equal(t, "Describe this", parts[0].Text)
}

func TestChatCompletionsToResponses_WhitespaceOnlyBase64ImageURLSkipped(t *testing.T) {
	content := `[{"type":"text","text":"Describe this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,   "}}]`
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(content)},
		},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 1)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 1)
	assert.Equal(t, "input_text", parts[0].Type)
	assert.Equal(t, "Describe this", parts[0].Text)
}

func TestChatCompletionsToResponses_FilePartFileData(t *testing.T) {
	content := `[{"type":"text","text":"Summarize the attached document"},{"type":"file","file":{"filename":"document.pdf","file_data":"data:application/pdf;base64,JVBERi0xLjQ="}}]`
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(content)},
		},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 1)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 2)
	assert.Equal(t, "input_text", parts[0].Type)
	assert.Equal(t, "Summarize the attached document", parts[0].Text)
	assert.Equal(t, "input_file", parts[1].Type)
	assert.Equal(t, "document.pdf", parts[1].Filename)
	assert.Equal(t, "data:application/pdf;base64,JVBERi0xLjQ=", parts[1].FileData)
	assert.Empty(t, parts[1].FileID)
}

func TestChatCompletionsToResponses_FilePartFileID(t *testing.T) {
	content := `[{"type":"file","file":{"file_id":"file-abc123"}}]`
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(content)},
		},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 1)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 1)
	assert.Equal(t, "input_file", parts[0].Type)
	assert.Equal(t, "file-abc123", parts[0].FileID)
	assert.Empty(t, parts[0].FileData)
}

func TestChatCompletionsToResponses_EmptyFilePartSkipped(t *testing.T) {
	// 同时缺少 file_data 和 file_id 的文件 part 没有可供 Responses 使用的内容；
	// 与空图片 URL 一样丢弃，避免空 input_file 导致上游 400。
	content := `[{"type":"text","text":"Describe this"},{"type":"file","file":{"filename":"empty.pdf"}}]`
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(content)},
		},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 1)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 1)
	assert.Equal(t, "input_text", parts[0].Type)
}

func TestChatCompletionsToResponses_EmptyContentNeverNull(t *testing.T) {
	// 回归覆盖 #2515：上游 Responses API 会拒绝 content 为 JSON null 的
	// input item。任何无法产生可用 content parts 的 chat-completions 消息，
	// 都必须把 content 序列化成字符串。
	cases := []struct {
		name    string
		content json.RawMessage
	}{
		{"null content", json.RawMessage(`null`)},
		{"empty array content", json.RawMessage(`[]`)},
		{"only empty text part", json.RawMessage(`[{"type":"text","text":""}]`)},
		{"only empty base64 image part", json.RawMessage(`[{"type":"image_url","image_url":{"url":"data:image/png;base64,"}}]`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &protocolopenai.ChatCompletionsRequest{
				Model: "gpt-5.5",
				Messages: []protocolopenai.ChatMessage{
					{Role: "user", Content: tc.content},
				},
			}
			resp, err := ChatCompletionsToResponses(req)
			require.NoError(t, err)
			assert.NotContains(t, string(resp.Input), `"content":null`,
				"converted input must not contain a null content field")

			var items []protocolopenai.ResponsesInputItem
			require.NoError(t, json.Unmarshal(resp.Input, &items))
			require.Len(t, items, 1)
			assert.Equal(t, `""`, string(items[0].Content),
				"content must be an empty string, not null")
		})
	}
}

func TestChatCompletionsToResponses_SystemArrayContent(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "system", Content: json.RawMessage(`[{"type":"text","text":"You are a careful visual assistant."}]`)},
			{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Describe this image"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc123"}}]`)},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 2)

	var systemParts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &systemParts))
	require.Len(t, systemParts, 1)
	assert.Equal(t, "input_text", systemParts[0].Type)
	assert.Equal(t, "You are a careful visual assistant.", systemParts[0].Text)

	var userParts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[1].Content, &userParts))
	require.Len(t, userParts, 2)
	assert.Equal(t, "input_image", userParts[1].Type)
	assert.Equal(t, "data:image/png;base64,abc123", userParts[1].ImageURL)
}

func TestChatCompletionsToResponses_LegacyFunctions(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
		},
		Functions: []protocolopenai.ChatFunction{
			{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		},
		FunctionCall: json.RawMessage(`{"name":"get_weather"}`),
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.Len(t, resp.Tools, 1)
	assert.Equal(t, "function", resp.Tools[0].Type)
	assert.Equal(t, "get_weather", resp.Tools[0].Name)

	// tool_choice should be converted
	require.NotNil(t, resp.ToolChoice)
	var tc map[string]any
	require.NoError(t, json.Unmarshal(resp.ToolChoice, &tc))
	assert.Equal(t, "function", tc["type"])
	assert.Equal(t, "get_weather", tc["name"])
	assert.NotContains(t, tc, "function")
}

func TestChatCompletionsToResponses_ServiceTier(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model:       "gpt-4o",
		ServiceTier: "flex",
		Messages:    []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
	}
	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	assert.Equal(t, "flex", resp.ServiceTier)
}

func TestChatCompletionsToResponses_ParallelToolCalls(t *testing.T) {
	for _, value := range []bool{false, true} {
		req := &protocolopenai.ChatCompletionsRequest{
			Model:             "gpt-4o",
			ParallelToolCalls: &value,
			Messages:          []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
		}

		resp, err := ChatCompletionsToResponses(req)
		require.NoError(t, err)
		require.NotNil(t, resp.ParallelToolCalls)
		assert.Equal(t, value, *resp.ParallelToolCalls)

		payload, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.Contains(t, string(payload), `"parallel_tool_calls":`+string(mustMarshalJSON(t, value)))
	}
}

func TestChatCompletionsToResponses_TemperatureStrippedForReasoningModel(t *testing.T) {
	temp := 0.7
	req := &protocolopenai.ChatCompletionsRequest{
		Model:       "gpt-5.2",
		Messages:    []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
		Temperature: &temp,
		TopP:        &temp,
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	assert.Nil(t, resp.Temperature, "reasoning model: temperature must be stripped")
	assert.Nil(t, resp.TopP, "reasoning model: top_p must be stripped")

	// 发给上游的序列化请求体不能包含这些字段。
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"temperature"`)
	assert.NotContains(t, string(b), `"top_p"`)
}

func TestChatCompletionsToResponses_TemperaturePreservedForNonReasoningModel(t *testing.T) {
	temp := 0.7
	req := &protocolopenai.ChatCompletionsRequest{
		Model:       "gpt-4o",
		Messages:    []protocolopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi"`)}},
		Temperature: &temp,
		TopP:        &temp,
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.NotNil(t, resp.Temperature, "non-reasoning model: temperature must be preserved")
	assert.InDelta(t, 0.7, *resp.Temperature, 1e-9)
	require.NotNil(t, resp.TopP, "non-reasoning model: top_p must be preserved")
	assert.InDelta(t, 0.7, *resp.TopP, 1e-9)
}

func TestChatCompletionsToResponses_AssistantWithTextAndToolCalls(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Do something"`)},
			{
				Role:    "assistant",
				Content: json.RawMessage(`"Let me call a function."`),
				ToolCalls: []protocolopenai.ChatToolCall{
					{
						ID:   "call_abc",
						Type: "function",
						Function: protocolopenai.ChatFunctionCall{
							Name:      "do_thing",
							Arguments: `{}`,
						},
					},
				},
			},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	// user + assistant message (with text) + function_call
	require.Len(t, items, 3)
	assert.Equal(t, "user", items[0].Role)
	assert.Equal(t, "assistant", items[1].Role)
	assert.Equal(t, "function_call", items[2].Type)
	assert.Empty(t, items[2].ID)
}

func TestChatCompletionsToResponses_AssistantArrayContentPreserved(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"A"},{"type":"text","text":"B"}]`)},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 2)
	assert.Equal(t, "assistant", items[1].Role)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[1].Content, &parts))
	require.Len(t, parts, 1)
	assert.Equal(t, "output_text", parts[0].Type)
	assert.Equal(t, "AB", parts[0].Text)
}

func TestChatCompletionsToResponses_AssistantThinkingTagPreserved(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"thinking","thinking":"internal plan"},{"type":"text","text":"final answer"}]`)},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 2)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[1].Content, &parts))
	require.Len(t, parts, 1)
	assert.Equal(t, "output_text", parts[0].Type)
	assert.Contains(t, parts[0].Text, "<thinking>internal plan</thinking>")
	assert.Contains(t, parts[0].Text, "final answer")
}

func TestChatCompletionsToResponses_AssistantReasoningContentPreserved(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
			{
				Role:             "assistant",
				ReasoningContent: "internal plan",
				Content:          json.RawMessage(`"final answer"`),
			},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 2)

	var parts []protocolopenai.ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[1].Content, &parts))
	require.Len(t, parts, 1)
	assert.Equal(t, "output_text", parts[0].Type)
	assert.Contains(t, parts[0].Text, "<thinking>internal plan</thinking>")
	assert.Contains(t, parts[0].Text, "final answer")
}

func TestChatCompletionsToResponses_ToolArrayContent(t *testing.T) {
	req := &protocolopenai.ChatCompletionsRequest{
		Model: "gpt-4o",
		Messages: []protocolopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"Use the tool"`)},
			{
				Role: "assistant",
				ToolCalls: []protocolopenai.ChatToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: protocolopenai.ChatFunctionCall{
							Name:      "inspect_image",
							Arguments: `{}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_1",
				Content: json.RawMessage(
					`[{"type":"text","text":"image width: 100"},{"type":"image_url","image_url":{"url":"data:image/png;base64,ignored"}},{"type":"text","text":"; image height: 200"}]`,
				),
			},
		},
	}

	resp, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []protocolopenai.ResponsesInputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 3)
	assert.Equal(t, "function_call_output", items[2].Type)
	assert.Equal(t, "call_1", items[2].CallID)
	assert.Equal(t, "image width: 100; image height: 200", items[2].Output)
}
