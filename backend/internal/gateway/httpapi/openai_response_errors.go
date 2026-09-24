package httpapi

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

func LogOpenAIInstructionsRequiredDebug(
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
	fields = AppendCodexRejectedRequestFields(fields, c, requestBody)

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

func (p *OpenAIResponseOutput) ReadErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	limit := p.errorBodyReadLimit()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body
}

func (p *OpenAIResponseOutput) ResponseError(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestBody []byte,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := p.ReadErrorBody(resp)
	body = p.redact(ctx, account, body)

	if hit, code, cyberMsg := openai.DetectOpenAICyberPolicy(body); hit {
		MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           code,
			Message:        cyberMsg,
			Body:           logredact.TruncateUTF8(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
		SetOpsUpstreamError(c, resp.StatusCode, cyberMsg, logredact.TruncateUTF8(string(body), 2048))
		WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, p.Headers)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		MarkResponseCommitted(c)
		c.Data(resp.StatusCode, contentType, body)
		if cyberMsg == "" {
			return nil, fmt.Errorf("openai cyber_policy: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai cyber_policy: %s", cyberMsg)
	}
	if account != nil && account.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := gatewayprovider.GrokContentPolicyClientMessage(body)
		SetOpsUpstreamError(c, resp.StatusCode, clientMsg, logredact.TruncateUTF8(string(body), 2048))
		WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, p.Headers)
		MarkResponseCommitted(c)
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
	if p.Options.Configured && p.Options.LogUpstreamErrorBody {
		maxBytes := p.Options.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	LogOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)

	if p.Options.Configured && p.Options.LogUpstreamErrorBody {
		logging.LegacyPrintf("service.openai_gateway",
			"OpenAI upstream error %d (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.Record.ID,
			account.Record.Platform,
			account.Record.Type,
			logredact.TruncateLine(body, p.Options.LogUpstreamErrorBodyMaxBytes),
		)
	}

	if gatewayprovider.IsOpenAICyberWarningPayload(body, upstreamMsg) {
		errMsg := gatewayprovider.ExtractOpenAICyberWarningMessage(body, upstreamMsg)
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
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

	if gatewayprovider.IsOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body) {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, p.Headers)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		MarkResponseCommitted(c)
		c.Data(resp.StatusCode, contentType, body)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream request body too large: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream request body too large: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	if openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body) {
		// 参数型 400 不影响账号健康；保留上游上下文供 Ops 排障，并向客户端透传完整错误结构。
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, p.Headers)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		MarkResponseCommitted(c)
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
		decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, p.GrokHealth, account, resp.StatusCode, resp.Header, body, "", reqModel)
	} else {
		decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, account, resp.StatusCode, resp.Header, body, false, reqModel)
	}
	if decision.ShouldReturnGenericError() {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		MarkResponseCommitted(c)
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
	defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, body)
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, body)
	}
	if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, decision.StopScheduling, defaultFailover) {
		kind = "failover"
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
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
	if status, errType, errMsg, matched := ApplyErrorPassthroughRule(
		c,
		account.Record.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
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
		IsOpenAIDeterministicClientError(resp.StatusCode, defaultFailover) {
		MarkResponseCommitted(c)
		WriteOpenAIUpstreamClientError(c, resp.StatusCode, body, upstreamMsg)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	MarkResponseCommitted(c)

	// 保留原状态码对应的客户端错误信封。
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

// CompatError 是 Chat Completions 与 Anthropic Messages 共用的非故障转移错误处理器。
func (p *OpenAIResponseOutput) CompatError(
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	writeError func(*gin.Context, int, string, string),
	writeErrorBody func(*gin.Context, int, []byte),
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := p.ReadErrorBody(resp)
	body = p.redact(context.Background(), account, body)

	if hit, code, cyberMsg := openai.DetectOpenAICyberPolicy(body); hit {
		MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           code,
			Message:        cyberMsg,
			Body:           logredact.TruncateUTF8(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
		SetOpsUpstreamError(c, resp.StatusCode, cyberMsg, logredact.TruncateUTF8(string(body), 2048))
		clientMsg := cyberMsg
		if clientMsg == "" {
			clientMsg = "Request blocked by upstream cyber-security policy"
		}
		MarkResponseCommitted(c)
		writeError(c, resp.StatusCode, "invalid_request_error", clientMsg)
		if cyberMsg == "" {
			return nil, fmt.Errorf("openai cyber_policy: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai cyber_policy: %s", cyberMsg)
	}
	if account != nil && account.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := gatewayprovider.GrokContentPolicyClientMessage(body)
		SetOpsUpstreamError(c, resp.StatusCode, clientMsg, logredact.TruncateUTF8(string(body), 2048))
		MarkResponseCommitted(c)
		writeError(c, http.StatusForbidden, "invalid_request_error", clientMsg)
		return nil, fmt.Errorf("grok content policy rejection: %s", clientMsg)
	}

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("Upstream error: %d", resp.StatusCode)
	}
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

	if openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body) {
		// 兼容协议也必须保留上游 error 对象中的 code、param 等结构化详情。
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		MarkResponseCommitted(c)
		writeErrorBody(c, http.StatusBadRequest, body)
		return nil, fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	if gatewayprovider.IsOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body) {
		MarkResponseCommitted(c)
		writeErrorBody(c, resp.StatusCode, body)
		return nil, fmt.Errorf("upstream request body too large: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	var modelForCooldown string
	if len(requestedModel) > 0 {
		modelForCooldown = requestedModel[0]
	}
	var decision accountcore.UpstreamErrorDecision
	if account.Record.Platform == capability.PlatformGrok {
		decision = gatewayprovider.ApplyGrokExecutionHealth(c.Request.Context(), p.GrokHealth, account, resp.StatusCode, resp.Header, body, "", modelForCooldown)
	} else {
		decision = gatewayprovider.ApplyOpenAIResponseHealth(c.Request.Context(), p.Health, account, resp.StatusCode, resp.Header, body, false, modelForCooldown)
	}
	if decision.ShouldReturnGenericError() {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		MarkResponseCommitted(c)
		writeError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	kind := "http_error"
	defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, body)
	if account.Record.Platform == capability.PlatformGrok {
		defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, body)
	}
	if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, decision.StopScheduling, defaultFailover) {
		kind = "failover"
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
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
	if status, errType, errMsg, matched := ApplyErrorPassthroughRule(
		c, account.Record.Platform, resp.StatusCode, body,
		http.StatusBadGateway, "api_error", "Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		writeError(c, status, errType, errMsg)
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}
	MarkResponseCommitted(c)

	// 按既有状态码映射写出兼容协议错误。
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

func (p *OpenAIResponseOutput) errorBodyReadLimit() int64 {
	limit := int64(512 << 10)
	if p != nil && p.Options.LogUpstreamErrorBody && p.Options.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(p.Options.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}
