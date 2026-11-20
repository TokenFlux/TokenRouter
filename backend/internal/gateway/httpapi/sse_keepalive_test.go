package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// keepaliveTestTransport supplies deterministic HTTP header waits inside synctest.
type keepaliveTestTransport func(*http.Request) (*http.Response, error)

// keepaliveDelayedBody delays its first read while preserving the fixture payload.
type keepaliveDelayedBody struct {
	io.Reader         // remaining upstream payload bytes
	beforeRead func() // first-read wait assertion
}

// failingSSEWriter simulates a client disconnect error while keeping http.Flusher.
type failingSSEWriter struct {
	gin.ResponseWriter       // embedded recorder response writer
	err                error // error returned by every Write call
}

// Write returns the configured write failure without writing bytes to the client.
func (w *failingSSEWriter) Write([]byte) (int, error) { return 0, w.err }

// RoundTrip runs the fixture's synchronous transport without opening a network connection.
func (f keepaliveTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestDownstreamSSEKeepaliveFraming exercises complete frames, split boundaries,
// terminal events and client protocols. Idle beats must use the selected wire
// format and never enter an incomplete or already terminated SSE event stream.
func TestDownstreamSSEKeepaliveFraming(t *testing.T) {
	for _, tc := range []struct {
		name     string              // framing scenario label
		protocol protocol.ProtocolID // client wire protocol
		pieces   []string            // separately delivered frame fragments
		want     string              // expected idle heartbeat suffix
	}{
		{"responses", protocol.ProtocolOpenAIResponses, []string{"data: {}\n\n"}, ": keepalive\n\n"},
		{"chat", protocol.ProtocolOpenAIChatCompletions, []string{"data: {}\n\n"}, ": keepalive\n\n"},
		{"gemini", protocol.ProtocolGeminiGenerateContent, []string{"data: {}\n\n"}, "\n\n"},
		{"qoder", protocol.ProtocolQoderChat, []string{"data: {}\n\n"}, ": keepalive\n\n"},
		{"messages", protocol.ProtocolAnthropicMessages, []string{"data: {}\n\n"}, "event: ping\ndata: {\"type\": \"ping\"}\n\n"},
		{"compact excluded", protocol.ProtocolResponsesCompact, []string{"data: {}\n\n"}, ""},
		{"partial", protocol.ProtocolOpenAIResponses, []string{"data: {"}, ""},
		{"mixed LF and CRLF", protocol.ProtocolOpenAIResponses, []string{"data: {}\n", "\r", "\n"}, ": keepalive\n\n"},
		{"split CRLF", protocol.ProtocolOpenAIResponses, []string{"data: {}\r", "\n\r", "\n"}, ": keepalive\n\n"},
		{"done", protocol.ProtocolOpenAIChatCompletions, []string{"data: [DONE]\n\n"}, ""},
		{"message stop", protocol.ProtocolAnthropicMessages, []string{"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"}, ""},
		{"failed", protocol.ProtocolOpenAIResponses, []string{"event: response.failed\ndata: {}\n\n"}, ""},
		{"truncated terminal", protocol.ProtocolOpenAIResponses, []string{"event: response.completed\ndata: {\"text\":\"" + strings.Repeat("x", 5000) + "\"}\n\n"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runSSEKeepaliveTest(t, tc.protocol, "", true, 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
				c.Header("Content-Type", "text/event-stream")
				for _, piece := range tc.pieces {
					_, err := c.Writer.WriteString(piece)
					require.NoError(t, err)
				}
				time.Sleep(25 * time.Millisecond)
				synctest.Wait()
				suffix := strings.TrimPrefix(readKeepaliveBody(t, c, rec), strings.Join(tc.pieces, ""))
				if tc.want == "" {
					require.Empty(t, suffix)
				} else {
					require.Equal(t, strings.Repeat(tc.want, 2), suffix)
					require.Equal(t, len(suffix), StreamHeartbeatBytes(c))
				}
			})
		})
	}
}

// TestDownstreamSSEKeepaliveClaudeToolDelta keeps an open Claude Code tool block
// idle and then closes it. The worker must emit empty tool-input deltas only
// while that block remains open and return to ping after its stop event.
func TestDownstreamSSEKeepaliveClaudeToolDelta(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolAnthropicMessages, "claude-cli/2.1.193 (external, cli)", true, 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\"}}\n\n")
		require.NoError(t, err)
		time.Sleep(15 * time.Millisecond)
		synctest.Wait()
		require.Contains(t, readKeepaliveBody(t, c, rec), `"type":"input_json_delta","partial_json":""`)
		require.Contains(t, readKeepaliveBody(t, c, rec), `"index":3`)
		_, err = c.Writer.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":3}\n\n")
		require.NoError(t, err)
		before := len(readKeepaliveBody(t, c, rec))
		time.Sleep(15 * time.Millisecond)
		synctest.Wait()
		require.Equal(t, "event: ping\ndata: {\"type\": \"ping\"}\n\n", readKeepaliveBody(t, c, rec)[before:])
	})
}

// TestDownstreamSSEKeepaliveHeaderWait exercises dispatch before dependency timing
// is enabled. Streaming waits may commit SSE, while nonstream requests, explicit
// error statuses, handler header access and non-SSE metadata must remain uncommitted.
func TestDownstreamSSEKeepaliveHeaderWait(t *testing.T) {
	for _, mode := range []string{"stream", "buffered", "status", "header access", "json"} {
		t.Run(mode, func(t *testing.T) {
			runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", mode != "buffered", 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
				if mode == "status" {
					c.Writer.WriteHeader(http.StatusTooManyRequests)
				}
				if mode == "json" {
					c.Header("Content-Type", "application/json")
				}
				client := &http.Client{Transport: keepaliveTestTransport(func(*http.Request) (*http.Response, error) {
					if mode == "header access" {
						c.Writer.Header().Set("X-Handler", "owns headers")
					}
					time.Sleep(25 * time.Millisecond)
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
				})}
				req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, "https://upstream.test", nil)
				require.NoError(t, err)
				resp, err := timing.Do(client, req)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				synctest.Wait()
				if mode != "stream" {
					require.False(t, c.Writer.Written())
					require.Empty(t, readKeepaliveBody(t, c, rec))
					return
				}
				require.True(t, c.Writer.Written())
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
				require.Equal(t, strings.Repeat(": keepalive\n\n", 2), readKeepaliveBody(t, c, rec))
				require.Equal(t, -1, HeartbeatAdjustedWrittenSize(c))
			})
		})
	}
}

// TestDownstreamSSEKeepaliveStallLimit holds a committed body beyond its inactivity
// budget. Only beats before the cutoff may reach the client; observing another
// upstream byte renews activity and permits downstream heartbeats again.
func TestDownstreamSSEKeepaliveStallLimit(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 25*time.Millisecond, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		w, ok := c.Writer.(*sseKeepaliveWriter)
		require.True(t, ok)
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.Write([]byte("data: {}\n\n"))
		require.NoError(t, err)
		time.Sleep(80 * time.Millisecond)
		synctest.Wait()
		require.Equal(t, "data: {}\n\n"+strings.Repeat(": keepalive\n\n", 2), readKeepaliveBody(t, c, rec))
		w.stages.ObserveUpstreamActivity(time.Now())
		time.Sleep(15 * time.Millisecond)
		synctest.Wait()
		require.Greater(t, StreamHeartbeatBytes(c), 2*len(": keepalive\n\n"))
	})
}

// TestDownstreamSSEKeepaliveInitialOutputPreservesWait initializes both eager and
// deferred codec output after dispatch. Read-only header snapshots must preserve
// waiting heartbeats until the codec begins actual output or handler metadata.
func TestDownstreamSSEKeepaliveInitialOutputPreservesWait(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
			w, ok := c.Writer.(*sseKeepaliveWriter)
			require.True(t, ok)
			w.openHeaderWait(c.Request.Context())
			var out *upstream.OutputContext
			sink := &gateway.OutputTracker{Sink: NewResponseSink(c)}
			if deferred {
				out = upstream.NewDeferredOutputContext(sink)
			} else {
				out = upstream.NewOutputContext(sink)
			}
			out.Writer.Header().Set("X-Upstream", "pending")
			time.Sleep(25 * time.Millisecond)
			synctest.Wait()
			require.Equal(t, strings.Repeat(": keepalive\n\n", 2), readKeepaliveBody(t, c, rec))
			require.True(t, out.Writer.Written())
			require.False(t, out.Writer.ContentWritten())
			_, err := out.Writer.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
			require.NoError(t, err)
			require.Empty(t, rec.Header().Get("X-Upstream"))
		})
	}
}

// TestDownstreamSSEKeepaliveConcurrentHeaders exercises late handler header edits
// while timer writes remain possible. Returned header copies must prevent data
// races without changing the committed wire headers or interleaving partial frames.
func TestDownstreamSSEKeepaliveConcurrentHeaders(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolOpenAIChatCompletions, "", true, 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		w, ok := c.Writer.(*sseKeepaliveWriter)
		require.True(t, ok)
		w.openHeaderWait(c.Request.Context())
		time.Sleep(15 * time.Millisecond)
		for range 100 {
			c.Writer.Header().Set("X-Late", "ignored")
			_, err := c.Writer.Write([]byte("data: {}\n\n"))
			require.NoError(t, err)
			time.Sleep(11 * time.Millisecond)
		}
		synctest.Wait()
		require.Empty(t, rec.Header().Get("X-Late"))
		require.Positive(t, StreamHeartbeatBytes(c))
	})
}

// TestDownstreamSSEKeepaliveSkipsUntimedRequests confirms non-text requests never
// acquire a writer or dispatch listener. The route remains ordinary HTTP even
// when the caller marks a protocol on its request context.
func TestDownstreamSSEKeepaliveSkipsUntimedRequests(t *testing.T) {
	for _, path := range []string{"/media", "/v1/responses/compact", "/responses/compact", "/backend-api/codex/responses/compact"} {
		engine := gin.New()
		engine.POST(path, TextRequestTiming(), DownstreamSSEKeepalive(time.Millisecond, 0), func(c *gin.Context) {
			_, installed := c.Get(sseKeepaliveWriterKey)
			require.False(t, installed)
			c.String(http.StatusOK, "plain")
		})
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, nil))
		require.Equal(t, "plain", rec.Body.String())
	}
}

// readKeepaliveBody copies recorder output under the production writer mutex.
// Tests use it while the handler is active so future timer writes have the
// same synchronization boundary as the recorder read being asserted.
func readKeepaliveBody(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) string {
	t.Helper()
	value, _ := c.Get(sseKeepaliveWriterKey)
	w, ok := value.(*sseKeepaliveWriter)
	require.True(t, ok)
	w.mu.Lock()
	defer w.mu.Unlock()
	return rec.Body.String()
}

// Read invokes the first-read hook once before consuming any upstream payload.
func (r *keepaliveDelayedBody) Read(p []byte) (int, error) {
	if r.beforeRead != nil {
		r.beforeRead()
		r.beforeRead = nil
	}
	return r.Reader.Read(p)
}

// TestDownstreamSSEKeepaliveThinkingWait returns headers immediately but delays
// the first upstream event. Staged Responses, Grok and Anthropic conversion
// must deliver downstream heartbeats before any provider content becomes available.
func TestDownstreamSSEKeepaliveThinkingWait(t *testing.T) {
	for _, name := range []string{"responses", "grok", "anthropic conversion"} {
		conversion := name == "anthropic conversion"
		t.Run(name, func(t *testing.T) {
			runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 0, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
				payload := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
				if conversion {
					payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"role\":\"assistant\",\"model\":\"model\",\"content\":[],\"usage\":{\"input_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				client := &http.Client{Transport: keepaliveTestTransport(func(*http.Request) (*http.Response, error) {
					body := &keepaliveDelayedBody{Reader: strings.NewReader(payload), beforeRead: func() {
						time.Sleep(25 * time.Millisecond)
						synctest.Wait()
						if !strings.Contains(readKeepaliveBody(t, c, rec), ": keepalive\n\n") {
							t.Error("downstream heartbeat missing before the first upstream event")
						}
					}}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(body)}, nil
				})}
				req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, "https://upstream.test", nil)
				require.NoError(t, err)
				resp, err := timing.Do(client, req)
				require.NoError(t, err)
				defer func() { require.NoError(t, resp.Body.Close()) }()
				timing.ObserveResponse(req.Context(), resp)
				if conversion {
					boundary := &MessageForwardBoundary{context: c}
					output := boundary.ConversionOutput(true, &messageforward.AttemptState{})
					_, err = forwardcore.ResponsesStreaming(forwardcore.Response{
						Lines: bufio.NewScanner(resp.Body), Headers: resp.Header,
						Runtime: bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
					}, output, "model", "model", nil, time.Now(), bridge.ResponsesClientToolMapping{})
				} else {
					fixture := newResponsesFixture(responsesFixtureInputs{})
					platform := capability.PlatformOpenAI
					if name == "grok" {
						platform = capability.PlatformGrok
					}
					_, err = fixture.Output.ReadStreamObservation(req.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{Platform: platform}}, time.Now(), "model", "model", "")
				}
				require.NoError(t, err)
			})
		})
	}
}

// TestDownstreamSSEKeepaliveHTTPFailover exercises real network header waiting
// for every public text protocol. The client must receive a heartbeat before
// the first provider fails and then receive content from a second provider.
func TestDownstreamSSEKeepaliveHTTPFailover(t *testing.T) {
	for _, tc := range []struct {
		path      string              // public client endpoint
		protocol  protocol.ProtocolID // downstream wire protocol
		heartbeat string              // first expected SSE frame
	}{
		{"/v1/messages", protocol.ProtocolAnthropicMessages, "event: ping\ndata: {\"type\": \"ping\"}\n\n"},
		{"/v1/chat/completions", protocol.ProtocolOpenAIChatCompletions, ": keepalive\n\n"},
		{"/v1/responses", protocol.ProtocolOpenAIResponses, ": keepalive\n\n"},
		{"/v1beta/models/test:streamGenerateContent", protocol.ProtocolGeminiGenerateContent, "\n\n"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			releaseHeaders := make(chan struct{}, 1)
			var attempts atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					select {
					case <-releaseHeaders:
					case <-r.Context().Done():
						return
					}
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"content\":\"second provider\"}\n\n")
			}))
			engine := gin.New()
			engine.POST(tc.path, TextRequestTiming(), DownstreamSSEKeepalive(10*time.Millisecond, time.Second), func(c *gin.Context) {
				c.Request = c.Request.WithContext(requeststate.WithClientProtocol(c.Request.Context(), tc.protocol))
				timing.StagesFromContext(c.Request.Context()).SetStream(true)
				baseline := HeartbeatAdjustedWrittenSize(c)
				for range 2 {
					req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, provider.URL, nil)
					if err != nil {
						t.Error(err)
						return
					}
					resp, err := timing.Do(provider.Client(), req)
					if err != nil {
						t.Error(err)
						return
					}
					timing.ObserveResponse(req.Context(), resp)
					if resp.StatusCode == http.StatusBadGateway {
						_ = resp.Body.Close()
						if HeartbeatAdjustedWrittenSize(c) != baseline {
							t.Error("heartbeat closed the retry window")
							return
						}
						continue
					}
					output := upstream.NewOutputContext(NewResponseSink(c))
					output.Header("Content-Type", "text/event-stream")
					_, err = io.Copy(output.Writer, resp.Body)
					_ = resp.Body.Close()
					if err != nil {
						t.Error(err)
					}
					return
				}
			})
			gateway := httptest.NewServer(engine)
			t.Cleanup(func() {
				close(releaseHeaders)
				gateway.Close()
				provider.Close()
			})
			client := gateway.Client()
			client.Timeout = 3 * time.Second
			resp, err := client.Post(gateway.URL+tc.path, "application/json", strings.NewReader(`{"stream":true}`))
			require.NoError(t, err)
			defer func() { require.NoError(t, resp.Body.Close()) }()
			first := make([]byte, len(tc.heartbeat))
			_, err = io.ReadFull(resp.Body, first)
			require.NoError(t, err)
			require.Equal(t, tc.heartbeat, string(first))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			releaseHeaders <- struct{}{}
			rest, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Contains(t, string(rest), `"content":"second provider"`)
			require.Equal(t, int32(2), attempts.Load())
		})
	}
}

// TestDownstreamSSEKeepaliveIgnoresUpstreamHeartbeatCadence sends upstream comments
// more frequently than downstream beats. Those reads renew the stall clock,
// while clients still receive local heartbeats before the first business event.
func TestDownstreamSSEKeepaliveIgnoresUpstreamHeartbeatCadence(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 12*time.Millisecond, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		reader, writer := io.Pipe()
		defer func() { require.NoError(t, reader.Close()) }()
		go func() {
			defer func() { _ = writer.Close() }()
			for range 8 {
				time.Sleep(5 * time.Millisecond)
				_, _ = io.WriteString(writer, ": upstream-only-marker\n\n")
			}
			_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"content\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_active\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		}()
		client := &http.Client{Transport: keepaliveTestTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
		})}
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, "https://upstream.test", nil)
		require.NoError(t, err)
		resp, err := timing.Do(client, req)
		require.NoError(t, err)
		timing.ObserveResponse(req.Context(), resp)
		fixture := newResponsesFixture(responsesFixtureInputs{})
		_, err = fixture.Output.ReadStreamObservation(req.Context(), resp, c, &gatewayprovider.ExecutionProvider{Record: providercore.Record{Platform: capability.PlatformOpenAI}}, time.Now(), "model", "model", "")
		require.NoError(t, err)
		synctest.Wait()
		body := readKeepaliveBody(t, c, rec)
		require.NotContains(t, body, "upstream-only-marker")
		require.GreaterOrEqual(t, strings.Count(body, ": keepalive\n\n"), 2)
		require.Less(t, strings.Index(body, ": keepalive"), strings.Index(body, "response.output_text.delta"))
	})
}

// TestDownstreamSSEKeepaliveLongHeaderWait keeps the transport pending beyond
// several body inactivity budgets. Heartbeats must continue after their own
// HTTP commitment, then respect a fresh body budget when upstream headers arrive.
func TestDownstreamSSEKeepaliveLongHeaderWait(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 25*time.Millisecond, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		client := &http.Client{Transport: keepaliveTestTransport(func(*http.Request) (*http.Response, error) {
			time.Sleep(85 * time.Millisecond)
			synctest.Wait()
			require.GreaterOrEqual(t, strings.Count(readKeepaliveBody(t, c, rec), ": keepalive"), 8)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, "https://upstream.test", nil)
		require.NoError(t, err)
		resp, err := timing.Do(client, req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		require.Equal(t, 10, strings.Count(readKeepaliveBody(t, c, rec), ": keepalive"))
		before := StreamHeartbeatBytes(c)
		time.Sleep(60 * time.Millisecond)
		synctest.Wait()
		require.Equal(t, before, StreamHeartbeatBytes(c), "local beats must not renew the body stall clock")
	})
}

// TestDownstreamSSEKeepaliveHandlerActivity covers an output path without HTTP
// transport observations. Accepted handler bytes must renew the body budget,
// while subsequent timer heartbeats cannot keep a silent response alive forever.
func TestDownstreamSSEKeepaliveHandlerActivity(t *testing.T) {
	runSSEKeepaliveTest(t, protocol.ProtocolOpenAIResponses, "", true, 25*time.Millisecond, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
		stages := timing.StagesFromContext(c.Request.Context())
		stages.ObserveUpstreamActivity(time.Now().Add(-time.Hour))
		c.Header("Content-Type", "text/event-stream")
		for range 3 {
			_, err := c.Writer.Write([]byte("data: {\"text\":\"active\"}\n\n"))
			require.NoError(t, err)
			before := StreamHeartbeatBytes(c)
			time.Sleep(20 * time.Millisecond)
			synctest.Wait()
			require.Greater(t, StreamHeartbeatBytes(c), before)
		}
		time.Sleep(30 * time.Millisecond)
		synctest.Wait()
		before := StreamHeartbeatBytes(c)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		require.Equal(t, before, StreamHeartbeatBytes(c))
		require.Equal(t, 3, strings.Count(readKeepaliveBody(t, c, rec), `"active"`))
	})
}

// runSSEKeepalivePathTest executes handle under TextRequestTiming and
// DownstreamSSEKeepalive on the specified endpoint path inside synctest.
// Callers use it when route path matching selects protocol-specific SSE error
// envelopes such as Responses response.failed versus generic event: error.
func runSSEKeepalivePathTest(t *testing.T, path string, p protocol.ProtocolID, handle func(*testing.T, *gin.Context, *httptest.ResponseRecorder)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		rec := httptest.NewRecorder()
		engine := gin.New()
		engine.POST(path, TextRequestTiming(), DownstreamSSEKeepalive(10*time.Millisecond, 0), func(c *gin.Context) {
			c.Request = c.Request.WithContext(requeststate.WithClientProtocol(c.Request.Context(), p))
			timing.StagesFromContext(c.Request.Context()).SetStream(true)
			handle(t, c, rec)
		})
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	})
}

// commitHeaderWaitHeartbeat opens the header-wait window and advances the
// synctest clock until DownstreamSSEKeepalive commits HTTP 200 text/event-stream
// and flushes at least one idle heartbeat frame.
func commitHeaderWaitHeartbeat(t *testing.T, c *gin.Context) {
	t.Helper()
	w, ok := c.Writer.(*sseKeepaliveWriter)
	require.True(t, ok)
	require.False(t, CommittedSSE(c))
	require.False(t, TerminalSSEDelivered(c))
	w.openHeaderWait(c.Request.Context())
	time.Sleep(15 * time.Millisecond)
	synctest.Wait()
	require.True(t, CommittedSSE(c))
	require.False(t, TerminalSSEDelivered(c))
	require.Positive(t, StreamHeartbeatBytes(c))
}

// assertKeepaliveSSEBodyValid verifies that every non-blank line in body is
// valid SSE syntax with parseable JSON payloads, catching trailing raw JSON.
func assertKeepaliveSSEBodyValid(t *testing.T, body string) {
	t.Helper()
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event: ") {
			continue
		}
		require.True(t, strings.HasPrefix(line, "data: "), "expected SSE framing, found raw line %q in %q", line, body)
		payload := strings.TrimPrefix(line, "data: ")
		require.True(t, json.Valid([]byte(payload)), "SSE data line must hold valid JSON: %q", payload)
	}
}

// TestDownstreamSSEKeepaliveHeaderWaitErrorStopsBeats lets header-wait
// heartbeats commit HTTP 200 text/event-stream, then emits terminal errors with
// streamStarted false. Each shared error writer must emit valid protocol SSE,
// mark OpsStreamError, and stop subsequent idle beats atomically.
func TestDownstreamSSEKeepaliveHeaderWaitErrorStopsBeats(t *testing.T) {
	cases := []struct {
		name      string              // error writer scenario label
		path      string              // client request endpoint
		proto     protocol.ProtocolID // client wire protocol
		wantToken string              // expected terminal SSE marker
		emit      func(*gin.Context)  // terminal error emitter
	}{
		{
			name:      "anthropic streaming error",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			emit: func(c *gin.Context) {
				DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, http.StatusBadGateway, "upstream_error", "Upstream gateway error", false)
			},
		},
		{
			name:      "anthropic legacy stream error",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `data: {"type":"error"`,
			emit: func(c *gin.Context) {
				WriteAnthropicStreamError(c, http.StatusBadGateway, "upstream_error", "", "Upstream gateway error", false, MarkOpsStreamError)
			},
		},
		{
			name:      "chat streaming error",
			path:      EndpointChatCompletions,
			proto:     protocol.ProtocolOpenAIChatCompletions,
			wantToken: "event: error\n",
			emit: func(c *gin.Context) {
				DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, http.StatusBadGateway, "upstream_error", "", "Upstream gateway error", false, false)
			},
		},
		{
			name:      "responses streaming error",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			emit: func(c *gin.Context) {
				DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, http.StatusBadGateway, "upstream_error", "", "Upstream gateway error", false, false)
			},
		},
		{
			name:      "responses WriteError fallback",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			emit: func(c *gin.Context) {
				DefaultOpenAIErrorOutput().WriteError(c, http.StatusBadGateway, "upstream_error", "Upstream gateway error")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSSEKeepalivePathTest(t, tc.path, tc.proto, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
				commitHeaderWaitHeartbeat(t, c)
				tc.emit(c)

				require.True(t, TerminalSSEDelivered(c))
				beatsAfterTerminal := StreamHeartbeatBytes(c)
				time.Sleep(40 * time.Millisecond)
				synctest.Wait()
				require.Equal(t, beatsAfterTerminal, StreamHeartbeatBytes(c), "terminal SSE error must stop subsequent keepalives")

				body := readKeepaliveBody(t, c, rec)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, body, tc.wantToken)
				assertKeepaliveSSEBodyValid(t, body)
				streamErr, ok := GetOpsStreamError(c)
				require.True(t, ok)
				require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
			})
		})
	}
}

// TestDownstreamSSEKeepaliveHeaderWaitDirectHTTPErrorsEmitSSE triggers direct
// single-attempt error adapters after DownstreamSSEKeepalive commits an event
// stream. Every adapter across Anthropic, OpenAI, Gemini, Antigravity, and
// local policy checks must emit valid SSE error frames and stop further beats.
func TestDownstreamSSEKeepaliveHeaderWaitDirectHTTPErrorsEmitSSE(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{ID: 1, Name: "fixture", Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey},
	}
	geminiProvider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{ID: 2, Name: "gemini-fixture", Platform: capability.PlatformGemini, Type: capability.ProviderTypeAPIKey},
	}
	antigravityProvider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{ID: 3, Name: "ag-fixture", Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeAPIKey},
	}
	badReqBody := []byte(`{"error":{"type":"invalid_request_error","code":"invalid_param","message":"bad input"}}`)

	cases := []struct {
		name      string              // adapter scenario label
		path      string              // client request endpoint
		proto     protocol.ProtocolID // client wire protocol
		wantToken string              // expected terminal SSE marker
		wantCode  int                 // expected recorded Ops status
		emit      func(*gin.Context)  // direct error adapter invocation
	}{
		{
			name:      "AnthropicForwardErrorOutput.Message",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `data: {"type":"error"`,
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				(AnthropicForwardErrorOutput{Context: c}).Message(http.StatusBadRequest, "invalid_request_error", "bad messages input")
			},
		},
		{
			name:      "AnthropicForwardErrorOutput.Raw",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `"type":"error"`,
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				(AnthropicForwardErrorOutput{Context: c}).Raw(http.StatusBadRequest, badReqBody)
			},
		},
		{
			name:      "WriteForwardAnthropicError",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteForwardAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "bad input")
			},
		},
		{
			name:      "WriteForwardAnthropicErrorBody",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteForwardAnthropicErrorBody(c, http.StatusBadRequest, badReqBody)
			},
		},
		{
			name:      "WriteForwardChatError",
			path:      EndpointChatCompletions,
			proto:     protocol.ProtocolOpenAIChatCompletions,
			wantToken: "event: error\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteForwardChatError(c, http.StatusBadRequest, "invalid_request_error", "bad chat input")
			},
		},
		{
			name:      "WriteForwardChatErrorBody",
			path:      EndpointChatCompletions,
			proto:     protocol.ProtocolOpenAIChatCompletions,
			wantToken: "event: error\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteForwardChatErrorBody(c, http.StatusBadRequest, badReqBody)
			},
		},
		{
			name:      "WriteForwardResponsesFallbackError",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadGateway,
			emit: func(c *gin.Context) {
				WriteForwardResponsesFallbackError(c, http.StatusBadGateway, "upstream_error", "fallback failed")
			},
		},
		{
			name:      "WriteForwardPassthroughErrorEnvelope",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadGateway,
			emit: func(c *gin.Context) {
				WriteForwardPassthroughErrorEnvelope(c, http.StatusBadGateway, nil, "Upstream request failed", func(*gin.Context, int, []byte) bool { return false })
			},
		},
		{
			name:      "WriteForwardFastPolicyBlocked messages",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			wantCode:  http.StatusForbidden,
			emit: func(c *gin.Context) {
				WriteForwardFastPolicyBlocked(c, "fast tier denied", func(*gin.Context) bool { return false }, func(*gin.Context, int, string, string) {})
			},
		},
		{
			name:      "WriteOpenAIForwardRejection",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteOpenAIForwardRejection(c, http.StatusBadRequest, "invalid_request_error", "invalid tool", "tools")
			},
		},
		{
			name:      "OpenAIResponseOutput.ResponseError 400",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				out := &OpenAIResponseOutput{}
				resp := &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(string(badReqBody))),
				}
				_, _ = out.ResponseError(c.Request.Context(), resp, c, provider, []byte(`{"model":"gpt-5.4"}`), "gpt-5.4")
			},
		},
		{
			name:      "WriteOpenAIUpstreamClientError",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteOpenAIUpstreamClientError(c, http.StatusBadRequest, badReqBody, "bad input")
			},
		},
		{
			name:      "GeminiOutput.GeminiMappedError",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `data: {"type":"error"`,
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				out := &GeminiOutput{GoogleOutput: GoogleOutput{Context: c}}
				_ = out.GeminiMappedError(geminiProvider, http.StatusBadRequest, "req-1", []byte(`{"error":{"message":"invalid prompt"}}`))
			},
		},
		{
			name:      "AntigravityOutput.MappedClaudeError",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `data: {"type":"error"`,
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				out := &AntigravityOutput{GoogleOutput: GoogleOutput{Context: c}}
				_ = out.MappedClaudeError(antigravityProvider, http.StatusBadRequest, "req-2", []byte(`{"error":{"message":"bad ag input"}}`))
			},
		},
		{
			name:      "AntigravityOutput.MappedAntigravityCompatError",
			path:      EndpointChatCompletions,
			proto:     protocol.ProtocolOpenAIChatCompletions,
			wantToken: "event: error\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				out := &AntigravityOutput{GoogleOutput: GoogleOutput{Context: c}}
				_ = out.MappedAntigravityCompatError(antigravityProvider, http.StatusBadRequest, "req-3", []byte(`{"error":{"message":"bad compat input"}}`))
			},
		},
		{
			name:      "ForwardConversionOutput.Error responses",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadGateway,
			emit: func(c *gin.Context) {
				(ForwardConversionOutput{Context: c, Responses: true, Commit: func() {}}).Error(http.StatusBadGateway, "upstream_error", "conversion failed")
			},
		},
		{
			name:      "WriteForwardMessageGenericError",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `data: {"type":"error"`,
			wantCode:  http.StatusInternalServerError,
			emit: func(c *gin.Context) {
				WriteForwardMessageGenericError(c, func() {})
			},
		},
		{
			name:      "AnthropicResponseTooLarge",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `data: {"type":"error"`,
			wantCode:  http.StatusBadGateway,
			emit: func(c *gin.Context) {
				AnthropicResponseTooLarge(c)
			},
		},
		{
			name:      "OpenAIResponseTooLarge",
			path:      EndpointChatCompletions,
			proto:     protocol.ProtocolOpenAIChatCompletions,
			wantToken: "event: error\n",
			wantCode:  http.StatusBadGateway,
			emit: func(c *gin.Context) {
				OpenAIResponseTooLarge(c)
			},
		},
		{
			name:      "OpenAIRequests.EnforceClient messages",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			wantCode:  http.StatusForbidden,
			emit: func(c *gin.Context) {
				codexProvider := &gatewayprovider.ExecutionProvider{
					Record: providercore.Record{
						ID:       4,
						Name:     "codex-only",
						Platform: capability.PlatformOpenAI,
						Type:     capability.ProviderTypeOAuth,
						Extra:    map[string]any{"codex_cli_only": true},
					},
				}
				_ = (&OpenAIRequests{}).EnforceClient(c.Request.Context(), c, codexProvider, nil, egress.TLSFingerprintRouterMatchResult{})
			},
		},
		{
			name:      "openAIMessagesExecutionAdapter.Error",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			wantCode:  http.StatusForbidden,
			emit: func(c *gin.Context) {
				(&openAIMessagesExecutionAdapter{c: c}).Error(http.StatusForbidden, "forbidden_error", "reasoning limit exceeded")
				require.True(t, IsResponseCommitted(c))
			},
		},
		{
			name:      "WriteGrokForwardInvalidRequest responses",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteGrokForwardInvalidRequest(c, "unsupported tool", "tools")
				require.True(t, IsResponseCommitted(c))
			},
		},
		{
			name:      "WriteGrokForwardInvalidRequest messages",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				WriteGrokForwardInvalidRequest(c, "unsupported tool", "tools")
				require.True(t, IsResponseCommitted(c))
			},
		},
		{
			name:      "OpenAIResponseOutput.ProtocolError",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusBadGateway,
			emit: func(c *gin.Context) {
				resp := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}}
				_ = (&OpenAIResponseOutput{}).ProtocolError(resp, c, "malformed upstream payload")
			},
		},
		{
			name:      "CyberHandler.RejectSession anthropic",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: "event: error\n",
			wantCode:  http.StatusForbidden,
			emit: func(c *gin.Context) {
				p := &cyberTestPorts{enabled: true, scope: true, found: "blocked"}
				require.True(t, newCyberTest(p).RejectSession(c, prefaceKey(), nil, "claude-sonnet-4-5", CyberBlockAnthropic))
			},
		},
		{
			name:      "CyberHandler.RejectSession responses",
			path:      EndpointResponses,
			proto:     protocol.ProtocolOpenAIResponses,
			wantToken: "event: response.failed\n",
			wantCode:  http.StatusForbidden,
			emit: func(c *gin.Context) {
				p := &cyberTestPorts{enabled: true, scope: true, found: "blocked"}
				require.True(t, newCyberTest(p).RejectSession(c, prefaceKey(), nil, "gpt-5.4", CyberBlockResponses))
			},
		},
		{
			name:      "UnifiedTextExecutor.prepareNativeMessages parse error",
			path:      EndpointMessages,
			proto:     protocol.ProtocolAnthropicMessages,
			wantToken: `data: {"type":"error"`,
			wantCode:  http.StatusBadRequest,
			emit: func(c *gin.Context) {
				_, _, _, err := (&UnifiedTextExecutor{}).prepareNativeMessages(c, provider, []byte("{bad-json"))
				require.Error(t, err)
				require.True(t, IsResponseCommitted(c))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSSEKeepalivePathTest(t, tc.path, tc.proto, func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder) {
				commitHeaderWaitHeartbeat(t, c)
				tc.emit(c)

				require.True(t, TerminalSSEDelivered(c))
				beatsAfterTerminal := StreamHeartbeatBytes(c)
				time.Sleep(40 * time.Millisecond)
				synctest.Wait()
				require.Equal(t, beatsAfterTerminal, StreamHeartbeatBytes(c))

				body := readKeepaliveBody(t, c, rec)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, body, tc.wantToken)
				assertKeepaliveSSEBodyValid(t, body)
				streamErr, ok := GetOpsStreamError(c)
				require.True(t, ok)
				require.Equal(t, tc.wantCode, streamErr.IntendedStatus)
			})
		})
	}
}

// TestTerminalSSEDeliveredWithoutKeepaliveMiddlewareSuppressesDuplicateFallback
// commits SSE without DownstreamSSEKeepalive and also exercises a failing client
// write. Local terminal SSE emission must set TerminalSSEDelivered so outer
// fallback helpers never append a second generic error frame.
func TestTerminalSSEDeliveredWithoutKeepaliveMiddlewareSuppressesDuplicateFallback(t *testing.T) {
	t.Run("queue heartbeat without keepalive middleware", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, EndpointMessages, nil)
		c.Request = req.WithContext(requeststate.WithClientProtocol(req.Context(), protocol.ProtocolAnthropicMessages))
		c.Header("Content-Type", "text/event-stream")
		n, err := c.Writer.WriteString("event: ping\ndata: {\"type\": \"ping\"}\n\n")
		require.NoError(t, err)
		RecordStreamHeartbeat(c, n)
		c.Writer.Flush()

		require.True(t, CommittedSSE(c))
		require.False(t, TerminalSSEDelivered(c))

		WriteForwardFastPolicyBlocked(c, "fast tier denied", func(*gin.Context) bool { return false }, func(*gin.Context, int, string, string) {})

		require.True(t, TerminalSSEDelivered(c))
		require.False(t, DefaultOpenAIErrorOutput().EnsureFallback(c, true))
		require.False(t, (MessagesErrorOutput{}).EnsureResponse(c, true))
		require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error\n"))
		assertKeepaliveSSEBodyValid(t, rec.Body.String())
	})

	t.Run("failed terminal client write still deduplicates fallback", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, nil)
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.WriteString(": keepalive\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
		c.Writer = &failingSSEWriter{ResponseWriter: c.Writer, err: errors.New("broken pipe")}

		DefaultOpenAIErrorOutput().StreamError(c, http.StatusBadGateway, "upstream_error", "upstream failed", false)

		require.True(t, TerminalSSEDelivered(c))
		require.NotEmpty(t, c.Errors)
		require.False(t, DefaultOpenAIErrorOutput().EnsureFallback(c, true))
	})
}
