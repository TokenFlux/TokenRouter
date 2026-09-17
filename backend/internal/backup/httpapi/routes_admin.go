package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterBackupRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterBackupRoutes(admin *gin.RouterGroup, endpoint *BackupHandler, stepUpAuth gin.HandlerFunc) {
	backup := admin.Group("/backups")
	{
		// 备份存储配置
		backup.GET("/storage-config", endpoint.GetStorageConfig)
		// 统一存储配置同样可以切换 S3 目标，必须执行二次验证。
		backup.PUT("/storage-config", stepUpAuth, endpoint.UpdateStorageConfig)
		backup.POST("/storage-config/test", endpoint.TestStorageConnection)

		// 备份内容配置
		backup.GET("/content-config", endpoint.GetContentConfig)
		backup.PUT("/content-config", endpoint.UpdateContentConfig)

		// S3 存储配置
		backup.GET("/s3-config", endpoint.GetS3Config)
		// 修改 S3 目标可将数据库备份外泄——要求 step-up 2FA
		backup.PUT("/s3-config", stepUpAuth, endpoint.UpdateS3Config)
		backup.POST("/s3-config/test", endpoint.TestS3Connection)

		// 定时备份配置
		backup.GET("/schedule", endpoint.GetSchedule)
		backup.PUT("/schedule", endpoint.UpdateSchedule)

		// 备份操作
		backup.POST("", stepUpAuth, endpoint.CreateBackup)
		backup.GET("", endpoint.ListBackups)
		backup.GET("/:id", RequireCanonicalBackupID, endpoint.GetBackup)
		backup.DELETE("/:id", RequireCanonicalBackupID, endpoint.DeleteBackup)
		// 备份下载链接可直接取走整库数据——要求 step-up 2FA
		backup.GET("/:id/download-url", RequireCanonicalBackupID, stepUpAuth, endpoint.GetDownloadURL)
		backup.GET("/:id/download", RequireCanonicalBackupID, stepUpAuth, endpoint.DownloadBackup)

		// 恢复操作：整库覆盖可回滚安全设置（含 step-up 开关本身）——要求 step-up 2FA
		backup.POST("/:id/restore", RequireCanonicalBackupID, stepUpAuth, endpoint.RestoreBackup)
	}
}

// RegisterDataManagementRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterDataManagementRoutes(admin *gin.RouterGroup, endpoint *DataManagementHandler, stepUpAuth gin.HandlerFunc) {
	dataManagement := admin.Group("/data-management")
	{
		dataManagement.GET("/agent/health", endpoint.GetAgentHealth)
		dataManagement.GET("/config", endpoint.GetConfig)
		dataManagement.PUT("/config", endpoint.UpdateConfig)
		dataManagement.GET("/sources/:source_type/profiles", endpoint.ListSourceProfiles)
		dataManagement.POST("/sources/:source_type/profiles", endpoint.CreateSourceProfile)
		dataManagement.PUT("/sources/:source_type/profiles/:profile_id", endpoint.UpdateSourceProfile)
		dataManagement.DELETE("/sources/:source_type/profiles/:profile_id", endpoint.DeleteSourceProfile)
		dataManagement.POST("/sources/:source_type/profiles/:profile_id/activate", endpoint.SetActiveSourceProfile)
		dataManagement.POST("/s3/test", endpoint.TestS3)
		dataManagement.GET("/s3/profiles", endpoint.ListS3Profiles)
		// 修改 S3 目标可将数据备份外泄——要求 step-up 2FA
		dataManagement.POST("/s3/profiles", stepUpAuth, endpoint.CreateS3Profile)
		dataManagement.PUT("/s3/profiles/:profile_id", stepUpAuth, endpoint.UpdateS3Profile)
		dataManagement.DELETE("/s3/profiles/:profile_id", endpoint.DeleteS3Profile)
		dataManagement.POST("/s3/profiles/:profile_id/activate", stepUpAuth, endpoint.SetActiveS3Profile)
		dataManagement.POST("/backups", stepUpAuth, endpoint.CreateBackupJob)
		dataManagement.GET("/backups", endpoint.ListBackupJobs)
		dataManagement.GET("/backups/:job_id", endpoint.GetBackupJob)
	}
}

func RequireCanonicalBackupID(c *gin.Context) {
	if IsCanonicalBackupID(c.Param("id")) {
		c.Next()
		return
	}
	c.AbortWithStatus(http.StatusNotFound)
}

func IsCanonicalBackupID(value string) bool {
	if len(value) != 8 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if (value[i] < '0' || value[i] > '9') && (value[i] < 'a' || value[i] > 'f') {
			return false
		}
	}
	return true
}
