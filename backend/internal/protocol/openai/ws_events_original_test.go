package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOpenAIWSEventEnvelope(t *testing.T) {
	eventType, responseID, response := ParseWSEventEnvelope([]byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.1"}}`))
	require.Equal(t, "response.completed", eventType)
	require.Equal(t, "resp_1", responseID)
	require.True(t, response.Exists())
	require.Equal(t, `{"id":"resp_1","model":"gpt-5.1"}`, response.Raw)

	eventType, responseID, response = ParseWSEventEnvelope([]byte(`{"type":"response.delta","id":"evt_1"}`))
	require.Equal(t, "response.delta", eventType)
	require.Equal(t, "evt_1", responseID)
	require.False(t, response.Exists())
}

func TestParseOpenAIWSResponseUsageFromCompletedEvent(t *testing.T) {
	usage := &ForwardUsage{}
	ParseWSResponseUsageFromCompletedEvent(
		[]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":7,"input_tokens_details":{"cached_tokens":3}}}}`),
		usage,
	)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Equal(t, 3, usage.CacheReadInputTokens)
	ParseWSResponseUsageFromCompletedEvent(
		[]byte(`{"type":"response.completed","response":{"usage":{"prompt_tokens":19,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}}}`),
		usage,
	)
	require.Equal(t, 19, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	ParseWSResponseUsageFromCompletedEvent(
		[]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":0,"output_tokens":0,"input_tokens_details":{"cached_tokens":0}}}}`),
		usage,
	)
	require.Equal(t, ForwardUsage{InputTokens: 19, OutputTokens: 5, CacheReadInputTokens: 4}, *usage)
	ParseWSResponseUsageFromCompletedEvent(
		[]byte(`{"type":"response.failed","response":{"usage":{"input_tokens":3,"output_tokens":0,"input_tokens_details":{"cached_tokens":0}}}}`),
		usage,
	)
	require.Equal(t, ForwardUsage{InputTokens: 3}, *usage)
}

func TestOpenAIWSEventShouldParseUsageTerminalEvents(t *testing.T) {
	t.Parallel()

	for _, eventType := range []string{
		"response.completed",
		"response.done",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled",
	} {
		require.True(t, WSEventShouldParseUsage(eventType), eventType)
		require.True(t, WSEventShouldParseUsage("  "+eventType+"  "), eventType)
	}
	require.False(t, WSEventShouldParseUsage("response.output_text.delta"))
	require.True(t, WSEventShouldParseUsage("response.output_text.done"))
	require.False(t, WSEventShouldParseUsage(""))
	require.False(t, WSMessageShouldParseUsage("response.in_progress", []byte(`{"type":"response.in_progress"}`)))
	require.True(t, WSMessageShouldParseUsage("response.in_progress", []byte(`{"type":"response.in_progress","usage":{}}`)))
	require.False(t, WSMessageShouldParseUsage("response.output_text.delta", []byte(`{"type":"response.output_text.delta","usage":{}}`)))
}

func TestOpenAIWSMessageLikelyContainsToolCalls(t *testing.T) {
	require.False(t, WSMessageLikelyContainsToolCalls([]byte(`{"type":"response.output_text.delta","delta":"hello"}`)))
	require.True(t, WSMessageLikelyContainsToolCalls([]byte(`{"type":"response.output_item.added","item":{"tool_calls":[{"id":"tc1"}]}}`)))
	require.True(t, WSMessageLikelyContainsToolCalls([]byte(`{"type":"response.output_item.added","item":{"type":"function_call"}}`)))
}
