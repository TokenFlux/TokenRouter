package httpapi

import "github.com/gin-gonic/gin"

// routes_user.go registers authenticated user usage routes, lightweight export
// task endpoints, and public signed-ticket CSV downloads.
// Authenticated users query usage records, rankings, errors, and dashboard
// reports under a heavy-query rate-limited group, while export-task creation,
// progress polling, cancellation, and ticket exchange mount directly on the
// authenticated group so background export tracking does not consume heavy-query
// quota. Browser CSV downloads mount under the public /api/v1 group with a
// public-IP rate limiter and short-lived HMAC ticket verification.

// RegisterUserRoutes attaches the user usage endpoints, export-task commands,
// and heavy-query middleware to their respective route groups. The authenticated
// parent supplies shared authentication, global panel rate limiting, and audit
// handling; endpoint and heavy must be configured before registration.
func RegisterUserRoutes(authenticated *gin.RouterGroup, endpoint *UsageHandler, heavy gin.HandlerFunc) {
	// Register export-task endpoints directly on authenticated rather than the
	// heavy-limited usage group so status polling never exhausts heavy-query RPM.
	authenticated.GET("/usage/export-tasks", endpoint.ListExportTasks)
	authenticated.POST("/usage/export-tasks", endpoint.CreateExportTask)
	authenticated.POST("/usage/export-tasks/:id/cancel", endpoint.CancelExportTask)
	authenticated.POST("/usage/export-tasks/:id/download-ticket", endpoint.CreateExportDownloadTicket)
	authenticated.GET("/usage/export-tasks/:id/download", endpoint.DownloadExportTask)

	usage := authenticated.Group("/usage")
	usage.Use(heavy)
	{
		usage.GET("", endpoint.List)
		usage.GET("/ranking", endpoint.Ranking)
		usage.GET("/errors", endpoint.ListErrors)
		usage.GET("/errors/:id", endpoint.GetErrorDetail)
		usage.GET("/stats", endpoint.Stats)
		// User dashboard routes.
		usage.GET("/dashboard/stats", endpoint.DashboardStats)
		usage.GET("/dashboard/trend", endpoint.DashboardTrend)
		usage.GET("/dashboard/models", endpoint.DashboardModels)
		usage.GET("/dashboard/snapshot-v2", endpoint.DashboardSnapshotV2)
		usage.POST("/dashboard/api-keys-usage", endpoint.DashboardAPIKeysUsage)
		usage.GET("/:id", endpoint.GetByID)
	}
	authenticated.GET("/user/api-keys/:id/usage/daily", heavy, endpoint.GetMyAPIKeyDailyUsage)
}

// RegisterPublicExportDownloadRoutes attaches the short-lived signed-ticket CSV
// download endpoint under v1 with an optional public-IP rate limiter. Callers
// authenticate through the HMAC ticket embedded in the path parameter so native
// browser downloads stream directly from disk without bearer headers or JS blobs.
func RegisterPublicExportDownloadRoutes(v1 *gin.RouterGroup, endpoint *UsageHandler, publicLimiter gin.HandlerFunc) {
	if publicLimiter != nil {
		v1.GET("/usage/export-downloads/:ticket", publicLimiter, endpoint.DownloadExportByTicket)
		return
	}
	v1.GET("/usage/export-downloads/:ticket", endpoint.DownloadExportByTicket)
}
