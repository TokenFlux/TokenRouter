//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package service

import (
	context "context"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

const maxTokenLength = identity.MaxTokenLength

type signupGrantPlan = identity.AuthSignupGrantPlan

// canBypassRegistrationDisabledForOAuth 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) canBypassRegistrationDisabledForOAuth(ctx context.Context, signupSource string) bool {
	return s.identityCore().AuthCanBypassRegistrationDisabledForOAuth(ctx, signupSource)
}

const pendingOAuthPurpose = identity.PendingOAuthPurpose

type pendingOAuthClaims = identity.PendingOAuthClaims

type registrationArtifacts = identity.AuthRegistrationArtifacts

// createRegisteredUser 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) createRegisteredUser(ctx context.Context, user *User, artifacts *registrationArtifacts) error {
	userProjection := IdentityUser(user)
	result := s.identityCore().AuthCreateRegisteredUser(ctx, userProjection, artifacts)
	ApplyIdentityUser(user, userProjection)
	return result
}

// resolveSignupGrantPlan 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) resolveSignupGrantPlan(ctx context.Context, signupSource string) signupGrantPlan {
	return s.identityCore().AuthResolveSignupGrantPlan(ctx, signupSource)
}

// validateRegistrationEmailQuota 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) validateRegistrationEmailQuota(ctx context.Context, email string) error {
	return s.identityCore().AuthValidateRegistrationEmailQuota(ctx, email)
}

// snapshotPlatformQuotaDefaults 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) snapshotPlatformQuotaDefaults(ctx context.Context, userID int64, plan *signupGrantPlan) error {
	return s.identityCore().AuthSnapshotPlatformQuotaDefaults(ctx, userID, plan)
}
