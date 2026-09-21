// 本文件直接登记各原生队列拥有者，保持既有启动、排空与关闭顺序。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"

	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/notification"
)

type queuesRuntimeReady struct{}

func provideQueuesRuntime(
	emailQueue *notification.EmailQueueService,
	billingCache *billing.Eligibility,
	usageRecordWorkerPool *completion.UsageRecordWorkerPool,
	quotaFlusher *billing.UserPlatformQuotaUsageFlusher,
	ollamaCloudUsage *account.OllamaCloudUsageService,
	auditLog *audit.AuditLogService,
	grokQuota *account.GrokQuotaService,
	openaiQuota *account.OpenAIQuotaService,
	manager *lifecycle.Manager,
	deferred *account.DeferredService,
	contentModeration *moderationcore.ContentModerationService,
) *queuesRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "EmailQueueService", StartOrder: 930, StopOrder: 70, Start: func(ctx context.Context) error {
		if emailQueue != nil {
			emailQueue.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if emailQueue != nil {
			return emailQueue.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "BillingCacheService", StartOrder: 950, StopOrder: 50, Start: func(ctx context.Context) error {
		if billingCache != nil {
			billingCache.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if billingCache != nil {
			billingCache.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "UsageRecordWorkerPool", StartOrder: 960, StopOrder: 40, Start: func(ctx context.Context) error {
		if usageRecordWorkerPool != nil {
			usageRecordWorkerPool.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if usageRecordWorkerPool != nil {
			return usageRecordWorkerPool.StopContext(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "UserPlatformQuotaUsageFlusher", StartOrder: 945, StopOrder: 55, Start: func(ctx context.Context) error {
		if quotaFlusher != nil {
			quotaFlusher.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if quotaFlusher != nil {
			return quotaFlusher.Shutdown(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "OllamaCloudUsageService", StartOrder: 940, StopOrder: 60, Start: func(ctx context.Context) error {
		if ollamaCloudUsage != nil {
			return ollamaCloudUsage.StartContext(ctx)
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if ollamaCloudUsage != nil {
			return ollamaCloudUsage.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "AuditLogService", StartOrder: 925, StopOrder: 75, Start: func(ctx context.Context) error {
		if auditLog != nil {
			auditLog.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if auditLog != nil {
			return auditLog.StopContext(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "DeferredService", StartOrder: 935, StopOrder: 65, Start: func(ctx context.Context) error {
		if deferred != nil {
			deferred.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if deferred != nil {
			return deferred.StopContext(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "ContentModerationService", StartOrder: 955, StopOrder: 45, Start: func(ctx context.Context) error {
		if contentModeration != nil {
			contentModeration.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if contentModeration != nil {
			return contentModeration.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "GrokQuotaProbes", StopOrder: 25, Stop: grokQuota.StopContext})
	// 直接管理查询与平台额度消费同样在共享依赖关闭前取消并等待。
	manager.Register(lifecycle.Hook{Name: "OpenAIQuotaService", StopOrder: 35, Stop: openaiQuota.StopContext})
	return &queuesRuntimeReady{}
}
