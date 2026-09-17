package admin

import (
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"
	"github.com/gin-gonic/gin"
)

// UpdateSettingsRequest 保留旧测试的输入类型，生产端点由 settings/httpapi 拥有。
type UpdateSettingsRequest = settingsdto.UpdateSettingsRequest

// UpdateSettings 仅转接原生处理器，综合规则与 HTTP 实现没有第二份。
func (h *SettingHandler) UpdateSettings(c *gin.Context) { h.compositeHTTP().UpdateSettings(c) }

var _ *settingshttp.Handler
