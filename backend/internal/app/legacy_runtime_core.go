// 本文件登记旧共享资源资源；业务迁移后删除对应绑定，S16 清零。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type coreRuntimeReady struct{}

func provideCoreRuntime(
	authCacheInvalidationWorker *service.AuthCacheInvalidationWorker,
	schedulerSnapshot *service.SchedulerSnapshotService,
	usageCleanup *service.UsageCleanupService,
	idempotencyCleanup *service.IdempotencyCleanupService,
	openAIGateway *service.OpenAIGatewayService,
	paymentOrderExpiry *service.PaymentOrderExpiryService,
	tlsFingerprintCollector *service.TLSFingerprintCollectorService,
	manager *lifecycle.Manager,
	timingWheel *service.TimingWheelService,
	gateway *service.GatewayService,
	digestStore *service.DigestSessionStore,
	usageRepo service.UsageLogRepository,
	tasks *lifecycle.Tasks,
	httpUpstream service.HTTPUpstream,
) *coreRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "AuthCacheInvalidationWorker", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if authCacheInvalidationWorker != nil {
			authCacheInvalidationWorker.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if authCacheInvalidationWorker != nil {
			authCacheInvalidationWorker.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "SchedulerSnapshotService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if schedulerSnapshot != nil {
			schedulerSnapshot.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if schedulerSnapshot != nil {
			schedulerSnapshot.Stop()
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "UsageCleanupService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if usageCleanup != nil {
			usageCleanup.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if usageCleanup != nil {
			usageCleanup.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "IdempotencyCleanupService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if idempotencyCleanup != nil {
			idempotencyCleanup.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if idempotencyCleanup != nil {
			idempotencyCleanup.Stop()
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "OpenAIGatewayService", StartOrder: 990, StopOrder: 10, Start: nil, Stop: func(ctx context.Context) error {
		if openAIGateway != nil {
			openAIGateway.CloseOpenAIWSPool()
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "PaymentOrderExpiryService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if paymentOrderExpiry != nil {
			paymentOrderExpiry.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if paymentOrderExpiry != nil {
			paymentOrderExpiry.Stop()
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "TLSFingerprintCollectorService", StartOrder: 995, StopOrder: 5, Start: nil, Stop: func(ctx context.Context) error {
		if tlsFingerprintCollector != nil {
			return tlsFingerprintCollector.Shutdown(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "TimingWheelService", StartOrder: 180, StopOrder: 820, Start: func(ctx context.Context) error {
		if timingWheel != nil {
			return timingWheel.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if timingWheel != nil {
			return timingWheel.Shutdown(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "RuntimeLocalCaches", StartOrder: 185, StopOrder: 815,
		Start: func(context.Context) error {
			timingWheel.ScheduleRecurring("runtime:local_caches", time.Minute, func() {
				gateway.ExpireRuntimeCaches()
				openAIGateway.ExpireRuntimeCaches()
				digestStore.ExpireRuntimeCaches()
				if cache, ok := usageRepo.(interface{ ExpireRuntimeCaches() }); ok {
					cache.ExpireRuntimeCaches()
				}
			})
			return nil
		}, Stop: func(context.Context) error { timingWheel.CancelAndWait("runtime:local_caches"); return nil }})
	manager.Register(lifecycle.Hook{Name: "UsageLogBatchers", StartOrder: 952, StopOrder: 48, Stop: func(context.Context) error {
		if closer, ok := usageRepo.(interface{ StopUsageBatchers() }); ok {
			closer.StopUsageBatchers()
		}
		return nil
	}})

	for _, phase := range []int{16, 21, 26, 31, 41, 46, 49, 51, 56, 61, 66} {
		manager.Register(lifecycle.Hook{Name: fmt.Sprintf("BackgroundBarrier%d", phase), StartOrder: 1000 - phase, StopOrder: phase, Stop: tasks.Wait})
	}

	manager.Register(lifecycle.Hook{Name: "OpenAILiveObservers", StartOrder: 995, StopOrder: 5, Stop: openAIGateway.StopLiveObservers})
	manager.Register(lifecycle.Hook{Name: "HTTPIdleConnections", StartOrder: 160, StopOrder: 840, Stop: func(context.Context) error {
		if closer, ok := httpUpstream.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
		httpclient.CloseSharedIdleConnections()
		return nil
	}})

	return &coreRuntimeReady{}
}
