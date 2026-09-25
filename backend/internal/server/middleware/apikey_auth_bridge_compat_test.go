// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"

	context "context"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	config "github.com/TokenFlux/TokenRouter/internal/config"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	clientip "github.com/TokenFlux/TokenRouter/internal/server/clientip"

	gin "github.com/gin-gonic/gin"
)

// newGatewayAuthorization 不执行业务规则；装配同一原生 Key 与订阅实例。
func newGatewayAuthorization(keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, cfg *config.Config, google bool) gin.HandlerFunc {
	options := gatewayhttp.APIKeyAuthorizationOptions{Simple: cfg.RunMode == config.RunModeSimple, Authentication: keyhttp.AuthenticationOptions{
		Google: google, Context: func(c *gin.Context) context.Context { return c.Request.Context() },
		ClientIP: func(c *gin.Context) string {
			return clientip.GetSecurityClientIP(c, cfg.TrustForwardedIPForAPIKeyACL())
		},
		AbuseClientKey: invalidAuthClientKey, NonConsuming: func(c *gin.Context) bool {
			return gatewayhttp.IsAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path)
		},
		Rejected: func(c *gin.Context, reason string) { MarkIngressRejected(c, IngressRejectReason(reason)) },
		BusinessLimited: func(c *gin.Context, reason string) {
			gatewayhttp.MarkOpsClientBusinessLimited(c, reason)
		},
		Loaded: func(c *gin.Context, key *apikey.APIKey) { keyhttp.SetOpsFallbackAPIKey(c, apikey.CopyAPIKey(key)) },
	},
		BindLegacyKey: func(c *gin.Context, key *apikey.APIKey) {
			legacy := apikey.CopyAPIKey(key)
			c.Set(string(keyhttp.ContextKeyAPIKey), legacy)
			setGroupContext(c, legacy.Group)
		},
	}
	var reader gatewayhttp.AuthorizationSubscriptions
	if subscriptions != nil {
		reader = subscriptions
	}
	var nativeKeys *apikey.APIKeyService
	if keys != nil {
		nativeKeys = keys
	}
	if google {
		return gatewayhttp.NewGoogleAPIKeyAuthorization(nativeKeys, reader, options)
	}
	return gatewayhttp.NewAPIKeyAuthorization(nativeKeys, reader, options)
}
