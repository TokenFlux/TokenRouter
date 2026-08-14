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
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	grokChatResponsesEndpoint = "/v1/responses"
	grokChatRawEndpoint       = "/v1/chat/completions"
)

var grokChatResponsesBridgeTopLevelFields = map[string]struct{}{
	"model":                 {},
	"messages":              {},
	"instructions":          {},
	"stream":                {},
	"stream_options":        {},
	"max_tokens":            {},
	"max_completion_tokens": {},
	"temperature":           {},
	"top_p":                 {},
	"stop":                  {},
	"reasoning_effort":      {},
	"prompt_cache_key":      {},
	"tools":                 {},
	"tool_choice":           {},
	"functions":             {},
	"function_call":         {},
	"parallel_tool_calls":   {},
	"response_format":       {},
	"service_tier":          {},
}

// grokChatResponsesBridgeEligibility 只接受能由 Responses 桥接完整保留语义的
// Chat Completions 请求；其它请求继续走原始 Chat，避免字段被静默丢弃或改写。
func grokChatResponsesBridgeEligibility(body []byte) (bool, string) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil || root == nil {
		return false, "invalid_json"
	}

	// 这些字段显式设为 JSON null 时不产生效果；接受常见 SDK 的这种表示可继续走桥接，
	// 非 null 值仍不支持，因为 Responses 转换器无法保留其 Chat Completions 语义。
	for _, field := range []string{"stop", "reasoning_effort"} {
		if raw, exists := root[field]; exists && !grokChatJSONNull(raw) {
			return false, "unsupported_" + field
		}
	}
	if raw, exists := root["instructions"]; exists {
		var instructions string
		if !grokChatJSONNull(raw) && json.Unmarshal(raw, &instructions) != nil {
			return false, "invalid_instructions"
		}
	}
	if raw, exists := root["response_format"]; exists {
		var responseFormat map[string]json.RawMessage
		if !grokChatJSONNull(raw) && (json.Unmarshal(raw, &responseFormat) != nil || responseFormat == nil) {
			return false, "invalid_response_format"
		}
	}
	if raw, exists := root["service_tier"]; exists {
		var serviceTier string
		if !grokChatJSONNull(raw) && json.Unmarshal(raw, &serviceTier) != nil {
			return false, "invalid_service_tier"
		}
	}
	if raw, exists := root["tools"]; exists {
		if ok, reason := grokChatFunctionDeclarationsBridgeable(raw); !ok {
			return false, reason
		}
	}
	if raw, exists := root["functions"]; exists && !grokChatNullOrEmptyArray(raw) {
		return false, "unsupported_functions"
	}
	if raw, exists := root["tool_choice"]; exists {
		if ok, reason := grokChatToolChoiceBridgeable(raw); !ok {
			return false, reason
		}
		var choice string
		if json.Unmarshal(raw, &choice) == nil && choice == "required" && !grokChatHasFunctionDeclarations(root) {
			return false, "required_tool_choice_without_tools"
		}
	}
	if raw, exists := root["function_call"]; exists && !grokChatNullOrNone(raw) {
		return false, "unsupported_function_call"
	}
	for field := range root {
		if _, supported := grokChatResponsesBridgeTopLevelFields[field]; !supported {
			return false, "unknown_field_" + field
		}
	}

	var model string
	if raw, ok := root["model"]; !ok || json.Unmarshal(raw, &model) != nil || strings.TrimSpace(model) == "" {
		return false, "invalid_model"
	}

	if raw, ok := root["stream"]; ok {
		var stream *bool
		if json.Unmarshal(raw, &stream) != nil || stream == nil {
			return false, "invalid_stream"
		}
	}
	if raw, ok := root["parallel_tool_calls"]; ok {
		var parallelToolCalls *bool
		if json.Unmarshal(raw, &parallelToolCalls) != nil || parallelToolCalls == nil {
			return false, "invalid_parallel_tool_calls"
		}
	}
	if raw, ok := root["stream_options"]; ok {
		var options map[string]json.RawMessage
		if json.Unmarshal(raw, &options) != nil || options == nil {
			return false, "invalid_stream_options"
		}
		for field, value := range options {
			if field != "include_usage" {
				return false, "unknown_stream_option_" + field
			}
			var includeUsage *bool
			if json.Unmarshal(value, &includeUsage) != nil || includeUsage == nil {
				return false, "invalid_stream_include_usage"
			}
		}
	}

	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		if raw, ok := root[field]; ok {
			var value *int
			if json.Unmarshal(raw, &value) != nil || value == nil || *value < 128 {
				return false, "unsafe_" + field
			}
		}
	}
	if _, hasMaxTokens := root["max_tokens"]; hasMaxTokens {
		if _, hasMaxCompletionTokens := root["max_completion_tokens"]; hasMaxCompletionTokens {
			return false, "conflicting_max_tokens"
		}
	}
	for _, field := range []string{"temperature", "top_p"} {
		if raw, ok := root[field]; ok {
			var value *float64
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return false, "invalid_" + field
			}
		}
	}
	if raw, ok := root["prompt_cache_key"]; ok {
		var key string
		if json.Unmarshal(raw, &key) != nil {
			return false, "invalid_prompt_cache_key"
		}
	}

	var messages []map[string]json.RawMessage
	rawMessages, ok := root["messages"]
	if !ok || json.Unmarshal(rawMessages, &messages) != nil || len(messages) == 0 {
		return false, "invalid_messages"
	}
	for _, message := range messages {
		var role string
		if raw, exists := message["role"]; !exists || json.Unmarshal(raw, &role) != nil {
			return false, "invalid_message_role"
		}
		switch role {
		case "system", "user":
			if ok, reason := grokChatMessageFieldsBridgeable(message, "role", "content"); !ok {
				return false, reason
			}
			raw, exists := message["content"]
			if !exists {
				return false, "non_text_message_content"
			}
			if ok, reason := grokChatRequiredMessageContentBridgeable(raw); !ok {
				return false, reason
			}
		case "assistant":
			if ok, reason := grokChatMessageFieldsBridgeable(message, "role", "content", "reasoning_content", "tool_calls"); !ok {
				return false, reason
			}
			reasoningContent := ""
			if raw, exists := message["reasoning_content"]; exists {
				if !grokChatJSONNull(raw) && json.Unmarshal(raw, &reasoningContent) != nil {
					return false, "invalid_reasoning_content"
				}
			}
			hasReasoningContent := strings.TrimSpace(reasoningContent) != ""
			toolCallCount := 0
			if raw, exists := message["tool_calls"]; exists {
				var reason string
				toolCallCount, reason = grokChatAssistantToolCallsBridgeable(raw)
				if reason != "" {
					return false, reason
				}
			}
			raw, hasContent := message["content"]
			if !hasContent || strings.TrimSpace(string(raw)) == "null" {
				if toolCallCount == 0 && !hasReasoningContent {
					return false, "non_text_message_content"
				}
				continue
			}
			var content string
			if json.Unmarshal(raw, &content) == nil {
				if strings.TrimSpace(content) == "" && toolCallCount == 0 && !hasReasoningContent {
					return false, "empty_message_content"
				}
				continue
			}
			if ok, reason := grokChatStructuredContentBridgeable(raw); !ok {
				// 空内容数组伴随 reasoning_content 时，转换器仍可输出独立推理部分；
				// 不要把此例外扩展到不支持或格式错误的内容部分。
				if !hasReasoningContent || reason != "empty_message_content" {
					return false, reason
				}
			}
		case "tool":
			if ok, reason := grokChatMessageFieldsBridgeable(message, "role", "content", "tool_call_id"); !ok {
				return false, reason
			}
			var callID string
			if raw, exists := message["tool_call_id"]; !exists || json.Unmarshal(raw, &callID) != nil || strings.TrimSpace(callID) == "" {
				return false, "invalid_tool_call_id"
			}
			var output string
			if raw, exists := message["content"]; !exists || json.Unmarshal(raw, &output) != nil || output == "" {
				return false, "invalid_tool_message_content"
			}
		default:
			return false, "unsupported_message_role_" + role
		}
	}

	return true, ""
}

func grokChatFunctionDeclarationsBridgeable(raw json.RawMessage) (bool, string) {
	if strings.TrimSpace(string(raw)) == "null" {
		return true, ""
	}
	var declarations []json.RawMessage
	if json.Unmarshal(raw, &declarations) != nil {
		return false, "invalid_tools"
	}
	for _, declaration := range declarations {
		var tool map[string]json.RawMessage
		if json.Unmarshal(declaration, &tool) != nil || tool == nil {
			return false, "invalid_tool"
		}
		for field := range tool {
			if field != "type" && field != "function" {
				return false, "unsafe_tool_field_" + field
			}
		}
		var toolType string
		if rawType, exists := tool["type"]; !exists || json.Unmarshal(rawType, &toolType) != nil || toolType != "function" {
			return false, "unsupported_tool_type"
		}
		functionRaw, exists := tool["function"]
		if !exists {
			return false, "invalid_tool_function"
		}

		var function map[string]json.RawMessage
		if json.Unmarshal(functionRaw, &function) != nil || function == nil {
			return false, "invalid_tool_function"
		}
		for field := range function {
			switch field {
			case "name", "description", "parameters", "strict":
			default:
				return false, "unsafe_tool_function_field_" + field
			}
		}
		var name string
		if rawName, exists := function["name"]; !exists || json.Unmarshal(rawName, &name) != nil || strings.TrimSpace(name) == "" {
			return false, "invalid_tool_function_name"
		}
		if rawDescription, exists := function["description"]; exists {
			var description string
			if json.Unmarshal(rawDescription, &description) != nil {
				return false, "invalid_tool_function_description"
			}
		}
		var parameters map[string]json.RawMessage
		if rawParameters, exists := function["parameters"]; !exists || json.Unmarshal(rawParameters, &parameters) != nil || parameters == nil {
			return false, "invalid_tool_function_parameters"
		}
		if rawStrict, exists := function["strict"]; exists {
			var strict bool
			if json.Unmarshal(rawStrict, &strict) != nil {
				return false, "invalid_tool_function_strict"
			}
		}
	}
	return true, ""
}

func grokChatToolChoiceBridgeable(raw json.RawMessage) (bool, string) {
	if strings.TrimSpace(string(raw)) == "null" {
		return true, ""
	}
	var choice string
	if json.Unmarshal(raw, &choice) != nil {
		return false, "unsupported_tool_choice"
	}
	switch choice {
	case "auto", "none", "required":
		return true, ""
	default:
		return false, "unsupported_tool_choice"
	}
}

func grokChatHasFunctionDeclarations(root map[string]json.RawMessage) bool {
	for _, field := range []string{"tools", "functions"} {
		raw, exists := root[field]
		if !exists {
			continue
		}
		var declarations []json.RawMessage
		if json.Unmarshal(raw, &declarations) == nil && len(declarations) > 0 {
			return true
		}
	}
	return false
}

func grokChatMessageFieldsBridgeable(message map[string]json.RawMessage, allowedFields ...string) (bool, string) {
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		allowed[field] = struct{}{}
	}
	for field := range message {
		if _, ok := allowed[field]; !ok {
			return false, "unsafe_message_field_" + field
		}
	}
	return true, ""
}

func grokChatRequiredMessageContentBridgeable(raw json.RawMessage) (bool, string) {
	var content string
	if json.Unmarshal(raw, &content) == nil {
		if strings.TrimSpace(content) == "" {
			return false, "empty_message_content"
		}
		return true, ""
	}
	// 结构化内容仅允许由 text、image_url 或 input_image 组成的数组；它们可无损转换为
	// Responses input_text/input_image，从而保留 Chat Completions 语义。
	return grokChatStructuredContentBridgeable(raw)
}

func grokChatAssistantToolCallsBridgeable(raw json.RawMessage) (int, string) {
	if strings.TrimSpace(string(raw)) == "null" {
		return 0, ""
	}
	var calls []map[string]json.RawMessage
	if json.Unmarshal(raw, &calls) != nil {
		return 0, "invalid_tool_calls"
	}
	for _, call := range calls {
		if call == nil {
			return 0, "invalid_tool_call"
		}
		for field := range call {
			switch field {
			case "id", "type", "function", "index":
			default:
				return 0, "unsafe_tool_call_field_" + field
			}
		}
		if rawIndex, exists := call["index"]; exists {
			var index *int
			if json.Unmarshal(rawIndex, &index) != nil || (index != nil && *index < 0) {
				return 0, "invalid_tool_call_index"
			}
		}
		var callID string
		if rawID, exists := call["id"]; !exists || json.Unmarshal(rawID, &callID) != nil || strings.TrimSpace(callID) == "" {
			return 0, "invalid_tool_call_id"
		}
		var callType string
		if rawType, exists := call["type"]; !exists || json.Unmarshal(rawType, &callType) != nil || callType != "function" {
			return 0, "unsupported_tool_call_type"
		}
		var function map[string]json.RawMessage
		if rawFunction, exists := call["function"]; !exists || json.Unmarshal(rawFunction, &function) != nil || function == nil {
			return 0, "invalid_tool_call_function"
		}
		for field := range function {
			if field != "name" && field != "arguments" {
				return 0, "unsafe_tool_call_function_field_" + field
			}
		}
		var name string
		if rawName, exists := function["name"]; !exists || json.Unmarshal(rawName, &name) != nil || strings.TrimSpace(name) == "" {
			return 0, "invalid_tool_call_function_name"
		}
		var arguments string
		if rawArguments, exists := function["arguments"]; !exists || json.Unmarshal(rawArguments, &arguments) != nil || !json.Valid([]byte(arguments)) {
			return 0, "invalid_tool_call_arguments"
		}
	}
	return len(calls), ""
}

func grokChatStructuredContentBridgeable(raw json.RawMessage) (bool, string) {
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return false, "non_text_message_content"
	}
	if len(parts) == 0 {
		return false, "empty_message_content"
	}
	hasContent := false
	for _, part := range parts {
		var partType string
		rawType, ok := part["type"]
		if !ok || json.Unmarshal(rawType, &partType) != nil {
			return false, "non_text_message_content"
		}
		switch strings.TrimSpace(partType) {
		case "text":
			var text string
			if raw, ok := part["text"]; ok && json.Unmarshal(raw, &text) == nil {
				if strings.TrimSpace(text) != "" {
					hasContent = true
				}
			}
		case "image_url", "input_image":
			hasContent = true
		default:
			return false, "unsupported_content_part_" + strings.TrimSpace(partType)
		}
	}
	if !hasContent {
		return false, "empty_message_content"
	}
	return true, ""
}

func grokChatNullOrNone(raw json.RawMessage) bool {
	if strings.TrimSpace(string(raw)) == "null" {
		return true
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.EqualFold(strings.TrimSpace(value), "none")
}

func grokChatJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func grokChatNullOrEmptyArray(raw json.RawMessage) bool {
	if strings.TrimSpace(string(raw)) == "null" {
		return true
	}
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && len(values) == 0
}

func grokChatResponsesCacheIntentBody(body []byte) ([]byte, error) {
	// Responses 转换器会省略空 Chat tools 数组；此时 auto/none 也没有语义效果，不能阻止
	// 普通无工具缓存路由。非空的已转换工具始终完整保留。
	if gjson.GetBytes(body, "tools").Exists() {
		return append([]byte(nil), body...), nil
	}
	choice := gjson.GetBytes(body, "tool_choice")
	if !choice.Exists() || choice.Type != gjson.String || (choice.String() != "auto" && choice.String() != "none") {
		return append([]byte(nil), body...), nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	delete(root, "tool_choice")
	return json.Marshal(root)
}

// grokChatResponsesBridgeModel 判断模型是否支持 Chat 到 Responses 的 Grok 桥接。
func grokChatResponsesBridgeModel(model string) bool {
	switch strings.ToLower(xai.StripGrokProviderPrefix(strings.TrimSpace(model))) {
	case "grok-4.5", "grok-4.6", "grok-4.6-latest":
		return true
	default:
		return false
	}
}

func grokChatResponsesRuntimeEligible(upstreamModel, cacheIdentity string) bool {
	return grokChatResponsesBridgeModel(upstreamModel) && strings.TrimSpace(cacheIdentity) != ""
}

// forwardGrokChatCompletionsViaResponses 将严格兼容的 Chat 请求转换为 xAI
// Responses 格式，并复用既有的 Responses-to-Chat 响应转换器。Grok CLI 使用独立的
// 上游协议，因此这里不会执行 Codex OAuth 转换。
func (s *OpenAIGatewayService) forwardGrokChatCompletionsViaResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	var chatReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse grok chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	cacheIdentity := resolveGrokCacheIdentity(c, body, promptCacheKey, upstreamModel)
	// 图片输入必须通过 Responses 桥接：原始 Chat Completions 路径无法把 image_url
	// 转发给非 Composer 模型的 Grok 原生视觉能力，否则图片会被静默丢弃；
	// 因此即使没有 prompt-cache 身份也要路由到 Responses。
	hasImageInput := openAIJSONValueMayContainImageInput(gjson.GetBytes(body, "messages"))
	if !grokChatResponsesRuntimeEligible(upstreamModel, cacheIdentity) && (!hasImageInput || !grokChatResponsesBridgeModel(upstreamModel)) {
		return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel, tlsRouterMatch...)
	}

	responsesReq, err := apicompat.ChatCompletionsToResponses(&chatReq)
	if err != nil {
		return nil, fmt.Errorf("convert grok chat completions to responses: %w", err)
	}
	responsesReq.Model = upstreamModel
	responsesReq.Stream = true
	// 让 Chat 与原生 Responses 对 OpenAI 兼容的 service_tier 别名保持一致；共享
	// 规范化器会丢弃未知值，避免其到达 xAI。
	normalizeResponsesRequestServiceTier(responsesReq)
	// 这些字段对 Codex 有用，但 Grok CLI 协议不需要；桥接请求应尽量贴近原生 Grok。
	responsesReq.Include = nil
	responsesReq.Store = nil

	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("marshal grok responses bridge request: %w", err)
	}
	// 在 Grok 能力清理前保留转换后的 Responses 意图；缓存路由必须看到真实客户端函数工具，
	// 而不是嵌套的 Chat Completions 声明或无工具副本。
	intentBody, err := grokChatResponsesCacheIntentBody(responsesBody)
	if err != nil {
		return nil, fmt.Errorf("normalize grok responses bridge cache intent: %w", err)
	}
	responsesBody, err = patchGrokResponsesBody(responsesBody, upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("patch grok responses bridge request: %w", err)
	}
	responsesBody, err = applyGrokResponsesCacheIdentity(responsesBody, intentBody, cacheIdentity, true)
	if err != nil {
		return nil, fmt.Errorf("apply grok responses bridge cache identity: %w", err)
	}
	responsesBody, err = applyGrokFreeRequestToolCacheRoute(c, responsesBody, intentBody, account, cacheIdentity)
	if err != nil {
		return nil, fmt.Errorf("apply grok responses bridge function-tool cache route: %w", err)
	}

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

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, fmt.Errorf("get grok access token: %w", err)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	upstreamReq, err := buildGrokResponsesRequest(upstreamCtx, c, account, responsesBody, token, cacheIdentity, s.cfg, s.settingService)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build grok responses bridge request: %w", err)
	}
	SetActualOpenAIUpstreamEndpoint(c, grokChatResponsesEndpoint)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(
		upstreamReq,
		proxyURL,
		account.ID,
		account.Concurrency,
		s.resolveOpenAITLSProfile(account, tlsRouterMatch...),
	)
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		if upstreamMsg == "" {
			upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
		}
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

	s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, upstreamModel), account, resp.Header, resp.StatusCode)

	var result *OpenAIForwardResult
	if clientStream {
		result, err = s.handleChatStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime, len(body))
	} else {
		result, err = s.handleChatBufferedStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime)
	}
	if result != nil {
		result.UpstreamEndpoint = grokChatResponsesEndpoint
		result.ResponseHeaders = resp.Header.Clone()
		if result.RequestID == "" {
			result.RequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
		}
		result.ReasoningEffort = extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	}
	return result, err
}
