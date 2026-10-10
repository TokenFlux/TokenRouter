package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
)

// provideSystemOneExecutor 为决策请求绑定共享传输、价格预检和提供商健康组件。
func provideSystemOneExecutor(cfg *config.Config, transport httpclient.UpstreamTransport, headers *egress.CompiledHeaderFilter, health *provideradapter.UpstreamHealth, prices *billing.PriceResolver, activity *gatewayRequestActivity) *gatewayhttp.SystemOneExecutor {
	executor := &gatewayhttp.SystemOneExecutor{
		Transport:    transport,
		HeaderFilter: headers,
		Health:       health,
		Pricing:      &admission.ModelPricing{Resolver: prices},
		ReadLimit:    config.DefaultUpstreamResponseReadMaxBytes,
		Enter:        activity.Enter,
	}
	if cfg != nil {
		policy := cfg.Security.URLAllowlist
		executor.URLPolicy = egress.OperatorURLPolicy{
			Enabled:           policy.Enabled,
			AllowInsecureHTTP: policy.AllowInsecureHTTP,
			AllowPrivateHosts: policy.AllowPrivateHosts,
			UpstreamHosts:     append([]string(nil), policy.UpstreamHosts...),
		}
		if cfg.Gateway.UpstreamResponseReadMaxBytes > 0 {
			executor.ReadLimit = cfg.Gateway.UpstreamResponseReadMaxBytes
		}
	}
	return executor
}
