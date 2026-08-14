package service

// 本文件承载 /v1/responses 透传转发及其流式、非流式响应与错误处理。

import (
	"bufio"
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

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
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
	upstreamPassthroughModel := ""
	if isOpenAIResponsesCompactPath(c) {
		compactMappedModel := resolveOpenAICompactForwardModel(account, reqModel)
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

	if account != nil && account.Type == AccountTypeOAuth {
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
	}

	sanitizedBody, sanitized, err := sanitizeEmptyBase64InputImagesInOpenAIBody(body)
	if err != nil {
		return nil, err
	}
	if sanitized {
		body = sanitizedBody
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
	if explicitImageIntent && !GroupAllowsImageGeneration(apiKeyGroup(apiKey)) {
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
	var resp *http.Response
	for {
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
		if resp.StatusCode < 400 {
			break
		}

		// 只读取一次响应体判断 task 是否失效；恢复失败时仍把原响应交给既有错误路径。
		probeBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(probeBody))
		if !agentTaskRecoveryTried && s.isAgentIdentityAccount(ctx, account) && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, probeBody) {
			agentTaskRecoveryTried = true
			expectedTaskID := account.GetCredential("task_id")
			if recoveryErr := s.recoverAgentIdentityTask(ctx, account, expectedTaskID); recoveryErr != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
			}
			continue
		}

		// 透传模式默认保持原样代理；容量错误以及 API-key 上游的瞬时
		// 5xx 应先触发多账号 failover；probeBody 已在 task 探测时读取，不再重复消费响应体。
		if shouldFailoverOpenAIPassthroughResponse(account, resp.StatusCode, probeBody) {
			return nil, s.handleFailoverErrorResponsePassthrough(ctx, resp, c, account, body, probeBody)
		}
		return nil, s.handleErrorResponsePassthrough(ctx, resp, c, account, body, probeBody)
	}
	defer func() { _ = resp.Body.Close() }()

	serviceTier := extractOpenAIServiceTierFromBody(body)

	var usage *OpenAIUsage
	var firstTokenMs *int
	responseID := ""
	imageCount := 0
	var imageOutputSizes []string
	var responseBody []byte
	if reqStream {
		result, err := s.handleStreamingResponsePassthrough(ctx, resp, c, account, startTime, reqModel, upstreamPassthroughModel)
		if err != nil {
			return nil, err
		}
		usage = result.usage
		firstTokenMs = result.firstTokenMs
		responseID = strings.TrimSpace(result.responseID)
		imageCount = result.imageCount
		imageOutputSizes = result.imageOutputSizes
		responseBody = result.responseBody
	} else {
		result, err := s.handleNonStreamingResponsePassthrough(ctx, resp, c, account, reqModel, upstreamPassthroughModel)
		if err != nil {
			return nil, err
		}
		usage = result.usage
		responseID = strings.TrimSpace(result.responseID)
		imageCount = result.imageCount
		imageOutputSizes = result.imageOutputSizes
		responseBody = result.responseBody
	}
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
		RequestID:       resp.Header.Get("x-request-id"),
		ResponseID:      responseID,
		Usage:           *usage,
		Model:           reqModel,
		UpstreamModel:   upstreamPassthroughModel,
		ServiceTier:     serviceTier,
		ReasoningEffort: reasoningEffort,
		Stream:          reqStream,
		OpenAIWSMode:    false,
		ResponseBody:    cloneDataSharingRequestBody(responseBody),
		Duration:        time.Since(startTime),
		FirstTokenMs:    firstTokenMs,
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
	case AccountTypeAPIKey:
		baseURL := account.GetOpenAIBaseURL()
		if baseURL != "" {
			validatedURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, err
			}
			targetURL = buildOpenAIResponsesURL(validatedURL)
		}
	}
	targetURL = appendOpenAIResponsesRequestPathSuffix(targetURL, openAIResponsesRequestPathSuffix(c))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))

	allowTimeoutHeaders := s.isOpenAIPassthroughTimeoutHeadersAllowed()
	if c != nil && c.Request != nil {
		for key, values := range c.Request.Header {
			lower := strings.ToLower(strings.TrimSpace(key))
			if !isOpenAIPassthroughAllowedRequestHeader(lower, allowTimeoutHeaders) {
				continue
			}
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
	}

	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")
	authHeaders, err := s.buildOpenAIAuthenticationHeaders(ctx, account, token)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	if account.Type == AccountTypeOAuth {
		// 当前 Codex OAuth HTTP 不再协商旧版 Responses 实验；透传路径可能接收
		// 旧客户端的标记，因此只移除该标记并保留其他独立 beta 协商项。
		stripOpenAILegacyResponsesBeta(req.Header)
		promptCacheKey := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
		req.Host = "chatgpt.com"
		if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
			return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
		}
		apiKeyID := getAPIKeyIDFromContext(c)

		clientSessionID := strings.TrimSpace(req.Header.Get("session_id"))
		clientConversationID := strings.TrimSpace(req.Header.Get("conversation_id"))
		if isOpenAIResponsesCompactPath(c) {
			req.Header.Set("accept", "application/json")
			if req.Header.Get("version") == "" {
				req.Header.Set("version", codexCLIVersion)
			}
			if clientSessionID == "" {
				clientSessionID = resolveOpenAICompactSessionID(c)
			}
		} else if req.Header.Get("accept") == "" {
			req.Header.Set("accept", "text/event-stream")
		}
		if req.Header.Get("originator") == "" {
			req.Header.Set("originator", openai.CodexDefaultOriginator)
		}
		if len(routerMatch) > 0 && routerMatch[0].Matched {
			if originator := strings.TrimSpace(routerMatch[0].UpstreamOriginator); originator != "" {
				req.Header.Set("originator", originator)
			}
		}

		if clientSessionID == "" {
			clientSessionID = promptCacheKey
		}
		if clientConversationID == "" {
			clientConversationID = promptCacheKey
		}
		if clientSessionID != "" {
			req.Header.Set("session_id", isolateOpenAISessionID(apiKeyID, clientSessionID))
		}
		if clientConversationID != "" {
			req.Header.Set("conversation_id", isolateOpenAISessionID(apiKeyID, clientConversationID))
		}
	} else if isOpenAIResponsesCompactPath(c) {
		// 透传白名单会放行客户端的 Accept: text/event-stream；compact 上游是
		// unary JSON 协议，API-key 账号同样强制 Accept，避免上游按 SSE 返回
		// （#3777 期望行为 4）。
		req.Header.Set("accept", "application/json")
	}

	s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, true, routerMatch...)

	// 终态收口：originator 必须与最终 User-Agent 首段配套且为官方身份，非官方 UA 整体回退为
	// 默认 Codex TUI 身份，同时避免 originator 与 UA 首段错配导致上游 404，详见 issue #3901。
	if account.Type == AccountTypeOAuth {
		enforceCodexIdentityHeaders(req.Header)
	}

	if req.Header.Get("content-type") == "" {
		req.Header.Set("content-type", "application/json")
	}

	account.ApplyHeaderOverrides(req.Header)
	setOpenAICodexRoutingHintFromBody(req.Header, account, body)
	logOpenAIRoutingDiagnosticsFromBody(ctx, account, "http_passthrough", req.Header, body, "not_applicable")

	return req, nil
}

func stripOpenAILegacyResponsesBeta(headers http.Header) {
	if headers == nil {
		return
	}

	preserved := make([]string, 0)
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), "OpenAI-Beta") {
			continue
		}
		delete(headers, key)
		for _, value := range values {
			parts := strings.Split(value, ",")
			kept := parts[:0]
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" || strings.EqualFold(part, "responses=experimental") {
					continue
				}
				kept = append(kept, part)
			}
			if len(kept) > 0 {
				preserved = append(preserved, strings.Join(kept, ", "))
			}
		}
	}
	for _, value := range preserved {
		headers.Add("OpenAI-Beta", value)
	}
}

func shouldFailoverOpenAIPassthroughResponse(account *Account, statusCode int, responseBody []byte) bool {
	if isOpenAIContextWindowError("", responseBody) {
		return false
	}
	if IsOpenAICyberWarningPayload(responseBody, "") {
		return false
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
	return newOpenAIUpstreamFailoverError(
		resp.StatusCode,
		resp.Header,
		body,
		upstreamMsg,
		decision.RetryableOnSameAccount(account, resp.StatusCode),
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
	responseBody     []byte
}

type openaiNonStreamingResultPassthrough struct {
	*OpenAIUsage
	usage            *OpenAIUsage
	responseID       string
	imageCount       int
	imageOutputSizes []string
	responseBody     []byte
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

func openAIStreamEventIsPreamble(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.created", "response.in_progress":
		return true
	default:
		return false
	}
}

func openAIStreamDataStartsClientOutput(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	switch strings.TrimSpace(eventType) {
	case "response.failed":
		return false
	case "error":
		// 上游降载/瞬时故障会先推 {"type":"error"} 帧、再以 response.failed 收尾。
		// 可重试类错误帧不能算客户端输出：一旦把它当首输出 flush，
		// clientOutputStarted 即被固化，随后的 failed 事件永远进不了 pre-output
		// failover 分支，只能把致命错误原样转发给客户端。不可重试类
		// （content_policy / invalid_request 等）维持原样转发，保留上游错误细节。
		payload := []byte(trimmed)
		return !openAIStreamFailedEventShouldFailover(payload, extractOpenAISSEErrorMessage(payload))
	}
	return !openAIStreamEventIsPreamble(eventType)
}

func openAIStreamItemHasVisibleOutput(item gjson.Result) bool {
	if item.Get("arguments").String() != "" || item.Get("input").String() != "" || item.Get("result").String() != "" {
		return true
	}
	for _, path := range []string{"content", "summary"} {
		for _, part := range item.Get(path).Array() {
			if part.Get("text").String() != "" || part.Get("transcript").String() != "" {
				return true
			}
		}
	}
	return false
}

// 结构进度可以提交当前 attempt 并解除首输出故障转移，但只有客户端可用内容才开始计算 TTFT。
func openAIStreamDataStartsVisibleOutput(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[DONE]" || !gjson.Valid(trimmed) {
		return false
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		eventType = strings.TrimSpace(gjson.Get(trimmed, "type").String())
	}
	if strings.HasSuffix(eventType, ".delta") {
		delta := gjson.Get(trimmed, "delta")
		return delta.Exists() && delta.String() != ""
	}
	switch eventType {
	case "response.output_text.done",
		"response.reasoning_summary_text.done",
		"response.reasoning_text.done",
		"response.audio_transcript.done":
		return gjson.Get(trimmed, "text").String() != ""
	case "response.function_call_arguments.done":
		return gjson.Get(trimmed, "arguments").String() != ""
	case "response.custom_tool_call_input.done":
		return gjson.Get(trimmed, "input").String() != ""
	case "response.image_generation_call.partial_image":
		return gjson.Get(trimmed, "partial_image_b64").String() != ""
	case "response.content_part.added", "response.content_part.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		part := gjson.Get(trimmed, "part")
		return part.Get("text").String() != "" || part.Get("transcript").String() != ""
	case "response.output_item.added", "response.output_item.done":
		return openAIStreamItemHasVisibleOutput(gjson.Get(trimmed, "item"))
	case "response.completed", "response.done":
		for _, item := range gjson.Get(trimmed, "response.output").Array() {
			if openAIStreamItemHasVisibleOutput(item) {
				return true
			}
		}
	}
	return false
}

// openAIStreamFailedEventErrorCode 提取流内 failed 事件的错误码（小写），
// 兼容 response.failed 的嵌套形态与裸 error 形态。
func openAIStreamFailedEventErrorCode(payload []byte) string {
	code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
	if code == "" {
		code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
	}
	return code
}

// isOpenAIUpstreamCapacityShedEvent 判断流内 failed 事件是否为上游容量降载信号。
// 上游在容量紧张时会把请求丢进降载路径：HTTP 200 之后立刻推 event: error
// （code=server_is_overloaded / slow_down）并以 response.failed 收尾。
func isOpenAIUpstreamCapacityShedEvent(payload []byte) bool {
	switch openAIStreamFailedEventErrorCode(payload) {
	case "server_is_overloaded", "slow_down":
		return true
	default:
		return false
	}
}

// openAICapacityShedRetryableClientCode 是把上游容量降载错误转发给客户端时改写
// 使用的错误码。Codex CLI 按闭集对错误码分类：server_is_overloaded / slow_down
// 被判为致命错误（客户端提示 "Selected model is at capacity. Please try a
// different model." 并直接终止会话），而 server_error 等致命集之外的错误码会进入
// 客户端内置的退避重试。
const openAICapacityShedRetryableClientCode = "server_error"

// sanitizeOpenAICapacityShedErrorCodeForClient 把即将写给下游客户端的
// error / response.failed 事件中的容量降载错误码改写为客户端可重试的错误码。
// 走到转发这一步说明网关侧 failover 已不可用（流中途）或已用尽；保留原始降载码
// 只会让客户端就地终止会话。错误消息原样保留；监控与账号状态判定都基于改写前
// 的原始 payload，不受影响。rate_limit 等其他错误码一律不动（客户端依赖
// rate_limit_exceeded 原码解析重试延时）。
func sanitizeOpenAICapacityShedErrorCodeForClient(payload []byte) ([]byte, bool) {
	if len(payload) == 0 || !gjson.ValidBytes(payload) || !isOpenAIUpstreamCapacityShedEvent(payload) {
		return payload, false
	}
	updated := payload
	changed := false
	for _, path := range []string{"response.error.code", "error.code"} {
		switch strings.ToLower(strings.TrimSpace(gjson.GetBytes(updated, path).String())) {
		case "server_is_overloaded", "slow_down":
		default:
			continue
		}
		next, err := sjson.SetBytes(updated, path, openAICapacityShedRetryableClientCode)
		if err != nil {
			return payload, false
		}
		updated = next
		changed = true
	}
	return updated, changed
}

func openAIStreamFailedEventSemanticStatus(payload []byte, message string) int {
	if isOpenAIContextWindowError(message, payload) {
		return http.StatusBadRequest
	}
	// 聚合上游可能在 HTTP 200 的 response.failed 中携带真实状态码；
	// 必须优先保留，才能让任意自定义错误码命中统一账号策略。
	for _, path := range []string{
		"response.error.status_code",
		"response.error.status",
		"error.status_code",
		"error.status",
	} {
		status := int(gjson.GetBytes(payload, path).Int())
		if status >= http.StatusBadRequest && status <= 599 {
			return status
		}
	}

	code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
	if code == "" {
		code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
	}
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.type").String()))
	if errType == "" {
		errType = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.type").String()))
	}
	combined := strings.TrimSpace(errType + " " + code + " " + strings.ToLower(strings.TrimSpace(message)))
	switch {
	// 上游可能用泛化 invalid_request 类型包装真实限流代码，限流信号优先。
	case strings.Contains(combined, "rate_limit"):
		return http.StatusTooManyRequests
	case strings.Contains(errType, "invalid_request"):
		return http.StatusBadRequest
	case strings.Contains(combined, "authentication") || strings.Contains(combined, "unauthorized") || strings.Contains(combined, "invalid_api_key"):
		return http.StatusUnauthorized
	case strings.Contains(combined, "permission") || strings.Contains(combined, "forbidden") || strings.Contains(combined, "access denied"):
		return http.StatusForbidden
	case code == "server_is_overloaded" || code == "slow_down":
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func openAIStreamFailureStatus(payload []byte, message string) int {
	if len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return http.StatusBadGateway
	}
	// 其他 response.failed 保持既有 502 语义；只将限流提升为可配置重试的 429。
	if openAIStreamFailedEventSemanticStatus(payload, message) == http.StatusTooManyRequests {
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}

// openAIStreamGenericFailedEventPayload 构造流已经提交后使用的通用失败终止事件。
func openAIStreamGenericFailedEventPayload() []byte {
	return []byte(`{"type":"response.failed","response":{"status":"failed","error":{"type":"upstream_error","message":"Upstream gateway error"}}}`)
}

// applyOpenAIStreamFailedAccountPolicy 将 HTTP 200 流内失败接入统一账号策略。
// status 是事件推断出的语义状态码，仅用于策略、故障转移和最终错误分类。
func (s *OpenAIGatewayService) applyOpenAIStreamFailedAccountPolicy(
	ctx context.Context,
	account *Account,
	canonicalModel string,
	headers http.Header,
	payload []byte,
	message string,
) (int, UpstreamErrorDecision) {
	status := openAIStreamFailedEventSemanticStatus(payload, message)
	if account != nil && account.Platform == PlatformGrok {
		return status, s.applyGrokAccountUpstreamError(ctx, account, status, headers, payload, canonicalModel)
	}
	if status == http.StatusTooManyRequests {
		return status, s.applyOpenAIAccountStreamRateLimitError(ctx, account, status, headers, payload, canonicalModel)
	}
	return status, s.applyOpenAIAccountUpstreamError(ctx, account, status, headers, payload, canonicalModel)
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
	if isOpenAIContextWindowError(message, payload) {
		return false
	}
	// response.failed 通过 HTTP 200 传输，必须优先按内部限流信号进入 429 重试策略。
	if openAIStreamFailureStatus(payload, message) == http.StatusTooManyRequests {
		return true
	}
	if isOpenAITransientProcessingError(http.StatusBadRequest, message, payload) {
		return true
	}
	if IsOpenAICyberWarningText(message) || IsOpenAICyberWarningText(string(payload)) {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
	if code == "" {
		code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
	}
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.type").String()))
	if errType == "" {
		errType = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.type").String()))
	}
	combined := strings.ToLower(strings.TrimSpace(message + " " + code + " " + errType))
	if combined == "" {
		return true
	}
	nonRetryableMarkers := []string{
		"invalid_request",
		"content_policy",
		"policy",
		"safety",
		"high-risk cyber",
		"not allowed",
		"violat",
	}
	for _, marker := range nonRetryableMarkers {
		if strings.Contains(combined, marker) {
			return false
		}
	}
	return true
}

// openAIStreamFailedEventRetryableOnSameAccount 在统一账号策略允许池模式绕过时，
// 额外保留 OpenAI 流内瞬态处理错误的同账号重试语义。
func openAIStreamFailedEventRetryableOnSameAccount(
	decision UpstreamErrorDecision,
	account *Account,
	statusCode int,
	payload []byte,
	message string,
) bool {
	if account == nil {
		return false
	}
	// 容量降载由客户端身份或模型容量触发，与当前账号健康无关；非池账号也应先
	// 做有界同账号重试，避免无意义地轮换并冷却整组账号。
	if isOpenAIUpstreamCapacityShedEvent(payload) {
		return true
	}
	if decision.Policy != ErrorPolicyPoolBypassed {
		return false
	}
	return account.IsPoolModeRetryableStatus(statusCode) ||
		isOpenAITransientProcessingError(http.StatusBadRequest, message, payload)
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
) *UpstreamFailoverError {
	return s.newOpenAIStreamPolicyFailoverError(
		c, account, passthrough, upstreamRequestID, nil, http.StatusBadGateway, payload, message, false,
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
	retryableOnSameAccount bool,
) *UpstreamFailoverError {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if message == "" {
		message = "OpenAI stream disconnected before completion"
	}
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
	failoverErr := newOpenAIUpstreamFailoverError(statusCode, responseHeaders, body, message, retryableOnSameAccount)
	failoverErr.RequestScopedTransient = isOpenAIUpstreamCapacityShedEvent(payload)
	return failoverErr
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
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if v := resp.Header.Get("x-request-id"); v != "" {
		c.Header("x-request-id", v)
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	usage := &OpenAIUsage{}
	imageCounter := newOpenAIImageOutputCounter()
	var firstTokenMs *int
	responseID := ""
	clientDisconnected := false
	sawDone := false
	sawTerminalEvent := false
	sawFailedEvent := false
	semanticOutputSeen := false
	failedMessage := ""
	var failedPayload []byte
	clientOutputStarted := false
	upstreamRequestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
	// pendingLines 在首个可见输出前保留前导事件，确保无输出失败仍可安全 failover。
	pendingLines := make([]string, 0, 8)
	// flushPending 表示已写入但未到 SSE 空行边界的脏状态；defer 兜底函数退出前的残留，断连后不再 Flush。
	flushPending := false
	flushPendingOutput := func() {
		if clientDisconnected || !flushPending {
			return
		}
		flusher.Flush()
		flushPending = false
	}
	defer flushPendingOutput()
	writePendingLines := func() bool {
		for _, pending := range pendingLines {
			if _, err := fmt.Fprintln(w, pending); err != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.openai_gateway", "[OpenAI passthrough] Client disconnected during streaming, continue draining upstream for usage: account=%d", account.ID)
				return false
			}
		}
		pendingLines = pendingLines[:0]
		return true
	}

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	defer putSSEScannerBuf64K(scanBuf)
	documentScanner := newOpenAISSEJSONDocumentScanner(scanner)

	needModelReplace := strings.TrimSpace(originalModel) != "" && strings.TrimSpace(mappedModel) != "" && strings.TrimSpace(originalModel) != strings.TrimSpace(mappedModel)
	var finalResponseBody []byte
	responseAccumulator := apicompat.NewBufferedResponseAccumulator()
	streamImageOutputs := make([]json.RawMessage, 0, 1)
	streamSeenImages := make(map[string]struct{})
	resultWithUsage := func() *openaiStreamingResultPassthrough {
		return &openaiStreamingResultPassthrough{
			usage:            usage,
			firstTokenMs:     firstTokenMs,
			responseID:       responseID,
			imageCount:       imageCounter.Count(),
			imageOutputSizes: imageCounter.Sizes(),
			responseBody:     cloneDataSharingRequestBody(finalResponseBody),
		}
	}

	for documentScanner.Scan() {
		line := documentScanner.Text()
		lineStartsClientOutput := false
		forceFlushFailedEvent := false
		if data, ok := extractOpenAISSEDataLine(line); ok {
			dataBytes := []byte(data)
			trimmedData := strings.TrimSpace(data)
			if needModelReplace && strings.Contains(data, mappedModel) {
				line = s.replaceModelInSSELine(line, mappedModel, originalModel)
				if replacedData, replaced := extractOpenAISSEDataLine(line); replaced {
					dataBytes = []byte(replacedData)
					trimmedData = strings.TrimSpace(replacedData)
				}
			}
			if normalizedData, normalized := normalizeOpenAIResponsesFunctionCallArguments(dataBytes); normalized {
				dataBytes = normalizedData
				trimmedData = strings.TrimSpace(string(normalizedData))
				line = "data: " + string(normalizedData)
			}
			if normalizedData, normalized := normalizeCompletedImageGenerationStatus(dataBytes); normalized {
				dataBytes = normalizedData
				trimmedData = strings.TrimSpace(string(normalizedData))
				line = "data: " + string(normalizedData)
			}
			if trimmedData != "[DONE]" {
				restoredData, restoreErr := restoreOpenAIResponsesNamespacePayload(c, dataBytes)
				if restoreErr != nil {
					return resultWithUsage(), fmt.Errorf("restore OpenAI passthrough namespace response: %w", restoreErr)
				}
				if !bytes.Equal(restoredData, dataBytes) {
					dataBytes = restoredData
					trimmedData = strings.TrimSpace(string(restoredData))
					line = "data: " + string(restoredData)
				}
			}
			eventType := strings.TrimSpace(gjson.Get(trimmedData, "type").String())
			if eventType == "response.failed" {
				failedMessage = extractOpenAISSEErrorMessage(dataBytes)
				failedPayload = append(failedPayload[:0], dataBytes...)
				s.parseSSEUsageBytes(dataBytes, usage)
				if hit, code, msg := detectOpenAICyberPolicy(dataBytes); hit {
					MarkOpsCyberPolicy(c, CyberPolicyMark{
						Code:           code,
						Message:        msg,
						Body:           truncateString(string(dataBytes), 4096),
						UpstreamStatus: http.StatusOK,
						UpstreamInTok:  usage.InputTokens,
						UpstreamOutTok: usage.OutputTokens,
					})
				}
				policyStatus, decision := s.applyOpenAIStreamFailedAccountPolicy(
					ctx, account, mappedModel, resp.Header, dataBytes, failedMessage,
				)
				outputStarted := openAIStreamClientOutputStarted(c, clientOutputStarted)
				if !outputStarted && decision.ShouldReturnGenericError() {
					MarkResponseCommitted(c)
					writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
					return resultWithUsage(), fmt.Errorf("upstream response failed: status=%d (not in custom error codes)", policyStatus)
				}
				if !outputStarted && decision.ShouldFailover(
					account, policyStatus, openAIStreamFailedEventShouldFailover(dataBytes, failedMessage),
				) {
					return resultWithUsage(), s.newOpenAIStreamPolicyFailoverError(
						c, account, true, upstreamRequestID, resp.Header, policyStatus, dataBytes, failedMessage,
						openAIStreamFailedEventRetryableOnSameAccount(decision, account, policyStatus, dataBytes, failedMessage),
					)
				}
				if outputStarted && decision.ShouldReturnGenericError() {
					// 流已提交时无法改写 HTTP 状态，只下发净化后的通用终止事件。
					dataBytes = openAIStreamGenericFailedEventPayload()
					trimmedData = string(dataBytes)
					line = "data: " + string(dataBytes)
					failedMessage = "Upstream gateway error"
				}
				if !outputStarted && !decision.ShouldReturnGenericError() {
					if status, errType, errMsg, matched := applyOpenAIStreamFailedErrorPassthroughRule(c, account.Platform, dataBytes, failedMessage); matched {
						// 命中透传规则也要记录 ops 上游错误事件（对齐 CC/Messages 与
						// antigravity 先例），否则透传命中的 failed 在监控中不可见。
						s.recordOpenAIStreamUpstreamError(c, account, true, upstreamRequestID, "http_error", dataBytes, failedMessage)
						MarkResponseCommitted(c)
						c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
						c.JSON(status, gin.H{
							"error": gin.H{
								"type":    errType,
								"message": errMsg,
							},
						})
						return resultWithUsage(), fmt.Errorf("upstream response failed: passthrough rule matched message=%s", errMsg)
					}
				}
				forceFlushFailedEvent = true
				sawFailedEvent = true
			}
			if trimmedData == "[DONE]" {
				sawDone = true
			}
			if openAIStreamEventIsTerminal(trimmedData) {
				sawTerminalEvent = true
			}
			if responseID == "" {
				responseID = extractOpenAIResponseIDFromJSONBytes(dataBytes)
			}
			var responseEvent apicompat.ResponsesStreamEvent
			if err := json.Unmarshal(dataBytes, &responseEvent); err == nil {
				responseAccumulator.ProcessEvent(&responseEvent)
			}
			if imageOutput, ok := extractImageGenerationOutputFromSSEData(dataBytes, streamSeenImages); ok {
				streamImageOutputs = append(streamImageOutputs, imageOutput)
			}
			if normalizedData, normalized := normalizeResponsesStreamingTerminalOutput(dataBytes, responseAccumulator, streamImageOutputs); normalized {
				dataBytes = normalizedData
				data = string(normalizedData)
				trimmedData = strings.TrimSpace(data)
				line = "data: " + data
				eventType = strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
			}
			if eventType == "response.completed" || eventType == "response.done" {
				if response := gjson.GetBytes(dataBytes, "response"); response.Exists() && response.Type == gjson.JSON && response.Raw != "" {
					finalResponseBody = []byte(response.Raw)

					if len(gjson.GetBytes(finalResponseBody, "output").Array()) == 0 {
						if outputJSON, reconstructed := buildResponsesOutputJSON(responseAccumulator, streamImageOutputs); reconstructed {
							if patched, err := sjson.SetRawBytes(finalResponseBody, "output", outputJSON); err == nil {
								finalResponseBody = patched
							}
						}
					}
				}
			}
			imageCounter.AddSSEData(dataBytes)
			if sanitizedData, sanitized := sanitizeOpenAIResponseFailedEventForClient(
				dataBytes,
				eventType,
				openAIStreamClientOutputStarted(c, clientOutputStarted),
			); sanitized {
				dataBytes = sanitizedData
				trimmedData = strings.TrimSpace(string(sanitizedData))
				line = "data: " + string(sanitizedData)
			}
			lineStartsClientOutput = forceFlushFailedEvent || openAIStreamDataStartsClientOutput(trimmedData, eventType)
			if lineStartsClientOutput && trimmedData != "[DONE]" && !openAIStreamEventTypeIsTerminal(eventType) {
				semanticOutputSeen = true
			}
			// 透传流在写出前也要识别空 completed，确保仍可安全切换账号。
			if (eventType == "response.completed" || eventType == "response.done") &&
				!sawFailedEvent && !semanticOutputSeen && !clientOutputStarted &&
				openAIResponsesCompletedEventIsEmpty(dataBytes, usage) {
				return resultWithUsage(), newOpenAIResponsesEmptyCompletedFailoverError(c, account, upstreamRequestID)
			}
			if firstTokenMs == nil && openAIStreamDataStartsVisibleOutput(trimmedData, eventType) {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}
			if eventType != "response.failed" {
				s.parseSSEUsageBytes(dataBytes, usage)
			}
		}

		if !clientDisconnected {
			if !clientOutputStarted && !lineStartsClientOutput {
				pendingLines = append(pendingLines, line)
				continue
			}
			if !clientOutputStarted && len(pendingLines) > 0 {
				if !writePendingLines() {
					continue
				}
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				clientDisconnected = true
				logger.LegacyPrintf("service.openai_gateway", "[OpenAI passthrough] Client disconnected during streaming, continue draining upstream for usage: account=%d", account.ID)
			} else {
				clientOutputStarted = true
				flushPending = true
				if line == "" {
					flushPendingOutput()
				}
			}
		}
	}
	if err := documentScanner.Err(); err != nil {
		if (sawDone || sawTerminalEvent) && !sawFailedEvent {
			s.clearOpenAIProxyStreamDisconnect(account)
			return resultWithUsage(), nil
		}
		if sawFailedEvent {
			err := fmt.Errorf("upstream response failed: %s", failedMessage)
			return resultWithUsage(), wrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, failedPayload, failedMessage, err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", err)
		}
		if errors.Is(err, bufio.ErrTooLong) {
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI passthrough] SSE line too long: account=%d max_size=%d error=%v", account.ID, maxLineSize, err)
			return resultWithUsage(), err
		}
		if !openAIStreamClientOutputStarted(c, clientOutputStarted) {
			msg := "OpenAI stream disconnected before completion"
			if errText := strings.TrimSpace(err.Error()); errText != "" {
				msg += ": " + errText
			}
			return resultWithUsage(),
				s.newOpenAIStreamFailoverError(c, account, true, upstreamRequestID, nil, msg)
		}
		if clientDisconnected {
			return resultWithUsage(), fmt.Errorf("stream usage incomplete after disconnect: %w", err)
		}
		s.recordOpenAIProxyStreamDisconnect(account, err, upstreamRequestID)
		logger.LegacyPrintf("service.openai_gateway",
			"[OpenAI passthrough] 流读取异常中断: account=%d request_id=%s err=%v",
			account.ID,
			upstreamRequestID,
			err,
		)
		return resultWithUsage(), fmt.Errorf("stream read error: %w", err)
	}
	if sawFailedEvent {
		err := fmt.Errorf("upstream response failed: %s", failedMessage)
		return resultWithUsage(), wrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, failedPayload, failedMessage, err)
	}
	if !clientDisconnected && !sawDone && !sawTerminalEvent && ctx.Err() == nil {
		logger.FromContext(ctx).With(
			zap.String("component", "service.openai_gateway"),
			zap.Int64("account_id", account.ID),
			zap.String("upstream_request_id", upstreamRequestID),
		).Info("OpenAI passthrough 上游流在未收到 [DONE] 时结束，疑似断流")
		if !openAIStreamClientOutputStarted(c, clientOutputStarted) {
			return resultWithUsage(),
				s.newOpenAIStreamFailoverError(c, account, true, upstreamRequestID, nil, "OpenAI stream ended before a terminal event")
		}
		s.recordOpenAIProxyStreamDisconnect(account, errors.New("stream ended before terminal event"), upstreamRequestID)
		return resultWithUsage(), errors.New("stream usage incomplete: missing terminal event")
	}
	if (sawDone || sawTerminalEvent) && !sawFailedEvent {
		s.clearOpenAIProxyStreamDisconnect(account)
	}

	return resultWithUsage(), nil
}

func (s *OpenAIGatewayService) handleNonStreamingResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	originalModel string,
	mappedModel string,
) (*openaiNonStreamingResultPassthrough, error) {
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}

	if isEventStreamResponse(resp.Header) {
		return s.handlePassthroughSSEToJSON(ctx, resp, c, account, body, originalModel, mappedModel)
	}

	usage := &OpenAIUsage{}
	usageParsed := false
	if len(body) > 0 {
		if parsedUsage, ok := extractOpenAIUsageFromJSONBytes(body); ok {
			*usage = parsedUsage
			usageParsed = true
		}
	}
	if !usageParsed {

		usage = s.parseSSEUsageFromBody(string(body))
	}

	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	if originalModel != "" && mappedModel != "" && originalModel != mappedModel {
		body = s.replaceModelInResponseBody(body, mappedModel, originalModel)
	}
	body, err = restoreOpenAIResponsesNamespacePayload(c, body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI passthrough namespace response: %w", err)
	}
	if !writeOpenAICompactSSEBridge(c, resp.StatusCode, body) {
		c.Data(resp.StatusCode, contentType, body)
	}
	return &openaiNonStreamingResultPassthrough{
		OpenAIUsage:      usage,
		usage:            usage,
		responseID:       extractOpenAIResponseIDFromJSONBytes(body),
		imageCount:       countOpenAIResponseImageOutputsFromJSONBytes(body),
		imageOutputSizes: collectOpenAIResponseImageOutputSizesFromJSONBytes(body),
		responseBody:     cloneDataSharingRequestBody(body),
	}, nil
}

// handlePassthroughSSEToJSON converts an SSE response body into a JSON
// response for the passthrough path. It mirrors handleSSEToJSON while
// preserving passthrough payloads, except compact-only model remapping may
// rewrite model fields back to the original requested model.
func (s *OpenAIGatewayService) handlePassthroughSSEToJSON(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, body []byte, originalModel string, mappedModel string) (*openaiNonStreamingResultPassthrough, error) {
	bodyText := string(body)
	finalResponse, ok := extractCodexFinalResponse(bodyText)

	usage := &OpenAIUsage{}
	if ok {
		if parsedUsage, parsed := extractOpenAIUsageFromJSONBytes(finalResponse); parsed {
			*usage = parsedUsage
		}

		if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 {
			if outputJSON, reconstructed := reconstructResponseOutputFromSSE(bodyText); reconstructed {
				if patched, err := sjson.SetRawBytes(finalResponse, "output", outputJSON); err == nil {
					finalResponse = patched
				}
			}
		}
		finalResponse = supplementCompactionItemFromSSE(c, finalResponse, bodyText)
		body = finalResponse
		if originalModel != "" && mappedModel != "" && originalModel != mappedModel {
			body = s.replaceModelInResponseBody(body, mappedModel, originalModel)
		}

		body = s.correctToolCallsInResponseBody(body)
		restoredBody, restoreErr := restoreOpenAIResponsesNamespacePayload(c, body)
		if restoreErr != nil {
			return nil, fmt.Errorf("restore OpenAI passthrough namespace response: %w", restoreErr)
		}
		body = restoredBody
	} else {
		terminalType, terminalPayload, terminalOK := extractOpenAISSETerminalEvent(bodyText)
		if terminalOK && terminalType == "response.failed" {
			msg := extractOpenAISSEErrorMessage(terminalPayload)
			if msg == "" {
				msg = "Upstream compact response failed"
			}
			policyStatus, decision := s.applyOpenAIStreamFailedAccountPolicy(
				ctx, account, mappedModel, resp.Header, terminalPayload, msg,
			)
			if decision.ShouldReturnGenericError() {
				MarkResponseCommitted(c)
				writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
				return nil, fmt.Errorf("upstream compact response failed: status=%d (not in custom error codes)", policyStatus)
			}
			if decision.ShouldFailover(account, policyStatus, openAIStreamFailedEventShouldFailover(terminalPayload, msg)) {
				return nil, s.newOpenAIStreamPolicyFailoverError(
					c, account, true, strings.TrimSpace(resp.Header.Get("x-request-id")), resp.Header,
					policyStatus, terminalPayload, msg,
					openAIStreamFailedEventRetryableOnSameAccount(decision, account, policyStatus, terminalPayload, msg),
				)
			}
			err := s.writeOpenAINonStreamingProtocolError(resp, c, msg)
			return nil, wrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, terminalPayload, msg, err)
		}
		usage = s.parseSSEUsageFromBody(bodyText)
		if originalModel != "" && mappedModel != "" && originalModel != mappedModel {
			bodyText = s.replaceModelInSSEBody(bodyText, mappedModel, originalModel)
		}
		body = []byte(bodyText)
	}

	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	contentType := "application/json; charset=utf-8"
	if !ok {
		contentType = resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "text/event-stream"
		}
	}
	if !writeOpenAICompactSSEBridge(c, resp.StatusCode, body) {
		c.Data(resp.StatusCode, contentType, body)
	}

	return &openaiNonStreamingResultPassthrough{
		OpenAIUsage:      usage,
		usage:            usage,
		responseID:       extractOpenAIResponseIDFromJSONBytes(body),
		imageCount:       countOpenAIImageOutputsFromSSEBody(bodyText),
		imageOutputSizes: collectOpenAIImageOutputSizesFromSSEBody(bodyText),
		responseBody:     cloneDataSharingRequestBody(body),
	}, nil
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
}
