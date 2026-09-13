// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// CRSHandler 只解析管理请求，六类同步及逐条结果由账号用例拥有。
type CRSHandler struct{ core *account.CRSSync }

func NewCRSHandler(core *account.CRSSync) *CRSHandler { return &CRSHandler{core: core} }

type SyncFromCRSRequest struct {
	BaseURL            string   `json:"base_url" binding:"required"`
	Username           string   `json:"username" binding:"required"`
	Password           string   `json:"password" binding:"required"`
	SyncProxies        *bool    `json:"sync_proxies"`
	SelectedAccountIDs []string `json:"selected_account_ids"`
}
type PreviewFromCRSRequest struct {
	BaseURL  string `json:"base_url" binding:"required"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// SyncFromCRS 执行管理员同步。
// POST /api/v1/admin/accounts/sync/crs
func (h *CRSHandler) SyncFromCRS(c *gin.Context) {
	var req SyncFromCRSRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 缺省同步代理；显式 false 保留关闭语义。
	syncProxies := true
	if req.SyncProxies != nil {
		syncProxies = *req.SyncProxies
	}

	result, err := h.core.SyncFromCRS(c.Request.Context(), account.SyncFromCRSInput{
		BaseURL:            req.BaseURL,
		Username:           req.Username,
		Password:           req.Password,
		SyncProxies:        syncProxies,
		SelectedAccountIDs: req.SelectedAccountIDs,
	})
	if err != nil {
		// 保留旧 CRS 错误响应内容。
		response.InternalError(c, "CRS sync failed: "+err.Error())
		return
	}

	response.Success(c, result)
}

// PreviewFromCRS 返回同步前预览。
// POST /api/v1/admin/accounts/sync/crs/preview
func (h *CRSHandler) PreviewFromCRS(c *gin.Context) {
	var req PreviewFromCRSRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.core.PreviewFromCRS(c.Request.Context(), account.SyncFromCRSInput{
		BaseURL:  req.BaseURL,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		response.InternalError(c, "CRS preview failed: "+err.Error())
		return
	}

	response.Success(c, result)
}
