package routing

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// GroupAccount 仅提供管理分组需要的账号资格与已配置模型，不携带凭据。
type GroupAccount struct {
	ID       int64
	Platform string
	Type     string
	Models   []string
}

type GroupAccounts interface {
	GetByIDs(context.Context, []int64) ([]GroupAccount, error)
	ListSchedulableByGroupID(context.Context, int64) ([]GroupAccount, error)
}

type GroupKeyReader interface {
	ListKeysByGroupID(context.Context, int64) ([]string, error)
}
type GroupAdminInvalidator interface {
	GroupAuthInvalidator
	InvalidateAuthCacheByKey(context.Context, string)
}
type GroupPricingInvalidator interface{ InvalidateCache() }

// GroupAdminOptions 注入读取时机和原有闭合事务；规则不依赖装配和具体存储。
type GroupAdminOptions struct {
	Pricing              PricingConfigValidation
	DefaultModels        func(string) []string
	NormalizeMappedModel func(string) string
	GlobalWeights        func(context.Context) (policy.ScoreWeights, error)
	Mutate               func(context.Context, func(context.Context) error) error
}

// GroupAdmin 拥有分组管理和复制规则，缓存与读写均使用 app 提供的唯一实例。
type GroupAdmin struct {
	groupRepo                     GroupRepository
	groupDuplicateRepo            GroupDuplicateRepository
	groupSortOrderRepo            GroupSortOrderRepository
	accountRepo                   GroupAccounts
	apiKeyRepo                    GroupKeyReader
	authCacheInvalidator          GroupAdminInvalidator
	pricingConfigCacheInvalidator GroupPricingInvalidator
	options                       GroupAdminOptions
}

func NewGroupAdmin(repo GroupRepository, duplicate GroupDuplicateRepository, sortOrder GroupSortOrderRepository, accounts GroupAccounts, keys GroupKeyReader, invalidator GroupAdminInvalidator, pricingConfigs GroupPricingInvalidator, options GroupAdminOptions) *GroupAdmin {
	return &GroupAdmin{groupRepo: repo, groupDuplicateRepo: duplicate, groupSortOrderRepo: sortOrder, accountRepo: accounts, apiKeyRepo: keys, authCacheInvalidator: invalidator, pricingConfigCacheInvalidator: pricingConfigs, options: options}
}

// ValidateAdvancedOverrides 按旧次序先验证局部字段，确有权重覆盖才读取动态全局值。
func (s *GroupAdmin) ValidateAdvancedOverrides(ctx context.Context, overrides GroupAdvancedSchedulerOverrides) error {
	if err := policy.ValidateGroupOverrides(overrides); err != nil {
		return err
	}
	if !policy.HasWeightOverrides(overrides) {
		return nil
	}
	weights, err := s.options.GlobalWeights(ctx)
	if err != nil {
		return err
	}
	return policy.ValidateEffectiveWeights(policy.ApplyGroupWeightOverrides(weights, overrides))
}

// Mutate 供旧管理入口委托原有事务边界，参与方沿用同一个 context。
func (s *GroupAdmin) Mutate(ctx context.Context, fn func(context.Context) error) error {
	return s.options.Mutate(ctx, fn)
}
