package openai

import (
	"bytes"
	"context"
	"errors"
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

type embeddingsTestSink struct {
	head upstream.OutputHead
	body bytes.Buffer
}

func (s *embeddingsTestSink) Begin(head upstream.OutputHead) error { s.head = head; return nil }
func (s *embeddingsTestSink) Emit(event upstream.OutputEvent) error {
	_, err := s.body.Write(event.Data)
	return err
}

type embeddingsTestBody struct {
	io.ReadCloser
	closed *atomic.Int32
}

func (b *embeddingsTestBody) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }

// 本地 TLS 验证实际请求、同步输出及响应体先于活动释放，不连接真实供应商。
func TestEmbeddingsExecutorLocalTLSAndResourceOwnership(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests, closed, released atomic.Int32
			requestBody := []byte(`{"model":"fixture-embedding","input":["one","two"]}`)
			responseBody := `{"data":[{"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":7,"prompt_tokens_details":{"image_tokens":2}}}`
			if status != http.StatusOK {
				responseBody = `{"error":{"message":"fixture rate limit"}}`
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
				require.Equal(t, "fixture-agent", r.UserAgent())
				require.Equal(t, "final-override", r.Header.Get("X-Test-Order"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, requestBody, body)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-request-id", "fixture-request")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, responseBody)
			}))
			defer server.Close()
			failure := errors.New("fixture classified response")
			target := &EmbeddingsTarget{

				AccountID: 21,
				Model:     "fixture-embedding",
				URL:       server.URL,
				Token:     "fixture-token",
				UserAgent: "fixture-agent",

				ForwardHeaders: http.Header{"X-Test-Order": {"client"}},

				RequestContext: func(ctx context.Context) (context.Context, context.CancelFunc) { return ctx, func() {} },

				Enter: func() (func(), error) {
					return func() { require.EqualValues(t, 1, closed.Load()); released.Add(1) }, nil
				},

				ApplyHeaders: func(headers http.Header) { headers.Set("X-Test-Order", "final-override") },

				Do: func(request *http.Request) (*http.Response, error) {
					response, err := server.Client().Do(request)
					if err == nil {
						response.Body = &embeddingsTestBody{ReadCloser: response.Body, closed: &closed}
					}
					return response, err
				},

				TransportError: func(err error) error { return err },

				ReadErrorBody: func(response *http.Response) []byte {
					body, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					return body
				},

				HTTPError: func(response *http.Response, body []byte) error {
					require.Equal(t, status, response.StatusCode)
					require.Equal(t, responseBody, string(body))
					require.EqualValues(t, 1, closed.Load())
					return failure
				},

				ReadBody: io.ReadAll,

				ReadFailure: func(err error) error { return err },

				WriteHeaders: func(output, input http.Header) { output.Set("x-request-id", input.Get("x-request-id")) },
			}
			sink := &embeddingsTestSink{}
			result, err := (EmbeddingsExecutor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolEmbeddings, Body: requestBody, ResponseModel: "client-alias", Target: target}, sink)
			require.EqualValues(t, 1, requests.Load())
			require.EqualValues(t, 1, closed.Load())
			require.EqualValues(t, 1, released.Load())
			if status == http.StatusOK {
				require.NoError(t, err)
				require.True(t, result.HasUsage)
				require.True(t, result.Served)
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 2, result.ImageInputTokens)
				require.Equal(t, "client-alias", result.Model)
				require.Equal(t, "fixture-embedding", result.UpstreamModel)
				require.Equal(t, responseBody, sink.body.String())
				require.Equal(t, "fixture-request", sink.head.Header.Get("x-request-id"))
			} else {
				require.ErrorIs(t, err, failure)
				require.Empty(t, sink.body.String())
			}
			require.NotContains(t, target.String(), "fixture-token")
			require.False(t, strings.Contains(target.String(), server.URL))
		})
	}
}
