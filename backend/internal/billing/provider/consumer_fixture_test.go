package provider

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

// 测试准备发生在启动前，直接使用所属模块状态，不复制旧服务或增加生产接口。
type pricingServiceFixture struct {
	options     Options
	pricingData map[string]*pricing.LiteLLMModelPricing
}

func newPricingServiceFixture(fixture pricingServiceFixture) *PricingService {
	options := fixture.options
	options.DefaultOpenAIModel = openai.DefaultTestModel
	options.IsImageModel = media.IsImageGenerationModel
	options.ModelLookupCandidates = modelidentity.CandidatesFactory
	return NewPricingServiceFromSnapshot(options, nil, Snapshot{Data: fixture.pricingData})
}

func setPricingFixtureData(service *PricingService, data map[string]*pricing.LiteLLMModelPricing) {
	service.pricingData = data
}

func mutatePricingFixture(service *PricingService, change func(map[string]*pricing.LiteLLMModelPricing)) {
	data := service.Snapshot().Data
	change(data)
	setPricingFixtureData(service, data)
}

func setPricingFixtureRemote(service *PricingService, remote PricingRemoteClient) {
	service.remoteClient = remote
}

func newStubPricingServiceFromJSON(t *testing.T, body string) *PricingService {
	t.Helper()
	service := newPricingServiceFixture(pricingServiceFixture{})
	data, err := service.ParsePricingData([]byte(body))
	require.NoError(t, err)
	setPricingFixtureData(service, data)
	return service
}

// 目录和计费契约直接组合原生计算器，保留原测试的缺省倍率和时钟。
func newBillingFixture(catalog *PricingService) *billing.Calculator {
	var source billing.PriceCatalog
	if catalog != nil {
		source = catalog
	}
	warnings := &PricingWarnings{}
	return billing.NewCalculator(source, billing.CalculatorOptions{
		ModelPolicy:     modelidentity.PricingPolicy,
		Now:             time.Now,
		LoadLocation:    LoadPricingLocation,
		FallbackWarning: warnings.Fallback,
	})
}
