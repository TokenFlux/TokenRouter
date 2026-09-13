package ops

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type opsAccountStatsRepoStub struct {
	AccountReader
	accounts       []AccountObservation
	platformFilter string
	groupIDFilter  *int64
}

// ListOpsAccountsForStats 记录轻量查询参数，验证服务不会退回通用分页查询。
func (r *opsAccountStatsRepoStub) ListOpsAccountsForStats(_ context.Context, platformFilter string, groupIDFilter *int64) ([]AccountObservation, error) {
	r.platformFilter = platformFilter
	r.groupIDFilter = groupIDFilter
	return r.accounts, nil
}

type opsAccountStatsFallbackRepoStub struct {
	AccountReader
	platformFilter string
	groupIDFilter  int64
}

// ListWithFilters 模拟尚未实现轻量查询接口的仓储，锁定兼容回退行为。
func (r *opsAccountStatsFallbackRepoStub) ListPage(
	_ context.Context,
	params pagination.PaginationParams,
	platform string,
	groupID int64,
) ([]AccountObservation, *pagination.PaginationResult, error) {
	r.platformFilter = platform
	r.groupIDFilter = groupID
	accounts := []AccountObservation{{ID: 1, Name: "account-1"}}
	return accounts, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize, Total: 1}, nil
}

func TestListAllAccountsForOpsUsesLightweightRepository(t *testing.T) {
	groupID := int64(42)
	repo := &opsAccountStatsRepoStub{accounts: []AccountObservation{{ID: 1}}}
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
	otherGroup := &GroupObservation{ID: 8, Name: "其他分组", Platform: PlatformAnthropic}
	targetGroup := &GroupObservation{ID: targetGroupID, Name: "目标分组", Platform: PlatformAnthropic}
	repo := &opsAccountStatsRepoStub{accounts: []AccountObservation{
		{
			ID:          11,
			Name:        "多分组账号",
			Platform:    PlatformAnthropic,
			Status:      StatusActive,
			Schedulable: true,
			Groups:      []*GroupObservation{otherGroup, targetGroup},
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
