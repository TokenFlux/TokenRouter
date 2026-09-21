// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// UserReader 只为当前认证读取用户状态。
type UserReader interface {
	GetByID(context.Context, int64) (*identity.User, error)
}
type AdminUserReader interface {
	UserReader
	GetFirstAdmin(context.Context) (*identity.User, error)
}
type ActivityToucher interface {
	TouchLastActiveForUser(context.Context, *identity.User)
}
type SessionAuth interface {
	ValidateToken(string) (*identity.JWTClaims, error)
	RevokeSessionFamily(context.Context, string) error
}
type BindingSettings interface{ IsSessionBindingEnabled(context.Context) bool }
type AuthSettings interface {
	BindingSettings
	GetAdminAPIKey(context.Context) (string, error)
}
type StepUpSettingReader interface{ IsStepUpEnabled(context.Context) bool }
type StepUpGrantChecker interface {
	HasStepUpGrant(context.Context, int64, string) (bool, error)
}

// BindingMismatchEvent 是 HTTP 安全观察投影，由装配适配到既有审计能力。
type BindingMismatchEvent struct {
	UserID                                         int64
	Email, Role, Method, Path, ClientIP, UserAgent string
}
type AuthObserver interface {
	RecordBindingMismatch(context.Context, BindingMismatchEvent)
}
type ForwardedIPSettings struct {
	TrustForwardedIP bool
	Headers          []string
}
type JWTAuthMiddleware gin.HandlerFunc
type AdminAuthMiddleware gin.HandlerFunc
type StepUpAuthMiddleware gin.HandlerFunc

func AbortWithError(c *gin.Context, status int, code, message string) {
	httpx.AbortWithError(c, status, code, message)
}
func normalizePersistentText(v string, n int) string { return httpx.NormalizePersistentText(v, n) }

// 身份 HTTP 继续暴露旧名称，状态只由 authctx 叶子保存。
type AuthSubject = authctx.AuthSubject

const (
	ContextKeyUser              = authctx.ContextKeyUser
	ContextKeyUserRole          = authctx.ContextKeyUserRole
	ContextKeyAuthEmail         = authctx.ContextKeyAuthEmail
	ContextKeySessionID         = authctx.ContextKeySessionID
	maxPersistentUserAgentBytes = authctx.MaxPersistentUserAgentBytes
)

func SetPrincipal(c *gin.Context, p identity.Principal, n int, email string) {
	authctx.SetPrincipal(c, p, n, email)
}
func SetAuthenticatedPrincipal(c *gin.Context, p identity.Principal) {
	authctx.SetAuthenticatedPrincipal(c, p)
}
func GetPrincipal(c *gin.Context) (identity.Principal, bool) { return authctx.GetPrincipal(c) }
func GetAuthSubjectFromContext(c *gin.Context) (AuthSubject, bool) {
	return authctx.GetAuthSubjectFromContext(c)
}
func GetUserRoleFromContext(c *gin.Context) (string, bool) { return authctx.GetUserRoleFromContext(c) }
