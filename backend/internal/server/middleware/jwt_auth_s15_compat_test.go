// S15：旧实体仅保留为原中间件测试的局部输入适配，生产装配已直接调用 identity；S16 删除。
// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	context "context"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

// NewJWTAuthMiddleware 创建 JWT 认证中间件
func NewJWTAuthMiddleware(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) JWTAuthMiddleware {
	return JWTAuthMiddleware(jwtAuth(authService, userService, userService, settingService, auditService))
}

type jwtUserReader interface {
	GetByID(ctx context.Context, id int64) (*service.User, error)
}

type userActivityToucher interface {
	TouchLastActiveForUser(ctx context.Context, user *service.User)
}

// jwtAuth 委托身份 HTTP 适配，保留旧调用签名。
func jwtAuth(
	authService *service.AuthService,
	userService jwtUserReader,
	activityToucher userActivityToucher,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) gin.HandlerFunc {
	var activity identityhttp.ActivityToucher
	if activityToucher != nil {
		activity = identityHTTPActivity{activityToucher}
	}
	return identityhttp.JWTAuth(identityAuth(authService), identityHTTPUser{userService}, activity, identitySettings(settingService), identityAudit(auditService))
}
