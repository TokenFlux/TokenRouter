// 本地 HTTP 验证各原生执行入口的实际输出、用量与资源关闭，不重放到供应商。
package antigravity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

type executionSink struct {
	body   strings.Builder
	events []upstream.OutputEvent
	fail   bool
}

func (s *executionSink) Begin(upstream.OutputHead) error { return nil }
func (s *executionSink) Emit(e upstream.OutputEvent) error {
	s.events = append(s.events, e)
	if s.fail && e.Semantic {
		return io.ErrClosedPipe
	}
	_, _ = s.body.Write(e.Data)
	return nil
}

type executionBody struct {
	io.ReadCloser
	closes *atomic.Int32
}

func (b *executionBody) Close() error { b.closes.Add(1); return b.ReadCloser.Close() }
func executionResponseOptions() ResponseOptions {
	return ResponseOptions{ReverseTools: func(b []byte) []byte { return b }, ClaudeError: func(status int, kind, message string) error { return fmt.Errorf("%d %s %s", status, kind, message) }, CompatError: func(status int, kind, message string) error { return fmt.Errorf("%d %s %s", status, kind, message) }, MapCollectionError: func(err error) error { return err }, Failover: func(body []byte) error { return fmt.Errorf("failover: %s", body) }, IsFailover: func(err error) bool { return strings.HasPrefix(err.Error(), "failover:") }, MarkCommitted: func() {}}
}

func TestExecuteProtocolOutputsAndRelease(t *testing.T) {
	cases := []struct {
		name string
		mode ResponseMode
		wire protocol.ProtocolID
	}{{"claude", ModeClaudeResponse, protocol.ProtocolAnthropicMessages}, {"gemini", ModeGeminiResponse, protocol.ProtocolGeminiGenerateContent}, {"chat", ModeChatResponse, protocol.ProtocolOpenAIChatCompletions}, {"responses", ModeResponsesResponse, protocol.ProtocolOpenAIResponses}}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("x-request-id", "fixture-request")
					_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":1,\"totalTokenCount\":4}}}\n\n")
				}))
				defer server.Close()
				var closes, releases atomic.Int32
				sink := &executionSink{}
				target := &Target{AccountID: 12, Model: "gemini-fixture", Mode: tc.mode, IncludeUsage: true, Response: executionResponseOptions(), Enter: func() (func(), error) { return func() { releases.Add(1) }, nil }, Exchange: func(ctx context.Context) (*http.Response, error) {
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
					if err != nil {
						return nil, err
					}
					resp, err := server.Client().Do(req)
					if resp != nil {
						resp.Body = &executionBody{ReadCloser: resp.Body, closes: &closes}
					}
					return resp, err
				}}
				result, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: tc.wire, ResponseModel: "public-model", Stream: stream, Target: target}, sink)
				require.NoError(t, err)
				require.Contains(t, sink.body.String(), "hello")
				require.Equal(t, "fixture-request", result.RequestID)
				require.Equal(t, 3, result.Usage.InputTokens)
				require.Equal(t, 1, result.Usage.OutputTokens)
				require.True(t, result.HasUsage)
				require.True(t, result.Served)
				require.NotNil(t, result.FirstSemanticOutput)
				require.EqualValues(t, 1, closes.Load())
				require.EqualValues(t, 1, releases.Load())
				{
					var semantic bool
					for _, event := range sink.events {
						semantic = semantic || event.Semantic
					}
					require.True(t, semantic, "实际输出必须报告语义内容")
				}
			})
		}
	}
}

func TestExecuteDisconnectedStreamStillObservesTailUsage(t *testing.T) {
	body := "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}]}}\n\ndata: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":4}}}\n\n"
	var closes, releases atomic.Int32
	target := &Target{Mode: ModeClaudeResponse, Response: executionResponseOptions(), Enter: func() (func(), error) { return func() { releases.Add(1) }, nil }, Exchange: func(context.Context) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &executionBody{ReadCloser: io.NopCloser(strings.NewReader(body)), closes: &closes}}, nil
	}}
	result, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, ResponseModel: "fixture", Stream: true, Target: target}, &executionSink{fail: true})
	require.NoError(t, err)
	require.True(t, result.ClientDisconnect)
	require.True(t, result.HasUsage)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.EqualValues(t, 1, closes.Load())
	require.EqualValues(t, 1, releases.Load())
}

func TestExecuteFailedBeforeResponseReleasesOwnedBody(t *testing.T) {
	var closes, releases atomic.Int32
	want := errors.New("policy rejected")
	target := &Target{Response: executionResponseOptions(), Enter: func() (func(), error) { return func() { releases.Add(1) }, nil }, Exchange: func(context.Context) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: &executionBody{ReadCloser: io.NopCloser(strings.NewReader("denied")), closes: &closes}}, nil
	}, BeforeResponse: func(context.Context, *http.Response) (bool, error) { return true, want }}
	result, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, Target: target}, &executionSink{})
	require.ErrorIs(t, err, want)
	require.False(t, result.Served)
	require.False(t, result.HasUsage)
	require.EqualValues(t, 1, closes.Load())
	require.EqualValues(t, 1, releases.Load())
}

// 静态上游保留双凭据 Header、未知请求字段和原透传输出。
func TestExecuteStaticUpstreamWire(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/messages", r.URL.Path)
				require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
				require.Equal(t, "fixture-key", r.Header.Get("x-api-key"))
				require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Contains(t, string(body), `"custom_field":"kept"`)
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":3,"output_tokens":1}}`)
				}
			}))
			defer server.Close()
			body := []byte(fmt.Sprintf(`{"model":"fixture","stream":%v,"max_tokens":32,"messages":[],"custom_field":"kept"}`, stream))
			req, model, actualStream, err := BuildStaticRequest(context.Background(), body, StaticRequestInput{BaseURL: server.URL, APIKey: "fixture-key", Version: "2023-06-01", Sanitize: func(b []byte, _ string) ([]byte, bool) { return b, false }})
			require.NoError(t, err)
			require.Equal(t, stream, actualStream)
			var closes atomic.Int32
			target := &Target{Mode: ModeStaticClaudeResponse, Response: executionResponseOptions(), Exchange: func(context.Context) (*http.Response, error) {
				resp, err := server.Client().Do(req)
				if resp != nil {
					resp.Body = &executionBody{ReadCloser: resp.Body, closes: &closes}
				}
				return resp, err
			}}
			sink := &executionSink{}
			result, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, ResponseModel: model, Stream: stream, Target: target}, sink)
			require.NoError(t, err)
			require.Contains(t, sink.body.String(), "hello")
			require.True(t, result.HasUsage)
			require.True(t, result.Served)
			require.Equal(t, 3, result.Usage.InputTokens)
			require.Equal(t, 1, result.Usage.OutputTokens)
			require.EqualValues(t, 1, closes.Load())
		})
	}
}

// 新执行结果在读取失败时保留分批观测，不改变旧入站的失败结算规则。
type failedAfterPayload struct{ io.Reader }

func (r failedAfterPayload) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}
func TestExecutePartialUsageSurvivesReadFailure(t *testing.T) {
	body := "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}],\"usageMetadata\":{\"promptTokenCount\":9}}}\n\ndata: {\"response\":{\"usageMetadata\":{\"candidatesTokenCount\":4}}}\n\n"
	target := &Target{Mode: ModeClaudeResponse, Response: executionResponseOptions(), Exchange: func(context.Context) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(failedAfterPayload{Reader: strings.NewReader(body)})}, nil
	}}
	result, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, Stream: true, ResponseModel: "fixture", Target: target}, &executionSink{})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.True(t, result.Served)
	require.True(t, result.HasUsage)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
}
