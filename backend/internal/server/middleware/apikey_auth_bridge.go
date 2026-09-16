// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	context "context"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	clientip "github.com/TokenFlux/TokenRouter/internal/server/clientip"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

// newGatewayAuthorization 不执行业务规则；装配同一原生 Key 与订阅实例。
func newGatewayAuthorization(keys *service.APIKeyService, subscriptions *service.SubscriptionService, cfg *config.Config, google bool) gin.HandlerFunc {
	options := gatewayhttp.APIKeyAuthorizationOptions{Simple: cfg.RunMode == config.RunModeSimple, Authentication: keyhttp.AuthenticationOptions{
		Google: google, Context: func(c *gin.Context) context.Context { return service.KeyRequestContext(c.Request.Context()) },
		ClientIP: func(c *gin.Context) string {
			return clientip.GetSecurityClientIP(c, cfg.TrustForwardedIPForAPIKeyACL())
		},
		AbuseClientKey: invalidAuthClientKey, NonConsuming: func(c *gin.Context) bool {
			return gatewayhttp.IsAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path)
		},
		Rejected:        func(c *gin.Context, reason string) { MarkIngressRejected(c, IngressRejectReason(reason)) },
		BusinessLimited: func(c *gin.Context, reason string) { service.MarkOpsClientBusinessLimited(c, reason) },
		Loaded:          func(c *gin.Context, key *apikey.APIKey) { SetOpsFallbackAPIKey(c, service.APIKeyFromView(key)) },
	}, PrepareContext: func(c *gin.Context, key *apikey.APIKey) {
		ctx := context.WithValue(c.Request.Context(), ctxkey.UserID, key.User.ID)
		ctx = context.WithValue(ctx, ctxkey.APIKeyFastModePolicy, key.FastModePolicy)
		c.Request = c.Request.WithContext(ctx)
	},
		BindLegacyKey: func(c *gin.Context, key *apikey.APIKey) {
			legacy := service.APIKeyFromView(key)
			c.Set(string(ContextKeyAPIKey), legacy)
			setGroupContext(c, legacy.Group)
		},
	}
	var reader gatewayhttp.AuthorizationSubscriptions
	if subscriptions != nil {
		reader = subscriptions
	}
	var nativeKeys *apikey.APIKeyService
	if keys != nil {
		nativeKeys = keys.APIKeyService
	}
	if google {
		return gatewayhttp.NewGoogleAPIKeyAuthorization(nativeKeys, reader, options)
	}
	return gatewayhttp.NewAPIKeyAuthorization(nativeKeys, reader, options)
}
