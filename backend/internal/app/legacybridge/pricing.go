package legacybridge

import (
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
