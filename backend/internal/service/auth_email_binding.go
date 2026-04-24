package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

type normalizedEmailBindingConflictChecker interface {
	ExistsByNormalizedEmailExcluding(ctx context.Context, normalizedEmail string, excludedUserID int64) (bool, error)
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

	normalizedEmail, err := normalizeEmailForIdentityBinding(email)
	if err != nil {
		return nil, err
	}
	if isReservedEmail(normalizedEmail) {
		return nil, ErrEmailReserved
	}
	if strings.TrimSpace(password) == "" {
		return nil, ErrPasswordRequired
	}
	if err := s.VerifyOAuthEmailCode(ctx, normalizedEmail, verifyCode); err != nil {
		return nil, err
	}

	currentUser, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	firstRealEmailBind := !hasBindableEmailIdentitySubject(currentUser.Email)
	if firstRealEmailBind && len(password) < 6 {
		return nil, infraerrors.BadRequest("PASSWORD_TOO_SHORT", "password must be at least 6 characters")
	}
	if !firstRealEmailBind && !s.CheckPassword(password, currentUser.PasswordHash) {
		return nil, ErrPasswordIncorrect
	}

	registrationNormalizedEmail := s.normalizeRegistrationEmailForBinding(ctx, normalizedEmail)
	if err := s.ensureEmailBindingTargetAvailable(ctx, currentUser, normalizedEmail, registrationNormalizedEmail); err != nil {
		return nil, err
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	if s.entClient != nil {
		if err := s.updateBoundEmailIdentityTx(ctx, currentUser, normalizedEmail, registrationNormalizedEmail, hashedPassword, firstRealEmailBind); err != nil {
			return nil, err
		}
		s.revokeEmailIdentitySessions(ctx, userID)
		return currentUser, nil
	}

	currentUser.Email = normalizedEmail
	currentUser.PasswordHash = hashedPassword
	updateUser := s.userRepo.Update
	if registrationNormalizedEmail != "" {
		// 开启邮箱归一化后，绑定/换绑主邮箱也要复用同一套唯一性保护。
		updateUser = func(updateCtx context.Context, updateUser *User) error {
			return s.userRepo.UpdateWithNormalizedEmailGuard(updateCtx, updateUser, registrationNormalizedEmail)
		}
	}
	if err := updateUser(ctx, currentUser); err != nil {
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

	s.revokeEmailIdentitySessions(ctx, userID)
	return currentUser, nil
}

// SendEmailIdentityBindCode sends a verification code for authenticated email binding flows.
func (s *AuthService) SendEmailIdentityBindCode(ctx context.Context, userID int64, email string) error {
	if s == nil {
		return ErrServiceUnavailable
	}

	normalizedEmail, err := normalizeEmailForIdentityBinding(email)
	if err != nil {
		return err
	}
	if isReservedEmail(normalizedEmail) {
		return ErrEmailReserved
	}
	if s.emailService == nil {
		return ErrServiceUnavailable
	}
	currentUser, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrUserNotFound
		}
		return ErrServiceUnavailable
	}
	registrationNormalizedEmail := s.normalizeRegistrationEmailForBinding(ctx, normalizedEmail)
	if err := s.ensureEmailBindingTargetAvailable(ctx, currentUser, normalizedEmail, registrationNormalizedEmail); err != nil {
		return err
	}

	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}
	return s.emailService.SendVerifyCode(ctx, normalizedEmail, siteName)
}

func normalizeEmailForIdentityBinding(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || len(normalized) > 255 {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(normalized); err != nil {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	return normalized, nil
}

func hasBindableEmailIdentitySubject(email string) bool {
	normalized := strings.ToLower(strings.TrimSpace(email))
	return normalized != "" && !isReservedEmail(normalized)
}

func (s *AuthService) normalizeRegistrationEmailForBinding(ctx context.Context, email string) string {
	if s == nil || s.settingService == nil || !s.settingService.IsRegistrationEmailNormalizationEnabled(ctx) {
		return ""
	}
	return NormalizeRegistrationEmailAddress(email)
}

func (s *AuthService) ensureEmailBindingTargetAvailable(
	ctx context.Context,
	currentUser *User,
	email string,
	registrationNormalizedEmail string,
) error {
	existingUser, err := s.userRepo.GetByEmail(ctx, email)
	switch {
	case err == nil && existingUser != nil && currentUser != nil && existingUser.ID != currentUser.ID:
		return ErrEmailExists
	case err != nil && !errors.Is(err, ErrUserNotFound):
		return ErrServiceUnavailable
	}

	if registrationNormalizedEmail == "" {
		return nil
	}

	exists, err := s.hasNormalizedEmailBindingConflict(ctx, currentUser, registrationNormalizedEmail)
	if err != nil {
		return ErrServiceUnavailable
	}
	if exists {
		return ErrEmailExists
	}
	return nil
}

func (s *AuthService) hasNormalizedEmailBindingConflict(
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
		if checker, ok := s.userRepo.(normalizedEmailBindingConflictChecker); ok {
			return checker.ExistsByNormalizedEmailExcluding(ctx, registrationNormalizedEmail, currentUserID)
		}
		// 降级路径无法排除当前用户自身，保留原有宽松行为，避免把自己误判为冲突。
		return false, nil
	}

	return s.userRepo.ExistsByNormalizedEmail(ctx, registrationNormalizedEmail)
}

func (s *AuthService) updateBoundEmailIdentityTx(
	ctx context.Context,
	currentUser *User,
	email string,
	registrationNormalizedEmail string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return s.updateBoundEmailIdentityWithClient(ctx, tx.Client(), currentUser, email, registrationNormalizedEmail, hashedPassword, applyFirstBindDefaults)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return ErrServiceUnavailable
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := s.updateBoundEmailIdentityWithClient(txCtx, tx.Client(), currentUser, email, registrationNormalizedEmail, hashedPassword, applyFirstBindDefaults); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return ErrServiceUnavailable
	}
	return nil
}

func (s *AuthService) updateBoundEmailIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	currentUser *User,
	email string,
	registrationNormalizedEmail string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	if client == nil || currentUser == nil || currentUser.ID <= 0 {
		return ErrServiceUnavailable
	}

	oldEmail := currentUser.Email
	updatedUser := *currentUser
	updatedUser.Email = email
	updatedUser.PasswordHash = hashedPassword

	if registrationNormalizedEmail != "" {
		// 绑定邮箱入口也要与注册/资料修改共用归一化唯一性约束。
		if err := s.userRepo.LockRegistrationEmail(ctx, registrationNormalizedEmail); err != nil {
			return ErrServiceUnavailable
		}
		exists, err := s.hasNormalizedEmailBindingConflict(ctx, currentUser, registrationNormalizedEmail)
		if err != nil {
			return ErrServiceUnavailable
		}
		if exists {
			return ErrEmailExists
		}
	}

	if _, err := client.User.UpdateOneID(currentUser.ID).
		SetEmail(updatedUser.Email).
		SetPasswordHash(updatedUser.PasswordHash).
		Save(ctx); err != nil {
		if dbent.IsConstraintError(err) {
			return ErrEmailExists
		}
		if errors.Is(err, ErrEmailExists) {
			return ErrEmailExists
		}
		return ErrServiceUnavailable
	}

	if err := replaceBoundEmailAuthIdentityWithClient(ctx, client, currentUser.ID, oldEmail, email, "auth_service_email_bind"); err != nil {
		if errors.Is(err, ErrEmailExists) {
			return ErrEmailExists
		}
		return ErrServiceUnavailable
	}

	if applyFirstBindDefaults {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, currentUser.ID, "email"); err != nil {
			return fmt.Errorf("apply email first bind defaults: %w", err)
		}
	}

	refreshedUser, err := client.User.Get(ctx, currentUser.ID)
	if err != nil {
		return ErrServiceUnavailable
	}
	currentUser.Email = refreshedUser.Email
	currentUser.PasswordHash = refreshedUser.PasswordHash
	currentUser.Balance = refreshedUser.Balance
	currentUser.Concurrency = refreshedUser.Concurrency
	currentUser.UpdatedAt = refreshedUser.UpdatedAt
	return nil
}

func (s *AuthService) revokeEmailIdentitySessions(ctx context.Context, userID int64) {
	if err := s.RevokeAllUserSessions(ctx, userID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to revoke refresh sessions after email identity bind for user %d: %v", userID, err)
	}
}

func replaceBoundEmailAuthIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	oldEmail string,
	newEmail string,
	source string,
) error {
	newSubject := normalizeBoundEmailAuthIdentitySubject(newEmail)
	if err := ensureBoundEmailAuthIdentityWithClient(ctx, client, userID, newSubject, source); err != nil {
		return err
	}

	oldSubject := normalizeBoundEmailAuthIdentitySubject(oldEmail)
	if oldSubject == "" || oldSubject == newSubject {
		return nil
	}

	_, err := client.AuthIdentity.Delete().
		Where(
			authidentity.UserIDEQ(userID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(oldSubject),
		).
		Exec(ctx)
	return err
}

func ensureBoundEmailAuthIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	subject string,
	source string,
) error {
	if client == nil || userID <= 0 || subject == "" {
		return nil
	}

	if strings.TrimSpace(source) == "" {
		source = "auth_service_email_bind"
	}

	if err := client.AuthIdentity.Create().
		SetUserID(userID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(subject).
		SetVerifiedAt(time.Now().UTC()).
		SetMetadata(map[string]any{"source": strings.TrimSpace(source)}).
		OnConflictColumns(
			authidentity.FieldProviderType,
			authidentity.FieldProviderKey,
			authidentity.FieldProviderSubject,
		).
		DoNothing().
		Exec(ctx); err != nil {
		if !isSQLNoRowsError(err) {
			return err
		}
	}

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(subject),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil
		}
		return err
	}
	if identity.UserID != userID {
		return ErrEmailExists
	}
	return nil
}

func normalizeBoundEmailAuthIdentitySubject(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || isReservedEmail(normalized) {
		return ""
	}
	return normalized
}
