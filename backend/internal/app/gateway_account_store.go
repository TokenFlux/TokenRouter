// 执行账号适配只引用原生存储，事务、事件、缓存和资金规则均由其实际拥有者执行。
package app

import (
	context "context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// executionAccountStore 不拥有连接或备用构造路径，只引用原生存储。
type executionAccountStore struct {
	usage *billingpostgres.AccountUsageStore
	data  *accountpostgres.AccountStore
}

func (r *executionAccountStore) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.GetByID(ctx, id)
	return gatewayprovider.NewExecutionAccount(v), err
}

func (r *executionAccountStore) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionAccount, error) {
	values, err := r.data.GetByIDs(ctx, ids)
	if values == nil {
		return nil, err
	}
	out := make([]*gatewayprovider.ExecutionAccount, len(values))
	for i := range values {
		out[i] = gatewayprovider.NewExecutionAccount(values[i])
	}
	return out, err
}

func (r *executionAccountStore) ListOpsAccountsForStats(ctx context.Context, platformFilter string, groupIDFilter *int64) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListOpsAccountsForStats(ctx, platformFilter, groupIDFilter)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListByPlatform(ctx, platform)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulable(ctx context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulable(ctx)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulableAccountLoads(ctx context.Context) ([]scheduler.AccountWithConcurrency, error) {
	rows, err := r.data.ListSchedulableAccountLoads(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.AccountWithConcurrency, len(rows))
	for i, v := range rows {
		out[i] = scheduler.AccountWithConcurrency{ID: v.ID, MaxConcurrency: v.MaxConcurrency}
	}
	return out, nil
}

func (r *executionAccountStore) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulableByGroupID(ctx, groupID)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulableCapacityByGroupIDs(ctx context.Context, groupIDs []int64) ([]accountcore.GroupAccountCapacityRow, error) {
	return r.data.ListSchedulableCapacityByGroupIDs(ctx, groupIDs)
}

func (r *executionAccountStore) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulableByPlatform(ctx, platform)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulableByGroupIDAndPlatform(ctx, groupID, platform)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulableByPlatforms(ctx, platforms)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulableUngroupedByPlatform(ctx, platform)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulableUngroupedByPlatforms(ctx, platforms)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListSchedulableByGroupIDAndPlatforms(ctx, groupID, platforms)
	return gatewayprovider.ExecutionAccounts(v), err
}

func (r *executionAccountStore) ListModelAvailabilityCandidates(
	ctx context.Context,
	groupID *int64,
	platforms []string,
	includeGrouped bool,
) ([]gatewayprovider.ExecutionAccount, error) {
	v, err := r.data.ListModelAvailabilityCandidates(ctx, groupID, platforms, includeGrouped)
	return gatewayprovider.ExecutionAccounts(v), err
}
