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

	target := &nativeopenai.AlphaSearchTarget{
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
			if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, respBody) ||
				isOpenAIAlphaSearchEndpointUnsupported(account, resp.StatusCode) {
				resp.Body = io.NopCloser(bytes.NewReader(respBody))
				// alpha/search 是独立的工具端点，单次 401 不能证明账号的模型调用
				// 凭据全局失效。若沿用通用 401 逻辑，PAT 会因没有 refresh_token
				// 被永久标记为 error；历史导入且缺少 auth_mode 标记的 at- token 也会
				// 漏过 PAT 类型判断。这里仍允许本次请求换号，但不修改任何账号状态；
				// 真正的凭据失效由普通 Responses 请求或 whoami 校验判定。
				shouldDisable := false
				if shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(resp.StatusCode) {
					shouldDisable = s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
				}
				retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
				if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
					return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
				}
				if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
					return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
				}
				return &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
			}

			return nil
		},
		UpdateQuota: func(headers http.Header) {
			if !account.IsShadow() {
				s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, headers)
			}
		},
		Headers: func(dst, src http.Header) { writeOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) },
	}
	result, err := (nativeopenai.AlphaSearchExecutor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAlphaSearch, ResponseModel: requestedModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
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

	target := &nativeopenai.AlphaSearchTarget{
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
			if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, respBody) {
				resp.Body = io.NopCloser(bytes.NewReader(respBody))
				// 仍按 alpha/search 工具请求处理：PAT 的工具链路失败不能直接永久置错。
				shouldDisable := false
				if shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(resp.StatusCode) {
					shouldDisable = s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
				}
				retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
				if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
					return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
				}
				if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
					return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
				}
				return &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
			}

			return nil
		},
		UpdateQuota: func(headers http.Header) {
			if !account.IsShadow() {
				s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, headers)
			}
		},
		Headers: func(dst, src http.Header) { writeOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) },
	}
	result, err := (nativeopenai.AlphaSearchExecutor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAlphaSearch, ResponseModel: requestedModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
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

// isOpenAIAlphaSearchEndpointUnsupported 识别「API key 上游没有实现
// /v1/alpha/search 端点」的响应。404/405 不在通用 failover 状态集里（模型
// 调用中的 404 通常是用户请求问题），但对这个独立工具端点而言，它几乎只
// 意味着所选上游（官方平台或第三方中转）不提供该端点——应换号重试，而
// 不是把 404 透传给客户端，否则混合分组里 OAuth 账号明明可以承接搜索，
// 请求却可能死在先被选中的 API key 账号上。
func isOpenAIAlphaSearchEndpointUnsupported(account *Account, statusCode int) bool {
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	return statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed
}

func shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusMethodNotAllowed:
		// 401：工具端点的 access enforcement 不代表凭据全局失效；
		// 404/405：端点不存在只说明该上游不支持独立搜索，账号本身健康。
		// 两类都只换号，不写账号错误状态。
		return false
	default:
		return true
	}
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
