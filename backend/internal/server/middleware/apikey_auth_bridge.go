// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	clientip "github.com/TokenFlux/TokenRouter/internal/server/clientip"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

// authenticateAPIKeyRequest 是旧网关到新凭据入口的过渡投影，S11 删除。
func authenticateAPIKeyRequest(c *gin.Context, keys *service.APIKeyService, cfg *config.Config, google bool) (*apikey.AccessSnapshot, bool) {
	return keyhttp.Authenticate(c, keys.APIKeyService, keyhttp.AuthenticationOptions{
		Google: google, Context: func(c *gin.Context) context.Context { return service.KeyRequestContext(c.Request.Context()) },
		ClientIP: func(c *gin.Context) string {
			return clientip.GetSecurityClientIP(c, cfg.TrustForwardedIPForAPIKeyACL())
		},
		AbuseClientKey: invalidAuthClientKey, NonConsuming: func(c *gin.Context) bool { return isAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path) },
		Rejected:        func(c *gin.Context, reason string) { MarkIngressRejected(c, IngressRejectReason(reason)) },
		BusinessLimited: func(c *gin.Context, reason string) { service.MarkOpsClientBusinessLimited(c, reason) },
		Loaded:          func(c *gin.Context, key *apikey.APIKey) { SetOpsFallbackAPIKey(c, service.APIKeyFromView(key)) },
	})
}
