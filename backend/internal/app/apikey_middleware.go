// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
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
		AbuseClientKey: middleware.InvalidAuthClientKey, NonConsuming: func(c *gin.Context) bool {
			return gatewayhttp.IsAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path)
		},
		Rejected: func(c *gin.Context, reason string) {
			middleware.MarkIngressRejected(c, middleware.IngressRejectReason(reason))
		},
		BusinessLimited: func(c *gin.Context, reason string) {
			gatewayhttp.MarkOpsClientBusinessLimited(c, reason)
		},
		Loaded: func(c *gin.Context, key *apikey.APIKey) {
			keyhttp.SetOpsFallbackAPIKey(c, apikey.CopyAPIKey(key))
		},
	},
		BindLegacyKey: func(c *gin.Context, key *apikey.APIKey) {
			legacy := apikey.CopyAPIKey(key)
			c.Set(string(keyhttp.ContextKeyAPIKey), legacy)
			bindLegacyAuthorizationGroup(c, legacy.Group)
		},
	}
	var reader gatewayhttp.AuthorizationSubscriptions
	if subscriptions != nil {
		reader = subscriptions
	}
	nativeKeys := keys
	if google {
		return gatewayhttp.NewGoogleAPIKeyAuthorization(nativeKeys, reader, options)
	}
	return gatewayhttp.NewAPIKeyAuthorization(nativeKeys, reader, options)
}

// provideAPIKeyAuth 构造唯一原生认证链，旧实体只在适配回调中投影。
func provideAPIKeyAuth(keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, cfg *config.Config) keyhttp.APIKeyAuthMiddleware {
	return keyhttp.APIKeyAuthMiddleware(newGatewayAuthorization(keys, subscriptions, cfg, false))
}

// bindLegacyAuthorizationGroup 将有效分组写入请求状态，已有相同有效分组时保持原值。
func bindLegacyAuthorizationGroup(c *gin.Context, group *routing.Group) {
	if !routing.IsGroupContextValid(group) {
		return
	}
	if current, ok := requeststate.GroupFromContext(c.Request.Context()); ok && current != nil && current.ID == group.ID && routing.IsGroupContextValid(current) {
		return
	}
	c.Request = c.Request.WithContext(requeststate.WithGroup(c.Request.Context(), group))
}
