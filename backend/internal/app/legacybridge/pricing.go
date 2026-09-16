package legacybridge

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// PricingModelCandidates 仅调用旧平台能力投影；供应商模型目录在 S09 提供目标接口后删除。
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

func BillingModelPolicy(model string) pricing.ModelPolicy { return service.PricingModelPolicy(model) }
func BillingModelIdentity(model string) billing.ModelIdentity {
	return service.PricingModelIdentity(model)
}

func (c BillingCatalog) GetModelModalities(model string) ([]string, []string) {
	return c.Service.GetModelModalities(model)
}
