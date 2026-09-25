// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	ip "github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/gin-gonic/gin"
)

// SessionBindingContext 全局中间件：将请求的客户端 IP 与 User-Agent 注入
// request context，供 token 签发路径（登录 / 刷新 / OAuth 回调）读取并写入会话绑定，
// 同时作为审计日志、会话绑定校验的统一客户端 IP 来源。
// IP 取值与 API Key IP 限制共用转发 IP 开关：开启时旧版原始转发头逻辑
// 接管解析，关闭时使用 Gin 的 server.trusted_proxies 可信代理链。
func SessionBindingContext(settings func() ForwardedIPSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		forwardedIPSettings := settings()
		ip.SetForwardedIPSettings(c, forwardedIPSettings.TrustForwardedIP, forwardedIPSettings.Headers)
		userAgent := normalizePersistentText(c.Request.UserAgent(), authctx.MaxPersistentUserAgentBytes)
		c.Request.Header.Set("User-Agent", userAgent)
		binding := &identity.SessionBinding{
			IP:        ip.GetSecurityClientIP(c, forwardedIPSettings.TrustForwardedIP),
			UserAgent: userAgent,
		}
		c.Request = c.Request.WithContext(identity.WithSessionBinding(c.Request.Context(), binding))
		c.Next()
	}
}

// RequestSessionBinding 返回当前请求的会话指纹，优先取 SessionBindingContext
// 注入的解析结果（保证与 token 签发路径取值一致）；注入缺失时使用安全回退。
func RequestSessionBinding(c *gin.Context) *identity.SessionBinding {
	if binding := identity.SessionBindingFromContext(c.Request.Context()); binding != nil {
		return binding
	}
	return &identity.SessionBinding{
		IP:        ip.GetTrustedClientIP(c),
		UserAgent: normalizePersistentText(c.Request.UserAgent(), authctx.MaxPersistentUserAgentBytes),
	}
}

// SecurityClientIP 返回当前请求用于安全敏感记录（审计日志等）的客户端 IP。
// 与会话绑定、API Key IP 限制共用同一套客户端 IP 来源。
func SecurityClientIP(c *gin.Context) string {
	if binding := identity.SessionBindingFromContext(c.Request.Context()); binding != nil &&
		strings.TrimSpace(binding.IP) != "" {
		return binding.IP
	}
	return ip.GetTrustedClientIP(c)
}

// EnforceSessionBinding 校验 access token 的会话指纹（IP/UA 绑定）。
// 指纹不匹配时：撤销该会话家族的所有 refresh token、写入审计安全事件、返回 401。
// 返回 false 表示请求已被中断。
//
// 兼容性：claims.BindingHash 为空（功能上线前签发的旧 token）时放行，
// 该会话在下一次 refresh 轮转时会自动获得绑定。
func EnforceSessionBinding(
	c *gin.Context,
	authService SessionAuth,
	settingService BindingSettings,
	auditService AuthObserver,
	claims *identity.JWTClaims,
) bool {
	if settingService == nil || !settingService.IsSessionBindingEnabled(c.Request.Context()) {
		return true
	}
	if claims == nil || claims.BindingHash == "" {
		return true
	}
	binding := RequestSessionBinding(c)
	current := binding.Hash()
	if current == "" || current == claims.BindingHash {
		return true
	}

	if authService != nil {
		_ = authService.RevokeSessionFamily(c.Request.Context(), claims.SessionID)
	}
	if auditService != nil {
		uid := claims.UserID
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		auditService.RecordBindingMismatch(c.Request.Context(), BindingMismatchEvent{UserID: uid, Email: claims.Email, Role: claims.Role, Method: c.Request.Method, Path: path, ClientIP: binding.IP, UserAgent: normalizePersistentText(c.Request.UserAgent(), authctx.MaxPersistentUserAgentBytes)})
	}
	AbortWithError(c, 401, "SESSION_BINDING_MISMATCH", "Session network fingerprint changed, please login again")
	return false
}
