package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

type jevProbeTransport struct {
	httpclient.UpstreamTransport
	request *http.Request
	body    []byte
}
type jevProbeEvents struct{ events []provider.TestEvent }

func (t *jevProbeTransport) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	t.request = req
	t.body, _ = io.ReadAll(req.Body)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"model":"jev-1.13.0","answers":{"available":{"type":"noul","noul":1}},"usage":{"input_tokens":11,"output_tokens":2}}`))}, nil
}
func (s *jevProbeEvents) Begin(context.Context, bool) error { return nil }
func (s *jevProbeEvents) Emit(_ context.Context, event provider.TestEvent) error {
	s.events = append(s.events, event)
	return nil
}

// TestJevProviderProbeUsesSystemOne 检查后台和手动测试使用相同的原生请求及事件格式。
func TestJevProviderProbeUsesSystemOne(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		transport := &jevProbeTransport{}
		component := &JevProviderTest{Transport: transport, ValidateURL: func(url string) (string, error) { return url, nil }}
		target := component.Target(&provider.Record{ID: 7, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "probe-key"}})
		sink := &jevProbeEvents{}
		require.NoError(t, target.Execute(context.Background(), provider.PreparedTestRequest{TestRequest: provider.TestRequest{Automatic: automatic}}, sink))
		require.Equal(t, "https://api.typesafe.ai/v1/systemone", transport.request.URL.String())
		require.Equal(t, "Bearer probe-key", transport.request.Header.Get("Authorization"))
		require.Contains(t, string(transport.body), `"model":"jev-latest"`)
		require.Len(t, sink.events, 3)
		require.Equal(t, "test_start", sink.events[0].Type)
		require.Contains(t, sink.events[1].Text, `"noul":1`)
		require.True(t, sink.events[2].Success)
	}
}
