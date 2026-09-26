package app

import (
	"context"
	"log/slog"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"

	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// pricingCatalog 将目录服务限制为 billing 所需的只读/维护接口。
type pricingCatalog struct{ source *provider.PricingService }

func (c pricingCatalog) GetModelPricing(model string) *billing.LiteLLMModelPricing {
	return c.source.GetModelPricing(model)
}

func (c pricingCatalog) GetStatus() map[string]any { return c.source.GetStatus() }
func (c pricingCatalog) ForceUpdate() error        { return c.source.ForceUpdate() }
func (c pricingCatalog) GetModelModalities(model string) ([]string, []string) {
	return c.source.GetModelModalities(model)
}

// providePricingService 从同一份 bootstrap 配置投影技术参数，构造期间不启动任务。
// 初始化、周期更新和停止继续由既有 PricingInitialization/PricingService hook 唯一管理。
func providePricingService(cfg *config.Config, remote provider.PricingRemoteClient) (*provider.PricingService, error) {
	options := provider.Options{
		DataDir:                  cfg.Pricing.DataDir,
		RemoteURL:                cfg.Pricing.RemoteURL,
		HashURL:                  cfg.Pricing.HashURL,
		FallbackFile:             cfg.Pricing.FallbackFile,
		OverrideFile:             cfg.Pricing.OverrideFile,
		HashCheckIntervalMinutes: cfg.Pricing.HashCheckIntervalMinutes,
		UpdateIntervalHours:      cfg.Pricing.UpdateIntervalHours,
		URLAllowlistEnabled:      cfg.Security.URLAllowlist.Enabled,
		AllowInsecureHTTP:        cfg.Security.URLAllowlist.AllowInsecureHTTP,
		AllowPrivateHosts:        cfg.Security.URLAllowlist.AllowPrivateHosts,
		PricingHosts:             slices.Clone(cfg.Security.URLAllowlist.PricingHosts),
		DefaultOpenAIModel:       openai.DefaultTestModel,
		ModelLookupCandidates:    modelidentity.CandidatesFactory,
		IsImageModel:             media.IsImageGenerationModel,
	}
	return provider.NewPricingService(options, remote), nil
}

// provideBillingCalculator 用显式配置投影构造唯一计费实例。
func provideBillingCalculator(cfg *config.Config, catalog *provider.PricingService, calendar timezone.Calendar) *billing.Calculator {
	warnings := &provider.PricingWarnings{}
	return billing.NewCalculator(pricingCatalog{source: catalog}, billing.CalculatorOptions{DefaultRateMultiplier: cfg.Default.RateMultiplier, ModelPolicy: modelidentity.PricingPolicy, Now: calendar.Now, LoadLocation: provider.LoadPricingLocation, FallbackWarning: warnings.Fallback})
}

func provideBillingPriceResolver(modelConfigs *routing.PricingConfigService, calculator *billing.Calculator) *billing.PriceResolver {
	return billing.NewPriceResolver(modelConfigs, calculator, modelidentity.Identity, func(model string, err error) {
		slog.DebugContext(context.Background(), "failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	}, gatewayprovider.AccountStatsSource{Service: modelConfigs})
}
