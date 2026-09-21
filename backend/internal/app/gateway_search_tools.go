package app

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// ProvideGatewaySearchTools 由组合根为请求链持有唯一工具编排器；不创建新 Manager 或配额状态。
func ProvideGatewaySearchTools(gateway *service.GatewayService, settings *search.ConfigService, channels *routing.ChannelService) *searchtools.Emulator {
	runtime := gatewayprovider.NewSearchTools(settings, channels)
	gateway.BindSearchToolsRuntime(runtime)
	return runtime
}

// ProvideGatewaySearchHTTP 可直接替换原 WebSearch/XSearch 路由绑定，沿用旧窄适配端口。
func ProvideGatewaySearchHTTP(old *handler.GatewayHandler, _ *searchtools.Emulator, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.SearchHandler {
	old.BindCompletionRecorder(recorders.Forward)
	result := old.SearchHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}
