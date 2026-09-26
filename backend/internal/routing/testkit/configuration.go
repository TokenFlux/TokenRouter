package testkit

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// Configuration 是迁移前的组合输入。测试装配时将它投影为独立的价格和分组策略，
// 使已有协议用例继续验证同一请求行为；生产实体不包含这些策略字段。
type Configuration struct {
	ID                         int64
	Name                       string
	Description                string
	Status                     string
	BillingModelSource         string
	RestrictModels             bool
	Features                   string
	FeaturesConfig             map[string]any
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	GroupIDs                   []int64
	ModelPricing               []routing.ModelPricingEntry
	ModelMapping               map[string]map[string]string
	ApplyPricingToAccountStats bool
	AccountStatsPricingRules   []routing.AccountStatsPricingRule
}

func (c Configuration) Price() routing.PricingConfig {
	return routing.PricingConfig{ID: c.ID, Name: c.Name, Description: c.Description, Status: c.Status, BillingModelSource: c.BillingModelSource, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, GroupIDs: c.GroupIDs, ModelPricing: c.ModelPricing, ApplyPricingToAccountStats: c.ApplyPricingToAccountStats, AccountStatsPricingRules: c.AccountStatsPricingRules}
}

func (c Configuration) Policy() routing.GroupRoutingPolicy {
	allowed := make(map[string][]string)
	for _, price := range c.ModelPricing {
		allowed[price.Platform] = append(allowed[price.Platform], price.Models...)
	}
	return routing.GroupRoutingPolicy{Enabled: c.Status == routing.StatusActive, ModelMapping: c.ModelMapping, RestrictModels: c.RestrictModels, RestrictionModelSource: c.BillingModelSource, AllowedModels: allowed, Features: c.Features, FeaturesConfig: c.FeaturesConfig}
}

func (c *Configuration) Clone() *Configuration {
	if c == nil {
		return nil
	}
	out := *c
	price := c.Price()
	clone := price.Clone()
	out.GroupIDs, out.ModelPricing, out.AccountStatsPricingRules = clone.GroupIDs, clone.ModelPricing, clone.AccountStatsPricingRules
	policy := c.Policy().Clone()
	out.ModelMapping, out.FeaturesConfig = policy.ModelMapping, policy.FeaturesConfig
	return &out
}
func (c *Configuration) IsActive() bool { return c != nil && c.Status == routing.StatusActive }
func (c *Configuration) GetModelPricing(model string) *routing.ModelPricingEntry {
	price := c.Price()
	return price.GetModelPricing(model)
}

func (c *Configuration) IsWebSearchEmulationEnabled(platform string) bool {
	if c == nil {
		return false
	}
	p := routing.GroupPolicyView{GroupRoutingPolicy: c.Policy()}
	return p.IsWebSearchEmulationEnabled(platform)
}

func (c *Configuration) IsBedrockCCCompatEnabled(platform string) bool {
	if c == nil {
		return false
	}
	p := routing.GroupPolicyView{GroupRoutingPolicy: c.Policy()}
	return p.IsBedrockCCCompatEnabled(platform)
}

// configurationRows 将迁移夹具拆成生产服务的两个数据来源。
type configurationRows struct {
	routing.PricingConfigRepository
	source interface {
		ListAll(context.Context) ([]Configuration, error)
		GetGroupPlatforms(context.Context, []int64) (map[int64]string, error)
	}
}

func (r configurationRows) ListAll(ctx context.Context) ([]routing.PricingConfig, error) {
	rows, err := r.source.ListAll(ctx)
	out := make([]routing.PricingConfig, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Price())
	}
	return out, err
}

func (r configurationRows) GetGroupPlatforms(ctx context.Context, ids []int64) (map[int64]string, error) {
	return r.source.GetGroupPlatforms(ctx, ids)
}

func (r configurationRows) ReadGroup(ctx context.Context, id int64) (*routing.Group, error) {
	rows, err := r.source.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	platforms, err := r.source.GetGroupPlatforms(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		for _, groupID := range row.GroupIDs {
			if groupID == id {
				return &routing.Group{ID: id, Platform: platforms[id], RoutingPolicy: row.Policy()}, nil
			}
		}
	}
	return &routing.Group{ID: id, Platform: platforms[id]}, nil
}

// NewPricingConfigService 仅在测试中把旧组合输入拆为两个独立读取端口。
func NewPricingConfigService(repo any, invalidator routing.GroupAuthInvalidator, options ...routing.PricingConfigOptions) *routing.PricingConfigService {
	if repo == nil {
		return routing.NewPricingConfigService(nil, invalidator, options...)
	}
	opts := routing.PricingConfigOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	if source, ok := repo.(interface {
		ListAll(context.Context) ([]Configuration, error)
		GetGroupPlatforms(context.Context, []int64) (map[int64]string, error)
	}); ok {
		adapter := configurationRows{source: source}
		if opts.ReadGroup == nil {
			opts.ReadGroup = adapter.ReadGroup
		}
		return routing.NewPricingConfigService(adapter, invalidator, opts)
	}
	productionRepo, ok := repo.(routing.PricingConfigRepository)
	if !ok {
		panic("unsupported pricing fixture repository")
	}
	return routing.NewPricingConfigService(productionRepo, invalidator, opts)
}
