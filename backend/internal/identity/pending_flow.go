// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"context"
	"fmt"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// PendingDatabase 只暴露命名闭合操作，事务句柄只存在于 PostgreSQL Adapter。
type PendingDatabase interface {
	FindLinuxDoCompatEmailUser(context.Context, string) (*User, error)
	FindOIDCCompatEmailUser(context.Context, string) (*User, error)
	FindDingTalkCompatEmailUser(context.Context, string) (*User, error)
	FindOAuthBindTarget(context.Context, int64) (*User, error)
	EnsureWeChatBindOwnership(context.Context, int64, string, WeChatIdentityChannel, string) error
	FindWeChatUserByLegacyOpenID(context.Context, PendingAuthIdentityKey, WeChatIdentityChannel, string) (*User, error)
	EnsureWeChatRuntimeIdentityBinding(context.Context, int64, PendingAuthIdentityKey, map[string]any) error

	HasDatabase() bool
	LoadAdoptionDecision(context.Context, int64) (*IdentityAdoptionDecision, error)
	FindOAuthIdentityUser(context.Context, PendingAuthIdentityKey) (*User, error)
	FindUserByNormalizedEmail(context.Context, string) (*User, error)
	UpdateProgress(context.Context, *PendingAuthSession, string, string, *int64, map[string]any) (*PendingAuthSession, error)
	EnsureRegistrationIdentityAvailable(context.Context, *PendingAuthSession) error
	IdentityExistsForUser(context.Context, *PendingAuthSession, int64) (bool, error)
	ApplyBinding(context.Context, PendingBinding) error
	ApplyAdoption(context.Context, *PendingAuthSession, *IdentityAdoptionDecision, *int64) error
	ApplyAdoptionAndConsume(context.Context, *PendingAuthSession, *IdentityAdoptionDecision, int64) error
	FinalizeCreatedAccount(context.Context, PendingAccountFinalization) error
}
type PendingBinding struct {
	Session                           *PendingAuthSession
	Decision                          *IdentityAdoptionDecision
	OverrideUserID                    *int64
	ForceBind, ApplyFirstBindDefaults bool
}
type PendingAccountFinalization struct {
	// PrepareDecision 仅为原邮箱注册流程保留 Begin 后、绑定前的独立接纳写入时机。
	PrepareDecision func(context.Context) (*IdentityAdoptionDecision, error)

	Session                       *PendingAuthSession
	Decision                      *IdentityAdoptionDecision
	User                          *User
	InvitationCode, AffiliateCode string
	BeforeCommit                  func(context.Context, *PendingAuthSession) error
}

// PendingWriteError 保留不同失败点的响应与 cookie 清理差异，不能把事务退出当作成功。
type PendingWriteError struct {
	Phase string
	Cause error
}

func (e *PendingWriteError) Error() string { return e.Cause.Error() }
func (e *PendingWriteError) Unwrap() error { return e.Cause }

type OAuthAdoptionChoice struct{ AdoptDisplayName, AdoptAvatar *bool }

func (c OAuthAdoptionChoice) HasDecision() bool {
	return c.AdoptDisplayName != nil || c.AdoptAvatar != nil
}

// PendingFlow 拥有接纳选择及创建失败补偿，数据库和会话能力由 app 注入。
type PendingFlow struct {
	Store    PendingStore
	Database PendingDatabase
	Auth     *AuthService
	Profiles *UserService
}

func (f *PendingFlow) Available() bool {
	return f != nil && f.Database != nil && f.Database.HasDatabase()
}
func (f *PendingFlow) AdoptionDecision(ctx context.Context, sessionID int64, choice OAuthAdoptionChoice, ensure bool) (*IdentityAdoptionDecision, error) {
	if !f.Available() {
		return nil, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	existing, err := f.Database.LoadAdoptionDecision(ctx, sessionID)
	if err != nil {
		return nil, infraerrors.InternalServer("PENDING_AUTH_ADOPTION_LOAD_FAILED", "failed to load oauth profile adoption decision").WithCause(err)
	}
	if !choice.HasDecision() {
		if existing != nil || !ensure {
			return existing, nil
		}
	}
	input := PendingIdentityAdoptionDecisionInput{PendingAuthSessionID: sessionID}
	if existing != nil {
		input.AdoptDisplayName = existing.AdoptDisplayName
		input.AdoptAvatar = existing.AdoptAvatar
		input.IdentityID = existing.IdentityID
	}
	if choice.AdoptDisplayName != nil {
		input.AdoptDisplayName = *choice.AdoptDisplayName
	}
	if choice.AdoptAvatar != nil {
		input.AdoptAvatar = *choice.AdoptAvatar
	}
	out, err := f.Store.UpsertAdoptionDecision(ctx, input)
	if err != nil {
		return nil, infraerrors.InternalServer("PENDING_AUTH_ADOPTION_SAVE_FAILED", "failed to save oauth profile adoption decision").WithCause(err)
	}
	return out, nil
}

// FinalizeCreatedAccount 保留原来的注册提交与后续绑定事务，不扩大成新的大事务。
func (f *PendingFlow) FinalizeCreatedAccount(ctx context.Context, p PendingAccountFinalization) error {
	if err := f.Database.FinalizeCreatedAccount(ctx, p); err != nil {
		return f.compensateCreatedAccount(ctx, p, err)
	}
	return nil
}

// WeChatIdentityChannel 仅表达已有身份通道复合键。
type WeChatIdentityChannel struct{ Mode, AppID string }

// CompleteSecondFactorBinding 延续旧两段绑定/消费边界，不改变登录挑战的删除时机。
func (f *PendingFlow) CompleteSecondFactorBinding(ctx context.Context, b *PendingOAuthBindLoginSession, userID int64) error {
	if !f.Available() {
		return infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	p, e := f.Store.GetBrowserSession(ctx, b.PendingSessionToken, b.BrowserSessionKey)
	if e != nil {
		return e
	}
	choice, e := f.AdoptionDecision(ctx, p.ID, OAuthAdoptionChoice{}, true)
	if e != nil {
		return e
	}
	if e = f.Database.ApplyBinding(ctx, PendingBinding{Session: p, Decision: choice, OverrideUserID: &userID, ForceBind: true, ApplyFirstBindDefaults: true}); e != nil {
		return infraerrors.InternalServer("PENDING_AUTH_BIND_APPLY_FAILED", "failed to bind pending oauth identity").WithCause(e)
	}
	_, e = f.Store.ConsumeBrowserSession(ctx, p.SessionToken, p.BrowserSessionKey)
	return e
}

// FinalizeCreatedAccountWithChoice 先保存接纳选择，再进入原绑定事务；失败补偿仍由身份用例拥有。
func (f *PendingFlow) FinalizeCreatedAccountWithChoice(ctx context.Context, p PendingAccountFinalization, choice OAuthAdoptionChoice) error {
	decision, err := f.AdoptionDecision(ctx, p.Session.ID, choice, true)
	if err != nil {
		return f.compensateCreatedAccount(ctx, p, err)
	}
	p.Decision = decision
	return f.FinalizeCreatedAccount(ctx, p)
}
func (f *PendingFlow) compensateCreatedAccount(ctx context.Context, p PendingAccountFinalization, original error) error {
	if p.User != nil && p.User.ID > 0 {
		if rollbackErr := f.Auth.RollbackOAuthEmailAccountCreation(ctx, p.User.ID, strings.TrimSpace(p.InvitationCode)); rollbackErr != nil {
			return &PendingWriteError{Phase: "compensation", Cause: infraerrors.InternalServer("PENDING_AUTH_ACCOUNT_ROLLBACK_FAILED", "failed to rollback pending oauth account creation").WithCause(fmt.Errorf("original error: %w; rollback error: %v", original, rollbackErr))}
		}
	}
	return original
}

// FinalizeVerifiedAccount 保留邮箱注册的两段事务和独立接纳写入；Begin 失败也须补偿已经创建的用户。
func (f *PendingFlow) FinalizeVerifiedAccount(ctx context.Context, p PendingAccountFinalization) error {
	if !f.Available() {
		return &PendingWriteError{Phase: "not_ready", Cause: infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")}
	}
	session := *p.Session
	session.UpstreamIdentityClaims = ClonePendingMap(p.Session.UpstreamIdentityClaims)
	if code := strings.TrimSpace(p.InvitationCode); code != "" {
		session.UpstreamIdentityClaims["invitation_code"] = code
	}
	p.Session = &session
	p.PrepareDecision = func(ctx context.Context) (*IdentityAdoptionDecision, error) {
		return f.AdoptionDecision(ctx, session.ID, OAuthAdoptionChoice{}, true)
	}
	if err := f.Database.FinalizeCreatedAccount(ctx, p); err != nil {
		// 沿用邮箱流程已有的尽力补偿，不改变原失败响应；新增覆盖无法开启第二段事务的情况。
		_ = f.Auth.RollbackOAuthEmailAccountCreation(ctx, p.User.ID, strings.TrimSpace(p.InvitationCode))
		return err
	}
	return nil
}
