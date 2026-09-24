package httpapi

import (
	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	// 本文件承载 /v1/responses 透传转发及其流式、非流式响应与错误处理。

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// writeSanitizedOpenAIPassthroughError 委托 HTTP Adapter，保留旧调用入口。
func writeSanitizedOpenAIPassthroughError(c *gin.Context, upstreamStatus int, upstreamHeaders http.Header) {
	WriteSanitizedForwardPassthroughError(c, upstreamStatus, upstreamHeaders, func(c *gin.Context, status int, body []byte) bool {
		return WriteOpenAICompactSSEBridge(c, status, body, MarkOpsStreamError)
	})
}

// writeOpenAIPassthroughErrorEnvelope 委托 HTTP Adapter，保留旧调用入口。
func writeOpenAIPassthroughErrorEnvelope(c *gin.Context, downstreamStatus int, upstreamHeaders http.Header, message string) {
	WriteForwardPassthroughErrorEnvelope(c, downstreamStatus, upstreamHeaders, message, func(c *gin.Context, status int, body []byte) bool {
		return WriteOpenAICompactSSEBridge(c, status, body, MarkOpsStreamError)
	})
}

func (p *OpenAIResponseOutput) PassthroughFailoverError(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestBody []byte,
	responseBody []byte,
) error {
	body := p.redact(ctx, account, responseBody)

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if p.Options.Configured && p.Options.LogUpstreamErrorBody {
		maxBytes := p.Options.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	LogOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)
	reqModel, _, _ := requeststate.OpenAIRequestMetaFromBody(requestBody)
	canonicalModel := gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(reqModel)
	decision := gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, account, resp.StatusCode, resp.Header, body, false, canonicalModel)
	if decision.ShouldReturnGenericError() {
		MarkResponseCommitted(c)
		writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
		return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
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
	return (gatewayprovider.OpenAIFailoverPolicy{Health: p.Health}).NewAccountFailure(
		account,
		resp.StatusCode,
		resp.Header,
		body,
		upstreamMsg,
		shouldDisable,
		!shouldDisable && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(resp.StatusCode),
	)
}

func (p *OpenAIResponseOutput) PassthroughError(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestBody []byte,
	responseBody []byte,
) error {
	body := p.redact(ctx, account, responseBody)

	// cyber_policy 仍按原始 body 打内部标记，供 handler 事后写风控/邮件；面向客户端的
	// 错误体在下方统一重建。cyber 是上游网络安全策略拦截，不冷却账号，
	// 故下方跳过 handleOpenAIAccountUpstreamError（避免自定义 temp-unschedulable 规则误冷却）。
	cyberHit, cyberCode, cyberMsg := openai.DetectOpenAICyberPolicy(body)
	if cyberHit {
		MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           cyberCode,
			Message:        cyberMsg,
			Body:           logredact.TruncateUTF8(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
	}

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if p.Options.Configured && p.Options.LogUpstreamErrorBody {
		maxBytes := p.Options.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	LogOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)
	clientInvalidRequest := openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body)
	requestScopedError := cyberHit || clientInvalidRequest || openai.IsOpenAIContextWindowError(upstreamMsg, body) ||
		gatewayprovider.IsOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body)
	// 错误体虽不会原样透传，运行态账号状态仍需更新，避免粘性路由继续复用
	// 刚被限流的账号。请求级错误例外：不冷却账号，也不触发池模式重试。
	if !requestScopedError {
		reqModel, _, _ := requeststate.OpenAIRequestMetaFromBody(requestBody)
		canonicalModel := gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(reqModel)
		decision := gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, account, resp.StatusCode, resp.Header, body, false, canonicalModel)
		if decision.ShouldReturnGenericError() {
			MarkResponseCommitted(c)
			writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
			return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, false, false) {
			return gatewayprovider.NewOpenAIUpstreamFailure(
				resp.StatusCode,
				resp.Header,
				body,
				upstreamMsg,
				decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
			)
		}
	}
	MarkResponseCommitted(c)
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
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
		WriteForwardPassthroughErrorHeaders(c.Writer.Header(), resp.Header)
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

func LogOpenAICapacityFailoverSuppressed(
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

// ApplyOpenAIStreamFailedErrorRule 对 response.failed 事件应用错误透传规则：
// 归一化 body 供关键词匹配/消息提取，并推断语义状态码使按错误码配置的规则可以命中。
// platform 必须传 account.Platform——本服务同时承载 openai 与 grok 平台账号，规则按平台匹配。
func ApplyOpenAIStreamFailedErrorRule(
	c *gin.Context,
	platform string,
	payload []byte,
	failedMessage string,
) (status int, errType string, errMsg string, matched bool) {
	ruleBody := openAIStreamFailedEventPassthroughBody(payload, failedMessage)
	upstreamStatus := openai.OpenAIStreamFailedEventSemanticStatus(payload, failedMessage)
	return ApplyErrorPassthroughRule(
		c,
		platform,
		upstreamStatus,
		ruleBody,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	)
}

func (p *OpenAIResponseOutput) TerminalAccountEffects(
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
		model := requeststate.FirstNonEmpty(canonicalModel...)
		if model == "" {
			model = requeststate.FirstNonEmpty(gjson.GetBytes(payload, "model").String(), gjson.GetBytes(payload, "response.model").String())
		}
		accountHeaders := headers
		if statusCode == http.StatusTooManyRequests {
			// 普通模型的流式 429 不能继承外层 HTTP 200 的全局 quota 快照；
			// 只有 OAuth/SetupToken 的 Spark 配额 429 才需要读取明确的窗口 reset。
			accountHeaders = gatewayprovider.OpenAISemantic429Headers(account, model, headers)
		}
		return statusCode, gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, account, statusCode, accountHeaders, payload, false, model).StopScheduling
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
			return statusCode, gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, account, statusCode, headers, payload, false, requeststate.FirstNonEmpty(canonicalModel...)).StopScheduling
		}
		return statusCode, false
	}
}

// ApplyStreamFailurePolicy 将 HTTP 200 流内的 response.failed
// 统一映射到现有账号策略管线，避免各协议入口重复推导状态码。
func (p *OpenAIResponseOutput) ApplyStreamFailurePolicy(
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
	return status, gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, account, status, headers, payload, true, model)
}

func (p *OpenAIResponseOutput) RecordStreamError(
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
	if len(payload) > 0 && p != nil && p.Options.Configured && p.Options.LogUpstreamErrorBody {
		maxBytes := p.Options.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		detail = logredact.TruncateUTF8(string(payload), maxBytes)
	}
	if c != nil {
		SetOpsUpstreamError(c, statusCode, message, detail)
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
		AppendOpsUpstreamError(c, event)
	}
	return message
}

func (p *OpenAIResponseOutput) NewStreamFailure(
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	upstreamRequestID string,
	payload []byte,
	message string,
	responseHeaders ...http.Header,
) *forwardcore.UpstreamFailoverError {
	return p.NewStreamFailureWithModel(c, account, passthrough, upstreamRequestID, payload, message, "", responseHeaders...)
}

func (p *OpenAIResponseOutput) NewStreamFailureWithModel(
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
	return p.NewStreamPolicyFailureWithModel(
		c, account, passthrough, upstreamRequestID, headers, http.StatusBadGateway, payload, message, false, canonicalModel,
	)
}

// NewStreamPolicyFailure 构造应用账号策略后的流内故障转移错误。
// 下游错误体保持统一封装，同时保留语义状态和上游响应头供 handler 最终处理。
func (p *OpenAIResponseOutput) NewStreamPolicyFailure(
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
	return p.NewStreamPolicyFailureWithModel(c, account, passthrough, upstreamRequestID, responseHeaders, statusCode, payload, message, false)
}

func (p *OpenAIResponseOutput) NewStreamPolicyFailureWithModel(
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
	observedStatus, shouldDisable, sideEffectsApplied := consumeOpenAIResponseFailureEffects(c)
	if sideEffectsApplied {
		statusCode = observedStatus
	}
	if !sideEffectsApplied {
		statusCode, shouldDisable = p.TerminalAccountEffects(c, account, payload, message, headers, canonicalModel...)
	}
	if statusCode < http.StatusBadRequest {
		statusCode = openai.OpenAIStreamFailureStatus(payload, message)
	}
	// 流内 failed 事件承载于 HTTP 200；使用事件的语义状态更新账号健康，
	// 再由 failover 引擎按 StatusCode/RetryableOnSameAccount 决定恢复策略。
	message = p.RecordStreamError(c, account, passthrough, upstreamRequestID, "failover", payload, message)
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
	retryable := gatewayprovider.OpenAIStreamFailureRetryable(account, payload, message)
	// 流终止事件承载在 HTTP 200 内，外层响应头描述的是成功流状态，而不是语义上的
	// 429 事件。仅在配额分类时忽略这些头；故障转移错误仍保留它们，使 Retry-After
	// 和请求 ID 能继续传递给后续处理。
	classificationHeaders := headers
	if statusCode == http.StatusTooManyRequests {
		classificationHeaders = nil
	}
	failoverErr := (gatewayprovider.OpenAIFailoverPolicy{Health: p.Health}).NewAccountFailureWithClassificationHeaders(account, statusCode, headers, classificationHeaders, payload, message, shouldDisable, retryable)
	if failoverErr.IsCredentialFailure() || failoverErr.RequestScopedTransient {
		return failoverErr
	}
	// 未分类的流失败保留通用信封；凭据和容量错误继续携带原报文。
	failoverErr.ResponseBody = body
	return failoverErr
}

// nonStreamingTerminalFailure 对非流请求收到的 SSE 终态使用原流式裁决。
// error 事件只在明确的瞬态信号下提议换号，response.failed 使用完整分类。
// 此时上游报文已缓冲；实际是否换号仍由入口按已提交状态和心跳写出量决定。
// 没有账号或响应已提交时保留协议错误路径，不在这里再实现输出仲裁。
func (p *OpenAIResponseOutput) nonStreamingTerminalFailure(
	c *gin.Context,
	resp *http.Response,
	account *gatewayprovider.ExecutionAccount,
	passthrough bool,
	terminalType string,
	payload []byte,
	message string,
	canonicalModel ...string,
) *forwardcore.UpstreamFailoverError {
	if account == nil || IsResponseCommitted(c) {
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
	return p.NewStreamFailureWithModel(c, account, passthrough, upstreamRequestID, payload, message, requeststate.FirstNonEmpty(canonicalModel...), headers)
}
