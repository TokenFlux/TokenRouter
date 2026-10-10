package routing_test

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// groupAdminPricingStore 为目录测试提供空价卡，模型权限只由分组配置决定。
type groupAdminPricingStore struct {
	routing.PricingConfigRepository
}

type groupPortFixture struct{ routing.GroupRepository }

type groupDuplicatePortFixture struct {
	routing.GroupDuplicateRepository
}

// groupRepoStubForAdmin 记录分组管理测试的存储读写。
type groupRepoStubForAdmin struct {
	created *routing.
		Group
	updated *routing.
		Group
	getByID *routing.
		Group
	getErr error // GetByID 返回的错误

	listWithFiltersCalls       int
	listWithFiltersParams      pagination.PaginationParams
	listWithFiltersPlatform    string
	listWithFiltersStatus      string
	listWithFiltersSearch      string
	listWithFiltersIsExclusive *bool
	listWithFiltersGroups      []routing.Group
	listWithFiltersResult      *pagination.PaginationResult
	listWithFiltersErr         error
	groupSortOrderLockCalls    int
}

func (groupAdminPricingStore) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return nil, nil
}

// newGroupAdminForTest 使用独立存储副本及默认策略构造分组管理用例。
func newGroupAdminForTest(repo routing.GroupRepository, duplicate routing.GroupDuplicateRepository, pricingConfigs routing.GroupPricingInvalidator) *routing.GroupAdmin {
	return newGroupAdminPortsForTest(repo, duplicate, pricingConfigs, nil, nil, nil, nil)
}

// newGroupAdminPortsForTest 装配分组管理测试需要的存储、提供商查询和缓存失效接口。
func newGroupAdminPortsForTest(repo routing.GroupRepository, duplicate routing.GroupDuplicateRepository, pricingConfigs routing.GroupPricingInvalidator, sortOrder routing.GroupSortOrderRepository, providers routing.GroupProviders, invalidator routing.GroupAdminInvalidator, weights *policy.ConfigScoreWeights, keyReaders ...routing.GroupKeyReader) *routing.GroupAdmin {
	var keys routing.GroupKeyReader
	if len(keyReaders) > 0 {
		keys = keyReaders[0]
	}
	var duplicates routing.GroupDuplicateRepository
	if duplicate != nil {
		duplicates = groupDuplicatePortFixture{duplicate}
	}
	modelPolicies := routing.NewPricingConfigService(groupAdminPricingStore{}, nil, routing.PricingConfigOptions{ReadGroup: func(ctx context.Context, id int64) (*routing.Group, error) {
		group, err := repo.GetByIDLite(ctx, id)
		if group != nil {
			group = routing.CloneGroup(group)
			if group.AllowedProtocols == nil {
				group.AllowedProtocols = capability.DefaultGroupClientProtocols("")
			}
		}
		return group, err
	}})
	return routing.NewGroupAdmin(groupPortFixture{repo}, duplicates, sortOrder, providers, keys, invalidator, pricingConfigs, routing.GroupAdminOptions{
		DefaultModels: func(string) []string { return []string{"claude-sonnet-4-6", "gpt-5.4"} },
		ModelResolver: routing.RequestableResolver{
			GroupPolicies: modelPolicies,
			Warn:          slog.Warn,
		},
		GlobalWeights: func(ctx context.Context) (policy.ScoreWeights, error) {
			defaults := scheduler.DefaultAdminSettingsDefaults()
			if weights != nil {
				defaults.Weights = *weights
			}
			return scheduler.LoadValidationWeights(ctx, nil, defaults)
		},
		Mutate: func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
	})
}

func testGroupsFixture(values []routing.Group) []routing.Group {
	if values == nil {
		return nil
	}
	out := make([]routing.Group, len(values))
	for i := range values {
		out[i] = *routing.CloneGroup(&values[i])
	}
	return out
}

func (r groupPortFixture) Create(ctx context.Context, value *routing.Group) error {
	old := routing.CloneGroup(value)
	err := r.GroupRepository.Create(ctx, old)
	*value = *routing.CloneGroup(old)
	return err
}

func (r groupPortFixture) Update(ctx context.Context, value *routing.Group) error {
	old := routing.CloneGroup(value)
	err := r.GroupRepository.Update(ctx, old)
	*value = *routing.CloneGroup(old)
	return err
}

func (r groupPortFixture) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	value, err := r.GroupRepository.GetByID(ctx, id)
	return routing.CloneGroup(value), err
}

func (r groupPortFixture) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	value, err := r.GroupRepository.GetByIDLite(ctx, id)
	return routing.CloneGroup(value), err
}

func (r groupPortFixture) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	v, p, e := r.GroupRepository.List(ctx, params)
	return testGroupsFixture(v), p, e
}

func (r groupPortFixture) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	v, p, e := r.GroupRepository.ListWithFilters(ctx, params, platform, status, search, isExclusive)
	return testGroupsFixture(v), p, e
}

func (r groupPortFixture) ListActive(ctx context.Context) ([]routing.Group, error) {
	v, e := r.GroupRepository.ListActive(ctx)
	return testGroupsFixture(v), e
}

func (p groupDuplicatePortFixture) FindByDuplicateOperationID(ctx context.Context, id string) (*routing.Group, error) {
	value, err := p.GroupDuplicateRepository.FindByDuplicateOperationID(ctx, id)
	return routing.CloneGroup(value), err
}

func (p groupDuplicatePortFixture) CreateFromSource(ctx context.Context, value *routing.Group, id int64) error {
	copy := routing.CloneGroup(value)
	err := p.GroupDuplicateRepository.CreateFromSource(ctx, copy, id)
	*value = *routing.CloneGroup(copy)
	return err
}

func (s *groupRepoStubForAdmin) Create(_ context.Context, g *routing.Group) error {
	s.created = g
	return nil
}

func (s *groupRepoStubForAdmin) Update(_ context.Context, g *routing.Group) error {
	s.updated = g
	return nil
}

func (s *groupRepoStubForAdmin) GetByID(_ context.Context, _ int64) (*routing.Group, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getByID, nil
}

func (s *groupRepoStubForAdmin) GetByIDLite(_ context.Context, _ int64) (*routing.Group, error) {
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

func (s *groupRepoStubForAdmin) List(_ context.Context, _ pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForAdmin) ListWithFilters(_ context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
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

func (s *groupRepoStubForAdmin) ListActive(_ context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForAdmin) ListActiveByPlatform(_ context.Context, _ string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStubForAdmin) ListActiveByPlatformLite(_ context.Context, _ string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatformLite call")
}

func (s *groupRepoStubForAdmin) ExistsByName(_ context.Context, _ string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStubForAdmin) GetProviderCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetProviderCount call")
}

func (s *groupRepoStubForAdmin) DeleteProviderGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteProviderGroupsByGroupID call")
}

func (s *groupRepoStubForAdmin) BindProvidersToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindProvidersToGroup call")
}

func (s *groupRepoStubForAdmin) GetProviderIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetProviderIDsByGroupIDs call")
}

func (s *groupRepoStubForAdmin) UpdateSortOrders(_ context.Context, _ []routing.GroupSortOrderUpdate) error {
	return nil
}

// LockGroupSortOrder 记录创建流程是否申请了排序位置锁。
func (s *groupRepoStubForAdmin) LockGroupSortOrder(_ context.Context) error {
	s.groupSortOrderLockCalls++
	return nil
}
