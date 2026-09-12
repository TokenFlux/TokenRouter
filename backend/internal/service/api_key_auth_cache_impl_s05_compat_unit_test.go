//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package service

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

// authCacheKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) authCacheKey(key string) string {
	return s.KeyAuthCacheKey(key)
}

// hydrateTeamAPIKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) hydrateTeamAPIKey(ctx context.Context, apiKey *APIKey, err error) (*APIKey, error) {
	apiKeyView := APIKeyView(apiKey)
	v, e := s.KeyHydrateTeamAPIKey(KeyRequestContext(ctx), apiKeyView, err)
	ApplyAPIKeyView(apiKey, apiKeyView)
	return APIKeyFromView(v), e
}

// authGroupSnapshotFromGroup 委托 Key 模块的唯一实现。
func authGroupSnapshotFromGroup(group *Group) *APIKeyAuthGroupSnapshot {
	groupView := APIKeyGroupView(group)
	return apikey.KeyAuthGroupSnapshotFromGroup(groupView)
}

// groupFromAuthSnapshot 委托 Key 模块的唯一实现。
func groupFromAuthSnapshot(snapshot *APIKeyAuthGroupSnapshot) *Group {
	v := apikey.KeyGroupFromAuthSnapshot(snapshot)
	return GroupFromAPIKeyView(v)
}
