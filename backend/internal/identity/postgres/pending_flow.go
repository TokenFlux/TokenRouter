// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	authidentity "github.com/TokenFlux/TokenRouter/ent/authidentity"
	identityadoptiondecision "github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	strings "strings"
)

type PendingFlowDatabase struct {
	Client   *dbent.Client
	Auth     *identitycore.AuthService
	Profiles *identitycore.UserService
}

func (d *PendingFlowDatabase) HasDatabase() bool { return d != nil && d.Client != nil }
func (d *PendingFlowDatabase) LoadAdoptionDecision(ctx context.Context, id int64) (*identitycore.IdentityAdoptionDecision, error) {
	v, err := d.Client.IdentityAdoptionDecision.Query().Where(identityadoptiondecision.PendingAuthSessionIDEQ(id)).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	return IdentityAdoptionDecisionFromEntity(v), err
}
func (d *PendingFlowDatabase) FindUserByNormalizedEmail(ctx context.Context, email string) (*identitycore.User, error) {
	v, e := FindUserByNormalizedEmail(ctx, d.Client, email)
	return UserFromEntity(v), e
}
func (d *PendingFlowDatabase) UpdateProgress(ctx context.Context, p *identitycore.PendingAuthSession, intent, email string, target *int64, state map[string]any) (*identitycore.PendingAuthSession, error) {
	v, e := UpdatePendingOAuthSessionProgress(ctx, d.Client, PendingAuthSessionToEntity(p), intent, email, target, state)
	return PendingAuthSessionFromEntity(v), e
}
func (d *PendingFlowDatabase) EnsureRegistrationIdentityAvailable(ctx context.Context, p *identitycore.PendingAuthSession) error {
	return EnsurePendingOAuthRegistrationIdentityAvailable(ctx, d.Client, PendingAuthSessionToEntity(p))
}
func (d *PendingFlowDatabase) IdentityExistsForUser(ctx context.Context, p *identitycore.PendingAuthSession, id int64) (bool, error) {
	return PendingOAuthIdentityExistsForUser(ctx, d.Client, PendingAuthSessionToEntity(p), id)
}
func (d *PendingFlowDatabase) ApplyBinding(ctx context.Context, p identitycore.PendingBinding) error {
	return ApplyPendingOAuthBinding(ctx, d.Client, d.Auth, d.Profiles, PendingAuthSessionToEntity(p.Session), IdentityAdoptionDecisionToEntity(p.Decision), p.OverrideUserID, p.ForceBind, p.ApplyFirstBindDefaults)
}
func (d *PendingFlowDatabase) ApplyAdoption(ctx context.Context, p *identitycore.PendingAuthSession, choice *identitycore.IdentityAdoptionDecision, id *int64) error {
	return ApplyPendingOAuthAdoption(ctx, d.Client, d.Auth, d.Profiles, PendingAuthSessionToEntity(p), IdentityAdoptionDecisionToEntity(choice), id)
}
func (d *PendingFlowDatabase) ApplyAdoptionAndConsume(ctx context.Context, p *identitycore.PendingAuthSession, choice *identitycore.IdentityAdoptionDecision, id int64) error {
	return ApplyPendingOAuthAdoptionAndConsumeSession(ctx, d.Client, d.Auth, d.Profiles, PendingAuthSessionToEntity(p), IdentityAdoptionDecisionToEntity(choice), id)
}

// FinalizeCreatedAccount 只拥有原第二段事务；失败补偿在核心流程中执行。
func (d *PendingFlowDatabase) FinalizeCreatedAccount(ctx context.Context, p identitycore.PendingAccountFinalization) error {
	failure := func(phase string, err error) error { return &identitycore.PendingWriteError{Phase: phase, Cause: err} }
	tx, err := d.Client.Tx(ctx)
	if err != nil {
		return failure("begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if p.PrepareDecision != nil {
		decision, err := p.PrepareDecision(ctx)
		if err != nil {
			return failure("adoption", err)
		}
		p.Decision = decision
	}
	session, choice := PendingAuthSessionToEntity(p.Session), IdentityAdoptionDecisionToEntity(p.Decision)
	if err := ApplyPendingOAuthBinding(txCtx, d.Client, d.Auth, d.Profiles, session, choice, &p.User.ID, true, false); err != nil {
		return failure("binding", err)
	}
	if err := d.Auth.FinalizeOAuthEmailAccount(txCtx, p.User, strings.TrimSpace(p.InvitationCode), strings.TrimSpace(p.Session.ProviderType), strings.TrimSpace(p.AffiliateCode)); err != nil {
		return failure("finalize", err)
	}
	if err := ConsumePendingOAuthBrowserSessionTx(txCtx, tx, session); err != nil {
		return failure("consume", err)
	}
	if p.BeforeCommit != nil {
		if err := p.BeforeCommit(txCtx, p.Session); err != nil {
			return failure("hook", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return failure("commit", err)
	}
	return nil
}
func (d *PendingFlowDatabase) FindOAuthIdentityUser(ctx context.Context, identity identitycore.PendingAuthIdentityKey) (*identitycore.User, error) {
	client := d.Client
	if client == nil {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}

	record, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(strings.TrimSpace(identity.ProviderType)),
			authidentity.ProviderKeyEQ(strings.TrimSpace(identity.ProviderKey)),
			authidentity.ProviderSubjectEQ(strings.TrimSpace(identity.ProviderSubject)),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	v, e := FindActiveUserByID(ctx, client, record.UserID)
	return UserFromEntity(v), e
}
