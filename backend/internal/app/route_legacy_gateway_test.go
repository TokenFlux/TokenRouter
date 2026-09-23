package app

import (
	"time"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"

	batchhttp "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// 历史夹具只在构造边界转换 Key 服务，生产签名使用原生实例。
func legacyRouteMiddleware(auth keyhttp.APIKeyAuthMiddleware, keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, ops *opscore.OpsService, settings *routing.RuntimeSettings, cfg *config.Config) gatewayhttp.RouteMiddleware {
	var native *apikey.APIKeyService
	if keys != nil {
		native = keys
	}
	value := provideGatewayRouteMiddleware(auth, native, subscriptions, ops, settings, cfg, nil)
	options := gatewayhttp.GroupAssignmentOptions{Access: func(c *gin.Context) gatewayhttp.GroupAssignmentAccess {
		key, ok := keyhttp.GetAPIKeyFromContext(c)
		if !ok || key == nil {
			return gatewayhttp.GroupAssignmentAccess{}
		}
		_, noGroup := c.Get(gatewayhttp.CompositeKeyNoGroupContextKey)
		return gatewayhttp.GroupAssignmentAccess{Loaded: true, Assigned: key.GroupID != nil, CompositeNoGroup: key.IsComposite && noGroup}
	}, Rejected: func(c *gin.Context) {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned)
		middleware.MarkIngressRejected(c, middleware.IngressRejectGroupUnassigned)
	}}
	options.WriteError = gatewayhttp.AnthropicErrorWriter
	value.RequireGroupAnthropic = gatewayhttp.RequireGroupAssignment(settings, options)
	options.WriteError = gatewayhttp.GoogleErrorWriter
	value.RequireGroupGoogle = gatewayhttp.RequireGroupAssignment(settings, options)
	return value
}

func RegisterGatewayRoutes(
	r *gin.Engine,
	h *routeTestHandlers,
	apiKeyAuth keyhttp.APIKeyAuthMiddleware,
	apiKeyService *apikey.APIKeyService,
	subscriptionService *billing.SubscriptionService,
	opsService *opscore.OpsService,
	settingService *routing.RuntimeSettings,
	cfg *config.Config,
) {
	// 生产使用 app 已绑定的目标 handler；手工装配的兼容测试仍复用同一实现。
	openAITokensHTTP := h.OpenAITokensHTTP
	if openAITokensHTTP == nil {
		openAITokensHTTP = provideOpenAITokensHTTP(nil, nil, nil, nil, nil, cfg, nil, nil)
	}
	countTokensHTTP := h.CountTokensHTTP
	if countTokensHTTP == nil && h.Gateway != nil {
		countTokensHTTP = provideCountTokensHTTP(nil, nil, nil, nil, cfg, nil, nil)
	}
	qoderCompatibleHTTP := h.QoderCompatibleHTTP
	if qoderCompatibleHTTP == nil {
		qoderCompatibleHTTP = provideQoderCompatibleHTTP(nil, nil, nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil)
	}
	compatibleTextHTTP := h.CompatibleTextHTTP
	if compatibleTextHTTP == nil && h.Gateway != nil {
		compatibleTextHTTP = h.Gateway.NewCompatibleTextHTTPHandler()
	}
	geminiNativeHTTP := h.GeminiNativeHTTP
	if geminiNativeHTTP == nil && h.Gateway != nil {
		geminiNativeHTTP = h.Gateway.NewGeminiNativeHTTPHandler()
	}
	openAITextHTTP := h.OpenAITextHTTP
	if openAITextHTTP == nil && h.OpenAIGateway != nil {
		openAITextHTTP = h.OpenAIGateway.NewOpenAITextHTTPHandler()
	}
	responsesWSHTTP := h.ResponsesWSHTTP
	if responsesWSHTTP == nil && h.OpenAIGateway != nil {
		responsesWSHTTP = h.OpenAIGateway.NewResponsesWSHTTPHandler()
	}
	modelsHTTP := h.ModelsHTTP
	if modelsHTTP == nil && h.Gateway != nil {
		modelsHTTP = provideModelsHTTP(nil, nil, nil)
	}
	messagesHTTP := h.MessagesHTTP
	if messagesHTTP == nil && h.Gateway != nil {
		messagesHTTP = h.Gateway.NewMessagesHTTPHandler()
	}

	mediaHTTP, auxiliaryHTTP, liveHTTP, searchHTTP := h.MediaHTTP, h.AuxiliaryHTTP, h.LiveHTTP, h.SearchHTTP
	if h.OpenAIGateway != nil {
		if mediaHTTP == nil {
			mediaHTTP = h.OpenAIGateway.MediaHTTPHandler()
		}
		if auxiliaryHTTP == nil {
			auxiliaryHTTP = h.OpenAIGateway.AuxiliaryHTTPHandler()
		}
		if liveHTTP == nil {
			liveHTTP = provideLiveHTTP(nil, nil, nil, nil, nil)
		}
	}
	if searchHTTP == nil && h.Gateway != nil {
		searchHTTP = gatewayhttp.NewSearchHandler(gatewayhttp.SearchPorts{})
	}

	qoderChat := gin.HandlerFunc(qoderCompatibleHTTP.ChatCompletions)
	if h.QoderChat != nil {
		qoderChat = h.QoderChat.ChatCompletions
	}
	publicUsage := usagehttp.NewPublicUsageHandler(nil, nil, nil, nil, usagehttp.PublicUsageContext{
		Key: keyhttp.GetAPIKeyFromContext,
		Billing: func(c *gin.Context) (*billing.APIKeyBillingContext, bool) {
			return gatewayhttp.GetAPIKeyBillingContext(c)
		},
		Subscription: gatewayhttp.SubscriptionFromContext,
	}, timezone.NewCalendar(time.Local)).Usage
	if h.PublicUsage != nil {
		publicUsage = h.PublicUsage.Usage
	}
	gatewayhttp.RegisterGatewayRoutes(r, gatewayhttp.RouteEndpoints{CountTokens: countTokensHTTP, QoderCompatible: qoderCompatibleHTTP, CompatibleText: compatibleTextHTTP, GeminiNative: geminiNativeHTTP, OpenAIText: openAITextHTTP, OpenAITokens: openAITokensHTTP, ResponsesWS: responsesWSHTTP, Models: modelsHTTP, Messages: messagesHTTP, Media: mediaHTTP, Auxiliary: auxiliaryHTTP, Live: liveHTTP, Search: searchHTTP, PublicUsage: publicUsage, QoderChat: qoderChat}, legacyRouteMiddleware(apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, cfg), func(group *gin.RouterGroup) { batchhttp.RegisterGatewayRoutes(group, h.BatchImage) })
}
