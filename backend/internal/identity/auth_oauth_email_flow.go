// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	errors "errors"
	fmt "fmt"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	slog "log/slog"
	mail "net/mail"
	strings "strings"
	time "time"
)

func AuthNormalizeOAuthSignupSource(signupSource string) string {
	signupSource = strings.TrimSpace(strings.ToLower(signupSource))
	switch signupSource {
	case "", "email":
		return "email"
	case "linuxdo", "wechat", "oidc", "github", "google", "dingtalk":
		return signupSource
	default:
		return "email"
	}
}

// SendPendingOAuthVerifyCode sends a local verification code for pending OAuth
// account-creation flows without relying on the public registration gate.
func (s *AuthService) SendPendingOAuthVerifyCode(ctx context.Context, email string, locale ...string) (*SendVerifyCodeResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, ErrEmailVerifyRequired
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, ErrEmailVerifyRequired
	}
	if IsReservedEmail(email) {
		return nil, ErrEmailReserved
	}
	if s == nil || s.Email == nil {
		return nil, ErrServiceUnavailable
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return nil, err
	}

	siteName := "Sub2API"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}
	if err := s.Email.SendVerifyCode(ctx, email, siteName, FirstEmailLocale(locale)); err != nil {
		return nil, err
	}
	return &SendVerifyCodeResult{
		Countdown: int(VerifyCodeCooldown / time.Second),
	}, nil
}

func (s *AuthService) AuthValidateOAuthRegistrationInvitation(ctx context.Context, invitationCode string) (*RedeemCode, error) {
	if s == nil || s.Settings == nil || !s.Settings.IsInvitationCodeEnabled(ctx) {
		return nil, nil
	}
	if s.Redeem == nil && !s.HasDatabase() {
		return nil, ErrServiceUnavailable
	}

	invitationCode = strings.TrimSpace(invitationCode)
	if invitationCode == "" {
		return nil, ErrInvitationCodeRequired
	}

	redeemCode, err := s.AuthLoadOAuthRegistrationInvitation(ctx, invitationCode)
	if err != nil {
		return nil, ErrInvitationCodeInvalid
	}
	if redeemCode.Type != RedeemTypeInvitation || !redeemCode.CanUse() {
		return nil, ErrInvitationCodeInvalid
	}
	return redeemCode, nil
}

// VerifyOAuthEmailCode verifies the locally entered email verification code for
// third-party signup and binding flows. This is intentionally independent from
// the global registration email verification toggle.
func (s *AuthService) VerifyOAuthEmailCode(ctx context.Context, email, verifyCode string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	verifyCode = strings.TrimSpace(verifyCode)

	if email == "" {
		return ErrEmailVerifyRequired
	}
	if verifyCode == "" {
		return ErrEmailVerifyRequired
	}
	if s == nil || s.Email == nil {
		return ErrServiceUnavailable
	}
	return s.Email.VerifyCode(ctx, email, verifyCode)
}

// RegisterOAuthEmailAccount creates a local account from a third-party first
// login after the user has verified a local email address.
func (s *AuthService) RegisterOAuthEmailAccount(
	ctx context.Context,
	email string,
	password string,
	verifyCode string,
	invitationCode string,
	signupSource string,
) (*TokenPair, *User, error) {
	if s == nil {
		return nil, nil, ErrServiceUnavailable
	}
	if s.Settings == nil || (!s.Settings.IsRegistrationEnabled(ctx) && !s.AuthCanBypassRegistrationDisabledForOAuth(ctx, signupSource)) {
		return nil, nil, ErrRegDisabled
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if IsReservedEmail(email) {
		return nil, nil, ErrEmailReserved
	}
	if err := s.VerifyOAuthEmailCode(ctx, email, verifyCode); err != nil {
		slog.Error("oauth email register: verify code failed", "email", email, "error", err.Error())
		return nil, nil, err
	}

	if _, err := s.AuthValidateOAuthRegistrationInvitation(ctx, invitationCode); err != nil {
		slog.Error("oauth email register: invitation failed", "email", email, "error", err.Error())
		return nil, nil, err
	}

	existsEmail, err := s.Users.ExistsByEmail(ctx, email)
	if err != nil {
		slog.Error("oauth email register: ExistsByEmail failed", "email", email, "error", err.Error())
		return nil, nil, ErrServiceUnavailable
	}
	if existsEmail {
		return nil, nil, ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		slog.Error("oauth email register: policy rejected", "email", email, "error", err.Error())
		return nil, nil, err
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, nil, fmt.Errorf("hash password: %w", err)
	}

	signupSource = AuthNormalizeOAuthSignupSource(signupSource)
	grantPlan := s.AuthResolveSignupGrantPlan(ctx, signupSource)

	user := &User{
		Email:        email,
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      grantPlan.Balance,
		Concurrency:  grantPlan.Concurrency,
		Status:       StatusActive,
		SignupSource: signupSource,
	}
	if err := s.AuthCreateOAuthEmailAccountUser(ctx, user); err != nil {
		switch {
		case errors.Is(err, ErrEmailExists):
			return nil, nil, ErrEmailExists
		case errors.Is(err, ErrEmailDomainRegistrationLimit):
			return nil, nil, ErrEmailDomainRegistrationLimit
		default:
			slog.Error("oauth email register: userRepo.Create failed", "email", email, "signup_source", signupSource, "error", err.Error())
			return nil, nil, ErrServiceUnavailable
		}
	}

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		_ = s.RollbackOAuthEmailAccountCreation(ctx, user.ID, "")
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

// RegisterVerifiedOAuthEmailAccount 为已由 OAuth 提供方验证过邮箱的新用户创建本地账号。
func (s *AuthService) RegisterVerifiedOAuthEmailAccount(
	ctx context.Context,
	email string,
	password string,
	invitationCode string,
	signupSource string,
) (*TokenPair, *User, error) {
	if s == nil {
		return nil, nil, ErrServiceUnavailable
	}
	if s.Settings == nil || (!s.Settings.IsRegistrationEnabled(ctx) && !s.AuthCanBypassRegistrationDisabledForOAuth(ctx, signupSource)) {
		return nil, nil, ErrRegDisabled
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || len(email) > 255 {
		return nil, nil, ErrEmailVerifyRequired
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, nil, ErrEmailVerifyRequired
	}
	if IsReservedEmail(email) {
		return nil, nil, ErrEmailReserved
	}
	if strings.TrimSpace(password) == "" {
		return nil, nil, infraerrors.BadRequest("PASSWORD_REQUIRED", "password is required")
	}
	if _, err := s.AuthValidateOAuthRegistrationInvitation(ctx, invitationCode); err != nil {
		return nil, nil, err
	}

	existsEmail, err := s.Users.ExistsByEmail(ctx, email)
	if err != nil {
		return nil, nil, ErrServiceUnavailable
	}
	if existsEmail {
		return nil, nil, ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return nil, nil, err
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, nil, fmt.Errorf("hash password: %w", err)
	}

	signupSource = AuthNormalizeOAuthSignupSource(signupSource)
	grantPlan := s.AuthResolveSignupGrantPlan(ctx, signupSource)
	var defaultRPMLimit int
	if s.Settings != nil {
		defaultRPMLimit = s.Settings.GetDefaultUserRPMLimit(ctx)
	}
	user := &User{
		Email:        email,
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      grantPlan.Balance,
		Concurrency:  grantPlan.Concurrency,
		RPMLimit:     defaultRPMLimit,
		Status:       StatusActive,
		SignupSource: signupSource,
	}
	if err := s.AuthCreateOAuthEmailAccountUser(ctx, user); err != nil {
		switch {
		case errors.Is(err, ErrEmailExists):
			return nil, nil, ErrEmailExists
		case errors.Is(err, ErrEmailDomainRegistrationLimit):
			return nil, nil, ErrEmailDomainRegistrationLimit
		default:
			return nil, nil, ErrServiceUnavailable
		}
	}

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		_ = s.RollbackOAuthEmailAccountCreation(ctx, user.ID, "")
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

func (s *AuthService) AuthCreateOAuthEmailAccountUser(ctx context.Context, user *User) error {
	if s == nil || user == nil {
		return ErrServiceUnavailable
	}

	// 这些 OAuth 注册路径延后消费邀请码；这里只复用注册路径的邮箱归一化保护，不提前核销邀请码。
	err := s.AuthCreateRegisteredUser(ctx, user, &AuthRegistrationArtifacts{EnforceEmailDomainQuota: true})
	if err != nil && user.ID > 0 && !errors.Is(err, ErrEmailExists) {
		_ = s.RollbackOAuthEmailAccountCreation(ctx, user.ID, "")
	}
	return err
}

// FinalizeOAuthEmailAccount applies invitation usage and normal signup bootstrap
// only after the pending OAuth flow has fully reached its last reversible step.
func (s *AuthService) FinalizeOAuthEmailAccount(
	ctx context.Context,
	user *User,
	invitationCode string,
	signupSource string,
	affiliateCode string,
) error {
	if s == nil || user == nil || user.ID <= 0 {
		return ErrServiceUnavailable
	}

	signupSource = AuthNormalizeOAuthSignupSource(signupSource)
	InvitationRedeemCode, err := s.AuthValidateOAuthRegistrationInvitation(ctx, invitationCode)
	if err != nil {
		return err
	}
	if InvitationRedeemCode != nil {
		if err := s.AuthUseOAuthRegistrationInvitation(ctx, InvitationRedeemCode.ID, user.ID); err != nil {
			return ErrInvitationCodeInvalid
		}
	}

	s.AuthUpdateOAuthSignupSource(ctx, user.ID, signupSource)
	grantPlan := s.AuthResolveSignupGrantPlan(ctx, signupSource)
	s.AuthAssignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
	// 平台限额快照失败不阻断 OAuth 邮箱补全。
	_ = s.AuthSnapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
	s.AuthBindRegistrationAffiliate(ctx, user.ID, affiliateCode)
	return nil
}

// RollbackOAuthEmailAccountCreation removes a partially-created local account
// and restores any invitation code already consumed by that account.
func (s *AuthService) RollbackOAuthEmailAccountCreation(ctx context.Context, userID int64, invitationCode string) error {
	if s == nil || s.Users == nil || userID <= 0 {
		return ErrServiceUnavailable
	}
	if err := s.AuthRestoreOAuthRegistrationInvitation(ctx, invitationCode, userID); err != nil {
		return err
	}
	if err := s.Users.Delete(ctx, userID); err != nil {
		return fmt.Errorf("delete created oauth user: %w", err)
	}
	return nil
}

func AuthOauthEmailFlowStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// ValidatePasswordCredentials checks the local password without completing the
// login flow. This is used by pending third-party account adoption flows before
// the external identity has been bound.
func (s *AuthService) ValidatePasswordCredentials(ctx context.Context, email, password string) (*User, error) {
	if s == nil {
		return nil, ErrServiceUnavailable
	}

	user, err := s.Users.GetByEmail(ctx, strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, ErrServiceUnavailable
	}
	if !user.IsActive() {
		return nil, ErrUserNotActive
	}
	if !s.CheckPassword(password, user.PasswordHash) {
		return nil, ErrInvalidCredentials
	}
	return user, nil
}

// RecordSuccessfulLogin updates last-login activity after a non-standard login
// flow finishes with a real session.
func (s *AuthService) RecordSuccessfulLogin(ctx context.Context, userID int64) {
	if s != nil && s.Users != nil && userID > 0 {
		user, err := s.Users.GetByID(ctx, userID)
		if err == nil && user != nil && !IsReservedEmail(user.Email) {
			s.AuthBackfillEmailIdentityOnSuccessfulLogin(ctx, user)
		}
	}
	s.AuthTouchUserLogin(ctx, userID)
}
