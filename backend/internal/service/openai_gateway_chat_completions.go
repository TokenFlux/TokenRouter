package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// cursorResponsesUnsupportedFields are top-level Responses API parameters that
// Codex upstreams reject with "Unsupported parameter: ...". They must be
// stripped when forwarding a raw client body through the Responses-shape
// short-circuit in ForwardAsChatCompletions (see isResponsesShape branch).
// The normal Chat Completions → Responses conversion path is unaffected
// because ChatCompletionsRequest has no fields for these parameters — unknown
// fields are dropped naturally by json.Unmarshal. Kept semantically in sync
// with the list in openai_gateway_service.go:2034 used by the /v1/responses
// passthrough path.
var cursorResponsesUnsupportedFields = []string{
	"prompt_cache_retention",
	"safety_identifier",
	"metadata",
	"stream_options",
}

// ForwardAsChatCompletions accepts a Chat Completions request body, converts it
// to OpenAI Responses API format, forwards to the OpenAI upstream, and converts
// the response back to Chat Completions format.
//
// 历史背景：该函数原本对所有 OpenAI 账号无差别走 CC→Responses 转换 + /v1/responses
// 端点——这在 OAuth（ChatGPT 内部 API 仅支持 Responses）和官方 APIKey 账号上是
// 正确的，但 sub2api 接入 DeepSeek/Kimi/GLM 等第三方 OpenAI 兼容上游后假设破裂：
// 这些上游普遍只支持 /v1/chat/completions，无 /v1/responses 端点。
//
// 当前路由策略由客户端首选协议、账号协议配置和 Responses 探测状态共同决定。
func (s *OpenAIGatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	return s.forwardAsChatCompletions(ctx, c, account, body, promptCacheKey, defaultMappedModel, false, tlsRouterMatch...)
}

func (s *OpenAIGatewayService) forwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
	compatPromptCacheTenantIsolated bool,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	var routeErr error
	account, routeErr = accountForProtocolAttempt(ctx, account)
	if routeErr != nil {
		return nil, routeErr
	}
	beginUpstreamResponseModelObservation(c)
	ClearActualOpenAIUpstreamEndpoint(c)
	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions")
	}
	setCodexToolNameReverse(c, nil)
	if _, err := s.prepareCodexAccountIdentitySource(ctx, c, account); err != nil {
		return nil, err
	}

	var routerMatch TLSFingerprintRouterMatchResult
	if len(tlsRouterMatch) > 0 {
		routerMatch = tlsRouterMatch[0]
	} else {
		routerMatch = s.matchTLSFingerprintRouter(c, account)
	}
	restrictionResult := s.detectCodexClientRestriction(c, account, routerMatch)
	logCodexCLIOnlyDetection(ctx, c, account, getAPIKeyIDFromContext(c), restrictionResult, body)
	if restrictionResult.Enabled && !restrictionResult.Matched {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "forbidden_error",
				"message": "This account only allows Codex official clients",
			},
		})
		return nil, errors.New("codex_cli_only restriction: only codex official clients are allowed")
	}
	if account.resolvedProtocol != "" && account.resolvedProtocol != domain.ProtocolOpenAIResponses && !gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "input").Exists() {
		var request protocolopenai.ResponsesRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		converted, err := apicompat.ResponsesToChatCompletionsRequestWithOptions(&request, &apicompat.ResponsesToChatOptions{ReasoningContentByID: s.reasoningContentByID})
		if err != nil {
			return nil, err
		}
		body, err = json.Marshal(converted)
		if err != nil {
			return nil, err
		}
	}
	if account.Platform == PlatformGrok {
		if account.resolvedProtocol == domain.ProtocolOpenAIChatCompletions {
			return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel, tlsRouterMatch...)
		}
		if account.resolvedProtocol == domain.ProtocolOpenAIResponses {
			if eligible, reason := grokChatResponsesBridgeEligibility(body); !eligible {
				return nil, fmt.Errorf("configured Grok Responses conversion cannot preserve request: %s", reason)
			}
			return s.forwardGrokChatCompletionsViaResponses(ctx, c, account, body, promptCacheKey, defaultMappedModel, tlsRouterMatch...)
		}
		if account.IsGrokOAuth() {
			if eligible, reason := grokChatResponsesBridgeEligibility(body); eligible {
				return s.forwardGrokChatCompletionsViaResponses(ctx, c, account, body, promptCacheKey, defaultMappedModel, tlsRouterMatch...)
			} else {
				logger.L().Debug("grok chat_completions: using raw fallback",
					zap.Int64("account_id", account.ID),
					zap.String("reason", reason),
				)
			}
		}
		return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel, tlsRouterMatch...)
	}
	if err := validateOpenAIReasoningEffort(body, gjson.GetBytes(body, "model").String()); err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	// 某些客户端会把 Responses 形状请求发送到 Chat Completions URL；必须先于
	// 自适应协议分流识别，否则会把 input 原样发给只接受 messages 的上游。
	isResponsesShape := !gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "input").Exists()

	// 自适应账号的标准 Chat 入站使用供应商原生 CC 端点；Responses 形状下，
	// DeepSeek / Kimi 保留原生 Responses，智谱先转换为 Chat。
	if account.IsAdaptiveAPIProtocol() {
		if !isResponsesShape {
			return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel, tlsRouterMatch...)
		}
		if !account.SupportsNativeCNResponses() {
			var responsesReq protocolopenai.ResponsesRequest
			if err := json.Unmarshal(body, &responsesReq); err != nil {
				return nil, fmt.Errorf("parse responses-shaped chat completions request: %w", err)
			}
			chatReq, err := apicompat.ResponsesToChatCompletionsRequestWithOptions(
				&responsesReq,
				&apicompat.ResponsesToChatOptions{ReasoningContentByID: s.reasoningContentByID},
			)
			if err != nil {
				return nil, fmt.Errorf("convert responses-shaped chat completions request: %w", err)
			}
			chatBody, err := json.Marshal(chatReq)
			if err != nil {
				return nil, fmt.Errorf("marshal converted chat completions request: %w", err)
			}
			return s.forwardAsRawChatCompletions(ctx, c, account, chatBody, defaultMappedModel, tlsRouterMatch...)
		}
		// DeepSeek / Kimi 原生 Responses 请求继续走下方 Responses→Chat 回程转换。
	}

	// 固定 Anthropic 协议走原生 Anthropic 端点。
	if account.IsAnthropicProtocol() {
		return s.forwardChatCompletionsViaNativeAnthropic(ctx, c, account, body, defaultMappedModel)
	}

	// 固定 Chat 协议的 CN 账号，以及其他 APIKey 账号在探测/管理员策略要求
	// Chat 时，均走 CC 直转。
	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel, tlsRouterMatch...)
	}
	if !account.IsCNProvider() && resolveOpenAITextProtocolForAttempt(
		c,
		account,
		openai_compat.TextProtocolChatCompletions,
	) == openai_compat.TextProtocolChatCompletions {
		return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel, tlsRouterMatch...)
	}

	startTime := time.Now()

	// 1. Parse Chat Completions request
	var chatReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream

	// 2. Resolve model mapping early so compat prompt_cache_key injection can
	// derive a stable seed from the final upstream model family.
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)

	promptCacheKey = strings.TrimSpace(promptCacheKey)
	compatPromptCacheInjected := false
	if promptCacheKey == "" && !isResponsesShape && (account.UsesOpenAICodexProtocol() || account.IsOpenAIApiKey()) && shouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
		promptCacheKey = deriveCompatPromptCacheKey(&chatReq, upstreamModel)
		compatPromptCacheInjected = promptCacheKey != ""
		if compatPromptCacheInjected && account.IsOpenAIApiKey() {
			promptCacheKey = isolateOpenAISessionID(getAPIKeyIDFromContext(c), promptCacheKey)
			compatPromptCacheTenantIsolated = true
		}
	}

	// 3. Build the upstream (Responses API) body.
	//
	// Cursor compatibility: some clients (notably Cursor cloud) send Responses
	// API shaped bodies — `input: [...]` with no `messages` field — to the
	// /v1/chat/completions URL. Running those through ChatCompletionsToResponses
	// would silently drop Cursor's `input` array (the struct has no Input field)
	// and produce `input: null`, which Codex upstreams reject with
	// "Invalid type for 'input': expected a string, but got an object".
	//
	// Forward that shape as-is, only rewriting `model`
	// to the resolved upstream model. The downstream codex OAuth transform will
	// still normalize store/stream/instructions/etc.
	var (
		responsesReq  *protocolopenai.ResponsesRequest
		responsesBody []byte
		err           error
	)
	if isResponsesShape {
		responsesBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model in responses-shape body: %w", err)
		}
		// Strip Responses API parameters that no Codex upstream accepts.
		// Because this branch forwards the raw body (the normal path rebuilds
		// it from ChatCompletionsRequest and drops unknown fields naturally),
		// we must filter these fields explicitly here — otherwise the upstream
		// rejects the request with "Unsupported parameter: ...".
		for _, field := range cursorResponsesUnsupportedFields {
			if stripped, derr := sjson.DeleteBytes(responsesBody, field); derr == nil {
				responsesBody = stripped
			}
		}
		var normalizedServiceTier string
		responsesBody, normalizedServiceTier, err = normalizeResponsesBodyServiceTier(responsesBody)
		if err != nil {
			return nil, fmt.Errorf("normalize service_tier in responses-shape body: %w", err)
		}
		// Minimal stub populated from the raw body so downstream ServiceTier
		// propagation keeps working.
		responsesReq = &protocolopenai.ResponsesRequest{
			Model:       upstreamModel,
			ServiceTier: normalizedServiceTier,
		}
	} else {
		// Normal path: convert Chat Completions → Responses.
		// ChatCompletionsToResponses always sets Stream=true (upstream always streams).
		responsesReq, err = apicompat.ChatCompletionsToResponses(&chatReq)
		if err != nil {
			return nil, fmt.Errorf("convert chat completions to responses: %w", err)
		}
		responsesReq.Model = upstreamModel
		normalizeResponsesRequestServiceTier(responsesReq)
		responsesBody, err = json.Marshal(responsesReq)
		if err != nil {
			return nil, fmt.Errorf("marshal responses request: %w", err)
		}
	}

	logFields := []zap.Field{
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
		zap.Bool("responses_shape", isResponsesShape),
	}
	if compatPromptCacheInjected {
		logFields = append(logFields,
			zap.Bool("compat_prompt_cache_key_injected", true),
			zap.String("compat_prompt_cache_key_sha256", hashSensitiveValueForLog(promptCacheKey)),
		)
	}
	logger.L().Debug("openai chat_completions: model mapping applied", logFields...)

	if account.UsesOpenAICodexProtocol() {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for codex transform: %w", err)
		}
		isJSONObjectFormat := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(responsesBody, "text.format.type").String()), "json_object")
		codexResult := applyCodexOAuthTransformWithOptions(reqBody, codexOAuthTransformOptions{
			SkipDefaultInstructions:             !isResponsesShape,
			OmitPromotedSystemMessagesFromInput: !isResponsesShape && !isJSONObjectFormat,
		})
		if codexResult.Error != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": codexResult.Error.Error()}})
			return nil, codexResult.Error
		}
		setCodexToolNameReverse(c, codexResult.ToolNameReverse)
		if !isResponsesShape {
			ensureCodexOAuthInstructionsField(reqBody)
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		} else if promptCacheKey != "" {
			reqBody["prompt_cache_key"] = promptCacheKey
		}
		applyCodexAccountIdentityClientMetadataMap(reqBody, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c))
		responsesBody, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("remarshal after codex transform: %w", err)
		}
	}
	if account.Type == AccountTypeAPIKey {
		if trimmedKey := strings.TrimSpace(promptCacheKey); trimmedKey != "" {
			var reqBody map[string]any
			if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
				return nil, fmt.Errorf("unmarshal for prompt cache key injection: %w", err)
			}
			// API Key 账号的 Chat Completions 转 Responses 路径不会经过 Codex transform，
			// 需要在这里把入口解析出的 prompt_cache_key 补回上游请求体。
			if existing, ok := reqBody["prompt_cache_key"].(string); !ok || strings.TrimSpace(existing) == "" {
				reqBody["prompt_cache_key"] = trimmedKey
				responsesBody, err = json.Marshal(reqBody)
				if err != nil {
					return nil, fmt.Errorf("remarshal after prompt cache key injection: %w", err)
				}
			}
		}
	}

	// 4b. Apply OpenAI fast policy (may filter service_tier or block the request).
	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, responsesBody)
	if policyErr != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(policyErr, &blocked) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeChatCompletionsError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, policyErr
	}
	responsesBody = updatedBody
	// Usage Log 记录最终实际发送给上游的 effort，避免把转换过程中被丢弃的
	// Chat Completions 非标准字段误记为已转发。
	reasoningEffort := extractEffectiveOpenAIReasoningEffortFromBody(responsesBody, body, upstreamModel, billingModel, originalModel)
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, responsesBody, billingModel)
	if serviceTier := extractOpenAIServiceTierFromBody(responsesBody); serviceTier != nil {
		responsesReq.ServiceTier = *serviceTier
	} else {
		responsesReq.ServiceTier = ""
	}

	// 5. Get access token
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 6. Build upstream request
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	upstreamReq, err := s.buildUpstreamRequest(upstreamCtx, c, account, responsesBody, token, true, promptCacheKey, false, tlsRouterMatch...)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	if promptCacheKey != "" {
		apiKeyID := getAPIKeyIDFromContext(c)
		sessionKey := promptCacheKey
		if !compatPromptCacheTenantIsolated {
			sessionKey = isolateOpenAIUpstreamSessionID(apiKeyID, codexAccountIdentitySource(c, account), promptCacheKey)
		}
		upstreamReq.Header.Set("session_id", generateSessionUUID(sessionKey))
	}

	// 7. Send request
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	// 8. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := s.readOpenAIUpstreamError(resp)
		if !agentIdentityTaskRecoveryWasTried(ctx) && s.isAgentIdentityAccount(ctx, account) && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
			expectedTaskID := account.GetCredential("task_id")
			if err := s.recoverAgentIdentityTask(ctx, account, expectedTaskID); err != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
			}
			return s.forwardAsChatCompletions(markAgentIdentityTaskRecoveryTried(ctx), c, account, body, promptCacheKey, defaultMappedModel, compatPromptCacheTenantIsolated, tlsRouterMatch...)
		}
		respBody = s.redactAgentIdentitySensitiveBody(ctx, account, respBody)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
	}

	// 9. Handle normal response
	var result *OpenAIForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleChatStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime, len(body))
	} else {
		result, handleErr = s.handleChatBufferedStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime)
	}
	if GetOpsCyberPolicy(c) != nil {
		if handleErr == nil {
			handleErr = errOpenAICyberPolicyForwarded
		}
		return nil, handleErr
	}

	// Propagate ServiceTier and ReasoningEffort to result for billing.
	// 计费 tier 优先采用上游回显值；上游未回显时回退到最终出站 body（经过
	// fast policy filter/force 之后）里的 tier，policy filter 删掉字段后不再
	// 按原请求 Fast 计费。
	if handleErr == nil && result != nil {
		if tier := resolvedOpenAIUpstreamServiceTier(c, extractOpenAIServiceTierFromBody(responsesBody)); tier != nil {
			result.ServiceTier = tier
		}
		result.ReasoningEffort = reasoningEffort
	}

	// Extract and save Codex usage snapshot from response headers (for OAuth accounts).
	// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
	if handleErr == nil && account.UsesOpenAICodexProtocol() && !account.IsShadow() {
		if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
			s.updateCodexUsageSnapshot(ctx, account.ID, snapshot)
		}
	}

	return result, handleErr
}

func normalizeResponsesRequestServiceTier(req *protocolopenai.ResponsesRequest) {
	if req == nil {
		return
	}
	req.ServiceTier = normalizedOpenAIServiceTierValue(req.ServiceTier)
}

func normalizeResponsesBodyServiceTier(body []byte) ([]byte, string, error) {
	if len(body) == 0 {
		return body, "", nil
	}
	rawServiceTier := gjson.GetBytes(body, "service_tier").String()
	if rawServiceTier == "" {
		return body, "", nil
	}
	normalizedServiceTier := normalizedOpenAIServiceTierValue(rawServiceTier)
	if normalizedServiceTier == "" {
		trimmed, err := sjson.DeleteBytes(body, "service_tier")
		return trimmed, "", err
	}
	if normalizedServiceTier == rawServiceTier {
		return body, normalizedServiceTier, nil
	}
	trimmed, err := sjson.SetBytes(body, "service_tier", normalizedServiceTier)
	return trimmed, normalizedServiceTier, err
}

func normalizedOpenAIServiceTierValue(raw string) string {
	normalized := normalizeOpenAIServiceTier(raw)
	if normalized == nil {
		return ""
	}
	return *normalized
}

func openAICompatFailedResponseMessage(resp *protocolopenai.ResponsesResponse) string {
	if resp == nil || resp.Error == nil {
		return ""
	}
	return strings.TrimSpace(resp.Error.Message)
}

// handleChatCompletionsErrorResponse reads an upstream error and returns it in
// OpenAI Chat Completions error format.
func (s *OpenAIGatewayService) handleChatCompletionsErrorResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestedModel ...string,
) (*OpenAIForwardResult, error) {
	return s.handleCompatErrorResponse(resp, c, account, writeChatCompletionsError, writeChatCompletionsErrorBody, requestedModel...)
}

func (s *OpenAIGatewayService) handleChatBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	result, err := nativeopenai.ReadChatBuffered(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeChatResponseOptions(c, account, resp, originalModel, billingModel, upstreamModel), originalModel, upstreamModel, startTime)
	return chatForwardResult(result, billingModel), err
}

func (s *OpenAIGatewayService) newOpenAICompatBufferedReadFailoverError(
	c *gin.Context,
	account *Account,
	resp *http.Response,
	requestID string,
	err error,
) error {
	var readErr *openAICompatBufferedReadError
	if !errors.As(err, &readErr) || readErr == nil || errors.Is(readErr.Unwrap(), bufio.ErrTooLong) {
		return err
	}
	var requestContext context.Context
	if c != nil && c.Request != nil {
		requestContext = c.Request.Context()
	}
	if !shouldClassifyOpenAIUpstreamStreamReadError(readErr.Unwrap(), requestContext) {
		return err
	}
	classifiedErr := newOpenAIUpstreamStreamReadError(readErr.Unwrap())
	code, message, ok := OpenAIUpstreamStreamReadErrorDetails(classifiedErr)
	if !ok {
		return err
	}
	payload, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"code":    code,
			"message": message,
		},
	})
	var responseHeaders http.Header
	if resp != nil {
		responseHeaders = resp.Header
	}
	failoverErr := s.newOpenAIStreamPolicyFailoverError(
		c, account, false, requestID, responseHeaders, http.StatusBadGateway, payload, message, false,
	)
	// 保留稳定错误码，确保重试耗尽后客户端和透传规则仍能识别传输故障。
	failoverErr.ResponseBody = payload
	return failoverErr
}

func (s *OpenAIGatewayService) handleChatStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
	requestBodyLen int,
) (*OpenAIForwardResult, error) {
	result, err := nativeopenai.ReadChatStreaming(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeChatResponseOptions(c, account, resp, originalModel, billingModel, upstreamModel), originalModel, upstreamModel, startTime, requestBodyLen)
	return chatForwardResult(result, billingModel), err
}

// writeChatCompletionsError writes an error response in OpenAI Chat Completions format.
func writeChatCompletionsError(c *gin.Context, statusCode int, errType, message string) {
	MarkResponseCommitted(c)
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// writeChatCompletionsErrorBody 保留 OpenAI 错误对象的全部结构化字段。
func writeChatCompletionsErrorBody(c *gin.Context, statusCode int, body []byte) {
	MarkResponseCommitted(c)
	c.Data(statusCode, "application/json; charset=utf-8", body)
}

// buildChatStreamErrorSSE 构造一个 Chat Completions SSE 错误帧，用于终止 cyber_policy 流。
func buildChatStreamErrorSSE(code, message string) string {
	payload, err := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
	if err != nil {
		return `data: {"error":{"type":"invalid_request_error","code":"cyber_policy","message":"Request blocked by upstream cyber-security policy"}}` + "\n\n"
	}
	return "data: " + string(payload) + "\n\n"
}
