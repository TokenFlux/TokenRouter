package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/wsrelay"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// wsPassthroughAdapter 仅持有本次平台执行凭据、握手参数与单次原语，不拥有 turn/retry 状态。
type wsPassthroughAdapter struct {
	wsUsageDecoder
	service  *OpenAIGatewayService
	request  *gin.Context
	account  *Account
	token    string
	hooks    *OpenAIWSIngressHooks
	decision OpenAIWSProtocolDecision
	router   TLSFingerprintRouterMatchResult
	headers  http.Header
	wsURL    string
	proxyURL string
}

func (p *wsPassthroughAdapter) IsLite(body []byte) bool {
	return isOpenAIResponsesLiteWebSocketPayload(body)
}
func (p *wsPassthroughAdapter) NormalizeLite(body []byte) ([]byte, error) {
	value, _, err := normalizeOpenAIResponsesLitePayloadForAccount(p.account, body)
	return value, err
}
func (p *wsPassthroughAdapter) Reasoning(body []byte, model string) ([]byte, error) {
	return applyOpenAIWSReasoningEffortPolicy(body, p.hooks, model)
}
func (p *wsPassthroughAdapter) Models(turn int, model string, body []byte) (string, string, error) {
	return resolveOpenAIWSTurnModels(p.account, p.hooks, turn, model, body)
}
func (p *wsPassthroughAdapter) AliasTools(body []byte) ([]byte, error) {
	out, reverse, changed, err := aliasOpenAIOAuthReservedToolNamesBody(body)
	if err != nil {
		return nil, err
	}
	setCodexToolNameReverse(p.request, reverse)
	if changed {
		return out, nil
	}
	return body, nil
}
func (p *wsPassthroughAdapter) Compatibility(body []byte, lite bool) ([]byte, bool, error) {
	return normalizeOpenAIResponsesWebSocketCompatibilityBody(body, p.account, lite)
}
func (p *wsPassthroughAdapter) ScopeIdentity(body []byte) ([]byte, bool, error) {
	return applyCodexAccountIdentityClientMetadataRaw(body, codexAccountIdentitySource(p.request, p.account), getAPIKeyIDFromContext(p.request))
}
func (p *wsPassthroughAdapter) FastPolicy(ctx context.Context, turn int, model string, body []byte, scoped bool) ([]byte, *gatewayws.PolicyBlocked, error) {
	if scoped {
		ctx = openAIWSFastModePolicyContext(ctx, p.hooks, turn)
	}
	out, blocked, err := p.service.applyOpenAIFastPolicyToWSResponseCreate(ctx, p.account, model, body)
	if blocked == nil {
		return out, nil, err
	}
	return out, &gatewayws.PolicyBlocked{Message: blocked.Message, Cause: blocked}, err
}
func (p *wsPassthroughAdapter) PromptReplace(ctx context.Context, body []byte) []byte {
	return p.service.ApplyUserPromptReplacement(ctx, body, "openai_responses")
}
func (p *wsPassthroughAdapter) BlockedEvent(blocked *gatewayws.PolicyBlocked) []byte {
	return buildOpenAIFastPolicyBlockedWSEvent(&OpenAIFastBlockedError{Message: blocked.Message})
}
func (p *wsPassthroughAdapter) PolicyDenied() {
	MarkOpsClientBusinessLimited(p.request, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p *wsPassthroughAdapter) SetUpstreamModel(model string) { SetOpsUpstreamModel(p.request, model) }
func (p *wsPassthroughAdapter) PrepareDial(ctx context.Context, body []byte, promptCacheKey string) error {
	wsURL, err := p.service.buildOpenAIResponsesWSURL(p.account)
	if err != nil {
		return fmt.Errorf("build ws url: %w", err)
	}
	wsHost := "-"
	wsPath := "-"
	if parsedURL, parseErr := url.Parse(wsURL); parseErr == nil && parsedURL != nil {
		wsHost = normalizeOpenAIWSLogValue(parsedURL.Host)
		wsPath = normalizeOpenAIWSLogValue(parsedURL.Path)
	}
	logOpenAIWSV2Passthrough(
		"relay_dial_start account_id=%d ws_host=%p.service ws_path=%p.service proxy_enabled=%v",
		p.account.ID,
		wsHost,
		wsPath,
		p.account.ProxyID != nil && p.account.Proxy != nil,
	)

	isCodexCLI := false
	if p.request != nil {
		isCodexCLI = openai.IsCodexOfficialClientByHeaders(p.request.GetHeader("User-Agent"), p.request.GetHeader("originator"))
	}
	if p.service.cfg != nil && p.service.cfg.Gateway.ForceCodexCLI {
		isCodexCLI = true
	}
	turnState := ""
	turnMetadata := ""
	if p.request != nil {
		turnState = strings.TrimSpace(p.request.GetHeader(openAIWSTurnStateHeader))
		turnMetadata = strings.TrimSpace(p.request.GetHeader(openAIWSTurnMetadataHeader))
	}
	headers, _, buildHdrErr := p.service.buildOpenAIWSHeaders(
		ctx,
		p.request,
		p.account,
		p.token,
		p.decision,
		isCodexCLI,
		turnState,
		turnMetadata,
		promptCacheKey,
		gjson.GetBytes(body, "model").String(),
		gjson.GetBytes(body, "service_tier").String(),
		p.router,
	)
	if buildHdrErr != nil {
		return fmt.Errorf("build ws headers: %w", buildHdrErr)
	}
	proxyURL := ""
	if p.account.ProxyID != nil && p.account.Proxy != nil {
		proxyURL = p.account.Proxy.URL()
	}

	dialer := p.service.getOpenAIWSPassthroughDialer()
	if dialer == nil {
		return errors.New("openai ws passthrough dialer is nil")
	}

	p.headers = headers
	p.wsURL = wsURL
	p.proxyURL = proxyURL
	return nil
}
func (p *wsPassthroughAdapter) DialOnce(ctx context.Context) (gatewayws.DialResult, error) {
	headers, err := p.service.refreshOpenAIAgentIdentityHeaders(ctx, p.account, p.headers)
	if err != nil {
		return gatewayws.DialResult{PreparationError: true}, fmt.Errorf("refresh ws authentication headers: %w", err)
	}
	p.headers = headers
	dialCtx, cancel := context.WithTimeout(ctx, p.service.openAIWSDialTimeout())
	conn, status, handshake, err := p.service.getOpenAIWSPassthroughDialer().Dial(dialCtx, p.wsURL, p.headers, p.proxyURL, p.service.resolveOpenAITLSProfile(p.account, p.router))
	cancel()
	result := gatewayws.DialResult{Status: status, Headers: handshake}
	if err != nil {
		var failure *openAIWSHandshakeError
		if errors.As(err, &failure) && failure != nil {
			result.Body = failure.Body
		}
		return result, err
	}
	frames, ok := conn.(openaiwsv2.FrameConn)
	if !ok {
		_ = conn.Close()
		return gatewayws.DialResult{PreparationError: true}, errors.New("openai ws passthrough upstream connection does not support frame relay")
	}
	result.Conn = openAIWSCoreFrames{frames}
	logOpenAIWSV2Passthrough("relay_dial_ok account_id=%d status_code=%d upstream_request_id=%s", p.account.ID, status, openAIWSHeaderValueForLog(handshake, "x-request-id"))
	return result, nil
}
func (p *wsPassthroughAdapter) CanRecover(ctx context.Context, result gatewayws.DialResult, err error) bool {
	failure := &openAIWSDialError{StatusCode: result.Status, ResponseHeaders: result.Headers, ResponseBody: result.Body, Err: err}
	return p.service.isAgentIdentityAccount(ctx, p.account) && isAgentIdentityTaskInvalidWSDialError(failure)
}
func (p *wsPassthroughAdapter) Recover(ctx context.Context) error {
	return p.service.recoverAgentIdentityTask(ctx, p.account, p.account.GetCredential("task_id"))
}
func (p *wsPassthroughAdapter) DialFailure(ctx context.Context, model string, result gatewayws.DialResult, err error) error {
	logOpenAIWSV2Passthrough("relay_dial_failed account_id=%d status_code=%d err=%s", p.account.ID, result.Status, truncateOpenAIWSLogValue(err.Error(), openAIWSLogValueMaxLen))
	failure := &openAIWSDialError{StatusCode: result.Status, ResponseHeaders: cloneHeader(result.Headers), ResponseBody: result.Body, Err: err}
	decision := p.service.handleOpenAIWSDialTransientFailure(ctx, p.account, model, failure)
	if result.Status != 0 && decision.ShouldReturnGenericError() {
		return openAIWSGenericPolicyCloseError(result.Status)
	}
	if result.Status != 0 && decision.ShouldFailoverWithDefaults(p.account, result.Status, result.Status == http.StatusTooManyRequests, p.service.shouldFailoverOpenAIWSError(p.account, result.Status, result.Body)) {
		return newOpenAIUpstreamFailoverError(result.Status, result.Headers, result.Body, extractUpstreamErrorMessage(result.Body), decision.RetryableOnSameAccount(p.account, result.Status))
	}
	return p.service.mapOpenAIWSPassthroughDialError(err, result.Status, result.Headers)
}
func (p *wsPassthroughAdapter) NormalizeCompleted(body []byte) ([]byte, bool) {
	return normalizeCompletedImageGenerationStatus(body)
}
func (p *wsPassthroughAdapter) RestoreTools(body []byte) []byte {
	return restoreCodexToolNamesFromContext(p.request, body)
}
func (p *wsPassthroughAdapter) EventType(body []byte) string {
	event, _, _ := parseOpenAIWSEventEnvelope(body)
	return event
}
func (p *wsPassthroughAdapter) MayContainModel(event string) bool {
	return openAIWSEventMayContainModel(event)
}
func (p *wsPassthroughAdapter) ReplaceModel(body []byte, upstream, requested string) []byte {
	return replaceOpenAIWSMessageModel(body, upstream, requested)
}
func (p *wsPassthroughAdapter) IsTerminal(event string) bool { return isOpenAIWSTerminalEvent(event) }
func (p *wsPassthroughAdapter) NormalizeTerminal(event string) string {
	return normalizeOpenAIWSTerminalEvent(event)
}
func (p *wsPassthroughAdapter) NormalizeTier(tier string) string {
	return normalizeObservedOpenAIServiceTier(tier)
}
func (p *wsPassthroughAdapter) Warning(event string, body []byte) *gatewayws.UpstreamWarning {
	warning := buildOpenAIWSUpstreamWarning(event, body)
	if warning == nil {
		return nil
	}
	return &gatewayws.UpstreamWarning{StatusCode: warning.StatusCode, Message: warning.Message, ResponseBody: warning.ResponseBody}
}
func (p *wsPassthroughAdapter) BeforeWrite(ctx context.Context, routingModel string, payload []byte, wroteDownstream bool, rawHeaders map[string][]string) error {
	handshakeHeaders := http.Header(rawHeaders)
	eventType, _, _ := parseOpenAIWSEventEnvelope(payload)
	if (eventType == "error" || eventType == "response.failed") && markOpenAIWSV2PassthroughCyberPolicy(p.request, payload) {
		return nil
	}
	if eventType == "response.failed" {
		terminalPolicy := p.service.handleOpenAIWSTerminalTransientFailure(ctx, p.account, routingModel, handshakeHeaders, payload)
		if terminalPolicy.Decision.ShouldReturnGenericError() {
			return openAIWSGenericPolicyCloseError(terminalPolicy.StatusCode)
		}
		if !wroteDownstream && terminalPolicy.Decision.ShouldFailoverWithDefaults(
			p.account,
			terminalPolicy.StatusCode,
			false,
			p.service.shouldFailoverOpenAIWSError(p.account, terminalPolicy.StatusCode, payload),
		) {
			return newOpenAIUpstreamFailoverError(
				terminalPolicy.StatusCode,
				handshakeHeaders,
				payload,
				extractOpenAISSEErrorMessage(payload),
				terminalPolicy.Decision.RetryableOnSameAccount(p.account, terminalPolicy.StatusCode),
			)
		}
	}
	if eventType == "error" {
		errorDecision := p.service.handleOpenAIWSErrorEventTransientFailure(ctx, p.account, routingModel, handshakeHeaders, payload)
		if wroteDownstream {
			return nil
		}
		errCodeRaw, errTypeRaw, errMsgRaw := parseOpenAIWSErrorEventFields(payload)
		errorStatus := openAIWSErrorPolicyStatus(payload)
		if errorDecision.ShouldReturnGenericError() {
			return openAIWSGenericPolicyCloseError(errorStatus)
		}
		defaultFailover := p.service.shouldFailoverOpenAIWSError(p.account, errorStatus, payload)
		if errorStatus == 0 || !errorDecision.ShouldFailoverWithDefaults(
			p.account,
			errorStatus,
			errorStatus == http.StatusTooManyRequests,
			defaultFailover,
		) {
			return nil
		}
		logOpenAIWSV2Passthrough(
			"relay_error_failover account_id=%d status=%d err_code=%p.service err_type=%p.service err_message=%p.service",
			p.account.ID,
			errorStatus,
			truncateOpenAIWSLogValue(errCodeRaw, openAIWSLogValueMaxLen),
			truncateOpenAIWSLogValue(errTypeRaw, openAIWSLogValueMaxLen),
			truncateOpenAIWSLogValue(errMsgRaw, openAIWSLogValueMaxLen),
		)
		return newOpenAIUpstreamFailoverError(
			errorStatus,
			handshakeHeaders,
			append([]byte(nil), payload...),
			errMsgRaw,
			errorDecision.RetryableOnSameAccount(p.account, errorStatus),
		)
	}
	return nil
}
func (p *wsPassthroughAdapter) RelayClose(exit gatewayws.RelayExit, completed int) (int, string, bool) {
	status, reason, ok := openAIWSPassthroughRelayClientClose(openaiwsv2.RelayExit{Stage: exit.Stage, Err: exit.Err, Graceful: exit.Graceful, WroteDownstream: exit.WroteDownstream}, completed)
	return int(status), reason, ok
}
func (p *wsPassthroughAdapter) CloseError(status int, reason string, err error) error {
	return NewOpenAIWSClientCloseError(coderws.StatusCode(status), reason, err)
}
func (p *wsPassthroughAdapter) FirstOutputFailure(ctx context.Context, d gatewayws.Deadline, headers map[string][]string) error {
	return p.service.newOpenAIFirstOutputTimeoutError(ctx, p.request, p.account, d.StartedAt, d.RequestModel, d.ReasoningEffort, d.Timeout, "websocket_first_semantic_output", headers)
}
func (p *wsPassthroughAdapter) FirstOutputTimeout(effort string) time.Duration {
	return p.service.openAIFirstOutputTimeout(effort)
}
func (p *wsPassthroughAdapter) Log(message string) { logOpenAIWSV2Passthrough("%s", message) }
func (p *wsPassthroughAdapter) Truncate(message string, limit int) string {
	return truncateOpenAIWSLogValue(message, limit)
}
func (p *wsPassthroughAdapter) ClosedError() error { return errOpenAIWSConnClosed }

func (p *wsPassthroughAdapter) TruncateReason(message string, limit int) string {
	return truncateString(message, limit)
}
