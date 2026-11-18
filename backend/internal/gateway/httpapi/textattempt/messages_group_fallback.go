package textattempt

import (
	"errors"
	"net/http"
	"strconv"

	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// messages_group_fallback.go rebinds Messages and compatible attempts to an
// authorized fallback group after prompt-too-long failures.
// An Antigravity prompt-too-long failure can switch to a configured fallback
// group only before output or cancellation and only once per request. The
// target must resolve through authorization/funding policy and retain an
// active group that permits the original client protocol, preventing a
// provider failure from widening admission authority.
//
// Successful fallback replaces current Key, subscription and routing snapshots
// while retaining caller identity, forced platform and triggered cache billing
// in context. Old-group sticky hints are cleared before lookup under the new
// group, and session-attempt ownership is reset before selection restarts.
// Responses additionally rebuilds its mapped forwarding body from original
// client input; Chat retains its request context and new group mapping for
// later preparation.

// Fallback reports whether a prompt-too-long failure installed a newly
// authorized group for replay. Other errors, cancellation or existing output
// return false before resolution; the text loop supplies fallbackUsed to
// enforce its once-per-request limit.
//
// Resolution requires a distinct configured target plus bound authorization
// and planning services. Resolver errors are presented, and returned group
// identity/activity/protocol are checked before replacing funding. Old sticky
// hints are cleared, best-effort target-group lookup installs only a
// successful positive binding, and new routing is installed across
// HTTP/runtime Key views before resetting session attempts. Existing
// caller/platform/cache policy stays in context while original client input
// remains available for rebuilding.
func (b *messageAttemptBridge) Fallback(cause error, fallbackUsed bool) bool {
	var tooLong *antigravity.PromptTooLongError
	if !errors.As(cause, &tooLong) {
		return false
	}
	// Replaying under another group could duplicate already written work; stop
	// before authorization when cancellation, physical output or shared stream state has started.
	if b.Context().Err() != nil || b.c.Writer.Written() || (b.streamStarted != nil && *b.streamStarted) {
		return false
	}
	source, ok := requeststate.ClientProtocolFromContext(b.Context())
	if !ok {
		source = protocol.ProtocolAnthropicMessages
	}
	d := b.binding()
	key := b.currentAPIKey
	target := b.fallbackGroupID
	if fallbackUsed || key == nil || key.Group == nil || target == nil || *target <= 0 || *target == key.Group.ID || d.resolveFallback == nil || d.planRoute == nil {
		if d.writeMappedClaudeError != nil {
			// The mapped error writer owns best-effort presentation on a denied replay
			// path; discard its result because no authorized target attempt will follow.
			_ = d.writeMappedClaudeError(b.c, b.provider, tooLong.StatusCode, tooLong.RequestID, tooLong.Body)
		}
		return false
	}
	resolved, subscription, err := d.resolveFallback(b.Context(), key, *target, source)
	if err != nil {
		b.reqLog.Warn("gateway.resolve_fallback_group_failed", zap.Int64("fallback_group_id", *target), zap.Error(err))
		status, code, message, retryAfter := gatewayhttp.BillingErrorDetails(err)
		if retryAfter > 0 {
			b.c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		d.handleStreamingAwareError(b.c, status, code, message, false)
		return false
	}
	if resolved == nil || resolved.Group == nil || resolved.GroupID == nil || *resolved.GroupID != *target || resolved.Group.ID != *target || !resolved.Group.IsActive() || !resolved.Group.AllowsClientProtocol(source) {
		gatewayhttp.MarkOpsClientBusinessLimited(b.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		d.handleStreamingAwareError(b.c, http.StatusForbidden, "permission_error", "fallback group does not allow this request", false)
		return false
	}
	// Replace group/funding authority while retaining caller, forced platform and
	// triggered billing context; remove old-group sticky hints before target-group cache lookup.
	ctx := requeststate.WithGroup(b.Context(), resolved.Group)
	ctx = apikey.WithRuntimeAPIKey(ctx, resolved)
	ctx = requeststate.WithPrefetchedStickySession(ctx, 0, 0)
	b.hasBoundSession = false
	b.sessionBoundProviderID = 0
	// Sticky lookup is advisory under the new group: cache errors leave cleared
	// hints in place rather than carrying the old group’s bound provider into replay.
	if d.cachedSession != nil && b.sessionKey != "" {
		providerID, cacheErr := d.cachedSession(ctx, resolved.GroupID, b.sessionKey)
		if cacheErr != nil {
			b.reqLog.Warn("gateway.fallback_sticky_lookup_failed", zap.Error(cacheErr))
		} else if providerID > 0 {
			b.hasBoundSession = true
			b.sessionBoundProviderID = providerID
			ctx = requeststate.WithPrefetchedStickySession(ctx, providerID, resolved.Group.ID)
		}
	}
	plan := d.planRoute(ctx, resolved, b.reqModel)
	ctx = requeststate.WithRoutePlan(ctx, plan)
	b.c.Request = b.c.Request.WithContext(ctx)
	b.c.Set("gateway_effective_key", resolved)
	b.c.Set(string(keyhttp.ContextKeyAPIKey), resolved)
	b.apiKey, b.currentAPIKey = resolved, resolved
	b.subscription, b.currentSubscription = subscription, subscription
	b.attemptGroupMapping = plan.Mapping()
	// Finish ownership from the failed group before the text loop selects new
	// providers, allowing fresh session registration against the newly installed group.
	if b.sessionAttempts != nil {
		b.sessionAttempts.Reset()
	}
	return true
}
