// S15：旧实体仅保留为原中间件测试的局部输入适配，生产装配已直接调用 identity；S16 删除。
// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/audit"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	context "context"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	gin "github.com/gin-gonic/gin"
)

// NewJWTAuthMiddleware 创建 JWT 认证中间件
func NewJWTAuthMiddleware(
	authService *identity.SessionService,
	userService *identity.UserService,
	settingService *identity.RuntimeSettings,
	auditService *audit.AuditLogService,
) JWTAuthMiddleware {
	return JWTAuthMiddleware(jwtAuth(authService, userService, userService, settingService, auditService))
}

type jwtUserReader interface {
	GetByID(ctx context.Context, id int64) (*identity.User, error)
}

type userActivityToucher interface {
	TouchLastActiveForUser(ctx context.Context, user *identity.User)
}

// jwtAuth 委托身份 HTTP 适配，保留旧调用签名。
func jwtAuth(
	authService *identity.SessionService,
	userService jwtUserReader,
	activityToucher userActivityToucher,
	settingService *identity.RuntimeSettings,
	auditService *audit.AuditLogService,
) gin.HandlerFunc {
	var activity identityhttp.ActivityToucher
	if activityToucher != nil {
		activity = identityHTTPActivity{activityToucher}
	}
	return identityhttp.JWTAuth(identityAuth(authService), identityHTTPUser{userService}, activity, identitySettings(settingService), identityAudit(auditService))
}
