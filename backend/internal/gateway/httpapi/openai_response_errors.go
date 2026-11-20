package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_response_errors.go classifies upstream HTTP failures and writes
// protocol-specific client errors.
// Upstream HTTP failures need separate request-error, provider-health and
// client-presentation decisions. Responses and compatible Chat/Messages output
// preserve structured request rejection details, handle cyber evidence before
// provider policy, and classify remaining failures before applying
// administrator presentation rules.
//
// Error capture is bounded but intentionally returns available partial bytes
// when reads fail, allowing later classification to use whatever evidence was
// captured. Replayable reads close and replace the original body so subsequent
// protocol handling sees those captured bytes again. The request or explicitly
// supplied model selects health scope, and the final error path can return a
// failover signal before writing a client response. Execution callers retain
// overall response cleanup.

// LogOpenAIInstructionsRequiredDebug logs request diagnostics for recognized
// instructions-required HTTP 400 evidence. Other errors are ignored; nil
// logging context uses background context, and the appended diagnostic fields
// follow their own header, query and prompt-cache capture rules.
func LogOpenAIInstructionsRequiredDebug(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
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

	providerID := int64(0)
	providerName := ""
	if provider != nil {
		providerID = provider.Record.ID
		providerName = strings.TrimSpace(provider.Record.Name)
	}

	userAgent := ""
	originator := ""
	if c != nil {
		userAgent = strings.TrimSpace(c.GetHeader("User-Agent"))
		originator = strings.TrimSpace(c.GetHeader("originator"))
	}

	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("provider_id", providerID),
		zap.String("provider_name", providerName),
		zap.Int("upstream_status_code", upstreamStatusCode),
		zap.String("upstream_error_message", msg),
		zap.String("request_user_agent", userAgent),
		zap.Bool("codex_official_client_match", openai.IsCodexOfficialClientByHeaders(userAgent, originator)),
	}
	fields = AppendCodexRejectedRequestFields(fields, c, requestBody)

	logging.FromContext(ctx).With(fields...).Warn("OpenAI upstream returned Instructions are required; request details logged for investigation")
}

// isOpenAIInstructionsRequiredError recognizes instructions-required evidence
// only for HTTP 400. It examines the supplied message and selected nested
// error fields, accepts error.param instructions directly, and returns false
// when no matching message or body evidence is present.
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

// ReadErrorBody captures at most errorBodyReadLimit bytes from the upstream
// body. Missing response or body returns nil; read errors are suppressed and
// available partial bytes survive, while callers retain responsibility for
// closing or replacing the response body.
func (p *OpenAIResponseOutput) ReadErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	limit := p.errorBodyReadLimit()
	// Preserve bytes received before a read failure so downstream classifiers
	// can use partial upstream evidence; the caller separately controls body cleanup.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body
}

// ResponseError classifies a captured upstream HTTP failure for Responses
// output or provider recovery. Callers provide the selected provider, live
// HTTP context and response, and keep ownership of response-body closure. It
// captures and optionally redacts the body before interpreting cyber, Grok
// policy, size and parameter evidence.
//
// Request-specific branches precede provider health and preserve their
// dedicated presentation. Remaining failures use the first supplied scheduling
// model, or a canonical model derived from requestBody. Health policy chooses
// generic output or failover before administrator rules and default status
// mapping. A failover error returns without final client output; other
// branches write the selected rejection and return its diagnostic error.
func (p *OpenAIResponseOutput) ResponseError(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestBody []byte,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := p.ReadErrorBody(resp)
	body = p.redact(ctx, provider, body)

	// Dedicated cyber evidence belongs to this request. Record it and preserve
	// its rejection before provider health or administrator presentation can reinterpret it.
	if hit, code, cyberMsg := openai.DetectOpenAICyberPolicy(body); hit {
		// Bound cyber evidence and the Ops summary at complete UTF-8 characters.
		MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           code,
			Message:        cyberMsg,
			Body:           logredact.TruncateUTF8(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
		SetOpsUpstreamError(c, resp.StatusCode, cyberMsg, logredact.TruncateUTF8(string(body), 2048))
		MarkResponseCommitted(c)
		if CommittedSSE(c) {
			errType, bodyCode, message := extractSSEErrorFields(resp.StatusCode, body, "invalid_request_error", "Request blocked by upstream cyber-security policy")
			if code == "" {
				code = bodyCode
			}
			if trimmed := strings.TrimSpace(cyberMsg); trimmed != "" {
				message = trimmed
			}
			DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, resp.StatusCode, errType, code, message, true, false)
		} else {
			p.writePassthroughBody(c, resp, resp.StatusCode, body)
		}
		if cyberMsg == "" {
			return nil, fmt.Errorf("openai cyber_policy: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai cyber_policy: %s", cyberMsg)
	}
	// Grok content rejection is request-specific; its dedicated client error
	// precedes provider health so policy-blocked input does not become provider recovery.
	if provider != nil && provider.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := gatewayprovider.GrokContentPolicyClientMessage(body)
		// Keep the Grok rejection excerpt bounded at complete UTF-8 characters.
		SetOpsUpstreamError(c, resp.StatusCode, clientMsg, logredact.TruncateUTF8(string(body), 2048))
		MarkResponseCommitted(c)
		if !CommittedSSE(c) {
			WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, p.Headers)
		}
		writeOpenAIResponseJSONOrSSE(c, http.StatusForbidden, "invalid_request_error", clientMsg)
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
		// Limit optional diagnostic excerpts while keeping classification on the
		// complete captured bytes; UTF-8 truncation avoids a split character in Ops.
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	LogOpenAIInstructionsRequiredDebug(ctx, c, provider, resp.StatusCode, upstreamMsg, requestBody, body)

	if p.Options.Configured && p.Options.LogUpstreamErrorBody {
		logging.LegacyPrintf("service.openai_gateway",
			"OpenAI upstream error %d (provider=%d platform=%s type=%s): %s",
			resp.StatusCode,
			provider.Record.ID,
			provider.Record.Platform,
			provider.Record.Type,
			logredact.TruncateLine(body, p.Options.LogUpstreamErrorBodyMaxBytes),
		)
	}

	if gatewayprovider.IsOpenAICyberWarningPayload(body, upstreamMsg) {
		errMsg := gatewayprovider.ExtractOpenAICyberWarningMessage(body, upstreamMsg)
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            errMsg,
			Detail:             upstreamDetail,
		}, telemetry.RequestAttemptFromResponse(resp))
		writeOpenAIResponseJSONOrSSE(c, resp.StatusCode, "invalid_request_error", errMsg)
		return nil, gatewayprovider.WrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, body, errMsg, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, errMsg))
	}

	if gatewayprovider.IsOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body) {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		}, telemetry.RequestAttemptFromResponse(resp))
		MarkResponseCommitted(c)
		p.writePassthroughBodyOrSSE(c, resp, resp.StatusCode, body, "invalid_request_error", "Request body too large")
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream request body too large: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream request body too large: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	if openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body) {
		// Parameter rejection is tied to this input. Record its Ops evidence and
		// retain the captured structure before any provider-health branch can run.
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		}, telemetry.RequestAttemptFromResponse(resp))
		MarkResponseCommitted(c)
		p.writePassthroughBodyOrSSE(c, resp, http.StatusBadRequest, body, "invalid_request_error", "Invalid request")
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream invalid request: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	// These remaining failures can affect provider scheduling even when their
	// default status is nonretryable; apply health before selecting final presentation.
	var reqModel string
	if len(requestedModel) > 0 {
		reqModel = strings.TrimSpace(requestedModel[0])
	}
	if reqModel == "" {
		reqModel, _, _ = requeststate.OpenAIRequestMetaFromBody(requestBody)
		reqModel = gatewayprovider.ExecutionModelPolicy(provider).WithContext(ctx).CanonicalSchedulingModel(reqModel)
	}
	var decision providercore.UpstreamErrorDecision
	if provider != nil && provider.Record.Platform == capability.PlatformGrok {
		decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, p.GrokHealth, provider, resp.StatusCode, resp.Header, body, "", reqModel)
	} else {
		decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, provider, resp.StatusCode, resp.Header, body, false, reqModel)
	}
	if decision.ShouldReturnGenericError() {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		}, telemetry.RequestAttemptFromResponse(resp))
		MarkResponseCommitted(c)
		writeOpenAIResponseJSONOrSSE(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	kind := "http_error"
	defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, body)
	if provider != nil && provider.Record.Platform == capability.PlatformGrok {
		defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, body)
	}
	if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, decision.StopScheduling, defaultFailover) {
		kind = "failover"
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           provider.Record.Platform,
		ProviderID:         provider.Record.ID,
		ProviderName:       provider.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	}, telemetry.RequestAttemptFromResponse(resp))
	if kind == "failover" {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode:              resp.StatusCode,
			ResponseBody:            body,
			RetryableOnSameProvider: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode),
		}
	}

	// Health and recovery are already decided here. Presentation rules may
	// change the client envelope but cannot reopen the preceding retry decision.
	if status, errType, errMsg, matched := ApplyErrorPassthroughRule(
		c,
		provider.Record.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		writeOpenAIResponseJSONOrSSE(c, status, errType, errMsg)
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	// A 400 is deterministic only when the classifier rules out failover.
	// Pool retry statuses, server_is_overloaded, and transient processing failures follow retry or 502 handling.
	if provider != nil && provider.Record.Platform == capability.PlatformOpenAI &&
		IsOpenAIDeterministicClientError(resp.StatusCode, defaultFailover) {
		MarkResponseCommitted(c)
		WriteOpenAIUpstreamClientError(c, resp.StatusCode, body, upstreamMsg)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	MarkResponseCommitted(c)

	// Upstream authentication and billing statuses describe the selected provider.
	// Map them to gateway failures while retaining client-actionable rate-limit output.
	var errType, errMsg string
	var statusCode int

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream authentication failed, please contact administrator"
	case http.StatusPaymentRequired:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream payment required: insufficient balance or billing issue"
	case http.StatusForbidden:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream access forbidden, please contact administrator"
	case http.StatusTooManyRequests:
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

	writeOpenAIResponseJSONOrSSE(c, statusCode, errType, errMsg)

	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}
	return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

// CompatError classifies an upstream HTTP rejection for the supplied Chat or
// Messages writers. Callers retain response-body cleanup and provide a
// selected provider and live request. Captured error bytes are optionally
// redacted using background context before dedicated cyber, Grok policy,
// parameter and size handling.
//
// Remaining failures apply health using the first supplied model, then can
// return a failover error before either writer is invoked. Final presentation
// applies administrator rules or compatible status/type mapping. The body
// writer retains structured parameter errors, while ordinary errors use the
// message writer. Callers inspect the returned error to distinguish recovery
// from a rejection already written.
func (p *OpenAIResponseOutput) CompatError(
	resp *http.Response,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	writeError func(*gin.Context, int, string, string),
	writeErrorBody func(*gin.Context, int, []byte),
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := p.ReadErrorBody(resp)
	body = p.redact(context.Background(), provider, body)

	// Dedicated cyber evidence belongs to this request. Record it and preserve
	// its rejection before provider health or administrator presentation can reinterpret it.
	if hit, code, cyberMsg := openai.DetectOpenAICyberPolicy(body); hit {
		// Bound cyber evidence and the Ops summary at complete UTF-8 characters.
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
	// Grok content rejection is request-specific; its dedicated client error
	// precedes provider health so policy-blocked input does not become provider recovery.
	if provider != nil && provider.Record.Platform == capability.PlatformGrok && grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := gatewayprovider.GrokContentPolicyClientMessage(body)
		// Keep the Grok rejection excerpt bounded at complete UTF-8 characters.
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
		// Limit optional diagnostic excerpts while keeping classification on the
		// complete captured bytes; UTF-8 truncation avoids a split character in Ops.
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

	if openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body) {
		// Retain code, param and other captured error fields in the supplied
		// protocol body writer, before provider health can classify this request rejection.
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		}, telemetry.RequestAttemptFromResponse(resp))
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
	var decision providercore.UpstreamErrorDecision
	if provider.Record.Platform == capability.PlatformGrok {
		decision = gatewayprovider.ApplyGrokExecutionHealth(c.Request.Context(), p.GrokHealth, provider, resp.StatusCode, resp.Header, body, "", modelForCooldown)
	} else {
		decision = gatewayprovider.ApplyOpenAIResponseHealth(c.Request.Context(), p.Health, provider, resp.StatusCode, resp.Header, body, false, modelForCooldown)
	}
	if decision.ShouldReturnGenericError() {
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		}, telemetry.RequestAttemptFromResponse(resp))
		MarkResponseCommitted(c)
		writeError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	kind := "http_error"
	defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, body)
	if provider.Record.Platform == capability.PlatformGrok {
		defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, body)
	}
	if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, decision.StopScheduling, defaultFailover) {
		kind = "failover"
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           provider.Record.Platform,
		ProviderID:         provider.Record.ID,
		ProviderName:       provider.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	}, telemetry.RequestAttemptFromResponse(resp))
	if kind == "failover" {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode:              resp.StatusCode,
			ResponseBody:            body,
			RetryableOnSameProvider: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode),
		}
	}

	// Provider effects and retry eligibility precede presentation rules. Use a
	// matched envelope only after the failure has remained on the final-output path.
	if status, errType, errMsg, matched := ApplyErrorPassthroughRule(
		c, provider.Record.Platform, resp.StatusCode, body,
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

	// Compatible writers retain the upstream status while selecting a client
	// protocol category, so their envelope describes this already-classified final rejection.
	errType := "api_error"
	switch {
	case resp.StatusCode == http.StatusBadRequest:
		errType = "invalid_request_error"
	case resp.StatusCode == http.StatusNotFound:
		errType = "not_found_error"
	case resp.StatusCode == http.StatusTooManyRequests:
		errType = "rate_limit_error"
	case resp.StatusCode >= 500:
		errType = "api_error"
	}

	writeError(c, resp.StatusCode, errType, upstreamMsg)
	return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
}

// errorBodyReadLimit returns the capture ceiling for upstream HTTP error
// evidence. It defaults to 512 KiB; enabled body logging with a larger
// positive excerpt limit expands capture even when Options.Configured is
// false.
func (p *OpenAIResponseOutput) errorBodyReadLimit() int64 {
	// Bound error capture by default while allowing configured diagnostic expansion.
	limit := int64(512 << 10)
	if p != nil && p.Options.LogUpstreamErrorBody && p.Options.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(p.Options.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}

// ApplyHTTPFailure delegates OpenAI response-health handling using status,
// headers and captured body. The first supplied model scopes the observation
// when present; callers supply existing response bytes, and this helper does
// not reread or close the response body.
func (p *OpenAIResponseOutput) ApplyHTTPFailure(ctx context.Context, resp *http.Response, target *gatewayprovider.ExecutionProvider, body []byte, models ...string) providercore.UpstreamErrorDecision {
	if len(models) > 0 {
		return gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, target, resp.StatusCode, resp.Header, body, false, models[0])
	}
	return gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, target, resp.StatusCode, resp.Header, body, false)
}

// ReadReplayableError captures bounded error bytes, closes the upstream body
// and replaces resp.Body with a readable copy. Callers supply a live
// response/body; partial capture and close errors do not prevent replacement.
// The returned message sanitizes query values, while returned body bytes
// remain the captured content.
func (p *OpenAIResponseOutput) ReadReplayableError(resp *http.Response) ([]byte, string) {
	respBody := p.ReadErrorBody(resp)
	// Captured bytes remain replayable after the original body is closed, even
	// if that close reports an error; subsequent handlers consume the bounded copy.
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	return respBody, upstreamMsg
}

// writeOpenAIResponseJSONOrSSE writes an OpenAI JSON error envelope, or emits a
// terminal SSE failure event when event-stream framing is already committed.
// Callers retain semantic commitment marking before invoking this presentation
// helper.
func writeOpenAIResponseJSONOrSSE(c *gin.Context, status int, errType, message string) {
	if CommittedSSE(c) {
		DefaultOpenAIErrorOutput().StreamError(c, status, errType, message, true)
		return
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// writePassthroughBody copies filtered passthrough response headers and writes
// captured upstream bytes. Callers supply an uncommitted Gin writer and
// response; an empty upstream Content-Type defaults to application/json.
func (p *OpenAIResponseOutput) writePassthroughBody(c *gin.Context, resp *http.Response, status int, body []byte) {
	WriteOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, p.Headers)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(status, contentType, body)
}

// writePassthroughBodyOrSSE writes captured upstream bytes with passthrough
// headers on uncommitted responses. When event-stream framing is already
// committed, it extracts safe error fields and emits a terminal SSE failure
// event instead of mutating headers or appending raw JSON.
func (p *OpenAIResponseOutput) writePassthroughBodyOrSSE(c *gin.Context, resp *http.Response, status int, body []byte, defaultType, defaultMsg string) {
	if CommittedSSE(c) {
		errType, code, message := extractSSEErrorFields(status, body, defaultType, defaultMsg)
		DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, status, errType, code, message, true, false)
		return
	}
	p.writePassthroughBody(c, resp, status, body)
}
