package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIRequests) Target(c *gin.Context, a *gatewayprovider.ExecutionAccount, passthrough bool) forward.RequestTargetOptions {
	oauth := a.Record.Type == capability.AccountTypeOAuth || a.Record.Type == capability.AccountTypeSetupToken && (!passthrough || a.View().IsOpenAIOAuthLike())
	return forward.RequestTargetOptions{
		OAuthTarget: oauth, APIKey: a.Record.Type == capability.AccountTypeAPIKey, DefaultURL: openaiPlatformAPIURL, CodexURL: chatgptCodexURL,
		BaseURL: func() string {
			base := gatewayprovider.ExecutionProtocolTarget(a).GetOpenAIBaseURL()
			if _, unified := a.Record.Credentials[account.UpstreamProtocolsKey]; gatewayprovider.ExecutionProtocolTarget(a).UsesNativeCNResponses() && (unified || gatewayprovider.ExecutionProtocolTarget(a).IsAdaptiveAPIProtocol()) {
				base = gatewayprovider.ExecutionProtocolTarget(a).GetCNProtocolBaseURL(account.APIProtocolResponses)
			}
			return base
		}, Validate: s.ValidateBaseURL, FromBase: func(base string) string { return forward.ResponsesEndpoint(a.Record.Platform, base) }, AppendSuffix: func(base string) string {
			return openai.AppendResponsesPathSuffix(base, OpenAIResponsesRequestPathSuffix(c))
		},
	}
}
func (s *OpenAIRequests) ResponseOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, token, targetURL string, isCodexCLI bool, routerMatch ...egress.TLSFingerprintRouterMatchResult) openai.ResponsesRequestOptions {
	return openai.ResponsesRequestOptions{
		URL: targetURL, ForwardHeaders: func() http.Header { return c.Request.Header },
		Authenticate: func(ctx context.Context) (http.Header, error) {
			return s.Identity.Headers(ctx, account, token)
		},
		AccountHeaders: func(ctx context.Context, headers http.Header) error {
			return gatewayprovider.CredentialChatGPTHeaders(ctx, s.Accounts, headers, account)
		},
		UsesCodex:      account.View().UsesOpenAICodexProtocol,
		IsCompact:      func() bool { return IsOpenAIResponsesCompactPath(c) },
		ForceCodexCLI:  func() bool { return s.Options.ForceCLI },
		AllowHeader:    func(name string) bool { return openaiAllowedHeaders[name] },
		GuardTurnState: func(headers http.Header) { s.Turns.Guard(c, account, headers) },
		MessagesBridge: func(body []byte) bool {
			return IsOpenAICompatMessagesBridgeContext(c) || gatewayprovider.IsOpenAICompatMessagesBridgeBody(body)
		},
		Originator:     func() string { return ResolveOpenAIUpstreamOriginator(c, isCodexCLI, routerMatch...) },
		CompactSession: func() string { return ResolveOpenAICompactSessionID(c) },
		APIKeyID:       func() int64 { return APIKeyIDFromContext(c) },
		IsolateSession: func(keyID int64, raw string) string {
			return openai.IsolateOpenAIUpstreamSessionID(keyID, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(c, account.View())), raw)
		},
		ApplyUserAgent: func(req *http.Request) { s.ApplyUserAgent(ctx, c, account, req, false, routerMatch...) },
		ApplyAccountIdentity: func(headers http.Header) {
			openai.ApplyCodexAccountIdentityHeaders(headers, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(c, account.View())), APIKeyIDFromContext(c))
		},
		ApplyFingerprint: func(headers http.Header) { ApplyStagedCodexFingerprintHeaders(c, account.View(), headers) },
		OverrideHeaders:  gatewayprovider.BindExecutionHeaders(account),
		OpenCodeSession:  func(headers http.Header) { ApplyOpenCodeSessionHeader(c, account, targetURL, headers) },
		BetaFeatures: func(headers http.Header) {
			ApplyOpenAICodexBetaFeatures(c, account != nil && account.View().IsOpenAIOAuthLike(), headers)
		},
		RoutingHint: func(headers http.Header, body []byte) { SetOpenAICodexRoutingHintFromBody(headers, account, body) },
		Diagnostics: func(headers http.Header, body []byte) {
			LogOpenAIRoutingDiagnosticsFromBody(ctx, account, "http", headers, body, "not_applicable")
		},
	}
}

// Build 保留旧签名，仅投影目标与原生请求选项。
func (s *OpenAIRequests) Build(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, token string, isStream bool, promptCacheKey string, isCodexCLI bool, routerMatch ...egress.TLSFingerprintRouterMatchResult) (*http.Request, error) {
	return forward.BuildResponsesRequest(ctx, body, promptCacheKey, s.Target(c, account, false), func(path string) { SetActualOpenAIUpstreamEndpoint(c, path) }, func(b []byte) []byte {
		return forward.NormalizeCNResponsesBody(account != nil && gatewayprovider.ExecutionProtocolTarget(account).UsesNativeCNResponses(), b)
	}, func(target string) openai.ResponsesRequestOptions {
		return s.ResponseOptions(ctx, c, account, token, target, isCodexCLI, routerMatch...)
	})
}
func (s *OpenAIRequests) BuildPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	return forward.BuildPassthroughRequest(ctx, body, s.Target(c, account, true), func(b []byte) []byte {
		return forward.NormalizeCNResponsesBody(account != nil && gatewayprovider.ExecutionProtocolTarget(account).UsesNativeCNResponses(), b)
	}, func(target string) openai.PassthroughRequestOptions {
		options := s.ResponseOptions(ctx, c, account, token, target, false, routerMatch...)
		options.ForwardHeaders = func() http.Header {
			if c == nil || c.Request == nil {
				return nil
			}
			return c.Request.Header
		}
		options.ApplyUserAgent = func(req *http.Request) { s.ApplyUserAgent(ctx, c, account, req, true, routerMatch...) }
		options.Diagnostics = func(headers http.Header, body []byte) {
			LogOpenAIRoutingDiagnosticsFromBody(ctx, account, "http_passthrough", headers, body, "not_applicable")
		}
		return openai.PassthroughRequestOptions{
			ResponsesRequestOptions: options,
			AllowTimeoutHeaders:     s.AllowTimeoutHeaders,
			AllowPassthroughHeader:  isOpenAIPassthroughAllowedRequestHeader,
			MatchedOriginator: func() string {
				if len(routerMatch) > 0 && routerMatch[0].Matched {
					return strings.TrimSpace(routerMatch[0].UpstreamOriginator)
				}
				return ""
			},
		}
	})
}
func isOpenAIPassthroughAllowedRequestHeader(lowerKey string, allowTimeoutHeaders bool) bool {
	if lowerKey == "" {
		return false
	}
	if isOpenAIPassthroughTimeoutHeader(lowerKey) {
		return allowTimeoutHeaders
	}
	return openaiPassthroughAllowedHeaders[lowerKey]
}
func isOpenAIPassthroughTimeoutHeader(lowerKey string) bool {
	switch lowerKey {
	case "x-stainless-timeout", "x-stainless-read-timeout", "x-stainless-connect-timeout", "x-request-timeout", "request-timeout", "grpc-timeout":
		return true
	default:
		return false
	}
}
