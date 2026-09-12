// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	errors "errors"
	fmt "fmt"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	mail "net/mail"
	strings "strings"
)

type AuthNormalizedEmailBindingConflictChecker interface {
	ExistsByNormalizedEmailExcluding(ctx context.Context, normalizedEmail string, excludedUserID int64) (bool, error)
}

// AuthEmailIdentityAliasGuardRepository 提供换绑主邮箱所需的事务内原子写入能力。
// 通过可选接口接入，保留无数据库测试桩的兼容性。
type AuthEmailIdentityAliasGuardRepository interface {
	UpdateEmailWithAliasGuard(ctx context.Context, userID int64, email string, passwordHash string) error
}

// BindEmailIdentity verifies and binds a local email/password identity to the
// current user, or replaces the existing bound primary email.
func (s *AuthService) BindEmailIdentity(
	ctx context.Context,
	userID int64,
	email string,
	verifyCode string,
	password string,
) (*User, error) {
	if s == nil {
		return nil, ErrServiceUnavailable
	}

	normalizedEmail, err := AuthNormalizeEmailForIdentityBinding(email)
	if err != nil {
		return nil, err
	}
	if IsReservedEmail(normalizedEmail) {
		return nil, ErrEmailReserved
	}
	if strings.TrimSpace(password) == "" {
		return nil, ErrPasswordRequired
	}
	currentUser, err := s.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.AuthEnsureUserEmailChangeAllowed(ctx, currentUser, normalizedEmail); err != nil {
		return nil, err
	}
	if err := s.VerifyOAuthEmailCode(ctx, normalizedEmail, verifyCode); err != nil {
		return nil, err
	}
	if err := s.AuthValidateRegistrationEmailPolicy(ctx, normalizedEmail); err != nil {
		return nil, err
	}
	firstRealEmailBind := !AuthHasBindableEmailIdentitySubject(currentUser.Email)
	if firstRealEmailBind && len(password) < 6 {
		return nil, infraerrors.BadRequest("PASSWORD_TOO_SHORT", "password must be at least 6 characters")
	}
	if !firstRealEmailBind && !s.CheckPassword(password, currentUser.PasswordHash) {
		return nil, ErrPasswordIncorrect
	}

	registrationNormalizedEmail := s.AuthNormalizeRegistrationEmailForBinding(ctx, normalizedEmail)
	if err := s.AuthEnsureEmailBindingTargetAvailable(ctx, currentUser, normalizedEmail, registrationNormalizedEmail); err != nil {
		return nil, err
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	if s.HasDatabase() {
		if err := s.AuthUpdateBoundEmailIdentityTx(ctx, currentUser, normalizedEmail, registrationNormalizedEmail, hashedPassword, firstRealEmailBind); err != nil {
			return nil, err
		}
		s.AuthRevokeEmailIdentitySessions(ctx, userID)
		return currentUser, nil
	}

	currentUser.Email = normalizedEmail
	currentUser.PasswordHash = hashedPassword
	fields := UserUpdateFields{Email: true, PasswordHash: true}
	updateUser := s.Users.Update
	if registrationNormalizedEmail != "" {
		// 开启邮箱归一化后，绑定/换绑主邮箱也要复用同一套唯一性保护。
		updateUser = func(updateCtx context.Context, updateUser *User, updateFields UserUpdateFields) error {
			return s.Users.UpdateWithNormalizedEmailGuard(updateCtx, updateUser, registrationNormalizedEmail, updateFields)
		}
	}
	if err := updateUser(ctx, currentUser, fields); err != nil {
		if errors.Is(err, ErrEmailExists) {
			return nil, ErrEmailExists
		}
		return nil, ErrServiceUnavailable
	}

	if firstRealEmailBind {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, userID, "email"); err != nil {
			return nil, fmt.Errorf("apply email first bind defaults: %w", err)
		}
	}

	s.AuthRevokeEmailIdentitySessions(ctx, userID)
	return currentUser, nil
}

// SendEmailIdentityBindCode sends a verification code for authenticated email binding flows.
func (s *AuthService) SendEmailIdentityBindCode(ctx context.Context, userID int64, email string, locale ...string) error {
	if s == nil {
		return ErrServiceUnavailable
	}

	normalizedEmail, err := AuthNormalizeEmailForIdentityBinding(email)
	if err != nil {
		return err
	}
	if IsReservedEmail(normalizedEmail) {
		return ErrEmailReserved
	}
	currentUser, err := s.Users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrUserNotFound
		}
		return ErrServiceUnavailable
	}
	if err := s.AuthEnsureUserEmailChangeAllowed(ctx, currentUser, normalizedEmail); err != nil {
		return err
	}
	if err := s.AuthValidateRegistrationEmailPolicy(ctx, normalizedEmail); err != nil {
		return err
	}
	if s.Email == nil {
		return ErrServiceUnavailable
	}
	registrationNormalizedEmail := s.AuthNormalizeRegistrationEmailForBinding(ctx, normalizedEmail)
	if err := s.AuthEnsureEmailBindingTargetAvailable(ctx, currentUser, normalizedEmail, registrationNormalizedEmail); err != nil {
		return err
	}

	siteName := "Sub2API"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}
	return s.Email.SendVerifyCode(ctx, normalizedEmail, siteName, FirstEmailLocale(locale))
}

// AuthEnsureUserEmailChangeAllowed 只限制真实邮箱发生变化；首次绑定或验证现有邮箱仍保持可用。
func (s *AuthService) AuthEnsureUserEmailChangeAllowed(ctx context.Context, currentUser *User, targetEmail string) error {
	if currentUser == nil {
		return ErrUserNotFound
	}
	if !AuthHasBindableEmailIdentitySubject(currentUser.Email) || strings.EqualFold(strings.TrimSpace(currentUser.Email), targetEmail) {
		return nil
	}
	if s.Settings == nil || !s.Settings.IsUserEmailChangeEnabled(ctx) {
		return ErrEmailChangeDisabled
	}
	return nil
}

func AuthNormalizeEmailForIdentityBinding(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || len(normalized) > 255 {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(normalized); err != nil {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	return normalized, nil
}

func AuthHasBindableEmailIdentitySubject(email string) bool {
	normalized := strings.ToLower(strings.TrimSpace(email))
	return normalized != "" && !IsReservedEmail(normalized)
}

func (s *AuthService) AuthNormalizeRegistrationEmailForBinding(ctx context.Context, email string) string {
	if s == nil || s.Settings == nil || !s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
		return ""
	}
	return NormalizeRegistrationEmailAddress(email)
}

func (s *AuthService) AuthEnsureEmailBindingTargetAvailable(
	ctx context.Context,
	currentUser *User,
	email string,
	registrationNormalizedEmail string,
) error {
	existingUser, err := s.Users.GetByEmail(ctx, email)
	switch {
	case err == nil:
		if existingUser != nil && (currentUser == nil || existingUser.ID != currentUser.ID) {
			return ErrEmailExists
		}
	case err != nil && !errors.Is(err, ErrUserNotFound):
		return ErrServiceUnavailable
	}

	if err := s.AuthEnsureEmailAliasTargetAvailable(ctx, currentUser, email); err != nil {
		return err
	}

	if registrationNormalizedEmail == "" {
		return nil
	}

	exists, err := s.AuthHasNormalizedEmailBindingConflict(ctx, currentUser, registrationNormalizedEmail)
	if err != nil {
		return ErrServiceUnavailable
	}
	if exists {
		return ErrEmailExists
	}
	return nil
}

// AuthEnsureEmailAliasTargetAvailable 检查邮箱是否已被其他用户的收件箱身份占用。
// 真实仓储提供 owner 查询，因此当前用户把自己的邮箱换成另一个 alias 时不会被误拒；
// 只有旧仓储仅提供布尔查询时才采取保守拒绝策略。
func (s *AuthService) AuthEnsureEmailAliasTargetAvailable(ctx context.Context, currentUser *User, email string) error {
	if currentUser == nil {
		return ErrUserNotFound
	}
	if lookup := s.AliasOwner; lookup != nil {
		ownerID, exists, err := lookup.EmailAliasOwnerID(ctx, email, currentUser.ID)
		if err != nil {
			return ErrServiceUnavailable
		}
		if exists && ownerID != currentUser.ID {
			return ErrEmailExists
		}
		return nil
	}
	lookup := s.AliasLookup
	ok := lookup != nil
	if !ok {
		return nil
	}
	exists, err := lookup.ExistsByEmailAlias(ctx, email)
	if err != nil {
		return ErrServiceUnavailable
	}
	if exists {
		// 无法区分当前用户时拒绝，避免并发或历史重复数据造成占用绕过。
		return ErrEmailExists
	}
	return nil
}

func (s *AuthService) AuthHasNormalizedEmailBindingConflict(
	ctx context.Context,
	currentUser *User,
	registrationNormalizedEmail string,
) (bool, error) {
	if registrationNormalizedEmail == "" {
		return false, nil
	}

	currentUserID := int64(0)
	currentNormalizedEmail := ""
	if currentUser != nil {
		currentUserID = currentUser.ID
		currentNormalizedEmail = NormalizeRegistrationEmailAddress(currentUser.Email)
	}
	if currentUserID > 0 && currentNormalizedEmail == registrationNormalizedEmail {
		if checker := s.NormalizedEmailConflict; checker != nil {
			return checker.ExistsByNormalizedEmailExcluding(ctx, registrationNormalizedEmail, currentUserID)
		}
		// 降级路径无法排除当前用户自身，保留原有宽松行为，避免把自己误判为冲突。
		return false, nil
	}

	return s.Users.ExistsByNormalizedEmail(ctx, registrationNormalizedEmail)
}

func (s *AuthService) AuthRevokeEmailIdentitySessions(ctx context.Context, userID int64) {
	if err := s.RevokeAllUserSessions(ctx, userID); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to revoke refresh sessions after email identity bind for user %d: %v", userID, err)
	}
}

func AuthNormalizeBoundEmailAuthIdentitySubject(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || IsReservedEmail(normalized) {
		return ""
	}
	return normalized
}
