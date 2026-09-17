package middleware

import (
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	gin "github.com/gin-gonic/gin"
)

// SessionBindingContext 仅保留旧配置夹具的测试装配，退出 S16。
func SessionBindingContext(cfg *config.Config) gin.HandlerFunc {
	return identityhttp.SessionBindingContext(func() identityhttp.ForwardedIPSettings {
		v := cfg.ForwardedClientIPSettings()
		return identityhttp.ForwardedIPSettings{TrustForwardedIP: v.TrustForwardedIP, Headers: v.Headers}
	})
}
