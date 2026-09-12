// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
)

// SendPendingOAuthVerifyCode 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) SendPendingOAuthVerifyCode(ctx context.Context, email string, locale ...string) (*SendVerifyCodeResult, error) {
	return s.identityCore().SendPendingOAuthVerifyCode(ctx, email, locale...)
}

// VerifyOAuthEmailCode 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyOAuthEmailCode(ctx context.Context, email, verifyCode string) error {
	return s.identityCore().VerifyOAuthEmailCode(ctx, email, verifyCode)
}

// RegisterOAuthEmailAccount 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RegisterOAuthEmailAccount(
	ctx context.Context,
	email string,
	password string,
	verifyCode string,
	invitationCode string,
	signupSource string,
) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().RegisterOAuthEmailAccount(ctx, email, password, verifyCode, invitationCode, signupSource)
	return pair, UserFromIdentity(u), err
}

// RegisterVerifiedOAuthEmailAccount 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RegisterVerifiedOAuthEmailAccount(
	ctx context.Context,
	email string,
	password string,
	invitationCode string,
	signupSource string,
) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().RegisterVerifiedOAuthEmailAccount(ctx, email, password, invitationCode, signupSource)
	return pair, UserFromIdentity(u), err
}

// FinalizeOAuthEmailAccount 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) FinalizeOAuthEmailAccount(
	ctx context.Context,
	user *User,
	invitationCode string,
	signupSource string,
	affiliateCode string,
) error {
	userProjection := IdentityUser(user)
	result := s.identityCore().FinalizeOAuthEmailAccount(ctx, userProjection, invitationCode, signupSource, affiliateCode)
	ApplyIdentityUser(user, userProjection)
	return result
}

// RollbackOAuthEmailAccountCreation 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RollbackOAuthEmailAccountCreation(ctx context.Context, userID int64, invitationCode string) error {
	return s.identityCore().RollbackOAuthEmailAccountCreation(ctx, userID, invitationCode)
}

// ValidatePasswordCredentials 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) ValidatePasswordCredentials(ctx context.Context, email, password string) (*User, error) {
	u, err := s.identityCore().ValidatePasswordCredentials(ctx, email, password)
	return UserFromIdentity(u), err
}

// RecordSuccessfulLogin 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RecordSuccessfulLogin(ctx context.Context, userID int64) {
	s.identityCore().RecordSuccessfulLogin(ctx, userID)
}
