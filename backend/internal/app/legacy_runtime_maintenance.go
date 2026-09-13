// 本文件登记旧账号维护资源；业务迁移后删除对应绑定，S16 清零。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type maintenanceRuntimeReady struct{}

func provideMaintenanceRuntime(
	tokenRefresh *account.BackgroundRefreshService,
	accountExpiry *service.AccountExpiryService,
	proxyExpiry *service.ProxyExpiryService,
	subscriptionExpiry *billing.SubscriptionExpiryService,
	announcementExpiry *service.AnnouncementExpiryService,
	scheduledTestRunner *service.ScheduledTestRunnerService,
	groupAvailabilityProbeRunner *service.GroupAvailabilityProbeRunnerService,
	cfg *config.Config,
	manager *lifecycle.Manager,
	concurrency *service.ConcurrencyService,
	messageQueue *service.UserMessageQueueService,
) *maintenanceRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "TokenRefreshService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if tokenRefresh != nil {
			return tokenRefresh.StartContext(ctx)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if tokenRefresh != nil {
			return tokenRefresh.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "AccountExpiryService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if accountExpiry != nil {
			accountExpiry.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if accountExpiry != nil {
			return accountExpiry.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "ProxyExpiryService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if proxyExpiry != nil {
			proxyExpiry.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if proxyExpiry != nil {
			return proxyExpiry.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "SubscriptionExpiryService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if subscriptionExpiry != nil {
			subscriptionExpiry.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if subscriptionExpiry != nil {
			return subscriptionExpiry.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "AnnouncementExpiryService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if announcementExpiry != nil {
			announcementExpiry.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if announcementExpiry != nil {
			announcementExpiry.Stop()
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "ScheduledTestRunnerService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if scheduledTestRunner != nil {
			return scheduledTestRunner.StartContext(ctx)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if scheduledTestRunner != nil {
			return scheduledTestRunner.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "GroupAvailabilityProbeRunnerService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if groupAvailabilityProbeRunner != nil {
			groupAvailabilityProbeRunner.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if groupAvailabilityProbeRunner != nil {
			return groupAvailabilityProbeRunner.StopContext(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "ConcurrencyService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if concurrency != nil {
			concurrency.StartSlotCleanupWorker(nil, cfg.Gateway.Scheduling.SlotCleanupInterval)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if concurrency != nil {
			concurrency.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "UserMessageQueueService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if messageQueue != nil {
			messageQueue.StartCleanupWorker(time.Duration(cfg.Gateway.UserMessageQueue.CleanupIntervalSeconds) * time.Second)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if messageQueue != nil {
			messageQueue.Stop()
		}
		return nil
	}})
	return &maintenanceRuntimeReady{}
}
