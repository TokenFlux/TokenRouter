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

// runtime_maintenance.go registers periodic provider maintenance, expiry scans,
// availability probes, and admission cleanup. These periodic producers share
// stop level 20 after request draining, while configured concurrency-slot and
// user-message cleanup intervals are passed to their own worker start methods.

// maintenanceRuntimeReady marks registration of periodic provider, expiry,
// probe, and admission maintenance.
type maintenanceRuntimeReady struct{}

// provideMaintenanceRuntime registers periodic producer hooks and admission
// cleanup workers before manager startup or shutdown. Hooks remain registered
// for nil services; callbacks skip those service calls. Configured intervals
// are read when their start callbacks run. Each
// component retains its own Start/StopContext behavior, including callbacks
// that use context-free Stop. Supply cfg when concurrency or messageQueue is
// present because those callbacks read its configured cleanup intervals.
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
	manager.Register(lifecycle.Hook{Name: "TokenRefreshService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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
	manager.Register(lifecycle.Hook{Name: "ProviderExpiryService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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
	manager.Register(lifecycle.Hook{Name: "ProxyExpiryService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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
	manager.Register(lifecycle.Hook{Name: "SubscriptionExpiryService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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
	manager.Register(lifecycle.Hook{Name: "AnnouncementExpiryService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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

	manager.Register(lifecycle.Hook{Name: "ScheduledTestRunnerService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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
	manager.Register(lifecycle.Hook{Name: "GroupAvailabilityProbeRunnerService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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

	manager.Register(lifecycle.Hook{Name: "ConcurrencyService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
		if concurrency != nil {
			concurrency.StartSlotCleanupWorker(time.Duration(cfg.Scheduling.SlotCleanupIntervalSeconds) * time.Second)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if concurrency != nil {
			return concurrency.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "UserMessageQueueService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
		if messageQueue != nil {
			// The message-queue worker accepts a duration, while its deployment field
			// stores seconds; use that unit for the lifecycle-start interval.
			messageQueue.StartCleanupWorker(time.Duration(cfg.Gateway.Anthropic.UserMessageQueue.CleanupIntervalSeconds) * time.Second)
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
