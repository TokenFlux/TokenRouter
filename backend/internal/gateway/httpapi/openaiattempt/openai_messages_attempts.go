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

// openai_messages_attempts.go manages Messages attempt models, retry readiness
// and completion capture.
// Messages attempts retain the provider-layer model separately from the
// original client model and use a request-local cache for group-mapped body
// bytes. Each selected provider receives those bytes with its own
// TLS/client-policy check, and the common bridge supplies capacity and admitted
// funding state.
//
// Native interception stops execution before Cyber and completion observation.
// Otherwise the attempt separates result presence, images, native partial
// usage, and commitment for the text loop. Reasoning-limit rejection stops a
// nonimage result locally; usage-bearing partial results remain available to
// completion. Writer-size changes close retry readiness, and terminal failures
// use Messages output while successful completion captures the
// Messages-specific group mapping rather than the embedded Responses mapping.

// openAIMessageAttemptBridge adds Messages model mapping and body caching to
// shared attempts.
type openAIMessageAttemptBridge struct {
	responsesAttemptBridge // embedded admitted request state
	// providerLayerModel, currentRoutingModel, and promptCacheKey belong to
	// this admitted Messages request. Group fallback replaces routing
	// identities while prompt-cache identity remains available to
	// selected-provider conversion.
	// providerLayerModel: mapped provider selection model.
	// currentRoutingModel: scheduling feedback model name.
	// promptCacheKey: retained Messages cache identity.
	providerLayerModel, currentRoutingModel, promptCacheKey string
	// groupMappingMsg retains the Messages group mapping used for mapped
	// input and completion pricing. Group fallback updates it after the
	// shared destination switch.
	groupMappingMsg routing.GroupMappingResult
	// mappedBodyForMessages caches request-local model replacement results.
	// Forward reads its retained bytes as immutable across serial provider
	// selections and retries.
	mappedBodyForMessages func(bool, string) []byte
}

// Select resolves a Messages provider with separate client and provider-layer
// models. Selection errors return to the loop. An unavailable target is
// diagnosed using the routed model and displayed with the client name;
// successful selection retains provider/session identity and publishes platform
// observations.
func (b *openAIMessageAttemptBridge) Select(excluded map[int64]struct{}) (textflow.ResponseSelection, error) {
	b.reqLog.Debug("openai_messages.provider_selecting", zap.Int("excluded_provider_count", len(excluded)))
	var scheduleDecision scheduler.PlatformDecision
	var err error
	b.selection, scheduleDecision, err = b.binding().selectProviderWithSchedulerForCapabilityAndRoutingModel(
		b.c.Request.Context(),
		b.apiKey.GroupID,
		"", // no previous_response_id
		b.sessionHash,
		b.reqModel,
		b.providerLayerModel,
		excluded, egress.OpenAIUpstreamTransportAny, provider.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
		b.requestPlatform,
	)
	if err != nil {
		return textflow.ResponseSelection{}, err
	}
	if b.selection == nil || b.selection.Provider == nil {
		cls := ClassifyOpenAICompatibleNoProviderErrorFromGin(b.c, b.binding().resolvedDiagnoser, b.apiKey, b.providerLayerModel, b.reqModel)
		if !cls.ModelNotFound {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
		}
		b.binding().anthropicStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, *b.streamStarted)
		return textflow.ResponseSelection{}, nil
	}
	b.provider = b.selection.Provider
	b.sessionHash = EnsureOpenAIPoolModeSessionHash(b.sessionHash, b.provider)
	b.reqLog.Debug("openai_messages.provider_selected", zap.Int64("provider_id", b.provider.Record.ID), zap.String("provider_name", b.provider.Record.Name))
	_ = scheduleDecision
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())

	return b.selectedView(), nil
}

// SelectionFailure formats terminal Messages selection output while the client
// remains connected. Initial nonnil errors check business handling before
// routed-model diagnosis. After exclusions, captured failover evidence uses
// Anthropic exhaustion output; missing evidence writes the default upstream
// failure.
func (b *openAIMessageAttemptBridge) SelectionFailure(err error, excludedCount int, last *textflow.AttemptFailure) {
	var lastFailoverErr *forwardcore.UpstreamFailoverError
	if last != nil {
		errors.As(last.Cause, &lastFailoverErr)
	}

	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai_messages.provider_select_aborted_client_disconnected", zap.Error(err))
		return
	}
	b.reqLog.Warn("openai_messages.provider_select_failed",
		zap.Error(gatewayhttp.OpenAICompatibleSelectionErrorForLog(err, b.requestPlatform)),
		zap.Int("excluded_provider_count", excludedCount),
	)
	if excludedCount == 0 {
		if err != nil {
			if b.binding().handleOpenAISelectionBusinessError(b.c, err, *b.streamStarted) {
				return
			}
			cls := ClassifyOpenAICompatibleNoProviderErrorFromGin(b.c, b.binding().resolvedDiagnoser, b.apiKey, b.providerLayerModel, b.reqModel)
			if !cls.ModelNotFound {
				gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
			}
			b.binding().anthropicStreamingAwareError(b.c, cls.Status, cls.ErrType, cls.Message, *b.streamStarted)
			return
		}
	} else {
		if lastFailoverErr != nil {
			b.binding().handleAnthropicFailoverExhausted(b.c, lastFailoverErr, *b.streamStarted)
		} else {
			b.binding().anthropicStreamingAwareError(b.c, http.StatusBadGateway, "api_error", "Upstream request failed", *b.streamStarted)
		}
		return
	}
}

// Forward executes group-mapped Messages input with the current selected
// provider. It binds the shared native stream marker and obtains immutable
// mapped bytes from the request-local cache. Provider cleanup runs after policy
// or exchange return, and native interception returns a stop outcome before
// later request observations.
//
// Other results record Cyber handling and measured response latency,
// subtracting upstream time only when it fits inside the total duration. Native
// usage, images, HTTP commitment, and retry classification remain independent
// outcome evidence. A reasoning-effort limit error without images sets Stop
// locally; the text loop handles remaining partial-result and completion
// decisions.
func (b *openAIMessageAttemptBridge) Forward() textflow.ResponseOutcome {
	gatewayhttp.BindNativeMessageStreamState(b.c, b.streamStarted)
	var err error
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsRoutingLatencyMsKey, time.Since(b.routingStart).Milliseconds())
	forwardStart := time.Now()

	// Read mapped input from this request's canonical cache before platform
	// conversion, keeping each provider attempt from inheriting mutable
	// output from the previous conversion.
	attemptBody := b.mappedBodyForMessages(b.groupMappingMsg.Mapped, b.groupMappingMsg.MappedModel)
	b.writerSizeBeforeForward = gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	// Retire provider capacity after policy rejection or exchange return,
	// while retaining the result for native interception, Cyber processing,
	// and completion capture by the loop.
	b.result, err = func() (*forwardcore.OpenAIResult, error) {
		defer func() {
			if b.providerReleaseFunc != nil {
				b.providerReleaseFunc()
			}
		}()
		tlsRouterMatch := b.binding().matchOpenAITLSFingerprintRouterForRequest(b.c, b.provider)
		if err := b.binding().enforceOpenAIClientPolicyForRequest(b.c.Request.Context(), b.c, b.provider, attemptBody, tlsRouterMatch); err != nil {
			return nil, err
		}
		return b.binding().forwardAsAnthropic(b.c.Request.Context(), b.c, b.provider, attemptBody, b.promptCacheKey, b.providerLayerModel, tlsRouterMatch)
	}()
	if gatewayhttp.NativeMessageIntercepted(b.c) {
		return textflow.ResponseOutcome{Outcome: textflow.Outcome{Stop: true}}
	}
	var cyberBlockBodyMsg []byte
	if gatewayhttp.GetOpsCyberPolicy(b.c) != nil {
		cyberBlockBodyMsg = b.body
	}
	b.cyberPolicyHandled = b.binding().recordCyberPolicyIfMarked(b.c, b.apiKey, b.provider, b.subscription, b.reqModel, err != nil, cyberBlockBodyMsg, gatewayhttp.ClientRequestedUsageFields(b.c, b.groupMappingMsg, b.reqModel, ""), billing.HashUsageRequestPayload(b.body))
	forwardDurationMs := time.Since(forwardStart).Milliseconds()
	upstreamLatencyMs, _ := GetContextInt64(b.c, gatewayhttp.OpsUpstreamLatencyMsKey)
	responseLatencyMs := forwardDurationMs
	// Subtract the positive upstream phase only when measured total time
	// contains it; other observations keep total duration instead of a
	// negative response-latency estimate.
	if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
		responseLatencyMs = forwardDurationMs - upstreamLatencyMs
	}
	gatewayhttp.SetOpsLatencyMs(b.c, gatewayhttp.OpsResponseLatencyMsKey, responseLatencyMs)

	out := textflow.ResponseOutcome{Outcome: textflow.Outcome{Attempt: openAIObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil}, Images: b.result != nil && b.result.ImageCount > 0}
	out.NativePartial = err != nil && b.result != nil && b.result.NativeUsage != nil && (b.result.NativeUsage.HasObservedTokens() || b.result.ImageCount > 0)
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	// Local reasoning limits end a nonimage request before provider retry.
	// Image-bearing partial results remain available to the common loop's
	// existing completion and error decisions.
	if err != nil && !out.Images {
		if overLimit, ok := errors.AsType[*routing.ReasoningEffortOverLimitError](err); ok {
			b.reqLog.Info("openai_messages.reasoning_effort_policy_denied", zap.String("reason", overLimit.Error()))
			out.Stop = true
			return out
		}
	}
	if retry, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out
}

// Complete captures Messages usage, request hash, and its own group pricing
// mapping. A nil result skips submission. The queued task retains captured
// completion input and logging identity values, allowing Recorder to run
// independently of subsequent HTTP bridge changes.
func (b *openAIMessageAttemptBridge) Complete() {
	res := b.result

	if res == nil {
		return
	}
	completionInput := b.captureCompletion(gatewaycapture.OpenAICapture{
		Result:             res,
		RequestPayloadHash: billing.HashUsageRequestPayload(b.body),
		PricingUsageFields: b.groupMappingMsg.ToUsageFields(b.reqModel, res.UpstreamModel),
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitOpenAIUsageRecordTask(b.c, res, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, true); err != nil {
			logging.L().With(
				zap.String("component", "handler.openai_gateway.messages"),
				zap.Int64("user_id", completionUserID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("provider_id", completionInput.Provider.ID),
			).Error("openai_messages.record_usage_failed", zap.Error(err))
		}
	})
}

// PartialImages reports the selected Messages provider's error and observed
// image count.
func (b *openAIMessageAttemptBridge) PartialImages(err error) {
	b.reqLog.Warn("openai_messages.forward_partial_error_with_image_result",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("image_count", b.result.ImageCount),
		zap.Error(err),
	)
}

// RetryReady accepts classified Messages recovery before downstream output size
// changes. A nonfailover cause or client disconnection ends recovery. Callers
// supply a nonnil failure, and warning recording runs before the byte baseline
// is checked.
//
// Changed output delegates health observation and Anthropic terminal error
// handling before returning false, protecting the current stream from a second
// provider's content. Otherwise the classified error decides whether scheduling
// failure is reported using currentRoutingModel. The text loop owns retry
// waiting and provider switch allowances after this readiness callback returns
// true.
func (b *openAIMessageAttemptBridge) RetryReady(failure *textflow.AttemptFailure) bool {
	err := failure.Cause
	var failoverErr *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		return false
	}
	if gatewayhttp.FailoverClientGone(b.c) {
		b.reqLog.Info("openai_messages.failover_aborted_client_disconnected",
			zap.Int64("provider_id", b.provider.Record.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
		)
		return false
	}
	b.binding().recordOpenAICyberWarning(b.c, b.reqLog, b.apiKey, b.provider, b.reqModel, failoverErr.StatusCode, failoverErr.ResponseBody, err.Error())
	// Cyber warning handling can emit Messages output. Recheck actual bytes
	// against the captured baseline before replay so another provider
	// cannot extend that started response.
	if gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) != b.writerSizeBeforeForward {
		b.binding().observeOpenAIProviderHealthFailure(b.c.Request.Context(), b.provider, err)
		b.binding().handleAnthropicFailoverExhausted(b.c, failoverErr, true)
		return false
	}
	if failoverErr.ShouldReportProviderScheduleFailure() {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.currentRoutingModel, false, nil), false, nil, err)
	}
	return true
}

// RetryWait logs Messages same-provider retry parameters from the classified
// failure. Callers supply UpstreamFailoverError evidence with retry allowance,
// count, and delay. The outer loop owns waiting and cancellation after this
// observation.
func (b *openAIMessageAttemptBridge) RetryWait(failure *textflow.AttemptFailure, retryLimit, retryCount int, retryDelay time.Duration) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai_messages.pool_mode_same_provider_retry",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("retry_limit", retryLimit),
		zap.Int("retry_count", retryCount),
		zap.Duration("retry_delay", retryDelay),
	)
}

// Switching logs the Messages provider and the loop's chosen switch budget.
// Callers supply a classified upstream failure. The text loop owns provider
// exclusion and subsequent selection after the callback records this
// transition.
func (b *openAIMessageAttemptBridge) Switching(failure *textflow.AttemptFailure, switchCount, maxProviderSwitches int) {
	var failoverErr *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &failoverErr)
	b.reqLog.Warn("openai_messages.upstream_failover_switching",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", switchCount),
		zap.Int("max_switches", maxProviderSwitches),
	)
}

// OtherFailure supplies missing Messages failure output after warning and
// scheduling observations. A result reporting client disconnection returns
// after its disconnect log, keeping that path separate from warning handling
// and fallback output.
//
// Other failures prefer warning evidence from the error chain before recording
// error text. After those callbacks, communicated-error evidence and the raw
// writer size decide whether a default Anthropic response is needed. A recorded
// warning permits fallback while size remains unchanged; communicated errors
// suppress it. Diagnostics retain the selected fallback and communication
// observations.
func (b *openAIMessageAttemptBridge) OtherFailure(err error) {
	// A mixed-platform native executor can already have delivered a local
	// capability error. No provider failure or upstream warning follows that refusal.
	if gatewayhttp.IsResponseCommitted(b.c) && routing.IsModelAttributeError(err) {
		return
	}

	if b.result != nil && b.result.ClientDisconnect {
		b.reqLog.Info("openai_messages.client_disconnected",
			zap.Int64("provider_id", b.provider.Record.ID),
			zap.Error(err),
		)
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
	b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.currentRoutingModel, false, b.result), false, nil, err)
	upstreamErrorAlreadyCommunicated := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	b.wroteFallback = false
	// Cyber warning callbacks can write Messages errors. Recheck
	// communication and byte observations before supplying a default
	// response so their terminal output is retained.
	if !upstreamErrorAlreadyCommunicated && (!recordedWarning || gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) == b.writerSizeBeforeForward) {
		b.wroteFallback = b.binding().ensureAnthropicErrorResponse(b.c, *b.streamStarted)
	}
	b.reqLog.Warn("openai_messages.forward_failed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Bool("fallback_error_response_written", b.wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	)
}

// Success reports Messages scheduling success under currentRoutingModel and
// observed latency. Missing result reports success with nil TTFT
// latency. Request completion and settlement use their independently captured
// result rather than this feedback callback.
func (b *openAIMessageAttemptBridge) Success() {
	if b.result != nil {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.currentRoutingModel, false, b.result), true, b.result.Timings.TTFTMs())
	} else {
		b.binding().reportOpenAIProviderScheduleResult(b.provider, OpenAIProviderScheduleModel(b.c, b.provider, b.currentRoutingModel, false, b.result), true, nil)
	}
}

// Completed records the serving Messages provider and final provider-switch
// count.
func (b *openAIMessageAttemptBridge) Completed(switchCount int) {
	b.reqLog.Debug("openai_messages.request_completed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("switch_count", switchCount),
	)
}

// CanAttempt reports whether the Messages client remains connected for provider
// execution. It delegates request disconnection detection to
// FailoverClientGone. Completed-attempt output and retry permission are checked
// separately by RetryReady.
func (b *openAIMessageAttemptBridge) CanAttempt() bool { return !gatewayhttp.FailoverClientGone(b.c) }

// Failed satisfies the Messages terminal hook after protocol-specific failure
// handling.
func (b *openAIMessageAttemptBridge) Failed() {}

// Exhausted writes Messages terminal recovery output for the last failure. A
// classified upstream cause uses Anthropic failover formatting. Missing or
// unclassified failure data writes the default 502 with the current stream
// marker.
func (b *openAIMessageAttemptBridge) Exhausted(failure *textflow.AttemptFailure) {
	var original *forwardcore.UpstreamFailoverError
	if failure != nil && errors.As(failure.Cause, &original) {
		b.binding().handleAnthropicFailoverExhausted(b.c, original, *b.streamStarted)
	} else {
		b.binding().anthropicStreamingAwareError(b.c, http.StatusBadGateway, "api_error", "Upstream request failed", *b.streamStarted)
	}
}
