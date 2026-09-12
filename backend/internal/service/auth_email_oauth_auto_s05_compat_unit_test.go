//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package service

import (
	context "context"
)

// createEmailOAuthUser 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) createEmailOAuthUser(ctx context.Context, email, username, providerType, invitationCode, affiliateCode string) (*User, error) {
	u, err := s.identityCore().AuthCreateEmailOAuthUser(ctx, email, username, providerType, invitationCode, affiliateCode)
	return UserFromIdentity(u), err
}
