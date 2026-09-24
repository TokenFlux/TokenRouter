// 本文件登记旧共享资源资源；业务迁移后删除对应绑定，S16 清零。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	payment "github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type coreRuntimeReady struct{}

func provideCoreRuntime(
	accountRuntime *account.RuntimeBlockState,
	cfg *config.Config,
	authCacheInvalidationWorker *apikey.AuthCacheInvalidationWorker,
	schedulerSnapshot *scheduler.SnapshotService,
	models *routing.ModelList,

	shared *schedulerSharedState,
	usageCleanup *usage.UsageCleanupService,
	idempotencyCleanup *idempotency.IdempotencyCleanupService,
	openAIGateway *service.OpenAIGatewayService,
	openAIAuthorization *account.OpenAIAuthorization,
	paymentOrderExpiry *payment.OrderExpiry,
	tlsFingerprintCollector *provider.TLSFingerprintCollectorService,
	manager *lifecycle.Manager,
	timingWheel *timingwheel.Wheel,
	digestStore *session.DigestSessionStore,
	usageRepo usage.UsageLogRepository,
	tasks *lifecycle.Tasks,
	httpUpstream httpclient.UpstreamTransport, requestActivity *gatewayRequestActivity, rates *gatewayBillingRates,
) *coreRuntimeReady {
	// 原生平台仅登记同步尝试，不改变客户端取消或供应商重试预算。

	nativeAttempts := requestActivity
	openAIGateway.BindRuntimeBlockState(accountRuntime)
	bindGatewayBackground(tasks, openAIGateway)

	if openAIGateway != nil {
		openAIGateway.BindSchedulerStickyStats(shared.Sticky)

		openAIGateway.BindOpenAIAuthorization(openAIAuthorization)
		openAIGateway.BindNativeAttemptActivity(nativeAttempts.Enter)
	}
	manager.Register(lifecycle.Hook{Name: "AuthCacheInvalidationWorker", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if authCacheInvalidationWorker != nil {
			authCacheInvalidationWorker.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if authCacheInvalidationWorker != nil {
			return authCacheInvalidationWorker.StopContext(ctx)
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
			return schedulerSnapshot.StopContext(ctx)
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
			paymentOrderExpiry.Start(ctx)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if paymentOrderExpiry != nil {
			return paymentOrderExpiry.StopContext(ctx)
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
				models.Expire()
				rates.Expire()
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

// bindGatewayBackground 使执行侧派生工作与其他应用任务共享关闭屏障。
func bindGatewayBackground(tasks *lifecycle.Tasks, openai *service.OpenAIGatewayService) {
	if openai != nil {
		openai.BindBackgroundTasks(tasks.Go)
		if openai.Text != nil && openai.Text.CodexUsage != nil {
			openai.Text.CodexUsage.Go = tasks.Go
		}
	}
}
