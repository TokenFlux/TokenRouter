// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

type EmailOAuthIdentityInput = identity.EmailOAuthIdentityInput

// LoginOrRegisterVerifiedEmailOAuth 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) LoginOrRegisterVerifiedEmailOAuth(ctx context.Context, input EmailOAuthIdentityInput) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().LoginOrRegisterVerifiedEmailOAuth(ctx, input)
	return pair, UserFromIdentity(u), err
}

// LoginOrRegisterVerifiedEmailOAuthWithInvitation 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) LoginOrRegisterVerifiedEmailOAuthWithInvitation(
	ctx context.Context,
	input EmailOAuthIdentityInput,
	invitationCode string,
	affiliateCode string,
) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().LoginOrRegisterVerifiedEmailOAuthWithInvitation(ctx, input, invitationCode, affiliateCode)
	return pair, UserFromIdentity(u), err
}

// LoginOrRegisterVerifiedEmailOAuthWithSignupCodes 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(
	ctx context.Context,
	input EmailOAuthIdentityInput,
	invitationCode string,
	affiliateCode string,
	promoCode string,
) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(ctx, input, invitationCode, affiliateCode, promoCode)
	return pair, UserFromIdentity(u), err
}
