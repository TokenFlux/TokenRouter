package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

type maintenanceRuntimeReady struct{}

func provideMaintenanceRuntime(
	tokenRefresh *provider.BackgroundRefreshService,
	providerExpiry *provider.ExpiryService,
	proxyExpiry *egress.ProxyExpiryService,
	subscriptionExpiry *billing.SubscriptionExpiryService,
	announcementExpiry *site.AnnouncementExpiryService,
	scheduledTestRunner *provider.ScheduledTestRunnerService,
	groupAvailabilityProbeRunner *routing.GroupAvailabilityProbeRunnerService,
	cfg *config.Config,
	manager *lifecycle.Manager,
	concurrency *scheduler.ConcurrencyService,
	messageQueue *scheduler.UserMessageQueueService,
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
	manager.Register(lifecycle.Hook{Name: "ProviderExpiryService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if providerExpiry != nil {
			providerExpiry.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if providerExpiry != nil {
			return providerExpiry.StopContext(ctx)
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
			concurrency.StartSlotCleanupWorker(cfg.Gateway.Scheduling.SlotCleanupInterval)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if concurrency != nil {
			return concurrency.StopContext(ctx)
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
			return messageQueue.StopContext(ctx)
		}
		return nil
	}})
	return &maintenanceRuntimeReady{}
}
