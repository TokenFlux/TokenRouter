package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// provideWSConnections 共享平台拨号器并保留连接池的按需启动时点。
func provideWSConnections(cfg *config.Config, manager *lifecycle.Manager) *gatewayhttp.OpenAIWSConnections {
	connections := gatewayhttp.NewOpenAIWSConnections(openAIWSPoolOptions(cfg), openai.NewDefaultWSClientDialer())
	manager.Register(lifecycle.Hook{Name: "OpenAIWSConnections", StartOrder: 990, StopOrder: 10, Stop: func(context.Context) error { connections.Close(); return nil }})
	return connections
}

func openAIWSPoolOptions(cfg *config.Config) *openai.WSPoolOptions {
	if cfg == nil {
		return nil
	}
	options := cfg.Gateway.OpenAIWS
	return &openai.WSPoolOptions{
		MaxConnsPerAccount:                         options.MaxConnsPerAccount,
		DynamicMaxConnsByAccountConcurrencyEnabled: options.DynamicMaxConnsByAccountConcurrencyEnabled,
		ModeRouterV2Enabled:                        options.ModeRouterV2Enabled,
		OAuthMaxConnsFactor:                        options.OAuthMaxConnsFactor,
		APIKeyMaxConnsFactor:                       options.APIKeyMaxConnsFactor,
		MinIdlePerAccount:                          options.MinIdlePerAccount,
		MaxIdlePerAccount:                          options.MaxIdlePerAccount,
		QueueLimitPerConn:                          options.QueueLimitPerConn,
		PoolTargetUtilization:                      options.PoolTargetUtilization,
		PrewarmCooldownMS:                          options.PrewarmCooldownMS,
		DialTimeoutSeconds:                         options.DialTimeoutSeconds,
	}
}
