package service

// 本文件承载 /v1/responses 透传转发及其流式、非流式响应与错误处理。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

func (s *OpenAIGatewayService) forwardOpenAIPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	canonicalImageIntentBody []byte,
	reqModel string,
	attemptImageIntentInvalidated bool,
	reasoningEffort *string,
	reqStream bool,
	startTime time.Time,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	requestedModel := reqModel
	upstreamPassthroughModel := ""
	if isOpenAIResponsesCompactPath(c) {
		compactMappedModel := s.resolveOpenAICompactFallbackModel(account, reqModel)
		if compactMappedModel != "" && compactMappedModel != reqModel {
			nextBody, setErr := sjson.SetBytes(body, "model", compactMappedModel)
			if setErr != nil {
				return nil, fmt.Errorf("set compact passthrough model: %w", setErr)
			}
			body = nextBody
			upstreamPassthroughModel = compactMappedModel
			attemptImageIntentInvalidated = true
		}
	}

	if account != nil && account.UsesOpenAICodexProtocol() {
		if rejectReason := detectOpenAIPassthroughInstructionsRejectReason(reqModel, body); rejectReason != "" {
			rejectMsg := "OpenAI codex passthrough requires a non-empty instructions field"
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			logOpenAIPassthroughInstructionsRejected(ctx, c, account, reqModel, rejectReason, body)
			c.JSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"type":    "forbidden_error",
					"message": rejectMsg,
				},
			})
			return nil, fmt.Errorf("openai passthrough rejected before upstream: %s", rejectReason)
		}
		// Codex passthrough 允许省略 instructions，但仍拒绝显式的非法值。
		if isOpenAICodexModel(reqModel) && !gjson.GetBytes(body, "instructions").Exists() {
			nextBody, setErr := sjson.SetBytes(body, "instructions", defaultCodexSynthInstructions(reqModel))
			if setErr != nil {
				return nil, fmt.Errorf("set passthrough codex instructions: %w", setErr)
			}
			body = nextBody
		}

		normalizedBody, normalized, err := normalizeOpenAIPassthroughOAuthBody(body, isOpenAIResponsesCompactPath(c))
		if err != nil {
			return nil, err
		}
		if normalized {
			body = normalizedBody
		}
		reqStream = gjson.GetBytes(body, "stream").Bool()

		accountScopedBody, accountScoped, scopeErr := applyCodexAccountIdentityClientMetadataRaw(body, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c))
		if scopeErr != nil {
			return nil, scopeErr
		}
		if accountScoped {
			body = accountScopedBody
		}

		stageCodexFingerprintIDs(c, nil)
		// 透传与普通转换路径共享指纹收敛语义。只局部改写 client_metadata，
		// 避免为大请求体做整包反序列化。
		if !isOpenAIResponsesCompactPath(c) {
			var clientHeaders http.Header
			if c != nil && c.Request != nil {
				clientHeaders = c.Request.Header
			}
			fingerprintIDs := resolveCodexFingerprintIDsFromRequest(account, clientHeaders)
			if fingerprintIDs != nil {
				updatedBody, changed, fingerprintErr := applyCodexFingerprintClientMetadataRaw(body, fingerprintIDs)
				if fingerprintErr != nil {
					return nil, fingerprintErr
				}
				if changed {
					body = updatedBody
				}
			}
			// nil 也必须覆盖，避免 failover 复用前一个账号的收敛 ID。
			stageCodexFingerprintIDs(c, fingerprintIDs)
		}
	}
	if account != nil && account.IsOpenAI() {
		responsesLite := false
		if c != nil {
			responsesLite = isOpenAIResponsesLiteHeader(c.GetHeader(responsesLiteHeader))
		}
		responsesLite = responsesLite || isOpenAIResponsesLiteWebSocketPayload(body)
		normalizedBody, normalized, normalizeErr := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, account, responsesLite)
		if normalizeErr != nil {
			return nil, fmt.Errorf("normalize passthrough Responses compatibility: %w", normalizeErr)
		}
		if normalized {
			body = normalizedBody
		}
		if account.IsOpenAIOAuthLike() {
			aliasedBody, reverse, aliased, aliasErr := aliasOpenAIOAuthReservedToolNamesBody(body)
			if aliasErr != nil {
				return nil, aliasErr
			}
			mergeCodexToolNameReverse(c, reverse)
			if aliased {
				body = aliasedBody
			}
		}
	}

	if account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey &&
		!isOpenAIResponsesCompactPath(c) && needsOpenAIResponsesClientToolAdaptation(body) {
		adaptedBody, mapping, adaptErr := adaptOpenAIResponsesClientTools(body)
		if adaptErr != nil {
			return nil, adaptErr
		}
		body = adaptedBody
		setOpenAIResponsesClientToolMapping(c, mapping)
	}

	sanitizedBody, sanitized, err := sanitizeEmptyBase64InputImagesInOpenAIBody(body)
	if err != nil {
		return nil, err
	}
	if sanitized {
		body = sanitizedBody
	}
	// 透传分支后续的 OAuth/APIKey 兼容归一化可能删除无工具请求的
	// parallel_tool_calls；Responses Lite 契约仍要求显式发送 false。
	if c != nil && isOpenAIResponsesLiteHeader(c.GetHeader(responsesLiteHeader)) {
		liteBody, liteChanged, liteErr := normalizeOpenAIResponsesLitePayloadForAccount(account, body)
		if liteErr != nil {
			return nil, liteErr
		}
		if liteChanged {
			body = liteBody
		}
	}

	policyModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if policyModel == "" {
		policyModel = reqModel
	}
	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, policyModel, body)
	if policyErr != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(policyErr, &blocked) {
			writeOpenAIFastPolicyBlockedResponse(c, blocked)
		}
		return nil, policyErr
	}
	body = updatedBody

	apiKey := getAPIKeyFromContext(c)
	// 宽泛意图保留给图片状态和计费，显式意图单独负责权限门禁。
	imageIntent := resolveOpenAIPassthroughImageIntent(
		c,
		reqModel,
		canonicalImageIntentBody,
		policyModel,
		body,
		attemptImageIntentInvalidated,
		IsImageGenerationIntent,
	)
	explicitImageIntent := IsExplicitImageGenerationIntent(openAIResponsesEndpoint, policyModel, body)
	if explicitImageIntent && !GroupAllowsResponsesImages(apiKeyGroup(apiKey)) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "permission_error",
				"message": ImageGenerationPermissionMessage(),
			},
		})
		return nil, errors.New("image generation disabled for group")
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfgErr error
		imageCfg, imageCfgErr := resolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, reqModel)
		if imageCfgErr != nil {
			setOpsUpstreamError(c, http.StatusBadRequest, imageCfgErr.Error(), "")
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"message": imageCfgErr.Error(),
					"param":   "size",
				},
			})
			return nil, imageCfgErr
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	logger.LegacyPrintf("service.openai_gateway",
		"[OpenAI 自动透传] 命中自动透传分支: account=%d name=%s type=%s model=%s stream=%v",
		account.ID,
		account.Name,
		account.Type,
		reqModel,
		reqStream,
	)
	if reqStream && c != nil && c.Request != nil {
		if timeoutHeaders := collectOpenAIPassthroughTimeoutHeaders(c.Request.Header); len(timeoutHeaders) > 0 {
			streamWarnLogger := logger.FromContext(ctx).With(
				zap.String("component", "service.openai_gateway"),
				zap.Int64("account_id", account.ID),
				zap.Strings("timeout_headers", timeoutHeaders),
			)
			if s.isOpenAIPassthroughTimeoutHeadersAllowed() {
				streamWarnLogger.Warn("OpenAI passthrough 透传请求包含超时相关请求头，且当前配置为放行，可能导致上游提前断流")
			} else {
				streamWarnLogger.Warn("OpenAI passthrough 检测到超时相关请求头，将按配置过滤以降低断流风险")
			}
		}
	}

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	if c != nil {
		c.Set("openai_passthrough", true)
	}

	agentTaskRecoveryTried := false
	compactModelFallbackRetried := false
	rejectedFieldRetryState := openAIResponsesRejectedFieldRetryStateForRequest(c, body)
	var resp *http.Response
	var usage *OpenAIUsage
	var firstTokenMs *int
	responseID := ""
	imageCount := 0
	var imageOutputSizes []string
	for {
		actualModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
		if actualModel == "" {
			actualModel = reqModel
		}
		SetOpsUpstreamModel(c, actualModel)
		upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
		upstreamReq, buildErr := s.buildUpstreamRequestOpenAIPassthrough(upstreamCtx, c, account, body, token, tlsRouterMatch...)
		releaseUpstreamCtx()
		if buildErr != nil {
			return nil, buildErr
		}

		upstreamStart := time.Now()
		resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		if err != nil {
			// 未收到 HTTP 响应时交给外层切换账号，持久故障仍由统一处理器临时摘除。
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
		}
		if resp.StatusCode >= 400 {
			// Peek only to identify an invalid task. Restore the body so the existing
			// passthrough error handling sees the same response after recovery fails.
			probeBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(probeBody))
			if retryBody, reason, changed, retryErr := normalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, probeBody); retryErr != nil {
				return nil, fmt.Errorf("normalize passthrough rejected Responses field retry body: %w", retryErr)
			} else if changed && rejectedFieldRetryState.Allow(retryBody) {
				body = retryBody
				logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Retrying passthrough request after %s (account: %s)", reason, account.Name)
				continue
			}
			if !agentTaskRecoveryTried && s.isAgentIdentityAccount(ctx, account) && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, probeBody) {
				agentTaskRecoveryTried = true
				expectedTaskID := account.GetCredential("task_id")
				if recoveryErr := s.recoverAgentIdentityTask(ctx, account, expectedTaskID); recoveryErr != nil {
					return nil, fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
				}
				continue
			}
			upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(probeBody)))
			if retryBody, fallbackModel, retry := s.prepareOpenAICompactFallbackRetry(
				c, account, requestedModel, body, resp.StatusCode, upstreamMsg, probeBody, compactModelFallbackRetried,
			); retry {
				s.appendOpenAICompactFallbackRetryOps(c, account, resp, probeBody, upstreamMsg, true)
				fromModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
				body = retryBody
				upstreamPassthroughModel = fallbackModel
				compactModelFallbackRetried = true
				SetOpsUpstreamModel(c, fallbackModel)
				logger.LegacyPrintf(
					"service.openai_gateway",
					"[OpenAI passthrough] Retrying explicit compact request once with fallback model (account: %s, from: %s, to: %s, upstream_code: %s)",
					account.Name, fromModel, fallbackModel, extractUpstreamErrorCode(probeBody),
				)
				continue
			}

			// 透传模式默认保持原样代理；容量错误以及 API-key 上游的瞬时
			// 5xx 应先触发多账号 failover，且此时尚未写入下游响应。
			// probeBody 已在上方任务探测时读取过一次，直接复用避免重复读取。
			if shouldFailoverOpenAIPassthroughResponse(account, resp.StatusCode, probeBody) {
				return nil, s.handleFailoverErrorResponsePassthrough(ctx, resp, c, account, body, probeBody)
			}
			return nil, s.handleErrorResponsePassthrough(ctx, resp, c, account, body, probeBody)
		}

		if mapping, ok := openAIResponsesClientToolMapping(c); ok && isEventStreamResponse(resp.Header) {
			maxLineSize := defaultMaxLineSize
			if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
				maxLineSize = s.cfg.Gateway.MaxLineSize
			}
			resp.Body = newOpenAIResponsesClientToolStreamBody(resp.Body, mapping, maxLineSize)
		}

		// x-codex-turn-state 溯源：下游回传由 writeOpenAIPassthroughResponseHeaders
		// 在各 handler 的写头点强制放行，铸造账号在此统一记录，供出站守卫剥离
		// failover 换号后的跨账号回带（openai_codex_turn_state.go）。
		if extractOpenAICodexTurnState(resp.Header) != "" {
			s.noteOpenAICodexTurnStateProvenance(c, account)
		}

		if reqStream {
			result, handleErr := s.handleStreamingResponsePassthrough(ctx, resp, c, account, startTime, reqModel, upstreamPassthroughModel)
			if handleErr != nil {
				if retryBody, fallbackModel, retry := s.applyOpenAIPassthroughCompactFallbackFromSignal(
					c, account, requestedModel, body, handleErr, compactModelFallbackRetried, resp,
				); retry {
					body = retryBody
					upstreamPassthroughModel = fallbackModel
					compactModelFallbackRetried = true
					continue
				}
				if signal, ok := asOpenAICompactFallbackSignal(handleErr); ok {
					_ = resp.Body.Close()
					compactResp, compactBody := openAICompactFallbackErrorResponse(resp, signal)
					if shouldFailoverOpenAIPassthroughResponse(account, compactResp.StatusCode, compactBody) {
						return nil, s.handleFailoverErrorResponsePassthrough(ctx, compactResp, c, account, body, compactBody)
					}
					return nil, s.handleErrorResponsePassthrough(ctx, compactResp, c, account, body, compactBody)
				}
				_ = resp.Body.Close()
				return nil, handleErr
			}
			usage = result.usage
			firstTokenMs = result.firstTokenMs
			responseID = strings.TrimSpace(result.responseID)
			imageCount = result.imageCount
			imageOutputSizes = result.imageOutputSizes
		} else {
			result, handleErr := s.handleNonStreamingResponsePassthrough(ctx, resp, c, account, reqModel, upstreamPassthroughModel)
			if handleErr != nil {
				if retryBody, fallbackModel, retry := s.applyOpenAIPassthroughCompactFallbackFromSignal(
					c, account, requestedModel, body, handleErr, compactModelFallbackRetried, resp,
				); retry {
					body = retryBody
					upstreamPassthroughModel = fallbackModel
					compactModelFallbackRetried = true
					continue
				}
				if signal, ok := asOpenAICompactFallbackSignal(handleErr); ok {
					_ = resp.Body.Close()
					compactResp, compactBody := openAICompactFallbackErrorResponse(resp, signal)
					if shouldFailoverOpenAIPassthroughResponse(account, compactResp.StatusCode, compactBody) {
						return nil, s.handleFailoverErrorResponsePassthrough(ctx, compactResp, c, account, body, compactBody)
					}
					return nil, s.handleErrorResponsePassthrough(ctx, compactResp, c, account, body, compactBody)
				}
				_ = resp.Body.Close()
				return nil, handleErr
			}
			usage = result.usage
			responseID = strings.TrimSpace(result.responseID)
			imageCount = result.imageCount
			imageOutputSizes = result.imageOutputSizes
		}
		break
	}
	defer func() { _ = resp.Body.Close() }()
	serviceTier := extractOpenAIServiceTierFromBody(body)
	s.bindHTTPResponseAccount(ctx, c, account, responseID)

	if !account.IsShadow() {
		if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
			s.updateCodexUsageSnapshot(ctx, account.ID, snapshot)
		}
	}

	if usage == nil {
		usage = &OpenAIUsage{}
	}

	forwardResult := &OpenAIForwardResult{
		RequestID:                   resp.Header.Get("x-request-id"),
		UpstreamHeaders:             resp.Header,
		ResponseID:                  responseID,
		Usage:                       *usage,
		Model:                       reqModel,
		UpstreamModel:               upstreamPassthroughModel,
		UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
		ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
		ReasoningEffort:             reasoningEffort,
		Stream:                      reqStream,
		OpenAIWSMode:                false,
		Duration:                    time.Since(startTime),
		FirstTokenMs:                firstTokenMs,
	}
	if imageCount > 0 {
		forwardResult.ImageCount = imageCount
		forwardResult.ImageSize = imageSizeTier
		forwardResult.ImageInputSize = imageInputSize
		forwardResult.ImageOutputSizes = imageOutputSizes
		forwardResult.BillingModel = imageBillingModel
	}
	return forwardResult, nil
}

func logOpenAIPassthroughInstructionsRejected(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	reqModel string,
	rejectReason string,
	body []byte,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	accountID := int64(0)
	accountName := ""
	accountType := ""
	if account != nil {
		accountID = account.ID
		accountName = strings.TrimSpace(account.Name)
		accountType = strings.TrimSpace(string(account.Type))
	}
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("account_name", accountName),
		zap.String("account_type", accountType),
		zap.String("request_model", strings.TrimSpace(reqModel)),
		zap.String("reject_reason", strings.TrimSpace(rejectReason)),
	}
	fields = appendCodexCLIOnlyRejectedRequestFields(fields, c, body)
	logger.FromContext(ctx).With(fields...).Warn("OpenAI passthrough 本地拦截：Codex 请求缺少有效 instructions")
}

func (s *OpenAIGatewayService) buildUpstreamRequestOpenAIPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
	routerMatch ...TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	targetURL := openaiPlatformAPIURL
	switch account.Type {
	case AccountTypeOAuth:
		targetURL = chatgptCodexURL
	case AccountTypeSetupToken:
		if account.IsOpenAIOAuthLike() {
			targetURL = chatgptCodexURL
		}
	case AccountTypeAPIKey:
		baseURL := account.GetOpenAIBaseURL()
		if _, unified := account.Credentials[upstreamProtocolsKey]; account.UsesNativeCNResponses() && (unified || account.IsAdaptiveAPIProtocol()) {
			baseURL = account.GetCNProtocolBaseURL(APIProtocolResponses)
		}
		if baseURL != "" {
			validatedURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, err
			}
			targetURL = buildOpenAIResponsesURLForPlatform(account.Platform, validatedURL)
		}
	}
	targetURL = appendOpenAIResponsesRequestPathSuffix(targetURL, openAIResponsesRequestPathSuffix(c))

	// DeepSeek / Kimi 原生 Responses 端点为无状态实现（见 normalizeDeepSeekResponsesRequestBody）。
	body = normalizeDeepSeekResponsesRequestBody(account, body)

	options := s.nativeResponsesRequestOptions(ctx, c, account, token, targetURL, false, routerMatch...)
	options.ForwardHeaders = func() http.Header {
		if c == nil || c.Request == nil {
			return nil
		}
		return c.Request.Header
	}
	options.ApplyUserAgent = func(req *http.Request) { s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, true, routerMatch...) }
	options.Diagnostics = func(headers http.Header, body []byte) {
		logOpenAIRoutingDiagnosticsFromBody(ctx, account, "http_passthrough", headers, body, "not_applicable")
	}
	return nativeopenai.BuildPassthroughRequest(ctx, body, nativeopenai.PassthroughRequestOptions{
		ResponsesRequestOptions: options,
		AllowTimeoutHeaders:     s.isOpenAIPassthroughTimeoutHeadersAllowed,
		AllowPassthroughHeader:  isOpenAIPassthroughAllowedRequestHeader,
		MatchedOriginator: func() string {
			if len(routerMatch) > 0 && routerMatch[0].Matched {
				return strings.TrimSpace(routerMatch[0].UpstreamOriginator)
			}
			return ""
		},
	})
}

func shouldFailoverOpenAIPassthroughResponse(account *Account, statusCode int, responseBody []byte) bool {
	if hit, _, _ := detectOpenAICyberPolicy(responseBody); hit {
		return false
	}
	if isOpenAIContextWindowError("", responseBody) {
		return false
	}
	if isOpenAIHTTPUpstreamAccessStateError(statusCode, "", responseBody) {
		return true
	}
	if isOpenAIRequestBodyTooLargeError(statusCode, "", responseBody) {
		return true
	}
	if account != nil && account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode) {
		return true
	}
	switch statusCode {
	case http.StatusTooManyRequests, 529:
		return true
	}
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	switch statusCode {
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		520, 521, 522, 523, 524:
		return true
	default:
		return false
	}
}

// writeOpenAIPassthroughErrorHeaders 仅保留可安全转发的错误响应头，避免泄露上游信息。
func writeOpenAIPassthroughErrorHeaders(dst, src http.Header) {
	if dst == nil {
		return
	}
	dst.Set("Content-Type", "application/json; charset=utf-8")
	dst.Set("Cache-Control", "no-store")
	dst.Del("Retry-After")
	if src == nil {
		return
	}
	rawRetryAfter := strings.TrimSpace(src.Get("Retry-After"))
	if validOpenAIPassthroughRetryAfter(rawRetryAfter, time.Now()) {
		dst.Set("Retry-After", rawRetryAfter)
	}
}

// validOpenAIPassthroughRetryAfter 校验 Retry-After 是否为正整数秒或未来的 HTTP 时间。
func validOpenAIPassthroughRetryAfter(raw string, now time.Time) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	delaySeconds := true
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			delaySeconds = false
			break
		}
	}
	if delaySeconds {
		seconds, err := strconv.ParseUint(raw, 10, 64)
		return err == nil && seconds > 0
	}
	parsed, err := http.ParseTime(raw)
	return err == nil && parsed.After(now)
}

// writeSanitizedOpenAIPassthroughError 使用本地错误信封替换不可信的上游错误正文。
func writeSanitizedOpenAIPassthroughError(c *gin.Context, upstreamStatus int, upstreamHeaders http.Header) {
	downstreamStatus := upstreamStatus
	message := "Upstream request failed"
	switch upstreamStatus {
	case http.StatusUnauthorized:
		downstreamStatus = http.StatusBadGateway
		message = "Upstream authentication failed"
	case http.StatusForbidden:
		downstreamStatus = http.StatusBadGateway
		message = "Upstream access denied"
	default:
		if upstreamStatus >= http.StatusInternalServerError {
			message = "Upstream service temporarily unavailable"
		}
	}
	writeOpenAIPassthroughErrorEnvelope(c, downstreamStatus, upstreamHeaders, message)
}

// writeOpenAIPassthroughErrorEnvelope 以本地 JSON 信封 + 净化后的头策略写出
// 错误响应；message 由调用方决定（净化通用文案或脱敏后的上游消息）。
func writeOpenAIPassthroughErrorEnvelope(c *gin.Context, downstreamStatus int, upstreamHeaders http.Header, message string) {
	if c == nil {
		return
	}
	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
	if writeOpenAICompactSSEBridge(c, downstreamStatus, body) {
		return
	}
	writeOpenAIPassthroughErrorHeaders(c.Writer.Header(), upstreamHeaders)
	c.Data(downstreamStatus, "application/json; charset=utf-8", body)
}

func (s *OpenAIGatewayService) handleFailoverErrorResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestBody []byte,
	responseBody []byte,
) error {
	body := s.redactAgentIdentitySensitiveBody(ctx, account, responseBody)

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)
	reqModel, _, _ := extractOpenAIRequestMetaFromBody(requestBody)
	canonicalModel := canonicalOpenAIAccountSchedulingModel(account, reqModel)
	decision := s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, canonicalModel)
	if decision.ShouldReturnGenericError() {
		MarkResponseCommitted(c)
		writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
		return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:             account.Platform,
		AccountID:            account.ID,
		AccountName:          account.Name,
		UpstreamStatusCode:   resp.StatusCode,
		UpstreamRequestID:    resp.Header.Get("x-request-id"),
		Passthrough:          true,
		Kind:                 "failover",
		Message:              upstreamMsg,
		Detail:               upstreamDetail,
		UpstreamResponseBody: upstreamDetail,
	})
	shouldDisable := decision.StopScheduling
	return s.newOpenAIAccountFailoverError(
		account,
		resp.StatusCode,
		resp.Header,
		body,
		upstreamMsg,
		shouldDisable,
		!shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
	)
}

func (s *OpenAIGatewayService) handleErrorResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestBody []byte,
	responseBody []byte,
) error {
	body := s.redactAgentIdentitySensitiveBody(ctx, account, responseBody)

	// cyber_policy 仍按原始 body 打内部标记，供 handler 事后写风控/邮件；面向客户端的
	// 错误体在下方统一重建。cyber 是上游网络安全策略拦截，不冷却账号，
	// 故下方跳过 handleOpenAIAccountUpstreamError（避免自定义 temp-unschedulable 规则误冷却）。
	cyberHit, cyberCode, cyberMsg := detectOpenAICyberPolicy(body)
	if cyberHit {
		MarkOpsCyberPolicy(c, CyberPolicyMark{
			Code:           cyberCode,
			Message:        cyberMsg,
			Body:           truncateString(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
	}

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)
	clientInvalidRequest := isOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body)
	requestScopedError := cyberHit || clientInvalidRequest || isOpenAIContextWindowError(upstreamMsg, body) ||
		isOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body)
	// 错误体虽不会原样透传，运行态账号状态仍需更新，避免粘性路由继续复用
	// 刚被限流的账号。请求级错误例外：不冷却账号，也不触发池模式重试。
	if !requestScopedError {
		reqModel, _, _ := extractOpenAIRequestMetaFromBody(requestBody)
		canonicalModel := canonicalOpenAIAccountSchedulingModel(account, reqModel)
		decision := s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, canonicalModel)
		if decision.ShouldReturnGenericError() {
			MarkResponseCommitted(c)
			writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
			return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		if decision.ShouldFailoverWithDefaults(account, resp.StatusCode, false, false) {
			return newOpenAIUpstreamFailoverError(
				resp.StatusCode,
				resp.Header,
				body,
				upstreamMsg,
				decision.RetryableOnSameAccount(account, resp.StatusCode),
			)
		}
	}
	MarkResponseCommitted(c)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:             account.Platform,
		AccountID:            account.ID,
		AccountName:          account.Name,
		UpstreamStatusCode:   resp.StatusCode,
		UpstreamRequestID:    resp.Header.Get("x-request-id"),
		Passthrough:          true,
		Kind:                 "http_error",
		Message:              upstreamMsg,
		Detail:               upstreamDetail,
		UpstreamResponseBody: upstreamDetail,
	})
	if clientInvalidRequest {
		// 参数型 400 使用安全响应头并透传完整脱敏错误对象，不再改写成 upstream_error。
		writeOpenAIPassthroughErrorHeaders(c.Writer.Header(), resp.Header)
		c.Data(http.StatusBadRequest, "application/json; charset=utf-8", body)
		return fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	// context-window 超限是确定性请求失败（shouldFailoverOpenAIPassthroughResponse
	// 已保证不切号），其文案对客户端可操作（如触发自动压缩）；在净化信封内保留
	// 脱敏后的上游消息，而不是抹成通用文案。
	if isOpenAIContextWindowError(upstreamMsg, body) && upstreamMsg != "" {
		writeOpenAIPassthroughErrorEnvelope(c, resp.StatusCode, resp.Header, upstreamMsg)
	} else {
		writeSanitizedOpenAIPassthroughError(c, resp.StatusCode, resp.Header)
	}

	return fmt.Errorf("upstream error: %d (client response sanitized)", resp.StatusCode)
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

func (s *OpenAIGatewayService) isOpenAIPassthroughTimeoutHeadersAllowed() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIPassthroughAllowTimeoutHeaders
}

func collectOpenAIPassthroughTimeoutHeaders(h http.Header) []string {
	if h == nil {
		return nil
	}
	var matched []string
	for key, values := range h {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if isOpenAIPassthroughTimeoutHeader(lowerKey) {
			entry := lowerKey
			if len(values) > 0 {
				entry = fmt.Sprintf("%s=%s", lowerKey, strings.Join(values, "|"))
			}
			matched = append(matched, entry)
		}
	}
	sort.Strings(matched)
	return matched
}

type openaiStreamingResultPassthrough struct {
	usage            *OpenAIUsage
	firstTokenMs     *int
	responseID       string
	imageCount       int
	imageOutputSizes []string
}

type openaiNonStreamingResultPassthrough struct {
	*OpenAIUsage
	usage            *OpenAIUsage
	responseID       string
	imageCount       int
	imageOutputSizes []string
}

const openAIStreamKeepaliveBytesKey = "openai_stream_keepalive_bytes"

func recordOpenAIStreamKeepaliveBytes(c *gin.Context, written int) {
	if c == nil || written <= 0 {
		return
	}
	current := 0
	if value, ok := c.Get(openAIStreamKeepaliveBytesKey); ok {
		current, _ = value.(int)
	}
	c.Set(openAIStreamKeepaliveBytesKey, current+written)
}

func openAIStreamClientOutputStarted(c *gin.Context, localStarted bool) bool {
	if localStarted {
		return true
	}
	if c == nil || c.Writer == nil {
		return false
	}
	// compact 心跳会提交 HTTP 200，但不属于模型业务输出，不应阻止安全重试。
	return OpenAICompactKeepaliveAdjustedWrittenSize(c) >= 0
}

func openAIStreamDataStartsClientOutput(data, eventType string) bool {
	return nativeopenai.OpenAIStreamDataStartsClientOutput(data, eventType)
}

func openAIStreamDataStartsVisibleOutput(data, eventType string) bool {
	return protocolopenai.StreamDataStartsVisibleOutput(data, eventType)
}

func openAIStreamDataStartsSemanticTTFT(data, eventType string) bool {
	return nativeopenai.OpenAIStreamDataStartsSemanticTTFT(data, eventType)
}

// openAIStreamDataStartsTTFT 按管理员设置选择语义事件或真实可见内容作为 TTFT 起点。
func openAIStreamDataStartsTTFT(data, eventType string, forceOutput bool, mode string) bool {
	if normalizeOpenAITTFTMode(mode) == OpenAITTFTModeVisible {
		return openAIStreamDataStartsVisibleOutput(data, eventType)
	}
	return forceOutput || openAIStreamDataStartsSemanticTTFT(data, eventType)
}

// openAITTFTMode 读取网关设置；未注入设置服务时复用进程缓存并默认安全回退。
func (s *OpenAIGatewayService) openAITTFTMode(ctx context.Context) string {
	mode := OpenAITTFTModeSemantic
	if s != nil && s.settingService != nil {
		mode = s.settingService.GetOpenAITTFTMode(ctx)
	} else if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		if cached.expiresAt == 0 || time.Now().UnixNano() < cached.expiresAt {
			mode = normalizeOpenAITTFTMode(cached.openAITTFTMode)
		}
	}
	return normalizeOpenAITTFTMode(mode)
}

func isOpenAIUpstreamCapacityShedEvent(payload []byte) bool {
	return nativeopenai.IsOpenAIUpstreamCapacityShedEvent(payload)
}

func logOpenAICapacityFailoverSuppressed(
	ctx context.Context,
	account *Account,
	path string,
	upstreamRequestID string,
	eventType string,
) {
	fields := []zap.Field{
		zap.String("path", path),
		zap.String("event_type", strings.TrimSpace(eventType)),
		zap.String("upstream_request_id", strings.TrimSpace(upstreamRequestID)),
	}
	if account != nil {
		fields = append(fields,
			zap.Int64("account_id", account.ID),
			zap.String("platform", account.Platform),
		)
	}
	logger.FromContext(ctx).Warn("gateway.failover_suppressed_after_semantic_output", fields...)
}

func sanitizeOpenAICapacityShedErrorCodeForClient(payload []byte) ([]byte, bool) {
	return nativeopenai.SanitizeOpenAICapacityShedErrorCodeForClient(payload)
}

func openAIStreamFailedEventSemanticStatus(payload []byte, message string) int {
	return nativeopenai.OpenAIStreamFailedEventSemanticStatus(payload, message)
}

func openAIStreamFailureStatus(payload []byte, message string) int {
	return nativeopenai.OpenAIStreamFailureStatus(payload, message)
}

func openAIStream403AccountFailure(payload []byte, message string) bool {
	return nativeopenai.OpenAIStream403AccountFailure(payload, message)
}

func openAIStreamFailedEventPassthroughBody(payload []byte, failedMessage string) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}
	if gjson.GetBytes(payload, "error").Exists() {
		return payload
	}
	responseError := gjson.GetBytes(payload, "response.error")
	if !responseError.Exists() {
		if strings.TrimSpace(failedMessage) == "" {
			return payload
		}
		body, err := marshalOpenAIUpstreamJSON(gin.H{
			"error": gin.H{
				"message": failedMessage,
			},
		})
		if err != nil {
			return payload
		}
		return body
	}

	errorPayload := gin.H{}
	if errType := strings.TrimSpace(gjson.Get(responseError.Raw, "type").String()); errType != "" {
		errorPayload["type"] = errType
	}
	if code := strings.TrimSpace(gjson.Get(responseError.Raw, "code").String()); code != "" {
		errorPayload["code"] = code
	}
	if param := strings.TrimSpace(gjson.Get(responseError.Raw, "param").String()); param != "" {
		errorPayload["param"] = param
	}
	message := strings.TrimSpace(gjson.Get(responseError.Raw, "message").String())
	if message == "" {
		message = strings.TrimSpace(failedMessage)
	}
	if message != "" {
		errorPayload["message"] = message
	}
	if len(errorPayload) == 0 {
		return payload
	}
	body, err := marshalOpenAIUpstreamJSON(gin.H{"error": errorPayload})
	if err != nil {
		return payload
	}
	return body
}

// applyOpenAIStreamFailedErrorPassthroughRule 对 response.failed 事件应用错误透传规则：
// 归一化 body 供关键词匹配/消息提取，并推断语义状态码使按错误码配置的规则可以命中。
// platform 必须传 account.Platform——本服务同时承载 openai 与 grok 平台账号，规则按平台匹配。
func applyOpenAIStreamFailedErrorPassthroughRule(
	c *gin.Context,
	platform string,
	payload []byte,
	failedMessage string,
) (status int, errType string, errMsg string, matched bool) {
	ruleBody := openAIStreamFailedEventPassthroughBody(payload, failedMessage)
	upstreamStatus := openAIStreamFailedEventSemanticStatus(payload, failedMessage)
	return applyErrorPassthroughRule(
		c,
		platform,
		upstreamStatus,
		ruleBody,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)
}

func openAIStreamFailedEventShouldFailover(payload []byte, message string) bool {
	return nativeopenai.OpenAIStreamFailedEventShouldFailover(payload, message)
}

func openAIStreamErrorEventShouldFailover(payload []byte, message string) bool {
	return nativeopenai.OpenAIStreamErrorEventShouldFailover(payload, message)
}

func (s *OpenAIGatewayService) handleOpenAIStreamTerminalAccountSideEffects(
	c *gin.Context,
	account *Account,
	payload []byte,
	message string,
	headers http.Header,
	canonicalModel ...string,
) (int, bool) {
	statusCode := openAIStreamFailureStatus(payload, message)
	switch statusCode {
	case http.StatusForbidden:
		if !openAIStream403AccountFailure(payload, message) {
			return statusCode, false
		}
		fallthrough
	case http.StatusUnauthorized, http.StatusTooManyRequests, 529:
		ctx := context.Background()
		if c != nil && c.Request != nil {
			ctx = c.Request.Context()
		}
		model := firstNonEmpty(canonicalModel...)
		if model == "" {
			model = firstNonEmpty(gjson.GetBytes(payload, "model").String(), gjson.GetBytes(payload, "response.model").String())
		}
		accountHeaders := headers
		if statusCode == http.StatusTooManyRequests {
			// 普通模型的流式 429 不能继承外层 HTTP 200 的全局 quota 快照；
			// 只有 OAuth/SetupToken 的 Spark 配额 429 才需要读取明确的窗口 reset。
			accountHeaders = openAIWSSemantic429Headers(account, model, headers)
		}
		return statusCode, s.handleOpenAIAccountUpstreamError(ctx, account, statusCode, accountHeaders, payload, model)
	default:
		// response.failed 可携带管理员自定义的非默认状态码（例如 422）。
		// 只有命中显式策略或池模式重试状态时才进入账号策略，普通请求级
		// 校验错误仍保持无副作用。
		customMatched := account != nil && account.IsCustomErrorCodesEnabled() && account.ShouldHandleErrorCode(statusCode)
		poolRetryable := account != nil && account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode)
		if customMatched || poolRetryable {
			ctx := context.Background()
			if c != nil && c.Request != nil {
				ctx = c.Request.Context()
			}
			return statusCode, s.handleOpenAIAccountUpstreamError(ctx, account, statusCode, headers, payload, firstNonEmpty(canonicalModel...))
		}
		return statusCode, false
	}
}

// openAIStreamFailedEventRetryableOnSameAccount 兼容旧调用点和带策略决策的测试入口。
// 两种入口最终都按账号池模式与事件语义判断同账号重试，决策参数仅用于保持旧 API 兼容。
func openAIStreamFailedEventRetryableOnSameAccount(args ...any) bool {
	var account *Account
	var payload []byte
	var message string
	if len(args) == 3 {
		account, _ = args[0].(*Account)
		payload, _ = args[1].([]byte)
		message, _ = args[2].(string)
	} else if len(args) >= 5 {
		account, _ = args[1].(*Account)
		payload, _ = args[3].([]byte)
		message, _ = args[4].(string)
	}
	if account == nil {
		return false
	}
	// 容量降载由客户端身份或模型容量触发，与当前账号健康无关；非池账号也应先
	// 做有界同账号重试，避免无意义地轮换并冷却整组账号。
	if isOpenAIUpstreamCapacityShedEvent(payload) {
		return true
	}
	if !account.IsPoolMode() {
		return false
	}
	semanticStatus := openAIStreamFailedEventSemanticStatus(payload, message)
	return account.IsPoolModeRetryableStatus(semanticStatus) ||
		isOpenAITransientProcessingError(http.StatusBadRequest, message, payload)
}

// applyOpenAIStreamFailedAccountPolicy 将 HTTP 200 流内的 response.failed
// 统一映射到现有账号策略管线，避免各协议入口重复推导状态码。
func (s *OpenAIGatewayService) applyOpenAIStreamFailedAccountPolicy(
	ctx context.Context,
	account *Account,
	model string,
	headers http.Header,
	payload []byte,
	message string,
) (int, UpstreamErrorDecision) {
	status := openAIStreamFailedEventSemanticStatus(payload, message)
	if status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	return status, s.applyOpenAIAccountStreamRateLimitError(ctx, account, status, headers, payload, model)
}

func (s *OpenAIGatewayService) recordOpenAIStreamUpstreamError(
	c *gin.Context,
	account *Account,
	passthrough bool,
	upstreamRequestID string,
	kind string,
	payload []byte,
	message string,
) string {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if message == "" {
		message = "OpenAI upstream response failed"
	}
	statusCode := openAIStreamFailureStatus(payload, message)
	detail := ""
	if len(payload) > 0 && s != nil && s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		detail = truncateString(string(payload), maxBytes)
	}
	if c != nil {
		setOpsUpstreamError(c, statusCode, message, detail)
		event := OpsUpstreamErrorEvent{
			Platform:           PlatformOpenAI,
			UpstreamStatusCode: statusCode,
			UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
			Passthrough:        passthrough,
			Kind:               kind,
			Message:            message,
			Detail:             detail,
		}
		if account != nil {
			event.Platform = account.Platform
			event.AccountID = account.ID
			event.AccountName = account.Name
		}
		appendOpsUpstreamError(c, event)
	}
	return message
}

func (s *OpenAIGatewayService) newOpenAIStreamFailoverError(
	c *gin.Context,
	account *Account,
	passthrough bool,
	upstreamRequestID string,
	payload []byte,
	message string,
	responseHeaders ...http.Header,
) *UpstreamFailoverError {
	return s.newOpenAIStreamFailoverErrorWithModel(c, account, passthrough, upstreamRequestID, payload, message, "", responseHeaders...)
}

func (s *OpenAIGatewayService) newOpenAIStreamFailoverErrorWithModel(
	c *gin.Context,
	account *Account,
	passthrough bool,
	upstreamRequestID string,
	payload []byte,
	message string,
	canonicalModel string,
	responseHeaders ...http.Header,
) *UpstreamFailoverError {
	var headers http.Header
	if len(responseHeaders) > 0 && responseHeaders[0] != nil {
		headers = responseHeaders[0]
	}
	return s.newOpenAIStreamPolicyFailoverErrorWithModel(
		c, account, passthrough, upstreamRequestID, headers, http.StatusBadGateway, payload, message, false, canonicalModel,
	)
}

// newOpenAIStreamPolicyFailoverError 构造应用账号策略后的流内故障转移错误。
// 下游错误体保持统一封装，同时保留语义状态和上游响应头供 handler 最终处理。
func (s *OpenAIGatewayService) newOpenAIStreamPolicyFailoverError(
	c *gin.Context,
	account *Account,
	passthrough bool,
	upstreamRequestID string,
	responseHeaders http.Header,
	statusCode int,
	payload []byte,
	message string,
	_ bool,
) *UpstreamFailoverError {
	return s.newOpenAIStreamPolicyFailoverErrorWithModel(c, account, passthrough, upstreamRequestID, responseHeaders, statusCode, payload, message, false)
}

func (s *OpenAIGatewayService) newOpenAIStreamPolicyFailoverErrorWithModel(
	c *gin.Context,
	account *Account,
	passthrough bool,
	upstreamRequestID string,
	responseHeaders http.Header,
	statusCode int,
	payload []byte,
	message string,
	_ bool,
	canonicalModel ...string,
) *UpstreamFailoverError {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if message == "" {
		message = "OpenAI stream disconnected before completion"
	}
	var headers http.Header
	if len(responseHeaders) > 0 {
		headers = responseHeaders.Clone()
	}
	shouldDisable := false
	sideEffectsApplied := false
	if c != nil {
		if rawState, ok := c.Get(openAIWSFailureSideEffectsStateKey); ok {
			// Gin 没有公开的 Delete 方法；只消费当前请求上下文中的一次性值。
			delete(c.Keys, openAIWSFailureSideEffectsStateKey)
			if state, ok := rawState.(openAIWSFailureSideEffectsState); ok {
				statusCode = state.StatusCode
				shouldDisable = state.ShouldDisable
				sideEffectsApplied = true
			}
		}
	}
	if !sideEffectsApplied {
		statusCode, shouldDisable = s.handleOpenAIStreamTerminalAccountSideEffects(c, account, payload, message, headers, canonicalModel...)
	}
	if statusCode < http.StatusBadRequest {
		statusCode = openAIStreamFailureStatus(payload, message)
	}
	// 流内 failed 事件承载于 HTTP 200；使用事件的语义状态更新账号健康，
	// 再由 failover 引擎按 StatusCode/RetryableOnSameAccount 决定恢复策略。
	message = s.recordOpenAIStreamUpstreamError(c, account, passthrough, upstreamRequestID, "failover", payload, message)
	errType := "upstream_error"
	if statusCode == http.StatusTooManyRequests {
		errType = "rate_limit_error"
	}
	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
	retryable := openAIStreamFailedEventRetryableOnSameAccount(account, payload, message)
	// 流终止事件承载在 HTTP 200 内，外层响应头描述的是成功流状态，而不是语义上的
	// 429 事件。仅在配额分类时忽略这些头；故障转移错误仍保留它们，使 Retry-After
	// 和请求 ID 能继续传递给后续处理。
	classificationHeaders := headers
	if statusCode == http.StatusTooManyRequests {
		classificationHeaders = nil
	}
	failoverErr := s.newOpenAIAccountFailoverErrorWithClassificationHeaders(account, statusCode, headers, classificationHeaders, payload, message, shouldDisable, retryable)
	if failoverErr.IsCredentialFailure() || failoverErr.RequestScopedTransient {
		return failoverErr
	}
	// Preserve the existing generic envelope for unclassified stream failures;
	// only typed access/capacity failures need the original payload downstream.
	failoverErr.ResponseBody = body
	return failoverErr
}

// nonStreamingTerminalFailureFailover applies the streaming path's terminal-event
// verdict to a stream=false request whose upstream answered with SSE anyway
// (other sub2api instances and several OpenAI-compatible upstreams do this).
//
// Both handleSSEToJSON and handlePassthroughSSEToJSON collapsed every terminal
// `response.failed` / `error` frame into writeOpenAINonStreamingProtocolError, a
// fixed 502. The streaming readers sitting a few hundred lines away classify the
// very same frame with openAIStreamFailedEventShouldFailover /
// openAIStreamErrorEventShouldFailover and return an UpstreamFailoverError, so an
// upstream capacity error switched accounts when stream=true and was handed to the
// caller verbatim when stream=false — with other schedulable accounts still in the
// pool. Same event, same upstream, opposite outcome, decided only by a request flag
// the upstream never saw.
//
// Dispatching on terminalType keeps the two verdicts distinct exactly as the
// streaming readers do: a bare `error` frame goes through the conservative
// classifier that fails over only on positively transient markers, while
// `response.failed` uses the fuller one.
//
// Replay is safe here because the body was fully buffered by
// ReadUpstreamResponseBody and this runs before any semantic byte is written.
// Whether a failover actually happens stays the handler's call:
// openAIForwardMayFailover compares OpenAICompactKeepaliveAdjustedWrittenSize
// against its pre-Forward snapshot, so a request that already emitted output is
// refused there (#3887). This function only declines to propose failover once the
// service has explicitly committed a response, and never re-implements the
// keepalive accounting — a second copy of that rule would drift from the handler's.
//
// A nil account means there is nothing to fail over from: newOpenAIStreamFailoverError
// records ops attribution and account health against it, so those callers keep the
// protocol-error behaviour.
func (s *OpenAIGatewayService) nonStreamingTerminalFailureFailover(
	c *gin.Context,
	resp *http.Response,
	account *Account,
	passthrough bool,
	terminalType string,
	payload []byte,
	message string,
	canonicalModel ...string,
) *UpstreamFailoverError {
	if account == nil || IsResponseCommitted(c) {
		return nil
	}
	shouldFailover := openAIStreamFailedEventShouldFailover(payload, message)
	if terminalType == "error" {
		shouldFailover = openAIStreamErrorEventShouldFailover(payload, message)
	}
	if !shouldFailover {
		return nil
	}
	var headers http.Header
	upstreamRequestID := ""
	if resp != nil {
		headers = resp.Header
		upstreamRequestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
	}
	return s.newOpenAIStreamFailoverErrorWithModel(c, account, passthrough, upstreamRequestID, payload, message, firstNonEmpty(canonicalModel...), headers)
}

func (s *OpenAIGatewayService) handleStreamingResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	startTime time.Time,
	originalModel string,
	mappedModel string,
) (*openaiStreamingResultPassthrough, error) {
	result, err := nativeopenai.ReadPassthroughStreaming(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativePassthroughOptions(ctx, c, account), startTime, originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiStreamingResultPassthrough{usage: result.Usage, firstTokenMs: result.FirstTokenMs, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes}, err
}

func (s *OpenAIGatewayService) handleNonStreamingResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	originalModel string,
	mappedModel string,
) (*openaiNonStreamingResultPassthrough, error) {
	result, err := nativeopenai.ReadPassthroughNonStreaming(ctx, resp, gatewayhttp.ResponseSink{Writer: c.Writer}, s.nativePassthroughOptions(ctx, c, account), originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResultPassthrough{OpenAIUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes}, err
}

func (s *OpenAIGatewayService) handlePassthroughSSEToJSON(resp *http.Response, c *gin.Context, account *Account, body []byte, originalModel string, mappedModel string) (*openaiNonStreamingResultPassthrough, error) {
	result, err := nativeopenai.ReadPassthroughSSEAsJSON(resp, gatewayhttp.ResponseSink{Writer: c.Writer}, s.nativePassthroughOptions(c.Request.Context(), c, account), body, originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResultPassthrough{OpenAIUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes}, err
}

func writeOpenAIPassthroughResponseHeaders(dst http.Header, src http.Header, filter *responseheaders.CompiledHeaderFilter) {
	if dst == nil || src == nil {
		return
	}
	if filter != nil {
		responseheaders.WriteFilteredHeaders(dst, src, filter)
	} else {
		// 兜底：尽量保留最基础的 content-type
		if v := strings.TrimSpace(src.Get("Content-Type")); v != "" {
			dst.Set("Content-Type", v)
		}
	}
	// 透传模式强制放行 x-codex-* 响应头（若上游返回）。
	// 注意：真实 http.Response.Header 的 key 一般会被 canonicalize；但为了兼容测试/自建响应，
	// 这里用 EqualFold 做一次大小写不敏感的查找。
	getCaseInsensitiveValues := func(h http.Header, want string) []string {
		if h == nil {
			return nil
		}
		for k, vals := range h {
			if strings.EqualFold(k, want) {
				return vals
			}
		}
		return nil
	}

	for _, rawKey := range []string{
		"x-codex-primary-used-percent",
		"x-codex-primary-reset-after-seconds",
		"x-codex-primary-window-minutes",
		"x-codex-secondary-used-percent",
		"x-codex-secondary-reset-after-seconds",
		"x-codex-secondary-window-minutes",
		"x-codex-primary-over-secondary-limit-percent",
	} {
		vals := getCaseInsensitiveValues(src, rawKey)
		if len(vals) == 0 {
			continue
		}
		key := http.CanonicalHeaderKey(rawKey)
		dst.Del(key)
		for _, v := range vals {
			dst.Add(key, v)
		}
	}

	// 回合状态不受通用响应头白名单控制；上游缺失时也要清理旧值，避免
	// failover 后把其它账号的状态留在下游响应中。
	turnStateKey := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	dst.Del(turnStateKey)
	for _, value := range getCaseInsensitiveValues(src, openAICodexTurnStateHeader) {
		dst.Add(turnStateKey, value)
	}
}
