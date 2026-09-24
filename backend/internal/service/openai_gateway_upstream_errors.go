package service

import (
	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func logOpenAIInstructionsRequiredDebug(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	upstreamStatusCode int,
	upstreamMsg string,
	requestBody []byte,
	upstreamBody []byte,
) {
	msg := strings.TrimSpace(upstreamMsg)
	if !isOpenAIInstructionsRequiredError(upstreamStatusCode, msg, upstreamBody) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	accountID := int64(0)
	accountName := ""
	if account != nil {
		accountID = account.Record.ID
		accountName = strings.TrimSpace(account.Record.Name)
	}

	userAgent := ""
	originator := ""
	if c != nil {
		userAgent = strings.TrimSpace(c.GetHeader("User-Agent"))
		originator = strings.TrimSpace(c.GetHeader("originator"))
	}

	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("account_name", accountName),
		zap.Int("upstream_status_code", upstreamStatusCode),
		zap.String("upstream_error_message", msg),
		zap.String("request_user_agent", userAgent),
		zap.Bool("codex_official_client_match", openai.IsCodexOfficialClientByHeaders(userAgent, originator)),
	}
	fields = appendCodexCLIOnlyRejectedRequestFields(fields, c, requestBody)

	logging.FromContext(ctx).With(fields...).Warn("OpenAI 上游返回 Instructions are required，已记录请求详情用于排查")
}

func isOpenAIInstructionsRequiredError(upstreamStatusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if upstreamStatusCode != http.StatusBadRequest {
		return false
	}

	hasInstructionRequired := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "" {
			return false
		}
		if strings.Contains(lower, "instructions are required") {
			return true
		}
		if strings.Contains(lower, "required parameter: 'instructions'") {
			return true
		}
		if strings.Contains(lower, "required parameter: instructions") {
			return true
		}
		if strings.Contains(lower, "missing required parameter") && strings.Contains(lower, "instructions") {
			return true
		}
		return strings.Contains(lower, "instruction") && strings.Contains(lower, "required")
	}

	if hasInstructionRequired(upstreamMsg) {
		return true
	}
	if len(upstreamBody) == 0 {
		return false
	}

	errMsg := gjson.GetBytes(upstreamBody, "error.message").String()
	errMsgLower := strings.ToLower(strings.TrimSpace(errMsg))
	errCode := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.code").String()))
	errParam := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.param").String()))
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.type").String()))

	if errParam == "instructions" {
		return true
	}
	if hasInstructionRequired(errMsg) {
		return true
	}
	if strings.Contains(errCode, "missing_required_parameter") && strings.Contains(errMsgLower, "instructions") {
		return true
	}
	if strings.Contains(errType, "invalid_request") && strings.Contains(errMsgLower, "instructions") && strings.Contains(errMsgLower, "required") {
		return true
	}

	return false
}

func (s *OpenAIGatewayService) shouldFailoverUpstreamError(statusCode int) bool {
	switch statusCode {
	case 401, 402, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

func (s *OpenAIGatewayService) shouldFailoverOpenAIUpstreamResponse(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	// cyber_policy is request-scoped even when an intermediary wraps the
	// provider response in a retryable 5xx status. Never punish or rotate the
	// selected credential for it.
	if gatewayprovider.IsOpenAICyberWarningPayload(upstreamBody, upstreamMsg) {
		return false
	}
	if openai.IsOpenAIContextWindowError(upstreamMsg, upstreamBody) {
		return false
	}
	if isOpenAIHTTPUpstreamAccessStateError(statusCode, upstreamMsg, upstreamBody) {
		return true
	}
	if isOpenAIRequestBodyTooLargeError(statusCode, upstreamMsg, upstreamBody) {
		return true
	}
	if s.shouldFailoverUpstreamError(statusCode) {
		return true
	}
	return openai.IsOpenAITransientProcessingError(statusCode, upstreamMsg, upstreamBody)
}

// forwardcore.OpenAIRequestBodyTooLargeClientMessage 是账号级请求体限制切号耗尽后使用的固定下游文案。

const openAIRequestBodyTooLargeReason = forwardcore.GatewayFailureReason("openai_request_body_too_large")

func isOpenAIRequestBodyTooLargeError(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	return statusCode == http.StatusRequestEntityTooLarge && !openai.IsOpenAIContextWindowError(upstreamMsg, upstreamBody)
}

func newOpenAIUpstreamFailoverError(
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	retryableOnSameAccount bool,
) *forwardcore.UpstreamFailoverError {
	requestScopedCapacity := openai.IsOpenAIRequestScopedCapacityShed(upstreamMsg, responseBody)
	failoverErr := &forwardcore.UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseBody:           responseBody,
		ResponseHeaders:        responseHeaders.Clone(),
		RetryableOnSameAccount: retryableOnSameAccount || requestScopedCapacity,
		RequestScopedTransient: requestScopedCapacity,
	}
	if isOpenAIRequestBodyTooLargeError(statusCode, upstreamMsg, responseBody) {
		failoverErr.RetryableOnSameAccount = false
		failoverErr.RequestScopedTransient = false
		failoverErr.Scope = forwardcore.GatewayFailureScopeAccount
		failoverErr.Reason = openAIRequestBodyTooLargeReason
		failoverErr.NextAccountAction = forwardcore.NextAccountRetry
		failoverErr.ClientStatusCode = http.StatusRequestEntityTooLarge
		failoverErr.ClientMessage = forwardcore.OpenAIRequestBodyTooLargeClientMessage
	}
	if isOpenAIHTTPUpstreamAccessStateError(statusCode, upstreamMsg, responseBody) {
		failoverErr.RetryableOnSameAccount = false
		failoverErr.RequestScopedTransient = false
		failoverErr.Stage = forwardcore.GatewayFailureStageAccountAuth
		failoverErr.Scope = forwardcore.GatewayFailureScopeAccount
		failoverErr.Reason = forwardcore.OpenAIUpstreamAccessStateReason
		failoverErr.NextAccountAction = forwardcore.NextAccountRetry
		failoverErr.ClientStatusCode = http.StatusBadGateway
		failoverErr.ClientMessage = openAIUpstreamAccessUnavailableClientMessage
	} else if requestScopedCapacity {
		// Preserve the provider's actionable overload message after gateway
		// retries are exhausted, but expose it as a retryable server_error.
		failoverErr.ClientStatusCode = http.StatusServiceUnavailable
		failoverErr.ClientMessage = openAICapacityShedClientMessage(upstreamMsg, responseBody)
	}
	return failoverErr
}

func (s *OpenAIGatewayService) newOpenAIAccountFailoverError(
	account *gatewayprovider.ExecutionAccount,
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	shouldDisable bool,
	retryableOnSameAccount bool,
) *forwardcore.UpstreamFailoverError {
	return s.newOpenAIAccountFailoverErrorWithClassificationHeaders(account, statusCode, responseHeaders, responseHeaders, responseBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
}

func (s *OpenAIGatewayService) newOpenAIAccountFailoverErrorWithClassificationHeaders(
	account *gatewayprovider.ExecutionAccount,
	statusCode int,
	responseHeaders http.Header,
	classificationHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	shouldDisable bool,
	retryableOnSameAccount bool,
) *forwardcore.UpstreamFailoverError {
	oauth429Retry := s.shouldRetryOpenAIOAuth429OnSameAccountWithResponse(account, statusCode, shouldDisable, classificationHeaders, responseBody)
	failoverErr := newOpenAIUpstreamFailoverError(
		statusCode,
		responseHeaders,
		responseBody,
		upstreamMsg,
		retryableOnSameAccount || oauth429Retry,
	)
	if oauth429Retry {
		failoverErr.SameAccountRetryDeadline = s.openAIOAuth429RetryDeadline(account)
		failoverErr.SameAccountRetryDelay = openAIOAuth429SameAccountRetryDelay(responseHeaders, failoverErr.SameAccountRetryDeadline)
	}
	return failoverErr
}

const (
	openAIUpstreamAccessUnavailableClientMessage = "Upstream access is temporarily unavailable, please retry later"
	// forwardcore.OpenAIUpstreamAccessStateReason marks a provider credential whose
	// account, workspace, or organization is unavailable.

	// forwardcore.OpenAIHTTPContinuationUnsupportedReason identifies accounts that cannot
	// preserve an official Responses HTTP continuation without dropping state.

)

func isOpenAIUpstreamAccessStateError(_ string, body []byte) bool {
	return openai.IsOpenAIUpstreamAccessStateError("", body)
}

func isOpenAIHTTPUpstreamAccessStateError(_ int, _ string, body []byte) bool {
	return openai.IsOpenAIHTTPUpstreamAccessStateError(0, "", body)
}

func openAICapacityShedClientMessage(upstreamMsg string, body []byte) string {
	for _, candidate := range []string{
		upstreamMsg,
		gjson.GetBytes(body, "error.message").String(),
		gjson.GetBytes(body, "response.error.message").String(),
		gjson.GetBytes(body, "message").String(),
	} {
		candidate = logredact.SanitizeUpstreamQueries(strings.TrimSpace(candidate))
		if candidate != "" && openai.IsOpenAICapacityShedMessage(candidate) {
			return candidate
		}
	}
	return "Upstream service is temporarily overloaded, please retry later"
}

func openAIUpstreamErrorBodyReadLimitForConfig(cfg *config.Config) int64 {
	limit := openAIUpstreamErrorBodyReadLimit
	if cfg != nil && cfg.Gateway.LogUpstreamErrorBody && cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}

func (s *OpenAIGatewayService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	cfg := (*config.Config)(nil)
	if s != nil {
		cfg = s.cfg
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, openAIUpstreamErrorBodyReadLimitForConfig(cfg)))
	return body
}

func (s *OpenAIGatewayService) handleFailoverSideEffects(ctx context.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount, responseBody []byte, canonicalModel ...string) bool {
	return s.applyFailoverSideEffects(ctx, resp, account, responseBody, canonicalModel...).StopScheduling
}

// applyFailoverSideEffects 返回完整策略决策，避免自定义未命中被误当作池模式可重试。
func (s *OpenAIGatewayService) applyFailoverSideEffects(ctx context.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount, responseBody []byte, canonicalModel ...string) accountcore.UpstreamErrorDecision {
	if len(canonicalModel) > 0 {
		return s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, responseBody, canonicalModel[0])
	}
	return s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, responseBody)
}

func (s *OpenAIGatewayService) handleErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestBody []byte,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := s.readUpstreamErrorBody(resp)
	body = s.agentIdentity.Redact(ctx, account, body)

	if hit, code, cyberMsg := openai.DetectOpenAICyberPolicy(body); hit {
		gatewayhttp.MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           code,
			Message:        cyberMsg,
			Body:           logredact.TruncateUTF8(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
		gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, cyberMsg, logredact.TruncateUTF8(string(body), 2048))
		gatewayhttp.WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		gatewayhttp.MarkResponseCommitted(c)
		c.Data(resp.StatusCode, contentType, body)
		if cyberMsg == "" {
			return nil, fmt.Errorf("openai cyber_policy: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai cyber_policy: %s", cyberMsg)
	}
	if account != nil && account.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := grokContentPolicyClientMessage(body)
		gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, clientMsg, logredact.TruncateUTF8(string(body), 2048))
		gatewayhttp.WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		gatewayhttp.MarkResponseCommitted(c)
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": clientMsg,
			},
		})
		return nil, fmt.Errorf("grok content policy rejection: %s", clientMsg)
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

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logging.LegacyPrintf("service.openai_gateway",
			"OpenAI upstream error %d (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.Record.ID,
			account.Record.Platform,
			account.Record.Type,
			logredact.TruncateLine(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	if gatewayprovider.IsOpenAICyberWarningPayload(body, upstreamMsg) {
		errMsg := gatewayprovider.ExtractOpenAICyberWarningMessage(body, upstreamMsg)
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            errMsg,
			Detail:             upstreamDetail,
		})
		c.JSON(resp.StatusCode, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": errMsg,
			},
		})
		return nil, gatewayprovider.WrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, body, errMsg, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, errMsg))
	}

	if isOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body) {
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		gatewayhttp.WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		gatewayhttp.MarkResponseCommitted(c)
		c.Data(resp.StatusCode, contentType, body)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream request body too large: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream request body too large: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	if openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body) {
		// 参数型 400 不影响账号健康；保留上游上下文供 Ops 排障，并向客户端透传完整错误结构。
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		gatewayhttp.WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		gatewayhttp.MarkResponseCommitted(c)
		c.Data(http.StatusBadRequest, contentType, body)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream invalid request: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	// 请求级排除完成后再执行账号策略，避免非故障转移状态漏掉显式配置。
	var reqModel string
	if len(requestedModel) > 0 {
		reqModel = strings.TrimSpace(requestedModel[0])
	}
	if reqModel == "" {
		reqModel, _, _ = requeststate.OpenAIRequestMetaFromBody(requestBody)
		reqModel = gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(reqModel)
	}
	var decision accountcore.UpstreamErrorDecision
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.grokHealth, account, resp.StatusCode, resp.Header, body, "", reqModel)
	} else {
		decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, reqModel)
	}
	if decision.ShouldReturnGenericError() {
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		gatewayhttp.MarkResponseCommitted(c)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "upstream_error",
				"message": "Upstream gateway error",
			},
		})
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	kind := "http_error"
	defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, body)
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, body)
	}
	if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, decision.StopScheduling, defaultFailover) {
		kind = "failover"
	}
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	if kind == "failover" {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
		}
	}

	// 透传规则只改变最终客户端响应，不得绕过已经执行的账号策略。
	if status, errType, errMsg, matched := gatewayhttp.ApplyErrorPassthroughRule(
		c,
		account.Record.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		gatewayhttp.MarkResponseCommitted(c)
		c.JSON(status, gin.H{
			"error": gin.H{
				"type":    errType,
				"message": errMsg,
			},
		})
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	// 只有既有分类明确判定不可故障转移的 400 才属于确定性请求错误。
	// 池模式重试状态码、server_is_overloaded 和瞬时处理错误仍保留原有重试或 502 语义。
	if account != nil && account.Record.Platform == capability.PlatformOpenAI &&
		isOpenAIDeterministicClientError(resp.StatusCode, defaultFailover) {
		gatewayhttp.MarkResponseCommitted(c)
		writeOpenAIUpstreamClientError(c, resp.StatusCode, body, upstreamMsg)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	gatewayhttp.MarkResponseCommitted(c)

	// Return appropriate error response
	var errType, errMsg string
	var statusCode int

	switch resp.StatusCode {
	case 401:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream authentication failed, please contact administrator"
	case 402:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream payment required: insufficient balance or billing issue"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream access forbidden, please contact administrator"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded, please retry later"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}
	if openai.IsOpenAIContextWindowError(upstreamMsg, body) && upstreamMsg != "" {
		errMsg = upstreamMsg
	}

	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": errMsg,
		},
	})

	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}
	return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

// compatErrorWriter 由兼容协议路径写入普通错误信封。
type compatErrorWriter func(c *gin.Context, statusCode int, errType, message string)

// compatErrorBodyWriter 由兼容协议路径写入包含 code/param 的完整脱敏错误对象。
type compatErrorBodyWriter func(c *gin.Context, statusCode int, body []byte)

// handleCompatErrorResponse 是 Chat Completions 与 Anthropic Messages 共用的非故障转移错误处理器。
func (s *OpenAIGatewayService) handleCompatErrorResponse(
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	writeError compatErrorWriter,
	writeErrorBody compatErrorBodyWriter,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := s.readUpstreamErrorBody(resp)
	body = s.agentIdentity.Redact(context.Background(), account, body)

	if hit, code, cyberMsg := openai.DetectOpenAICyberPolicy(body); hit {
		gatewayhttp.MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           code,
			Message:        cyberMsg,
			Body:           logredact.TruncateUTF8(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
		gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, cyberMsg, logredact.TruncateUTF8(string(body), 2048))
		clientMsg := cyberMsg
		if clientMsg == "" {
			clientMsg = "Request blocked by upstream cyber-security policy"
		}
		gatewayhttp.MarkResponseCommitted(c)
		writeError(c, resp.StatusCode, "invalid_request_error", clientMsg)
		if cyberMsg == "" {
			return nil, fmt.Errorf("openai cyber_policy: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai cyber_policy: %s", cyberMsg)
	}
	if account != nil && account.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := grokContentPolicyClientMessage(body)
		gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, clientMsg, logredact.TruncateUTF8(string(body), 2048))
		gatewayhttp.MarkResponseCommitted(c)
		writeError(c, http.StatusForbidden, "invalid_request_error", clientMsg)
		return nil, fmt.Errorf("grok content policy rejection: %s", clientMsg)
	}

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("Upstream error: %d", resp.StatusCode)
	}
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

	if openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body) {
		// 兼容协议也必须保留上游 error 对象中的 code、param 等结构化详情。
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		gatewayhttp.MarkResponseCommitted(c)
		writeErrorBody(c, http.StatusBadRequest, body)
		return nil, fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	if isOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body) {
		gatewayhttp.MarkResponseCommitted(c)
		writeErrorBody(c, resp.StatusCode, body)
		return nil, fmt.Errorf("upstream request body too large: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	var modelForCooldown string
	if len(requestedModel) > 0 {
		modelForCooldown = requestedModel[0]
	}
	var decision accountcore.UpstreamErrorDecision
	if account.Record.Platform == capability.PlatformGrok {
		decision = gatewayprovider.ApplyGrokExecutionHealth(c.Request.Context(), s.grokHealth, account, resp.StatusCode, resp.Header, body, "", modelForCooldown)
	} else {
		decision = s.applyOpenAIAccountUpstreamError(c.Request.Context(), account, resp.StatusCode, resp.Header, body, modelForCooldown)
	}
	if decision.ShouldReturnGenericError() {
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		gatewayhttp.MarkResponseCommitted(c)
		writeError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	kind := "http_error"
	defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, body)
	if account.Record.Platform == capability.PlatformGrok {
		defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, body)
	}
	if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, decision.StopScheduling, defaultFailover) {
		kind = "failover"
	}
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	if kind == "failover" {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
		}
	}

	// 透传规则只负责最终响应格式，不能绕过账号策略。
	if status, errType, errMsg, matched := gatewayhttp.ApplyErrorPassthroughRule(
		c, account.Record.Platform, resp.StatusCode, body,
		http.StatusBadGateway, "api_error", "Upstream request failed",
	); matched {
		gatewayhttp.MarkResponseCommitted(c)
		writeError(c, status, errType, errMsg)
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}
	gatewayhttp.MarkResponseCommitted(c)

	// Map status code to error type and write response
	errType := "api_error"
	switch {
	case resp.StatusCode == 400:
		errType = "invalid_request_error"
	case resp.StatusCode == 404:
		errType = "not_found_error"
	case resp.StatusCode == 429:
		errType = "rate_limit_error"
	case resp.StatusCode >= 500:
		errType = "api_error"
	}

	writeError(c, resp.StatusCode, errType, upstreamMsg)
	return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
}
