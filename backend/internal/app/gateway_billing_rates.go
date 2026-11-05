package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// gateway_billing_rates.go constructs and expires the rate caches used by
// gateway completion recorders.
// Gateway completion recording uses separate user-group rate resolvers for
// forward and OpenAI execution. They read the same rate repository with the
// selected TTL, while timing-wheel cleanup sweeps both independent caches
// on periodic cleanup ticks.

// gatewayBillingRates holds the two rate resolvers selected by completion recording.
type gatewayBillingRates struct {
	// Forward and OpenAI use the same repository and TTL but own separate caches
	// and singleflight groups. Completion recording chooses its execution family's
	// resolver, and application cleanup sweeps both caches.
	Forward *billing.GroupRateResolver // general completion rates
	OpenAI  *billing.GroupRateResolver // OpenAI completion rates
}

// provideGatewayBillingRates constructs the completion families' separate
// resolvers with one repository and TTL setting. Missing or nonpositive TTL
// configuration selects the billing default; a nil repository leaves each
// resolver using group-default multipliers.
func provideGatewayBillingRates(repo billing.UserGroupRateRepository, cfg *config.Config) *gatewayBillingRates {
	ttl := gatewayGroupRateCacheTTL(cfg)
	return &gatewayBillingRates{
		Forward: billing.NewGroupRateResolver(repo, nil, ttl, nil, "service.gateway", logging.LegacyPrintf),
		OpenAI:  billing.NewGroupRateResolver(repo, nil, ttl, nil, "service.openai_gateway", logging.LegacyPrintf),
	}
}

// gatewayGroupRateCacheTTL returns the configured rate-cache lifetime as a
// duration. A nil configuration or a nonpositive seconds value selects the
// billing default lifetime used when constructing both completion resolvers.
func gatewayGroupRateCacheTTL(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Billing.GroupRateCacheTTLSeconds <= 0 {
		return billing.DefaultGroupRateCacheTTL
	}
	// Resolver duration units come from the deployment field's configured seconds.
	return time.Duration(cfg.Billing.GroupRateCacheTTLSeconds) * time.Second
}

// Expire sweeps elapsed entries from both completion resolvers for the
// application timing wheel. A nil receiver has no effect; the underlying
// resolver also tolerates a missing cache when cleanup runs.
func (r *gatewayBillingRates) Expire() {
	if r != nil {
		// Both protocol families use distinct cache instances. Sweep them in the
		// same timer callback so each completion resolver releases its expired entries.
		r.Forward.DeleteExpired()
		r.OpenAI.DeleteExpired()
	}
}
