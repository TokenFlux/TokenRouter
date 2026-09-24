package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// provideCompactExecutor 投影静态配置，恢复过程不读取完整应用配置。
func provideCompactExecutor(cfg *config.Config) *gatewayhttp.CompactExecutor {
	value := &gatewayhttp.CompactExecutor{}
	if cfg != nil {
		value.Models = provider.CompactModels{Default: cfg.Gateway.OpenAICompactModel}
		value.LogBody = cfg.Gateway.LogUpstreamErrorBody
		value.LogBodyMaxBytes = cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	}
	return value
}
