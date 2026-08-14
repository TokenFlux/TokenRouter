//go:build embed

package server

import (
	"log"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/BrandonVee/TokenRouter/internal/web"
	"github.com/gin-gonic/gin"
)

func registerFrontendMiddleware(r *gin.Engine, settingService *service.SettingService, refreshFrameOrigins func()) {
	frontendServer, err := web.NewFrontendServer(settingService)
	if err != nil {
		// embed 构建应始终包含 dist/index.html；若资源异常，启动时直接暴露问题。
		log.Panicf("failed to create embedded frontend server: %v", err)
	}

	// 设置变更时同时刷新 HTML 注入缓存和 CSP frame-src 来源缓存。
	settingService.SetOnUpdateCallback(func() {
		frontendServer.InvalidateCache()
		refreshFrameOrigins()
	})
	r.Use(frontendServer.Middleware())
}
