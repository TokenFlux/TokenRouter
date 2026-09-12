// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	"context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

// keyAdministration 只投影旧管理聚合依赖，Key 判断与状态由新模块持有。
func (s *adminServiceImpl) keyAdministration() *apikey.Admin {
	if s.keyAdmin != nil {
		return s.keyAdmin
	}
	keys := KeyRepositoryView(s.apiKeyRepo)
	mut := &keypostgres.AdminGroupMutations{Client: s.entClient, Keys: keys, Users: s.userRepo, Observer: logger.LegacyPrintf}
	if _, ok := s.userRepo.(interface {
		IdentityRepository() identity.UserRepository
	}); ok {
		mut.UsersInTx = func(tx *dbent.Tx) keypostgres.GroupAccessWriter { return identitypostgres.GroupAccessInTx(tx) }
	}
	var groups apikey.GroupRepository
	if s.groupRepo != nil {
		groups = legacyKeyGroups{s.groupRepo}
	}
	out := &apikey.Admin{Keys: keys, Users: IdentityRepository(s.userRepo), Groups: groups, Mutations: mut, Invalidator: s.authCacheInvalidator}
	if s.billingCacheService != nil {
		out.RateLimits = s.billingCacheService
	}
	return out
}

// AdminUpdateAPIKeyFields 为旧装配提供原子管理入口，不保留第二份规则。
func (s *adminServiceImpl) AdminUpdateAPIKeyFields(ctx context.Context, id int64, gid *int64, reset bool) (*AdminUpdateAPIKeyGroupIDResult, error) {
	v, e := s.keyAdministration().UpdateManagedFields(ctx, id, gid, reset)
	if v == nil {
		return nil, e
	}
	return &AdminUpdateAPIKeyGroupIDResult{APIKey: APIKeyFromView(v.APIKey), AutoGrantedGroupAccess: v.AutoGrantedGroupAccess, GrantedGroupID: v.GrantedGroupID, GrantedGroupName: v.GrantedGroupName}, e
}
func (s *adminServiceImpl) KeyAdministration() *apikey.Admin { return s.keyAdministration() }
