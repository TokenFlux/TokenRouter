package httpapi

import "github.com/gin-gonic/gin"

// CreativeSettingsEndpoints 只描述所属设置的 HTTP 操作，规则由对应模块实现。
type CreativeSettingsEndpoints interface {
	ListCreativeModelCandidates(*gin.Context)
	GetCreativeWorkerStatus(*gin.Context)
}

// RegisterCreativeSettingsRoutes 在已经鉴权和审计的设置组中注册原路径。
func RegisterCreativeSettingsRoutes(adminSettings *gin.RouterGroup, endpoint CreativeSettingsEndpoints) {
	adminSettings.GET("/creative-model-candidates", endpoint.ListCreativeModelCandidates)
	adminSettings.GET("/creative-worker-status", endpoint.GetCreativeWorkerStatus)
}
