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

// gateway_systemone.go constructs SystemOne decision execution with transport,
// pricing checks, and request activity.
// SystemOne decisions use the common transport, provider health, and pricing
// preflight. Destination policy and read limits come from startup
// configuration, with the allowed-host slice copied into the executor and
// request activity shared with other gateway operations.

// provideSystemOneExecutor binds decision execution to transport, price checks,
// and request activity. activity is required; nil cfg selects the default
// response-read limit, while positive configured bytes and copied URL-policy
// hosts apply when supplied.
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
		if cfg.Gateway.UpstreamResponseMaxBytes > 0 {
			executor.ReadLimit = cfg.Gateway.UpstreamResponseMaxBytes
		}
	}
	return executor
}
