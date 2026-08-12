package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// openaiCCRawAllowedHeaders 是 CC 直转路径专用的客户端 header 透传白名单。
//
// **关键**：不能复用 openaiAllowedHeaders——后者含 Codex 客户端专属 header
// （originator / session_id / x-codex-turn-state / x-codex-turn-metadata / conversation_id），
// 这些在 ChatGPT OAuth 上游是必需的，但透传给 DeepSeek/Kimi/GLM 等第三方
// OpenAI 兼容上游会造成：
//   - 完全忽略（多数友好厂商）——隐性污染上游统计
//   - 400 "unknown parameter"（严格上游）——可见错误
//
// 这里仅放行通用 HTTP header；content-type / authorization / accept 由上下文
// 显式设置，不依赖透传。
//
// 参见决策记录：
// pensieve/short-term/maxims/dont-reuse-shared-headers-whitelist-across-different-upstream-trust-domains
var openaiCCRawAllowedHeaders = map[string]bool{
	"accept-language": true,
	"user-agent":      true,
}

// forwardAsRawChatCompletions 直转客户端的 Chat Completions 请求到上游
// `{base_url}/v1/chat/completions`，**不**做 CC↔Responses 协议转换。
//
// 适用场景：account.platform=openai && account.type=apikey && 上游已被探测确认
// 不支持 /v1/responses 端点（如 DeepSeek/Kimi/GLM/Qwen 等第三方 OpenAI 兼容上游）。
//
// 与 ForwardAsChatCompletions 的关键差异：
//
//   - 不调用 apicompat.ChatCompletionsToResponses，body 仅做模型 ID 改写
//   - 上游 URL 拼到 /v1/chat/completions 而非 /v1/responses
//   - 流式响应 SSE 直接透传给客户端（上游 chunk 已是 CC 格式）
//   - 非流式响应 JSON 直接透传，仅按需提取 usage
//   - 不应用 codex OAuth transform（APIKey 路径无 OAuth）
//   - 不注入 prompt_cache_key（OAuth 专属机制）
//
// 调用入口：openai_gateway_chat_completions.go::ForwardAsChatCompletions
// 在函数顶部按 openai_compat.ShouldUseResponsesAPI 分流。
func (s *OpenAIGatewayService) forwardAsRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// 1. Parse minimal fields needed for routing/billing
	originalModel := gjson.GetBytes(body, "model").String()
	if originalModel == "" {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := gjson.GetBytes(body, "stream").Bool()

	// 2. Resolve model mapping (same as ForwardAsChatCompletions)
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	grokCacheIdentity := ""
	if account.Platform == PlatformGrok {
		// 在图片桥接或其它请求体改写前解析，使回退身份始终基于客户端稳定的会话前缀。
		grokCacheIdentity = resolveGrokCacheIdentity(c, body, "", upstreamModel)
	}
	// 3. Rewrite model in body (no protocol conversion)
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = ReplaceModelInBody(body, upstreamModel)
	}
	if normalizedBody, normalized := NormalizeGLMOpenAIReasoningEffort(upstreamBody, upstreamModel); normalized {
		upstreamBody = normalizedBody
	}

	// 4. Apply OpenAI fast policy on the CC body
	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, upstreamBody)
	if policyErr != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(policyErr, &blocked) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeChatCompletionsError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, policyErr
	}
	upstreamBody = updatedBody
	// GLM 归一化和 fast policy 都可能改写上游请求，Usage Log 必须读取最终值。
	reasoningEffort := extractEffectiveOpenAIReasoningEffortFromBody(upstreamBody, body, upstreamModel, billingModel, originalModel)
	// 国产模型没有显式 effort 档位时，thinking 启用后补默认展示值。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, upstreamBody, billingModel)
	serviceTier := extractOpenAIServiceTierFromBody(upstreamBody)

	// Grok Composer 不直接接受 image_url；仅在该场景通过 Grok Build 生成图片描述后转发纯文本。
	token, tokenKind, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("account %d missing %s credential", account.ID, tokenKind)
	}

	var bridgeUsage OpenAIUsage
	if account.Platform == PlatformGrok {
		bridgedBody, usage, bridged, bridgeErr := s.bridgeGrokComposerImageInputs(ctx, c, account, upstreamBody, token)
		if bridgeErr != nil {
			var failoverErr *UpstreamFailoverError
			if !errors.As(bridgeErr, &failoverErr) && c != nil && c.Writer != nil && !c.Writer.Written() {
				writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", bridgeErr.Error())
			}
			return nil, bridgeErr
		}
		if bridged {
			upstreamBody = bridgedBody
			addOpenAIUsage(&bridgeUsage, usage)
		}
	}

	if clientStream {
		var usageErr error
		upstreamBody, usageErr = ensureOpenAIChatStreamUsage(upstreamBody)
		if usageErr != nil {
			return nil, fmt.Errorf("enable stream usage: %w", usageErr)
		}
	}
	if account.Platform == PlatformGrok {
		upstreamBody, err = stripGrokChatPromptCacheKey(upstreamBody)
		if err != nil {
			return nil, fmt.Errorf("remove Responses-only Grok prompt cache key: %w", err)
		}
	}

	logger.L().Debug("openai chat_completions raw: forwarding without protocol conversion",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// 5. 通过共享 CC 管线构造并发送上游请求。
	targetURL, err := s.rawChatCompletionsURL(account)
	if err != nil {
		return nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, grokChatRawEndpoint)
	customUA := account.GetOpenAIUserAgent()
	if customUA == "" && account.IsGrokOAuth() {
		customUA = grokGatewayUserAgent
	}
	resp, err := s.sendCCUpstreamRequest(ctx, c, account, targetURL, upstreamBody, clientStream, token, customUA, grokCacheIdentity, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 7. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		if account.Platform == PlatformGrok {
			decision := s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
			kind := "http_error"
			if decision.ShouldFailover(account, resp.StatusCode, s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)) {
				kind = "failover"
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
				Kind:               kind,
				Message:            upstreamMsg,
			})
			if decision.ShouldReturnGenericError() {
				return s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
			}
			if kind == "failover" {
				return nil, &UpstreamFailoverError{
					StatusCode:             resp.StatusCode,
					ResponseBody:           respBody,
					ResponseHeaders:        resp.Header.Clone(),
					RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
				}
			}
			return s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
		}
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
	}

	if account.Platform == PlatformGrok {
		s.updateGrokUsageFromResponse(ctx, account, resp.Header, resp.StatusCode)
	}

	// 8. 转发响应
	var result *OpenAIForwardResult
	var forwardErr error
	if clientStream {
		result, forwardErr = s.streamRawChatCompletions(c, resp, account, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime, len(body))
	} else {
		result, forwardErr = s.bufferRawChatCompletions(c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
	}
	if result != nil {
		addOpenAIUsage(&result.Usage, bridgeUsage)
		result.UpstreamEndpoint = grokChatRawEndpoint
	}
	return result, forwardErr
}

func (s *OpenAIGatewayService) rawChatCompletionsURL(account *Account) (string, error) {
	if account.Platform == PlatformGrok {
		targetURL, err := buildGrokChatCompletionsURL(account, s.cfg)
		if err != nil {
			return "", fmt.Errorf("invalid grok base_url: %w", err)
		}
		return targetURL, nil
	}

	return s.openAIChatCompletionsTargetURL(account)
}

// streamRawChatCompletions 透传上游 CC SSE 流到客户端，并提取 usage（包括
// 末尾 [DONE] 之前的 chunk 中的 usage 字段，按 OpenAI CC 协议）。
//
// usage 字段仅在客户端请求 stream_options.include_usage=true 时出现于上游响应中。
// 网关会对上游强制打开 include_usage 以保证计费完整，并原样向下游透传 usage，
// 让级联代理或下游计费系统也能拿到完整用量。
func (s *OpenAIGatewayService) streamRawChatCompletions(
	c *gin.Context,
	resp *http.Response,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
	requestBodyLen int,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)
	scanner := s.newUpstreamSSEScanner(resp.Body)

	var usage OpenAIUsage
	var firstTokenMs *int
	clientDisconnected := false
	clientOutputStarted := false
	pendingLines := make([]string, 0, 8)
	refusalDetector := newOpenAIChatSilentRefusalDetector(requestBodyLen)
	streamAccumulator := newOpenAIChatCompletionsStreamAccumulator(originalModel)

	writeLine := func(line string) {
		if clientDisconnected {
			return
		}
		if !clientOutputStarted && !refusalDetector.ShouldReleaseClientOutput() {
			pendingLines = append(pendingLines, line)
			return
		}
		if !clientOutputStarted {
			writeStreamHeaders()
			for _, pending := range pendingLines {
				if _, werr := c.Writer.WriteString(pending + "\n"); werr != nil {
					clientDisconnected = true
					logger.L().Debug("openai chat_completions raw: client disconnected, continuing to drain upstream for billing",
						zap.Error(werr),
						zap.String("request_id", requestID),
					)
					return
				}
			}
			pendingLines = pendingLines[:0]
			clientOutputStarted = true
		}
		if _, werr := c.Writer.WriteString(line + "\n"); werr != nil {
			clientDisconnected = true
			logger.L().Debug("openai chat_completions raw: client disconnected, continuing to drain upstream for billing",
				zap.Error(werr),
				zap.String("request_id", requestID),
			)
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		refusalDetector.ObserveSSELine(line)
		if payload, ok := extractOpenAISSEDataLine(line); ok {
			trimmedPayload := strings.TrimSpace(payload)
			if trimmedPayload != "[DONE]" {
				usageOnlyChunk := isOpenAIChatUsageOnlyStreamChunk(payload)
				if u := extractCCStreamUsage(payload); u != nil {
					usage = *u
				}
				streamAccumulator.ObservePayload(payload)
				if firstTokenMs == nil && !usageOnlyChunk {
					elapsed := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &elapsed
				}
			}
		}

		writeLine(line)
		if line == "" {
			if !clientDisconnected && clientOutputStarted {
				c.Writer.Flush()
			}
			continue
		}
		if !clientDisconnected && clientOutputStarted {
			c.Writer.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai chat_completions raw: stream read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	} else if !clientDisconnected && !clientOutputStarted {
		if refusalDetector.IsSilentRefusal() {
			return nil, newOpenAISilentRefusalFailoverError(c, account, requestID)
		}
		if len(pendingLines) > 0 {
			writeStreamHeaders()
			for _, pending := range pendingLines {
				if _, werr := c.Writer.WriteString(pending + "\n"); werr != nil {
					clientDisconnected = true
					logger.L().Debug("openai chat_completions raw: client disconnected during final flush",
						zap.Error(werr),
						zap.String("request_id", requestID),
					)
					break
				}
			}
			if !clientDisconnected {
				c.Writer.Flush()
				clientOutputStarted = true
			}
		}
	}

	return &OpenAIForwardResult{
		RequestID:       requestID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          true,
		Duration:        time.Since(startTime),
		FirstTokenMs:    firstTokenMs,
		ResponseBody:    streamAccumulator.ResponseBody(&usage),
	}, nil
}

// ensureOpenAIChatStreamUsage 确保 raw Chat Completions 流式请求会让上游返回 usage。
// usage 也会继续向下游透传，支持级联代理和下游计费系统。
func ensureOpenAIChatStreamUsage(body []byte) ([]byte, error) {
	updated, err := sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return body, err
	}
	return updated, nil
}

func isOpenAIChatUsageOnlyStreamChunk(payload string) bool {
	if strings.TrimSpace(payload) == "" {
		return false
	}
	if !gjson.Get(payload, "usage").Exists() {
		return false
	}
	choices := gjson.Get(payload, "choices")
	return choices.Exists() && choices.IsArray() && len(choices.Array()) == 0
}

// extractCCStreamUsage 从单个 CC 流式 chunk 的 payload 中提取 usage 字段。
// CC 协议中 usage 仅出现在末尾 chunk（且仅当 include_usage 生效时），
// 但上游可能在多个 chunk 中重复——总是用最新值。
func extractCCStreamUsage(payload string) *OpenAIUsage {
	usageResult := gjson.Get(payload, "usage")
	if !usageResult.Exists() || !usageResult.IsObject() {
		return nil
	}
	u, ok := openAIUsageFromGJSON(usageResult)
	if !ok {
		return nil
	}
	return &u
}

type openAIChatCompletionsStreamAccumulator struct {
	id             string
	model          string
	created        int64
	role           string
	content        strings.Builder
	reasoning      strings.Builder
	toolCalls      map[int]*apicompat.ChatToolCall
	finishReason   string
	systemFP       string
	serviceTier    string
	observedChunks bool
}

// newOpenAIChatCompletionsStreamAccumulator 聚合 Chat Completions 流式 chunk 供数据共享落盘。
func newOpenAIChatCompletionsStreamAccumulator(model string) *openAIChatCompletionsStreamAccumulator {
	return &openAIChatCompletionsStreamAccumulator{
		model:     model,
		toolCalls: make(map[int]*apicompat.ChatToolCall),
	}
}

// ObservePayload 读取一个 SSE payload，并累积 assistant 文本、reasoning 和工具调用。
func (a *openAIChatCompletionsStreamAccumulator) ObservePayload(payload string) {
	var chunk apicompat.ChatCompletionsChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return
	}
	if len(chunk.Choices) == 0 {
		return
	}
	a.observedChunks = true
	if chunk.ID != "" {
		a.id = chunk.ID
	}
	if chunk.Model != "" {
		a.model = chunk.Model
	}
	if chunk.Created > 0 {
		a.created = chunk.Created
	}
	if chunk.SystemFingerprint != "" {
		a.systemFP = chunk.SystemFingerprint
	}
	if chunk.ServiceTier != "" {
		a.serviceTier = chunk.ServiceTier
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			a.finishReason = *choice.FinishReason
		}
		if choice.Delta.Role != "" {
			a.role = choice.Delta.Role
		}
		if choice.Delta.Content != nil {
			_, _ = a.content.WriteString(*choice.Delta.Content)
		}
		if choice.Delta.ReasoningContent != nil {
			_, _ = a.reasoning.WriteString(*choice.Delta.ReasoningContent)
		}
		for _, deltaCall := range choice.Delta.ToolCalls {
			idx := len(a.toolCalls)
			if deltaCall.Index != nil {
				idx = *deltaCall.Index
			}
			stored := a.toolCalls[idx]
			if stored == nil {
				copyCall := apicompat.ChatToolCall{Type: "function"}
				a.toolCalls[idx] = &copyCall
				stored = &copyCall
			}
			if deltaCall.ID != "" {
				stored.ID = deltaCall.ID
			}
			if deltaCall.Type != "" {
				stored.Type = deltaCall.Type
			}
			if deltaCall.Function.Name != "" {
				stored.Function.Name = deltaCall.Function.Name
			}
			if deltaCall.Function.Arguments != "" {
				stored.Function.Arguments += deltaCall.Function.Arguments
			}
		}
	}
}

// ResponseBody 将已累积的流式结果还原为非流式 Chat Completions 响应体。
func (a *openAIChatCompletionsStreamAccumulator) ResponseBody(usage *OpenAIUsage) []byte {
	if a == nil || !a.observedChunks {
		return nil
	}
	id := a.id
	if id == "" {
		id = "chatcmpl-data-share"
	}
	created := a.created
	if created == 0 {
		created = time.Now().Unix()
	}
	role := a.role
	if role == "" {
		role = "assistant"
	}
	msg := apicompat.ChatMessage{Role: role}
	if a.content.Len() > 0 {
		raw, _ := json.Marshal(a.content.String())
		msg.Content = raw
	}
	if a.reasoning.Len() > 0 {
		msg.ReasoningContent = a.reasoning.String()
	}
	if len(a.toolCalls) > 0 {
		for i := 0; i < len(a.toolCalls); i++ {
			call := a.toolCalls[i]
			if call == nil {
				continue
			}
			copied := *call
			copied.Index = nil
			if copied.Type == "" {
				copied.Type = "function"
			}
			msg.ToolCalls = append(msg.ToolCalls, copied)
		}
	}
	finishReason := a.finishReason
	if finishReason == "" {
		if len(msg.ToolCalls) > 0 {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}
	resp := apicompat.ChatCompletionsResponse{
		ID:                id,
		Object:            "chat.completion",
		Created:           created,
		Model:             a.model,
		SystemFingerprint: a.systemFP,
		ServiceTier:       a.serviceTier,
		Choices: []apicompat.ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
	}
	if usage != nil {
		resp.Usage = &apicompat.ChatUsage{
			PromptTokens:     usage.InputTokens,
			CompletionTokens: usage.OutputTokens,
			TotalTokens:      usage.InputTokens + usage.OutputTokens,
		}
		if usage.CacheReadInputTokens > 0 {
			resp.Usage.PromptTokensDetails = &apicompat.ChatTokenDetails{CachedTokens: usage.CacheReadInputTokens}
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	return cloneDataSharingRequestBody(body)
}

// observeOpenAIChatStreamChunk 聚合单个 Chat Completions chunk，并返回当前可用于数据共享的快照。
func observeOpenAIChatStreamChunk(a *openAIChatCompletionsStreamAccumulator, chunk apicompat.ChatCompletionsChunk, usage *OpenAIUsage) []byte {
	if a == nil {
		return nil
	}
	payload, err := json.Marshal(chunk)
	if err != nil {
		return nil
	}
	a.ObservePayload(string(payload))
	return a.ResponseBody(usage)
}

// observeOpenAIChatStreamPayload 聚合已经序列化的 Chat Completions chunk payload。
func observeOpenAIChatStreamPayload(a *openAIChatCompletionsStreamAccumulator, payload []byte, usage *OpenAIUsage) []byte {
	if a == nil || len(payload) == 0 {
		return nil
	}
	a.ObservePayload(string(payload))
	return a.ResponseBody(usage)
}

// bufferRawChatCompletions 透传上游 CC 非流式 JSON 响应。
func (s *OpenAIGatewayService) bufferRawChatCompletions(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			writeChatCompletionsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		return nil, fmt.Errorf("read upstream body: %w", err)
	}

	var usage OpenAIUsage
	if parsedUsage, ok := extractOpenAIUsageFromJSONBytes(respBody); ok {
		usage = parsedUsage
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	} else {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(respBody)

	return &OpenAIForwardResult{
		RequestID:       requestID,
		Usage:           usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		ServiceTier:     serviceTier,
		Stream:          false,
		Duration:        time.Since(startTime),
		ResponseBody:    cloneDataSharingRequestBody(respBody),
	}, nil
}

// buildOpenAIChatCompletionsURL 拼接上游 Chat Completions 端点 URL。
//
//   - base 已是 /chat/completions：原样返回
//   - base 以 /v1 结尾：追加 /chat/completions
//   - base 以其他版本段结尾（如 /v4）：追加 /chat/completions
//   - 其他情况：追加 /v1/chat/completions
//
// 与 buildOpenAIResponsesURL 是姐妹函数。
func buildOpenAIChatCompletionsURL(base string) string {
	return buildOpenAIEndpointURL(base, "/v1/chat/completions")
}
