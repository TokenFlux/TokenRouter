package textattempt

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// native_gemini_attempts.go executes native Gemini attempts with
// provider-aware thought signature cleanup and usage capture. Gemini-native
// attempts can carry thought signatures whose ownership belongs to a previous
// provider. Selection checks that history before execution and rewrites the
// retained request when ownership changed or is initially unknown. The client
// action and mapped model remain separate from provider credentials and group
// pricing input.
//
// Capacity waiting and final forwarding use the HTTP request context, while
// generation preflight prices each selected attempt before upstream execution.
// Provider release runs when forwarding returns, including a local pricing
// rejection. Completion can save digest affinity before capturing admitted
// funding and usage, but that optional cache write failure does not suppress
// the usage submission.

// nativeGeminiAttemptBridge adapts Gemini-native requests with Google error
// output and Gemini wait handling.
type nativeGeminiAttemptBridge struct {
	messageAttemptBridge // embedded admitted native request state
	// modelName is the mapped native model; action is the requested Google
	// method name.
	modelName, action string
	stream            bool // native client streaming intent
	// native HTTP capacity helper
	geminiConcurrency *gatewayhttp.ConcurrencyHelper
	useDigestFallback bool // digest affinity persistence enabled
	// geminiDigestChain stores the current digest chain; geminiPrefixHash
	// stores its transcript prefix hash; geminiSessionUUID is the session
	// identifier; matchedDigestChain names the previously matched chain.
	// Complete saves these together when useDigestFallback is true and the
	// current chain and prefix hash are nonempty.
	geminiDigestChain, geminiPrefixHash, geminiSessionUUID, matchedDigestChain string
	// admitted group pricing mapping
	groupMapping routing.GroupMappingResult
	// serial thought-signature ownership tracker
	signatureState requeststate.GeminiSignatureState
}

// Select retains a Gemini-native target and applies provider-specific
// thought-signature cleanup before forwarding. Selection receives the requested
// model and excluded IDs so shared scheduling resolves group mapping, then
// publishes actual provider attribution.
//
// SignatureState can request cleanup for changed or initially unknown history
// ownership. The cleanup result replaces the retained request body, with
// recognized signatures changed to dummy values; later attempts and completion
// fingerprints read that retained result. This state belongs to one serial
// request, and callers acquire capacity before platform execution.
func (b *nativeGeminiAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	// Selection receives the client model because shared scheduling resolves
	// group mapping before validating the selected provider's supported model.
	var err error
	b.selection, err = b.binding().selectProvider(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.reqModel, excluded)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.provider = b.selection.Provider
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())
	change := b.signatureState.Select(b.provider.Record.ID, b.sessionKey != "", b.body)
	// Clean retained native signatures before another provider receives the
	// history, using the state's missing-owner flag to distinguish that
	// diagnostic from a known provider switch.
	if change.Clean {
		if change.Missing {
			b.reqLog.Info("gemini.sticky_session_binding_missing", zap.Bool("clean_thought_signature", true))
		} else {
			b.reqLog.Info("gemini.sticky_session_provider_switched", zap.Int64("from_provider_id", change.PreviousProviderID), zap.Int64("to_provider_id", b.provider.Record.ID), zap.Bool("clean_thought_signature", true))
		}
		b.body = protocolgemini.CleanNativeThoughtSignatures(b.body, bridge.DummyThoughtSignature)
	}
	return gatewaycapture.CaptureTextSelection(b.provider), nil
}

// FirstSelectionFailure presents unsupported group models using a Google
// permission error. Other failures use Gemini availability diagnosis; non-model
// failures retain selection details in their message and may record routing
// capacity limitation before Google error output.
func (b *nativeGeminiAttemptBridge) FirstSelectionFailure(err error, _ bool) {
	if handleGeminiGroupModelUnsupportedError(b.c, err) {
		return
	}
	cls := classifyNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.apiKey, b.reqModel, b.reqModel, capability.PlatformGemini)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available Gemini providers: " + err.Error()
	}
	gatewayhttp.WriteGoogleError(b.c, cls.Status, message)
}

// Acquire adopts Gemini provider ownership or registers waiting before bounded
// slot acquisition. Missing plans and denied queue admission write Google
// errors. The returned ownership is wrapped for request cancellation and later
// forwarding cleanup. Acceptance freezes initial HTTP admission before sticky
// persistence; retries preserve that first endpoint and isolate later waiting.
//
// Only allowed wait registrations are released, and WaitResult itself decides
// whether a counter was confirmed. Successful waiting removes queue occupancy
// before sticky binding; binding failure is diagnostic and retains the usable
// slot. Deferred wait cleanup covers failure and cancellation paths, while a
// locally cleared flag prevents repeated success cleanup from removing another
// entry.
func (b *nativeGeminiAttemptBridge) Acquire() bool {
	// Adopt active selection capacity before registering wait ownership;
	// waiting replaces the release callback only after its provider slot is
	// actually obtained.
	b.providerReleaseFunc = b.selection.ReleaseFunc
	if b.selection.Acquired {
		gatewayhttp.MarkOpsProviderSlotAcquired(b.c)
	} else {
		if b.selection.WaitPlan == nil {
			gatewayhttp.MarkOpsRoutingCapacityLimited(b.c)
			gatewayhttp.WriteGoogleError(b.c, http.StatusServiceUnavailable, "No available Gemini providers")
			return false
		}
		providerWaitCounted := false
		waitEntry, err := b.geminiConcurrency.EnterProviderWait(b.c.Request.Context(), b.provider.Record.ID, b.selection.WaitPlan.MaxWaiting)
		canWait := waitEntry.Allowed
		// An error entering wait registration leaves no acquired entry;
		// log it and proceed to the bounded provider acquisition path,
		// which retains its own error handling.
		if err != nil {
			b.reqLog.Warn("gemini.provider_wait_counter_increment_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		} else if !canWait {
			b.reqLog.Info("gemini.provider_wait_queue_full",
				zap.Int64("provider_id", b.provider.Record.ID),
				zap.Int("max_waiting", b.selection.WaitPlan.MaxWaiting),
			)
			gatewayhttp.WriteGoogleError(b.c, http.StatusTooManyRequests, "Too many pending requests, please retry later")
			return false
		}
		if err == nil && canWait {
			providerWaitCounted = true
		}
		// Waiting failure and cancellation still release an allowed
		// registration. WaitResult skips uncertain increments, and the
		// success path clears this local flag after early cleanup.
		defer func() {
			if providerWaitCounted {
				waitEntry.Release()
			}
		}()

		b.providerReleaseFunc, err = b.geminiConcurrency.AcquireProviderSlotWithWaitTimeout(
			b.c,
			b.provider.Record.ID,
			b.selection.WaitPlan.MaxConcurrency,
			b.selection.WaitPlan.Timeout,
			b.stream,
			b.streamStarted,
		)
		if err != nil {
			b.reqLog.Warn("gemini.provider_slot_acquire_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
			gatewayhttp.WriteGoogleError(b.c, http.StatusTooManyRequests, err.Error())
			return false
		}
		// Remove allowed wait ownership before publishing stickiness so
		// the selected active request no longer occupies a confirmed
		// queue entry during that cache write.
		if providerWaitCounted {
			waitEntry.Release()
			providerWaitCounted = false
		}
		gatewayhttp.MarkOpsProviderSlotAcquired(b.c)
		// Sticky persistence failure leaves the acquired slot available
		// to this attempt; keep the warning while returning ownership
		// with its cancellation-safe release.
		if err := b.binding().bindSticky(b.c.Request.Context(), b.apiKey.GroupID, b.sessionKey, b.provider.Record.ID); err != nil {
			b.reqLog.Warn("gemini.bind_sticky_session_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}
	// Wrap active provider ownership for cancellation as well as forwarding
	// return, sharing one release when request teardown and completion
	// overlap.
	b.providerReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.providerReleaseFunc)

	return true
}

// Forward executes the selected native action and releases its provider
// callback on return. Generation actions recheck the effective billing model
// price for every attempt; other actions delegate counting or action rejection
// to the platform.
//
// Missing price stops before forwarding with local Google policy output. Other
// pricing failures retain their cause and write a safe server error. A positive
// switch count is added to the execution context without replacing Gin context.
// After a platform call, scheduler feedback receives its success flag and any
// retained result; cleanup follows when this function returns. Partial results
// and typed failover remain separate loop evidence.
// @project-doc docs/domains/routing_and_billing.md#missing_model_pricing
func (b *nativeGeminiAttemptBridge) Forward(state textflow.AttemptState) textflow.Outcome {
	if b.providerReleaseFunc != nil {
		defer b.providerReleaseFunc()
	}

	requestCtx := b.c.Request.Context()
	if state.SwitchCount > 0 {
		requestCtx = requeststate.WithProviderSwitchCount(requestCtx, state.SwitchCount)
	}
	// Token counting is handled by the platform executor; each generation
	// attempt checks pricing again.
	if b.action == "generateContent" || b.action == "streamGenerateContent" {
		err := gatewayhttp.CheckTextModelPricing(
			requestCtx, b.binding().pricing, b.apiKey, b.provider,
			b.reqModel, b.modelName, nil, protocol.ProtocolGeminiGenerateContent,
		)
		if err != nil {
			if errors.Is(err, admission.ErrModelPricingRejected) {
				gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
				gatewayhttp.WriteGoogleError(b.c, http.StatusBadRequest, admission.ModelPricingUnavailableMessage)
			} else {
				b.reqLog.Error("gemini.pricing_check_failed", zap.Error(err))
				gatewayhttp.WriteGoogleError(b.c, http.StatusInternalServerError, "Failed to check model pricing")
			}
			return textflow.Outcome{Err: err, Stop: true}
		}
	}
	// Capture after admission and pricing so queue heartbeats cannot become real
	// output attributed to this forwarding attempt when deciding whether to retry.
	before := gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	var err error
	sessionGroupID := derefGroupID(b.apiKey.GroupID)
	if b.provider.Record.Platform == capability.PlatformAntigravity {
		b.result, err = b.binding().forwardAntigravityGemini(
			requestCtx,
			b.c,
			b.provider,
			b.modelName,
			b.action,
			b.stream,
			b.body,
			b.hasBoundSession,
			forwardcore.WithGeminiSession(sessionGroupID, b.sessionKey),
		)
	} else {
		b.result, err = b.binding().forwardGeminiNative(requestCtx, b.c, b.provider, b.modelName, b.action, b.stream, b.body)
	}
	b.binding().reportSchedule(b.selection, b.provider.Record.ID, err == nil, b.result)
	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) != before}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	if retry, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		out.Failure = &textflow.AttemptFailure{Cause: err, Policy: retry.RetryFailure()}
	}
	return out
}

// OtherFailure logs the native platform error after forwarding has supplied
// client presentation. It uses the current provider identity and retained
// request logger; the shared loop selects this path for an ordinary forwarding
// failure.
func (b *nativeGeminiAttemptBridge) OtherFailure(err error) {
	// The platform forwarder owns the client error response.
	b.reqLog.Error("gemini.forward_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
}

// Complete captures a nonnil native result and admitted funding for usage
// submission. Callers invoke it after forwarding has retained that result.
// state.ForceCacheBilling is copied into the completion input, while
// state.SwitchCount supplies the final request diagnostic.
//
// Digest fallback affinity is saved before capture when the retained chain and
// prefix qualify. A cache failure logs its cause and recording proceeds. The
// submitted callback uses captured request metadata; its failure diagnostic
// retains the actor identity and model from this call.
func (b *nativeGeminiAttemptBridge) Complete(state textflow.AttemptState) {
	completionActorID := b.subject.UserID
	completionModel := b.modelName
	// Capture request metadata while the HTTP context is available.
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)

	// Save qualifying digest affinity before usage capture so later
	// requests can match this provider history; cache failure remains
	// diagnostic and recording proceeds.
	if b.useDigestFallback && b.geminiDigestChain != "" && b.geminiPrefixHash != "" {
		if err := b.binding().saveGeminiSession(
			b.c.Request.Context(),
			derefGroupID(b.apiKey.GroupID),
			b.geminiPrefixHash,
			b.geminiDigestChain,
			b.geminiSessionUUID,
			b.provider.Record.ID,
			b.matchedDigestChain,
		); err != nil {
			b.reqLog.Warn("gemini.digest_session_save_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}

	// Prepare metadata for submission to the configured usage worker pool.
	requestPayloadHash := billing.HashUsageRequestPayload(b.body)
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.provider.Record.Platform)
	// Capture the cache-billing flag by value so completion does not retain
	// the failover body.
	forceCacheBilling := state.ForceCacheBilling

	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	// Capture admitted funding, usage and request attribution before
	// submission, letting the recorder run after Gin and provider attempt
	// state have moved on.
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
		ForceCacheBilling:  forceCacheBilling,
		APIKeyService:      b.binding().apiKeyService,
		ClientSessionID:    clientSessionID,
		PricingUsageFields: b.groupMapping.ToUsageFields(b.reqModel, b.result.UpstreamModel),
	})
	completionRuntime := b.binding().recorder
	b.binding().submitUsageRecordTask(b.c, func(ctx context.Context) {
		// Shared billing applies catalogue long-context tiers and the
		// group's pricing settings.
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			logging.L().With(
				zap.String("component", "handler.gemini_v1beta.models"),
				zap.Int64("user_id", completionActorID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Any("group_id", completionInput.APIKey.GroupID),
				zap.String("model", completionModel),
				zap.Int64("provider_id", completionInput.Provider.ID),
			).Error("gemini.record_usage_failed", zap.Error(err))
		}
	})
	b.reqLog.Debug("gemini.request_completed",
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.Int("switch_count", state.SwitchCount),
	)
}

// Begin enables the inherited retry hint when the admitted group has one
// provider. It uses the live request context and bound group checker; other
// groups leave the inherited hint unchanged before selection begins.
func (b *nativeGeminiAttemptBridge) Begin() {
	if b.binding().singleProviderGroup(b.Context(), b.apiKey.GroupID) {
		b.SingleProviderRetry()
	}
}

// PrepareAttempt accepts the native Gemini request already prepared by the HTTP
// entry.
func (b *nativeGeminiAttemptBridge) PrepareAttempt() bool { return true }

// Intercept reports no local probe interception for the native Gemini adapter.
func (b *nativeGeminiAttemptBridge) Intercept() bool { return false }

// Abandon requires no additional adapter-specific cleanup after native Gemini
// exclusions.
func (b *nativeGeminiAttemptBridge) Abandon(int64) {}

// Success adds no native-specific effects beyond the shared loop completion
// path.
func (b *nativeGeminiAttemptBridge) Success() {}

// Exhausted extracts typed upstream evidence when present and delegates final
// Google presentation. Missing or unrecognized causes pass nil to the bound
// exhaustion helper, allowing it to choose the shared no-provider fallback.
func (b *nativeGeminiAttemptBridge) Exhausted(failure *textflow.AttemptFailure, _ string, _ bool) {
	var original *forwardcore.UpstreamFailoverError
	if failure != nil {
		errors.As(failure.Cause, &original)
	}
	b.binding().handleGeminiFailoverExhausted(b.c, original)
}
