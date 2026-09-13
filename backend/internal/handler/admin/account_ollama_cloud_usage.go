// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	gin "github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetOllamaCloudUsageSettings(c *gin.Context) {
	accounthttp.NewOllamaUsageHandler(h.ollamaCloudUsage.Core()).GetOllamaCloudUsageSettings(c)
}
func (h *AccountHandler) UpdateOllamaCloudUsageSettings(c *gin.Context) {
	accounthttp.NewOllamaUsageHandler(h.ollamaCloudUsage.Core()).UpdateOllamaCloudUsageSettings(c)
}
func (h *AccountHandler) GetOllamaCloudUsage(c *gin.Context) {
	accounthttp.NewOllamaUsageHandler(h.ollamaCloudUsage.Core()).GetOllamaCloudUsage(c)
}
func (h *AccountHandler) SaveOllamaCloudUsageSession(c *gin.Context) {
	accounthttp.NewOllamaUsageHandler(h.ollamaCloudUsage.Core()).SaveOllamaCloudUsageSession(c)
}
func (h *AccountHandler) DeleteOllamaCloudUsageSession(c *gin.Context) {
	accounthttp.NewOllamaUsageHandler(h.ollamaCloudUsage.Core()).DeleteOllamaCloudUsageSession(c)
}
func (h *AccountHandler) SetOllamaCloudUsageAutoRefresh(c *gin.Context) {
	accounthttp.NewOllamaUsageHandler(h.ollamaCloudUsage.Core()).SetOllamaCloudUsageAutoRefresh(c)
}
func (h *AccountHandler) RefreshOllamaCloudUsage(c *gin.Context) {
	accounthttp.NewOllamaUsageHandler(h.ollamaCloudUsage.Core()).RefreshOllamaCloudUsage(c)
}
