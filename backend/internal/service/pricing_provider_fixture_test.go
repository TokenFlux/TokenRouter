package service

import (
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// pricingServiceFixture 显式构造尚未启动的目录输入，不复制任何生产算法或运行状态。
type pricingServiceFixture struct {
	cfg         *config.Config
	pricingData map[string]*LiteLLMModelPricing
}

func newPricingServiceFixture(fixture pricingServiceFixture) *PricingService {
	service := NewPricingService(fixture.cfg, nil)
	setPricingFixtureData(service, fixture.pricingData)
	return service
}

// setPricingFixtureData 只用于 Start 前的测试准备，通过同一 provider 构造快照。
func setPricingFixtureData(service *PricingService, data map[string]*LiteLLMModelPricing) {
	snapshot := service.runtime.Snapshot()
	snapshot.Data = data
	service.runtime = provider.NewPricingServiceWithOptionsSource(func() provider.Options { return legacyPricingOptions(service.cfg) }, service.remoteClient, snapshot)
}

func mutatePricingFixture(service *PricingService, change func(map[string]*LiteLLMModelPricing)) {
	data := service.runtime.Snapshot().Data
	change(data)
	setPricingFixtureData(service, data)
}

// setPricingFixtureRemote 保留已加载的目录与 hash，仅替换测试提供的传输替身。
func setPricingFixtureRemote(service *PricingService, remote PricingRemoteClient) {
	snapshot := service.runtime.Snapshot()
	service.remoteClient = remote
	service.runtime = provider.NewPricingServiceWithOptionsSource(func() provider.Options { return legacyPricingOptions(service.cfg) }, remote, snapshot)
}
