package rediscache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const stickySessionPrefix = "sticky_session:"
const stickySessionOwnerPrefix = "sticky_session_owner:"

// StickyCache 复用原 Redis 实例和键，普通粘性与显式归属分别保持原 TTL。
type StickyCache struct{ rdb *redis.Client }

func NewStickyCache(rdb *redis.Client) *StickyCache { return &StickyCache{rdb: rdb} }
func (c *StickyCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Get(ctx, key).Int64()
}
func (c *StickyCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Set(ctx, key, accountID, ttl).Err()
}
func (c *StickyCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Expire(ctx, key, ttl).Err()
}
func (c *StickyCache) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Del(ctx, key).Err()
}
func (c *StickyCache) SetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error) {
	key := buildSessionOwnerKey(userID, source, sessionHash)
	return c.rdb.SetNX(ctx, key, groupID, ttl).Result()
}
func (c *StickyCache) GetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string) (int64, error) {
	key := buildSessionOwnerKey(userID, source, sessionHash)
	return c.rdb.Get(ctx, key).Int64()
}
func (c *StickyCache) RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, sessionHash string, ttl time.Duration) error {
	key := buildSessionOwnerKey(userID, source, sessionHash)
	return c.rdb.Expire(ctx, key, ttl).Err()
}
func buildSessionKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", stickySessionPrefix, groupID, sessionHash)
}
func buildSessionOwnerKey(userID int64, source, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s:%s", stickySessionOwnerPrefix, userID, source, sessionHash)
}
