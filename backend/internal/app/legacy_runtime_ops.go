// 登记运行观测资源的启动、停止与依赖顺序。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/ops"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

type opsRuntimeReady struct{}

func provideOpsRuntime(
	opsMetricsCollector *ops.OpsMetricsCollector,
	opsAggregation *ops.OpsAggregationService,
	opsAlertEvaluator *ops.OpsAlertEvaluatorService,
	opsCleanup *ops.OpsCleanupService,
	opsScheduledReport *ops.OpsScheduledReportService,
	opsSystemLogSink *ops.OpsSystemLogSink,
	opsService *ops.OpsService,
	opsIngressReject *ops.OpsIngressRejectAggregator,
	manager *lifecycle.Manager,
	dashboardAggregation *usage.DashboardAggregationService,
) *opsRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "OpsMetricsCollector", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if opsMetricsCollector != nil {
			opsMetricsCollector.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsMetricsCollector != nil {
			opsMetricsCollector.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpsAggregationService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if opsAggregation != nil {
			opsAggregation.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsAggregation != nil {
			opsAggregation.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpsAlertEvaluatorService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if opsAlertEvaluator != nil {
			opsAlertEvaluator.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsAlertEvaluator != nil {
			opsAlertEvaluator.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpsCleanupService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if opsCleanup != nil {
			opsCleanup.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsCleanup != nil {
			opsCleanup.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpsScheduledReportService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if opsScheduledReport != nil {
			opsScheduledReport.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsScheduledReport != nil {
			opsScheduledReport.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpsSystemLogSink", StartOrder: 210, StopOrder: 790, Start: func(ctx context.Context) error {
		if opsSystemLogSink != nil {
			opsSystemLogSink.Start()
			logger.SetSink(opsSystemLogSink)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsSystemLogSink != nil {
			logger.SetSink(nil)
			return opsSystemLogSink.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpsService", StartOrder: 975, StopOrder: 25, Start: func(ctx context.Context) error {
		if opsService != nil {
			opsService.StartRuntimeSettingsRefresh(context.Background())
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsService != nil {
			opsService.StopRuntimeSettingsRefresh()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpsIngressRejectAggregator", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if opsIngressReject != nil {
			opsIngressReject.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if opsIngressReject != nil {
			opsIngressReject.Stop()
			if health := opsIngressReject.Health(); health.PendingBatches != 0 {
				return fmt.Errorf("ingress reject drain incomplete: %d batches, %d rows", health.PendingBatches, health.PendingRows)
			}
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "DashboardAggregationService", StartOrder: 970, StopOrder: 30, Start: func(ctx context.Context) error {
		if dashboardAggregation != nil {
			dashboardAggregation.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if dashboardAggregation != nil {
			return dashboardAggregation.StopContext(ctx)
		}
		return nil
	}})
	return &opsRuntimeReady{}
}
