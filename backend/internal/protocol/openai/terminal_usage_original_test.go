//go:build unit

package openai_test

import (
	"strings"
	"testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEffectiveOpenAISSEEventTypePrefersPayload(t *testing.T) {
	t.Parallel()

	require.Equal(t, "response.failed", protocolopenai.EffectiveOpenAISSEEventType([]byte(`{"type":"response.failed"}`), "error"))
	require.Equal(t, "error", protocolopenai.EffectiveOpenAISSEEventType([]byte(`{"error":{"message":"failed"}}`), " error "))
	require.JSONEq(t, `{"type":"error","error":{"message":"failed"}}`, protocolopenai.OpenAICompatPayloadWithEventType(`{"type":"","error":{"message":"failed"}}`, "error"))
}

func TestExtractOpenAISSETerminalEventUsesFinalAuthoritativeTerminal(t *testing.T) {
	t.Parallel()

	body := "data: {\"type\":\"error\",\"error\":{\"message\":\"recovering\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"
	eventType, payload, ok := protocolopenai.ExtractOpenAISSETerminalEvent(body)
	require.True(t, ok)
	require.Equal(t, "response.completed", eventType)
	require.Equal(t, "resp_1", gjson.GetBytes(payload, "response.id").String())
}

func TestParseSSEUsageEffectiveTerminalRules(t *testing.T) {
	t.Parallel()

	usage := &protocolopenai.ForwardUsage{}
	protocolopenai.ParseSSEUsageBytesWithType([]byte(`{"usage":{"input_tokens":17,"output_tokens":5,"input_tokens_details":{"cached_tokens":3}}}`), "response.in_progress", usage)
	protocolopenai.ParseSSEUsageBytesWithType([]byte(`{"response":{"id":"resp_1"}}`), "response.completed", usage)
	require.Equal(t, protocolopenai.ForwardUsage{InputTokens: 17, OutputTokens: 5, CacheReadInputTokens: 3}, *usage)

	protocolopenai.ParseSSEUsageBytesWithType([]byte(`{"response":{"usage":{"input_tokens":0,"output_tokens":0,"input_tokens_details":{"cached_tokens":0}}}}`), "response.completed", usage)
	require.Equal(t, protocolopenai.ForwardUsage{InputTokens: 17, OutputTokens: 5, CacheReadInputTokens: 3}, *usage)

	protocolopenai.ParseSSEUsageBytesWithType([]byte(`{"response":{"usage":{"input_tokens":2,"output_tokens":0,"input_tokens_details":{"cached_tokens":0}}}}`), "response.completed", usage)
	require.Equal(t, protocolopenai.ForwardUsage{InputTokens: 2}, *usage)
}

func BenchmarkParseSSEUsageNoUsageDelta(b *testing.B) {
	usage := &protocolopenai.ForwardUsage{}
	payload := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		protocolopenai.ParseSSEUsageBytesWithType(payload, "response.output_text.delta", usage)
	}
}

func TestForEachOpenAISSEFrameDataTypeOverridesEventField(t *testing.T) {
	t.Parallel()

	var types []string
	protocolopenai.ForEachOpenAISSEFrame(strings.Join([]string{
		"event: response.in_progress",
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
		"",
	}, "\n"), func(eventType string, _ []byte) {
		types = append(types, eventType)
	})
	require.Equal(t, []string{"response.completed"}, types)
}
