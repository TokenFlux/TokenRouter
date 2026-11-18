package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
)

// qoder_compatible_attempt.go manages compatible Qoder attempts, refresh
// recovery and provider-capacity release.
// A compatible Qoder attempt retains its selected target and acquired slot
// across serial Select, Acquire and Forward calls. Forward releases the slot
// before reporting selection feedback, then returns independent observations
// of partial service, cancellation and changed output. The text loop uses
// those observations to decide whether another attempt is permitted.
//
// The initial attempt captures writer size after waiting, while a refresh
// retry captures it before waiting; retry heartbeats therefore count as output
// changed for that attempt. A ready refresh replaces the target, pending
// refresh produces retry guidance, and failed refresh leaves no ready result.
// Partial completion bypasses success binding, while known failures or changed
// output stop replay through the bridge's Failure result.

// qoderCompatibleAttemptBridge retains mutable request state for serial Qoder
// compatibility attempts.
type qoderCompatibleAttemptBridge struct {
	h         *QoderCompatibleRuntime // shared compatible runtime owner
	c         *gin.Context            // active Gin request context
	endpoint  QoderEndpoint           // selected client protocol format
	key       *apikey.APIKey          // captured authenticated request key
	subjectID int64                   // authenticated user identity
	// hash: sticky session lookup identity
	// model: original requested client model
	hash, model string
	stream      bool // client stream intent
	// streamStarted is shared with HTTP waits and failure presentation.
	// Admission supplies a nonnil pointer that remains valid throughout the
	// serial loop. shared stream output marker
	streamStarted *bool
	body          []byte      // group-mapped attempt input bytes
	log           *zap.Logger // request-scoped diagnostic logger
	// completion capture and submission callback
	record func(QoderCompatibleTarget, *forwardcore.MessagesResult)
	// partial completion and error callback
	partial func(QoderCompatibleTarget, *forwardcore.MessagesResult, error) bool
	// selection and provider belong to the current serial attempt. Acquire
	// sets release and writerSize; Forward stores result, releases the
	// slot, then reports feedback before the loop decides completion or
	// recovery.
	selection QoderCompatibleSelection    // current provider selection handle
	provider  QoderCompatibleTarget       // current selected execution target
	result    *forwardcore.MessagesResult // last forwarding result observations
	// release follows explicit Forward completion for streams; buffered
	// requests also share its idempotent cleanup with request cancellation.
	release    func() // current wrapped slot cleanup
	writerSize int    // attempt response byte baseline
}

// Context returns the active request context used by Qoder attempt operations.
func (b *qoderCompatibleAttemptBridge) Context() context.Context { return b.c.Request.Context() }

// Select captures a provider and selection handle under the current excluded
// identifiers. Callers run attempts serially; successful selection installs
// its provider identity in Ops before acquisition and forwarding use the
// retained target.
func (b *qoderCompatibleAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, error) {
	selected, err := b.h.options.Execution.Select(b.Context(), b.key.GroupID, b.hash, b.model, excluded, b.subjectID)
	if err != nil {
		return textflow.Selection{}, err
	}
	b.selection = selected
	b.provider = selected.Target()
	SetOpsSelectedProvider(b.c, b.provider.Snapshot())
	return textflow.Selection{Provider: b.provider.Snapshot()}, nil
}

// SelectionFailed ends provider selection using pending-refresh and
// previous-attempt evidence supplied by the text loop. Pending refresh takes
// precedence, then first-selection business errors, while later failure can
// retain recognized details from the preceding attempt.
//
// The first failure records capacity diagnostics before selecting a client
// error. Later exhaustion skips output after context cancellation and
// otherwise asks the shared Qoder presenter to preserve the last recognized
// failure; an unrecognized last error receives generic provider exhaustion. No
// further attempt is acquired by this method.
func (b *qoderCompatibleAttemptBridge) SelectionFailed(err error, pending, first bool, last error) {
	if pending {
		b.RefreshPending(*b.streamStarted)
		return
	}
	if first {
		MarkOpsRoutingCapacityLimitedIfNoAvailable(b.c, err)
		if WriteGroupSelectionBusinessError(b.c, err, *b.streamStarted, b.h.options.ReadAccess, b.h.options.Errors.Catalogue, func(status int, kind, message string, started bool) {
			b.h.streamingAwareError(b.c, status, kind, message, started, b.endpoint)
		}) {
			return
		}
		b.h.errorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available providers: "+err.Error(), b.endpoint)
		return
	}
	// This loop passes its last ordinary error directly rather than
	// retaining an Anthropic FailoverState, so exhaustion cannot apply that
	// state’s single-provider backoff.
	if b.Context().Err() != nil {
		return
	}
	if b.h.writeQoderFailoverExhaustedError(b.c, b.endpoint, *b.streamStarted, last) {
		return
	}
	b.h.errorResponse(b.c, http.StatusBadGateway, "upstream_error", "All available providers exhausted", b.endpoint)
}

// Acquire obtains the selected provider slot and reports whether forwarding
// can proceed. Callers first Select a usable target; initial acquisition
// reuses an already acquired lease or its wait plan, while a refresh retry
// reacquires through runtime slot handling. Errors are presented and return
// false.
//
// The retry byte baseline precedes its wait, with recorded heartbeat bytes
// excluded from both observations. Initial acquisition captures size after
// waiting instead. Successful cleanup is wrapped for the request’s stream mode,
// letting Forward explicitly release streams while buffered cancellation can
// also release the same lease.
func (b *qoderCompatibleAttemptBridge) Acquire(retry bool) bool {
	var err error
	if retry {
		// Capture before the retry wait and subtract its heartbeat bytes.
		// Only business output closes replay; the initial acquisition
		// instead captures after its admission wait.
		b.writerSize = HeartbeatAdjustedWrittenSize(b.c)
		b.release, err = b.h.acquireQoderRetryProviderSlot(b.c, qoderTargetSnapshot(b.provider), b.selection, b.stream, b.streamStarted)
	} else {
		b.release = b.selection.ReleaseFunc()
		if !b.selection.Acquired() {
			if b.selection.WaitPlan() == nil {
				MarkOpsRoutingCapacityLimited(b.c)
				b.h.errorResponse(b.c, http.StatusServiceUnavailable, "api_error", "No available providers", b.endpoint)
				return false
			}
			b.release, err = b.h.acquireQoderProviderSlotWithWait(b.c, qoderTargetSnapshot(b.provider), b.selection.WaitPlan(), b.stream, b.streamStarted, b.log)
		}
	}
	if err != nil {
		event := "qoder.provider_slot_acquire_failed"
		if retry {
			event = "qoder.provider_retry_slot_acquire_failed"
		}
		b.log.Warn(event, zap.Int64("provider_id", b.provider.Snapshot().ID), zap.Error(err))
		b.h.handleConcurrencyError(b.c, err, "provider", *b.streamStarted, b.endpoint)
		return false
	}
	MarkOpsProviderSlotAcquired(b.c)
	b.release = wrapQoderReleaseOnDone(b.Context(), b.release, b.stream)
	if !retry {
		b.writerSize = HeartbeatAdjustedWrittenSize(b.c)
	}
	return true
}

// Forward dispatches retained attempt bytes through the client wire protocol
// and returns recovery observations. It requires successful acquisition and
// serial use of the captured target; it stores any partial result, releases
// its slot and reports provider feedback before the text loop handles
// completion.
//
// A result accompanying an error becomes Partial, context.Canceled becomes
// Canceled, and writer size changes become OutputChanged. Refresh eligibility
// is classified with a false stream argument here because the outer text loop
// separately gates refresh on changed output; failover eligibility is also
// returned independently for that loop to interpret.
func (b *qoderCompatibleAttemptBridge) Forward() textflow.QoderCompatibleOutcome {
	var err error
	switch b.endpoint {
	case QoderChat:
		b.result, err = b.provider.Forward(b.Context(), b.c, b.body, protocol.ProtocolOpenAIChatCompletions, b.model)
	case QoderResponses:
		b.result, err = b.provider.Forward(b.Context(), b.c, b.body, protocol.ProtocolOpenAIResponses, b.model)
	default:
		b.result, err = b.provider.Forward(b.Context(), b.c, b.body, protocol.ProtocolAnthropicMessages, b.model)
	}
	// Finish this slot before reporting outcome so refresh or provider
	// selection cannot retain the prior attempt’s occupied resource during
	// recovery.
	if b.release != nil {
		b.release()
		b.release = nil
	}
	if !gatewayprovider.IsLocalQoderRejection(err) {
		b.selection.Report(b.provider.Snapshot().ID, err == nil, b.result)
	}
	return textflow.QoderCompatibleOutcome{Err: err, OutputChanged: HeartbeatAdjustedWrittenSize(b.c) != b.writerSize, Partial: err != nil && b.result != nil, Canceled: err != nil && qoderRequestCanceled(b.Context(), err), CanRefresh: b.h.shouldRefreshQoderProvider(err, false), CanFailover: b.h.options.MaySwitch(err)}
}

// Refresh requests bounded credential recovery for the retained target. Only a
// nonnil target with no error becomes ready and replaces the current provider;
// in-progress recovery returns Pending, and other failures produce neither
// ready nor pending.
func (b *qoderCompatibleAttemptBridge) Refresh() textflow.QoderRefreshResult {
	// Recovery becomes usable only with a returned target and nil error;
	// pending or failed refresh must not authorize retrying the retained
	// old credentials.
	provider, err := b.h.refreshQoderProvider(b.Context(), b.provider)
	if err == nil && provider != nil {
		b.log.Info("qoder.provider_refreshed_after_auth_error", zap.Int64("provider_id", b.provider.Snapshot().ID))
		b.provider = provider
		SetOpsSelectedProvider(b.c, provider.Snapshot())
		return textflow.QoderRefreshResult{Ready: true}
	}
	if errors.Is(err, providercore.ErrQoderRefreshInProgress) {
		b.log.Info("qoder.provider_refresh_after_auth_error_in_progress", zap.Int64("provider_id", b.provider.Snapshot().ID))
		return textflow.QoderRefreshResult{Pending: true}
	}
	if err != nil {
		b.log.Warn("qoder.provider_refresh_after_auth_error_failed", zap.Int64("provider_id", b.provider.Snapshot().ID), zap.Error(err))
	}
	return textflow.QoderRefreshResult{}
}

// RefreshPending presents HTTP 503 or the corresponding started-stream error
// with Retry-After one second. Callers supply the current presentation state
// after the text loop cannot continue with a still-refreshing target.
func (b *qoderCompatibleAttemptBridge) RefreshPending(started bool) {
	b.c.Header("Retry-After", "1")
	b.h.streamingAwareError(b.c, http.StatusServiceUnavailable, "upstream_error", "Qoder provider refresh is still in progress, please retry shortly", started, b.endpoint)
}

// Partial delegates observed partial completion using the current target and
// result. The text loop calls it once and ends before success binding or
// another attempt; its callback owns usage capture and any final error
// presentation.
func (b *qoderCompatibleAttemptBridge) Partial(out textflow.QoderCompatibleOutcome) {
	b.partial(b.provider, b.result, out.Err)
}

// Canceled logs cancellation of an initial or credential-refresh retry
// attempt.
func (b *qoderCompatibleAttemptBridge) Canceled(retry bool, err error) {
	event := "qoder.forward_canceled"
	if retry {
		event = "qoder.retry_forward_canceled"
	}
	b.log.Info(event, zap.Int64("provider_id", b.provider.Snapshot().ID), zap.Error(err))
}

// Failure presents a recognized Qoder error or a generic error after changed
// output, returning whether the loop must stop. It requires the current
// request, target and error-classification dependencies installed by
// admission.
//
// Recognized details retain the original upstream status for Ops and use
// OutputChanged to select streaming presentation. Even an unrecognized error
// ends when output changed, preventing the final recovery branch from
// replaying already written work. False leaves an unclassified, unwritten
// failure eligible for the text loop’s remaining provider allowance.
func (b *qoderCompatibleAttemptBridge) Failure(out textflow.QoderCompatibleOutcome) bool {
	if status, kind, message, ok := b.h.qoderGatewayErrorDetails(b.c, out.Err); ok {
		b.h.streamingAwareError(b.c, status, kind, message, out.OutputChanged, b.endpoint)
		return true
	}
	if out.OutputChanged {
		b.h.streamingAwareError(b.c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, b.endpoint)
		return true
	}
	return false
}

// Exhausted logs the final forwarding error and presents generic Qoder
// failure. The text loop invokes it after terminal classification declined the
// error and its distinct-provider allowance no longer permits another
// selection.
func (b *qoderCompatibleAttemptBridge) Exhausted(err error) {
	b.log.Error("qoder.forward_failed", zap.Int64("provider_id", b.provider.Snapshot().ID), zap.Error(err))
	b.h.errorResponse(b.c, http.StatusBadGateway, "upstream_error", "Upstream request failed", b.endpoint)
}

// Success binds the request affinity before
// recording completion. The text loop calls it only after a successful
// forwarding result; binding failures are advisory and usage capture still
// proceeds through the configured callback.
func (b *qoderCompatibleAttemptBridge) Success() {
	b.h.bindQoderStickySession(b.Context(), b.key.GroupID, b.hash, b.provider.Snapshot().ID, b.log)
	b.record(b.provider, b.result)
}

// Switched reports a provider switch to the retained selection handle.
func (b *qoderCompatibleAttemptBridge) Switched() {
	b.selection.Switched()
}

// qoderTargetSnapshot returns target metadata by value for provider slot
// handling. A nil target returns nil; callers require the actual target
// separately for forwarding and credential refresh after any wait.
func qoderTargetSnapshot(target QoderCompatibleTarget) *providercore.ProviderSnapshot {
	if target == nil {
		return nil
	}
	value := target.Snapshot()
	return &value
}
