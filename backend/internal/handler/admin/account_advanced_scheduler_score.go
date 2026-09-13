// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	gin "github.com/gin-gonic/gin"
)

// GetAdvancedSchedulerScore 委托新 HTTP，评分仍经原诊断端口执行。
func (h *AccountHandler) GetAdvancedSchedulerScore(c *gin.Context) {
	h.managementHTTP().GetAdvancedSchedulerScore(c)
}

// PreviewAdvancedSchedulerScore 委托新 HTTP，评分仍经原诊断端口执行。
func (h *AccountHandler) PreviewAdvancedSchedulerScore(c *gin.Context) {
	h.managementHTTP().PreviewAdvancedSchedulerScore(c)
}
