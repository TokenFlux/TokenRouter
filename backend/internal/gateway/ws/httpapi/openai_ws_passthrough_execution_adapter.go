package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	openaicore "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws/relay"
)

// openai_ws_passthrough_execution_adapter.go adapts dedicated WebSocket
// requests and upstream failures to provider execution policy.
// Dedicated Responses WS sessions prepare provider-scoped request frames
// before forwarding them and inspect failure events before client delivery.
// Authentication headers are refreshed for each dial attempt, while the
// session core owns any single Agent Identity repair and decides whether an
// initial failure can select another provider.
//
// Model and tool restoration share the HTTP request's captured state. Provider
// policy runs before close or failover presentation; response.failed can
// select generic closure after earlier output, while a standalone error after
// output keeps the frame for relay delivery. Cyber-marked error events bypass
// ordinary provider policy. Timeout adapters delegate observation and health
// effects to the shared output service before the core chooses first-turn
// failover or later-turn reconnect.

// wsPassthroughAdapter holds platform credentials, handshake parameters, and execution operations.
type wsPassthroughAdapter struct {
	attributeContext              context.Context                        // current turn attribute snapshot context
	gatewayws.RequestUsageDecoder                                        // shared request metadata extraction rules
	service                       *OpenAIWebSocketExecutor               // shared WS execution dependencies
	request                       *gin.Context                           // upgraded Gin request context
	provider                      *gatewayprovider.ExecutionProvider     // selected provider execution target
	token                         string                                 // resolved bearer request credential
	hooks                         *gatewayws.OpenAIIngressHooks          // optional turn lifecycle callbacks
	decision                      egress.OpenAIWSProtocolDecision        // resolved upstream transport decision
	router                        egress.TLSFingerprintRouterMatchResult // captured TLS fingerprint router match
	// headers, wsURL and proxyURL are published together by successful PrepareDial
	// before DialOnce. Each attempt refreshes headers for authentication; endpoint
	// and proxy remain the prepared selection for that dedicated session.
	headers  http.Header // prepared upstream handshake header values
	wsURL    string      // prepared upstream WebSocket endpoint
	proxyURL string      // prepared upstream proxy endpoint
}

// ModelAttributes prepares request parameters for the resolved upstream model
// and logical turn context. ContextForTurn takes precedence when present;
// absent request readers leave the attribute service nil, and the provider
// helper reports any model-parameter rejection.
func (p *wsPassthroughAdapter) ModelAttributes(ctx context.Context, turn int, model string, body []byte) ([]byte, error) {
	if p.hooks != nil && p.hooks.ContextForTurn != nil {
		ctx = p.hooks.ContextForTurn(turn)
	}
	var service *routing.ModelAttributeService
	if p.service.Requests != nil && p.service.Requests.Readers != nil {
		service = p.service.Requests.Readers.Attributes
	}
	if routing.ModelAttributesFromContext(ctx) == nil {
		ctx = routing.WithModelAttributes(ctx, service)
	}
	p.attributeContext = ctx
	return gatewayprovider.PrepareModelAttributeParameters(ctx, p.provider, model, body, service)
}

// StripOutboundEffort removes wire reasoning effort fields from an outbound
// WebSocket payload when the selected provider enables
// effort_model_mapping_strip_effort and an effort-scoped model override matched.
// Callers invoke this after usage metadata and failover snapshots capture the
// pre-strip bytes so accounting and replacement providers keep intact effort.
func (p *wsPassthroughAdapter) StripOutboundEffort(ctx context.Context, routingModel string, body []byte) []byte {
	if p == nil || p.provider == nil || len(body) == 0 {
		return body
	}
	effort := requeststate.ExtractRoutingReasoningEffort(body, protocol.ProtocolResponsesWebSocket)
	sourceModel := strings.TrimSpace(routingModel)
	if sourceModel == "" {
		if plan, ok := requeststate.RoutePlanFromContext(ctx); ok {
			if planned := plan.Models().GroupMappedModel; planned != "" {
				sourceModel = planned
			} else if requested := plan.Models().RequestedModel; requested != "" {
				sourceModel = requested
			}
		}
	}
	if sourceModel == "" {
		sourceModel = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	if !gatewayprovider.ExecutionModelPolicy(p.provider).WithEffort(effort).ShouldStripWireEffort(ctx, sourceModel) {
		return body
	}
	return requeststate.StripWireReasoningEffort(body, protocol.ProtocolResponsesWebSocket)
}

// IsLite reports whether this client frame requests Responses Lite handling.
func (p *wsPassthroughAdapter) IsLite(body []byte) bool {
	return gatewayprovider.ImageIntent().IsOpenAIResponsesLiteWebSocketPayload(body)
}

// NormalizeLite returns provider-specific Responses Lite fields and any
// normalization error. It discards the helper's changed flag because its
// caller replaces the result directly after success rather than deciding
// replacement from that flag.
func (p *wsPassthroughAdapter) NormalizeLite(body []byte) ([]byte, error) {
	value, _, err := gatewayprovider.NormalizeResponsesLiteForProvider(p.provider.View(), body)
	return value, err
}

// Reasoning applies the connection's inherited group effort rules to this
// model and request body. Nil hooks leave the body unchanged; the shared WS
// helper returns policy rejection before the frame is admitted upstream.
func (p *wsPassthroughAdapter) Reasoning(body []byte, model string) ([]byte, error) {
	return gatewayws.ApplyReasoningEffortPolicy(body, p.hooks, model)
}

// Models returns group-eligible routing and provider-normalized upstream
// models for this turn. The supplied model is the retained client choice; the
// routing hook can reject eligibility before provider mapping changes the
// upstream spelling.
func (p *wsPassthroughAdapter) Models(turn int, model string, body []byte) (string, string, error) {
	return resolveOpenAIWSTurnModels(p.provider, p.hooks, turn, model, body)
}

// AliasTools rewrites OAuth-reserved tool names and stores the response
// reverse map in the Gin request. An aliasing error stops before publication;
// an unchanged result returns the original body while still replacing the
// reverse-map state with the helper's result.
func (p *wsPassthroughAdapter) AliasTools(body []byte) ([]byte, error) {
	out, reverse, changed, err := openai.AliasOpenAIOAuthReservedToolNamesBody(body)
	if err != nil {
		return nil, err
	}
	gatewayhttp.SetCodexToolNameReverse(p.request, reverse)
	if changed {
		return out, nil
	}
	return body, nil
}

// Compatibility normalizes this frame for the selected provider's Responses
// protocol and Lite flag. Its changed result controls replacement in the
// caller; errors reject the local frame before upstream execution.
func (p *wsPassthroughAdapter) Compatibility(body []byte, lite bool) ([]byte, bool, error) {
	return gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, gatewayprovider.ExecutionProtocolRecord(p.provider), lite)
}

// ScopeIdentity applies Key and provider namespace to Codex client metadata
// and reports whether bytes changed. The caller retains pre-scope bytes
// separately so a replacement provider can derive its own namespace during
// replay.
func (p *wsPassthroughAdapter) ScopeIdentity(body []byte) ([]byte, bool, error) {
	return openai.ApplyCodexProviderIdentityClientMetadataRaw(body, provideradapter.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(p.request, p.provider.View())), gatewayhttp.APIKeyIDFromContext(p.request))
}

// FastPolicy applies selected-provider service-tier rules and returns a
// separate policy-blocked result when rejected. Scoped calls first bind the
// turn's refreshed Key policy; other calls retain the supplied context. A
// blocked result keeps its cause for client presentation while encoding errors
// remain ordinary errors.
func (p *wsPassthroughAdapter) FastPolicy(ctx context.Context, turn int, model string, body []byte, scoped bool) ([]byte, *gatewayws.PolicyBlocked, error) {
	if scoped {
		ctx = openAIWSFastModePolicyContext(ctx, p.hooks, turn)
	}
	out, blocked, err := gatewayws.ApplyServiceTierFrame(body, model, p.service.FastPolicy.Input(ctx, p.provider, model))
	if blocked == nil {
		return out, nil, err
	}
	return out, &gatewayws.PolicyBlocked{Message: blocked.Message, Cause: blocked}, err
}

// PromptReplace applies user Responses prompt replacements through the bound
// service. Missing executor returns the supplied body unchanged; the caller
// decides whether this frame or retry should request prompt replacement at
// all.
func (p *wsPassthroughAdapter) PromptReplace(ctx context.Context, body []byte) []byte {
	if p.service == nil {
		return body
	}
	return p.service.Prompts.ApplyUserPromptReplacementToBody(ctx, body, "openai_responses")
}

// BlockedEvent renders the supplied nonnil service-tier rejection as a WS error
// event. The controller retains Cause separately for its returned close
// instruction; this event is built from the rejection message.
func (p *wsPassthroughAdapter) BlockedEvent(blocked *gatewayws.PolicyBlocked) []byte {
	return gatewayws.BuildFastPolicyBlockedEvent(&tierpolicy.BlockedError{Message: blocked.Message})
}

// PolicyDenied marks local service-tier rejection on this request's Ops
// observations.
func (p *wsPassthroughAdapter) PolicyDenied() {
	gatewayhttp.MarkOpsClientBusinessLimited(p.request, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}

// SetUpstreamModel publishes the resolved first-turn upstream model to the Gin
// Ops observation.
func (p *wsPassthroughAdapter) SetUpstreamModel(model string) {
	gatewayhttp.SetOpsUpstreamModel(p.request, model)
}

// PrepareDial prepares a dedicated endpoint, handshake headers and proxy
// selection from the normalized first request. Endpoint or header failure
// returns before fields are published; a missing dialer also prevents success.
// The first model, tier, cache key and inbound continuation state determine
// handshake identity and routing.
//
// The resulting headers, URL and proxy are retained together for DialOnce.
// Identity authentication is refreshed there immediately before dialing, so
// preparation can retain stable session headers while task assertion changes
// between attempts. Preparation requires initialized executor services and a
// provider; the core owns the resulting transport and performs any later
// cleanup.
func (p *wsPassthroughAdapter) PrepareDial(ctx context.Context, body []byte, promptCacheKey string) error {
	wsURL, err := p.service.buildOpenAIResponsesWSURL(p.provider)
	if err != nil {
		return fmt.Errorf("build ws url: %w", err)
	}
	wsHost := "-"
	wsPath := "-"
	// This parse supplies diagnostic labels after endpoint preparation; a failure
	// keeps placeholders while the prepared dial URL remains available for execution.
	if parsedURL, parseErr := url.Parse(wsURL); parseErr == nil && parsedURL != nil {
		wsHost = gatewayprovider.NormalizeOpenAIWSLogValue(parsedURL.Host)
		wsPath = gatewayprovider.NormalizeOpenAIWSLogValue(parsedURL.Path)
	}
	logOpenAIWSV2Passthrough(
		"relay_dial_start provider_id=%d ws_host=%s ws_path=%s proxy_enabled=%v",
		p.provider.Record.ID,
		wsHost,
		wsPath,
		p.provider.Record.ProxyID != nil && p.provider.Record.Proxy != nil,
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
		p.provider,
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
	proxyURL := "" // prepared upstream proxy endpoint
	if p.provider.Record.ProxyID != nil && p.provider.Record.Proxy != nil {
		proxyURL = p.provider.Record.Proxy.URL()
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

// DialOnce refreshes authentication headers and dials under the captured
// handshake timeout. Refresh failure returns PreparationError before dialing;
// other handshake errors retain status, headers and an available
// WSHandshakeError body. Success requires a frame-capable transport. An
// incompatible connection is closed and returned as a preparation failure,
// while a compatible connection is transferred to the session core for relay
// and final close.
func (p *wsPassthroughAdapter) DialOnce(ctx context.Context) (gatewayws.DialResult, error) {
	headers, err := p.service.Requests.Identity.RefreshHeaders(ctx, p.provider, p.headers)
	if err != nil {
		return gatewayws.DialResult{PreparationError: true}, fmt.Errorf("refresh ws authentication headers: %w", err)
	}
	p.headers = headers
	dialCtx, cancel := context.WithTimeout(ctx, p.service.openAIWSDialTimeout())
	conn, status, handshake, err := p.service.Connections.Dialer().Dial(dialCtx, p.wsURL, p.headers, p.proxyURL, p.service.Requests.TLSProfile(p.provider, p.router))
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
		// A successful dial that lacks frame operations cannot enter the relay;
		// close that acquired transport before returning its incompatible preparation result.
		_ = conn.Close()
		return gatewayws.DialResult{PreparationError: true}, errors.New("openai ws passthrough upstream connection does not support frame relay")
	}
	result.Conn = openAIWSCoreFrames{frames}
	logOpenAIWSV2Passthrough("relay_dial_ok provider_id=%d status_code=%d upstream_request_id=%s", p.provider.Record.ID, status, gatewayprovider.OpenAIWSHeaderValueForLog(handshake, "x-request-id"))
	return result, nil
}

// CanRecover reports an Agent Identity task-invalid handshake eligible for the
// core's single repair. It constructs dial classification from the supplied
// result and error; actual repair and retry ordering remain owned by
// PassthroughSession.
func (p *wsPassthroughAdapter) CanRecover(ctx context.Context, result gatewayws.DialResult, err error) bool {
	failure := &openai.WSDialError{StatusCode: result.Status, ResponseHeaders: result.Headers, ResponseBody: result.Body, Err: err}
	return p.service.Requests.Identity.UsesAgentIdentity(ctx, p.provider) && openai.IsAgentTaskInvalidWSDialError(failure)
}

// Recover invokes provider Agent Identity repair using the supplied context
// and current task_id. The session marks its one-recovery flag before calling
// and attempts another dial only if this operation returns success.
func (p *wsPassthroughAdapter) Recover(ctx context.Context) error {
	return p.service.Requests.Identity.Recover(ctx, p.provider, p.provider.View().GetCredential("task_id"))
}

// DialFailure applies handshake provider policy and returns generic closure,
// provider failover, or mapped transport failure. err must be nonnil because
// diagnostics format its message. Status, cloned response headers and retained
// body feed the health decision before presentation.
//
// A generic policy decision produces an internal-error close instruction. A
// status-bearing eligible decision produces a typed upstream failure with its
// same-provider retry flag; remaining failures pass through cancellation,
// timeout, busy and authentication close mapping. The caller invokes this
// after its credential-recovery opportunity, so the selected presentation
// describes the final initial dial failure.
func (p *wsPassthroughAdapter) DialFailure(ctx context.Context, model string, result gatewayws.DialResult, err error) error {
	logOpenAIWSV2Passthrough("relay_dial_failed provider_id=%d status_code=%d err=%s", p.provider.Record.ID, result.Status, gatewayprovider.TruncateOpenAIWSLogValue(err.Error(), gatewayprovider.OpenAIWSLogValueMaxLen))
	failure := &openai.WSDialError{StatusCode: result.Status, ResponseHeaders: upstreamcore.CloneHeader(result.Headers), ResponseBody: result.Body, Err: err}
	decision := p.service.handleOpenAIWSDialTransientFailure(ctx, p.provider, model, failure)
	if result.Status != 0 && decision.ShouldReturnGenericError() {
		return openAIWSGenericPolicyCloseError(result.Status)
	}
	if result.Status != 0 && decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(p.provider), result.Status, result.Status == http.StatusTooManyRequests, p.service.shouldFailoverOpenAIWSError(p.provider, result.Status, result.Body)) {
		return gatewayprovider.NewOpenAIUpstreamFailure(result.Status, result.Headers, result.Body, upstreamcore.ExtractErrorMessage(result.Body), decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(p.provider), result.Status))
	}
	return p.service.mapOpenAIWSPassthroughDialError(err, result.Status, result.Headers)
}

// NormalizeCompleted repairs image-generation status fields in a completed
// response and reports whether bytes changed.
func (p *wsPassthroughAdapter) NormalizeCompleted(body []byte) ([]byte, bool) {
	return openaicore.NormalizeCompletedImageGenerationStatus(body)
}

// RestoreTools restores reserved Codex tool names from this request's captured
// reverse map.
func (p *wsPassthroughAdapter) RestoreTools(body []byte) []byte {
	return gatewayhttp.RestoreCodexToolNamesFromContext(p.request, body)
}

// EventType returns the OpenAI WS event label parsed from this frame.
func (p *wsPassthroughAdapter) EventType(body []byte) string {
	event, _, _ := openaicore.ParseWSEventEnvelope(body)
	return event
}

// MayContainModel reports whether this event label can carry protocol model
// metadata.
func (p *wsPassthroughAdapter) MayContainModel(event string) bool {
	return openaicore.WSEventMayContainModel(event)
}

// ReplaceModel rewrites matching root or response model fields to the
// requested model. Blank or unmatched models retain the supplied bytes;
// protocol patching is best effort and does not rewrite arbitrary text
// content.
func (p *wsPassthroughAdapter) ReplaceModel(body []byte, upstream, requested string) []byte {
	return openaicore.ReplaceWSMessageModel(body, upstream, requested)
}

// IsTerminal reports whether the protocol recognizes this event as response
// completion.
func (p *wsPassthroughAdapter) IsTerminal(event string) bool {
	return openaicore.IsWSTerminalEvent(event)
}

// NormalizeTerminal returns the shared canonical response terminal label,
// folding canceled into cancelled. Unknown or nonterminal labels return empty,
// so the controller can keep the current turn active while inspecting later
// frames.
func (p *wsPassthroughAdapter) NormalizeTerminal(event string) string {
	return normalizeOpenAIWSTerminalEvent(event)
}

// NormalizeTier canonicalizes observed service-tier evidence for reporting and
// completion. It follows ResponseObserver's accepted labels, maps fast to
// priority, and returns empty for absent or unrecognized tier evidence.
func (p *wsPassthroughAdapter) NormalizeTier(tier string) string {
	return forwardcore.NormalizeObservedOpenAIServiceTier(tier)
}

// Warning returns copied-body observations for error, failed and incomplete
// event frames. Other labels or empty bytes return nil; callers retain the
// warning before any later client model or capacity-code presentation.
func (p *wsPassthroughAdapter) Warning(event string, body []byte) *forwardcore.UpstreamWarning {
	warning := buildOpenAIWSUpstreamWarning(event, body)
	if warning == nil {
		return nil
	}
	return &forwardcore.UpstreamWarning{StatusCode: warning.StatusCode, Message: warning.Message, ResponseBody: warning.ResponseBody}
}

// BeforeWrite inspects provider failure policy before a relay frame is
// delivered to the client. Cyber-marked error or failed events return nil
// after recording Cyber evidence. A failed terminal applies provider policy
// and can return generic closure regardless of prior output; failover for that
// terminal requires no prior downstream write.
//
// Standalone errors also apply health policy first, but an already-output
// error returns nil before generic or failover selection. Initial errors with
// eligible status produce a typed upstream failure and retain response
// diagnostics. These distinct progress gates let the relay present later error
// frames while preventing replacement from joining another provider's output
// to an already delivered turn.
func (p *wsPassthroughAdapter) BeforeWrite(ctx context.Context, routingModel string, payload []byte, wroteDownstream bool, rawHeaders map[string][]string) error {
	handshakeHeaders := http.Header(rawHeaders)
	eventType, _, _ := openaicore.ParseWSEventEnvelope(payload)
	if (eventType == "error" || eventType == "response.failed") && markOpenAIWSV2PassthroughCyberPolicy(p.request, payload) {
		return nil
	}
	if eventType == "response.failed" {
		terminalPolicy := p.service.handleOpenAIWSTerminalTransientFailure(ctx, p.provider, routingModel, handshakeHeaders, payload)
		if terminalPolicy.Decision.ShouldReturnGenericError() {
			return openAIWSGenericPolicyCloseError(terminalPolicy.StatusCode)
		}
		if !wroteDownstream && terminalPolicy.Decision.ShouldFailoverWithDefaults(
			gatewayprovider.ExecutionErrorPolicy(p.provider),
			terminalPolicy.StatusCode,
			false,
			p.service.shouldFailoverOpenAIWSError(p.provider, terminalPolicy.StatusCode, payload),
		) {
			return gatewayprovider.NewOpenAIUpstreamFailure(
				terminalPolicy.StatusCode,
				handshakeHeaders,
				payload,
				openai.ExtractOpenAISSEErrorMessage(payload),
				terminalPolicy.Decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(p.provider), terminalPolicy.StatusCode),
			)
		}
	}
	// Standalone errors still update provider policy after output; the following
	// progress gate then retains the frame for client delivery instead of failover.
	if eventType == "error" {
		errorDecision := p.service.handleOpenAIWSErrorEventTransientFailure(ctx, p.provider, routingModel, handshakeHeaders, payload)
		if wroteDownstream {
			return nil
		}
		errCodeRaw, errTypeRaw, errMsgRaw := openaicore.ParseWSErrorEventFields(payload)
		errorStatus := openAIWSErrorPolicyStatus(payload)
		if errorDecision.ShouldReturnGenericError() {
			return openAIWSGenericPolicyCloseError(errorStatus)
		}
		defaultFailover := p.service.shouldFailoverOpenAIWSError(p.provider, errorStatus, payload)
		if errorStatus == 0 || !errorDecision.ShouldFailoverWithDefaults(
			gatewayprovider.ExecutionErrorPolicy(p.provider),
			errorStatus,
			errorStatus == http.StatusTooManyRequests,
			defaultFailover,
		) {
			return nil
		}
		logOpenAIWSV2Passthrough(
			"relay_error_failover provider_id=%d status=%d err_code=%s err_type=%s err_message=%s",
			p.provider.Record.ID,
			errorStatus, gatewayprovider.TruncateOpenAIWSLogValue(errCodeRaw, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(errTypeRaw, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(errMsgRaw, gatewayprovider.OpenAIWSLogValueMaxLen),
		)
		return gatewayprovider.NewOpenAIUpstreamFailure(
			errorStatus,
			handshakeHeaders,
			append([]byte(nil), payload...),
			errMsgRaw,
			errorDecision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(p.provider), errorStatus),
		)
	}
	return nil
}

// RelayClose maps the relay exit and completed-turn count to an optional
// client close instruction. The helper distinguishes explicit closes, active
// read timeout, first-output timeout and failed upstream reads; false lets the
// core use its remaining exit handling.
func (p *wsPassthroughAdapter) RelayClose(exit gatewayws.RelayExit, completed int) (int, string, bool) {
	status, reason, ok := openAIWSPassthroughRelayClientClose(openaiwsv2.RelayExit{Stage: exit.Stage, Err: exit.Err, Graceful: exit.Graceful, WroteDownstream: exit.WroteDownstream}, completed)
	return int(status), reason, ok
}

// CloseError constructs the HTTP adapter's WS close instruction around the
// supplied cause.
func (p *wsPassthroughAdapter) CloseError(status int, reason string, err error) error {
	return gatewayhttp.NewOpenAIWSClientCloseError(coderws.StatusCode(status), reason, err)
}

// FirstOutputFailure delegates timeout observation and provider feedback to
// the shared output service using this deadline's turn metadata. It returns
// the resulting failure; the session separately selects initial failover
// versus reconnect presentation after those side effects occur.
func (p *wsPassthroughAdapter) FirstOutputFailure(ctx context.Context, d gatewayws.Deadline, headers map[string][]string) error {
	return p.service.Output.FirstOutputFailure(ctx, p.request, p.provider, d.StartedAt, d.RequestModel, d.ReasoningEffort, d.Timeout, "websocket_first_semantic_output", headers)
}

// FirstOutputTimeout returns the shared output service's budget for this
// reasoning effort label. Unconfigured or nonpositive base settings return
// zero; high, xhigh and max use a positive high-effort override when available.
func (p *wsPassthroughAdapter) FirstOutputTimeout(effort string) time.Duration {
	return p.service.Output.FirstOutputTimeout(effort)
}

// Log sends dedicated relay diagnostics through the WS passthrough logger.
func (p *wsPassthroughAdapter) Log(message string) { logOpenAIWSV2Passthrough("%s", message) }

// Truncate normalizes diagnostic text and retains at most limit source bytes
// before adding an ellipsis. Nonpositive limits keep normalized text intact;
// byte truncation can split a UTF-8 encoding.
func (p *wsPassthroughAdapter) Truncate(message string, limit int) string {
	return gatewayprovider.TruncateOpenAIWSLogValue(message, limit)
}

// ClosedError returns the upstream connection-closed sentinel for absent relay
// transport.
func (p *wsPassthroughAdapter) ClosedError() error { return openai.ErrWSConnClosed }

// TruncateReason limits close text by bytes without splitting a UTF-8
// character. A nonpositive limit returns empty, text within the limit is
// unchanged, and this helper retains surrounding whitespace.
func (p *wsPassthroughAdapter) TruncateReason(message string, limit int) string {
	return logredact.TruncateUTF8(message, limit)
}
