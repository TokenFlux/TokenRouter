package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// PricingModelCandidates 仅调用旧平台能力投影；S06/S09 提供目标接口后删除。
func PricingModelCandidatesFactory() func(string) []string {
	return service.PricingModelCandidatesFactory()
}

// PricingImageModel 不在桥接中增加模型资格规则。
func PricingImageModel(model string) bool {
	return service.PricingImageModel(model)
}

// PricingDefaultOpenAIModel 保留旧平台默认模型作为价格回退输入。
func PricingDefaultOpenAIModel() string {
	return openai.DefaultTestModel
}

// BillingCatalog 复用 S03 唯一运行目录，S15 清理旧构造外壳。
type BillingCatalog struct{ Service *service.PricingService }

func (c BillingCatalog) GetModelPricing(model string) *pricing.LiteLLMModelPricing {
	return c.Service.GetModelPricing(model)
}
func (c BillingCatalog) GetStatus() map[string]any { return c.Service.GetStatus() }
func (c BillingCatalog) ForceUpdate() error        { return c.Service.ForceUpdate() }

// BillingChannelPrices 只按需调用渠道价卡查询，渠道规则仍归 S06。
type BillingChannelPrices struct{ Service *service.ChannelService }

func (c BillingChannelPrices) GetEffectiveChannelModelPricing(ctx context.Context, id int64, model string) *pricing.ChannelModelPricing {
	return c.Service.GetEffectiveChannelModelPricing(ctx, id, model)
}
func BillingModelPolicy(model string) pricing.ModelPolicy { return service.PricingModelPolicy(model) }
func BillingModelIdentity(model string) billing.ModelIdentity {
	return service.PricingModelIdentity(model)
}

func (c BillingCatalog) GetModelModalities(model string) ([]string, []string) {
	return c.Service.GetModelModalities(model)
}

// BillingAccountStats 只调用旧渠道读取投影，不执行价卡选择。
type BillingAccountStats struct{ Service *service.ChannelService }

func (s BillingAccountStats) AccountStatsGroup(ctx context.Context, id int64) (*billing.AccountStatsChannel, error) {
	return (service.LegacyAccountStatsSource{Service: s.Service}).AccountStatsGroup(ctx, id)
}
func (s BillingAccountStats) AccountStatsPlatform(ctx context.Context, id int64) billing.AccountStatsPlatform {
	return (service.LegacyAccountStatsSource{Service: s.Service}).AccountStatsPlatform(ctx, id)
}
