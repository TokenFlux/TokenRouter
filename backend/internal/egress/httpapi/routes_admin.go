package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterProxyRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterProxyRoutes(admin *gin.RouterGroup, endpoint *ProxyHandler, stepUpAuth gin.HandlerFunc) {
	proxies := admin.Group("/proxies")
	{
		proxies.GET("", endpoint.List)
		proxies.GET("/all", endpoint.GetAll)
		// 代理导出泄露账号密码原文——要求 step-up 2FA
		proxies.GET("/data", stepUpAuth, endpoint.ExportData)
		proxies.POST("/data", endpoint.ImportData)
		proxies.GET("/:id", endpoint.GetByID)
		proxies.POST("", endpoint.Create)
		proxies.PUT("/:id", endpoint.Update)
		proxies.DELETE("/:id", endpoint.Delete)
		proxies.POST("/:id/test", endpoint.Test)
		proxies.POST("/:id/quality-check", endpoint.CheckQuality)
		proxies.GET("/:id/stats", endpoint.GetStats)
		proxies.GET("/:id/accounts", endpoint.GetProxyAccounts)
		proxies.POST("/batch-delete", endpoint.BatchDelete)
		proxies.POST("/batch", endpoint.BatchCreate)
	}
}

// RegisterTLSFingerprintProfileRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterTLSFingerprintProfileRoutes(admin *gin.RouterGroup, endpoint *TLSFingerprintProfileHandler) {
	profiles := admin.Group("/tls-fingerprint-profiles")
	{
		collector := profiles.Group("/collector")
		{
			collector.GET("/status", endpoint.CollectorStatus)
			collector.POST("/start", endpoint.StartCollector)
			collector.POST("/stop", endpoint.StopCollector)
			collector.POST("/sessions", endpoint.CreateCollectorSession)
			collector.GET("/sessions/:token/captures", endpoint.ListCollectorCaptures)
			collector.DELETE("/sessions/:token", endpoint.DeleteCollectorSession)
		}
		profiles.GET("", endpoint.List)
		profiles.GET("/:id", endpoint.GetByID)
		profiles.POST("", endpoint.Create)
		profiles.PUT("/:id", endpoint.Update)
		profiles.DELETE("/:id", endpoint.Delete)
	}
}

// RegisterTLSFingerprintRouterRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterTLSFingerprintRouterRoutes(admin *gin.RouterGroup, endpoint *TLSFingerprintRouterHandler) {
	routers := admin.Group("/tls-fingerprint-routers")
	{
		routers.GET("", endpoint.List)
		routers.GET("/:id", endpoint.GetByID)
		routers.POST("", endpoint.Create)
		routers.PUT("/:id", endpoint.Update)
		routers.DELETE("/:id", endpoint.Delete)
	}
}
