//go:build unit

// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	require "github.com/stretchr/testify/require"
	testing "testing"
)

// groupRepoStubForAdmin 用于测试 AdminService 的 GroupRepository Stub
type groupRepoStubForAdmin struct {
	created *Group // 记录 Create 调用的参数
	updated *Group // 记录 Update 调用的参数
	getByID *Group // GetByID 返回值
	getErr  error  // GetByID 返回的错误

	listWithFiltersCalls       int
	listWithFiltersParams      pagination.PaginationParams
	listWithFiltersPlatform    string
	listWithFiltersStatus      string
	listWithFiltersSearch      string
	listWithFiltersIsExclusive *bool
	listWithFiltersGroups      []Group
	listWithFiltersResult      *pagination.PaginationResult
	listWithFiltersErr         error
	groupSortOrderLockCalls    int
}

type groupAccountCopyRepoStub struct {
	*groupRepoStubForAdmin
	groupsByID       map[int64]*Group
	sourceAccountIDs []int64
	deletedGroupID   int64
	boundGroupID     int64
	boundAccountIDs  []int64
}

func (s *groupAccountCopyRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	group := s.groupsByID[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func (s *groupAccountCopyRepoStub) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	return s.GetByID(ctx, id)
}

func (s *groupAccountCopyRepoStub) GetAccountIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	return append([]int64(nil), s.sourceAccountIDs...), nil
}

func (s *groupAccountCopyRepoStub) DeleteAccountGroupsByGroupID(_ context.Context, groupID int64) (int64, error) {
	s.deletedGroupID = groupID
	return 1, nil
}

func (s *groupAccountCopyRepoStub) BindAccountsToGroup(_ context.Context, groupID int64, accountIDs []int64) error {
	s.boundGroupID = groupID
	s.boundAccountIDs = append([]int64(nil), accountIDs...)
	return nil
}

func (s *groupRepoStubForAdmin) Create(_ context.Context, g *Group) error {
	s.created = g
	return nil
}

func (s *groupRepoStubForAdmin) Update(_ context.Context, g *Group) error {
	s.updated = g
	return nil
}

func (s *groupRepoStubForAdmin) GetByID(_ context.Context, _ int64) (*Group, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getByID, nil
}

func (s *groupRepoStubForAdmin) GetByIDLite(_ context.Context, _ int64) (*Group, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getByID, nil
}

func (s *groupRepoStubForAdmin) Delete(_ context.Context, _ int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForAdmin) DeleteCascade(_ context.Context, _ int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForAdmin) List(_ context.Context, _ pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForAdmin) ListWithFilters(_ context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]Group, *pagination.PaginationResult, error) {
	s.listWithFiltersCalls++
	s.listWithFiltersParams = params
	s.listWithFiltersPlatform = platform
	s.listWithFiltersStatus = status
	s.listWithFiltersSearch = search
	s.listWithFiltersIsExclusive = isExclusive

	if s.listWithFiltersErr != nil {
		return nil, nil, s.listWithFiltersErr
	}

	result := s.listWithFiltersResult
	if result == nil {
		result = &pagination.PaginationResult{
			Total:    int64(len(s.listWithFiltersGroups)),
			Page:     params.Page,
			PageSize: params.PageSize,
		}
	}

	return s.listWithFiltersGroups, result, nil
}

func (s *groupRepoStubForAdmin) ListActive(_ context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForAdmin) ListActiveByPlatform(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStubForAdmin) ListActiveByPlatformLite(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatformLite call")
}

func (s *groupRepoStubForAdmin) ExistsByName(_ context.Context, _ string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStubForAdmin) GetAccountCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *groupRepoStubForAdmin) DeleteAccountGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *groupRepoStubForAdmin) BindAccountsToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *groupRepoStubForAdmin) GetAccountIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *groupRepoStubForAdmin) UpdateSortOrders(_ context.Context, _ []GroupSortOrderUpdate) error {
	return nil
}

func TestAdminServiceUpdateGroupCopiesMembershipWithoutAssociationPriority(t *testing.T) {
	target := &Group{ID: 1701, Name: "target", Platform: PlatformGemini, Status: StatusActive}
	source := &Group{ID: 1702, Name: "source", Platform: PlatformGemini, Status: StatusActive}
	base := &groupRepoStubForAdmin{}
	repo := &groupAccountCopyRepoStub{
		groupRepoStubForAdmin: base,
		groupsByID:            map[int64]*Group{target.ID: target, source.ID: source},
		sourceAccountIDs:      []int64{71, 72},
	}
	svc := &GroupAdmin{groupRepo: repo, options: GroupAdminOptions{Mutate: func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }}}

	updated, err := svc.UpdateGroup(context.Background(), target.ID, &UpdateGroupInput{
		CopyAccountsFromGroupIDs: []int64{source.ID},
	})

	require.NoError(t, err)
	require.Same(t, target, updated)
	require.Equal(t, target.ID, repo.deletedGroupID)
	require.Equal(t, target.ID, repo.boundGroupID)
	require.Equal(t, []int64{71, 72}, repo.boundAccountIDs)
}

// LockGroupSortOrder 记录创建流程是否申请了排序位置锁。
func (s *groupRepoStubForAdmin) LockGroupSortOrder(_ context.Context) error {
	s.groupSortOrderLockCalls++
	return nil
}
