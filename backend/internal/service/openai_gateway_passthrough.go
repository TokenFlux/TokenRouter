package service

import (
	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	// 本文件承载 /v1/responses 透传转发及其流式、非流式响应与错误处理。

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// 旧透传入口仅保持签名，当前账号的请求准备和恢复由目标执行器唯一实现。
func (s *OpenAIGatewayService) forwardOpenAIPassthrough(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body, canonicalImageIntentBody []byte, reqModel string, attemptImageIntentInvalidated bool, reasoningEffort *string, reqStream bool, startTime time.Time, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
	input := forward.PassthroughInput{Body: body, CanonicalImageIntentBody: canonicalImageIntentBody, Model: reqModel, ImageIntentInvalidated: attemptImageIntentInvalidated, ReasoningEffort: reasoningEffort, Stream: reqStream, StartedAt: startTime}
	p := &openAIPassthroughExecutionAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}}
	result, err := forward.RunPassthrough(ctx, input, p)
	return openAIForwardResultFromHTTP(result), err
}

func logOpenAIPassthroughInstructionsRejected(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
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
		accountID = account.Record.ID
		accountName = strings.TrimSpace(account.Record.Name)
		accountType = strings.TrimSpace(string(account.Record.Type))
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
	logging.FromContext(ctx).With(fields...).Warn("OpenAI passthrough 本地拦截：Codex 请求缺少有效 instructions")
}

func (s *OpenAIGatewayService) buildUpstreamRequestOpenAIPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	return forward.BuildPassthroughRequest(ctx, body, s.openAIRequestTarget(c, account, true), func(b []byte) []byte {
		return forward.NormalizeCNResponsesBody(account != nil && gatewayprovider.ExecutionProtocolTarget(account).UsesNativeCNResponses(), b)
	}, func(target string) openai.PassthroughRequestOptions {
		options := s.nativeResponsesRequestOptions(ctx, c, account, token, target, false, routerMatch...)
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
		return openai.PassthroughRequestOptions{
			ResponsesRequestOptions: options,
			AllowTimeoutHeaders:     s.isOpenAIPassthroughTimeoutHeadersAllowed,
			AllowPassthroughHeader:  isOpenAIPassthroughAllowedRequestHeader,
			MatchedOriginator: func() string {
				if len(routerMatch) > 0 && routerMatch[0].Matched {
					return strings.TrimSpace(routerMatch[0].UpstreamOriginator)
				}
				return ""
			},
		}
	})
}

// shouldFailoverOpenAIPassthroughResponse 只投影账号类别与平台错误分类。
func shouldFailoverOpenAIPassthroughResponse(account *gatewayprovider.ExecutionAccount, status int, body []byte) bool {
	return forward.ShouldFailoverPassthrough(status, body, forward.PassthroughFailureOptions{
		APIKey:        account != nil && account.Record.Type == capability.AccountTypeAPIKey,
		Cyber:         func(b []byte) bool { hit, _, _ := openai.DetectOpenAICyberPolicy(b); return hit },
		ContextWindow: func(b []byte) bool { return openai.IsOpenAIContextWindowError("", b) },
		AccessState:   func(s int, b []byte) bool { return isOpenAIHTTPUpstreamAccessStateError(s, "", b) },
		BodyTooLarge:  func(s int, b []byte) bool { return isOpenAIRequestBodyTooLargeError(s, "", b) },
		PoolRetryable: func(s int) bool {
			return account != nil && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(s)
		},
	})
}

// writeSanitizedOpenAIPassthroughError 委托 HTTP Adapter，保留旧调用入口。
func writeSanitizedOpenAIPassthroughError(c *gin.Context, upstreamStatus int, upstreamHeaders http.Header) {
	gatewayhttp.WriteSanitizedForwardPassthroughError(c, upstreamStatus, upstreamHeaders, func(c *gin.Context, status int, body []byte) bool {
		return gatewayhttp.WriteOpenAICompactSSEBridge(c, status, body, gatewayhttp.MarkOpsStreamError)
	})
}

// writeOpenAIPassthroughErrorEnvelope 委托 HTTP Adapter，保留旧调用入口。
func writeOpenAIPassthroughErrorEnvelope(c *gin.Context, downstreamStatus int, upstreamHeaders http.Header, message string) {
	gatewayhttp.WriteForwardPassthroughErrorEnvelope(c, downstreamStatus, upstreamHeaders, message, func(c *gin.Context, status int, body []byte) bool {
		return gatewayhttp.WriteOpenAICompactSSEBridge(c, status, body, gatewayhttp.MarkOpsStreamError)
	})
}

func (s *OpenAIGatewayService) handleFailoverErrorResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestBody []byte,
	responseBody []byte,
) error {
	body := s.agentIdentity.Redact(ctx, account, responseBody)

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)
	reqModel, _, _ := requeststate.OpenAIRequestMetaFromBody(requestBody)
	canonicalModel := gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(reqModel)
	decision := s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, canonicalModel)
	if decision.ShouldReturnGenericError() {
		gatewayhttp.MarkResponseCommitted(c)
		writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
		return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
	}
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:             account.Record.Platform,
		AccountID:            account.Record.ID,
		AccountName:          account.Record.Name,
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
		!shouldDisable && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(resp.StatusCode),
	)
}

func (s *OpenAIGatewayService) handleErrorResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestBody []byte,
	responseBody []byte,
) error {
	body := s.agentIdentity.Redact(ctx, account, responseBody)

	// cyber_policy 仍按原始 body 打内部标记，供 handler 事后写风控/邮件；面向客户端的
	// 错误体在下方统一重建。cyber 是上游网络安全策略拦截，不冷却账号，
	// 故下方跳过 handleOpenAIAccountUpstreamError（避免自定义 temp-unschedulable 规则误冷却）。
	cyberHit, cyberCode, cyberMsg := openai.DetectOpenAICyberPolicy(body)
	if cyberHit {
		gatewayhttp.MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           cyberCode,
			Message:        cyberMsg,
			Body:           logredact.TruncateUTF8(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
	}

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)
	clientInvalidRequest := openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body)
	requestScopedError := cyberHit || clientInvalidRequest || openai.IsOpenAIContextWindowError(upstreamMsg, body) ||
		isOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body)
	// 错误体虽不会原样透传，运行态账号状态仍需更新，避免粘性路由继续复用
	// 刚被限流的账号。请求级错误例外：不冷却账号，也不触发池模式重试。
	if !requestScopedError {
		reqModel, _, _ := requeststate.OpenAIRequestMetaFromBody(requestBody)
		canonicalModel := gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(reqModel)
		decision := s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, canonicalModel)
		if decision.ShouldReturnGenericError() {
			gatewayhttp.MarkResponseCommitted(c)
			writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
			return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, false, false) {
			return newOpenAIUpstreamFailoverError(
				resp.StatusCode,
				resp.Header,
				body,
				upstreamMsg,
				decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
			)
		}
	}
	gatewayhttp.MarkResponseCommitted(c)
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:             account.Record.Platform,
		AccountID:            account.Record.ID,
		AccountName:          account.Record.Name,
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
		gatewayhttp.WriteForwardPassthroughErrorHeaders(c.Writer.Header(), resp.Header)
		c.Data(http.StatusBadRequest, "application/json; charset=utf-8", body)
		return fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	// context-window 超限是确定性请求失败（shouldFailoverOpenAIPassthroughResponse
	// 已保证不切号），其文案对客户端可操作（如触发自动压缩）；在净化信封内保留
	// 脱敏后的上游消息，而不是抹成通用文案。
	if openai.IsOpenAIContextWindowError(upstreamMsg, body) && upstreamMsg != "" {
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
	usage            *protocolopenai.ForwardUsage
	firstTokenMs     *int
	responseID       string
	imageCount       int
	imageOutputSizes []string
}

type openaiNonStreamingResultPassthrough struct {
	*protocolopenai.ForwardUsage
	usage            *protocolopenai.ForwardUsage
	responseID       string
	imageCount       int
	imageOutputSizes []string
}

// openAITTFTMode 读取本实例网关设置，缺少依赖时使用原安全默认。
func (s *OpenAIGatewayService) openAITTFTMode(ctx context.Context) string {
	mode := gateway.OpenAITTFTModeSemantic
	if s != nil && s.settingService != nil {
		mode = s.settingService.Gateway.GetOpenAITTFTMode(ctx)
	}
	return gateway.NormalizeOpenAITTFTMode(mode)
}

func logOpenAICapacityFailoverSuppressed(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
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
			zap.Int64("account_id", account.Record.ID),
			zap.String("platform", account.Record.Platform),
		)
	}
	logging.FromContext(ctx).Warn("gateway.failover_suppressed_after_semantic_output", fields...)
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
		body, err := wirejson.Marshal(gin.H{
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
	body, err := wirejson.Marshal(gin.H{"error": errorPayload})
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
	upstreamStatus := openai.OpenAIStreamFailedEventSemanticStatus(payload, failedMessage)
	return gatewayhttp.ApplyErrorPassthroughRule(
		c,
		platform,
		upstreamStatus,
		ruleBody,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)
}

func (s *OpenAIGatewayService) handleOpenAIStreamTerminalAccountSideEffects(
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	payload []byte,
	message string,
	headers http.Header,
	canonicalModel ...string,
) (int, bool) {
	statusCode := openai.OpenAIStreamFailureStatus(payload, message)
	switch statusCode {
	case http.StatusForbidden:
		if !openai.OpenAIStream403AccountFailure(payload, message) {
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
		customMatched := account != nil && account.View().IsCustomErrorCodesEnabled() && account.View().ShouldHandleErrorCode(statusCode)
		poolRetryable := account != nil && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(statusCode)
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
	var account *gatewayprovider.ExecutionAccount
	var payload []byte
	var message string
	if len(args) == 3 {
		account, _ = args[0].(*gatewayprovider.ExecutionAccount)
		payload, _ = args[1].([]byte)
		message, _ = args[2].(string)
	} else if len(args) >= 5 {
		account, _ = args[1].(*gatewayprovider.ExecutionAccount)
		payload, _ = args[3].([]byte)
		message, _ = args[4].(string)
	}
	if account == nil {
		return false
	}
	// 容量降载由客户端身份或模型容量触发，与当前账号健康无关；非池账号也应先
	// 做有界同账号重试，避免无意义地轮换并冷却整组账号。
	if openai.IsOpenAIUpstreamCapacityShedEvent(payload) {
		return true
	}
	if !account.View().IsPoolMode() {
		return false
	}
	semanticStatus := openai.OpenAIStreamFailedEventSemanticStatus(payload, message)
	return account.View().IsPoolModeRetryableStatus(semanticStatus) ||
		openai.IsOpenAITransientProcessingError(http.StatusBadRequest, message, payload)
}

// applyOpenAIStreamFailedAccountPolicy 将 HTTP 200 流内的 response.failed
// 统一映射到现有账号策略管线，避免各协议入口重复推导状态码。
func (s *OpenAIGatewayService) applyOpenAIStreamFailedAccountPolicy(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	model string,
	headers http.Header,
	payload []byte,
	message string,
) (int, accountcore.UpstreamErrorDecision) {
	status := openai.OpenAIStreamFailedEventSemanticStatus(payload, message)
	if status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	return status, s.applyOpenAIAccountStreamRateLimitError(ctx, account, status, headers, payload, model)
}

func (s *OpenAIGatewayService) recordOpenAIStreamUpstreamError(
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	upstreamRequestID string,
	kind string,
	payload []byte,
	message string,
) string {
	message = logredact.SanitizeUpstreamQueries(strings.TrimSpace(message))
	if message == "" {
		message = "OpenAI upstream response failed"
	}
	statusCode := openai.OpenAIStreamFailureStatus(payload, message)
	detail := ""
	if len(payload) > 0 && s != nil && s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		detail = logredact.TruncateUTF8(string(payload), maxBytes)
	}
	if c != nil {
		gatewayhttp.SetOpsUpstreamError(c, statusCode, message, detail)
		event := ops.OpsUpstreamErrorEvent{
			Platform:           capability.PlatformOpenAI,
			UpstreamStatusCode: statusCode,
			UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
			Passthrough:        passthrough,
			Kind:               kind,
			Message:            message,
			Detail:             detail,
		}
		if account != nil {
			event.Platform = account.Record.Platform
			event.AccountID = account.Record.ID
			event.AccountName = account.Record.Name
		}
		gatewayhttp.AppendOpsUpstreamError(c, event)
	}
	return message
}

func (s *OpenAIGatewayService) newOpenAIStreamFailoverError(
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	upstreamRequestID string,
	payload []byte,
	message string,
	responseHeaders ...http.Header,
) *forwardcore.UpstreamFailoverError {
	return s.newOpenAIStreamFailoverErrorWithModel(c, account, passthrough, upstreamRequestID, payload, message, "", responseHeaders...)
}

func (s *OpenAIGatewayService) newOpenAIStreamFailoverErrorWithModel(
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	upstreamRequestID string,
	payload []byte,
	message string,
	canonicalModel string,
	responseHeaders ...http.Header,
) *forwardcore.UpstreamFailoverError {
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
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	upstreamRequestID string,
	responseHeaders http.Header,
	statusCode int,
	payload []byte,
	message string,
	_ bool,
) *forwardcore.UpstreamFailoverError {
	return s.newOpenAIStreamPolicyFailoverErrorWithModel(c, account, passthrough, upstreamRequestID, responseHeaders, statusCode, payload, message, false)
}

func (s *OpenAIGatewayService) newOpenAIStreamPolicyFailoverErrorWithModel(
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	upstreamRequestID string,
	responseHeaders http.Header,
	statusCode int,
	payload []byte,
	message string,
	_ bool,
	canonicalModel ...string,
) *forwardcore.UpstreamFailoverError {
	message = logredact.SanitizeUpstreamQueries(strings.TrimSpace(message))
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
		statusCode = openai.OpenAIStreamFailureStatus(payload, message)
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
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	terminalType string,
	payload []byte,
	message string,
	canonicalModel ...string,
) *forwardcore.UpstreamFailoverError {
	if account == nil || gatewayhttp.IsResponseCommitted(c) {
		return nil
	}
	shouldFailover := openai.OpenAIStreamFailedEventShouldFailover(payload, message)
	if terminalType == "error" {
		shouldFailover = openai.OpenAIStreamErrorEventShouldFailover(payload, message)
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
	account *gatewayprovider.ExecutionAccount,
	startTime time.Time,
	originalModel string,
	mappedModel string,
) (*openaiStreamingResultPassthrough, error) {
	result, err := openai.ReadPassthroughStreaming(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativePassthroughOptions(ctx, c, account), startTime, originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiStreamingResultPassthrough{usage: result.Usage, firstTokenMs: result.FirstTokenMs, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes}, err
}

func (s *OpenAIGatewayService) handleNonStreamingResponsePassthrough(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	originalModel string,
	mappedModel string,
) (*openaiNonStreamingResultPassthrough, error) {
	result, err := openai.ReadPassthroughNonStreaming(ctx, resp, gatewayhttp.ResponseSink{Writer: c.Writer}, s.nativePassthroughOptions(ctx, c, account), originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResultPassthrough{ForwardUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes}, err
}

func (s *OpenAIGatewayService) handlePassthroughSSEToJSON(resp *http.Response, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, originalModel string, mappedModel string) (*openaiNonStreamingResultPassthrough, error) {
	result, err := openai.ReadPassthroughSSEAsJSON(resp, gatewayhttp.ResponseSink{Writer: c.Writer}, s.nativePassthroughOptions(c.Request.Context(), c, account), body, originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResultPassthrough{ForwardUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes}, err
}

func writeOpenAIPassthroughResponseHeaders(dst http.Header, src http.Header, filter *egress.CompiledHeaderFilter) {
	if dst == nil || src == nil {
		return
	}
	if filter != nil {
		provider.WriteFilteredHeaders(dst, src, filter)
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
