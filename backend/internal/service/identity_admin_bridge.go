// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

type legacyAdminGroups struct{ GroupRepository }

func (s legacyAdminGroups) GetByID(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	g, e := s.GroupRepository.GetByID(ctx, id)
	return adminGroupProjection(g), e
}
func (s legacyAdminGroups) GetByIDLite(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	g, e := s.GroupRepository.GetByIDLite(ctx, id)
	return adminGroupProjection(g), e
}
func adminGroupProjection(g *Group) *identity.AdminGroup {
	if g == nil {
		return nil
	}
	return &identity.AdminGroup{ID: g.ID, Name: g.Name, Status: g.Status, IsExclusive: g.IsExclusive, RPMLimit: g.RPMLimit}
}

type legacyAdminKeys struct{ APIKeyRepository }

func (s legacyAdminKeys) List(ctx context.Context, id int64, page, size int, sortBy, order string) ([]identity.AdminKeySummary, int64, error) {
	keys, p, e := s.ListByUserID(ctx, id, pagination.PaginationParams{Page: page, PageSize: size, SortBy: sortBy, SortOrder: order}, APIKeyListFilters{})
	if e != nil {
		return nil, 0, e
	}
	out := adminKeySummaries(keys)
	if p == nil {
		return out, 0, nil
	}
	return out, p.Total, nil
}
func adminKeySummaries(keys []APIKey) []identity.AdminKeySummary {
	if keys == nil {
		return nil
	}
	out := make([]identity.AdminKeySummary, len(keys))
	for i, k := range keys {
		out[i] = identity.AdminKeySummary{ID: k.ID, Key: k.Key, GroupID: k.GroupID}
	}
	return out
}

// identityAdministration 只为兼容调用者投影，生产构造后固定同一个实例。
func (s *adminServiceImpl) identityAdministration() *identity.UserAdmin {
	if s.identityAdmin != nil {
		return s.identityAdmin
	}
	d := identity.AdminDependencies{Users: IdentityRepository(s.userRepo), Rates: s.userGroupRateRepo, RPM: s.userRPMCache, Subscriptions: s.defaultSubAssigner, Balances: s.balanceAdjuster(), Records: s.redeemAdministration(), Invalidator: s.authCacheInvalidator, Observer: identity.Observer{Log: logger.LegacyPrintf}, Background: func(name string, fn func()) bool { return RunBackgroundTask(name, BackgroundCall0(fn)) }}
	if s.billingCacheService != nil {
		d.BalanceCache = s.billingCacheService
	}
	if s.affiliateService != nil {
		d.Affiliates = s.affiliateService
	}
	if s.settingService != nil {
		d.Settings = s.settingService
	}
	if s.groupRepo != nil {
		d.Groups = legacyAdminGroups{s.groupRepo}
	}
	if s.apiKeyRepo != nil {
		d.Keys = legacyAdminKeys{s.apiKeyRepo}
	}
	tx := &identitypostgres.AdminMutations{Client: s.entClient, Users: d.Users, Keys: s.apiKeyRepo, Observer: d.Observer}
	if actual, ok := s.apiKeyRepo.(interface{ APIKeyRepository() *keypostgres.KeyStore }); ok {
		tx.KeysInTx = func(t *dbent.Tx) identity.AdminKeyParticipant { return actual.APIKeyRepository().LifecycleInTx(t) }
	}
	d.Transactions = tx
	return identity.NewUserAdmin(d)
}

// IdentityAdministration 交付唯一生产用户管理用例，供 app 与过渡 HTTP 使用。
func (s *adminServiceImpl) IdentityAdministration() *identity.UserAdmin {
	return s.identityAdministration()
}
