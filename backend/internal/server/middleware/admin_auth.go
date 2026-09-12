// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

// NewAdminAuthMiddleware 创建管理员认证中间件
func NewAdminAuthMiddleware(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) AdminAuthMiddleware {
	return AdminAuthMiddleware(adminAuth(authService, userService, settingService, auditService))
}

// adminAuth 委托身份 HTTP 适配，保留旧调用签名。
func adminAuth(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) gin.HandlerFunc {
	return identityhttp.AdminAuth(identityAuth(authService), identityHTTPAdmin{userService}, identitySettings(settingService), identityAudit(auditService))
}
