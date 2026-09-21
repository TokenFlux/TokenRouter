// Package testkit 只装配认证回归夹具，不持有业务规则或额外运行状态。
package testkit

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// NewService 使跨模块旧夹具直接构造原生认证服务；生产装配仍由 app 注入。
func NewService(keys apikey.APIKeyRepository, users identity.UserRepository, groups routing.GroupRepository, subs billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository, cache apikey.APIKeyCache, cfg *config.Config) *apikey.APIKeyService {
	var groupReader apikey.GroupRepository
	if groups != nil {
		groupReader = groupSource{groups}
	}
	var options *apikey.Options
	if cfg != nil {
		options = &apikey.Options{APIKeyAuth: apikey.APIKeyAuthCacheConfig{
			L1Size: cfg.APIKeyAuth.L1Size, L1TTLSeconds: cfg.APIKeyAuth.L1TTLSeconds,
			L2TTLSeconds: cfg.APIKeyAuth.L2TTLSeconds, NegativeTTLSeconds: cfg.APIKeyAuth.NegativeTTLSeconds,
			JitterPercent: cfg.APIKeyAuth.JitterPercent, Singleflight: cfg.APIKeyAuth.Singleflight,
			LookupConcurrency: cfg.APIKeyAuth.LookupConcurrency,
			InvalidAbuse:      apikey.InvalidAuthAbuseConfig(cfg.APIKeyAuth.InvalidAbuse),
		}}
		options.Default.APIKeyPrefix = cfg.Default.APIKeyPrefix
		options.Team.Enabled = cfg.Team.Enabled
	}
	core := apikey.NewAPIKeyService(keys, users, groupReader, subs, rates, cache, options)
	core.SetGroupFastPolicy(func(raw string, force bool) string {
		return (&routing.Group{OpenAIFastPolicy: raw, ForceOpenAIFast: force}).EffectiveOpenAIFastPolicy()
	})
	return core
}

// groupSource 只补充原生分组接口的默认组读取，选择规则仍由 routing 拥有。
type groupSource struct{ routing.GroupRepository }

func (g groupSource) FindDefault(ctx context.Context, platform string) (*routing.Group, error) {
	return routing.FindPlatformDefaultGroup(ctx, g.GroupRepository, platform)
}
