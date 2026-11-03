package openaiforward

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// cacheOutputSink retains the serialized native-Anthropic compatibility response.
type cacheOutputSink struct {
	bytes.Buffer // captured client response bytes
}

// Begin accepts output metadata without writing synthetic response content.
func (*cacheOutputSink) Begin(upstream.OutputHead) error { return nil }

// Emit retains every serialized payload for final wire-level usage assertions.
func (s *cacheOutputSink) Emit(event upstream.OutputEvent) error {
	_, err := s.Write(event.Data)
	return err
}

// TestNativeAnthropicOutputCacheOnlyMatrix supplies cache-only usage after an
// empty start event. Every buffered and streaming client adapter must report
// the same cache counts as its native accounting snapshot.
func TestNativeAnthropicOutputCacheOnlyMatrix(t *testing.T) {
	body := "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_cache","model":"vendor","role":"assistant","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}` + "\n\n" +
		"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"cached_tokens":60,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":8}}}` + "\n\n" +
		"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			name := map[bool]string{false: "responses", true: "chat"}[chat] + map[bool]string{false: "/json", true: "/sse"}[stream]
			t.Run(name, func(t *testing.T) {
				sink := &cacheOutputSink{}
				output := upstream.NewOutputContext(sink)
				options := AnthropicOutputOptions{
					Runtime:     bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
					MaxLineSize: func() int { return 1 << 20 }, StreamInterval: func() time.Duration { return 0 },
					CopyHeaders: func(http.Header, http.Header) {}, ReverseTools: func(body []byte) []byte { return body },
					Error: func(_ int, _, message string) { t.Error(message) }, Warn: func(string, ...zap.Field) {},
				}
				response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
				var result *Result
				var err error
				if chat && stream {
					result, err = ChatFromAnthropicStreaming(response, output, options, "client", "vendor", "vendor", nil, time.Now(), true)
				} else if chat {
					result, err = ChatFromAnthropicBuffered(response, output, options, "client", "vendor", "vendor", nil, time.Now())
				} else if stream {
					result, err = ResponsesFromAnthropicStreaming(response, output, options, "client", "vendor", "vendor", nil, time.Now(), bridge.ResponsesClientToolMapping{})
				} else {
					result, err = ResponsesFromAnthropicBuffered(response, output, options, "client", "vendor", "vendor", nil, time.Now(), bridge.ResponsesClientToolMapping{})
				}
				require.NoError(t, err)
				require.Zero(t, result.NativeUsage.InputTokens)
				require.Equal(t, 60, result.NativeUsage.CacheReadInputTokens)
				require.Equal(t, 20, result.NativeUsage.CacheCreationInputTokens)
				require.Equal(t, 80, result.Usage.InputTokens)
				visible := gjson.Get(sink.String(), "usage")
				if stream {
					for line := range strings.SplitSeq(sink.String(), "\n") {
						payload := strings.TrimPrefix(line, "data: ")
						if value := gjson.Get(payload, "usage"); value.IsObject() {
							visible = value
						}
						if value := gjson.Get(payload, "response.usage"); value.IsObject() {
							visible = value
						}
					}
				}
				if chat {
					require.Equal(t, int64(80), visible.Get("prompt_tokens").Int())
					require.Equal(t, int64(60), visible.Get("prompt_tokens_details.cached_tokens").Int())
					require.Equal(t, int64(20), visible.Get("prompt_tokens_details.cache_creation_tokens").Int())
				} else {
					require.Equal(t, int64(80), visible.Get("input_tokens").Int())
					require.Equal(t, int64(60), visible.Get("input_tokens_details.cached_tokens").Int())
					require.Equal(t, int64(20), visible.Get("cache_creation_input_tokens").Int())
				}
			})
		}
	}
}

// TestMergeAnthropicUsageNormalizesKimiStreamForOpenAIBilling merges start and
// terminal usage from a Kimi stream fixture. Normalized uncached and cache-read
// buckets must reconstruct the inclusive OpenAI input total while retaining
// output tokens.
func TestMergeAnthropicUsageNormalizesKimiStreamForOpenAIBilling(t *testing.T) {
	var start protocolanthropic.AnthropicStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":173306,"prompt_tokens":173306,"cached_tokens":0}}}`), &start))
	var delta protocolanthropic.AnthropicStreamEvent
	require.NoError(t, json.Unmarshal([]byte(`{"type":"message_delta","usage":{"input_tokens":250,"cache_read_input_tokens":173056,"output_tokens":166,"prompt_tokens":173306,"cached_tokens":173056}}`), &delta))

	usage := &protocol.TokenUsage{}
	protocolanthropic.MergeAnthropicUsage(usage, start.Message.Usage)
	protocolanthropic.MergeAnthropicUsage(usage, *delta.Usage)
	require.Equal(t, 250, usage.InputTokens)
	require.Equal(t, 173056, usage.CacheReadInputTokens)

	openAIUsage := AnthropicUsageToOpenAI(usage)
	require.Equal(t, 173306, openAIUsage.InputTokens, "OpenAI gateway expects an inclusive input total")
	require.Equal(t, 250, openAIUsage.InputTokens-openAIUsage.CacheReadInputTokens-openAIUsage.CacheCreationInputTokens)
	require.Equal(t, 166, openAIUsage.OutputTokens)
}

// TestMergeAnthropicUsageNormalizesGLMAndDeepSeekAliases parses provider cache
// aliases from buffered usage fixtures. Both encodings must yield separate
// uncached and cache-read buckets whose conversion recovers the inclusive input
// count.
func TestMergeAnthropicUsageNormalizesGLMAndDeepSeekAliases(t *testing.T) {
	tests := []struct {
		name string // provider fixture name
		raw  string // encoded usage response
	}{
		{
			name: "GLM",
			raw:  `{"input_tokens":1200,"output_tokens":30,"prompt_tokens":1200,"prompt_tokens_details":{"cached_tokens":800}}`,
		},
		{
			name: "DeepSeek",
			raw:  `{"input_tokens":1200,"output_tokens":30,"prompt_cache_hit_tokens":800,"prompt_cache_miss_tokens":400}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var src protocolanthropic.AnthropicUsage
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &src))

			usage := &protocol.TokenUsage{}
			protocolanthropic.MergeAnthropicUsage(usage, src)
			require.Equal(t, 400, usage.InputTokens)
			require.Equal(t, 800, usage.CacheReadInputTokens)

			openAIUsage := AnthropicUsageToOpenAI(usage)
			require.Equal(t, 1200, openAIUsage.InputTokens)
			require.Equal(t, 400, openAIUsage.InputTokens-openAIUsage.CacheReadInputTokens-openAIUsage.CacheCreationInputTokens)
		})
	}
}

// TestClaudeUsageToOpenAIUsagePreservesKimiZhipuDeepSeekNativeAnthropicBuckets
// converts already-normalized Anthropic token buckets for two Kimi, Zhipu, and
// DeepSeek fixtures. Cache creation and reads must remain separate while their
// sum with uncached input forms the OpenAI total.
func TestClaudeUsageToOpenAIUsagePreservesKimiZhipuDeepSeekNativeAnthropicBuckets(t *testing.T) {
	tests := []struct {
		name         string              // provider fixture name
		usage        protocol.TokenUsage // normalized token buckets
		wantTotal    int                 // expected inclusive input
		wantUncached int                 // expected uncached input
	}{
		{
			name: "GLM",
			usage: protocol.TokenUsage{
				InputTokens:              2,
				OutputTokens:             302,
				CacheCreationInputTokens: 733,
				CacheReadInputTokens:     376156,
			},
			wantTotal:    376891,
			wantUncached: 2,
		},
		{
			name: "DeepSeek",
			usage: protocol.TokenUsage{
				InputTokens:          400,
				OutputTokens:         30,
				CacheReadInputTokens: 800,
			},
			wantTotal:    1200,
			wantUncached: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openAIUsage := AnthropicUsageToOpenAI(&tt.usage)
			require.Equal(t, tt.wantTotal, openAIUsage.InputTokens)
			require.Equal(t, tt.wantUncached, openAIUsage.InputTokens-openAIUsage.CacheReadInputTokens-openAIUsage.CacheCreationInputTokens)
			require.Equal(t, tt.usage.CacheReadInputTokens, openAIUsage.CacheReadInputTokens)
			require.Equal(t, tt.usage.CacheCreationInputTokens, openAIUsage.CacheCreationInputTokens)
		})
	}
}

// TestAnthropicUsageCollectorMergesEventsAndBuildsResult feeds a start event,
// an undecodable payload, and a delta with a TTL bucket move to the shared
// reader collector. Output counts from message_start, explicit zero TTL
// buckets, speed, model evidence, and result identity must all survive.
func TestAnthropicUsageCollectorMergesEventsAndBuildsResult(t *testing.T) {
	resp := &http.Response{Header: http.Header{"X-Request-Id": []string{"req-1"}}}
	effort := "high"
	collector := newAnthropicUsageCollector(resp, "client-model", "billing-model", "upstream-model", &effort, time.Now(), true)
	require.Nil(t, collector.PartialResult(), "no observed usage leaves no partial result")

	start, decoded := collector.Observe(`{"type":"message_start","message":{"model":"vendor-model","usage":{"input_tokens":100,"output_tokens":3,"cache_creation_input_tokens":12,"cache_read_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"speed":"fast"}}}`)
	require.True(t, decoded)
	require.Equal(t, "message_start", start.Type)
	require.Equal(t, 3, collector.usage.OutputTokens, "typed merge keeps compatible message_start output")
	_, decoded = collector.Observe(`not json`)
	require.False(t, decoded)
	_, decoded = collector.Observe(`{"type":"message_delta","usage":{"output_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":0}}}`)
	require.True(t, decoded)

	want := protocol.TokenUsage{InputTokens: 100, OutputTokens: 10, CacheCreationInputTokens: 12, CacheReadInputTokens: 30, CacheCreation5mTokens: 12, CacheCreation1hTokens: 0, Speed: "fast"}
	require.Equal(t, want, collector.usage)
	result := collector.Result(true)
	require.Equal(t, &want, result.NativeUsage)
	require.Equal(t, 142, result.Usage.InputTokens)
	require.Equal(t, "req-1", result.RequestID)
	require.Equal(t, "client-model", result.Model)
	require.Equal(t, "billing-model", result.BillingModel)
	require.Equal(t, "upstream-model", result.UpstreamModel)
	require.Equal(t, "vendor-model", result.UpstreamResponseModel)
	require.Equal(t, "/v1/messages", result.UpstreamEndpoint)
	require.Same(t, &effort, result.ReasoningEffort)
	require.True(t, result.Stream)
	require.True(t, result.ClientDisconnect)
	require.NotNil(t, collector.PartialResult())
}
