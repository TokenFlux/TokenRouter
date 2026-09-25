// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"strconv"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// RefreshTier 保留原 ID、404 和响应字段。
func (h *ManagementHandler) RefreshTier(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	v, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "Account not found")
		return
	}
	result, err := h.tier.Refresh(c.Request.Context(), v)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	extra := result.StorageInfo
	response.Success(c, gin.H{"tier_id": result.TierID, "storage_info": extra, "drive_storage_limit": extra["drive_storage_limit"], "drive_storage_usage": extra["drive_storage_usage"], "updated_at": extra["drive_tier_updated_at"]})
}

// BatchRefreshTier 保留损坏/空输入回落全量 Google One 账号的历史行为。
func (h *ManagementHandler) BatchRefreshTier(c *gin.Context) {
	var req BatchRefreshTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = BatchRefreshTierRequest{}
	}
	result, err := h.tier.Batch(c.Request.Context(), req.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var failures []gin.H
	for _, v := range result.Errors {
		failures = append(failures, gin.H{"account_id": v.AccountID, "error": v.Error})
	}
	response.Success(c, gin.H{"total": result.Total, "success": result.Success, "failed": result.Failed, "errors": failures})
}

// BatchRefreshTierRequest represents batch tier refresh request
type BatchRefreshTierRequest struct {
	AccountIDs []int64 `json:"account_ids"`
}
