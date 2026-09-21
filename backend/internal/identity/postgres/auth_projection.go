// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
)

type AuthRepository struct{ State *AuthState }

func (r *AuthRepository) HasDatabase() bool { return r != nil && r.State.HasDatabase() }
func (s *AuthRepository) ApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	return s.State.ApplyProviderDefaultSettingsOnFirstBind(ctx, userID, providerType)
}
func (s *AuthRepository) AuthApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	return s.State.AuthApplyProviderDefaultSettingsOnFirstBind(ctx, userID, providerType)
}
func (s *AuthRepository) AuthCreateRegisteredUser(ctx context.Context, user *identitycore.User, artifacts *identitycore.AuthRegistrationArtifacts) error {
	return s.State.AuthCreateRegisteredUser(ctx, user, artifacts)
}
func (s *AuthRepository) AuthEnsureEmailAuthIdentity(ctx context.Context, user *identitycore.User, source string) (*identitycore.AuthIdentity, bool) {
	v, created := s.State.AuthEnsureEmailAuthIdentity(ctx, user, source)
	return AuthIdentityFromEntity(v), created
}
func (s *AuthRepository) AuthEnsureEmailOAuthIdentity(ctx context.Context, userID int64, input identitycore.EmailOAuthIdentityInput) error {
	return s.State.AuthEnsureEmailOAuthIdentity(ctx, userID, input)
}
func (s *AuthRepository) AuthFindEmailOAuthIdentityOwner(ctx context.Context, providerType, providerKey, providerSubject string) (*identitycore.User, error) {
	return s.State.AuthFindEmailOAuthIdentityOwner(ctx, providerType, providerKey, providerSubject)
}
func (s *AuthRepository) AuthHasProviderGrantRecord(
	ctx context.Context,
	userID int64,
	providerType string,
	grantReason string,
) (bool, error) {
	return s.State.AuthHasProviderGrantRecord(ctx, userID, providerType, grantReason)
}
func (s *AuthRepository) AuthLoadOAuthRegistrationInvitation(ctx context.Context, invitationCode string) (*identitycore.RedeemCode, error) {
	return s.State.AuthLoadOAuthRegistrationInvitation(ctx, invitationCode)
}
func (s *AuthRepository) AuthRestoreOAuthRegistrationInvitation(ctx context.Context, invitationCode string, userID int64) error {
	return s.State.AuthRestoreOAuthRegistrationInvitation(ctx, invitationCode, userID)
}
func (s *AuthRepository) AuthRunFailOpenDBStep(ctx context.Context, savepointName string, fn func(context.Context) error) error {
	return s.State.AuthRunFailOpenDBStep(ctx, savepointName, fn)
}
func (s *AuthRepository) AuthShouldApplyEmailFirstBindDefaults(
	ctx context.Context,
	userID int64,
	identity *identitycore.AuthIdentity,
	created bool,
) bool {
	return s.State.AuthShouldApplyEmailFirstBindDefaults(ctx, userID, AuthIdentityToEntity(identity), created)
}
func (s *AuthRepository) AuthSnapshotPlatformQuotaDefaults(ctx context.Context, userID int64, plan *identitycore.AuthSignupGrantPlan) error {
	return s.State.AuthSnapshotPlatformQuotaDefaults(ctx, userID, plan)
}
func (s *AuthRepository) AuthTouchUserLogin(ctx context.Context, userID int64) {
	s.State.AuthTouchUserLogin(ctx, userID)
}
func (s *AuthRepository) AuthUpdateBoundEmailIdentityTx(
	ctx context.Context,
	currentUser *identitycore.User,
	email string,
	registrationNormalizedEmail string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	return s.State.AuthUpdateBoundEmailIdentityTx(ctx, currentUser, email, registrationNormalizedEmail, hashedPassword, applyFirstBindDefaults)
}
func (s *AuthRepository) AuthUpdateOAuthRegistrationInvitation(ctx context.Context, code *identitycore.RedeemCode) error {
	return s.State.AuthUpdateOAuthRegistrationInvitation(ctx, code)
}
func (s *AuthRepository) AuthUpdateOAuthSignupSource(ctx context.Context, userID int64, signupSource string) {
	s.State.AuthUpdateOAuthSignupSource(ctx, userID, signupSource)
}
func (s *AuthRepository) AuthUpdateUserSignupSource(ctx context.Context, userID int64, signupSource string) {
	s.State.AuthUpdateUserSignupSource(ctx, userID, signupSource)
}
func (s *AuthRepository) AuthUseOAuthRegistrationInvitation(ctx context.Context, invitationID, userID int64) error {
	return s.State.AuthUseOAuthRegistrationInvitation(ctx, invitationID, userID)
}
func AuthIdentityFromEntity(v *dbent.AuthIdentity) *identitycore.AuthIdentity {
	if v == nil {
		return nil
	}
	return &identitycore.AuthIdentity{ID: v.ID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, UserID: v.UserID, ProviderType: v.ProviderType, ProviderKey: v.ProviderKey, ProviderSubject: v.ProviderSubject, VerifiedAt: v.VerifiedAt, Issuer: v.Issuer, Metadata: v.Metadata}
}
func AuthIdentityToEntity(v *identitycore.AuthIdentity) *dbent.AuthIdentity {
	if v == nil {
		return nil
	}
	return &dbent.AuthIdentity{ID: v.ID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, UserID: v.UserID, ProviderType: v.ProviderType, ProviderKey: v.ProviderKey, ProviderSubject: v.ProviderSubject, VerifiedAt: v.VerifiedAt, Issuer: v.Issuer, Metadata: v.Metadata}
}
