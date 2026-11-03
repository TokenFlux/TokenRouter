package gemini

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// cache_usage_scenario_test.go covers response.go and response_openai.go together.
// It compares wire usage and internal accounting across Messages, Chat, and
// Responses, including cache counts arriving after Gemini candidate completion.

// cacheWireSink captures serialized client payloads for protocol-level assertions.
type cacheWireSink struct {
	bytes.Buffer // captured client payload bytes
}

// Begin accepts response metadata without adding bytes to the fixture.
func (s *cacheWireSink) Begin(upstream.OutputHead) error { return nil }

// Emit retains each synchronous client payload in its original emission order.
func (s *cacheWireSink) Emit(event upstream.OutputEvent) error {
	_, err := s.Write(event.Data)
	return err
}

// TestGeminiClientCacheUsageMatrix converts the same cached prompt into each
// supported client protocol. JSON and sparse-tail SSE must expose the cache
// count while preserving disjoint accounting and inclusive OpenAI input totals.
func TestGeminiClientCacheUsageMatrix(t *testing.T) {
	jsonBody := `{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":60,"candidatesTokenCount":10}}`
	sseBody := "data: " + `{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100}}` + "\n\n" +
		"data: " + `{"usageMetadata":{"cachedContentTokenCount":60,"candidatesTokenCount":10}}` + "\n\ndata: [DONE]\n\n"
	for _, protocol := range []string{"messages", "chat", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				sink := &cacheWireSink{}
				output := upstream.NewOutputContext(sink)
				adapter := ResponseAdapter{Options: ResponseOptions{
					ObserveImages: func([]byte) {}, ReadBody: io.ReadAll,
					WriteHeaders: func(http.Header, http.Header) {},
					ReverseTools: func(body []byte) []byte { return body },
				}}
				body := jsonBody
				if stream {
					body = sseBody
				}
				response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
				var usage *upstream.TokenUsage
				var err error
				if stream {
					var result *StreamResult
					if protocol == "messages" {
						result, err = adapter.HandleStreamingResponse(output, response, time.Now(), "gemini")
					} else {
						mode := OpenAICompatChatCompletions
						if protocol == "responses" {
							mode = OpenAICompatResponses
						}
						result, err = adapter.HandleOpenAICompatStreamingResponseFromGemini(output, response, time.Now(), "gemini", false, true, mode, bridge.ResponsesClientToolMapping{})
					}
					require.NoError(t, err)
					usage = result.Usage
				} else {
					switch protocol {
					case "messages":
						usage, err = adapter.HandleNonStreamingResponse(output, response, "gemini")
					case "chat":
						usage, err = adapter.HandleChatCompletionsNonStreamingResponseFromGemini(output, response, "gemini", false)
					case "responses":
						usage, err = adapter.HandleResponsesNonStreamingResponseFromGemini(output, response, "gemini", false, bridge.ResponsesClientToolMapping{})
					}
				}
				require.NoError(t, err)
				require.Equal(t, 40, usage.InputTokens)
				require.Equal(t, 60, usage.CacheReadInputTokens)
				require.Equal(t, 10, usage.OutputTokens)
				visible := gjson.Get(sink.String(), "usage")
				if stream {
					// Select the final serialized usage rather than converter state, so
					// tests detect fields omitted from actual client-facing terminal events.
					for line := range strings.SplitSeq(sink.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						payload := strings.TrimPrefix(line, "data: ")
						if u := gjson.Get(payload, "usage"); u.IsObject() {
							visible = u
						}
						if u := gjson.Get(payload, "response.usage"); u.IsObject() {
							visible = u
						}
					}
				}
				require.True(t, visible.IsObject(), sink.String())
				switch protocol {
				case "messages":
					require.Equal(t, int64(40), visible.Get("input_tokens").Int())
					require.Equal(t, int64(60), visible.Get("cache_read_input_tokens").Int())
				case "chat":
					require.Equal(t, int64(100), visible.Get("prompt_tokens").Int())
					require.Equal(t, int64(60), visible.Get("prompt_tokens_details.cached_tokens").Int())
					require.Equal(t, int64(110), visible.Get("total_tokens").Int())
				case "responses":
					require.Equal(t, int64(100), visible.Get("input_tokens").Int())
					require.Equal(t, int64(60), visible.Get("input_tokens_details.cached_tokens").Int())
					require.Equal(t, int64(110), visible.Get("total_tokens").Int())
				}
			})
		}
	}
}

// TestGeminiChatUsageOptOutRetainsAccounting disables optional Chat usage
// while the upstream reports cached input. Client chunks must omit usage
// without removing the internal cache observation used for settlement.
func TestGeminiChatUsageOptOutRetainsAccounting(t *testing.T) {
	body := "data: " + `{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":60,"candidatesTokenCount":10}}` + "\n\n"
	sink := &cacheWireSink{}
	adapter := ResponseAdapter{Options: ResponseOptions{ReverseTools: func(data []byte) []byte { return data }}}
	response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	result, err := adapter.HandleOpenAICompatStreamingResponseFromGemini(upstream.NewOutputContext(sink), response, time.Now(), "model", false, false, OpenAICompatChatCompletions, bridge.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.Equal(t, 60, result.Usage.CacheReadInputTokens)
	require.NotContains(t, sink.String(), `"usage"`)
}
