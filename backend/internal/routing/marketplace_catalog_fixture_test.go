package routing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// pricingServiceFixture 显式构造尚未启动的目录输入，不复制任何生产算法或运行状态。
type pricingServiceFixture struct {
	pricingData map[string]*pricing.LiteLLMModelPricing
}

func newPricingServiceFixture(fixture pricingServiceFixture) *provider.PricingService {
	return provider.NewPricingServiceFromSnapshot(provider.Options{
		DefaultOpenAIModel:    openai.DefaultTestModel,
		IsImageModel:          media.IsImageGenerationModel,
		ModelLookupCandidates: modelidentity.CandidatesFactory,
	}, nil, provider.Snapshot{Data: fixture.pricingData})
}
