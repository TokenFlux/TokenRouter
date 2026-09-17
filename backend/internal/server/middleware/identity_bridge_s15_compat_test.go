// S15：旧实体仅保留为原中间件测试的局部输入适配，生产装配已直接调用 identity；S16 删除。
// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	context "context"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// identityHTTPUser 将旧测试/过渡消费者投影到新身份读取端口，S16 删除。
type identityHTTPUser struct {
	reader interface {
		GetByID(context.Context, int64) (*service.User, error)
	}
}

func (u identityHTTPUser) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	v, e := u.reader.GetByID(ctx, id)
	return service.IdentityUser(v), e
}

type identityHTTPAdmin struct{ *service.UserService }

func (u identityHTTPAdmin) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	v, e := u.UserService.GetByID(ctx, id)
	return service.IdentityUser(v), e
}
func (u identityHTTPAdmin) GetFirstAdmin(ctx context.Context) (*identity.User, error) {
	v, e := u.UserService.GetFirstAdmin(ctx)
	return service.IdentityUser(v), e
}

type identityHTTPActivity struct{ userActivityToucher }

func (u identityHTTPActivity) TouchLastActiveForUser(ctx context.Context, v *identity.User) {
	u.userActivityToucher.TouchLastActiveForUser(ctx, service.UserFromIdentity(v))
}

type identityHTTPAudit struct{ *service.AuditLogService }

func (a identityHTTPAudit) RecordBindingMismatch(ctx context.Context, e identityhttp.BindingMismatchEvent) {
	a.Record(&service.AuditLog{ActorUserID: &e.UserID, ActorEmail: e.Email, ActorRole: e.Role, AuthMethod: service.AuditAuthMethodJWT, Action: service.AuditActionSessionBindingMismatch, Method: e.Method, Path: e.Path, ClientIP: e.ClientIP, UserAgent: e.UserAgent, StatusCode: 401})
}
func identityAudit(a *service.AuditLogService) identityhttp.AuthObserver {
	if a == nil {
		return nil
	}
	return identityHTTPAudit{a}
}
func identitySettings(s *service.SettingService) identityhttp.AuthSettings {
	if s == nil {
		return nil
	}
	return s
}
func identityAuth(a *service.AuthService) identityhttp.SessionAuth {
	if a == nil {
		return nil
	}
	return a
}
