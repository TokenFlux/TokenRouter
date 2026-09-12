// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	gin "github.com/gin-gonic/gin"
)

// SessionBindingContext 委托身份 HTTP 适配，保留旧调用签名。
func SessionBindingContext(cfg *config.Config) gin.HandlerFunc {
	return identityhttp.SessionBindingContext(func() identityhttp.ForwardedIPSettings {
		v := cfg.ForwardedClientIPSettings()
		return identityhttp.ForwardedIPSettings{TrustForwardedIP: v.TrustForwardedIP, Headers: v.Headers}
	})
}

// SecurityClientIP 委托身份 HTTP 适配。
func SecurityClientIP(c *gin.Context) string { return identityhttp.SecurityClientIP(c) }
