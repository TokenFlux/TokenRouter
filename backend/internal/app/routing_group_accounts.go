package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// routingGroupAccounts 投影账号存储；平台默认目录通过按需查询端口提供。
type routingGroupAccounts struct {
	Store    *accountpostgres.AccountStore
	Defaults account.ModelMappingDefaults
}

func (r routingGroupAccounts) GetByIDs(ctx context.Context, ids []int64) ([]routing.GroupAccount, error) {
	values, err := r.Store.GetByIDs(ctx, ids)
	// 原批量入口将缺省结果投影为空数组，保留与列表入口的 nil 差异。
	out := make([]routing.GroupAccount, len(values))
	for i, v := range values {
		out[i] = r.project(v)
	}
	return out, err
}
func (r routingGroupAccounts) ListSchedulableByGroupID(ctx context.Context, id int64) ([]routing.GroupAccount, error) {
	values, err := r.Store.ListSchedulableByGroupID(ctx, id)
	if values == nil {
		return nil, err
	}
	out := make([]routing.GroupAccount, len(values))
	for i := range values {
		out[i] = r.project(&values[i])
	}
	return out, err
}
func (r routingGroupAccounts) project(v *account.Record) routing.GroupAccount {
	return routing.GroupAccount{ID: v.ID, Platform: v.Platform, Type: v.Type, Models: v.GetConfiguredRequestModels(r.Defaults)}
}
