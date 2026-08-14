package service

import (
	"context"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type opsAccountStatsRepoStub struct {
	AccountRepository
	accounts       []Account
	platformFilter string
	groupIDFilter  *int64
}

// ListOpsAccountsForStats 记录轻量查询参数，验证服务不会退回通用分页查询。
func (r *opsAccountStatsRepoStub) ListOpsAccountsForStats(_ context.Context, platformFilter string, groupIDFilter *int64) ([]Account, error) {
	r.platformFilter = platformFilter
	r.groupIDFilter = groupIDFilter
	return r.accounts, nil
}

type opsAccountStatsFallbackRepoStub struct {
	AccountRepository
	platformFilter string
	groupIDFilter  int64
}

// ListWithFilters 模拟尚未实现轻量查询接口的仓储，锁定兼容回退行为。
func (r *opsAccountStatsFallbackRepoStub) ListWithFilters(
	_ context.Context,
	params pagination.PaginationParams,
	platform, _, _, _ string,
	groupID int64,
	_ string,
) ([]Account, *pagination.PaginationResult, error) {
	r.platformFilter = platform
	r.groupIDFilter = groupID
	accounts := []Account{{ID: 1, Name: "account-1"}}
	return accounts, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize, Total: 1}, nil
}

func TestListAllAccountsForOpsUsesLightweightRepository(t *testing.T) {
	groupID := int64(42)
	repo := &opsAccountStatsRepoStub{accounts: []Account{{ID: 1}}}
	service := &OpsService{accountRepo: repo}

	accounts, err := service.listAllAccountsForOps(context.Background(), PlatformOpenAI, &groupID)

	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, PlatformOpenAI, repo.platformFilter)
	require.Same(t, &groupID, repo.groupIDFilter)
}

func TestListAllAccountsForOpsFallbackPassesGroupFilter(t *testing.T) {
	groupID := int64(77)
	repo := &opsAccountStatsFallbackRepoStub{}
	service := &OpsService{accountRepo: repo}

	accounts, err := service.listAllAccountsForOps(context.Background(), PlatformAnthropic, &groupID)

	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, PlatformAnthropic, repo.platformFilter)
	require.Equal(t, groupID, repo.groupIDFilter)
}

func TestGetAccountAvailabilityStatsOnlyAggregatesSelectedGroup(t *testing.T) {
	targetGroupID := int64(7)
	otherGroup := &Group{ID: 8, Name: "其他分组", Platform: PlatformAnthropic}
	targetGroup := &Group{ID: targetGroupID, Name: "目标分组", Platform: PlatformAnthropic}
	repo := &opsAccountStatsRepoStub{accounts: []Account{
		{
			ID:          11,
			Name:        "多分组账号",
			Platform:    PlatformAnthropic,
			Status:      StatusActive,
			Schedulable: true,
			Groups:      []*Group{otherGroup, targetGroup},
		},
	}}
	service := &OpsService{accountRepo: repo}

	_, groups, accounts, _, err := service.GetAccountAvailabilityStats(
		context.Background(),
		PlatformAnthropic,
		&targetGroupID,
	)

	require.NoError(t, err)
	require.Contains(t, groups, targetGroupID)
	require.NotContains(t, groups, otherGroup.ID)
	require.Len(t, groups, 1)
	require.Equal(t, targetGroupID, accounts[11].GroupID)
	require.Equal(t, targetGroup.Name, accounts[11].GroupName)
}
