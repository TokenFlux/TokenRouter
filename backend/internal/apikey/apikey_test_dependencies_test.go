package apikey_test

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

// apiKeyTestDependencies 提供测试依赖替身，实际状态由所属模块创建。
type apiKeyTestDependencies struct {
	apiKeyRepo            apikey.APIKeyRepository
	userRepo              identity.UserRepository
	groupRepo             routing.GroupRepository
	userSubRepo           billing.UserSubscriptionRepository
	userGroupRateRepo     billing.UserGroupRateRepository
	teamRepo              team.TeamRepository
	cache                 apikey.APIKeyCache
	cfg                   *config.Config
	concurrencyService    *scheduler.ConcurrencyService
	rateLimitCacheInvalid apikey.RateLimitCacheInvalidator
}

func newAPIKeyTestService(d apiKeyTestDependencies) *apikey.APIKeyService {
	cfg := d.cfg
	if cfg == nil {
		cfg = &config.Config{}
	}
	s := testkit.NewService(d.apiKeyRepo, d.userRepo, d.groupRepo, d.userSubRepo, d.userGroupRateRepo, d.cache, cfg)
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
