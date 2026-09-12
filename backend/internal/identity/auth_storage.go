// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
)

// AuthStorage 保护已存在的事务和 savepoint 范围；参与实现不发布成功副作用。
type AuthStorage interface {
	HasDatabase() bool
	ApplyProviderDefaultSettingsOnFirstBind(
		ctx context.Context,
		userID int64,
		providerType string,
	) error
	AuthApplyProviderDefaultSettingsOnFirstBind(
		ctx context.Context,
		userID int64,
		providerType string,
	) error
	AuthCreateRegisteredUser(ctx context.Context, user *User, artifacts *AuthRegistrationArtifacts) error
	AuthEnsureEmailAuthIdentity(ctx context.Context, user *User, source string) (*AuthIdentity, bool)
	AuthEnsureEmailOAuthIdentity(ctx context.Context, userID int64, input EmailOAuthIdentityInput) error
	AuthFindEmailOAuthIdentityOwner(ctx context.Context, providerType, providerKey, providerSubject string) (*User, error)
	AuthHasProviderGrantRecord(
		ctx context.Context,
		userID int64,
		providerType string,
		grantReason string,
	) (bool, error)
	AuthLoadOAuthRegistrationInvitation(ctx context.Context, invitationCode string) (*RedeemCode, error)
	AuthRestoreOAuthRegistrationInvitation(ctx context.Context, invitationCode string, userID int64) error
	AuthRunFailOpenDBStep(ctx context.Context, savepointName string, fn func(context.Context) error) error
	AuthShouldApplyEmailFirstBindDefaults(
		ctx context.Context,
		userID int64,
		identity *AuthIdentity,
		created bool,
	) bool
	AuthSnapshotPlatformQuotaDefaults(ctx context.Context, userID int64, plan *AuthSignupGrantPlan) error
	AuthTouchUserLogin(ctx context.Context, userID int64)
	AuthUpdateBoundEmailIdentityTx(
		ctx context.Context,
		currentUser *User,
		email string,
		registrationNormalizedEmail string,
		hashedPassword string,
		applyFirstBindDefaults bool,
	) error
	AuthUpdateOAuthRegistrationInvitation(ctx context.Context, code *RedeemCode) error
	AuthUpdateOAuthSignupSource(ctx context.Context, userID int64, signupSource string)
	AuthUpdateUserSignupSource(ctx context.Context, userID int64, signupSource string)
	AuthUseOAuthRegistrationInvitation(ctx context.Context, invitationID, userID int64) error
}

func (s *AuthService) ApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	return s.Storage.ApplyProviderDefaultSettingsOnFirstBind(ctx, userID, providerType)
}
func (s *AuthService) AuthApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	return s.Storage.AuthApplyProviderDefaultSettingsOnFirstBind(ctx, userID, providerType)
}
func (s *AuthService) AuthCreateRegisteredUser(ctx context.Context, user *User, artifacts *AuthRegistrationArtifacts) error {
	return s.Storage.AuthCreateRegisteredUser(ctx, user, artifacts)
}
func (s *AuthService) AuthEnsureEmailAuthIdentity(ctx context.Context, user *User, source string) (*AuthIdentity, bool) {
	return s.Storage.AuthEnsureEmailAuthIdentity(ctx, user, source)
}
func (s *AuthService) AuthEnsureEmailOAuthIdentity(ctx context.Context, userID int64, input EmailOAuthIdentityInput) error {
	return s.Storage.AuthEnsureEmailOAuthIdentity(ctx, userID, input)
}
func (s *AuthService) AuthFindEmailOAuthIdentityOwner(ctx context.Context, providerType, providerKey, providerSubject string) (*User, error) {
	return s.Storage.AuthFindEmailOAuthIdentityOwner(ctx, providerType, providerKey, providerSubject)
}
func (s *AuthService) AuthHasProviderGrantRecord(
	ctx context.Context,
	userID int64,
	providerType string,
	grantReason string,
) (bool, error) {
	return s.Storage.AuthHasProviderGrantRecord(ctx, userID, providerType, grantReason)
}
func (s *AuthService) AuthLoadOAuthRegistrationInvitation(ctx context.Context, invitationCode string) (*RedeemCode, error) {
	return s.Storage.AuthLoadOAuthRegistrationInvitation(ctx, invitationCode)
}
func (s *AuthService) AuthRestoreOAuthRegistrationInvitation(ctx context.Context, invitationCode string, userID int64) error {
	return s.Storage.AuthRestoreOAuthRegistrationInvitation(ctx, invitationCode, userID)
}
func (s *AuthService) AuthRunFailOpenDBStep(ctx context.Context, savepointName string, fn func(context.Context) error) error {
	return s.Storage.AuthRunFailOpenDBStep(ctx, savepointName, fn)
}
func (s *AuthService) AuthShouldApplyEmailFirstBindDefaults(
	ctx context.Context,
	userID int64,
	identity *AuthIdentity,
	created bool,
) bool {
	return s.Storage.AuthShouldApplyEmailFirstBindDefaults(ctx, userID, identity, created)
}
func (s *AuthService) AuthSnapshotPlatformQuotaDefaults(ctx context.Context, userID int64, plan *AuthSignupGrantPlan) error {
	return s.Storage.AuthSnapshotPlatformQuotaDefaults(ctx, userID, plan)
}
func (s *AuthService) AuthTouchUserLogin(ctx context.Context, userID int64) {
	s.Storage.AuthTouchUserLogin(ctx, userID)
}
func (s *AuthService) AuthUpdateBoundEmailIdentityTx(
	ctx context.Context,
	currentUser *User,
	email string,
	registrationNormalizedEmail string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	return s.Storage.AuthUpdateBoundEmailIdentityTx(ctx, currentUser, email, registrationNormalizedEmail, hashedPassword, applyFirstBindDefaults)
}
func (s *AuthService) AuthUpdateOAuthRegistrationInvitation(ctx context.Context, code *RedeemCode) error {
	return s.Storage.AuthUpdateOAuthRegistrationInvitation(ctx, code)
}
func (s *AuthService) AuthUpdateOAuthSignupSource(ctx context.Context, userID int64, signupSource string) {
	s.Storage.AuthUpdateOAuthSignupSource(ctx, userID, signupSource)
}
func (s *AuthService) AuthUpdateUserSignupSource(ctx context.Context, userID int64, signupSource string) {
	s.Storage.AuthUpdateUserSignupSource(ctx, userID, signupSource)
}
func (s *AuthService) AuthUseOAuthRegistrationInvitation(ctx context.Context, invitationID, userID int64) error {
	return s.Storage.AuthUseOAuthRegistrationInvitation(ctx, invitationID, userID)
}
