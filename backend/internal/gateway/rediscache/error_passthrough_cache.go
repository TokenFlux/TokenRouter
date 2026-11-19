package rediscache

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// error_passthrough_cache.go caches error rules in Redis and local memory and
// manages update subscriptions.
// Error-rule instances share a Redis JSON value and update notifications while
// each cache adapter keeps its own local rule copy. Set acknowledges the
// shared write before publishing local data, whereas Invalidate clears the
// local value before attempting shared deletion.
//
// Local copies protect callers from mutating cached rules, but the Redis TTL
// applies to the shared blob rather than expiring a populated local slice.
// Notifications clear that local slice before synchronously invoking the
// refresh callback. A lifetime admission lock permits only one subscriber and
// rejects registration after stopping, with the task counted before shutdown
// can wait. The owning rule service serializes its database/cache publication;
// this adapter does not add a cross-instance version check.

// The shared JSON key and notification channel locate error-rule publication;
// the TTL governs Redis retention while local invalidation follows update events.
const (
	errorPassthroughCacheKey  = "error_passthrough_rules"         // shared error-rule JSON cache key
	errorPassthroughPubSubKey = "error_passthrough_rules_updated" // cross-instance rule update notification channel
	errorPassthroughCacheTTL  = 24 * time.Hour                    // shared Redis rule value lifetime
)

// errorPassthroughCache retains Redis rules, local copies, and one managed
// subscriber task.
type errorPassthroughCache struct {
	// subscriptionMu protects subscriptionCancel and subscriptionStopped while
	// registration adds subscriptionWG before releasing admission. Stop therefore
	// cannot miss an accepted task when waiting for its callback to finish.
	subscriptionMu      sync.Mutex         // subscriber lifetime admission lock
	subscriptionCancel  context.CancelFunc // retained active subscriber cancellation function
	subscriptionStopped bool               // permanent subscriber admission stop flag
	subscriptionWG      sync.WaitGroup     // admitted subscriber task completion count
	rdb                 *redis.Client      // shared application Redis command client
	// localCache and localMu publish and copy independent rule snapshots. A nonnil
	// slice is reused until invalidated; the Redis TTL is not checked for local hits.
	localCache []*errorpolicy.ErrorPassthroughRule // owned local error-rule snapshot slice
	localMu    sync.RWMutex                        // local error-rule snapshot access lock
}

// NewErrorPassthroughCache binds a usable Redis client and leaves local rule
// and subscriber state empty. Construction starts no subscriber; the owning
// service later calls SubscribeUpdates with its lifecycle context.
func NewErrorPassthroughCache(rdb *redis.Client) errorpolicy.ErrorPassthroughCache {
	return &errorPassthroughCache{
		rdb: rdb,
	}
}

// Get returns an independent cached rule list with true, preferring a
// populated local slice before Redis. Missing shared data, command failures,
// and invalid JSON return false; non-miss errors are logged. Redis null is a
// valid cached nil list.
func (c *errorPassthroughCache) Get(ctx context.Context) ([]*errorpolicy.ErrorPassthroughRule, bool) {
	// A populated local snapshot can outlive the Redis blob's TTL; return a copy
	// under the read lock until notification or explicit invalidation clears this
	// slice.
	c.localMu.RLock()
	if c.localCache != nil {
		rules := errorpolicy.CloneRules(c.localCache)
		c.localMu.RUnlock()
		return rules, true
	}
	c.localMu.RUnlock()

	// Read the shared value after a local miss. Command failures become a cache
	// miss so the owning service can obtain authoritative rules through its
	// database path.
	data, err := c.rdb.Get(ctx, errorPassthroughCacheKey).Bytes()
	if err != nil {
		if err != redis.Nil {
			logging.LegacyPrintf("cache.error_passthrough", "[ErrorPassthroughCache] Failed to get from Redis: %v", err)
		}
		return nil, false
	}

	var rules []*errorpolicy.ErrorPassthroughRule
	if err := json.Unmarshal(data, &rules); err != nil {
		logging.LegacyPrintf("cache.error_passthrough", "[ErrorPassthroughCache] Failed to unmarshal rules: %v", err)
		return nil, false
	}

	// Publish a cloned list under the local lock so modifying the returned or
	// submitted rules cannot alter the adapter's retained nested fields and
	// condition slices.
	c.localMu.Lock()
	c.localCache = errorpolicy.CloneRules(rules)
	c.localMu.Unlock()

	return rules, true
}

// Set clones supplied rules, writes their JSON to Redis, and publishes an
// independent local copy after acknowledgement. Callers retain ownership of
// mutable inputs, and a Redis or encoding error leaves this method's local
// publication skipped.
//
// The local copy is published under its mutex, while the shared value has a
// twenty-four-hour TTL. The owning error service holds its update coordinator
// around these cache calls and database reads so a prior service refresh
// cannot overwrite a newer administrative publication. Direct concurrent cache
// callers receive local synchronization but no version or generation ordering
// from this adapter.
func (c *errorPassthroughCache) Set(ctx context.Context, rules []*errorpolicy.ErrorPassthroughRule) error {
	rules = errorpolicy.CloneRules(rules)
	data, err := json.Marshal(rules)
	if err != nil {
		return err
	}

	if err := c.rdb.Set(ctx, errorPassthroughCacheKey, data, errorPassthroughCacheTTL).Err(); err != nil {
		return err
	}

	// Publish a cloned list under the local lock so modifying the returned or
	// submitted rules cannot alter the adapter's retained nested fields and
	// condition slices.
	c.localMu.Lock()
	c.localCache = errorpolicy.CloneRules(rules)
	c.localMu.Unlock()

	return nil
}

// Invalidate clears local rule data before deleting the shared Redis key and
// returns that deletion error. Local clearing still takes effect when deletion
// fails, but another shared read can then encounter the retained old Redis
// value.
func (c *errorPassthroughCache) Invalidate(ctx context.Context) error {
	// Clear local data before the shared deletion, so this instance drops its
	// snapshot even when the Redis command fails and the shared blob remains
	// available.
	c.localMu.Lock()
	c.localCache = nil
	c.localMu.Unlock()

	// Return shared deletion failure after local invalidation; the service
	// observes that error while continuing its database refresh and
	// cross-instance notification attempts.
	return c.rdb.Del(ctx, errorPassthroughCacheKey).Err()
}

// NotifyUpdate publishes the refresh signal on the shared error-rule channel.
// It returns the Redis publication error; successful publication reports
// command acceptance and provides no acknowledgement that other instances
// completed their callbacks.
func (c *errorPassthroughCache) NotifyUpdate(ctx context.Context) error {
	return c.rdb.Publish(ctx, errorPassthroughPubSubKey, "refresh").Err()
}

// SubscribeUpdates admits one subscriber with a usable lifecycle context and
// synchronous refresh handler. Later calls are rejected after the first
// registration or stopping, even when the earlier subscriber has already
// exited.
//
// The admission mutex installs cancellation and counts the task before
// releasing registration, preventing StopSubscription from missing that
// subscriber. Each message clears local rules before the callback reloads, and
// context cancellation closes subscription cleanup after an active callback
// returns. The handler remains responsible for finishing or responding to its
// own service cancellation so shutdown can wait for it.
func (c *errorPassthroughCache) SubscribeUpdates(ctx context.Context, handler func()) {
	subscriberCtx, cancel := context.WithCancel(ctx)
	c.subscriptionMu.Lock()
	if c.subscriptionCancel != nil || c.subscriptionStopped {
		c.subscriptionMu.Unlock()
		cancel()
		return
	}
	c.subscriptionCancel = cancel
	c.subscriptionWG.Add(1)
	c.subscriptionMu.Unlock()
	// Count and install cancellation before launching subscription work, so
	// stopping can cancel an admitted task even when its goroutine has not
	// reached Redis yet.
	go func() {
		defer c.subscriptionWG.Done()
		sub := c.rdb.Subscribe(subscriberCtx, errorPassthroughPubSubKey)
		// Close the subscription after cancellation or channel termination,
		// discarding transport-close errors once this subscriber is already leaving
		// its managed task and callback loop.
		defer func() { _ = sub.Close() }()

		ch := sub.Channel()
		for {
			select {
			case <-subscriberCtx.Done():
				return
			case msg := <-ch:
				if msg == nil {
					return
				}
				// Remove the local snapshot before invoking refresh, so the synchronous
				// callback sees shared or authoritative data rather than immediately
				// reusing this adapter's old rule slice.
				c.localMu.Lock()
				c.localCache = nil
				c.localMu.Unlock()

				handler()
			}
		}
	}()
}

// StopSubscription closes subscriber admission, cancels its context, and waits
// for the admitted task including any synchronous callback. It has no caller
// timeout; the owning service supplies a bounded wait around this cleanup.
//
// Cancellation is captured under the admission mutex, which is released before
// waiting. Registration increments the same wait count while protected by that
// mutex, so no accepted subscriber can appear after the stopped flag is set.
// Repeated stop calls retain closed admission and can wait again; a callback
// that fails to return also keeps this wait active.
func (c *errorPassthroughCache) StopSubscription() {
	c.subscriptionMu.Lock()
	c.subscriptionStopped = true
	cancel := c.subscriptionCancel
	c.subscriptionMu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.subscriptionWG.Wait()
}
