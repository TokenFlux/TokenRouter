package openaiattempt

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// openai_chat_attempts.go manages Chat attempt state, retry readiness and
// completion capture.
// Chat input is rebuilt from the admitted body for each selected provider,
// using the current group mapping and prompt-cache identity. The bridge checks
// client policy with the selected TLS route before sending, then retains result
// and usage observations after releasing the provider's capacity.
//
// Retry readiness compares the writer size captured immediately before
// forwarding with its size after error handling. Client disconnection or
// changed bytes ends replay and invokes terminal OpenAI output; accepted
// retries report scheduling failure according to the classified error. Partial
// images and native usage remain distinct outcome evidence for the shared text
// loop, and completion captures its own group pricing fields before
// asynchronous recording can read the result.

// openAIChatAttemptBridge adds Chat prompt-cache identity to shared
// request-attempt state.
type openAIChatAttemptBridge struct {
	responsesAttemptBridge        // embedded admitted request state
	promptCacheKey         string // retained Chat prompt-cache identity
}

// Select resolves a text-capable Chat provider under the current group and
// exclusions. An error returns to the text loop; no usable target writes
// diagnosed output. Successful selection retains provider/session identity and
// publishes the selected platform before returning loop metadata for
// acquisition.
func (b *openAIChatAttemptBridge) Select(excluded map[int64]struct{}) (textflow.ResponseSelection, error) {
	b.reqLog.Debug("openai_chat_completions.provider_selecting", zap.Int("excluded_provider_count", len(excluded)))
	var scheduleDecision scheduler.PlatformDecision
	var err error
	b.selection, scheduleDecision, err = b.binding().selectProviderWithSchedulerForCapability(
		b.c.Request.Context(),
		b.apiKey.GroupID,
		"",
		b.sessionHash,
		b.reqModel,
		excluded, egress.OpenAIUpstreamTransportAny, provider.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
		b.requestPlatform,
	)
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
	b.provider = b.selection.Provider
	b.sessionHash = EnsureOpenAIPoolModeSessionHash(b.sessionHash, b.provider)
	b.reqLog.Debug("openai_chat_completions.provider_selected", zap.Int64("provider_id", b.provider.Record.ID), zap.String("provider_name", b.provider.Record.Name))
	_ = scheduleDecision
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())

	return b.selectedView(), nil
}

// SelectionFailure formats terminal Chat selection output while the request
// remains connected. Initial business errors are handled first, then model
// diagnosis and scheduler refinement. After provider exclusions, a captured
// upstream failure uses exhausted handling; missing failure evidence writes the
// default upstream error.
func (b *openAIChatAttemptBridge) SelectionFailure(err error, excludedCount int, last *textflow.AttemptFailure) {
	var lastFailoverErr *forwardcore.UpstreamFailoverError
	if last != nil {
		errors.As(last.Cause, &lastFailoverErr)
	}

	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai_chat_completions.provider_select_aborted_client_disconnected", zap.Error(err))
		return
	}
	b.reqLog.Warn("openai_chat_completions.provider_select_failed",
		zap.Error(gatewayhttp.OpenAICompatibleSelectionErrorForLog(err, b.requestPlatform)),
		zap.Int("excluded_provider_count", excludedCount),
	)
	if excludedCount == 0 {
		if b.binding().handleOpenAISelectionBusinessError(b.c, err, *b.streamStarted) {
			return
		}
		cls := ClassifyOpenAICompatibleNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel)
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
		b.binding().handleStreamingAwareError(b.c, http.StatusBadGateway, "api_error", "Upstream request failed", *b.streamStarted)
	}
}

// Forward executes the current Chat provider with group-mapped input and client
// policy. It rebuilds forwardBody from the admitted body before reading TLS
// identity and checking policy. Acquired provider cleanup runs when that
// exchange returns, before Cyber processing and outcome capture for the outer
// text loop.
//
// Response latency subtracts the positive upstream phase only when measured
// total forward time exceeds it. Result presence, images, native partial usage,
// HTTP commitment, and classified failures remain separate observations. The
// heartbeat-adjusted writer size before execution becomes RetryReady's baseline for
// determining whether an error can be replayed through another provider.
func (b *openAIChatAttemptBridge) Forward() textflow.ResponseOutcome {
	var err error
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsRoutingLatencyMsKey, time.Since(b.routingStart).Milliseconds())
	forwardStart := time.Now()

	// Rebuild input from the admitted body for each provider, applying the
	// current group mapping before platform conversion so prior attempt
	// edits cannot become source input.
	b.forwardBody = b.body
	if b.groupMapping.Mapped {
		b.forwardBody = b.binding().replaceModelInBody(b.body, b.groupMapping.MappedModel)
	}
	b.writerSizeBeforeForward = gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	// Release provider capacity on policy rejection and exchange return
	// before Cyber and completion handling consume the retained
	// provider/result observations for this attempt.
	b.result, err = func() (*forwardcore.OpenAIResult, error) {
		defer func() {
			if b.providerReleaseFunc != nil {
				b.providerReleaseFunc()
			}
		}()
		tlsRouterMatch := b.binding().matchOpenAITLSFingerprintRouterForRequest(b.c, b.provider)
		if err := b.binding().enforceOpenAIClientPolicyForRequest(b.c.Request.Context(), b.c, b.provider, b.forwardBody, tlsRouterMatch); err != nil {
			return nil, err
		}
		return b.binding().forwardAsChatCompletions(b.c.Request.Context(), b.c, b.provider, b.forwardBody, b.promptCacheKey, "", tlsRouterMatch)
	}()
	var cyberBlockBodyChat []byte
	if gatewayhttp.GetOpsCyberPolicy(b.c) != nil {
		cyberBlockBodyChat = b.body
	}
	b.cyberPolicyHandled = b.binding().recordCyberPolicyIfMarked(b.c, b.apiKey, b.provider, b.subscription, b.reqModel, err != nil, cyberBlockBodyChat, gatewayhttp.ClientRequestedUsageFields(b.c, b.groupMapping, b.reqModel, ""), billing.HashUsageRequestPayload(b.body))

	forwardDurationMs := time.Since(forwardStart).Milliseconds()
	upstreamLatencyMs, _ := GetContextInt64(b.c, gatewayhttp.OpsUpstreamLatencyMsKey)
	responseLatencyMs := forwardDurationMs
	// Use the upstream phase only when it fits inside measured forwarding
	// duration; missing or larger observations leave total time as the
	// response-latency estimate.
	if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
		responseLatencyMs = forwardDurationMs - upstreamLatencyMs
	}
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsResponseLatencyMsKey, responseLatencyMs)

	out := textflow.ResponseOutcome{Outcome: textflow.Outcome{Attempt: openAIObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil}, Images: b.result != nil && b.result.ImageCount > 0}
	out.NativePartial = err != nil && b.result != nil && b.result.NativeUsage != nil && (b.result.NativeUsage.HasObservedTokens() || b.result.ImageCount > 0)
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	if retry, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out
}

// Complete captures Chat usage and group pricing before submitting its
// recording task. Nil result returns immediately. The callback retains the
// completed input and scalar logging identities, so Recorder consumes snapshots
// after submission rather than rereading the active HTTP bridge.
func (b *openAIChatAttemptBridge) Complete() {
	res := b.result

	if res == nil {
		return
	}
	completionInput := b.captureCompletion(gatewaycapture.OpenAICapture{
		Result:             res,
		PricingUsageFields: b.groupMapping.ToUsageFields(b.reqModel, res.UpstreamModel),
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitOpenAIUsageRecordTask(b.c, res, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, true); err != nil {
			logging.L().With(
				zap.String("component", "handler.openai_gateway.chat_completions"),
				zap.Int64("user_id", completionUserID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("provider_id", completionInput.Provider.ID),
			).Error("openai_chat_completions.record_usage_failed", zap.Error(err))
		}
	})
}

// PartialImages reports the selected Chat provider's error and observed image
// count.
func (b *openAIChatAttemptBridge) PartialImages(err error) {
	b.reqLog.Warn("openai_chat_completions.forward_partial_error_with_image_result",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("image_count", b.result.ImageCount),
		zap.Error(err),
	)
}

// RetryReady accepts a classified Chat retry before downstream byte output
// changes. Callers supply a nonnil failure. A nonfailover cause or disconnected
// client ends recovery; warning recording is attempted before the writer-size
// comparison.
//
// Changed output invokes health observation and terminal error handling, then
// returns false so the text loop cannot append another provider's content.
// Unchanged output promotes streamStarted if heartbeat headers were already
// committed and permits retry, with schedule failure reported only when the
// classified upstream error requests that observation. Waiting and switch
// budgets remain with the outer text loop.
func (b *openAIChatAttemptBridge) RetryReady(failure *textflow.AttemptFailure) bool {
	err := failure.Cause
	var failoverErr *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		return false
	}
	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai_chat_completions.failover_aborted_client_disconnected",
			zap.Int64("provider_id", b.provider.Record.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
		)
		return false
	}
	b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.provider, b.reqModel, failoverErr.StatusCode, failoverErr.ResponseBody, err.Error())
	// Warning handling can already have written protocol output. Recheck
	// its byte count before accepting replay so the client cannot receive
	// content from another provider.
	if gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) != b.writerSizeBeforeForward {
		b.binding().observeOpenAIProviderHealthFailure(b.c.Request.Context(), b.provider, err)
		b.binding().handleFailoverExhausted(b.c, failoverErr, true)
		return false
	}
	// Replay may remain safe after early heartbeat headers were committed. Mark
	// the stream before another attempt so later exhaustion uses SSE framing.
	if b.streamStarted != nil && b.c.Writer.Written() {
		*b.streamStarted = true
	}
	if failoverErr.ShouldReportProviderScheduleFailure() {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.reqModel, false, nil), false, nil, err)
	}
	return true
}

// RetryWait logs the Chat provider's chosen retry count, allowance, and delay.
// The supplied failure carries UpstreamFailoverError metadata. The outer loop
// performs its context-aware wait; this callback records the selected retry
// parameters.
func (b *openAIChatAttemptBridge) RetryWait(failure *textflow.AttemptFailure, retryLimit, retryCount int, retryDelay time.Duration) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai_chat_completions.pool_mode_same_provider_retry",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("retry_limit", retryLimit),
		zap.Int("retry_count", retryCount),
		zap.Duration("retry_delay", retryDelay),
	)
}

// Switching records the Chat provider and the loop's switch count and limit.
// Callers supply a classified upstream failure. Provider exclusion and the next
// selection remain with the text loop rather than this diagnostic callback.
func (b *openAIChatAttemptBridge) Switching(failure *textflow.AttemptFailure, switchCount, maxProviderSwitches int) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai_chat_completions.upstream_failover_switching",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", switchCount),
		zap.Int("max_switches", maxProviderSwitches),
	)
}

// OtherFailure records Chat warnings and scheduling failure before supplying
// missing output. It prefers warning evidence carried by the error chain, then
// passes error text to the recorder when that evidence is absent.
//
// Communicated-error state and the heartbeat-adjusted writer size are checked after warning
// handling. A recorded warning allows fallback while size remains unchanged;
// communicated errors suppress that path. Once eligible, classified stream-read
// output takes precedence over the general OpenAI fallback, and the chosen
// output result is logged.
func (b *openAIChatAttemptBridge) OtherFailure(err error) {
	// A mixed-platform native executor can already have delivered a local
	// capability error. No provider failure or upstream warning follows that refusal.
	if gatewayhttp.IsResponseCommitted(b.c) && routing.IsModelAttributeError(err) {
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
	b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.reqModel, false, nil), false, nil, err)
	upstreamErrorAlreadyCommunicated := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	b.wroteFallback = false
	// Warning recording may already have supplied terminal output. Combine
	// its evidence with communicated-error and byte-count checks before
	// choosing another missing error response.
	if !upstreamErrorAlreadyCommunicated && (!recordedWarning || gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) == b.writerSizeBeforeForward) {
		// Prefer classified stream-read output before the generic
		// upstream fallback, preserving that failure's code and message
		// while emitting at most this selected missing error path.
		b.wroteFallback = b.binding().ensureOpenAIStreamReadErrorResponse(b.c, err, *b.streamStarted)
		if !b.wroteFallback {
			b.wroteFallback = b.binding().ensureOpenAIForwardErrorResponse(b.c, *b.streamStarted, err)
		}
	}
	b.reqLog.Warn("openai_chat_completions.forward_failed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Bool("fallback_error_response_written", b.wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	)
}

// Success reports Chat scheduling success with the final model and optional
// TTFT latency. A nil result still reports success with absent latency.
// Completion recording and funding effects are handled separately by the text
// loop.
func (b *openAIChatAttemptBridge) Success() {
	if b.result != nil {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.reqModel, false, b.result), true, b.result.Timings.TTFTMs())
	} else {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.reqModel, false, b.result), true, nil)
	}
}

// Completed records the serving Chat provider and final provider-switch count.
func (b *openAIChatAttemptBridge) Completed(switchCount int) {
	b.reqLog.Debug("openai_chat_completions.request_completed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("switch_count", switchCount),
	)
}

// CanAttempt reports whether the Chat client remains available for another
// provider attempt. It delegates that request check to FailoverClientGone.
// RetryReady separately checks written bytes when a completed forwarding
// attempt proposes recovery.
func (b *openAIChatAttemptBridge) CanAttempt() bool { return !gatewayhttp.FailoverClientGone(b.c) }

// Failed satisfies the Chat terminal hook after its protocol-specific failure
// notification.
func (b *openAIChatAttemptBridge) Failed() {}
