//go:build unit

package routing_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// newOriginalGroupAdmin 保留原测试的存储副本边界及默认策略，直接构造唯一分组用例。
func newOriginalGroupAdmin(repo routing.GroupRepository, duplicate routing.GroupDuplicateRepository, channels routing.GroupChannelInvalidator) *routing.GroupAdmin {
	return newOriginalGroupAdminPorts(repo, duplicate, channels, nil, nil, nil, nil)
}

// newOriginalGroupAdminPorts 只装配原测试需要的窄端口，不复制管理规则。
func newOriginalGroupAdminPorts(repo routing.GroupRepository, duplicate routing.GroupDuplicateRepository, channels routing.GroupChannelInvalidator, sortOrder routing.GroupSortOrderRepository, accounts routing.GroupAccounts, invalidator routing.GroupAdminInvalidator, weights *policy.ConfigScoreWeights, keyReaders ...routing.GroupKeyReader) *routing.GroupAdmin {
	var keys routing.GroupKeyReader
	if len(keyReaders) > 0 {
		keys = keyReaders[0]
	}
	var duplicates routing.GroupDuplicateRepository
	if duplicate != nil {
		duplicates = originalGroupDuplicatePort{duplicate}
	}
	return routing.NewGroupAdmin(originalGroupPort{repo}, duplicates, sortOrder, accounts, keys, invalidator, channels, routing.GroupAdminOptions{
		Pricing:              routing.ChannelValidation{LoadLocation: pricingprovider.LoadPricingLocation},
		DefaultModels:        routingprovider.DefaultGroupModelCandidates,
		NormalizeMappedModel: gatewayprovider.NormalizeOpenAICompatRequestedModel,
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

type originalGroupPort struct{ routing.GroupRepository }

func originalTestGroups(values []routing.Group) []routing.Group {
	if values == nil {
		return nil
	}
	out := make([]routing.Group, len(values))
	for i := range values {
		out[i] = *routing.CloneGroup(&values[i])
	}
	return out
}
func (r originalGroupPort) Create(ctx context.Context, value *routing.Group) error {
	old := routing.CloneGroup(value)
	err := r.GroupRepository.Create(ctx, old)
	*value = *routing.CloneGroup(old)
	return err
}
func (r originalGroupPort) Update(ctx context.Context, value *routing.Group) error {
	old := routing.CloneGroup(value)
	err := r.GroupRepository.Update(ctx, old)
	*value = *routing.CloneGroup(old)
	return err
}
func (r originalGroupPort) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	value, err := r.GroupRepository.GetByID(ctx, id)
	return routing.CloneGroup(value), err
}
func (r originalGroupPort) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	value, err := r.GroupRepository.GetByIDLite(ctx, id)
	return routing.CloneGroup(value), err
}
func (r originalGroupPort) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	v, p, e := r.GroupRepository.List(ctx, params)
	return originalTestGroups(v), p, e
}
func (r originalGroupPort) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	v, p, e := r.GroupRepository.ListWithFilters(ctx, params, platform, status, search, isExclusive)
	return originalTestGroups(v), p, e
}
func (r originalGroupPort) ListActive(ctx context.Context) ([]routing.Group, error) {
	v, e := r.GroupRepository.ListActive(ctx)
	return originalTestGroups(v), e
}
func (r originalGroupPort) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	v, e := r.GroupRepository.ListActiveByPlatform(ctx, platform)
	return originalTestGroups(v), e
}
func (r originalGroupPort) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	v, e := r.GroupRepository.ListActiveByPlatformLite(ctx, platform)
	return originalTestGroups(v), e
}

type originalGroupDuplicatePort struct {
	routing.GroupDuplicateRepository
}

func (p originalGroupDuplicatePort) FindByDuplicateOperationID(ctx context.Context, id string) (*routing.Group, error) {
	value, err := p.GroupDuplicateRepository.FindByDuplicateOperationID(ctx, id)
	return routing.CloneGroup(value), err
}
func (p originalGroupDuplicatePort) CreateFromSource(ctx context.Context, value *routing.Group, id int64) error {
	copy := routing.CloneGroup(value)
	err := p.GroupDuplicateRepository.CreateFromSource(ctx, copy, id)
	*value = *routing.CloneGroup(copy)
	return err
}

// originalImagePricing 仅构造原单张价格夹具，不计算费用。
func originalImagePricing(prices map[string]*float64) []routing.ChannelModelPricing {
	card := routing.ChannelModelPricing{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ChannelModelPricing{card}
}
