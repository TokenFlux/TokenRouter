// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
)

// BindEmailIdentity 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) BindEmailIdentity(
	ctx context.Context,
	userID int64,
	email string,
	verifyCode string,
	password string,
) (*User, error) {
	u, err := s.identityCore().BindEmailIdentity(ctx, userID, email, verifyCode, password)
	return UserFromIdentity(u), err
}

// SendEmailIdentityBindCode 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) SendEmailIdentityBindCode(ctx context.Context, userID int64, email string, locale ...string) error {
	return s.identityCore().SendEmailIdentityBindCode(ctx, userID, email, locale...)
}
