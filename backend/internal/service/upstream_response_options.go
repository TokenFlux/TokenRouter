package service

import "github.com/TokenFlux/TokenRouter/internal/config"

// 仅为剩余执行器投影原配置值；技术读取和 HTTP 错误处理已归实际所有者。
func resolveUpstreamResponseReadLimit(cfg *config.Config) int64 {
	if cfg != nil && cfg.Gateway.UpstreamResponseReadMaxBytes > 0 {
		return cfg.Gateway.UpstreamResponseReadMaxBytes
	}
	return config.DefaultUpstreamResponseReadMaxBytes
}
