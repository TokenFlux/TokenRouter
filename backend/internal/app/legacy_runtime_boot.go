// 本文件登记旧启动预热资源；业务迁移后删除对应绑定，S16 清零。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type bootRuntimeReady struct{}

func provideBootRuntime(
	creativeWorker *service.CreativeWorkerRuntime,
	pricing *service.PricingService,
	manager *lifecycle.Manager,
	settingService *service.SettingService, forwarded *runtimeconfig.ForwardedSettings,
) *bootRuntimeReady {
	pricingReady := false
	manager.Register(lifecycle.Hook{Name: "PricingInitialization", StartOrder: 188, Start: func(context.Context) error {
		if pricing == nil {
			return nil
		}
		if err := pricing.Initialize(); err != nil {
			logger.LegacyPrintf("service.pricing", "[Service] Warning: Pricing service initialization failed: %v", err)
			return nil
		}
		pricingReady = true
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "LegacySettingsInitialization", StartOrder: 181, StopOrder: 800, Start: func(ctx context.Context) error {
		if err := forwarded.LoadForwardedClientIPSettings(ctx); err != nil {
			logger.LegacyPrintf("service.setting", "Warning: load forwarded client IP settings failed: %v", err)
		}
		if err := settingService.MigrateGrokDefaultTextModel(ctx); err != nil {
			logger.LegacyPrintf("service.setting", "Warning: migrate Grok default text model failed: %v", err)
		}
		return nil
	}})
	settingService.SetCreativeWorkerCountCallback(creativeWorker.SetWorkerCount)
	settingService.SetCreativeWorkerStatusCallback(creativeWorker.Status)

	manager.Register(lifecycle.Hook{Name: "PricingService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if pricing != nil && pricingReady {
			pricing.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if pricing != nil {
			pricing.Stop()
		}
		return nil
	}})
	return &bootRuntimeReady{}
}
