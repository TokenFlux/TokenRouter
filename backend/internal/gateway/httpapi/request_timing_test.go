package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// timingFailedWriter models a disconnected client after response headers.
type timingFailedWriter struct {
	gin.ResponseWriter /* borrowed original writer */
}

// timingUploadBody models slow original reads before composite-key memory replay.
type timingUploadBody struct {
	io.Reader /* original encoded request bytes */
}

// identifierFailureReader terminates an upstream stream after its observable metadata.
type identifierFailureReader struct{}

// TestTextRequestTimingWireMatrix exercises common wire shapes used by every
// HTTP text platform. Metadata and heartbeats precede useful content, and final
// output bytes and flush behavior remain those supplied by the handler.
func TestTextRequestTimingWireMatrix(t *testing.T) {
	cases := map[string]string{
		"openai_responses":   `{"type":"response.output_text.delta","delta":"hello"}`,
		"openai_chat":        `{"choices":[{"delta":{"content":"hello"}}]}`,
		"anthropic_messages": `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
		"deepseek_reasoning": `{"choices":[{"delta":{"reasoning_content":"reason"}}]}`,
		"kimi_tool":          `{"type":"content_block_start","content_block":{"type":"tool_use","id":"tool1","name":"read"}}`,
		"zhipu_tool":         `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`,
		"gemini_native":      `{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`,
		"gemini_function":    `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{}}}]}}]}`,
		"antigravity_native": `{"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}}`,
		"qoder_chat":         `{"choices":[{"delta":{"content":"hello"}}]}`,
		"grok_responses":     `{"type":"response.function_call_arguments.delta","delta":"{}"}`,
		"vertex_claude":      `{"type":"content_block_delta","delta":{"thinking":"reason"}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(TextRequestTiming())
			var stages *timing.Stages
			router.POST("/v1/messages", func(c *gin.Context) {
				stages = timing.StagesFromContext(c.Request.Context())
				require.NotNil(t, stages)
				SetOpsRequestContext(c, "model", true)
				MarkOpsProviderSlotAcquired(c)
				response := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(": ping\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\ndata: " + payload + "\n\n"))}
				timing.ObserveResponse(timing.BeginHTTPTrace(c.Request.Context()), response)
				_, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				c.Header("Content-Type", "text/event-stream")
				if name == "gemini_native" {
					c.Header("Content-Type", "application/json")
				}
				_, err = c.Writer.WriteString(": ping\n\n")
				require.NoError(t, err)
				c.Writer.Flush()
				require.NotContains(t, stages.Snapshot(), "first_downstream_flush_ms")
				sink := NewResponseSink(c)
				err = sink.Emit(upstream.OutputEvent{Data: []byte("data: {\"type\":\"message_start\"}\n\n")})
				require.NoError(t, err)
				require.NotContains(t, stages.Snapshot(), "first_visible_output_ms")
				frame := "data: " + payload + "\n\n"
				for _, b := range []byte(frame) {
					err = sink.Emit(upstream.OutputEvent{Data: []byte{b}})
					require.NoError(t, err)
				}
				c.Writer.Flush()
			})
			out := httptest.NewRecorder()
			router.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`)))
			got := stages.Snapshot()
			for _, key := range []string{"upstream_first_event_ms", "first_visible_output_ms", "first_downstream_flush_ms"} {
				require.Contains(t, got, key)
			}
			require.Equal(t, ": ping\n\ndata: {\"type\":\"message_start\"}\n\ndata: "+payload+"\n\n", out.Body.String())
		})
	}
}

// TestTimingFramesBoundaries rejects truncated data and metadata-only payloads.
// Multiline events and EOF completion retain their actual frame boundary rather
// than treating every network read as an upstream event.
func TestTimingFramesBoundaries(t *testing.T) {
	var events []string
	f := timingFrames{observe: func(data, _ string) { events = append(events, data) }}
	f.feed([]byte("event: message\r\ndata:{\r\ndata: \"type\": \"message_start\"}\r\n\r\n"))
	require.Len(t, events, 1)
	require.False(t, timingVisible(events[0], ""))
	f.feed([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"text\"}"))
	f.finish()
	require.Len(t, events, 2)
	require.True(t, timingVisible(events[1], ""))
	f.feed([]byte("data: {\"type\":\"truncated\""))
	f.finish()
	require.Len(t, events, 2)
	for _, payload := range []string{`{"type":"content_block_delta","delta":{"signature":"secret"}}`, `{"type":"response.created","response":{"id":"r","model":"gpt"}}`, `{"type":"content_block_delta","delta":{"text":""}}`} {
		require.False(t, timingVisible(payload, ""), payload)
	}
}

// TestTimingWriterEventNamesAndLegacyTools covers payloads whose event type is
// supplied by SSE and legacy Chat function calls. Only delivered content counts;
// metadata, empty tool fragments and protocol heartbeats leave milestones absent.
func TestTimingWriterEventNamesAndLegacyTools(t *testing.T) {
	cases := []struct {
		name    string // descriptive protocol case name
		event   string // optional SSE event header
		data    string // complete JSON event payload
		visible bool   // useful output is delivered
		flushed bool   // response event is flushed
	}{
		{"responses_text", "response.output_text.delta", `{"delta":"hello"}`, true, true},
		{"responses_reasoning", "response.reasoning_summary_text.delta", `{"delta":"reason"}`, true, true},
		{"responses_tool", "response.function_call_arguments.delta", `{"delta":"{}"}`, true, true},
		{"messages_text", "content_block_delta", `{"delta":{"type":"text_delta","text":"hello"}}`, true, true},
		{"messages_thinking", "content_block_delta", `{"delta":{"type":"thinking_delta","thinking":"reason"}}`, true, true},
		{"payload_wins", "ping", `{"type":"response.output_text.delta","delta":"hello"}`, true, true},
		{"payload_ping_wins", "response.output_text.delta", `{"type":"ping","delta":"hello"}`, false, false},
		{"metadata", "response.created", `{"response":{"id":"r"}}`, false, true},
		{"empty_text", "content_block_delta", `{"delta":{"type":"text_delta","text":""}}`, false, false},
		{"empty_thinking", "content_block_delta", `{"delta":{"type":"thinking_delta","thinking":""}}`, false, false},
		{"signature", "content_block_delta", `{"delta":{"signature":"secret"}}`, false, true},
		{"legacy_tool_name", "", `{"choices":[{"delta":{"function_call":{"name":"lookup"}}}]}`, true, true},
		{"legacy_tool_arguments", "", `{"choices":[{"delta":{"function_call":{"arguments":"{}"}}}]}`, true, true},
		{"legacy_empty_tool", "", `{"choices":[{"delta":{"function_call":{}}}]}`, false, true},
		{"legacy_empty_fields", "", `{"choices":[{"delta":{"function_call":{"name":"","arguments":""}}}]}`, false, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			out := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(out)
			_, stages := timing.WithStages(t.Context(), time.Now(), nil)
			stages.SetStream(true)
			writer := &timingWriter{ResponseWriter: c.Writer, stages: stages}
			writer.frames.observe = writer.observe
			writer.Header().Set("Content-Type", "text/event-stream")
			frame := "event: " + tt.event + "\ndata: " + tt.data + "\n\n"
			sink := ResponseSink{Writer: writer, timing: writer}
			err := sink.Emit(upstream.OutputEvent{Data: []byte(frame)})
			require.NoError(t, err)
			writer.Flush()
			_, visible := stages.Snapshot()["first_visible_output_ms"]
			_, flushed := stages.Snapshot()["first_downstream_flush_ms"]
			require.Equal(t, tt.visible, visible)
			require.Equal(t, tt.flushed, flushed)
			require.Equal(t, frame, out.Body.String())
		})
	}
}

// TestTextRequestTimingScope excludes media and WebSocket requests from the new
// contract. Failed text admission remains observable without inventing a slot,
// while synchronous requests retain explicit stream inapplicability.
func TestTextRequestTimingScope(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/v1/responses", "/v1/systemone"} {
		t.Run(path, func(t *testing.T) {
			router := gin.New()
			router.Use(TextRequestTiming())
			var stages *timing.Stages
			router.POST(path, func(c *gin.Context) { stages = timing.StagesFromContext(c.Request.Context()); c.Status(403) })
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
			if strings.Contains(path, "images") {
				require.Nil(t, stages)
				return
			}
			require.Equal(t, int64(0), stages.Snapshot()["timing_stream"])
			require.NotContains(t, stages.Snapshot(), "provider_slot_acquired_ms")
		})
	}
	ctx, stages := timing.WithStages(context.Background(), time.Now(), nil)
	ctx = scheduler.WithAdmissionObservation(ctx, stages.Admit)
	scheduler.ObserveAdmission(ctx)
	require.Equal(t, int64(1), stages.Snapshot()["timing_admitted"])
}

// Write rejects client bytes so observation cannot claim useful delivery.
func (w timingFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestTimingWriterFailureDoesNotPublishDelivery rejects a complete useful event.
// A failed write followed by Flush must leave useful-output and response-flush
// milestones absent while preserving the exact original write error.
func TestTimingWriterFailureDoesNotPublishDelivery(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, stages := timing.WithStages(t.Context(), time.Now(), nil)
	w := &timingWriter{ResponseWriter: timingFailedWriter{c.Writer}, stages: stages}
	w.frames.observe = w.observe
	w.Header().Set("Content-Type", "text/event-stream")
	sink := ResponseSink{Writer: w, timing: w}
	err := sink.Emit(upstream.OutputEvent{Data: []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")})
	require.ErrorIs(t, err, io.ErrClosedPipe)
	w.Flush()
	require.NotContains(t, stages.Snapshot(), "first_visible_output_ms")
	require.NotContains(t, stages.Snapshot(), "first_downstream_flush_ms")
	require.Zero(t, w.upstreamWrites.Load())
	w.ResponseWriter = c.Writer
	_, err = w.WriteString("event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n")
	require.NoError(t, err)
	w.Flush()
	require.NotContains(t, stages.Snapshot(), "first_downstream_flush_ms")
}

// TestTimingOutputProvenanceSurvivesWriterWrappers distinguishes upstream output
// from local errors even after admission. Partial SSE writes pass through model
// restoration before observation, and a local terminal flush can deliver earlier
// upstream bytes without requiring an upstream-parser timestamp to exist first.
func TestTimingOutputProvenanceSurvivesWriterWrappers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := gin.New()
		router.Use(TextRequestTiming())
		var stages *timing.Stages
		router.POST("/v1/responses", func(c *gin.Context) {
			stages = timing.StagesFromContext(c.Request.Context())
			SetOpsRequestContext(c, "model", true)
			MarkOpsProviderSlotAcquired(c)
			c.Writer = &CompositeModelResponseWriter{ResponseWriter: c.Writer, actualModel: "model", clientModel: "group/model"}
			c.Header("Content-Type", "text/event-stream")
			require.True(t, WriteResponsesFailedSSE(c, "server_error", "local", "local", "test", "model"))
			require.NotContains(t, stages.Snapshot(), "first_downstream_flush_ms")
			sink := NewResponseSink(c)
			require.NoError(t, sink.Emit(upstream.OutputEvent{Data: []byte("data: {\"type\":\"response.output_text.delta\",\"model\":\"model\",\"delta\":\"hello\"}\n")}))
			require.NotContains(t, stages.Snapshot(), "first_visible_output_ms")
			time.Sleep(time.Second)
			require.NoError(t, sink.Emit(upstream.OutputEvent{Data: []byte("\n")}))
			require.Equal(t, int64(1000), stages.Snapshot()["first_visible_output_ms"])
			require.NotContains(t, stages.Snapshot(), "upstream_first_event_ms")
			require.NotContains(t, stages.Snapshot(), "first_downstream_flush_ms")
			time.Sleep(time.Second)
			require.True(t, WriteResponsesFailedSSE(c, "server_error", "local", "local", "test", "model"))
			require.Equal(t, int64(2000), stages.Snapshot()["first_downstream_flush_ms"])
			time.Sleep(time.Second)
			require.NoError(t, sink.Emit(upstream.OutputEvent{Data: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"later\"}}]}\n\n"), Flush: true}))
			require.Equal(t, int64(1000), stages.Snapshot()["first_visible_output_ms"])
			require.Equal(t, int64(2000), stages.Snapshot()["first_downstream_flush_ms"])
		})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))
		require.Contains(t, out.Body.String(), `"model":"group/model"`)
	})
}

// TestTextRequestTimingConvertedChat verifies actual Chat readers and
// conversion into each supported client text protocol. Kimi and DeepSeek and
// OpenAI routes share observation after conversion, while usage and terminal
// delivery remain intact.
func TestTextRequestTimingConvertedChat(t *testing.T) {
	payload := "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
	for _, platform := range []string{capability.PlatformOpenAI, capability.PlatformDeepseek, capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformGrok} {
		for _, ingress := range textAPIKeyProtocolIngressCases() {
			t.Run(platform+"/"+ingress.name, func(t *testing.T) {
				body := strings.ReplaceAll(string(ingress.body), "false", "true")
				if platform == capability.PlatformGrok {
					body = strings.ReplaceAll(body, "deepseek-chat", "grok-4.5")
				}
				source := protocol.ProtocolOpenAIResponses
				if ingress.name == "messages" {
					source = protocol.ProtocolAnthropicMessages
				}
				if ingress.name == "chat completions" {
					source = protocol.ProtocolOpenAIChatCompletions
				}
				target := protocol.ProtocolOpenAIChatCompletions
				provider := adaptiveProtocolTestProvider(platform, nil)
				provider.Record.Credentials = map[string]any{"api_key": "test", "base_url": "http://upstream.example", providercore.UpstreamProtocolsKey: []protocol.ProtocolID{target}}
				group := &routing.Group{ProtocolFallbacks: map[protocol.ProtocolID][]protocol.ProtocolID{source: {target}}}
				router := gin.New()
				router.Use(TextRequestTiming())
				var stages *timing.Stages
				router.POST(ingress.path, func(c *gin.Context) {
					ctx := requeststate.WithClientProtocol(requeststate.WithGroup(c.Request.Context(), group), source)
					c.Request = c.Request.WithContext(ctx)
					stages = timing.StagesFromContext(ctx)
					SetOpsRequestContext(c, "model", true)
					MarkOpsProviderSlotAcquired(c)
					response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}
					timing.ObserveResponse(timing.BeginHTTPTrace(ctx), response)
					transport := &auxiliaryHTTPRecorder{resp: response}
					svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: transport})
					var err error
					switch source {
					case protocol.ProtocolAnthropicMessages:
						_, err = svc.Text.Messages(ctx, c, provider, []byte(body), "", "")
					case protocol.ProtocolOpenAIChatCompletions:
						_, err = svc.Text.Chat(ctx, c, provider, []byte(body), "", "")
					default:
						_, err = svc.Forward(ctx, c, provider, []byte(body))
					}
					require.NoError(t, err)
				})
				output := httptest.NewRecorder()
				router.ServeHTTP(output, httptest.NewRequest(http.MethodPost, ingress.path, strings.NewReader(body)))
				require.Contains(t, output.Body.String(), "hello")
				for _, key := range []string{"upstream_first_event_ms", "first_visible_output_ms", "first_downstream_flush_ms"} {
					require.Contains(t, stages.Snapshot(), key)
				}
			})
		}
	}
}

// Read delays each original read while leaving its result unchanged.
func (b timingUploadBody) Read(p []byte) (int, error) {
	time.Sleep(time.Second)
	return b.Reader.Read(p)
}

// Close accepts ownership cleanup without changing the fixture's readable bytes.
func (b timingUploadBody) Close() error { return nil }

// TestTextRequestTimingCompositeCompressedRead consumes the original request in
// composite authentication, then replays decoded JSON in the handler. Slow raw
// reads count once, while deliberate post-read processing stays outside queues.
func TestTextRequestTimingCompositeCompressedRead(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprint(compressed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				raw := []byte(`{"model":"text","stream":false}`)
				payload := raw
				if compressed {
					var buf bytes.Buffer
					gz := gzip.NewWriter(&buf)
					_, err := gz.Write(raw)
					require.NoError(t, err)
					require.NoError(t, gz.Close())
					payload = buf.Bytes()
				}
				router := gin.New()
				router.Use(TextRequestTiming())
				router.Use(func(c *gin.Context) {
					read, err := ReadAndRestoreRequestBody(c.Request)
					require.NoError(t, err)
					require.Equal(t, raw, read)
					c.Next()
				})
				var stages *timing.Stages
				router.POST("/v1/messages", func(c *gin.Context) {
					read, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, 1<<20)
					require.NoError(t, err)
					require.Equal(t, raw, read)
					time.Sleep(3 * time.Second)
					MarkOpsProviderSlotAcquired(c)
					stages = timing.StagesFromContext(c.Request.Context())
				})
				req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				req.Body = timingUploadBody{bytes.NewReader(payload)}
				if compressed {
					req.Header.Set("Content-Encoding", "gzip")
				}
				router.ServeHTTP(httptest.NewRecorder(), req)
				got := stages.Snapshot()
				require.Equal(t, int64(2000), got["request_body_read_ms"])
				require.Equal(t, int64(3000), got["preprocess_ms"])
				require.Zero(t, got["user_slot_wait_ms"])
				require.Zero(t, got["provider_slot_wait_ms"])
			})
		})
	}
}

// TestTimingDataEventDistinguishesKeepaliveFrames covers Claude no-op deltas and
// inconsistent Grok ping headers. Real payload types win over header hints,
// while generated empty deltas cannot establish a response-data timestamp.
func TestTimingDataEventDistinguishesKeepaliveFrames(t *testing.T) {
	require.False(t, timingDataEvent(`{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}`, "content_block_delta"))
	require.False(t, timingDataEvent(`{"type":"ping"}`, "ping"))
	require.True(t, timingDataEvent(`{"type":"response.output_text.delta","delta":"hello"}`, "ping"))
	require.True(t, timingDataEvent(`{"type":"message_start"}`, "message_start"))
}

// TestTimingLocalFailureDoesNotFlushUpstream records a queue-only Responses failure.
// The request never reaches a provider, so no upstream delivery event exists.
func TestTimingLocalFailureDoesNotFlushUpstream(t *testing.T) {
	router := gin.New()
	router.Use(TextRequestTiming())
	var stages *timing.Stages
	router.POST("/v1/responses", func(c *gin.Context) {
		stages = timing.StagesFromContext(c.Request.Context())
		SetOpsRequestContext(c, "model", true)
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.WriteString(": ping\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
		require.True(t, WriteResponsesFailedSSE(c, "rate_limit_error", "concurrency_timeout", "queue timed out", "test", "model"))
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))
	require.NotContains(t, stages.Snapshot(), "upstream_first_event_ms")
	require.NotContains(t, stages.Snapshot(), "first_downstream_flush_ms")
}

// TestTextRequestTimingFinalUsageAndToolInput exercises the upstream body observer
// with separately arriving SSE frames. Both accepted Chat completion and newly
// arrived tool arguments must retain complete usage and the actual content interval.
func TestTextRequestTimingFinalUsageAndToolInput(t *testing.T) {
	for name, frames := range map[string][]string{
		"chat_without_done": {
			`{"choices":[{"delta":{"content":"hello"}}]}`,
			`{"choices":[{"delta":{"content":" world"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
		},
		"tool_arguments_done": {
			`{"type":"response.output_item.added","item":{"id":"tool","type":"function_call","name":"lookup","arguments":""}}`,
			`{"type":"response.function_call_arguments.done","item_id":"tool","arguments":"{}"}`,
			`{"type":"response.completed","response":{"output":[{"id":"tool","type":"function_call","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":20}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := gin.New()
				router.Use(TextRequestTiming())
				var observed *timing.Performance
				router.POST("/v1/responses", func(c *gin.Context) {
					SetOpsRequestContext(c, "model", true)
					MarkOpsProviderSlotAcquired(c)
					reader, writer := io.Pipe()
					// Each pipe write waits for the reader before virtual time advances again,
					// distinguishing upstream arrivals without relying on machine scheduling delays.
					go func() {
						defer func() { _ = writer.Close() }()
						for _, frame := range frames {
							time.Sleep(100 * time.Millisecond)
							if _, err := io.WriteString(writer, "data: "+frame+"\n\n"); err != nil {
								return
							}
						}
					}()
					response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}
					timing.ObserveResponse(timing.BeginHTTPTrace(c.Request.Context()), response)
					_, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					observed = timing.PerformanceFromContext(c.Request.Context())
				})
				router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
				require.NotNil(t, observed)
				require.True(t, observed.Complete)
				require.True(t, observed.UsageComplete)
				require.Equal(t, int64(2), observed.Values["upstream_output_event_count"])
				require.Equal(t, 100.0, *observed.TPS(20))
			})
		})
	}
}

// TestTextRequestTimingBufferedOutputExcludesAdmissionAndDelivery delays a coalesced
// upstream response and then emits converted downstream text. Average output speed
// includes only the upstream wait, excluding earlier admission and later delivery.
func TestTextRequestTimingBufferedOutputExcludesAdmissionAndDelivery(t *testing.T) {
	for _, coalesced := range []bool{false, true} {
		t.Run(fmt.Sprintf("coalesced_%t", coalesced), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := gin.New()
				router.Use(TextRequestTiming())
				var observed *timing.Performance
				router.POST("/v1/responses", func(c *gin.Context) {
					SetOpsRequestContext(c, "model", true)
					time.Sleep(3 * time.Second)
					MarkOpsProviderSlotAcquired(c)
					body := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"world\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"candidatesTokenCount\":20}}\n\n"
					if coalesced {
						body = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n" + body
					}
					response := &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(body)),
					}
					timing.ObserveResponse(timing.BeginHTTPTrace(c.Request.Context()), response)
					// A virtual delay precedes the single body read, matching buffered upstream
					// delivery without manufacturing separate arrival times while parsing events.
					time.Sleep(2 * time.Second)
					_, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					// Downstream conversion and delivery occur after the upstream read. Their
					// later visible output must not replace the usage-producing attempt's endpoint.
					time.Sleep(4 * time.Second)
					c.Header("Content-Type", "text/event-stream")
					_, err = c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n")
					require.NoError(t, err)
					observed = timing.PerformanceFromContext(c.Request.Context())
				})
				router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
				require.NotNil(t, observed)
				require.Equal(t, int64(3000), observed.Values["upstream_started_ms"])
				require.Equal(t, int64(5000), observed.Values["upstream_first_output_ms"])
				require.Equal(t, int64(5000), observed.Values["upstream_last_output_ms"])
				require.Equal(t, 10.0, *observed.TPS(20))
			})
		})
	}
}

// Read ends the fixture with an unexpected upstream EOF. The preceding reader
// already supplied a complete response-created frame, so the collector must
// preserve that earlier metadata.
func (identifierFailureReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestRequestIdentifiersSurviveStreamFailure observes a response-created frame
// before failure or cancellation. The selected header and parsed response ID
// survive later empty and conflicting Ops events without changing wire headers.
func TestRequestIdentifiersSurviveStreamFailure(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		var saved telemetry.RequestRecord
		var mu sync.Mutex
		router := gin.New()
		router.Use(middleware.RequestLogger(func(r telemetry.RequestRecord) {
			mu.Lock()
			saved = r
			mu.Unlock()
		}), TextRequestTiming())
		router.POST("/v1/responses", func(c *gin.Context) {
			SetOpsSelectedProvider(c, providercore.ProviderSnapshot{ID: 7, Platform: "openai", RequestIDHeader: "X-Client-Request-ID"})
			base, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(base)
			ctx := timing.BeginHTTPTrace(base)
			body := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\"}}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"unselected"}, "X-Client-Request-Id": {"chosen"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(body), identifierFailureReader{}))}
			timing.ObserveResponse(ctx, resp)
			_, err := io.ReadAll(resp.Body)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.NoError(t, resp.Body.Close())
			if cancelRequest {
				cancel()
			}
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 7, UpstreamStatusCode: 502, Kind: "stream_error"})
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 7, UpstreamStatusCode: 502, UpstreamRequestID: "unselected", Kind: "stream_error"})
			require.Equal(t, "unselected", resp.Header.Get("X-Request-ID"))
			require.Equal(t, "chosen", resp.Header.Get("X-Client-Request-ID"))
			c.Status(502)
		})
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}")))
		mu.Lock()
		actual := saved
		mu.Unlock()
		require.Len(t, actual.Attempts, 1)
		require.Equal(t, "chosen", actual.Attempts[0].RequestID)
		require.Equal(t, "X-Client-Request-Id", actual.Attempts[0].RequestIDHeader)
		require.Contains(t, actual.Aliases, telemetry.RequestAlias{Kind: "response", Value: "resp_partial", Source: "response.id", AttemptNumber: 1, ProviderID: 7})
		for _, alias := range actual.Aliases {
			require.NotEqual(t, "unselected", alias.Value)
		}
	}
}

// TestRequestIdentifierResponseUsesOriginalDispatch delays the first response
// until another provider is selected. Actual response observation must retain
// its original provider, header policy, and ordinal despite the newer collector state.
func TestRequestIdentifierResponseUsesOriginalDispatch(t *testing.T) {
	var saved telemetry.RequestRecord
	router := gin.New()
	router.Use(middleware.RequestLogger(func(r telemetry.RequestRecord) { saved = r }), TextRequestTiming())
	router.POST("/v1/responses", func(c *gin.Context) {
		SetOpsSelectedProvider(c, providercore.ProviderSnapshot{ID: 7, Platform: "openai", RequestIDHeader: "X-Client-Request-ID"})
		first := timing.BeginHTTPTrace(c.Request.Context())
		SetOpsSelectedProvider(c, providercore.ProviderSnapshot{ID: 8, Platform: "openai"})
		second := timing.BeginHTTPTrace(c.Request.Context())
		timing.ObserveResponse(second, &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": {"second"}}})
		timing.ObserveResponse(first, &http.Response{StatusCode: 502, Header: http.Header{"X-Request-Id": {"unselected"}, "X-Client-Request-Id": {"first"}}})
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 7, UpstreamStatusCode: 502, UpstreamRequestID: "unselected"})
		c.Status(200)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}")))
	require.Len(t, saved.Attempts, 2)
	require.Equal(t, "first", saved.Attempts[0].RequestID)
	require.Equal(t, 7, int(saved.Attempts[0].ProviderID))
	require.Equal(t, "second", saved.Attempts[1].RequestID)
	require.Equal(t, 200, saved.Attempts[1].Status)
}
