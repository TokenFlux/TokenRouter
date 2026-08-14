//go:build !embed

package server

import (
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

func registerFrontendMiddleware(_ *gin.Engine, settingService *service.SettingService, refreshFrameOrigins func()) {
	// 非 embed 构建不注册前端静态资源，仅保留设置更新后的 CSP 来源刷新。
	settingService.SetOnUpdateCallback(refreshFrameOrigins)
}
