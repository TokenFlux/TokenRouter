package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// provideGatewayRuntimeReaders 只绑定现有唯一读取器，不提前读取请求动态设置。
func provideGatewayRuntimeReaders(store *settings.Store, gatewayRuntime *gateway.RuntimeSettings, accountRuntime *account.RuntimeSettings, quota *account.QuotaSettingsCache, routingRuntime *routing.RuntimeSettings, moderationRuntime *moderation.RuntimeSettings, searchRuntime *search.ConfigService) *gatewayprovider.RuntimeReaders {
	antigravity.SetUserAgentVersionResolver(gatewayRuntime.GetAntigravityUserAgentVersion)
	openai.SetCodexCanonicalUserAgentResolver(func() string { return gatewayRuntime.GetOpenAICodexUserAgent(context.Background()) })
	readers := &gatewayprovider.RuntimeReaders{Gateway: gatewayRuntime, Account: accountRuntime, Quota: quota, Routing: routingRuntime, Moderation: moderationRuntime, Search: searchRuntime, Scheduler: store}
	return readers
}

// provideModerationSettings 延续网关 cyber 热路径的独立缓存作用域与唯一生产实例。
func provideModerationSettings(store *settings.Store) *moderation.RuntimeSettings {
	return moderation.NewRuntimeSettings(store, settings.ErrSettingNotFound)
}
