// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

// PendingRepository 实现身份用例的持久化边界。
type PendingRepository struct{ store *AuthPendingIdentityService }

func NewPendingRepository(client *dbent.Client, clocks ...func() time.Time) *PendingRepository {
	return &PendingRepository{store: NewAuthPendingIdentityService(client, clocks...)}
}
func (s *PendingRepository) CreatePendingSession(ctx context.Context, input CreatePendingAuthSessionInput) (*identity.PendingAuthSession, error) {
	v, err := s.store.CreatePendingSession(ctx, input)
	return PendingAuthSessionFromEntity(v), err
}
func (s *PendingRepository) IssueCompletionCode(ctx context.Context, input IssuePendingAuthCompletionCodeInput) (*IssuePendingAuthCompletionCodeResult, error) {
	return s.store.IssueCompletionCode(ctx, input)
}
func (s *PendingRepository) ConsumeCompletionCode(ctx context.Context, rawCode, browserSessionKey string) (*identity.PendingAuthSession, error) {
	v, err := s.store.ConsumeCompletionCode(ctx, rawCode, browserSessionKey)
	return PendingAuthSessionFromEntity(v), err
}
func (s *PendingRepository) ConsumeBrowserSession(ctx context.Context, sessionToken, browserSessionKey string) (*identity.PendingAuthSession, error) {
	v, err := s.store.ConsumeBrowserSession(ctx, sessionToken, browserSessionKey)
	return PendingAuthSessionFromEntity(v), err
}
func (s *PendingRepository) GetBrowserSession(ctx context.Context, sessionToken, browserSessionKey string) (*identity.PendingAuthSession, error) {
	v, err := s.store.GetBrowserSession(ctx, sessionToken, browserSessionKey)
	return PendingAuthSessionFromEntity(v), err
}
func (s *PendingRepository) UpsertAdoptionDecision(ctx context.Context, input PendingIdentityAdoptionDecisionInput) (*identity.IdentityAdoptionDecision, error) {
	v, err := s.store.UpsertAdoptionDecision(ctx, input)
	return IdentityAdoptionDecisionFromEntity(v), err
}

func PendingAuthSessionFromEntity(v *dbent.PendingAuthSession) *identity.PendingAuthSession {
	if v == nil {
		return nil
	}
	return &identity.PendingAuthSession{ID: v.ID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, SessionToken: v.SessionToken, Intent: v.Intent, ProviderType: v.ProviderType, ProviderKey: v.ProviderKey, ProviderSubject: v.ProviderSubject, TargetUserID: v.TargetUserID, RedirectTo: v.RedirectTo, ResolvedEmail: v.ResolvedEmail, RegistrationPasswordHash: v.RegistrationPasswordHash, UpstreamIdentityClaims: v.UpstreamIdentityClaims, LocalFlowState: v.LocalFlowState, BrowserSessionKey: v.BrowserSessionKey, CompletionCodeHash: v.CompletionCodeHash, CompletionCodeExpiresAt: v.CompletionCodeExpiresAt, EmailVerifiedAt: v.EmailVerifiedAt, PasswordVerifiedAt: v.PasswordVerifiedAt, TotpVerifiedAt: v.TotpVerifiedAt, ExpiresAt: v.ExpiresAt, ConsumedAt: v.ConsumedAt}
}
func PendingAuthSessionToEntity(v *identity.PendingAuthSession) *dbent.PendingAuthSession {
	if v == nil {
		return nil
	}
	return &dbent.PendingAuthSession{ID: v.ID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, SessionToken: v.SessionToken, Intent: v.Intent, ProviderType: v.ProviderType, ProviderKey: v.ProviderKey, ProviderSubject: v.ProviderSubject, TargetUserID: v.TargetUserID, RedirectTo: v.RedirectTo, ResolvedEmail: v.ResolvedEmail, RegistrationPasswordHash: v.RegistrationPasswordHash, UpstreamIdentityClaims: v.UpstreamIdentityClaims, LocalFlowState: v.LocalFlowState, BrowserSessionKey: v.BrowserSessionKey, CompletionCodeHash: v.CompletionCodeHash, CompletionCodeExpiresAt: v.CompletionCodeExpiresAt, EmailVerifiedAt: v.EmailVerifiedAt, PasswordVerifiedAt: v.PasswordVerifiedAt, TotpVerifiedAt: v.TotpVerifiedAt, ExpiresAt: v.ExpiresAt, ConsumedAt: v.ConsumedAt}
}
func IdentityAdoptionDecisionFromEntity(v *dbent.IdentityAdoptionDecision) *identity.IdentityAdoptionDecision {
	if v == nil {
		return nil
	}
	return &identity.IdentityAdoptionDecision{ID: v.ID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, PendingAuthSessionID: v.PendingAuthSessionID, IdentityID: v.IdentityID, AdoptDisplayName: v.AdoptDisplayName, AdoptAvatar: v.AdoptAvatar, DecidedAt: v.DecidedAt}
}
func IdentityAdoptionDecisionToEntity(v *identity.IdentityAdoptionDecision) *dbent.IdentityAdoptionDecision {
	if v == nil {
		return nil
	}
	return &dbent.IdentityAdoptionDecision{ID: v.ID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, PendingAuthSessionID: v.PendingAuthSessionID, IdentityID: v.IdentityID, AdoptDisplayName: v.AdoptDisplayName, AdoptAvatar: v.AdoptAvatar, DecidedAt: v.DecidedAt}
}
