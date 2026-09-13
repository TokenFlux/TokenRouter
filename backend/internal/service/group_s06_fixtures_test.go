//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// prepareRoutingAdmin 让原高层回归使用新规则，保留旧替身记录的调用与事务错误。
func prepareRoutingAdmin(s *adminServiceImpl) *adminServiceImpl {
	repo := groupRepoTestPort{s.groupRepo}
	var duplicate routing.GroupDuplicateRepository
	if s.groupDuplicateRepo != nil {
		duplicate = groupDuplicateTestPort{s.groupDuplicateRepo}
	}
	var accounts routing.GroupAccounts
	if s.accountRepo != nil {
		accounts = groupAccountsTestPort{s.accountRepo}
	}
	s.routingAdmin = routing.NewGroupAdmin(repo, duplicate, s.groupSortOrderRepo, accounts, s.apiKeyRepo, s.authCacheInvalidator, s.channelCacheInvalidator, routing.GroupAdminOptions{
		Pricing: routing.ChannelValidation{LoadLocation: loadChannelTimePricingLocation}, DefaultModels: defaultModelsListCandidateIDs, NormalizeMappedModel: normalizeOpenAIMessagesDispatchMappedModel,
		GlobalWeights: func(ctx context.Context) (policy.ScoreWeights, error) {
			v, e := s.advancedSchedulerGlobalWeightsForValidation(ctx)
			return policy.ScoreWeights(v), e
		},
		Mutate: func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
	})
	return s
}

type groupRepoTestPort struct{ GroupRepository }
type groupDuplicateTestPort struct{ GroupDuplicateRepository }
type groupAccountsTestPort struct{ AccountRepository }

func (r groupAccountsTestPort) GetByIDs(ctx context.Context, ids []int64) ([]routing.GroupAccount, error) {
	v, e := r.AccountRepository.GetByIDs(ctx, ids)
	rows := make([]Account, len(v))
	for i := range v {
		rows[i] = *v[i]
	}
	return groupAccountViews(rows), e
}
func (r groupAccountsTestPort) ListSchedulableByGroupID(ctx context.Context, id int64) ([]routing.GroupAccount, error) {
	v, e := r.AccountRepository.ListSchedulableByGroupID(ctx, id)
	return groupAccountViews(v), e
}
func routingTestGroups(values []Group) []routing.Group {
	if values == nil {
		return nil
	}
	out := make([]routing.Group, len(values))
	for i := range values {
		out[i] = *RoutingGroupView(&values[i])
	}
	return out
}
func (r groupRepoTestPort) Create(ctx context.Context, value *routing.Group) error {
	old := GroupFromRouting(value)
	err := r.GroupRepository.Create(ctx, old)
	*value = *RoutingGroupView(old)
	return err
}
func (r groupRepoTestPort) Update(ctx context.Context, value *routing.Group) error {
	old := GroupFromRouting(value)
	err := r.GroupRepository.Update(ctx, old)
	*value = *RoutingGroupView(old)
	return err
}
func (r groupRepoTestPort) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	value, err := r.GroupRepository.GetByID(ctx, id)
	return RoutingGroupView(value), err
}
func (r groupRepoTestPort) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	value, err := r.GroupRepository.GetByIDLite(ctx, id)
	return RoutingGroupView(value), err
}
func (r groupRepoTestPort) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	v, p, e := r.GroupRepository.List(ctx, params)
	return routingTestGroups(v), p, e
}
func (r groupRepoTestPort) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	v, p, e := r.GroupRepository.ListWithFilters(ctx, params, platform, status, search, isExclusive)
	return routingTestGroups(v), p, e
}
func (r groupRepoTestPort) ListActive(ctx context.Context) ([]routing.Group, error) {
	v, e := r.GroupRepository.ListActive(ctx)
	return routingTestGroups(v), e
}
func (r groupRepoTestPort) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	v, e := r.GroupRepository.ListActiveByPlatform(ctx, platform)
	return routingTestGroups(v), e
}
func (r groupRepoTestPort) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	v, e := r.GroupRepository.ListActiveByPlatformLite(ctx, platform)
	return routingTestGroups(v), e
}
func (r groupDuplicateTestPort) FindByDuplicateOperationID(ctx context.Context, id string) (*routing.Group, error) {
	v, e := r.GroupDuplicateRepository.FindByDuplicateOperationID(ctx, id)
	return RoutingGroupView(v), e
}
func (r groupDuplicateTestPort) CreateFromSource(ctx context.Context, value *routing.Group, id int64) error {
	old := GroupFromRouting(value)
	e := r.GroupDuplicateRepository.CreateFromSource(ctx, old, id)
	*value = *RoutingGroupView(old)
	return e
}
