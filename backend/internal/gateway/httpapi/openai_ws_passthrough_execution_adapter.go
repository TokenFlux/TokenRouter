package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	openaicore "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/wsrelay"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// wsPassthroughAdapter 仅持有本次平台执行凭据、握手参数与单次原语，不拥有 turn/retry 状态。
type wsPassthroughAdapter struct {
	gatewayws.RequestUsageDecoder
	service  *OpenAIWebSocketExecutor
	request  *gin.Context
	account  *gatewayprovider.ExecutionAccount
	token    string
	hooks    *gatewayws.OpenAIIngressHooks
	decision egress.OpenAIWSProtocolDecision
	router   egress.TLSFingerprintRouterMatchResult
	headers  http.Header
	wsURL    string
	proxyURL string
}

func (p *wsPassthroughAdapter) IsLite(body []byte) bool {
	return gatewayprovider.ImageIntent().IsOpenAIResponsesLiteWebSocketPayload(body)
}
func (p *wsPassthroughAdapter) NormalizeLite(body []byte) ([]byte, error) {
	value, _, err := gatewayprovider.NormalizeResponsesLiteForAccount(p.account.View(), body)
	return value, err
}
func (p *wsPassthroughAdapter) Reasoning(body []byte, model string) ([]byte, error) {
	return gatewayws.ApplyReasoningEffortPolicy(body, p.hooks, model)
}
func (p *wsPassthroughAdapter) Models(turn int, model string, body []byte) (string, string, error) {
	return resolveOpenAIWSTurnModels(p.account, p.hooks, turn, model, body)
}
func (p *wsPassthroughAdapter) AliasTools(body []byte) ([]byte, error) {
	out, reverse, changed, err := openai.AliasOpenAIOAuthReservedToolNamesBody(body)
	if err != nil {
		return nil, err
	}
	SetCodexToolNameReverse(p.request, reverse)
	if changed {
		return out, nil
	}
	return body, nil
}
func (p *wsPassthroughAdapter) Compatibility(body []byte, lite bool) ([]byte, bool, error) {
	return gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, gatewayprovider.ExecutionProtocolRecord(p.account), lite)
}
func (p *wsPassthroughAdapter) ScopeIdentity(body []byte) ([]byte, bool, error) {
	return openai.ApplyCodexAccountIdentityClientMetadataRaw(body, accountprovider.CodexIdentityNamespace(CodexIdentityRecord(p.request, p.account.View())), APIKeyIDFromContext(p.request))
}
func (p *wsPassthroughAdapter) FastPolicy(ctx context.Context, turn int, model string, body []byte, scoped bool) ([]byte, *gatewayws.PolicyBlocked, error) {
	if scoped {
		ctx = openAIWSFastModePolicyContext(ctx, p.hooks, turn)
	}
	out, blocked, err := gatewayws.ApplyServiceTierFrame(body, model, p.service.FastPolicy.Input(ctx, p.account, model))
	if blocked == nil {
		return out, nil, err
	}
	return out, &gatewayws.PolicyBlocked{Message: blocked.Message, Cause: blocked}, err
}
func (p *wsPassthroughAdapter) PromptReplace(ctx context.Context, body []byte) []byte {
	if p.service == nil {
		return body
	}
	return p.service.Prompts.ApplyUserPromptReplacementToBody(ctx, body, "openai_responses")
}
func (p *wsPassthroughAdapter) BlockedEvent(blocked *gatewayws.PolicyBlocked) []byte {
	return gatewayws.BuildFastPolicyBlockedEvent(&tierpolicy.BlockedError{Message: blocked.Message})
}
func (p *wsPassthroughAdapter) PolicyDenied() {
	MarkOpsClientBusinessLimited(p.request, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p *wsPassthroughAdapter) SetUpstreamModel(model string) {
	SetOpsUpstreamModel(p.request, model)
}
func (p *wsPassthroughAdapter) PrepareDial(ctx context.Context, body []byte, promptCacheKey string) error {
	wsURL, err := p.service.buildOpenAIResponsesWSURL(p.account)
	if err != nil {
		return fmt.Errorf("build ws url: %w", err)
	}
	wsHost := "-"
	wsPath := "-"
	if parsedURL, parseErr := url.Parse(wsURL); parseErr == nil && parsedURL != nil {
		wsHost = gatewayprovider.NormalizeOpenAIWSLogValue(parsedURL.Host)
		wsPath = gatewayprovider.NormalizeOpenAIWSLogValue(parsedURL.Path)
	}
	logOpenAIWSV2Passthrough(
		"relay_dial_start account_id=%d ws_host=%p.service ws_path=%p.service proxy_enabled=%v",
		p.account.Record.ID,
		wsHost,
		wsPath,
		p.account.Record.ProxyID != nil && p.account.Record.Proxy != nil,
	)

	isCodexCLI := false
	if p.request != nil {
		isCodexCLI = openai.IsCodexOfficialClientByHeaders(p.request.GetHeader("User-Agent"), p.request.GetHeader("originator"))
	}
	if p.service.Options != nil && p.service.Requests.Options.ForceCLI {
		isCodexCLI = true
	}
	turnState := ""
	turnMetadata := ""
	if p.request != nil {
		turnState = strings.TrimSpace(p.request.GetHeader(openAIWSTurnStateHeader))
		turnMetadata = strings.TrimSpace(p.request.GetHeader(openai.WSTurnMetadataHeader))
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
	if p.account.Record.ProxyID != nil && p.account.Record.Proxy != nil {
		proxyURL = p.account.Record.Proxy.URL()
	}

	dialer := p.service.Connections.Dialer()
	if dialer == nil {
		return errors.New("openai ws passthrough dialer is nil")
	}

	p.headers = headers
	p.wsURL = wsURL
	p.proxyURL = proxyURL
	return nil
}
func (p *wsPassthroughAdapter) DialOnce(ctx context.Context) (gatewayws.DialResult, error) {
	headers, err := p.service.Requests.Identity.RefreshHeaders(ctx, p.account, p.headers)
	if err != nil {
		return gatewayws.DialResult{PreparationError: true}, fmt.Errorf("refresh ws authentication headers: %w", err)
	}
	p.headers = headers
	dialCtx, cancel := context.WithTimeout(ctx, p.service.openAIWSDialTimeout())
	conn, status, handshake, err := p.service.Connections.Dialer().Dial(dialCtx, p.wsURL, p.headers, p.proxyURL, p.service.Requests.TLSProfile(p.account, p.router))
	cancel()
	result := gatewayws.DialResult{Status: status, Headers: handshake}
	if err != nil {
		var failure *openai.WSHandshakeError
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
	logOpenAIWSV2Passthrough("relay_dial_ok account_id=%d status_code=%d upstream_request_id=%s", p.account.Record.ID, status, gatewayprovider.OpenAIWSHeaderValueForLog(handshake, "x-request-id"))
	return result, nil
}
func (p *wsPassthroughAdapter) CanRecover(ctx context.Context, result gatewayws.DialResult, err error) bool {
	failure := &openai.WSDialError{StatusCode: result.Status, ResponseHeaders: result.Headers, ResponseBody: result.Body, Err: err}
	return p.service.Requests.Identity.UsesAgentIdentity(ctx, p.account) && openai.IsAgentTaskInvalidWSDialError(failure)
}
func (p *wsPassthroughAdapter) Recover(ctx context.Context) error {
	return p.service.Requests.Identity.Recover(ctx, p.account, p.account.View().GetCredential("task_id"))
}
func (p *wsPassthroughAdapter) DialFailure(ctx context.Context, model string, result gatewayws.DialResult, err error) error {
	logOpenAIWSV2Passthrough("relay_dial_failed account_id=%d status_code=%d err=%s", p.account.Record.ID, result.Status, gatewayprovider.TruncateOpenAIWSLogValue(err.Error(), gatewayprovider.OpenAIWSLogValueMaxLen))
	failure := &openai.WSDialError{StatusCode: result.Status, ResponseHeaders: upstreamcore.CloneHeader(result.Headers), ResponseBody: result.Body, Err: err}
	decision := p.service.handleOpenAIWSDialTransientFailure(ctx, p.account, model, failure)
	if result.Status != 0 && decision.ShouldReturnGenericError() {
		return openAIWSGenericPolicyCloseError(result.Status)
	}
	if result.Status != 0 && decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(p.account), result.Status, result.Status == http.StatusTooManyRequests, p.service.shouldFailoverOpenAIWSError(p.account, result.Status, result.Body)) {
		return gatewayprovider.NewOpenAIUpstreamFailure(result.Status, result.Headers, result.Body, upstreamcore.ExtractErrorMessage(result.Body), decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(p.account), result.Status))
	}
	return p.service.mapOpenAIWSPassthroughDialError(err, result.Status, result.Headers)
}
func (p *wsPassthroughAdapter) NormalizeCompleted(body []byte) ([]byte, bool) {
	return openaicore.NormalizeCompletedImageGenerationStatus(body)
}
func (p *wsPassthroughAdapter) RestoreTools(body []byte) []byte {
	return RestoreCodexToolNamesFromContext(p.request, body)
}
func (p *wsPassthroughAdapter) EventType(body []byte) string {
	event, _, _ := openaicore.ParseWSEventEnvelope(body)
	return event
}
func (p *wsPassthroughAdapter) MayContainModel(event string) bool {
	return openaicore.WSEventMayContainModel(event)
}
func (p *wsPassthroughAdapter) ReplaceModel(body []byte, upstream, requested string) []byte {
	return openaicore.ReplaceWSMessageModel(body, upstream, requested)
}
func (p *wsPassthroughAdapter) IsTerminal(event string) bool {
	return openaicore.IsWSTerminalEvent(event)
}
func (p *wsPassthroughAdapter) NormalizeTerminal(event string) string {
	return normalizeOpenAIWSTerminalEvent(event)
}
func (p *wsPassthroughAdapter) NormalizeTier(tier string) string {
	return forwardcore.NormalizeObservedOpenAIServiceTier(tier)
}
func (p *wsPassthroughAdapter) Warning(event string, body []byte) *forwardcore.UpstreamWarning {
	warning := buildOpenAIWSUpstreamWarning(event, body)
	if warning == nil {
		return nil
	}
	return &forwardcore.UpstreamWarning{StatusCode: warning.StatusCode, Message: warning.Message, ResponseBody: warning.ResponseBody}
}
func (p *wsPassthroughAdapter) BeforeWrite(ctx context.Context, routingModel string, payload []byte, wroteDownstream bool, rawHeaders map[string][]string) error {
	handshakeHeaders := http.Header(rawHeaders)
	eventType, _, _ := openaicore.ParseWSEventEnvelope(payload)
	if (eventType == "error" || eventType == "response.failed") && markOpenAIWSV2PassthroughCyberPolicy(p.request, payload) {
		return nil
	}
	if eventType == "response.failed" {
		terminalPolicy := p.service.handleOpenAIWSTerminalTransientFailure(ctx, p.account, routingModel, handshakeHeaders, payload)
		if terminalPolicy.Decision.ShouldReturnGenericError() {
			return openAIWSGenericPolicyCloseError(terminalPolicy.StatusCode)
		}
		if !wroteDownstream && terminalPolicy.Decision.ShouldFailoverWithDefaults(
			gatewayprovider.ExecutionErrorPolicy(p.account),
			terminalPolicy.StatusCode,
			false,
			p.service.shouldFailoverOpenAIWSError(p.account, terminalPolicy.StatusCode, payload),
		) {
			return gatewayprovider.NewOpenAIUpstreamFailure(
				terminalPolicy.StatusCode,
				handshakeHeaders,
				payload,
				openai.ExtractOpenAISSEErrorMessage(payload),
				terminalPolicy.Decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(p.account), terminalPolicy.StatusCode),
			)
		}
	}
	if eventType == "error" {
		errorDecision := p.service.handleOpenAIWSErrorEventTransientFailure(ctx, p.account, routingModel, handshakeHeaders, payload)
		if wroteDownstream {
			return nil
		}
		errCodeRaw, errTypeRaw, errMsgRaw := openaicore.ParseWSErrorEventFields(payload)
		errorStatus := openAIWSErrorPolicyStatus(payload)
		if errorDecision.ShouldReturnGenericError() {
			return openAIWSGenericPolicyCloseError(errorStatus)
		}
		defaultFailover := p.service.shouldFailoverOpenAIWSError(p.account, errorStatus, payload)
		if errorStatus == 0 || !errorDecision.ShouldFailoverWithDefaults(
			gatewayprovider.ExecutionErrorPolicy(p.account),
			errorStatus,
			errorStatus == http.StatusTooManyRequests,
			defaultFailover,
		) {
			return nil
		}
		logOpenAIWSV2Passthrough(
			"relay_error_failover account_id=%d status=%d err_code=%p.service err_type=%p.service err_message=%p.service",
			p.account.Record.ID,
			errorStatus, gatewayprovider.TruncateOpenAIWSLogValue(errCodeRaw, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(errTypeRaw, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(errMsgRaw, gatewayprovider.OpenAIWSLogValueMaxLen),
		)
		return gatewayprovider.NewOpenAIUpstreamFailure(
			errorStatus,
			handshakeHeaders,
			append([]byte(nil), payload...),
			errMsgRaw,
			errorDecision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(p.account), errorStatus),
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
	return p.service.Output.FirstOutputFailure(ctx, p.request, p.account, d.StartedAt, d.RequestModel, d.ReasoningEffort, d.Timeout, "websocket_first_semantic_output", headers)
}
func (p *wsPassthroughAdapter) FirstOutputTimeout(effort string) time.Duration {
	return p.service.Output.FirstOutputTimeout(effort)
}
func (p *wsPassthroughAdapter) Log(message string) { logOpenAIWSV2Passthrough("%s", message) }
func (p *wsPassthroughAdapter) Truncate(message string, limit int) string {
	return gatewayprovider.TruncateOpenAIWSLogValue(message, limit)
}
func (p *wsPassthroughAdapter) ClosedError() error { return openai.ErrWSConnClosed }

func (p *wsPassthroughAdapter) TruncateReason(message string, limit int) string {
	return logredact.TruncateUTF8(message, limit)
}
