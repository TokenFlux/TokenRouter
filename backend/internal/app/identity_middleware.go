package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	audithttp "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// identityAuthAudit 将认证观察投影到既有审计记录，保持异步尽力写入。
type identityAuthAudit struct{ audit *audit.AuditLogService }

func (a identityAuthAudit) RecordBindingMismatch(_ context.Context, event identityhttp.BindingMismatchEvent) {
	a.audit.Record(&audit.AuditLog{ActorUserID: &event.UserID, ActorEmail: event.Email, ActorRole: event.Role, AuthMethod: audit.AuditAuthMethodJWT, Action: audit.AuditActionSessionBindingMismatch, Method: event.Method, Path: event.Path, ClientIP: event.ClientIP, UserAgent: event.UserAgent, StatusCode: 401})
}

// provideJWTAuth 直接装配原生身份、设置和活动记录，server 不接收旧业务对象。
func provideJWTAuth(graph *identityAuthGraph, users *identity.UserService, settings *identity.RuntimeSettings, recorder *audit.AuditLogService) identityhttp.JWTAuthMiddleware {
	return identityhttp.JWTAuthMiddleware(identityhttp.JWTAuth(graph.Core, users, users, settings, identityAuthAudit{recorder}))
}

// provideAdminAuth 与 JWT 共用身份运行时，不构造第二套认证服务。
func provideAdminAuth(graph *identityAuthGraph, users *identity.UserService, settings *identity.RuntimeSettings, recorder *audit.AuditLogService) identityhttp.AdminAuthMiddleware {
	return identityhttp.AdminAuthMiddleware(identityhttp.AdminAuth(graph.Core, users, settings, identityAuthAudit{recorder}))
}

// provideStepUpAuth 保留同一会话凭据、TOTP 及动态开关。
func provideStepUpAuth(totp *identity.TotpService, users *identity.UserService, settings *identity.RuntimeSettings) identityhttp.StepUpAuthMiddleware {
	return identityhttp.StepUpAuthMiddleware(identityhttp.StepUpAuth(totp, users, settings))
}

// provideAuditMiddleware 注入与旧捕获入口同一份脱敏策略。
func provideAuditMiddleware(recorder *audit.AuditLogService, redactor *audit.Redactor) middleware.AuditLogMiddleware {
	return audithttp.NewAuditLogMiddleware(recorder, redactor)
}
