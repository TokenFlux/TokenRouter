package rediscache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// concurrency_ws_ingress.go admits, renews, and releases OpenAI WebSocket
// ingress connection leases per API key. Client WebSocket connections outlive
// individual request turns, so they use dedicated sorted sets with sixty-second
// inactivity windows that stay independent of ordinary provider and user
// request-slot lifetimes.
//
// Admission and renewal scripts prune inactive connection members under Redis
// TIME before counting active connections or refreshing an existing lease
// identifier. Re-admitting an already active connection refreshes its score
// even when the set is at capacity, while renewal checks membership after
// pruning so an expired connection reports lost ownership instead of silently
// recreating a dropped lease.

// Ingress lease Lua scripts prune inactive connection members under Redis TIME
// before admitting a connection below the API-key limit or renewing an active
// lease.
var (
	// acquireOpenAIWSIngressLeaseScript prunes expired connection members and
	// admits a new ID below its API-key limit. Existing IDs refresh even when
	// the current set is full.
	acquireOpenAIWSIngressLeaseScript = redis.NewScript(`
		redis.replicate_commands()
		local key = KEYS[1]
		local maxConnections = tonumber(ARGV[1])
		local ttl = tonumber(ARGV[2])
		local leaseID = ARGV[3]
		local now = tonumber(redis.call('TIME')[1])
		local expireBefore = now - ttl
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		if redis.call('ZSCORE', key, leaseID) ~= false then
			redis.call('ZADD', key, now, leaseID)
			redis.call('EXPIRE', key, ttl)
			return 1
		end
		if redis.call('ZCARD', key) < maxConnections then
			redis.call('ZADD', key, now, leaseID)
			redis.call('EXPIRE', key, ttl)
			return 1
		end
		return 0
	`)

	// refreshOpenAIWSIngressLeaseScript prunes old connection scores before
	// renewing a present lease. A missing or pruned member returns zero so the
	// caller can recognize lost ownership.
	refreshOpenAIWSIngressLeaseScript = redis.NewScript(`
		redis.replicate_commands()
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local leaseID = ARGV[2]
		local now = tonumber(redis.call('TIME')[1])
		local expireBefore = now - ttl
		redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
		if redis.call('ZSCORE', key, leaseID) == false then
			return 0
		end
		redis.call('ZADD', key, now, leaseID)
		redis.call('EXPIRE', key, ttl)
		return 1
	`)
)

// AcquireOpenAIWSIngressLease admits or refreshes an API-key connection
// member with a sixty-second lifetime. Unusable clients, nonpositive
// identities/limits or empty IDs return false; successful callers renew and
// release that lease ID.
func (c *concurrencyCache) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int, leaseID string) (bool, error) {
	if c == nil || c.rdb == nil || apiKeyID <= 0 || maxConnections <= 0 || leaseID == "" {
		return false, nil
	}
	result, err := acquireOpenAIWSIngressLeaseScript.Run(
		ctx,
		c.rdb,
		[]string{openAIWSIngressLeaseKey(apiKeyID)},
		maxConnections,
		openAIWSIngressLeaseTTLSeconds,
		leaseID,
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// RefreshOpenAIWSIngressLease renews a present ingress member after pruning
// expired scores. Invalid cache/identity inputs return false, and a missing
// member reports false so the owner can recognize lost connection capacity.
func (c *concurrencyCache) RefreshOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) (bool, error) {
	if c == nil || c.rdb == nil || apiKeyID <= 0 || leaseID == "" {
		return false, nil
	}
	result, err := refreshOpenAIWSIngressLeaseScript.Run(
		ctx,
		c.rdb,
		[]string{openAIWSIngressLeaseKey(apiKeyID)},
		openAIWSIngressLeaseTTLSeconds,
		leaseID,
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// ReleaseOpenAIWSIngressLease removes the supplied API-key connection member.
// Invalid cache/identity inputs are ignored, and repeating a completed
// removal is harmless when that member is already absent.
func (c *concurrencyCache) ReleaseOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) error {
	if c == nil || c.rdb == nil || apiKeyID <= 0 || leaseID == "" {
		return nil
	}
	return c.rdb.ZRem(ctx, openAIWSIngressLeaseKey(apiKeyID), leaseID).Err()
}
