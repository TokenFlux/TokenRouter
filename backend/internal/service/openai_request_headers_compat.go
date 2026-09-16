// 请求 Header 兼容层只投影身份、出站策略和原请求级状态，执行顺序由 upstream 拥有。
package service

import (
	"context"
	"net/http"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeResponsesRequestOptions(ctx context.Context, c *gin.Context, account *Account, token, targetURL string, isCodexCLI bool, routerMatch ...TLSFingerprintRouterMatchResult) native.ResponsesRequestOptions {
	return native.ResponsesRequestOptions{
		URL: targetURL, ForwardHeaders: func() http.Header { return c.Request.Header },
		Authenticate: func(ctx context.Context) (http.Header, error) {
			return s.buildOpenAIAuthenticationHeaders(ctx, account, token)
		},
		AccountHeaders: func(ctx context.Context, headers http.Header) error {
			return resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, account)
		},
		UsesCodex:      account.UsesOpenAICodexProtocol,
		IsCompact:      func() bool { return isOpenAIResponsesCompactPath(c) },
		ForceCodexCLI:  func() bool { return s.cfg != nil && s.cfg.Gateway.ForceCodexCLI },
		AllowHeader:    func(name string) bool { return openaiAllowedHeaders[name] },
		GuardTurnState: func(headers http.Header) { s.guardOpenAICodexTurnStateEcho(c, account, headers) },
		MessagesBridge: func(body []byte) bool {
			return isOpenAICompatMessagesBridgeContext(c) || isOpenAICompatMessagesBridgeBody(body)
		},
		Originator:     func() string { return resolveOpenAIUpstreamOriginator(c, isCodexCLI, routerMatch...) },
		CompactSession: func() string { return resolveOpenAICompactSessionID(c) },
		APIKeyID:       func() int64 { return getAPIKeyIDFromContext(c) },
		IsolateSession: func(keyID int64, raw string) string {
			return isolateOpenAIUpstreamSessionID(keyID, codexAccountIdentitySource(c, account), raw)
		},
		ApplyUserAgent: func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, false, routerMatch...) },
		ApplyAccountIdentity: func(headers http.Header) {
			applyCodexAccountIdentityHeaders(headers, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c))
		},
		ApplyFingerprint: func(headers http.Header) { applyStagedCodexFingerprintHeaders(c, account, headers) },
		OverrideHeaders:  account.ApplyHeaderOverrides,
		OpenCodeSession:  func(headers http.Header) { applyOpenCodeSessionHeader(c, account, targetURL, headers) },
		BetaFeatures:     func(headers http.Header) { applyOpenAICodexBetaFeatures(c, account, headers) },
		RoutingHint:      func(headers http.Header, body []byte) { setOpenAICodexRoutingHintFromBody(headers, account, body) },
		Diagnostics: func(headers http.Header, body []byte) {
			logOpenAIRoutingDiagnosticsFromBody(ctx, account, "http", headers, body, "not_applicable")
		},
	}
}
