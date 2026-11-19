package rediscache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// concurrency_live_leases.go reserves, renews, and releases short-lived Live
// session leases across provider, user, and API-key sorted sets. Live sessions
// share provider and user admission capacity with ordinary request slots while
// supporting a one-slot replacement allowance when an admitted request hands
// its reservation over to a sixty-second renewable Live lease.
//
// Acquisition and renewal run inside Lua scripts under Redis TIME so pruning
// expired Live scores, counting ordinary and Live members, and refreshing
// provider, user, and API-key memberships execute atomically. Renewal requires
// the lease identifier to remain present in all three sorted sets after pruning
// before extending any score, and release removes all three entries in one
// transaction.

// Live lease Lua scripts coordinate multi-key pruning, capacity checks against
// ordinary sets, and all-or-nothing renewal across provider, user, and API-key
// Live sorted sets.
var (
	// acquireLiveLeaseScript prunes three Live sets and checks provider/user
	// capacity including ordinary ZCARD values. A provider-side duplicate
	// returns success before renewal; replacement permits one extra member, and
	// new leases write all three Live sets.
	acquireLiveLeaseScript = redis.NewScript(`
		redis.replicate_commands()
		local providerRegular = KEYS[1]
		local providerLive = KEYS[2]
		local userRegular = KEYS[3]
		local userLive = KEYS[4]
		local apiLive = KEYS[5]
		local providerMax = tonumber(ARGV[1])
		local userMax = tonumber(ARGV[2])
		local ttl = tonumber(ARGV[3])
		local leaseID = ARGV[4]
		local replacing = tonumber(ARGV[5])
		local now = tonumber(redis.call('TIME')[1])
		local liveExpireBefore = now - ttl
		redis.call('ZREMRANGEBYSCORE', providerLive, '-inf', liveExpireBefore)
		redis.call('ZREMRANGEBYSCORE', userLive, '-inf', liveExpireBefore)
		redis.call('ZREMRANGEBYSCORE', apiLive, '-inf', liveExpireBefore)
		if redis.call('ZSCORE', providerLive, leaseID) ~= false then
			return 1
		end
		local providerCount = redis.call('ZCARD', providerRegular) + redis.call('ZCARD', providerLive)
		local userCount = redis.call('ZCARD', userRegular) + redis.call('ZCARD', userLive)
		local allowance = 0
		if replacing == 1 then allowance = 1 end
		if providerMax > 0 and providerCount >= providerMax + allowance then return 0 end
		if userMax > 0 and userCount >= userMax + allowance then return 0 end
		redis.call('ZADD', providerLive, now, leaseID)
		redis.call('ZADD', userLive, now, leaseID)
		redis.call('ZADD', apiLive, now, leaseID)
		redis.call('EXPIRE', providerLive, ttl)
		redis.call('EXPIRE', userLive, ttl)
		redis.call('EXPIRE', apiLive, ttl)
		return 1
	`)

	// refreshLiveLeaseScript visits provider, user and API-key Live sets in order,
	// pruning and checking each membership. A missing member stops before later
	// sets are visited; renewal starts after all three checks succeed.
	refreshLiveLeaseScript = redis.NewScript(`
		redis.replicate_commands()
		local ttl = tonumber(ARGV[1])
		local leaseID = ARGV[2]
		local now = tonumber(redis.call('TIME')[1])
		local expireBefore = now - ttl
		for _, key in ipairs(KEYS) do
			redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
			if redis.call('ZSCORE', key, leaseID) == false then return 0 end
		end
		for _, key in ipairs(KEYS) do
			redis.call('ZADD', key, now, leaseID)
			redis.call('EXPIRE', key, ttl)
		end
		return 1
	`)
)

// AcquireLiveLease reserves provider, user and API-key Live memberships with
// the short lease lifetime. Invalid clients, IDs or empty lease identity
// return false; nonpositive provider/user limits bypass their capacity gates.
//
// Replacement permits one extra member while the caller still owns regular
// slots, which the caller releases after successful transfer. The script
// prunes Live scores but counts ordinary sets as stored. A provider-side
// duplicate returns success without refreshing the three memberships; callers
// use RefreshLiveLease for renewal and release all three memberships after
// the session ends.
func (c *concurrencyCache) AcquireLiveLease(
	ctx context.Context,
	providerID int64,
	providerMax int,
	userID int64,
	userMax int,
	apiKeyID int64,
	leaseID string,
	replacingRegularSlots bool,
) (bool, error) {
	if c == nil || c.rdb == nil || providerID <= 0 || userID <= 0 || apiKeyID <= 0 || leaseID == "" {
		return false, nil
	}
	replacing := 0
	if replacingRegularSlots {
		replacing = 1
	}
	result, err := acquireLiveLeaseScript.Run(ctx, c.rdb, []string{
		providerSlotKey(providerID),
		liveProviderSlotKey(providerID),
		userSlotKey(userID),
		liveUserSlotKey(userID),
		liveAPIKeySlotKey(apiKeyID),
	}, providerMax, userMax, liveLeaseTTLSeconds, leaseID, replacing).Int()
	return result == 1, err
}

// RefreshLiveLease checks memberships in provider, user and API-key order,
// pruning each visited Live set before checking. Use the acquisition IDs for
// renewal; nil caches, nil clients or empty lease IDs return false.
//
// A missing member stops the checks and reports lease loss, leaving later
// sets unvisited. Scores and TTLs renew after every membership check succeeds.
func (c *concurrencyCache) RefreshLiveLease(ctx context.Context, providerID, userID, apiKeyID int64, leaseID string) (bool, error) {
	if c == nil || c.rdb == nil || leaseID == "" {
		return false, nil
	}
	result, err := refreshLiveLeaseScript.Run(ctx, c.rdb, []string{
		liveProviderSlotKey(providerID),
		liveUserSlotKey(userID),
		liveAPIKeySlotKey(apiKeyID),
	}, liveLeaseTTLSeconds, leaseID).Int()
	return result == 1, err
}

// ReleaseLiveLease removes this identity from the provider, user and API-key
// Live sets in one transaction. Nil caches or empty IDs are ignored; repeated
// removal succeeds for already absent memberships.
func (c *concurrencyCache) ReleaseLiveLease(ctx context.Context, providerID, userID, apiKeyID int64, leaseID string) error {
	if c == nil || c.rdb == nil || leaseID == "" {
		return nil
	}
	// Queue removal from all three lease sets before EXEC so a successful
	// release applies the provider, user and API-key membership cleanup
	// together.
	pipe := c.rdb.TxPipeline()
	pipe.ZRem(ctx, liveProviderSlotKey(providerID), leaseID)
	pipe.ZRem(ctx, liveUserSlotKey(userID), leaseID)
	pipe.ZRem(ctx, liveAPIKeySlotKey(apiKeyID), leaseID)
	_, err := pipe.Exec(ctx)
	return err
}
