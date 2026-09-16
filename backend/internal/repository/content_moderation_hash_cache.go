// 审核 hash 缓存唯一实现在所属 Adapter。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/moderation/rediscache"
	"github.com/redis/go-redis/v9"
)

func NewContentModerationHashCache(r *redis.Client) moderation.ContentModerationHashCache {
	return rediscache.NewContentModerationHashCache(r)
}
