package admin

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// routes_admin.go registers administrator dashboard, usage-query, cleanup-task,
// and export-task HTTP routes.
// Administrator dashboard and usage routes expose statistics, searches,
// persisted cleanup tasks, and background CSV export tasks through separate
// path groups. The application supplies their authentication, rate limiting,
// and audit middleware on the parent group before these handlers are registered.

// RegisterDashboardRoutes attaches dashboard reports, chart snapshots and
// batch usage reads beneath admin. The caller supplies a nonnil handler and
// installs authentication, rate limiting and audit middleware on the parent
// group first.
func RegisterDashboardRoutes(admin *gin.RouterGroup, endpoint *DashboardHandler) {
	dashboard := admin.Group("/dashboard")
	{
		adminapi.Register(dashboard, ManagementOperations["dashboard.snapshot_v2.list"], endpoint.GetSnapshotV2)
		adminapi.Register(dashboard, ManagementOperations["dashboard.stats.list"], endpoint.GetStats)
		adminapi.Register(dashboard, ManagementOperations["dashboard.realtime.list"], endpoint.GetRealtimeMetrics)
		adminapi.Register(dashboard, ManagementOperations["dashboard.trend.list"], endpoint.GetUsageTrend)
		adminapi.Register(dashboard, ManagementOperations["dashboard.models.list"], endpoint.GetModelStats)
		adminapi.Register(dashboard, ManagementOperations["dashboard.groups.list"], endpoint.GetGroupStats)
		adminapi.Register(dashboard, ManagementOperations["dashboard.api_keys_trend.list"], endpoint.GetAPIKeyUsageTrend)
		adminapi.Register(dashboard, ManagementOperations["dashboard.users_trend.list"], endpoint.GetUserUsageTrend)
		adminapi.Register(dashboard, ManagementOperations["dashboard.users_ranking.list"], endpoint.GetUserSpendingRanking)
		adminapi.Register(dashboard, ManagementOperations["dashboard.users_usage.create"], endpoint.GetBatchUsersUsage)
		adminapi.Register(dashboard, ManagementOperations["dashboard.api_keys_usage.create"], endpoint.GetBatchAPIKeysUsage)
		adminapi.Register(dashboard, ManagementOperations["dashboard.user_breakdown.list"], endpoint.GetUserBreakdown)
	}
}

// RegisterUsageRoutes attaches usage records, statistics, searches,
// cleanup-task commands, and export-task commands beneath admin. The caller
// supplies a nonnil handler and installs authentication, rate limiting, and
// audit middleware on the parent group first.
func RegisterUsageRoutes(admin *gin.RouterGroup, endpoint *UsageHandler) {
	usage := admin.Group("/usage")
	{
		adminapi.Register(usage, ManagementOperations["usage.list"], endpoint.List)
		adminapi.Register(usage, ManagementOperations["usage.stats.list"], endpoint.Stats)
		adminapi.Register(usage, ManagementOperations["usage.search_users.list"], endpoint.SearchUsers)
		adminapi.Register(usage, ManagementOperations["usage.search_api_keys.list"], endpoint.SearchAPIKeys)
		adminapi.Register(usage, ManagementOperations["usage.cleanup_tasks.list"], endpoint.ListCleanupTasks)
		adminapi.Register(usage, ManagementOperations["usage.cleanup_tasks.create"], endpoint.CreateCleanupTask)
		adminapi.Register(usage, ManagementOperations["usage.cleanup_tasks.cancel.create"], endpoint.CancelCleanupTask)
		adminapi.Register(usage, ManagementOperations["usage.export_tasks.list"], endpoint.ListExportTasks)
		adminapi.Register(usage, ManagementOperations["usage.export_tasks.create"], endpoint.CreateExportTask)
		adminapi.Register(usage, ManagementOperations["usage.export_tasks.cancel.create"], endpoint.CancelExportTask)
		adminapi.Register(usage, ManagementOperations["usage.export_tasks.download_ticket.create"], endpoint.CreateExportDownloadTicket)
		adminapi.Register(usage, ManagementOperations["usage.export_tasks.download.get"], endpoint.DownloadExportTask)
	}
}
