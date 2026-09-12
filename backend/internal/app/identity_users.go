// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	site "github.com/TokenFlux/TokenRouter/internal/site"
)

// provideLegacyUserRepository 只为尚未迁出的消费者恢复旧模型形状。
func provideLegacyUserRepository(client *dbent.Client, db *sql.DB, users *identitypostgres.UserStore) service.UserRepository {
	return repository.WrapUserStore(client, db, users)
}

// announcementUsers 直接投影 identity，替换 S02 的旧身份桥接。
type announcementUsers struct{ Repository identity.UserRepository }

func provideAnnouncementUsers(users *identitypostgres.UserStore) site.UserReader {
	return &announcementUsers{users}
}
func (a *announcementUsers) GetByID(ctx context.Context, id int64) (*site.UserSnapshot, error) {
	u, e := a.Repository.GetByID(ctx, id)
	if e != nil || u == nil {
		return nil, e
	}
	return &site.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username, Balance: u.Balance}, nil
}
func (a *announcementUsers) ListWithFilters(ctx context.Context, p pagination.PaginationParams, f site.UserListFilters) ([]site.UserSnapshot, *pagination.PaginationResult, error) {
	users, page, e := a.Repository.ListWithFilters(ctx, p, identity.UserListFilters{Search: f.Search})
	if e != nil {
		return nil, page, e
	}
	out := make([]site.UserSnapshot, len(users))
	for i, u := range users {
		out[i] = site.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username, Balance: u.Balance}
	}
	return out, page, nil
}

// billingIdentityUsers 直接提供资金用例所需身份投影，查询顺序与原接口一致。
type billingIdentityUsers struct{ Repository identity.UserRepository }

func (b billingIdentityUsers) GetByID(ctx context.Context, id int64) (*billing.UserSummary, error) {
	u, e := b.Repository.GetByID(ctx, id)
	return billingIdentitySummary(u), e
}

// BillingUserSummary 将旧用户投影为权益只读数据，S05 后由身份接口直接提供。
func billingIdentitySummary(u *identity.User) *billing.UserSummary {
	if u == nil {
		return nil
	}
	out := &billing.UserSummary{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		AllowedGroups:              u.AllowedGroups,
		DisabledPublicGroups:       u.DisabledPublicGroups,
		LastActiveAt:               u.LastActiveAt,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RPMLimit,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt,
	}
	if u.BalanceNotifyExtraEmails != nil {
		out.BalanceNotifyExtraEmails = make([]billing.NotifyEmailSummary, len(u.BalanceNotifyExtraEmails))
		copy(out.BalanceNotifyExtraEmails, u.BalanceNotifyExtraEmails)
	}
	return out
}
