package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	batchhttp "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// 历史夹具只在构造边界转换 Key 服务，生产签名使用原生实例。
func legacyRouteMiddleware(auth middleware.APIKeyAuthMiddleware, keys *service.APIKeyService, subscriptions *service.SubscriptionService, ops *service.OpsService, settings *service.SettingService, cfg *config.Config) gatewayhttp.RouteMiddleware {
	var native *apikey.APIKeyService
	if keys != nil {
		native = keys.APIKeyService
	}
	value := provideGatewayRouteMiddleware(auth, native, subscriptions, ops, settings, cfg)
	options := gatewayhttp.GroupAssignmentOptions{Access: func(c *gin.Context) gatewayhttp.GroupAssignmentAccess {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil {
			return gatewayhttp.GroupAssignmentAccess{}
		}
		_, noGroup := c.Get(gatewayhttp.CompositeKeyNoGroupContextKey)
		return gatewayhttp.GroupAssignmentAccess{Loaded: true, Assigned: key.GroupID != nil, CompositeNoGroup: key.IsComposite && noGroup}
	}, Rejected: func(c *gin.Context) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned)
		middleware.MarkIngressRejected(c, middleware.IngressRejectGroupUnassigned)
	}}
	options.WriteError = middleware.AnthropicErrorWriter
	value.RequireGroupAnthropic = gatewayhttp.RequireGroupAssignment(settings, options)
	options.WriteError = middleware.GoogleErrorWriter
	value.RequireGroupGoogle = gatewayhttp.RequireGroupAssignment(settings, options)
	return value
}

func RegisterGatewayRoutes(
	r *gin.Engine,
	h *routeTestHandlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	// 生产使用 app 已绑定的目标 handler；手工装配的兼容测试仍复用同一实现。
	countTokensHTTP := h.CountTokensHTTP
	if countTokensHTTP == nil && h.Gateway != nil {
		countTokensHTTP = h.Gateway.NewCountTokensHTTPHandler()
	}
	qoderCompatibleHTTP := h.QoderCompatibleHTTP
	if qoderCompatibleHTTP == nil && h.QoderGateway != nil {
		qoderCompatibleHTTP = h.QoderGateway.NewCompatibleHTTPHandler()
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
		modelsHTTP = h.Gateway.NewModelsHTTPHandler()
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
			liveHTTP = h.OpenAIGateway.NewLiveHTTPHandler()
		}
	}
	if searchHTTP == nil && h.Gateway != nil {
		searchHTTP = h.Gateway.SearchHTTPHandler()
	}

	qoderChat := gin.HandlerFunc(h.QoderGateway.ChatCompletions)
	if h.QoderChat != nil {
		qoderChat = h.QoderChat.ChatCompletions
	}
	publicUsage := gin.HandlerFunc(h.Gateway.Usage)
	if h.PublicUsage != nil {
		publicUsage = h.PublicUsage.Usage
	}
	gatewayhttp.RegisterGatewayRoutes(r, gatewayhttp.RouteEndpoints{CountTokens: countTokensHTTP, QoderCompatible: qoderCompatibleHTTP, CompatibleText: compatibleTextHTTP, GeminiNative: geminiNativeHTTP, OpenAIText: openAITextHTTP, ResponsesWS: responsesWSHTTP, Models: modelsHTTP, Messages: messagesHTTP, Media: mediaHTTP, Auxiliary: auxiliaryHTTP, Live: liveHTTP, Search: searchHTTP, PublicUsage: publicUsage, QoderChat: qoderChat}, legacyRouteMiddleware(apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, cfg), func(group *gin.RouterGroup) { batchhttp.RegisterGatewayRoutes(group, h.BatchImage) })
}
