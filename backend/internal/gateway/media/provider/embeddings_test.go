package provider

import (
	"context"
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

type providerOutput struct {
	status int
	data   []byte
}

func (s *providerOutput) Begin(head upstream.OutputHead) error { s.status = head.Status; return nil }
func (s *providerOutput) Emit(event upstream.OutputEvent) error {
	s.data = append(s.data, event.Data...)
	return nil
}

type trackedMediaBody struct {
	io.ReadCloser
	closed *atomic.Bool
}

func (b trackedMediaBody) Close() error { b.closed.Store(true); return b.ReadCloser.Close() }

// 本地 HTTP 夹具验证目标投影、凭据 Header、原报文和响应关闭，不访问供应商环境。
func TestEmbeddingsAdapterOwnsNativeTargetAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer sensitive-fixture", r.Header.Get("Authorization"))
		require.Equal(t, "custom", r.Header.Get("X-Override"))
		require.Equal(t, "forwarded", r.Header.Get("X-Forwarded-Test"))
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"upstream","input":["a","b"]}`, string(data))
		w.Header().Set("X-Request-Id", "embedding-id")
		_, _ = io.WriteString(w, `{"data":[{"embedding":[1,2]}],"usage":{"prompt_tokens":4}}`)
	}))
	defer server.Close()
	var closed atomic.Bool
	var ended atomic.Bool
	options := EmbeddingsOptions{AccountID: 3, Model: "upstream", URL: server.URL, Token: "sensitive-fixture", ForwardHeaders: http.Header{"X-Forwarded-Test": []string{"forwarded"}}, RequestContext: func(ctx context.Context) (context.Context, context.CancelFunc) { return ctx, func() {} }, ApplyHeaders: func(headers http.Header) { headers.Set("X-Override", "custom") }, Enter: func() (func(), error) { return func() { require.True(t, closed.Load()); ended.Store(true) }, nil }, Do: func(r *http.Request) (*http.Response, error) {
		resp, err := server.Client().Do(r)
		if resp != nil {
			resp.Body = trackedMediaBody{resp.Body, &closed}
		}
		return resp, err
	}, ReadBody: io.ReadAll, WriteHeaders: func(dst, src http.Header) {
		for k, v := range src {
			dst[k] = append([]string(nil), v...)
		}
	}}
	require.NotContains(t, fmt.Sprintf("%#v", options), "sensitive-fixture")
	output := &providerOutput{}
	result, err := (Embeddings{Options: options}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolEmbeddings, Body: []byte(`{"model":"upstream","input":["a","b"]}`), ResponseModel: "client"}, output)
	require.NoError(t, err)
	require.Equal(t, 200, output.status)
	require.True(t, strings.Contains(string(output.data), "embedding"))
	require.Equal(t, "embedding-id", result.RequestID)
	require.Equal(t, "client", result.Model)
	require.Equal(t, "upstream", result.UpstreamModel)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.True(t, closed.Load())
	require.True(t, ended.Load())
}
