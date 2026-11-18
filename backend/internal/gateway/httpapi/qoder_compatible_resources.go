package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// qoder_compatible_resources.go derives Qoder session affinity and manages
// provider-slot waiting and cleanup.
// Qoder sticky selection first uses an explicit request identity, then a
// parsed-content fallback when execution is configured. Fallback hashes
// receive a seed prefix so the generated digest differs from the same text
// supplied as an unprefixed explicit session. Successful requests bind only the affinity used during selection.
//
// Sticky writes run under a detached bounded context, retaining request values
// while avoiding client cancellation after useful output. Binding errors
// remain advisory. Provider-slot waits instead use the live request context:
// registration errors permit uncounted waiting, full queues reject
// immediately, and only registered entries are released after acquisition or
// failure. Streaming slot cleanup waits for explicit completion; buffered
// cleanup also follows cancellation.

// qoderRequestCanceled reports context.Canceled from the supplied error or
// context state. Nil context is permitted; DeadlineExceeded alone returns
// false, so callers retain ordinary timeout handling rather than treating
// every ended context as cancellation.
func qoderRequestCanceled(ctx context.Context, err error) bool {
	if err != nil && errors.Is(err, context.Canceled) {
		return true
	}
	return ctx != nil && errors.Is(ctx.Err(), context.Canceled)
}

// wrapQoderReleaseOnDone delegates idempotent resource cleanup using the
// request’s stream mode. Streams release only on explicit completion, while
// buffered requests also release on cancellation; nil cleanup is handled by
// the shared wrapper.
func wrapQoderReleaseOnDone(ctx context.Context, releaseFunc func(), isStream bool) func() {
	mode := scheduler.ReleaseOnCancel
	if isStream {
		mode = scheduler.ReleaseOnCompletion
	}
	return scheduler.WrapRelease(ctx, mode, releaseFunc)
}

// qoderSessionHash derives Qoder sticky identity with explicit seeds taking
// precedence over content fallback. An explicit header/body seed works even
// without configured execution; otherwise absent runtime/execution, parse
// failure or missing generated identity returns empty.
//
// Responses input selects its own parser while the other endpoints use
// Anthropic parsing for the fallback. Request client identity is attached when
// Gin state exists, and generated content hashes receive a fallback prefix
// before Qoder hashing. The same generated text supplied as an unprefixed
// explicit seed therefore has a different final key; an explicit seed already
// containing that prefix follows ordinary seed hashing.
func (h *QoderCompatibleRuntime) qoderSessionHash(c *gin.Context, endpoint QoderEndpoint, body []byte, apiKeyID int64) string {
	if seed := qoderExplicitStickySessionSeed(c, body); seed != "" {
		return qoderStickySessionHashFromSeed(seed)
	}
	if h == nil || h.options.Execution == nil {
		return ""
	}
	protocol := capability.PlatformAnthropic
	if endpoint == QoderResponses {
		protocol = "responses"
	}
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), protocol)
	// Parsing is advisory for sticky fallback, so malformed input supplies no
	// derived key rather than turning identity generation into a second admission error.
	if err != nil {
		return ""
	}
	if c != nil {
		parsed.SessionContext = &requeststate.SessionContext{
			ClientIP:  clientip.GetClientIP(c),
			UserAgent: c.GetHeader("User-Agent"),
			APIKeyID:  apiKeyID,
		}
	}
	// Prefix the generated digest before hashing so an explicit seed equal to
	// that raw digest does not select the same final identity.
	if generated := gatewaysession.GenerateSessionHash(parsed, slog.Info); generated != "" {
		return qoderStickySessionHashFromSeed("fallback:" + generated)
	}
	return ""
}

// qoderExplicitStickySessionSeed delegates explicit identity resolution using
// live request headers and body. Missing request state passes absent headers,
// leaving the shared resolver to apply its body identities and precedence
// rules.
func qoderExplicitStickySessionSeed(c *gin.Context, body []byte) string {
	var headers map[string][]string
	if c != nil && c.Request != nil {
		headers = c.Request.Header
	}
	return gatewaysession.QoderExplicitSessionSeed(headers, body)
}

// qoderStickySessionHashFromSeed delegates scheduler hashing for a chosen
// Qoder identity seed. Whitespace is trimmed and blank input returns empty;
// callers retain explicit versus fallback seed precedence before invoking this
// namespaced hash.
func qoderStickySessionHashFromSeed(seed string) string {
	return gatewaysession.QoderHashFromSeed(seed)
}

// bindQoderStickySession retains request affinity after successful output. Storage
// uses a detached five-second budget so client cancellation does not interrupt
// publication. An empty selection key produces no write, and a cache failure
// leaves the completed response and its accounting intact.
func (h *QoderCompatibleRuntime) bindQoderStickySession(ctx context.Context, groupID *int64, sessionHash string, providerID int64, reqLog *zap.Logger) {
	if h == nil || h.options.Execution == nil || providerID <= 0 || sessionHash == "" {
		return
	}
	bindCtx, cancel := qoderDetachedTimeoutContext(ctx, 5*time.Second)
	defer cancel()
	if err := h.options.Execution.BindStickySession(bindCtx, groupID, sessionHash, providerID); err != nil && reqLog != nil {
		reqLog.Warn("qoder.bind_sticky_session_failed", zap.Int64("provider_id", providerID), zap.Error(err))
	}
}

// qoderDetachedTimeoutContext returns a bounded context retaining parent
// values without parent cancellation. Nil parent uses Background and
// nonpositive timeout selects five seconds; callers invoke the returned cancel
// function when detached work finishes.
func qoderDetachedTimeoutContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, timeout)
}

// prepareQoderRequestContext installs Claude client detection only for
// Messages input. Other endpoints leave that client-specific state unchanged,
// allowing shared request observation to supply their ordinary model and
// streaming information.
func prepareQoderRequestContext(c *gin.Context, body []byte, endpoint QoderEndpoint) {
	if endpoint == QoderMessages {
		SetClaudeCodeClientContext(c, body, nil)
	}
}

// shouldRefreshQoderProvider reports configured refresh eligibility before
// stream output. Missing runtime, unavailable platform or a true started flag
// returns false; otherwise callers provide the MayRefresh classifier for the
// supplied error.
func (h *QoderCompatibleRuntime) shouldRefreshQoderProvider(err error, streamStarted bool) bool {
	if h == nil || !h.options.PlatformAvailable || streamStarted {
		return false
	}
	return h.options.MayRefresh(err)
}

// refreshQoderProvider delegates target credential recovery under a
// thirty-second child context. Missing runtime or unavailable platform returns
// a configuration error; callers supply a usable target and interpret a
// returned in-progress error separately from ready credentials.
func (h *QoderCompatibleRuntime) refreshQoderProvider(ctx context.Context, target QoderCompatibleTarget) (QoderCompatibleTarget, error) {
	if h == nil || !h.options.PlatformAvailable {
		return nil, errors.New("qoder gateway service is not configured")
	}
	// Give credential recovery its own thirty-second deadline while retaining
	// parent cancellation; the target receives this budget before the loop interprets readiness.
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return target.Refresh(refreshCtx)
}

// acquireQoderProviderSlotWithWait obtains a provider slot, optionally using
// selection’s explicit wait plan. Nil provider is an error; missing
// concurrency support returns nil cleanup and nil error, while an absent plan
// delegates the ordinary provider wait.
//
// With a plan, registration errors permit waiting without incremented
// bookkeeping and an explicit full queue stops before slot polling. The helper
// tracks only successful registrations, releasing that wait entry after either
// timed acquisition or failure. Returned slot cleanup remains the caller’s
// responsibility and is subsequently wrapped by the attempt bridge for
// streaming or cancellation lifetime.
func (h *QoderCompatibleRuntime) acquireQoderProviderSlotWithWait(c *gin.Context, provider *providercore.ProviderSnapshot, waitPlan *scheduler.ProviderWaitPlan, reqStream bool, streamStarted *bool, reqLog *zap.Logger) (func(), error) {
	if provider == nil {
		return nil, errors.New("provider is nil")
	}
	if h == nil || h.concurrencyHelper == nil {
		return nil, nil
	}
	if waitPlan == nil {
		return h.concurrencyHelper.AcquireProviderSlotWithWait(c, provider.ID, provider.Concurrency, reqStream, streamStarted)
	}

	ctx := c.Request.Context()
	providerWaitCounted := false
	waitEntry, err := h.concurrencyHelper.EnterProviderWait(ctx, provider.ID, waitPlan.MaxWaiting)
	canWait := waitEntry.Allowed
	// Counter failure permits an uncounted slot wait; an explicit full result
	// rejects before polling and never owns a registered entry to decrement.
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("qoder.provider_wait_counter_increment_failed", zap.Int64("provider_id", provider.ID), zap.Error(err))
		}
	} else if !canWait {
		if reqLog != nil {
			reqLog.Info("qoder.provider_wait_queue_full",
				zap.Int64("provider_id", provider.ID),
				zap.Int("max_waiting", waitPlan.MaxWaiting),
			)
		}
		return nil, &WaitQueueFullError{SlotType: "provider"}
	}
	if err == nil && canWait {
		providerWaitCounted = true
	}
	// Only successful registration owns a decrement. Clear that ownership after
	// release so acquisition failure and success cannot clean up the same entry twice.
	releaseWait := func() {
		if providerWaitCounted {
			waitEntry.Release()
			providerWaitCounted = false
		}
	}

	providerRelease, err := h.concurrencyHelper.AcquireProviderSlotWithWaitTimeout(c, provider.ID, waitPlan.MaxConcurrency, waitPlan.Timeout, reqStream, streamStarted)
	if err != nil {
		releaseWait()
		return nil, err
	}
	releaseWait()
	return providerRelease, nil
}

// acquireQoderRetryProviderSlot reacquires a target slot after credential
// refresh. Nil provider errors and missing concurrency returns no-op success;
// a retained selection wait plan is reused, otherwise the target’s configured
// concurrency selects ordinary waiting.
func (h *QoderCompatibleRuntime) acquireQoderRetryProviderSlot(c *gin.Context, provider *providercore.ProviderSnapshot, selection QoderCompatibleSelection, reqStream bool, streamStarted *bool) (func(), error) {
	if provider == nil {
		return nil, errors.New("provider is nil")
	}
	if h == nil || h.concurrencyHelper == nil {
		return nil, nil
	}
	maxConcurrency := provider.Concurrency
	if selection != nil && selection.WaitPlan() != nil {
		return h.acquireQoderProviderSlotWithWait(c, provider, selection.WaitPlan(), reqStream, streamStarted, nil)
	}
	return h.concurrencyHelper.AcquireProviderSlotWithWait(c, provider.ID, maxConcurrency, reqStream, streamStarted)
}

// handleConcurrencyError maps provider or user slot failure to Qoder protocol
// output. It retains status, category and message while using the supplied
// stream state and endpoint to select the client envelope.
func (h *QoderCompatibleRuntime) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool, endpoint QoderEndpoint) {
	status, errType, _, message := ConcurrencyErrorResponse(err, slotType)
	h.streamingAwareError(c, status, errType, message, streamStarted, endpoint)
}

// writeQoderFailoverExhaustedError reports whether a recognized final Qoder
// failure was presented. Nil or unrecognized errors return false for generic
// exhaustion; recognized details retain original upstream status in Ops before
// endpoint-aware output.
func (h *QoderCompatibleRuntime) writeQoderFailoverExhaustedError(c *gin.Context, endpoint QoderEndpoint, streamStarted bool, err error) bool {
	if err == nil {
		return false
	}
	if status, errType, message, ok := h.qoderGatewayErrorDetails(c, err); ok {
		h.streamingAwareError(c, status, errType, message, streamStarted, endpoint)
		return true
	}
	return false
}

// streamingAwareError delegates Qoder error presentation with observed stream
// state and endpoint.
func (h *QoderCompatibleRuntime) streamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool, endpoint QoderEndpoint) {
	WriteQoderStreamError(c, status, errType, message, streamStarted, endpoint)
}

// errorResponse delegates plain Qoder JSON errors in the selected client
// format.
func (h *QoderCompatibleRuntime) errorResponse(c *gin.Context, status int, errType, message string, endpoint QoderEndpoint) {
	WriteQoderError(c, status, errType, message, endpoint)
}

// qoderGatewayErrorDetails delegates classification and response overrides to
// the required error presenter. It returns the recognized flag unchanged,
// letting the caller distinguish a classified terminal failure from generic
// recovery handling.
func (h *QoderCompatibleRuntime) qoderGatewayErrorDetails(c *gin.Context, err error) (int, string, string, bool) {
	return h.options.Errors.Details(c, err)
}

// submitUsageRecordTask delegates captured completion work to the shared
// submission policy. The policy interprets pool configuration and running
// state; callers capture request/funding data before submission so the task
// does not depend on later Gin access.
func (h *QoderCompatibleRuntime) submitUsageRecordTask(c *gin.Context, task completion.UsageRecordTask) {
	NewQoderCompletionSubmission(h.options.Pool).Submit(c, task)
}
