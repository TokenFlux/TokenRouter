package provider

import (
	"context"
	"encoding/json"
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
	request  *http.Request
	body     []byte
	response string
}
type jevProbeEvents struct{ events []provider.TestEvent }

func (t *jevProbeTransport) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	t.request = req
	t.body, _ = io.ReadAll(req.Body)
	body := t.response
	if body == "" {
		body = `{"model":"jev-1.13.0","answers":{"available":{"type":"noul","noul":1}},"usage":{"input_tokens":11,"output_tokens":2}}`
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}

// TestJevDecisionTestForwardsQuestions 检查混合问题、结构化状态和扩展字段随测试请求发送，型号按提供商映射解析。
func TestJevDecisionTestForwardsQuestions(t *testing.T) {
	transport := &jevProbeTransport{response: `{"model":"jev-1.13.0","answers":{"ready":{"type":"noul","noul":0.9},"route":{"type":"choice","choice":"fast","confidence":0.8,"probabilities":{"fast":0.8,"slow":0.2}},"quality":{"type":"score","score":1,"confidence":0.9,"probabilities":{"0":0.1,"1":0.9},"legend":"low to high"}},"usage":{"input_tokens":23,"output_tokens":0}}`}
	component := &JevProviderTest{Transport: transport, ValidateURL: func(url string) (string, error) { return url, nil }}
	target := component.Target(&provider.Record{ID: 7, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "probe-key", "model_mapping": map[string]any{"alias": "jev-preview"}}})
	body := json.RawMessage(`{"model":"ignored","state":{"available":true},"questions":{"ready":{"type":"noul","instructions":"Available?"},"route":{"type":"choice","instructions":"Choose a route","criteria":{"fast":"Fast","slow":"Slow"}},"quality":{"type":"score","instructions":"Rate quality","criteria":["Low","High"]}},"seed":42}`)
	sink := &jevProbeEvents{}
	require.NoError(t, target.Execute(context.Background(), provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "alias", SystemOne: body}}, sink))
	var sent map[string]any
	require.NoError(t, json.Unmarshal(transport.body, &sent))
	require.Equal(t, "jev-preview", sent["model"])
	require.Equal(t, float64(42), sent["seed"])
	require.Equal(t, map[string]any{"available": true}, sent["state"])
	require.Len(t, sent["questions"], 3)
	complete := sink.events[len(sink.events)-1]
	require.True(t, complete.Success)
	require.Equal(t, "jev-1.13.0", complete.Model)
	data, ok := complete.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, data["usage_valid"])
	response, ok := data["response"].(json.RawMessage)
	require.True(t, ok)
	require.JSONEq(t, transport.response, string(response))
}

// TestJevDecisionTestRejectsInvalidPayload 检查无效表单在发出 HTTP 请求前失败。
func TestJevDecisionTestRejectsInvalidPayload(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"state":true,"questions":{}}`, `{"state":"ok","questions":{"x":{"type":"score","instructions":"Rate","criteria":["one"]}}}`, `{"stream":true,"state":"ok","questions":{"x":{"type":"noul","instructions":"Ready?"}}}`} {
		t.Run(body, func(t *testing.T) {
			transport := &jevProbeTransport{}
			component := &JevProviderTest{Transport: transport, ValidateURL: func(url string) (string, error) { return url, nil }}
			target := component.Target(&provider.Record{Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test"}})
			require.Error(t, target.Execute(context.Background(), provider.PreparedTestRequest{TestRequest: provider.TestRequest{SystemOne: json.RawMessage(body)}}, &jevProbeEvents{}))
			require.Nil(t, transport.request)
		})
	}
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
