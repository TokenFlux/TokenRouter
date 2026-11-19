package taskqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

// queue.go provides shared Redis Lua scripts, polling reservation helpers, and
// token-fenced lock operations for task queues.
// Creative Studio and batch-image queues both pair ready lists with delayed and
// active sorted sets, expiring inflight markers, and token-owned execution
// locks. Centralizing atomic enqueue, delayed-item promotion, stale-active
// recovery, polling waits, and lock renewal keeps their Lua semantics aligned
// while allowing each domain adapter to preserve its own lease rules.
//
// Stale-active recovery offers both untouched-inflight movement for queues with
// separate lock tokens and inflight-marker reset for queues that reuse the
// inflight key as a worker lease token, preventing recovered tasks from losing
// duplicate-enqueue protection before the next worker reserves them.

var (
	// enqueueScript atomically sets an expiring inflight deduplication marker and
	// pushes the job identifier onto the ready list.
	enqueueScript = redis.NewScript(`
if redis.call("SET", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
  redis.call("LPUSH", KEYS[2], ARGV[1])
  return 1
end
return 0
`)

	// moveDueDelayedScript transfers delayed sorted-set members whose score is at
	// or before the cutoff into the ready list in one Redis execution.
	moveDueDelayedScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
end
return #jobs
`)

	// recoverStaleActiveScript moves stale active sorted-set members back to the
	// ready list while leaving separate inflight and lock keys untouched.
	recoverStaleActiveScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
end
return #jobs
`)

	// recoverStaleActiveResetInflightScript moves stale active sorted-set members
	// back to ready and resets each inflight key to the job ID with fresh TTL so
	// old worker lease tokens are fenced while duplicate Enqueue calls stay blocked.
	recoverStaleActiveResetInflightScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
  redis.call("SET", KEYS[3] .. job, job, "PX", ARGV[3])
end
return #jobs
`)

	// releaseLockScript deletes a lock key only when its stored value still
	// matches the releasing holder's token.
	releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

	// refreshLockScript extends a lock key's expiry in milliseconds only when its
	// stored value still matches the holder's token.
	refreshLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)
)

// NewToken generates sixteen cryptographic random bytes and encodes them as a
// 32-character lowercase hexadecimal ownership token.
func NewToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Enqueue atomically creates an expiring inflight marker and pushes id onto
// readyKey, reporting false when the inflight marker already exists. Callers
// supply an inflightTTL of at least one millisecond so Redis accepts the PX
// expiration argument.
func Enqueue(ctx context.Context, rdb *redis.Client, inflightKey, readyKey, id string, inflightTTL time.Duration) (bool, error) {
	applied, err := enqueueScript.Run(ctx, rdb, []string{inflightKey, readyKey}, id, inflightTTL.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return applied == 1, nil
}

// PollReserve repeatedly invokes reserveOnce until a value is returned, a
// non-empty error occurs, ctx is canceled, or blockTimeout elapses. It always
// executes one immediate attempt even when blockTimeout is nonpositive, and
// caps inter-attempt waits to min(remaining, pollInterval).
func PollReserve[T any](
	ctx context.Context,
	blockTimeout, pollInterval time.Duration,
	isEmpty func(error) bool,
	reserveOnce func(context.Context) (T, error),
) (T, error) {
	var zero T
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	deadline := time.Now().Add(blockTimeout)
	for {
		val, err := reserveOnce(ctx)
		if err == nil {
			return val, nil
		}
		if !isEmpty(err) {
			return zero, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return zero, err
		}
		// Bound idle polling by the remaining caller budget so a short timeout does
		// not sleep for a full pollInterval after an empty check.
		wait := min(remaining, pollInterval)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

// MoveDueDelayedToReady moves up to limit delayed members scored at or below
// nowUnixMilli into readyKey, defaulting nonpositive limits to 100.
func MoveDueDelayedToReady(ctx context.Context, rdb *redis.Client, delayedKey, readyKey string, nowUnixMilli int64, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	return moveDueDelayedScript.Run(ctx, rdb, []string{delayedKey, readyKey}, nowUnixMilli, limit).Int()
}

// RecoverStaleActive moves up to limit active members scored at or below
// cutoffUnixMilli back to readyKey without modifying inflight or lock keys.
// Nonpositive limits default to 100.
func RecoverStaleActive(ctx context.Context, rdb *redis.Client, activeKey, readyKey string, cutoffUnixMilli int64, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	return recoverStaleActiveScript.Run(ctx, rdb, []string{activeKey, readyKey}, cutoffUnixMilli, limit).Int()
}

// RecoverStaleActiveResetInflight moves up to limit active members scored at or
// below cutoffUnixMilli back to readyKey and resets each member's inflight key
// under inflightPrefix to the job ID with inflightTTL. Resetting rather than
// deleting the inflight key fences stale worker lease tokens while keeping
// concurrent Enqueue calls deduplicated until acknowledgment or TTL expiry.
func RecoverStaleActiveResetInflight(ctx context.Context, rdb *redis.Client, activeKey, readyKey, inflightPrefix string, cutoffUnixMilli int64, limit int, inflightTTL time.Duration) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	return recoverStaleActiveResetInflightScript.Run(
		ctx, rdb,
		[]string{activeKey, readyKey, inflightPrefix},
		cutoffUnixMilli, limit, inflightTTL.Milliseconds(),
	).Int()
}

// AcquireLock generates a fresh random token and attempts SET NX on key with
// ttl, returning the token and true when acquired or empty and false on contention.
func AcquireLock(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) (string, bool, error) {
	token, err := NewToken()
	if err != nil {
		return "", false, err
	}
	ok, err := rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil || !ok {
		return "", false, err
	}
	return token, true, nil
}

// ReleaseLock deletes key only when its stored value matches token, returning
// nil when rdb, key, or token is empty or when another holder owns the key.
func ReleaseLock(ctx context.Context, rdb *redis.Client, key, token string) error {
	if rdb == nil || key == "" || token == "" {
		return nil
	}
	return releaseLockScript.Run(ctx, rdb, []string{key}, token).Err()
}

// RefreshLock extends key's expiration to ttl when its stored value matches
// token and reports whether ownership was confirmed. Empty inputs return false
// without contacting Redis.
func RefreshLock(ctx context.Context, rdb *redis.Client, key, token string, ttl time.Duration) (bool, error) {
	if rdb == nil || key == "" || token == "" {
		return false, nil
	}
	applied, err := refreshLockScript.Run(ctx, rdb, []string{key}, token, ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return applied == 1, nil
}
