package textattempt

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// generic_responses_attempts.go executes generic Responses attempts using
// retained selection context and prepared forwarding input.
// Generic Responses dispatch receives the prepared forwarding bytes and
// selection context retained when its bridge opens. Selection and platform
// callbacks use that retained context, while capacity acquisition reads the
// current Gin request context. Selection copies the provider's diagnostic header
// policy into the retained context before each dispatch, while other Gin context
// changes leave the execution argument's cancellation and request values intact.
//
// An exchange records HTTP commitment separately from a writer-size change.
// Any size change closes its retry window, including Gin's -1-to-zero first
// header commitment before body output. A partial result can still accompany
// an error. The adapter returns provider capacity before scheduler
// feedback, then completion captures admitted funding and request attribution
// for asynchronous recording. Unconfigured compatibility branches stop with
// their client error and release the selected capacity before any platform
// exchange occurs.

// genericResponsesAttemptBridge holds request-local state for the shared text attempt loop.
type genericResponsesAttemptBridge struct {
	messageAttemptBridge                            // embedded admitted request attempt state
	requestCtx           context.Context            // retained selection-loop context
	forwardBody          []byte                     // prepared current forwarding request bytes
	groupMapping         routing.GroupMappingResult // admitted group model mapping
}

// Select obtains the current Responses provider target and retains its
// selection result for Acquire and Forward. Excluded IDs pass to shared
// selection, and successful selection records the actual provider platform for
// request Ops before returning loop metadata.
func (b *genericResponsesAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	var err error
	b.selection, err = b.binding().selectProvider(b.requestCtx, b.apiKey.GroupID, b.sessionKey, b.reqModel, excluded)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.provider = b.selection.Provider
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())
	// Forward retains the selection context independently of Gin. Copy the chosen
	// diagnostic policy into that context before its upstream callbacks dispatch.
	selected := telemetry.RequestProvider(b.c.Request.Context())
	b.requestCtx = telemetry.WithRequestProvider(b.requestCtx, selected.ProviderID, selected.Header)
	return gatewaycapture.CaptureTextSelection(b.provider), nil
}

// FirstSelectionFailure diagnoses initial Responses provider rejection and
// writes its client envelope. Model configuration failures retain the diagnosed
// message; other failures append the selection error text and may record
// routing capacity limitation. Concrete rate evidence refines the diagnosed
// fallback.
func (b *genericResponsesAttemptBridge) FirstSelectionFailure(err error, _ bool) {
	if gatewayhttp.WriteOpenAIModelAttributeError(b.c, err, *b.streamStarted) {
		return
	}
	cls := classifyNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, gatewayhttp.EffectiveAPIKeyPlatform(b.c, b.apiKey))
	cls = gatewayhttp.RefineSelectionError(err, cls)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available providers: " + err.Error()
	}
	b.binding().responsesErrorResponse(b.c, cls.Status, cls.ErrType, message)
}

// Acquire adopts the selected provider release or waits for its plan when
// capacity was not acquired. A missing plan writes a no-provider error and
// returns false. Successful ownership is wrapped for request cancellation, and
// later forwarding or cleanup releases it idempotently. Successful acquisition
// freezes initial HTTP admission timing; retries preserve the first endpoint
// and record later waiting separately.
func (b *genericResponsesAttemptBridge) Acquire() bool {
	var err error
	// Adopt the scheduler's acquired ownership first; waiting obtains a replacement
	// callback only when selection returned a plan rather than active capacity.
	b.providerReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			b.binding().responsesErrorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available providers")
			return false
		}
		b.providerReleaseFunc, err = b.binding().concurrencyHelper.AcquireProviderSlotWithWaitTimeout(
			b.c,
			b.provider.Record.ID,
			b.selection.WaitPlan.MaxConcurrency,
			b.selection.WaitPlan.Timeout,
			b.reqStream,
			b.streamStarted,
		)
		if err != nil {
			b.reqLog.Warn("gateway.responses.provider_slot_acquire_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
			b.binding().handleConcurrencyError(b.c, err, "provider", *b.streamStarted)
			return false
		}
	}
	gatewayhttp.MarkOpsProviderSlotAcquired(b.c)
	// Share provider cleanup between request cancellation and normal forwarding
	// return, allowing both paths to call the same lease-managed callback safely.
	b.providerReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.providerReleaseFunc)

	return true
}

// Forward executes one Responses attempt using the retained target and prepared
// request state. Missing platform compatibility writes an error and releases
// provider ownership before stopping; normal dispatch retains the result even
// when execution also fails.
//
// The provider release runs before scheduling feedback. A writer-size change
// closes this attempt's retry window, including Gin's -1-to-zero first header
// commitment; HTTPCommitted separately records overall commitment. Beta denial
// and typed failover carry their separate failure classifications; completion
// can still use a nonnil partial result after an ordinary execution error.
func (b *genericResponsesAttemptBridge) Forward(_ textflow.AttemptState) textflow.Outcome {
	var err error
	// Compare adjusted size around this exchange for retry decisions. Recorded
	// heartbeats preserve -1, while headers alone retain Gin's zero-byte state.
	b.writerSizeBeforeForward = gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	gatewayhttp.SetActualUpstreamEndpoint(b.c, "")
	if b.provider.Record.Platform == capability.PlatformGemini {
		// A missing Gemini compatibility executor ends this attempt before an
		// exchange; present the local service error and return its already acquired
		// capacity.
		if !b.binding().geminiAvailable {
			b.binding().responsesErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Gemini compatibility service is not configured")
			if b.providerReleaseFunc != nil {
				b.providerReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		gatewayhttp.SetActualUpstreamEndpoint(b.c, gatewayhttp.EndpointGeminiModels)
		b.result, err = b.binding().forwardGeminiResponses(b.requestCtx, b.c, b.provider, b.forwardBody, b.parsedReq)
	} else if shouldUseAntigravityCompat(b.provider) {
		// A missing Antigravity compatibility executor ends this attempt before an
		// exchange; present the local service error and return its already acquired
		// capacity.
		if !b.binding().antigravityAvailable {
			b.binding().responsesErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Antigravity compatibility service is not configured")
			if b.providerReleaseFunc != nil {
				b.providerReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		gatewayhttp.SetActualUpstreamEndpoint(b.c, gatewayhttp.EndpointAntigravityGenerateContent)
		b.result, err = b.binding().forwardAntigravityResponses(b.requestCtx, b.c, b.provider, b.forwardBody, b.parsedReq)
	} else {
		b.result, err = b.binding().forwardResponses(b.requestCtx, b.c, b.provider, b.forwardBody, b.parsedReq)
	}

	// Return capacity after the exchange before scheduler feedback; retained
	// partial usage stays available while later completion captures this attempt's
	// result.
	if b.providerReleaseFunc != nil {
		b.providerReleaseFunc()
	}
	// Attribute validation can stop this attempt before dispatch, so these
	// errors carry no provider outcome to record in scheduling feedback.
	if !routing.IsModelAttributeError(err) {
		b.binding().reportSchedule(b.selection, b.provider.Record.ID, err == nil, b.result)
	}

	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) != b.writerSizeBeforeForward}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	var policy *anthropic.BetaBlockedError
	var retry *forwardcore.UpstreamFailoverError
	switch {
	case errors.As(err, &policy):
		out.Kind = textflow.FailurePolicy
	case errors.As(err, &retry):
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out
}

// OtherFailure fills an uncommunicated Responses forwarding error and logs the
// result. Prior writer changes or already-communicated errors suppress
// duplicate client output; otherwise the shared error helper decides whether a
// final envelope can be written.
func (b *genericResponsesAttemptBridge) OtherFailure(err error) {
	if gatewayhttp.WriteOpenAIModelAttributeError(b.c, err, *b.streamStarted) {
		return
	}
	upstreamErrorAlreadyCommunicated := gatewayhttp.ForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	wroteFallback := false
	// Fill an error only when the platform did not communicate this failure,
	// preventing a generic response from replacing already delivered protocol
	// error output.
	if !upstreamErrorAlreadyCommunicated {
		wroteFallback = b.binding().ensureForwardErrorResponse(b.c, *b.streamStarted)
	}
	b.reqLog.Error("gateway.responses.forward_failed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	)
}

// Complete captures a nonnil retained result with admitted funding, provider
// metadata and request attribution before submitting usage work. The supplied
// loop state is ignored, including ForceCacheBilling. The callback retains its
// completion snapshot and logger so recording does not revisit live Gin state.
func (b *genericResponsesAttemptBridge) Complete(_ textflow.AttemptState) {
	// Read client attribution while Gin is still owned by this request; usage
	// submission retains the captured values rather than revisiting live headers.
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.provider.Record.Platform)

	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	gatewayhttp.StampForwardRequestedReasoningEffort(b.result, b.c)
	// Capture the result and admitted funding before asynchronous submission,
	// keeping subsequent request-state changes outside the recorder's input snapshot.
	completionInput := gatewaycapture.CaptureMessages(gatewayhttp.CompletionContext(b.c), &gatewaycapture.MessagesCapture{
		Result: b.result,

		APIKey:             b.apiKey,
		User:               b.apiKey.User,
		Provider:           gatewaycapture.ExecutionCompletionRecord(b.provider),
		Subscription:       b.subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        b.body,
		APIKeyService:      b.binding().apiKeyService,
		ClientSessionID:    clientSessionID,
		PricingUsageFields: b.groupMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
	})
	completionRuntime := b.binding().recorder
	completionLog := b.reqLog
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			completionLog.Error("gateway.responses.record_usage_failed",
				zap.Int64("provider_id", completionInput.Provider.ID),
				zap.Error(err),
			)
		}
	})
}

// Context returns the retained selection context supplied when this bridge
// opened.
func (b *genericResponsesAttemptBridge) Context() context.Context { return b.requestCtx }

// Begin leaves this prepared Responses bridge ready for the shared loop.
func (b *genericResponsesAttemptBridge) Begin() {}

// PrepareAttempt accepts the Responses input already supplied by request
// preparation.
func (b *genericResponsesAttemptBridge) PrepareAttempt() bool { return true }

// Intercept reports no local probe interception for this generic Responses
// adapter.
func (b *genericResponsesAttemptBridge) Intercept() bool { return false }

// SingleProviderRetry leaves retry-context flags untouched for generic
// Responses attempts.
func (b *genericResponsesAttemptBridge) SingleProviderRetry() {}

// Abandon requires no additional session cleanup for this generic Responses
// adapter.
func (b *genericResponsesAttemptBridge) Abandon(int64) {}

// Success adds no adapter-specific effects after this generic Responses
// attempt.
func (b *genericResponsesAttemptBridge) Success() {}

// Exhausted presents the final Responses failure with streaming state from the
// loop or wait output. Typed failover retains upstream evidence for final
// rules; absent typed evidence uses the generic exhausted-provider response.
func (b *genericResponsesAttemptBridge) Exhausted(err *textflow.AttemptFailure, _ string, stream bool) {
	var original *forwardcore.UpstreamFailoverError
	if err != nil && errors.As(err.Cause, &original) {
		b.binding().handleResponsesFailoverExhausted(b.c, original, stream || *b.streamStarted)
	} else {
		b.binding().responsesErrorResponse(b.c, http.StatusBadGateway, "server_error", "All available providers exhausted")
	}
}

// PolicyFailure presents a recognized beta-policy denial as an invalid request
// and local policy observation. Other error types make no change here, because
// the shared loop calls separate handlers for their failure classification.
func (b *genericResponsesAttemptBridge) PolicyFailure(err error) {
	if original, ok := errors.AsType[*anthropic.BetaBlockedError](err); ok {
		gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		b.binding().responsesErrorResponse(b.c, http.StatusBadRequest, "invalid_request_error", original.Message)
	}
}
