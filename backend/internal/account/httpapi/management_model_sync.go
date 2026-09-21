// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	errors "errors"
	slog "log/slog"
	http "net/http"
	strconv "strconv"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// SyncUpstreamModels 从账号上游同步实时支持模型列表。
// POST /api/v1/admin/accounts/:id/models/sync-upstream
func (h *ManagementHandler) SyncUpstreamModels(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.NotFound(c, "Account not found")
		return
	}

	if h.models == nil {
		response.InternalError(c, "Account test service is not configured")
		return
	}

	models, err := h.models.Fetch(c.Request.Context(), account)
	if err != nil {
		var syncErr *accountcore.UpstreamModelSyncError
		if errors.As(err, &syncErr) {
			switch syncErr.Kind {
			case accountcore.UpstreamModelSyncErrorConfiguration, accountcore.UpstreamModelSyncErrorUnsupported:
				response.BadRequest(c, syncErr.SafeMessage())
			default:
				slog.Warn("sync_upstream_models_failed", "account_id", accountID, "kind", syncErr.Kind)
				response.Error(c, http.StatusBadGateway, syncErr.SafeMessage())
			}
			return
		}

		slog.Warn("sync_upstream_models_failed", "account_id", accountID)
		response.Error(c, http.StatusBadGateway, "Failed to sync upstream models from upstream")
		return
	}

	response.Success(c, gin.H{"models": models})
}

// SyncUpstreamModelsPreview 使用未保存账号的临时凭证同步上游实时支持模型列表。
// POST /api/v1/admin/accounts/models/sync-upstream-preview
func (h *ManagementHandler) SyncUpstreamModelsPreview(c *gin.Context) {
	var req struct {
		Platform string `json:"platform" binding:"required"`
		Type     string `json:"type" binding:"required"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tempAccount := &accountcore.Record{
		Platform: req.Platform,
		Type:     req.Type,
		Credentials: map[string]any{
			"api_key":  req.APIKey,
			"base_url": req.BaseURL,
		},
	}

	if h.models == nil {
		response.InternalError(c, "Account test service is not configured")
		return
	}

	models, err := h.models.Fetch(c.Request.Context(), tempAccount)
	if err != nil {
		var syncErr *accountcore.UpstreamModelSyncError
		if errors.As(err, &syncErr) {
			switch syncErr.Kind {
			case accountcore.UpstreamModelSyncErrorConfiguration, accountcore.UpstreamModelSyncErrorUnsupported:
				response.BadRequest(c, syncErr.SafeMessage())
			default:
				slog.Warn("sync_upstream_models_preview_failed", "platform", req.Platform, "kind", syncErr.Kind)
				response.Error(c, http.StatusBadGateway, syncErr.SafeMessage())
			}
			return
		}

		slog.Warn("sync_upstream_models_preview_failed", "platform", req.Platform)
		response.Error(c, http.StatusBadGateway, "Failed to sync upstream models from upstream")
		return
	}

	response.Success(c, gin.H{"models": models})
}
