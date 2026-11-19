package antigravity

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// retry_contract.go defines Antigravity retry budgets, shared capacity
// deadline storage, and provider-switch errors.
// Rate-limit recovery separates normal transport retries, short structured
// waits, shared model-capacity retries, and bounded single-provider waits. The
// constants fix attempt and wait budgets, while exhausted capacity stores a
// per-model deadline checked before later retry sequences.
//
// Capacity records share a mutex but do not reserve an active retry sequence;
// concurrent sequences already admitted can each retry. Provider-switch errors
// retain provider/model identity and the sticky-session observation so the
// owning gateway can choose another provider and account for lost affinity.
// Actual dispatch and deadline updates belong to RetryAdapter, with these
// values defining its local policy.

const (
	// Normal retries count at most three dispatch attempts; exponential base
	// delays are capped before jitter is applied by SleepAntigravityBackoffWithContext.
	AntigravityMaxRetries     = 3                // total normal dispatch attempt budget
	AntigravityRetryBaseDelay = 1 * time.Second  // initial normal retry delay
	AntigravityRetryMaxDelay  = 16 * time.Second // normal delay cap before jitter
	// Structured model delays at or above the threshold select cooldown/switch
	// handling, while shorter delays use one bounded smart retry.
	AntigravityRateLimitThreshold       = 7 * time.Second  // long model-delay selection threshold
	AntigravitySmartRetryMinWait        = 1 * time.Second  // minimum smart-retry wait
	AntigravitySmartRetryMaxAttempts    = 1                // one retry limits repeated throttling and wait time
	AntigravityDefaultRateLimitDuration = 30 * time.Second // used when retryDelay is absent or invalid

	// MODEL_CAPACITY_EXHAUSTED uses sixty same-provider retry attempts at a fixed
	// one-second wait, independently of the ordinary structured-delay threshold.
	AntigravityModelCapacityRetryMaxAttempts = 60              // same-provider capacity retry count
	AntigravityModelCapacityRetryWait        = 1 * time.Second // fixed capacity retry delay

	// Google RPC status, detail types, and reasons select structured retry
	// observations only when status/reason combinations and model metadata match.
	// RPC status for rate-limit observations.
	GoogleRPCStatusResourceExhausted = "RESOURCE_EXHAUSTED"
	GoogleRPCStatusUnavailable       = "UNAVAILABLE" // RPC status for capacity observations
	// RPC retry-delay detail identifier.
	GoogleRPCTypeRetryInfo = "type.googleapis.com/google.rpc.RetryInfo"
	// RPC model-reason detail identifier.
	GoogleRPCTypeErrorInfo = "type.googleapis.com/google.rpc.ErrorInfo"
	// shared model-capacity reason marker.
	GoogleRPCReasonModelCapacityExhausted = "MODEL_CAPACITY_EXHAUSTED"
	GoogleRPCReasonRateLimitExceeded      = "RATE_LIMIT_EXCEEDED" // per-model rate-limit reason marker

	// AntigravitySingleProviderSmartRetryMaxAttempts bounds retries after a long-delay 503.
	// HandleSmartRetry normally marks long model delays for provider switching.
	// Single-provider mode waits in place to avoid an additional local cooldown wait.
	AntigravitySingleProviderSmartRetryMaxAttempts = 3

	// AntigravitySingleProviderSmartRetryMaxWait caps each requested in-place
	// wait; transport time is additional to this delay budget.
	AntigravitySingleProviderSmartRetryMaxWait = 15 * time.Second

	// AntigravitySingleProviderSmartRetryTotalMaxWait caps accumulated retry waits.
	// Exhausting this budget returns the failure response.
	AntigravitySingleProviderSmartRetryTotalMaxWait = 30 * time.Second

	// AntigravityModelCapacityCooldown supplies the deadline lifetime stored
	// after capacity attempts exhaust; preexisting active sequences remain
	// independent.
	AntigravityModelCapacityCooldown = 10 * time.Second
)

// CapacityTracker stores per-model capacity-exhaustion cooldown deadlines for
// one runtime instance so concurrent tests and services remain isolated.
type CapacityTracker struct {
	mu    sync.RWMutex         // synchronizes deadline reads and updates
	until map[string]time.Time // normalized model name to recovery deadline
}

// AntigravityProviderSwitchError carries provider/model identity for gateway
// retry selection after local rate-limit handling.
type AntigravityProviderSwitchError struct {
	OriginalProviderID int64  // provider that requested a switch
	RateLimitedModel   string // model selected for limit handling
	IsStickySession    bool   // observed sticky-session affinity flag
}

// NewCapacityTracker constructs an empty per-model capacity cooldown tracker.
func NewCapacityTracker() *CapacityTracker {
	return &CapacityTracker{until: make(map[string]time.Time)}
}

// CooldownUntil reports the active capacity-exhaustion deadline for model when
// now precedes that deadline. Nil trackers, blank models, and expired entries
// return false.
func (t *CapacityTracker) CooldownUntil(model string, now time.Time) (time.Time, bool) {
	if t == nil || model == "" {
		return time.Time{}, false
	}
	t.mu.RLock()
	deadline, ok := t.until[model]
	t.mu.RUnlock()
	if !ok || !now.Before(deadline) {
		return time.Time{}, false
	}
	return deadline, true
}

// Clear removes any stored capacity-exhaustion deadline for model. Nil trackers
// and blank models are ignored.
func (t *CapacityTracker) Clear(model string) {
	if t == nil || model == "" {
		return
	}
	t.mu.Lock()
	delete(t.until, model)
	t.mu.Unlock()
}

// MarkExhausted records until as the capacity-exhaustion deadline for model and
// prunes expired entries observed at now. Nil trackers and blank models are
// ignored.
func (t *CapacityTracker) MarkExhausted(model string, until, now time.Time) {
	if t == nil || model == "" {
		return
	}
	t.mu.Lock()
	if t.until == nil {
		t.until = make(map[string]time.Time)
	} else {
		for key, existing := range t.until {
			if !now.Before(existing) {
				delete(t.until, key)
			}
		}
	}
	t.until[model] = until
	t.mu.Unlock()
}

// Error formats the limited provider and model into the switch message.
func (e *AntigravityProviderSwitchError) Error() string {
	return fmt.Sprintf("provider %d model %s rate limited, need switch",
		e.OriginalProviderID, e.RateLimitedModel)
}

// IsAntigravityProviderSwitchError unwraps a typed switch signal from err
// using errors.AsType. An unmatched or nil error returns false; a typed nil
// match can return true with a nil pointer.
func IsAntigravityProviderSwitchError(err error) (*AntigravityProviderSwitchError, bool) {
	if switchErr, ok := errors.AsType[*AntigravityProviderSwitchError](err); ok {
		return switchErr, true
	}
	return nil, false
}
