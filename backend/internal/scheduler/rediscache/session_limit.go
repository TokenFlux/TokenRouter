package rediscache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// session_limit.go tracks active provider sessions and enforces their
// idle-window capacity in Redis.
// Provider session capacity measures distinct sessions active within an idle
// window, rather than the lifetime of each forwarding request. Sorted-set
// members hold session UUIDs with Redis Unix-second activity scores;
// registration and count queries prune scores at or before the idle cutoff,
// and key TTLs remove inactive sets.
//
// Existing registrations can refresh even when capacity is full. Activity
// checks compare scores without deleting them, while explicit unregistration
// lets a failed attempt free capacity immediately. Timeout conversion uses
// whole seconds, with the constructor's default applied by registration and
// refresh when conversion is nonpositive. Batch queries evaluate script
// source directly and retain successful per-provider results when other
// commands fail.

// sessionLimitKeyPrefix identifies each provider's active-session set so
// admission, activity refresh and failed-attempt cleanup operate on the same
// registered session members.
const (
	sessionLimitKeyPrefix = "session_limit:provider:" // provider session activity namespace
)

// Session scripts use Redis TIME for shared idle cutoffs; writes and pruning
// stay within one provider key so admission and activity work uses a common
// member set.
var (
	// registerSessionScript prunes idle members, refreshes existing sessions and
	// admits new ones below maxSessions using one Redis TIME observation.
	// Admission refreshes key expiry to idleTimeout plus sixty seconds.
	registerSessionScript = redis.NewScript(`
		-- 兼容 3.2-4.x：脚本使用 TIME，需启用按效果复制，确保写入能同步到从库。
		-- 5.0 及以上默认按效果复制；保留调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local maxSessions = tonumber(ARGV[1])
		local idleTimeout = tonumber(ARGV[2])
		local sessionUUID = ARGV[3]

		-- 使用 Redis 服务器时间，确保多实例时钟一致
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - idleTimeout

		-- 清理过期会话
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)

		-- 检查会话是否已存在（支持刷新时间戳）
		local exists = redis.call('ZSCORE', key, sessionUUID)
		if exists ~= false then
			-- 会话已存在，刷新时间戳
			redis.call('ZADD', key, now, sessionUUID)
			redis.call('EXPIRE', key, idleTimeout + 60)
			return 1
		end

		-- 检查是否达到会话数量上限
		local count = redis.call('ZCARD', key)
		if count < maxSessions then
			-- 未达上限，添加新会话
			redis.call('ZADD', key, now, sessionUUID)
			redis.call('EXPIRE', key, idleTimeout + 60)
			return 1
		end

		-- 达到上限，拒绝新会话
		return 0
	`)

	// refreshSessionScript renews an existing member and its set expiry using
	// Redis time. Missing members are left absent, while an existing old score
	// is refreshed without an idle-age check.
	refreshSessionScript = redis.NewScript(`
		-- 兼容 3.2-4.x：脚本使用 TIME，需启用按效果复制，确保写入能同步到从库。
		-- 5.0 及以上默认按效果复制；保留调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local idleTimeout = tonumber(ARGV[1])
		local sessionUUID = ARGV[2]

		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])

		-- 检查会话是否存在
		local exists = redis.call('ZSCORE', key, sessionUUID)
		if exists ~= false then
			redis.call('ZADD', key, now, sessionUUID)
			redis.call('EXPIRE', key, idleTimeout + 60)
		end
		return 1
	`)

	// getActiveSessionCountScript removes scores at or before the Redis idle
	// cutoff and counts the remaining sessions. Reads can therefore release
	// expired admission capacity.
	getActiveSessionCountScript = redis.NewScript(`
		-- 兼容 3.2-4.x：脚本使用 TIME，需启用按效果复制，确保写入能同步到从库。
		-- 5.0 及以上默认按效果复制；保留调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local idleTimeout = tonumber(ARGV[1])

		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - idleTimeout

		-- 清理过期会话
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)

		return redis.call('ZCARD', key)
	`)

	// isSessionActiveScript reports whether a member's score exceeds the Redis
	// idle cutoff. An expired score returns zero and stays stored until
	// registration, counting or key expiry removes it.
	isSessionActiveScript = redis.NewScript(`
		-- 兼容 3.2-4.x：脚本使用 TIME，需启用按效果复制，确保写入能同步到从库。
		-- 5.0 及以上默认按效果复制；保留调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local idleTimeout = tonumber(ARGV[1])
		local sessionUUID = ARGV[2]

		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - idleTimeout

		-- 获取会话的时间戳
		local score = redis.call('ZSCORE', key, sessionUUID)
		if score == false then
			return 0
		end

		-- 检查是否过期
		if tonumber(score) <= expireBefore then
			return 0
		end

		return 1
	`)
)

// sessionLimitCache applies provider session admission and idle-window
// queries through Redis scripts.
type sessionLimitCache struct {
	// rdb remains caller-owned; script preloading and later admission operations
	// use the same shared Redis client for provider sets.
	rdb *redis.Client // shared session storage client
	// defaultIdleTimeout comes from the constructor and supplies individual
	// query cutoffs plus registration/refresh durations whose second conversion
	// is nonpositive.
	defaultIdleTimeout time.Duration // default session idle duration
}

// NewSessionLimitCache constructs provider session storage and attempts to
// preload its scripts. Nonpositive minute values select five minutes; preload
// failures are logged, and later Run calls can recover through EVAL.
func NewSessionLimitCache(rdb *redis.Client, defaultIdleTimeoutMinutes int) scheduler.SessionLimitCache {
	if defaultIdleTimeoutMinutes <= 0 {
		defaultIdleTimeoutMinutes = 5
	}

	// Preloading reduces later EVALSHA misses; log load errors and keep
	// constructing the cache because Run retries missing scripts with their EVAL
	// source.
	ctx := context.Background()
	scripts := []*redis.Script{
		registerSessionScript,
		refreshSessionScript,
		getActiveSessionCountScript,
		isSessionActiveScript,
	}
	for _, script := range scripts {
		if err := script.Load(ctx, rdb).Err(); err != nil {
			logging.LegacyPrintf("cache.session_limit", "[SessionLimitCache] Failed to preload Lua script: %v", err)
		}
	}

	return &sessionLimitCache{
		rdb:                rdb,
		defaultIdleTimeout: time.Duration(defaultIdleTimeoutMinutes) * time.Minute,
	}
}

// sessionLimitKey returns the Redis sorted-set key for one provider's session
// activity.
func sessionLimitKey(providerID int64) string {
	return fmt.Sprintf("%s%d", sessionLimitKeyPrefix, providerID)
}

// RegisterSession refreshes existing sessions or admits new sessions below
// maxSessions after pruning idle members. Empty IDs or nonpositive limits
// bypass storage; nonpositive converted timeouts use the configured default,
// and Redis errors return allowed=true with the error for caller policy.
func (c *sessionLimitCache) RegisterSession(ctx context.Context, providerID int64, sessionUUID string, maxSessions int, idleTimeout time.Duration) (bool, error) {
	if sessionUUID == "" || maxSessions <= 0 {
		return true, nil
	}

	key := sessionLimitKey(providerID)
	// Truncate durations to the whole-second score resolution before checking
	// whether the default timeout needs to replace the converted value.
	idleTimeoutSeconds := int(idleTimeout.Seconds())
	if idleTimeoutSeconds <= 0 {
		idleTimeoutSeconds = int(c.defaultIdleTimeout.Seconds())
	}

	result, err := registerSessionScript.Run(ctx, c.rdb, []string{key}, maxSessions, idleTimeoutSeconds, sessionUUID).Int()
	// Return admission with the cache error so scheduler session policy can
	// continue an attempt when distributed registration is unconfirmed.
	if err != nil {
		return true, err
	}
	return result == 1, nil
}

// UnregisterSession removes this provider's session member immediately,
// releasing idle-window capacity for failed attempts. Empty session IDs are
// ignored, and deleting an already absent member succeeds.
func (c *sessionLimitCache) UnregisterSession(ctx context.Context, providerID int64, sessionUUID string) error {
	if sessionUUID == "" {
		return nil
	}
	return c.rdb.ZRem(ctx, sessionLimitKey(providerID), sessionUUID).Err()
}

// RefreshSession renews an existing session's activity score and set expiry.
// Empty IDs are ignored; nonpositive whole-second timeouts use the default,
// and existing old members can be refreshed before another query prunes them.
func (c *sessionLimitCache) RefreshSession(ctx context.Context, providerID int64, sessionUUID string, idleTimeout time.Duration) error {
	if sessionUUID == "" {
		return nil
	}

	key := sessionLimitKey(providerID)
	// Truncate durations to the whole-second score resolution before checking
	// whether the default timeout needs to replace the converted value.
	idleTimeoutSeconds := int(idleTimeout.Seconds())
	if idleTimeoutSeconds <= 0 {
		idleTimeoutSeconds = int(c.defaultIdleTimeout.Seconds())
	}

	_, err := refreshSessionScript.Run(ctx, c.rdb, []string{key}, idleTimeoutSeconds, sessionUUID).Result()
	return err
}

// GetActiveSessionCount prunes and counts this provider's sessions using the
// default idle timeout. The read can remove expired registrations; Redis
// failures remain errors for the caller's admission policy.
func (c *sessionLimitCache) GetActiveSessionCount(ctx context.Context, providerID int64) (int, error) {
	key := sessionLimitKey(providerID)
	// Convert the constructor's minute-based duration into the seconds used by
	// Redis activity scores.
	idleTimeoutSeconds := int(c.defaultIdleTimeout.Seconds())

	result, err := getActiveSessionCountScript.Run(ctx, c.rdb, []string{key}, idleTimeoutSeconds).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// GetActiveSessionCountBatch prunes requested provider sets and returns
// successful counts by ID. Positive duration overrides select per-provider
// idle windows; whole-second conversion can turn a subsecond override into a
// zero-second idle window.
//
// Every pipeline command uses EVAL source so it can run with an empty script
// cache. Successful results are retained even when another command fails,
// failed providers are absent from the map, and the returned error is nil.
func (c *sessionLimitCache) GetActiveSessionCountBatch(ctx context.Context, providerIDs []int64, idleTimeouts map[int64]time.Duration) (map[int64]int, error) {
	if len(providerIDs) == 0 {
		return make(map[int64]int), nil
	}

	results := make(map[int64]int, len(providerIDs))

	pipe := c.rdb.Pipeline()

	cmds := make(map[int64]*redis.Cmd, len(providerIDs))
	for _, providerID := range providerIDs {
		key := sessionLimitKey(providerID)
		idleTimeout := c.defaultIdleTimeout
		if idleTimeouts != nil {
			if t, ok := idleTimeouts[providerID]; ok && t > 0 {
				idleTimeout = t
			}
		}
		// Convert each selected duration to seconds after accepting a positive
		// override; subsecond overrides therefore produce a zero-second cutoff in
		// this batch path.
		idleTimeoutSeconds := int(idleTimeout.Seconds())
		// Send script source per provider so batch activity pruning remains
		// available after Redis loses its loaded script cache.
		cmds[providerID] = getActiveSessionCountScript.Eval(ctx, pipe, []string{key}, idleTimeoutSeconds)
	}

	// Inspect individual commands after pipeline errors so successful counts
	// remain usable; omit failures and let callers apply their
	// missing-observation policy.
	_, _ = pipe.Exec(ctx)

	for providerID, cmd := range cmds {
		if result, err := cmd.Int(); err == nil {
			results[providerID] = result
		}
	}

	return results, nil
}

// IsSessionActive compares this member's score with the default idle cutoff
// and returns false for empty IDs. The query leaves expired members stored;
// Redis errors remain errors for caller handling.
func (c *sessionLimitCache) IsSessionActive(ctx context.Context, providerID int64, sessionUUID string) (bool, error) {
	if sessionUUID == "" {
		return false, nil
	}

	key := sessionLimitKey(providerID)
	// Convert the default idle duration to the second resolution used by stored
	// activity scores.
	idleTimeoutSeconds := int(c.defaultIdleTimeout.Seconds())

	result, err := isSessionActiveScript.Run(ctx, c.rdb, []string{key}, idleTimeoutSeconds, sessionUUID).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
