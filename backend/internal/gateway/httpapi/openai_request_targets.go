package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// ChatURL 解析账号的（非 Grok）Chat Completions 上游端点。
func (s *OpenAIRequests) ChatURL(account *gatewayprovider.ExecutionAccount) (string, error) {
	baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.ValidateBaseURL(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return httpclient.BuildOpenAIEndpointURL(validatedURL, "/v1/chat/completions"), nil
}

// ChatFallbackTarget 解析两条 CC 回退路径共用的账号凭证与上游端点
// Grok 沿用自己的 OAuth/API Key 认证和 CLI 端点。
func (s *OpenAIRequests) ChatFallbackTarget(ctx context.Context, account *gatewayprovider.ExecutionAccount) (apiKey string, targetURL string, err error) {
	if account.View().IsGrok() {
		apiKey, _, err = s.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
		if err != nil {
			return "", "", err
		}
		targetURL, err = s.GrokRoutes.Chat(account, true)
		return apiKey, targetURL, err
	}
	apiKey = strings.TrimSpace(account.View().GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return "", "", fmt.Errorf("account %d missing api_key", account.Record.ID)
	}
	targetURL, err = s.ChatURL(account)
	if err != nil {
		return "", "", err
	}
	return apiKey, targetURL, nil
}
func (s *OpenAIRequests) SendChat(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	targetURL string,
	body []byte,
	stream bool,
	bearerToken string,
	userAgent string,
	grokCacheIdentity string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Response, error) {
	return openai.SendChatRequest(ctx, body, openai.CCRequestOptions{
		URL: targetURL, Token: bearerToken, Stream: stream, Headers: c.Request.Header,
		RequestContext:  gatewayprovider.DetachUpstreamContext,
		ObserveEndpoint: func() { SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions") },
		AllowHeader:     func(name string) bool { return openaiCCRawAllowedHeaders[name] },
		PrepareTransport: func(upstreamReq *http.Request) {
			if len(tlsRouterMatch) == 0 {
				tlsRouterMatch = []egress.TLSFingerprintRouterMatchResult{s.MatchTLS(c, account)}
			}
			if account.Record.Platform == capability.PlatformGrok && userAgent != "" {
				upstreamReq.Header.Set("user-agent", userAgent)
			} else if account.Record.Platform != capability.PlatformGrok {
				s.ApplyUserAgent(c.Request.Context(), c, account, upstreamReq, false, tlsRouterMatch[0])
			}

			if account.Record.Platform == capability.PlatformGrok {
				if account.View().IsGrokOAuth() {
					grok.ApplyCLIHeaders(upstreamReq.Header)
				}
				grok.ApplyGrokCacheHeaders(upstreamReq.Header, grokCacheIdentity)
			}
		},
		FinalizeHeaders: func(headers http.Header) {
			accountprovider.ApplyAccountHeaderOverrides(gatewayprovider.ExecutionProtocolRecord(account), headers)
			ApplyOpenCodeSessionHeader(c, account, targetURL, headers)
		},
		Do: func(req *http.Request) (*http.Response, error) {
			proxyURL := ""
			if account.Record.Proxy != nil {
				proxyURL = account.Record.Proxy.URL()
			}
			return s.Transport.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, s.TLSProfile(account, tlsRouterMatch...))
		},
		TransportError: func(err error) error { return s.Failure.Handle(ctx, c, account, err, false) },
	})
}

// AnthropicURL 保留旧入口，目标校验与路径拼接由唯一实现执行。
func (s *OpenAIRequests) AnthropicURL(account *gatewayprovider.ExecutionAccount) (string, error) {
	return forward.NativeAnthropicTargetURL(account.Record.ID, gatewayprovider.ExecutionProtocolTarget(account).GetAnthropicProtocolBaseURL(), s.ValidateBaseURL)
}

// BuildAnthropic 只投影本次请求 Header 与账号策略。
func (s *OpenAIRequests) BuildAnthropic(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, apiKey, targetURL string) (*http.Request, []byte, error) {
	var headers http.Header
	if c != nil && c.Request != nil {
		headers = c.Request.Header
	}
	return forward.BuildNativeAnthropicRequest(ctx, body, apiKey, targetURL, forward.NativeAnthropicRequestOptions{
		Headers: headers, GetHeader: anthropic.GetHeaderRaw, OverrideValue: gatewayprovider.BindExecutionHeaderValue(account),
		Sanitize: anthropic.SanitizeAnthropicBodyForBetaTokens, AllowedHeader: func(key string) bool { return anthropic.AllowedHeaders[key] },
		WireCasing: anthropic.ResolveWireCasing, AddHeader: anthropic.AddHeaderRaw, SetHeader: anthropic.SetHeaderRaw,
		AuthHeader: func(h http.Header, key string) {
			anthropic.SetAPIKeyAuthHeader(h, gatewayprovider.ExecutionProtocolRecord(account).GetAnthropicAPIKeyAuthScheme() == accountcore.AnthropicAPIKeyAuthSchemeAuthorizationBearer, key)
		}, ApplyOverrides: gatewayprovider.BindExecutionHeaders(account),
	})
}

func (s *OpenAIRequests) RawChatURL(account *gatewayprovider.ExecutionAccount) (string, error) {
	if account.Record.Platform == capability.PlatformGrok {
		targetURL, err := s.GrokRoutes.Chat(account, true)
		if err != nil {
			return "", fmt.Errorf("invalid grok base_url: %w", err)
		}
		return targetURL, nil
	}

	return s.ChatURL(account)
}
