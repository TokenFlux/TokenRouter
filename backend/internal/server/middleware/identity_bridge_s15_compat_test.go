// S15：旧实体仅保留为原中间件测试的局部输入适配，生产装配已直接调用 identity；S16 删除。
// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/audit"

	context "context"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
)

// identityHTTPUser 将旧测试/过渡消费者投影到新身份读取端口，S16 删除。
type identityHTTPUser struct {
	reader interface {
		GetByID(context.Context, int64) (*identity.User, error)
	}
}

func (u identityHTTPUser) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	v, e := u.reader.GetByID(ctx, id)
	return identity.CopyUser(v), e
}

type identityHTTPAdmin struct{ *identity.UserService }

func (u identityHTTPAdmin) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	v, e := u.UserService.GetByID(ctx, id)
	return identity.CopyUser(v), e
}
func (u identityHTTPAdmin) GetFirstAdmin(ctx context.Context) (*identity.User, error) {
	v, e := u.UserService.GetFirstAdmin(ctx)
	return identity.CopyUser(v), e
}

type identityHTTPActivity struct{ userActivityToucher }

func (u identityHTTPActivity) TouchLastActiveForUser(ctx context.Context, v *identity.User) {
	u.userActivityToucher.TouchLastActiveForUser(ctx, identity.CopyUser(v))
}

type identityHTTPAudit struct{ *audit.AuditLogService }

func (a identityHTTPAudit) RecordBindingMismatch(ctx context.Context, e identityhttp.BindingMismatchEvent) {
	a.Record(&audit.AuditLog{ActorUserID: &e.UserID, ActorEmail: e.Email, ActorRole: e.Role, AuthMethod: audit.AuditAuthMethodJWT, Action: audit.AuditActionSessionBindingMismatch, Method: e.Method, Path: e.Path, ClientIP: e.ClientIP, UserAgent: e.UserAgent, StatusCode: 401})
}
func identityAudit(a *audit.AuditLogService) identityhttp.AuthObserver {
	if a == nil {
		return nil
	}
	return identityHTTPAudit{a}
}
func identitySettings(s *identity.RuntimeSettings) identityhttp.AuthSettings {
	if s == nil {
		return nil
	}
	return s
}
func identityAuth(a *identity.SessionService) identityhttp.SessionAuth {
	if a == nil {
		return nil
	}
	return a
}
