package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	chatgptCodexAlphaSearchURL   = "https://chatgpt.com/backend-api/codex/alpha/search"
	openAIPlatformAlphaSearchURL = "https://api.openai.com/v1/alpha/search"
)

// ForwardAlphaSearch 透传 Codex 独立网页搜索，不绑定仍在演进的 alpha 请求和响应结构。
//
// 仅当上游返回 2xx 时返回带 WebSearchCalls=1 的结果供按次计费；上游错误原样透传时
// 返回 nil 结果，不产生费用。
func (s *OpenAIGatewayService) ForwardAlphaSearch(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	if s == nil || c == nil || account == nil {
		return nil, fmt.Errorf("service, context, and account are required")
	}
	if _, err := gatewayhttp.PrepareCodexIdentity(ctx, c, s.accountRepo, account); err != nil {
		return nil, err
	}
	modelResult := gjson.GetBytes(body, "model")
	requestedModel := strings.TrimSpace(modelResult.String())
	if modelResult.Type != gjson.String || requestedModel == "" {
		return nil, fmt.Errorf("model is required")
	}

	upstreamModel := gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(gatewayprovider.ExecutionModelPolicy(account).Mapped(requestedModel))
	if upstreamModel != "" && upstreamModel != requestedModel {
		body = openaiprotocol.ReplaceModelInBody(body, upstreamModel)
	}
	sanitizedBody, err := openai.SanitizeOpenAIAlphaSearchBody(body)
	if err != nil {
		return nil, fmt.Errorf("sanitize alpha search request body: %w", err)
	}
	body = sanitizedBody

	token, _, err := s.executionCredentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	if err := s.ensureOpenAIAlphaSearchAuthMetadata(ctx, account, token, proxyURL); err != nil {
		return nil, err
	}
	gatewayhttp.SetOpsUpstreamModel(c, upstreamModel)

	// Codex Personal Access Token（at-...）目前可访问 ChatGPT Codex
	// /responses，但会被 standalone /alpha/search 的 access enforcement
	// 拒绝为 no_matching_rule。对 PAT 账号使用等价的 hosted web_search
	// Responses 路径兜底，避免把可用账号误判为搜索不可用。
	if account.View().IsOpenAIPersonalAccessToken() {
		return s.forwardAlphaSearchViaResponsesWebSearch(ctx, c, account, body, token, proxyURL, requestedModel, upstreamModel, tlsRouterMatch...)
	}

	req, err := s.buildOpenAIAlphaSearchRequest(ctx, c, account, body, token, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}

	target := &mediaprovider.AlphaSearchOptions{
		AccountID: account.Record.ID, Request: req, ResponsesFallback: false, Model: upstreamModel, Enter: s.nativeAttemptActivity,
		Do: func(request *http.Request) (*http.Response, error) {
			return s.httpUpstream.DoWithTLS(request, proxyURL, account.Record.ID, account.Record.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
		},
		Latency: func(duration time.Duration) {
			gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsUpstreamLatencyMsKey, duration.Milliseconds())
		},
		TransportError: func(err error) error { return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true) },
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return gatewayhttp.ReadUpstreamResponseBody(reader, resolveUpstreamResponseReadLimit(s.cfg), c, gatewayhttp.OpenAIResponseTooLarge)
		},
		HTTPError: func(resp *http.Response, respBody []byte) error {
			upstreamMessage := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			return gatewaymedia.ResolveAlphaFailure(resp.StatusCode, gatewaymedia.AlphaFailurePorts{
				Failover:            func() bool { return s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, respBody) },
				EndpointUnsupported: func() bool { return isOpenAIAlphaSearchEndpointUnsupported(account, resp.StatusCode) },
				Prepare:             func() { resp.Body = io.NopCloser(bytes.NewReader(respBody)) },
				ApplySideEffects: func() bool {
					return s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
				},
				NewFailover: func(shouldDisable bool) error {
					retryableOnSameAccount := !shouldDisable && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(resp.StatusCode)
					if account.View().IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
					}
					if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
						return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
					}
					return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
				},
			})
		},
		UpdateQuota: func(headers http.Header) {
			if !account.View().IsShadow() {
				s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.Record.ID, headers)
			}
		},
		Headers: func(dst, src http.Header) { writeOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) },
	}
	result, err := (mediaprovider.AlphaSearch{Options: *target}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAlphaSearch, ResponseModel: requestedModel}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	if result.SearchCount == 0 {
		return nil, nil
	}
	output := &forwardcore.OpenAIResult{RequestID: result.RequestID, UpstreamHeaders: result.UpstreamHeaders, Model: requestedModel, UpstreamModel: upstreamModel, Duration: result.Duration, WebSearchCalls: 1}
	return output, nil
}

func (s *OpenAIGatewayService) forwardAlphaSearchViaResponsesWebSearch(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	alphaBody []byte,
	token string,
	proxyURL string,
	requestedModel string,
	upstreamModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	responsesBody, err := openai.BuildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody, upstreamModel)
	if err != nil {
		return nil, err
	}
	req, err := s.buildOpenAIAlphaSearchResponsesWebSearchRequest(ctx, c, account, alphaBody, responsesBody, token, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}
	gatewayhttp.SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")

	target := &mediaprovider.AlphaSearchOptions{
		AccountID: account.Record.ID, Request: req, ResponsesFallback: true, Model: upstreamModel, Enter: s.nativeAttemptActivity,
		Do: func(request *http.Request) (*http.Response, error) {
			return s.httpUpstream.DoWithTLS(request, proxyURL, account.Record.ID, account.Record.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
		},
		Latency: func(duration time.Duration) {
			gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsUpstreamLatencyMsKey, duration.Milliseconds())
		},
		TransportError: func(err error) error { return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true) },
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return gatewayhttp.ReadUpstreamResponseBody(reader, resolveUpstreamResponseReadLimit(s.cfg), c, gatewayhttp.OpenAIResponseTooLarge)
		},
		HTTPError: func(resp *http.Response, respBody []byte) error {
			upstreamMessage := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			return gatewaymedia.ResolveAlphaFailure(resp.StatusCode, gatewaymedia.AlphaFailurePorts{
				Failover:            func() bool { return s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, respBody) },
				EndpointUnsupported: func() bool { return false },
				Prepare:             func() { resp.Body = io.NopCloser(bytes.NewReader(respBody)) },
				ApplySideEffects: func() bool {
					return s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
				},
				NewFailover: func(shouldDisable bool) error {
					retryableOnSameAccount := !shouldDisable && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(resp.StatusCode)
					if account.View().IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
					}
					if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
						return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
					}
					return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
				},
			})
		},
		UpdateQuota: func(headers http.Header) {
			if !account.View().IsShadow() {
				s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.Record.ID, headers)
			}
		},
		Headers: func(dst, src http.Header) { writeOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) },
	}
	result, err := (mediaprovider.AlphaSearch{Options: *target}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAlphaSearch, ResponseModel: requestedModel}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	if result.SearchCount == 0 {
		return nil, nil
	}
	output := &forwardcore.OpenAIResult{RequestID: result.RequestID, UpstreamHeaders: result.UpstreamHeaders, Model: requestedModel, UpstreamModel: upstreamModel, Duration: result.Duration, WebSearchCalls: 1}
	output.UpstreamEndpoint = "/v1/responses"
	output.ResponseHeaders = result.UpstreamHeaders.Clone()
	return output, nil
}

func openAIAlphaSearchSchedulingModel(account *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(requestedModel)
}

func (s *OpenAIGatewayService) buildOpenAIAlphaSearchResponsesWebSearchRequest(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, alphaBody []byte, body []byte, token string, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*http.Request, error) {
	targetURL := chatgptCodexURL
	options := s.nativeResponsesRequestOptions(ctx, c, account, token, targetURL, true, tlsRouterMatch...)
	options.ApplyUserAgent = func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, true, tlsRouterMatch...) }
	return openai.BuildAlphaSearchResponsesRequest(ctx, alphaBody, body, openai.AlphaSearchRequestOptions{
		ResponsesRequestOptions: options,
		Query: func() url.Values {
			if c == nil || c.Request == nil || c.Request.URL == nil {
				return nil
			}
			return c.Request.URL.Query()
		},
		OAuth:         func() bool { return account.Record.Type == capability.AccountTypeOAuth },
		InboundHeader: func(key string) string { return openAIAlphaSearchInboundHeader(c, key) },
		IdentityWithKey: func(headers http.Header, key int64) {
			openai.ApplyCodexAccountIdentityHeaders(headers, accountprovider.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(c, account.View())), key)
		},
		ResponsesLiteHeader: gatewaymedia.ResponsesLiteHeaderKey,
	})
}

func (s *OpenAIGatewayService) buildOpenAIAlphaSearchRequest(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	targetURL, err := s.openAIAlphaSearchURL(account)
	if err != nil {
		return nil, err
	}
	options := s.nativeResponsesRequestOptions(ctx, c, account, token, targetURL, true, tlsRouterMatch...)
	options.ApplyUserAgent = func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, true, tlsRouterMatch...) }
	return openai.BuildAlphaSearchRequest(ctx, body, openai.AlphaSearchRequestOptions{
		ResponsesRequestOptions: options,
		Query: func() url.Values {
			if c == nil || c.Request == nil || c.Request.URL == nil {
				return nil
			}
			return c.Request.URL.Query()
		},
		OAuth:         func() bool { return account.Record.Type == capability.AccountTypeOAuth },
		InboundHeader: func(key string) string { return openAIAlphaSearchInboundHeader(c, key) },
		IdentityWithKey: func(headers http.Header, key int64) {
			openai.ApplyCodexAccountIdentityHeaders(headers, accountprovider.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(c, account.View())), key)
		},
		ResponsesLiteHeader: gatewaymedia.ResponsesLiteHeaderKey,
	})
}

func openAIAlphaSearchInboundHeader(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.GetHeader(key))
}

func (s *OpenAIGatewayService) ensureOpenAIAlphaSearchAuthMetadata(ctx context.Context, account *gatewayprovider.ExecutionAccount, token string, proxyURL string) error {
	if s == nil || account == nil || !account.View().IsOpenAIPersonalAccessToken() {
		return nil
	}
	if strings.TrimSpace(account.View().GetChatGPTAccountID()) != "" {
		return nil
	}
	oauthService := s.openAIAuthorization
	if oauthService == nil {
		return nil
	}
	ports := accountcore.OpenAIAlphaMetadataPorts{
		Apply: func(credentials map[string]any) { account.Record.Credentials = querycache.ShallowMap(credentials) },
	}
	if s.accountRepo != nil {
		ports.Persist = func(ctx context.Context, credentials map[string]any) error {
			return gatewayprovider.PersistExecutionCredentials(ctx, s.accountRepo, account, credentials)
		}
	}
	return oauthService.EnsureAlphaSearchMetadata(ctx, gatewayprovider.ExecutionRecord(account), token, proxyURL, ports)
}

// BindOpenAIAuthorization 在构造阶段绑定同一授权实例，搜索元数据不再从 token 包装取回依赖。
func (s *OpenAIGatewayService) BindOpenAIAuthorization(authorization *accountcore.OpenAIAuthorization) {
	s.openAIAuthorization = authorization
}

func isOpenAIAlphaSearchEndpointUnsupported(account *gatewayprovider.ExecutionAccount, statusCode int) bool {
	return gatewaymedia.AlphaEndpointUnsupported(account != nil && account.Record.Type == capability.AccountTypeAPIKey, statusCode)
}

// openAIAlphaSearchURL 按账号类型选择 ChatGPT Codex 或 API-key 搜索端点。
func (s *OpenAIGatewayService) openAIAlphaSearchURL(account *gatewayprovider.ExecutionAccount) (string, error) {
	if account == nil {
		return "", fmt.Errorf("account is required")
	}
	switch account.Record.Type {
	case capability.AccountTypeOAuth, capability.AccountTypeSetupToken:
		return chatgptCodexAlphaSearchURL, nil
	case capability.AccountTypeAPIKey:
		baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIBaseURL()
		if baseURL == "" {
			return openAIPlatformAlphaSearchURL, nil
		}
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return "", err
		}
		return httpclient.BuildOpenAIEndpointURL(validatedURL, "/v1/alpha/search"), nil
	default:
		return "", fmt.Errorf("unsupported OpenAI account type: %s", account.Record.Type)
	}
}
