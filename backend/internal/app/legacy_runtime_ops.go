// 本文件登记旧运行观测资源；业务迁移后删除对应绑定，S16 清零。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type opsRuntimeReady struct{}

func provideOpsRuntime(
	opsMetricsCollector *service.OpsMetricsCollector,
	opsAggregation *service.OpsAggregationService,
	opsAlertEvaluator *service.OpsAlertEvaluatorService,
	opsCleanup *service.OpsCleanupService,
	opsScheduledReport *service.OpsScheduledReportService,
	opsSystemLogSink *service.OpsSystemLogSink,
	opsService *service.OpsService,
	opsIngressReject *service.OpsIngressRejectAggregator,
	backupSvc *service.BackupService,
	manager *lifecycle.Manager,
	dashboardAggregation *service.DashboardAggregationService,
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
			opsSystemLogSink.Stop()
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

	manager.Register(lifecycle.Hook{Name: "BackupService", StartOrder: 980, StopOrder: 20, Start: func(ctx context.Context) error {
		if backupSvc != nil {
			backupSvc.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if backupSvc != nil {
			backupSvc.Stop()
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
			dashboardAggregation.Stop()
		}
		return nil
	}})
	return &opsRuntimeReady{}
}
