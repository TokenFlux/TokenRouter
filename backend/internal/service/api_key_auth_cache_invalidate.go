// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
)

// InvalidateAuthCacheByKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	s.APIKeyService.InvalidateAuthCacheByKey(KeyRequestContext(ctx), key)
}

// InvalidateAuthCacheByUserID 委托 Key 模块的唯一实现。
func (s *APIKeyService) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	s.APIKeyService.InvalidateAuthCacheByUserID(KeyRequestContext(ctx), userID)
}

// InvalidateAuthCacheByGroupID 委托 Key 模块的唯一实现。
func (s *APIKeyService) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	s.APIKeyService.InvalidateAuthCacheByGroupID(KeyRequestContext(ctx), groupID)
}
