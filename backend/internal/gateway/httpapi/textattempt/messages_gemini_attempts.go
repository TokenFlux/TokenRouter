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
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// messages_gemini_attempts.go executes Gemini-backed Messages attempts and
// captures their original client attribution and usage.
// Forced Gemini-backed Messages retains its own mapped model and body beside
// original client input. Provider selection uses the request's group, model
// and sticky identity, and forwarding chooses native Gemini or
// Antigravity Gemini for the selected platform. The shared text loop still
// owns provider failover and interprets returned output observations.
//
// Selection leases are reused or obtained under a wait plan; only counted
// waiters are released, and acquired slots share cancellation cleanup with
// forwarding return. Byte changes mark retry commitment independently of
// physical writer state. Completion fills missing reasoning from client
// parsing and model policy, then captures original input, group pricing and
// the attempt cache-billing flag before asynchronous recording.

// geminiMessageAttemptBridge adapts mapped Gemini Messages input to serial
// text attempts.
type geminiMessageAttemptBridge struct {
	messageAttemptBridge // shared request-local attempt observations
	// forwardModel, forwardBody and groupMapping come from forced-Gemini
	// preparation, while embedded body and reqModel retain client input for
	// completion metadata and pricing identity.
	forwardModel string                     // prepared mapped Gemini model
	forwardBody  []byte                     // prepared mapped Gemini request bytes
	groupMapping routing.GroupMappingResult // retained group pricing/model mapping
}

// Select retains a Gemini-capable provider under request group, model, session
// and exclusions. A successful selection stores the executable target for
// acquisition and publishes its routing snapshot to the request's Ops
// observations before returning loop metadata.
func (b *geminiMessageAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	var err error
	b.selection, err = b.binding().selectProvider(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.reqModel, excluded)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.provider = b.selection.Provider
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())
	return gatewaycapture.CaptureTextSelection(b.provider), nil
}

// FirstSelectionFailure presents an initial Gemini routing failure after
// checking business errors. Model-not-found retains diagnosis, while other
// failures record applicable capacity limitation and provider-unavailability
// details; the unused bool does not alter this path.
func (b *geminiMessageAttemptBridge) FirstSelectionFailure(err error, _ bool) {
	if handleGroupSelectionBusinessError(b.c, err, *b.streamStarted, func(status int, errType string, message string, responseStarted bool) {
		b.binding().handleStreamingAwareError(b.c, status, errType, message, responseStarted)
	}) {
		return
	}
	cls := classifyNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, capability.PlatformGemini)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	b.reqLog.Warn("gateway.select_provider_no_available",
		zap.String("model", b.reqModel),
		zap.Int64p("group_id", b.apiKey.GroupID),
		zap.String("platform", b.platform),
		zap.Bool("model_not_found", cls.ModelNotFound),
		zap.Error(err),
	)
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available providers: " + err.Error()
	}
	b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, message, *b.streamStarted)
}

// Acquire reuses the selected provider lease or waits under its required plan.
// Missing plan rejects forwarding; after a successful wait it best-effort
// binds sticky identity, while an already acquired selection skips that bind.
// Acceptance freezes initial HTTP admission before sticky persistence; retries
// retain the first acquisition endpoint and record their waiting separately.
//
// Registration errors permit uncounted waiting and explicit queue refusal
// rejects before slot polling. Counted ownership is released after acquisition
// or failure, then provider cleanup is wrapped for cancellation. Unlike base
// Messages, this method does not attach that callback to SessionAttempts;
// Forward owns its explicit release when the attempt returns.
func (b *geminiMessageAttemptBridge) Acquire() bool {
	// Preserve a selection-owned lease; waiting is used only when selection did
	// not acquire the provider resource and supplied its explicit wait plan.
	b.providerReleaseFunc = b.selection.ReleaseFunc
	if b.selection.Acquired {
		gatewayhttp.MarkOpsProviderSlotAcquired(b.c)
	} else {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			b.reqLog.Warn("gateway.select_provider_no_slot_no_wait_plan",
				zap.Int64("provider_id", b.provider.Record.ID),
				zap.String("model", b.reqModel),
				zap.String("platform", b.platform),
			)
			b.binding().handleStreamingAwareError(b.c, http.StatusServiceUnavailable, "api_error", "No available providers", *b.streamStarted)
			return false
		}
		providerWaitCounted := false
		waitEntry, err := b.binding().concurrencyHelper.EnterProviderWait(b.c.Request.Context(), b.provider.Record.ID, b.selection.WaitPlan.MaxWaiting)
		canWait := waitEntry.Allowed
		// Counter errors permit uncounted waiting, while explicit refusal rejects
		// before slot polling and never owns a queue entry that needs cleanup.
		if err != nil {
			b.reqLog.Warn("gateway.provider_wait_counter_increment_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		} else if !canWait {
			b.reqLog.Info("gateway.provider_wait_queue_full",
				zap.Int64("provider_id", b.provider.Record.ID),
				zap.Int("max_waiting", b.selection.WaitPlan.MaxWaiting),
			)
			b.binding().handleStreamingAwareErrorWithCode(b.c, http.StatusTooManyRequests, "rate_limit_error", gatewayhttp.GatewayQueueFullCode, "Too many pending requests, please retry later", *b.streamStarted)
			return false
		}
		if err == nil && canWait {
			providerWaitCounted = true
		}
		// Release only an owned registration and clear its flag so both acquisition
		// outcomes can share cleanup without decrementing a failed or released entry.
		releaseWait := func() {
			if providerWaitCounted {
				waitEntry.Release()
				providerWaitCounted = false
			}
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
			b.reqLog.Warn("gateway.provider_slot_acquire_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
			releaseWait()
			b.binding().handleConcurrencyError(b.c, err, "provider", *b.streamStarted)
			return false
		}
		// Stop counting this request as waiting once its slot is acquired.
		releaseWait()
		gatewayhttp.MarkOpsProviderSlotAcquired(b.c)
		if err := b.binding().bindSticky(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.provider.Record.ID); err != nil {
			b.reqLog.Warn("gateway.bind_sticky_session_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}
	// Share explicit forwarding cleanup with request cancellation through the
	// lease wrapper, keeping either path from returning the acquired slot twice.
	b.providerReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.providerReleaseFunc)

	return true
}

// Forward dispatches retained Gemini input through the selected platform and
// returns attempt observations. Callers first Select and Acquire, and keep the
// request-local bridge serial; Antigravity receives session options, while
// other selected platforms use the native Gemini forwarding callback.
//
// The byte baseline is captured after acquisition and before output,
// separating retry commitment from already written HTTP headers. Forward
// releases the provider lease before schedule reporting and preserves partial
// result/error observations. A classified UpstreamFailoverError supplies retry
// policy to the text loop, which decides later recovery rather than replaying
// here.
func (b *geminiMessageAttemptBridge) Forward(state textflow.AttemptState) textflow.Outcome {
	var err error
	// Carry the attempt's provider-switch state into forwarding.

	requestCtx := b.c.Request.Context()
	if state.SwitchCount > 0 {
		requestCtx = requeststate.WithProviderSwitchCount(requestCtx, state.SwitchCount)
	}
	// Capture after slot waits so retry commitment observes this forwarding’s
	// bytes independently of headers or keepalives already written during admission.
	b.writerSizeBeforeForward = gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	if b.provider.Record.Platform == capability.PlatformAntigravity {
		b.result, err = b.binding().forwardAntigravityGemini(
			requestCtx,
			b.c,
			b.provider,
			b.forwardModel,
			"generateContent",
			b.reqStream,
			b.forwardBody,
			b.hasBoundSession,
			forwardcore.WithGeminiSession(derefGroupID(b.apiKey.GroupID), b.sessionKey),
		)
	} else {
		b.result, err = b.binding().forwardGemini(requestCtx, b.c, b.provider, b.forwardBody)
	}
	if b.providerReleaseFunc != nil {
		b.providerReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.provider.Record.ID, err == nil, b.result)
	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) != b.writerSizeBeforeForward}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	if retry, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		out.Failure = &textflow.AttemptFailure{Cause: retry, Policy: retry.RetryFailure()}
	}
	return out
}

// Success best-effort increments RPM after a successful eligible Anthropic
// OAuth/setup-token provider attempt. A positive configured base limit is
// required; selection and increment remain separate, and counter-write failure
// is logged without replacing served output.
func (b *geminiMessageAttemptBridge) Success() {
	// Count successful forwarding toward the provider's soft RPM limit.
	// Selection and increment are separate, so concurrent requests can temporarily exceed the limit.
	// A counter-write failure is logged after the response has succeeded.
	if b.provider.View().IsAnthropicOAuthOrSetupToken() && gatewaycapture.ExecutionRuntimeConfig(b.provider).GetBaseRPM() > 0 {
		if err := b.binding().incrementRPM(b.c.Request.Context(), b.provider.Record.ID); err != nil {
			b.reqLog.Warn("gateway.rpm_increment_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}
}

// Complete captures a served Gemini-backed Messages result for completion
// recording. It requires a nonnil result and retained parsed client request;
// observed reasoning wins before parsed output effort and a model-dependent
// thinking default fill missing effort.
//
// Original body supplies the request fingerprint and copied completion input,
// while group mapping retains requested/upstream pricing identities. The
// attempt cache-billing flag is captured by value before provider completion
// capture and worker submission. Recorder errors are logged using captured
// identities, keeping asynchronous work independent of later Gin reads.
func (b *geminiMessageAttemptBridge) Complete(state textflow.AttemptState) {
	// Capture request metadata while the HTTP context is available.
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.provider.Record.Platform)

	// Retain observed effort first, then fill from client output intent and an
	// applicable thinking default selected by upstream model before client model.
	if b.result.ReasoningEffort == nil {
		b.result.ReasoningEffort = protocol.NormalizeClaudeOutputEffort(b.parsedReq.OutputEffort)
	}
	if b.result.ReasoningEffort == nil && b.parsedReq.ThinkingEnabled {
		protocolModel := b.result.UpstreamModel
		if protocolModel == "" {
			protocolModel = b.result.Model
		}
		b.result.ReasoningEffort = gatewaycapture.DefaultEffortForThinkingEnabled(protocolModel)
	}

	// Retain the attempt’s cache-billing choice as a scalar before asynchronous
	// submission, so completion does not depend on mutable recovery state afterward.
	forceCacheBilling := state.ForceCacheBilling

	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	gatewayhttp.StampForwardRequestedReasoningEffort(b.result, b.c)
	// Snapshot funding, pricing and copied client body before worker submission,
	// keeping later recording tied to this completed attempt rather than mutable HTTP state.
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
		ClientSessionID:    clientSessionID,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        append([]byte(nil), b.body...),
		ForceCacheBilling:  forceCacheBilling,
		APIKeyService:      b.binding().apiKeyService,
		PricingUsageFields: b.groupMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
	})
	completionUserID := b.subject.UserID
	completionModel := b.reqModel
	completionRuntime := b.binding().recorder
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			logging.L().With(
				zap.String("component", "handler.gateway.messages"),
				zap.Int64("user_id", completionUserID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("provider_id", completionInput.Provider.ID),
			).Error("gateway.record_usage_failed", zap.Error(err))
		}
	})
}

// Begin initializes current funding and the single-provider recovery hint for
// this bridge. It deliberately does not create base Messages session tracking
// or prepare another body, because forced-Gemini input was captured before
// this loop.
func (b *geminiMessageAttemptBridge) Begin() {
	b.currentAPIKey = b.apiKey
	b.currentSubscription = b.subscription
	if b.binding().singleProviderGroup(b.Context(), b.apiKey.GroupID) {
		b.SingleProviderRetry()
	}
}

// PrepareAttempt reports readiness using the forced-Gemini input already
// captured at Open. It always returns true and performs no parsing or
// validation here, so callers retain the prepared forwarding body and model.
func (b *geminiMessageAttemptBridge) PrepareAttempt() bool { return true }

// Abandon leaves prior-provider session cleanup absent for this Gemini bridge.
// Selection does not register base Messages SessionAttempts here, so the
// unused identifier has no per-attempt session resource to finish.
func (b *geminiMessageAttemptBridge) Abandon(int64) {}

// Exhausted delegates final attempt failure under the Gemini platform
// identity. It ignores the supplied platform string, retaining the stream flag
// for the base bridge’s classified or generic exhaustion presentation.
func (b *geminiMessageAttemptBridge) Exhausted(err *textflow.AttemptFailure, _ string, stream bool) {
	b.messageAttemptBridge.Exhausted(err, capability.PlatformGemini, stream)
}
