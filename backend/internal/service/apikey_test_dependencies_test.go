package service

import "github.com/TokenFlux/TokenRouter/internal/config"

// apiKeyTestDependencies 保留旧测试的依赖替身，实际状态只在新模块中创建。
type apiKeyTestDependencies struct {
	apiKeyRepo            APIKeyRepository
	userRepo              UserRepository
	groupRepo             GroupRepository
	userSubRepo           UserSubscriptionRepository
	userGroupRateRepo     UserGroupRateRepository
	teamRepo              TeamRepository
	cache                 APIKeyCache
	cfg                   *config.Config
	concurrencyService    *ConcurrencyService
	rateLimitCacheInvalid RateLimitCacheInvalidator
}

func newAPIKeyTestService(d apiKeyTestDependencies) *APIKeyService {
	cfg := d.cfg
	if cfg == nil {
		cfg = &config.Config{}
	}
	s := NewAPIKeyService(d.apiKeyRepo, d.userRepo, d.groupRepo, d.userSubRepo, d.userGroupRateRepo, d.cache, cfg)
	if d.teamRepo != nil {
		s.SetTeamRepository(d.teamRepo)
	}
	if d.concurrencyService != nil {
		s.SetConcurrencyService(d.concurrencyService)
	}
	if d.rateLimitCacheInvalid != nil {
		s.SetRateLimitCacheInvalidator(d.rateLimitCacheInvalid)
	}
	return s
}
