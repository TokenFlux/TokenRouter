package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIWSClientReadLimitBytesDefault     int64 = 64 * 1024 * 1024
	openAIWSHTTPBridgeThresholdBytesDefault int64 = 15 * 1024 * 1024
	openAIWSHTTPBridgeErrorBodyLimitBytes         = 64 * 1024
)

// ResolveOpenAIWSClientFirstMessageTimeout 返回生效的客户端入站首消息截止时间。
func ResolveOpenAIWSClientFirstMessageTimeout(cfg *config.Config) time.Duration {
	seconds := config.DefaultOpenAIWSClientFirstMessageTimeoutSeconds
	if cfg != nil && cfg.Gateway.OpenAIWS.ClientFirstMessageTimeoutSeconds > 0 {
		seconds = cfg.Gateway.OpenAIWS.ClientFirstMessageTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

// ResolveOpenAIWSClientReadLimitBytes 返回入站客户端 WS 单帧读取上限。
func ResolveOpenAIWSClientReadLimitBytes(cfg *config.Config) int64 {
	if cfg == nil || cfg.Gateway.OpenAIWS.ClientReadLimitBytes <= 0 {
		return openAIWSClientReadLimitBytesDefault
	}
	return cfg.Gateway.OpenAIWS.ClientReadLimitBytes
}

// openAIWSHTTPBridgeEnabled 判断是否允许过大首帧走 HTTP bridge。
func (s *OpenAIGatewayService) openAIWSHTTPBridgeEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled
}

// openAIWSHTTPBridgeThresholdBytes 返回触发 HTTP bridge 的 payload 阈值。
func (s *OpenAIGatewayService) openAIWSHTTPBridgeThresholdBytes() int64 {
	if s == nil || s.cfg == nil || s.cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes <= 0 {
		return openAIWSHTTPBridgeThresholdBytesDefault
	}
	return s.cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes
}

// shouldBridgeOpenAIWSHTTP 判断当前 WS 首帧是否应改用 HTTP Responses 上游。
func (s *OpenAIGatewayService) shouldBridgeOpenAIWSHTTP(account *Account, payloadBytes int, previousResponseID string) bool {
	if account != nil && account.Platform == PlatformGrok {
		return true
	}
	if !s.openAIWSHTTPBridgeEnabled() {
		return false
	}
	if strings.TrimSpace(previousResponseID) != "" {
		return false
	}
	threshold := s.openAIWSHTTPBridgeThresholdBytes()
	return threshold > 0 && int64(payloadBytes) >= threshold
}

// prepareOpenAIWSHTTPBridgeBody 将 response.create WS payload 转成 HTTP Responses body。
func prepareOpenAIWSHTTPBridgeBody(payload []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("response.create payload must be a JSON object")
	}
	delete(body, "type")
	delete(body, "generate")
	delete(body, "previous_response_id")
	body["stream"] = true
	return json.Marshal(body)
}

// openAIWSToolCallReplayCollector 收集上游输出里的工具调用上下文，供后续 bridge turn 重放。
type openAIWSToolCallReplayCollector struct {
	items []json.RawMessage
	seen  map[string]struct{}
}

// AddEvent 从上游事件中提取可重放的 function_call 项。
func (c *openAIWSToolCallReplayCollector) AddEvent(eventType string, message []byte) {
	switch strings.TrimSpace(eventType) {
	case "response.output_item.done":
		c.addItem(gjson.GetBytes(message, "item"))
	case "response.completed", "response.done":
		output := gjson.GetBytes(message, "response.output")
		if !output.IsArray() {
			return
		}
		for _, item := range output.Array() {
			c.addItem(item)
		}
	}
}

// Items 返回已收集工具调用上下文的拷贝。
func (c *openAIWSToolCallReplayCollector) Items() []json.RawMessage {
	return cloneOpenAIWSRawMessages(c.items)
}

func (c *openAIWSToolCallReplayCollector) addItem(item gjson.Result) {
	if !item.Exists() || item.Type != gjson.JSON {
		return
	}
	raw := strings.TrimSpace(item.Raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return
	}
	if !isCodexToolCallContextItemType(item.Get("type").String()) {
		return
	}
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("call_id").String())
	}
	if key == "" {
		key = raw
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.items = append(c.items, json.RawMessage(raw))
}

func buildOpenAIWSHTTPBridgeErrorEvent(statusCode int, message string) []byte {
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(statusCode)
	}
	if message == "" {
		message = "upstream request failed"
	}
	event := map[string]any{
		"type":   "error",
		"status": statusCode,
		"error": map[string]any{
			"type":    "upstream_error",
			"message": message,
		},
	}
	body, err := json.Marshal(event)
	if err != nil {
		return []byte(`{"type":"error","error":{"type":"upstream_error","message":"upstream request failed"}}`)
	}
	return body
}

// detectOpenAIWSHTTPBridgeRequestScopedError 识别只与当前请求有关的错误。
// 这类错误既不修改账号状态，也不能因为池模式配置而回放当前 turn。
func detectOpenAIWSHTTPBridgeRequestScopedError(account *Account, statusCode int, message string, body []byte) bool {
	if hit, _, _ := detectOpenAICyberPolicy(body); hit {
		return true
	}
	if IsOpenAICyberWarningPayload(body, message) ||
		isOpenAIClientInvalidRequestError(statusCode, message, body) ||
		isOpenAIContextWindowError(message, body) {
		return true
	}
	return account != nil && account.Platform == PlatformGrok && isGrokContentPolicyRejection(statusCode, body)
}

// proxyOpenAIWSHTTPBridgeTurn 使用 HTTP Responses 上游完成一个 WS ingress turn，并把 SSE 事件转回 WS 消息。
func (s *OpenAIGatewayService) proxyOpenAIWSHTTPBridgeTurn(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token string,
	payload []byte,
	payloadBytes int,
	originalModel string,
	routingModel string,
	imageBillingModel string,
	imageSizeTier string,
	imageInputSize string,
	grokCacheIdentity string,
	turn int,
	writeClientMessage func([]byte) error,
	routerMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	if s == nil {
		return nil, errors.New("service is nil")
	}
	if s.httpUpstream == nil {
		return nil, errors.New("openai http upstream is nil")
	}
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if writeClientMessage == nil {
		return nil, errors.New("client websocket writer is nil")
	}

	body, err := prepareOpenAIWSHTTPBridgeBody(payload)
	if err != nil {
		return nil, fmt.Errorf("prepare http bridge body: %w", err)
	}
	responsesLite := account.Platform == PlatformOpenAI && isOpenAIResponsesLiteWebSocketPayload(payload)
	if responsesLite {
		liteBody, changed, liteErr := normalizeOpenAIResponsesLitePayloadForAccount(account, body)
		if liteErr != nil {
			return nil, fmt.Errorf("normalize http bridge Lite body: %w", liteErr)
		}
		if changed {
			body = liteBody
		}
	}
	billingModel := ""
	mappedModel := ""
	if account.Platform == PlatformGrok {
		billingModel, mappedModel = resolveGrokWSModels(account, body, routingModel)
	} else if routingModel != "" {
		billingModel = resolveAccountMappedModelForForward(account, routingModel)
		mappedModel = normalizeOpenAIModelForUpstream(account, billingModel)
	}
	// 只有客户端明确提供模型时才回写下游，避免默认模型被替换成空字符串。
	needModelReplace := routingModel != "" && mappedModel != "" && mappedModel != originalModel
	var mappedModelBytes []byte
	if needModelReplace {
		mappedModelBytes = []byte(mappedModel)
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	var upstreamReq *http.Request
	if account.Platform == PlatformGrok {
		grokIntentSourceBody := body
		body, err = patchGrokResponsesBody(body, mappedModel)
		if err != nil {
			releaseUpstreamCtx()
			return nil, err
		}
		grokMixedCacheIntentBody := append([]byte(nil), body...)
		body, err = applyGrokResponsesCacheIdentity(body, grokIntentSourceBody, grokCacheIdentity, account.IsGrokOAuth())
		if err != nil {
			releaseUpstreamCtx()
			return nil, fmt.Errorf("apply grok prompt cache identity: %w", err)
		}
		body, err = applyGrokFreeRequestToolCacheRoute(c, body, grokMixedCacheIntentBody, account, grokCacheIdentity)
		if err != nil {
			releaseUpstreamCtx()
			return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", err)
		}
		upstreamReq, err = buildGrokResponsesRequest(upstreamCtx, c, account, body, token, grokCacheIdentity, s.cfg, s.settingService)
	} else {
		upstreamReq, err = s.buildUpstreamRequestOpenAIPassthrough(upstreamCtx, c, account, body, token, routerMatch...)
	}
	releaseUpstreamCtx()
	if err != nil {
		return nil, err
	}
	if responsesLite {
		upstreamReq.Header.Set(responsesLiteHeader, "true")
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	if c != nil {
		c.Set("openai_passthrough", true)
		c.Set("openai_ws_http_bridge", true)
	}

	turnStart := time.Now()
	resp, err := s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, routerMatch...))
	if err != nil {
		if turn == 1 {
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
		}
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		_ = writeClientMessage(buildOpenAIWSHTTPBridgeErrorEvent(http.StatusBadGateway, "Upstream request failed"))
		return nil, fmt.Errorf("upstream http bridge request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, openAIWSHTTPBridgeErrorBodyLimitBytes))
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if upstreamMsg == "" {
			upstreamMsg = http.StatusText(resp.StatusCode)
		}
		requestScopedError := detectOpenAIWSHTTPBridgeRequestScopedError(account, resp.StatusCode, upstreamMsg, respBody)
		decision := UpstreamErrorDecision{Policy: ErrorPolicyNone}
		defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody)
		if account.Platform == PlatformGrok {
			defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
		}
		if !requestScopedError {
			if account.Platform == PlatformGrok {
				decision = s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			} else {
				decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			}
		}
		if decision.ShouldReturnGenericError() {
			_ = writeClientMessage(buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error"))
			return nil, fmt.Errorf("upstream http bridge error: status=%d (not in custom error codes)", resp.StatusCode)
		}
		if turn == 1 && !requestScopedError && decision.ShouldFailover(account, resp.StatusCode, defaultFailover) {
			return nil, newOpenAIUpstreamFailoverError(
				resp.StatusCode,
				resp.Header,
				respBody,
				upstreamMsg,
				decision.RetryableOnSameAccount(account, resp.StatusCode),
			)
		}
		_ = writeClientMessage(buildOpenAIWSHTTPBridgeErrorEvent(resp.StatusCode, upstreamMsg))
		return nil, fmt.Errorf("upstream http bridge error: status=%d message=%s", resp.StatusCode, upstreamMsg)
	}
	if account.Platform == PlatformGrok {
		s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, resolveGrokWSUpstreamModel(account, body, originalModel)), account, resp.Header, resp.StatusCode)
	}

	responseID := ""
	usage := OpenAIUsage{}
	imageCounter := newOpenAIImageOutputCounter()
	var firstTokenMs *int
	reqStream := openAIWSPayloadBoolFromRaw(body, "stream", true)
	eventCount := 0
	tokenEventCount := 0
	terminalEventCount := 0
	replayCollector := &openAIWSToolCallReplayCollector{}
	firstEventType := ""
	lastEventType := ""
	upstreamTerminalEvent := ""
	sawDone := false
	wroteDownstream := false
	clientDisconnected := false
	resultWithUsage := func() *OpenAIForwardResult {
		imageCount := imageCounter.Count()
		result := &OpenAIForwardResult{
			RequestID:             responseID,
			ResponseID:            responseID,
			Usage:                 usage,
			Model:                 originalModel,
			BillingModel:          billingModel,
			UpstreamModel:         mappedModel,
			ServiceTier:           extractOpenAIServiceTierFromBody(body),
			ReasoningEffort:       ApplyThinkingEnabledFallback(extractOpenAIReasoningEffortFromBody(body, mappedModel, originalModel), body, mappedModel),
			Stream:                reqStream,
			OpenAIWSMode:          true,
			UpstreamTerminalEvent: upstreamTerminalEvent,
			ResponseHeaders:       cloneHeader(resp.Header),
			Duration:              time.Since(turnStart),
			FirstTokenMs:          firstTokenMs,
		}
		if replayInput := replayCollector.Items(); len(replayInput) > 0 {
			result.wsReplayInput = replayInput
			result.wsReplayInputExists = true
		}
		if imageCount > 0 {
			result.ImageCount = imageCount
			result.ImageSize = imageSizeTier
			result.ImageInputSize = imageInputSize
			result.ImageOutputSizes = imageCounter.Sizes()
			result.BillingModel = imageBillingModel
		}
		return result
	}

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	defer putSSEScannerBuf64K(scanBuf)

	for scanner.Scan() {
		line := scanner.Text()
		data, ok := extractOpenAISSEDataLine(line)
		if !ok {
			continue
		}
		trimmedData := strings.TrimSpace(data)
		if trimmedData == "" {
			continue
		}
		if trimmedData == "[DONE]" {
			sawDone = true
			continue
		}

		upstreamMessage := []byte(trimmedData)
		if normalized, changed := normalizeCompletedImageGenerationStatus(upstreamMessage); changed {
			upstreamMessage = normalized
		}
		eventType, eventResponseID, _ := parseOpenAIWSEventEnvelope(upstreamMessage)
		if responseID == "" && eventResponseID != "" {
			responseID = eventResponseID
		}
		if eventType != "" {
			eventCount++
			if firstEventType == "" {
				firstEventType = eventType
			}
			lastEventType = eventType
		}
		if isOpenAIWSTokenEvent(eventType) {
			tokenEventCount++
			if firstTokenMs == nil {
				ms := int(time.Since(turnStart).Milliseconds())
				firstTokenMs = &ms
			}
		}
		if openAIWSEventShouldParseUsage(eventType) {
			parseOpenAIWSResponseUsageFromCompletedEvent(upstreamMessage, &usage)
		}
		imageCounter.AddSSEData(upstreamMessage)

		if needModelReplace && len(mappedModelBytes) > 0 && openAIWSEventMayContainModel(eventType) && strings.Contains(trimmedData, mappedModel) {
			upstreamMessage = replaceOpenAIWSMessageModel(upstreamMessage, mappedModel, originalModel)
		}
		if s.toolCorrector != nil && openAIWSEventMayContainToolCalls(eventType) && openAIWSMessageLikelyContainsToolCalls(upstreamMessage) {
			if corrected, changed := s.toolCorrector.CorrectToolCallsInSSEBytes(upstreamMessage); changed {
				upstreamMessage = corrected
			}
		}
		replayCollector.AddEvent(eventType, upstreamMessage)

		var upstreamEventErr error
		terminalPolicy := openAIWSTerminalPolicyDecision{
			TerminalEvent: normalizeOpenAIWSTerminalEvent(eventType),
			Decision:      UpstreamErrorDecision{Policy: ErrorPolicyNone},
		}
		if isOpenAIWSTerminalEvent(eventType) {
			terminalPolicy = s.handleOpenAIWSTerminalTransientFailure(
				ctx,
				account,
				mappedModel,
				resp.Header,
				upstreamMessage,
			)
		}
		if eventType == "response.failed" {
			if terminalPolicy.Decision.ShouldReturnGenericError() {
				upstreamMessage = buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
				upstreamEventErr = errors.New("upstream response failed with status not in custom error codes")
			} else if turn == 1 && !wroteDownstream && terminalPolicy.Decision.ShouldFailoverWithDefaults(
				account,
				terminalPolicy.StatusCode,
				false,
				s.shouldFailoverOpenAIWSError(account, terminalPolicy.StatusCode, upstreamMessage),
			) {
				return nil, newOpenAIUpstreamFailoverError(
					terminalPolicy.StatusCode,
					resp.Header,
					upstreamMessage,
					extractOpenAISSEErrorMessage(upstreamMessage),
					terminalPolicy.Decision.RetryableOnSameAccount(account, terminalPolicy.StatusCode),
				)
			}
		}
		if eventType == "error" {
			_, _, errMsgRaw := parseOpenAIWSErrorEventFields(upstreamMessage)
			errMessage := strings.TrimSpace(errMsgRaw)
			if errMessage == "" {
				errMessage = "upstream error event"
			}
			statusCode := openAIWSErrorPolicyStatus(upstreamMessage)
			policyStatus := statusCode
			requestScopedError := detectOpenAIWSHTTPBridgeRequestScopedError(account, statusCode, errMessage, upstreamMessage)
			decision := UpstreamErrorDecision{Policy: ErrorPolicyNone}
			defaultFailover := s.shouldFailoverOpenAIWSError(account, policyStatus, upstreamMessage)
			if account.Platform == PlatformGrok {
				// SSE 错误事件不携带 HTTP 状态码，本地映射会把未知 xAI 错误码
				//（例如 new_sensitive）默认映射为 502；应用基于状态码的故障转移或
				// 账号状态变更前，先按请求级 403 内容拒绝检查响应体。
				if isGrokContentPolicyRejection(http.StatusForbidden, upstreamMessage) {
					requestScopedError = true
					defaultFailover = false
				} else {
					defaultFailover = s.shouldFailoverGrokUpstreamError(statusCode, upstreamMessage)
					decision = s.applyGrokAccountUpstreamError(ctx, account, statusCode, resp.Header, upstreamMessage, mappedModel)
				}
			} else if !requestScopedError {
				defaultFailover = s.shouldFailoverOpenAIWSError(account, policyStatus, upstreamMessage)
				decision = s.applyOpenAIAccountUpstreamError(ctx, account, policyStatus, resp.Header, upstreamMessage, mappedModel)
			}
			if decision.ShouldReturnGenericError() {
				upstreamMessage = buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
				upstreamEventErr = errors.New("upstream error not in custom error codes")
			} else if turn == 1 && !wroteDownstream && !requestScopedError && decision.ShouldFailover(account, policyStatus, defaultFailover) {
				return nil, newOpenAIUpstreamFailoverError(
					policyStatus,
					resp.Header,
					upstreamMessage,
					errMessage,
					decision.RetryableOnSameAccount(account, policyStatus),
				)
			}
			if upstreamEventErr == nil {
				upstreamEventErr = errors.New(errMessage)
			}
		}

		// 客户端写出副本改写容量降载码：Codex 对 error/response.failed 中的
		// server_is_overloaded / slow_down 判致命并终止会话，改写后走客户端内置
		// 重试。账号状态与终止事件判定（下方 handleOpenAIWSTerminalTransientFailure）
		// 仍使用未改写的 upstreamMessage。
		clientMessage := upstreamMessage
		if eventType == "error" || eventType == "response.failed" {
			if rewritten, changed := sanitizeOpenAICapacityShedErrorCodeForClient(clientMessage); changed {
				clientMessage = rewritten
			}
		}
		if !clientDisconnected {
			if err := writeClientMessage(clientMessage); err != nil {
				if isOpenAIWSClientDisconnectError(err) {
					clientDisconnected = true
					closeStatus, closeReason := summarizeOpenAIWSReadCloseError(err)
					logOpenAIWSModeInfo(
						"ingress_ws_http_bridge_client_disconnected_drain account_id=%d turn=%d close_status=%s close_reason=%s",
						account.ID,
						turn,
						closeStatus,
						truncateOpenAIWSLogValue(closeReason, openAIWSHeaderValueMaxLen),
					)
				} else {
					return nil, wrapOpenAIWSIngressTurnError(
						"write_client",
						fmt.Errorf("write client websocket event: %w", err),
						wroteDownstream,
					)
				}
			} else {
				wroteDownstream = true
			}
		}

		if upstreamEventErr != nil {
			return resultWithUsage(), upstreamEventErr
		}
		if isOpenAIWSTerminalEvent(eventType) {
			upstreamTerminalEvent = terminalPolicy.TerminalEvent
			terminalEventCount++
			firstTokenMsValue := -1
			if firstTokenMs != nil {
				firstTokenMsValue = *firstTokenMs
			}
			logOpenAIWSModeInfo(
				"ingress_ws_http_bridge_turn_completed account_id=%d turn=%d response_id=%s payload_bytes=%d duration_ms=%d events=%d token_events=%d terminal_events=%d first_event=%s last_event=%s first_token_ms=%d client_disconnected=%v",
				account.ID,
				turn,
				truncateOpenAIWSLogValue(responseID, openAIWSIDValueMaxLen),
				payloadBytes,
				time.Since(turnStart).Milliseconds(),
				eventCount,
				tokenEventCount,
				terminalEventCount,
				truncateOpenAIWSLogValue(firstEventType, openAIWSLogValueMaxLen),
				truncateOpenAIWSLogValue(lastEventType, openAIWSLogValueMaxLen),
				firstTokenMsValue,
				clientDisconnected,
			)
			return resultWithUsage(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		streamErr := fmt.Errorf("read upstream http bridge stream: %w", err)
		if turn == 1 && !wroteDownstream {
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, streamErr, true)
		}
		return resultWithUsage(), streamErr
	}
	terminalErr := errors.New("upstream http bridge stream ended before terminal event")
	if sawDone {
		terminalErr = errors.New("upstream http bridge stream sent [DONE] before terminal event")
	}
	if turn == 1 && !wroteDownstream {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, terminalErr, true)
	}
	return resultWithUsage(), terminalErr
}

func resolveGrokWSCacheIdentity(c *gin.Context, account *Account, payload []byte, routingModel string) (string, error) {
	body, err := prepareOpenAIWSHTTPBridgeBody(payload)
	if err != nil {
		return "", err
	}
	upstreamModel := resolveGrokWSUpstreamModel(account, body, routingModel)
	body, err = patchGrokResponsesBody(body, upstreamModel)
	if err != nil {
		return "", err
	}
	return resolveGrokCacheIdentity(c, body, "", upstreamModel), nil
}

func resolveGrokWSUpstreamModel(account *Account, body []byte, originalModel string) string {
	_, upstreamModel := resolveGrokWSModels(account, body, originalModel)
	return upstreamModel
}

// resolveGrokWSModels 只解析一次账号映射与 Grok 平台规范化，供请求、错误状态和结果记录复用。
func resolveGrokWSModels(account *Account, body []byte, originalModel string) (string, string) {
	requestedModel := strings.TrimSpace(originalModel)
	if requestedModel == "" {
		requestedModel = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	billingModel := requestedModel
	if account != nil {
		billingModel = resolveAccountMappedModelForForward(account, requestedModel)
	}
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if upstreamModel == "" {
		upstreamModel = grokDefaultResponsesModel
	}
	return billingModel, upstreamModel
}
