package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/apicompat"
	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// grokGatewayUserAgent 标识本服务转发到 xAI/Grok 上游的网关请求。
	grokGatewayUserAgent = "tokenrouter-grok/1.0"
	// Composer 图片桥接使用 Grok Build 生成简洁描述，限制输出长度以控制额外用量。
	grokComposerImageBridgeVisionModel     = "grok-build-0.1"
	grokComposerImageBridgeMaxOutputTokens = 512
	grokCLIVersion                         = xai.CLIClientVersion
	grokDefaultResponsesModel              = xai.DefaultResponsesModel
	grokRateLimitFallbackCooldown          = 2 * time.Minute
	grokRateLimitRepeatCooldown            = 10 * time.Minute
	grokRateLimitSustainedCooldown         = 30 * time.Minute
	grokRateLimitMaxAdaptiveCooldown       = time.Hour
	grokRateLimitBackoffQuietPeriod        = time.Hour
)

func (s *OpenAIGatewayService) forwardGrokResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	if account.Type != AccountTypeOAuth && account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("grok account type %s is not supported by Responses forwarding", account.Type)
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if isGrokImageGenerationModel(upstreamModel) {
		err := fmt.Errorf("model %s is an image model and is not available on the Responses endpoint; use /v1/images/generations instead", upstreamModel)
		// 这是客户端点选择错误，直接返回 400，避免 handler 将普通错误改写为通用 502。
		if c != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"message": err.Error(),
					"param":   "model",
				},
			})
		}
		return nil, err
	}
	patchedBody, clientToolMapping, err := patchGrokResponsesBodyWithClientTools(body, upstreamModel)
	if err != nil {
		setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"type": "invalid_request_error", "message": err.Error(), "param": "tools",
		}})
		return nil, err
	}
	setGrokResponsesClientToolMapping(c, clientToolMapping)
	// xAI 没有原生 /responses/compact；改成普通 Responses 摘要轮次，响应阶段再封装为 compaction 条目。
	if isOpenAIResponsesCompactPath(c) {
		patchedBody, err = buildGrokCompactRequestBody(patchedBody)
		if err != nil {
			return nil, err
		}
	}
	// 从 xAI 实际接收的请求派生身份，使 Codex Responses Lite 的 additional_tools
	// 成为稳定工具前缀的一部分。
	cacheIdentity := resolveGrokCacheIdentity(c, patchedBody, "", upstreamModel)
	mixedCacheIntentBody := append([]byte(nil), patchedBody...)
	patchedBody, err = applyGrokResponsesCacheIdentity(patchedBody, body, cacheIdentity, account.IsGrokOAuth())
	if err != nil {
		return nil, fmt.Errorf("apply grok prompt cache identity: %w", err)
	}
	// Free OAuth 携带客户端函数工具时复用混合工具缓存路由，补齐 web_search/x_search，
	// 避免 xAI 强制落到不可缓存的 build-free 层级。
	patchedBody, err = applyGrokFreeRequestToolCacheRoute(c, patchedBody, mixedCacheIntentBody, account, cacheIdentity)
	if err != nil {
		return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", err)
	}

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	upstreamStart := time.Now()
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		upstreamReq, buildErr := buildGrokResponsesRequest(upstreamCtx, c, account, patchedBody, token, cacheIdentity, s.cfg, s.settingService)
		if buildErr != nil {
			return nil, buildErr
		}

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
		if err != nil {
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
		}

		// xAI 可能拒绝从其他账号或缓存标识响应中复制的加密 reasoning。
		// 仅移除被拒绝的加密 reasoning 载荷后，使用相同路由与凭据重试一次。
		if attempt > 0 || resp.StatusCode != http.StatusBadRequest {
			break
		}
		respBody := s.readUpstreamErrorBody(resp)
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if !isGrokInvalidEncryptedContentResponse(resp.StatusCode, respBody) {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}

		retryBody, changed, trimErr := trimGrokInvalidEncryptedContentRetryBody(patchedBody)
		if trimErr != nil {
			return nil, fmt.Errorf("prepare Grok invalid encrypted_content retry: %w", trimErr)
		}
		if !changed {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}

		patchedBody = retryBody
		slog.Info("grok_invalid_encrypted_content_retry", "account_id", account.ID, "cache_identity_present", cacheIdentity != "")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
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
			return s.handleErrorResponse(ctx, resp, c, account, patchedBody, upstreamModel)
		}
		if kind == "failover" {
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				ResponseHeaders:        resp.Header.Clone(),
				RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
			}
		}
		return s.handleErrorResponse(ctx, resp, c, account, patchedBody, upstreamModel)
	}

	// 附加模型信息，使限流快照能够扩散团队与模型冷却状态。
	stateCtx := withGrokTeamRateLimitModel(ctx, upstreamModel)
	s.updateGrokUsageFromResponse(stateCtx, account, resp.Header, resp.StatusCode)

	var usage *OpenAIUsage
	var firstTokenMs *int
	responseID := ""
	searchCount := 0
	imageCount := 0
	var imageOutputSizes []string
	if reqStream {
		maxLineSize := defaultMaxLineSize
		if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
			maxLineSize = s.cfg.Gateway.MaxLineSize
		}
		resp.Body = newGrokResponsesBillingPingFilterBody(resp.Body, account, maxLineSize)
		if hasGrokResponsesClientToolMapping(clientToolMapping) {
			resp.Body = newGrokResponsesClientToolStreamBody(resp.Body, clientToolMapping, maxLineSize)
		}
		streamResult, err := s.handleStreamingResponse(ctx, resp, c, account, startTime, originalModel, upstreamModel)
		if err != nil {
			return nil, err
		}
		usage = streamResult.usage
		firstTokenMs = streamResult.firstTokenMs
		responseID = strings.TrimSpace(streamResult.responseID)
		searchCount = streamResult.searchCount
		imageCount = streamResult.imageCount
		imageOutputSizes = streamResult.imageOutputSizes
	} else {
		nonStreamResult, err := s.handleNonStreamingResponse(ctx, resp, c, account, originalModel, upstreamModel)
		if err != nil {
			return nil, err
		}
		usage = nonStreamResult.usage
		responseID = strings.TrimSpace(nonStreamResult.responseID)
		searchCount = nonStreamResult.searchCount
		imageCount = nonStreamResult.imageCount
		imageOutputSizes = nonStreamResult.imageOutputSizes
	}

	if usage == nil {
		usage = &OpenAIUsage{}
	}
	reasoningEffort := extractOpenAIReasoningEffortFromBody(patchedBody, originalModel)
	result := &OpenAIForwardResult{
		RequestID:       firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
		ResponseID:      responseID,
		Usage:           *usage,
		Model:           originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
		ReasoningEffort: reasoningEffort,
		Stream:          reqStream,
		OpenAIWSMode:    false,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
		FirstTokenMs:    firstTokenMs,
	}
	// 从共享 Responses 处理器传递搜索与图片计数；否则流式或 JSON 统计虽会运行，
	// 但 search_price_per_1k 与图片费用不会生效。
	if searchCount > 0 {
		result.SearchCount = searchCount
	}
	if imageCount > 0 {
		result.ImageCount = imageCount
		result.ImageOutputSizes = imageOutputSizes
	}
	return result, nil
}

func isGrokInvalidEncryptedContentResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}

	// xAI 同时使用过扁平和嵌套两种错误封装：
	// 扁平形式：{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content."}
	// 嵌套形式：{"error":{"message":"Could not decrypt the provided encrypted_content."}}
	code := strings.TrimSpace(gjson.GetBytes(body, "code").String())
	message := ""
	errNode := gjson.GetBytes(body, "error")
	switch {
	case errNode.Type == gjson.String:
		message = errNode.String()
	case errNode.IsObject():
		message = firstNonEmpty(errNode.Get("message").String(), errNode.Get("error").String())
		if code == "" {
			code = strings.TrimSpace(errNode.Get("code").String())
		}
	default:
		message = gjson.GetBytes(body, "message").String()
	}
	normalizedMessage := strings.ToLower(strings.TrimSpace(message))
	if normalizedMessage == "" {
		return false
	}

	if strings.EqualFold(code, "invalid_encrypted_content") {
		return true
	}
	// 保留 xAI 官方扁平错误码校验，避免重试无关的 400 响应。
	if !strings.EqualFold(code, "invalid-argument") && code != "" {
		return false
	}
	// OpenAI 风格的嵌套错误可能省略顶层错误码，此时必须包含解密错误文本。
	if code == "" && !strings.Contains(normalizedMessage, "decrypt") {
		return false
	}
	return strings.Contains(normalizedMessage, "encrypted_content") &&
		(strings.Contains(normalizedMessage, "decrypt") ||
			strings.Contains(normalizedMessage, "unmodified"))
}

// requestHasGrokEncryptedReasoning 判断出站 Responses 请求体是否仍包含可在重试前
// 剥离的 reasoning.encrypted_content。
func requestHasGrokEncryptedReasoning(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return false
	}
	items := input.Array()
	if input.IsObject() {
		items = []gjson.Result{input}
	}
	for _, item := range items {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		enc := item.Get("encrypted_content")
		if enc.Exists() && enc.Type != gjson.Null && strings.TrimSpace(enc.String()) != "" {
			return true
		}
	}
	return false
}

type grokEncryptedContentStripRetriedKey struct{}

func markGrokEncryptedContentStripRetried(ctx context.Context) context.Context {
	return context.WithValue(ctx, grokEncryptedContentStripRetriedKey{}, true)
}

func grokEncryptedContentStripRetried(ctx context.Context) bool {
	v, _ := ctx.Value(grokEncryptedContentStripRetriedKey{}).(bool)
	return v
}

// stripAnthropicThinkingSignatures 从 Claude 历史记录中移除 thinking.signature，
// 使另一个 Grok OAuth 账号在解密失败后仍能接收多轮工具续接。没有修改时返回 false。
func stripAnthropicThinkingSignatures(body []byte) ([]byte, bool) {
	if len(body) == 0 || !bytes.Contains(body, []byte(`"signature"`)) {
		return body, false
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, false
	}
	messages, ok := req["messages"].([]any)
	if !ok || len(messages) == 0 {
		return body, false
	}
	changed := false
	for _, rawMsg := range messages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			if typ, _ := block["type"].(string); typ != "thinking" {
				continue
			}
			if _, has := block["signature"]; has {
				delete(block, "signature")
				changed = true
			}
		}
	}
	if !changed {
		return body, false
	}
	out, err := json.Marshal(req)
	if err != nil {
		return body, false
	}
	return out, true
}

func trimGrokInvalidEncryptedContentRetryBody(body []byte) ([]byte, bool, error) {
	input := gjson.GetBytes(body, "input")
	items := input.Array()
	if input.IsObject() {
		items = []gjson.Result{input}
	}

	hasEncryptedReasoning := false
	for _, item := range items {
		if strings.TrimSpace(item.Get("type").String()) == "reasoning" && item.Get("encrypted_content").Exists() {
			hasEncryptedReasoning = true
			break
		}
	}
	if !hasEncryptedReasoning {
		return body, false, nil
	}

	var requestBody map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&requestBody); err != nil {
		return nil, false, err
	}
	if !trimOpenAIEncryptedReasoningItems(requestBody) {
		return body, false, nil
	}

	retryBody, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return nil, false, err
	}
	return retryBody, true, nil
}

func patchGrokResponsesBody(body []byte, upstreamModel string) ([]byte, error) {
	return patchGrokResponsesBodyBase(body, upstreamModel)
}

func patchGrokResponsesBodyWithClientTools(body []byte, upstreamModel string) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	if !json.Valid(body) {
		return nil, apicompat.ResponsesClientToolMapping{}, fmt.Errorf("invalid json request body")
	}
	promoted, err := sanitizeGrokResponsesInput(body)
	if err != nil {
		return nil, apicompat.ResponsesClientToolMapping{}, err
	}
	adapted, mapping, err := adaptGrokResponsesClientTools(promoted)
	if err != nil {
		return nil, apicompat.ResponsesClientToolMapping{}, err
	}
	patched, err := patchGrokResponsesBodyBase(adapted, upstreamModel)
	if err != nil {
		return nil, apicompat.ResponsesClientToolMapping{}, err
	}
	return patched, mapping, nil
}

func patchGrokResponsesBodyBase(body []byte, upstreamModel string) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid json request body")
	}
	// sjson 可能复用输入底层数组，因此保留调用方请求字节不变，
	// 同一请求体还可能被计费或重试路径读取。
	out, err := sjson.SetBytes(append([]byte(nil), body...), "model", upstreamModel)
	if err != nil {
		return nil, err
	}
	out, err = normalizeGrokResponsesReasoningEffort(out, upstreamModel)
	if err != nil {
		return nil, err
	}
	out, err = sanitizeGrokResponsesModelCapabilities(out, upstreamModel)
	if err != nil {
		return nil, err
	}
	for _, unsupportedField := range []string{"prompt_cache_retention", "safety_identifier"} {
		if gjson.GetBytes(out, unsupportedField).Exists() {
			out, err = sjson.DeleteBytes(out, unsupportedField)
			if err != nil {
				return nil, err
			}
		}
	}
	if strings.EqualFold(upstreamModel, "grok-4.5") {
		for _, unsupportedField := range []string{"presence_penalty", "presencePenalty", "frequency_penalty", "frequencyPenalty", "stop"} {
			if gjson.GetBytes(out, unsupportedField).Exists() {
				out, err = sjson.DeleteBytes(out, unsupportedField)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if grokModelRejectsLogprobs(upstreamModel) {
		for _, unsupportedField := range []string{"logprobs", "top_logprobs"} {
			if gjson.GetBytes(out, unsupportedField).Exists() {
				out, err = sjson.DeleteBytes(out, unsupportedField)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	out, err = sanitizeGrokResponsesUnsupportedFields(out)
	if err != nil {
		return nil, err
	}
	out, err = convertOpenAICompactInputsForGrok(out)
	if err != nil {
		return nil, err
	}
	out, err = sanitizeGrokResponsesInput(out)
	if err != nil {
		return nil, err
	}
	out, err = sanitizeGrokReasoningNullContent(out)
	if err != nil {
		return nil, err
	}
	out, err = sanitizeGrokResponsesTools(out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// grokModelRejectsLogprobs 识别不接受 OpenAI logprobs 字段的 Grok 4.20 模型族。
func grokModelRejectsLogprobs(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(model, "/"); slash >= 0 {
		model = strings.TrimSpace(model[slash+1:])
	}
	return strings.HasPrefix(model, "grok-4.20")
}

// sanitizeGrokResponsesModelCapabilities 移除目标模型明确不支持的 Responses 参数。
func sanitizeGrokResponsesModelCapabilities(body []byte, upstreamModel string) ([]byte, error) {
	if !grokModelRejectsReasoningEffort(upstreamModel) {
		return body, nil
	}

	out := body
	for _, field := range []string{"reasoning", "reasoning_effort", "reasoningEffort"} {
		if !gjson.GetBytes(out, field).Exists() {
			continue
		}
		var err error
		out, err = sjson.DeleteBytes(out, field)
		if err != nil {
			return nil, fmt.Errorf("remove unsupported Grok Composer %s: %w", field, err)
		}
	}
	return out, nil
}

// grokModelRejectsReasoningEffort 判断 Composer 别名是否拒绝 reasoning 参数。
func grokModelRejectsReasoningEffort(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if slash := strings.LastIndex(model, "/"); slash >= 0 {
		model = strings.TrimSpace(model[slash+1:])
	}
	switch model {
	case "grok-composer", "grok-composer-2.5-fast", "composer-2.5":
		return true
	default:
		return false
	}
}

func normalizeGrokResponsesReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	supportsEffort := grokSupportsReasoningEffort(upstreamModel)
	out := body
	var err error
	for _, field := range []string{"reasoning.effort", "reasoning_effort"} {
		value := gjson.GetBytes(out, field)
		if !value.Exists() {
			continue
		}
		normalized, keep := normalizeGrokReasoningEffortValue(value.String())
		if !supportsEffort || !keep {
			out, err = sjson.DeleteBytes(out, field)
		} else {
			out, err = sjson.SetBytes(out, field, normalized)
		}
		if err != nil {
			return nil, fmt.Errorf("normalize Grok reasoning field %s: %w", field, err)
		}
	}
	if camel := gjson.GetBytes(out, "reasoningEffort"); camel.Exists() {
		normalized, keep := normalizeGrokReasoningEffortValue(camel.String())
		out, err = sjson.DeleteBytes(out, "reasoningEffort")
		if err != nil {
			return nil, fmt.Errorf("remove Grok reasoningEffort: %w", err)
		}
		if supportsEffort && keep && !gjson.GetBytes(out, "reasoning_effort").Exists() {
			out, err = sjson.SetBytes(out, "reasoning_effort", normalized)
			if err != nil {
				return nil, fmt.Errorf("set Grok reasoning_effort: %w", err)
			}
		}
	}
	if reasoning := gjson.GetBytes(out, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
		out, err = sjson.DeleteBytes(out, "reasoning")
		if err != nil {
			return nil, fmt.Errorf("remove empty Grok reasoning: %w", err)
		}
	}
	return out, nil
}

func normalizeGrokChatReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoningEffort").String())
	}
	normalized, keep := normalizeGrokReasoningEffortValue(raw)
	keep = keep && grokSupportsReasoningEffort(upstreamModel)
	out := body
	var err error
	if gjson.GetBytes(out, "reasoningEffort").Exists() {
		out, err = sjson.DeleteBytes(out, "reasoningEffort")
		if err != nil {
			return nil, err
		}
	}
	if !keep {
		if gjson.GetBytes(out, "reasoning_effort").Exists() {
			out, err = sjson.DeleteBytes(out, "reasoning_effort")
		}
		return out, err
	}
	out, err = sjson.SetBytes(out, "reasoning_effort", normalized)
	return out, err
}

func normalizeGrokReasoningEffortValue(raw string) (string, bool) {
	value := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case "none", "low", "medium", "high":
		return value, true
	case "minimal":
		return "low", true
	case "xhigh", "extrahigh", "max", "ultra":
		return "high", true
	default:
		return "", false
	}
}

func grokSupportsReasoningEffort(model string) bool {
	model = strings.ToLower(xai.StripGrokProviderPrefix(strings.TrimSpace(model)))
	switch model {
	case xai.DefaultTextModel, "grok-4.5-latest", "grok-4.6", "grok-4.6-latest",
		"grok-4.3", "grok-4.3-latest",
		"grok-3-mini", "grok-3-mini-fast", "grok-4.20-0309-reasoning",
		"grok-4.20-reasoning", "grok-4.20-multi-agent-0309":
		return true
	default:
		return false
	}
}

var grokResponsesUnsupportedRecursiveFields = map[string]struct{}{
	"external_web_access": {},
}

func sanitizeGrokResponsesUnsupportedFields(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"external_web_access"`)) {
		return body, nil
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !deleteJSONFields(payload, grokResponsesUnsupportedRecursiveFields) {
		return body, nil
	}
	return json.Marshal(payload)
}

func deleteJSONFields(value any, fields map[string]struct{}) bool {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for field := range fields {
			if _, ok := typed[field]; ok {
				delete(typed, field)
				changed = true
			}
		}
		for _, child := range typed {
			if deleteJSONFields(child, fields) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range typed {
			if deleteJSONFields(child, fields) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// sanitizeGrokResponsesInput 移除 Codex/Responses Lite 私有的 additional_tools 载体，
// 同时按载体顺序把其中受支持的工具提升到顶层并保留原顶层顺序；最终由
// sanitizeGrokResponsesTools 过滤 xAI 不支持的类型。
func sanitizeGrokResponsesInput(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"additional_tools"`)) {
		return body, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body, nil
	}

	rawItems := input.Array()
	filtered := make([]json.RawMessage, 0, len(rawItems))
	topLevelTools := gjson.GetBytes(body, "tools")
	mergedTools := make([]json.RawMessage, 0)
	seenTools := make(map[string]struct{})
	appendTool := func(tool gjson.Result) bool {
		key := grokResponsesToolDedupKey(tool)
		if _, exists := seenTools[key]; exists {
			return false
		}
		seenTools[key] = struct{}{}
		mergedTools = append(mergedTools, json.RawMessage(tool.Raw))
		return true
	}
	if topLevelTools.IsArray() {
		for _, tool := range topLevelTools.Array() {
			seenTools[grokResponsesToolDedupKey(tool)] = struct{}{}
			mergedTools = append(mergedTools, json.RawMessage(tool.Raw))
		}
	}

	promoted := false
	for _, item := range rawItems {
		if strings.TrimSpace(item.Get("type").String()) == "additional_tools" {
			tools := item.Get("tools")
			if tools.IsArray() {
				for _, tool := range tools.Array() {
					if appendTool(tool) {
						promoted = true
					}
				}
			}
			continue
		}
		filtered = append(filtered, json.RawMessage(item.Raw))
	}
	if len(filtered) == len(rawItems) {
		return body, nil
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	body, err = sjson.SetRawBytes(body, "input", encoded)
	if err != nil || !promoted {
		return body, err
	}
	encodedTools, err := json.Marshal(mergedTools)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "tools", encodedTools)
}

func grokResponsesToolDedupKey(tool gjson.Result) string {
	toolType := strings.TrimSpace(tool.Get("type").String())
	if toolType != "" {
		if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
			return "type:" + toolType + "\x00name:" + name
		}
		if toolType == "mcp" {
			if label := strings.TrimSpace(tool.Get("server_label").String()); label != "" {
				return "type:mcp\x00server_label:" + label
			}
		}
	}
	return "json:" + normalizeCompatSeedJSON(json.RawMessage(tool.Raw))
}

// sanitizeGrokReasoningNullContent 删除 reasoning 项中的 "content": null。
// xAI 的 untagged enum 反序列化器拒收该字段，返回 422。
func sanitizeGrokReasoningNullContent(body []byte) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body, nil
	}

	items := input.Array()
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		contentResult := item.Get("content")
		if contentResult.Exists() && contentResult.Type == gjson.Null {
			var err error
			body, err = sjson.DeleteBytes(body, fmt.Sprintf("input.%d.content", i))
			if err != nil {
				return nil, err
			}
		}
	}
	return body, nil
}

var grokResponsesSupportedToolTypes = map[string]struct{}{
	"code_execution":     {},
	"code_interpreter":   {},
	"collections_search": {},
	"file_search":        {},
	"function":           {},
	"mcp":                {},
	"shell":              {},
	"web_search":         {},
	"x_search":           {},
}

func sanitizeGrokResponsesTools(body []byte) ([]byte, error) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() {
		if gjson.GetBytes(body, "tool_choice").Exists() {
			return sjson.DeleteBytes(body, "tool_choice")
		}
		return body, nil
	}
	if !tools.IsArray() {
		return body, nil
	}

	rawTools := tools.Array()
	filteredTools := make([]json.RawMessage, 0, len(rawTools))
	toolsChanged := false
	for _, tool := range rawTools {
		toolType := strings.TrimSpace(tool.Get("type").String())
		if _, ok := grokResponsesSupportedToolTypes[toolType]; ok {
			raw := json.RawMessage(tool.Raw)
			if toolType == "function" && (!tool.Get("parameters").Exists() || tool.Get("parameters").Type == gjson.Null) {
				var payload map[string]any
				if err := json.Unmarshal(raw, &payload); err != nil {
					return nil, err
				}
				payload["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
				encoded, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				raw = encoded
				toolsChanged = true
			}
			filteredTools = append(filteredTools, raw)
		}
	}

	var err error
	if len(filteredTools) != len(rawTools) || toolsChanged {
		if len(filteredTools) == 0 {
			body, err = sjson.DeleteBytes(body, "tools")
		} else {
			var encoded []byte
			encoded, err = json.Marshal(filteredTools)
			if err != nil {
				return nil, err
			}
			body, err = sjson.SetRawBytes(body, "tools", encoded)
		}
		if err != nil {
			return nil, err
		}
	}

	toolChoice := gjson.GetBytes(body, "tool_choice")
	if !toolChoice.Exists() {
		return body, nil
	}
	if shouldDropGrokToolChoice(toolChoice, filteredTools) {
		body, err = sjson.DeleteBytes(body, "tool_choice")
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

func shouldDropGrokToolChoice(toolChoice gjson.Result, tools []json.RawMessage) bool {
	if len(tools) == 0 {
		return true
	}
	if !toolChoice.IsObject() {
		return false
	}
	choiceType := strings.TrimSpace(toolChoice.Get("type").String())
	if choiceType == "" {
		return false
	}
	if _, ok := grokResponsesSupportedToolTypes[choiceType]; !ok {
		return true
	}
	if choiceType == "function" {
		choiceName := strings.TrimSpace(toolChoice.Get("name").String())
		if choiceName == "" {
			choiceName = strings.TrimSpace(toolChoice.Get("function.name").String())
		}
		if choiceName == "" {
			return false
		}
		for _, tool := range tools {
			var item struct {
				Type     string `json:"type"`
				Name     string `json:"name"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if err := json.Unmarshal(tool, &item); err != nil {
				continue
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = strings.TrimSpace(item.Function.Name)
			}
			if strings.TrimSpace(item.Type) == "function" && name == choiceName {
				return false
			}
		}
		return true
	}
	return false
}

// bridgeGrokComposerImageInputs 先用 Grok Build 描述 Composer 请求中的图片，
// 再把图片块改写为文本描述，并累计预检请求产生的用量。
func (s *OpenAIGatewayService) bridgeGrokComposerImageInputs(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) ([]byte, OpenAIUsage, bool, error) {
	if !shouldBridgeGrokComposerImageInputs(body) {
		return body, OpenAIUsage{}, false, nil
	}

	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return body, OpenAIUsage{}, false, fmt.Errorf("parse grok composer image bridge request: %w", err)
	}

	imageURLs := collectGrokComposerImageURLs(reqBody)
	if len(imageURLs) == 0 {
		return body, OpenAIUsage{}, false, nil
	}

	descriptions := make([]string, 0, len(imageURLs))
	var bridgeUsage OpenAIUsage
	for index, imageURL := range imageURLs {
		description, usage, err := s.describeGrokComposerImage(ctx, c, account, token, imageURL, index+1)
		if err != nil {
			return body, bridgeUsage, false, err
		}
		descriptions = append(descriptions, description)
		addOpenAIUsage(&bridgeUsage, usage)
	}

	if !rewriteGrokComposerImagesAsText(reqBody, descriptions) {
		return body, bridgeUsage, false, nil
	}
	bridgedBody, err := marshalOpenAIUpstreamJSON(reqBody)
	if err != nil {
		return body, bridgeUsage, false, fmt.Errorf("serialize grok composer image bridge request: %w", err)
	}
	return bridgedBody, bridgeUsage, true, nil
}

func shouldBridgeGrokComposerImageInputs(body []byte) bool {
	if len(body) == 0 || !isGrokComposerModel(gjson.GetBytes(body, "model").String()) {
		return false
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() {
		return false
	}
	return openAIJSONValueMayContainImageInput(messages)
}

func isGrokComposerModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if model == "" {
		return false
	}
	if strings.Contains(model, "/") {
		parts := strings.Split(model, "/")
		model = strings.TrimSpace(parts[len(parts)-1])
	}
	return strings.Contains(model, "composer")
}

func collectGrokComposerImageURLs(reqBody map[string]any) []string {
	messages, ok := reqBody["messages"].([]any)
	if !ok {
		return nil
	}

	var imageURLs []string
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			if imageURL := grokComposerImageURLFromPart(part); imageURL != "" {
				imageURLs = append(imageURLs, imageURL)
			}
		}
	}
	return imageURLs
}

func grokComposerImageURLFromPart(part any) string {
	partMap, ok := part.(map[string]any)
	if !ok {
		return ""
	}
	if strings.TrimSpace(strings.ToLower(fmt.Sprint(partMap["type"]))) != "image_url" {
		return ""
	}
	switch imageURL := partMap["image_url"].(type) {
	case string:
		return normalizeGrokComposerImageURL(imageURL)
	case map[string]any:
		raw, _ := imageURL["url"].(string)
		return normalizeGrokComposerImageURL(raw)
	default:
		return ""
	}
}

func normalizeGrokComposerImageURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || isEmptyBase64DataURI(trimmed) {
		return ""
	}
	return trimmed
}

// describeGrokComposerImage 复用 Grok Responses 转发配置执行单张图片预检，
// 并沿用账号快照、错误记录和故障转移策略。
func (s *OpenAIGatewayService) describeGrokComposerImage(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token string,
	imageURL string,
	index int,
) (string, OpenAIUsage, error) {
	body, err := buildGrokComposerImageDescriptionBody(imageURL, index)
	if err != nil {
		return "", OpenAIUsage{}, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	// 图片描述探测是辅助请求而非会话轮次，不能绑定调用方的 Grok 提示缓存身份。
	upstreamReq, err := buildGrokResponsesRequest(upstreamCtx, c, account, body, token, "", s.cfg)
	releaseUpstreamCtx()
	if err != nil {
		return "", OpenAIUsage{}, fmt.Errorf("build grok composer image bridge request: %w", err)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return "", OpenAIUsage{}, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
		if upstreamMsg == "" {
			upstreamMsg = fmt.Sprintf("xAI image bridge upstream returned status %d", resp.StatusCode)
		}
		decision := s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, grokComposerImageBridgeVisionModel)
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
			return "", OpenAIUsage{}, fmt.Errorf("grok composer image bridge upstream gateway error")
		}
		if kind == "failover" {
			return "", OpenAIUsage{}, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				ResponseHeaders:        resp.Header.Clone(),
				RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
			}
		}
		return "", OpenAIUsage{}, fmt.Errorf("grok composer image bridge upstream error: %s", upstreamMsg)
	}

	s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, grokComposerImageBridgeVisionModel), account, resp.Header, resp.StatusCode)
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
	if err != nil {
		return "", OpenAIUsage{}, fmt.Errorf("read grok composer image bridge response: %w", err)
	}

	var parsed apicompat.ResponsesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", OpenAIUsage{}, fmt.Errorf("parse grok composer image bridge response: %w", err)
	}
	description := strings.TrimSpace(grokResponsesOutputText(&parsed))
	if description == "" {
		return "", copyOpenAIUsageFromResponsesUsage(parsed.Usage), fmt.Errorf("grok composer image bridge returned empty description")
	}
	return description, copyOpenAIUsageFromResponsesUsage(parsed.Usage), nil
}

func buildGrokComposerImageDescriptionBody(imageURL string, index int) ([]byte, error) {
	prompt := fmt.Sprintf("Describe image %d in concise, factual text for a downstream coding/composer model. Include visible text, UI elements, diagrams, errors, and spatial relationships. Do not mention that you are an image analysis bridge.", index)
	req := map[string]any{
		"model":             grokComposerImageBridgeVisionModel,
		"stream":            false,
		"store":             false,
		"max_output_tokens": grokComposerImageBridgeMaxOutputTokens,
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": prompt},
					map[string]any{"type": "input_image", "image_url": imageURL},
				},
			},
		},
	}
	return marshalOpenAIUpstreamJSON(req)
}

func grokResponsesOutputText(resp *apicompat.ResponsesResponse) string {
	if resp == nil {
		return ""
	}
	var parts []string
	for _, output := range resp.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" || content.Type == "text" || content.Type == "input_text" {
				if text := strings.TrimSpace(content.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// rewriteGrokComposerImagesAsText 按图片出现顺序替换消息内容，同时保留原文本块。
func rewriteGrokComposerImagesAsText(reqBody map[string]any, descriptions []string) bool {
	messages, ok := reqBody["messages"].([]any)
	if !ok {
		return false
	}

	imageIndex := 0
	changed := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		var textParts []string
		messageChanged := false
		for _, part := range parts {
			if imageURL := grokComposerImageURLFromPart(part); imageURL != "" {
				if imageIndex < len(descriptions) {
					textParts = append(textParts, fmt.Sprintf("Image %d description: %s", imageIndex+1, strings.TrimSpace(descriptions[imageIndex])))
				}
				imageIndex++
				messageChanged = true
				continue
			}
			if text := grokComposerTextFromPart(part); text != "" {
				textParts = append(textParts, text)
			}
		}
		if messageChanged {
			msgMap["content"] = strings.Join(textParts, "\n\n")
			changed = true
		}
	}
	return changed
}

func grokComposerTextFromPart(part any) string {
	partMap, ok := part.(map[string]any)
	if !ok {
		return ""
	}
	partType := strings.TrimSpace(strings.ToLower(fmt.Sprint(partMap["type"])))
	switch partType {
	case "text", "input_text":
		text, _ := partMap["text"].(string)
		return strings.TrimSpace(text)
	default:
		return ""
	}
}

func addOpenAIUsage(dst *OpenAIUsage, usage OpenAIUsage) {
	if dst == nil {
		return
	}
	dst.InputTokens += usage.InputTokens
	dst.ImageInputTokens += usage.ImageInputTokens
	dst.OutputTokens += usage.OutputTokens
	dst.CacheCreationInputTokens += usage.CacheCreationInputTokens
	dst.CacheReadInputTokens += usage.CacheReadInputTokens
	dst.ImageOutputTokens += usage.ImageOutputTokens
}

func buildGrokResponsesRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token, cacheIdentity string, cfg *config.Config, settings ...*SettingService) (*http.Request, error) {
	targetURL, err := buildGrokResponsesURL(account, cfg, settings...)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if account.IsGrokOAuth() {
		applyGrokCLIHeaders(req.Header)
	}
	applyGrokCacheHeaders(req.Header, cacheIdentity)
	if c != nil {
		if v := c.GetHeader("OpenAI-Beta"); strings.TrimSpace(v) != "" {
			req.Header.Set("OpenAI-Beta", v)
		}
	}
	// 账号级请求头覆写最后应用，使配置值优先于上面的内置默认头；
	// 打到官方 CLI 网关时身份头仍由共享传输层最终强制。
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

// applyGrokCLIHeaders 将订阅流量标识为受支持的 Grok CLI 版本；缺少这些标识时，
// CLI 网关会拒绝其它字段均有效的 OAuth 请求。
func applyGrokCLIHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Set("User-Agent", grokGatewayUserAgent)
	headers.Set("X-Grok-Client-Version", grokCLIVersion)
	headers.Set("X-Grok-Client-Mode", "interactive")
}

func (s *OpenAIGatewayService) updateGrokUsageSnapshot(ctx context.Context, account *Account, snapshot *xai.QuotaSnapshot) {
	if s == nil || account == nil || account.ID <= 0 || snapshot == nil {
		return
	}
	accountID := account.ID
	now := time.Now()
	resetAt, hasActiveLimit := grokRateLimitResetAtForAccount(account, snapshot, now)
	if hasActiveLimit {
		normalizeGrokExhaustedWindowResets(snapshot, resetAt, now)
	}
	recovery := isSuccessfulGrokRateLimitRecovery(account, snapshot)
	critical := snapshot.StatusCode == http.StatusTooManyRequests || hasActiveLimit || recovery
	if s.codexSnapshotThrottle != nil {
		allowed := s.codexSnapshotThrottle.Allow(accountID, now)
		if !critical && !allowed {
			return
		}
	}

	updates := map[string]any{
		grokQuotaSnapshotExtraKey: snapshot,
	}
	// 同时派生 grokThresholdCandidates 评估器读取的调度阈值扩展字段 grok_sched_*。
	// 缺少此写入逻辑时，管理员配置的 Grok 自动暂停阈值无法触发。
	for k, v := range buildGrokSchedulerExtraUpdates(snapshot) {
		updates[k] = v
	}
	stateCtx := ctx
	if hasActiveLimit {
		var cancel context.CancelFunc
		stateCtx, cancel = openAIAccountStateContext(ctx)
		defer cancel()
	}
	// 请求路径中的 Account 指针来自每次 Redis/DB 解码，不是进程内共享缓存；
	// 这里与 token 刷新和限流写入保持一致，调用方不得跨 goroutine 复用同一指针。
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[grokQuotaSnapshotExtraKey] = snapshot
	if s.accountRepo != nil {
		_ = s.accountRepo.UpdateExtra(stateCtx, accountID, updates)
	}
	// 池模式上游本身负责在真实账号池中切换，额度头只作为观测数据保留，不能反向
	// 冷却本地这个聚合账号。非池模式仍将错误响应或成功后耗尽的窗口写成真实限流。
	if hasActiveLimit && !account.IsPoolMode() {
		s.rateLimitGrok(stateCtx, account, resetAt)
	} else if recovery {
		clearGrokRateLimitAfterRecovery(stateCtx, s.accountRepo, account)
	}
}

func (s *OpenAIGatewayService) updateGrokUsageFromResponse(ctx context.Context, account *Account, headers http.Header, statusCode int) {
	snapshot := parseGrokQuotaSnapshot(headers, statusCode, time.Now())
	if snapshot != nil {
		stampGrokQuotaSnapshotForPlan(account, snapshot, grokRequestedModelFromCtx(ctx))
		s.updateGrokUsageSnapshot(ctx, account, snapshot)
		return
	}
	// 即使上游没有返回可选的额度响应头，成功响应仍能证明账号已经恢复。
	// 不用空快照覆盖已有信息，只清除本次请求观察到的精确冷却代次。
	recoverySnapshot := &xai.QuotaSnapshot{StatusCode: statusCode}
	if isSuccessfulGrokRateLimitRecovery(account, recoverySnapshot) {
		clearGrokRateLimitAfterRecovery(ctx, s.accountRepo, account)
	}
}

func parseGrokQuotaSnapshot(headers http.Header, statusCode int, now time.Time) *xai.QuotaSnapshot {
	snapshot := xai.ParseQuotaHeaders(headers, statusCode)
	if snapshot == nil && statusCode == http.StatusTooManyRequests {
		return &xai.QuotaSnapshot{
			StatusCode: statusCode,
			UpdatedAt:  now.UTC().Format(time.RFC3339),
		}
	}
	return snapshot
}

func normalizeGrokExhaustedWindowResets(snapshot *xai.QuotaSnapshot, resetAt, now time.Time) {
	if snapshot == nil || !resetAt.After(now) {
		return
	}
	for _, window := range []*xai.QuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window == nil || window.Remaining == nil || *window.Remaining > 0 {
			continue
		}
		candidate := time.Time{}
		if window.ResetUnix != nil && *window.ResetUnix > 0 {
			candidate = time.Unix(*window.ResetUnix, 0)
		} else if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(window.ResetAt)); err == nil {
			candidate = parsed
		}
		if !candidate.After(now) {
			candidate = resetAt
		}
		resetUnix := candidate.Unix()
		window.ResetUnix = &resetUnix
		window.ResetAt = candidate.UTC().Format(time.RFC3339)
	}
}

func grokRateLimitResetAt(snapshot *xai.QuotaSnapshot, now time.Time) (time.Time, bool) {
	if snapshot == nil {
		return time.Time{}, false
	}

	// Retry-After 是 xAI 明确给出的重试边界；以观测时间为基准，避免每次读取持久化
	// 快照时重新启动冷却。
	retryAfterExpired := false
	var resetAt time.Time
	if snapshot.RetryAfterSeconds != nil && *snapshot.RetryAfterSeconds > 0 {
		observedAt := now
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(snapshot.UpdatedAt)); err == nil {
			observedAt = parsed
		}
		retryAfterResetAt := observedAt.Add(time.Duration(*snapshot.RetryAfterSeconds) * time.Second)
		if retryAfterResetAt.After(now) {
			resetAt = retryAfterResetAt
		} else {
			retryAfterExpired = true
		}
	}

	exhausted := false
	for _, window := range []*xai.QuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window == nil || window.Remaining == nil || *window.Remaining > 0 {
			continue
		}
		exhausted = true
		candidate := time.Time{}
		if window.ResetUnix != nil && *window.ResetUnix > 0 {
			candidate = time.Unix(*window.ResetUnix, 0)
		} else if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(window.ResetAt)); err == nil {
			candidate = parsed
		}
		if candidate.After(now) && candidate.After(resetAt) {
			resetAt = candidate
		}
	}
	if !resetAt.IsZero() {
		return resetAt, true
	}
	// Retry-After 与快照时间组合后即为绝对边界。已过期的持久化快照不能被转换为新的
	// 滚动回退冷却，但仍允许采用更晚的明确窗口重置时间。
	if retryAfterExpired {
		return time.Time{}, false
	}
	if exhausted || snapshot.StatusCode == http.StatusTooManyRequests {
		return now.Add(grokRateLimitFallbackCooldown), true
	}
	return time.Time{}, false
}

func grokRateLimitResetAtForAccount(account *Account, snapshot *xai.QuotaSnapshot, now time.Time) (time.Time, bool) {
	resetAt, limited := grokRateLimitResetAt(snapshot, now)
	if !limited || !isGrokOAuthAccount(account) || snapshot == nil || snapshot.StatusCode != http.StatusTooManyRequests {
		return resetAt, limited
	}
	if account.RateLimitedAt == nil || account.RateLimitResetAt == nil {
		return resetAt, true
	}
	previousResetAt := *account.RateLimitResetAt
	if previousResetAt.After(now) || now.Sub(previousResetAt) > grokRateLimitBackoffQuietPeriod {
		return resetAt, true
	}
	previousCooldown := previousResetAt.Sub(*account.RateLimitedAt)
	if previousCooldown <= 0 {
		return resetAt, true
	}

	adaptiveCooldown := grokRateLimitRepeatCooldown
	switch {
	case previousCooldown >= grokRateLimitSustainedCooldown:
		adaptiveCooldown = grokRateLimitMaxAdaptiveCooldown
	case previousCooldown >= grokRateLimitRepeatCooldown:
		adaptiveCooldown = grokRateLimitSustainedCooldown
	}
	adaptiveResetAt := now.Add(adaptiveCooldown)
	if adaptiveResetAt.After(resetAt) {
		resetAt = adaptiveResetAt
	}
	return resetAt, true
}

func normalizeGrokRateLimitResetAt(account *Account, resetAt, now time.Time) time.Time {
	if !resetAt.After(now) {
		resetAt = now.Add(grokRateLimitFallbackCooldown)
	}
	if account != nil && account.RateLimitResetAt != nil && account.RateLimitResetAt.After(resetAt) {
		resetAt = *account.RateLimitResetAt
	}
	return resetAt
}

type grokRateLimitExtendingRepository interface {
	SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error
}

type grokRateLimitRecoveryRepository interface {
	ClearRateLimitIfObserved(ctx context.Context, id int64, observedLimitedAt, observedResetAt time.Time) (bool, error)
}

func isSuccessfulGrokRateLimitRecovery(account *Account, snapshot *xai.QuotaSnapshot) bool {
	return isGrokOAuthAccount(account) &&
		account.RateLimitedAt != nil &&
		account.RateLimitResetAt != nil &&
		snapshot != nil &&
		snapshot.StatusCode >= http.StatusOK &&
		snapshot.StatusCode < http.StatusMultipleChoices
}

func clearGrokRateLimitAfterRecovery(ctx context.Context, repo AccountRepository, account *Account) {
	if repo == nil || account == nil || account.RateLimitedAt == nil || account.RateLimitResetAt == nil || ctx.Err() != nil {
		return
	}
	recoveryRepo, ok := repo.(grokRateLimitRecoveryRepository)
	if !ok {
		return
	}
	_, err := recoveryRepo.ClearRateLimitIfObserved(ctx, account.ID, *account.RateLimitedAt, *account.RateLimitResetAt)
	if err != nil {
		slog.Warn("grok_rate_limit_recovery_clear_failed", "account_id", account.ID, "error", err)
	}
}

func persistGrokRateLimit(ctx context.Context, repo AccountRepository, account *Account, resetAt time.Time) {
	if repo == nil || account == nil || account.ID <= 0 {
		return
	}
	resetAt = normalizeGrokRateLimitResetAt(account, resetAt, time.Now())
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	var err error
	if extendingRepo, ok := repo.(grokRateLimitExtendingRepository); ok {
		err = extendingRepo.SetRateLimitedIfLater(stateCtx, account.ID, resetAt)
	} else {
		err = repo.SetRateLimited(stateCtx, account.ID, resetAt)
	}
	if err != nil {
		slog.Warn("persist_grok_rate_limit_failed", "account_id", account.ID, "reset_at", resetAt.UTC(), "error", err)
	}
}

func (s *OpenAIGatewayService) rateLimitGrok(ctx context.Context, account *Account, resetAt time.Time) {
	if s == nil || account == nil {
		return
	}
	now := time.Now()
	resetAt = normalizeGrokRateLimitResetAt(account, resetAt, now)

	runtimeUntil := resetAt
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(runtimeUntil) {
		runtimeUntil = *account.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, runtimeUntil, "429")
	persistGrokRateLimit(ctx, s.accountRepo, account, resetAt)

	// 扩散短期团队与模型冷却，使同一 xAI 团队的关联 OAuth 账号跳过热点模型，
	// 无需等待每个账号分别收到 429。模型优先取最新请求上下文；为空时
	// markGrokTeamModelRateLimit 不执行操作。
	if model, _ := ctx.Value(grokTeamRateLimitModelContextKey{}).(string); model != "" {
		markGrokTeamModelRateLimit(account, model, resolveGrokTeamRateLimitUntil(resetAt, now))
	}
}

// buildGrokSchedulerExtraUpdates 派生 EvaluateAccountSchedulingThreshold 使用的
// grok_sched_* 调度快照，包括利用率百分比与重置时间。
// 利用率取请求数与令牌数窗口中约束最强的一项。
func buildGrokSchedulerExtraUpdates(snapshot *xai.QuotaSnapshot) map[string]any {
	if snapshot == nil {
		return nil
	}
	util, reset, ok := grokSnapshotUtilization(snapshot)
	if !ok {
		return nil
	}
	updates := map[string]any{
		"grok_sched_utilization":      util,
		"grok_sched_usage_updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if reset != nil {
		// 防御：调度阈值暂停时长由 grok_sched_reset_at 决定。若上游返回脏的
		// reset 头（例如把相对毫秒 "6000" 误当相对秒解析出 ~33h 的未来时刻），
		// 不设上限会把耗尽账号长时间锁死。xAI 配额窗口不会超过一天，因此对
		// 未来时刻做 grokMaxSchedulingResetHorizon 钳制；过去/无效值直接不写。
		now := time.Now()
		if reset.After(now) {
			capped := *reset
			if horizon := now.Add(grokMaxSchedulingResetHorizon); capped.After(horizon) {
				capped = horizon
			}
			updates["grok_sched_reset_at"] = capped.UTC().Format(time.RFC3339)
		}
	}
	return updates
}

// grokSnapshotUtilization 返回请求数与令牌数额度窗口中的最高利用率（0 到 100），
// 以及该窗口的重置时间。
func grokSnapshotUtilization(snapshot *xai.QuotaSnapshot) (float64, *time.Time, bool) {
	if snapshot == nil {
		return 0, nil, false
	}
	best := -1.0
	var bestReset *time.Time
	consider := func(window *xai.QuotaWindow) {
		if window == nil || window.Limit == nil || *window.Limit <= 0 || window.Remaining == nil {
			return
		}
		remaining := *window.Remaining
		if remaining < 0 {
			remaining = 0
		}
		util := (1 - float64(remaining)/float64(*window.Limit)) * 100
		if util < 0 {
			util = 0
		}
		if util > 100 {
			util = 100
		}
		if util > best {
			best = util
			if window.ResetUnix != nil {
				t := time.Unix(*window.ResetUnix, 0).UTC()
				bestReset = &t
			} else {
				bestReset = nil
			}
		}
	}
	consider(snapshot.Requests)
	consider(snapshot.Tokens)
	if best < 0 {
		return 0, nil, false
	}
	return best, bestReset, true
}

// grokMaxSchedulingResetHorizon 限制 Grok 调度阈值暂停时间 grok_sched_reset_at 的最远范围，
// 防止异常上游重置响应头将超阈值账号暂停数天；xAI 额度窗口通常不超过一天。
const grokMaxSchedulingResetHorizon = 25 * time.Hour

// grokTeamRateLimitModelContextKey 在上下文中携带团队冷却所需的上游模型。
type grokTeamRateLimitModelContextKey struct{}

// withGrokTeamRateLimitModel 附加上游模型名，供团队与模型冷却等限流副作用使用。
// 模型为空时可安全调用。
func withGrokTeamRateLimitModel(ctx context.Context, model string) context.Context {
	model = strings.TrimSpace(model)
	if model == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, grokTeamRateLimitModelContextKey{}, model)
}

// grokRequestedModelFromCtx 读取当前请求实际使用的 Grok 上游模型。
func grokRequestedModelFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	model, _ := ctx.Value(grokTeamRateLimitModelContextKey{}).(string)
	return strings.TrimSpace(model)
}

// isGrokHeavyTransientModel 判断模型是否需要独立的瞬态冷却。
func isGrokHeavyTransientModel(requestedModel string) bool {
	model := strings.ToLower(strings.TrimSpace(xai.ResolveGrokTextResponsesModelID(requestedModel)))
	return strings.Contains(model, "multi-agent")
}

// persistGrokTransientModelCooldown 为高成本模型记录账号内的模型级瞬态冷却。
func persistGrokTransientModelCooldown(account *Account, decision GrokUpstreamFailureDecision) bool {
	if account == nil {
		return false
	}
	model := strings.TrimSpace(decision.Model)
	if model == "" || !isGrokHeavyTransientModel(model) {
		return false
	}
	cooldown := decision.Cooldown
	if cooldown <= 0 {
		cooldown = 3 * time.Minute
	}
	markGrokModelTransientBlock(account.ID, model, time.Now().Add(cooldown))
	return true
}

// applyGrokAccountUpstreamError 先处理精确模型状态和显式策略，再处理非池模式的 Grok 默认状态。
// 调用方应传入账号映射后的模型；这里仅补做幂等的平台规范化，绝不再次执行账号映射。
func (s *OpenAIGatewayService) applyGrokAccountUpstreamError(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	responseBody []byte,
	canonicalModel ...string,
) UpstreamErrorDecision {
	if s == nil || account == nil {
		return UpstreamErrorDecision{Policy: ErrorPolicyNone}
	}
	if isOpenAIAccountPolicyRequestScopedError(account, statusCode, responseBody) {
		return UpstreamErrorDecision{Policy: ErrorPolicyNone}
	}
	if model := firstRequestedModel(canonicalModel); model != "" {
		canonicalModel = []string{normalizeOpenAIModelForUpstream(account, model)}
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	stateCtx = withTempUnschedulableModel(stateCtx, canonicalModel)

	now := time.Now()
	quotaSnapshot := parseGrokQuotaSnapshot(headers, statusCode, now)
	quotaModel := firstRequestedModel(canonicalModel)
	if quotaModel == "" {
		quotaModel = grokRequestedModelFromCtx(ctx)
	}
	stampGrokQuotaSnapshotForPlan(account, quotaSnapshot, quotaModel)
	s.updateGrokUsageSnapshot(stateCtx, account, quotaSnapshot)

	decision := upstreamErrorDecisionWithoutPersistence(account, statusCode)
	if s.rateLimitService != nil {
		if account.IsPoolMode() || account.IsCustomErrorCodesEnabled() {
			decision.Policy = s.rateLimitService.ApplyExplicitErrorPolicy(stateCtx, account, statusCode, responseBody, canonicalModel...)
			decision.StopScheduling = decision.Policy == ErrorPolicyCustomMatched || decision.Policy == ErrorPolicyTempUnscheduled
		} else {
			decision = UpstreamErrorDecision{Policy: ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		return decision
	case ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case ErrorPolicyCustomSkipped, ErrorPolicyPoolBypassed:
		return decision
	}

	if s.rateLimitService != nil && len(canonicalModel) > 0 &&
		s.rateLimitService.HandleUpstreamModelNotFound(stateCtx, account, canonicalModel[0], statusCode, responseBody) {
		decision.StopScheduling = true
		return decision
	}
	// 普通账号先保留模型不存在等精确处理，再应用管理员临时规则。
	if s.rateLimitService != nil && statusCode != http.StatusUnauthorized &&
		!account.IsPoolMode() && !account.IsCustomErrorCodesEnabled() &&
		s.rateLimitService.HandleTempUnschedulable(stateCtx, account, statusCode, responseBody, canonicalModel...) {
		decision.Policy = ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if firstRequestedModel(canonicalModel) == "" {
			s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		}
		return decision
	}

	// Grok API Key 的 5xx 与 OpenAI API Key 共用账号+最终模型的瞬态冷却；
	// OAuth 和模型未知的请求继续沿用账号级退避，避免扩大既有行为变化。
	model := firstRequestedModel(canonicalModel)
	if model == "" {
		model = quotaModel
	}
	if account.Type == AccountTypeAPIKey && model != "" && statusCode >= 500 &&
		shouldCooldownOpenAITransientUpstreamError(statusCode, responseBody) {
		s.recordOpenAICompatibleModelTransientFailure(account, model)
		return decision
	}

	// 响应体中的免费额度、账单、空输出和容量语义优先于通用状态码处理。
	failure := classifyGrokUpstreamFailure(statusCode, responseBody, model)
	if failure.ShouldCooldown && failure.Class != GrokFailureNone && failure.Class != GrokFailureRateLimit {
		if failure.Class == GrokFailureFreeUsage {
			if resetAt, limited := grokRateLimitResetAtForAccount(account, quotaSnapshot, now); limited && resetAt.After(now) {
				if failure.Model != "" && isGrokModelSpecificFreeUsage(strings.ToLower(failure.Reason), failure.Model) {
					markGrokModelQuotaBlock(account.ID, failure.Model, resetAt)
					decision.StopScheduling = true
					return decision
				}
				s.rateLimitGrok(stateCtx, account, resetAt)
				decision.StopScheduling = true
				return decision
			}
		}
		if s.applyGrokUpstreamFailureDecision(stateCtx, account, failure) {
			decision.StopScheduling = true
			return decision
		}
	}

	switch statusCode {
	case http.StatusUnauthorized:
		s.tempUnscheduleGrok(stateCtx, account, 10*time.Minute, "grok credentials unauthorized")
		decision.StopScheduling = true
		return decision
	case http.StatusPaymentRequired:
		// 402 表示当前账号计费不可用，短期排除以避免后续请求反复命中。
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok payment required")
		decision.StopScheduling = true
		return decision
	case http.StatusForbidden:
		if s.applyGrokForbiddenPolicy(stateCtx, account, responseBody) {
			decision.StopScheduling = true
			return decision
		}
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok access or entitlement denied")
		decision.StopScheduling = true
		return decision
	case http.StatusMethodNotAllowed:
		// 当前账号不支持所选 Grok 端点，临时排除可避免粘性会话反复命中同一账号。
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok endpoint not supported (405)")
		decision.StopScheduling = true
		return decision
	case http.StatusTooManyRequests:
		// updateGrokUsageSnapshot 已同时写入运行时和持久化限流状态。
		decision.StopScheduling = true
		return decision
	default:
		if statusCode >= 500 {
			s.tempUnscheduleGrok(stateCtx, account, 2*time.Minute, "grok upstream temporary error")
			decision.StopScheduling = true
			return decision
		}
	}
	return decision
}

// isGrokSpendingLimitError 判断响应体是否表示 xAI 消费限额或余额耗尽。
func isGrokSpendingLimitError(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "code").String(),
		gjson.GetBytes(responseBody, "error.code").String(),
	)))
	if code == "personal-team-blocked:spending-limit" {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "error").String(),
		gjson.GetBytes(responseBody, "error.message").String(),
		gjson.GetBytes(responseBody, "message").String(),
	)))
	return strings.Contains(message, "spending limit") || strings.Contains(message, "run out of credits")
}

func (s *OpenAIGatewayService) tempUnscheduleGrok(ctx context.Context, account *Account, cooldown time.Duration, reason string) {
	if s == nil || account == nil {
		return
	}
	until := time.Now().Add(cooldown)
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(until) {
		until = *account.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, until, reason)
	if s.accountRepo != nil {
		stateCtx, cancel := openAIAccountStateContext(ctx)
		defer cancel()
		_ = s.accountRepo.SetTempUnschedulable(stateCtx, account.ID, until, reason)
	}
}
