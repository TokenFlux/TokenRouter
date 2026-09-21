// 本文件维护 rediscache 的所属能力；兼容入口复用唯一实现。
package rediscache

import (
	context "context"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	log "log"
	time "time"

	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	redis "github.com/redis/go-redis/v9"
)

const (
	ApiKeyRateLimitKeyPrefix   = "apikey:ratelimit:"
	ApiKeyRateLimitDuration    = 24 * time.Hour
	ApiKeyAuthCachePrefix      = "apikey:auth:"
	AuthCacheInvalidateChannel = "auth:cache:invalidate"
)

// ApiKeyRateLimitKey generates the Redis key for API key creation rate limiting.
func ApiKeyRateLimitKey(userID int64) string {
	return fmt.Sprintf("%s%d", ApiKeyRateLimitKeyPrefix, userID)
}

func ApiKeyAuthCacheKey(key string) string {
	return fmt.Sprintf("%s%s", ApiKeyAuthCachePrefix, key)
}

type ApiKeyCache struct {
	rdb *redis.Client
}

func NewAPIKeyCache(rdb *redis.Client) keycore.APIKeyCache {
	return &ApiKeyCache{rdb: rdb}
}

func (c *ApiKeyCache) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	key := ApiKeyRateLimitKey(userID)
	count, err := c.rdb.Get(ctx, key).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return count, err
}

func (c *ApiKeyCache) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	key := ApiKeyRateLimitKey(userID)
	pipe := c.rdb.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ApiKeyRateLimitDuration)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *ApiKeyCache) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	key := ApiKeyRateLimitKey(userID)
	return c.rdb.Del(ctx, key).Err()
}

func (c *ApiKeyCache) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return c.rdb.Incr(ctx, apiKey).Err()
}

func (c *ApiKeyCache) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, apiKey, ttl).Err()
}

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

func (c *ApiKeyCache) DeleteAuthCache(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, ApiKeyAuthCacheKey(key)).Err()
}

// PublishAuthCacheInvalidation publishes a cache invalidation message to all instances
func (c *ApiKeyCache) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return c.rdb.Publish(ctx, AuthCacheInvalidateChannel, cacheKey).Err()
}

// SubscribeAuthCacheInvalidation subscribes to cache invalidation messages
func (c *ApiKeyCache) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	pubsub := c.rdb.Subscribe(ctx, AuthCacheInvalidateChannel)

	// Verify subscription is working
	_, err := pubsub.Receive(ctx)
	if err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe to auth cache invalidation: %w", err)
	}

	defer func() {
		if err := pubsub.Close(); err != nil {
			log.Printf("Warning: failed to close auth cache invalidation pubsub: %v", err)
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
