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
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// generic_chat_attempts.go executes generic Chat attempts with group model
// mapping, provider admission and completion capture.
// Group model mapping rebuilds a Chat request from retained client bytes at
// each attempt. A Gemini-valued request platform skips selected targets from
// other platforms after returning their capacity; ordinary platform dispatch
// receives the current Gin request context and its rebuilt body.
//
// The result and writer-size change answer separate loop questions. A result
// can retain partial usage while an error remains. Gin changes size from -1 to
// zero on header-only commitment without heartbeats, so a size change can
// close this attempt's retry window before body bytes exist; prior commitment can also predate the
// attempt. Provider ownership returns after the exchange before scheduling
// feedback, while completion later captures
// admitted funding and original request attribution. Missing compatibility
// services end before the exchange and release their selected capacity after
// presenting a local error.

// genericChatAttemptBridge holds forwarding state for one Chat Completions
// request.
type genericChatAttemptBridge struct {
	messageAttemptBridge                 // embedded admitted request attempt state
	requestCtx           context.Context // retained selection-loop context
	// prepared current forwarding request bytes
	forwardBody []byte
	// groupPlatform retains the request selection platform;
	// selectionSessionHash supplies its sticky selection identity. Forward
	// skips non-Gemini targets when groupPlatform equals Gemini.
	groupPlatform, selectionSessionHash string
	// admitted group model mapping
	groupMapping routing.GroupMappingResult
}

// Select obtains the current Chat provider target and retains its selection
// result for Acquire and Forward. Excluded IDs pass to shared selection, and
// successful selection records the actual provider platform for request Ops
// before returning loop metadata.
func (b *genericChatAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	var err error
	b.selection, err = b.binding().selectProvider(b.c.Request.Context(), b.apiKey.GroupID, b.selectionSessionHash, b.reqModel, excluded)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.provider = b.selection.Provider
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())
	return gatewaycapture.CaptureTextSelection(b.provider), nil
}

// FirstSelectionFailure first handles group business restrictions for Chat
// output. Remaining errors are diagnosed and refined for model or rate
// conditions; non-model failures append the selection error and may record
// routing capacity limitation before writing the client envelope.
func (b *genericChatAttemptBridge) FirstSelectionFailure(err error, _ bool) {
	if handleGroupSelectionBusinessError(b.c, err, *b.streamStarted, func(status int, errType string, message string, responseStarted bool) {
		b.binding().chatCompletionsErrorResponse(b.c, status, errType, message)
	}) {
		return
	}
	cls := classifyNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, b.groupPlatform)
	cls = gatewayhttp.RefineSelectionError(err, cls)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available providers: " + err.Error()
	}
	b.binding().chatCompletionsErrorResponse(b.c, cls.Status, cls.ErrType, message)
}

// Acquire adopts the selected provider release or waits for its plan when
// capacity was not acquired. A missing plan writes a no-provider error and
// returns false. Successful ownership is wrapped for request cancellation, and
// later forwarding or cleanup releases it idempotently. Successful acquisition
// freezes initial HTTP admission timing; retries preserve the first endpoint
// and record later waiting separately.
func (b *genericChatAttemptBridge) Acquire() bool {
	var err error
	// Adopt the scheduler's acquired ownership first; waiting obtains a
	// replacement callback only when selection returned a plan rather than
	// active capacity.
	b.providerReleaseFunc = b.selection.ReleaseFunc
	if !b.selection.Acquired {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			b.binding().chatCompletionsErrorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available providers")
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
			b.reqLog.Warn("gateway.cc.provider_slot_acquire_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
			b.binding().handleConcurrencyError(b.c, err, "provider", *b.streamStarted)
			return false
		}
	}
	gatewayhttp.MarkOpsProviderSlotAcquired(b.c)
	// Share provider cleanup between request cancellation and normal
	// forwarding return, allowing both paths to call the same lease-managed
	// callback safely.
	b.providerReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.providerReleaseFunc)

	return true
}

// Forward executes one Chat attempt from the retained client body, applying its
// group mapping for this provider. A Gemini-valued groupPlatform skips targets
// from other platforms after returning their slot; missing compatible services
// release ownership and stop with an error.
//
// Normal dispatch uses the current Gin request context, retains partial results
// and releases capacity before scheduler feedback. Any writer-size change
// closes the reported retry window, including Gin's -1-to-zero first header
// commitment. HTTPCommitted separately records whether this writer has
// committed at all. Beta denial and typed failover supply separate
// classifications so the shared loop can choose policy output, switching or
// completion capture.
func (b *genericChatAttemptBridge) Forward(_ textflow.AttemptState) textflow.Outcome {
	var err error

	// A Gemini-valued request platform excludes this selected non-Gemini
	// target here; return its capacity before asking the loop to skip that
	// attempt.
	if b.groupPlatform == capability.PlatformGemini && b.provider.Record.Platform != capability.PlatformGemini {
		if b.providerReleaseFunc != nil {
			b.providerReleaseFunc()
		}

		return textflow.Outcome{Skip: true}
	}
	// Compare size around this exchange for the loop's retry decision.
	// Recorded heartbeats keep the baseline at -1; header-only commitment
	// without recorded bytes still changes it to zero before body output.
	b.writerSizeBeforeForward = gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	b.forwardBody = b.body
	// Rebuild model replacement from retained client bytes for this
	// attempt, keeping the original requested name available for completion
	// pricing and attribution.
	if b.groupMapping.Mapped {
		b.forwardBody = b.binding().replaceModel(b.body, b.groupMapping.MappedModel)
	}
	gatewayhttp.SetActualUpstreamEndpoint(b.c, "")
	if b.provider.Record.Platform == capability.PlatformGemini {
		// A missing Gemini compatibility executor ends this attempt
		// before an exchange; present the local service error and
		// return its already acquired capacity.
		if !b.binding().geminiAvailable {
			b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Gemini compatibility service is not configured")
			if b.providerReleaseFunc != nil {
				b.providerReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		b.result, err = b.binding().forwardGeminiChat(b.c.Request.Context(), b.c, b.provider, b.forwardBody)
	} else if shouldUseAntigravityCompat(b.provider) {
		// A missing Antigravity compatibility executor ends this
		// attempt before an exchange; present the local service error
		// and return its already acquired capacity.
		if !b.binding().antigravityAvailable {
			b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadGateway, "upstream_error", "Antigravity compatibility service is not configured")
			if b.providerReleaseFunc != nil {
				b.providerReleaseFunc()
			}
			return textflow.Outcome{Stop: true}
		}
		gatewayhttp.SetActualUpstreamEndpoint(b.c, gatewayhttp.EndpointAntigravityGenerateContent)
		b.result, err = b.binding().forwardAntigravityChat(b.c.Request.Context(), b.c, b.provider, b.forwardBody, b.parsedReq)
	} else {
		b.result, err = b.binding().forwardChat(b.c.Request.Context(), b.c, b.provider, b.forwardBody, b.parsedReq)
	}

	// Return capacity after the exchange before scheduler feedback;
	// retained partial usage stays available while later completion
	// captures this attempt's result.
	if b.providerReleaseFunc != nil {
		b.providerReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.provider.Record.ID, err == nil, b.result)

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

// OtherFailure fills an uncommunicated Chat forwarding error and logs the
// result. Prior writer changes or already-communicated errors suppress
// duplicate client output; otherwise the shared error helper decides whether a
// final envelope can be written.
func (b *genericChatAttemptBridge) OtherFailure(err error) {
	upstreamErrorAlreadyCommunicated := gatewayhttp.ForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	wroteFallback := false
	// Fill an error only when the platform did not communicate this
	// failure, preventing a generic response from replacing already
	// delivered protocol error output.
	if !upstreamErrorAlreadyCommunicated {
		wroteFallback = b.binding().ensureForwardErrorResponse(b.c, *b.streamStarted)
	}
	b.reqLog.Error("gateway.cc.forward_failed",
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
func (b *genericChatAttemptBridge) Complete(_ textflow.AttemptState) {
	// Read client attribution while Gin is still owned by this request;
	// usage submission retains the captured values rather than revisiting
	// live headers.
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.provider.Record.Platform)

	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	gatewayhttp.StampForwardRequestedReasoningEffort(b.result, b.c)
	// Capture the result and admitted funding before asynchronous
	// submission, keeping subsequent request-state changes outside the
	// recorder's input snapshot.
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
			completionLog.Error("gateway.cc.record_usage_failed",
				zap.Int64("provider_id", completionInput.Provider.ID),
				zap.Error(err),
			)
		}
	})
}

// Context returns the retained selection context supplied when this bridge
// opened.
func (b *genericChatAttemptBridge) Context() context.Context { return b.requestCtx }

// Begin leaves this prepared Chat bridge ready for the shared loop.
func (b *genericChatAttemptBridge) Begin() {}

// PrepareAttempt accepts the Chat input already supplied by request
// preparation.
func (b *genericChatAttemptBridge) PrepareAttempt() bool { return true }

// Intercept reports no local probe interception for this generic Chat adapter.
func (b *genericChatAttemptBridge) Intercept() bool { return false }

// SingleProviderRetry leaves retry-context flags untouched for generic Chat
// attempts.
func (b *genericChatAttemptBridge) SingleProviderRetry() {}

// Abandon requires no additional session cleanup for this generic Chat adapter.
func (b *genericChatAttemptBridge) Abandon(int64) {}

// Success adds no adapter-specific effects after this generic Chat attempt.
func (b *genericChatAttemptBridge) Success() {}

// Exhausted presents the final Chat failure with streaming state from the loop
// or wait output. Typed failover retains upstream evidence for final rules;
// absent typed evidence uses the generic exhausted-provider response.
func (b *genericChatAttemptBridge) Exhausted(err *textflow.AttemptFailure, _ string, stream bool) {
	var original *forwardcore.UpstreamFailoverError
	if err != nil && errors.As(err.Cause, &original) {
		b.binding().handleCCFailoverExhausted(b.c, original, stream || *b.streamStarted)
	} else {
		b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadGateway, "server_error", "All available providers exhausted")
	}
}

// PolicyFailure presents a recognized beta-policy denial as an invalid request
// and local policy observation. Other error types make no change here, because
// the shared loop calls separate handlers for their failure classification.
func (b *genericChatAttemptBridge) PolicyFailure(err error) {
	if original, ok := errors.AsType[*anthropic.BetaBlockedError](err); ok {
		gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		b.binding().chatCompletionsErrorResponse(b.c, http.StatusBadRequest, "invalid_request_error", original.Message)
	}
}

// shouldUseAntigravityCompat reports whether the target is an Antigravity OAuth
// provider.
func shouldUseAntigravityCompat(provider *gatewaycapture.ExecutionProvider) bool {
	return provider != nil &&
		provider.Record.Platform == capability.PlatformAntigravity &&
		provider.Record.Type == capability.ProviderTypeOAuth
}
