package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// cache.go stores authentication snapshots and creation counters in Redis
// and publishes cache invalidations.
// Shared authentication entries store JSON snapshots under hashed credential
// identities, while creation-failure counters are scoped by user ID. The Redis
// adapter retains a caller-owned client and uses a dedicated Pub/Sub channel to
// deliver local invalidation callbacks until cancellation or subscriber
// closure.
//
// Creation-failure updates queue an increment followed by expiration in an
// ordinary pipeline. Either command can take effect when the other fails, so
// the adapter reports the execution error rather than promising rollback.
// Subscription readiness follows the Redis acknowledgement; every exit closes
// the subscription before its caller can retry or finish shutdown.

// Redis names separate user creation counters, hashed authentication entries
// and invalidation messages; creation increments refresh their configured counter lifetime.
const (
	ApiKeyRateLimitKeyPrefix   = "apikey:ratelimit:"
	ApiKeyRateLimitDuration    = 24 * time.Hour
	ApiKeyAuthCachePrefix      = "apikey:auth:"
	AuthCacheInvalidateChannel = "auth:cache:invalidate"
)

// ApiKeyCache uses a shared Redis client for credential snapshots and request
// counters.
type ApiKeyCache struct {
	// rdb is supplied and shared by assembly. The adapter retains it, and
	// its caller closes the client after active operations and subscribers finish.
	rdb *redis.Client
}

// ApiKeyRateLimitKey returns the user-scoped Redis creation-failure counter
// name.
func ApiKeyRateLimitKey(userID int64) string {
	return fmt.Sprintf("%s%d", ApiKeyRateLimitKeyPrefix, userID)
}

// ApiKeyAuthCacheKey prefixes the supplied hashed credential identity for
// shared authentication storage.
func ApiKeyAuthCacheKey(key string) string {
	return fmt.Sprintf("%s%s", ApiKeyAuthCachePrefix, key)
}

// NewAPIKeyCache constructs the authentication adapter around the supplied
// Redis client. The caller retains client ownership and closes it after service
// subscribers and operations stop; the returned adapter also implements
// RequestLimitCache.
func NewAPIKeyCache(rdb *redis.Client) keycore.APIKeyCache {
	return &ApiKeyCache{rdb: rdb}
}

// GetCreateAttemptCount reads the user's creation-failure counter, treating a
// missing Redis key as zero. Other errors retain their identity for the caller,
// which decides whether failed counter reads block creation.
func (c *ApiKeyCache) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	key := ApiKeyRateLimitKey(userID)
	count, err := c.rdb.Get(ctx, key).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return count, err
}

// IncrementCreateAttemptCount pipelines one creation-failure increment and
// expiry refresh for userID. Successful expiry resets the 24-hour lifetime;
// either command can fail after the other takes effect because an ordinary
// Pipeline groups transport and supplies no transaction rollback.
func (c *ApiKeyCache) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	key := ApiKeyRateLimitKey(userID)
	// Queue expiry after the increment so existing counters receive a fresh
	// retention deadline; ordinary pipeline execution can leave either command's partial effects.
	pipe := c.rdb.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ApiKeyRateLimitDuration)
	_, err := pipe.Exec(ctx)
	return err
}

// DeleteCreateAttemptCount removes the user's recorded creation-failure counter
// from Redis storage.
func (c *ApiKeyCache) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	key := ApiKeyRateLimitKey(userID)
	return c.rdb.Del(ctx, key).Err()
}

// IncrementDailyUsage increments the exact counter name supplied by its service
// caller.
func (c *ApiKeyCache) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return c.rdb.Incr(ctx, apiKey).Err()
}

// SetDailyUsageExpiry sets ttl on the supplied daily counter name and returns
// Redis failures. The service constructs date-based names and chooses their
// lifetime; this adapter forwards both inputs directly.
func (c *ApiKeyCache) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, apiKey, ttl).Err()
}

// GetAuthCache reads and decodes JSON under the supplied hashed credential
// identity. Missing storage and decode failures return errors; snapshot version
// validation belongs to KeyApplyAuthCacheEntry after a successful read.
func (c *ApiKeyCache) GetAuthCache(ctx context.Context, key string) (*keycore.APIKeyAuthCacheEntry, error) {
	val, err := c.rdb.Get(ctx, ApiKeyAuthCacheKey(key)).Bytes()
	if err != nil {
		return nil, err
	}
	var entry keycore.APIKeyAuthCacheEntry
	if err := json.Unmarshal(val, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// SetAuthCache serializes entry as JSON and writes it using the supplied ttl
// and hashed identity. Nil entries do nothing; encoding and Redis failures are
// returned, and service callers decide how to handle optional publication
// failure.
func (c *ApiKeyCache) SetAuthCache(ctx context.Context, key string, entry *keycore.APIKeyAuthCacheEntry, ttl time.Duration) error {
	if entry == nil {
		return nil
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, ApiKeyAuthCacheKey(key), payload, ttl).Err()
}

// DeleteAuthCache removes the shared snapshot named by the supplied hashed
// identity.
func (c *ApiKeyCache) DeleteAuthCache(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, ApiKeyAuthCacheKey(key)).Err()
}

// PublishAuthCacheInvalidation publishes the supplied hashed identity for other
// instances' local-cache callbacks. Successful publication reports Redis
// acceptance; subscribers, missed messages and later recovery remain the
// invalidation protocol's responsibility.
func (c *ApiKeyCache) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return c.rdb.Publish(ctx, AuthCacheInvalidateChannel, cacheKey).Err()
}

// SubscribeAuthCacheInvalidation waits for Redis acknowledgement, reports
// readiness and consumes invalidation messages until its context or channel
// ends. Callers supply a usable context and callback, and callbacks run
// synchronously in this loop.
//
// The subscription is closed on every return path. A failed acknowledgement
// returns before readiness; channel closure reports an error, while context
// cancellation returns ctx.Err. go-redis owns channel buffering and transport
// recovery, so a successful acknowledgement does not guarantee every later
// publication is delivered. The service wrapper retries returned failures
// before cache shutdown.
func (c *ApiKeyCache) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	pubsub := c.rdb.Subscribe(ctx, AuthCacheInvalidateChannel)

	// Readiness follows Redis acknowledgement so startup observers do not
	// interpret local PubSub allocation as an established invalidation subscription.
	_, err := pubsub.Receive(ctx)
	// Failed startup still owns a PubSub resource. Close it before the
	// wrapper retries so attempts do not accumulate unused subscription connections.
	if err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe to auth cache invalidation: %w", err)
	}

	// This loop owns the subscription lifetime. Close it after cancellation
	// or channel failure before the service can join and close local cache storage.
	defer func() {
		if err := pubsub.Close(); err != nil {
			logging.LegacyPrintf("cache.apikey_auth", "Warning: failed to close auth cache invalidation pubsub: %v", err)
		}
	}()
	keycore.NotifyAuthCacheSubscriptionReady(ctx)

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return errors.New("auth cache invalidation pubsub channel closed")
			}
			if msg != nil {
				handler(msg.Payload)
			}
		}
	}
}
