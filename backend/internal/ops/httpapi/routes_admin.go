package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterOpsRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterOpsRoutes(admin *gin.RouterGroup, endpoint *OpsHandler) {
	ops := admin.Group("/ops")
	{
		// 实时运维信号
		ops.GET("/concurrency", endpoint.GetConcurrencyStats)
		ops.GET("/user-concurrency", endpoint.GetUserConcurrencyStats)
		ops.GET("/account-availability", endpoint.GetAccountAvailability)
		ops.GET("/realtime-traffic", endpoint.GetRealtimeTrafficSummary)

		// 告警规则和事件
		ops.GET("/alert-rules", endpoint.ListAlertRules)
		ops.POST("/alert-rules", endpoint.CreateAlertRule)
		ops.PUT("/alert-rules/:id", endpoint.UpdateAlertRule)
		ops.DELETE("/alert-rules/:id", endpoint.DeleteAlertRule)
		ops.GET("/alert-events", endpoint.ListAlertEvents)
		ops.GET("/alert-events/:id", endpoint.GetAlertEvent)
		ops.PUT("/alert-events/:id/status", endpoint.UpdateAlertEventStatus)
		ops.POST("/alert-silences", endpoint.CreateAlertSilence)

		// 邮件通知配置（数据库持久化）
		ops.GET("/email-notification/config", endpoint.GetEmailNotificationConfig)
		ops.PUT("/email-notification/config", endpoint.UpdateEmailNotificationConfig)

		// 运行时设置（数据库持久化）
		runtime := ops.Group("/runtime")
		{
			runtime.GET("/alert", endpoint.GetAlertRuntimeSettings)
			runtime.PUT("/alert", endpoint.UpdateAlertRuntimeSettings)
			runtime.GET("/logging", endpoint.GetRuntimeLogConfig)
			runtime.PUT("/logging", endpoint.UpdateRuntimeLogConfig)
			runtime.POST("/logging/reset", endpoint.ResetRuntimeLogConfig)
		}

		// 高级设置（数据库持久化）
		ops.GET("/advanced-settings", endpoint.GetAdvancedSettings)
		ops.PUT("/advanced-settings", endpoint.UpdateAdvancedSettings)

		// 设置分组（数据库持久化）
		settings := ops.Group("/settings")
		{
			settings.GET("/metric-thresholds", endpoint.GetMetricThresholds)
			settings.PUT("/metric-thresholds", endpoint.UpdateMetricThresholds)
		}

		// WebSocket 实时 QPS/TPS
		ws := ops.Group("/ws")
		{
			ws.GET("/qps", endpoint.QPSWSHandler)
		}

		// 旧版错误日志
		ops.GET("/errors", endpoint.GetErrorLogs)
		ops.GET("/errors/:id", endpoint.GetErrorLogByID)
		ops.PUT("/errors/:id/resolve", endpoint.UpdateErrorResolution)

		// 请求错误（客户端可见失败）
		ops.GET("/request-errors", endpoint.ListRequestErrors)
		ops.GET("/request-errors/:id", endpoint.GetRequestError)
		ops.GET("/request-errors/:id/upstream-errors", endpoint.ListRequestErrorUpstreamErrors)
		ops.PUT("/request-errors/:id/resolve", endpoint.ResolveRequestError)

		// 有界聚合入口准入拒绝记录。
		ops.GET("/ingress-rejections", endpoint.ListIngressRejects)
		ops.GET("/ingress-rejections/health", endpoint.GetIngressRejectHealth)
		ops.GET("/auth-cache-invalidation/health", endpoint.GetAuthCacheInvalidationHealth)

		// 上游错误（独立上游失败）
		ops.GET("/upstream-errors", endpoint.ListUpstreamErrors)
		ops.GET("/upstream-errors/:id", endpoint.GetUpstreamError)
		ops.PUT("/upstream-errors/:id/resolve", endpoint.ResolveUpstreamError)

		// 请求明细（成功和失败）
		ops.GET("/requests", endpoint.ListRequestDetails)

		// 已索引系统日志
		ops.GET("/system-logs", endpoint.ListSystemLogs)
		ops.POST("/system-logs/cleanup", endpoint.CleanupSystemLogs)
		ops.GET("/system-logs/health", endpoint.GetSystemLogIngestionHealth)

		// 新版仪表盘原始数据接口
		ops.GET("/dashboard/snapshot-v2", endpoint.GetDashboardSnapshotV2)
		ops.GET("/dashboard/overview", endpoint.GetDashboardOverview)
		ops.GET("/dashboard/throughput-trend", endpoint.GetDashboardThroughputTrend)
		ops.GET("/dashboard/latency-histogram", endpoint.GetDashboardLatencyHistogram)
		ops.GET("/dashboard/error-trend", endpoint.GetDashboardErrorTrend)
		ops.GET("/dashboard/error-distribution", endpoint.GetDashboardErrorDistribution)
		ops.GET("/dashboard/token-stats", endpoint.GetDashboardTokenStats)
		// 旧路由作为兼容别名保留，响应结构与新路由一致。
		ops.GET("/dashboard/openai-token-stats", endpoint.GetDashboardTokenStats)
	}
}

// RegisterSystemRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterSystemRoutes(admin *gin.RouterGroup, endpoint *SystemHandler) {
	system := admin.Group("/system")
	{
		system.GET("/version", endpoint.GetVersion)
		system.GET("/check-updates", endpoint.CheckUpdates)
		system.GET("/rollback-versions", endpoint.GetRollbackVersions)
		system.POST("/update", endpoint.PerformUpdate)
		system.POST("/rollback", endpoint.Rollback)
		system.POST("/restart", endpoint.RestartService)
	}
}
