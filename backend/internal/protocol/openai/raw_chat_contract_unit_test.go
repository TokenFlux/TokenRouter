//go:build unit

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIRawStreamTerminalState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		payloads       []string
		clientStarted  bool
		wantTerminated bool
		wantTruncated  bool
	}{
		{
			name:           "done sentinel",
			payloads:       []string{`{"choices":[{"delta":{"content":"a"}}]}`, "[DONE]"},
			clientStarted:  true,
			wantTerminated: true,
		},
		{
			name:           "usage chunk",
			payloads:       []string{`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`},
			clientStarted:  true,
			wantTerminated: true,
		},
		{
			name:           "finish reason",
			payloads:       []string{`{"choices":[{"delta":{},"finish_reason":"length"}]}`},
			clientStarted:  true,
			wantTerminated: true,
		},
		{
			name:          "null finish reason is not terminal",
			payloads:      []string{`{"choices":[{"delta":{"content":"a"},"finish_reason":null}]}`},
			clientStarted: true,
			wantTruncated: true,
		},
		{
			name:          "usage null is not terminal",
			payloads:      []string{`{"choices":[{"delta":{"content":"a"}}],"usage":null}`},
			clientStarted: true,
			wantTruncated: true,
		},
		{
			// 上游对 stream 请求回了裸 JSON：无 data: 行，既有行为是原样透传。
			name:          "non-sse body already forwarded",
			payloads:      nil,
			clientStarted: true,
		},
		{
			name:          "no bytes at all",
			payloads:      nil,
			clientStarted: false,
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var state RawStreamTerminalState
			for _, payload := range tt.payloads {
				state.ObserveDataLine(payload)
			}
			require.Equal(t, tt.wantTerminated, state.Terminated())
			require.Equal(t, tt.wantTruncated, state.IsTruncated(tt.clientStarted))
		})
	}
}

func TestIsOpenAIChatUsageOnlyStreamChunk(t *testing.T) {
	t.Parallel()

	require.True(t, IsOpenAIChatUsageOnlyStreamChunk(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	require.False(t, IsOpenAIChatUsageOnlyStreamChunk(`{"choices":[{"index":0}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	require.False(t, IsOpenAIChatUsageOnlyStreamChunk(`{"choices":[]}`))
	require.False(t, IsOpenAIChatUsageOnlyStreamChunk(``))
}

func TestEnsureOpenAIChatStreamUsage(t *testing.T) {
	t.Parallel()

	body, err := EnsureOpenAIChatStreamUsage([]byte(`{"model":"gpt-5.4"}`))
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())

	body, err = EnsureOpenAIChatStreamUsage([]byte(`{"model":"gpt-5.4","stream_options":{"include_usage":false}}`))
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
}
