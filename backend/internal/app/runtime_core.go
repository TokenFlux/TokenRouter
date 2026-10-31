package app

import (
	"context"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// runtime_core.go registers shared maintenance workers, cache expiry,
// connection cleanup, and background-task barriers. Background side effects can
// be created by different shutdown levels, so one early task wait cannot
// establish that all later effects have finished. Ordinary Tasks.Wait barriers
// follow selected producer and consumer phases while leaving admission open for
// subsequent callbacks. Final task sealing is registered separately by the
// application.
//
// The timing wheel starts before cache maintenance is scheduled, and
// cancellation waits for that recurring callback before the wheel closes.
// Cleanup workers, provider snapshots, usage batchers, and idle HTTP
// connections are stopped at their selected levels. Capability interfaces make
// optional cache expiry, batching, and idle-client cleanup available without
// adding those methods to every repository or transport.

const (
	// Shutdown stages group concurrent stops and order their consumers before
	// shared resources close. Startup levels remain independent because rollback
	// follows reverse startup and registration order, including acquired resources.
	shutdownStageLocalObservers              = 5   // local observation cleanup
	shutdownStageRequests                    = 15  // HTTP and gateway request completion
	shutdownStageProducers                   = 20  // periodic and task-producing workers
	shutdownStageProbesAndRefresh            = 25  // probes and runtime refresh loops
	shutdownStagePlugins                     = 27  // plugin process draining and cleanup
	shutdownStageAggregatesSessionsAndSearch = 30  // aggregation, credential sessions, and search cleanup
	shutdownStageOpenAIQuota                 = 35  // administrative OpenAI quota operations
	shutdownStageUsageRecords                = 40  // request completion recording
	shutdownStageModeration                  = 45  // queued content moderation
	shutdownStageUsageBatchers               = 48  // buffered usage persistence
	shutdownStageBillingCache                = 50  // queued billing cache updates
	shutdownStageOllamaUsage                 = 60  // Ollama usage synchronization
	shutdownStageDeferredWrites              = 65  // deferred provider writes
	shutdownStageEmail                       = 70  // queued notification delivery
	shutdownStageAudit                       = 75  // buffered audit persistence
	shutdownStageRequestAndSystemLogs        = 790 // request and system log persistence
	shutdownStageSettings                    = 800 // runtime settings subscriptions
	shutdownStageAPIKeys                     = 805 // API key cache cleanup
	shutdownStageAuthAndSubscriptions        = 810 // authorization maintenance and cache subscriptions
	shutdownStageLocalCaches                 = 815 // recurring local cache expiry
	shutdownStageTimingWheel                 = 820 // shared timer execution
	shutdownStageHTTPConnections             = 840 // idle upstream HTTP connections

	// This additional reusable wait follows billing-cache draining and its first
	// task barrier. It precedes Ollama usage shutdown and has no producer hook.
	shutdownStageBillingCacheFollowupBarrier = 56
)

// coreRuntimeReady marks registration of shared cleanup, cache, connection,
// and task barriers.
type coreRuntimeReady struct{}

// provideCoreRuntime registers shared maintenance and the task barriers used
// between shutdown levels. Call before manager startup or shutdown and provide
// the timing wheel and task tracker required by the unguarded scheduling and
// barrier callbacks. Hooks remain registered for optional nil dependencies;
// callbacks with nil guards skip the corresponding service calls.
//
// The recurring cache callback expires rate and digest caches, plus usage
// caches when the repository supports that capability. Its cancellation waits
// before timing-wheel shutdown. Ordinary task barriers can be followed by
// later work; final sealing belongs to installBackgroundTasks. Component stop
// callbacks retain their own context support, while manager timeout can leave
// an already launched callback unfinished.
func provideCoreRuntime(
	authCacheInvalidationWorker *apikey.AuthCacheInvalidationWorker,
	schedulerSnapshot *scheduler.SnapshotService,
	usageCleanup *usage.UsageCleanupService,
	idempotencyCleanup *idempotency.IdempotencyCleanupService,
	paymentOrderExpiry *payment.OrderExpiry,
	tlsFingerprintCollector *provider.TLSFingerprintCollectorService,
	manager *lifecycle.Manager,
	timingWheel *timingwheel.Wheel,
	digestStore *session.DigestSessionStore,
	usageRepo usage.UsageLogRepository,
	tasks *lifecycle.Tasks,
	httpUpstream httpclient.UpstreamTransport,
	rates *gatewayBillingRates,
) *coreRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "AuthCacheInvalidationWorker", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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
	manager.Register(lifecycle.Hook{Name: "SchedulerSnapshotService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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

	manager.Register(lifecycle.Hook{Name: "UsageCleanupService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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
	manager.Register(lifecycle.Hook{Name: "IdempotencyCleanupService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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

	manager.Register(lifecycle.Hook{Name: "PaymentOrderExpiryService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: func(ctx context.Context) error {
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

	manager.Register(lifecycle.Hook{Name: "TLSFingerprintCollectorService", StartOrder: 995, StopOrder: shutdownStageLocalObservers, Start: nil, Stop: func(ctx context.Context) error {
		if tlsFingerprintCollector != nil {
			return tlsFingerprintCollector.Shutdown(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "TimingWheelService", StartOrder: 180, StopOrder: shutdownStageTimingWheel, Start: func(ctx context.Context) error {
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

	manager.Register(lifecycle.Hook{
		Name: "RuntimeLocalCaches", StartOrder: 185, StopOrder: shutdownStageLocalCaches,
		Start: func(context.Context) error {
			// The wheel has started before this hook schedules cache maintenance.
			// A named callback lets its stop hook cancel and await this work later.
			timingWheel.ScheduleRecurring("runtime:local_caches", time.Minute, func() {
				rates.Expire()
				digestStore.ExpireRuntimeCaches()
				if cache, ok := usageRepo.(interface {
					// ExpireRuntimeCaches removes elapsed repository-local cache entries
					// during the periodic cleanup callback scheduled by the application.
					ExpireRuntimeCaches()
				}); ok {
					cache.ExpireRuntimeCaches()
				}
			})
			return nil
		}, Stop: func(context.Context) error { timingWheel.CancelAndWait("runtime:local_caches"); return nil },
	})
	manager.Register(lifecycle.Hook{Name: "UsageLogBatchers", StartOrder: 952, StopOrder: shutdownStageUsageBatchers, Stop: func(context.Context) error {
		if closer, ok := usageRepo.(interface {
			// StopUsageBatchers closes repository write queues and waits for their
			// workers before shared storage cleanup. It has no context argument;
			// the manager can stop waiting while that callback remains unfinished.
			StopUsageBatchers()
		}); ok {
			closer.StopUsageBatchers()
		}
		return nil
	}})

	// Earlier stop callbacks may enqueue tracked side effects. Insert reusable
	// waits after their levels before later consumers or shared resources close.
	for _, phase := range []int{
		shutdownStageRequests + 1,
		shutdownStageProducers + 1,
		shutdownStageProbesAndRefresh + 1,
		shutdownStageAggregatesSessionsAndSearch + 1,
		shutdownStageUsageRecords + 1,
		shutdownStageModeration + 1,
		shutdownStageUsageBatchers + 1,
		shutdownStageBillingCache + 1,
		shutdownStageBillingCacheFollowupBarrier,
		shutdownStageOllamaUsage + 1,
		shutdownStageDeferredWrites + 1,
	} {
		manager.Register(lifecycle.Hook{Name: fmt.Sprintf("BackgroundBarrier%d", phase), StartOrder: 1000 - phase, StopOrder: phase, Stop: tasks.Wait})
	}

	manager.Register(lifecycle.Hook{Name: "HTTPIdleConnections", StartOrder: 160, StopOrder: shutdownStageHTTPConnections, Stop: func(context.Context) error {
		if closer, ok := httpUpstream.(interface {
			// CloseIdleConnections releases currently unused transport connections.
			// Active requests retain their connections; the application invokes this
			// capability after request barriers during the shared HTTP cleanup phase.
			CloseIdleConnections()
		}); ok {
			closer.CloseIdleConnections()
		}
		httpclient.CloseSharedIdleConnections()
		return nil
	}})

	return &coreRuntimeReady{}
}
