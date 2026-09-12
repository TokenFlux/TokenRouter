package apikey

import "context"

// PublishedAuthCacheInvalidator 保留团队写入后的原缓存失效顺序，由 Key 模块拥有键格式。
type PublishedAuthCacheInvalidator struct{ Cache APIKeyCache }

func (p PublishedAuthCacheInvalidator) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	cacheKey := AuthCacheKey(key)
	_ = p.Cache.DeleteAuthCache(ctx, cacheKey)
	_ = p.Cache.PublishAuthCacheInvalidation(ctx, cacheKey)
}
