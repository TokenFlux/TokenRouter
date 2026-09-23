package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// 旧入口只投影静态策略与原共享传输，供应商请求由原生 provider 唯一实现。
func grokOperatorPolicyValidator(cfg *config.Config) grok.BaseURLValidator {
	if cfg == nil {
		return grok.ValidateBaseURL
	}
	policy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: cfg.Security.URLAllowlist.UpstreamHosts}
	return policy.Validate
}

func grokBaseURLValidator(value *gatewayprovider.ExecutionAccount, cfg *config.Config) (grok.BaseURLValidator, error) {
	return provider.GrokBaseURLValidator(gatewayprovider.ExecutionRecord(value), grokOperatorPolicyValidator(cfg))
}
