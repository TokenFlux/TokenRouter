package forward

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// cacheConversionOutput captures the wire body emitted by conversion readers.
type cacheConversionOutput struct {
	bytes.Buffer        // captured client response bytes
	err          string // unexpected protocol error
}

// CopyHeaders ignores upstream metadata while checking converted response bodies.
func (*cacheConversionOutput) CopyHeaders(map[string][]string) {}

// BeginJSON accepts buffered delivery without adding bytes to the fixture.
func (*cacheConversionOutput) BeginJSON() {}

// BeginStream accepts streaming delivery without adding bytes to the fixture.
func (*cacheConversionOutput) BeginStream() {}

// ReverseTools keeps fixture payloads unchanged because no tools are supplied.
func (*cacheConversionOutput) ReverseTools(body []byte) []byte { return body }

// JSONBytes captures the serialized response produced by a buffered converter.
func (o *cacheConversionOutput) JSONBytes(body []byte) { _, _ = o.Write(body) }

// ResponsesJSON serializes the response exactly as the HTTP adapter would.
func (o *cacheConversionOutput) ResponsesJSON(value *openai.ResponsesResponse) {
	_ = json.NewEncoder(&o.Buffer).Encode(value)
}

// ChatJSON serializes the completion exactly as the HTTP adapter would.
func (o *cacheConversionOutput) ChatJSON(value *openai.ChatCompletionsResponse) {
	_ = json.NewEncoder(&o.Buffer).Encode(value)
}

// Event captures complete SSE data frames in their original emission order.
func (o *cacheConversionOutput) Event(_ string, body []byte) (int, error) {
	_, _ = o.WriteString("data: ")
	n, err := o.Write(body)
	_, _ = o.WriteString("\n\n")
	return n, err
}

// LocalEvent captures locally generated events through the same fixture framing.
func (o *cacheConversionOutput) LocalEvent(name string, body []byte) (int, error) {
	return o.Event(name, body)
}

// Flush leaves synchronous captured bytes available to the following assertion.
func (*cacheConversionOutput) Flush() {}

// Error records unexpected protocol failure text without hiding test diagnostics.
func (o *cacheConversionOutput) Error(_ int, _ string, message string) { o.err = message }

// Observe ignores routine diagnostics while preserving the reader's returned error.
func (*cacheConversionOutput) Observe(string, string, error, string, string) {}

// TestAnthropicConversionCacheUsageMatrix feeds aliases and lifetime-only writes
// through buffered and streaming client adapters. Serialized client totals must
// match internal disjoint billing buckets, including a cache-only terminal update.
func TestAnthropicConversionCacheUsageMatrix(t *testing.T) {
	for _, cacheOnly := range []bool{false, true} {
		for _, protocol := range []string{"chat", "responses"} {
			for _, stream := range []bool{false, true} {
				name := protocol + map[bool]string{false: "/json", true: "/sse"}[stream] + map[bool]string{false: "/mixed", true: "/cache-only"}[cacheOnly]
				t.Run(name, func(t *testing.T) {
					update := `{"prompt_tokens":100,"cached_tokens":60,"output_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":8}}`
					input, output := 20, 10
					if cacheOnly {
						update = `{"input_tokens":0,"cache_read_input_tokens":60,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":8}}`
						input, output = 0, 0
					}
					body := "data: " + `{"type":"message_start","message":{"id":"msg_cache","model":"vendor","role":"assistant","type":"message","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}` + "\n\n" +
						"data: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":` + update + "}\n\n" +
						"data: " + `{"type":"message_stop"}` + "\n\n"
					in := Response{Lines: bufio.NewScanner(strings.NewReader(body)), Runtime: bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}}
					out := &cacheConversionOutput{}
					var result *Result
					var err error
					if protocol == "responses" {
						if stream {
							result, err = ResponsesStreaming(in, out, "client", "vendor", nil, time.Now(), bridge.ResponsesClientToolMapping{})
						} else {
							result, err = ResponsesBuffered(in, out, "client", "vendor", nil, time.Now(), bridge.ResponsesClientToolMapping{})
						}
					} else if stream {
						result, err = ChatStreaming(in, out, "client", "vendor", nil, time.Now(), true)
					} else {
						result, err = ChatBuffered(in, out, "client", "vendor", nil, time.Now())
					}
					require.NoError(t, err)
					require.Empty(t, out.err)
					require.Equal(t, input, result.Usage.InputTokens)
					require.Equal(t, output, result.Usage.OutputTokens)
					require.Equal(t, 60, result.Usage.CacheReadInputTokens)
					require.Equal(t, 20, result.Usage.CacheCreationInputTokens)
					visible := gjson.Get(out.String(), "usage")
					if stream {
						for line := range strings.SplitSeq(out.String(), "\n") {
							payload := strings.TrimPrefix(line, "data: ")
							if value := gjson.Get(payload, "usage"); value.IsObject() {
								visible = value
							}
							if value := gjson.Get(payload, "response.usage"); value.IsObject() {
								visible = value
							}
						}
					}
					require.True(t, visible.IsObject(), out.String())
					inputPath, cachePath, writePath := "input_tokens", "input_tokens_details.cached_tokens", "cache_creation_input_tokens"
					if protocol == "chat" {
						inputPath, cachePath, writePath = "prompt_tokens", "prompt_tokens_details.cached_tokens", "prompt_tokens_details.cache_creation_tokens"
					}
					require.Equal(t, int64(input+80), visible.Get(inputPath).Int())
					require.Equal(t, int64(60), visible.Get(cachePath).Int())
					require.Equal(t, int64(20), visible.Get(writePath).Int())
				})
			}
		}
	}
}

// TestAppendRawJSON_EmptyObjectPlaceholder supplies a whitespace-formatted empty
// object and a nonempty object as existing argument bytes. The placeholder is
// replaced, while the nonempty object's literal bytes precede the new fragment.
func TestAppendRawJSON_EmptyObjectPlaceholder(t *testing.T) {
	t.Parallel()

	fragment := `{"query":"status"}`
	require.JSONEq(t, fragment, string(AppendRawJSON(json.RawMessage("{ \n\t }"), fragment)))
	require.Equal(t, `{"existing":true}{"query":"status"}`, string(AppendRawJSON(json.RawMessage(`{"existing":true}`), fragment)))
}
