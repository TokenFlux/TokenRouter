package messageforward

import (
	"context"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// anthropicPassthroughAdapter 保留直通请求的代理解析与 Header 规则。
type anthropicPassthroughAdapter struct{ *attempt }

func (a *anthropicPassthroughAdapter) TokenKind() string { return a.tokenType }
func (a *anthropicPassthroughAdapter) ResolveProxy() {
	if a.account.Record.ProxyID != nil && a.account.Record.Proxy != nil {
		a.proxyURL = a.account.Record.Proxy.URL()
	}
}
func (a *anthropicPassthroughAdapter) MarkPassthrough() {
	a.c.MarkPassthrough()
}
func (a *anthropicPassthroughAdapter) ExecutePassthrough(ctx context.Context, in *forwardcore.APIKeyInput, h forwardcore.MessageHooks) (upstream.AttemptResult, error) {
	options := a.s.passthroughExchange(a.c, a.state, a.account, a.token, a.proxyURL, in)
	target := &claude.Target{
		AccountID: a.account.Record.ID, Model: in.RequestModel, Passthrough: true, Exchange: options,
		Response: a.s.responseOptions(ctx, a.c, a.state, a.account, in.RequestModel, true), StartedAt: in.StartTime,
		BeforeResponse: func(ctx context.Context, resp *http.Response, wire []byte) (bool, error) {
			a.response = resp
			return h.Before(ctx, &forwardcore.ExchangeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, RequestID: resp.Header.Get("x-request-id")}, wire)
		},
		OnWireBody: h.Wire,
		OnStream: func(result *claude.StreamResult, _ error) {
			if result != nil {
				h.Stream(&forwardcore.StreamOutcome{Usage: result.Usage, FirstTokenMs: result.FirstTokenMs, ClientDisconnect: result.ClientDisconnect})
			}
		},
	}
	return (claude.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolAnthropicMessages,
		Body:          in.Body,
		ResponseModel: in.OriginalModel,
		Stream:        in.RequestStream,
		Target:        target}, a.c.Sink())
}

// passthrough 使用独立 attempt，保持直通分支自己的请求准备时点。
func (r *Runtime) passthrough(ctx context.Context, output HTTPBoundary, target *gatewayprovider.ExecutionAccount, input forwardcore.APIKeyInput) (*forwardcore.Result, error) {
	adapter := &anthropicPassthroughAdapter{attempt: newAttempt(r, output, target)}
	return forwardcore.APIKeyPassthrough(ctx, adapter, adapter.input(), input)
}
