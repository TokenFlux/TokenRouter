package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_response_stream_errors.go classifies Responses stream failures and
// carries health evidence into failover errors.
// Responses terminal failures carry status evidence inside HTTP 200 streams,
// so health and presentation inspect the event payload. Request-specific
// parameter, context-window and cyber rejections remain distinct from
// credential or provider failures, and final presentation can use normalized
// error fields rather than the transport status.
//
// A converted reader may already have applied health before constructing its
// failover error. NewStreamPolicyFailureWithModel accepts that applied-effects
// evidence directly and otherwise performs terminal effects itself.
// Retained response headers support retry and request identifiers, while
// semantic 429 classification avoids treating successful stream-establishment
// quota headers as fresh global failure evidence. Caller-visible error bodies
// retain classified credential or transient evidence and rebuild other
// failures into a generic protocol envelope.

// AppliedOpenAIResponseEffects carries provider effects already evaluated for
// the terminal event supplied to the failure builder.
type AppliedOpenAIResponseEffects struct {
	// StatusCode preserves the applied policy status. Values below HTTP 400 make
	// the builder infer status from the payload without repeating provider effects.
	StatusCode int
	// StopScheduling carries the health decision for this event. It prevents the
	// builder from granting OAuth 429 retries when scheduling was already disabled.
	StopScheduling bool
}

// writeSanitizedOpenAIPassthroughError delegates status-based fixed client
// messages with Compact SSE support. Its callback can terminate an
// already-started Compact response; otherwise the shared envelope writer emits
// JSON with validated retry headers.
func writeSanitizedOpenAIPassthroughError(c *gin.Context, upstreamStatus int, upstreamHeaders http.Header) {
	WriteSanitizedForwardPassthroughError(c, upstreamStatus, upstreamHeaders, func(c *gin.Context, status int, body []byte) bool {
		return WriteOpenAICompactSSEBridge(c, status, body, MarkOpsStreamError)
	})
}

// writeOpenAIPassthroughErrorEnvelope writes the chosen message using Compact
// or JSON presentation. It supplies the Compact bridge callback required by
// the shared envelope helper, which checks that format before committing
// ordinary JSON output.
func writeOpenAIPassthroughErrorEnvelope(c *gin.Context, downstreamStatus int, upstreamHeaders http.Header, message string) {
	WriteForwardPassthroughErrorEnvelope(c, downstreamStatus, upstreamHeaders, message, func(c *gin.Context, status int, body []byte) bool {
		return WriteOpenAICompactSSEBridge(c, status, body, MarkOpsStreamError)
	})
}

// PassthroughFailoverError applies health to a passthrough failure selected
// for recovery and returns its final error. Callers supply already-captured
// responseBody, the selected provider and a live request. Optional redaction
// and diagnostics precede canonical scheduling-model classification.
//
// Generic policy rejection commits a fixed client envelope instead of
// returning a provider-switch signal. Other decisions record passthrough
// failover evidence and construct the classified failure using scheduling
// disablement and pool retry policy. The caller still enforces whether output
// commitment permits another attempt; this helper does not own the provider
// loop.
func (p *OpenAIResponseOutput) PassthroughFailoverError(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestBody []byte,
	responseBody []byte,
) error {
	body := p.redact(ctx, provider, responseBody)

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if p.Options.Configured && p.Options.LogUpstreamErrorBody {
		maxBytes := p.Options.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		// Bound optional logged excerpts while health still sees the captured
		// response bytes, retaining whole UTF-8 characters in the diagnostic text.
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	LogOpenAIInstructionsRequiredDebug(ctx, c, provider, resp.StatusCode, upstreamMsg, requestBody, body)
	reqModel, _, _ := requeststate.OpenAIRequestMetaFromBody(requestBody)
	canonicalModel := gatewayprovider.ExecutionModelPolicy(provider).WithContext(ctx).CanonicalSchedulingModel(reqModel)
	decision := gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, provider, resp.StatusCode, resp.Header, body, false, canonicalModel)
	if decision.ShouldReturnGenericError() {
		MarkResponseCommitted(c)
		writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
		return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:             provider.Record.Platform,
		ProviderID:           provider.Record.ID,
		ProviderName:         provider.Record.Name,
		UpstreamStatusCode:   resp.StatusCode,
		UpstreamRequestID:    resp.Header.Get("x-request-id"),
		Passthrough:          true,
		Kind:                 "failover",
		Message:              upstreamMsg,
		Detail:               upstreamDetail,
		UpstreamResponseBody: upstreamDetail,
	})
	shouldDisable := decision.StopScheduling
	return (gatewayprovider.OpenAIFailoverPolicy{Health: p.Health}).NewProviderFailure(
		provider,
		resp.StatusCode,
		resp.Header,
		body,
		upstreamMsg,
		shouldDisable,
		!shouldDisable && provider.View().IsPoolMode() && provider.View().IsPoolModeRetryableStatus(resp.StatusCode),
	)
}

// PassthroughError presents a captured passthrough HTTP rejection or returns
// configured recovery. Callers supply the selected provider, live
// request/response and captured bytes. Cyber, invalid-input, context-window
// and size evidence identify request-scoped failures that bypass the
// subsequent provider-health branch.
//
// Health can choose generic output or a configured failover signal for
// remaining errors. Final presentation commits the request, records
// passthrough Ops evidence and retains a redacted parameter-error body when
// appropriate. Context-window messages use the sanitized extracted message,
// allowing client recovery; other failures use fixed status-based text through
// the Compact-aware envelope writer.
func (p *OpenAIResponseOutput) PassthroughError(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestBody []byte,
	responseBody []byte,
) error {
	body := p.redact(ctx, provider, responseBody)

	// Record cyber_policy from the redacted upstream body for subsequent risk handling and email.
	// The client error is rebuilt below. Cyber policy rejection is request-scoped,
	// so skip provider health updates that could apply a temporary scheduling ban.
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
		// Bound optional logged excerpts while health still sees the captured
		// response bytes, retaining whole UTF-8 characters in the diagnostic text.
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	LogOpenAIInstructionsRequiredDebug(ctx, c, provider, resp.StatusCode, upstreamMsg, requestBody, body)
	clientInvalidRequest := openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, body)
	requestScopedError := cyberHit || clientInvalidRequest || openai.IsOpenAIContextWindowError(upstreamMsg, body) ||
		gatewayprovider.IsOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body)
	// Input, context-window, size and cyber rejections describe this request.
	// Remaining failures can affect provider reuse, so health runs before final presentation.
	if !requestScopedError {
		reqModel, _, _ := requeststate.OpenAIRequestMetaFromBody(requestBody)
		canonicalModel := gatewayprovider.ExecutionModelPolicy(provider).WithContext(ctx).CanonicalSchedulingModel(reqModel)
		decision := gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, provider, resp.StatusCode, resp.Header, body, false, canonicalModel)
		if decision.ShouldReturnGenericError() {
			MarkResponseCommitted(c)
			writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
			return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		if decision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, false, false) {
			return gatewayprovider.NewOpenAIUpstreamFailure(
				resp.StatusCode,
				resp.Header,
				body,
				upstreamMsg,
				decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode),
			)
		}
	}
	MarkResponseCommitted(c)
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:             provider.Record.Platform,
		ProviderID:           provider.Record.ID,
		ProviderName:         provider.Record.Name,
		UpstreamStatusCode:   resp.StatusCode,
		UpstreamRequestID:    resp.Header.Get("x-request-id"),
		Passthrough:          true,
		Kind:                 "http_error",
		Message:              upstreamMsg,
		Detail:               upstreamDetail,
		UpstreamResponseBody: upstreamDetail,
	})
	if clientInvalidRequest {
		// Committed event streams cannot emit raw JSON or update response headers;
		// extract safe error fields and emit a terminal protocol event instead.
		if CommittedSSE(c) {
			errType, code, message := extractSSEErrorFields(http.StatusBadRequest, body, "invalid_request_error", "Invalid request")
			if upstreamMsg != "" {
				message = upstreamMsg
			}
			DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, http.StatusBadRequest, errType, code, message, true, false)
			return fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
		}
		// Keep the captured redacted parameter error intact while the shared
		// header writer admits retry metadata for this already-final client rejection.
		WriteForwardPassthroughErrorHeaders(c.Writer.Header(), resp.Header)
		c.Data(http.StatusBadRequest, "application/json; charset=utf-8", body)
		return fmt.Errorf("upstream invalid request: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	// Context-window text describes the client recovery condition. Retain its
	// sanitized message so a client can identify the need to compact its input.
	if openai.IsOpenAIContextWindowError(upstreamMsg, body) && upstreamMsg != "" {
		writeOpenAIPassthroughErrorEnvelope(c, resp.StatusCode, resp.Header, upstreamMsg)
	} else {
		writeSanitizedOpenAIPassthroughError(c, resp.StatusCode, resp.Header)
	}

	return fmt.Errorf("upstream error: %d (client response sanitized)", resp.StatusCode)
}

// LogOpenAICapacityFailoverSuppressed records that capacity recovery was
// suppressed after semantic output. A missing provider omits its identity
// fields; callers supply the path and event evidence from the reader's own
// output-state decision.
func LogOpenAICapacityFailoverSuppressed(
	ctx context.Context,
	provider *gatewayprovider.ExecutionProvider,
	path string,
	upstreamRequestID string,
	eventType string,
) {
	fields := []zap.Field{
		zap.String("path", path),
		zap.String("event_type", strings.TrimSpace(eventType)),
		zap.String("upstream_request_id", strings.TrimSpace(upstreamRequestID)),
	}
	if provider != nil {
		fields = append(fields,
			zap.Int64("provider_id", provider.Record.ID),
			zap.String("platform", provider.Record.Platform),
		)
	}
	logging.FromContext(ctx).Warn("gateway.failover_suppressed_after_semantic_output", fields...)
}

// openAIStreamFailedEventPassthroughBody supplies normalized error evidence
// for administrator presentation rules. Empty, invalid JSON and payloads
// already containing top-level error pass through. For nested response.error,
// it extracts type, code, param and message, using failedMessage when nested
// message is blank.
//
// A missing nested error can still produce a message-only envelope when the
// reader supplied failure text. An empty extracted error or failed
// serialization retains the supplied payload, keeping available event evidence
// for matching. Callers use the unmodified event separately to infer status,
// so normalization does not erase the status source.
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
		// Failed normalization must leave the captured event available for rules;
		// retaining its bytes avoids replacing usable failure evidence with an empty body.
		if err != nil {
			return payload
		}
		return body
	}

	// Presentation rules consume a root error object. Extract its recognized
	// diagnostic fields from response.error while the original event remains the status source.
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
	// Keep the supplied event when normalized serialization fails, so rule
	// matching can still inspect the available upstream evidence for this failure.
	if err != nil {
		return payload
	}
	return body
}

// ApplyOpenAIStreamFailedErrorRule matches final presentation using
// event-derived status and normalized error fields. Callers pass the selected
// provider platform so OpenAI and Grok rules remain scoped correctly; a miss
// returns the shared default presentation alongside false.
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

// TerminalProviderEffects applies health effects eligible for the inferred
// terminal failure status and returns that status with scheduling disablement.
// Ordinary permission 403 events return without provider effects unless their
// payload supplies provider-failure evidence.
//
// Authentication, rate-limit and overload status use the selected canonical
// model, or event model evidence when no canonical value was supplied.
// Semantic 429 headers follow the model-specific quota policy before health
// handling. Other statuses apply health only when custom or pool retry
// settings admit them, preventing an unrecognized terminal status from
// automatically entering those provider policies.
func (p *OpenAIResponseOutput) TerminalProviderEffects(
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	payload []byte,
	message string,
	headers http.Header,
	canonicalModel ...string,
) (int, bool) {
	statusCode := openai.OpenAIStreamFailureStatus(payload, message)
	switch statusCode {
	case http.StatusForbidden:
		// A permission error needs structured provider-failure evidence before
		// credential or scheduling effects can be attributed to the selected provider.
		if !openai.OpenAIStream403ProviderFailure(payload, message) {
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
		providerHeaders := headers
		if statusCode == http.StatusTooManyRequests {
			// A streaming 429 for ordinary models must not inherit the HTTP 200 global quota snapshot.
			// OAuth and Setup Token Spark quota failures need the explicit reset window.
			providerHeaders = gatewayprovider.OpenAISemantic429Headers(provider, model, headers)
		}
		return statusCode, gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, provider, statusCode, providerHeaders, payload, false, model).StopScheduling
	default:
		// response.failed can carry custom statuses such as 422. Apply configured or pool retry policy, while
		// request validation leaves health unchanged.
		customMatched := provider != nil && provider.View().IsCustomErrorCodesEnabled() && provider.View().ShouldHandleErrorCode(statusCode)
		poolRetryable := provider != nil && provider.View().IsPoolMode() && provider.View().IsPoolModeRetryableStatus(statusCode)
		if customMatched || poolRetryable {
			ctx := context.Background()
			if c != nil && c.Request != nil {
				ctx = c.Request.Context()
			}
			return statusCode, gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, provider, statusCode, headers, payload, false, requeststate.FirstNonEmpty(canonicalModel...)).StopScheduling
		}
		return statusCode, false
	}
}

// ApplyStreamFailurePolicy interprets a failed stream event for OpenAI health
// classification. An inferred status below HTTP 400 becomes 502; callers
// supply the canonical model and event bytes, and true semantic-stream
// handling distinguishes this observation from ordinary HTTP failures.
func (p *OpenAIResponseOutput) ApplyStreamFailurePolicy(
	ctx context.Context,
	provider *gatewayprovider.ExecutionProvider,
	model string,
	headers http.Header,
	payload []byte,
	message string,
) (int, providercore.UpstreamErrorDecision) {
	status := openai.OpenAIStreamFailedEventSemanticStatus(payload, message)
	// A failed lifecycle event needs an error status even when its payload
	// lacks one; 502 lets provider health evaluate the failure under that condition.
	if status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	return status, gatewayprovider.ApplyOpenAIResponseHealth(ctx, p.Health, provider, status, headers, payload, true, model)
}

// RecordStreamError sanitizes the message and records inferred event status
// with optional bounded detail. Empty messages use a standard failure
// description, missing provider uses OpenAI identity, and nil contexts skip
// Ops publication while still returning the normalized message.
func (p *OpenAIResponseOutput) RecordStreamError(
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
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
		// Keep optional Ops payload excerpts bounded and UTF-8 complete; event
		// status and retry predicates continue to use the full supplied event evidence.
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
		if provider != nil {
			event.Platform = provider.Record.Platform
			event.ProviderID = provider.Record.ID
			event.ProviderName = provider.Record.Name
		}
		AppendOpsUpstreamError(c, event)
	}
	return message
}

// NewStreamFailure delegates stream failover construction with no
// canonical-model override. The first optional response-header map is
// considered by the builder; callers decide retry eligibility from commitment
// and actual output before using the returned signal.
func (p *OpenAIResponseOutput) NewStreamFailure(
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	passthrough bool,
	upstreamRequestID string,
	payload []byte,
	message string,
	responseHeaders ...http.Header,
) *forwardcore.UpstreamFailoverError {
	return p.NewStreamFailureWithModel(c, provider, passthrough, upstreamRequestID, payload, message, "", responseHeaders...)
}

// NewStreamFailureWithModel delegates stream failover construction using the
// canonical scheduling model and first optional headers. Missing headers pass
// nil, and the shared builder applies terminal health effects before choosing
// the final classified response body. This entry supplies no applied evidence.
func (p *OpenAIResponseOutput) NewStreamFailureWithModel(
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
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
		c, provider, passthrough, upstreamRequestID, headers, payload, message, nil, canonicalModel,
	)
}

// NewStreamPolicyFailure delegates stream failure construction using captured
// event evidence. The model-aware builder applies terminal provider effects
// and computes retryability from the provider and event payload; callers with
// applied evidence use that builder directly.
func (p *OpenAIResponseOutput) NewStreamPolicyFailure(
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	passthrough bool,
	upstreamRequestID string,
	responseHeaders http.Header,
	payload []byte,
	message string,
) *forwardcore.UpstreamFailoverError {
	return p.NewStreamPolicyFailureWithModel(c, provider, passthrough, upstreamRequestID, responseHeaders, payload, message, nil)
}

// NewStreamPolicyFailureWithModel constructs a classified failover error from
// terminal event and applied-health evidence. It sanitizes failure text,
// clones supplied response headers and uses appliedEffects for this event when
// nonnil. Nil evidence makes TerminalProviderEffects compute the status and
// scheduling decision. Callers supply evidence only after evaluating provider
// effects for this same terminal; the builder does not retain it for later calls.
//
// Event retryability follows the provider and payload. Semantic 429
// classification omits stream-establishment headers while retaining them
// on the returned failure for downstream metadata. Classified credential or
// request-transient failures keep their response body; other failures receive
// a rebuilt protocol error envelope. Callers enforce the output window before
// invoking another provider.
func (p *OpenAIResponseOutput) NewStreamPolicyFailureWithModel(
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	passthrough bool,
	upstreamRequestID string,
	responseHeaders http.Header,
	payload []byte,
	message string,
	appliedEffects *AppliedOpenAIResponseEffects,
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
	var statusCode int
	var shouldDisable bool
	if appliedEffects != nil {
		statusCode = appliedEffects.StatusCode
		shouldDisable = appliedEffects.StopScheduling
	} else {
		statusCode, shouldDisable = p.TerminalProviderEffects(c, provider, payload, message, headers, canonicalModel...)
	}
	// Applied effects can omit a usable status. Recover it from the same event
	// while preserving the supplied scheduling decision and avoiding another health update.
	if statusCode < http.StatusBadRequest {
		statusCode = openai.OpenAIStreamFailureStatus(payload, message)
	}
	// Record failover after applied or newly computed effects select the policy
	// status. Ops infers its own status from the event, while the returned
	// failure retains that policy status for recovery.
	message = p.RecordStreamError(c, provider, passthrough, upstreamRequestID, "failover", payload, message)
	errType := "upstream_error"
	if statusCode == http.StatusTooManyRequests {
		errType = "rate_limit_error"
	}
	// The rebuilt envelope uses fixed string fields. Classified credentials or
	// request-transient failures retain their own upstream body at the final selection.
	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
	retryable := gatewayprovider.OpenAIStreamFailureRetryable(provider, payload, message)
	// HTTP 200 quota headers describe stream establishment. A semantic 429
	// omits them from classification while retained headers still supply retry and request IDs.
	classificationHeaders := headers
	if statusCode == http.StatusTooManyRequests {
		classificationHeaders = nil
	}
	failoverErr := (gatewayprovider.OpenAIFailoverPolicy{Health: p.Health}).NewProviderFailureWithClassificationHeaders(provider, statusCode, headers, classificationHeaders, payload, message, shouldDisable, retryable)
	if failoverErr.IsCredentialFailure() || failoverErr.RequestScopedTransient {
		return failoverErr
	}
	// Credential and request-transient failures have already retained their
	// classified evidence; remaining failures use the rebuilt client error envelope here.
	failoverErr.ResponseBody = body
	return failoverErr
}

// nonStreamingTerminalFailure proposes stream-classified recovery for a
// buffered SSE terminal. Missing provider or committed response returns nil,
// leaving protocol-error handling to the reader. The failed-event predicate is
// replaced by the more conservative bare-error predicate when terminalType is
// error.
//
// Only an accepted predicate reaches the shared failure builder with response
// headers and the first nonempty canonical model. The entry still checks
// actual output and heartbeat-adjusted commitment before using that proposal.
// A nil return is an absent recovery proposal, so callers converting it to an
// error interface preserve nil rather than a typed nil pointer.
func (p *OpenAIResponseOutput) nonStreamingTerminalFailure(
	c *gin.Context,
	resp *http.Response,
	provider *gatewayprovider.ExecutionProvider,
	passthrough bool,
	terminalType string,
	payload []byte,
	message string,
	canonicalModel ...string,
) *forwardcore.UpstreamFailoverError {
	if provider == nil || IsResponseCommitted(c) {
		return nil
	}
	shouldFailover := openai.OpenAIStreamFailedEventShouldFailover(payload, message)
	// Bare error frames need recognized transient evidence. Their conservative
	// predicate keeps a generic message from automatically requesting another provider.
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
	return p.NewStreamFailureWithModel(c, provider, passthrough, upstreamRequestID, payload, message, requeststate.FirstNonEmpty(canonicalModel...), headers)
}
