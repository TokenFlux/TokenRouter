// S15：旧实体仅保留为原中间件测试的局部输入适配，生产装配已直接调用 identity；S16 删除。
// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/audit"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	gin "github.com/gin-gonic/gin"
)

// NewAdminAuthMiddleware 创建管理员认证中间件
func NewAdminAuthMiddleware(
	authService *identity.SessionService,
	userService *identity.UserService,
	settingService *identity.RuntimeSettings,
	auditService *audit.AuditLogService,
) AdminAuthMiddleware {
	return AdminAuthMiddleware(adminAuth(authService, userService, settingService, auditService))
}

// adminAuth 委托身份 HTTP 适配，保留旧调用签名。
func adminAuth(
	authService *identity.SessionService,
	userService *identity.UserService,
	settingService *identity.RuntimeSettings,
	auditService *audit.AuditLogService,
) gin.HandlerFunc {
	return identityhttp.AdminAuth(identityAuth(authService), identityHTTPAdmin{userService}, identitySettings(settingService), identityAudit(auditService))
}
