package rediscache

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// concurrency_slots.go admits ordinary provider and user request slots, tracks
// API-key concurrency statistics, manages wait counters, and reads batch load.
// Ordinary admission scripts prune expired members against Redis TIME and count
// ordinary and Live sets together before recording or refreshing a request ID,
// while Go updates active discovery indexes separately after confirmed writes.
//
// Batch concurrency and load readers share one Redis TIME observation across a
// pipeline, pruning ordinary and sixty-second Live sets before reading ZCARD
// and optional wait keys. Every command in the batch is checked for non-nil
// errors other than redis.Nil so partial slot failures abort the observation
// instead of reporting zero load.

// Ordinary slot, statistics, and wait-queue Lua scripts couple capacity checks
// to member or counter mutations under the shared Redis server clock.
var (
	// acquireScript prunes ordinary and Live scores, then admits a new request
	// below their combined limit or refreshes an existing ordinary member. It
	// returns admission and Redis seconds so Go can index confirmed writes.
	acquireScript = redis.NewScript(`
		-- 兼容 3.2-4.x：脚本使用 TIME，需启用按效果复制，确保写入能同步到从库。
		-- 5.0 及以上默认按效果复制；保留调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local liveKey = KEYS[2]
		local maxConcurrency = tonumber(ARGV[1])
		local ttl = tonumber(ARGV[2])
		local requestID = ARGV[3]

		-- 使用 Redis 服务器时间，确保多实例时钟一致
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		-- 清理过期槽位
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		redis.call('ZREMRANGEBYSCORE', liveKey, '-inf', now - 60)

		-- 检查是否已存在（支持重试场景刷新时间戳）
		local exists = redis.call('ZSCORE', key, requestID)
		if exists ~= false then
			redis.call('ZADD', key, now, requestID)
			redis.call('EXPIRE', key, ttl)
			return {1, now}
		end

		-- 检查是否达到并发上限
		local count = redis.call('ZCARD', key) + redis.call('ZCARD', liveKey)
		if count < maxConcurrency then
			redis.call('ZADD', key, now, requestID)
			redis.call('EXPIRE', key, ttl)
			return {1, now}
		end

		return {0, now}
	`)

	// getCountScript prunes ordinary members by the configured slot lifetime and
	// Live members by sixty seconds, then returns their combined count for
	// admission observations.
	getCountScript = redis.NewScript(`
		-- 兼容 3.2-4.x：脚本使用 TIME，需启用按效果复制，确保写入能同步到从库。
		-- 5.0 及以上默认按效果复制；保留调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local liveKey = KEYS[2]
		local ttl = tonumber(ARGV[1])

		-- 使用 Redis 服务器时间
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		redis.call('ZREMRANGEBYSCORE', liveKey, '-inf', now - 60)
		return redis.call('ZCARD', key) + redis.call('ZCARD', liveKey)
	`)

	// trackSlotScript prunes and records an ordinary statistics member using
	// Redis seconds, refreshing the set TTL without an admission-capacity
	// comparison.
	trackSlotScript = redis.NewScript(`
		-- 兼容 Redis 3.2-4.x：启用按效果复制，确保 TIME 调用正确同步。
		-- Redis 5.0 及以上默认按效果复制，因此该调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local requestID = ARGV[2]

		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		redis.call('ZADD', key, now, requestID)
		redis.call('EXPIRE', key, ttl)
		return 1
	`)

	// incrementWaitScript admits another user waiter below maxWait and refreshes
	// the counter TTL. It returns admission and Redis seconds for subsequent
	// user-index discovery updates.
	incrementWaitScript = redis.NewScript(`
		-- Redis 3.2-4.x compat: opt into effects replication so redis.call('TIME')
		-- replicates correctly. No-op on Redis 5.0+ (effects replication is default).
		redis.replicate_commands()
		local current = redis.call('GET', KEYS[1])
		if current == false then
			current = 0
		else
			current = tonumber(current)
		end
		local now = tonumber(redis.call('TIME')[1])

		if current >= tonumber(ARGV[1]) then
			return {0, now}
		end

		redis.call('INCR', KEYS[1])

		-- Refresh TTL so long-running traffic doesn't expire active queue counters.
		redis.call('EXPIRE', KEYS[1], ARGV[2])

		return {1, now}
	`)

	// incrementProviderWaitScript admits another provider waiter below maxWait
	// and refreshes its counter TTL. It returns admission and Redis seconds for
	// subsequent provider-index discovery updates.
	incrementProviderWaitScript = redis.NewScript(`
		-- Redis 3.2-4.x compat: opt into effects replication so redis.call('TIME')
		-- replicates correctly. No-op on Redis 5.0+ (effects replication is default).
		redis.replicate_commands()
		local current = redis.call('GET', KEYS[1])
		if current == false then
			current = 0
		else
			current = tonumber(current)
		end
		local now = tonumber(redis.call('TIME')[1])

		if current >= tonumber(ARGV[1]) then
			return {0, now}
		end

		redis.call('INCR', KEYS[1])

		-- Refresh TTL so long-running traffic doesn't expire active queue counters.
		redis.call('EXPIRE', KEYS[1], ARGV[2])

		return {1, now}
	`)

	// decrementWaitScript reduces a present positive wait counter while leaving
	// missing or zero counters unchanged, supporting cleanup of confirmed
	// admission counts.
	decrementWaitScript = redis.NewScript(`
			local current = redis.call('GET', KEYS[1])
			if current ~= false and tonumber(current) > 0 then
				redis.call('DECR', KEYS[1])
			end
			return 1
		`)
)

// loadBatchEntry pairs a provider or user identity with the capacity
// denominator used to compute its integer load percentage.
type loadBatchEntry struct {
	id             int64 // provider or user identity
	maxConcurrency int   // configured capacity denominator
}

// loadBatchObservation holds combined slot, queue, and load-rate figures for one
// provider or user entry from a shared Redis pipeline read.
type loadBatchObservation struct {
	id                 int64 // observed provider or user identity
	currentConcurrency int   // combined ordinary and Live slot count
	waitingCount       int   // parsed stored waiting count
	loadRate           int   // integer load percentage including queue pressure
}

// Provider slot operations

// AcquireProviderSlot admits a new ordinary request below combined
// ordinary/Live capacity, or refreshes its existing member. Nonpositive
// limits reject new IDs; callers release confirmed ownership using the same
// request ID.
func (c *concurrencyCache) AcquireProviderSlot(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
	key := providerSlotKey(providerID)
	result, now, err := runScriptInt64Pair(ctx, c.rdb, acquireScript, []string{key, liveProviderSlotKey(providerID)}, maxConcurrency, c.slotTTLSeconds, requestID)
	if err != nil {
		return false, err
	}
	if result == 1 {
		// Index confirmed ordinary acquisition using the script's clock, keeping
		// discovery writes separate so maintenance failures do not reverse the
		// acquired provider member.
		c.touchActiveIndexAt(ctx, providerActiveIndexKey, providerID, now+int64(c.slotTTLSeconds))
	}
	return result == 1, nil
}

// ReleaseProviderSlot removes this request's ordinary provider member and
// reconciles discovery load afterward. Missing members are harmless; primary
// removal errors propagate, while index maintenance errors are logged.
func (c *concurrencyCache) ReleaseProviderSlot(ctx context.Context, providerID int64, requestID string) error {
	key := providerSlotKey(providerID)
	if err := c.rdb.ZRem(ctx, key, requestID).Err(); err != nil {
		return err
	}
	// Recheck ordinary slots and waiters after removing the request member so
	// discovery can retire an idle provider while later traffic may still race
	// with that observation.
	c.refreshProviderActiveIndex(ctx, providerID)
	return nil
}

// GetProviderConcurrency prunes ordinary and Live scores with their
// respective activity lifetimes and returns the combined count. This
// observation can change before the next atomic admission attempt.
func (c *concurrencyCache) GetProviderConcurrency(ctx context.Context, providerID int64) (int, error) {
	key := providerSlotKey(providerID)
	result, err := getCountScript.Run(ctx, c.rdb, []string{key, liveProviderSlotKey(providerID)}, c.slotTTLSeconds).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// GetProviderConcurrencyBatch returns combined ordinary and Live counts keyed
// by provider ID, with an allocated empty map for empty input. Duplicate IDs
// share one result entry, using the last count observed for that provider.
func (c *concurrencyCache) GetProviderConcurrencyBatch(ctx context.Context, providerIDs []int64) (map[int64]int, error) {
	return c.getConcurrencyBatch(ctx, providerIDs, providerSlotKeyPrefix, liveProviderSlotKeyPrefix)
}

// User slot operations

// AcquireUserSlot admits a new ordinary request below combined ordinary/Live
// user capacity, or refreshes its existing member. Nonpositive limits reject
// new IDs; callers release confirmed ownership using the same request ID.
func (c *concurrencyCache) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	key := userSlotKey(userID)
	result, now, err := runScriptInt64Pair(ctx, c.rdb, acquireScript, []string{key, liveUserSlotKey(userID)}, maxConcurrency, c.slotTTLSeconds, requestID)
	if err != nil {
		return false, err
	}
	if result == 1 {
		// Index confirmed ordinary acquisition using the script's clock so startup
		// and periodic cleanup can find user load after request activity or a
		// crash.
		c.touchActiveIndexAt(ctx, userActiveIndexKey, userID, now+int64(c.slotTTLSeconds))
	}
	return result == 1, nil
}

// ReleaseUserSlot removes this request's ordinary user member and reconciles
// discovery load afterward. Missing members are harmless; primary removal
// errors propagate, while index maintenance errors are logged.
func (c *concurrencyCache) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	key := userSlotKey(userID)
	if err := c.rdb.ZRem(ctx, key, requestID).Err(); err != nil {
		return err
	}
	// Recheck ordinary slots and waiters after primary removal so user discovery
	// follows remaining request load; maintenance errors leave that completed
	// removal in place.
	c.refreshUserActiveIndex(ctx, userID)
	return nil
}

// GetUserConcurrency prunes ordinary and Live scores with their respective
// activity lifetimes and returns the combined user count. This observation
// can change before the next atomic admission attempt.
func (c *concurrencyCache) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	key := userSlotKey(userID)
	result, err := getCountScript.Run(ctx, c.rdb, []string{key, liveUserSlotKey(userID)}, c.slotTTLSeconds).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// TrackAPIKeySlot records or refreshes an ordinary request member for API-key
// statistics without enforcing an admission limit. Callers release it using
// the same request ID after its counted request finishes.
func (c *concurrencyCache) TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	key := apiKeySlotKey(apiKeyID)
	_, err := trackSlotScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds, requestID).Result()
	return err
}

// ReleaseAPIKeySlot removes the ordinary API-key statistics member for this
// request ID. Missing members are harmless, while Redis failures propagate to
// the caller coordinating request cleanup.
func (c *concurrencyCache) ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	key := apiKeySlotKey(apiKeyID)
	return c.rdb.ZRem(ctx, key, requestID).Err()
}

// GetAPIKeyConcurrencyBatch returns ordinary-plus-Live statistics counts
// keyed by API-key ID, with an allocated empty map for empty input. Duplicate
// IDs share one statistics entry, using the last count observed for that key.
func (c *concurrencyCache) GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	return c.getConcurrencyBatch(ctx, apiKeyIDs, apiKeySlotKeyPrefix, liveAPIKeySlotKeyPrefix)
}

// getConcurrencyBatch returns counts for the ordinary and Live sets under the
// supplied resource prefixes. Empty inputs return an allocated empty map before
// Redis is called. Duplicate IDs each queue their own reads, and the last
// observation for an ID supplies that map entry.
//
// One Redis TIME sample supplies both pruning cutoffs. The pipeline queues both
// removals before the two counts for each input ID, retaining the count commands
// with that ID until results are assembled. Any command or pipeline error other
// than redis.Nil discards the batch.
func (c *concurrencyCache) getConcurrencyBatch(ctx context.Context, ids []int64, slotKeyPrefix, liveKeyPrefix string) (map[int64]int, error) {
	if len(ids) == 0 {
		return map[int64]int{}, nil
	}

	now, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("redis TIME: %w", err)
	}
	// Use one Redis clock observation with the ordinary lifetime; Live pruning
	// below uses its shorter renewal window for the other counted set.
	cutoffTime := now.Unix() - int64(c.slotTTLSeconds)

	pipe := c.rdb.Pipeline()
	// resourceCountCmd associates a resource identity with its queued count commands.
	type resourceCountCmd struct {
		// id, zcardCmd and liveCmd address the same resource; keeping their count
		// commands together associates both ordinary and Live results with that ID
		// during map assembly.
		id       int64         // associated resource count identity
		zcardCmd *redis.IntCmd // queued ordinary slot count
		liveCmd  *redis.IntCmd // queued Live lease count
	}
	cmds := make([]resourceCountCmd, 0, len(ids))
	for _, id := range ids {
		slotKey := slotKeyPrefix + strconv.FormatInt(id, 10)
		liveKey := liveKeyPrefix + strconv.FormatInt(id, 10)
		// Prune both activity sets before queuing their counts so the result
		// combines current ordinary and Live membership under their respective
		// expiration windows.
		pipe.ZRemRangeByScore(ctx, slotKey, "-inf", strconv.FormatInt(cutoffTime, 10))
		pipe.ZRemRangeByScore(ctx, liveKey, "-inf", strconv.FormatInt(now.Unix()-liveLeaseTTLSeconds, 10))
		cmds = append(cmds, resourceCountCmd{
			id:       id,
			zcardCmd: pipe.ZCard(ctx, slotKey),
			liveCmd:  pipe.ZCard(ctx, liveKey),
		})
	}

	// Permit redis.Nil while rejecting any failed pruning or count command in
	// the pipeline before assembling resource totals.
	if err := execPipelineAllowingNil(ctx, pipe); err != nil {
		return nil, err
	}

	result := make(map[int64]int, len(ids))
	for _, cmd := range cmds {
		result[cmd.id] = int(cmd.zcardCmd.Val() + cmd.liveCmd.Val())
	}
	return result, nil
}

// Wait queue operations

// IncrementWaitCount records another user waiter below maxWait and refreshes
// its configured expiry. Nonpositive limits reject new increments; callers
// decrement a count only after receiving confirmed admission.
func (c *concurrencyCache) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	key := waitQueueKey(userID)
	result, now, err := runScriptInt64Pair(ctx, c.rdb, incrementWaitScript, []string{key}, maxWait, c.waitQueueTTLSeconds)
	if err != nil {
		return false, err
	}
	if result == 1 {
		// Index confirmed wait activity independently of slot occupancy so a user
		// with only queued requests remains discoverable for maintenance.
		c.touchActiveIndexAt(ctx, userActiveIndexKey, userID, now+int64(c.waitQueueTTLSeconds))
	}
	return result == 1, nil
}

// DecrementWaitCount reduces an existing positive user wait counter and then
// reconciles discovery load. Missing or zero counters stay unchanged; callers
// use it to release confirmed wait admission.
func (c *concurrencyCache) DecrementWaitCount(ctx context.Context, userID int64) error {
	key := waitQueueKey(userID)
	_, err := decrementWaitScript.Run(ctx, c.rdb, []string{key}).Result()
	if err == nil {
		// Reconcile discovery after the confirmed decrement so remaining ordinary
		// slots or positive waiters retain the entry, while an idle user can be
		// removed.
		c.refreshUserActiveIndex(ctx, userID)
	}
	return err
}

// Provider wait queue operations

// IncrementProviderWaitCount records another provider waiter below maxWait
// and refreshes its configured expiry. Nonpositive limits reject new
// increments; callers decrement a count only after receiving confirmed
// admission.
func (c *concurrencyCache) IncrementProviderWaitCount(ctx context.Context, providerID int64, maxWait int) (bool, error) {
	key := providerWaitKey(providerID)
	result, now, err := runScriptInt64Pair(ctx, c.rdb, incrementProviderWaitScript, []string{key}, maxWait, c.waitQueueTTLSeconds)
	if err != nil {
		return false, err
	}
	if result == 1 {
		// Record confirmed provider wait activity in discovery so cleanup can find
		// queue-only load after all ordinary provider members have finished.
		c.touchActiveIndexAt(ctx, providerActiveIndexKey, providerID, now+int64(c.waitQueueTTLSeconds))
	}
	return result == 1, nil
}

// DecrementProviderWaitCount reduces an existing positive provider wait
// counter and then reconciles discovery load. Missing or zero counters stay
// unchanged; callers use it to release confirmed wait admission.
func (c *concurrencyCache) DecrementProviderWaitCount(ctx context.Context, providerID int64) error {
	key := providerWaitKey(providerID)
	_, err := decrementWaitScript.Run(ctx, c.rdb, []string{key}).Result()
	if err == nil {
		// Reconcile discovery after the confirmed decrement so ordinary slots or
		// positive waiters retain the entry, while an idle provider can be removed.
		c.refreshProviderActiveIndex(ctx, providerID)
	}
	return err
}

// GetProviderWaitingCount returns the stored provider waiting count, with
// zero for a missing key. Redis and integer-conversion errors propagate so
// callers can distinguish missing load from unavailable observation.
func (c *concurrencyCache) GetProviderWaitingCount(ctx context.Context, providerID int64) (int, error) {
	key := providerWaitKey(providerID)
	val, err := c.rdb.Get(ctx, key).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	// An absent wait key supplies a zero queue observation, letting load
	// consumers distinguish a provider with no stored waiters from a failed
	// read.
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return val, nil
}

// GetProvidersLoadBatch reports occupied ordinary/Live slots, stored waiter
// counts and integer load percentages for the supplied provider capacities.
// Empty input returns an empty map; nonpositive capacities produce a zero
// percentage. For duplicate IDs, later input entries replace earlier result
// entries and supply their own percentage denominator.
//
// One Redis clock sample drives the two score-pruning windows before
// per-provider commands read both slot sets and wait counters. Missing or
// unparsable waits contribute zero, while any command or pipeline error other
// than redis.Nil aborts the batch so failed slot reads cannot report zero load.
func (c *concurrencyCache) GetProvidersLoadBatch(ctx context.Context, providers []scheduler.ProviderWithConcurrency) (map[int64]*scheduler.ProviderLoadInfo, error) {
	if len(providers) == 0 {
		return map[int64]*scheduler.ProviderLoadInfo{}, nil
	}
	entries := make([]loadBatchEntry, len(providers))
	for i, acc := range providers {
		entries[i] = loadBatchEntry{id: acc.ID, maxConcurrency: acc.MaxConcurrency}
	}
	observations, err := c.execLoadBatch(ctx, entries, providerSlotKeyPrefix, liveProviderSlotKeyPrefix, providerWaitKeyPrefix)
	if err != nil {
		return nil, err
	}
	loadMap := make(map[int64]*scheduler.ProviderLoadInfo, len(providers))
	for _, obs := range observations {
		loadMap[obs.id] = &scheduler.ProviderLoadInfo{
			ProviderID:         obs.id,
			CurrentConcurrency: obs.currentConcurrency,
			WaitingCount:       obs.waitingCount,
			LoadRate:           obs.loadRate,
		}
	}
	return loadMap, nil
}

// GetUsersLoadBatch reports occupied ordinary/Live user slots, stored waiter
// counts and integer load percentages for supplied capacities. Empty input
// returns an empty map; nonpositive capacities produce a zero percentage.
// For duplicate IDs, later input entries replace earlier results and supply
// their own percentage denominator.
//
// One Redis time sample provides ordinary-slot and short Live pruning cutoffs
// before queued commands read each user's load. Missing or unparsable waits
// contribute zero, while any command or pipeline error other than redis.Nil
// aborts the batch so failed slot reads cannot report zero load.
func (c *concurrencyCache) GetUsersLoadBatch(ctx context.Context, users []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	if len(users) == 0 {
		return map[int64]*scheduler.UserLoadInfo{}, nil
	}
	entries := make([]loadBatchEntry, len(users))
	for i, u := range users {
		entries[i] = loadBatchEntry{id: u.ID, maxConcurrency: u.MaxConcurrency}
	}
	observations, err := c.execLoadBatch(ctx, entries, userSlotKeyPrefix, liveUserSlotKeyPrefix, waitQueueKeyPrefix)
	if err != nil {
		return nil, err
	}
	loadMap := make(map[int64]*scheduler.UserLoadInfo, len(users))
	for _, obs := range observations {
		loadMap[obs.id] = &scheduler.UserLoadInfo{
			UserID:             obs.id,
			CurrentConcurrency: obs.currentConcurrency,
			WaitingCount:       obs.waitingCount,
			LoadRate:           obs.loadRate,
		}
	}
	return loadMap, nil
}

// execLoadBatch prunes ordinary and Live sorted sets under one Redis TIME
// sample, reads slot cardinalities and wait counters for entries in order, and
// computes integer load percentages. Callers pass non-empty entries; any
// command error other than redis.Nil aborts the batch.
func (c *concurrencyCache) execLoadBatch(
	ctx context.Context,
	entries []loadBatchEntry,
	slotKeyPrefix, liveKeyPrefix, waitKeyPrefix string,
) ([]loadBatchObservation, error) {
	now, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("redis TIME: %w", err)
	}
	// Use one Redis clock observation with the ordinary lifetime; Live pruning
	// below uses its shorter renewal window for the other counted set.
	cutoffTime := now.Unix() - int64(c.slotTTLSeconds)
	liveCutoff := now.Unix() - liveLeaseTTLSeconds

	pipe := c.rdb.Pipeline()
	// entryCmds retains one identity's supplied capacity beside its queued
	// ordinary-slot, Live-lease, and wait-counter commands.
	type entryCmds struct {
		// id, maxConcurrency, zcardCmd, liveCmd and getCmd describe one entity
		// observation so result assembly applies its denominator to its own counts.
		id             int64            // associated provider or user identity
		maxConcurrency int              // supplied load percentage denominator
		zcardCmd       *redis.IntCmd    // queued ordinary slot count
		liveCmd        *redis.IntCmd    // queued Live lease count
		getCmd         *redis.StringCmd // queued wait counter observation
	}
	cmds := make([]entryCmds, 0, len(entries))
	for _, entry := range entries {
		idStr := strconv.FormatInt(entry.id, 10)
		slotKey := slotKeyPrefix + idStr
		liveKey := liveKeyPrefix + idStr
		waitKey := waitKeyPrefix + idStr
		// Prune both activity sets before queuing their counts so the result
		// combines current ordinary and Live membership under their respective
		// expiration windows.
		pipe.ZRemRangeByScore(ctx, slotKey, "-inf", strconv.FormatInt(cutoffTime, 10))
		pipe.ZRemRangeByScore(ctx, liveKey, "-inf", strconv.FormatInt(liveCutoff, 10))
		cmds = append(cmds, entryCmds{
			id:             entry.id,
			maxConcurrency: entry.maxConcurrency,
			zcardCmd:       pipe.ZCard(ctx, slotKey),
			liveCmd:        pipe.ZCard(ctx, liveKey),
			getCmd:         pipe.Get(ctx, waitKey),
		})
	}

	if err := execPipelineAllowingNil(ctx, pipe); err != nil {
		return nil, err
	}

	observations := make([]loadBatchObservation, 0, len(cmds))
	for _, ec := range cmds {
		currentConcurrency := int(ec.zcardCmd.Val() + ec.liveCmd.Val())
		waitingCount := 0
		// Keep parsed waiting values as observed and use zero when the key is
		// absent or unparsable, allowing successful slot counts to determine load.
		if v, err := ec.getCmd.Int(); err == nil {
			waitingCount = v
		}
		loadRate := 0
		if ec.maxConcurrency > 0 {
			// Combine occupied slots with queue pressure; integer division truncates
			// fractional load percentages.
			loadRate = (currentConcurrency + waitingCount) * 100 / ec.maxConcurrency
		}
		observations = append(observations, loadBatchObservation{
			id:                 ec.id,
			currentConcurrency: currentConcurrency,
			waitingCount:       waitingCount,
			loadRate:           loadRate,
		})
	}
	return observations, nil
}
