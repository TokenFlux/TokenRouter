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

	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

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
	account *Account,
	body []byte,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	if s == nil || c == nil || account == nil {
		return nil, fmt.Errorf("service, context, and account are required")
	}
	if _, err := s.prepareCodexAccountIdentitySource(ctx, c, account); err != nil {
		return nil, err
	}
	modelResult := gjson.GetBytes(body, "model")
	requestedModel := strings.TrimSpace(modelResult.String())
	if modelResult.Type != gjson.String || requestedModel == "" {
		return nil, fmt.Errorf("model is required")
	}

	upstreamModel := normalizeOpenAIModelForUpstream(account, account.GetMappedModel(requestedModel))
	if upstreamModel != "" && upstreamModel != requestedModel {
		body = ReplaceModelInBody(body, upstreamModel)
	}
	sanitizedBody, err := sanitizeOpenAIAlphaSearchBody(body)
	if err != nil {
		return nil, fmt.Errorf("sanitize alpha search request body: %w", err)
	}
	body = sanitizedBody

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	if err := s.ensureOpenAIAlphaSearchAuthMetadata(ctx, account, token, proxyURL); err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)

	// Codex Personal Access Token（at-...）目前可访问 ChatGPT Codex
	// /responses，但会被 standalone /alpha/search 的 access enforcement
	// 拒绝为 no_matching_rule。对 PAT 账号使用等价的 hosted web_search
	// Responses 路径兜底，避免把可用账号误判为搜索不可用。
	if account.IsOpenAIPersonalAccessToken() {
		return s.forwardAlphaSearchViaResponsesWebSearch(ctx, c, account, body, token, proxyURL, requestedModel, upstreamModel, tlsRouterMatch...)
	}

	req, err := s.buildOpenAIAlphaSearchRequest(ctx, c, account, body, token, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}

	target := &mediaprovider.AlphaSearchOptions{
		AccountID: account.ID, Request: req, ResponsesFallback: false, Model: upstreamModel, Enter: s.nativeAttemptActivity,
		Do: func(request *http.Request) (*http.Response, error) {
			return s.httpUpstream.DoWithTLS(request, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
		},
		Latency:        func(duration time.Duration) { SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, duration.Milliseconds()) },
		TransportError: func(err error) error { return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true) },
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},
		HTTPError: func(resp *http.Response, respBody []byte) error {
			upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
			return gatewaymedia.ResolveAlphaFailure(resp.StatusCode, gatewaymedia.AlphaFailurePorts{
				Failover:            func() bool { return s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, respBody) },
				EndpointUnsupported: func() bool { return isOpenAIAlphaSearchEndpointUnsupported(account, resp.StatusCode) },
				Prepare:             func() { resp.Body = io.NopCloser(bytes.NewReader(respBody)) },
				ApplySideEffects: func() bool {
					return s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
				},
				NewFailover: func(shouldDisable bool) error {
					retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
					if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
					}
					if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
						return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
					}
					return &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
				},
			})
		},
		UpdateQuota: func(headers http.Header) {
			if !account.IsShadow() {
				s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, headers)
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
	output := &OpenAIForwardResult{RequestID: result.RequestID, UpstreamHeaders: result.UpstreamHeaders, Model: requestedModel, UpstreamModel: upstreamModel, Duration: result.Duration, WebSearchCalls: 1}
	return output, nil
}

func (s *OpenAIGatewayService) forwardAlphaSearchViaResponsesWebSearch(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	alphaBody []byte,
	token string,
	proxyURL string,
	requestedModel string,
	upstreamModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	responsesBody, err := buildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody, upstreamModel)
	if err != nil {
		return nil, err
	}
	req, err := s.buildOpenAIAlphaSearchResponsesWebSearchRequest(ctx, c, account, alphaBody, responsesBody, token, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")

	target := &mediaprovider.AlphaSearchOptions{
		AccountID: account.ID, Request: req, ResponsesFallback: true, Model: upstreamModel, Enter: s.nativeAttemptActivity,
		Do: func(request *http.Request) (*http.Response, error) {
			return s.httpUpstream.DoWithTLS(request, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
		},
		Latency:        func(duration time.Duration) { SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, duration.Milliseconds()) },
		TransportError: func(err error) error { return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true) },
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},
		HTTPError: func(resp *http.Response, respBody []byte) error {
			upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
			return gatewaymedia.ResolveAlphaFailure(resp.StatusCode, gatewaymedia.AlphaFailurePorts{
				Failover:            func() bool { return s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, respBody) },
				EndpointUnsupported: func() bool { return false },
				Prepare:             func() { resp.Body = io.NopCloser(bytes.NewReader(respBody)) },
				ApplySideEffects: func() bool {
					return s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
				},
				NewFailover: func(shouldDisable bool) error {
					retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
					if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
					}
					if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
						return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
					}
					return &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
				},
			})
		},
		UpdateQuota: func(headers http.Header) {
			if !account.IsShadow() {
				s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, headers)
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
	output := &OpenAIForwardResult{RequestID: result.RequestID, UpstreamHeaders: result.UpstreamHeaders, Model: requestedModel, UpstreamModel: upstreamModel, Duration: result.Duration, WebSearchCalls: 1}
	output.UpstreamEndpoint = "/v1/responses"
	output.ResponseHeaders = result.UpstreamHeaders.Clone()
	return output, nil
}

func openAIAlphaSearchSchedulingModel(account *Account, requestedModel string) string {
	return canonicalOpenAIAccountSchedulingModel(account, requestedModel)
}

func (s *OpenAIGatewayService) buildOpenAIAlphaSearchResponsesWebSearchRequest(ctx context.Context, c *gin.Context, account *Account, alphaBody []byte, body []byte, token string, tlsRouterMatch ...TLSFingerprintRouterMatchResult) (*http.Request, error) {
	targetURL := chatgptCodexURL
	options := s.nativeResponsesRequestOptions(ctx, c, account, token, targetURL, true, tlsRouterMatch...)
	options.ApplyUserAgent = func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, true, tlsRouterMatch...) }
	return nativeopenai.BuildAlphaSearchResponsesRequest(ctx, alphaBody, body, nativeopenai.AlphaSearchRequestOptions{
		ResponsesRequestOptions: options,
		Query: func() url.Values {
			if c == nil || c.Request == nil || c.Request.URL == nil {
				return nil
			}
			return c.Request.URL.Query()
		},
		OAuth:         func() bool { return account.Type == AccountTypeOAuth },
		InboundHeader: func(key string) string { return openAIAlphaSearchInboundHeader(c, key) },
		IdentityWithKey: func(headers http.Header, key int64) {
			applyCodexAccountIdentityHeaders(headers, codexAccountIdentitySource(c, account), key)
		},
		ResponsesLiteHeader: responsesLiteHeaderKey,
	})
}

func buildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody []byte, model string) ([]byte, error) {
	return nativeopenai.BuildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody, model)
}

func (s *OpenAIGatewayService) buildOpenAIAlphaSearchRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	targetURL, err := s.openAIAlphaSearchURL(account)
	if err != nil {
		return nil, err
	}
	options := s.nativeResponsesRequestOptions(ctx, c, account, token, targetURL, true, tlsRouterMatch...)
	options.ApplyUserAgent = func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, true, tlsRouterMatch...) }
	return nativeopenai.BuildAlphaSearchRequest(ctx, body, nativeopenai.AlphaSearchRequestOptions{
		ResponsesRequestOptions: options,
		Query: func() url.Values {
			if c == nil || c.Request == nil || c.Request.URL == nil {
				return nil
			}
			return c.Request.URL.Query()
		},
		OAuth:         func() bool { return account.Type == AccountTypeOAuth },
		InboundHeader: func(key string) string { return openAIAlphaSearchInboundHeader(c, key) },
		IdentityWithKey: func(headers http.Header, key int64) {
			applyCodexAccountIdentityHeaders(headers, codexAccountIdentitySource(c, account), key)
		},
		ResponsesLiteHeader: responsesLiteHeaderKey,
	})
}

func openAIAlphaSearchInboundHeader(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.GetHeader(key))
}

func sanitizeOpenAIAlphaSearchBody(body []byte) ([]byte, error) {
	return nativeopenai.SanitizeOpenAIAlphaSearchBody(body)
}

func (s *OpenAIGatewayService) ensureOpenAIAlphaSearchAuthMetadata(ctx context.Context, account *Account, token string, proxyURL string) error {
	if s == nil || account == nil || !account.IsOpenAIPersonalAccessToken() {
		return nil
	}
	if strings.TrimSpace(account.GetChatGPTAccountID()) != "" {
		return nil
	}
	var oauthService *OpenAIOAuthService
	if s.openAITokenProvider != nil {
		oauthService = s.openAITokenProvider.openAIOAuthService
	}
	if oauthService == nil {
		return nil
	}
	ports := accountcore.OpenAIAlphaMetadataPorts{
		Apply: func(credentials map[string]any) { account.Credentials = shallowCopyMap(credentials) },
	}
	if s.accountRepo != nil {
		ports.Persist = func(ctx context.Context, credentials map[string]any) error {
			return persistAccountCredentials(ctx, s.accountRepo, account, credentials)
		}
	}
	return oauthService.Core().EnsureAlphaSearchMetadata(ctx, AccountRecordView(account), token, proxyURL, ports)
}

func isOpenAIAlphaSearchEndpointUnsupported(account *Account, statusCode int) bool {
	return gatewaymedia.AlphaEndpointUnsupported(account != nil && account.Type == AccountTypeAPIKey, statusCode)
}

func shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(statusCode int) bool {
	return gatewaymedia.AlphaAccountErrorSideEffects(statusCode)
}

// openAIAlphaSearchURL 按账号类型选择 ChatGPT Codex 或 API-key 搜索端点。
func (s *OpenAIGatewayService) openAIAlphaSearchURL(account *Account) (string, error) {
	if account == nil {
		return "", fmt.Errorf("account is required")
	}
	switch account.Type {
	case AccountTypeOAuth, AccountTypeSetupToken:
		return chatgptCodexAlphaSearchURL, nil
	case AccountTypeAPIKey:
		baseURL := account.GetOpenAIBaseURL()
		if baseURL == "" {
			return openAIPlatformAlphaSearchURL, nil
		}
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return "", err
		}
		return buildOpenAIEndpointURL(validatedURL, "/v1/alpha/search"), nil
	default:
		return "", fmt.Errorf("unsupported OpenAI account type: %s", account.Type)
	}
}
