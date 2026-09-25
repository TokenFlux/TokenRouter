// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// BatchDelete 以有限并发删除多个账号，并返回稳定的逐账号结果。
// POST /api/v1/admin/accounts/batch-delete
func (h *ManagementHandler) BatchDelete(c *gin.Context) {
	var req struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	accountIDs := response.NormalizeInt64IDList(req.AccountIDs)
	if len(accountIDs) == 0 {
		response.BadRequest(c, "account_ids is required")
		return
	}

	result, err := h.batch.DeleteNormalized(c.Request.Context(), accountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"total": result.Total, "success": result.Success, "failed": result.Failed, "errors": managementFailures(result.Errors), "success_ids": result.SuccessIDs, "failed_ids": result.FailedIDs})
}

// BatchRefresh handles batch refreshing account credentials
// POST /api/v1/admin/accounts/batch-refresh
func (h *ManagementHandler) BatchRefresh(c *gin.Context) {
	var req struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.AccountIDs) == 0 {
		response.BadRequest(c, "account_ids is required")
		return
	}

	result, err := h.batch.Refresh(c.Request.Context(), req.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"total": result.Total, "success": result.Success, "failed": result.Failed, "errors": managementFailures(result.Errors), "warnings": managementWarnings(result.Warnings)})
}

// BatchClearError handles batch clearing account errors
// POST /api/v1/admin/accounts/batch-clear-error
func (h *ManagementHandler) BatchClearError(c *gin.Context) {
	var req struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.AccountIDs) == 0 {
		response.BadRequest(c, "account_ids is required")
		return
	}

	result, err := h.batch.ClearError(c.Request.Context(), req.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"total": result.Total, "success": result.Success, "failed": result.Failed, "errors": managementFailures(result.Errors)})
}

// 展示转换保留原 nil/空数组，并把领域结果限制为已有 HTTP 字段。
func managementFailures(values []accountcore.ManagementBatchFailure) []gin.H {
	if values == nil {
		return nil
	}
	out := make([]gin.H, 0, len(values))
	for _, v := range values {
		out = append(out, gin.H{"account_id": v.AccountID, "error": v.Error})
	}
	return out
}
func managementWarnings(values []accountcore.ManagementBatchWarning) []gin.H {
	if values == nil {
		return nil
	}
	out := make([]gin.H, 0, len(values))
	for _, v := range values {
		out = append(out, gin.H{"account_id": v.AccountID, "warning": v.Warning})
	}
	return out
}
