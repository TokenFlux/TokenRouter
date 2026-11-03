package bridge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResponsesToChatUsesCanonicalCacheWriteZero supplies a rejected positive
// write alias beside authoritative zero creation. Client output must agree with
// billing rather than resurrecting the alias during a second protocol conversion.
func TestResponsesToChatUsesCanonicalCacheWriteZero(t *testing.T) {
	response := &ResponsesResponse{
		ID: "resp_zero", Model: "model", Status: "completed",
		Usage: &ResponsesUsage{
			InputTokens: 100, OutputTokens: 10, CacheCreationInputTokens: 0,
			InputTokensDetails: &ResponsesInputTokensDetails{CachedTokens: 60, CacheWriteTokens: 20},
		},
	}
	chat := ResponsesToChatCompletions(testRuntime(), response, "model")
	require.Equal(t, 100, chat.Usage.PromptTokens)
	require.Equal(t, 60, chat.Usage.PromptTokensDetails.CachedTokens)
	require.Zero(t, chat.Usage.PromptTokensDetails.CacheWriteTokens)
	require.Zero(t, chat.Usage.PromptTokensDetails.CacheCreationTokens)
}

// TestResponsesToChatCompletions_BasicText converts one text output with
// usage. It checks Chat object/model, one stop choice, decoded content, and
// copied input/output plus total counts.
func TestResponsesToChatCompletions_BasicText(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_123",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type: "message",
				Content: []ResponsesContentPart{
					{Type: "output_text", Text: "Hello, world!"},
				},
			},
		},
		Usage: &ResponsesUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-4o")
	assert.Equal(t, "chat.completion", chat.Object)
	assert.Equal(t, "gpt-4o", chat.Model)
	require.Len(t, chat.Choices, 1)
	assert.Equal(t, "stop", chat.Choices[0].FinishReason)

	var content string
	require.NoError(t, json.Unmarshal(chat.Choices[0].Message.Content, &content))
	assert.Equal(t, "Hello, world!", content)

	require.NotNil(t, chat.Usage)
	assert.Equal(t, 10, chat.Usage.PromptTokens)
	assert.Equal(t, 5, chat.Usage.CompletionTokens)
	assert.Equal(t, 15, chat.Usage.TotalTokens)
}

// TestResponsesToChatCompletions_ToolCalls converts one function output
// without visible text. Its single choice must use tool_calls and retain the
// inspected correlation, function kind, name, and argument text.
func TestResponsesToChatCompletions_ToolCalls(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_456",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type:      "function_call",
				CallID:    "call_xyz",
				Name:      "get_weather",
				Arguments: `{"city":"NYC"}`,
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-4o")
	require.Len(t, chat.Choices, 1)
	assert.Equal(t, "tool_calls", chat.Choices[0].FinishReason)

	msg := chat.Choices[0].Message
	require.Len(t, msg.ToolCalls, 1)
	assert.Equal(t, "call_xyz", msg.ToolCalls[0].ID)
	assert.Equal(t, "function", msg.ToolCalls[0].Type)
	assert.Equal(t, "get_weather", msg.ToolCalls[0].Function.Name)
	assert.Equal(t, `{"city":"NYC"}`, msg.ToolCalls[0].Function.Arguments)
}

// TestResponsesToChatCompletions_Reasoning supplies reasoning summary before
// visible text. The single returned choice must expose answer text as decoded
// content and summary text in reasoning_content.
func TestResponsesToChatCompletions_Reasoning(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_789",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type: "reasoning",
				Summary: []ResponsesSummary{
					{Type: "summary_text", Text: "I thought about it."},
				},
			},
			{
				Type: "message",
				Content: []ResponsesContentPart{
					{Type: "output_text", Text: "The answer is 42."},
				},
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-4o")
	require.Len(t, chat.Choices, 1)

	var content string
	require.NoError(t, json.Unmarshal(chat.Choices[0].Message.Content, &content))
	assert.Equal(t, "The answer is 42.", content)
	assert.Equal(t, "I thought about it.", chat.Choices[0].Message.ReasoningContent)
}

// TestResponsesToChatCompletions_Incomplete supplies token-limit incomplete
// details with partial text. Conversion must return one choice whose finish
// reason is length for that status/detail fixture.
func TestResponsesToChatCompletions_Incomplete(t *testing.T) {
	resp := &ResponsesResponse{
		ID:                "resp_inc",
		Status:            "incomplete",
		IncompleteDetails: &ResponsesIncompleteDetails{Reason: "max_output_tokens"},
		Output: []ResponsesOutput{
			{
				Type: "message",
				Content: []ResponsesContentPart{
					{Type: "output_text", Text: "partial..."},
				},
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-4o")
	require.Len(t, chat.Choices, 1)
	assert.Equal(t, "length", chat.Choices[0].FinishReason)
}

// TestResponsesToChatCompletions_CachedTokens supplies cached input within
// Responses usage. The returned Chat usage must include prompt details with
// the inspected cached-token count retained.
func TestResponsesToChatCompletions_CachedTokens(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_cache",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type:    "message",
				Content: []ResponsesContentPart{{Type: "output_text", Text: "cached"}},
			},
		},
		Usage: &ResponsesUsage{
			InputTokens:  100,
			OutputTokens: 10,
			TotalTokens:  110,
			InputTokensDetails: &ResponsesInputTokensDetails{
				CachedTokens: 80,
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-4o")
	require.NotNil(t, chat.Usage)
	require.NotNil(t, chat.Usage.PromptTokensDetails)
	assert.Equal(t, 80, chat.Usage.PromptTokensDetails.CachedTokens)
}

// TestResponsesToChatCompletions_ReasoningTokens supplies reasoning detail
// within output usage. The returned completion count remains supplied output,
// and its detail object retains the reasoning-token value.
func TestResponsesToChatCompletions_ReasoningTokens(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_reasoning",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type:    "message",
				Content: []ResponsesContentPart{{Type: "output_text", Text: "ping"}},
			},
		},
		Usage: &ResponsesUsage{
			InputTokens:  24,
			OutputTokens: 33,
			TotalTokens:  57,
			OutputTokensDetails: &ResponsesOutputTokensDetails{
				ReasoningTokens: 32,
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-5.5")
	require.NotNil(t, chat.Usage)
	assert.Equal(t, 33, chat.Usage.CompletionTokens)
	require.NotNil(t, chat.Usage.CompletionTokensDetails)
	assert.Equal(t, 32, chat.Usage.CompletionTokensDetails.ReasoningTokens)
}

// TestResponsesToChatCompletions_AllTokenDetailsPassThrough supplies
// cached/audio prompt and reasoning/audio/prediction completion counts. It
// checks each copied field plus selected detail keys/counts in encoded Chat
// usage.
func TestResponsesToChatCompletions_AllTokenDetailsPassThrough(t *testing.T) {
	// Check audio and prediction token details as well as reasoning counts.
	resp := &ResponsesResponse{
		ID:     "resp_full_details",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type:    "message",
				Content: []ResponsesContentPart{{Type: "output_text", Text: "x"}},
			},
		},
		Usage: &ResponsesUsage{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
			InputTokensDetails: &ResponsesInputTokensDetails{
				CachedTokens: 60,
				AudioTokens:  4,
			},
			OutputTokensDetails: &ResponsesOutputTokensDetails{
				ReasoningTokens:          30,
				AudioTokens:              2,
				AcceptedPredictionTokens: 10,
				RejectedPredictionTokens: 3,
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-5.5")
	require.NotNil(t, chat.Usage)
	require.NotNil(t, chat.Usage.PromptTokensDetails)
	assert.Equal(t, 60, chat.Usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 4, chat.Usage.PromptTokensDetails.AudioTokens)

	require.NotNil(t, chat.Usage.CompletionTokensDetails)
	assert.Equal(t, 30, chat.Usage.CompletionTokensDetails.ReasoningTokens)
	assert.Equal(t, 2, chat.Usage.CompletionTokensDetails.AudioTokens)
	assert.Equal(t, 10, chat.Usage.CompletionTokensDetails.AcceptedPredictionTokens)
	assert.Equal(t, 3, chat.Usage.CompletionTokensDetails.RejectedPredictionTokens)

	raw, err := json.Marshal(chat.Usage)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"prompt_tokens_details"`)
	assert.Contains(t, string(raw), `"completion_tokens_details"`)
	assert.Contains(t, string(raw), `"reasoning_tokens":30`)
	assert.Contains(t, string(raw), `"accepted_prediction_tokens":10`)
}

// TestResponsesToChatCompletions_NoReasoningTokensWhenZero supplies an all-
// zero supported output detail object. The returned completion details must be
// nil, and encoded usage omits the inspected detail/reasoning keys.
func TestResponsesToChatCompletions_NoReasoningTokensWhenZero(t *testing.T) {
	// Omit completion_tokens_details when all supported counts are zero.
	resp := &ResponsesResponse{
		ID:     "resp_no_reasoning",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type:    "message",
				Content: []ResponsesContentPart{{Type: "output_text", Text: "hi"}},
			},
		},
		Usage: &ResponsesUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
			OutputTokensDetails: &ResponsesOutputTokensDetails{
				ReasoningTokens: 0,
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-4o")
	require.NotNil(t, chat.Usage)
	assert.Nil(t, chat.Usage.CompletionTokensDetails)

	raw, err := json.Marshal(chat.Usage)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "completion_tokens_details")
	assert.NotContains(t, string(raw), "reasoning_tokens")
}

// TestResponsesToChatCompletions_WebSearch combines hosted search with visible
// answer text. The single choice must use stop and retain the decoded message
// text in the inspected output.
func TestResponsesToChatCompletions_WebSearch(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_ws",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type:   "web_search_call",
				Action: &WebSearchAction{Type: "search", Query: "test"},
			},
			{
				Type:    "message",
				Content: []ResponsesContentPart{{Type: "output_text", Text: "search results"}},
			},
		},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-4o")
	require.Len(t, chat.Choices, 1)
	assert.Equal(t, "stop", chat.Choices[0].FinishReason)

	var content string
	require.NoError(t, json.Unmarshal(chat.Choices[0].Message.Content, &content))
	assert.Equal(t, "search results", content)
}

// TestResponsesEventToChatChunks_TextDelta feeds creation then visible text.
// It checks the assistant role chunk, SentRole state, and the later nonnil
// content pointer containing the supplied fragment.
func TestResponsesEventToChatChunks_TextDelta(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"

	// response.created → role chunk
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.created",
		Response: &ResponsesResponse{
			ID: "resp_stream",
		},
	}, state)
	require.Len(t, chunks, 1)
	assert.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	assert.True(t, state.SentRole)

	// response.output_text.delta → content chunk
	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:  "response.output_text.delta",
		Delta: "Hello",
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].Delta.Content)
	assert.Equal(t, "Hello", *chunks[0].Choices[0].Delta.Content)
}

// TestResponsesEventToChatChunks_ToolCallDelta registers two nonzero source
// positions and interleaves their fragments. Assertions check sequential Chat
// indexes, retained initial metadata, and fragment routing back to each
// announced call.
func TestResponsesEventToChatChunks_ToolCallDelta(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.SentRole = true

	// Place the function at output_index 1; index 0 may belong to a message.
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 1,
		Item: &ResponsesOutput{
			Type:   "function_call",
			CallID: "call_1",
			Name:   "get_weather",
		},
	}, state)
	require.Len(t, chunks, 1)
	require.Len(t, chunks[0].Choices[0].Delta.ToolCalls, 1)
	tc := chunks[0].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_1", tc.ID)
	assert.Equal(t, "get_weather", tc.Function.Name)
	require.NotNil(t, tc.Index)
	assert.Equal(t, 0, *tc.Index)

	// Resolve argument deltas using the Responses output_index.
	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 1, // matches the output_index from output_item.added above
		Delta:       `{"city":`,
	}, state)
	require.Len(t, chunks, 1)
	tc = chunks[0].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tc.Index)
	assert.Equal(t, 0, *tc.Index, "argument delta must use same index as the tool call")
	assert.Equal(t, `{"city":`, tc.Function.Arguments)

	// Add a second function call at output_index=2
	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 2,
		Item: &ResponsesOutput{
			Type:   "function_call",
			CallID: "call_2",
			Name:   "get_time",
		},
	}, state)
	require.Len(t, chunks, 1)
	tc = chunks[0].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tc.Index)
	assert.Equal(t, 1, *tc.Index, "second tool call should get index 1")

	// Argument delta for second tool call
	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 2,
		Delta:       `{"tz":"UTC"}`,
	}, state)
	require.Len(t, chunks, 1)
	tc = chunks[0].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tc.Index)
	assert.Equal(t, 1, *tc.Index, "second tool arg delta must use index 1")

	// Argument delta for first tool call (interleaved)
	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 1,
		Delta:       `"Tokyo"}`,
	}, state)
	require.Len(t, chunks, 1)
	tc = chunks[0].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tc.Index)
	assert.Equal(t, 0, *tc.Index, "first tool arg delta must still use index 0")
}

// TestResponsesEventToChatChunks_Completed requests usage on a terminal
// response with cached input. It checks finish-before-usage chunks, stop
// reason, complete counts, and the retained cached prompt detail.
func TestResponsesEventToChatChunks_Completed(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.IncludeUsage = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.completed",
		Response: &ResponsesResponse{
			Status: "completed",
			Usage: &ResponsesUsage{
				InputTokens:  50,
				OutputTokens: 20,
				TotalTokens:  70,
				InputTokensDetails: &ResponsesInputTokensDetails{
					CachedTokens: 30,
				},
			},
		},
	}, state)
	// finish chunk + usage chunk
	require.Len(t, chunks, 2)

	// First chunk: finish_reason
	require.NotNil(t, chunks[0].Choices[0].FinishReason)
	assert.Equal(t, "stop", *chunks[0].Choices[0].FinishReason)

	// Second chunk: usage
	require.NotNil(t, chunks[1].Usage)
	assert.Equal(t, 50, chunks[1].Usage.PromptTokens)
	assert.Equal(t, 20, chunks[1].Usage.CompletionTokens)
	assert.Equal(t, 70, chunks[1].Usage.TotalTokens)
	require.NotNil(t, chunks[1].Usage.PromptTokensDetails)
	assert.Equal(t, 30, chunks[1].Usage.PromptTokensDetails.CachedTokens)
}

// TestResponsesEventToChatChunks_CompletedWithReasoningTokens requests
// separate usage on a reasoning terminal. Two chunks must appear, and the
// usage chunk retains the inspected reasoning detail count.
func TestResponsesEventToChatChunks_CompletedWithReasoningTokens(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-5.5"
	state.IncludeUsage = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.completed",
		Response: &ResponsesResponse{
			Status: "completed",
			Usage: &ResponsesUsage{
				InputTokens:  24,
				OutputTokens: 33,
				TotalTokens:  57,
				OutputTokensDetails: &ResponsesOutputTokensDetails{
					ReasoningTokens: 32,
				},
			},
		},
	}, state)
	require.Len(t, chunks, 2)

	require.NotNil(t, chunks[1].Usage)
	require.NotNil(t, chunks[1].Usage.CompletionTokensDetails)
	assert.Equal(t, 32, chunks[1].Usage.CompletionTokensDetails.ReasoningTokens)
}

// TestResponsesEventToChatChunks_ResponseDone requests usage for a legacy
// completed done event. It checks stop and usage counts, then requires EOF
// finalization to return nil.
func TestResponsesEventToChatChunks_ResponseDone(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.IncludeUsage = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.done",
		Response: &ResponsesResponse{
			Status: "completed",
			Usage:  &ResponsesUsage{InputTokens: 13, OutputTokens: 7},
		},
	}, state)
	require.Len(t, chunks, 2)
	require.NotNil(t, chunks[0].Choices[0].FinishReason)
	assert.Equal(t, "stop", *chunks[0].Choices[0].FinishReason)
	require.NotNil(t, chunks[1].Usage)
	assert.Equal(t, 13, chunks[1].Usage.PromptTokens)
	assert.Equal(t, 7, chunks[1].Usage.CompletionTokens)
	assert.Nil(t, FinalizeResponsesChatStream(state))
}

// TestResponsesEventToChatChunks_TopLevelTerminalUsage omits nested usage and
// supplies event counters with cached input. The separate usage chunk must
// retain prompt/completion counts and the inspected cached detail.
func TestResponsesEventToChatChunks_TopLevelTerminalUsage(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.IncludeUsage = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.completed",
		Response: &ResponsesResponse{
			Status: "completed",
		},
		Usage: &ResponsesUsage{
			InputTokens:  21,
			OutputTokens: 9,
			InputTokensDetails: &ResponsesInputTokensDetails{
				CachedTokens: 4,
			},
		},
	}, state)

	require.Len(t, chunks, 2)
	require.NotNil(t, chunks[1].Usage)
	assert.Equal(t, 21, chunks[1].Usage.PromptTokens)
	assert.Equal(t, 9, chunks[1].Usage.CompletionTokens)
	require.NotNil(t, chunks[1].Usage.PromptTokensDetails)
	assert.Equal(t, 4, chunks[1].Usage.PromptTokensDetails.CachedTokens)
}

// TestResponsesEventToChatChunks_ResponseDoneIncomplete supplies legacy done
// with token-limit details and usage. It checks length plus usage counts and
// confirms that EOF finalization contributes no later chunks.
func TestResponsesEventToChatChunks_ResponseDoneIncomplete(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.IncludeUsage = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.done",
		Response: &ResponsesResponse{
			Status:            "incomplete",
			IncompleteDetails: &ResponsesIncompleteDetails{Reason: "max_output_tokens"},
			Usage:             &ResponsesUsage{InputTokens: 13, OutputTokens: 7},
		},
	}, state)
	require.Len(t, chunks, 2)
	require.NotNil(t, chunks[0].Choices[0].FinishReason)
	assert.Equal(t, "length", *chunks[0].Choices[0].FinishReason)
	require.NotNil(t, chunks[1].Usage)
	assert.Equal(t, 13, chunks[1].Usage.PromptTokens)
	assert.Equal(t, 7, chunks[1].Usage.CompletionTokens)
	assert.Nil(t, FinalizeResponsesChatStream(state))
}

// TestResponsesEventToChatChunks_CompletedWithToolCalls marks a tool
// observation before completed status. The single emitted finish chunk must
// have a nonnil tool_calls reason for that state.
func TestResponsesEventToChatChunks_CompletedWithToolCalls(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.SawToolCall = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.completed",
		Response: &ResponsesResponse{
			Status: "completed",
		},
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].FinishReason)
	assert.Equal(t, "tool_calls", *chunks[0].Choices[0].FinishReason)
}

// TestResponsesEventToChatChunks_ReasoningDelta supplies summary text after
// role state is set. It checks the nonnil reasoning_content pointer, retained
// fragment, and no chunks from summary completion.
func TestResponsesEventToChatChunks_ReasoningDelta(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:  "response.reasoning_summary_text.delta",
		Delta: "Thinking...",
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "Thinking...", *chunks[0].Choices[0].Delta.ReasoningContent)

	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.reasoning_summary_text.done",
	}, state)
	require.Len(t, chunks, 0)
}

// TestResponsesEventToChatChunks_ReasoningThenTextAutoCloseTag sends reasoning
// followed by answer text. Assertions inspect separate reasoning_content and
// content pointers with their supplied values; no tag-closing assertion is
// performed.
func TestResponsesEventToChatChunks_ReasoningThenTextAutoCloseTag(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:  "response.reasoning_summary_text.delta",
		Delta: "plan",
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "plan", *chunks[0].Choices[0].Delta.ReasoningContent)

	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:  "response.output_text.delta",
		Delta: "answer",
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].Delta.Content)
	assert.Equal(t, "answer", *chunks[0].Choices[0].Delta.Content)
}

// TestFinalizeResponsesChatStream finalizes constructor state with stored
// usage and no source terminal. It checks stop then usage output and nil
// chunks on the second finalization call.
func TestFinalizeResponsesChatStream(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.IncludeUsage = true
	state.Usage = &ChatUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	chunks := FinalizeResponsesChatStream(state)
	require.Len(t, chunks, 2)

	// Finish chunk
	require.NotNil(t, chunks[0].Choices[0].FinishReason)
	assert.Equal(t, "stop", *chunks[0].Choices[0].FinishReason)

	// Usage chunk
	require.NotNil(t, chunks[1].Usage)
	assert.Equal(t, 100, chunks[1].Usage.PromptTokens)

	// Idempotent: second call returns nil
	assert.Nil(t, FinalizeResponsesChatStream(state))
}

// TestFinalizeResponsesChatStream_AfterCompleted supplies terminal usage
// before EOF finalization. Terminal conversion must produce chunks, and the
// later finalizer must return nil for that already-finalized state.
func TestFinalizeResponsesChatStream_AfterCompleted(t *testing.T) {
	// If response.completed already emitted the finish chunk,
	// FinalizeResponsesChatStream must be a no-op (prevents double finish_reason
	// being sent to the client).
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.IncludeUsage = true

	// Simulate response.completed
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.completed",
		Response: &ResponsesResponse{
			Status: "completed",
			Usage: &ResponsesUsage{
				InputTokens:  10,
				OutputTokens: 5,
				TotalTokens:  15,
			},
		},
	}, state)
	require.NotEmpty(t, chunks) // finish + usage chunks

	assert.Nil(t, FinalizeResponsesChatStream(state))
}

// TestChatChunkToSSE serializes an assistant-role chunk with fixed metadata.
// The returned frame must contain the data prefix, response identity, and role
// text, and exceed the asserted length.
func TestChatChunkToSSE(t *testing.T) {
	chunk := ChatCompletionsChunk{
		ID:      "chatcmpl-test",
		Object:  "chat.completion.chunk",
		Created: 1700000000,
		Model:   "gpt-4o",
		Choices: []ChatChunkChoice{
			{
				Index:        0,
				Delta:        ChatDelta{Role: "assistant"},
				FinishReason: nil,
			},
		},
	}

	sse, err := ChatChunkToSSE(chunk)
	require.NoError(t, err)
	assert.Contains(t, sse, "data: ")
	assert.Contains(t, sse, "chatcmpl-test")
	assert.Contains(t, sse, "assistant")
	assert.True(t, len(sse) > 10)
}

// TestChatCompletionsStreamRoundTrip feeds creation, four text fragments, and
// terminal usage. It checks seven ordered chunks, joined answer text, stop,
// usage counts, and one shared response ID.
func TestChatCompletionsStreamRoundTrip(t *testing.T) {
	// Exercise conversion of a complete Responses SSE sequence into Chat chunks.

	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-4o"
	state.IncludeUsage = true

	var allChunks []ChatCompletionsChunk

	// 1. response.created
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:     "response.created",
		Response: &ResponsesResponse{ID: "resp_rt"},
	}, state)
	allChunks = append(allChunks, chunks...)

	// 2. text deltas
	for _, text := range []string{"Hello", ", ", "world", "!"} {
		chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
			Type:  "response.output_text.delta",
			Delta: text,
		}, state)
		allChunks = append(allChunks, chunks...)
	}

	// 3. response.completed
	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.completed",
		Response: &ResponsesResponse{
			Status: "completed",
			Usage: &ResponsesUsage{
				InputTokens:  10,
				OutputTokens: 4,
				TotalTokens:  14,
			},
		},
	}, state)
	allChunks = append(allChunks, chunks...)

	// Verify: role chunk + 4 text chunks + finish chunk + usage chunk = 7
	require.Len(t, allChunks, 7)

	// First chunk has role
	assert.Equal(t, "assistant", allChunks[0].Choices[0].Delta.Role)

	// Text chunks
	var fullText string
	for i := 1; i <= 4; i++ {
		require.NotNil(t, allChunks[i].Choices[0].Delta.Content)
		fullText += *allChunks[i].Choices[0].Delta.Content
	}
	assert.Equal(t, "Hello, world!", fullText)

	// Finish chunk
	require.NotNil(t, allChunks[5].Choices[0].FinishReason)
	assert.Equal(t, "stop", *allChunks[5].Choices[0].FinishReason)

	// Usage chunk
	require.NotNil(t, allChunks[6].Usage)
	assert.Equal(t, 10, allChunks[6].Usage.PromptTokens)
	assert.Equal(t, 4, allChunks[6].Usage.CompletionTokens)

	// All chunks share the same ID
	for _, c := range allChunks {
		assert.Equal(t, "resp_rt", c.ID)
	}
}

// TestBufferedResponseAccumulator_TextOnly appends two visible text fragments.
// HasContent must become true, and reconstruction must produce one assistant
// message with one output-text part containing their concatenation.
func TestBufferedResponseAccumulator_TextOnly(t *testing.T) {
	acc := NewBufferedResponseAccumulator()

	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "Hello"})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: ", world!"})

	assert.True(t, acc.HasContent())

	output := acc.BuildOutput()
	require.Len(t, output, 1)
	assert.Equal(t, "message", output[0].Type)
	assert.Equal(t, "assistant", output[0].Role)
	require.Len(t, output[0].Content, 1)
	assert.Equal(t, "output_text", output[0].Content[0].Type)
	assert.Equal(t, "Hello, world!", output[0].Content[0].Text)
}

// TestBufferedResponseAccumulator_ToolCalls announces one function and
// supplies two argument fragments. It checks content presence and one
// reconstructed call retaining correlation/name and complete concatenated
// argument text.
func TestBufferedResponseAccumulator_ToolCalls(t *testing.T) {
	acc := NewBufferedResponseAccumulator()

	// Add function call at output_index=1
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 1,
		Item: &ResponsesOutput{
			Type:   "function_call",
			CallID: "call_abc",
			Name:   "get_weather",
		},
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 1,
		Delta:       `{"city":`,
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 1,
		Delta:       `"NYC"}`,
	})

	assert.True(t, acc.HasContent())

	output := acc.BuildOutput()
	require.Len(t, output, 1)
	assert.Equal(t, "function_call", output[0].Type)
	assert.Equal(t, "call_abc", output[0].CallID)
	assert.Equal(t, "get_weather", output[0].Name)
	assert.Equal(t, `{"city":"NYC"}`, output[0].Arguments)
}

// TestBufferedResponseAccumulator_Reasoning appends two reasoning-summary
// fragments. Reconstruction must return one reasoning item with one summary-
// text member containing the complete concatenation, after HasContent becomes
// true.
func TestBufferedResponseAccumulator_Reasoning(t *testing.T) {
	acc := NewBufferedResponseAccumulator()

	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.reasoning_summary_text.delta", Delta: "Step 1: "})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.reasoning_summary_text.delta", Delta: "think about it"})

	assert.True(t, acc.HasContent())

	output := acc.BuildOutput()
	require.Len(t, output, 1)
	assert.Equal(t, "reasoning", output[0].Type)
	require.Len(t, output[0].Summary, 1)
	assert.Equal(t, "summary_text", output[0].Summary[0].Type)
	assert.Equal(t, "Step 1: think about it", output[0].Summary[0].Text)
}

// TestBufferedResponseAccumulator_Mixed supplies reasoning, visible text, then
// one call with arguments. It checks three ordered reconstructed kinds,
// retained message text, and the selected function name.
func TestBufferedResponseAccumulator_Mixed(t *testing.T) {
	acc := NewBufferedResponseAccumulator()

	// Reasoning first
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.reasoning_summary_text.delta", Delta: "I thought about it."})

	// Then text
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "The answer is 42."})

	// Then a tool call
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 2,
		Item: &ResponsesOutput{
			Type:   "function_call",
			CallID: "call_1",
			Name:   "verify",
		},
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: 2,
		Delta:       `{}`,
	})

	assert.True(t, acc.HasContent())

	output := acc.BuildOutput()
	// Order: reasoning → message → function_calls
	require.Len(t, output, 3)
	assert.Equal(t, "reasoning", output[0].Type)
	assert.Equal(t, "message", output[1].Type)
	assert.Equal(t, "function_call", output[2].Type)
	assert.Equal(t, "The answer is 42.", output[1].Content[0].Text)
	assert.Equal(t, "verify", output[2].Name)
}

// TestBufferedResponseAccumulator_SupplementEmptyOutput supplies visible
// deltas beside a terminal response with nil output. Supplementation must
// create the inspected message/text while retaining the asserted input usage
// count.
func TestBufferedResponseAccumulator_SupplementEmptyOutput(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "Hello"})

	resp := &ResponsesResponse{
		ID:     "resp_1",
		Status: "completed",
		Output: nil, // empty output
		Usage:  &ResponsesUsage{InputTokens: 10, OutputTokens: 5},
	}

	acc.SupplementResponseOutput(resp)

	require.Len(t, resp.Output, 1)
	assert.Equal(t, "message", resp.Output[0].Type)
	assert.Equal(t, "Hello", resp.Output[0].Content[0].Text)
	// Usage should be untouched
	assert.Equal(t, 10, resp.Usage.InputTokens)
}

// TestBufferedResponseAccumulator_NoSupplementWhenOutputExists supplies delta
// text beside populated terminal output. The terminal's single message and
// text must retain precedence after the supplement helper is called.
func TestBufferedResponseAccumulator_NoSupplementWhenOutputExists(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "from deltas"})

	resp := &ResponsesResponse{
		ID:     "resp_2",
		Status: "completed",
		Output: []ResponsesOutput{
			{
				Type: "message",
				Content: []ResponsesContentPart{
					{Type: "output_text", Text: "from terminal event"},
				},
			},
		},
	}

	acc.SupplementResponseOutput(resp)

	// Output should NOT be overwritten
	require.Len(t, resp.Output, 1)
	assert.Equal(t, "from terminal event", resp.Output[0].Content[0].Text)
}

// TestBufferedResponseAccumulator_EmptyDeltas supplies empty text and an
// unrelated creation event. HasContent remains false, and supplementing an
// empty terminal leaves its output slice nil.
func TestBufferedResponseAccumulator_EmptyDeltas(t *testing.T) {
	acc := NewBufferedResponseAccumulator()

	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: ""})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.created"})

	assert.False(t, acc.HasContent())

	resp := &ResponsesResponse{ID: "resp_3", Status: "completed"}
	acc.SupplementResponseOutput(resp)
	assert.Nil(t, resp.Output)
}

// TestBufferedResponseAccumulator_IgnoresNonFunctionCallItems announces a
// message item without text deltas. HasContent must remain false, protecting
// the no-recorded-content result for that unsupported addition kind.
func TestBufferedResponseAccumulator_IgnoresNonFunctionCallItems(t *testing.T) {
	acc := NewBufferedResponseAccumulator()

	// output_item.added with type "message" should be ignored
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "message"},
	})

	assert.False(t, acc.HasContent())
}

// TestResponsesEventToChatChunks_CustomToolCallInputDelta opens a custom call
// at source position one. It checks initial call identity/name and raw input
// text routed to Chat tool index zero.
func TestResponsesEventToChatChunks_CustomToolCallInputDelta(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-5-codex"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 1,
		Item: &ResponsesOutput{
			Type:   "custom_tool_call",
			CallID: "call_patch",
			Name:   "apply_patch",
		},
	}, state)
	require.Len(t, chunks, 1)
	require.Len(t, chunks[0].Choices[0].Delta.ToolCalls, 1)
	tc := chunks[0].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_patch", tc.ID)
	assert.Equal(t, "apply_patch", tc.Function.Name)

	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.custom_tool_call_input.delta",
		OutputIndex: 1,
		Delta:       "*** Begin Patch",
	}, state)
	require.Len(t, chunks, 1)
	tc = chunks[0].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tc.Index)
	assert.Equal(t, 0, *tc.Index)
	assert.Equal(t, "*** Begin Patch", tc.Function.Arguments)
}

// TestResponsesEventToChatChunks_ReasoningTextDelta supplies raw reasoning
// text with role state already set. One chunk must carry a nonnil
// reasoning_content pointer containing that exact fragment.
func TestResponsesEventToChatChunks_ReasoningTextDelta(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.Model = "gpt-5-codex"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:  "response.reasoning_text.delta",
		Delta: "thinking step",
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "thinking step", *chunks[0].Choices[0].Delta.ReasoningContent)
}

// TestBufferedResponseAccumulator_CodexEvents supplies a custom addition/input
// fragment and raw reasoning text. The assertion requires HasContent to be
// true; reconstruction fields are not checked in this case.
func TestBufferedResponseAccumulator_CodexEvents(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "custom_tool_call", CallID: "c1", Name: "apply_patch"},
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.custom_tool_call_input.delta",
		OutputIndex: 0,
		Delta:       "patch-body",
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:  "response.reasoning_text.delta",
		Delta: "raw-reasoning",
	})
	require.True(t, acc.HasContent())
}

// TestResponsesEventToChatChunks_ArgumentsDeltaOmitsEmptyName serializes an
// added call followed by two argument chunks. Initial JSON retains the name,
// while each later payload contains arguments and omits the inspected name
// key.
func TestResponsesEventToChatChunks_ArgumentsDeltaOmitsEmptyName(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())

	added := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item: &ResponsesOutput{
			Type:   "function_call",
			CallID: "call_a",
			Name:   "exec",
		},
	}, state)
	require.Len(t, added, 1)
	first, err := json.Marshal(added[0])
	require.NoError(t, err)
	require.Contains(t, string(first), `"name":"exec"`)

	for _, fragment := range []string{`{"cmd":"ls"}`, `{"cmd":"ls","flags":"-la"}`} {
		deltas := ResponsesEventToChatChunks(&ResponsesStreamEvent{
			Type:        "response.function_call_arguments.delta",
			OutputIndex: 0,
			Delta:       fragment,
		}, state)
		require.Len(t, deltas, 1)
		raw, err := json.Marshal(deltas[0])
		require.NoError(t, err)
		require.NotContains(t, string(raw), `"name"`, "arguments delta must not carry a name field")
		require.Contains(t, string(raw), `"arguments"`)
	}
}

// TestResponsesToChatCompletions_PreservesUpstreamServiceTier converts a
// priority reply with visible text. The returned tier must be priority and
// encoded JSON must contain that service_tier member.
func TestResponsesToChatCompletions_PreservesUpstreamServiceTier(t *testing.T) {
	resp := &ResponsesResponse{
		ID:          "resp_1",
		Object:      "response",
		Model:       "gpt-5.5",
		Status:      "completed",
		ServiceTier: "priority",
		Output: []ResponsesOutput{{
			Type: "message",
			Role: "assistant",
			Content: []ResponsesContentPart{{
				Type: "output_text",
				Text: "hi",
			}},
		}},
		Usage: &ResponsesUsage{InputTokens: 1, OutputTokens: 1},
	}

	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-5.5")
	require.Equal(t, "priority", chat.ServiceTier)

	raw, err := json.Marshal(chat)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"service_tier":"priority"`)
}

// TestResponsesToChatCompletions_OmitsMissingServiceTier converts a reply with
// no tier value. The returned field stays empty, and encoded Chat JSON must
// omit the service_tier key.
func TestResponsesToChatCompletions_OmitsMissingServiceTier(t *testing.T) {
	resp := &ResponsesResponse{ID: "resp_1", Model: "gpt-5.5", Status: "completed"}
	chat := ResponsesToChatCompletions(testRuntime(), resp, "gpt-5.5")
	require.Empty(t, chat.ServiceTier)
	raw, err := json.Marshal(chat)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "service_tier")
}

// TestResponsesEventToChatChunks_PreservesUpstreamServiceTier observes
// priority at creation before text and completion without another tier. Every
// inspected role/content/finish/usage chunk must retain that observed value.
func TestResponsesEventToChatChunks_PreservesUpstreamServiceTier(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.IncludeUsage = true

	created := &ResponsesStreamEvent{Type: "response.created"}
	require.NoError(t, json.Unmarshal([]byte(`{"type":"response.created","response":{"id":"resp_s1","model":"gpt-5.5","service_tier":"priority","status":"in_progress"}}`), created))

	chunks := ResponsesEventToChatChunks(created, state)
	require.NotEmpty(t, chunks)
	for _, chunk := range chunks {
		require.Equal(t, "priority", chunk.ServiceTier)
	}

	// Subsequent deltas retain the observed service tier.
	delta := &ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "hi"}
	chunks = ResponsesEventToChatChunks(delta, state)
	require.NotEmpty(t, chunks)
	require.Equal(t, "priority", chunks[0].ServiceTier)

	// Completion and usage chunks retain it too.
	completed := &ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{
		ID: "resp", Model: "gpt-5.5", Status: "completed",
		Usage: &ResponsesUsage{InputTokens: 1, OutputTokens: 1},
	}}
	chunks = ResponsesEventToChatChunks(completed, state)
	require.NotEmpty(t, chunks)
	for _, chunk := range chunks {
		require.Equal(t, "priority", chunk.ServiceTier)
	}
}

// TestResponsesEventToChatChunks_NoServiceTierStaysClean creates a stream
// without tier metadata. The first produced chunk keeps an empty typed tier
// and omits service_tier from its encoded JSON.
func TestResponsesEventToChatChunks_NoServiceTierStaysClean(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	created := &ResponsesStreamEvent{Type: "response.created", Response: &ResponsesResponse{ID: "resp", Model: "gpt-5.5"}}
	chunks := ResponsesEventToChatChunks(created, state)
	require.NotEmpty(t, chunks)
	require.Empty(t, chunks[0].ServiceTier)
	raw, err := json.Marshal(chunks[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "service_tier")
}

// TestResponsesToChatCompletions_ContentFilter supplies incomplete status with
// content_filter details and partial text. The single buffered choice must use
// the corresponding content_filter finish reason.
func TestResponsesToChatCompletions_ContentFilter(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_cf",
		Status: "incomplete",
		IncompleteDetails: &ResponsesIncompleteDetails{
			Reason: "content_filter",
		},
		Output: []ResponsesOutput{{
			Type:    "message",
			Content: []ResponsesContentPart{{Type: "output_text", Text: "partial"}},
		}},
		Usage: &ResponsesUsage{InputTokens: 10, OutputTokens: 5},
	}

	cc := ResponsesToChatCompletions(testRuntime(), resp, "gpt-5.5")
	require.Len(t, cc.Choices, 1)
	assert.Equal(t, "content_filter", cc.Choices[0].FinishReason)
}

// TestResponsesToChatCompletionsStreaming_ContentFilter supplies the content-
// filter incomplete terminal to started role state. At least one returned
// choice must carry the inspected content_filter finish reason.
func TestResponsesToChatCompletionsStreaming_ContentFilter(t *testing.T) {
	state := NewResponsesEventToChatState(testRuntime())
	state.ID = "resp_cf"
	state.Model = "gpt-5.5"
	state.SentRole = true

	// Content filtering terminates Responses streams with response.incomplete.
	events := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type: "response.incomplete",
		Response: &ResponsesResponse{
			ID:     "resp_cf",
			Status: "incomplete",
			IncompleteDetails: &ResponsesIncompleteDetails{
				Reason: "content_filter",
			},
		},
	}, state)

	hasContentFilter := false
	for _, chunk := range events {
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason == "content_filter" {
				hasContentFilter = true
			}
		}
	}
	assert.True(t, hasContentFilter, "streaming content_filter should map to finish_reason content_filter")
}
