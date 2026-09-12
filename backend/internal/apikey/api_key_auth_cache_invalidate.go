// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	context "context"
)

// InvalidateAuthCacheByKey 清除指定 API Key 的认证缓存
func (s *APIKeyService) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	if s == nil || !s.operations.enter() {
		return
	}
	defer s.operations.leave()

	if key == "" {
		return
	}
	cacheKey := s.KeyAuthCacheKey(key)
	s.KeyDeleteAuthCache(ctx, cacheKey)
}

// InvalidateAuthCacheByUserID 清除用户相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	if s == nil || !s.operations.enter() {
		return
	}
	defer s.operations.leave()

	if userID <= 0 {
		return
	}
	keys, err := s.apiKeyRepo.ListKeysByUserID(ctx, userID)
	if err != nil {
		return
	}
	s.KeyDeleteAuthCacheByKeys(ctx, keys)
}

// InvalidateAuthCacheByGroupID 清除分组相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	if s == nil || !s.operations.enter() {
		return
	}
	defer s.operations.leave()

	if groupID <= 0 {
		return
	}
	keys, err := s.apiKeyRepo.ListKeysByGroupID(ctx, groupID)
	if err != nil {
		return
	}
	s.KeyDeleteAuthCacheByKeys(ctx, keys)
}

func (s *APIKeyService) KeyDeleteAuthCacheByKeys(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		s.KeyDeleteAuthCache(ctx, s.KeyAuthCacheKey(key))
	}
}
