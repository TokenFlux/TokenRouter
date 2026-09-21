package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// BatchCreate 保留原输入和幂等 scope，核心只在首次执行时创建与派发后置任务。
func (h *ManagementHandler) BatchCreate(c *gin.Context) {
	var req struct {
		Accounts []CreateAccountRequest `json:"accounts" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	for i := range req.Accounts {
		account.DiscardDeprecatedAccountExtra(req.Accounts[i].Extra)
	}
	idempotencyhttp.ExecuteAdminIdempotentJSON(c, "admin.accounts.batch_create", req, idempotency.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		inputs := make([]account.CreateAccountInput, 0, len(req.Accounts))
		for _, item := range req.Accounts {
			inputs = append(inputs, account.CreateAccountInput{Name: item.Name, Notes: item.Notes, Platform: item.Platform, Type: item.Type, Credentials: item.Credentials, Extra: item.Extra, ProxyID: item.ProxyID, Concurrency: item.Concurrency, Priority: item.Priority, RateMultiplier: item.RateMultiplier, LoadFactor: item.LoadFactor, GroupIDs: item.GroupIDs, ExpiresAt: item.ExpiresAt, AutoPauseOnExpired: item.AutoPauseOnExpired, SkipMixedChannelCheck: item.ConfirmMixedChannelRisk != nil && *item.ConfirmMixedChannelRisk})
		}
		result, err := h.batch.Create(ctx, inputs)
		if err != nil {
			return nil, err
		}
		items := make([]gin.H, 0, len(result.Results))
		for _, item := range result.Results {
			value := gin.H{"name": item.Name, "success": item.Success}
			if item.Success {
				value["id"] = item.ID
			} else {
				value["error"] = item.Error
			}
			items = append(items, value)
		}
		return gin.H{"success": result.Success, "failed": result.Failed, "results": items}, nil
	})
}
