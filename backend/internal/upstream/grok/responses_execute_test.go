package grok

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

// 本地 HTTP 验证原生恢复只重放一次，关闭前一响应后才读取后一响应。
func TestGrokResponsesExecuteReplayAndResourceOwnership(t *testing.T) {
	var calls, closes, active atomic.Int64
	var replay atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "Bearer fixture", r.Header.Get("Authorization"))
		if calls.Add(1) == 1 {
			require.Contains(t, string(data), "opaque-fixture")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"invalid_encrypted_content","message":"invalid encrypted content"}}`)
			return
		}
		require.NotContains(t, string(data), "opaque-fixture")
		require.EqualValues(t, 1, closes.Load())
		w.Header().Set("xai-request-id", "fixture-id")
		_, _ = io.WriteString(w, `{"id":"resp-fixture","usage":{"input_tokens":7,"output_tokens":2}}`)
	}))
	defer server.Close()
	target := &ResponsesTarget{

		AccountID: 19,

		Model: "grok-fixture",

		Enter: func() (func(), error) {
			active.Add(1)
			return func() { active.Add(-1) }, nil
		},

		Exchange: ResponsesExchange{

			Build: func(body []byte) (*http.Request, error) {
				return BuildResponsesRequest(context.Background(), body, ResponsesRequestOptions{URL: server.URL, Token: "fixture"})
			},

			Do: func(req *http.Request) (*http.Response, error) {
				resp, err := server.Client().Do(req)
				if resp != nil {
					resp.Body = &voiceBody{ReadCloser: resp.Body, closed: &closes}
				}
				return resp, err
			},

			ReadError: func(resp *http.Response) []byte {
				data, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				return data
			},

			OnReplay: func() { replay.Store(true) },
		},

		ReadResponse: func(resp *http.Response, _ upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			data, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Contains(t, string(data), "resp-fixture")
			return upstream.ResponsesObservation{Usage: &wire.ForwardUsage{InputTokens: 7, OutputTokens: 2}, HasUsage: true, ResponseID: "resp-fixture"}, nil
		},
	}
	body := []byte(`{"model":"grok-fixture","input":[{"type":"reasoning","encrypted_content":"opaque-fixture","summary":[{"type":"summary_text","text":"visible"}]}]}`)
	result, err := (ResponsesExecutor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIResponses, Body: body, Target: target}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
	require.EqualValues(t, 2, closes.Load())
	require.Zero(t, active.Load())
	require.True(t, replay.Load())
	require.Equal(t, "fixture-id", result.RequestID)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.True(t, result.HasUsage)
	// 仅响应 ID 不证明实际语义输出。
	require.False(t, result.Served)
}

// 新接口保留与读取错误并存的事实；不把错误变成成功，不按 ID 推断已服务。
func TestGrokResponsesExecutePreservesPartialObservation(t *testing.T) {
	failure := errors.New("fixture read failure")
	elapsed := 3 * time.Millisecond
	var closed atomic.Int64
	target := &ResponsesTarget{
		Exchange: ResponsesExchange{

			Build: func(body []byte) (*http.Request, error) {
				return http.NewRequest(http.MethodPost, "https://fixture.invalid", strings.NewReader(string(body)))
			},

			Do: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &voiceBody{ReadCloser: io.NopCloser(strings.NewReader("")), closed: &closed}}, nil
			},

			ReadError: func(*http.Response) []byte { return nil },
		},
		ReadResponse: func(*http.Response, upstream.AttemptInput, upstream.OutputSink) (upstream.ResponsesObservation, error) {
			return upstream.ResponsesObservation{

				Usage: &wire.ForwardUsage{InputTokens: 11, OutputTokens: 3},

				HasUsage: true,

				Served: true,

				HTTPCommitted: true,

				RetryCommitted: true,

				ClientDisconnected: true,

				FirstSemanticOutput: &elapsed,
			}, failure
		},
	}
	result, err := (ResponsesExecutor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIResponses, Target: target}, nil)
	require.ErrorIs(t, err, failure)
	require.True(t, result.Served)
	require.True(t, result.HasUsage)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.True(t, result.HTTPCommitted)
	require.True(t, result.RetryCommitted)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, &elapsed, result.FirstSemanticOutput)
	require.EqualValues(t, 1, closed.Load())
}
