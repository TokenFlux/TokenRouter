// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	errors "errors"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// BatchUpdateCredentialsRequest represents batch credentials update request
type BatchUpdateCredentialsRequest struct {
	AccountIDs []int64 `json:"account_ids" binding:"required,min=1"`
	Field      string  `json:"field" binding:"required,oneof=account_uuid org_uuid intercept_warmup_requests"`
	Value      any     `json:"value"`
}

// BatchUpdateCredentials 保留类型校验、预验证 404 和逐项结果格式。
func (h *ManagementHandler) BatchUpdateCredentials(c *gin.Context) {
	var req BatchUpdateCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := accountcore.ValidateCredentialFieldValue(req.Field, req.Value); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.batch.PatchCredentials(c.Request.Context(), req.AccountIDs, req.Field, req.Value)
	if err != nil {
		var missing *accountcore.ManagementAccountMissing
		if errors.As(err, &missing) {
			response.Error(c, 404, missing.Error())
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}
	items := make([]gin.H, 0, len(result.Results))
	for _, item := range result.Results {
		view := gin.H{"account_id": item.AccountID, "success": item.Success}
		if !item.Success {
			view["error"] = item.Error
		}
		items = append(items, view)
	}
	response.Success(c, gin.H{"success": result.Success, "failed": result.Failed, "success_ids": result.SuccessIDs, "failed_ids": result.FailedIDs, "results": items})
}
