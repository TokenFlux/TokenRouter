package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/server"
)

// provideHTTPOptions 在组合根固化监听参数及请求体上限的原优先级。
func provideHTTPOptions(cfg *config.Config) server.Options {
	maxBody := cfg.Server.MaxRequestBodySize
	if maxBody <= 0 {
		maxBody = cfg.Gateway.MaxBodySize
	}
	h := cfg.Server.H2C
	return server.Options{Address: cfg.Server.Address(), Mode: cfg.Server.Mode, TrustedProxies: append([]string(nil), cfg.Server.TrustedProxies...), TrustedProxiesConfigured: cfg.Server.TrustedProxiesConfigured,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout, IdleTimeout: cfg.Server.IdleTimeout, MaxHeaderBytes: cfg.Server.MaxHeaderBytes, MaxRequestBodySize: maxBody,
		H2C: server.H2COptions{Enabled: h.Enabled, MaxConcurrentStreams: h.MaxConcurrentStreams, IdleTimeout: h.IdleTimeout, MaxReadFrameSize: h.MaxReadFrameSize, MaxUploadBufferPerConnection: h.MaxUploadBufferPerConnection, MaxUploadBufferPerStream: h.MaxUploadBufferPerStream}}
}
