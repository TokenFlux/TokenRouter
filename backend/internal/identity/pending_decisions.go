// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	errors "errors"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	strings "strings"
)

func (f *PendingFlow) EmailShouldCreatePendingRegistration(ctx context.Context, input EmailOAuthIdentityInput) (bool, error) {
	if !f.Available() {
		return false, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	identityUser, err := f.Database.FindOAuthIdentityUser(ctx, PendingAuthIdentityKey{
		ProviderType:    strings.TrimSpace(input.ProviderType),
		ProviderKey:     strings.TrimSpace(input.ProviderKey),
		ProviderSubject: strings.TrimSpace(input.ProviderSubject),
	})
	if err != nil {
		return false, err
	}
	email := strings.TrimSpace(strings.ToLower(input.Email))
	if identityUser != nil {
		if !strings.EqualFold(strings.TrimSpace(identityUser.Email), email) {
			return false, infraerrors.Conflict("AUTH_IDENTITY_EMAIL_MISMATCH", "oauth identity belongs to a different email")
		}
		return false, nil
	}
	if _, err := f.Database.FindUserByNormalizedEmail(ctx, email); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}
func (f *PendingFlow) LegacyRegistrationStatus(
	ctx context.Context,
	session *PendingAuthSession, emailVerificationRequired, forceEmailOnSignup bool,
) (*PendingAuthSession, bool, error) {
	if session == nil {
		return nil, false, infraerrors.BadRequest("PENDING_AUTH_SESSION_INVALID", "pending auth registration context is invalid")
	}

	payload := NormalizePendingOAuthCompletionResponse(MergePendingCompletionResponse(session, nil))
	if step := PendingSessionStringValue(payload, "step"); step != "" {
		return session, true, nil
	}

	if !emailVerificationRequired && !forceEmailOnSignup {
		return session, false, nil
	}

	if !f.Available() {
		return nil, false, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	updatedSession, err := f.Database.UpdateProgress(
		ctx,
		session,
		strings.TrimSpace(session.Intent),
		strings.TrimSpace(session.ResolvedEmail),
		nil,
		BuildLegacyCompleteRegistrationPendingResponse(session, forceEmailOnSignup, emailVerificationRequired),
	)
	if err != nil {
		return nil, false, infraerrors.InternalServer("PENDING_AUTH_SESSION_UPDATE_FAILED", "failed to update pending oauth session").WithCause(err)
	}
	return updatedSession, true, nil
}
func (f *PendingFlow) TransitionAccountToChoice(
	ctx context.Context,
	session *PendingAuthSession,
	targetUser *User,
	email string,
) (*PendingAuthSession, error) {
	completionResponse := PendingOAuthChoiceCompletionResponse(session, email)
	var targetUserID *int64
	if targetUser != nil && targetUser.ID > 0 {
		targetUserID = &targetUser.ID
	}
	session, err := f.Database.UpdateProgress(
		ctx,
		session,
		strings.TrimSpace(session.Intent),
		email,
		targetUserID,
		completionResponse,
	)
	if err != nil {
		return nil, infraerrors.InternalServer("PENDING_AUTH_SESSION_UPDATE_FAILED", "failed to update pending oauth session").WithCause(err)
	}
	return session, nil
}
func (f *PendingFlow) SkipAdoptionPrompt(
	ctx context.Context,
	session *PendingAuthSession,
	payload map[string]any,
) (bool, error) {
	if session == nil || len(payload) == 0 {
		return false, nil
	}
	if !PendingOAuthCompletionCanIssueTokenPair(session, payload) {
		return false, nil
	}
	if PendingSessionStringValue(session.UpstreamIdentityClaims, "suggested_display_name") == "" &&
		PendingSessionStringValue(session.UpstreamIdentityClaims, "suggested_avatar_url") == "" {
		return false, nil
	}

	return f.Database.IdentityExistsForUser(ctx, session, *session.TargetUserID)
}
