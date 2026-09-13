package app

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"log/slog"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// providePricingService 从同一份 bootstrap 配置投影技术参数，构造期间不启动任务。
// 初始化、周期更新和停止继续由既有 PricingInitialization/PricingService hook 唯一管理。
func providePricingService(cfg *config.Config, remote provider.PricingRemoteClient) (*service.PricingService, error) {
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
		DefaultOpenAIModel:       legacybridge.PricingDefaultOpenAIModel(),
		ModelLookupCandidates:    legacybridge.PricingModelCandidatesFactory,
		IsImageModel:             legacybridge.PricingImageModel,
	}
	return service.WrapPricingService(provider.NewPricingService(options, remote)), nil
}

// provideBillingCalculator 用显式配置投影构造唯一计费实例。
func provideBillingCalculator(cfg *config.Config, catalog *service.PricingService) *service.BillingService {
	warnings := &provider.PricingWarnings{}
	return service.WrapBillingCalculator(billing.NewCalculator(legacybridge.BillingCatalog{Service: catalog}, billing.CalculatorOptions{DefaultRateMultiplier: cfg.Default.RateMultiplier, ModelPolicy: legacybridge.BillingModelPolicy, Now: timezone.Now, LoadLocation: provider.LoadPricingLocation, FallbackWarning: warnings.Fallback}))
}
func provideBillingPriceResolver(coreChannels *routing.ChannelService, channels *service.ChannelService, calculator *service.BillingService) *service.ModelPricingResolver {
	core := billing.NewPriceResolver(coreChannels, calculator.Calculator, legacybridge.BillingModelIdentity, func(model string, err error) {
		slog.DebugContext(context.Background(), "failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	}, billingChannelStats{Service: coreChannels})
	return service.WrapPriceResolver(core, calculator, channels)
}
