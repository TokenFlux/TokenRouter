package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

type groupClientProtocolErrorFormat = gatewayhttp.GroupClientProtocolErrorFormat

const (
	groupClientProtocolErrorAnthropic = gatewayhttp.GroupClientProtocolErrorAnthropic
	groupClientProtocolErrorOpenAI    = gatewayhttp.GroupClientProtocolErrorOpenAI
	groupClientProtocolErrorGoogle    = gatewayhttp.GroupClientProtocolErrorGoogle
)

var routeProtocol = gatewayhttp.RouteProtocol

// 测试门禁只持有无状态适配函数，每次检查仍读取当前请求投影。
var testRouteGuards = gatewayhttp.NewRouteGuards(legacyRouteMiddleware(nil, nil, nil, nil, nil, &config.Config{}))
var requireGroupClientProtocol = testRouteGuards.RequireGroupClientProtocol
var extendedRouteProtocol = gatewayhttp.ExtendedRouteProtocol
var requireGeminiGenerateContentProtocol = testRouteGuards.RequireGeminiGenerateContentProtocol
