// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"

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
func newGatewayAuthorization(keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, cfg *config.Config, google bool) gin.HandlerFunc {
	options := gatewayhttp.APIKeyAuthorizationOptions{Simple: cfg.RunMode == config.RunModeSimple, Authentication: keyhttp.AuthenticationOptions{
		Google: google, Context: func(c *gin.Context) context.Context { return service.KeyRequestContext(c.Request.Context()) },
		ClientIP: func(c *gin.Context) string {
			return clientip.GetSecurityClientIP(c, cfg.TrustForwardedIPForAPIKeyACL())
		},
		AbuseClientKey: middleware.InvalidAuthClientKey, NonConsuming: func(c *gin.Context) bool {
			return gatewayhttp.IsAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path)
		},
		Rejected: func(c *gin.Context, reason string) {
			middleware.MarkIngressRejected(c, middleware.IngressRejectReason(reason))
		},
		BusinessLimited: func(c *gin.Context, reason string) { service.MarkOpsClientBusinessLimited(c, reason) },
		Loaded: func(c *gin.Context, key *apikey.APIKey) {
			middleware.SetOpsFallbackAPIKey(c, service.APIKeyFromView(key))
		},
	}, PrepareContext: func(c *gin.Context, key *apikey.APIKey) {
		ctx := context.WithValue(c.Request.Context(), ctxkey.UserID, key.User.ID)
		ctx = context.WithValue(ctx, ctxkey.APIKeyFastModePolicy, key.FastModePolicy)
		c.Request = c.Request.WithContext(ctx)
	},
		BindLegacyKey: func(c *gin.Context, key *apikey.APIKey) {
			legacy := service.APIKeyFromView(key)
			c.Set(string(middleware.ContextKeyAPIKey), legacy)
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
func provideAPIKeyAuth(keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, cfg *config.Config) middleware.APIKeyAuthMiddleware {
	return middleware.APIKeyAuthMiddleware(newGatewayAuthorization(keys, subscriptions, cfg, false))
}

// bindLegacyAuthorizationGroup 仅服务 S16 尚未清零的请求 context 消费者。
func bindLegacyAuthorizationGroup(c *gin.Context, group *service.Group) {
	if !service.IsGroupContextValid(group) {
		return
	}
	if current, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group); ok && current != nil && current.ID == group.ID && service.IsGroupContextValid(current) {
		return
	}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, group))
}
