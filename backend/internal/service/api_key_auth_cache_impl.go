// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

const apiKeyAuthSnapshotVersion = apikey.KeyApiKeyAuthSnapshotVersion

// StartAuthCacheInvalidationSubscriber 委托 Key 模块的唯一实现。
func (s *APIKeyService) StartAuthCacheInvalidationSubscriber(ctx context.Context) {
	s.APIKeyService.StartAuthCacheInvalidationSubscriber(KeyRequestContext(ctx))
}

type AuthCacheInvalidationSubscriberHealth = apikey.AuthCacheInvalidationSubscriberHealth

// AuthCacheInvalidationSubscriberHealth 委托 Key 模块的唯一实现。
func (s *APIKeyService) AuthCacheInvalidationSubscriberHealth() AuthCacheInvalidationSubscriberHealth {
	return s.APIKeyService.AuthCacheInvalidationSubscriberHealth()
}

// StopAuthCacheInvalidationSubscriber 委托 Key 模块的唯一实现。
func (s *APIKeyService) StopAuthCacheInvalidationSubscriber() {
	s.APIKeyService.StopAuthCacheInvalidationSubscriber()
}

// applyAuthCacheEntry 委托 Key 模块的唯一实现。
func (s *APIKeyService) applyAuthCacheEntry(key string, entry *APIKeyAuthCacheEntry) (*APIKey, bool, error) {
	v, hit, err := s.KeyApplyAuthCacheEntry(key, entry)
	return APIKeyFromView(v), hit, err
}

// snapshotFromAPIKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) snapshotFromAPIKey(ctx context.Context, apiKey *APIKey) *APIKeyAuthSnapshot {
	apiKeyView := APIKeyView(apiKey)
	result0 := s.KeySnapshotFromAPIKey(KeyRequestContext(ctx), apiKeyView)
	ApplyAPIKeyView(apiKey, apiKeyView)
	return result0
}

// snapshotToAPIKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) snapshotToAPIKey(key string, snapshot *APIKeyAuthSnapshot) *APIKey {
	v := s.KeySnapshotToAPIKey(key, snapshot)
	return APIKeyFromView(v)
}
