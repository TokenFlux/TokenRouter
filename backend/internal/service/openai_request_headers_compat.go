// 请求 Header 兼容层只投影身份、出站策略和原请求级状态，执行顺序由 upstream 拥有。
package service

import (
	"context"
	"net/http"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeResponsesRequestOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, token, targetURL string, isCodexCLI bool, routerMatch ...egress.TLSFingerprintRouterMatchResult) openai.ResponsesRequestOptions {
	return openai.ResponsesRequestOptions{
		URL: targetURL, ForwardHeaders: func() http.Header { return c.Request.Header },
		Authenticate: func(ctx context.Context) (http.Header, error) {
			return s.agentIdentity.Headers(ctx, account, token)
		},
		AccountHeaders: func(ctx context.Context, headers http.Header) error {
			return gatewayprovider.CredentialChatGPTHeaders(ctx, s.accountRepo, headers, account)
		},
		UsesCodex:      account.View().UsesOpenAICodexProtocol,
		IsCompact:      func() bool { return gatewayhttp.IsOpenAIResponsesCompactPath(c) },
		ForceCodexCLI:  func() bool { return s.cfg != nil && s.cfg.Gateway.ForceCodexCLI },
		AllowHeader:    func(name string) bool { return openaiAllowedHeaders[name] },
		GuardTurnState: func(headers http.Header) { s.turnStateHeaders.Guard(c, account, headers) },
		MessagesBridge: func(body []byte) bool {
			return isOpenAICompatMessagesBridgeContext(c) || isOpenAICompatMessagesBridgeBody(body)
		},
		Originator:     func() string { return resolveOpenAIUpstreamOriginator(c, isCodexCLI, routerMatch...) },
		CompactSession: func() string { return gatewayhttp.ResolveOpenAICompactSessionID(c) },
		APIKeyID:       func() int64 { return gatewayhttp.APIKeyIDFromContext(c) },
		IsolateSession: func(keyID int64, raw string) string {
			return openai.IsolateOpenAIUpstreamSessionID(keyID, accountprovider.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(c, account.View())), raw)
		},
		ApplyUserAgent: func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, false, routerMatch...) },
		ApplyAccountIdentity: func(headers http.Header) {
			openai.ApplyCodexAccountIdentityHeaders(headers, accountprovider.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(c, account.View())), gatewayhttp.APIKeyIDFromContext(c))
		},
		ApplyFingerprint: func(headers http.Header) { gatewayhttp.ApplyStagedCodexFingerprintHeaders(c, account.View(), headers) },
		OverrideHeaders:  bindAccountHeaders(account),
		OpenCodeSession:  func(headers http.Header) { applyOpenCodeSessionHeader(c, account, targetURL, headers) },
		BetaFeatures: func(headers http.Header) {
			gatewayhttp.ApplyOpenAICodexBetaFeatures(c, account != nil && account.View().IsOpenAIOAuthLike(), headers)
		},
		RoutingHint: func(headers http.Header, body []byte) { setOpenAICodexRoutingHintFromBody(headers, account, body) },
		Diagnostics: func(headers http.Header, body []byte) {
			logOpenAIRoutingDiagnosticsFromBody(ctx, account, "http", headers, body, "not_applicable")
		},
	}
}
