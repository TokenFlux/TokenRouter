//go:build wireinject

package app

import (
	anthropicredis "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/rediscache"

	gatewaytransport "github.com/TokenFlux/TokenRouter/internal/gateway/provider/transport"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/google/wire"
)

// 共享上游技术客户端的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var upstreamAssemblyProviders = wire.NewSet(
	wire.Bind(new(httpclient.UpstreamTransport), new(*gatewaytransport.Client)),
	upstreamClientProviders,
	providePrivacyClientFactory,
	anthropicredis.NewFingerprintStore,
)
