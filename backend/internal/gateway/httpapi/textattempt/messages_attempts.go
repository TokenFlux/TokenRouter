package textattempt

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	queuepolicy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// messages_attempts.go prepares and executes Messages provider attempts and
// captures their usage for completion recording.
// Messages attempts retain client body/model identity while preparing
// group-mapped provider input. Selection and session tracking establish the
// target before slot acquisition; the serial text loop owns failover, while
// this bridge owns each forwarding operation and captures its completion
// observations. User-message queue policy can serialize or delay requests
// independently of provider concurrency.
//
// Serial leases can release on cancellation, upstream acceptance or return,
// using one wrapped callback and clearing the callback before later attempts.
// Output byte changes prevent replay after forwarding. Completion captures
// current group funding, pricing and cache-billing state before worker
// submission, allowing group fallback to replace its authorized snapshot
// without erasing triggered billing policy. Sticky success refreshes an
// existing provider binding only when selection retained that provider,
// preserving affinity when another provider temporarily served the request.

// messageAttemptBridge retains request-local Messages selection, resources and
// completion state for serial attempts.
type messageAttemptBridge struct {
	fixed *messageExecutionDependencies // shared bound execution services
	c     *gin.Context                  // active Gin request context
	// apiKey and currentAPIKey begin with the admitted Key; fallback
	// replaces both with its authorized group snapshot. Completion reads
	// currentAPIKey with currentSubscription, while subject remains the
	// original caller.
	// apiKey: admitted effective funding key.
	// currentAPIKey: current authorized funding key.
	apiKey, currentAPIKey *apikey.APIKey
	// authenticated caller and concurrency identity
	subject authctx.AuthSubject
	// subscription and currentSubscription begin with the optional admitted
	// funding choice. Authorized group fallback replaces both; Complete
	// reads currentSubscription alongside currentAPIKey for the served
	// attempt.
	// subscription: admitted optional funding subscription.
	// currentSubscription: current authorized funding subscription.
	subscription, currentSubscription *billing.UserSubscription
	// parsedReq and attemptParsedReq distinguish retained client parsing
	// from per-attempt model/body reconstruction. body and reqModel
	// preserve original identity for fallback rebuilding and completion
	// pricing.
	// parsedReq: retained parsed client input.
	// attemptParsedReq: current mapped provider input.
	parsedReq, attemptParsedReq *requeststate.ParsedRequest
	body                        []byte // retained original request bytes
	// reqModel: original client requested model.
	// platform: optional forced selection platform.
	// sessionKey: sticky conversation lookup identity.
	reqModel, platform, sessionKey string
	// reqStream: client streaming intent.
	// isClaudeCodeClient: detected Claude client flag.
	// hasBoundSession: observed sticky provider presence.
	reqStream, isClaudeCodeClient, hasBoundSession bool
	// streamStarted is shared with queue/slot waits and protocol error
	// output. Admission supplies a nonnil pointer throughout the
	// synchronous text loop.
	streamStarted          *bool       // shared HTTP streaming marker
	sessionBoundProviderID int64       // cached sticky provider identity
	reqLog                 *zap.Logger // request-scoped diagnostic logger
	// fallbackGroupID comes from the admitted group during Begin; replay
	// requires a distinct positive target and an authorized resolver before
	// changing current funding.
	fallbackGroupID *int64 // configured prompt-too-long target group
	// sessionAttempts is created by Begin for base Messages. Its tracked
	// entries own session outcomes and attached releases; Gemini overrides
	// do not create it here. request session/resource completion tracker
	sessionAttempts *scheduler.SessionAttempts
	// selection and provider belong to the current serial attempt; Acquire
	// sets providerReleaseFunc, Forward captures writerSizeBeforeForward
	// and result, and the text loop consumes those observations before
	// another Select.
	selection *gatewaycapture.SelectionResult // current provider selection result
	// current selected execution provider
	provider *gatewaycapture.ExecutionProvider
	// providerReleaseFunc wraps the selected slot release for cancellation
	// and normal forwarding return; tracked session entries can also own
	// that cleanup.
	providerReleaseFunc func() // wrapped current slot cleanup
	// current attempt model/pricing mapping
	attemptGroupMapping     routing.GroupMappingResult
	writerSizeBeforeForward int // forward response byte baseline
	// last observed forwarding result
	result *forwardcore.MessagesResult
}

// PrepareAttempt rebuilds mapped provider input under the current authorized
// Key. It retains original client model/body for subsequent attempts;
// preparation errors write an admission response and return false before
// provider selection proceeds.
func (b *messageAttemptBridge) PrepareAttempt() bool {
	var err error
	b.attemptParsedReq, b.attemptGroupMapping, err = b.binding().prepareGatewayAttemptRequest(
		b.c.Request.Context(), b.parsedReq, b.body, b.currentAPIKey, b.reqModel,
	)
	if err != nil {
		b.binding().errorResponse(b.c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return false
	}

	return true
}

// Select chooses and retains a provider under the current group, model,
// session and excluded identifiers. Begin and successful
// preparation precede serial use; a nonempty session key also registers this
// target in request session tracking.
func (b *messageAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	b.reqLog.Info("sticky.selecting_provider",
		zap.String("session_key", b.sessionKey),
		zap.Int64("sticky_bound_provider_id", b.sessionBoundProviderID),
		zap.Bool("has_bound_session", b.hasBoundSession),
		zap.Int("failed_provider_count", len(excluded)),
	)
	var err error
	b.selection, err = b.binding().selectProvider(b.c.Request.Context(), b.currentAPIKey.GroupID, b.sessionKey, b.reqModel, excluded)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.provider = b.selection.Provider
	gatewayhttp.SetOpsSelectedProvider(b.c, b.provider.Record.RoutingSnapshot())
	if b.sessionKey != "" {
		b.binding().trackSession(b.sessionAttempts, b.provider, b.sessionKey)
	}

	b.reqLog.Info("sticky.provider_selected",
		zap.Int64("selected_provider_id", b.provider.Record.ID),
		zap.String("provider_name", b.provider.Record.Name),
		zap.Bool("slot_acquired", b.selection.Acquired),
		zap.Bool("has_wait_plan", b.selection.WaitPlan != nil),
		zap.Int64("sticky_bound_provider_id", b.sessionBoundProviderID),
		zap.Bool("sticky_honored", b.sessionBoundProviderID > 0 && b.sessionBoundProviderID == b.provider.Record.ID),
	)

	return gatewaycapture.CaptureTextSelection(b.provider), nil
}

// FirstSelectionFailure presents an initial routing error using current
// funding and model context. Business errors take precedence over availability
// diagnosis; model-not-found retains its diagnostic message, while other cases
// record applicable capacity limitation and provider-unavailability details.
func (b *messageAttemptBridge) FirstSelectionFailure(err error, fallbackUsed bool) {
	if handleGroupSelectionBusinessError(b.c, err, *b.streamStarted, func(status int, errType string, message string, responseStarted bool) {
		b.binding().handleStreamingAwareError(b.c, status, errType, message, responseStarted)
	}) {
		return
	}
	cls := classifyNoProviderErrorFromGin(b.c, b.binding().diagnoser, b.currentAPIKey, b.reqModel, b.reqModel, b.platform)
	if !cls.ModelNotFound {
		gatewayhttp.MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
	}
	b.reqLog.Warn("gateway.select_provider_no_available",
		zap.String("model", b.reqModel),
		zap.Int64p("group_id", b.currentAPIKey.GroupID),
		zap.String("platform", b.platform),
		zap.Bool("fallback_used", fallbackUsed),
		zap.Bool("model_not_found", cls.ModelNotFound),
		zap.Error(err),
	)
	message := cls.Message
	if !cls.ModelNotFound {
		message = "No available providers: " + err.Error()
	}
	b.binding().handleStreamingAwareError(b.c, cls.Status, cls.ErrType, message, *b.streamStarted)
}

// Intercept writes a local warmup response when the selected provider enables
// interception and input matches a recognized probe. Callers Select first; an
// already acquired selection slot is released before local output, and true
// ends the text loop.
func (b *messageAttemptBridge) Intercept() bool {
	if b.provider.View().IsInterceptWarmupEnabled() {
		interceptType := clientmeta.DetectInterceptType(b.body, b.reqModel, b.parsedReq.MaxTokens, b.isClaudeCodeClient)
		if interceptType != clientmeta.InterceptTypeNone {
			// Warmup interception ends before normal Acquire or
			// Forward cleanup. Return any scheduler-owned slot here
			// so local probe output cannot retain provider capacity
			// after this request finishes.
			if b.selection.Acquired && b.selection.ReleaseFunc != nil {
				b.selection.ReleaseFunc()
			}
			if b.reqStream {
				gatewayhttp.WriteInterceptStream(b.c, b.reqModel, interceptType)
			} else {
				gatewayhttp.WriteInterceptResponse(b.c, b.reqModel, interceptType)
			}
			return true
		}
	}

	return false
}

// Acquire obtains the selected provider slot and reports whether forwarding
// can proceed. A retained selection lease is reused; otherwise a wait plan is
// required, and successful waiting best-effort binds sticky identity before
// execution. Acceptance freezes the initial HTTP admission timing before sticky
// persistence; later acquisitions retain that first endpoint.
//
// Queue registration errors allow uncounted waiting, but an explicit full
// queue rejects before slot polling. Only counted entries are released after
// timed acquisition or failure. Slot cleanup is wrapped for cancellation and
// attached to any tracked session attempt, sharing cleanup with early
// forwarding return and final session outcome handling.
func (b *messageAttemptBridge) Acquire() bool {
	// Preserve the selection’s acquired lease; only a missing lease path
	// joins its explicit wait plan before provider forwarding can use the
	// resource.
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
		// Registration failure leaves no counted waiter but permits
		// slot acquisition; an explicit full queue stops before polling
		// and cannot own a decrement.
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
		// Clear counted ownership when releasing so failed and
		// successful acquisition share cleanup without decrementing an
		// unregistered or already released entry.
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
		// Stop counting this request as waiting once its slot is
		// acquired.
		releaseWait()
		gatewayhttp.MarkOpsProviderSlotAcquired(b.c)
		b.reqLog.Info("sticky.bind_after_wait",
			zap.String("session_key", b.sessionKey),
			zap.Int64("provider_id", b.provider.Record.ID),
		)
		if err := b.binding().bindSticky(b.c.Request.Context(), b.currentAPIKey.GroupID, b.sessionKey, b.provider.Record.ID); err != nil {
			b.reqLog.Warn("gateway.bind_sticky_session_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}
	// Share cancellation cleanup with explicit forwarding release; session
	// tracking attaches this callback only when it already has an entry for
	// the provider.
	b.providerReleaseFunc = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, b.providerReleaseFunc)
	b.sessionAttempts.Own(b.provider.Record.ID, b.providerReleaseFunc)

	return true
}

// Forward applies queue policy and compatibility before dispatching the
// captured provider attempt. It requires Begin, preparation, selection and
// acquisition in the serial text loop; queue/throttle failures are logged
// while provider forwarding still proceeds.
//
// A serial lease is shared by cancellation and upstream acceptance. After
// platform dispatch returns, explicit cleanup releases it and clears the
// acceptance callback. A body-preparation error before dispatch returns before
// those steps: cleanup then relies on cancellation or a tracked session entry,
// and the callback remains on attemptParsedReq. Cache-billing policy is
// retained in request context for group fallback, while writer byte changes
// independently mark retry commitment. The
// returned outcome retains partial observations and distinguishes policy,
// prompt-too-long and classified failover errors for the outer loop.
func (b *messageAttemptBridge) Forward(state textflow.AttemptState) textflow.Outcome {
	var err error
	// Queue mode determines whether to serialize or apply a soft RPM delay.
	var queueRelease func()
	umqMode := b.binding().getUserMsgQueueMode(b.provider, b.attemptParsedReq)

	switch umqMode {
	case queuepolicy.MessageQueueSerialize:
		// Serialize requests under a lease and apply the provider's RPM
		// delay.
		baseRPM := gatewaycapture.ExecutionRuntimeConfig(b.provider).GetBaseRPM()
		release, qErr := b.binding().userMsgQueueHelper.AcquireWithWait(
			b.c, b.provider.Record.ID, baseRPM, b.reqStream, b.streamStarted,
			b.binding().messageWaitTimeout,
			b.reqLog,
		)
		if qErr != nil {
			// Queue-store failure does not become provider failure
			// here; retain its diagnostic and continue with the
			// independently acquired provider concurrency slot.
			b.reqLog.Warn("gateway.umq_acquire_failed",
				zap.Int64("provider_id", b.provider.Record.ID),
				zap.Error(qErr),
			)
		} else {
			queueRelease = release
		}

	case queuepolicy.MessageQueueThrottle:
		// Throttling controls dispatch timing separately from the
		// acquired provider slot; a delay error is logged below while
		// forwarding continues on that slot.
		baseRPM := gatewaycapture.ExecutionRuntimeConfig(b.provider).GetBaseRPM()
		if tErr := b.binding().userMsgQueueHelper.ThrottleWithPing(
			b.c, b.provider.Record.ID, baseRPM, b.reqStream, b.streamStarted,
			b.binding().messageWaitTimeout,
			b.reqLog,
		); tErr != nil {
			b.reqLog.Warn("gateway.umq_throttle_failed",
				zap.Int64("provider_id", b.provider.Record.ID),
				zap.Error(tErr),
			)
		}

	default:
		if umqMode != "" {
			b.reqLog.Warn("gateway.umq_unknown_mode",
				zap.String("mode", umqMode),
				zap.Int64("provider_id", b.provider.Record.ID),
			)
		}
	}

	// One wrapped serial lease serves cancellation, acceptance and return
	// cleanup, preventing those independently triggered paths from
	// releasing the same queue resource twice.
	queueRelease = scheduler.WrapRelease(b.c.Request.Context(), scheduler.ReleaseOnCancel, queueRelease)
	b.sessionAttempts.Own(b.provider.Record.ID, queueRelease)
	// Upstream acceptance ends queue serialization before stream
	// completion; use the wrapped callback so this early release also
	// unregisters its cancellation observer.
	b.attemptParsedReq.OnUpstreamAccepted = queueRelease

	// Replace parsed input with the Bedrock-compatible body before dispatch
	// so body-field and beta filtering applies consistently to either
	// selected forwarding path. Preparation failure precedes explicit
	// queue/provider cleanup and callback clearing below; wrapped
	// cancellation release and any tracked entry still own those resources
	// when this branch exits.
	if err := b.attemptParsedReq.ReplaceBody(b.binding().bedrockCompat(b.c, b.attemptParsedReq.Body.Bytes(), b.attemptParsedReq.Model, b.provider, b.currentAPIKey.GroupID)); err != nil {
		b.binding().errorResponse(b.c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return textflow.Outcome{Stop: true}
	}
	attemptBody := b.attemptParsedReq.Body.Bytes()

	b.c.Set("parsed_request", b.attemptParsedReq)

	requestCtx := b.c.Request.Context()
	if state.SwitchCount > 0 {
		requestCtx = requeststate.WithProviderSwitchCount(requestCtx, state.SwitchCount)
	}
	if state.ForceCacheBilling {
		// Carry triggered cache billing into response rewriting and
		// persist it on the HTTP context because group fallback
		// reconstructs subsequent attempt state.
		requestCtx = requeststate.WithForceCacheBilling(requestCtx)

		b.c.Request = b.c.Request.WithContext(requestCtx)
	}
	// Capture after queue waits so forwarding output can be distinguished
	// from earlier keepalives when the outer loop decides whether replay
	// remains permitted.
	b.writerSizeBeforeForward = gatewayhttp.HeartbeatAdjustedWrittenSize(b.c)
	if b.provider.Record.Platform == capability.PlatformAntigravity {
		b.result, err = b.binding().forwardAntigravity(requestCtx, b.c, b.provider, attemptBody, b.hasBoundSession)
	} else {
		b.result, err = b.binding().forwardMessages(requestCtx, b.c, b.provider, b.attemptParsedReq)
	}

	// The same wrapped callback also covers upstream failures before
	// acceptance, releasing any serial lease that no acceptance event has
	// yet ended.
	if queueRelease != nil {
		queueRelease()
	}
	// Remove this attempt’s acceptance callback after forwarding so
	// reconstructed or reused parsed input cannot invoke a prior provider’s
	// serial cleanup.
	b.attemptParsedReq.OnUpstreamAccepted = nil

	if b.providerReleaseFunc != nil {
		b.providerReleaseFunc()
	}
	b.binding().reportSchedule(b.selection, b.provider.Record.ID, err == nil, b.result)

	out := textflow.Outcome{Attempt: messageObservedAttempt(b.result, err), Err: err, HasResult: b.result != nil, OutputChanged: gatewayhttp.HeartbeatAdjustedWrittenSize(b.c) != b.writerSizeBeforeForward}
	out.Attempt.HTTPCommitted = b.c.Writer.Written()
	out.Attempt.RetryCommitted = out.OutputChanged
	var policy *anthropic.BetaBlockedError
	var prompt *antigravity.PromptTooLongError
	var retry *forwardcore.UpstreamFailoverError
	switch {
	case errors.As(err, &policy):
		out.Kind = textflow.FailurePolicy
	case errors.As(err, &prompt):
		out.Kind = textflow.FailurePromptTooLong
	case errors.As(err, &retry):
		out.Failure = &textflow.AttemptFailure{Cause: retry, Policy: retry.RetryFailure()}
	}
	return out
}

// Complete captures measured result and current authorized funding for
// asynchronous completion. A nil result does nothing; callers invoke it after
// the text loop selects served output, using the retained provider and mapped
// attempt state.
//
// Observed reasoning effort wins, then parsed output effort and an applicable
// thinking default fill missing effort. Attempt-body bytes supply payload
// hashing, original body bytes supply captured request reasoning, and group
// mapping preserves client/upstream pricing identities. The cache-billing flag
// combines current attempt state with retained fallback context before
// capture, so worker submission cannot erase billing policy or reread mutable
// Gin state.
func (b *messageAttemptBridge) Complete(state textflow.AttemptState) {
	usageResult := b.result

	if usageResult == nil {
		return
	}
	gatewayhttp.StampForwardRequestedReasoningEffort(usageResult, b.c)
	userAgent := b.c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(b.c)
	requestPayloadHash := billing.HashUsageRequestPayload(b.attemptParsedReq.Body.Bytes())
	inboundEndpoint := gatewayhttp.GetInboundEndpoint(b.c)
	upstreamEndpoint := gatewayhttp.GetUpstreamEndpoint(b.c, b.provider.Record.Platform)

	// Keep observed provider effort first; parsed effort and a thinking
	// default fill only missing observations using upstream model identity
	// before client identity.
	if usageResult.ReasoningEffort == nil {
		usageResult.ReasoningEffort = protocol.NormalizeClaudeOutputEffort(b.attemptParsedReq.OutputEffort)
	}
	if usageResult.ReasoningEffort == nil && b.attemptParsedReq.ThinkingEnabled {
		protocolModel := usageResult.UpstreamModel
		if protocolModel == "" {
			protocolModel = usageResult.Model
		}
		usageResult.ReasoningEffort = gatewaycapture.DefaultEffortForThinkingEnabled(protocolModel)
	}

	// Read both attempt and retained context billing flags before capture,
	// keeping a triggered provider-switch policy after the group fallback
	// loop resets state.
	forceCacheBilling := state.ForceCacheBilling || requeststate.IsForceCacheBilling(b.Context())

	clientSessionID := gatewayhttp.ExtractClientSessionID(b.c)
	// Snapshot current group funding and request data before submission, so
	// the worker records the completed attempt without consulting later
	// mutable request state.
	completionInput := gatewaycapture.CaptureMessages(gatewayhttp.CompletionContext(b.c), &gatewaycapture.MessagesCapture{
		Result: usageResult,

		APIKey:             b.currentAPIKey,
		User:               b.currentAPIKey.User,
		Provider:           gatewaycapture.ExecutionCompletionRecord(b.provider),
		Subscription:       b.currentSubscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		ClientSessionID:    clientSessionID,
		RequestPayloadHash: requestPayloadHash,
		RequestBody:        append([]byte(nil), b.body...),
		ForceCacheBilling:  forceCacheBilling,
		APIKeyService:      b.binding().apiKeyService,
		PricingUsageFields: b.attemptGroupMapping.ToUsageFields(b.reqModel, usageResult.UpstreamModel),
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

// OtherFailure presents any missing client failure and logs provider/proxy
// diagnostics. New non-SSE bytes can count as an already communicated failure
// without parsing the body; otherwise semantic-commit-aware fallback output
// decides whether another response is attempted.
//
// The forwarding byte baseline comes from this attempt, separating its
// response from earlier admission writes. SSE progress remains eligible for
// terminal handling, while an existing committed response stays unchanged
// through the bound fallback writer. The logged fallback flag reports the
// presentation decision, not a guarantee that all error bytes reached the
// client.
func (b *messageAttemptBridge) OtherFailure(err error) {
	upstreamErrorAlreadyCommunicated := gatewayhttp.ForwardErrorAlreadyCommunicated(b.c, b.writerSizeBeforeForward, err)
	wroteFallback := false
	if !upstreamErrorAlreadyCommunicated {
		wroteFallback = b.binding().ensureForwardErrorResponse(b.c, *b.streamStarted)
	}
	forwardFailedFields := []zap.Field{
		zap.Int64("provider_id", b.provider.Record.ID),
		zap.String("provider_name", b.provider.Record.Name),
		zap.String("provider_platform", b.provider.Record.Platform),
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
		zap.Error(err),
	}
	if b.provider.Record.Proxy != nil {
		forwardFailedFields = append(forwardFailedFields,
			zap.Int64("proxy_id", b.provider.Record.Proxy.ID),
			zap.String("proxy_name", b.provider.Record.Proxy.Name),
			zap.String("proxy_host", b.provider.Record.Proxy.Host),
			zap.Int("proxy_port", b.provider.Record.Proxy.Port),
		)
	} else if b.provider.Record.ProxyID != nil {
		forwardFailedFields = append(forwardFailedFields, zap.Int64p("proxy_id", b.provider.Record.ProxyID))
	}
	b.reqLog.Error("gateway.forward_failed", forwardFailedFields...)
}

// Success best-effort updates applicable RPM accounting and sticky affinity
// after successful forwarding. The RPM check and increment are separate
// operations, so this soft limit can be exceeded by concurrent requests and
// write failure is only logged.
//
// A nonempty sticky key is bound when no provider was previously bound or the
// selected provider matches that binding. A different selected provider leaves
// the existing association intact, letting later requests return to the
// original provider; sticky persistence failure does not undo the already
// served response.
func (b *messageAttemptBridge) Success() {
	// Count successful forwarding toward the provider's soft RPM limit.
	// Selection and increment are separate, so concurrent requests can
	// temporarily exceed the limit. A counter-write failure is logged after
	// the response has succeeded.
	if b.provider.View().IsAnthropicOAuthOrSetupToken() && gatewaycapture.ExecutionRuntimeConfig(b.provider).GetBaseRPM() > 0 {
		if err := b.binding().incrementRPM(b.c.Request.Context(), b.provider.Record.ID); err != nil {
			b.reqLog.Warn("gateway.rpm_increment_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}

	// Create or refresh a sticky binding after successful forwarding. A
	// first request creates the binding. Selecting the bound provider
	// refreshes its TTL. When load or RPM sends the request elsewhere, keep
	// the existing binding so later requests can return to that provider
	// after it recovers.
	if b.sessionKey != "" && (b.sessionBoundProviderID == 0 || b.sessionBoundProviderID == b.provider.Record.ID) {
		if err := b.binding().bindSticky(b.c.Request.Context(), b.currentAPIKey.GroupID, b.sessionKey, b.provider.Record.ID); err != nil {
			b.reqLog.Warn("gateway.bind_sticky_session_failed", zap.Int64("provider_id", b.provider.Record.ID), zap.Error(err))
		}
	}
}

// Context returns the active request context used by serial Messages attempts.
func (b *messageAttemptBridge) Context() context.Context { return b.c.Request.Context() }

// Begin initializes current funding, configured fallback target and request
// session tracking. Callers invoke it once before preparation/selection; a
// single-provider group installs the retry hint so outer recovery can use its
// corresponding policy.
func (b *messageAttemptBridge) Begin() {
	b.currentAPIKey = b.apiKey
	b.currentSubscription = b.subscription
	if b.apiKey.Group != nil {
		b.fallbackGroupID = b.apiKey.Group.FallbackGroupIDOnInvalidRequest
	}
	b.sessionAttempts = b.binding().newSessionAttempts()
	if b.binding().singleProviderGroup(b.Context(), b.currentAPIKey.GroupID) {
		b.SingleProviderRetry()
	}
}

// Finish delegates final served outcome to request session tracking when it
// exists. Callers run it after the text loop ends; the tracker coordinates
// session retention and any owned resource cleanup with earlier attempt
// releases.
func (b *messageAttemptBridge) Finish(served bool) {
	if b.sessionAttempts != nil {
		b.sessionAttempts.Finish(scheduler.AttemptOutcome{Served: served})
	}
}

// SingleProviderRetry installs the request hint used by single-provider
// recovery policy. Begin calls it after detecting one provider in the current
// group, before the text loop starts its attempt decisions.
func (b *messageAttemptBridge) SingleProviderRetry() {
	b.c.Request = b.c.Request.WithContext(requeststate.WithSingleProviderRetry(b.Context(), true))
}

// Canceled delegates client-gone presentation to the shared HTTP helper. A
// canceled request retains already written status or selects 499 before
// output; the text loop ends cancellation handling without using the helper’s
// recognition result.
func (b *messageAttemptBridge) Canceled() { gatewayhttp.FailoverClientGone(b.c) }

// Exhausted presents final classified failover using the selected or retained
// platform. Nil failure delegates generic 502 using current stream state; only
// an UpstreamFailoverError cause receives detailed presentation, and other
// causes produce no output here.
func (b *messageAttemptBridge) Exhausted(err *textflow.AttemptFailure, platform string, forceStream bool) {
	if platform == "" {
		platform = b.platform
	}
	if err == nil {
		b.binding().handleFailoverExhaustedSimple(b.c, 502, *b.streamStarted)
		return
	}
	if original, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err.Cause); ok {
		b.binding().handleFailoverExhausted(b.c, original, platform, forceStream || *b.streamStarted)
	}
}

// PolicyFailure presents a recognized Anthropic beta-block policy error and
// records local business limitation. Other error types do nothing; callers
// classify the error before invoking this terminal policy path.
func (b *messageAttemptBridge) PolicyFailure(err error) {
	if original, ok := errors.AsType[*anthropic.BetaBlockedError](err); ok {
		gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		b.binding().errorResponse(b.c, http.StatusBadRequest, "invalid_request_error", original.Message)
	}
}

// Switched delegates provider-switch observation for the current retained
// selection result.
func (b *messageAttemptBridge) Switched() {
	b.binding().providerSwitched(b.selection)
}

// Abandon delegates prior-provider session cleanup before selection switches
// to another target. Begin must have created session tracking; its tracked
// resources and session state finish as an unserved attempt.
func (b *messageAttemptBridge) Abandon(id int64) { b.sessionAttempts.Abandon(id) }

// TempUnscheduleRetryableError delegates temporary scheduling suppression for
// an UpstreamFailoverError cause. Other causes do nothing; callers provide the
// current recovery context and provider identity after the text loop
// classifies a retryable failure.
func (b *messageAttemptBridge) TempUnscheduleRetryableError(ctx context.Context, id int64, err *textflow.AttemptFailure) {
	if original, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err.Cause); ok {
		b.binding().tempUnschedule(ctx, id, original)
	}
}
