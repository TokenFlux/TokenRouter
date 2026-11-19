package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// fixed_window.go counts requests in Redis expiry windows and decides whether
// each request stays within its rate limit.
// Each rate-limit key counts every request until its Redis expiry, including
// requests already over the caller's limit. A Lua script increments and
// examines TTL atomically, starting the window on the first count and
// repairing an existing counter whose expiry is missing. Later requests with
// an expiry keep the same window rather than extending it.
//
// Admission compares the resulting count with the supplied limit. A blocked
// request separately reads remaining milliseconds for Retry-After, so the
// count decision and reported delay describe different Redis operations. An
// unavailable or nonpositive PTTL leaves the caller's full window as the
// delay, and callers choose their own allow-or-reject policy when counting
// fails.

// rateLimitScript increments and repairs expiry in one Redis operation so
// competing requests cannot leave the first counter without its window TTL.
var rateLimitScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
local repaired = 0
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
elseif ttl == -1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  repaired = 1
end
return {current, repaired}
`)

// FixedWindowLimiter atomically counts requests in Redis under a caller-supplied prefix.
type FixedWindowLimiter struct {
	// redis is supplied by the caller and remains open for this limiter's
	// operations; lifecycle code owns closing the shared client after use.
	redis  *redis.Client
	prefix string // exact counter key prefix
}

// allowResult pairs an admission decision with its observed count and retry delay.
type allowResult struct {
	Allowed bool  // count within limit
	Count   int64 // count including this request
	// RetryAfter is zero for admission and a separately sampled PTTL when
	// blocked; an unavailable or nonpositive PTTL retains the supplied window.
	RetryAfter time.Duration
}

// NewFixedWindowLimiter creates a fixed-window counter using client and the
// exact prefix. The caller owns the Redis client and supplies keys without
// that prefix; construction performs no network operations.
func NewFixedWindowLimiter(client *redis.Client, prefix string) *FixedWindowLimiter {
	return &FixedWindowLimiter{redis: client, prefix: prefix}
}

// runFixedWindowScript atomically increments key and sets or repairs its
// millisecond expiry. It decodes count and repair status from the script's
// response, returning errors for failed execution, missing values, or
// unsupported integer representations.
func runFixedWindowScript(ctx context.Context, client *redis.Client, key string, windowMillis int64) (int64, bool, error) {
	values, err := rateLimitScript.Run(ctx, client, []string{key}, windowMillis).Slice()
	if err != nil {
		return 0, false, err
	}
	if len(values) < 2 {
		return 0, false, fmt.Errorf("rate limit script returned %d values", len(values))
	}
	count, err := parseInt64(values[0])
	if err != nil {
		return 0, false, err
	}
	repaired, err := parseInt64(values[1])
	if err != nil {
		return 0, false, err
	}
	return count, repaired == 1, nil
}

// Allow counts this request under the exact prefix plus key and returns
// admission, total count, retry delay, and any counting error. Every
// successful script call increments, including blocked requests; limit is
// compared directly without validation.
//
// The script starts an expiry or repairs a missing expiry using at least one
// millisecond. Blocked requests then read PTTL separately, keeping window as
// the delay if that lookup fails or has no positive result. Callers choose
// the failure policy and format the delay for their response.
// @project-doc docs/operations/edge_security.md#fixed_window_rate_limits
func (r *FixedWindowLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int64, time.Duration, error) {
	redisKey := r.prefix + key
	windowMillis := windowTTLMillis(window)

	count, repaired, err := runFixedWindowScript(ctx, r.redis, redisKey, windowMillis)
	if err != nil {
		return false, 0, 0, err
	}
	if repaired {
		logging.LegacyPrintf("infra.redis_fixed_window", "[RateLimit] ttl repaired: key=%s window_ms=%d", redisKey, windowMillis)
	}

	result := allowResult{Allowed: count <= int64(limit), Count: count}
	if !result.Allowed {
		// Admission has already been decided. Keep the supplied window when a
		// separate TTL lookup cannot provide a positive remaining delay for the response.
		result.RetryAfter = window
		if ttl, ttlErr := r.redis.PTTL(ctx, redisKey).Result(); ttlErr == nil && ttl > 0 {
			result.RetryAfter = ttl
		}
	}
	return result.Allowed, result.Count, result.RetryAfter, nil
}

// windowTTLMillis converts window to whole milliseconds with a
// one-millisecond minimum. Submillisecond, zero, and negative durations all
// select one millisecond so the incremented key receives a positive expiry.
func windowTTLMillis(window time.Duration) int64 {
	ttl := window.Milliseconds()
	// Redis needs a positive millisecond expiry to keep counter lifetime bounded.
	if ttl < 1 {
		return 1
	}
	return ttl
}

// parseInt64 accepts int64, int, or a decimal string from a Redis script
// result. Other dynamic types and failed decimal parsing return an error
// instead of being treated as a count.
func parseInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unexpected value type %T", value)
	}
}
