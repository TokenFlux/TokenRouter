// 这些契约直接运行新执行入口，验证逐段输出、观测事实和资源关闭。
package anthropic

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

type executionSink struct {
	body    bytes.Buffer
	events  []upstream.OutputEvent
	entered chan struct{}
	resume  chan struct{}
	once    atomic.Bool
	fail    bool
}

func (s *executionSink) Begin(upstream.OutputHead) error { return nil }
func (s *executionSink) Emit(event upstream.OutputEvent) error {
	if len(event.Data) > 0 && s.entered != nil && s.once.CompareAndSwap(false, true) {
		close(s.entered)
		<-s.resume
	}
	if s.fail {
		return errors.New("client disconnected")
	}
	event.Data = bytes.Clone(event.Data)
	s.events = append(s.events, event)
	_, _ = s.body.Write(event.Data)
	return nil
}

type countedResponse struct {
	io.ReadCloser
	closed *atomic.Int32
}

func (r *countedResponse) Close() error { r.closed.Add(1); return r.ReadCloser.Close() }
func executionTarget(server *httptest.Server, stream, passthrough bool, closed *atomic.Int32) *Target {
	return &Target{AccountID: 7, Model: "claude-test", Passthrough: passthrough, Exchange: ExchangeOptions{Stream: stream, MaxAttempts: 1, MaxElapsed: time.Second,
		Context: func(ctx context.Context, _ bool) (context.Context, context.CancelFunc) { return ctx, func() {} },
		Build: func(ctx context.Context, body []byte) (*http.Request, []byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(body))
			return req, body, err
		},
		Do: func(req *http.Request) (*http.Response, error) {
			res, err := server.Client().Do(req)
			if res != nil {
				res.Body = &countedResponse{ReadCloser: res.Body, closed: closed}
			}
			return res, err
		},
		TransportError: func(_ context.Context, err error, _ string) error { return err },
	}, Response: ResponseOptions{ReadBody: io.ReadAll, InvalidJSON: func(_ context.Context, _ *http.Response, _ []byte, err error) error { return err }}}
}
func executeInput(target *Target, stream bool) upstream.AttemptInput {
	return upstream.AttemptInput{Target: target, Protocol: protocol.ProtocolAnthropicMessages, ResponseModel: "claude-test", Body: []byte(`{"model":"claude-test"}`), Stream: stream}
}

func TestExecuteNonStreamObservedZero(t *testing.T) {
	for _, pass := range []bool{false, true} {
		t.Run(fmt.Sprint(pass), func(t *testing.T) {
			payload := `{"id":"msg","type":"message","model":"claude-test","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":0,"output_tokens":0},"future":{"keep":true}}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("x-request-id", "local-request")
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()
			var closed atomic.Int32
			sink := &executionSink{}
			result, err := (Executor{}).Execute(context.Background(), executeInput(executionTarget(server, false, pass, &closed), false), sink)
			require.NoError(t, err)
			require.True(t, result.HasUsage)
			require.True(t, result.Served)
			require.NotNil(t, result.FirstSemanticOutput)
			require.Nil(t, result.FirstTokenMs)
			require.Equal(t, "local-request", result.RequestID)
			require.Equal(t, payload, sink.body.String())
			require.EqualValues(t, 1, closed.Load())
			require.True(t, sink.events[0].Semantic)
		})
	}
}
func TestExecuteStreamingProgressAndPartialFailure(t *testing.T) {
	for _, pass := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("pass=%v/partial=%v", pass, partial), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0}}}\n\n")
					_ = http.NewResponseController(w).Flush()
					_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"visible\"}}\n\n")
					if partial {
						_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"failed\"}}\n\n")
					} else {
						_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					}
				}))
				defer server.Close()
				var closed atomic.Int32
				sink := &executionSink{entered: make(chan struct{}), resume: make(chan struct{})}
				done := make(chan struct{})
				var result upstream.AttemptResult
				var err error
				go func() {
					result, err = (Executor{}).Execute(context.Background(), executeInput(executionTarget(server, true, pass, &closed), true), sink)
					close(done)
				}()
				select {
				case <-sink.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("首段未渐进到达")
				}
				select {
				case <-done:
					t.Fatal("慢客户端尚未释放时执行已返回")
				default:
				}
				close(sink.resume)
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("执行未收尾")
				}
				if partial && !pass {
					require.Error(t, err)
				} else if !partial {
					require.NoError(t, err)
				}
				// passthrough 对终态 error 保持原返回行为，不能借新接口改变旧策略。
				require.True(t, result.HasUsage)
				require.True(t, result.Served)
				require.NotNil(t, result.FirstTokenMs)
				require.NotNil(t, result.FirstSemanticOutput)
				require.Contains(t, sink.body.String(), "visible")
				require.EqualValues(t, 1, closed.Load())
				sawSemantic := false
				for _, event := range sink.events {
					if event.Semantic {
						sawSemantic = true
					}
					if strings.Contains(string(event.Data), "message_start") {
						require.False(t, event.Semantic)
					}
				}
				require.True(t, sawSemantic)
			})
		}
	}
}
func TestExecuteNonStreamCancellationClosesAttempt(t *testing.T) {
	entered := make(chan struct{})
	stopServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-stopServer:
		}
	}))
	defer server.Close()
	defer close(stopServer)
	var closed, release atomic.Int32
	target := executionTarget(server, false, false, &closed)
	target.Enter = func() (func(), error) { return func() { release.Add(1) }, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var cancelled bool
	go func() {
		result, err := (Executor{}).Execute(ctx, executeInput(target, false), &executionSink{})
		cancelled = result.Cancelled
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("请求未进入本地上游")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.True(t, cancelled)
	case <-time.After(3 * time.Second):
		t.Fatal("取消未返回")
	}
	require.EqualValues(t, 1, release.Load())
}
