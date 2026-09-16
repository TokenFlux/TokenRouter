// 本地真实 HTTP 验证单次 Execute 的协议输出、部分事实及资源释放。
package gemini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

type executeSink struct {
	body   bytes.Buffer
	events []upstream.OutputEvent
	first  chan struct{}
	resume chan struct{}
	once   atomic.Bool
	fail   bool
}

func (s *executeSink) Begin(upstream.OutputHead) error { return nil }
func (s *executeSink) Emit(event upstream.OutputEvent) error {
	if len(event.Data) > 0 && s.first != nil && s.once.CompareAndSwap(false, true) {
		close(s.first)
		<-s.resume
	}
	if s.fail {
		return errors.New("downstream closed")
	}
	event.Data = bytes.Clone(event.Data)
	s.events = append(s.events, event)
	_, _ = s.body.Write(event.Data)
	return nil
}

type executeBody struct {
	io.ReadCloser
	closes *atomic.Int32
}

func (b *executeBody) Close() error { b.closes.Add(1); return b.ReadCloser.Close() }
func localTarget(server *httptest.Server, mode ResponseMode, stream bool, closes *atomic.Int32) *Target {
	return &Target{AccountID: 41, Model: "gemini-fixture", Mode: mode, Exchange: ExchangeOptions{MaxRetries: 1, RequestIDHeader: "x-request-id", Build: func(ctx context.Context) (*http.Request, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(`{"contents":[]}`))
		return req, "x-request-id", err
	}, Do: func(req *http.Request) (*http.Response, error) {
		res, err := server.Client().Do(req)
		if res != nil {
			res.Body = &executeBody{ReadCloser: res.Body, closes: closes}
		}
		return res, err
	}, CheckPolicy: func(_ context.Context, res *http.Response) (bool, *http.Response) { return false, res }, ShouldRetry: func(int) bool { return false }, ReadError: func(res *http.Response) []byte { body, _ := io.ReadAll(res.Body); return body }, Observe: func(ExchangeNotice) {}, SetError: func(int, string, string) {}, Sanitize: func(s string) string { return s }, Message: func(b []byte) string { return string(b) }, Detail: func([]byte) string { return "" }, BuildError: func(err error) error { return err }, FinalError: func(message string) error { return errors.New(message) }}, Response: ResponseOptions{ReadBody: io.ReadAll, WriteHeaders: func(dst, src http.Header) {
		for key, values := range src {
			dst[key] = append([]string(nil), values...)
		}
	}, ObserveImages: func([]byte) {}, ReverseTools: func(b []byte) []byte { return b }, ClaudeError: func(_ int, _ string, m string) error { return errors.New(m) }, ChatError: func(_ int, _ string, m string) error { return errors.New(m) }, GoogleError: func(_ int, m string) error { return errors.New(m) }, CompatError: func(_ OpenAICompatProtocol, _ int, _ string, m string) error { return errors.New(m) }}}
}
func nativeInput(target *Target, stream bool) upstream.AttemptInput {
	id := protocol.ProtocolAnthropicMessages
	if target.Mode == NativeResponse {
		id = protocol.ProtocolGeminiGenerateContent
	}
	if target.Mode == OpenAIResponse {
		id = protocol.ProtocolOpenAIChatCompletions
		if target.OpenAIProtocol == OpenAICompatResponses {
			id = protocol.ProtocolOpenAIResponses
		}
	}
	return upstream.AttemptInput{Target: target, Protocol: id, Stream: stream, ResponseModel: "gemini-fixture", Body: []byte(`{"contents":[]}`)}
}
func TestExecuteGeminiNonStreamObservedZero(t *testing.T) {
	for _, kind := range []string{"messages", "native", "chat", "responses"} {
		t.Run(kind, func(t *testing.T) {
			payload := `{"candidates":[{"content":{"parts":[{"text":"visible"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0},"future":{"preserve":true}}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("x-request-id", "gemini-request")
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()
			var closes, releases atomic.Int32
			mode := MessagesResponse
			switch kind {
			case "native":
				mode = NativeResponse
			case "chat", "responses":
				mode = OpenAIResponse
			}
			target := localTarget(server, mode, false, &closes)
			if kind == "responses" {
				target.OpenAIProtocol = OpenAICompatResponses
			}
			target.Enter = func() (func(), error) { return func() { releases.Add(1) }, nil }
			sink := &executeSink{}
			result, err := (Executor{}).Execute(context.Background(), nativeInput(target, false), sink)
			require.NoError(t, err)
			require.True(t, result.HasUsage)
			require.True(t, result.Served)
			require.NotNil(t, result.FirstSemanticOutput)
			require.Nil(t, result.FirstTokenMs)
			require.Zero(t, result.Usage.InputTokens)
			require.Equal(t, "gemini-request", result.RequestID)
			require.Contains(t, sink.body.String(), "visible")
			require.EqualValues(t, 1, closes.Load())
			require.EqualValues(t, 1, releases.Load())
			require.True(t, sink.events[len(sink.events)-1].Semantic)
			if kind == "native" {
				require.Equal(t, payload, sink.body.String())
			}
		})
	}
}
func TestExecuteGeminiStreamingProgressAndPartial(t *testing.T) {
	for _, variant := range []int{0, 1, 2, 3} {
		mode := ResponseMode(variant)
		if variant == 3 {
			mode = OpenAIResponse
		}
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode=%d/partial=%v", variant, partial), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					if partial {
						w.Header().Set("Content-Length", "10000")
					}
					_, _ = io.WriteString(w, "data: {\"usageMetadata\":{\"promptTokenCount\":0}}\n\n")
					_ = http.NewResponseController(w).Flush()
					_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"visible\"}]}}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1}}\n\n")
					if !partial {
						_, _ = io.WriteString(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3}}\n\ndata: [DONE]\n\n")
					}
				}))
				defer server.Close()
				var closes atomic.Int32
				target := localTarget(server, mode, true, &closes)
				if variant == 3 {
					target.OpenAIProtocol = OpenAICompatResponses
				}
				sink := &executeSink{first: make(chan struct{}), resume: make(chan struct{})}
				done := make(chan struct{})
				var result upstream.AttemptResult
				var runErr error
				go func() {
					result, runErr = (Executor{}).Execute(context.Background(), nativeInput(target, true), sink)
					close(done)
				}()
				select {
				case <-sink.first:
				case <-time.After(3 * time.Second):
					t.Fatal("未取得渐进输出")
				}
				select {
				case <-done:
					t.Fatal("同步背压未生效")
				default:
				}
				close(sink.resume)
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("流未结束")
				}
				if partial {
					require.Error(t, runErr)
					require.Equal(t, 1, result.Usage.OutputTokens)
				} else {
					require.NoError(t, runErr)
					require.Equal(t, 3, result.Usage.OutputTokens)
				}
				require.True(t, result.HasUsage)
				require.True(t, result.Served)
				require.NotNil(t, result.FirstTokenMs)
				require.NotNil(t, result.FirstSemanticOutput)
				require.Contains(t, sink.body.String(), "visible")
				require.EqualValues(t, 1, closes.Load())
				semantic := false
				for _, event := range sink.events {
					semantic = semantic || event.Semantic
					if strings.Contains(string(event.Data), "promptTokenCount\":0") {
						require.False(t, event.Semantic)
					}
				}
				require.True(t, semantic)
			})
		}
	}
}

func TestExecuteGeminiCountFallbackIsNotUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	var closes atomic.Int32
	target := localTarget(server, NativeResponse, false, &closes)
	target.Exchange.ShouldRetry = func(int) bool { return true }
	target.Exchange.CountFallback = true
	target.Exchange.EstimateCount = func() int { return 17 }
	sink := &executeSink{}
	result, err := (Executor{}).Execute(context.Background(), nativeInput(target, false), sink)
	require.NoError(t, err)
	require.NotNil(t, result.EstimatedTokenCount)
	require.Equal(t, 17, *result.EstimatedTokenCount)
	require.False(t, result.HasUsage)
	require.False(t, result.Served)
	require.Zero(t, result.Usage.InputTokens)
	require.Contains(t, sink.body.String(), `"totalTokens":17`)
	require.EqualValues(t, 1, closes.Load())
}
func TestExecuteGeminiCancelledRequestDoesNotReachServer(t *testing.T) {
	var calls, closes, releases atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	target := localTarget(server, NativeResponse, false, &closes)
	target.Enter = func() (func(), error) { return func() { releases.Add(1) }, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (Executor{}).Execute(ctx, nativeInput(target, false), &executeSink{})
	require.Error(t, err)
	require.True(t, result.Cancelled)
	require.Zero(t, calls.Load())
	require.EqualValues(t, 1, releases.Load())
}
