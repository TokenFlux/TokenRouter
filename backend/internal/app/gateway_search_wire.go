//go:build wireinject

package app

import "github.com/google/wire"

// 搜索能力单独成组，避免向旧聚合装配继续增加依赖。
var gatewaySearchProviders = wire.NewSet(ProvideGatewaySearchTools, ProvideGatewaySearchHTTP, provideGrokSearchExecutor)
