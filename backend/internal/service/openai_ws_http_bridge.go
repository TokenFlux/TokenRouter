package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIWSClientReadLimitBytesDefault     int64 = 64 * 1024 * 1024
	openAIWSHTTPBridgeThresholdBytesDefault int64 = 15 * 1024 * 1024
	openAIWSHTTPBridgeErrorBodyLimitBytes         = 64 * 1024
)

const openAIWSHTTPBridgeToolStateContextKey = "openai_ws_http_bridge_tool_state"

// openAIWSHTTPBridgeToolState 保存 bridge 会话内可跨轮次复用的客户端工具降级状态。
type openAIWSHTTPBridgeToolState struct {
	ClientMapping bridge.ResponsesClientToolMapping
	LoweredTools  json.RawMessage
}

func openAIWSHTTPBridgeToolStateFromContext(c *gin.Context) (openAIWSHTTPBridgeToolState, bool) {
	if c == nil {
		return openAIWSHTTPBridgeToolState{}, false
	}
	value, ok := c.Get(openAIWSHTTPBridgeToolStateContextKey)
	state, typed := value.(openAIWSHTTPBridgeToolState)
	return state, ok && typed
}

func setOpenAIWSHTTPBridgeToolState(c *gin.Context, state openAIWSHTTPBridgeToolState) {
	if c == nil {
		return
	}
	state.LoweredTools = append(json.RawMessage(nil), state.LoweredTools...)
	c.Set(openAIWSHTTPBridgeToolStateContextKey, state)
}

func decodeOpenAIWSHTTPBridgeLoweredTools(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return nil
	}
	var tools []any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	return tools
}

func openAIWSHTTPBridgeRawField(body []byte, name string) (json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, false
	}
	raw, present := fields[name]
	return append(json.RawMessage(nil), raw...), present
}

func openAIWSHTTPBridgeToolUpstreamName(account *Account) string {
	if account != nil && account.Platform == capability.PlatformGrok {
		return "Grok WS HTTP bridge"
	}
	return "OpenAI WS HTTP bridge"
}

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
	if account != nil && account.Platform == capability.PlatformGrok {
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

// shouldBridgeOpenAIWSPassthroughFirstMessage 判断透传首帧是否应切换到 HTTP bridge。
func (s *OpenAIGatewayService) shouldBridgeOpenAIWSPassthroughFirstMessage(account *Account, payload []byte) bool {
	if account != nil && account.Platform == capability.PlatformGrok {
		return true
	}
	if !s.openAIWSHTTPBridgeEnabled() || int64(len(payload)) < s.openAIWSHTTPBridgeThresholdBytes() {
		return false
	}
	if !json.Valid(payload) {
		return false
	}

	i := skipOpenAIWSJSONSpace(payload, 0)
	if i >= len(payload) || payload[i] != '{' {
		return false
	}
	i++
	eventType := "response.create"
	previousResponseID := ""
	typeSeen, previousResponseIDSeen := false, false
	for {
		i = skipOpenAIWSJSONSpace(payload, i)
		if payload[i] == '}' {
			break
		}
		keyStart := i
		keyEnd := scanOpenAIWSJSONString(payload, keyStart)
		i = skipOpenAIWSJSONSpace(payload, keyEnd)
		i++ // json.Valid 已保证此处为冒号。
		i = skipOpenAIWSJSONSpace(payload, i)
		valueStart := i
		i = skipOpenAIWSJSONValue(payload, i)

		key := ""
		// 关键字段解码后最多约 20 字节；限制编码长度可避免分配攻击者构造的超长键。
		if keyEnd-keyStart <= 128 {
			_ = json.Unmarshal(payload[keyStart:keyEnd], &key)
		}
		switch key {
		case "type":
			if typeSeen {
				return false
			}
			typeSeen = true
			var value *string
			if err := json.Unmarshal(payload[valueStart:i], &value); err != nil {
				return false
			}
			if value == nil || strings.TrimSpace(*value) == "" {
				eventType = "response.create"
			} else {
				eventType = strings.TrimSpace(*value)
			}
		case "previous_response_id":
			if previousResponseIDSeen {
				return false
			}
			previousResponseIDSeen = true
			var value *string
			if err := json.Unmarshal(payload[valueStart:i], &value); err != nil {
				return false
			}
			if value != nil {
				previousResponseID = strings.TrimSpace(*value)
			}
		}
		i = skipOpenAIWSJSONSpace(payload, i)
		if payload[i] == ',' {
			i++
		}
	}
	return eventType == "response.create" && previousResponseID == ""
}

func skipOpenAIWSJSONSpace(payload []byte, i int) int {
	for i < len(payload) {
		switch payload[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func scanOpenAIWSJSONString(payload []byte, i int) int {
	for i++; i < len(payload); i++ {
		switch payload[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(payload)
}

func skipOpenAIWSJSONValue(payload []byte, i int) int {
	if payload[i] == '"' {
		return scanOpenAIWSJSONString(payload, i)
	}
	if payload[i] != '{' && payload[i] != '[' {
		for i < len(payload) && payload[i] != ',' && payload[i] != '}' {
			i++
		}
		return i
	}
	depth := 0
	for ; i < len(payload); i++ {
		switch payload[i] {
		case '"':
			i = scanOpenAIWSJSONString(payload, i) - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(payload)
}

// prepareOpenAIWSHTTPBridgeBody 将 response.create WS payload 转成 HTTP Responses body。
func prepareOpenAIWSHTTPBridgeBody(account *Account, payload []byte) ([]byte, error) {
	var body map[string]any
	if err := wirejson.DecodeUseNumber(payload, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("response.create payload must be a JSON object")
	}
	delete(body, "type")
	delete(body, "generate")
	delete(body, "previous_response_id")
	deleteOpenAIResponsesNoneReasoningEffortFromObject(account, body)
	body["stream"] = true
	return json.Marshal(body)
}

// openAIWSToolCallReplayCollector 收集上游输出里的工具调用上下文，供后续 bridge turn 重放。
type openAIWSToolCallReplayCollector struct {
	items    []json.RawMessage
	seen     map[string]struct{}
	allItems []json.RawMessage
	allSeen  map[string]struct{}
}

// AddEvent 从上游事件中提取可重放的 function_call 项。
func (c *openAIWSToolCallReplayCollector) AddEvent(eventType string, message []byte) {
	switch strings.TrimSpace(eventType) {
	case "response.output_item.done":
		item := gjson.GetBytes(message, "item")
		c.addAllItem(item)
		c.addItem(item)
	case "response.completed", "response.done":
		output := gjson.GetBytes(message, "response.output")
		if !output.IsArray() {
			return
		}
		for _, item := range output.Array() {
			c.addAllItem(item)
			c.addItem(item)
		}
	}
}

// Items 返回已收集工具调用上下文的拷贝。
// Items/AllItems 返回浅拷贝头数组；正文由 collector 独立分配且此后不可变，
// 调用方按 replay 所有权不变式共享持有。
func (c *openAIWSToolCallReplayCollector) Items() []json.RawMessage {
	return slices.Clone(c.items)
}

// AllItems 返回完整输出项，供账号切换时重建当前回合上下文。
func (c *openAIWSToolCallReplayCollector) AllItems() []json.RawMessage {
	return slices.Clone(c.allItems)
}

func (c *openAIWSToolCallReplayCollector) addAllItem(item gjson.Result) {
	if !item.Exists() || item.Type != gjson.JSON {
		return
	}
	raw := strings.TrimSpace(item.Raw)
	if raw == "" || !strings.HasPrefix(raw, "{") || strings.TrimSpace(item.Get("type").String()) == "" {
		return
	}
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("call_id").String())
	}
	if key == "" {
		key = raw
	}
	if c.allSeen == nil {
		c.allSeen = make(map[string]struct{})
	}
	if _, ok := c.allSeen[key]; ok {
		return
	}
	c.allSeen[key] = struct{}{}
	c.allItems = append(c.allItems, json.RawMessage(raw))
}

func (c *openAIWSToolCallReplayCollector) addItem(item gjson.Result) {
	if !item.Exists() || item.Type != gjson.JSON {
		return
	}
	raw := strings.TrimSpace(item.Raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return
	}
	if !openai.IsCodexToolCallContextItemType(item.Get("type").String()) {
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

// buildOpenAIWSHTTPBridgeFailedEvent 将没有正式 response.failed 事件的上游错误
// 封装成客户端可消费的 Responses 终止事件，并保留上游错误码和消息。
func buildOpenAIWSHTTPBridgeFailedEvent(responseID, model string, source []byte, fallbackMessage string) []byte {
	errorType := strings.TrimSpace(gjson.GetBytes(source, "error.type").String())
	if errorType == "" {
		errorType = strings.TrimSpace(gjson.GetBytes(source, "response.error.type").String())
	}
	code := strings.TrimSpace(gjson.GetBytes(source, "error.code").String())
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(source, "response.error.code").String())
	}
	if code == "" {
		code = "upstream_error"
	}
	message := upstreamopenai.ExtractOpenAISSEErrorMessage(source)
	if message == "" {
		message = strings.TrimSpace(fallbackMessage)
	}
	if message == "" {
		message = "Upstream response failed"
	}
	errorBody := map[string]any{"code": code, "message": message}
	if errorType != "" {
		errorBody["type"] = errorType
	}
	response := map[string]any{
		"id": responseID, "object": "response", "status": "failed",
		"output": []any{}, "error": errorBody,
	}
	if model = strings.TrimSpace(model); model != "" {
		response["model"] = model
	}
	body, err := json.Marshal(map[string]any{"type": "response.failed", "response": response})
	if err != nil {
		return []byte(`{"type":"response.failed","response":{"status":"failed","output":[],"error":{"code":"upstream_error","message":"Upstream response failed"}}}`)
	}
	return body
}

// detectOpenAIWSHTTPBridgeRequestScopedError 识别只与当前请求有关的错误。
// 这类错误既不修改账号状态，也不能因为池模式配置而回放当前 turn。
func detectOpenAIWSHTTPBridgeRequestScopedError(account *Account, statusCode int, message string, body []byte) bool {
	if hit, _, _ := upstreamopenai.DetectOpenAICyberPolicy(body); hit {
		return true
	}
	if gatewayprovider.IsOpenAICyberWarningPayload(body, message) ||
		upstreamopenai.IsOpenAIClientInvalidRequestError(statusCode, message, body) ||
		upstreamopenai.IsOpenAIContextWindowError(message, body) {
		return true
	}
	return account != nil && account.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(statusCode, body)
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
	args ...any,
) (*forwardcore.OpenAIResult, error) {
	responseModelObserver := gatewayhttp.UpstreamResponseModelObserverFromContext(c)
	if responseModelObserver == nil {
		responseModelObserver = gatewayhttp.BeginUpstreamResponseModelObservation(c)
	}
	var routingModel, imageBillingModel, imageSizeTier, imageInputSize, grokCacheIdentity string
	var turn int
	var writeClientMessage func([]byte) error
	var routerMatch []egress.TLSFingerprintRouterMatchResult
	var stringArgs []string
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			stringArgs = append(stringArgs, value)
		case int:
			turn = value
		case func([]byte) error:
			writeClientMessage = value
		case egress.TLSFingerprintRouterMatchResult:
			routerMatch = append(routerMatch, value)
		case []egress.TLSFingerprintRouterMatchResult:
			routerMatch = append(routerMatch, value...)
		}
	}
	if len(stringArgs) >= 5 {
		routingModel, imageBillingModel, imageSizeTier, imageInputSize, grokCacheIdentity = stringArgs[0], stringArgs[1], stringArgs[2], stringArgs[3], stringArgs[4]
	} else if len(stringArgs) == 4 {
		imageBillingModel, imageSizeTier, imageInputSize, grokCacheIdentity = stringArgs[0], stringArgs[1], stringArgs[2], stringArgs[3]
	} else {
		return nil, errors.New("invalid websocket HTTP bridge arguments")
	}
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

	body, err := prepareOpenAIWSHTTPBridgeBody(account, payload)
	if err != nil {
		return nil, fmt.Errorf("prepare http bridge body: %w", err)
	}
	grokIntentSourceBody := append([]byte(nil), body...)
	_, grokExplicitToolsField := openAIWSHTTPBridgeRawField(grokIntentSourceBody, "tools")
	grokExplicitToolIntent := account.Platform == capability.PlatformGrok && grok.HasGrokResponsesToolIntent(grokIntentSourceBody)
	var clientToolMapping bridge.ResponsesClientToolMapping
	functionToolUpstream := (account.Platform == capability.PlatformOpenAI && account.Type == capability.AccountTypeAPIKey) || account.Platform == capability.PlatformGrok
	if functionToolUpstream {
		if account.Platform == capability.PlatformGrok {
			body, err = sanitizeGrokResponsesInput(body)
			if err != nil {
				return nil, fmt.Errorf("sanitize Grok WS HTTP bridge input: %w", err)
			}
		}
		inheritedState, _ := openAIWSHTTPBridgeToolStateFromContext(c)
		inheritedLoweredTools := decodeOpenAIWSHTTPBridgeLoweredTools(inheritedState.LoweredTools)
		body, clientToolMapping, err = bridge.AdaptResponsesClientToolsJSONWithMapping(
			body,
			openAIWSHTTPBridgeToolUpstreamName(account),
			inheritedState.ClientMapping,
			inheritedLoweredTools,
		)
		if err != nil {
			return nil, fmt.Errorf("adapt %s client tools: %w", openAIWSHTTPBridgeToolUpstreamName(account), err)
		}
		if account.Platform == capability.PlatformGrok && !grokExplicitToolsField && !grokExplicitToolIntent && len(inheritedLoweredTools) > 0 && grok.HasGrokResponsesToolIntent(body) {
			// 本轮省略 tools 时，缓存路由也必须看到继承后的有效声明，
			// 否则会把客户端函数误判为无工具请求。
			grokIntentSourceBody = append(grokIntentSourceBody[:0], body...)
		}
		loweredTools := inheritedState.LoweredTools
		if currentTools, present := openAIWSHTTPBridgeRawField(body, "tools"); present {
			loweredTools = currentTools
		}
		setOpenAIWSHTTPBridgeToolState(c, openAIWSHTTPBridgeToolState{
			ClientMapping: clientToolMapping,
			LoweredTools:  loweredTools,
		})
	}
	responsesLite := account.Platform == capability.PlatformOpenAI && isOpenAIResponsesLiteWebSocketPayload(payload)
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
	if account.Platform == capability.PlatformGrok {
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

	if account.Platform == capability.PlatformGrok {
		upstreamModel := resolveGrokWSUpstreamModel(account, body, originalModel)
		body, err = patchGrokResponsesBody(body, upstreamModel)
		if err != nil {
			return nil, err
		}
		grokMixedCacheIntentBody := append([]byte(nil), body...)
		body, err = grok.ApplyGrokResponsesCacheIdentity(body, grokIntentSourceBody, grokCacheIdentity, account.IsGrokOAuth())
		if err != nil {
			return nil, fmt.Errorf("apply grok prompt cache identity: %w", err)
		}
		body, err = applyGrokFreeRequestToolCacheRoute(c, body, grokMixedCacheIntentBody, account, grokCacheIdentity)
		if err != nil {
			return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", err)
		}
	}
	actualModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if actualModel == "" {
		actualModel = canonicalOpenAIAccountSchedulingModel(account, originalModel)
	}
	if actualModel != "" {
		mappedModel = actualModel
	}
	gatewayhttp.SetOpsUpstreamModel(c, actualModel)
	needModelReplace = originalModel != "" && mappedModel != "" && mappedModel != originalModel
	if needModelReplace {
		mappedModelBytes = []byte(mappedModel)
	}

	buildUpstreamRequest := func(requestBody []byte) (*http.Request, error) {
		upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
		defer releaseUpstreamCtx()
		var upstreamReq *http.Request
		var buildErr error
		if account.Platform == capability.PlatformGrok {
			upstreamReq, buildErr = buildGrokResponsesRequest(upstreamCtx, c, account, requestBody, token, grokCacheIdentity, s.cfg, s.settingService)
		} else {
			upstreamReq, buildErr = s.buildUpstreamRequestOpenAIPassthrough(upstreamCtx, c, account, requestBody, token, routerMatch...)
		}
		if buildErr != nil {
			return nil, buildErr
		}
		if account.Platform != capability.PlatformGrok && responsesLite {
			upstreamReq.Header.Set(media.ResponsesLiteHeader, "true")
		}
		return upstreamReq, nil
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
	rejectedFieldRetryState := upstreamopenai.NewOpenAIResponsesRejectedFieldRetryState(body)
	var resp *http.Response
	for {
		upstreamReq, buildErr := buildUpstreamRequest(body)
		if buildErr != nil {
			return nil, buildErr
		}
		resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, routerMatch...))
		if err != nil {
			if turn == 1 {
				return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
			}
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			clientError := buildOpenAIWSHTTPBridgeErrorEvent(http.StatusBadGateway, "Upstream request failed")
			if writeErr := writeClientMessage(clientError); writeErr == nil {
				markOpenAIWSClientVisibleFailure(c, "error", clientError)
			}
			return nil, fmt.Errorf("upstream http bridge request failed: %s", safeErr)
		}
		if resp.StatusCode < 400 {
			break
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, openAIWSHTTPBridgeErrorBodyLimitBytes))
		_ = resp.Body.Close()
		markOpenAICyberPolicyEvent(c, respBody, resp.StatusCode, nil)
		if resp.StatusCode == http.StatusBadRequest &&
			upstream.ExtractErrorCode(respBody) == openAIWSFallbackReasonInvalidEncryptedContent {
			s.markOpenAIWSInvalidEncryptedContentLineageFromPayload(
				c, body, "ingress_ws_http_bridge_invalid_encrypted_lineage_mark", account.ID, turn,
			)
		}
		retryBody, retryReason, changed, retryErr := upstreamopenai.NormalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, respBody)
		if retryErr != nil {
			return nil, fmt.Errorf("normalize websocket http bridge rejected field retry: %w", retryErr)
		}
		if changed && rejectedFieldRetryState.Allow(retryBody) {
			gatewayprovider.LogOpenAIWSModeInfo(
				"ingress_ws_http_bridge_rejected_field_retry account_id=%d turn=%d reason=%s",
				account.ID,
				turn, gatewayprovider.TruncateOpenAIWSLogValue(retryReason, gatewayprovider.OpenAIWSLogValueMaxLen),
			)
			body = retryBody
			payloadBytes = len(body)
			continue
		}

		upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
		if upstreamMsg == "" {
			upstreamMsg = http.StatusText(resp.StatusCode)
		}
		requestScopedError := detectOpenAIWSHTTPBridgeRequestScopedError(account, resp.StatusCode, upstreamMsg, respBody)
		decision := UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
		defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody)
		if account.Platform == capability.PlatformGrok {
			defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
		}
		if !requestScopedError {
			if account.Platform == capability.PlatformGrok {
				decision = s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			} else {
				decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			}
		}
		if decision.ShouldReturnGenericError() {
			_ = writeClientMessage(buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error"))
			return nil, fmt.Errorf("upstream http bridge error: status=%d (not in custom error codes)", resp.StatusCode)
		}
		if !requestScopedError && decision.ShouldFailover(account, resp.StatusCode, defaultFailover) &&
			(turn == 1 || resp.StatusCode == http.StatusTooManyRequests) {
			return nil, newOpenAIUpstreamFailoverError(
				resp.StatusCode,
				resp.Header,
				respBody,
				upstreamMsg,
				decision.RetryableOnSameAccount(account, resp.StatusCode),
			)
		}
		clientError := buildOpenAIWSHTTPBridgeErrorEvent(resp.StatusCode, upstreamMsg)
		if writeErr := writeClientMessage(clientError); writeErr == nil {
			markOpenAIWSClientVisibleFailure(c, "error", clientError)
		}
		return nil, fmt.Errorf("upstream http bridge error: status=%d message=%s", resp.StatusCode, upstreamMsg)
	}
	defer func() { _ = resp.Body.Close() }()
	stopCancelBody := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
	defer stopCancelBody()
	if account.Platform == capability.PlatformGrok {
		s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, resolveGrokWSUpstreamModel(account, body, originalModel)), account, resp.Header, resp.StatusCode)
	}

	responseID := ""
	usage := openai.ForwardUsage{}
	imageCounter := openai.NewOpenAIImageOutputCounter()
	var firstTokenMs *int
	reqStream := openai.WSPayloadBoolFromRaw(body, "stream", true)
	eventCount := 0
	tokenEventCount := 0
	terminalEventCount := 0
	replayCollector := &openAIWSToolCallReplayCollector{}
	firstEventType := ""
	lastEventType := ""
	upstreamTerminalEvent := ""
	sawDone := false
	wroteDownstream := false
	pendingClientMessages := make([][]byte, 0, 4)
	pendingClientMessageBytes := int64(0)
	capacityFailoverSuppressedLogged := false
	clientDisconnected := false
	officialOpenAIResponses := account != nil && account.Platform == capability.PlatformOpenAI
	bareErrorPending := false
	var bareErrorPayload []byte
	bareErrorMessage := ""
	failureAccountSideEffectsApplied := false
	resultWithUsage := func() *forwardcore.OpenAIResult {
		imageCount := imageCounter.Count()
		result := &forwardcore.OpenAIResult{
			RequestID:                   responseID,
			ResponseID:                  responseID,
			Usage:                       usage,
			Model:                       originalModel,
			BillingModel:                billingModel,
			UpstreamModel:               mappedModel,
			UpstreamResponseServiceTier: responseModelObserver.ServiceTier(),
			ServiceTier:                 gatewayhttp.ResolvedOpenAIUpstreamServiceTierFromObserver(responseModelObserver, extractOpenAIServiceTierFromBody(body)),
			ReasoningEffort:             ApplyThinkingEnabledFallback(extractOpenAIReasoningEffortFromBody(body, mappedModel, originalModel), body, mappedModel),
			RequestedReasoningEffort:    CanonicalRequestedReasoningEffort(body, originalModel, mappedModel),
			Stream:                      reqStream,
			OpenAIWSMode:                true,
			UpstreamTerminalEvent:       upstreamTerminalEvent,
			ResponseHeaders:             upstream.CloneHeader(resp.Header),
			Duration:                    time.Since(turnStart),
			FirstTokenMs:                firstTokenMs,
		}
		if replayInput := replayCollector.Items(); len(replayInput) > 0 {
			result.SetWSReplayInput(replayInput, true)
		}
		result.SetWSAccountFailoverReplayInput(replayCollector.AllItems())
		if imageCount > 0 {
			result.ImageCount = imageCount
			result.ImageSize = imageSizeTier
			result.ImageInputSize = imageInputSize
			result.ImageOutputSizes = imageCounter.Sizes()
			result.BillingModel = imageBillingModel
		}
		return result
	}

	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	if hasOpenAIResponsesClientToolMapping(clientToolMapping) {
		resp.Body = upstream.NewResponsesClientToolStreamBody(resp.Body, clientToolMapping, maxLineSize)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanBuf := httpclient.GetSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	defer httpclient.PutSSEScannerBuf64K(scanBuf)

	pendingSSEEventType := ""
	finalizeBareError := func() error {
		if !bareErrorPending {
			return nil
		}
		if !failureAccountSideEffectsApplied {
			failureAccountSideEffectsApplied = s.handleOpenAIWSFailureAccountSideEffects(ctx, account, mappedModel, resp.Header, bareErrorPayload)
		}
		upstreamTerminalEvent = "response.failed"
		if clientDisconnected {
			return nil
		}
		clientMessage := buildOpenAIWSHTTPBridgeFailedEvent(responseID, originalModel, bareErrorPayload, bareErrorMessage)
		if rewritten, changed := upstreamopenai.SanitizeOpenAICapacityShedErrorCodeForClient(clientMessage); changed {
			clientMessage = rewritten
		}
		messages := append(pendingClientMessages, clientMessage)
		pendingClientMessages = nil
		pendingClientMessageBytes = 0
		for _, message := range messages {
			if err := writeClientMessage(message); err != nil {
				if gatewayprovider.IsOpenAIWSClientDisconnectError(err) {
					clientDisconnected = true
					return nil
				}
				return fmt.Errorf("write synthesized websocket response.failed: %w", err)
			}
			wroteDownstream = true
		}
		markOpenAIWSClientVisibleFailure(c, "response.failed", clientMessage)
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if eventType, ok := openai.ExtractSSEEventLine(line); ok {
			pendingSSEEventType = eventType
			continue
		}
		if strings.TrimSpace(line) == "" {
			pendingSSEEventType = ""
			continue
		}
		data, ok := openai.ExtractSSEDataLine(line)
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

		upstreamMessage := []byte(openai.OpenAICompatPayloadWithEventType(trimmedData, pendingSSEEventType))
		if normalized, changed := openai.NormalizeCompletedImageGenerationStatus(upstreamMessage); changed {
			upstreamMessage = normalized
		}
		eventType, eventResponseID, _ := openai.ParseWSEventEnvelope(upstreamMessage)
		responseModelObserver.ObserveOpenAI(upstreamMessage, eventType)
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
		if openai.IsWSTokenEvent(eventType) {
			tokenEventCount++
			if firstTokenMs == nil {
				ms := int(time.Since(turnStart).Milliseconds())
				firstTokenMs = &ms
			}
		}
		if openai.WSMessageShouldParseUsage(eventType, upstreamMessage) {
			openai.ParseWSResponseUsageFromCompletedEvent(upstreamMessage, &usage)
		}
		if eventType == "error" || eventType == "response.failed" {
			markOpenAICyberPolicyEvent(c, upstreamMessage, http.StatusOK, &usage)
		}
		imageCounter.AddSSEData(upstreamMessage)

		if needModelReplace && len(mappedModelBytes) > 0 && openai.WSEventMayContainModel(eventType) && strings.Contains(trimmedData, mappedModel) {
			upstreamMessage = openai.ReplaceWSMessageModel(upstreamMessage, mappedModel, originalModel)
		}
		if s.toolCorrector != nil && openai.WSEventMayContainToolCalls(eventType) && openai.WSMessageLikelyContainsToolCalls(upstreamMessage) {
			if corrected, changed := s.toolCorrector.CorrectToolCallsInSSEBytes(upstreamMessage); changed {
				upstreamMessage = corrected
			}
		}
		replayCollector.AddEvent(eventType, upstreamMessage)

		var upstreamEventErr error
		if officialOpenAIResponses && bareErrorPending && (eventType == "response.completed" || eventType == "response.done") {
			// 成功终态优先于此前可恢复的裸错误，避免合成失败并保留旧副作用。
			bareErrorPending = false
			bareErrorPayload = nil
			bareErrorMessage = ""
		}
		suppressClientMessage := bareErrorPending && eventType != "response.failed"
		requestScopedCapacity := account.Platform == capability.PlatformOpenAI &&
			(eventType == "error" || eventType == "response.failed") &&
			upstreamopenai.IsOpenAIUpstreamCapacityShedEvent(upstreamMessage)
		terminalPolicy := openAIWSTerminalPolicyDecision{
			TerminalEvent: normalizeOpenAIWSTerminalEvent(eventType),
			Decision:      UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone},
		}
		if openai.IsWSTerminalEvent(eventType) && !requestScopedCapacity &&
			(eventType != "response.failed" || !failureAccountSideEffectsApplied) {
			terminalPolicy = s.handleOpenAIWSTerminalTransientFailure(
				ctx,
				account,
				mappedModel,
				resp.Header,
				upstreamMessage,
			)
		}
		if eventType == "response.failed" {
			errMessage := upstreamopenai.ExtractOpenAISSEErrorMessage(upstreamMessage)
			if errMessage == "" {
				errMessage = "upstream error event"
			}
			shouldFailover := requestScopedCapacity
			if terminalPolicy.Decision.ShouldReturnGenericError() && !requestScopedCapacity {
				upstreamMessage = buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
				upstreamEventErr = errors.New("upstream response failed with status not in custom error codes")
			} else if !requestScopedCapacity {
				shouldFailover = terminalPolicy.Decision.ShouldFailoverWithDefaults(
					account,
					terminalPolicy.StatusCode,
					false,
					s.shouldFailoverOpenAIWSError(account, terminalPolicy.StatusCode, upstreamMessage),
				)
			}
			statusCode := terminalPolicy.StatusCode
			if statusCode == 0 {
				statusCode = http.StatusServiceUnavailable
			}
			if !wroteDownstream && shouldFailover &&
				(turn == 1 || statusCode == http.StatusTooManyRequests) {
				retrySame := requestScopedCapacity || terminalPolicy.Decision.RetryableOnSameAccount(account, statusCode)
				if !requestScopedCapacity {
					// 终止事件策略已在上方执行；交给错误构造器消费一次性状态，避免重复写入模型限流。
					markOpenAIWSFailureSideEffectsApplied(c, statusCode, terminalPolicy.Decision.StopScheduling)
				}
				return nil, s.newOpenAIStreamPolicyFailoverErrorWithModel(c, account, true, resp.Header.Get("x-request-id"), resp.Header, statusCode, upstreamMessage, errMessage, retrySame, mappedModel)
			}
			if wroteDownstream && requestScopedCapacity && !capacityFailoverSuppressedLogged {
				logOpenAICapacityFailoverSuppressed(ctx, account, "ws_http_bridge", resp.Header.Get("x-request-id"), eventType)
				capacityFailoverSuppressedLogged = true
			}
		}
		if eventType == "error" {
			errCodeRaw, errTypeRaw, _ := openai.ParseWSErrorEventFields(upstreamMessage)
			if reason, _ := upstreamopenai.ClassifyWSErrorEventFromRaw(errCodeRaw, errTypeRaw, upstreamopenai.ExtractOpenAISSEErrorMessage(upstreamMessage)); reason == openAIWSFallbackReasonInvalidEncryptedContent {
				s.markOpenAIWSInvalidEncryptedContentLineageFromPayload(
					c, body, "ingress_ws_http_bridge_invalid_encrypted_lineage_mark", account.ID, turn,
				)
			}
			_, _, errMsgRaw := openai.ParseWSErrorEventFields(upstreamMessage)
			errMessage := strings.TrimSpace(errMsgRaw)
			if errMessage == "" {
				errMessage = "upstream error event"
			}
			statusCode := openAIWSErrorPolicyStatus(upstreamMessage)
			policyStatus := statusCode
			requestScopedError := detectOpenAIWSHTTPBridgeRequestScopedError(account, statusCode, errMessage, upstreamMessage)
			decision := UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
			defaultFailover := s.shouldFailoverOpenAIWSError(account, policyStatus, upstreamMessage)
			if requestScopedCapacity {
				requestScopedError = true
				defaultFailover = true
			} else if account.Platform == capability.PlatformGrok {
				// SSE 错误事件不携带 HTTP 状态码，本地映射会把未知 xAI 错误码
				//（例如 new_sensitive）默认映射为 502；应用基于状态码的故障转移或
				// 账号状态变更前，先按请求级 403 内容拒绝检查响应体。
				if grok.IsGrokContentPolicyRejection(http.StatusForbidden, upstreamMessage) {
					requestScopedError = true
					defaultFailover = false
				} else {
					defaultFailover = s.shouldFailoverGrokUpstreamError(statusCode, upstreamMessage)
					decision = s.applyGrokAccountUpstreamError(ctx, account, statusCode, resp.Header, upstreamMessage, mappedModel)
				}
			} else if !requestScopedError {
				defaultFailover = s.shouldFailoverOpenAIWSError(account, policyStatus, upstreamMessage)
				semanticHeaders := resp.Header
				if policyStatus == http.StatusTooManyRequests {
					semanticHeaders = openAIWSSemantic429Headers(account, mappedModel, semanticHeaders)
				}
				decision = s.applyOpenAIAccountUpstreamError(ctx, account, policyStatus, semanticHeaders, upstreamMessage, mappedModel)
			}
			if decision.StopScheduling {
				failureAccountSideEffectsApplied = true
			}
			if !requestScopedError && account.Platform == capability.PlatformOpenAI &&
				(policyStatus == http.StatusUnauthorized || policyStatus == http.StatusTooManyRequests || policyStatus == 529 ||
					(policyStatus == http.StatusForbidden && upstreamopenai.OpenAIStream403AccountFailure(upstreamMessage, errMessage))) {
				// error 与 response.failed 可能成对出现；前者已经执行账号副作用时，
				// 后者只负责客户端事件，不得再次写入限流状态。
				failureAccountSideEffectsApplied = true
			}
			if decision.ShouldReturnGenericError() && !requestScopedCapacity {
				upstreamMessage = buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
				upstreamEventErr = errors.New("upstream error not in custom error codes")
			} else if !wroteDownstream && (requestScopedCapacity || (!requestScopedError && decision.ShouldFailover(account, policyStatus, defaultFailover))) &&
				(turn == 1 || policyStatus == http.StatusTooManyRequests) {
				retrySame := requestScopedCapacity || decision.RetryableOnSameAccount(account, policyStatus)
				if c != nil && !requestScopedCapacity && !requestScopedError {
					c.Set(openAIWSFailureSideEffectsStateKey, openAIWSFailureSideEffectsState{
						StatusCode:    policyStatus,
						ShouldDisable: decision.StopScheduling,
					})
				}
				return nil, s.newOpenAIStreamPolicyFailoverErrorWithModel(c, account, true, resp.Header.Get("x-request-id"), resp.Header, policyStatus, upstreamMessage, errMessage, retrySame, mappedModel)
			}
			if wroteDownstream && requestScopedCapacity && !capacityFailoverSuppressedLogged {
				logOpenAICapacityFailoverSuppressed(ctx, account, "ws_http_bridge", resp.Header.Get("x-request-id"), eventType)
				capacityFailoverSuppressedLogged = true
			}
			if account.Platform == capability.PlatformGrok {
				upstreamEventErr = errors.New(errMessage)
			} else {
				bareErrorPending = true
				bareErrorPayload = append(bareErrorPayload[:0], upstreamMessage...)
				bareErrorMessage = errMessage
				suppressClientMessage = true
			}
		}
		if eventType == "response.failed" {
			bareErrorPending = false
		}

		// 客户端写出副本改写容量降载码：Codex 对 error/response.failed 中的
		// server_is_overloaded / slow_down 判致命并终止会话，改写后走客户端内置
		// 重试。账号状态与终止事件判定（下方 handleOpenAIWSTerminalTransientFailure）
		// 仍使用未改写的 upstreamMessage。
		clientMessage := upstreamMessage
		if eventType == "error" || eventType == "response.failed" {
			if rewritten, changed := upstreamopenai.SanitizeOpenAICapacityShedErrorCodeForClient(clientMessage); changed {
				clientMessage = rewritten
			}
		}
		if !clientDisconnected && !suppressClientMessage {
			stageBeforeSemanticOutput := turn == 1 && account.Platform == capability.PlatformOpenAI && !wroteDownstream
			commitStagedMessages := !stageBeforeSemanticOutput ||
				upstreamopenai.OpenAIStreamDataStartsClientOutput(string(clientMessage), eventType) || openai.IsWSTerminalEvent(eventType)
			if stageBeforeSemanticOutput && !commitStagedMessages {
				if pendingClientMessageBytes+int64(len(clientMessage)) > upstreamopenai.OpenAIFirstOutputStageMaxBytes {
					return nil, s.newOpenAIStreamPolicyFailoverError(
						c,
						account,
						true,
						resp.Header.Get("x-request-id"),
						resp.Header,
						http.StatusBadGateway,
						nil,
						"OpenAI WS HTTP bridge first-output staging limit exceeded",
						false,
					)
				}
				pendingClientMessages = append(pendingClientMessages, append([]byte(nil), clientMessage...))
				pendingClientMessageBytes += int64(len(clientMessage))
			} else {
				messages := append(pendingClientMessages, clientMessage)
				pendingClientMessages = nil
				pendingClientMessageBytes = 0
				for _, message := range messages {
					if err := writeClientMessage(message); err != nil {
						if gatewayprovider.IsOpenAIWSClientDisconnectError(err) {
							clientDisconnected = true
							closeStatus, closeReason := gatewayprovider.SummarizeOpenAIWSReadCloseError(err)
							gatewayprovider.LogOpenAIWSModeInfo(
								"ingress_ws_http_bridge_client_disconnected_drain account_id=%d turn=%d close_status=%s close_reason=%s",
								account.ID,
								turn,
								closeStatus, gatewayprovider.TruncateOpenAIWSLogValue(closeReason, gatewayprovider.OpenAIWSHeaderValueMaxLen),
							)
							break
						}
						return nil, ws.WrapIngressTurnError(
							"write_client",
							fmt.Errorf("write client websocket event: %w", err),
							wroteDownstream,
						)
					}
					wroteDownstream = true
				}
			}
		}
		if !clientDisconnected && !suppressClientMessage {
			markOpenAIWSClientVisibleFailure(c, eventType, upstreamMessage)
		}

		if upstreamEventErr != nil {
			return resultWithUsage(), upstreamEventErr
		}
		if openai.IsWSTerminalEvent(eventType) && !bareErrorPending {
			upstreamTerminalEvent = terminalPolicy.TerminalEvent
			terminalEventCount++
			firstTokenMsValue := -1
			if firstTokenMs != nil {
				firstTokenMsValue = *firstTokenMs
			}
			gatewayprovider.LogOpenAIWSModeInfo(
				"ingress_ws_http_bridge_turn_completed account_id=%d turn=%d response_id=%s payload_bytes=%d duration_ms=%d events=%d token_events=%d terminal_events=%d first_event=%s last_event=%s first_token_ms=%d client_disconnected=%v",
				account.ID,
				turn, gatewayprovider.TruncateOpenAIWSLogValue(responseID, gatewayprovider.OpenAIWSIDValueMaxLen), payloadBytes,
				time.Since(turnStart).Milliseconds(),
				eventCount,
				tokenEventCount,
				terminalEventCount, gatewayprovider.TruncateOpenAIWSLogValue(firstEventType, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(lastEventType, gatewayprovider.OpenAIWSLogValueMaxLen), firstTokenMsValue,
				clientDisconnected,
			)
			return resultWithUsage(), nil
		}
	}
	if bareErrorPending {
		if finalizeErr := finalizeBareError(); finalizeErr != nil {
			return resultWithUsage(), finalizeErr
		}
		if scanErr := scanner.Err(); scanErr != nil {
			return resultWithUsage(), fmt.Errorf("read upstream http bridge stream after error event: %w", scanErr)
		}
		return resultWithUsage(), errors.New(bareErrorMessage)
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
	body, err := prepareOpenAIWSHTTPBridgeBody(account, payload)
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
		upstreamModel = grok.DefaultResponsesModel
	}
	return billingModel, upstreamModel
}
