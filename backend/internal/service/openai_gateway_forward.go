package service

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/domain"

	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
)

// Forward forwards request to OpenAI API
func (s *OpenAIGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	var routeErr error
	account, routeErr = accountForProtocolAttempt(ctx, account)
	if routeErr != nil {
		return nil, routeErr
	}
	profile := openAIForwardProfile(account)
	prepared, err := forward.PreparePrelude(ctx, body, profile, openAIForwardPreludeAdapter{s: s, c: c, account: account, ctx: ctx})
	if err != nil {
		return nil, err
	}
	body = prepared.Body
	startTime := prepared.StartedAt
	canonicalImageIntentBody := prepared.CanonicalImageIntentBody
	tlsRouterMatch := prepared.TLS
	wsDecision := OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransport(prepared.Transport.Transport), Reason: prepared.Transport.Reason}
	originalBody := prepared.OriginalBody
	requestView := openAIRequestView{OpenAIRequestView: prepared.View}
	reqModel, reqStream, promptCacheKey := requestView.Model, requestView.Stream, requestView.PromptCacheKey
	originalModel := reqModel
	isCodexCLI := prepared.CodexCLI
	switch prepared.Route {
	case forward.DispatchRawChat:
		return s.forwardResponsesViaRawChatCompletions(ctx, c, account, body, tlsRouterMatch)
	case forward.DispatchGrok:
		return s.forwardGrokResponses(ctx, c, account, body, originalModel, reqStream, startTime)
	case forward.DispatchAnthropic:
		return s.forwardResponsesViaNativeAnthropic(ctx, c, account, body, reqModel)
	case forward.DispatchPassthrough:
		return s.forwardOpenAIPassthrough(ctx, c, account, originalBody, canonicalImageIntentBody, reqModel, prepared.ImageIntentInvalidated, prepared.ReasoningEffort, reqStream, startTime, tlsRouterMatch)
	}

	transformed, err := forward.TransformRequest(ctx, prepared, profile, openAIForwardTransformAdapter{openAIForwardPreludeAdapter{s: s, c: c, account: account, ctx: ctx}})
	if err != nil {
		return nil, err
	}
	body = transformed.Body
	requestView = openAIRequestView{OpenAIRequestView: transformed.View}
	reqModel = transformed.Model
	promptCacheKey = transformed.PromptCacheKey
	clientPromptCacheKey := transformed.ClientPromptCacheKey
	requestedModel, billingModel, upstreamModel := transformed.RequestedModel, transformed.BillingModel, transformed.UpstreamModel
	imageIntent := transformed.ImageIntent
	fingerprintIDs := transformed.Fingerprint
	reqBody := transformed.Decoded
	// 后续 WS 或密文恢复按需读取同一份准备结果，不重新运行模型映射和策略。
	ensureReqBody := func() (map[string]any, error) {
		if requestView.HasPatches() {
			patched, err := requestView.ApplyPatches()
			if err != nil {
				return nil, err
			}
			body = patched
			requestView = newOpenAIRequestView(body)
			reqBody = nil
		}
		if reqBody != nil {
			return reqBody, nil
		}
		decoded, err := requestView.Decode(c)
		if err != nil {
			return nil, err
		}
		reqBody = decoded
		return reqBody, nil
	}
	// 剥离本会话已被上游判定失效的加密项（invalid_encrypted_content lineage），
	// 阻断同一失效密文随客户端历史在每一轮重复触发"被拒→剥离→重试/重连"。
	// lineage 会话键统一按进场形态的 body 派生：后续重试可能改写 body，
	// 延迟计算会与下一请求的进场键漂移。
	lineageGroupID := getOpenAIGroupIDFromContext(c)
	lineageEntryBody := body
	lineageSessionHash := ""
	if stateStore := s.getOpenAIWSStateStore(); stateStore != nil && stateStore.HasAnySessionInvalidEncryptedContent() {
		lineageSessionHash = s.GenerateSessionHash(c, body)
		if invalidDigests := stateStore.GetSessionInvalidEncryptedContentDigests(lineageGroupID, lineageSessionHash); len(invalidDigests) > 0 {
			strippedBody, strippedCount := s.stripSessionInvalidEncryptedContentLogged(
				body, invalidDigests, "invalid_encrypted_lineage_strip", account.ID, 0,
			)
			if strippedCount > 0 {
				body = strippedBody
				requestView = newOpenAIRequestView(body)
				reqBody = nil
			}
		}
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfg OpenAIResponsesImageBillingConfig
		var imageCfgErr error
		if reqBody != nil {
			imageCfg, imageCfgErr = resolveOpenAIResponsesImageBillingConfigDetailed(reqBody, billingModel)
		} else {
			imageCfg, imageCfgErr = resolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, billingModel)
		}
		if imageCfgErr != nil {
			openAIForwardPreludeAdapter{s: s, c: c, account: account, ctx: ctx}.Reject(forward.Rejection{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: imageCfgErr.Error(), Param: "size", ObserveUpstream: true})
			return nil, imageCfgErr
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)

	if wsDecision.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 {
		wsReqBody, err := ensureReqBody()
		if err != nil {
			return nil, err
		}
		adapter := &openAIHTTPWSForwardAdapter{s: s, c: c, account: account, clientPromptCacheKey: clientPromptCacheKey, token: token, decision: wsDecision, isCodexCLI: isCodexCLI, stream: reqStream, originalModel: originalModel, upstreamModel: upstreamModel, startedAt: startTime, tls: tlsRouterMatch, lineageGroupID: lineageGroupID, lineageSessionHash: lineageSessionHash}
		result, err := gatewayws.RunHTTPForward(ctx, wsReqBody, gatewayws.HTTPForwardInput{AccountID: account.ID, AccountType: account.Type, UpstreamModel: upstreamModel, BillingModel: billingModel, ImageBillingModel: imageBillingModel, ImageSizeTier: imageSizeTier, ImageInputSize: imageInputSize, LineageEntryBody: lineageEntryBody, Stream: reqStream, RetryLimit: openAIWSReconnectRetryLimit, IDLogLimit: openAIWSIDValueMaxLen}, adapter)
		return legacyWSForwardResult(result), err
	}

	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	// 国产模型默认 effort 补充：此处 reqModel 已被 mapping 重写为 billingModel。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, reqModel)
	reasoningEffortValue := ""
	if reasoningEffort != nil {
		reasoningEffortValue = *reasoningEffort
	}
	firstOutputTimeout := time.Duration(0)
	if reqStream && account.Platform == PlatformOpenAI {
		firstOutputTimeout = s.openAIFirstOutputTimeout(reasoningEffortValue)
	}

	input := forward.HTTPInput{
		Body: body, LineageEntryBody: lineageEntryBody, AccountID: account.ID, AccountName: account.Name, Platform: account.Platform,
		RequestedModel: requestedModel, OriginalModel: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel,
		ReasoningEffort: reasoningEffort, ReasoningEffortValue: reasoningEffortValue,
		ImageBillingModel: imageBillingModel, ImageSizeTier: imageSizeTier, ImageInputSize: imageInputSize,
		Stream: reqStream, OAuth: account.IsOAuth(), Shadow: account.IsShadow(), Grok: account.IsGrok(), StartedAt: startTime,
	}
	exchange := nativeopenai.HTTPExchangeOptions{
		StartedAt:          startTime,
		FirstOutputTimeout: firstOutputTimeout,
		RequestContext:     detachUpstreamContext,
		Build: func(ctx context.Context, body []byte) (*http.Request, error) {
			return s.buildUpstreamRequest(ctx, c, account, body, token, reqStream, promptCacheKey, isCodexCLI, tlsRouterMatch)
		},
		ApplyHeaders: func(headers http.Header) { applyCodexFingerprintHeaders(headers, fingerprintIDs) },
		Do: func(request *http.Request) (*http.Response, error) {
			proxyURL := ""
			if account.ProxyID != nil && account.Proxy != nil {
				proxyURL = account.Proxy.URL()
			}
			return s.httpUpstream.DoWithTLS(request, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch))
		},
		Latency: func(elapsed time.Duration) { SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds()) },
		HeaderTimeout: func() error {
			return s.newOpenAIFirstOutputTimeoutError(ctx, c, account, startTime, originalModel, reasoningEffortValue, firstOutputTimeout, "response_headers", nil)
		},
		TransportError: func(err error) error { return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false) },
	}
	options := s.nativeForwardHTTPOptions(ctx, c, account, input, exchange,
		func(current []byte) ([]byte, bool, error) {
			return prepareOpenAIHTTPEncryptedRetry(current, func(current []byte) (map[string]any, error) {
				if !bytes.Equal(body, current) {
					body = current
					requestView = newOpenAIRequestView(body)
					reqBody = nil
				}
				return ensureReqBody()
			})
		},
		func(entry []byte) {
			digests := collectOpenAIEncryptedContentDigestsRaw(entry)
			if len(digests) == 0 {
				return
			}
			if lineageSessionHash == "" {
				lineageSessionHash = s.GenerateSessionHash(c, entry)
			}
			s.markOpenAIWSInvalidEncryptedContentLineage(lineageGroupID, lineageSessionHash, digests)
		},
	)
	options.ReleaseDecodedRequest = func() { reqBody = nil }
	result, err := forward.RunHTTP(ctx, input, options)
	return openAIForwardResultFromHTTP(result), err
}

func shouldForwardOpenAIResponsesViaRawChatCompletions(account *Account) bool {
	if account != nil && account.resolvedProtocol != "" {
		return account.resolvedProtocol == domain.ProtocolOpenAIChatCompletions
	}
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	if account.IsCNProvider() {
		// CN 直接使用显式协议配置；adaptive 仅 DeepSeek / Kimi
		// 有原生 Responses，GLM 回退 Chat Completions。
		switch account.GetAPIProtocol() {
		case APIProtocolChatCompletions:
			return true
		case APIProtocolAdaptive:
			return !account.SupportsNativeCNResponses()
		default:
			return false
		}
	}
	return openai_compat.ResolveUpstreamTextProtocol(account.Extra, openai_compat.TextProtocolResponses) == openai_compat.TextProtocolChatCompletions
}

// buildUpstreamRequest 保留旧签名，仅投影目标与原生请求选项。
func (s *OpenAIGatewayService) buildUpstreamRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token string, isStream bool, promptCacheKey string, isCodexCLI bool, routerMatch ...TLSFingerprintRouterMatchResult) (*http.Request, error) {
	return forward.BuildResponsesRequest(ctx, body, promptCacheKey, s.openAIRequestTarget(c, account, false), func(path string) { SetActualOpenAIUpstreamEndpoint(c, path) }, func(b []byte) []byte { return normalizeDeepSeekResponsesRequestBody(account, b) }, func(target string) nativeopenai.ResponsesRequestOptions {
		return s.nativeResponsesRequestOptions(ctx, c, account, token, target, isCodexCLI, routerMatch...)
	})
}

// overrideBrowserUserAgent 检查请求的最终 user-agent，若为浏览器 UA 则替换为后台配置的 Codex UA。
// 用于规避 Cloudflare 对浏览器型 UA 在 ChatGPT 内部接口上的访问质询。
// 影响范围严格限定：仅 OAuth（Codex/ChatGPT 内部接口）账号生效；API Key 等其他账号原样透传。
// 仅在识别为浏览器（Mozilla/...）时改写，其他 CLI/工具 UA 不动。
func (s *OpenAIGatewayService) overrideBrowserUserAgent(ctx context.Context, account *Account, req *http.Request) {
	if req == nil || account == nil {
		return
	}
	if !account.IsOAuth() {
		return
	}
	currentUA := req.Header.Get("user-agent")
	if !nativeopenai.IsBrowserUserAgent(currentUA) {
		return
	}
	codexUA := DefaultOpenAICodexUserAgent
	if s != nil && s.settingService != nil {
		if v := strings.TrimSpace(s.settingService.GetOpenAICodexUserAgent(ctx)); v != "" {
			codexUA = v
		}
	}
	req.Header.Set("user-agent", codexUA)
}
