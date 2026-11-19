package rediscache

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// concurrency_cache.go defines shared Redis concurrency state, key builders,
// pipeline execution checks, active-index discovery, and startup slot cleanup.
// Ordinary provider and user slots share capacity with short-lived Live leases
// across separate sorted sets, while wait counters add queue pressure to load
// observations. Redis TIME supplies score cutoffs so instances prune members
// against one clock.
//
// Lua scripts commit primary slot or wait writes before Go updates discovery
// indexes used by periodic and startup maintenance. Index updates log failures
// without rolling back primary admissions, and startup cleanup retains only the
// supplied process prefix in indexed ordinary sets while resetting wait keys.
// Pipeline observations inspect every queued command error so an earlier
// redis.Nil wait lookup cannot hide a later slot-count failure.

// Concurrency namespaces separate ordinary slots, renewable leases and wait
// observations; TTLs and index batch limits control expiry pruning and
// discovery maintenance work.
const (
	// providerSlotKeyPrefix names ordinary provider request sets, whose
	// Redis-time scores are counted together with the corresponding Live lease
	// set.
	providerSlotKeyPrefix = "concurrency:provider:"
	// userSlotKeyPrefix names ordinary user request sets, separating user-wide
	// admission from provider capacity and API-key statistics.
	userSlotKeyPrefix = "concurrency:user:"
	// apiKeySlotKeyPrefix names ordinary API-key statistics sets, recorded
	// independently of the dedicated ingress-connection admission keys.
	apiKeySlotKeyPrefix = "concurrency:api_key:"
	// liveProviderSlotKeyPrefix names renewable provider lease sets counted with
	// ordinary provider slots during request admission.
	liveProviderSlotKeyPrefix = "concurrency:live:provider:"
	// liveUserSlotKeyPrefix names renewable user lease sets counted with
	// ordinary user slots during request admission.
	liveUserSlotKeyPrefix = "concurrency:live:user:"
	// liveAPIKeySlotKeyPrefix names renewable API-key lease sets included in
	// batch concurrency statistics for long-lived Live sessions.
	liveAPIKeySlotKeyPrefix = "concurrency:live:api_key:"
	// openAIWSIngressLeaseKeyPrefix names API-key connection admission sets,
	// separating client WebSocket ingress lifetime from ordinary request turns.
	openAIWSIngressLeaseKeyPrefix = "concurrency:openai_ws_ingress:api_key:"
	// openAIWSIngressLeaseTTLSeconds is the sixty-second inactivity window used
	// when acquiring, pruning and renewing ingress connection members.
	openAIWSIngressLeaseTTLSeconds = 60
	// liveLeaseTTLSeconds is the sixty-second inactivity window used to prune
	// and renew provider, user and API-key Live memberships.
	liveLeaseTTLSeconds = 60
	// waitQueueKeyPrefix names user queue counters, indexed with ordinary user
	// load so maintenance can discover waiting activity.
	waitQueueKeyPrefix = "concurrency:wait:"
	// providerWaitKeyPrefix names provider queue counters, combined with
	// occupied slots when reporting provider load and indexing cleanup
	// candidates.
	providerWaitKeyPrefix = "wait:provider:"

	// defaultSlotTTLMinutes supplies the ordinary request lifetime when
	// NewConcurrencyCache receives a nonpositive configured minute value.
	defaultSlotTTLMinutes = 15

	// providerActiveIndexKey stores provider IDs with expected ordinary-slot or
	// wait-counter expiry scores for cleanup discovery.
	providerActiveIndexKey = "concurrency:provider:active_index"
	// userActiveIndexKey stores user IDs with expected ordinary-slot or
	// wait-counter expiry scores for cleanup discovery.
	userActiveIndexKey = "concurrency:user:active_index"

	// activeIndexCleanupBatchSize bounds the due members read from one index in
	// a periodic reconciliation call.
	activeIndexCleanupBatchSize = 1000
	// activeIndexPipelineChunkSize bounds candidate identities per load-reading
	// pipeline while each candidate queues pruning and count operations.
	activeIndexPipelineChunkSize = 500

	// legacyWaitSweepMarkerKey persists successful completion of the startup
	// wait-prefix sweep, allowing a failed or absent-marker sweep to retry.
	legacyWaitSweepMarkerKey = "concurrency:startup:legacy_wait_sweep:v1"
)

// Maintenance scripts prune ordinary sets during periodic reconciliation and
// startup recovery, while providerSlotIndex and userSlotIndex pair each
// discovery set with its primary ordinary-slot and wait keys.
var (
	// cleanupExpiredSlotsScript prunes one ordinary set by Redis activity age,
	// deletes an empty set and refreshes the key TTL for remaining members.
	cleanupExpiredSlotsScript = redis.NewScript(`
		-- 兼容 3.2-4.x：脚本使用 TIME，需启用按效果复制，确保写入能同步到从库。
		-- 5.0 及以上默认按效果复制；保留调用不改变行为。
		redis.replicate_commands()
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		if redis.call('ZCARD', key) == 0 then
			redis.call('DEL', key)
		else
			redis.call('EXPIRE', key, ttl)
		end
		return 1
	`)

	// startupCleanupSlotScript removes ordinary members outside the supplied
	// current-process prefix, then returns removed/remaining counts for Go's
	// separate wait reset and index maintenance.
	startupCleanupSlotScript = redis.NewScript(`
		local key = KEYS[1]
		local activePrefix = ARGV[1]
		local slotTTL = tonumber(ARGV[2])
		local removed = 0
		local members = redis.call('ZRANGE', key, 0, -1)
		for _, member in ipairs(members) do
			if string.sub(member, 1, string.len(activePrefix)) ~= activePrefix then
				removed = removed + redis.call('ZREM', key, member)
			end
		end
		local remaining = redis.call('ZCARD', key)
		if remaining == 0 then
			redis.call('DEL', key)
		else
			redis.call('EXPIRE', key, slotTTL)
		end
		return {removed, remaining}
	`)

	// providerSlotIndex associates provider discovery members with ordinary
	// provider slot sets and provider wait counters for load-based maintenance.
	providerSlotIndex = slotIndexSpec{indexKey: providerActiveIndexKey, slotKey: providerSlotKey, waitKey: providerWaitKey}
	// userSlotIndex associates user discovery members with ordinary user slot
	// sets and user wait counters for load-based maintenance.
	userSlotIndex = slotIndexSpec{indexKey: userActiveIndexKey, slotKey: userSlotKey, waitKey: waitQueueKey}
)

// concurrencyCache applies Redis slot admission, lease renewal and indexed
// ordinary-load maintenance.
type concurrencyCache struct {
	// rdb is shared with callers and remains open through slot/lease release,
	// batch observations and discovery-index maintenance.
	rdb *redis.Client // shared concurrency storage client
	// slotTTLSeconds and waitQueueTTLSeconds come from NewConcurrencyCache;
	// activeIndexTTL chooses the longer configured lifetime for the kinds of
	// ordinary load still present.
	slotTTLSeconds      int // ordinary slot lifetime seconds
	waitQueueTTLSeconds int // wait counter lifetime seconds
}

// slotIndexSpec associates one discovery index with ordinary slot and
// wait-counter key builders.
type slotIndexSpec struct {
	// indexKey, slotKey and waitKey address the same provider or user identity;
	// maintenance reads both primary load kinds before removing or refreshing
	// that identity's discovery member.
	indexKey string             // provider or user discovery index
	slotKey  func(int64) string // ordinary slot key builder
	waitKey  func(int64) string // wait counter key builder
}

// activeIndexLoad associates an observed ordinary-load pair with its parsed
// and stored identity.
type activeIndexLoad struct {
	// id selects primary load keys while member retains the discovery set's text
	// for exact removal or rescheduling, including valid noncanonical numeric
	// spellings.
	id     int64  // parsed numeric entity identity
	member string // stored discovery member text
	// slotCount and waitCount describe ordinary slots and positive wait activity
	// used by activeIndexTTL; Live members use their own short-lived sets
	// outside this discovery calculation.
	slotCount int // remaining ordinary slot count
	waitCount int // positive parsed waiter count
}

// NewConcurrencyCache constructs ordinary slot, wait and renewable-lease
// storage over the caller's Redis client. Nonpositive slot minutes select
// fifteen minutes; nonpositive wait seconds use that selected slot lifetime.
func NewConcurrencyCache(rdb *redis.Client, slotTTLMinutes int, waitQueueTTLSeconds int) scheduler.ConcurrencyCache {
	if slotTTLMinutes <= 0 {
		slotTTLMinutes = defaultSlotTTLMinutes
	}
	if waitQueueTTLSeconds <= 0 {
		// Convert the selected minute lifetime to seconds for wait expiry.
		waitQueueTTLSeconds = slotTTLMinutes * 60
	}
	return &concurrencyCache{
		rdb: rdb,
		// Store ordinary lifetimes in the same seconds used by Redis scores.
		slotTTLSeconds:      slotTTLMinutes * 60,
		waitQueueTTLSeconds: waitQueueTTLSeconds,
	}
}

// providerSlotKey returns the ordinary request-member set key for one
// provider.
func providerSlotKey(providerID int64) string {
	return fmt.Sprintf("%s%d", providerSlotKeyPrefix, providerID)
}

// userSlotKey returns the ordinary request-member set key for one user.
func userSlotKey(userID int64) string {
	return fmt.Sprintf("%s%d", userSlotKeyPrefix, userID)
}

// apiKeySlotKey returns the ordinary statistics-member set key for one API
// key.
func apiKeySlotKey(apiKeyID int64) string {
	return fmt.Sprintf("%s%d", apiKeySlotKeyPrefix, apiKeyID)
}

// liveProviderSlotKey returns the renewable Live-member set key for one
// provider.
func liveProviderSlotKey(providerID int64) string {
	return fmt.Sprintf("%s%d", liveProviderSlotKeyPrefix, providerID)
}

// liveUserSlotKey returns the renewable Live-member set key for one user.
func liveUserSlotKey(userID int64) string {
	return fmt.Sprintf("%s%d", liveUserSlotKeyPrefix, userID)
}

// liveAPIKeySlotKey returns the renewable Live-member statistics key for one
// API key.
func liveAPIKeySlotKey(apiKeyID int64) string {
	return fmt.Sprintf("%s%d", liveAPIKeySlotKeyPrefix, apiKeyID)
}

// openAIWSIngressLeaseKey returns the client-connection lease set key for one
// API key.
func openAIWSIngressLeaseKey(apiKeyID int64) string {
	return fmt.Sprintf("%s%d", openAIWSIngressLeaseKeyPrefix, apiKeyID)
}

// waitQueueKey returns the user waiting-count key used by queue admission.
func waitQueueKey(userID int64) string {
	return fmt.Sprintf("%s%d", waitQueueKeyPrefix, userID)
}

// providerWaitKey returns the provider waiting-count key used by queue
// admission.
func providerWaitKey(providerID int64) string {
	return fmt.Sprintf("%s%d", providerWaitKeyPrefix, providerID)
}

// redisUnixSeconds returns the shared Redis clock value used for discovery
// expiry scores.
func (c *concurrencyCache) redisUnixSeconds(ctx context.Context) (int64, error) {
	now, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return 0, fmt.Errorf("redis TIME: %w", err)
	}
	return now.Unix(), nil
}

// execPipelineAllowingNil executes pipe and rejects any non-nil command or
// batch error other than redis.Nil. Checking each queued command prevents an
// earlier missing wait counter from masking a later failed ZCARD or pruning
// command in the same pipeline.
func execPipelineAllowingNil(ctx context.Context, pipe redis.Pipeliner) error {
	cmds, err := pipe.Exec(ctx)
	for _, cmd := range cmds {
		if cmdErr := cmd.Err(); cmdErr != nil && !errors.Is(cmdErr, redis.Nil) {
			return fmt.Errorf("pipeline exec: %w", cmdErr)
		}
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("pipeline exec: %w", err)
	}
	return nil
}

// touchActiveIndexAt records an entity's expected discovery expiry after a
// confirmed primary write. Nil clients or nonpositive identities/times are
// ignored; Redis indexing failures are logged, and later activity can retry
// discovery for an unindexed identity.
func (c *concurrencyCache) touchActiveIndexAt(ctx context.Context, indexKey string, id int64, expireAt int64) {
	if c == nil || c.rdb == nil || id <= 0 || expireAt <= 0 {
		return
	}
	// Log discovery write failure while retaining the already-completed primary
	// member or wait increment; subsequent activity can attempt indexing this
	// identity again.
	if err := c.rdb.ZAdd(ctx, indexKey, redis.Z{
		Score:  float64(expireAt),
		Member: strconv.FormatInt(id, 10),
	}).Err(); err != nil {
		logger.LegacyPrintf("repository.concurrency", "Warning: touch active index %s for %d failed: %v", indexKey, id, err)
	}
}

// refreshProviderActiveIndex reconciles this provider's discovery member with
// its remaining ordinary slots and waiters. Maintenance failures are logged
// by the shared helper so the completed release can still succeed.
func (c *concurrencyCache) refreshProviderActiveIndex(ctx context.Context, providerID int64) {
	c.refreshActiveIndex(ctx, providerActiveIndexKey, providerID, providerSlotKey(providerID), providerWaitKey(providerID))
}

// refreshUserActiveIndex reconciles this user's discovery member with its
// remaining ordinary slots and waiters. Maintenance failures are logged by
// the shared helper so the completed release can still succeed.
func (c *concurrencyCache) refreshUserActiveIndex(ctx context.Context, userID int64) {
	c.refreshActiveIndex(ctx, userActiveIndexKey, userID, userSlotKey(userID), waitQueueKey(userID))
}

// refreshActiveIndex updates one discovery member according to its ordinary
// slot and wait load, ignoring unusable clients or identities. Clock, load
// and index failures are logged while its caller's primary operation remains
// completed.
//
// The load read first prunes expired ordinary scores, then observes slots and
// positive wait count. An idle identity loses its discovery member; otherwise
// the configured lifetime of the present load kinds determines a new expiry
// score. These reads and index writes are separate Redis commands, so
// concurrent new activity can race with removal or rescheduling.
func (c *concurrencyCache) refreshActiveIndex(ctx context.Context, indexKey string, id int64, slotKey, waitKey string) {
	if c == nil || c.rdb == nil || id <= 0 {
		return
	}
	// Retain the discovery entry when a shared-clock read fails because a new
	// expiry cannot be computed from a confirmed Redis time sample.
	now, err := c.redisUnixSeconds(ctx)
	if err != nil {
		logger.LegacyPrintf("repository.concurrency", "Warning: refresh active index %s for %d failed: %v", indexKey, id, err)
		return
	}

	// Retain discovery when primary observation fails so unavailable load is not
	// treated as idle; any earlier pruning can remain completed.
	load, err := c.readActiveLoadForKey(ctx, id, slotKey, waitKey, now)
	if err != nil {
		logger.LegacyPrintf("repository.concurrency", "Warning: refresh active index %s for %d failed: %v", indexKey, id, err)
		return
	}
	member := strconv.FormatInt(id, 10)
	// Remove an idle observed identity from discovery; ordinary load reads and
	// this mutation are separate commands, so a concurrent acquisition can race
	// with removal.
	if load.slotCount == 0 && load.waitCount <= 0 {
		if err := c.rdb.ZRem(ctx, indexKey, member).Err(); err != nil {
			logger.LegacyPrintf("repository.concurrency", "Warning: remove active index member %s from %s failed: %v", member, indexKey, err)
		}
		return
	}

	ttlSeconds := c.activeIndexTTL(load.slotCount, load.waitCount)
	if ttlSeconds <= 0 {
		return
	}
	c.touchActiveIndexAt(ctx, indexKey, id, now+int64(ttlSeconds))
}

// activeIndexTTL returns the configured discovery lifetime for the ordinary
// load kinds that remain. With no positive slot or wait count it returns
// zero, letting the caller remove an idle member.
//
// Ordinary slots select slotTTLSeconds; positive wait counts can extend that
// result to waitQueueTTLSeconds when it is longer. This comparison keeps an
// indexed waiter discoverable after slots finish, and retains the longer
// configured window when both kinds remain. The result is a maintenance hint
// from the caller's observation, not a fresh read of each primary key's
// remaining TTL.
func (c *concurrencyCache) activeIndexTTL(slotCount int, waitCount int) int {
	ttlSeconds := 0
	if slotCount > 0 {
		ttlSeconds = c.slotTTLSeconds
	}
	if waitCount > 0 && c.waitQueueTTLSeconds > ttlSeconds {
		ttlSeconds = c.waitQueueTTLSeconds
	}
	return ttlSeconds
}

// readActiveLoadForKey prunes ordinary slot scores using the supplied Redis
// seconds and observes slots plus this identity's wait counter. Missing,
// nonpositive or unparsable waits become zero when every command succeeds or
// reports redis.Nil; any other command or pipeline error aborts the observation.
func (c *concurrencyCache) readActiveLoadForKey(ctx context.Context, id int64, slotKey, waitKey string, now int64) (activeIndexLoad, error) {
	cutoffTime := now - int64(c.slotTTLSeconds)
	pipe := c.rdb.Pipeline()
	// Prune ordinary scores before queuing their count so expired request
	// members cannot extend the configured discovery lifetime selected from this
	// observation.
	pipe.ZRemRangeByScore(ctx, slotKey, "-inf", strconv.FormatInt(cutoffTime, 10))
	zcardCmd := pipe.ZCard(ctx, slotKey)
	getCmd := pipe.Get(ctx, waitKey)
	if err := execPipelineAllowingNil(ctx, pipe); err != nil {
		return activeIndexLoad{}, err
	}

	waitCount := 0
	// Treat absent, unparsable or nonpositive waiting data as no positive
	// discovery load, keeping usable ordinary counts after a successful
	// pipeline.
	if v, err := getCmd.Int(); err == nil && v > 0 {
		waitCount = v
	}
	return activeIndexLoad{
		id:        id,
		member:    strconv.FormatInt(id, 10),
		slotCount: int(zcardCmd.Val()),
		waitCount: waitCount,
	}, nil
}

// readIndexLoads reads ordinary slot/wait load for valid discovery members
// and returns malformed members separately for removal. The caller supplies
// one Redis time sample for all pruning cutoffs.
//
// Each queued result retains both parsed identity and original member text so
// command results and later index repairs stay attached to the correct
// candidate. Pipelines are bounded by candidate chunks, prune before counting
// slots, and normalize missing or invalid waits to zero. Any command or
// pipeline error other than redis.Nil aborts the batch after earlier pruning.
func (c *concurrencyCache) readIndexLoads(ctx context.Context, spec slotIndexSpec, members []string, now int64) ([]activeIndexLoad, []string, error) {
	loads := make([]activeIndexLoad, 0, len(members))
	staleMembers := make([]string, 0)
	candidates := make([]activeIndexLoad, 0, len(members))
	for _, member := range members {
		id, err := strconv.ParseInt(member, 10, 64)
		if err != nil || id <= 0 {
			staleMembers = append(staleMembers, member)
			continue
		}
		candidates = append(candidates, activeIndexLoad{id: id, member: member})
	}

	cutoffTime := now - int64(c.slotTTLSeconds)
	// Bound candidates per pipeline while retaining their original member text,
	// so result assembly and later removals stay linked to the scanned discovery
	// entries.
	for start := 0; start < len(candidates); start += activeIndexPipelineChunkSize {
		end := min(start+activeIndexPipelineChunkSize, len(candidates))
		chunk := candidates[start:end]

		pipe := c.rdb.Pipeline()
		// loadCmd retains one discovery candidate beside its queued ordinary-slot
		// and wait observations.
		type loadCmd struct {
			activeIndexLoad // associated parsed discovery identity
			// zcardCmd and getCmd are queued for the embedded activeIndexLoad
			// candidate, retaining the relationship between its stored discovery
			// identity and observed primary load.
			zcardCmd *redis.IntCmd    // queued ordinary member count
			getCmd   *redis.StringCmd // queued stored wait count
		}
		cmds := make([]loadCmd, 0, len(chunk))
		for _, candidate := range chunk {
			slotKey := spec.slotKey(candidate.id)
			waitKey := spec.waitKey(candidate.id)
			// Prune old ordinary scores before their queued count so expired request
			// members cannot keep this observed discovery candidate classified as
			// loaded.
			pipe.ZRemRangeByScore(ctx, slotKey, "-inf", strconv.FormatInt(cutoffTime, 10))
			cmds = append(cmds, loadCmd{
				activeIndexLoad: candidate,
				zcardCmd:        pipe.ZCard(ctx, slotKey),
				getCmd:          pipe.Get(ctx, waitKey),
			})
		}
		if err := execPipelineAllowingNil(ctx, pipe); err != nil {
			return nil, nil, err
		}
		for _, cmd := range cmds {
			waitCount := 0
			// Normalize missing, invalid or nonpositive wait observations to zero so
			// successful ordinary counts still determine whether this candidate has
			// discoverable load.
			if v, err := cmd.getCmd.Int(); err == nil && v > 0 {
				waitCount = v
			}
			loads = append(loads, activeIndexLoad{
				id:        cmd.id,
				member:    cmd.member,
				slotCount: int(cmd.zcardCmd.Val()),
				waitCount: waitCount,
			})
		}
	}

	return loads, staleMembers, nil
}

// removeActiveIndexMembers attempts to remove discovery members selected as
// stale by its caller. Empty input is ignored; Redis failures are logged
// while the completed load check remains usable for other candidates.
func (c *concurrencyCache) removeActiveIndexMembers(ctx context.Context, indexKey string, members []string) {
	if len(members) == 0 {
		return
	}
	args := make([]any, 0, len(members))
	for _, member := range members {
		args = append(args, member)
	}
	// Log discovery removal failure and continue the caller's maintenance pass;
	// primary load decisions have already completed and a later pass can retry
	// stale entries.
	if err := c.rdb.ZRem(ctx, indexKey, args...).Err(); err != nil {
		logger.LegacyPrintf("repository.concurrency", "Warning: remove %d active index members from %s failed: %v", len(members), indexKey, err)
	}
}

// runScriptInt64Pair invokes a Lua script and decodes its first two
// integer-like response elements. Script and array/conversion errors
// propagate so callers index only successfully parsed operation results.
func runScriptInt64Pair(ctx context.Context, rdb *redis.Client, script *redis.Script, keys []string, args ...any) (int64, int64, error) {
	raw, err := script.Run(ctx, rdb, keys, args...).Result()
	if err != nil {
		return 0, 0, err
	}
	first, err := redisScriptInt64At(raw, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("parse script value 0: %w", err)
	}
	second, err := redisScriptInt64At(raw, 1)
	if err != nil {
		return 0, 0, fmt.Errorf("parse script value 1: %w", err)
	}
	return first, second, nil
}

// CleanupExpiredProviderSlots prunes one provider's expired ordinary members
// and adjusts key lifetime for the remainder. Successful pruning is followed
// by discovery reconciliation; errors from primary cleanup propagate.
func (c *concurrencyCache) CleanupExpiredProviderSlots(ctx context.Context, providerID int64) error {
	key := providerSlotKey(providerID)
	_, err := cleanupExpiredSlotsScript.Run(ctx, c.rdb, []string{key}, c.slotTTLSeconds).Result()
	if err == nil {
		// Read ordinary and waiting load after primary pruning so the discovery
		// score reflects what remains; index failures are logged while cleanup
		// stays completed.
		c.refreshProviderActiveIndex(ctx, providerID)
	}
	return err
}

// CleanupExpiredProviderSlotKeys reconciles bounded due candidates from
// provider discovery first, then user discovery. A provider-index failure
// stops this call before user work; unindexed primary keys remain outside
// these candidate queries.
func (c *concurrencyCache) CleanupExpiredProviderSlotKeys(ctx context.Context) error {
	if err := c.reconcileExpiredIndexCandidates(ctx, providerSlotIndex); err != nil {
		return err
	}
	return c.reconcileExpiredIndexCandidates(ctx, userSlotIndex)
}

// reconcileExpiredIndexCandidates reads a bounded set of discovery members
// whose scores are due and repairs their ordinary-load hints. Clock,
// index-query and load failures abort the call after any earlier primary
// pruning.
//
// Malformed identities and candidates with no positive load are selected for
// index removal. Loaded candidates receive expiry scores using their
// configured slot/wait lifetime; refresh and removal failures are logged so
// other candidates can continue. Primary counts and discovery mutation happen
// separately, so the result reflects an observation that concurrent
// acquisitions or releases can change.
func (c *concurrencyCache) reconcileExpiredIndexCandidates(ctx context.Context, spec slotIndexSpec) error {
	now, err := c.redisUnixSeconds(ctx)
	if err != nil {
		return err
	}
	members, err := c.rdb.ZRangeByScore(ctx, spec.indexKey, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   strconv.FormatInt(now, 10),
		Count: activeIndexCleanupBatchSize,
	}).Result()
	if err != nil {
		return fmt.Errorf("read expired index %s: %w", spec.indexKey, err)
	}

	loads, staleMembers, err := c.readIndexLoads(ctx, spec, members, now)
	if err != nil {
		return err
	}
	refreshed := make([]redis.Z, 0, len(loads))
	for _, load := range loads {
		if load.slotCount == 0 && load.waitCount <= 0 {
			// Select an idle discovery member for removal from this observed load;
			// concurrent activity may begin before the separate index mutation runs.
			staleMembers = append(staleMembers, load.member)
			continue
		}
		refreshed = append(refreshed, redis.Z{
			Score:  float64(now + int64(c.activeIndexTTL(load.slotCount, load.waitCount))),
			Member: load.member,
		})
	}
	// Publish refreshed discovery scores after reading all selected primary
	// loads; indexing errors are logged so stale-member removal can still run in
	// this pass.
	if len(refreshed) > 0 {
		if err := c.rdb.ZAdd(ctx, spec.indexKey, refreshed...).Err(); err != nil {
			logger.LegacyPrintf("repository.concurrency", "Warning: refresh %d active index members in %s failed: %v", len(refreshed), spec.indexKey, err)
		}
	}
	c.removeActiveIndexMembers(ctx, spec.indexKey, staleMembers)
	return nil
}

// CleanupStaleProcessSlots removes indexed ordinary members outside
// activeRequestPrefix and resets their wait counters. Empty prefixes skip
// startup cleanup; API-key statistics and Live sets are outside this
// operation.
//
// A successful first-startup prefix sweep writes a persistent marker before
// indexed provider and user cleanup. Every index member is considered,
// including expired scores, so crash leftovers remain discoverable after a
// long shutdown. Prefix matching treats other processes' ordinary members as
// removable, even when that process is still live; callers account for this
// startup scope when sharing Redis between instances.
func (c *concurrencyCache) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	if activeRequestPrefix == "" {
		return nil
	}
	if err := c.sweepLegacyWaitKeysOnce(ctx); err != nil {
		return err
	}
	now, err := c.redisUnixSeconds(ctx)
	if err != nil {
		return err
	}

	providerMembers, err := c.allIndexMembers(ctx, providerActiveIndexKey)
	if err != nil {
		return err
	}
	if err := c.cleanupStaleProcessSlotsForIndex(ctx, providerSlotIndex, providerMembers, activeRequestPrefix, now); err != nil {
		return err
	}

	userMembers, err := c.allIndexMembers(ctx, userActiveIndexKey)
	if err != nil {
		return err
	}
	return c.cleanupStaleProcessSlotsForIndex(ctx, userSlotIndex, userMembers, activeRequestPrefix, now)
}

// sweepLegacyWaitKeysOnce scans and deletes both wait-key prefixes when the
// successful-sweep marker is absent. An existing marker skips scanning; a
// failure stops the sweep and leaves its marker unwritten.
//
// The prefixes include indexed and unindexed counters because old keys can
// keep refreshing under traffic and cannot all be discovered through an
// index. Deletions complete in cursor batches before the persistent marker is
// written. Concurrent starts can both see an absent marker, and current wait
// activity inside those prefixes can be removed; this marker records
// completion rather than providing mutual exclusion.
func (c *concurrencyCache) sweepLegacyWaitKeysOnce(ctx context.Context) error {
	exists, err := c.rdb.Exists(ctx, legacyWaitSweepMarkerKey).Result()
	if err != nil {
		return fmt.Errorf("check legacy wait sweep marker: %w", err)
	}
	if exists > 0 {
		return nil
	}
	// Sweep both wait prefixes before recording completion so unindexed legacy
	// counters are reached even when no discovery entry identifies their owner.
	for _, pattern := range []string{providerWaitKeyPrefix + "*", waitQueueKeyPrefix + "*"} {
		var cursor uint64
		for {
			// SCAN's count is a cursor-work hint for the wait-prefix sweep.
			keys, next, err := c.rdb.Scan(ctx, cursor, pattern, 200).Result()
			if err != nil {
				return fmt.Errorf("scan legacy wait keys %s: %w", pattern, err)
			}
			if len(keys) > 0 {
				if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
					return fmt.Errorf("delete legacy wait keys: %w", err)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	// Write the persistent marker after every scan/delete batch succeeds so a
	// failed startup sweep can retry its undiscovered wait counters later.
	if err := c.rdb.Set(ctx, legacyWaitSweepMarkerKey, "1", 0).Err(); err != nil {
		return fmt.Errorf("set legacy wait sweep marker: %w", err)
	}
	return nil
}

// allIndexMembers returns all stored discovery members, including entries
// whose expiry scores are past. Startup cleanup needs that complete list to
// find ordinary process leftovers after a prolonged shutdown.
func (c *concurrencyCache) allIndexMembers(ctx context.Context, indexKey string) ([]string, error) {
	members, err := c.rdb.ZRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("read active index %s: %w", indexKey, err)
	}
	return members, nil
}

// cleanupStaleProcessSlotsForIndex processes supplied provider/user discovery
// members using current-prefix ordinary cleanup and wait reset. Invalid
// member identities are retained for later index removal, and primary
// operation errors stop the sequence.
//
// The script's remaining ordinary count determines whether each discovery
// member is rescheduled after its wait key is deleted. A failure can leave
// earlier slot removal or wait resets completed; discovery refresh/removal
// runs only after candidate processing reaches the end. Index maintenance
// errors are logged, and separate primary/index commands leave room for
// concurrent activity to change the observed state.
func (c *concurrencyCache) cleanupStaleProcessSlotsForIndex(
	ctx context.Context,
	spec slotIndexSpec,
	members []string,
	activeRequestPrefix string,
	now int64,
) error {
	staleMembers := make([]string, 0)
	refreshed := make([]redis.Z, 0)
	for _, member := range members {
		id, err := strconv.ParseInt(member, 10, 64)
		if err != nil || id <= 0 {
			staleMembers = append(staleMembers, member)
			continue
		}

		_, remaining, err := runScriptInt64Pair(ctx, c.rdb, startupCleanupSlotScript, []string{spec.slotKey(id)}, activeRequestPrefix, c.slotTTLSeconds)
		if err != nil {
			return fmt.Errorf("cleanup stale process slots %s: %w", spec.slotKey(id), err)
		}
		// Reset the wait counter after current-prefix slot cleanup so the script's
		// remaining ordinary count decides which startup discovery entries are
		// retained.
		if err := c.rdb.Del(ctx, spec.waitKey(id)).Err(); err != nil {
			return fmt.Errorf("delete stale wait key %s: %w", spec.waitKey(id), err)
		}
		if remaining > 0 {
			refreshed = append(refreshed, redis.Z{
				Score:  float64(now + int64(c.slotTTLSeconds)),
				Member: member,
			})
		} else {
			staleMembers = append(staleMembers, member)
		}
	}
	// Publish retained-prefix discovery scores after primary cleanup reaches the
	// end; logged index failures do not restore removed ordinary members or
	// reset wait counters.
	if len(refreshed) > 0 {
		if err := c.rdb.ZAdd(ctx, spec.indexKey, refreshed...).Err(); err != nil {
			logger.LegacyPrintf("repository.concurrency", "Warning: refresh %d active index members in %s failed: %v", len(refreshed), spec.indexKey, err)
		}
	}
	c.removeActiveIndexMembers(ctx, spec.indexKey, staleMembers)
	return nil
}
