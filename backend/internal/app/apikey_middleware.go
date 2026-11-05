package app

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// apikey_middleware.go binds authenticated API key and group views to gateway
// request contexts.
// Gateway authorization publishes key snapshots to Gin and a hydrated group
// to the typed request context. A supplied group needs a positive ID, a
// hydration marker, and status text before it can replace that context's
// selection.
//
// The loaded-key callback captures Ops diagnostics before admission may reject
// the request; legacy binding publishes a copied key after funding admission.
// When the typed context already holds a valid group with the same ID, it keeps
// that object so Gin rebinding retains its hydrated routing policy. Standard
// and Google entry points share these callbacks and the configured client IP
// trust setting used for key ACLs.

// newGatewayAuthorization constructs gateway authentication with request
// observation and group-binding callbacks. A nil subscription service leaves
// the subscription reader unset; google selects the Google credential entry
// point with the same key service and configured IP trust policy.
//
// CopyAPIKey copies user and actor views, group views, and the composite-group
// slice. Other key reference fields remain shared with the callback's input.
func newGatewayAuthorization(keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, cfg *config.Config, google bool) gin.HandlerFunc {
	options := gatewayhttp.APIKeyAuthorizationOptions{
		Authentication: keyhttp.AuthenticationOptions{
			Google: google, Context: func(c *gin.Context) context.Context { return c.Request.Context() },
			ClientIP: func(c *gin.Context) string {
				return clientip.GetSecurityClientIP(c, cfg.TrustForwardedHeaders())
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
				// Save a diagnostic key view when authentication supplies loaded data,
				// even if group or funding admission rejects this request later.
				keyhttp.SetOpsFallbackAPIKey(c, apikey.CopyAPIKey(key))
			},
		},
		BindLegacyKey: func(c *gin.Context, key *apikey.APIKey) {
			// After funding admission, populate the Gin key expected by keyhttp
			// readers and bind its group for typed request consumers.
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

// provideAPIKeyAuth constructs standard API key middleware with application
// observation and context-binding callbacks. It uses newGatewayAuthorization's
// configured IP trust policy; nil subscriptions leave the optional subscription
// reader unset.
func provideAPIKeyAuth(keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, cfg *config.Config) keyhttp.APIKeyAuthMiddleware {
	return keyhttp.APIKeyAuthMiddleware(newGatewayAuthorization(keys, subscriptions, cfg, false))
}

// bindLegacyAuthorizationGroup makes a hydrated authorization group available
// to typed request consumers. Nil groups, nonpositive IDs, missing hydration,
// or empty status text leave the request untouched.
//
// A request may already carry the hydrated group selected by an earlier
// authorization step. Matching IDs retain that existing object, so rebinding
// the legacy key does not replace the request's group data. A different ID or
// invalid current group receives the supplied group through a derived context.
func bindLegacyAuthorizationGroup(c *gin.Context, group *routing.Group) {
	if !routing.IsGroupContextValid(group) {
		return
	}
	// The legacy key supplies its own group clone. Keep the typed context's
	// existing object when compatibility binding brings the same hydrated group ID.
	if current, ok := requeststate.GroupFromContext(c.Request.Context()); ok && current != nil && current.ID == group.ID && routing.IsGroupContextValid(current) {
		return
	}
	c.Request = c.Request.WithContext(requeststate.WithGroup(c.Request.Context(), group))
}
