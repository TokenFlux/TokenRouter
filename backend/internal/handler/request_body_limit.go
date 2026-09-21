package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// gatewayMaxBodySize 读取网关配置的请求体上限，空配置由 httputil 使用保守默认值。
func gatewayMaxBodySize(cfg *config.Config) int64 {
	if cfg == nil {
		return 0
	}
	return cfg.Gateway.MaxBodySize
}
