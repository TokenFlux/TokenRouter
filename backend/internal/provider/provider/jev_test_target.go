package provider

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/jev"
)

// JevProviderTest 使用网关相同的执行器进行手动和后台探测。
type JevProviderTest struct {
	Transport   httpclient.UpstreamTransport
	ValidateURL func(string) (string, error)
}

type jevTestTarget struct {
	executor *JevProviderTest
	record   *provider.Record
}

// Target 固定本次测试使用的提供商配置。
func (s *JevProviderTest) Target(value *provider.Record) provider.TestTarget {
	return jevTestTarget{s, value}
}

// Information 返回测试使用的提供商快照。
func (t jevTestTarget) Information() provider.TestTargetInfo {
	return provider.TestTargetInfo{ProviderSnapshot: t.record.RoutingSnapshot()}
}

// Execute 执行 Noul 探测，输出答案和耗时。
func (t jevTestTarget) Execute(ctx context.Context, request provider.PreparedTestRequest, sink provider.TestEventSink) error {
	run := NewTestRun(ctx, nil, sink)
	defer run.Cancel()
	output := TestStreamOutput{}
	if err := provider.ValidateJevCredentials(t.record); err != nil {
		return output.Error(run, err.Error())
	}
	base, err := t.executor.ValidateURL(t.record.GetJevBaseURL())
	if err != nil {
		return output.Error(run, "Invalid Jev base URL")
	}
	model := request.Model
	if model == "" {
		model = "jev-latest"
	}
	model = mappedTestModel(t.record, model)
	proxy := ""
	if t.record.Proxy != nil {
		proxy = t.record.Proxy.URL()
	}
	run.Begin(true)
	output.SendEvent(run, provider.TestEvent{Type: "test_start", Model: model})
	target := &jev.Target{
		ProviderID: t.record.ID, URL: jev.EndpointURL(base, "systemone"), Model: model, Token: t.record.GetCredential("api_key"),
		ApplyHeaders: func(headers http.Header) { ApplyProviderHeaderOverrides(t.record, headers) },
		Do: func(req *http.Request) (*http.Response, error) {
			return t.executor.Transport.Do(req, proxy, t.record.ID, t.record.Concurrency)
		},
	}
	result, err := (jev.Executor{}).Execute(run.Context, upstream.AttemptInput{Protocol: protocol.ProtocolSystemOne, Body: systemone.ProbeBody(model, request.Prompt), Target: target}, nil)
	if err != nil {
		return output.Error(run, "SystemOne test failed: "+err.Error())
	}
	output.SendEvent(run, provider.TestEvent{Type: "content", Text: string(result.MediaBody)})
	data := map[string]any{"duration_ms": result.Duration.Milliseconds(), "usage_valid": result.HasUsage, "response": json.RawMessage(result.MediaBody)}
	output.SendEvent(run, provider.TestEvent{Type: "test_complete", Model: result.UpstreamResponseModel, Success: true, Data: data})
	return run.Result(nil)
}
