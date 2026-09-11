package app

import (
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
