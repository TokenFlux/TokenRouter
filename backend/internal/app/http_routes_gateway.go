package app

import (
	batchhttp "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	native_usage_httpapi "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	gin "github.com/gin-gonic/gin"
)

// provideGatewayRouteMount 在构造时固定原生端点，不再在注册时创建旧 handler。
func provideGatewayRouteMount(eCountTokensHTTP *gatewayhttp.CountTokensHandler,
	eQoderCompatibleHTTP *gatewayhttp.QoderCompatibleHandler,
	eCompatibleTextHTTP *gatewayhttp.CompatibleTextHandler,
	eGeminiNativeHTTP *gatewayhttp.GeminiNativeHandler,
	eOpenAITextHTTP *gatewayhttp.OpenAITextHandler,
	eResponsesWSHTTP *gatewayhttp.ResponsesWSHandler,
	eModelsHTTP *gatewayhttp.ModelsHandler,
	eMessagesHTTP *gatewayhttp.MessagesHandler,
	eMediaHTTP *gatewayhttp.MediaHandler,
	eAuxiliaryHTTP *gatewayhttp.AuxiliaryHandler,
	eLiveHTTP *gatewayhttp.LiveHandler,
	eSearchHTTP *gatewayhttp.SearchHandler,
	ePublicUsage *native_usage_httpapi.PublicUsageHandler,
	eQoderChat *gatewayhttp.QoderChatHandler,
	batch *batchhttp.BatchImageHandler,
	options gatewayhttp.RouteMiddleware) gatewayRouteMount {
	return func(r *gin.Engine) {
		gatewayhttp.RegisterGatewayRoutes(r, gatewayhttp.RouteEndpoints{CountTokens: eCountTokensHTTP, QoderCompatible: eQoderCompatibleHTTP, CompatibleText: eCompatibleTextHTTP, GeminiNative: eGeminiNativeHTTP, OpenAIText: eOpenAITextHTTP, ResponsesWS: eResponsesWSHTTP, Models: eModelsHTTP, Messages: eMessagesHTTP, Media: eMediaHTTP, Auxiliary: eAuxiliaryHTTP, Live: eLiveHTTP, Search: eSearchHTTP, PublicUsage: ePublicUsage.Usage, QoderChat: eQoderChat.ChatCompletions}, options, func(group *gin.RouterGroup) { batchhttp.RegisterGatewayRoutes(group, batch) })
	}
}
