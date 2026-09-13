// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	config "github.com/TokenFlux/TokenRouter/internal/config"
	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func NewProxyExitInfoProber(cfg *config.Config) service.ProxyExitInfoProber {
	insecure := false
	allowPrivate := false
	validateResolvedIP := true
	maxResponseBytes := int64(1024 * 1024)
	if cfg != nil {
		insecure = cfg.Security.ProxyProbe.InsecureSkipVerify
		allowPrivate = cfg.Security.URLAllowlist.AllowPrivateHosts
		validateResolvedIP = cfg.Security.URLAllowlist.Enabled
		if cfg.Gateway.ProxyProbeResponseReadMaxBytes > 0 {
			maxResponseBytes = cfg.Gateway.ProxyProbeResponseReadMaxBytes
		}
	}
	// 配置存在时按管理员指定顺序覆盖内置探测端点。
	var configuredTargets []configuredProbeTarget
	if cfg != nil && len(cfg.Security.ProxyProbe.URLs) > 0 {
		configuredTargets = make([]configuredProbeTarget, 0, len(cfg.Security.ProxyProbe.URLs))
		for _, target := range cfg.Security.ProxyProbe.URLs {
			configuredTargets = append(configuredTargets, configuredProbeTarget{
				url:    target.URL,
				parser: target.Parser,
			})
		}
	}

	targets := make([]provider.ProxyProbeTarget, 0, len(configuredTargets))
	for _, target := range configuredTargets {
		targets = append(targets, provider.ProxyProbeTarget{URL: target.url, Parser: target.parser})
	}
	return provider.NewProxyExitInfoProber(provider.ProxyProbeOptions{InsecureSkipVerify: insecure, AllowPrivateHosts: allowPrivate, ValidateResolvedIP: validateResolvedIP, MaxResponseBytes: maxResponseBytes, Targets: targets})
}

type configuredProbeTarget struct {
	url    string
	parser string
}
