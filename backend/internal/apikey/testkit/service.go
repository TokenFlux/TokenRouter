package testkit

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// service.go constructs API key services for tests using deployment cache
// settings and routing policy.
// Cross-module authentication tests use deployment configuration but construct
// the same Key service as production. The adapter copies cache and team startup
// values into Options, leaves its lifecycle unstarted for the caller, and binds
// routing's group Fast normalizer before test requests begin.

// groupSource adapts routing's group reader methods to the authentication
// dependency.
type groupSource struct {
	routing.GroupRepository // shared fixture group repository
}

// NewService constructs a Key service from supplied test dependencies and
// copied deployment values. Nil groups leave the group reader absent, and nil
// configuration leaves Options absent. Callers bind additional dependencies,
// start the service when resources are needed, and stop it after use; the
// constructor binds routing's Fast policy resolver before return.
func NewService(keys apikey.APIKeyRepository, users identity.UserRepository, groups routing.GroupRepository, subs billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository, cache apikey.APIKeyCache, cfg *config.Config) *apikey.APIKeyService {
	var groupReader apikey.GroupRepository
	if groups != nil {
		groupReader = groupSource{groups}
	}
	var options *apikey.Options
	if cfg != nil {
		options = &apikey.Options{APIKeyAuth: apikey.APIKeyAuthCacheConfig{
			L1Size: cfg.APIKey.AuthCache.L1Size, L1TTLSeconds: cfg.APIKey.AuthCache.L1TTLSeconds,
			L2TTLSeconds: cfg.APIKey.AuthCache.L2TTLSeconds, NegativeTTLSeconds: cfg.APIKey.AuthCache.NegativeTTLSeconds,
			JitterPercent: cfg.APIKey.AuthCache.JitterPercent, Singleflight: cfg.APIKey.AuthCache.Singleflight,
			LookupConcurrency: cfg.APIKey.LookupConcurrency,
			InvalidAbuse:      apikey.InvalidAuthAbuseConfig(cfg.APIKey.InvalidKeyAbuse),
		}}
		options.Default.APIKeyPrefix = cfg.APIKey.Prefix
		options.Team.Enabled = cfg.Team.Enabled
	}
	core := apikey.NewAPIKeyService(keys, users, groupReader, subs, rates, cache, options)
	core.SetGroupFastPolicy(func(raw string, force bool) string {
		return (&routing.Group{OpenAIFastPolicy: raw, ForceOpenAIFast: force}).EffectiveOpenAIFastPolicy()
	})
	return core
}
