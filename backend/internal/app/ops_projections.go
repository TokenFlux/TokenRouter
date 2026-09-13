// Ops 直接绑定新账号/身份模块的只读查询，不持有业务缓存。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

type opsAccounts struct{ store *accountpostgres.AccountStore }

func (a opsAccounts) ListPage(ctx context.Context, p pagination.PaginationParams, platform string, group int64) ([]ops.AccountObservation, *pagination.PaginationResult, error) {
	v, pg, e := a.store.ListWithFilters(ctx, p, platform, "", "", "", group, "")
	return opsAccountViews(v), pg, e
}
func (a opsAccounts) ListOpsAccountsForStats(ctx context.Context, platform string, g *int64) ([]ops.AccountObservation, error) {
	v, e := a.store.ListOpsAccountsForStats(ctx, platform, g)
	return opsAccountViews(v), e
}
func (a opsAccounts) ListSchedulable(ctx context.Context) ([]ops.AccountObservation, error) {
	v, e := a.store.ListSchedulable(ctx)
	return opsAccountViews(v), e
}
func (a opsAccounts) ListSchedulableAccountLoads(ctx context.Context) ([]ops.AccountWithConcurrency, error) {
	v, e := a.store.ListSchedulableAccountLoads(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]ops.AccountWithConcurrency, len(v))
	for i, a := range v {
		out[i] = ops.AccountWithConcurrency{ID: a.ID, MaxConcurrency: a.MaxConcurrency}
	}
	return out, nil
}
func opsAccountViews(a []account.Record) []ops.AccountObservation {
	out := make([]ops.AccountObservation, len(a))
	for i, v := range a {
		out[i] = ops.AccountObservation{ID: v.ID, Name: v.Name, Platform: v.Platform, Status: v.Status, ErrorMessage: v.ErrorMessage, Schedulable: v.Schedulable, Concurrency: v.Concurrency, LoadFactor: v.EffectiveLoadFactor(), TempUnschedulableUntil: v.TempUnschedulableUntil, RateLimitResetAt: v.RateLimitResetAt, OverloadUntil: v.OverloadUntil}
		if v.Groups != nil {
			out[i].Groups = make([]*ops.GroupObservation, len(v.Groups))
			for j, g := range v.Groups {
				if g != nil {
					out[i].Groups[j] = &ops.GroupObservation{ID: g.ID, Name: g.Name, Platform: g.Platform}
				}
			}
		}
	}
	return querycache.Clone(out)
}

type opsUsers struct{ store *identitypostgres.UserStore }

func (a opsUsers) ListActivePage(ctx context.Context, p pagination.PaginationParams) ([]ops.UserObservation, *pagination.PaginationResult, error) {
	v, pg, e := a.store.ListWithFilters(ctx, p, identity.UserListFilters{Status: "active"})
	out := make([]ops.UserObservation, len(v))
	for i, u := range v {
		out[i] = ops.UserObservation{ID: u.ID, Email: u.Email, Username: u.Username, Concurrency: u.Concurrency}
	}
	return out, pg, e
}
func (a opsUsers) GetFirstAdmin(ctx context.Context) (*ops.UserObservation, error) {
	u, e := a.store.GetFirstAdmin(ctx)
	if u == nil {
		return nil, e
	}
	return &ops.UserObservation{ID: u.ID, Email: u.Email, Username: u.Username, Concurrency: u.Concurrency}, e
}
