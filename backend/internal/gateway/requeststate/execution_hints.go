package requeststate

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// execution_hints.go stores request execution observations in context values
// and exposes their presence-aware accessors.
// Execution hints capture entry-time identity and selection decisions for later
// provider attempts. Context derivation copies scalar hint values, so an
// attempt can replace its own flags while the parent request retains the
// earlier decision. Presence bits distinguish an omitted observation from
// explicit false or zero, and helper-specific nil-context behavior determines
// whether a derivation can create state.

// openAILegacySessionHashKey identifies the compatibility session hash kept
// alongside current request isolation evidence for sticky provider lookup.
var openAILegacySessionHashKey = openAILegacySessionHashContextKey{}

// agentTaskRecoveryKey identifies the consumed recovery opportunity within an
// attempt sequence.
type agentTaskRecoveryKey struct{}

// cacheBillingKey identifies the forced-cache billing choice within an attempt
// context.
type cacheBillingKey struct{}

// Hint stores a scalar execution value with a separate presence flag.
type Hint[T bool | int | int64] struct {
	// Value and Set form one observation: Set records whether Value was supplied,
	// including an explicit false or zero. Consumers retain both when missing
	// input changes their decision.
	Value T    // captured scalar observation value
	Set   bool // explicit observation presence flag
}

// ExecutionHints contains scalar identity, selection, and execution
// observations captured for an attempt. Context helpers copy this value before
// updates, and consumers use individual Hint.Set flags where absence differs
// from zero.
type ExecutionHints struct {
	HealthModel string // model used for health observations
	// SessionIsolationSource and SessionIsolationHash are captured together from
	// the request entry. Group fallback reuses these values to revalidate the same
	// parsed session against its new group.
	SessionIsolationSource string // session identifier extraction source
	SessionIsolationHash   string // parsed current isolation hash
	// QuotaAutoPauseThreshold5h and QuotaAutoPauseThreshold7d are global usage
	// fractions. Provider resolution uses them when that window lacks a positive
	// override, clips values below zero or above one, and treats zero as no pause
	// threshold.
	QuotaAutoPauseThreshold5h float64 // five-hour quota pause threshold
	QuotaAutoPauseThreshold7d float64 // seven-day quota pause threshold
	// GroupPrivacyRequirement applies to GroupPrivacyGroupID. The selector reuses
	// its Value when Set is true and that group matches the current selection;
	// another group can load its own requirement after fallback.
	GroupPrivacyRequirement     Hint[bool] // cached group privacy requirement
	GroupPrivacyGroupID         int64      // group associated with privacy evidence
	ProxyQuarantineBypass       bool       // selection proxy quarantine bypass flag
	ClaudeCode                  bool       // entry Claude Code classification
	ClaudeCodeVersion           string     // captured Claude Code version
	OpenAIImageGenerationIntent bool       // entry image-generation intent flag
	OpenAIImagesEndpoint        bool       // dedicated Images entry flag
	IsMaxTokensOneHaikuRequest  Hint[bool] // explicit Haiku probe classification
	ThinkingEnabled             Hint[bool] // explicit thinking enabled observation
	// RoutingReasoningEffort and RoutingReasoningEffortSet capture the normalized
	// effective reasoning effort used for provider effort_model_mapping selection.
	// RoutingReasoningEffortSet distinguishes an explicit empty effort from an
	// omitted context override so attempt resolution can clear a prior plan effort.
	RoutingReasoningEffort    string // effective effort for provider model mapping
	RoutingReasoningEffortSet bool   // explicit routing effort presence flag
	// PrefetchedStickyProviderID and PrefetchedStickyGroupID describe the same
	// prefetched sticky lookup. Their Set flags retain explicit zero results, and
	// consumers compare the saved group before reusing the provider.
	PrefetchedStickyProviderID Hint[int64] // prefetched sticky provider identifier
	PrefetchedStickyGroupID    Hint[int64] // group owning prefetched stickiness
	SingleProviderRetry        Hint[bool]  // explicit single-provider retry state
	ProviderSwitchCount        Hint[int]   // explicit provider-switch count
}

// executionHintsKey identifies the scalar execution snapshot stored in request
// contexts.
type executionHintsKey struct{}

// openAILegacySessionHashContextKey separates legacy affinity hashes from other
// context values.
type openAILegacySessionHashContextKey struct{}

// WithAgentTaskRecovery marks recovery as tried in a derived context for this
// attempt sequence. Callers provide a nonnil parent; the marker records use of
// the opportunity and does not indicate recovery succeeded.
func WithAgentTaskRecovery(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentTaskRecoveryKey{}, true)
}

// AgentTaskRecoveryTried reports the bool recovery marker stored in a nonnil
// context. Missing or wrong-type values produce false, letting the request
// adapter decide whether its recovery opportunity is still available.
func AgentTaskRecoveryTried(ctx context.Context) bool {
	value, _ := ctx.Value(agentTaskRecoveryKey{}).(bool)
	return value
}

// IsForceCacheBilling reports the forced-cache billing bool stored in a nonnil
// context. Missing or wrong-type values return false; the consumer applies this
// attempt policy separately from measured upstream token usage.
func IsForceCacheBilling(ctx context.Context) bool {
	value, _ := ctx.Value(cacheBillingKey{}).(bool)
	return value
}

// WithForceCacheBilling sets forced-cache billing in a derived context with a
// nonnil parent. Other context values remain available, and parent state keeps
// its preceding flag for later independent derivations.
func WithForceCacheBilling(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheBillingKey{}, true)
}

// IsClaudeCodeClient reports the entry classification stored in execution
// hints. Missing or nil context returns false through the snapshot reader, so
// callers distinguish classified clients from absent evidence before applying
// client-specific execution policy.
func IsClaudeCodeClient(ctx context.Context) bool {
	return ExecutionHintsFromContext(ctx).ClaudeCode
}

// SetClaudeCodeClient returns derived hints containing the supplied client
// classification. Existing flags remain in the copied snapshot; nil ctx yields
// nil, so callers needing stored state provide a parent context.
func SetClaudeCodeClient(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.ClaudeCode = value })
}

// SetClaudeCodeVersion stores the supplied version spelling in a derived hint
// snapshot. It performs no trimming or validation and preserves other fields;
// nil ctx yields nil through the common update path.
func SetClaudeCodeVersion(ctx context.Context, value string) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.ClaudeCodeVersion = value })
}

// GetClaudeCodeVersion returns the captured Claude Code version or empty when
// absent. Nil contexts are tolerated through ExecutionHintsFromContext, and
// this read does not validate the stored client classification or version.
func GetClaudeCodeVersion(ctx context.Context) string {
	return ExecutionHintsFromContext(ctx).ClaudeCodeVersion
}

// WithOpenAIImageGenerationIntent marks image intent in a derived execution
// snapshot. A nil parent becomes Background, so this entry helper can create
// usable state while retaining existing hints on an ordinary parent.
func WithOpenAIImageGenerationIntent(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return updateHints(ctx, func(h *ExecutionHints) { h.OpenAIImageGenerationIntent = true })
}

// OpenAIImageGenerationIntentFromContext reports the stored entry image-intent
// flag from execution hints. Missing and nil contexts return false; platform
// policy determines how the entry classified the request before this read.
func OpenAIImageGenerationIntentFromContext(ctx context.Context) bool {
	return ExecutionHintsFromContext(ctx).OpenAIImageGenerationIntent
}

// WithOpenAIImagesEndpoint marks a dedicated Images entry in derived execution
// hints. Nil ctx becomes Background, and other captured values remain available
// for the selected provider's execution decision.
func WithOpenAIImagesEndpoint(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return updateHints(ctx, func(h *ExecutionHints) { h.OpenAIImagesEndpoint = true })
}

// OpenAIImagesEndpointFromContext reports the stored dedicated Images entry
// flag. Missing or nil context yields false, and this flag identifies entry
// shape separately from a model's general image-generation intent.
func OpenAIImagesEndpointFromContext(ctx context.Context) bool {
	return ExecutionHintsFromContext(ctx).OpenAIImagesEndpoint
}

// ExecutionHintsFromContext returns the scalar snapshot stored in ctx or zero
// hints when absent. Nil contexts are tolerated, and changing the returned
// value leaves the captured context value unchanged.
func ExecutionHintsFromContext(ctx context.Context) ExecutionHints {
	if ctx == nil {
		return ExecutionHints{}
	}
	hints, _ := ctx.Value(executionHintsKey{}).(ExecutionHints)
	return hints
}

// WithExecutionHints stores the supplied scalar snapshot in a derived context.
// Nil ctx returns nil, and the caller chooses the complete replacement value
// rather than merging it with an existing snapshot.
func WithExecutionHints(ctx context.Context, hints ExecutionHints) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, executionHintsKey{}, hints)
}

// updateHints copies the existing scalar snapshot, applies update, and stores
// the result in a derived context. Callers supply a usable callback; it still
// runs on zero hints for nil ctx, whose final result remains nil.
func updateHints(ctx context.Context, update func(*ExecutionHints)) context.Context {
	hints := ExecutionHintsFromContext(ctx)
	update(&hints)
	return WithExecutionHints(ctx, hints)
}

// WithIsMaxTokensOneHaikuRequest stores the probe decision with its presence
// bit set in derived hints. Explicit false remains distinguishable from
// omission, and a nil parent yields nil through the common updater.
func WithIsMaxTokensOneHaikuRequest(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.IsMaxTokensOneHaikuRequest = Hint[bool]{value, true} })
}

// WithThinkingEnabled stores thinking evidence with its presence bit set in
// derived hints. Explicit false remains a supplied observation for health
// consumers; nil ctx yields nil through the common update path.
func WithThinkingEnabled(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.ThinkingEnabled = Hint[bool]{value, true} })
}

// WithRoutingReasoningEffort stores the normalized effective reasoning effort
// for provider model mapping in derived execution hints. Setting a nonblank
// effort or clearing a previously stored effort marks RoutingReasoningEffortSet
// so attempt resolution distinguishes explicit overrides from effort-free
// entries; nil ctx yields nil through updateHints.
func WithRoutingReasoningEffort(ctx context.Context, effort string) context.Context {
	normalized := protocol.NormalizeReasoningEffort(effort)
	return updateHints(ctx, func(h *ExecutionHints) {
		if normalized != "" || h.RoutingReasoningEffort != "" {
			h.RoutingReasoningEffortSet = true
		}
		h.RoutingReasoningEffort = normalized
	})
}

// WithPrefetchedStickySession stores provider and group lookup results with
// both presence bits set. Zero identifiers remain explicit results, and nil ctx
// yields nil; selection validates group applicability before reusing this
// evidence.
func WithPrefetchedStickySession(ctx context.Context, providerID, groupID int64) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) {
		h.PrefetchedStickyProviderID = Hint[int64]{providerID, true}
		h.PrefetchedStickyGroupID = Hint[int64]{groupID, true}
	})
}

// WithSingleProviderRetry stores the retry-state bool with its presence bit set
// in derived hints. It retains explicit false as evidence distinct from
// absence, while nil ctx yields nil.
func WithSingleProviderRetry(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.SingleProviderRetry = Hint[bool]{value, true} })
}

// WithProviderSwitchCount stores the supplied switch count with its presence
// bit set in derived hints. Zero is an explicit count; validation or clamping
// remains with callers, and nil ctx yields nil.
func WithProviderSwitchCount(ctx context.Context, value int) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.ProviderSwitchCount = Hint[int]{value, true} })
}

// IsMaxTokensOneHaikuRequestFromContext returns the stored probe decision and
// presence flag. Missing or nil context produces false, false, while an
// explicitly stored false retains true presence for its consumer.
func IsMaxTokensOneHaikuRequestFromContext(ctx context.Context) (bool, bool) {
	h := ExecutionHintsFromContext(ctx).IsMaxTokensOneHaikuRequest
	return h.Value, h.Set
}

// ThinkingEnabledFromContext returns the thinking bool and its separate
// presence flag. Missing or nil context yields false, false, while stored false
// remains present for callers such as HealthThinking.
func ThinkingEnabledFromContext(ctx context.Context) (bool, bool) {
	h := ExecutionHintsFromContext(ctx).ThinkingEnabled
	return h.Value, h.Set
}

// RoutingReasoningEffortFromContext returns the normalized effective reasoning
// effort stored in execution hints for provider model mapping, or empty when
// absent or nil ctx is supplied.
func RoutingReasoningEffortFromContext(ctx context.Context) string {
	return ExecutionHintsFromContext(ctx).RoutingReasoningEffort
}

// RoutingReasoningEffortLookupFromContext returns the normalized routing
// reasoning effort and whether it was explicitly stored on ctx. Callers use
// the presence flag to distinguish an explicit empty effort that clears a plan
// override from an omitted context hint.
func RoutingReasoningEffortLookupFromContext(ctx context.Context) (string, bool) {
	hints := ExecutionHintsFromContext(ctx)
	return hints.RoutingReasoningEffort, hints.RoutingReasoningEffortSet
}

// PrefetchedStickyProviderIDFromContext returns the prefetched provider
// identifier and its presence flag. Missing or nil context gives zero, false;
// an explicitly stored zero remains present and requires the caller's selection
// handling.
func PrefetchedStickyProviderIDFromContext(ctx context.Context) (int64, bool) {
	h := ExecutionHintsFromContext(ctx).PrefetchedStickyProviderID
	return h.Value, h.Set
}

// PrefetchedStickyGroupIDFromContext returns the group associated with
// prefetched stickiness and its presence flag. Missing or nil ctx gives zero,
// false, while explicit zero remains present alongside the provider lookup
// result.
func PrefetchedStickyGroupIDFromContext(ctx context.Context) (int64, bool) {
	h := ExecutionHintsFromContext(ctx).PrefetchedStickyGroupID
	return h.Value, h.Set
}

// SingleProviderRetryFromContext returns retry-state evidence and its presence
// flag. Missing or nil context gives false, false, while explicit false keeps
// true presence for caller decisions that distinguish an omitted value.
func SingleProviderRetryFromContext(ctx context.Context) (bool, bool) {
	h := ExecutionHintsFromContext(ctx).SingleProviderRetry
	return h.Value, h.Set
}

// ProviderSwitchCountFromContext returns switch-count evidence and its presence
// flag. Missing or nil context gives zero, false, while an explicitly stored
// zero remains present for diagnostics and execution adapters.
func ProviderSwitchCountFromContext(ctx context.Context) (int, bool) {
	h := ExecutionHintsFromContext(ctx).ProviderSwitchCount
	return h.Value, h.Set
}

// WithSessionIsolation stores source and hash together in derived execution
// hints for group-fallback revalidation. Values retain their supplied spelling;
// this operation does not resolve identity again, and nil ctx yields nil.
func WithSessionIsolation(ctx context.Context, source, hash string) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.SessionIsolationSource, h.SessionIsolationHash = source, hash })
}

// WithOpenAILegacySessionHash stores a trimmed nonblank compatibility hash in a
// derived context. Nil ctx returns nil and blank input returns the same parent;
// blank input therefore does not clear an earlier legacy value.
func WithOpenAILegacySessionHash(ctx context.Context, legacyHash string) context.Context {
	if ctx == nil {
		return nil
	}
	trimmed := strings.TrimSpace(legacyHash)
	if trimmed == "" {
		return ctx
	}
	return context.WithValue(ctx, openAILegacySessionHashKey, trimmed)
}

// OpenAILegacySessionHashFromContext returns the trimmed compatibility hash or
// empty when missing. Nil contexts and wrong-type values yield empty; current
// isolation identity remains in its separate execution-hint fields.
func OpenAILegacySessionHashFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(openAILegacySessionHashKey).(string)
	return strings.TrimSpace(value)
}
