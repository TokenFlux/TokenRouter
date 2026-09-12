// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
)

// ApplyProviderDefaultSettingsOnFirstBind 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) ApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	return s.identityCore().ApplyProviderDefaultSettingsOnFirstBind(ctx, userID, providerType)
}
