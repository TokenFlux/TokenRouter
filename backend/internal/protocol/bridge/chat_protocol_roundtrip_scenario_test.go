package bridge

// These scenarios cover reasoning, usage, and tool streams across
// chatcompletions_anthropic_bridge.go, chatcompletions_responses_bridge.go,
// anthropic_to_responses_response.go, responses_to_anthropic.go, and
// responses_to_chatcompletions.go.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheUsageRoundTripsAcrossTextProtocols decodes equivalent vendor usage
// representations before converting through Messages, Responses, and Chat.
// Serialized results must retain disjoint cache buckets and inclusive totals
// without multiplying cache writes during a multi-step conversion.
func TestCacheUsageRoundTripsAcrossTextProtocols(t *testing.T) {
	for _, source := range []string{"chat", "responses", "messages"} {
		t.Run(source, func(t *testing.T) {
			var responses *ResponsesResponse
			switch source {
			case "chat":
				var chat ChatCompletionsResponse
				require.NoError(t, json.Unmarshal([]byte(`{"id":"chat_cache","model":"model","choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"cache_read_input_tokens":60,"cache_write_tokens":20}}`), &chat))
				messages := ChatCompletionsResponseToAnthropic(testRuntime(), &chat, "model")
				responses = AnthropicToResponsesResponse(testRuntime(), messages)
			case "responses":
				var response ResponsesResponse
				require.NoError(t, json.Unmarshal([]byte(`{"id":"resp_cache","model":"model","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10,"cached_tokens":60,"cache_creation_input_tokens":20}}`), &response))
				messages := ResponsesToAnthropic(&response, "model")
				responses = AnthropicToResponsesResponse(testRuntime(), messages)
			case "messages":
				var messages AnthropicResponse
				require.NoError(t, json.Unmarshal([]byte(`{"id":"msg_cache","model":"model","content":[],"stop_reason":"end_turn","usage":{"prompt_tokens":100,"cached_tokens":60,"output_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":8}}}`), &messages))
				responses = AnthropicToResponsesResponse(testRuntime(), &messages)
			}
			chat := ResponsesToChatCompletions(testRuntime(), responses, "model")
			encoded, err := json.Marshal(chat)
			require.NoError(t, err)
			var decoded ChatCompletionsResponse
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			require.Equal(t, 100, decoded.Usage.PromptTokens)
			require.Equal(t, 110, decoded.Usage.TotalTokens)
			require.Equal(t, 60, decoded.Usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 20, decoded.Usage.PromptTokensDetails.CacheCreationTokens)
			final := ChatCompletionsResponseToAnthropic(testRuntime(), &decoded, "model")
			require.Equal(t, 20, final.Usage.InputTokens)
			require.Equal(t, 60, final.Usage.CacheReadInputTokens)
			require.Equal(t, 20, final.Usage.CacheCreationInputTokens)
			require.Equal(t, 10, final.Usage.OutputTokens)
		})
	}
}

// TestChatReasoningAliasNonStreaming decodes the Chat reasoning alias beside
// final text. Each response bridge returns two entries and places the
// asserted thinking/reasoning kind and selected alias text in its first entry.
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

// TestChatReasoningAliasStreaming compares alias-only, preferred
// reasoning_content, and explicitly empty preferred-field chunks. Both stream
// bridges must accumulate exactly the expected reasoning text for each
// fixture.
func TestChatReasoningAliasStreaming(t *testing.T) {
	tests := []struct {
		name string // reasoning alias fixture label
		// payload and want pair one decoded chunk with the reasoning accumulated
		// by both bridges. Presence of an empty preferred field suppresses alias text.
		payload string // supplied Chat chunk JSON
		want    string // expected accumulated reasoning text
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

// TestUsageConversionsPreserveCacheWriteTokens decodes detailed Responses
// cache usage, converts it to Chat, and converts it back. Assertions check
// cached/write counts and both returned Responses write-token locations.
func TestUsageConversionsPreserveCacheWriteTokens(t *testing.T) {
	var responsesUsage ResponsesUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":1000,
		"output_tokens":50,
		"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":200}
	}`), &responsesUsage))
	require.NotNil(t, responsesUsage.InputTokensDetails)
	require.Equal(t, 200, responsesUsage.InputTokensDetails.CacheWriteTokens)

	chatUsage := chatUsageFromResponsesUsage(&responsesUsage)
	require.NotNil(t, chatUsage.PromptTokensDetails)
	require.Equal(t, 100, chatUsage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 200, chatUsage.PromptTokensDetails.CacheWriteTokens)

	roundTrip := ChatUsageToResponsesUsage(chatUsage)
	require.NotNil(t, roundTrip.InputTokensDetails)
	require.Equal(t, 200, roundTrip.CacheCreationInputTokens)
	require.Equal(t, 200, roundTrip.InputTokensDetails.CacheWriteTokens)
}

// TestStreamingParallelToolUseNoGhostDelta converts two Chat tool calls
// through Responses into Messages events and finalizes the stream. It checks
// two opened tool blocks, membership of delta/stop indexes among opened
// blocks, and the tool-use stop reason.
func TestStreamingParallelToolUseNoGhostDelta(t *testing.T) {
	ccState := NewChatCompletionsToResponsesStreamState(testRuntime(), "glm-5.2")
	anthropicState := NewResponsesEventToAnthropicState(testRuntime())
	anthropicState.Model = "glm-5.2"

	// The first chunk carries the first call's ID, name, and arguments.
	chatChunk1 := &ChatCompletionsChunk{
		ID:    "chatcmpl-1",
		Model: "glm-5.2",
		Choices: []ChatChunkChoice{{
			Index: 0,
			Delta: ChatDelta{
				ToolCalls: []ChatToolCall{{
					Index: intPtr(0),
					ID:    "call_weather",
					Type:  "function",
					Function: ChatFunctionCall{
						Name:      "get_weather",
						Arguments: `{"city":"Tokyo"}`,
					},
				}},
			},
		}},
	}

	// The second chunk carries the second call's ID, name, and arguments.
	chatChunk2 := &ChatCompletionsChunk{
		ID:    "chatcmpl-1",
		Model: "glm-5.2",
		Choices: []ChatChunkChoice{{
			Index: 0,
			Delta: ChatDelta{
				ToolCalls: []ChatToolCall{{
					Index: intPtr(1),
					ID:    "call_time",
					Type:  "function",
					Function: ChatFunctionCall{
						Name:      "get_time",
						Arguments: `{}`,
					},
				}},
			},
		}},
	}

	// The third chunk finishes tool calls.
	chatChunk3 := &ChatCompletionsChunk{
		ID:    "chatcmpl-1",
		Model: "glm-5.2",
		Choices: []ChatChunkChoice{{
			Index:        0,
			Delta:        ChatDelta{},
			FinishReason: strPtr("tool_calls"),
		}},
	}

	// Convert Chat chunks to Responses events, then to Anthropic events.
	var allAnthropicEvents []AnthropicStreamEvent
	for _, chunk := range []*ChatCompletionsChunk{chatChunk1, chatChunk2, chatChunk3} {
		responsesEvents := ChatCompletionsChunkToResponsesEvents(testRuntime(), chunk, ccState)
		for _, rEvent := range responsesEvents {
			allAnthropicEvents = append(allAnthropicEvents, ResponsesEventToAnthropicEvents(&rEvent, anthropicState)...)
		}
	}

	// Finish both intermediate tool lifecycles before checking block references.
	// This exposes done-event handling when each argument body already arrived in
	// a single delta.
	finalResponsesEvents := FinalizeChatCompletionsResponsesStream(testRuntime(), ccState)
	for _, rEvent := range finalResponsesEvents {
		allAnthropicEvents = append(allAnthropicEvents, ResponsesEventToAnthropicEvents(&rEvent, anthropicState)...)
	}

	// Record blocks announced by content_block_start.
	startedBlocks := make(map[int]string) // block index to type
	for _, e := range allAnthropicEvents {
		if e.Type == "content_block_start" && e.ContentBlock != nil {
			startedBlocks[*e.Index] = e.ContentBlock.Type
		}
	}

	// Every delta index must occur among the collected block starts.
	for _, e := range allAnthropicEvents {
		if e.Type != "content_block_delta" || e.Index == nil {
			continue
		}
		idx := *e.Index
		_, ok := startedBlocks[idx]
		require.Truef(t, ok,
			"content_block_delta on index %d which was never content_block_start'ed (ghost delta bug #4193)", idx)
	}

	// Every stop index must occur among the collected block starts.
	for _, e := range allAnthropicEvents {
		if e.Type != "content_block_stop" || e.Index == nil {
			continue
		}
		idx := *e.Index
		_, ok := startedBlocks[idx]
		require.Truef(t, ok,
			"content_block_stop on index %d which was never content_block_start'ed", idx)
	}

	// Both parallel tool_use blocks must be opened.
	var toolUseBlocks []int
	for idx, blockType := range startedBlocks {
		if blockType == "tool_use" {
			toolUseBlocks = append(toolUseBlocks, idx)
		}
	}
	assert.Len(t, toolUseBlocks, 2, "both parallel tool_use blocks should be opened")

	// The final stop reason must indicate tool use.
	var sawMessageDelta bool
	for _, e := range allAnthropicEvents {
		if e.Type == "message_delta" {
			sawMessageDelta = true
			assert.Equal(t, "tool_use", e.Delta.StopReason)
		}
	}
	assert.True(t, sawMessageDelta, "message_delta should be emitted")
}

// intPtr allocates an integer value for optional stream fixture indexes.
func intPtr(v int) *int { return &v }
