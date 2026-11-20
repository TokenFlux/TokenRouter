package openaiattempt

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// responses_attempts.go manages Responses provider attempts and qualifies
// their retry and completion outcomes.
// Responses attempts retain canonical request input while providers, resource
// callbacks, and observed results change through a serial text loop. A selected
// HTTP continuation target needs eligible native Responses API-key credentials.
// Unsuitable routes release acquired capacity and return a skip result; the
// selector keeps an immutable continuation bound to its originating provider.
//
// Forwarding releases provider capacity before recording Cyber effects and
// exposing its outcome. Adjusted output size distinguishes registered
// keepalives from meaningful response bytes, so committed headers can coexist
// with an open retry window. Native usage and produced images can support
// partial completion, while retry eligibility still follows classified errors,
// client cancellation, and output recovery evidence. Completion captures
// request/result values before submitting work that can run after HTTP handling
// finishes.

// responsesAttemptBridge retains admitted request identity and current
// provider-attempt state. The serial text loop changes provider resources and
// results while shared runtime services remain bound through fixed.
type responsesAttemptBridge struct {
	// optional request session registrations
	sessionAttempts *scheduler.SessionAttempts
	// known sticky provider binding
	hasBoundSession bool
	// request retry cache-billing intent
	forceCacheBilling bool
	// consumed destination fallback opportunity
	groupFallbackUsed bool
	// shared execution service bindings
	fixed *openAIExecutionDependencies
	// active HTTP request context
	c *gin.Context
	// current effective funding key
	apiKey *apikey.APIKey
	// original authenticated caller identity
	subject authctx.AuthSubject
	// current effective funding subscription
	subscription *billing.UserSubscription
	// request attempt diagnostic logger
	reqLog *zap.Logger
	// body, forwardBody, and sessionHashBody retain one request's source
	// representations. The serial loop derives provider input from
	// forwardBody and hashes body for completion; sessionHashBody supplies
	// Cyber session matching when marked.
	// body: admitted client request bytes. forwardBody: planned
	// group-mapped input bytes.
	// sessionHashBody: retained session matching input.
	body, forwardBody, sessionHashBody []byte
	// reqModel, forwardModel, sessionHash, previousResponseID, and
	// requestPlatform describe one admitted request. Group fallback changes
	// forwarding identity while the client model and continuation remain
	// available for diagnostics and selection.
	// reqModel: original requested model. forwardModel: current
	// group-mapped model.
	// sessionHash: active sticky selection seed. previousResponseID:
	// supplied continuation response ID.
	// requestPlatform: optional provider-platform selection filter.
	reqModel, forwardModel, sessionHash, previousResponseID, requestPlatform string
	// reqStream, nativeCompactionV2, legacyCompact, and requireCompact
	// retain admission flags through serial selection and forwarding. They
	// select output, compaction accounting, and provider capability checks
	// for the same HTTP request.
	// reqStream: client streaming request flag. nativeCompactionV2: native
	// compaction accounting flag.
	// legacyCompact: legacy Compact endpoint flag. requireCompact: required
	// provider compaction capability.
	reqStream, nativeCompactionV2, legacyCompact, requireCompact bool
	// streamStarted is shared with slot waits and terminal output. The
	// output adapter supplies a nonnil pointer that remains valid through
	// the serial request loop.
	streamStarted *bool
	// selectionCtx holds the current routing and billing context. Begin,
	// PrepareRetry, and group fallback publish updates before the next
	// selection reads it.
	selectionCtx context.Context
	// current admitted group mapping
	groupMapping routing.GroupMappingResult
	// request routing phase start
	routingStart time.Time
	// selected endpoint capability requirement
	requiredCapability providercore.OpenAIEndpointCapability
	// selection retains current scheduler target and acquired/wait
	// metadata. Select replaces it before Acquire adopts its provider
	// capacity and resource callback.
	selection *gatewaycapture.SelectionResult
	// provider retains the current execution target for forwarding and
	// synchronous completion capture. A subsequent Select replaces it for
	// the next serial attempt.
	provider *gatewaycapture.ExecutionProvider
	// providerReleaseFunc owns acquired provider cleanup. Forward invokes
	// it before policy/latency processing; the shared slot adapter
	// determines cancellation versus completion release.
	providerReleaseFunc func()
	// result retains the most recent forwarding observation, including
	// usage- bearing partial failures. Complete reads it only after the
	// text loop chooses completion handling.
	result *forwardcore.OpenAIResult
	// writerSizeBeforeForward measures adjusted output before this
	// exchange. Heartbeat subtraction makes RetryReady compare
	// meaningful bytes rather than committed keepalive traffic.
	writerSizeBeforeForward int
	// cyberPolicyHandled and wroteFallback retain different terminal
	// effects for this request. Cyber handling is passed to completion
	// capture, while fallback emission controls failure logging after
	// protocol output handling.
	// cyberPolicyHandled: current Cyber mark handled. wroteFallback:
	// missing terminal error supplied.
	cyberPolicyHandled, wroteFallback bool
	// retained cross-mode reasoning history
	passthroughFailoverState openAIPassthroughFailoverState
	// current terminal-failure diagnostic attributes
	fields []zap.Field
}

// Select resolves the current request under transport, capability, and
// continuation constraints. Selection errors return to the text loop; an absent
// target writes diagnosed client output and returns unavailable. A selected
// target is retained for acquisition, forwarding, scheduling observations, and
// cleanup.
//
// HTTP previous_response_id requires an eligible native Responses API-key
// route. Other selected routes release any selection callback and return
// a classified Skip outcome. Selection retains the originating provider for
// immutable HTTP continuations, so a rejected binding cannot send its response
// reference through another credential. Eligible targets receive a retained
// pool-session seed and selected-provider observations.
func (b *responsesAttemptBridge) Select(excluded map[int64]struct{}) (textflow.ResponseSelection, error) {
	b.reqLog.Debug("openai.provider_selecting", zap.Int("excluded_provider_count", len(excluded)))
	var scheduleDecision scheduler.PlatformDecision
	var err error
	selectStart := time.Now()
	b.selection, scheduleDecision, err = b.binding().selectProviderWithSchedulerForCapability(
		b.selectionCtx,
		b.apiKey.GroupID,
		b.previousResponseID,
		b.sessionHash,
		b.reqModel,
		excluded, egress.OpenAIUpstreamTransportAny, b.requiredCapability,
		b.requireCompact,
		false,
		b.requestPlatform,
	)
	if b.c != nil && b.c.Request != nil {
		timing.ObserveSubstage(b.c.Request.Context(), "scheduler_select_ms", time.Since(selectStart))
	}
	if err != nil {
		return textflow.ResponseSelection{}, err
	}
	if b.selection == nil || b.selection.Provider == nil {
		cls := ClassifyOpenAICompatibleNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel)
		if !cls.ModelNotFound {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
		}
		b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, *b.streamStarted)
		return textflow.ResponseSelection{}, nil
	}
	if b.previousResponseID != "" && b.selection != nil && b.selection.Provider != nil {
		b.reqLog.Debug("openai.provider_selected_with_previous_response_id", zap.Int64("provider_id", b.selection.Provider.Record.ID))
	}
	b.reqLog.Debug("openai.provider_schedule_decision",
		zap.String("layer", scheduleDecision.Layer),
		zap.Bool("sticky_previous_hit", scheduleDecision.StickyPreviousHit),
		zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
		zap.Int("candidate_count", scheduleDecision.CandidateCount),
		zap.Int("top_k", scheduleDecision.TopK),
		zap.Int64("latency_ms", scheduleDecision.LatencyMs),
		zap.Float64("load_skew", scheduleDecision.LoadSkew),
	)
	b.provider = b.selection.Provider
	if b.previousResponseID != "" && !gatewaycapture.ExecutionProtocolTarget(b.provider).SupportsHTTPResponsesContinuation() {
		// Continuation references require an eligible native Responses route.
		// Release the rejected target so failed admission cannot retain provider capacity.
		if b.selection.ReleaseFunc != nil {
			b.selection.ReleaseFunc()
			b.selection.ReleaseFunc = nil
		}
		skipped := &forwardcore.UpstreamFailoverError{
			StatusCode:       http.StatusBadRequest,
			Stage:            forwardcore.GatewayFailureStageInference,
			Scope:            forwardcore.GatewayFailureScopeRequest,
			Reason:           forwardcore.OpenAIHTTPContinuationUnsupportedReason,
			ClientStatusCode: http.StatusBadRequest,
			ClientMessage:    "previous_response_id requires a native Responses API-key provider for HTTP requests",
		}
		b.reqLog.Debug("openai.provider_skipped_http_continuation_unsupported",
			zap.Int64("provider_id", b.provider.Record.ID),
			zap.String("provider_type", b.provider.Record.Type),
		)
		picked := b.selectedView()
		picked.Skip = &textflow.AttemptFailure{Cause: skipped, Policy: skipped.RetryFailure()}
		return picked, nil
	}
	b.sessionHash = EnsureOpenAIPoolModeSessionHash(b.sessionHash, b.provider)
	b.reqLog.Debug("openai.provider_selected", zap.Int64("provider_id", b.provider.Record.ID), zap.String("provider_name", b.provider.Record.Name))
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())

	return b.selectedView(), nil
}

// SelectionFailure writes terminal Responses selection output unless the client
// is gone. An initial Compact capacity error receives its specific response;
// other initial errors use group-model diagnosis and scheduler refinement.
// Local capability failures retain their own status and reason. After provider
// exclusions, the last classified upstream failure takes
// precedence over a simple 502 fallback.
func (b *responsesAttemptBridge) SelectionFailure(err error, excludedCount int, last *textflow.AttemptFailure) {
	if gatewayhttp.WriteOpenAIModelAttributeError(b.c, err, *b.streamStarted) {
		return
	}
	var lastFailoverErr *forwardcore.UpstreamFailoverError
	if last != nil {
		errors.As(last.Cause, &lastFailoverErr)
	}

	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai.provider_select_aborted_client_disconnected", zap.Error(err))
		return
	}
	b.reqLog.Warn("openai.provider_select_failed",
		zap.Error(gatewayhttp.OpenAICompatibleSelectionErrorForLog(err, b.requestPlatform)),
		zap.Int("excluded_provider_count", excludedCount),
	)
	if excludedCount == 0 {
		if b.legacyCompact && errors.Is(err, scheduler.ErrNoAvailableCompactProviders) {
			gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
			b.binding().handleStreamingAwareError(b.c, http.StatusServiceUnavailable, "compact_not_supported", "No available providers support /responses/compact", *b.streamStarted)
			return
		}
		cls := ClassifyNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, b.requestPlatform)
		cls = classifySelectionFailureError(err, cls)
		if !cls.ModelNotFound {
			gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
		}
		b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, *b.streamStarted)
		return
	}
	if lastFailoverErr != nil {
		b.binding().handleFailoverExhausted(b.c, lastFailoverErr, *b.streamStarted)
	} else {
		b.binding().handleFailoverExhaustedSimple(b.c, 502, *b.streamStarted)
	}
}

// Acquire tracks the selected session and adopts or waits for its provider
// slot. Callers select a usable provider first. The returned bool determines
// whether Forward can run; the stored release callback is retired by forwarding
// cleanup.
func (b *responsesAttemptBridge) Acquire() bool {
	if b.sessionAttempts != nil && b.binding().sessions.Track != nil {
		b.binding().sessions.Track(b.sessionAttempts, b.provider, b.sessionHash)
	}
	var acquired bool
	b.providerReleaseFunc, acquired = b.binding().acquireResponsesProviderSlot(b.c, b.apiKey.GroupID, b.sessionHash, b.selection, b.reqStream, b.streamStarted, b.reqLog)
	return acquired
}

// Forward executes canonical-derived Responses input and returns observed
// attempt state. Callers acquire the selected provider first. Provider cleanup
// runs when forwarding returns, before Cyber policy recording and completion
// capture, while the bridge retains provider and result metadata for those
// later steps.
//
// Adjusted pre-forward output size excludes all recorded heartbeat bytes.
// Response timing subtracts a positive upstream phase only when total
// forwarding duration exceeds it; otherwise it retains the full duration.
// Result presence, images, native partial usage, HTTP commitment, and
// classified retry permission are recorded separately so the outer loop can
// decide replay or completion.
func (b *responsesAttemptBridge) Forward() textflow.ResponseOutcome {
	var err error
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsRoutingLatencyMsKey, time.Since(b.routingStart).Milliseconds())
	forwardStart := time.Now()
	// Recorded heartbeat bytes can commit headers before usable
	// output. Subtract those bytes here so later retry readiness compares
	// the actual exchange output.
	b.writerSizeBeforeForward = gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	// A managed-mode provider after passthrough needs separately derived
	// reasoning input. Rebuild from canonical bytes so a previous
	// provider's sanitation does not replace future input.
	attemptBody := b.binding().deriveOpenAIForwardAttemptBody(b.reqLog, b.forwardBody, b.provider, &b.passthroughFailoverState)
	// Retire acquired provider capacity when the exchange returns on every
	// path. Keep its result and identity available for subsequent Cyber and
	// completion snapshot processing.
	b.result, err = func() (*forwardcore.OpenAIResult, error) {
		defer func() {
			if b.providerReleaseFunc != nil {
				b.providerReleaseFunc()
			}
		}()
		return b.binding().forward(b.c.Request.Context(), b.c, b.provider, attemptBody)
	}()
	var cyberBlockBodyHTTP []byte
	if gatewayhttp.GetOpsCyberPolicy(b.c) != nil {
		cyberBlockBodyHTTP = b.sessionHashBody
	}
	b.cyberPolicyHandled = b.binding().recordCyberPolicyIfMarked(b.c, b.apiKey, b.provider, b.subscription, b.reqModel, err != nil, cyberBlockBodyHTTP, gatewayhttp.ClientRequestedUsageFields(b.c, b.groupMapping, b.reqModel, ""), billing.HashUsageRequestPayload(b.body), b.nativeCompactionV2)
	forwardDurationMs := time.Since(forwardStart).Milliseconds()
	upstreamLatencyMs, _ := GetContextInt64(b.c, gatewayhttp.OpsUpstreamLatencyMsKey)
	responseLatencyMs := forwardDurationMs
	// Subtract only a positive upstream phase smaller than the measured
	// total. Missing or incompatible timing keeps total duration rather
	// than manufacturing negative response time.
	if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
		responseLatencyMs = forwardDurationMs - upstreamLatencyMs
	}
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsResponseLatencyMsKey, responseLatencyMs)

	out := textflow.ResponseOutcome{Outcome: textflow.Outcome{Attempt: openAIObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil}, Images: b.result != nil && b.result.ImageCount > 0}
	out.NativePartial = err != nil && b.result != nil && b.result.NativeUsage != nil && (b.result.NativeUsage.HasObservedTokens() || b.result.ImageCount > 0)
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	if retry, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
		out.FirstOutputRecovery = retry.SafeToFailoverAfterWrite
	}
	return out
}

// Complete captures Responses usage and request metadata before submitting its
// recording task. A nil result ends immediately. The task captures
// completionInput and diagnostic identity values, allowing Recorder to run
// after request state changes; recording errors are logged by the task rather
// than replaying upstream execution.
func (b *responsesAttemptBridge) Complete() {
	res := b.result

	if res == nil {
		return
	}
	completionInput := b.captureCompletion(gatewaycapture.OpenAICapture{
		Result:             res,
		RequestPayloadHash: billing.HashUsageRequestPayload(b.body),
		PricingUsageFields: b.groupMapping.ToUsageFields(b.reqModel, res.UpstreamModel),
		NativeCompactionV2: b.nativeCompactionV2,
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitOpenAIUsageRecordTask(b.c, res, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, true); err != nil {
			logging.L().With(
				zap.String("component", "handler.openai_gateway.responses"),
				zap.Int64("user_id", completionUserID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("provider_id", completionInput.Provider.ID),
			).Error("openai.record_usage_failed", zap.Error(err))
		}
	})
}

// captureCompletion fills HTTP metadata and delegates snapshot creation to
// CaptureOpenAI. It stamps requested reasoning on the result and reads the
// current effective key, provider, subscription, body, quota updater, and Cyber
// handling state synchronously. Callers complete this capture before handing
// its result to asynchronous submission.
func (b *responsesAttemptBridge) captureCompletion(capture gatewaycapture.OpenAICapture) *completion.Input {
	gatewayhttp.StampOpenAIRequestedReasoningEffort(capture.Result, b.c)
	capture.UserAgent = b.c.GetHeader("User-Agent")
	capture.IPAddress = clientip.GetClientIP(b.c)
	capture.InboundEndpoint = gatewayhttp.GetInboundEndpoint(b.c)
	capture.UpstreamEndpoint = ResolveOpenAIUpstreamEndpoint(b.c, b.provider, capture.Result)
	capture.ClientSessionID = gatewayhttp.ExtractClientSessionID(b.c)
	capture.APIKey = b.apiKey
	capture.User = b.apiKey.User
	capture.Provider = gatewaycapture.ExecutionCompletionRecord(b.provider)
	capture.Subscription = b.subscription
	capture.RequestBody = b.body
	capture.APIKeyService = b.binding().apiKeyService
	capture.CyberBlocked = b.cyberPolicyHandled
	return gatewaycapture.CaptureOpenAI(gatewayhttp.CompletionContext(b.c), &capture)
}

// PartialImages logs a forwarding error alongside images observed by the
// current provider.
func (b *responsesAttemptBridge) PartialImages(err error) {
	b.reqLog.Warn("openai.forward_partial_error_with_image_result",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("image_count", b.result.ImageCount),
		zap.Error(err),
	)
}

// RetryReady permits a classified Responses retry while the client and output
// allow replay. Callers supply a nonnil attempt failure. Nonfailover causes or
// client disconnection return false; Cyber warning handling is attempted before
// checking adjusted output through OpenAIForwardMayFailover.
//
// When replay is closed, health failure is observed and terminal streaming
// output is delegated before returning false. Allowed retries can retain
// committed headers; that sets the shared stream marker for exhaustion output.
// Eligible scheduling failures are reported only after this replay decision
// accepts another attempt.
func (b *responsesAttemptBridge) RetryReady(failure *textflow.AttemptFailure) bool {
	err := failure.Cause
	var failoverErr *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		return false
	}
	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai.failover_aborted_client_disconnected",
			zap.Int64("provider_id", b.provider.Record.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
		)
		return false
	}
	b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.provider, b.reqModel, failoverErr.StatusCode, failoverErr.ResponseBody, err.Error())
	if !OpenAIForwardMayFailover(b.c, b.writerSizeBeforeForward, failoverErr) {
		b.binding().observeOpenAIProviderHealthFailure(b.c.Request.Context(), b.provider, err)
		b.binding().handleFailoverExhausted(b.c, failoverErr, true)
		return false
	}
	// Replay may remain safe after headers were committed. Mark the stream
	// before another attempt so exhausted recovery uses the protocol's
	// streaming terminal error path.
	if b.streamStarted != nil && b.c.Writer.Written() {
		*b.streamStarted = true
	}
	if failoverErr.ShouldReportProviderScheduleFailure() {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.forwardModel, b.requireCompact, nil), false, nil, err)
	}
	return true
}

// RetryWait logs the selected provider's same-provider retry allowance and
// delay. Callers supply a failure carrying UpstreamFailoverError. The outer
// text loop owns the wait and cancellation; this callback records its chosen
// parameters.
func (b *responsesAttemptBridge) RetryWait(failure *textflow.AttemptFailure, retryLimit, retryCount int, retryDelay time.Duration) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai.pool_mode_same_provider_retry",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("retry_limit", retryLimit),
		zap.Int("retry_count", retryCount),
		zap.Duration("retry_delay", retryDelay),
	)
}

// Switching logs the selected provider and configured switch budget with proxy
// metadata. Callers supply a classified upstream failure. The outer text loop
// owns exclusion and switching; this callback reports the impending provider
// change.
func (b *responsesAttemptBridge) Switching(failure *textflow.AttemptFailure, switchCount, maxProviderSwitches int) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	failoverSwitchFields := []zap.Field{
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", switchCount),
		zap.Int("max_switches", maxProviderSwitches),
	}
	failoverSwitchFields = AppendOpenAIProviderProxyLogFields(failoverSwitchFields, b.provider)
	b.reqLog.Warn("openai.upstream_failover_switching", failoverSwitchFields...)
}

// OtherFailure presents local capability errors before recording any upstream
// failure feedback. For other errors it records Cyber warnings and scheduling
// failure, then supplies missing Responses error output. It prefers warning evidence carried by the
// error chain; absent evidence sends the error text to the warning recorder.
//
// After warning handling, communicated-error evidence and adjusted output size
// decide whether another terminal response is needed. A recorded warning
// permits fallback while adjusted size remains unchanged, and an already
// communicated error suppresses fallback. The selected output state is retained
// for Failed to choose log severity.
func (b *responsesAttemptBridge) OtherFailure(err error) {
	// Attribute admission can fail before transport. Present it once and stop
	// here so a local configuration rejection cannot reduce provider health scores.
	if routing.IsModelAttributeError(err) {
		b.wroteFallback = b.binding().ensureOpenAIForwardErrorResponse(b.c, *b.streamStarted, err)
		return
	}
	statusCode := 0
	if v, ok := GetContextInt64(b.c, gatewayhttp.OpsUpstreamStatusCodeKey); ok {
		statusCode = int(v)
	}
	recordedWarning := b.binding().recordOpenAIForwardErrorCyberWarning(b.c, b.reqLog, b.apiKey, b.provider, b.reqModel, statusCode, err)
	if !recordedWarning {
		b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.provider, b.reqModel, statusCode, nil, err.Error())
	}
	b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.forwardModel, b.requireCompact, b.result), false, nil, err)
	upstreamErrorAlreadyCommunicated := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	b.wroteFallback = false
	// Warning handling can emit terminal JSON or response.failed. Recheck
	// the communicated-error and adjusted-size evidence so supplying a
	// fallback does not duplicate that output.
	if !upstreamErrorAlreadyCommunicated && (!recordedWarning || gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) == b.writerSizeBeforeForward) {
		b.wroteFallback = b.binding().ensureOpenAIForwardErrorResponse(b.c, *b.streamStarted, err)
	}
	b.fields = []zap.Field{
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Bool("fallback_error_response_written", b.wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	}
}

// Failed logs the stored Responses forwarding failure at its selected severity.
func (b *responsesAttemptBridge) Failed() {
	if gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(b.c, b.wroteFallback) {
		b.reqLog.Warn("openai.forward_failed", b.fields...)
		return
	}
	b.reqLog.Error("openai.forward_failed", b.fields...)
}

// Success reports the observed scheduling outcome and eligible OAuth quota
// headers. Nonshadow OpenAI OAuth results update the Codex snapshot; shadow
// providers skip that header path. The result's SucceededForScheduling decision
// and optional TTFT latency drive feedback independently of completion
// recording.
func (b *responsesAttemptBridge) Success() {
	if b.result != nil {
		// Shadow credentials obtain quota state through their separate
		// usage query; ordinary nonshadow OAuth results can update this
		// selected provider's snapshot from response headers.
		if b.provider.View().IsOpenAI() && b.provider.Record.Type == capability.ProviderTypeOAuth && !b.provider.View().IsShadow() {
			b.binding().updateCodexUsageSnapshotFromHeaders(b.c.Request.Context(), b.provider.Record.ID, b.result.ResponseHeaders)
		}
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.forwardModel, b.requireCompact, b.result), b.result.SucceededForScheduling(), b.result.Timings.TTFTMs())
	} else {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.forwardModel, b.requireCompact, b.result), b.result.SucceededForScheduling(), nil)
	}
}

// Completed logs the serving Responses provider and final switch count.
func (b *responsesAttemptBridge) Completed(switchCount int) {
	b.reqLog.Debug("openai.request_completed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("switch_count", switchCount),
	)
}

// Context returns the current HTTP request context used by attempt
// coordination.
func (b *responsesAttemptBridge) Context() context.Context { return b.c.Request.Context() }

// CanAttempt reports whether the HTTP request remains available for provider
// replay. It delegates cancellation/disconnection checks to
// OpenAIRequestAllowsFailoverReplay. The separate RetryReady operation owns
// output-size permission after a forwarding failure.
func (b *responsesAttemptBridge) CanAttempt() bool { return OpenAIRequestAllowsFailoverReplay(b.c) }

// selectedView exposes the current provider's loop selection and OAuth retry
// classification. Callers retain a usable provider before invoking it. The view
// marks availability and distinguishes OpenAI OAuth-like from Grok OAuth
// credentials for retry policy.
func (b *responsesAttemptBridge) selectedView() textflow.ResponseSelection {
	return textflow.ResponseSelection{Selection: gatewaycapture.CaptureTextSelection(b.provider), Available: true, OAuth: failover.OAuth429Provider{OpenAI: b.provider.View().IsOpenAIOAuthLike(), Grok: b.provider.Record.Platform == capability.PlatformGrok && b.provider.Record.Type == capability.ProviderTypeOAuth}}
}

// Exhausted writes terminal Responses recovery output for the last attempt
// failure. A classified upstream cause uses its captured metadata. Missing or
// unclassified failure data uses the simple 502 path with the current stream
// marker.
func (b *responsesAttemptBridge) Exhausted(failure *textflow.AttemptFailure) {
	var original *forwardcore.UpstreamFailoverError
	if failure != nil && errors.As(failure.Cause, &original) {
		b.binding().handleFailoverExhausted(b.c, original, *b.streamStarted)
	} else {
		b.binding().handleFailoverExhaustedSimple(b.c, 502, *b.streamStarted)
	}
}

// Switched abandons the previous provider's session and records its selection
// switch. Session abandonment is conditional on tracking and provider presence.
// Scheduling feedback uses the retained selection after the serial loop decides
// to move providers.
func (b *responsesAttemptBridge) Switched() {
	if b.sessionAttempts != nil && b.provider != nil {
		b.sessionAttempts.Abandon(b.provider.Record.ID)
	}
	b.binding().recordOpenAIProviderSwitchForSelection(b.selection)
}
