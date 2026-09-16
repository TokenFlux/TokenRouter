package service

import (
	"context"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// anthropicPassthroughAdapter 复用旧构造参数与唯一原生平台交换，不保存另一份解析或流状态。
type anthropicPassthroughAdapter struct{ *messageExecutionAdapter }

func (a *anthropicPassthroughAdapter) TokenKind() string { return a.tokenType }
func (a *anthropicPassthroughAdapter) ResolveProxy() {
	if a.account.ProxyID != nil && a.account.Proxy != nil {
		a.proxyURL = a.account.Proxy.URL()
	}
}
func (a *anthropicPassthroughAdapter) MarkPassthrough() {
	if a.c != nil {
		a.c.Set("anthropic_passthrough", true)
	}
}
func (a *anthropicPassthroughAdapter) ExecutePassthrough(ctx context.Context, in *forwardcore.APIKeyInput, h forwardcore.MessageHooks) (upstream.AttemptResult, error) {
	options := a.s.anthropicPassthroughExchangeOptions(ctx, a.c, a.account, a.token, a.proxyURL, in)
	target := &claude.Target{
		AccountID: a.account.ID, Model: in.RequestModel, Passthrough: true, Exchange: options,
		Response: a.s.anthropicResponseOptions(ctx, a.c, a.account, in.RequestModel, true), StartedAt: in.StartTime,
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
		Target:        target}, gatewayhttp.ResponseSink{Writer: a.c.Writer})
}
